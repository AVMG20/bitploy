package traffic

import (
	"math"
	"time"
)

// SpeedBounds are the upper ends of the answer time buckets; the last bucket
// is everything slower. Counting into fixed buckets keeps one number per
// bucket per hour, adds up over any period, and still tells "typical" and
// "slowest 5%" closely enough to see an app getting slower.
var SpeedBounds = [SpeedBuckets - 1]time.Duration{
	50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond, 500 * time.Millisecond,
	time.Second, 2 * time.Second, 5 * time.Second,
}

const SpeedBuckets = 8

// SlowAfter is where an answer counts as slow: people notice a wait of a second.
const SlowAfter = time.Second

// Speed counts answers per answer time bucket (see SpeedBounds).
type Speed [SpeedBuckets]int64

// SpeedBucket returns the bucket of an answer time.
func SpeedBucket(d time.Duration) int {
	for i, b := range SpeedBounds {
		if d < b {
			return i
		}
	}
	return SpeedBuckets - 1
}

func (s Speed) Add(o Speed) Speed {
	for i := range s {
		s[i] += o[i]
	}
	return s
}

// Total is the number of timed answers.
func (s Speed) Total() int64 {
	var n int64
	for _, c := range s {
		n += c
	}
	return n
}

// Slow is the number of answers that took SlowAfter or longer.
func (s Speed) Slow() int64 {
	var n int64
	for i := SpeedBucket(SlowAfter); i < SpeedBuckets; i++ {
		n += s[i]
	}
	return n
}

// Quantile estimates the answer time that a share q (0 to 1) of the answers
// stayed under, in milliseconds, as if answers were spread evenly within
// their bucket. In the open last bucket it returns that bucket's lower end:
// "at least 5 seconds" is all that is known. ok is false without answers.
func (s Speed) Quantile(q float64) (ms int64, ok bool) {
	total := s.Total()
	if total == 0 {
		return 0, false
	}
	rank := q * float64(total)
	var below int64
	for i, c := range s {
		if c == 0 || float64(below+c) < rank {
			below += c
			continue
		}
		var lo time.Duration
		if i > 0 {
			lo = SpeedBounds[i-1]
		}
		if i == SpeedBuckets-1 {
			return lo.Milliseconds(), true
		}
		f := (rank - float64(below)) / float64(c)
		ms := (float64(lo) + f*float64(SpeedBounds[i]-lo)) / float64(time.Millisecond)
		return max(1, int64(math.Round(ms))), true
	}
	return SpeedBounds[SpeedBuckets-2].Milliseconds(), true
}
