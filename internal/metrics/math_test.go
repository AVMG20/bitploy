package metrics

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/dockerx"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestCPUPercent(t *testing.T) {
	tests := []struct {
		name      string
		prev, cur dockerx.Stats
		want      float64
	}{
		{
			name: "half a core on a 4 core host",
			prev: dockerx.Stats{CPUTotal: 1_000_000_000, SystemCPU: 100_000_000_000, OnlineCPUs: 4},
			cur:  dockerx.Stats{CPUTotal: 1_500_000_000, SystemCPU: 104_000_000_000, OnlineCPUs: 4},
			// 0.5 s of CPU while the host had 4 s of CPU time (1 s x 4 cores):
			// 12.5% of the host, which is 50% of one core.
			want: 50,
		},
		{
			name: "two full cores on 4 cores",
			prev: dockerx.Stats{CPUTotal: 0, SystemCPU: 0, OnlineCPUs: 4},
			cur:  dockerx.Stats{CPUTotal: 2_000_000_000, SystemCPU: 4_000_000_000, OnlineCPUs: 4},
			want: 200,
		},
		{
			name: "one core fully busy on a single core host",
			prev: dockerx.Stats{CPUTotal: 10, SystemCPU: 100, OnlineCPUs: 1},
			cur:  dockerx.Stats{CPUTotal: 110, SystemCPU: 200, OnlineCPUs: 1},
			want: 100,
		},
		{
			name: "missing online CPU count counts as one",
			prev: dockerx.Stats{CPUTotal: 0, SystemCPU: 0},
			cur:  dockerx.Stats{CPUTotal: 50, SystemCPU: 100},
			want: 50,
		},
		{
			name: "idle",
			prev: dockerx.Stats{CPUTotal: 5, SystemCPU: 100, OnlineCPUs: 2},
			cur:  dockerx.Stats{CPUTotal: 5, SystemCPU: 300, OnlineCPUs: 2},
			want: 0,
		},
		{
			name: "no system delta",
			prev: dockerx.Stats{CPUTotal: 5, SystemCPU: 100, OnlineCPUs: 2},
			cur:  dockerx.Stats{CPUTotal: 9, SystemCPU: 100, OnlineCPUs: 2},
			want: 0,
		},
		{
			name: "counter reset after a container restart",
			prev: dockerx.Stats{CPUTotal: 900, SystemCPU: 100, OnlineCPUs: 2},
			cur:  dockerx.Stats{CPUTotal: 10, SystemCPU: 300, OnlineCPUs: 2},
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CPUPercent(tt.prev, tt.cur); !near(got, tt.want) {
				t.Errorf("CPUPercent = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCounterDelta(t *testing.T) {
	for _, tt := range []struct{ prev, cur, want uint64 }{
		{100, 150, 50}, {100, 100, 0}, {500, 20, 20}, {0, 0, 0},
	} {
		if got := CounterDelta(tt.prev, tt.cur); got != tt.want {
			t.Errorf("CounterDelta(%d, %d) = %d, want %d", tt.prev, tt.cur, got, tt.want)
		}
	}
}

func TestBucketStart(t *testing.T) {
	for _, tt := range []struct{ in, want int64 }{
		{0, 0}, {299, 0}, {300, 300}, {1_700_000_123, 1_700_000_100}, {1_700_000_100, 1_700_000_100},
	} {
		if got := BucketStart(tt.in); got != tt.want {
			t.Errorf("BucketStart(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestRollup(t *testing.T) {
	base := int64(1_700_000_100) // a bucket boundary
	samples := []Sample{
		// bucket 1: 3 samples
		{TS: base + 10, CPU: 10, Mem: 100, Rx: 1, Tx: 2},
		{TS: base + 25, CPU: 30, Mem: 300, Rx: 10, Tx: 20},
		{TS: base + 299, CPU: 20, Mem: 200, Rx: 100, Tx: 200},
		// bucket 2: 1 sample
		{TS: base + 300, CPU: 55.5, Mem: 1000, Rx: 7, Tx: 8},
		// bucket 4 (bucket 3 empty)
		{TS: base + 905, CPU: 1, Mem: 3, Rx: 0, Tx: 0},
		{TS: base + 910, CPU: 2, Mem: 4, Rx: 0, Tx: 0},
	}
	got := Rollup(samples)
	want := []Rolled{
		{TS: base, CPU: 20, Mem: 200, Rx: 111, Tx: 222, CPUMax: 30, MemMax: 300},
		{TS: base + 300, CPU: 55.5, Mem: 1000, Rx: 7, Tx: 8, CPUMax: 55.5, MemMax: 1000},
		{TS: base + 900, CPU: 1.5, Mem: 4, Rx: 0, Tx: 0, CPUMax: 2, MemMax: 4}, // 3.5 rounds to 4
	}
	if len(got) != len(want) {
		t.Fatalf("got %d buckets, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.TS != w.TS || !near(g.CPU, w.CPU) || g.Mem != w.Mem || g.Rx != w.Rx || g.Tx != w.Tx || !near(g.CPUMax, w.CPUMax) || g.MemMax != w.MemMax {
			t.Errorf("bucket %d = %+v, want %+v", i, g, w)
		}
	}
	if len(Rollup(nil)) != 0 {
		t.Error("empty input should give no buckets")
	}
	// Order of the input must not matter.
	rev := make([]Sample, len(samples))
	for i, s := range samples {
		rev[len(samples)-1-i] = s
	}
	if g := Rollup(rev); len(g) != 3 || g[0].TS != base || g[2].TS != base+900 {
		t.Errorf("unordered input gave %+v", g)
	}
}

func TestParseMemInfo(t *testing.T) {
	in := "MemTotal:       16384000 kB\nMemFree:         1000000 kB\nMemAvailable:    8192000 kB\nBuffers: 5 kB\nHugePages_Total:       0\n"
	mi, err := ParseMemInfo(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if mi.Total != 16384000*1024 || mi.Available != 8192000*1024 {
		t.Errorf("got %+v", mi)
	}
	// Old kernels have no MemAvailable.
	mi, err = ParseMemInfo(strings.NewReader("MemTotal: 1000 kB\n"))
	if err != nil || mi.Available != mi.Total {
		t.Errorf("fallback: %+v, %v", mi, err)
	}
	if _, err := ParseMemInfo(strings.NewReader("nothing useful\n")); err == nil {
		t.Error("expected an error without MemTotal")
	}
}

func TestParseCPUAndLoad(t *testing.T) {
	a, err := ParseCPUStat(strings.NewReader("cpu  100 0 100 700 100 0 0 0 0 0\ncpu0 1 2 3 4 5 6 7 8 9 10\n"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Total != 1000 || a.Busy != 200 {
		t.Errorf("a = %+v", a)
	}
	b := CPUTimes{Busy: 400, Total: 1400}
	if got := CPUBusyPercent(a, b); !near(got, 50) {
		t.Errorf("CPUBusyPercent = %v, want 50", got)
	}
	if got := CPUBusyPercent(b, a); got != 0 {
		t.Errorf("going backwards should give 0, got %v", got)
	}
	if _, err := ParseCPUStat(strings.NewReader("intr 1 2 3\n")); err == nil {
		t.Error("expected an error without a cpu line")
	}
	l, err := ParseLoadAvg("0.52 0.40 0.30 1/234 5678\n")
	if err != nil || !near(l, 0.52) {
		t.Errorf("load = %v, %v", l, err)
	}
}

func TestHasRoom(t *testing.T) {
	const gb = 1 << 30
	tests := []struct {
		name            string
		host            uint64
		known           bool
		reserved, extra int64
		want            bool
	}{
		{"fits", 8 * gb, true, 4096, 512, true},
		{"exactly at host minus 1.5 GB", 8 * gb, true, 6144, 512, true}, // 6656 MB = 8192 - 1536
		{"one MB over", 8 * gb, true, 6144, 513, false},
		{"already over, removing memory is fine", 8 * gb, true, 7000, -1000, true},
		{"unknown host (macOS)", 0, false, 1 << 30, 1 << 30, true},
		{"tiny host", 1 * gb, true, 0, 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasRoom(tt.host, tt.known, tt.reserved, tt.extra); got != tt.want {
				t.Errorf("HasRoom = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEnoughFree(t *testing.T) {
	for _, tt := range []struct {
		total, free uint64
		want        bool
	}{{100, 15, true}, {100, 14, false}, {100, 80, true}, {1000, 149, false}} {
		if got := EnoughFree(tt.total, tt.free); got != tt.want {
			t.Errorf("EnoughFree(%d, %d) = %v", tt.total, tt.free, got)
		}
	}
}

func TestNextNightly(t *testing.T) {
	at := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, tt := range []struct{ now, want string }{
		{"2026-10-29T01:00:00Z", "2026-10-29T03:30:00Z"},
		{"2026-10-29T03:30:00Z", "2026-10-30T03:30:00Z"},
		{"2026-10-29T23:59:00Z", "2026-10-30T03:30:00Z"},
		{"2026-12-31T12:00:00+02:00", "2027-01-01T03:30:00Z"},
	} {
		if got := NextNightly(at(tt.now)); !got.Equal(at(tt.want)) {
			t.Errorf("NextNightly(%s) = %s, want %s", tt.now, got, tt.want)
		}
	}
}

func TestCrashTracker(t *testing.T) {
	c := newCrashTracker(5, 5*time.Minute)
	t0 := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		n, loop := c.Add("bp-a-1", t0.Add(time.Duration(i)*30*time.Second))
		if loop || n != i+1 {
			t.Fatalf("death %d: n=%d loop=%v", i+1, n, loop)
		}
	}
	if n, loop := c.Add("bp-a-1", t0.Add(2*time.Minute)); !loop || n != 5 {
		t.Errorf("fifth death inside 5 minutes: n=%d loop=%v", n, loop)
	}
	// Another container is counted separately.
	if n, loop := c.Add("bp-b-1", t0); loop || n != 1 {
		t.Errorf("other container: n=%d loop=%v", n, loop)
	}
	// Old deaths fall out of the window.
	c2 := newCrashTracker(5, 5*time.Minute)
	for i := 0; i < 4; i++ {
		c2.Add("x", t0.Add(time.Duration(i)*10*time.Second))
	}
	if n, loop := c2.Add("x", t0.Add(10*time.Minute)); loop || n != 1 {
		t.Errorf("deaths 10 minutes ago should not count: n=%d loop=%v", n, loop)
	}
	c2.Forget("x")
	if n, _ := c2.Add("x", t0); n != 1 {
		t.Errorf("after Forget: n=%d", n)
	}
}
