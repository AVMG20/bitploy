package traffic

import (
	"testing"
	"time"
)

func TestSpeedBucket(t *testing.T) {
	for d, want := range map[time.Duration]int{
		time.Millisecond:        0,
		49 * time.Millisecond:   0,
		50 * time.Millisecond:   1,
		150 * time.Millisecond:  2,
		499 * time.Millisecond:  3,
		time.Second:             5,
		4 * time.Second:         6,
		5 * time.Second:         7,
		10 * time.Minute:        7,
		999 * time.Millisecond:  4,
		1999 * time.Millisecond: 5,
	} {
		if got := SpeedBucket(d); got != want {
			t.Errorf("SpeedBucket(%v) = %d, want %d", d, got, want)
		}
	}
}

func TestSpeedSlowAndTotal(t *testing.T) {
	s := Speed{10, 5, 0, 0, 3, 2, 1, 1}
	if s.Total() != 22 || s.Slow() != 4 {
		t.Errorf("total %d, slow %d", s.Total(), s.Slow())
	}
	if got := s.Add(Speed{1, 1, 1, 1, 1, 1, 1, 1}); got != (Speed{11, 6, 1, 1, 4, 3, 2, 2}) {
		t.Errorf("add = %v", got)
	}
}

func TestSpeedQuantile(t *testing.T) {
	if _, ok := (Speed{}).Quantile(0.5); ok {
		t.Error("no answers, still a quantile")
	}
	for _, c := range []struct {
		s    Speed
		q    float64
		want int64
	}{
		{Speed{10}, 0.5, 25},                       // half way through 0 to 50 ms
		{Speed{0, 0, 4}, 0.5, 150},                 // all between 100 and 200 ms
		{Speed{50, 50}, 0.5, 50},                   // the median sits on the bound
		{Speed{90, 0, 0, 0, 10}, 0.95, 750},        // half way into 500 ms to 1 s
		{Speed{0, 0, 0, 0, 0, 0, 0, 3}, 0.5, 5000}, // over 5 s: only that is known
		{Speed{1}, 0, 1},                           // never 0 ms
	} {
		got, ok := c.s.Quantile(c.q)
		if !ok || got != c.want {
			t.Errorf("%v at %v = %d, want %d", c.s, c.q, got, c.want)
		}
	}
}
