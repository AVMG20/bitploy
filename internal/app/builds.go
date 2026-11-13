package app

// OWNER: deploy agent. The builds that wait or run, for the builds widget
// in the dashboard sidebar (GET /api/v1/builds).

import (
	"context"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
)

// Builds is what waits and runs of the caller's apps and the apps of the
// projects shared with them, with the server's build slots.
type Builds struct {
	Running []ActiveBuild `json:"running" doc:"Deployments that run (fetching to swapping), oldest first"`
	Queued  []ActiveBuild `json:"queued" doc:"Deployments that wait for a build slot or for the app's running deployment, next first"`
	Slots   BuildSlots    `json:"slots"`
}

// BuildSlots are the server's build slots: shared by every account.
type BuildSlots struct {
	Used   int `json:"used" doc:"Builds that hold a slot now (fetch, check and build)"`
	Max    int `json:"max" doc:"Builds that may run at the same time (Admin > Server)"`
	Queued int `json:"queued" doc:"Deployments waiting on the whole server, of every account"`
	// PausedForDisk: the server disk is almost full, so no build starts
	// until an admin frees space (rollbacks still run).
	PausedForDisk bool `json:"paused_for_disk,omitempty" doc:"The server disk is almost full: queued builds wait until space is free"`
}

// ActiveBuild is one deployment in Builds.
type ActiveBuild struct {
	ID            string     `json:"id"`
	App           BuildApp   `json:"app"`
	Seq           int64      `json:"seq"`
	State         string     `json:"state"`
	SourceKind    string     `json:"source_kind"`
	CommitMessage *string    `json:"commit_message"`
	QueuedAt      time.Time  `json:"queued_at"`
	StartedAt     *time.Time `json:"started_at"`
	// Position is the place in the server's line (1: next); 0 while it runs.
	// Builds of other accounts count, though only the caller's are listed.
	// The line is fair: an account with fewer builds running goes first, so
	// the place can move when builds start or end.
	Position int `json:"position,omitempty" doc:"Queued: the place in the server's line, 1 is next; position-1 builds of any account get a slot before it (an account with fewer builds running goes first)"`
}

// BuildApp names the app of an ActiveBuild.
type BuildApp struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	ProjectID string `json:"project_id"`
}

// ListBuilds returns the builds that wait or run of apps the caller can see.
func (s *Service) ListBuilds(ctx context.Context, a Actor) (Builds, error) {
	rows, err := s.Q.ListActiveDeploymentsForUser(ctx, a.UserID)
	if err != nil {
		return Builds{}, apperr.Internal(err)
	}
	queued, err := s.Q.ListQueuedDeployments(ctx)
	if err != nil {
		return Builds{}, apperr.Internal(err)
	}
	// The place in the line is the order the worker hands out build slots
	// in (fairBuildOrder), of every account: an owner with fewer builds
	// running goes first. A rollback takes no slot, and a deployment whose
	// app still deploys is passed over until that is done, so neither holds
	// anyone up; they get the place after the builds queued before them.
	order := fairBuildOrder(queued, s.buildSlotHolders(), s.appBusy)
	place := make(map[string]int, len(queued))
	for i, d := range order {
		place[d.ID] = i + 1
	}
	ahead := 0
	for _, d := range queued {
		if _, ok := place[d.ID]; ok {
			ahead++
		} else {
			place[d.ID] = ahead + 1
		}
	}
	out := Builds{
		Running: []ActiveBuild{}, Queued: []ActiveBuild{},
		Slots: BuildSlots{Used: s.buildSlotsUsed(), Max: s.maxParallelBuilds(ctx), Queued: len(queued), PausedForDisk: s.buildsPausedForDisk()},
	}
	for _, r := range rows {
		b := ActiveBuild{
			ID: r.ID, App: BuildApp{ID: r.AppID, Slug: r.AppSlug, Name: r.AppName, ProjectID: r.AppProjectID},
			Seq: r.Seq, State: r.State, SourceKind: r.SourceKind, CommitMessage: strPtr(r.CommitMessage),
			QueuedAt: ts(r.QueuedAt), StartedAt: tsPtr(r.StartedAt),
		}
		if r.State == "queued" {
			b.Position = place[r.ID]
			out.Queued = append(out.Queued, b)
		} else {
			out.Running = append(out.Running, b)
		}
	}
	return out, nil
}
