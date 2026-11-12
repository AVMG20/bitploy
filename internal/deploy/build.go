package deploy

import (
	"cmp"
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
	"github.com/AVMG20/bitploy/internal/config"
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
	Args   map[string]string
	Target string
	// Secrets are BuildKit secrets, name to value: the build sees one only in
	// a RUN step that mounts it (RUN --mount=type=secret,id=NAME,env=NAME),
	// never in the image or its history. The values are not on the command
	// line either: BuildArgs names an environment variable of the docker
	// process (SecretEnv).
	Secrets map[string]string
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
	for i, k := range b.secretNames() {
		args = append(args, "--secret", "id="+k+",env="+secretEnvName(i))
	}
	if b.Target != "" {
		args = append(args, "--target", b.Target)
	}
	return append(args, b.ContextDir)
}

func (b BuildSpec) secretNames() []string {
	keys := make([]string, 0, len(b.Secrets))
	for k := range b.Secrets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// secretEnvName is the environment variable of the docker process that holds
// the i-th secret. It is not the secret's own name, so a secret called PATH
// or DOCKER_HOST cannot change how docker runs; no app variable starts with
// BITPLOY_ (reserved).
func secretEnvName(i int) string { return fmt.Sprintf("BITPLOY_BUILD_SECRET_%d", i) }

// SecretEnv is the environment the docker process needs for the secrets that
// BuildArgs names.
func (b BuildSpec) SecretEnv() []string {
	out := make([]string, 0, len(b.Secrets))
	for i, k := range b.secretNames() {
		out = append(out, secretEnvName(i)+"="+b.Secrets[k])
	}
	return out
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
	cmd.Env = append(append(env, b.SecretEnv()...), "BUILDKIT_PROGRESS=plain", "DOCKER_CLI_HINTS=false")
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

// MemoryLimit is the memory limit of the builder that runs, such as "6 GB"
// or "1536 MB"; the configured one before it ran.
func (b *Builder) MemoryLimit() string {
	b.mu.Lock()
	mem := cmp.Or(b.applied, b.memory())
	b.mu.Unlock()
	mb := config.BuilderMemoryMB(mem)
	switch {
	case mb <= 0:
		return mem
	case mb%1024 == 0:
		return fmt.Sprintf("%d GB", mb/1024)
	default:
		return fmt.Sprintf("%d MB", mb)
	}
}

// BuildFailureSummary picks the most useful single line from a failed build's
// output: the cause, like a compiler message, rather than Docker's wrapper
// around it ("process ... did not complete successfully: exit code: 1").
//
// It looks in the step that failed first (buildkit prefixes every line of
// step N with "#N " and ends a failed step with "#N ERROR: process ..."), for
// the first line that names an error; a header like "error during build:"
// gives way to the line after it. With no such line it takes the last output
// of that step, then the first error line before the wrapper, then the last
// line that looks like an error, else the last line.
func BuildFailureSummary(lines []string) string {
	clean := make([]string, len(lines))
	for i, l := range lines {
		clean[i] = buildStepPrefixRe.ReplaceAllString(strings.TrimSpace(l), "")
	}
	end, step := len(lines), ""
	for i, l := range lines {
		if isBuildWrapper(l) {
			end = i
			if m := buildStepRe.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
				step = m[1]
			}
			break
		}
	}
	inStep := func(i int) bool { return step == "" || strings.HasPrefix(strings.TrimSpace(lines[i]), "#"+step+" ") }
	firstError := func(stepOnly bool) string {
		for i := 0; i < end; i++ {
			if (stepOnly && !inStep(i)) || !isBuildErrorLine(clean[i]) {
				continue
			}
			if strings.HasSuffix(clean[i], ":") {
				for j := i + 1; j < end; j++ {
					if stepOnly && !inStep(j) || clean[j] == "" {
						continue
					}
					if !isBuildNoise(clean[j]) {
						return shortLine(clean[j])
					}
					break
				}
			}
			return shortLine(clean[i])
		}
		return ""
	}
	if step != "" {
		if l := firstError(true); l != "" {
			return l
		}
		for i := end - 1; i >= 0; i-- {
			if inStep(i) && clean[i] != "" && !isBuildNoise(clean[i]) && !buildStepHeaderRe.MatchString(clean[i]) {
				return shortLine(clean[i])
			}
		}
	}
	if l := firstError(false); l != "" {
		return l
	}
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		low := strings.ToLower(l)
		if strings.Contains(low, "error") || strings.Contains(low, "failed") || strings.Contains(low, "not found") {
			return shortLine(strings.TrimLeft(l, "#0123456789. "))
		}
	}
	if len(lines) > 0 {
		return shortLine(strings.TrimSpace(lines[len(lines)-1]))
	}
	return ""
}

var (
	// "#9 " or "#9 3.950 " in front of a buildkit line.
	buildStepPrefixRe = regexp.MustCompile(`^#\d+ (?:\d+\.\d+ )?`)
	buildStepRe       = regexp.MustCompile(`^#(\d+) `)
	// "[build 6/6] RUN npm run build", the first line of a step.
	buildStepHeaderRe = regexp.MustCompile(`^\[[^\]]*\d+/\d+\] `)
	buildErrorRe      = regexp.MustCompile(`(?i)(error\b|err!|\bfailed\b|\bfatal\b|\bpanic\b|\bcannot\b|\bcan't\b|\bcould not\b|\bunable to\b|\bnot found\b|\bnot exported\b|\bno such file\b|\bundefined\b|permission denied)`)
	buildNoiseRe      = regexp.MustCompile(`(?i)^(at |-{3,}$|> \[|dockerfile\S*:\d+$|(>>>\s*)?\d+\s*\|| *\^|(npm )?warn(ing)?\b|debconf:|npm (error|err!)( (code|path|errno|syscall|command|workspace|location|signal|cwd|a complete log|lifecycle script|exit status|failed at)\b|$)|done \d|cached$)`)
	buildExitCodeRe   = regexp.MustCompile(`exit code: \d+$`)
)

// isBuildWrapper tells Docker's line about a failed step, which only repeats
// the command and its exit code.
func isBuildWrapper(l string) bool {
	return strings.Contains(l, "did not complete successfully") || strings.Contains(l, "executor failed running") || buildExitCodeRe.MatchString(strings.TrimSpace(l))
}

func isBuildNoise(l string) bool { return isBuildWrapper(l) || buildNoiseRe.MatchString(l) }

func isBuildErrorLine(l string) bool {
	return l != "" && !isBuildNoise(l) && buildErrorRe.MatchString(l)
}

// shortLine keeps a summary line under 300 characters.
func shortLine(l string) string {
	if r := []rune(l); len(r) > 300 {
		return string(r[:299]) + "…"
	}
	return l
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
