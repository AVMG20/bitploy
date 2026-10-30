package mcp

import (
	"testing"

	"github.com/AVMG20/bitploy/internal/app"
)

func TestRollbackTargetSkipsTheVersionRolledBackFrom(t *testing.T) {
	img := func(s string) *string { return &s }
	dep := func(id string, seq int64, state, kind, image string) app.Deployment {
		return app.Deployment{ID: id, Seq: seq, State: state, SourceKind: kind, Image: img(image)}
	}
	cases := []struct {
		name  string
		items []app.Deployment // newest first
		want  string
	}{
		{"plain history: the previous version", []app.Deployment{
			dep("d3", 3, "running", "files", "i3"),
			dep("d2", 2, "superseded", "files", "i2"),
			dep("d1", 1, "superseded", "files", "i1"),
		}, "d2"},
		{"after rolling back from #3 to #2: go to #1, not back to #3", []app.Deployment{
			dep("d4", 4, "running", "rollback", "i2"),
			dep("d3", 3, "superseded", "files", "i3"),
			dep("d2", 2, "superseded", "files", "i2"),
			dep("d1", 1, "superseded", "files", "i1"),
		}, "d1"},
		{"failed deployments are skipped", []app.Deployment{
			dep("d3", 3, "failed", "files", "i3"),
			dep("d2", 2, "running", "files", "i2"),
			dep("d1", 1, "superseded", "files", "i1"),
		}, "d1"},
		{"nothing older", []app.Deployment{
			dep("d2", 2, "running", "rollback", "i1"),
			dep("d1", 1, "superseded", "files", "i1"),
		}, ""},
		{"no live deployment: newest that ran", []app.Deployment{
			dep("d2", 2, "superseded", "files", "i2"),
			dep("d1", 1, "superseded", "files", "i1"),
		}, "d2"},
	}
	for _, tc := range cases {
		if got := rollbackTarget(tc.items); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestRuntimeFailureIsNotCalledAFailedDeploy(t *testing.T) {
	live := &app.Deployment{ID: "dep_1", State: "running"}
	ap := app.App{Slug: "shop", Status: "failed", StatusMessage: "Out of memory", Current: live}
	if b := newAppBrief(ap, nil); b.Problem == "" || b.Problem[:24] != "the live version stopped" {
		t.Errorf("problem: %q", b.Problem)
	}
	if next := appNext(ap); len(next) == 0 || next[0][:15] != "The deploy work" {
		t.Errorf("next: %v", next)
	}
	ap.Current = nil
	if b := newAppBrief(ap, nil); b.Problem[:20] != "the last deploy fail" {
		t.Errorf("first deploy failed: %q", b.Problem)
	}
}
