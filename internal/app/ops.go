package app

// OWNER: ops agent. Custom domains, metrics, usage, server stats and
// background jobs (domain checker, metrics collector, Docker events,
// crash-loop detection, nightly prune/rollup/backup). Signatures are a
// shared contract; implement the bodies. Split into more files freely.

import (
	"context"

	"github.com/arno/bitploy/internal/apperr"
)

// ---- domains ----

func (s *Service) ListDomains(ctx context.Context, a Actor, ref string) ([]Domain, error) {
	return []Domain{}, nil
}

func (s *Service) AddDomain(ctx context.Context, a Actor, ref, hostname string) (Domain, error) {
	return Domain{}, apperr.NotImplemented("domains")
}

func (s *Service) CheckDomain(ctx context.Context, a Actor, ref, hostname string) (Domain, error) {
	return Domain{}, apperr.NotImplemented("domains")
}

func (s *Service) RemoveDomain(ctx context.Context, a Actor, ref, hostname string) error {
	return apperr.NotImplemented("domains")
}

// ActiveHostnames returns active custom hostnames of an app (used by SyncRoute).
func (s *Service) ActiveHostnames(ctx context.Context, appID string) ([]string, error) {
	return nil, nil
}

// ---- metrics & usage ----

// GetMetrics returns CPU, memory and network for window 1h | 24h | 7d.
func (s *Service) GetMetrics(ctx context.Context, a Actor, ref, window string) (Metrics, error) {
	return Metrics{}, apperr.NotImplemented("metrics")
}

// Usage is the per-user usage for whoami, GET /api/v1/usage and the Overview cards.
func (s *Service) Usage(ctx context.Context, userID string) (Usage, error) {
	return Usage{}, nil
}

// LatestSample returns the newest in-memory sample for an app (nil if none),
// used by app lists for CPU % and memory MB.
func (s *Service) LatestSample(appID string) *MetricPoint { return nil }

// AdminServerStats: host CPU, RAM, disk, Docker disk use, build queue.
func (s *Service) AdminServerStats(ctx context.Context, a Actor) (ServerStats, error) {
	return ServerStats{}, apperr.NotImplemented("server stats")
}

// ServerHasRoom reports whether extraMB more of app memory limits fits under
// host RAM minus 1.5 GB. create_app/update_app fail with server_full otherwise.
func (s *Service) ServerHasRoom(ctx context.Context, extraMB int64) (bool, error) {
	return true, nil
}

// DiskHasRoom reports whether the disk has at least 15% free (else builds are refused).
func (s *Service) DiskHasRoom() (bool, error) { return true, nil }

// ---- background ----

// RunBackground starts the domain checker, metrics collector, Docker events
// listener (die/oom/health_status, crash-loop detection) and nightly jobs.
// It returns when ctx ends.
func (s *Service) RunBackground(ctx context.Context) {}
