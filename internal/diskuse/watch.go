package diskuse

import (
	"context"
	"time"
)

const (
	// Margin is how far above the threshold the automatic cleanup aims, so
	// it does not start again at the next small write.
	Margin = 0.02
	// Debounce: after a cleanup that could not free enough, the next one
	// waits this long, unless free space drops by DropAgain meanwhile.
	Debounce  = 30 * time.Minute
	DropAgain = 0.01
)

// Space is a filesystem's size and free bytes.
type Space struct {
	Total, Free uint64
}

// FreeFraction is free / total; 1 for an unknown filesystem.
func (s Space) FreeFraction() float64 {
	if s.Total == 0 {
		return 1
	}
	return float64(s.Free) / float64(s.Total)
}

// Watch decides when the automatic cleanup runs. The zero value has never
// run.
type Watch struct {
	lastAt   time.Time
	lastFree float64 // free fraction after the last run
}

// Due reports whether a cleanup should run now: free is below threshold,
// and no cleanup ran in the last Debounce, or free space dropped by
// DropAgain since the last one ended.
func (w *Watch) Due(now time.Time, free, threshold float64) bool {
	if free >= threshold {
		return false
	}
	if w.lastAt.IsZero() || now.Sub(w.lastAt) >= Debounce {
		return true
	}
	return free <= w.lastFree-DropAgain
}

// Ran records a cleanup that ended at now with freeAfter free.
func (w *Watch) Ran(now time.Time, freeAfter float64) {
	w.lastAt, w.lastFree = now, freeAfter
}

// Step is one cleanup kind and the function that runs it. Run returns the
// bytes Docker or buildx said it freed (0 when it does not say).
type Step struct {
	Kind string
	Run  func(ctx context.Context) (reported int64, removed int, err error)
}

// StepResult is what one step did.
type StepResult struct {
	Kind       string `json:"kind"`
	FreedBytes int64  `json:"freed_bytes"`
	Removed    int    `json:"removed,omitempty"`
	Error      string `json:"error,omitempty"`
}

// Freed is what a step freed: the growth of free space on the filesystem,
// or what the step reported when that is more (other writes at the same
// time hide part of the growth).
func Freed(before, after Space, reported int64) int64 {
	delta := int64(after.Free) - int64(before.Free)
	if reported > delta {
		return reported
	}
	if delta < 0 {
		return 0
	}
	return delta
}

// Escalate runs steps in order until free space reaches target (a
// fraction). It measures before the first step and after each one, and
// stops when ctx ends. reached reports whether target was reached.
func Escalate(ctx context.Context, steps []Step, measure func() (Space, error), target float64) (results []StepResult, reached bool) {
	sp, err := measure()
	known := err == nil
	if known && sp.FreeFraction() >= target {
		return nil, true
	}
	for _, st := range steps {
		if ctx.Err() != nil {
			break
		}
		before := sp
		reported, removed, runErr := st.Run(ctx)
		res := StepResult{Kind: st.Kind, Removed: removed}
		if runErr != nil {
			res.Error = runErr.Error()
		}
		after, merr := measure()
		switch {
		case merr == nil && known:
			res.FreedBytes = Freed(before, after, reported)
		case reported > 0:
			res.FreedBytes = reported
		}
		sp, known = after, merr == nil
		results = append(results, res)
		if merr == nil && after.FreeFraction() >= target {
			return results, true
		}
	}
	return results, false
}
