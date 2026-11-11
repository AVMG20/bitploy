package app

// The disk page (Admin > Server > Disk) and the automatic disk cleanup.
// internal/diskuse decides what may go; this file feeds it what Docker and
// the database say, runs the removals, and records and announces them.
//
// The automatic cleanup watches the data directory's filesystem every
// minute. Below the admin's threshold (10% free unless changed) it runs
// diskuse.AutoSteps in order and stops as soon as free space is back above
// the threshold plus diskuse.Margin. When a build, upload or backup is refused
// for disk space (below the admin's builds threshold, 15% unless changed) it
// runs the steps that only make the next build slower (diskuse.SafeSteps). It never deletes
// volumes and never touches running containers, and does nothing in dev mode.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
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
	// Limits of the admin's thresholds, in percent.
	minDiskThreshold, maxDiskThreshold = 2, 50
	minBuildsThreshold                 = 1
	diskTopItems                       = 100
	maxBuildCacheDays                  = 90
	// staleCacheEvery is how often build cache unused for the admin's days is deleted.
	staleCacheEvery = time.Hour
)

// Triggers of an automatic cleanup (disk_cleanups.trigger).
const (
	triggerLowDisk      = "low_disk"
	triggerBuildRefused = "build_refused"
	triggerManual       = "manual" // an admin ran the automatic cleanup by hand
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
	// lowAlerted and refusedAlerted: admins were told the disk stays low, and
	// free space has not come back above that watch's threshold since. Later
	// runs that free nothing are then neither recorded nor announced.
	lowAlerted, refusedAlerted bool
	// warned: admins got the early warning and free space has not come
	// back above the warning level since.
	warned bool

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

// diskSettings are the disk page's settings, with defaults for values out
// of range.
type diskSettings struct {
	enabled   bool
	percent   int             // the automatic cleanup starts below this much free
	builds    int             // builds, uploads and backups are refused below this much free
	steps     map[string]bool // the steps the automatic cleanup may take
	cacheDays int             // build cache unused this many days is deleted; 0 never
	warn      int             // admins are warned below this much free; 0 never
	versions  int             // versions every app keeps, the live one included
}

const (
	defaultRollbackVersions = 3
	maxRollbackVersions     = 10
	defaultWarnPercent      = 20
	maxWarnPercent          = 80
)

func diskSettingsOf(st db.ServerSetting) diskSettings {
	d := diskSettings{
		enabled: st.DiskAutoCleanup != 0, percent: int(st.DiskCleanupMinFreePercent),
		builds: int(st.DiskBuildsMinFreePercent), cacheDays: int(st.DiskBuildCacheDays),
		warn: int(st.DiskWarnFreePercent), versions: int(st.RollbackVersions), steps: map[string]bool{},
	}
	var steps []string
	if json.Unmarshal([]byte(st.DiskAutoSteps), &steps) != nil || len(steps) == 0 {
		steps = diskuse.AutoSteps
	}
	for _, k := range steps {
		d.steps[k] = true
	}
	if d.cacheDays < 0 || d.cacheDays > maxBuildCacheDays {
		d.cacheDays = 7
	}
	if d.percent < minDiskThreshold || d.percent > maxDiskThreshold {
		d.percent = 10
	}
	if d.builds < minBuildsThreshold || d.builds > maxDiskThreshold {
		d.builds = int(metrics.MinFreeDiskFraction * 100)
	}
	if d.warn < 0 || d.warn > maxWarnPercent {
		d.warn = defaultWarnPercent
	}
	if d.versions < 1 || d.versions > maxRollbackVersions {
		d.versions = defaultRollbackVersions
	}
	return d
}

// diskSettingsNow reads the settings; the defaults when that fails.
func (s *Service) diskSettingsNow(ctx context.Context) diskSettings {
	st, err := s.serverSettings(ctx, s.Q)
	if err != nil {
		return diskSettingsOf(db.ServerSetting{})
	}
	return diskSettingsOf(st)
}

// buildsMinFree is the free fraction below which builds, uploads and
// backups are refused.
func (s *Service) buildsMinFree(ctx context.Context) float64 {
	return float64(s.diskSettingsNow(ctx).builds) / 100
}

// rollbackVersions is how many versions every app keeps, the live one
// included (the others are its rollback targets).
func (s *Service) rollbackVersions(ctx context.Context) int {
	return s.diskSettingsNow(ctx).versions
}

// pick keeps the steps of all the admin allows, in all's order.
func (d diskSettings) pick(all []string) []string {
	out := make([]string, 0, len(all))
	for _, k := range all {
		if d.steps[k] {
			out = append(out, k)
		}
	}
	return out
}

// autoSteps are the automatic cleanup's steps under the admin's settings;
// safeSteps those of a build refused for disk space.
func (d diskSettings) autoSteps() []string { return d.pick(diskuse.AutoSteps) }
func (d diskSettings) safeSteps() []string { return d.pick(diskuse.SafeSteps) }

func (s *Service) diskAuto(ctx context.Context) (DiskAutoCleanup, error) {
	st, err := s.serverSettings(ctx, s.Q)
	if err != nil {
		return DiskAutoCleanup{}, err
	}
	d := diskSettingsOf(st)
	out := DiskAutoCleanup{
		Enabled: d.enabled, MinFreePercent: d.percent, BuildsMinFreePercent: d.builds, Steps: d.autoSteps(),
		BuildCacheDays: d.cacheDays, WarnFreePercent: d.warn, RollbackVersions: d.versions,
		Runs: []DiskCleanupRun{}, Dev: s.Cfg.Dev,
	}
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

// AdminUpdateDiskCleanup changes the disk page's settings (admin): the
// automatic cleanup, its threshold and steps, the builds threshold, the
// early warning, build cache days and the rollback versions apps keep.
func (s *Service) AdminUpdateDiskCleanup(ctx context.Context, a Actor, in UpdateDiskCleanupInput) (DiskAutoCleanup, error) {
	if err := RequireAdmin(a); err != nil {
		return DiskAutoCleanup{}, err
	}
	if in == (UpdateDiskCleanupInput{}) {
		return DiskAutoCleanup{}, apperr.BadRequest("nothing_to_change", "Send at least one setting to change.", "Example: {\"min_free_percent\": 10}.")
	}
	st, err := s.serverSettings(ctx, s.Q)
	if err != nil {
		return DiskAutoCleanup{}, err
	}
	d := diskSettingsOf(st)
	changes := map[string]any{}
	if in.Enabled != nil && *in.Enabled != d.enabled {
		changes["enabled"] = map[string]any{"from": d.enabled, "to": *in.Enabled}
		d.enabled = *in.Enabled
	}
	setInt := func(name string, v *int, cur *int, lo, hi int, hint string) error {
		if v == nil {
			return nil
		}
		if *v < lo || *v > hi {
			return apperr.BadRequest("invalid_"+name, fmt.Sprintf("%s must be between %d and %d.", name, lo, hi), hint)
		}
		if *v != *cur {
			changes[name] = map[string]any{"from": *cur, "to": *v}
			*cur = *v
		}
		return nil
	}
	for _, f := range []struct {
		name   string
		v      *int
		cur    *int
		lo, hi int
		hint   string
	}{
		{"min_free_percent", in.MinFreePercent, &d.percent, minDiskThreshold, maxDiskThreshold, "10 suits most servers."},
		{"builds_min_free_percent", in.BuildsMinFreePercent, &d.builds, minBuildsThreshold, maxDiskThreshold, "15 suits most servers; a large disk can go lower."},
		{"build_cache_days", in.BuildCacheDays, &d.cacheDays, 0, maxBuildCacheDays, "7 suits most servers; 0 keeps build cache until the disk runs low."},
		{"warn_free_percent", in.WarnFreePercent, &d.warn, 0, maxWarnPercent, "20 suits most servers; 0 turns the warning off."},
		{"rollback_versions", in.RollbackVersions, &d.versions, 1, maxRollbackVersions, "3 suits most apps."},
	} {
		if err := setInt(f.name, f.v, f.cur, f.lo, f.hi, f.hint); err != nil {
			return DiskAutoCleanup{}, err
		}
	}
	if in.Steps != nil {
		next := map[string]bool{}
		for _, k := range *in.Steps {
			if !slices.Contains(diskuse.AutoSteps, k) {
				return DiskAutoCleanup{}, apperr.BadRequest("invalid_steps", "There is no automatic cleanup step "+quote(k)+".",
					"Use any of: "+strings.Join(diskuse.AutoSteps, ", ")+".")
			}
			next[k] = true
		}
		if len(next) == 0 {
			return DiskAutoCleanup{}, apperr.BadRequest("invalid_steps", "Pick at least one step.", "To stop the automatic cleanup, turn it off instead.")
		}
		before, after := d.autoSteps(), (diskSettings{steps: next}).autoSteps()
		if !slices.Equal(before, after) {
			changes["steps"] = map[string]any{"from": before, "to": after}
			d.steps = next
		}
	}
	if d.warn != 0 && d.warn <= d.percent {
		return DiskAutoCleanup{}, apperr.BadRequest("invalid_warn_free_percent",
			fmt.Sprintf("The warning (%d%%) must be above the cleanup threshold (%d%%).", d.warn, d.percent), "Raise the warning, or set it to 0 to turn it off.")
	}
	if len(changes) > 0 {
		steps, _ := json.Marshal(d.autoSteps())
		if err := s.Q.UpdateDiskCleanupSettings(ctx, db.UpdateDiskCleanupSettingsParams{
			DiskAutoCleanup: b2i(d.enabled), DiskCleanupMinFreePercent: int64(d.percent),
			DiskBuildsMinFreePercent: int64(d.builds), DiskAutoSteps: string(steps),
			DiskBuildCacheDays: int64(d.cacheDays), DiskWarnFreePercent: int64(d.warn),
			RollbackVersions: int64(d.versions), UpdatedAt: s.now(),
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
	// bitploy's own helper image: deleting it only means pulling it again
	// for the next volume, backup or file browser, which fails offline.
	f.Protected[diskuse.NormalizeRef(dockerx.VolumeInitImage)] = true
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
		// Measured to the end even when the admin leaves the page: a request
		// cut off halfway would otherwise be kept as "cannot reach Docker".
		rep, err := s.measureDisk(context.WithoutCancel(ctx))
		if err != nil {
			return DiskReport{}, err
		}
		d.report, d.reportAt = &rep, s.Now()
	}
	out := *d.report
	out.Auto = auto
	out.BuildsPausedPercent = float64(auto.BuildsMinFreePercent)
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
		MeasuredAt: s.Now().UTC(),
		Segments:   []DiskSegment{}, ImageGroups: []DiskGroup{}, Images: []DiskImageItem{},
		Containers: []DiskContainerItem{}, Volumes: []DiskVolumeItem{}, Cleanups: []DiskCleanupOption{},
		Apps: []DiskAppUsage{},
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

	out.Apps = diskAppUsage(rep, apps)

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

// diskAppUsage adds up what every app's images, volumes and containers
// take, largest first. Apps that take nothing measurable are left out.
func diskAppUsage(rep diskuse.Report, apps map[string]db.App) []DiskAppUsage {
	by := map[string]*DiskAppUsage{}
	get := func(id string) *DiskAppUsage {
		if id == "" {
			return nil
		}
		u := by[id]
		if u == nil {
			ref := diskAppRef(apps, id)
			if ref == nil {
				return nil
			}
			u = &DiskAppUsage{App: *ref}
			by[id] = u
		}
		return u
	}
	for _, im := range rep.Images {
		u := get(im.AppID)
		if u == nil {
			continue
		}
		if im.Category == diskuse.ImageRollback {
			u.RollbackBytes += im.Unique
			u.RollbackCount++
		} else {
			u.ImageBytes += im.Unique
		}
	}
	for _, v := range rep.Volumes {
		if v.Category != diskuse.VolumeApp || v.Size <= 0 {
			continue
		}
		if u := get(v.AppID); u != nil {
			u.VolumeBytes += v.Size
		}
	}
	for _, c := range rep.Containers {
		if u := get(c.AppID); u != nil && c.SizeRw > 0 {
			u.ContainerBytes += c.SizeRw
		}
	}
	out := make([]DiskAppUsage, 0, len(by))
	for _, u := range by {
		u.TotalBytes = u.ImageBytes + u.RollbackBytes + u.VolumeBytes + u.ContainerBytes
		if u.TotalBytes > 0 {
			out = append(out, *u)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].TotalBytes != out[j].TotalBytes {
			return out[i].TotalBytes > out[j].TotalBytes
		}
		return out[i].App.Slug < out[j].App.Slug
	})
	return out
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
// volumes. enough, when set, stops removing images once it says so.
func (s *Service) diskStep(ctx context.Context, kind string, volumes []string, auto bool, enough func() bool) (reported int64, removed int, err error) {
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
		n, err := s.removeImages(ctx, rep, kind, enough)
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

// removeImages removes the images a cleanup of kind picks, in
// diskuse.RemovalOrder (oldest first, every app's newest rollback version
// last): by tag (Docker deletes the image with its last tag), or by id when
// it has none. Docker refuses an image a container uses; that one is
// skipped. enough (may be nil) is asked before each image; true stops.
func (s *Service) removeImages(ctx context.Context, rep diskuse.Report, kind string, enough func() bool) (int, error) {
	removed := 0
	var errs []error
	for _, im := range diskuse.RemovalOrder(rep.RemovableImages(kind)) {
		if enough != nil && enough() {
			break
		}
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
		return DiskCleanupResult{}, apperr.Conflict("dev_mode", "In dev mode bitploy does not delete images it did not make.", "Remove them with docker image prune.")
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
	reported, removed, runErr := s.diskStep(ctx, kind, in.Volumes, false, nil)
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
	stale := time.NewTicker(staleCacheEvery)
	defer stale.Stop()
	// The first old-cache pass runs soon after start, so a bitploy that
	// restarts often still gets one.
	firstStale := time.After(5 * time.Minute)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			start := s.Now()
			s.jobRan("disk_watch", start, nil, diskRunSummary(s.diskWatchOnce(ctx, triggerLowDisk)))
		case <-firstStale:
			s.staleCacheJob(ctx)
		case <-stale.C:
			s.staleCacheJob(ctx)
		case <-s.disk.kick:
			s.diskWatchOnce(ctx, triggerBuildRefused)
		}
	}
}

// diskRunSummary is a run on the Jobs tab: what it freed, or nothing for
// a check that found enough free space.
func diskRunSummary(run *DiskCleanupRun) string {
	if run == nil {
		return ""
	}
	return fmt.Sprintf("cleaned up: freed %s, %.0f%% free", bytesText(run.FreedBytes), run.FreeAfterPercent)
}

// diskWatchOnce runs the automatic cleanup when it is due; it returns what
// it recorded, or nil when nothing was recorded.
//
// Admins hear once that the disk stays low: a later run that frees nothing
// while it is still low is neither recorded nor announced, and one that
// frees something is recorded but not announced. Once free space is back
// above the threshold, the next low disk is news again.
func (s *Service) diskWatchOnce(ctx context.Context, trigger string) *DiskCleanupRun {
	if s.Cfg.Dev || s.Docker == nil {
		return nil
	}
	st, err := s.serverSettings(ctx, s.Q)
	if err != nil {
		return nil
	}
	set := diskSettingsOf(st)
	sp, err := s.diskSpace()
	if err != nil || sp.Total == 0 {
		return nil
	}
	if trigger == triggerLowDisk {
		s.diskWarnCheck(ctx, sp, set)
	}
	if !set.enabled {
		return nil
	}
	lowAt, buildsAt := float64(set.percent)/100, float64(set.builds)/100
	threshold, steps, w, alerted := lowAt, set.autoSteps(), &s.disk.low, &s.disk.lowAlerted
	if trigger == triggerBuildRefused {
		threshold, steps, w, alerted = buildsAt, set.safeSteps(), &s.disk.refused, &s.disk.refusedAlerted
	}
	now, free := s.Now(), sp.FreeFraction()
	s.disk.watchMu.Lock()
	s.seedDiskWatch(ctx)
	// Re-armed only diskuse.Margin above the level, so a disk hovering
	// around it does not count as a new episode every few minutes.
	if free >= lowAt+diskuse.Margin {
		s.disk.lowAlerted = false
	}
	if free >= buildsAt+diskuse.Margin {
		s.disk.refusedAlerted = false
	}
	due := w.Due(now, free, threshold)
	s.disk.watchMu.Unlock()
	if !due {
		return nil
	}
	if !s.disk.run.TryLock() {
		return nil // a cleanup by hand runs; look again next minute
	}
	defer s.disk.run.Unlock()

	s.Log.Warn("disk almost full, cleaning up", "trigger", trigger, "free_percent", round1(free*100), "threshold_percent", threshold*100)
	target := threshold + diskuse.Margin
	enough := func() bool {
		sp, err := s.diskSpace()
		return err == nil && sp.FreeFraction() >= target
	}
	runs := make([]diskuse.Step, 0, len(steps))
	for _, k := range steps {
		runs = append(runs, diskuse.Step{Kind: k, Run: func(ctx context.Context) (int64, int, error) {
			return s.diskStep(ctx, k, nil, true, enough)
		}})
	}
	// A Docker call that hangs must not hold the cleanup lock forever.
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	results, reached := diskuse.Escalate(runCtx, runs, s.diskSpace, target)
	after, err := s.diskSpace()
	if err != nil {
		after = sp
	}
	end := s.Now()

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
	s.disk.watchMu.Lock()
	w.Ran(end, after.FreeFraction())
	record, announce, nowAlerted := diskRunNews(result, freed, *alerted)
	*alerted = nowAlerted
	s.disk.watchMu.Unlock()
	s.forgetDiskReport()
	if len(results) == 0 {
		return nil // space came back before the first step
	}
	if !record {
		s.Log.Warn("disk still almost full; nothing left to clean up", "trigger", trigger, "free_percent", round1(after.FreeFraction()*100))
		return nil
	}

	run := s.recordDiskRun(ctx, System, trigger, now, end, sp, after, freed, results, result)
	if announce {
		s.notifyDiskCleanup(context.WithoutCancel(ctx), run, set)
	}
	return &run
}

// diskWarnCheck sends the early warning once when free space drops below
// the admin's warning level, so they can act before anything is deleted.
// Below the cleanup threshold the cleanup's own message says it instead.
// It is armed again once free space is back above the level plus
// diskuse.Margin.
func (s *Service) diskWarnCheck(ctx context.Context, sp diskuse.Space, set diskSettings) {
	free := sp.FreeFraction()
	s.disk.watchMu.Lock()
	if set.warn == 0 || free >= float64(set.warn)/100+diskuse.Margin {
		s.disk.warned = false
	}
	send := set.warn > 0 && !s.disk.warned && free < float64(set.warn)/100
	if send {
		s.disk.warned = true
	}
	s.disk.watchMu.Unlock()
	if !send || (set.enabled && free < float64(set.percent)/100) {
		return
	}
	m := notify.Message{
		Event: EventDiskLow, Time: s.Now(), Tag: "server-disk", URL: s.dashboardURL("/admin/disk"),
		Title: fmt.Sprintf("The server disk is filling up: %.1f%% free", free*100),
		Tone:  notify.ToneFailure, Severity: notify.Normal,
		Fields: []notify.Field{
			{Name: "Free", Value: bytesText(int64(sp.Free)) + " of " + bytesText(int64(sp.Total)), Inline: true},
			{Name: "Builds pause below", Value: fmt.Sprintf("%d%%", set.builds), Inline: true},
		},
		Hint: "Open the disk page to see what takes the space.",
	}
	if set.enabled {
		m.Fields = append(m.Fields, notify.Field{Name: "Automatic cleanup below", Value: fmt.Sprintf("%d%%", set.percent), Inline: true})
	}
	s.Log.Warn("disk getting full", "free_percent", round1(free*100), "warn_percent", set.warn)
	s.notifyAdmins(context.WithoutCancel(ctx), m, EventDiskLow)
}

// recordDiskRun stores a cleanup run (newest 50 are kept), audits it as
// actor and logs it.
func (s *Service) recordDiskRun(ctx context.Context, actor Actor, trigger string, start, end time.Time, before, after diskuse.Space,
	freed int64, results []diskuse.StepResult, result string) DiskCleanupRun {
	if results == nil {
		results = []diskuse.StepResult{}
	}
	stepsJSON, _ := json.Marshal(results)
	row := db.DiskCleanup{
		Trigger: trigger, StartedAt: start.Unix(), FinishedAt: end.Unix(), TotalBytes: int64(before.Total),
		FreeBefore: int64(before.Free), FreeAfter: int64(after.Free), FreedBytes: freed, Steps: string(stepsJSON), Result: result,
	}
	bg := context.WithoutCancel(ctx)
	if err := s.Q.InsertDiskCleanup(bg, db.InsertDiskCleanupParams{
		Trigger: row.Trigger, StartedAt: row.StartedAt, FinishedAt: row.FinishedAt, TotalBytes: row.TotalBytes,
		FreeBefore: row.FreeBefore, FreeAfter: row.FreeAfter, FreedBytes: row.FreedBytes, Steps: row.Steps, Result: row.Result,
	}); err != nil {
		s.Log.Error("recording the disk cleanup failed", "err", err)
	}
	_ = s.Q.DeleteOldDiskCleanups(bg)
	action := "server.disk_cleanup"
	if trigger == triggerManual {
		action = "admin.disk_auto_run"
	}
	s.Audit(bg, actor, action, "server", "disk", map[string]any{
		"trigger": trigger, "freed_bytes": freed, "result": result, "steps": results,
		"free_before_percent": round1(before.FreeFraction() * 100), "free_after_percent": round1(after.FreeFraction() * 100),
	})
	s.Log.Info("disk cleanup finished", "trigger", trigger, "result", result, "freed_bytes", freed, "free_percent", round1(after.FreeFraction()*100))
	return diskRunView(row)
}

// AdminRunDiskCleanup runs every step of the automatic cleanup the admin
// chose now, fully, whatever the free space (admin). It is recorded with
// the automatic runs (trigger manual) but announces nothing.
func (s *Service) AdminRunDiskCleanup(ctx context.Context, a Actor) (DiskCleanupRun, error) {
	if err := RequireAdmin(a); err != nil {
		return DiskCleanupRun{}, err
	}
	if s.Docker == nil {
		return DiskCleanupRun{}, apperr.Unavailable("docker_unavailable", "bitploy cannot reach Docker right now.", "Check that the Docker daemon runs.")
	}
	set := s.diskSettingsNow(ctx)
	steps := set.autoSteps()
	if s.Cfg.Dev {
		steps = slices.DeleteFunc(steps, func(k string) bool { return k == diskuse.OtherImages })
	}
	if !s.disk.run.TryLock() {
		return DiskCleanupRun{}, apperr.Conflict("disk_cleanup_running", "A disk cleanup is already running.", "Try again when it has finished.")
	}
	defer s.disk.run.Unlock()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Minute)
	defer cancel()

	start := s.Now()
	before, err := s.diskSpace()
	if err != nil {
		return DiskCleanupRun{}, apperr.Internal(err)
	}
	runs := make([]diskuse.Step, 0, len(steps))
	for _, k := range steps {
		runs = append(runs, diskuse.Step{Kind: k, Run: func(ctx context.Context) (int64, int, error) {
			return s.diskStep(ctx, k, nil, true, nil)
		}})
	}
	results, _ := diskuse.Escalate(ctx, runs, s.diskSpace, 2) // a target never reached: every step runs
	after, err := s.diskSpace()
	if err != nil {
		after = before
	}
	var freed int64
	failed := len(results) > 0
	for _, r := range results {
		freed += r.FreedBytes
		failed = failed && r.Error != ""
	}
	result := "still_low"
	switch {
	case failed:
		result = "failed"
	case after.FreeFraction() >= float64(set.percent)/100+diskuse.Margin:
		result = "recovered"
	}
	s.forgetDiskReport()
	return s.recordDiskRun(ctx, a, triggerManual, start, s.Now(), before, after, freed, results, result), nil
}

// diskRunNews decides what an automatic run is worth, given whether admins
// were already told the disk stays low (alerted): a recovery is always
// recorded and announced; the first run that leaves the disk low too; later
// ones that still leave it low are only recorded when they freed something,
// and never announced.
func diskRunNews(result string, freed int64, alerted bool) (record, announce, alertedAfter bool) {
	if result == "recovered" {
		return true, true, false
	}
	if !alerted {
		return true, true, true
	}
	return freed > 0, false, true
}

// staleCacheJob runs pruneStaleBuildCache and records it for the Jobs tab.
func (s *Service) staleCacheJob(ctx context.Context) {
	start := s.Now()
	freed, skipped, err := s.pruneStaleBuildCache(ctx)
	summary := skipped
	switch {
	case skipped != "":
	case freed > 0:
		summary = "freed " + bytesText(freed)
	default:
		summary = "nothing to delete"
	}
	s.jobRan("disk_stale_cache", start, err, summary)
}

// pruneStaleBuildCache deletes build cache that no build used in the
// admin's number of days (7 unless changed; 0 turns it off): the layers of
// an app that has not been built for that long. It returns what Docker and
// buildx said they freed, or why it did nothing. Nothing in dev mode.
func (s *Service) pruneStaleBuildCache(ctx context.Context) (freed int64, skipped string, err error) {
	if s.Cfg.Dev || s.Docker == nil {
		return 0, "off in dev mode", nil
	}
	days := s.diskSettingsNow(ctx).cacheDays
	if days == 0 {
		return 0, "off (0 days)", nil
	}
	if !s.disk.run.TryLock() {
		return 0, "skipped: another cleanup was running", nil // the next hour will do
	}
	defer s.disk.run.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	unused := time.Duration(days) * 24 * time.Hour
	freed, berr := s.buildBuilder().PruneUnused(ctx, unused)
	n, derr := s.Docker.PruneBuildCacheUnused(ctx, unused)
	freed += int64(n)
	if err = errors.Join(berr, derr); err != nil {
		s.Log.Warn("deleting old build cache failed", "err", err)
	}
	if freed > 0 {
		s.forgetDiskReport()
		s.Log.Info("deleted old build cache", "unused_days", days, "freed_bytes", freed)
	}
	return freed, "", err
}

// seedDiskWatch starts the watches from the last recorded run, so a restart
// of bitploy neither cleans up nor alerts again right away. Call with
// watchMu held.
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
		if seen[r.Trigger] || r.TotalBytes <= 0 || r.Trigger == triggerManual {
			continue
		}
		seen[r.Trigger] = true
		w, alerted := &s.disk.low, &s.disk.lowAlerted
		if r.Trigger == triggerBuildRefused {
			w, alerted = &s.disk.refused, &s.disk.refusedAlerted
		}
		w.Ran(ts(r.FinishedAt), float64(r.FreeAfter)/float64(r.TotalBytes))
		*alerted = r.Result != "recovered"
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
	diskuse.UnusedImages:      "unused images",
	diskuse.StoppedContainers: "leftover containers",
	diskuse.RollbackImages:    "rollback versions",
	diskuse.OtherImages:       "unused images not from bitploy",
}

// notifyDiskCleanup tells every admin who subscribed to server.disk_low
// what the automatic cleanup did.
func (s *Service) notifyDiskCleanup(ctx context.Context, run DiskCleanupRun, set diskSettings) {
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
		m.Hint = fmt.Sprintf("Open the disk page to see what takes the space. bitploy keeps trying and sends no further message until free space is back above %d%%.", set.percent)
		if run.Trigger == triggerBuildRefused {
			m.Hint = fmt.Sprintf("Builds, uploads and backups start again above %d%% free. Open the disk page to see what takes the space.", set.builds)
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
