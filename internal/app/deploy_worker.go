package app

// OWNER: deploy agent. The deploy queue worker.

import (
	"context"
	"github.com/arno/bitploy/internal/store/db"
	"time"
)

// RunWorker runs the deploy queue until ctx ends (max parallel builds from config).
//
// Queued deployments live in SQLite. Every pass takes the oldest queued
// deployment of each idle app, as long as a build slot is free (rollbacks
// need no slot: they do not build). One pipeline runs per app at a time.
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
	slots := s.buildSlots()
	for _, d := range rows {
		if ctx.Err() != nil {
			return
		}
		needSlot := d.SourceKind != "rollback"
		if s.appBusy(d.AppID) {
			continue
		}
		if needSlot {
			select {
			case slots <- struct{}{}:
			default:
				continue // all build slots taken; it stays queued
			}
		}
		pctx, cancel := context.WithCancel(ctx)
		if !s.tryBusy(d.AppID, d.ID, cancel) {
			cancel()
			if needSlot {
				<-slots
			}
			continue
		}
		s.deployRT.wg.Add(1)
		go func(d db.Deployment, needSlot bool) {
			defer s.deployRT.wg.Done()
			defer cancel()
			defer s.Wake()
			defer s.clearBusy(d.AppID, d.ID)
			s.runPipeline(pctx, d.ID, needSlot)
		}(d, needSlot)
	}
}
