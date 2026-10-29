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
}

// CreateContainer creates (does not start) a locked-down app container:
// memory = memory+swap, CPU quota, pids/nofile limits, no-new-privileges,
// NET_RAW dropped, json-file logs 10m x 3, restart unless-stopped, no
// published ports, no host mounts.
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
		CapDrop:       []string{"NET_RAW"},
		LogConfig:     container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "3"}},
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
	}
	if s.Volume != "" {
		hc.Mounts = []mount.Mount{{Type: mount.TypeVolume, Source: s.Volume, Target: DataMount}}
	}
	res, err := c.API.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name:       s.Name,
		Config:     &container.Config{Image: s.Image, Env: s.Env, Labels: s.Labels},
		HostConfig: hc,
		NetworkingConfig: &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{s.Network: {}},
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
	res, err := c.API.ContainerList(ctx, client.ContainerListOptions{
		All: true, Filters: client.Filters{}.Add("label", LabelManaged+"=1"),
	})
	return res.Items, err
}

// ListAppContainers lists every managed container of one app.
func (c *Client) ListAppContainers(ctx context.Context, appID string) ([]container.Summary, error) {
	res, err := c.API.ContainerList(ctx, client.ContainerListOptions{
		All: true, Filters: client.Filters{}.Add("label", LabelManaged+"=1", LabelAppID+"="+appID),
	})
	return res.Items, err
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
