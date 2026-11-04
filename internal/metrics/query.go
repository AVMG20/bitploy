package metrics

import (
	"context"
	"fmt"
	"time"

	"github.com/AVMG20/bitploy/internal/store/db"
)

// SeriesPoint is one chart point. Max values are zero for raw samples.
type SeriesPoint struct {
	TS       time.Time
	CPU      float64
	MemBytes int64
	RxBytes  int64
	TxBytes  int64
	CPUMax   float64
	MemMax   int64
}

// Windows are the chart ranges the API accepts.
var Windows = map[string]time.Duration{
	"1h":  time.Hour,
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
}

// Series returns the chart points of an app for window 1h | 24h | 7d.
//
//   - 1h: raw 15-second samples.
//   - 24h, 7d: 5-minute rollups, plus the newest raw samples that are not
//     rolled up yet, grouped into 5-minute buckets the same way, so the
//     series has one resolution.
func (m *Manager) Series(ctx context.Context, appID, window string) ([]SeriesPoint, error) {
	return m.SeriesOf(ctx, appID, "", window)
}

// SeriesOf is Series for one service of the app; service "" is the whole
// app, the sum over its services.
func (m *Manager) SeriesOf(ctx context.Context, appID, service, window string) ([]SeriesPoint, error) {
	dur, ok := Windows[window]
	if !ok {
		return nil, fmt.Errorf("unknown window %q", window)
	}
	q := m.Store.Q
	since := m.now().Add(-dur).Unix()
	out := []SeriesPoint{}

	if window == "1h" {
		rows, err := q.OpsListSamples(ctx, db.OpsListSamplesParams{AppID: appID, Ts: since, Service: service})
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			out = append(out, SeriesPoint{
				TS: time.Unix(r.Ts, 0).UTC(), CPU: r.Cpu, MemBytes: r.MemBytes, RxBytes: r.RxBytes, TxBytes: r.TxBytes,
			})
		}
		return out, nil
	}

	rolls, err := q.OpsListRollups(ctx, db.OpsListRollupsParams{AppID: appID, Ts: BucketStart(since), Service: service})
	if err != nil {
		return nil, err
	}
	tailFrom := since
	for _, r := range rolls {
		out = append(out, SeriesPoint{
			TS: time.Unix(r.Ts, 0).UTC(), CPU: r.Cpu, MemBytes: r.MemBytes, RxBytes: r.RxBytes, TxBytes: r.TxBytes,
			CPUMax: r.CpuMax, MemMax: r.MemMax,
		})
		if next := r.Ts + BucketSeconds; next > tailFrom {
			tailFrom = next
		}
	}
	raw, err := q.OpsListSamples(ctx, db.OpsListSamplesParams{AppID: appID, Ts: tailFrom, Service: service})
	if err != nil {
		return nil, err
	}
	samples := make([]Sample, len(raw))
	for i, r := range raw {
		samples[i] = Sample{TS: r.Ts, CPU: r.Cpu, Mem: r.MemBytes, Rx: r.RxBytes, Tx: r.TxBytes}
	}
	for _, r := range Rollup(samples) {
		out = append(out, SeriesPoint{
			TS: time.Unix(r.TS, 0).UTC(), CPU: r.CPU, MemBytes: r.Mem, RxBytes: r.Rx, TxBytes: r.Tx,
			CPUMax: r.CPUMax, MemMax: r.MemMax,
		})
	}
	return out, nil
}
