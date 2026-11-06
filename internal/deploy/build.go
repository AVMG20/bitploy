package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
)

// BuildTimeout is the hard limit for one `docker buildx build`.
const BuildTimeout = 30 * time.Minute

// BuildSpec is one image build.
type BuildSpec struct {
	ContextDir string
	Dockerfile string // absolute path
	Tag        string // bitploy/<slug>:<n>
	Labels     map[string]string
	// Args are --build-arg values and Target the stage to build (compose
	// build.args and build.target).
	Args    map[string]string
	Target  string
	Timeout time.Duration // default BuildTimeout
	Docker  string        // docker binary, default "docker"
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
	keys = keys[:0]
	for k := range b.Args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--build-arg", k+"="+b.Args[k])
	}
	if b.Target != "" {
		args = append(args, "--target", b.Target)
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

// BuildOutOfMemory reports whether a failed build's log shows that it ran out
// of memory: BuildKit's ResourceExhausted, ENOMEM, or a step killed with 137.
func BuildOutOfMemory(lines []string) bool {
	for _, l := range lines {
		low := strings.ToLower(l)
		if strings.Contains(low, "cannot allocate memory") || strings.Contains(low, "resourceexhausted") ||
			strings.Contains(low, "out of memory") || strings.Contains(low, "exit code: 137") {
			return true
		}
	}
	return false
}

// MemoryLimit is the builder's memory limit as configured, such as "6g".
func (b *Builder) MemoryLimit() string { return b.memory() }

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

var goRequiresRe = regexp.MustCompile(`requires go >= ?(\d+)\.(\d+)`)

// BuildFailureHint names the fix for build failures agents keep running into,
// in plain words, or returns "" when the log shows none of them. Each hint is
// one line under 300 characters, so get_deployment shows it as the fix.
func BuildFailureHint(lines []string) string {
	for _, l := range lines {
		low := strings.ToLower(l)
		switch {
		case strings.Contains(low, "requires go >="):
			// go mod tidy or a dependency asks for a newer Go than the image has,
			// and the golang images set GOTOOLCHAIN=local, so Go stops.
			need, tag := "a newer Go", "the golang: tag"
			if m := goRequiresRe.FindStringSubmatch(low); m != nil {
				v := m[1] + "." + m[2]
				need, tag = "Go "+v+" or newer", "the tag (FROM golang:"+v+"-alpine or newer)"
			}
			return fmt.Sprintf("A Go module needs %s and the golang: image in the Dockerfile is older. Raise %s or add ENV GOTOOLCHAIN=auto to the build stage, and pin versions in go.mod with go.sum instead of running go mod tidy in the Dockerfile.", need, tag)
		case strings.Contains(low, "gyp err!") || strings.Contains(low, "node-gyp") || strings.Contains(low, "prebuild-install"):
			// A native module without a ready-made binary for the image. On
			// Alpine that is common, and installing compilers there is slow.
			return "A native Node module (better-sqlite3, bcrypt, sharp...) had no ready-made binary for this image and could not be compiled. Use FROM node:22-bookworm-slim instead of an -alpine image in every stage that installs packages; do not add compilers with apk add."
		case strings.Contains(low, "node:node") && (strings.Contains(low, "invalid user") || strings.Contains(low, "unknown user")),
			strings.Contains(low, "unable to find user node"):
			return "The image has no user named node. On an oven/bun image the unprivileged user is bun: write USER bun and chown bun:bun instead of node."
		}
	}
	return ""
}
