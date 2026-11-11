package app

// The disk page (Admin > Server > Disk) and the automatic disk cleanup.
// internal/diskuse decides what may go; this file feeds it what Docker and
// the database say, runs the removals, and records and announces them.
//
// The automatic cleanup watches the data directory's filesystem every
// minute. Below the admin's threshold (10% free unless changed) it runs
// diskuse.AutoSteps in order and stops as soon as free space is back above
// the threshold plus diskuse.Margin. When a build, upload or backup is refused
// for disk space (below metrics.MinFreeDiskFraction) it runs the steps that
// only make the next build slower (diskuse.SafeSteps). It never deletes
// volumes and never touches running containers, and does nothing in dev mode.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/deploy"
	"github.com/AVMG20/bitploy/internal/diskuse"
	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/metrics"
	"github.com/AVMG20/bitploy/internal/notify"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// EventDiskLow is the admins' notification about an automatic cleanup.
const EventDiskLow = "server.disk_low"

const (
	// diskReportTTL is how long a measured disk report is reused; measuring
	// sizes every volume, which takes long on a big server.
	diskReportTTL = 2 * time.Minute
	// diskWatchEvery is how often the automatic cleanup checks free space.
	diskWatchEvery = time.Minute
	// Limits of the admin's threshold, in percent.
	minDiskThreshold, maxDiskThreshold = 2, 50
	diskTopItems                       = 100
)

// Triggers of an automatic cleanup (disk_cleanups.trigger).
const (
	triggerLowDisk      = "low_disk"
	triggerBuildRefused = "build_refused"
)

// diskState is the zero-value-usable state of the disk page and the
// automatic cleanup on Service.
type diskState struct {
	run sync.Mutex // one cleanup at a time, by hand or automatic

	reportMu sync.Mutex
	reportAt time.Time
	report   *DiskReport

	once     sync.Once
	kick     chan struct{}
	watchMu  sync.Mutex
	low      diskuse.Watch // the threshold
	refused  diskuse.Watch // builds refused for disk
	measured bool          // the watches were seeded from the last recorded run

	// space replaces the statfs of the data directory in tests.
	space func() (diskuse.Space, error)
}

func (s *Service) diskInit() {
	s.disk.once.Do(func() { s.disk.kick = make(chan struct{}, 1) })
}

// diskSpace is the size and free space of the data directory's filesystem.
func (s *Service) diskSpace() (diskuse.Space, error) {
	if s.disk.space != nil {
		return s.disk.space()
	}
	path := s.Cfg.DataDir
	if _, err := os.Stat(path); err != nil {
		path = "/"
	}
	total, free, err := metrics.DiskUsage(path)
	return diskuse.Space{Total: total, Free: free}, err
}

// kickDiskCleanup asks the watcher for the safe steps: a build, upload or
// backup was just refused for disk space. It never blocks.
func (s *Service) kickDiskCleanup() {
	s.diskInit()
	select {
	case s.disk.kick <- struct{}{}:
	default:
	}
}

// ---- settings ----

func diskAutoView(st db.ServerSetting) (enabled bool, percent int) {
	percent = int(st.DiskCleanupMinFreePercent)
	if percent < minDiskThreshold || percent > maxDiskThreshold {
		percent = 10
	}
	return st.DiskAutoCleanup != 0, percent
}

func (s *Service) diskAuto(ctx context.Context) (DiskAutoCleanup, error) {
	st, err := s.serverSettings(ctx, s.Q)
	if err != nil {
		return DiskAutoCleanup{}, err
	}
	out := DiskAutoCleanup{Runs: []DiskCleanupRun{}, Dev: s.Cfg.Dev}
	out.Enabled, out.MinFreePercent = diskAutoView(st)
	rows, err := s.Q.ListDiskCleanups(ctx, 5)
	if err != nil {
		return DiskAutoCleanup{}, apperr.Internal(err)
	}
	for _, r := range rows {
		out.Runs = append(out.Runs, diskRunView(r))
	}
	return out, nil
}

func diskRunView(r db.DiskCleanup) DiskCleanupRun {
	pct := func(free int64) float64 {
		if r.TotalBytes <= 0 {
			return 0
		}
		return round1(float64(free) / float64(r.TotalBytes) * 100)
	}
	run := DiskCleanupRun{
		StartedAt: ts(r.StartedAt), FinishedAt: ts(r.FinishedAt), Trigger: r.Trigger, Result: r.Result,
		FreedBytes: r.FreedBytes, FreeBeforePercent: pct(r.FreeBefore), FreeAfterPercent: pct(r.FreeAfter),
		Steps: []diskuse.StepResult{},
	}
	_ = json.Unmarshal([]byte(r.Steps), &run.Steps)
	return run
}

// AdminUpdateDiskCleanup turns the automatic cleanup on or off and sets its
// threshold (admin).
func (s *Service) AdminUpdateDiskCleanup(ctx context.Context, a Actor, in UpdateDiskCleanupInput) (DiskAutoCleanup, error) {
	if err := RequireAdmin(a); err != nil {
		return DiskAutoCleanup{}, err
	}
	if in.Enabled == nil && in.MinFreePercent == nil {
		return DiskAutoCleanup{}, apperr.BadRequest("nothing_to_change", "Send enabled and/or min_free_percent.", "Example: {\"min_free_percent\": 10}.")
	}
	st, err := s.serverSettings(ctx, s.Q)
	if err != nil {
		return DiskAutoCleanup{}, err
	}
	enabled, percent := diskAutoView(st)
	changes := map[string]any{}
	if in.Enabled != nil && *in.Enabled != enabled {
		changes["enabled"] = map[string]any{"from": enabled, "to": *in.Enabled}
		enabled = *in.Enabled
	}
	if in.MinFreePercent != nil {
		v := *in.MinFreePercent
		if v < minDiskThreshold || v > maxDiskThreshold {
			return DiskAutoCleanup{}, apperr.BadRequest("invalid_min_free_percent",
				fmt.Sprintf("The threshold must be %d to %d percent.", minDiskThreshold, maxDiskThreshold), "10 suits most servers.")
		}
		if v != percent {
			changes["min_free_percent"] = map[string]any{"from": percent, "to": v}
			percent = v
		}
	}
	if len(changes) > 0 {
		if err := s.Q.UpdateDiskCleanupSettings(ctx, db.UpdateDiskCleanupSettingsParams{
			DiskAutoCleanup: b2i(enabled), DiskCleanupMinFreePercent: int64(percent), UpdatedAt: s.now(),
		}); err != nil {
			return DiskAutoCleanup{}, apperr.Internal(err)
		}
		s.Audit(ctx, a, "admin.disk_cleanup_settings", "server", "disk", changes)
	}
	return s.diskAuto(ctx)
}

// ---- what is there ----

// diskFacts collects what the database and the deploy runtime know.
func (s *Service) diskFacts(ctx context.Context) (diskuse.Facts, map[string]db.App, error) {
	f := diskuse.Facts{
		Apps: map[string]diskuse.App{}, ImageApp: map[string]string{}, Protected: map[string]bool{},
		CurrentContainers: map[string]bool{}, ActiveDeployments: s.activeDeploymentIDs(), BusyApps: s.busyAppIDs(),
		AppVolumes: map[string]string{}, BuilderVolume: deploy.BuilderStateVolume, Now: s.Now(),
	}
	if s.Docker != nil {
		f.Instance = s.Docker.Instance
	}
	apps, err := s.Q.ListAllApps(ctx)
	if err != nil {
		return f, nil, err
	}
	byID := make(map[string]db.App, len(apps))
	for _, a := range apps {
		byID[a.ID] = a
		f.Apps[a.ID] = diskuse.App{ID: a.ID, Slug: a.Slug, Name: a.Name}
		f.AppVolumes[appVolumeName(a)] = a.ID
	}
	refs, err := s.Q.ListDeploymentImageRefs(ctx)
	if err != nil {
		return f, nil, err
	}
	for _, r := range refs {
		ref := diskuse.NormalizeRef(r.Image)
		f.ImageApp[ref] = r.AppID
		if r.IsCurrent != 0 || inFlight(r.State) {
			f.Protected[ref] = true
		}
	}
	cur, err := s.Q.ListCurrentContainers(ctx)
	if err != nil {
		return f, nil, err
	}
	for _, c := range cur {
		if c.ContainerName.Valid {
			f.CurrentContainers[c.ContainerName.String] = true
		}
	}
	vols, err := s.Q.ListAllAppVolumes(ctx)
	if err != nil {
		return f, nil, err
	}
	for _, v := range vols {
		f.AppVolumes[v.VolumeName] = v.AppID
	}
	return f, byID, nil
}

// inFlight: a deployment in one of these states may still need its image.
func inFlight(state string) bool {
	switch state {
	case "queued", "fetching", "validating", "building", "starting", "health_check", "swapping":
		return true
	}
	return false
}

// diskInventory lists what Docker stores and sorts it.
func (s *Service) diskInventory(ctx context.Context, volumes bool) (diskuse.Report, map[string]db.App, error) {
	inv, err := s.Docker.Inventory(ctx, volumes)
	if err != nil {
		return diskuse.Report{}, nil, err
	}
	f, apps, err := s.diskFacts(ctx)
	if err != nil {
		return diskuse.Report{}, nil, err
	}
	return f.Classify(inv), apps, nil
}

// diskFiles adds up bitploy's own files in the data directory.
func (s *Service) diskFiles() []DiskGroup {
	sums := map[string]*DiskGroup{}
	add := func(key string, size int64) {
		g := sums[key]
		if g == nil {
			g = &DiskGroup{Key: key}
			sums[key] = g
		}
		g.Bytes += size
		g.Count++
	}
	root := s.Cfg.DataDir
	entries, err := os.ReadDir(root)
	if err != nil {
		return []DiskGroup{}
	}
	for _, e := range entries {
		name := e.Name()
		key := "other"
		switch {
		case strings.HasPrefix(name, "bitploy.db"):
			key = "database"
		case name == "sources":
			key = "sources"
		case name == "uploads":
			key = "uploads"
		case name == "builds", name == "logs":
			key = "builds"
		case name == "traefik":
			key = "proxy"
		case name == "backups":
			// App backups live in backups/<app id>/, copies of the database next to them.
			_ = filepath.WalkDir(filepath.Join(root, name), func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() || !d.Type().IsRegular() {
					return nil
				}
				info, err := d.Info()
				if err != nil {
					return nil
				}
				if filepath.Dir(p) == filepath.Join(root, name) {
					add("database_backups", info.Size())
				} else {
					add("app_backups", info.Size())
				}
				return nil
			})
			continue
		}
		_ = filepath.WalkDir(filepath.Join(root, name), func(_ string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !d.Type().IsRegular() {
				return nil
			}
			if info, err := d.Info(); err == nil {
				add(key, info.Size())
			}
			return nil
		})
	}
	order := []string{"database", "database_backups", "app_backups", "sources", "uploads", "builds", "proxy", "other"}
	out := make([]DiskGroup, 0, len(order))
	for _, k := range order {
		if g := sums[k]; g != nil {
			out = append(out, *g)
		}
	}
	return out
}

func diskAppRef(apps map[string]db.App, id string) *DiskAppRef {
	a, ok := apps[id]
	if !ok {
		return nil
	}
	return &DiskAppRef{ID: a.ID, Slug: a.Slug, Name: a.Name, ProjectID: a.ProjectID}
}

func shortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// AdminDiskReport says where the disk space goes and what each cleanup
// would free (admin). It is measured at most every two minutes unless
// refresh is set.
func (s *Service) AdminDiskReport(ctx context.Context, a Actor, refresh bool) (DiskReport, error) {
	if err := RequireAdmin(a); err != nil {
		return DiskReport{}, err
	}
	auto, err := s.diskAuto(ctx)
	if err != nil {
		return DiskReport{}, err
	}
	d := &s.disk
	d.reportMu.Lock()
	defer d.reportMu.Unlock()
	if refresh || d.report == nil || s.Now().Sub(d.reportAt) >= diskReportTTL {
		rep, err := s.measureDisk(ctx)
		if err != nil {
			return DiskReport{}, err
		}
		d.report, d.reportAt = &rep, s.Now()
	}
	out := *d.report
	out.Auto = auto
	return out, nil
}

// forgetDiskReport makes the next page load measure again (after a cleanup).
func (s *Service) forgetDiskReport() {
	s.disk.reportMu.Lock()
	s.disk.report = nil
	s.disk.reportMu.Unlock()
}

func (s *Service) measureDisk(ctx context.Context) (DiskReport, error) {
	out := DiskReport{
		MeasuredAt: s.Now().UTC(), BuildsPausedPercent: metrics.MinFreeDiskFraction * 100,
		Segments: []DiskSegment{}, ImageGroups: []DiskGroup{}, Images: []DiskImageItem{},
		Containers: []DiskContainerItem{}, Volumes: []DiskVolumeItem{}, Cleanups: []DiskCleanupOption{},
	}
	if sp, err := s.diskSpace(); err == nil {
		out.Filesystem = DiskFilesystem{TotalBytes: int64(sp.Total), FreeBytes: int64(sp.Free), UsedBytes: int64(sp.Total - sp.Free)}
	}
	out.Files = s.diskFiles()
	var filesBytes int64
	for _, g := range out.Files {
		filesBytes += g.Bytes
	}
	seg := func(key string, b int64) {
		if b > 0 {
			out.Segments = append(out.Segments, DiskSegment{Key: key, Bytes: b})
		}
	}
	if s.Docker == nil {
		seg("bitploy_files", filesBytes)
		seg("other", out.Filesystem.UsedBytes-filesBytes)
		return out, nil
	}
	mctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	rep, apps, err := s.diskInventory(mctx, true)
	if err != nil {
		s.Log.Warn("disk inventory failed", "err", err)
		out.DockerError = dockerx.DescribeError(err)
		seg("bitploy_files", filesBytes)
		seg("other", out.Filesystem.UsedBytes-filesBytes)
		return out, nil
	}
	out.DockerAvailable = true

	// Images by category; layers several images share count with "in use".
	groups := map[string]*DiskGroup{}
	for _, k := range []string{diskuse.ImageInUse, diskuse.ImageRollback, diskuse.ImageUnused, diskuse.ImageOther} {
		groups[k] = &DiskGroup{Key: k}
	}
	for i, im := range rep.Images {
		g := groups[im.Category]
		g.Bytes += im.Unique
		g.Count++
		if i < diskTopItems {
			out.Images = append(out.Images, DiskImageItem{
				ID: shortID(im.ID), Tags: orEmptySlice(im.Tags), App: diskAppRef(apps, im.AppID), Category: im.Category,
				SizeBytes: im.Size, UniqueBytes: im.Unique, InUse: im.InUse, Removable: im.Removable, CreatedAt: im.Created,
			})
		}
	}
	groups[diskuse.ImageInUse].Bytes += rep.SharedImageBytes
	for _, k := range []string{diskuse.ImageInUse, diskuse.ImageRollback, diskuse.ImageUnused, diskuse.ImageOther} {
		out.ImageGroups = append(out.ImageGroups, *groups[k])
	}

	var containerBytes, appVolBytes, otherVolBytes, builderBytes int64
	for _, c := range rep.Containers {
		containerBytes += c.SizeRw
		if c.SizeRw > 0 || c.Category == diskuse.ContainerLeftover || c.Category == diskuse.ContainerStopped {
			if len(out.Containers) < diskTopItems {
				out.Containers = append(out.Containers, DiskContainerItem{
					Name: c.Name, State: c.State, Category: c.Category, App: diskAppRef(apps, c.AppID), Bytes: c.SizeRw, Removable: c.Removable,
				})
			}
		}
	}
	for _, v := range rep.Volumes {
		size := v.Size
		switch v.Category {
		case diskuse.VolumeApp:
			appVolBytes += max(size, 0)
		case diskuse.VolumeBuilder:
			builderBytes += max(size, 0)
		default:
			otherVolBytes += max(size, 0)
		}
		if len(out.Volumes) < diskTopItems {
			item := DiskVolumeItem{Name: v.Name, Category: v.Category, App: diskAppRef(apps, v.AppID), Bytes: size, InUse: v.RefCount > 0, Removable: v.Removable}
			if v.Category == diskuse.VolumeOrphan {
				item.App = nil
			}
			out.Volumes = append(out.Volumes, item)
		}
	}
	out.BuildCache = DiskBuildCache{BuilderBytes: builderBytes, DockerBytes: rep.BuildCacheBytes}

	seg("images_in_use", groups[diskuse.ImageInUse].Bytes)
	seg("images_rollback", groups[diskuse.ImageRollback].Bytes)
	seg("images_unused", groups[diskuse.ImageUnused].Bytes)
	seg("images_other", groups[diskuse.ImageOther].Bytes)
	seg("build_cache", builderBytes+rep.BuildCacheBytes)
	seg("containers", containerBytes)
	seg("volumes_app", appVolBytes)
	seg("volumes_other", otherVolBytes)
	seg("bitploy_files", filesBytes)
	var known int64
	for _, sg := range out.Segments {
		known += sg.Bytes
	}
	seg("other", out.Filesystem.UsedBytes-known)

	for _, k := range diskuse.Kinds {
		t := rep.Targets(k)
		opt := DiskCleanupOption{Kind: k, Bytes: t.Bytes, Count: t.Images + len(t.Containers) + len(t.Volumes), Volumes: t.Volumes}
		if k == diskuse.OrphanVolumes && opt.Volumes == nil {
			opt.Volumes = []string{}
		}
		if k != diskuse.OrphanVolumes {
			opt.Volumes = nil
		}
		out.Cleanups = append(out.Cleanups, opt)
	}
	return out, nil
}

func orEmptySlice(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// ---- cleaning up ----

// diskStep runs one cleanup kind. volumes are the orphan volumes to remove
// (OrphanVolumes only). auto: the automatic cleanup, which never removes
// volumes.
func (s *Service) diskStep(ctx context.Context, kind string, volumes []string, auto bool) (reported int64, removed int, err error) {
	if s.Docker == nil {
		return 0, 0, errors.New("docker is not available")
	}
	switch kind {
	case diskuse.BuildCache:
		n, err := s.buildBuilder().Prune(ctx, 0)
		reported += n
		if s.Cfg.Dev {
			return reported, 0, err // the daemon's own cache is the developer's
		}
		m, derr := s.Docker.PruneBuildCache(ctx, 0)
		reported += int64(m)
		return reported, 0, errors.Join(err, derr)
	case diskuse.UnusedImages, diskuse.RollbackImages, diskuse.OtherImages:
		rep, _, err := s.diskInventory(ctx, false)
		if err != nil {
			return 0, 0, err
		}
		n, err := s.removeImages(ctx, rep, kind)
		return 0, n, err
	case diskuse.StoppedContainers:
		rep, _, err := s.diskInventory(ctx, false)
		if err != nil {
			return 0, 0, err
		}
		var errs []error
		for _, c := range rep.Containers {
			if c.Category != diskuse.ContainerLeftover || !c.Removable {
				continue
			}
			if err := s.Docker.RemoveStoppedContainer(ctx, c.ID); err != nil {
				if !dockerx.IsInUse(err) {
					errs = append(errs, err)
				}
				continue
			}
			removed++
			reported += c.SizeRw
		}
		return reported, removed, firstErr(errs)
	case diskuse.OrphanVolumes:
		if auto {
			return 0, 0, errors.New("the automatic cleanup never removes volumes")
		}
		return s.removeOrphanVolumes(ctx, volumes)
	}
	return 0, 0, fmt.Errorf("unknown cleanup %q", kind)
}

// removeImages removes the images a cleanup of kind picks: by tag (Docker
// deletes the image with its last tag), or by id when it has none. Docker
// refuses an image a container uses; that one is skipped.
func (s *Service) removeImages(ctx context.Context, rep diskuse.Report, kind string) (int, error) {
	removed := 0
	var errs []error
	for _, im := range rep.RemovableImages(kind) {
		refs := im.Tags
		if len(refs) == 0 {
			refs = []string{im.ID}
		}
		ok := true
		for _, ref := range refs {
			if ctx.Err() != nil {
				return removed, ctx.Err()
			}
			if err := s.Docker.RemoveImage(ctx, ref); err != nil {
				ok = false
				if !dockerx.IsInUse(err) {
					errs = append(errs, err)
				}
				break
			}
		}
		if ok {
			removed++
		}
	}
	return removed, firstErr(errs)
}

// removeOrphanVolumes removes the named volumes that are still bitploy
// volumes of deleted apps; any other name is left alone.
func (s *Service) removeOrphanVolumes(ctx context.Context, names []string) (int64, int, error) {
	f, _, err := s.diskFacts(ctx)
	if err != nil {
		return 0, 0, err
	}
	var reported int64
	removed := 0
	var errs []error
	for _, name := range names {
		labels, err := s.Docker.VolumeLabels(ctx, name)
		if dockerx.IsNotFound(err) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		v := f.ClassifyVolume(dockerx.InvVolume{Name: name, Labels: labels, RefCount: 0, Size: -1})
		if v.Category != diskuse.VolumeOrphan {
			continue
		}
		if err := s.Docker.RemoveUnusedVolume(ctx, name); err != nil {
			errs = append(errs, err)
			continue
		}
		removed++
	}
	return reported, removed, firstErr(errs)
}

func firstErr(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return errs[0]
	}
	return fmt.Errorf("%w (and %d more)", errs[0], len(errs)-1)
}

// AdminDiskCleanup runs one cleanup now (admin) and says what it freed.
func (s *Service) AdminDiskCleanup(ctx context.Context, a Actor, in DiskCleanupInput) (DiskCleanupResult, error) {
	if err := RequireAdmin(a); err != nil {
		return DiskCleanupResult{}, err
	}
	kind := strings.TrimSpace(in.Kind)
	valid := false
	for _, k := range diskuse.Kinds {
		valid = valid || k == kind
	}
	if !valid {
		return DiskCleanupResult{}, apperr.BadRequest("invalid_cleanup", "There is no cleanup "+quote(kind)+".",
			"Use one of: "+strings.Join(diskuse.Kinds, ", ")+".")
	}
	if kind == diskuse.OrphanVolumes && len(in.Volumes) == 0 {
		return DiskCleanupResult{}, apperr.BadRequest("volumes_required", "Name the volumes to delete.",
			"Send the names from the disk report's orphan_volumes cleanup in volumes.")
	}
	if kind != diskuse.OrphanVolumes && len(in.Volumes) > 0 {
		return DiskCleanupResult{}, apperr.BadRequest("volumes_not_allowed", "Only the orphan_volumes cleanup takes volumes.", "Leave volumes out.")
	}
	if kind == diskuse.OtherImages && s.Cfg.Dev {
		return DiskCleanupResult{}, apperr.Conflict("dev_mode", "In dev mode bitploy leaves images that are not its own alone.", "Remove them with docker image prune.")
	}
	if s.Docker == nil {
		return DiskCleanupResult{}, apperr.Unavailable("docker_unavailable", "bitploy cannot reach Docker right now.", "Check that the Docker daemon runs.")
	}
	if !s.disk.run.TryLock() {
		return DiskCleanupResult{}, apperr.Conflict("disk_cleanup_running", "A disk cleanup is already running.", "Try again when it has finished.")
	}
	defer s.disk.run.Unlock()
	// A closed browser tab does not stop a cleanup halfway.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Minute)
	defer cancel()

	before, berr := s.diskSpace()
	reported, removed, runErr := s.diskStep(ctx, kind, in.Volumes, false)
	after, aerr := s.diskSpace()
	freed := reported
	if berr == nil && aerr == nil {
		freed = diskuse.Freed(before, after, reported)
	}
	s.forgetDiskReport()
	details := map[string]any{"kind": kind, "freed_bytes": freed, "removed": removed}
	if len(in.Volumes) > 0 {
		details["volumes"] = in.Volumes
	}
	if runErr != nil {
		details["error"] = dockerx.DescribeError(runErr)
	}
	s.Audit(ctx, a, "admin.disk_cleanup", "server", "disk", details)
	if runErr != nil && freed == 0 && removed == 0 {
		return DiskCleanupResult{}, apperr.Unavailable("disk_cleanup_failed", "The cleanup failed: "+apperr.Redact(dockerx.DescribeError(runErr)),
			"Check that Docker runs, then try again.")
	}
	out := DiskCleanupResult{Kind: kind, FreedBytes: freed, Removed: removed}
	if runErr != nil {
		out.Error = apperr.Redact(dockerx.DescribeError(runErr))
	}
	return out, nil
}

// ---- the automatic cleanup ----

// runDiskWatch checks free space every minute and cleans up when it is low,
// and runs the safe steps when a build was refused for disk space. It
// returns when ctx ends.
func (s *Service) runDiskWatch(ctx context.Context) {
	s.diskInit()
	t := time.NewTicker(diskWatchEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.diskWatchOnce(ctx, triggerLowDisk)
		case <-s.disk.kick:
			s.diskWatchOnce(ctx, triggerBuildRefused)
		}
	}
}

// diskWatchOnce runs the automatic cleanup when it is due; it returns what
// it recorded, or nil when nothing ran.
func (s *Service) diskWatchOnce(ctx context.Context, trigger string) *DiskCleanupRun {
	if s.Cfg.Dev || s.Docker == nil {
		return nil
	}
	st, err := s.serverSettings(ctx, s.Q)
	if err != nil {
		return nil
	}
	enabled, percent := diskAutoView(st)
	if !enabled {
		return nil
	}
	sp, err := s.diskSpace()
	if err != nil || sp.Total == 0 {
		return nil
	}
	threshold, steps, w := float64(percent)/100, diskuse.AutoSteps, &s.disk.low
	if trigger == triggerBuildRefused {
		threshold, steps, w = metrics.MinFreeDiskFraction, diskuse.SafeSteps, &s.disk.refused
	}
	now := s.Now()
	s.disk.watchMu.Lock()
	s.seedDiskWatch(ctx)
	due := w.Due(now, sp.FreeFraction(), threshold)
	s.disk.watchMu.Unlock()
	if !due {
		return nil
	}
	if !s.disk.run.TryLock() {
		return nil // a cleanup by hand runs; look again next minute
	}
	defer s.disk.run.Unlock()

	s.Log.Warn("disk almost full, cleaning up", "trigger", trigger, "free_percent", round1(sp.FreeFraction()*100), "threshold_percent", threshold*100)
	runs := make([]diskuse.Step, 0, len(steps))
	for _, k := range steps {
		runs = append(runs, diskuse.Step{Kind: k, Run: func(ctx context.Context) (int64, int, error) {
			return s.diskStep(ctx, k, nil, true)
		}})
	}
	results, reached := diskuse.Escalate(ctx, runs, s.diskSpace, threshold+diskuse.Margin)
	after, err := s.diskSpace()
	if err != nil {
		after = sp
	}
	end := s.Now()
	s.disk.watchMu.Lock()
	w.Ran(end, after.FreeFraction())
	s.disk.watchMu.Unlock()
	s.forgetDiskReport()
	if len(results) == 0 {
		return nil // space came back before the first step
	}

	var freed int64
	failed := len(results) > 0
	for _, r := range results {
		freed += r.FreedBytes
		failed = failed && r.Error != ""
	}
	result := "still_low"
	switch {
	case reached:
		result = "recovered"
	case failed:
		result = "failed"
	}
	stepsJSON, _ := json.Marshal(results)
	row := db.DiskCleanup{
		Trigger: trigger, StartedAt: now.Unix(), FinishedAt: end.Unix(), TotalBytes: int64(sp.Total),
		FreeBefore: int64(sp.Free), FreeAfter: int64(after.Free), FreedBytes: freed, Steps: string(stepsJSON), Result: result,
	}
	bg := context.WithoutCancel(ctx)
	if err := s.Q.InsertDiskCleanup(bg, db.InsertDiskCleanupParams{
		Trigger: row.Trigger, StartedAt: row.StartedAt, FinishedAt: row.FinishedAt, TotalBytes: row.TotalBytes,
		FreeBefore: row.FreeBefore, FreeAfter: row.FreeAfter, FreedBytes: row.FreedBytes, Steps: row.Steps, Result: row.Result,
	}); err != nil {
		s.Log.Error("recording the disk cleanup failed", "err", err)
	}
	_ = s.Q.DeleteOldDiskCleanups(bg)
	s.Audit(bg, System, "server.disk_cleanup", "server", "disk", map[string]any{
		"trigger": trigger, "freed_bytes": freed, "result": result, "steps": results,
		"free_before_percent": round1(sp.FreeFraction() * 100), "free_after_percent": round1(after.FreeFraction() * 100),
	})
	s.Log.Info("disk cleanup finished", "trigger", trigger, "result", result, "freed_bytes", freed, "free_percent", round1(after.FreeFraction()*100))
	run := diskRunView(row)
	s.notifyDiskCleanup(bg, run, percent)
	return &run
}

// seedDiskWatch starts the watches from the last recorded run, so a restart
// of bitploy does not clean up again right away. Call with watchMu held.
func (s *Service) seedDiskWatch(ctx context.Context) {
	if s.disk.measured {
		return
	}
	s.disk.measured = true
	rows, err := s.Q.ListDiskCleanups(ctx, 20)
	if err != nil {
		return
	}
	seen := map[string]bool{}
	for _, r := range rows { // newest first
		if seen[r.Trigger] || r.TotalBytes <= 0 {
			continue
		}
		seen[r.Trigger] = true
		w := &s.disk.low
		if r.Trigger == triggerBuildRefused {
			w = &s.disk.refused
		}
		w.Ran(ts(r.FinishedAt), float64(r.FreeAfter)/float64(r.TotalBytes))
	}
}

// busyAppIDs are the apps with a deployment, start or stop in progress.
func (s *Service) busyAppIDs() map[string]bool {
	r := &s.deployRT
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	out := make(map[string]bool, len(r.busy))
	for id := range r.busy {
		out[id] = true
	}
	return out
}

// ---- telling the admins ----

// diskStepNames are the cleanup kinds in a message.
var diskStepNames = map[string]string{
	diskuse.BuildCache:        "build cache",
	diskuse.UnusedImages:      "unused bitploy images",
	diskuse.StoppedContainers: "leftover containers",
	diskuse.RollbackImages:    "older app versions kept for rollback",
	diskuse.OtherImages:       "unused images that are not bitploy's",
}

// notifyDiskCleanup tells every admin who subscribed to server.disk_low
// what the automatic cleanup did.
func (s *Service) notifyDiskCleanup(ctx context.Context, run DiskCleanupRun, percent int) {
	var did []string
	for _, st := range run.Steps {
		if st.FreedBytes > 0 || st.Removed > 0 {
			did = append(did, diskStepNames[st.Kind])
		}
	}
	m := notify.Message{Event: EventDiskLow, Time: s.Now(), Tag: "server-disk", URL: s.dashboardURL("/admin/disk")}
	m.Fields = []notify.Field{
		{Name: "Free before", Value: fmt.Sprintf("%.1f%%", run.FreeBeforePercent), Inline: true},
		{Name: "Free now", Value: fmt.Sprintf("%.1f%%", run.FreeAfterPercent), Inline: true},
		{Name: "Freed", Value: bytesText(run.FreedBytes), Inline: true},
	}
	if len(did) > 0 {
		m.Body = "Deleted " + strings.Join(did, ", ") + "."
	}
	if run.Result == "recovered" {
		m.Title = "bitploy freed " + bytesText(run.FreedBytes) + " of disk space"
		m.Tone, m.Severity = notify.ToneSuccess, notify.Normal
	} else {
		m.Title = "The server disk is still almost full"
		if run.Trigger == triggerBuildRefused {
			m.Title = "The server disk is almost full: builds are paused"
		}
		m.Tone, m.Severity = notify.ToneFailure, notify.Emergency
		m.Hint = fmt.Sprintf("Open the disk page to see what takes the space. Below %d%% free bitploy cleans up again in 30 minutes, or sooner when free space drops further.", percent)
		if run.Trigger == triggerBuildRefused {
			m.Hint = fmt.Sprintf("Builds, uploads and backups start again above %.0f%% free. Open the disk page to see what takes the space.", metrics.MinFreeDiskFraction*100)
		}
	}
	s.notifyAdmins(ctx, m, EventDiskLow)
}

// notifyAdmins queues m for every enabled channel of every active admin
// that gets one of events.
func (s *Service) notifyAdmins(ctx context.Context, m notify.Message, events ...string) {
	s.notifyInit()
	ids, err := s.Q.ListAdminUserIDs(ctx)
	if err != nil {
		return
	}
	for _, uid := range ids {
		chans, err := s.Q.ListEnabledNotificationChannels(ctx, uid)
		if err != nil {
			continue
		}
		for _, c := range chans {
			if !subscribed(parseEvents(c.Events), events) {
				continue
			}
			select {
			case s.notif.queue <- notifyJob{channelID: c.ID, msg: m}:
			default:
				s.Log.Warn("notification queue full; message dropped", "channel", c.ID, "event", m.Event)
			}
		}
	}
}
