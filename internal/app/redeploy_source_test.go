package app

import (
	"bytes"
	"context"
	"os"
	"testing"
)

// filesWith returns a static site whose index.html says body, so two
// deployments have different source snapshots.
func filesWith(body string) []FileInput {
	return []FileInput{
		{Path: "Dockerfile", Content: depStaticDockerfile},
		{Path: "index.html", Content: body},
	}
}

// sameSource reports whether two deployments' stored snapshots are identical.
func sameSource(t *testing.T, e *depEnv, a, b string) bool {
	t.Helper()
	x, err := os.ReadFile(e.s.sourceZip(a))
	if err != nil {
		t.Fatalf("source of %s: %v", a, err)
	}
	y, err := os.ReadFile(e.s.sourceZip(b))
	if err != nil {
		t.Fatalf("source of %s: %v", b, err)
	}
	return bytes.Equal(x, y)
}

func TestRedeployRebuildsTheLiveVersionAndRetryLatestTheNewestAttempt(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})

	// #1 runs, #2 (newer code) failed.
	d1, err := e.s.DeployFiles(ctx, e.user, app.Slug, filesWith("v1"))
	if err != nil {
		t.Fatal(err)
	}
	e.exec(t, `UPDATE deployments SET state = 'running' WHERE id = ?`, d1.ID)
	e.exec(t, `UPDATE apps SET current_deployment_id = ?, status = 'running' WHERE id = ?`, d1.ID, app.ID)
	d2, err := e.s.DeployFiles(ctx, e.user, app.Slug, filesWith("v2"))
	if err != nil {
		t.Fatal(err)
	}
	e.exec(t, `UPDATE deployments SET state = 'failed' WHERE id = ?`, d2.ID)

	r, err := e.s.Redeploy(ctx, e.user, app.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if !sameSource(t, e, r.ID, d1.ID) {
		t.Error("Redeploy did not rebuild the live version (#1)")
	}
	e.exec(t, `UPDATE deployments SET state = 'cancelled' WHERE id = ?`, r.ID)

	// The agent fixed a setting after #2 failed: deploy with only app= retries #2.
	r, err = e.s.RetryLatest(ctx, e.user, app.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if !sameSource(t, e, r.ID, d2.ID) {
		t.Error("RetryLatest did not rebuild the newest attempt (#2)")
	}
}

func TestRedeployAfterRollbackUsesTheVersionRolledBackTo(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})

	d1, err := e.s.DeployFiles(ctx, e.user, app.Slug, filesWith("good"))
	if err != nil {
		t.Fatal(err)
	}
	e.exec(t, `UPDATE deployments SET state = 'superseded' WHERE id = ?`, d1.ID)
	d2, err := e.s.DeployFiles(ctx, e.user, app.Slug, filesWith("bad"))
	if err != nil {
		t.Fatal(err)
	}
	e.exec(t, `UPDATE deployments SET state = 'superseded' WHERE id = ?`, d2.ID)
	// #3 is a rollback to #1: no snapshot of its own.
	d3, err := e.s.DeployFiles(ctx, e.user, app.Slug, filesWith("placeholder"))
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(e.s.sourceZip(d3.ID))
	e.exec(t, `UPDATE deployments SET state = 'running', source_kind = 'rollback', source_ref = ? WHERE id = ?`, d1.ID, d3.ID)
	e.exec(t, `UPDATE apps SET current_deployment_id = ?, status = 'running' WHERE id = ?`, d3.ID, app.ID)

	// Retention keeps the rolled-back-to snapshot.
	e.s.pruneApp(ctx, app.ID)
	if _, err := os.Stat(e.s.sourceZip(d1.ID)); err != nil {
		t.Fatalf("snapshot of the live version was pruned: %v", err)
	}

	r, err := e.s.Redeploy(ctx, e.user, app.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if !sameSource(t, e, r.ID, d1.ID) {
		t.Error("Redeploy after a rollback rebuilt the version rolled back from")
	}
	e.exec(t, `UPDATE deployments SET state = 'cancelled' WHERE id = ?`, r.ID)

	// Snapshot gone: say so instead of building other code.
	_ = os.Remove(e.s.sourceZip(d1.ID))
	if _, err := e.s.Redeploy(ctx, e.user, app.Slug); depCode(err) != "source_gone" {
		t.Errorf("redeploy without the live version's snapshot: %v", err)
	}
}

// Every version whose image is kept for a rollback keeps its source too, so
// a redeploy after rolling back to it can rebuild it.
func TestPruneKeepsTheSourceOfEveryRollbackTarget(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})

	var ids []string
	for i := 1; i <= 4; i++ {
		d, err := e.s.DeployFiles(ctx, e.user, app.Slug, filesWith("v"+itoa(i)))
		if err != nil {
			t.Fatal(err)
		}
		e.exec(t, `UPDATE deployments SET state = 'superseded', image = ? WHERE id = ?`, "bitploy/"+app.Slug+":"+itoa(i), d.ID)
		ids = append(ids, d.ID)
	}
	e.exec(t, `UPDATE deployments SET state = 'running' WHERE id = ?`, ids[3])
	e.exec(t, `UPDATE apps SET current_deployment_id = ?, status = 'running' WHERE id = ?`, ids[3], app.ID)

	// Images of #4, #3 and #2 stay, so their sources stay; #1 goes.
	e.s.pruneApp(ctx, app.ID)
	for i, id := range ids {
		_, err := os.Stat(e.s.sourceZip(id))
		if (i > 0) != (err == nil) {
			t.Errorf("source of #%d: exists=%v", i+1, err == nil)
		}
	}

	// Roll back to #2, then redeploy and retry: both rebuild #2.
	rb, err := e.s.Rollback(ctx, e.user, app.Slug, ids[1])
	if err != nil {
		t.Fatal(err)
	}
	e.exec(t, `UPDATE deployments SET state = 'superseded' WHERE id = ?`, ids[3])
	e.exec(t, `UPDATE deployments SET state = 'running' WHERE id = ?`, rb.ID)
	e.exec(t, `UPDATE apps SET current_deployment_id = ? WHERE id = ?`, rb.ID, app.ID)
	e.s.pruneApp(ctx, app.ID)

	r, err := e.s.Redeploy(ctx, e.user, app.Slug)
	if err != nil {
		t.Fatalf("redeploy after a rollback: %v", err)
	}
	if !sameSource(t, e, r.ID, ids[1]) {
		t.Error("Redeploy after a rollback did not rebuild #2")
	}
	e.exec(t, `UPDATE deployments SET state = 'cancelled' WHERE id = ?`, r.ID)
	r, err = e.s.RetryLatest(ctx, e.user, app.Slug)
	if err != nil {
		t.Fatalf("retry after a rollback: %v", err)
	}
	if !sameSource(t, e, r.ID, ids[1]) {
		t.Error("RetryLatest after a rollback did not rebuild #2")
	}
}

func TestFailedDeployNeverLeavesTheAppDeploying(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	cases := []struct {
		name, desired, status string
		current               bool
		want                  string
	}{
		{"first deploy failed", "running", "deploying", false, "failed"},
		{"failed app redeployed, failed again", "running", "deploying", true, "failed"},
		{"stopped app redeployed, failed", "stopped", "deploying", true, "stopped"},
		{"running app keeps running", "running", "running", true, "running"},
		{"stopped app stays stopped", "stopped", "stopped", true, "stopped"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := e.mustApp(t, CreateAppInput{})
			d, err := e.s.DeployFiles(ctx, e.user, app.Slug, filesWith(tc.name))
			if err != nil {
				t.Fatal(err)
			}
			e.exec(t, `UPDATE apps SET desired_state = ?, status = ? WHERE id = ?`, tc.desired, tc.status, app.ID)
			if tc.current {
				e.exec(t, `UPDATE apps SET current_deployment_id = ? WHERE id = ?`, d.ID, app.ID)
			}
			e.s.settleStatusAfterFailure(ctx, app.ID, "it broke")
			row, _ := e.s.Q.GetApp(ctx, app.ID)
			if row.Status != tc.want {
				t.Errorf("status %q, want %q", row.Status, tc.want)
			}
		})
	}
}
