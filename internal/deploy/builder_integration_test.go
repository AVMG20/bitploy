//go:build integration

package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Needs a Docker daemon with the buildx plugin and internet access (busybox pull).
// Run: go test -tags=integration -count=1 -run Builder ./internal/deploy/
func TestIsolatedBuilderIntegration(t *testing.T) {
	docker := func(args ...string) (string, error) {
		out, err := exec.Command("docker", args...).CombinedOutput()
		return string(out), err
	}
	if _, err := docker("version"); err != nil {
		t.Skip("no docker")
	}
	b := &Builder{ConfigDir: filepath.Join(t.TempDir(), "buildx"), CPUs: "1", Memory: "1g"}
	if err := b.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	if out, err := docker("inspect", BuilderContainer, "--format", "{{.HostConfig.Memory}} {{.HostConfig.CpuQuota}} {{json .NetworkSettings.Networks}}"); err != nil ||
		!strings.HasPrefix(out, "1073741824 100000 ") || !strings.Contains(out, BuilderNetwork) {
		t.Fatalf("builder container: %v %s", err, out)
	}
	if err := b.Ensure(t.Context()); err != nil { // second call: nothing to do
		t.Fatal(err)
	}

	// A victim image in the daemon's local store must not be visible to a build.
	if out, err := docker("pull", "-q", "busybox:1.37"); err != nil {
		t.Skipf("cannot pull busybox: %s", out)
	}
	const victim = "bitploy/bp-test-victim:1"
	docker("tag", "busybox:1.37", victim)
	t.Cleanup(func() { docker("rmi", victim, "bitploy/bp-test-ok:1") })

	dir := t.TempDir()
	run := func(dockerfile, tag string) (string, error) {
		os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0o644)
		var lines []string
		err := RunBuild(t.Context(), BuildSpec{ContextDir: dir, Dockerfile: filepath.Join(dir, "Dockerfile"), Tag: tag, Builder: b},
			func(l string) { lines = append(lines, l) })
		return strings.Join(lines, "\n"), err
	}
	if out, err := run("FROM busybox:1.37\nRUN echo hello\n", "bitploy/bp-test-ok:1"); err != nil {
		t.Fatalf("normal build failed: %v\n%s", err, out)
	}
	if out, err := docker("image", "inspect", "bitploy/bp-test-ok:1"); err != nil {
		t.Fatalf("image not loaded into the daemon: %s", out)
	}
	if out, err := run("FROM "+victim+"\nRUN echo stolen\n", "bitploy/bp-test-evil:1"); err == nil {
		docker("rmi", "bitploy/bp-test-evil:1")
		t.Fatalf("build from a local bitploy image must fail:\n%s", out)
	}
	if _, err := b.Prune(t.Context(), 1<<30); err != nil {
		t.Errorf("prune: %v", err)
	}
}
