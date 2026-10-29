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

	mu sync.Mutex
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
// configured limits, creating or repairing them when needed. It is called
// before every build; when everything is in place it costs two docker calls.
func (b *Builder) Ensure(parent context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
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
	if out, err := b.run(ctx, "network", "inspect", BuilderNetwork); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return unavailable("", err)
		}
		if out, err = b.run(ctx, "network", "create", "--driver", "bridge", "--label", BuilderLabel+"=1", BuilderNetwork); err != nil &&
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

// Prune trims the builder's BuildKit cache so that at most keepBytes stay.
// Without a builder (no build has run yet) there is nothing to prune.
func (b *Builder) Prune(ctx context.Context, keepBytes int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, err := b.run(ctx, "buildx", "inspect", BuilderName); err != nil {
		return nil
	}
	out, err := b.run(ctx, "buildx", "prune", "--builder", BuilderName, "--all", "--force",
		"--max-used-space", strconv.FormatInt(keepBytes, 10))
	if err != nil {
		return fmt.Errorf("docker buildx prune: %s", lastLine(out))
	}
	return nil
}
