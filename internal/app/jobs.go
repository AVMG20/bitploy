package app

// Background jobs: what bitploy runs by itself, listed for admins on the
// Jobs tab (GET /admin/jobs) with what each did last time.
//
// Every job is defined once in jobDefs. Its loop records each run with
// jobRan, in memory, so the list starts empty again when bitploy restarts:
//
//	start := s.Now()
//	summary, err := doTheWork(ctx)
//	s.jobRan("disk_watch", start, err, summary)
//
// summary is a few words on what the run did ("freed 1.2 GB", "started 2
// backups"); leave it empty when there is nothing worth saying. The jobs of
// internal/metrics and internal/traffic report through their manager's OnJob
// hook (set in metricsMgr and trafficMgr), since those packages cannot import
// this one.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/metrics"
	"github.com/AVMG20/bitploy/internal/traffic"
)

// How a job is started (Job.kind).
const (
	jobTimed     = "timed"     // on a clock or an interval: it has a next run
	jobTriggered = "triggered" // when something happens
	jobAlwaysOn  = "always_on" // listens all the time; it has no runs to show
)

// maxJobError is how much of an error message a job keeps.
const maxJobError = 300

// jobDef is a job as the Jobs tab lists it.
type jobDef struct {
	id, group, title, description, schedule string
	kind                                    string
	// every is the time between two runs of an interval job: the next run
	// is that long after the last one started.
	every time.Duration
	// next is the next run of a job that runs at set times, after now.
	next func(now time.Time) time.Time
}

// nightlySchedule is the schedule text of the nightly steps (metrics.NextNightly).
const nightlySchedule = "Nightly at 03:30 UTC"

// jobDefs are bitploy's background jobs, in the order of the Jobs tab.
var jobDefs = []jobDef{
	{id: "disk_watch", group: "disk", title: "Low disk check", kind: jobTimed, every: diskWatchEvery,
		schedule:    "Every minute",
		description: "Warns admins and runs the automatic cleanup when free space is below the limits on the Disk page."},
	{id: "disk_stale_cache", group: "disk", title: "Old build cache", kind: jobTimed, every: staleCacheEvery,
		schedule:    "Hourly",
		description: "Deletes build cache unused for the days set on the Disk page."},
	{id: metrics.JobVolumeQuota, group: "disk", title: "Volume limits", kind: jobTimed, every: time.Hour,
		schedule:    "Hourly, and after deploys",
		description: "Measures app volumes and stops apps that are over their limit."},

	{id: "backups", group: "backups", title: "App backups", kind: jobTimed, every: backupCheckEvery,
		schedule:    "Every 10 minutes",
		description: "Starts scheduled backups that are due and deletes expired ones."},

	{id: metrics.JobCollect, group: "monitoring", title: "Metrics", kind: jobTimed, every: 15 * time.Second,
		schedule:    "Every 15 seconds",
		description: "Records CPU, memory and network use of running apps."},
	{id: metrics.JobRollup, group: "monitoring", title: "Metrics rollup", kind: jobTimed, every: time.Hour,
		schedule:    "Hourly",
		description: "Turns samples into 5-minute averages."},
	{id: traffic.JobFlush, group: "monitoring", title: "Traffic stats", kind: jobTimed, every: time.Minute,
		schedule:    "Every minute",
		description: "Saves request counts from the proxy's access log."},
	{id: "domain_check", group: "monitoring", title: "Domain check", kind: jobTimed, every: domainCheckEvery,
		schedule:    "Every 5 minutes",
		description: "Checks DNS and certificates of domains that are not live yet."},
	{id: "crash_watch", group: "monitoring", title: "Crash watch", kind: jobAlwaysOn,
		schedule:    "Always on",
		description: "Stops apps that crash in a loop and reports failing health checks."},

	{id: "reconciler", group: "upkeep", title: "Reconciler", kind: jobTimed, every: reconcileEvery,
		schedule:    "Every minute",
		description: "Makes containers, routes and ports match the database."},
	{id: "notifications", group: "upkeep", title: "Notifications", kind: jobTriggered,
		schedule:    "When one is queued",
		description: "Sends queued notifications to their channels."},
	{id: "builder_idle", group: "upkeep", title: "Idle builder stop", kind: jobTimed, every: builderIdleEvery,
		schedule:    "Every 5 minutes",
		description: "Stops the build container after 8 hours without builds to free its memory; the build cache stays."},
	{id: "accounts_cleanup", group: "upkeep", title: "Session cleanup", kind: jobTimed, every: time.Hour,
		schedule:    "Hourly",
		description: "Deletes expired sessions, invites and request keys."},

	{id: metrics.JobPruneMetrics, group: "nightly", title: "Prune metrics", kind: jobTimed, next: metrics.NextNightly,
		schedule:    nightlySchedule,
		description: "Deletes samples older than a day and averages older than 30 days."},
	{id: metrics.JobMeasureDisk, group: "nightly", title: "Measure disk", kind: jobTimed, next: metrics.NextNightly,
		schedule:    nightlySchedule,
		description: "Records how much disk each app's images, sources and volumes take."},
	{id: metrics.JobDockerPrune, group: "nightly", title: "Docker prune", kind: jobTimed, next: metrics.NextNightly,
		schedule:    nightlySchedule,
		description: "Deletes untagged bitploy images and trims the build cache."},
	{id: metrics.JobBackupDB, group: "nightly", title: "Database backup", kind: jobTimed, next: metrics.NextNightly,
		schedule:    nightlySchedule,
		description: "Saves a copy of bitploy's database and keeps the newest 7."},
	{id: metrics.JobPruneAudit, group: "nightly", title: "Prune activity log", kind: jobTimed, next: metrics.NextNightly,
		schedule:    nightlySchedule,
		description: "Deletes activity log entries older than 90 days."},
	{id: metrics.JobOldUploads, group: "nightly", title: "Old uploads", kind: jobTimed, next: metrics.NextNightly,
		schedule:    nightlySchedule,
		description: "Deletes uploaded files older than a day."},
}

// jobRecord is what jobsState keeps of one job.
type jobRecord struct {
	last *JobRun
	runs int64
}

// jobsState is the zero-value-usable record of job runs on Service.
type jobsState struct {
	mu    sync.Mutex
	since time.Time // when RunBackground started the jobs
	runs  map[string]*jobRecord
}

// jobsStarted notes that the background jobs start now.
func (s *Service) jobsStarted() {
	s.jobs.mu.Lock()
	defer s.jobs.mu.Unlock()
	s.jobs.since = s.Now().UTC()
}

// jobRan records one run of job id that started at started; the run ended
// now. err nil means it went well.
func (s *Service) jobRan(id string, started time.Time, err error, summary string) {
	run := &JobRun{
		StartedAt:  started.UTC(),
		DurationMs: max(s.Now().Sub(started).Milliseconds(), 0),
		OK:         err == nil,
		Summary:    summary,
	}
	if err != nil {
		run.Error = jobErrorText(err)
	}
	j := &s.jobs
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.runs == nil {
		j.runs = map[string]*jobRecord{}
	}
	r := j.runs[id]
	if r == nil {
		r = &jobRecord{}
		j.runs[id] = r
	}
	r.last = run
	r.runs++
}

// jobErrorText is err as the Jobs tab shows it: no secrets, one short line.
func jobErrorText(err error) string {
	msg := strings.ReplaceAll(apperr.Redact(err.Error()), "\n", "; ") // errors.Join puts each on a line
	if r := []rune(msg); len(r) > maxJobError {
		msg = string(r[:maxJobError-1]) + "…"
	}
	return msg
}

// jobCount is a summary such as "started 2 backups".
func jobCount(verb string, n int, one, many string) string {
	return fmt.Sprintf("%s %d %s", verb, n, plural(n, one, many))
}

// nextRun is when job d runs next, or nil when that is not known: it is
// started by events, or an interval job has not run since bitploy started.
func (d jobDef) nextRun(last *JobRun, now time.Time) *time.Time {
	var t time.Time
	switch {
	case d.next != nil:
		t = d.next(now)
	case d.every > 0 && last != nil:
		t = last.StartedAt.Add(d.every)
	default:
		return nil
	}
	t = t.UTC()
	return &t
}

// AdminJobs lists bitploy's background jobs with their last run since
// bitploy started.
func (s *Service) AdminJobs(_ context.Context, a Actor) (JobList, error) {
	if err := RequireAdmin(a); err != nil {
		return JobList{}, err
	}
	now := s.Now()
	s.jobs.mu.Lock()
	defer s.jobs.mu.Unlock()
	out := JobList{Jobs: make([]Job, 0, len(jobDefs))}
	if !s.jobs.since.IsZero() {
		since := s.jobs.since
		out.Since = &since
	}
	for _, d := range jobDefs {
		job := Job{
			ID: d.id, Group: d.group, Title: d.title, Description: d.description,
			Schedule: d.schedule, Kind: d.kind,
		}
		if r := s.jobs.runs[d.id]; r != nil {
			last := *r.last
			job.LastRun = &last
			job.Runs = r.runs
		}
		if d.kind == jobTimed {
			job.NextRunAt = d.nextRun(job.LastRun, now)
		}
		out.Jobs = append(out.Jobs, job)
	}
	return out, nil
}

// JobList is the Jobs tab (GET /admin/jobs).
type JobList struct {
	Jobs  []Job      `json:"jobs"`
	Since *time.Time `json:"since" doc:"When bitploy started its background jobs; runs count from then. null when they do not run in this process"`
}

// Job is one background job on the Jobs tab.
type Job struct {
	ID          string     `json:"id"`
	Group       string     `json:"group" enum:"disk,backups,monitoring,upkeep,nightly"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Schedule    string     `json:"schedule" doc:"When it runs, in words"`
	Kind        string     `json:"kind" enum:"timed,triggered,always_on" doc:"timed has a next run; always_on listens all the time and records no runs"`
	LastRun     *JobRun    `json:"last_run" doc:"The last run since bitploy started; null for none"`
	NextRunAt   *time.Time `json:"next_run_at" doc:"null when the job is not timed, or an interval job has not run since bitploy started"`
	Runs        int64      `json:"runs" doc:"Runs since bitploy started"`
}

// JobRun is one run of a background job.
type JobRun struct {
	StartedAt  time.Time `json:"started_at"`
	DurationMs int64     `json:"duration_ms"`
	OK         bool      `json:"ok"`
	Error      string    `json:"error,omitempty"`
	Summary    string    `json:"summary,omitempty" doc:"What the run did, in a few words"`
}
