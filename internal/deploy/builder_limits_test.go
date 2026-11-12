package deploy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loggingDocker is a docker binary that logs its arguments, one call per
// line, and succeeds.
func loggingDocker(t *testing.T) (bin, log string) {
	t.Helper()
	log = filepath.Join(t.TempDir(), "calls")
	return fakeDocker(t, `echo "$*" >> '`+log+`'`), log
}

// calls returns the logged calls since the last one and clears the log.
func calls(t *testing.T, log string) string {
	t.Helper()
	raw, _ := os.ReadFile(log)
	_ = os.Remove(log)
	return string(raw)
}

func TestBuilderLimitsFromSettings(t *testing.T) {
	ctx := context.Background()
	bin, log := loggingDocker(t)
	b := NewBuilder(t.TempDir(), "2", "6g")
	b.Docker = bin
	cpus, mem := "", ""
	b.Limits = func(context.Context) (string, string) { return cpus, mem }

	// First start: no hash yet, so the builder is created with the env limits.
	if err := b.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	got := calls(t, log)
	if !strings.Contains(got, "--driver-opt memory=6g") || !strings.Contains(got, "cpu-quota=200000") {
		t.Fatalf("first create:\n%s", got)
	}
	if b.MemoryLimit() != "6 GB" {
		t.Errorf("MemoryLimit = %q", b.MemoryLimit())
	}

	// Same limits: kept.
	if err := b.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if got := calls(t, log); strings.Contains(got, "buildx create") || strings.Contains(got, "buildx rm") {
		t.Fatalf("recreated with the same limits:\n%s", got)
	}

	// An admin sets other limits: recreated, keeping the build cache.
	cpus, mem = "1.5", "3072m"
	if err := b.BeginBuild(ctx); err != nil {
		t.Fatal(err)
	}
	got = calls(t, log)
	if !strings.Contains(got, "buildx rm --force bitploy --keep-state") || !strings.Contains(got, "--driver-opt memory=3072m") ||
		!strings.Contains(got, "cpu-quota=150000") {
		t.Fatalf("new limits:\n%s", got)
	}
	if b.MemoryLimit() != "3 GB" {
		t.Errorf("MemoryLimit = %q", b.MemoryLimit())
	}

	// While a build runs, other limits wait: the builder is not recreated.
	mem = "1536m"
	if err := b.BeginBuild(ctx); err != nil {
		t.Fatal(err)
	}
	if got := calls(t, log); strings.Contains(got, "buildx create") || strings.Contains(got, "buildx rm") {
		t.Fatalf("recreated while a build runs:\n%s", got)
	}
	if b.MemoryLimit() != "3 GB" {
		t.Errorf("MemoryLimit while waiting = %q, want the running builder's", b.MemoryLimit())
	}
	b.EndBuild()
	b.EndBuild()

	// The next build that starts alone applies them.
	if err := b.BeginBuild(ctx); err != nil {
		t.Fatal(err)
	}
	defer b.EndBuild()
	if got := calls(t, log); !strings.Contains(got, "--driver-opt memory=1536m") {
		t.Fatalf("waiting limits not applied:\n%s", got)
	}
	if b.MemoryLimit() != "1536 MB" {
		t.Errorf("MemoryLimit = %q", b.MemoryLimit())
	}

	// Invalid limits from the hook are ignored.
	cpus = "lots"
	if err := b.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
}
