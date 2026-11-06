//go:build integration

package dockerx

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/ids"
)

// Needs a Docker daemon and busybox.
// Run: go test -tags=integration -count=1 -run TestExecIntegration ./internal/dockerx/
func TestExecIntegration(t *testing.T) {
	ctx := context.Background()
	c, err := New(ctx)
	if err != nil {
		t.Skip("no docker: ", err)
	}
	defer c.Close()
	if err := c.ensureInitImage(ctx); err != nil {
		t.Fatal(err)
	}
	name := "bp-test-exec-" + strings.ToLower(ids.Hex(5))
	if _, err := c.CreateContainer(ctx, RunSpec{
		Name: name, Image: VolumeInitImage, Network: "none", MemoryMB: 64, CPUMillis: 500,
		Env: []string{"SECRET=s3cr3t"}, User: "999:999", Cmd: []string{"sleep", "600"},
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.RemoveContainer(context.Background(), name) })
	if err := c.StartContainer(ctx, name); err != nil {
		t.Fatal(err)
	}
	if ok, err := c.ContainerRunning(ctx, name); !ok || err != nil {
		t.Fatalf("running = %v %v", ok, err)
	}

	// The container's variables reach the command without being on its
	// command line; stdin is streamed and closed; stdout is separate from stderr.
	var out bytes.Buffer
	in := strings.Repeat("line\n", 100000)
	tail, err := c.Exec(ctx, name, []string{"sh", "-c", `id -u; [ "$SECRET" = s3cr3t ] && echo env-ok; wc -l; echo to-stderr >&2`}, ExecOptions{
		Stdin: strings.NewReader(in), Stdout: &out,
	})
	if err != nil {
		t.Fatal(err, tail)
	}
	if got := strings.Join(strings.Fields(out.String()), " "); got != "999 env-ok 100000" {
		t.Errorf("stdout = %q", out.String())
	}
	if strings.TrimSpace(tail) != "to-stderr" {
		t.Errorf("stderr = %q", tail)
	}

	// A non-zero exit is an ExecError with the end of stderr.
	_, err = c.Exec(ctx, name, []string{"sh", "-c", "echo bad thing >&2; exit 3"}, ExecOptions{})
	var ee *ExecError
	if !errors.As(err, &ee) || ee.Code != 3 || !strings.Contains(ee.Tail, "bad thing") {
		t.Errorf("exit 3: %v", err)
	}

	// A command that stops reading early does not hang the caller.
	_, err = c.Exec(ctx, name, []string{"sh", "-c", "head -c 10 > /dev/null; exit 1"}, ExecOptions{Stdin: strings.NewReader(strings.Repeat("x", 64<<20))})
	if !errors.As(err, &ee) || ee.Code != 1 {
		t.Errorf("early stop: %v", err)
	}

	// A failing reader is an error, not the end of the input.
	_, err = c.Exec(ctx, name, []string{"cat"}, ExecOptions{Stdin: &failReader{}})
	if err == nil || !strings.Contains(err.Error(), "read failed") {
		t.Errorf("failing stdin: %v", err)
	}
	if ok, _ := c.ContainerRunning(ctx, "bp-test-exec-missing"); ok {
		t.Error("a missing container runs")
	}
}

type failReader struct{ n int }

func (f *failReader) Read(p []byte) (int, error) {
	if f.n++; f.n > 3 {
		return 0, errors.New("read failed")
	}
	return copy(p, "data\n"), nil
}
