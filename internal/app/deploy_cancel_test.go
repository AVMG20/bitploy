package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/deploy"
)

func (e *depEnv) insertDep(t *testing.T, app App, id string, seq int64, state string) {
	t.Helper()
	e.exec(t, `INSERT INTO deployments (id, app_id, seq, state, source_kind, channel, queued_at, started_at) VALUES (?, ?, ?, ?, 'files', 'api', ?, ?)`,
		id, app.ID, seq, state, time.Now().Unix(), time.Now().Unix())
}

// fakePipeline stands in for a running pipeline of the deployment: it holds the
// app's busy flag and a build slot, and when its context is cancelled it ends
// like the real one (finish with errCancelled), then frees both.
func (e *depEnv) fakePipeline(t *testing.T, appID, depID string, holdSlot bool) (done chan struct{}) {
	t.Helper()
	if holdSlot && !e.s.tryBuildSlot(depID, e.user.UserID, 1) {
		t.Fatal("a build slot is taken already")
	}
	ctx, cancel := context.WithCancel(context.Background())
	if !e.s.tryBusy(appID, depID, cancel) {
		t.Fatal("the app is busy already")
	}
	done = make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		dep, _ := e.s.Q.GetDeployment(context.Background(), depID)
		app, _ := e.s.Q.GetApp(context.Background(), appID)
		p := &pipeline{s: e.s, slotHeld: holdSlot, ring: deploy.NewLineRing(10), dep: dep, app: app}
		p.finish(ctx, errCancelled)
		p.releaseSlot()
		e.s.clearBusy(appID, depID)
	}()
	return done
}

func (e *depEnv) depRow(t *testing.T, id string) (state, code, msg string) {
	t.Helper()
	var c, m *string
	if err := e.s.Store.DB.QueryRow(`SELECT state, error_code, error_message FROM deployments WHERE id = ?`, id).Scan(&state, &c, &m); err != nil {
		t.Fatal(err)
	}
	if c != nil {
		code = *c
	}
	if m != nil {
		msg = *m
	}
	return
}

func (e *depEnv) appStatus(t *testing.T, id string) (status, msg, current string) {
	t.Helper()
	var c *string
	if err := e.s.Store.DB.QueryRow(`SELECT status, status_message, current_deployment_id FROM apps WHERE id = ?`, id).Scan(&status, &msg, &c); err != nil {
		t.Fatal(err)
	}
	if c != nil {
		current = *c
	}
	return
}

func (e *depEnv) auditCancels(t *testing.T) []string {
	t.Helper()
	rows, err := e.s.Store.DB.Query(`SELECT target_id || ' ' || channel || ' ' || COALESCE(user_id, '') || ' ' || details FROM audit_log WHERE action = 'deployment.cancelled' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

func TestCancelQueuedDeployment(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})
	e.insertDep(t, app, "dep_q", 1, "queued")
	e.exec(t, `UPDATE apps SET status = 'deploying' WHERE id = ?`, app.ID)
	_ = os.MkdirAll(filepath.Dir(e.s.sourceZip("dep_q")), 0o750)
	if err := os.WriteFile(e.s.sourceZip("dep_q"), []byte("zip"), 0o640); err != nil {
		t.Fatal(err)
	}

	// Somebody else's deployment does not exist; a read token may not cancel; nothing changed so far.
	other := e.addUser(t, "usr_bob", "bob@example.com", "user")
	if _, err := e.s.CancelDeployment(ctx, other, "dep_q"); depCode(err) != "deployment_not_found" {
		t.Errorf("other user: %v", err)
	}
	ro := e.user
	ro.Scope = ScopeRead
	if _, err := e.s.CancelDeployment(ctx, ro, "dep_q"); depCode(err) != "token_read_only" {
		t.Errorf("read token: %v", err)
	}
	if _, err := e.s.CancelDeployment(ctx, e.user, "dep_nope"); depCode(err) != "deployment_not_found" {
		t.Errorf("unknown: %v", err)
	}
	if st, _, _ := e.depRow(t, "dep_q"); st != "queued" {
		t.Fatalf("state after refusals = %s", st)
	}

	d, err := e.s.CancelDeployment(ctx, e.user, "dep_q")
	if err != nil {
		t.Fatal(err)
	}
	// e.user is an API token: the message says so, not "by the user".
	if d.State != "cancelled" || d.FinishedAt == nil || d.Error == nil || d.Error.Code != "cancelled" || d.Error.Message != "Cancelled through the API." {
		t.Errorf("deployment = %+v", d)
	}
	if len(d.NextSteps) == 0 || strings.Contains(strings.Join(d.NextSteps, " "), "newer deployment") || strings.Contains(strings.ToLower(strings.Join(d.NextSteps, " ")), "fix this") {
		t.Errorf("next steps = %v", d.NextSteps)
	}
	if _, err := os.Stat(e.s.sourceZip("dep_q")); !os.IsNotExist(err) {
		t.Error("the stored source was not removed")
	}
	// A never-deployed app goes back to new.
	if st, msg, cur := e.appStatus(t, app.ID); st != "new" || msg != "" || cur != "" {
		t.Errorf("app = %s %q %q", st, msg, cur)
	}
	if a := e.auditCancels(t); len(a) != 1 || !strings.HasPrefix(a[0], "dep_q api usr_alice ") || !strings.Contains(a[0], `"was":"queued"`) {
		t.Errorf("audit = %v", a)
	}
	// Twice is refused.
	if _, err := e.s.CancelDeployment(ctx, e.user, "dep_q"); depCode(err) != "deployment_finished" {
		t.Errorf("again: %v", err)
	}
}

func TestCancelStatusSettles(t *testing.T) {
	ctx := context.Background()
	t.Run("live version keeps running", func(t *testing.T) {
		e := depTestSvc(t)
		app := e.mustApp(t, CreateAppInput{})
		live := e.seedRunning(t, app, 1)
		e.insertDep(t, app, "dep_q", 2, "queued")
		if _, err := e.s.CancelDeployment(ctx, e.user, "dep_q"); err != nil {
			t.Fatal(err)
		}
		if st, _, cur := e.appStatus(t, app.ID); st != "running" || cur != live {
			t.Errorf("app = %s current %s", st, cur)
		}
		if st, _, _ := e.depRow(t, live); st != "running" {
			t.Errorf("the live deployment is %s", st)
		}
	})
	t.Run("an earlier failure comes back", func(t *testing.T) {
		e := depTestSvc(t)
		app := e.mustApp(t, CreateAppInput{})
		e.insertDep(t, app, "dep_f", 1, "failed")
		e.exec(t, `UPDATE deployments SET error_code = 'build_failed', error_message = 'The build failed: x' WHERE id = 'dep_f'`)
		e.insertDep(t, app, "dep_q", 2, "queued")
		e.exec(t, `UPDATE apps SET status = 'deploying' WHERE id = ?`, app.ID)
		if _, err := e.s.CancelDeployment(ctx, e.user, "dep_q"); err != nil {
			t.Fatal(err)
		}
		if st, msg, _ := e.appStatus(t, app.ID); st != "failed" || msg != "The build failed: x" {
			t.Errorf("app = %s %q", st, msg)
		}
	})
	t.Run("a stopped app stays stopped", func(t *testing.T) {
		e := depTestSvc(t)
		app := e.mustApp(t, CreateAppInput{})
		e.seedRunning(t, app, 1)
		e.exec(t, `UPDATE apps SET desired_state = 'stopped', status = 'deploying' WHERE id = ?`, app.ID)
		e.insertDep(t, app, "dep_q", 2, "queued")
		if _, err := e.s.CancelDeployment(ctx, e.user, "dep_q"); err != nil {
			t.Fatal(err)
		}
		if st, _, _ := e.appStatus(t, app.ID); st != "stopped" {
			t.Errorf("app = %s", st)
		}
	})
	t.Run("another queued deployment keeps it deploying", func(t *testing.T) {
		e := depTestSvc(t)
		app := e.mustApp(t, CreateAppInput{})
		e.insertDep(t, app, "dep_a", 1, "queued")
		e.insertDep(t, app, "dep_b", 2, "queued")
		e.exec(t, `UPDATE apps SET status = 'deploying' WHERE id = ?`, app.ID)
		if _, err := e.s.CancelDeployment(ctx, e.user, "dep_a"); err != nil {
			t.Fatal(err)
		}
		if st, _, _ := e.appStatus(t, app.ID); st != "deploying" {
			t.Errorf("app = %s", st)
		}
	})
}

func TestCancelDuringBuild(t *testing.T) {
	for _, state := range []string{"fetching", "validating", "building", "starting", "health_check"} {
		t.Run(state, func(t *testing.T) {
			e := depTestSvc(t)
			ctx := context.Background()
			app := e.mustApp(t, CreateAppInput{})
			e.insertDep(t, app, "dep_b", 1, state)
			e.exec(t, `UPDATE apps SET status = 'deploying' WHERE id = ?`, app.ID)
			e.insertDep(t, app, "dep_later", 2, "running") // not live: only the shape of history
			e.exec(t, `UPDATE deployments SET state = 'failed' WHERE id = 'dep_later'`)
			done := e.fakePipeline(t, app.ID, "dep_b", true)
			if e.s.buildSlotsUsed() != 1 || !e.s.appBusy(app.ID) {
				t.Fatal("the fake pipeline does not hold the app")
			}

			agent := e.user
			agent.Channel = ChannelMCP
			d, err := e.s.CancelDeployment(ctx, agent, "dep_b")
			if err != nil {
				t.Fatal(err)
			}
			<-done
			if d.State != "cancelled" || d.Error == nil || d.Error.Code != "cancelled" || d.Error.Message != "Cancelled through MCP (an AI agent)." {
				t.Errorf("answer = %s %+v", d.State, d.Error)
			}
			if e.s.buildSlotsUsed() != 0 {
				t.Error("the build slot was not freed")
			}
			if e.s.appBusy(app.ID) || e.s.activeDeployment("dep_b") {
				t.Error("the busy flag is stuck")
			}
			if st, code, msg := e.depRow(t, "dep_b"); st != "cancelled" || code != "cancelled" || msg != "Cancelled through MCP (an AI agent)." {
				t.Errorf("row = %s %s %q", st, code, msg)
			}
			// Not a failure.
			var failed int
			_ = e.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'deployment.failed'`).Scan(&failed)
			if failed != 0 {
				t.Error("a cancel wrote deployment.failed")
			}
			if a := e.auditCancels(t); len(a) != 1 || !strings.Contains(a[0], `"was":"`+state+`"`) || !strings.Contains(a[0], "usr_alice") {
				t.Errorf("audit = %v", a)
			}
			if st, _, _ := e.appStatus(t, app.ID); st != "failed" {
				// An earlier deployment (dep_later) failed, so the app is failed again, not stuck deploying.
				t.Errorf("app = %s", st)
			}
			// The same app can build again: the flag is free.
			if !e.s.tryBusy(app.ID, "dep_next", func() {}) {
				t.Error("the app cannot start another pipeline")
			}
			e.s.clearBusy(app.ID, "dep_next")
		})
	}
}

func TestCancelKeepsLiveVersion(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})
	live := e.seedRunning(t, app, 1)
	e.insertDep(t, app, "dep_b", 2, "building")
	done := e.fakePipeline(t, app.ID, "dep_b", true)
	d, err := e.s.CancelDeployment(ctx, e.user, "dep_b")
	if err != nil || d.State != "cancelled" {
		t.Fatalf("cancel: %v %s", err, d.State)
	}
	<-done
	if st, _, cur := e.appStatus(t, app.ID); st != "running" || cur != live {
		t.Errorf("app = %s current %s", st, cur)
	}
	if st, _, _ := e.depRow(t, live); st != "running" {
		t.Errorf("live deployment = %s", st)
	}
}

func TestCancelTooLateAndFinished(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})
	e.insertDep(t, app, "dep_s", 1, "swapping")
	called := false
	if !e.s.tryBusy(app.ID, "dep_s", func() { called = true }) {
		t.Fatal("busy")
	}
	_, err := e.s.CancelDeployment(ctx, e.user, "dep_s")
	if depCode(err) != "too_late_to_cancel" || !strings.Contains(err.Error(), "already going live") {
		t.Errorf("swapping: %v", err)
	}
	if called {
		t.Error("a swapping pipeline was cancelled")
	}
	e.s.clearBusy(app.ID, "dep_s")

	for i, st := range []string{"running", "failed", "superseded", "cancelled"} {
		id := "dep_" + st
		e.insertDep(t, app, id, int64(10+i), st)
		if _, err := e.s.CancelDeployment(ctx, e.user, id); depCode(err) != "deployment_finished" {
			t.Errorf("%s: %v", st, err)
		}
		if got, _, _ := e.depRow(t, id); got != st {
			t.Errorf("%s became %s", st, got)
		}
	}

	// The point of no return is one decision: either the pipeline goes live or it is cancelled.
	e.insertDep(t, app, "dep_c1", 20, "health_check")
	cancelled := false
	e.s.tryBusy(app.ID, "dep_c1", func() { cancelled = true })
	if !e.s.commitSwap("dep_c1") {
		t.Fatal("commitSwap refused without a cancel")
	}
	if found, late := e.s.requestCancel("dep_c1", e.user); !found || !late || cancelled {
		t.Errorf("cancel after commit: found=%v late=%v cancelled=%v", found, late, cancelled)
	}
	e.s.clearBusy(app.ID, "dep_c1")
	e.s.tryBusy(app.ID, "dep_c2", func() {})
	if found, late := e.s.requestCancel("dep_c2", e.user); !found || late {
		t.Errorf("cancel before commit: %v %v", found, late)
	}
	if e.s.commitSwap("dep_c2") {
		t.Error("a cancelled pipeline may not swap")
	}
	e.s.clearBusy(app.ID, "dep_c2")
	e.s.deployRT.mu.Lock()
	left := len(e.s.deployRT.byUser) + len(e.s.deployRT.committed) + len(e.s.deployRT.cancelReq)
	e.s.deployRT.mu.Unlock()
	if left != 0 {
		t.Errorf("clearBusy left %d entries", left)
	}
}

// A deployment that is in progress but runs nowhere in this process (the
// reconciler ends those) is shown as it is, not reported as cancelled.
func TestCancelOrphanShownAsIs(t *testing.T) {
	e := depTestSvc(t)
	app := e.mustApp(t, CreateAppInput{})
	e.insertDep(t, app, "dep_o", 1, "building")
	d, err := e.s.CancelDeployment(context.Background(), e.user, "dep_o")
	if err != nil || d.State != "building" {
		t.Errorf("orphan: %v %s", err, d.State)
	}
}

func TestCancelAppDeployment(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})
	if _, err := e.s.CancelAppDeployment(ctx, e.user, app.Slug); depCode(err) != "no_deployment_to_cancel" {
		t.Errorf("nothing to cancel: %v", err)
	}
	e.insertDep(t, app, "dep_old", 1, "failed")
	e.insertDep(t, app, "dep_q", 2, "queued")
	d, err := e.s.CancelAppDeployment(ctx, e.user, app.Slug)
	if err != nil || d.ID != "dep_q" || d.State != "cancelled" {
		t.Errorf("by app: %v %+v", err, d)
	}
	other := e.addUser(t, "usr_bob", "bob@example.com", "user")
	if _, err := e.s.CancelAppDeployment(ctx, other, app.Slug); depCode(err) != "app_not_found" {
		t.Errorf("other user: %v", err)
	}
}

// Several cancels at once end with one cancel and no stuck state.
func TestCancelConcurrent(t *testing.T) {
	e := depTestSvc(t)
	app := e.mustApp(t, CreateAppInput{})
	e.insertDep(t, app, "dep_b", 1, "building")
	done := e.fakePipeline(t, app.ID, "dep_b", true)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if d, err := e.s.CancelDeployment(context.Background(), e.user, "dep_b"); err != nil && depCode(err) != "deployment_finished" {
				t.Errorf("cancel: %v %s", err, d.State)
			}
		}()
	}
	wg.Wait()
	<-done
	if a := e.auditCancels(t); len(a) != 1 {
		t.Errorf("audit rows = %d", len(a))
	}
	if e.s.buildSlotsUsed() != 0 || e.s.appBusy(app.ID) {
		t.Error("slot or busy flag left")
	}
}

// The message of a cancelled deployment names where the cancel came from, so
// an agent that cancelled does not read it as the user stepping in.
func TestCancelledMessage(t *testing.T) {
	for ch, want := range map[Channel]string{
		ChannelDashboard: "Cancelled in the dashboard.",
		ChannelMCP:       "Cancelled through MCP (an AI agent).",
		ChannelAPI:       "Cancelled through the API.",
		ChannelSystem:    "Cancelled.",
		ChannelGitHub:    "Cancelled.",
	} {
		if got := cancelledMessage(Actor{Channel: ch}); got != want {
			t.Errorf("%s: %q, want %q", ch, got, want)
		}
	}
}
