package app

// OWNER: ops agent. Shared state and background wiring for custom domains
// (ops_domains.go) and metrics, usage and server stats (ops_metrics.go).

import (
	"context"
	"sync"
	"time"

	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/domains"
	"github.com/AVMG20/bitploy/internal/metrics"
)

var _ metrics.Docker = (*dockerx.Client)(nil)

// opsState is the zero-value-usable state the ops code keeps on Service.
type opsState struct {
	once sync.Once
	mgr  *metrics.Manager

	// Tests replace these; nil means the real thing.
	resolvers   []domains.Named
	syncRoute   func(ctx context.Context, appID string) error
	diskHasRoom func() (bool, error)
}

// metricsMgr returns the metrics manager, creating it on first use.
func (s *Service) metricsMgr() *metrics.Manager {
	s.ops.once.Do(func() {
		var dk metrics.Docker // stays a nil interface when Docker is unavailable
		if s.Docker != nil {
			dk = s.Docker
		}
		m := metrics.New(s.Store, dk, s.Log)
		if s.Now != nil {
			m.Now = s.Now
		}
		m.DataDir = s.Cfg.DataDir
		m.Dev = s.Cfg.Dev
		m.Audit = func(ctx context.Context, action, targetType, targetID string, details map[string]any) {
			s.Audit(ctx, System, action, targetType, targetID, details)
		}
		m.SyncRoute = s.opsSyncRoute
		m.BuildxPrune = s.buildBuilder().Prune
		s.ops.mgr = m
	})
	return s.ops.mgr
}

func (s *Service) opsSyncRoute(ctx context.Context, appID string) error {
	if s.ops.syncRoute != nil {
		return s.ops.syncRoute(ctx, appID)
	}
	return s.SyncRoute(ctx, appID)
}

// ---- background ----

// RunBackground starts the domain checker, the automatic /data backups, metrics
// collector, Docker events listener (die/oom/health_status, crash-loop
// detection) and nightly jobs.
// It returns when ctx ends. It works without Docker (s.Docker == nil): then
// only the domain checker and the nightly database jobs run.
func (s *Service) RunBackground(ctx context.Context) {
	// Backups and restores started from the API or the schedule stop with ctx;
	// they thaw their app before they end, and RunBackground waits for that.
	s.backup.mu.Lock()
	s.backup.base = ctx
	s.backup.mu.Unlock()
	defer s.backup.wg.Wait()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.runDomainChecker(ctx)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.runBackups(ctx)
	}()
	s.metricsMgr().Run(ctx)
	wg.Wait()
}

// domainCheckEvery is how often pending domains are re-checked.
const domainCheckEvery = 5 * time.Minute
