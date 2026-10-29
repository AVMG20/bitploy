package metrics

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/AVMG20/bitploy/internal/dockerx"
)

// reservedForSystemBytes is the RAM kept free for Docker, Traefik and
// bitploy: the sum of all app memory limits must stay under host RAM minus this.
const reservedForSystemBytes = 1536 << 20

// dfCache caches `docker system df`, which is slow on big hosts.
type dfCache struct {
	mu   sync.Mutex
	at   time.Time
	data dockerx.DiskUsage
}

// DockerDisk returns Docker's disk use, cached for a minute.
func (m *Manager) DockerDisk(ctx context.Context) (dockerx.DiskUsage, error) {
	if m.Docker == nil {
		return dockerx.DiskUsage{}, nil
	}
	c := &m.dfCache
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && time.Since(c.at) < time.Minute {
		return c.data, nil
	}
	du, err := m.Docker.DiskUsage(ctx, false)
	if err != nil {
		return dockerx.DiskUsage{}, err
	}
	c.at, c.data = time.Now(), du
	return du, nil
}

// HasRoom reports whether extraMB more of app memory limits, on top of
// reservedMB already promised, fits in host RAM minus 1.5 GB. Without
// readable host stats (macOS dev) there is always room.
func HasRoom(hostTotalBytes uint64, known bool, reservedMB, extraMB int64) bool {
	if !known {
		return true
	}
	budget := int64(hostTotalBytes) - reservedForSystemBytes
	return (reservedMB+extraMB)<<20 <= budget
}

// ServerHasRoom checks the memory guard against the apps table.
func (m *Manager) ServerHasRoom(ctx context.Context, extraMB int64) (bool, error) {
	total, ok := HostMemTotal()
	if !ok {
		return true, nil
	}
	reserved, err := m.Store.Q.OpsSumMemoryLimitsMB(ctx)
	if err != nil {
		return false, err
	}
	return HasRoom(total, true, reserved, extraMB), nil
}

// MinFreeDiskFraction: builds are refused below 15% free disk.
const MinFreeDiskFraction = 0.15

// DiskHasRoom reports whether the data directory's filesystem has at least
// 15% free.
func (m *Manager) DiskHasRoom() (bool, error) {
	path := m.DataDir
	if _, err := os.Stat(path); err != nil {
		path = "/"
	}
	total, free, err := DiskUsage(path)
	if err != nil || total == 0 {
		return true, err
	}
	return EnoughFree(total, free), nil
}

// EnoughFree is the 15% rule.
func EnoughFree(total, free uint64) bool {
	return float64(free)/float64(total) >= MinFreeDiskFraction
}
