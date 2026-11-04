package dockerx

// Stats, container listing and housekeeping helpers used by the metrics
// collector. Owner: ops agent.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// Stats is one reading from a container's stats stream, reduced to what
// bitploy needs. CPU and network values are cumulative counters; callers
// compute deltas between two readings.
type Stats struct {
	Read         time.Time
	CPUTotal     uint64 // container CPU time in ns
	SystemCPU    uint64 // host CPU time in ns
	OnlineCPUs   uint32
	MemUsage     uint64 // includes page cache
	InactiveFile uint64 // reclaimable page cache inside MemUsage
	MemLimit     uint64
	RxBytes      uint64 // summed over all interfaces
	TxBytes      uint64
}

// MemWorkingSet is memory usage minus inactive file cache, which is what
// `docker stats` shows and what the OOM killer cares about.
func (s Stats) MemWorkingSet() uint64 {
	if s.InactiveFile < s.MemUsage {
		return s.MemUsage - s.InactiveFile
	}
	return s.MemUsage
}

// StatsFromResponse reduces a raw stats message.
func StatsFromResponse(r *container.StatsResponse) Stats {
	online := r.CPUStats.OnlineCPUs
	if online == 0 {
		online = uint32(len(r.CPUStats.CPUUsage.PercpuUsage))
	}
	s := Stats{
		Read:         r.Read,
		CPUTotal:     r.CPUStats.CPUUsage.TotalUsage,
		SystemCPU:    r.CPUStats.SystemUsage,
		OnlineCPUs:   online,
		MemUsage:     r.MemoryStats.Usage,
		InactiveFile: inactiveFile(r.MemoryStats.Stats),
		MemLimit:     r.MemoryStats.Limit,
	}
	for _, n := range r.Networks {
		s.RxBytes += n.RxBytes
		s.TxBytes += n.TxBytes
	}
	return s
}

// inactiveFile reads the page cache counter: cgroup v2 calls it
// "inactive_file", cgroup v1 "total_inactive_file".
func inactiveFile(m map[string]uint64) uint64 {
	if v, ok := m["inactive_file"]; ok {
		return v
	}
	return m["total_inactive_file"]
}

// StreamStats opens one streaming stats connection and calls fn for every
// reading (about one per second) until ctx ends, the container stops, or fn
// returns an error. A stopped container ends the stream with a nil error.
func (c *Client) StreamStats(ctx context.Context, id string, fn func(Stats) error) error {
	res, err := c.API.ContainerStats(ctx, id, client.ContainerStatsOptions{Stream: true})
	if err != nil {
		return err
	}
	defer res.Body.Close()
	dec := json.NewDecoder(res.Body)
	for {
		var r container.StatsResponse
		if err := dec.Decode(&r); err != nil {
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				return nil
			}
			return err
		}
		if err := fn(StatsFromResponse(&r)); err != nil {
			return err
		}
	}
}

// Managed is a container carrying the bitploy labels.
type Managed struct {
	ID      string
	Name    string // without leading slash
	AppID   string
	Slug    string
	Service string // nl.bitploy.service; "" on containers from before services
	State   string // running | exited | restarting | …
	Created time.Time
}

// ManagedContainers lists bitploy containers (label nl.bitploy.managed=1
// and an app id). With all=false only running ones are returned.
func (c *Client) ManagedContainers(ctx context.Context, all bool) ([]Managed, error) {
	f, err := c.ownedFilter()
	if err != nil {
		return nil, err
	}
	res, err := c.API.ContainerList(ctx, client.ContainerListOptions{All: all, Filters: f})
	if err != nil {
		return nil, err
	}
	var out []Managed
	for _, it := range res.Items {
		appID := it.Labels[LabelAppID]
		if appID == "" || !c.owned(it.Labels) {
			continue
		}
		name := ""
		if len(it.Names) > 0 {
			name = strings.TrimPrefix(it.Names[0], "/")
		}
		out = append(out, Managed{
			ID: it.ID, Name: name, AppID: appID, Slug: it.Labels[LabelSlug], Service: it.Labels[LabelService],
			State: string(it.State), Created: time.Unix(it.Created, 0).UTC(),
		})
	}
	return out, nil
}

// State is the part of `docker inspect` the event handlers need.
type State struct {
	Running      bool
	OOMKilled    bool
	ExitCode     int
	RestartCount int
	StartedAt    time.Time
	Health       string // "", starting, healthy, unhealthy
}

// InspectState returns the container's current state.
func (c *Client) InspectState(ctx context.Context, id string) (State, error) {
	res, err := c.API.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return State{}, err
	}
	var st State
	st.RestartCount = res.Container.RestartCount
	if s := res.Container.State; s != nil {
		st.Running, st.OOMKilled, st.ExitCode = s.Running, s.OOMKilled, s.ExitCode
		st.StartedAt, _ = time.Parse(time.RFC3339Nano, s.StartedAt)
		if s.Health != nil {
			st.Health = string(s.Health.Status)
		}
	}
	return st, nil
}

// DiskImage and DiskVolume carry the sizes the nightly job records. AppID
// comes from the nl.bitploy.app_id label (empty for foreign objects).
type DiskImage struct {
	AppID string
	Tags  []string
	Size  int64
	// SharedSize is the part of Size in layers other images use too; -1 when
	// Docker did not compute it.
	SharedSize int64
}

type DiskVolume struct {
	AppID string
	Name  string
	Size  int64
}

// DiskUsage is Docker's view of its own disk use (`docker system df`).
type DiskUsage struct {
	ImagesBytes     int64
	BuildCacheBytes int64
	VolumesBytes    int64
	Images          []DiskImage  // only with verbose
	Volumes         []DiskVolume // only with verbose
}

// DiskUsage asks the daemon for image, build cache and volume sizes.
// verbose also returns per-image and per-volume sizes (slow on big volumes).
func (c *Client) DiskUsage(ctx context.Context, verbose bool) (DiskUsage, error) {
	res, err := c.API.DiskUsage(ctx, client.DiskUsageOptions{
		Images: true, BuildCache: true, Volumes: true, Verbose: verbose,
	})
	if err != nil {
		return DiskUsage{}, err
	}
	out := DiskUsage{
		ImagesBytes:     res.Images.TotalSize,
		BuildCacheBytes: res.BuildCache.TotalSize,
		VolumesBytes:    res.Volumes.TotalSize,
	}
	for _, im := range res.Images.Items {
		out.Images = append(out.Images, DiskImage{AppID: im.Labels[LabelAppID], Tags: im.RepoTags, Size: im.Size, SharedSize: im.SharedSize})
	}
	for _, v := range res.Volumes.Items {
		if v.UsageData != nil && v.UsageData.Size >= 0 {
			out.Volumes = append(out.Volumes, DiskVolume{AppID: v.Labels[LabelAppID], Name: v.Name, Size: v.UsageData.Size})
		}
	}
	return out, nil
}

// PruneDanglingImages removes untagged images that bitploy built (label
// nl.bitploy.managed=1). Other images on the host are never touched.
func (c *Client) PruneDanglingImages(ctx context.Context) (uint64, error) {
	f, err := c.ownedFilter()
	if err != nil {
		return 0, err
	}
	res, err := c.API.ImagePrune(ctx, client.ImagePruneOptions{Filters: f.Add("dangling", "true")})
	if err != nil {
		return 0, err
	}
	return res.Report.SpaceReclaimed, nil
}

// PruneBuildCache trims the BuildKit cache down to keepBytes
// (`docker builder prune --keep-storage`).
func (c *Client) PruneBuildCache(ctx context.Context, keepBytes int64) (uint64, error) {
	res, err := c.API.BuildCachePrune(ctx, client.BuildCachePruneOptions{All: true, ReservedSpace: keepBytes})
	if err != nil {
		return 0, err
	}
	return res.Report.SpaceReclaimed, nil
}
