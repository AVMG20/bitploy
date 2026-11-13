package app

// The disk guard keeps a server whose disk fills up from going down with it.
// The automatic cleanup (admin_disk.go) frees what bitploy may delete; the
// guard acts on what keeps writing:
//
//   - Below the builds threshold (Admin > Disk, 15% free unless changed) it
//     pauses builds: no queued build starts until free space is back above
//     the threshold plus diskuse.Margin (rollbacks still run: they build
//     nothing). It asks the automatic cleanup to run first (when it is on;
//     the cleanup runs in its own goroutine, so the guard keeps looking), and
//     when the disk is still low at a later look, with no cleanup running,
//     it stops the builds that run (disk_full_build_cancelled).
//   - While builds are paused it asks the /data volume check to run early, so
//     an app over its storage limit is stopped without waiting for the hour.
//   - Below the critical level (half the builds threshold, between 1% and 5%
//     free) it stops the one app that wrote the most since its last look
//     (diskGrower), at most one every diskHeavyGap.
//
// Cost: every diskGuardEvery one statfs of the data directory, plus one
// read of the settings row when free space is below 52% (no threshold can be
// above that). Nothing else runs, and nothing is written, while the disk has
// room. Docker is asked for container sizes only while builds are paused, at
// most once per diskHeavyGap, one container at a time (running app
// containers only). The guard keeps a few numbers and, while the disk is low,
// one byte count per app; nothing grows over time.
//
// bitploy runs in a container and only sees its data directory, which on a
// standard install is on the same filesystem as Docker's own. Off Linux
// (development machines) the guard does not run.

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/diskuse"
	"github.com/AVMG20/bitploy/internal/notify"
	"github.com/AVMG20/bitploy/internal/store/db"
)

const (
	// diskGuardEvery is how often the guard looks at free space.
	diskGuardEvery = 30 * time.Second
	// The critical level is half the builds threshold, within these bounds.
	diskCriticalMin, diskCriticalMax = 0.01, 0.05
	// diskHeavyGap: while builds are paused, container sizes are measured
	// (and at the critical level at most one app stopped) this often at most.
	diskHeavyGap = 5 * time.Minute
	// diskVolumeCheckGap: while builds are paused, the /data volume check is
	// asked to run early this often at most.
	diskVolumeCheckGap = 10 * time.Minute
	// diskGrowerMin: an app is only stopped when it wrote at least this much
	// since the previous measurement.
	diskGrowerMin = 256 << 20
	// diskFastDrop: free space falling by this share of the disk between two
	// looks counts as falling fast (0.5% in 30 s: 800 MB on a 160 GB disk).
	diskFastDrop = 0.005
	// diskNotifyCooldown: admins hear about a new pause of builds at most
	// this often, so a disk hovering around the threshold does not message
	// them every few minutes.
	diskNotifyCooldown = time.Hour
)

// diskGuard is the guard's memory between looks; zero-value usable.
type diskGuard struct {
	mu        sync.Mutex
	paused    bool    // builds are paused for disk space
	cancelled bool    // this pause stopped the builds that ran
	lastFree  float64 // free fraction at the previous look; 0 before the first
	heavyAt   time.Time
	volumeAt  time.Time
	// sizes: per app, the bytes its running containers wrote plus its
	// volumes, at heavyAt. Replaced on every measurement; nil while the disk
	// has room.
	sizes map[string]int64
	// said: admins heard about this pause; saidAt: when they last heard about one.
	said   bool
	saidAt time.Time
}

// diskLevels are the guard's thresholds as free fractions.
type diskLevels struct{ builds, critical float64 }

func diskLevelsOf(set diskSettings) diskLevels {
	b := float64(set.builds) / 100
	return diskLevels{builds: b, critical: min(max(b/2, diskCriticalMin), diskCriticalMax)}
}

// diskGuardPlan is what one look asks for.
type diskGuardPlan struct {
	pause, resume  bool // pause: also ask the automatic cleanup to run
	stopBuilds     bool // still low a look later: stop the builds that run
	measureVolumes bool // run the /data volume check now
	measureApps    bool // measure what every app wrote
	critical       bool // stop the app that grows the most
}

// plan decides what to do at free (fraction) and remembers it. Call with mu held.
func (g *diskGuard) plan(now time.Time, free float64, lv diskLevels) diskGuardPlan {
	var p diskGuardPlan
	fast := g.lastFree > 0 && g.lastFree-free >= diskFastDrop
	g.lastFree = free
	if free >= lv.builds+diskuse.Margin {
		p.resume = g.paused
		g.paused, g.cancelled, g.sizes = false, false, nil
		g.heavyAt, g.volumeAt = time.Time{}, time.Time{}
		return p
	}
	if free >= lv.builds {
		return p // between the threshold and the margin: keep what is
	}
	if !g.paused {
		p.pause, g.paused = true, true // the cleanup gets one look to help first
	} else {
		p.stopBuilds = !g.cancelled
	}
	p.critical = free < lv.critical
	if (p.pause || fast || p.critical) && now.Sub(g.volumeAt) >= diskVolumeCheckGap {
		p.measureVolumes, g.volumeAt = true, now
	}
	if now.Sub(g.heavyAt) >= diskHeavyGap {
		p.measureApps, g.heavyAt = true, now
	} else {
		p.critical = false // stop at most one app per diskHeavyGap
	}
	return p
}

// sayPause reports whether admins should hear that builds are paused: once
// per pause, and no sooner than diskNotifyCooldown after the last one. Call
// with mu held.
func (g *diskGuard) sayPause(now time.Time) bool {
	if g.said || (!g.saidAt.IsZero() && now.Sub(g.saidAt) < diskNotifyCooldown) {
		return false
	}
	g.said, g.saidAt = true, now
	return true
}

// sayResume reports whether admins should hear that builds run again: only
// after they heard about the pause. Call with mu held.
func (g *diskGuard) sayResume() bool {
	said := g.said
	g.said = false
	return said
}

// diskGrower picks the app to stop when the disk is critically low: the one
// whose containers and volumes grew the most between two measurements (prev
// and cur, bytes per app), when that is at least diskGrowerMin.
//
// Why this rule: stopping an app frees nothing (its files stay), it only
// stops the growth, so only an app that is writing right now is worth
// stopping. An app without an earlier measurement is never picked, nor one
// that is merely big: that would take down a healthy app for space it took
// long ago. Writes outside /data are lost on the next deploy anyway, and
// /data has its own limit (the volume check stops an app over it), so the
// app that fills the disk this fast is almost always misbehaving (a log
// file, temp files). One app per measurement, so one runaway does not take
// others with it.
func diskGrower(prev, cur map[string]int64) (appID string, grew int64) {
	for id, n := range cur {
		before, ok := prev[id]
		if !ok {
			continue
		}
		if d := n - before; d >= diskGrowerMin && (d > grew || d == grew && id < appID) {
			appID, grew = id, d
		}
	}
	return appID, grew
}

// buildsPausedForDisk reports whether the guard holds the queued builds back.
func (s *Service) buildsPausedForDisk() bool {
	g := &s.disk.guard
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.paused
}

// diskBuildCancelledErr is the failure of a build the guard stopped.
func diskBuildCancelledErr() *apperr.Error {
	return apperr.Unavailable("disk_full_build_cancelled",
		"The server disk is almost full, so bitploy stopped this build. The version that was live keeps running.",
		"Tell the bitploy admin that the server disk is full. Deploy again once they have freed space.")
}

// runDiskGuard looks at free space every diskGuardEvery until ctx ends.
func (s *Service) runDiskGuard(ctx context.Context) {
	if s.Cfg.Dev || runtime.GOOS != "linux" {
		return // a development machine's disk is not the server's
	}
	t := time.NewTicker(diskGuardEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.diskGuardOnce(ctx)
		}
	}
}

// diskGuardOnce is one look of the guard.
func (s *Service) diskGuardOnce(ctx context.Context) {
	sp, err := s.diskSpace()
	if err != nil || sp.Total == 0 {
		return // not known (no statfs here): change nothing
	}
	free := sp.FreeFraction()
	// Above 52% free no threshold applies (the admin's are at most 50%), so
	// the settings are not even read.
	set := diskSettings{builds: maxDiskThreshold}
	if free < float64(maxDiskThreshold)/100+diskuse.Margin {
		set = s.diskSettingsNow(ctx)
	}
	lv := diskLevelsOf(set)
	now := s.Now()
	g := &s.disk.guard
	g.mu.Lock()
	p := g.plan(now, free, lv)
	sayResume := p.resume && g.sayResume()
	g.mu.Unlock()

	bg := context.WithoutCancel(ctx)
	if p.resume {
		s.Log.Info("disk has room again; builds run again", "free_percent", round1(free*100))
		s.Audit(bg, System, "server.builds_resumed", "server", "disk", map[string]any{"free_percent": round1(free * 100)})
		if sayResume {
			s.notifyAdmins(bg, notify.Message{
				Event: EventDiskLow, Time: now, Tag: "server-disk", URL: s.dashboardURL("/admin/disk"),
				Title: "Builds run again", Tone: notify.ToneSuccess, Severity: notify.Normal,
				Body: fmt.Sprintf("%.1f%% of the server disk is free again, so queued builds start.", free*100),
			}, EventDiskLow)
		}
		s.Wake()
		return
	}
	if p.pause {
		s.Log.Warn("disk almost full; builds paused", "free_percent", round1(free*100), "builds_threshold_percent", set.builds)
		// The safe steps now, in the cleanup's own goroutine (below the
		// cleanup threshold the minute's run takes the full steps). It does
		// nothing when the admin turned the automatic cleanup off.
		s.kickDiskCleanup()
	}
	if p.measureVolumes {
		s.metricsMgr().MeasureSoon() // the hourly /data check, now; it never runs twice at once
	}
	if p.stopBuilds {
		s.diskGuardStopBuilds(ctx, set, lv, sp)
	}
	if p.measureApps {
		s.diskGuardMeasure(ctx, p.critical, free)
	}
}

// diskGuardStopBuilds stops the builds that run, once the automatic cleanup
// had its chance and the disk is still below the builds threshold, and
// tells the admins.
func (s *Service) diskGuardStopBuilds(ctx context.Context, set diskSettings, lv diskLevels, after diskuse.Space) {
	if !s.disk.run.TryLock() {
		return // a cleanup runs (by hand, the minute's or ours): look again after it
	}
	s.disk.run.Unlock()
	n := s.cancelBuildsForDisk()
	now := s.Now()
	g := &s.disk.guard
	g.mu.Lock()
	g.cancelled = true
	say := g.sayPause(now)
	g.mu.Unlock()
	free := after.FreeFraction()
	bg := context.WithoutCancel(ctx)
	s.Audit(bg, System, "server.builds_paused", "server", "disk", map[string]any{
		"free_percent": round1(free * 100), "builds_threshold_percent": set.builds, "builds_stopped": n,
	})
	if !say {
		return
	}
	body := "No queued build starts until more of the disk is free. Running apps are not affected."
	if n > 0 {
		body = fmt.Sprintf("bitploy stopped %d %s that ran. ", n, plural(n, "build", "builds")) + body
	}
	s.notifyAdmins(bg, notify.Message{
		Event: EventDiskLow, Time: now, Tag: "server-disk", URL: s.dashboardURL("/admin/disk"),
		Title: "The server disk is almost full: builds are paused", Tone: notify.ToneFailure, Severity: notify.Emergency,
		Body: body,
		Fields: []notify.Field{
			{Name: "Free", Value: fmt.Sprintf("%s (%.1f%%)", bytesText(int64(after.Free)), free*100), Inline: true},
			{Name: "Builds start again above", Value: fmt.Sprintf("%.0f%%", (lv.builds+diskuse.Margin)*100), Inline: true},
		},
		Hint: "Open the disk page to see what takes the space. You hear about the next pause no sooner than in an hour.",
	}, EventDiskLow)
}

// diskGuardMeasure measures what every app wrote and, at the critical
// level, stops the app that grows the most (diskGrower).
func (s *Service) diskGuardMeasure(ctx context.Context, critical bool, free float64) {
	cur, err := s.appWrittenBytes(ctx)
	if err != nil {
		s.Log.Warn("disk guard: measuring the apps failed", "err", err)
		return
	}
	g := &s.disk.guard
	g.mu.Lock()
	prev, at := g.sizes, g.heavyAt
	g.sizes = cur
	g.mu.Unlock()
	if !critical {
		return
	}
	id, grew := diskGrower(prev, cur)
	if id == "" {
		return
	}
	since := diskHeavyGap
	if !at.IsZero() {
		since = max(s.Now().Sub(at), time.Minute)
	}
	s.stopAppForDisk(ctx, id, grew, since, free)
}

// appWrittenBytes is, per app, what its running containers wrote on top of
// their images plus what its volumes hold (as last measured by the /data
// volume check). Containers are asked one at a time, running app containers
// only.
func (s *Service) appWrittenBytes(ctx context.Context) (map[string]int64, error) {
	if s.disk.appBytes != nil {
		return s.disk.appBytes(ctx)
	}
	out := map[string]int64{}
	if s.Docker != nil {
		mctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		cs, err := s.Docker.ManagedContainers(mctx, false)
		if err != nil {
			return nil, err
		}
		for _, c := range cs {
			if n, err := s.Docker.ContainerWriteBytes(mctx, c.ID); err == nil {
				out[c.AppID] += n
			}
		}
	}
	vols, err := s.Q.OpsListVolumeUsage(ctx)
	if err != nil {
		return nil, err
	}
	for _, v := range vols {
		out[v.ID] += v.VolumeBytes
	}
	svcVols, err := s.Q.OpsListServiceVolumeUsage(ctx)
	if err != nil {
		return nil, err
	}
	for _, v := range svcVols {
		out[v.ID] += v.UsedBytes
	}
	return out, nil
}

// diskStopMessage is the status message of an app the guard stopped.
func diskStopMessage(grew int64, since time.Duration) string {
	return fmt.Sprintf("The server disk was almost full and this app wrote %s in the last %d minutes, more than any other app, so bitploy stopped it to keep the server running. "+
		"Check what the app writes (log files, temp files, uploads; keep what it must keep in /data), then start it again once the admin has freed space.",
		bytesText(grew), int((since+30*time.Second)/time.Minute))
}

// stopAppForDisk stops an app that fills the disk, with a status message
// that says why, and tells its owner and the admins.
func (s *Service) stopAppForDisk(ctx context.Context, appID string, grew int64, since time.Duration, free float64) {
	bg := context.WithoutCancel(ctx)
	row, err := s.Q.GetApp(bg, appID)
	if err != nil || row.DesiredState != "running" {
		return
	}
	s.Log.Warn("disk almost full: stopping the app that writes the most", "app", row.Slug, "wrote_bytes", grew, "free_percent", round1(free*100))
	if err := s.stopApp(bg, System, row); err != nil {
		s.Log.Error("stopping the app for disk space failed", "app", row.Slug, "err", err)
		return
	}
	msg := diskStopMessage(grew, since)
	if err := s.Q.SetAppStatus(bg, db.SetAppStatusParams{Status: "failed", StatusMessage: msg, UpdatedAt: s.now(), ID: appID}); err != nil {
		s.Log.Error("setting the disk stop message failed", "app", row.Slug, "err", err)
	}
	s.publishApp(bg, appID)
	s.Audit(bg, System, "app.disk_stopped", "app", appID, map[string]any{
		"slug": row.Slug, "wrote_bytes": grew, "free_percent": round1(free * 100),
	})
	s.notifyApp(bg, row, notify.Message{
		Event: EventAppCrashed, Severity: notify.Emergency, Tone: notify.ToneFailure, Time: s.Now(), Tag: row.ID,
		Title: fmt.Sprintf("%s was stopped: the server disk is almost full", row.Name), Body: msg,
		URL: s.appPage(row, ""), Fields: s.appFields(bg, row),
	}, EventAppCrashed)
	s.notifyAdmins(bg, notify.Message{
		Event: EventDiskLow, Time: s.Now(), Tag: "server-disk", URL: s.dashboardURL("/admin/disk"),
		Title: fmt.Sprintf("bitploy stopped %s to keep the server disk from filling up", row.Name), Tone: notify.ToneFailure, Severity: notify.Emergency,
		Body:   fmt.Sprintf("It wrote %s in the last %d minutes, more than any other app, with %.1f%% of the disk free.", bytesText(grew), int((since+30*time.Second)/time.Minute), free*100),
		Fields: []notify.Field{{Name: "App", Value: row.Slug, Inline: true}},
		Hint:   "Open the disk page to see what takes the space. Its owner was told and can start it again.",
	}, EventDiskLow)
}
