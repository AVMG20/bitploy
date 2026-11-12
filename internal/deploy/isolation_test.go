package deploy

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/config"
)

func TestBaseImageForbidden(t *testing.T) {
	tests := []struct {
		name, df string
		want     bool
	}{
		{"plain base", "FROM node:24-alpine\nEXPOSE 3000\n", false},
		{"FROM bitploy", "FROM bitploy/victim:3\nEXPOSE 3000\n", true},
		{"FROM bitploy with registry host", "FROM docker.io/bitploy/victim:3\nEXPOSE 3000\n", true},
		{"FROM bitploy upper case, platform flag", "FROM --platform=linux/amd64 BITPLOY/Victim:3\nEXPOSE 3000\n", true},
		{"COPY --from", "FROM node:24-alpine\nCOPY --from=bitploy/victim:3 /data /data\nEXPOSE 3000\n", true},
		{"RUN --mount from", "FROM node:24-alpine\nRUN --mount=type=bind,from=bitploy/victim:3,target=/m cat /m/x\nEXPOSE 3000\n", true},
		{"through ARG", "ARG B=bitploy/victim:3\nFROM ${B}\nEXPOSE 3000\n", true},
		{"stage name is fine", "FROM node:24-alpine AS build\nFROM node:24-alpine\nCOPY --from=build /a /a\nEXPOSE 3000\n", false},
		{"similar name is fine", "FROM mybitploy/app:1\nEXPOSE 3000\n", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := CheckDockerfile(CheckInput{Dockerfile: []byte(tc.df)})
			f, got := codes(res)[CodeBaseForbidden]
			if got != tc.want {
				t.Fatalf("base_image_forbidden = %v, want %v: %+v", got, tc.want, res.Findings)
			}
			if got && (f.Severity != "error" || f.Hint == "" || res.OK) {
				t.Errorf("finding must be an error with a hint: %+v ok=%v", f, res.OK)
			}
		})
	}
}

func TestReservedPort(t *testing.T) {
	res := CheckDockerfile(CheckInput{Dockerfile: []byte("FROM nginx:1.29-alpine\nEXPOSE 8081\n")})
	if _, ok := codes(res)[CodePortReserved]; !ok || res.OK {
		t.Errorf("port 8081 must be refused: %+v", res.Findings)
	}
}

func TestBuildArgsIsolatedBuilder(t *testing.T) {
	b := BuildSpec{ContextDir: "/ctx", Dockerfile: "/ctx/Dockerfile", Tag: "bitploy/x:3", Builder: &Builder{}}
	want := []string{"buildx", "build", "--builder", "bitploy", "--progress=plain", "--load", "--provenance=false", "--sbom=false", "-t", "bitploy/x:3", "-f", "/ctx/Dockerfile", "/ctx"}
	if got := b.BuildArgs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
}

func TestBuilderDriverOpts(t *testing.T) {
	got, err := (&Builder{CPUs: "1.5", Memory: "2g"}).driverOpts()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"network=bp-builder", "memory=2g", "memory-swap=2g", "cpu-period=100000", "cpu-quota=150000"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	if def, _ := (&Builder{}).driverOpts(); def[1] != "memory=6g" || def[4] != "cpu-quota=200000" {
		t.Errorf("defaults: %v", def)
	}
	for _, bad := range []struct{ c, m string }{{"0", "3g"}, {"abc", "3g"}, {"2", "lots"}, {"2", "0g"}, {"2", ""}} {
		if config.ValidateBuilderLimits(bad.c, bad.m) == nil {
			t.Errorf("%v must be invalid", bad)
		}
	}
	if err := config.ValidateBuilderLimits("2", "3g"); err != nil {
		t.Error(err)
	}
}

// Ensure runs against a fake docker: it creates the network and the builder
// once, leaves a working builder alone, and recreates it when it is gone or
// the limits changed.
func TestBuilderEnsure(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	state := filepath.Join(dir, "state")
	os.MkdirAll(state, 0o755)
	fake := fakeDocker(t, `echo "$@" >> `+log+`
case "$1 $2" in
  "network inspect") [ -e `+state+`/net ] || { echo "Error: No such network" >&2; exit 1; } ;;
  "network create") touch `+state+`/net ;;
  "buildx inspect") [ -e `+state+`/builder ] || { echo "no builder" >&2; exit 1; } ;;
  "buildx create") touch `+state+`/builder ;;
  "buildx rm") rm -f `+state+`/builder ;;
esac
exit 0`)
	b := &Builder{Docker: fake, ConfigDir: filepath.Join(dir, "buildx"), CPUs: "2", Memory: "3g"}
	calls := func() string { c, _ := os.ReadFile(log); return string(c) }

	if err := b.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	c := calls()
	for _, want := range []string{"network create --driver bridge --label nl.bitploy.builder=1 bp-builder", "buildx create --name bitploy --driver docker-container --bootstrap",
		"--driver-opt network=bp-builder", "--driver-opt memory=3g", "--driver-opt cpu-quota=200000"} {
		if !strings.Contains(c, want) {
			t.Errorf("missing %q in\n%s", want, c)
		}
	}

	os.Remove(log)
	if err := b.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	if c := calls(); strings.Contains(c, "buildx create") || strings.Contains(c, "network create") {
		t.Errorf("a working builder must be left alone:\n%s", c)
	}

	os.Remove(state + "/builder") // builder lost
	os.Remove(log)
	if err := b.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(calls(), "buildx create") {
		t.Errorf("a missing builder must be recreated:\n%s", calls())
	}

	b.Memory = "4g" // limits changed
	os.Remove(log)
	if err := b.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	if c := calls(); !strings.Contains(c, "buildx rm --force bitploy") || !strings.Contains(c, "memory=4g") {
		t.Errorf("changed limits must recreate the builder:\n%s", c)
	}
}

// The idle stop: a builder is stopped only after BuilderIdleTimeout without
// builds, never during one, and every prune starts a stopped builder first
// (buildx cannot prune one) and stops it again when it is idle.
func TestBuilderIdleStop(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	state := filepath.Join(dir, "state")
	os.MkdirAll(state, 0o755)
	fake := fakeDocker(t, `echo "$@" >> `+log+`
case "$1 $2" in
  "network inspect") ;;
  "buildx inspect")
    [ -e `+state+`/builder ] || { echo "no builder" >&2; exit 1; }
    if [ "$4" = "--bootstrap" ]; then touch `+state+`/running; fi
    if [ -e `+state+`/running ]; then echo "Status: running"; else echo "Status: stopped"; fi ;;
  "buildx create") touch `+state+`/builder `+state+`/running ;;
  "buildx stop") rm -f `+state+`/running ;;
  "buildx prune") [ -e `+state+`/running ] || { echo "ERROR: container is not running" >&2; exit 1; }; echo "Total: 1.5GB" ;;
esac
exit 0`)
	cfg := filepath.Join(dir, "buildx")
	now := time.Now()
	b := NewBuilder(cfg, "2", "3g")
	b.Docker = fake
	b.now = func() time.Time { return now }
	calls := func() string { c, _ := os.ReadFile(log); os.Remove(log); return string(c) }
	running := func() bool { _, err := os.Stat(state + "/running"); return err == nil }

	if stopped, err := b.StopIfIdle(t.Context()); stopped || err != nil {
		t.Fatalf("no builder yet: stopped=%v err=%v", stopped, err)
	}
	if n, err := b.Prune(t.Context(), 0); n != 0 || err != nil || strings.Contains(calls(), "buildx prune") {
		t.Fatalf("no builder, nothing to prune: %d %v", n, err)
	}

	if err := b.BeginBuild(t.Context()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(9 * time.Hour) // a long build
	if stopped, _ := b.StopIfIdle(t.Context()); stopped || !running() {
		t.Fatal("a builder in use must not be stopped")
	}
	b.EndBuild()
	now = now.Add(BuilderIdleTimeout - time.Minute)
	if stopped, _ := b.StopIfIdle(t.Context()); stopped || !running() {
		t.Fatal("stopped before the idle timeout")
	}
	now = now.Add(time.Minute)
	if stopped, err := b.StopIfIdle(t.Context()); !stopped || err != nil || running() {
		t.Fatalf("idle builder not stopped: %v %v", stopped, err)
	}
	if stopped, _ := b.StopIfIdle(t.Context()); stopped {
		t.Fatal("a stopped builder must not be stopped again")
	}

	// A prune of a stopped, idle builder starts it and stops it again.
	calls()
	n, err := b.PruneUnused(t.Context(), 7*24*time.Hour)
	if err != nil || n != 1_500_000_000 || running() {
		t.Fatalf("prune while stopped: %d %v running=%v", n, err, running())
	}
	if c := calls(); !strings.Contains(c, "buildx inspect bitploy --bootstrap") || !strings.Contains(c, "until=168h") || !strings.Contains(c, "buildx stop bitploy") {
		t.Errorf("calls:\n%s", c)
	}

	// The idle clock survives a restart of bitploy.
	b2 := NewBuilder(cfg, "2", "3g")
	if want := now.Add(-BuilderIdleTimeout); !b2.lastBuildUse.Equal(want) {
		t.Errorf("last use after restart = %v, want %v", b2.lastBuildUse, want)
	}

	// While a build runs, a prune leaves the builder running.
	if err := b.BeginBuild(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Prune(t.Context(), 10<<30); err != nil || !running() {
		t.Fatalf("prune during a build: %v running=%v", err, running())
	}
	b.EndBuild()
}
