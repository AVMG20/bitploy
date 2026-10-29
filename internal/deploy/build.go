package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
)

// BuildTimeout is the hard limit for one `docker buildx build`.
const BuildTimeout = 15 * time.Minute

// BuildSpec is one image build.
type BuildSpec struct {
	ContextDir string
	Dockerfile string // absolute path
	Tag        string // bitploy/<slug>:<n>
	Labels     map[string]string
	Timeout    time.Duration // default BuildTimeout
	Docker     string        // docker binary, default "docker"
	// Builder, when set, runs the build in the isolated builder (see builder.go)
	// instead of the daemon's default one. The caller must have called Ensure.
	Builder *Builder
}

// ErrBuildTimeout is returned when the build ran past its timeout.
var ErrBuildTimeout = errors.New("build timed out")

// BuildArgs returns the argument list for `docker`.
func (b BuildSpec) BuildArgs() []string {
	args := []string{"buildx", "build"}
	if b.Builder != nil {
		args = append(args, "--builder", BuilderName)
	}
	args = append(args, "--progress=plain", "--load", "--provenance=false", "--sbom=false", "-t", b.Tag, "-f", b.Dockerfile)
	keys := make([]string, 0, len(b.Labels))
	for k := range b.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--label", k+"="+b.Labels[k])
	}
	return append(args, b.ContextDir)
}

// RunBuild runs the build and feeds every output line (stdout and stderr
// merged) to onLine. It returns nil on success, ErrBuildTimeout past the
// timeout, ctx's error when cancelled, or the exit error.
func RunBuild(ctx context.Context, b BuildSpec, onLine func(string)) error {
	timeout := b.Timeout
	if timeout <= 0 {
		timeout = BuildTimeout
	}
	bin := b.Docker
	if bin == "" {
		bin = "docker"
	}
	bctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(bctx, bin, b.BuildArgs()...)
	env := os.Environ()
	if b.Builder != nil {
		env = b.Builder.Env()
	}
	cmd.Env = append(env, "BUILDKIT_PROGRESS=plain", "DOCKER_CLI_HINTS=false")
	cmd.WaitDelay = 10 * time.Second
	setChildAttrs(cmd)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		pw.Close()
		if errors.Is(err, exec.ErrNotFound) {
			return apperr.Unavailable("build_unavailable",
				"This server cannot run builds: the docker command was not found.",
				"Tell the bitploy admin that the Docker CLI with the buildx plugin is missing.")
		}
		return err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = ReadLines(pr, 8192, onLine)
	}()
	err := cmd.Wait()
	pw.Close()
	<-done
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.Is(bctx.Err(), context.DeadlineExceeded):
			return ErrBuildTimeout
		}
		return fmt.Errorf("docker buildx build failed: %w", err)
	}
	return nil
}

// BuildFailureSummary picks the most useful single line from a failed build's
// output: the last line that looks like an error, else the last line.
func BuildFailureSummary(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		low := strings.ToLower(l)
		if strings.Contains(low, "error") || strings.Contains(low, "failed") || strings.Contains(low, "not found") {
			return strings.TrimLeft(l, "#0123456789. ")
		}
	}
	if len(lines) > 0 {
		return strings.TrimSpace(lines[len(lines)-1])
	}
	return ""
}
