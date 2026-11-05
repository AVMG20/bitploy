package dockerx

import (
	"context"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// Container limits from SPEC "Container defaults".
const (
	PidsLimit  = 256
	NoFileSoft = 4096
	DataMount  = "/data"
)

// IsNotFound reports whether err is a Docker "no such object" error.
func IsNotFound(err error) bool { return err != nil && cerrdefs.IsNotFound(err) }

// IsConflict reports whether err is a Docker "already exists / in use" error.
func IsConflict(err error) bool { return err != nil && cerrdefs.IsConflict(err) }

// ManagedLabels are set on every container, network, image and volume bitploy owns.
func ManagedLabels(appID, slug string) map[string]string {
	return map[string]string{LabelManaged: "1", LabelAppID: appID, LabelSlug: slug}
}

// AppCapabilities are the only Linux capabilities an app container keeps
// (everything else, including NET_RAW and MKNOD, is dropped).
var AppCapabilities = []string{"CHOWN", "DAC_OVERRIDE", "FOWNER", "FSETID", "KILL", "SETGID", "SETUID", "NET_BIND_SERVICE"}

// RunSpec describes one app container.
type RunSpec struct {
	Name      string
	Image     string
	Env       []string
	Labels    map[string]string
	Network   string
	MemoryMB  int64
	CPUMillis int64
	Volume    string // named volume mounted at /data; empty = no volume
	// Mounts are further named volumes (services with volumes elsewhere than /data).
	Mounts []VolumeMount
	// Aliases are extra DNS names on Network, like the service name.
	Aliases []string
	// Cmd and Entrypoint override the image's when set.
	Cmd        []string
	Entrypoint []string
	// Healthcheck overrides the image's HEALTHCHECK when set.
	Healthcheck *container.HealthConfig
	// User and WorkingDir override the image's when set (compose user, working_dir).
	User       string
	WorkingDir string
	// Extra are further networks the container joins before it starts, like
	// its project's network (bp-p-<id>) with the app's short name as alias.
	Extra []NetworkAttach `json:",omitempty"`
	// ShmMB is the size of /dev/shm; 0 keeps Docker's default (64 MB).
	ShmMB int64 `json:",omitempty"`
}

// NetworkAttach is a network a container joins, with its DNS aliases there.
type NetworkAttach struct {
	Network string
	Aliases []string
}

// VolumeMount is a named Docker volume mounted at Target.
type VolumeMount struct {
	Source string
	Target string
}

// CreateContainer creates (does not start) a locked-down app container:
// memory = memory+swap, CPU quota, pids/nofile limits, no-new-privileges,
// all capabilities dropped except AppCapabilities, json-file logs 10m x 3, restart unless-stopped, no
// published ports, no host mounts (named volumes only).
func (c *Client) CreateContainer(ctx context.Context, s RunSpec) (string, error) {
	mem := s.MemoryMB * 1024 * 1024
	pids := int64(PidsLimit)
	hc := &container.HostConfig{
		NetworkMode: container.NetworkMode(s.Network),
		Resources: container.Resources{
			Memory:     mem,
			MemorySwap: mem,
			NanoCPUs:   s.CPUMillis * 1_000_000,
			PidsLimit:  &pids,
			Ulimits:    []*container.Ulimit{{Name: "nofile", Soft: NoFileSoft, Hard: NoFileSoft}},
		},
		SecurityOpt:   []string{"no-new-privileges"},
		CapDrop:       []string{"ALL"},
		CapAdd:        append([]string(nil), AppCapabilities...),
		LogConfig:     container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "3"}},
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
	}
	if s.ShmMB > 0 {
		hc.ShmSize = s.ShmMB * 1024 * 1024
	}
	if s.Volume != "" {
		hc.Mounts = []mount.Mount{{Type: mount.TypeVolume, Source: s.Volume, Target: DataMount}}
	}
	for _, m := range s.Mounts {
		hc.Mounts = append(hc.Mounts, mount.Mount{Type: mount.TypeVolume, Source: m.Source, Target: m.Target})
	}
	cfg := &container.Config{Image: s.Image, Env: s.Env, Labels: s.Labels, Healthcheck: s.Healthcheck, User: s.User, WorkingDir: s.WorkingDir}
	if len(s.Cmd) > 0 {
		cfg.Cmd = s.Cmd
	}
	if len(s.Entrypoint) > 0 {
		cfg.Entrypoint = s.Entrypoint
	}
	endpoint := &network.EndpointSettings{}
	if len(s.Aliases) > 0 {
		endpoint.Aliases = append([]string(nil), s.Aliases...)
	}
	// Everything bitploy manages carries this installation's id, so the
	// reconciler of another installation on the same daemon leaves it alone.
	if s.Labels[LabelManaged] == "1" && c.Instance != "" && s.Labels[LabelInstance] == "" {
		labels := make(map[string]string, len(s.Labels)+1)
		for k, v := range s.Labels {
			labels[k] = v
		}
		labels[LabelInstance] = c.Instance
		s.Labels = labels
	}
	res, err := c.API.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name:       s.Name,
		Config:     cfg,
		HostConfig: hc,
		NetworkingConfig: &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{s.Network: endpoint},
		},
	})
	if err != nil {
		return "", err
	}
	return res.ID, nil
}

func (c *Client) StartContainer(ctx context.Context, name string) error {
	_, err := c.API.ContainerStart(ctx, name, client.ContainerStartOptions{})
	return err
}

// StopContainerGrace stops a container gracefully (SIGTERM, then SIGKILL after
// timeoutSec). A missing or already stopped container is not an error.
func (c *Client) StopContainerGrace(ctx context.Context, name string, timeoutSec int) error {
	_, err := c.API.ContainerStop(ctx, name, client.ContainerStopOptions{Timeout: &timeoutSec})
	if IsNotFound(err) {
		return nil
	}
	return err
}

// RemoveContainer force-removes a container; a missing one is not an error.
func (c *Client) RemoveContainer(ctx context.Context, name string) error {
	_, err := c.API.ContainerRemove(ctx, name, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
	if IsNotFound(err) {
		return nil
	}
	return err
}

// InspectContainer returns the container, or an error satisfying IsNotFound.
func (c *Client) InspectContainer(ctx context.Context, name string) (container.InspectResponse, error) {
	res, err := c.API.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	return res.Container, err
}

// ContainerExists reports whether a container with this name exists.
func (c *Client) ContainerExists(ctx context.Context, name string) (bool, error) {
	_, err := c.InspectContainer(ctx, name)
	if IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

// ListManagedContainers lists every container (running or not) with the managed label.
func (c *Client) ListManagedContainers(ctx context.Context) ([]container.Summary, error) {
	f, err := c.ownedFilter()
	if err != nil {
		return nil, err
	}
	res, err := c.API.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: f})
	if err != nil {
		return nil, err
	}
	return keepOwned(c, res.Items, func(s container.Summary) map[string]string { return s.Labels }), nil
}

// keepOwned drops items that do not carry this installation's labels (a second
// line of defence behind the daemon-side filter).
func keepOwned[T any](c *Client, items []T, labels func(T) map[string]string) []T {
	out := items[:0:0]
	for _, it := range items {
		if c.owned(labels(it)) {
			out = append(out, it)
		}
	}
	return out
}

// CountForeign counts managed containers that belong to another installation
// or to a version before instance ids. They are never touched; callers log it once.
func (c *Client) CountForeign(ctx context.Context) (int, error) {
	res, err := c.API.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: client.Filters{}.Add("label", LabelManaged+"=1")})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, it := range res.Items {
		if it.Labels[LabelAppID] != "" && !c.owned(it.Labels) {
			n++
		}
	}
	return n, nil
}

// ListAppContainers lists every managed container of one app.
func (c *Client) ListAppContainers(ctx context.Context, appID string) ([]container.Summary, error) {
	f, err := c.appFilter(appID)
	if err != nil {
		return nil, err
	}
	res, err := c.API.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: f})
	if err != nil {
		return nil, err
	}
	out := res.Items[:0:0]
	for _, it := range res.Items {
		if c.ownedOrLegacy(it.Labels) {
			out = append(out, it)
		}
	}
	return out, nil
}

// SummaryName returns the container name without the leading slash.
func SummaryName(s container.Summary) string {
	if len(s.Names) == 0 {
		return ""
	}
	return strings.TrimPrefix(s.Names[0], "/")
}

// DescribeError turns a Docker error into one plain line for messages.
func DescribeError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	msg = strings.TrimPrefix(msg, "Error response from daemon: ")
	return msg
}
