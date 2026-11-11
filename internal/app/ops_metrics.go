package app

// Metrics queries, usage, server stats and the capacity guards.

import (
	"context"
	"strings"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/metrics"
	"github.com/AVMG20/bitploy/internal/store/db"
)

const mib = 1 << 20

func pointFromSample(p metrics.Point) *MetricPoint {
	return &MetricPoint{
		TS: p.TS.UTC(), CPU: round1(p.CPU), MemoryMB: round1(float64(p.MemBytes) / mib),
		RxBytes: p.RxBytes, TxBytes: p.TxBytes,
	}
}

func round1(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }

// GetMetrics returns CPU, memory and network for window 1h | 24h | 7d.
// 1h is raw 15-second samples ending at the live reading; 24h and 7d are 5-minute rollups (the newest
// raw samples are folded into 5-minute buckets so the series is uniform).
// An optional service name narrows an app with several services to one of
// them; without it the metrics are the sum over the app's services.
func (s *Service) GetMetrics(ctx context.Context, a Actor, ref, window string, service ...string) (Metrics, error) {
	app, err := s.LoadApp(ctx, a, ref, false)
	if err != nil {
		return Metrics{}, err
	}
	sp, _, err := s.pickService(ctx, app, firstOpt(service))
	if err != nil {
		return Metrics{}, err
	}
	svc, limit := "", app.MemoryMb
	if sp != nil && isMulti(app) { // a single-service app's one service is the app
		svc, limit = sp.Name, sp.MemoryMB
	}
	window = strings.ToLower(strings.TrimSpace(window))
	if window == "" {
		window = "24h"
	}
	if _, ok := metrics.Windows[window]; !ok {
		return Metrics{}, apperr.BadRequest("invalid_window",
			"The window "+quote(window)+" is not supported.", "Use 1h, 24h or 7d.")
	}
	series, err := s.metricsMgr().SeriesOf(ctx, app.ID, svc, window)
	if err != nil {
		return Metrics{}, apperr.Internal(err)
	}
	points := make([]MetricPoint, len(series))
	for i, p := range series {
		points[i] = MetricPoint{
			TS: p.TS, CPU: round1(p.CPU), MemoryMB: round1(float64(p.MemBytes) / mib),
			RxBytes: p.RxBytes, TxBytes: p.TxBytes,
			CPUMax: round1(p.CPUMax), MemMaxMB: round1(float64(p.MemMax) / mib),
		}
	}
	latest := s.LatestSample(app.ID)
	if svc != "" {
		latest = nil
		if p, ok := s.metricsMgr().LatestService(app.ID, svc); ok {
			latest = pointFromSample(p)
		}
	}
	// The hour chart ends at the live reading, so it moves between the
	// 15-second samples while someone watches it.
	if window == "1h" && latest != nil && (len(points) == 0 || latest.TS.After(points[len(points)-1].TS)) {
		points = append(points, *latest)
	}
	return Metrics{
		App: app.Slug, Service: svc, Window: window, MemoryLimitMB: limit, Points: points, Latest: latest,
	}, nil
}

// LatestSample returns the newest in-memory sample for an app (nil if none),
// used by app lists for CPU % and memory MB.
func (s *Service) LatestSample(appID string) *MetricPoint {
	if p, ok := s.metricsMgr().Latest(appID); ok {
		return pointFromSample(p)
	}
	return nil
}

// Usage is the per-user usage for whoami, GET /api/v1/usage and the Overview cards.
func (s *Service) Usage(ctx context.Context, userID string) (Usage, error) {
	q := s.Q
	now := s.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	tot, err := q.OpsUsageAppTotals(ctx, userID)
	if err != nil {
		return Usage{}, apperr.Internal(err)
	}
	u := Usage{
		Apps: tot.Apps, AppsRunning: tot.AppsRunning,
		MemoryReservedMB: tot.MemoryMb, CPUReservedMillis: tot.CpuMillis, VolumeReservedMB: tot.VolumeMb,
	}
	if u.DeploysMonth, err = q.OpsCountDeploymentsSince(ctx, db.OpsCountDeploymentsSinceParams{UserID: userID, QueuedAt: monthStart.Unix()}); err != nil {
		return Usage{}, apperr.Internal(err)
	}
	if u.DeploysWeek, err = q.OpsCountDeploymentsSince(ctx, db.OpsCountDeploymentsSinceParams{UserID: userID, QueuedAt: now.AddDate(0, 0, -7).Unix()}); err != nil {
		return Usage{}, apperr.Internal(err)
	}
	buildSecs, err := q.OpsBuildSecondsSince(ctx, db.OpsBuildSecondsSinceParams{UserID: userID, QueuedAt: monthStart.Unix()})
	if err != nil {
		return Usage{}, apperr.Internal(err)
	}
	u.BuildMinutesMonth = round1(float64(buildSecs) / 60)
	runSecs, err := q.OpsRunningSecondsSince(ctx, db.OpsRunningSecondsSinceParams{UserID: userID, Day: monthStart.Format("2006-01-02")})
	if err != nil {
		return Usage{}, apperr.Internal(err)
	}
	u.RunningHoursMonth = round1(float64(runSecs) / 3600)
	disk, err := q.OpsUserDiskBytes(ctx, userID)
	if err != nil {
		return Usage{}, apperr.Internal(err)
	}
	u.DiskUsedMB = round1(float64(disk) / mib)
	vol, err := q.OpsUserVolumeBytes(ctx, userID)
	if err != nil {
		return Usage{}, apperr.Internal(err)
	}
	u.VolumeUsedMB = round1(float64(vol) / mib)
	files, err := q.OpsUserFilesBytes(ctx, userID)
	if err != nil {
		return Usage{}, apperr.Internal(err)
	}
	u.FilesMB = round1(float64(files) / mib)
	backups, err := q.SumBackupBytesByUser(ctx, userID)
	if err != nil {
		return Usage{}, apperr.Internal(err)
	}
	u.BackupsMB = round1(float64(backups) / mib)

	apps, err := q.ListAppsByUser(ctx, userID)
	if err != nil {
		return Usage{}, apperr.Internal(err)
	}
	mgr := s.metricsMgr()
	var mem int64
	for _, ap := range apps {
		if p, ok := mgr.Latest(ap.ID); ok {
			mem += p.MemBytes
			u.CPUNowPercent += p.CPU
		}
	}
	u.MemoryUsedMB = round1(float64(mem) / mib)
	u.CPUNowPercent = round1(u.CPUNowPercent)
	return u, nil
}

// AdminServerStats: host CPU, RAM, disk, Docker disk use, build queue.
func (s *Service) AdminServerStats(ctx context.Context, a Actor) (ServerStats, error) {
	if err := RequireAdmin(a); err != nil {
		return ServerStats{}, err
	}
	const gb = 1 << 30
	st := ServerStats{Version: s.Version, DiskMinFreePercent: metrics.MinFreeDiskFraction * 100}
	if st.Version == "" {
		st.Version = "dev"
	}
	if host, err := metrics.ReadHost(ctx); err == nil {
		st.CPUPercent, st.Load1 = round1(host.CPUPercent), host.Load1
		st.MemTotalMB, st.MemUsedMB = int64(host.MemTotal/mib), int64(host.MemUsed/mib)
	}
	reserved, err := s.Q.OpsSumMemoryLimitsMB(ctx)
	if err != nil {
		return ServerStats{}, apperr.Internal(err)
	}
	st.MemReservedMB = reserved

	path := s.Cfg.DataDir
	if total, free, err := metrics.DiskUsage(path); err == nil {
		st.DiskTotalGB = round2(float64(total) / gb)
		st.DiskUsedGB = round2(float64(total-free) / gb)
	}
	if du, err := s.metricsMgr().DockerDisk(ctx); err == nil {
		st.DockerImagesGB = round2(float64(du.ImagesBytes) / gb)
		st.DockerCacheGB = round2(float64(du.BuildCacheBytes) / gb)
		st.DockerVolumesGB = round2(float64(du.VolumesBytes) / gb)
	} else {
		s.Log.Warn("docker disk usage failed", "err", err)
	}
	builds, err := s.Q.OpsBuildCounts(ctx)
	if err != nil {
		return ServerStats{}, apperr.Internal(err)
	}
	st.BuildsRunning, st.BuildsQueued = builds.Running, builds.Queued
	counts, err := s.Q.OpsAppCounts(ctx)
	if err != nil {
		return ServerStats{}, apperr.Internal(err)
	}
	st.AppsTotal, st.AppsRunning = counts.Total, counts.Running
	st.AppsMax = int64(s.Cfg.MaxApps)
	return st, nil
}

func round2(v float64) float64 { return float64(int64(v*100+0.5)) / 100 }

// AdminServerMetrics is the CPU and memory history of the whole server for
// window 1h | 24h | 7d, at the resolution of GetMetrics: raw 15-second
// samples for 1h, 5-minute buckets for 24h and 7d.
func (s *Service) AdminServerMetrics(ctx context.Context, a Actor, window string) (ServerMetrics, error) {
	if err := RequireAdmin(a); err != nil {
		return ServerMetrics{}, err
	}
	window = strings.ToLower(strings.TrimSpace(window))
	if window == "" {
		window = "24h"
	}
	if _, ok := metrics.Windows[window]; !ok {
		return ServerMetrics{}, apperr.BadRequest("invalid_window",
			"The window "+quote(window)+" is not supported.", "Use 1h, 24h or 7d.")
	}
	series, err := s.metricsMgr().HostSeries(ctx, window)
	if err != nil {
		return ServerMetrics{}, apperr.Internal(err)
	}
	out := ServerMetrics{Window: window, Points: make([]ServerMetricPoint, len(series))}
	for i, p := range series {
		out.Points[i] = ServerMetricPoint{
			TS: p.TS, CPU: round1(p.CPU), MemoryMB: round1(float64(p.MemUsed) / mib), MemoryTotalMB: round1(float64(p.MemTotal) / mib),
			CPUMax: round1(p.CPUMax), MemMaxMB: round1(float64(p.MemMax) / mib),
		}
	}
	if total, ok := metrics.HostMemTotal(); ok {
		out.MemTotalMB = int64(total / mib)
	} else if n := len(series); n > 0 {
		out.MemTotalMB = series[n-1].MemTotal / mib
	}
	return out, nil
}

// ServerHasRoom reports whether extraMB more of app memory limits fits under
// host RAM minus 1.5 GB. create_app/update_app fail with server_full otherwise.
// Where host RAM cannot be read (macOS dev) there is always room.
func (s *Service) ServerHasRoom(ctx context.Context, extraMB int64) (bool, error) {
	return s.metricsMgr().ServerHasRoom(ctx, extraMB)
}

// DiskHasRoom reports whether the disk has at least 15% free (else builds
// are refused). When it has not, the automatic disk cleanup is asked to
// delete build cache and unused images, so a later try can pass.
func (s *Service) DiskHasRoom() (bool, error) {
	var ok bool
	var err error
	if s.ops.diskHasRoom != nil {
		ok, err = s.ops.diskHasRoom()
	} else {
		ok, err = s.metricsMgr().DiskHasRoom()
	}
	if err == nil && !ok {
		s.kickDiskCleanup()
	}
	return ok, err
}
