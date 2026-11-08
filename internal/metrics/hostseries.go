package metrics

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/AVMG20/bitploy/internal/store/db"
)

// HostSample is one stored reading of the whole server.
type HostSample struct {
	TS       int64
	CPU      float64 // % of the whole server (all cores), 0-100
	MemUsed  int64   // bytes, excluding reclaimable cache
	MemTotal int64   // bytes
}

// HostPoint is one chart point of the whole server. The max values are zero
// for raw samples.
type HostPoint struct {
	TS       time.Time
	CPU      float64
	MemUsed  int64
	MemTotal int64
	CPUMax   float64
	MemMax   int64
}

// hostState is what the host collector keeps between two ticks.
type hostState struct {
	mu sync.Mutex
	// read returns the CPU counters and memory now; nil is the /proc reader
	// (readHostCounters). Tests replace it.
	read func() (CPUTimes, MemInfo, error)
	prev CPUTimes
	have bool
}

// runHostCollector stores a sample of the server's CPU and memory on the
// interval of the app samples. Off Linux there is nothing to read.
func (m *Manager) runHostCollector(ctx context.Context) {
	if m.host.read == nil && !HostSupported {
		return
	}
	if err := m.HostTick(ctx, m.now()); err != nil { // the first reading is the base
		m.Log.Warn("reading host stats failed", "err", err)
	}
	tick := time.NewTicker(m.SampleInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := m.HostTick(ctx, m.now()); err != nil {
				m.Log.Warn("writing host sample failed", "err", err)
			}
		}
	}
}

// HostTick reads the server's CPU counters and memory and stores one sample.
// CPU is the busy share of all cores since the previous tick, so a sample
// covers the whole interval; the first tick only sets the base.
func (m *Manager) HostTick(ctx context.Context, now time.Time) error {
	read := m.host.read
	if read == nil {
		read = readHostCounters
	}
	cpu, mem, err := read()
	if err != nil {
		return err
	}
	m.host.mu.Lock()
	prev, have := m.host.prev, m.host.have
	m.host.prev, m.host.have = cpu, true
	m.host.mu.Unlock()
	if !have {
		return nil
	}
	var used uint64
	if mem.Available < mem.Total {
		used = mem.Total - mem.Available
	}
	return m.Store.Q.OpsInsertHostSample(ctx, db.OpsInsertHostSampleParams{
		Ts: now.Unix(), Cpu: CPUBusyPercent(prev, cpu), MemUsedBytes: int64(used), MemTotalBytes: int64(mem.Total),
	})
}

// RollupHost groups host samples into 5-minute buckets like Rollup: averages
// with maxima for CPU and memory used. The total is the highest in the bucket.
func RollupHost(samples []HostSample) []HostPoint {
	base := make([]Sample, len(samples))
	totals := map[int64]int64{}
	for i, s := range samples {
		base[i] = Sample{TS: s.TS, CPU: s.CPU, Mem: s.MemUsed}
		if b := BucketStart(s.TS); s.MemTotal > totals[b] {
			totals[b] = s.MemTotal
		}
	}
	rolled := Rollup(base)
	out := make([]HostPoint, len(rolled))
	for i, r := range rolled {
		out[i] = HostPoint{
			TS: time.Unix(r.TS, 0).UTC(), CPU: r.CPU, MemUsed: r.Mem, MemTotal: totals[r.TS], CPUMax: r.CPUMax, MemMax: r.MemMax,
		}
	}
	return out
}

func hostSamples(rows []db.HostSample) []HostSample {
	out := make([]HostSample, len(rows))
	for i, r := range rows {
		out[i] = HostSample{TS: r.Ts, CPU: r.Cpu, MemUsed: r.MemUsedBytes, MemTotal: r.MemTotalBytes}
	}
	return out
}

// rollupHost writes the complete 5-minute buckets of host samples that are
// not rolled up yet, the same way Rollup does for the apps.
func (m *Manager) rollupHost(ctx context.Context) error {
	q := m.Store.Q
	end := BucketStart(m.now().Unix())
	last, err := q.OpsLastHostRollupTS(ctx)
	if err != nil {
		return err
	}
	from := int64(0)
	if last > 0 {
		from = last + BucketSeconds
	}
	if from >= end {
		return nil
	}
	rows, err := q.OpsListHostSamplesBefore(ctx, db.OpsListHostSamplesBeforeParams{Ts: from, Ts_2: end})
	if err != nil {
		return err
	}
	rolled := RollupHost(hostSamples(rows))
	if len(rolled) == 0 {
		return nil
	}
	return m.Store.Tx(ctx, func(q *db.Queries) error {
		for _, r := range rolled {
			if err := q.OpsUpsertHostRollup(ctx, db.OpsUpsertHostRollupParams{
				Ts: r.TS.Unix(), Cpu: r.CPU, MemUsedBytes: r.MemUsed, MemTotalBytes: r.MemTotal, CpuMax: r.CPUMax, MemMax: r.MemMax,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// HostSeries returns the chart points of the whole server for window
// 1h | 24h | 7d, with the same resolution as Series: raw 15-second samples
// for 1h; 5-minute rollups plus the newest raw samples folded into 5-minute
// buckets for 24h and 7d.
func (m *Manager) HostSeries(ctx context.Context, window string) ([]HostPoint, error) {
	dur, ok := Windows[window]
	if !ok {
		return nil, fmt.Errorf("unknown window %q", window)
	}
	q := m.Store.Q
	since := m.now().Add(-dur).Unix()
	out := []HostPoint{}

	if window == "1h" {
		rows, err := q.OpsListHostSamples(ctx, since)
		if err != nil {
			return nil, err
		}
		for _, s := range hostSamples(rows) {
			out = append(out, HostPoint{TS: time.Unix(s.TS, 0).UTC(), CPU: s.CPU, MemUsed: s.MemUsed, MemTotal: s.MemTotal})
		}
		return out, nil
	}

	rolls, err := q.OpsListHostRollups(ctx, BucketStart(since))
	if err != nil {
		return nil, err
	}
	tailFrom := since
	for _, r := range rolls {
		out = append(out, HostPoint{
			TS: time.Unix(r.Ts, 0).UTC(), CPU: r.Cpu, MemUsed: r.MemUsedBytes, MemTotal: r.MemTotalBytes, CPUMax: r.CpuMax, MemMax: r.MemMax,
		})
		if next := r.Ts + BucketSeconds; next > tailFrom {
			tailFrom = next
		}
	}
	raw, err := q.OpsListHostSamples(ctx, tailFrom)
	if err != nil {
		return nil, err
	}
	return append(out, RollupHost(hostSamples(raw))...), nil
}
