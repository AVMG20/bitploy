package deploy

// The isolated builder. With the default `docker` buildx driver a Dockerfile can
// FROM, COPY --from or RUN --mount from the daemon's local images (other users'
// apps), and RUN steps reach the network from the daemon's namespace or docker0,
// which the firewall does not cover. So every build runs in a BuildKit container
// (driver docker-container) named buildx_buildkit_bitploy0 that sits on the
// Docker network bp-builder. That network gets a /24 from the app address pool
// (daemon.json), so deploy/firewall.sh treats it like any app: internet yes,
// host, metadata service and private ranges no. The container has CPU and
// memory limits, and the buildx state lives in <data>/buildx so it survives
// recreating the bitploy container.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/config"
)

const (
	// BuilderName is the buildx builder instance.
	BuilderName = "bitploy"
	// BuilderNetwork is the dedicated Docker network of the builder.
	BuilderNetwork = "bp-builder"
	// BuilderContainer is the BuildKit container buildx creates for the builder.
	BuilderContainer = "buildx_buildkit_bitploy0"
	// BuilderStateVolume is the Docker volume that container keeps its
	// BuildKit state in (/var/lib/buildkit): the builder's cache. Docker counts
	// it as a volume, not as build cache.
	BuilderStateVolume = BuilderContainer + "_state"
	// BuilderIdleTimeout is how long the isolated builder may stay idle before
	// bitploy stops it to release its memory. The BuildKit state volume remains.
	BuilderIdleTimeout = 8 * time.Hour
	// BuilderLabel marks the network; it is deliberately not nl.bitploy.managed,
	// so no app cleanup ever touches it.
	BuilderLabel = "nl.bitploy.builder"
)

// Builder describes and maintains the isolated buildx builder.
type Builder struct {
	Docker    string // docker binary, default "docker"
	ConfigDir string // BUILDX_CONFIG, normally <data>/buildx
	CPUs      string // default config.DefaultBuilderCPUs
	Memory    string // default config.DefaultBuilderMemory

	mu           sync.Mutex
	activeBuilds int
	lastBuildUse time.Time
	now          func() time.Time // tests replace it; nil means time.Now
}

// lastUseFile keeps the end of the last build, so a restart of bitploy does
// not reset the idle interval.
const lastUseFile = "bitploy-builder.last-use"

// NewBuilder returns the isolated builder with its idle clock read from
// ConfigDir: a builder left running before a restart of bitploy is still
// stopped once it has been idle long enough.
func NewBuilder(configDir, cpus, memory string) *Builder {
	b := &Builder{ConfigDir: configDir, CPUs: cpus, Memory: memory}
	b.lastBuildUse = b.clock()
	if configDir != "" {
		if raw, err := os.ReadFile(filepath.Join(configDir, lastUseFile)); err == nil {
			if ns, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64); err == nil && ns > 0 {
				b.lastBuildUse = time.Unix(0, ns)
			}
		}
	}
	return b
}

func (b *Builder) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

func (b *Builder) docker() string {
	if b.Docker == "" {
		return "docker"
	}
	return b.Docker
}

func (b *Builder) cpus() string {
	if b.CPUs == "" {
		return config.DefaultBuilderCPUs
	}
	return b.CPUs
}

func (b *Builder) memory() string {
	if b.Memory == "" {
		return config.DefaultBuilderMemory
	}
	return b.Memory
}

// Env is the environment for docker commands that use the builder.
func (b *Builder) Env() []string {
	env := os.Environ()
	if b.ConfigDir != "" {
		env = append(env, "BUILDX_CONFIG="+b.ConfigDir)
	}
	return env
}

// driverOpts are the --driver-opt values; they decide the optsHash.
func (b *Builder) driverOpts() ([]string, error) {
	cpus, err := strconv.ParseFloat(b.cpus(), 64)
	if err != nil || cpus <= 0 {
		return nil, fmt.Errorf("invalid builder CPU limit %q", b.cpus())
	}
	const period = 100000
	return []string{
		"network=" + BuilderNetwork,
		"memory=" + b.memory(),
		"memory-swap=" + b.memory(), // memory + swap equal: no swap
		"cpu-period=" + strconv.Itoa(period),
		"cpu-quota=" + strconv.Itoa(int(cpus*period)),
	}, nil
}

func (b *Builder) optsHash(opts []string) string {
	sum := sha256.Sum256([]byte(strings.Join(opts, "\n") + "\nnetwork-label=" + BuilderLabel))
	return hex.EncodeToString(sum[:8])
}

func (b *Builder) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, b.docker(), args...)
	cmd.Env = b.Env()
	cmd.WaitDelay = 5 * time.Second
	setChildAttrs(cmd)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

// Ensure makes sure the network and the builder exist, run and match the
// configured limits, creating or repairing them when needed. Build pipelines
// should use BeginBuild so idle shutdown also knows when they are active.
func (b *Builder) Ensure(parent context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ensureLocked(parent)
}

// BeginBuild ensures the builder is ready and marks it in use, so the idle
// stop leaves it alone. The caller calls EndBuild when its builds are done.
func (b *Builder) BeginBuild(parent context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.ensureLocked(parent); err != nil {
		return err
	}
	b.activeBuilds++
	b.lastBuildUse = b.clock()
	return nil
}

// EndBuild ends one BeginBuild; the idle interval starts when the last
// active build ends.
func (b *Builder) EndBuild() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.activeBuilds > 0 {
		b.activeBuilds--
	}
	if b.activeBuilds == 0 {
		b.lastBuildUse = b.clock()
		if b.ConfigDir != "" {
			_ = os.WriteFile(filepath.Join(b.ConfigDir, lastUseFile),
				[]byte(strconv.FormatInt(b.lastBuildUse.UnixNano(), 10)+"\n"), 0o600)
		}
	}
}

// idleLocked: no build is running and none ended in the last BuilderIdleTimeout.
func (b *Builder) idleLocked() bool {
	return b.activeBuilds == 0 && b.clock().Sub(b.lastBuildUse) >= BuilderIdleTimeout
}

// statusLocked is the builder node's status as `buildx inspect` reports it
// ("running", "stopped", "inactive", ...); "" when there is no builder or
// Docker cannot be reached.
func (b *Builder) statusLocked(ctx context.Context) string {
	out, err := b.run(ctx, "buildx", "inspect", BuilderName)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "Status:"); ok {
			return strings.TrimSpace(v)
		}
	}
	return "unknown"
}

// StopIfIdle stops the BuildKit container once no build has run for
// BuilderIdleTimeout, to release its memory. `buildx stop` keeps the builder
// and its state volume with the build cache; the next build or prune starts
// it again. It reports whether it stopped a running builder.
func (b *Builder) StopIfIdle(ctx context.Context) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.idleLocked() || b.statusLocked(ctx) != "running" {
		return false, nil
	}
	if out, err := b.run(ctx, "buildx", "stop", BuilderName); err != nil {
		return false, fmt.Errorf("docker buildx stop: %s", lastLine(out))
	}
	return true, nil
}

// pruneLocked runs `docker buildx prune` with args. buildx cannot prune a
// stopped builder, so it starts one first and stops it again afterwards when
// it is idle. Without a builder (no build has run yet) there is nothing to
// prune.
func (b *Builder) pruneLocked(ctx context.Context, args ...string) (int64, error) {
	status := b.statusLocked(ctx)
	if status == "" {
		return 0, nil
	}
	if status != "running" {
		if out, err := b.run(ctx, "buildx", "inspect", BuilderName, "--bootstrap"); err != nil {
			return 0, fmt.Errorf("start the builder to prune it: %s", lastLine(out))
		}
	}
	out, err := b.run(ctx, append([]string{"buildx", "prune", "--builder", BuilderName, "--all", "--force"}, args...)...)
	if err != nil {
		err = fmt.Errorf("docker buildx prune: %s", lastLine(out))
	}
	if b.idleLocked() {
		if sout, serr := b.run(ctx, "buildx", "stop", BuilderName); serr != nil {
			err = errors.Join(err, fmt.Errorf("docker buildx stop: %s", lastLine(sout)))
		}
	}
	if err != nil {
		return 0, err
	}
	return PruneTotal(out), nil
}

func (b *Builder) ensureLocked(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()

	unavailable := func(why string, err error) error {
		if errors.Is(err, exec.ErrNotFound) {
			return apperr.Unavailable("build_unavailable",
				"This server cannot run builds: the docker command was not found.",
				"Tell the bitploy admin that the Docker CLI with the buildx plugin is missing.")
		}
		if parent.Err() != nil {
			return parent.Err()
		}
		return apperr.Unavailable("builder_unavailable",
			"The isolated build environment could not be set up: "+apperr.Redact(why),
			"This is a server problem, not your app. Try again in a minute; if it repeats, tell the bitploy admin.")
	}

	opts, err := b.driverOpts()
	if err != nil {
		return unavailable(err.Error(), err)
	}
	if b.ConfigDir != "" {
		if err := os.MkdirAll(b.ConfigDir, 0o700); err != nil {
			return unavailable(err.Error(), err)
		}
	}
	hashFile := filepath.Join(b.ConfigDir, "bitploy-builder.opts")
	want := b.optsHash(opts)

	// 1. The network.
	if _, err := b.run(ctx, "network", "inspect", BuilderNetwork); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return unavailable("", err)
		}
		if out, err := b.run(ctx, "network", "create", "--driver", "bridge", "--label", BuilderLabel+"=1", BuilderNetwork); err != nil &&
			!strings.Contains(out, "already exists") {
			return unavailable("creating the network "+BuilderNetwork+" failed: "+lastLine(out), err)
		}
	}

	// 2. The builder: keep it if it exists, has the same limits and boots.
	have, _ := os.ReadFile(hashFile)
	if b.ConfigDir == "" || strings.TrimSpace(string(have)) == want {
		if _, err := b.run(ctx, "buildx", "inspect", BuilderName, "--bootstrap"); err == nil {
			return nil
		}
	}
	// Missing, broken or configured differently: start over.
	_, _ = b.run(ctx, "buildx", "rm", "--force", BuilderName)
	_, _ = b.run(ctx, "rm", "-f", BuilderContainer)
	args := []string{"buildx", "create", "--name", BuilderName, "--driver", "docker-container", "--bootstrap"}
	for _, o := range opts {
		args = append(args, "--driver-opt", o)
	}
	if out, err := b.run(ctx, args...); err != nil {
		return unavailable("creating the builder failed: "+lastLine(out), err)
	}
	if b.ConfigDir != "" {
		_ = os.WriteFile(hashFile, []byte(want+"\n"), 0o600)
	}
	return nil
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

// Prune trims the builder's BuildKit cache so that at most keepBytes stay;
// keepBytes 0 deletes all of it. It returns the bytes buildx reports as freed
// (0 when it does not say). Without a builder (no build has run yet) there
// is nothing to prune.
func (b *Builder) Prune(ctx context.Context, keepBytes int64) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var args []string
	if keepBytes > 0 {
		args = append(args, "--max-used-space", strconv.FormatInt(keepBytes, 10))
	}
	return b.pruneLocked(ctx, args...)
}

// PruneUnused deletes the builder's cache that no build used in the last
// unusedFor (BuildKit's last-used time): the layers of an app that has not
// been built for a week go, those of apps that build often stay. It returns
// the bytes buildx reports as freed.
func (b *Builder) PruneUnused(ctx context.Context, unusedFor time.Duration) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.pruneLocked(ctx, "--filter", fmt.Sprintf("until=%dh", int(unusedFor.Hours())))
}

// PruneTotal reads the "Total: 1.2GB" line that `docker buildx prune` ends
// with; 0 when there is none.
func PruneTotal(out string) int64 {
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "Total:"); ok {
			if n, ok := ParseSize(strings.TrimSpace(v)); ok {
				return n
			}
		}
	}
	return 0
}

// ParseSize reads a size as Docker prints it: "1.23GB", "512kB", "0B" (powers
// of 1000) or "1.5GiB" (powers of 1024).
func ParseSize(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.') {
		i++
	}
	num, err := strconv.ParseFloat(s[:i], 64)
	if err != nil || num < 0 {
		return 0, false
	}
	units := map[string]float64{
		"": 1, "B": 1,
		"KB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12, "PB": 1e15,
		"KIB": 1 << 10, "MIB": 1 << 20, "GIB": 1 << 30, "TIB": 1 << 40, "PIB": 1 << 50,
	}
	mul, ok := units[strings.ToUpper(strings.TrimSpace(s[i:]))]
	if !ok {
		return 0, false
	}
	return int64(num * mul), true
}
