package app

import (
	"context"
	"sync"
	"time"
)

// hub lets long-polls (get_deployment wait_seconds) wake as soon as a
// deployment changes state instead of polling the database.
type hub struct {
	mu      sync.Mutex
	waiters map[string][]chan struct{}
}

func newHub() *hub { return &hub{waiters: map[string][]chan struct{}{}} }

func (h *hub) notify(id string) {
	h.mu.Lock()
	ws := h.waiters[id]
	delete(h.waiters, id)
	h.mu.Unlock()
	for _, w := range ws {
		close(w)
	}
}

func (h *hub) wait(ctx context.Context, id string, d time.Duration) {
	if d <= 0 {
		return
	}
	ch := make(chan struct{})
	h.mu.Lock()
	h.waiters[id] = append(h.waiters[id], ch)
	h.mu.Unlock()
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ch:
		return
	case <-t.C:
	case <-ctx.Done():
	}
	h.mu.Lock()
	ws := h.waiters[id]
	for i, w := range ws {
		if w == ch {
			h.waiters[id] = append(ws[:i], ws[i+1:]...)
			break
		}
	}
	if len(h.waiters[id]) == 0 {
		delete(h.waiters, id)
	}
	h.mu.Unlock()
}
