package app

// OWNER: deploy agent. The deploy queue worker.

import (
	"context"
	"time"

	"github.com/AVMG20/bitploy/internal/store/db"
)

// RunWorker runs the deploy queue until ctx ends (max parallel builds from
// the server settings, see maxParallelBuilds).
//
// Queued deployments live in SQLite. Every pass starts the queued rollbacks
// of idle apps (they build nothing and need no slot) and hands the free build
// slots out in fairBuildOrder. One pipeline runs per app at a time.
func (s *Service) RunWorker(ctx context.Context) {
	if s.Docker == nil {
		s.Log.Warn("deploy worker not started: Docker is unavailable")
		<-ctx.Done()
		return
	}
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		s.dispatch(ctx)
		select {
		case <-ctx.Done():
			done := make(chan struct{})
			go func() { s.deployRT.wg.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(20 * time.Second):
			}
			return
		case <-s.WakeCh():
		case <-t.C:
		}
	}
}

func (s *Service) dispatch(ctx context.Context) {
	rows, err := s.Q.ListQueuedDeployments(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.Log.Error("list queued deployments", "err", err)
		}
		return
	}
	for _, d := range rows {
		if ctx.Err() != nil {
			return
		}
		if d.SourceKind == "rollback" && !s.appBusy(d.AppID) {
			s.startPipeline(ctx, d.ID, d.AppID, false)
		}
	}
	limit := s.maxParallelBuilds(ctx)
	for _, d := range fairBuildOrder(rows, s.buildSlotHolders(), s.appBusy) {
		if ctx.Err() != nil {
			return
		}
		if !s.tryBuildSlot(d.ID, d.OwnerID, limit) {
			return // all build slots taken; the rest stays queued
		}
		if !s.startPipeline(ctx, d.ID, d.AppID, true) {
			s.releaseBuildSlot(d.ID)
		}
	}
}

// startPipeline runs a queued deployment in the background; false when its
// app already runs one.
func (s *Service) startPipeline(ctx context.Context, depID, appID string, slotHeld bool) bool {
	pctx, cancel := context.WithCancel(ctx)
	if !s.tryBusy(appID, depID, cancel) {
		cancel()
		return false
	}
	s.deployRT.wg.Add(1)
	go func() {
		defer s.deployRT.wg.Done()
		defer cancel()
		defer s.Wake()
		defer s.clearBusy(appID, depID)
		s.runPipeline(pctx, depID, slotHeld)
	}()
	return true
}

// fairBuildOrder is the order in which the queued builds get a build slot,
// given the slots each owner holds now (held) and the apps that already run a
// deployment (busy: theirs waits until that is done). Rollbacks are left
// out: they need no slot.
//
// The line is first come, first served, with one rule on top: an owner who
// holds fewer slots goes first. So someone whose build already runs does not
// get another slot while a build of someone else waits that could start; that
// one goes first, even when it was queued later. Between owners who hold as
// many slots as each other the oldest build wins, and an owner alone in the
// line may use every slot. Each build handed out counts as a slot held for
// the rest of the order, so owners with several builds each take turns.
func fairBuildOrder(line []db.ListQueuedDeploymentsRow, held map[string]int, busy func(appID string) bool) []db.ListQueuedDeploymentsRow {
	var wait []db.ListQueuedDeploymentsRow
	for _, d := range line {
		if d.SourceKind != "rollback" && !busy(d.AppID) {
			wait = append(wait, d)
		}
	}
	count := make(map[string]int, len(held))
	for owner, n := range held {
		count[owner] = n
	}
	out := make([]db.ListQueuedDeploymentsRow, 0, len(wait))
	for len(wait) > 0 {
		next := 0
		for i, d := range wait {
			if count[d.OwnerID] < count[wait[next].OwnerID] {
				next = i
			}
		}
		d := wait[next]
		out = append(out, d)
		count[d.OwnerID]++
		wait = append(wait[:next], wait[next+1:]...)
	}
	return out
}
