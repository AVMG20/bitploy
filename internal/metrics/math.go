// Package metrics samples running containers, stores the samples, rolls them
// up and runs bitploy's nightly housekeeping. It knows nothing about the
// service layer: internal/app wires it in through Manager's hooks.
package metrics

import (
	"sort"

	"github.com/arno/bitploy/internal/dockerx"
)

// BucketSeconds is the rollup resolution: 5 minutes.
const BucketSeconds = 300

// CPUPercent is the CPU use between two readings as a percentage of ONE
// core: container CPU delta / host CPU delta x online CPUs x 100. A container
// that keeps two cores busy shows 200.
func CPUPercent(prev, cur dockerx.Stats) float64 {
	if cur.CPUTotal < prev.CPUTotal || cur.SystemCPU <= prev.SystemCPU {
		return 0
	}
	cpuDelta := float64(cur.CPUTotal - prev.CPUTotal)
	sysDelta := float64(cur.SystemCPU - prev.SystemCPU)
	online := float64(cur.OnlineCPUs)
	if online == 0 {
		online = 1
	}
	return cpuDelta / sysDelta * online * 100
}

// CounterDelta is the growth of a cumulative counter. A counter that went
// down (the container restarted) restarts from zero.
func CounterDelta(prev, cur uint64) uint64 {
	if cur < prev {
		return cur
	}
	return cur - prev
}

// Sample is one stored raw measurement. Rx and Tx are bytes during the interval.
type Sample struct {
	TS  int64
	CPU float64
	Mem int64
	Rx  int64
	Tx  int64
}

// Rolled is one 5-minute bucket: averages for CPU and memory, sums for
// network bytes, plus the highest CPU and memory seen.
type Rolled struct {
	TS     int64 // start of the bucket
	CPU    float64
	Mem    int64
	Rx     int64
	Tx     int64
	CPUMax float64
	MemMax int64
}

// BucketStart rounds a unix timestamp down to its 5-minute bucket.
func BucketStart(ts int64) int64 { return ts - ((ts%BucketSeconds)+BucketSeconds)%BucketSeconds }

// Rollup groups samples into 5-minute buckets, oldest first.
func Rollup(samples []Sample) []Rolled {
	type acc struct {
		r Rolled
		n int64
		m float64 // memory sum as float to avoid overflow on huge hosts
	}
	byBucket := map[int64]*acc{}
	for _, s := range samples {
		b := BucketStart(s.TS)
		a := byBucket[b]
		if a == nil {
			a = &acc{r: Rolled{TS: b}}
			byBucket[b] = a
		}
		a.n++
		a.r.CPU += s.CPU
		a.m += float64(s.Mem)
		a.r.Rx += s.Rx
		a.r.Tx += s.Tx
		if s.CPU > a.r.CPUMax {
			a.r.CPUMax = s.CPU
		}
		if s.Mem > a.r.MemMax {
			a.r.MemMax = s.Mem
		}
	}
	out := make([]Rolled, 0, len(byBucket))
	for _, a := range byBucket {
		a.r.CPU /= float64(a.n)
		a.r.Mem = int64(a.m/float64(a.n) + 0.5)
		out = append(out, a.r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TS < out[j].TS })
	return out
}
