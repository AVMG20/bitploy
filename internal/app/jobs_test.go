package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/metrics"
	"github.com/AVMG20/bitploy/internal/traffic"
)

func jobByID(t *testing.T, list JobList, id string) Job {
	t.Helper()
	for _, j := range list.Jobs {
		if j.ID == id {
			return j
		}
	}
	t.Fatalf("no job %s", id)
	return Job{}
}

func TestJobDefs(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range jobDefs {
		if seen[d.id] {
			t.Errorf("job %s defined twice", d.id)
		}
		seen[d.id] = true
		if d.title == "" || d.description == "" || d.schedule == "" || d.group == "" {
			t.Errorf("job %s misses a title, description, schedule or group: %+v", d.id, d)
		}
		if words := len(strings.Fields(d.title)); words > 3 {
			t.Errorf("job %s: title %q is longer than 3 words", d.id, d.title)
		}
		switch d.kind {
		case jobTimed:
			if (d.every > 0) == (d.next != nil) {
				t.Errorf("timed job %s needs either every or next", d.id)
			}
		case jobTriggered, jobAlwaysOn:
			if d.every > 0 || d.next != nil {
				t.Errorf("job %s of kind %s has a schedule", d.id, d.kind)
			}
		default:
			t.Errorf("job %s: unknown kind %q", d.id, d.kind)
		}
	}
	// Every id the other packages report has a row, or its runs would be lost.
	for _, id := range []string{
		metrics.JobCollect, metrics.JobRollup, metrics.JobVolumeQuota, metrics.JobPruneMetrics, metrics.JobMeasureDisk,
		metrics.JobDockerPrune, metrics.JobBackupDB, metrics.JobPruneAudit, metrics.JobOldUploads, traffic.JobFlush,
		"disk_watch", "disk_stale_cache", "builder_idle",
	} {
		if !seen[id] {
			t.Errorf("no job row for %s", id)
		}
	}
}

func TestJobRan(t *testing.T) {
	e := newOpsEnv(t)
	ctx := context.Background()

	list, err := e.svc.AdminJobs(ctx, e.admin)
	if err != nil {
		t.Fatal(err)
	}
	if list.Since != nil {
		t.Errorf("since = %v before the jobs started", list.Since)
	}
	if j := jobByID(t, list, "backups"); j.LastRun != nil || j.Runs != 0 || j.NextRunAt != nil {
		t.Errorf("not run yet: %+v", j)
	}

	e.svc.jobsStarted()
	start := e.now
	e.now = e.now.Add(1500 * time.Millisecond)
	e.svc.jobRan("backups", start, nil, "started backups of 2 apps")
	list, _ = e.svc.AdminJobs(ctx, e.admin)
	if list.Since == nil || !list.Since.Equal(start) {
		t.Errorf("since = %v, want %v", list.Since, start)
	}
	j := jobByID(t, list, "backups")
	if j.Runs != 1 || j.LastRun == nil {
		t.Fatalf("after one run: %+v", j)
	}
	if r := *j.LastRun; !r.StartedAt.Equal(start) || r.DurationMs != 1500 || !r.OK || r.Error != "" || r.Summary != "started backups of 2 apps" {
		t.Errorf("last run = %+v", r)
	}
	if j.NextRunAt == nil || !j.NextRunAt.Equal(start.Add(backupCheckEvery)) {
		t.Errorf("next run = %v, want %v", j.NextRunAt, start.Add(backupCheckEvery))
	}

	// A failed run replaces the last one; the error is one short line.
	long := errors.Join(errors.New("first"), errors.New(strings.Repeat("x", 2*maxJobError)))
	e.svc.jobRan("backups", e.now, long, "")
	j = jobByID(t, mustJobs(t, e), "backups")
	if j.Runs != 2 || j.LastRun.OK || j.LastRun.Summary != "" {
		t.Errorf("after a failed run: %+v %+v", j, j.LastRun)
	}
	if msg := j.LastRun.Error; !strings.HasPrefix(msg, "first; x") || strings.Contains(msg, "\n") || len([]rune(msg)) != maxJobError {
		t.Errorf("error = %q (%d runes)", msg, len([]rune(msg)))
	}

	// What AdminJobs hands out is a copy.
	j.LastRun.Summary = "changed"
	if jobByID(t, mustJobs(t, e), "backups").LastRun.Summary == "changed" {
		t.Error("AdminJobs shares its record")
	}
}

func mustJobs(t *testing.T, e *opsEnv) JobList {
	t.Helper()
	list, err := e.svc.AdminJobs(context.Background(), e.admin)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestJobNextRun(t *testing.T) {
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	last := &JobRun{StartedAt: now.Add(-20 * time.Second)}

	interval := jobDef{kind: jobTimed, every: time.Minute}
	if got := interval.nextRun(nil, now); got != nil {
		t.Errorf("interval job that has not run: %v, want nil", got)
	}
	if got := interval.nextRun(last, now); got == nil || !got.Equal(now.Add(40*time.Second)) {
		t.Errorf("interval job: %v", got)
	}

	nightly := jobDef{kind: jobTimed, next: metrics.NextNightly}
	want := time.Date(2026, 11, 12, 3, 30, 0, 0, time.UTC)
	for _, l := range []*JobRun{nil, last} {
		if got := nightly.nextRun(l, now); got == nil || !got.Equal(want) {
			t.Errorf("nightly job (last %v): %v, want %v", l, got, want)
		}
	}

	e := newOpsEnv(t)
	e.svc.jobRan("notifications", e.now, nil, "deployment.failed")
	list := mustJobs(t, e)
	if j := jobByID(t, list, "notifications"); j.NextRunAt != nil || j.Runs != 1 || j.Kind != jobTriggered {
		t.Errorf("triggered job: %+v", j)
	}
	if j := jobByID(t, list, metrics.JobDockerPrune); j.NextRunAt == nil || !j.NextRunAt.Equal(metrics.NextNightly(e.now)) {
		t.Errorf("nightly job before its first run: %+v", j)
	}
	if j := jobByID(t, list, "crash_watch"); j.Kind != jobAlwaysOn || j.NextRunAt != nil || j.LastRun != nil {
		t.Errorf("always-on job: %+v", j)
	}
}

// The nightly job reports each step through the metrics manager's hook.
func TestNightlyStepsRecorded(t *testing.T) {
	e := newOpsEnv(t)
	e.svc.metricsMgr().Nightly(context.Background())
	list := mustJobs(t, e)
	for _, id := range []string{
		metrics.JobRollup, metrics.JobPruneMetrics, metrics.JobMeasureDisk, metrics.JobDockerPrune,
		metrics.JobBackupDB, metrics.JobPruneAudit, metrics.JobOldUploads,
	} {
		j := jobByID(t, list, id)
		if j.Runs != 1 || j.LastRun == nil || !j.LastRun.OK {
			t.Errorf("%s: %+v %+v", id, j, j.LastRun)
		}
	}
	if s := jobByID(t, list, metrics.JobDockerPrune).LastRun.Summary; s != "skipped: Docker is unavailable" {
		t.Errorf("docker prune summary = %q", s)
	}
	if s := jobByID(t, list, metrics.JobBackupDB).LastRun.Summary; !strings.HasPrefix(s, "saved a ") {
		t.Errorf("database backup summary = %q", s)
	}
	if s := jobByID(t, list, metrics.JobOldUploads).LastRun.Summary; s != "nothing to delete" {
		t.Errorf("old uploads summary = %q", s)
	}
}

func TestAdminJobsAdminOnly(t *testing.T) {
	e := newOpsEnv(t)
	ctx := context.Background()
	_, err := e.svc.AdminJobs(ctx, e.owner)
	wantCode(t, err, "admin_only")
	// An admin's API token is not a dashboard session.
	_, err = e.svc.AdminJobs(ctx, Actor{UserID: "usr_admin", Role: "admin", Channel: ChannelAPI, Scope: ScopeDeploy})
	wantCode(t, err, "admin_only")
	list, err := e.svc.AdminJobs(ctx, e.admin)
	if err != nil || len(list.Jobs) != len(jobDefs) {
		t.Fatalf("admin: %d jobs, %v", len(list.Jobs), err)
	}
}
