package metrics

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/store"
)

// Docker is the part of the Docker client the metrics code uses. It is an
// interface so tests can fake it; *dockerx.Client implements it.
type Docker interface {
	ManagedContainers(ctx context.Context, all bool) ([]dockerx.Managed, error)
	StreamStats(ctx context.Context, id string, fn func(dockerx.Stats) error) error
	WatchEvents(ctx context.Context, since time.Time, fn func(dockerx.Event)) error
	InspectState(ctx context.Context, id string) (dockerx.State, error)
	StopContainerGrace(ctx context.Context, name string, timeoutSec int) error
	TailLogs(ctx context.Context, name string, n int, since time.Time) ([]dockerx.LogEntry, error)
	DiskUsage(ctx context.Context, verbose bool) (dockerx.DiskUsage, error)
	PruneDanglingImages(ctx context.Context) (uint64, error)
	PruneBuildCache(ctx context.Context, keepBytes int64) (uint64, error)
}

// Manager owns the collector, the Docker event handler and the scheduled
// jobs. Create it with New and run it with Run.
type Manager struct {
	Store   *store.Store
	Docker  Docker // nil when Docker is unavailable
	Log     *slog.Logger
	Now     func() time.Time
	DataDir string
	Dev     bool // local development: never prune Docker caches

	// Audit writes an audit row as the system actor.
	Audit func(ctx context.Context, action, targetType, targetID string, details map[string]any)
	// SyncRoute rewrites an app's Traefik route (deploy agent). May be nil.
	SyncRoute func(ctx context.Context, appID string) error

	// SampleInterval is how often the collector writes (15 s).
	SampleInterval time.Duration
	// BuildCacheKeepBytes caps the BuildKit cache after the nightly prune.
	BuildCacheKeepBytes int64

	mu      sync.RWMutex
	latest  map[string]Point // newest sample per running app
	tracked map[string]*tracked
	appUser map[string]string
	dfCache dfCache

	crashes *crashTracker
	oomAt   map[string]time.Time
	oomMu   sync.Mutex
}

// New returns a Manager with defaults filled in.
func New(st *store.Store, dk Docker, log *slog.Logger) *Manager {
	return &Manager{
		Store: st, Docker: dk, Log: log,
		Now:                 func() time.Time { return time.Now().UTC() },
		SampleInterval:      15 * time.Second,
		BuildCacheKeepBytes: 10 << 30,
		latest:              map[string]Point{},
		tracked:             map[string]*tracked{},
		appUser:             map[string]string{},
		crashes:             newCrashTracker(5, 5*time.Minute),
		oomAt:               map[string]time.Time{},
	}
}

func (m *Manager) now() time.Time { return m.Now() }

func (m *Manager) audit(ctx context.Context, action, targetType, targetID string, details map[string]any) {
	if m.Audit != nil {
		m.Audit(ctx, action, targetType, targetID, details)
	}
}

// Run starts every background job and returns when ctx ends.
func (m *Manager) Run(ctx context.Context) {
	var wg sync.WaitGroup
	start := func(name string, fn func(context.Context)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					m.Log.Error("metrics job panicked", "job", name, "panic", r)
				}
			}()
			fn(ctx)
		}()
	}
	start("collector", m.runCollector)
	start("events", m.runEvents)
	start("schedule", m.runSchedule)
	wg.Wait()
}
