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
// 1h is raw 15-second samples; 24h and 7d are 5-minute rollups (the newest
// raw samples are folded into 5-minute buckets so the series is uniform).
func (s *Service) GetMetrics(ctx context.Context, a Actor, ref, window string) (Metrics, error) {
	app, err := s.LoadApp(ctx, a, ref, false)
	if err != nil {
		return Metrics{}, err
	}
	window = strings.ToLower(strings.TrimSpace(window))
	if window == "" {
		window = "24h"
	}
	if _, ok := metrics.Windows[window]; !ok {
		return Metrics{}, apperr.BadRequest("invalid_window",
			"The window "+quote(window)+" is not supported.", "Use 1h, 24h or 7d.")
	}
	series, err := s.metricsMgr().Series(ctx, app.ID, window)
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
	return Metrics{
		App: app.Slug, Window: window, MemoryLimitMB: app.MemoryMb, Points: points, Latest: s.LatestSample(app.ID),
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
		MemoryReservedMB: tot.MemoryMb, CPUReservedMillis: tot.CpuMillis,
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
	var st ServerStats
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
	return st, nil
}

func round2(v float64) float64 { return float64(int64(v*100+0.5)) / 100 }

// ServerHasRoom reports whether extraMB more of app memory limits fits under
// host RAM minus 1.5 GB. create_app/update_app fail with server_full otherwise.
// Where host RAM cannot be read (macOS dev) there is always room.
func (s *Service) ServerHasRoom(ctx context.Context, extraMB int64) (bool, error) {
	return s.metricsMgr().ServerHasRoom(ctx, extraMB)
}

// DiskHasRoom reports whether the disk has at least 15% free (else builds are refused).
func (s *Service) DiskHasRoom() (bool, error) { return s.metricsMgr().DiskHasRoom() }
