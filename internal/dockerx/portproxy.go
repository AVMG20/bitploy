package dockerx

// Public ports: the forwarder container of an app's public port. It runs
// `bitploy port-proxy` (internal/portproxy) from the same image as the
// database helper (busybox with the bitploy binary), joins only the app's own
// network bp-<slug>, and is the only container bitploy ever publishes a port
// for. The app container itself keeps no published ports, so turning a public
// port on or off never restarts the app.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

const (
	// LabelPublicPort is on a forwarder container: the id of the app it serves.
	// Forwarders carry no nl.bitploy.app_id, so the app container cleanup and
	// the event watcher leave them alone.
	LabelPublicPort = "nl.bitploy.public_port"
	// labelPublicPortHash is a hash of the forwarder's settings, to tell
	// whether a running forwarder is still the one that is wanted.
	labelPublicPortHash = "nl.bitploy.public_port_hash"
	// PortProxyPrefix starts the name of every forwarder container.
	PortProxyPrefix = "bitploy-port-"

	portProxyUser   = "65534:65534" // nobody
	portProxyMemory = 64 << 20
	portProxyPids   = 64
)

// ErrPortInUse is returned when the host port is taken by something else.
var ErrPortInUse = errors.New("the host port is already in use")

// PortProxyName is the container name of an app's forwarder.
func PortProxyName(appID string) string { return PortProxyPrefix + appID }

// PortProxySpec is what a forwarder does.
type PortProxySpec struct {
	AppID    string
	Slug     string
	Network  string   // the app's own network, bp-<slug>
	HostPort int      // published on 0.0.0.0 (IPv4 only); the forwarder listens on the same port inside
	Target   string   // <alias>:<port> on Network, like app:5432
	Allow    []string // CIDRs; empty: every address
	Until    int64    // unix seconds at which it closes by itself; 0: never
}

// PortProxyInfo is a forwarder container that exists.
type PortProxyInfo struct {
	Name    string
	AppID   string
	Running bool
}

func (s PortProxySpec) cmd() []string {
	c := []string{dbHelperBinary, "port-proxy", "--listen", ":" + strconv.Itoa(s.HostPort), "--target", s.Target}
	if len(s.Allow) > 0 {
		c = append(c, "--allow", strings.Join(s.Allow, ","))
	}
	if s.Until > 0 {
		c = append(c, "--until", strconv.FormatInt(s.Until, 10))
	}
	return c
}

func (s PortProxySpec) hash(image string) string {
	h := sha256.Sum256([]byte(strings.Join(append([]string{image, s.Network}, s.cmd()...), "\x00")))
	return hex.EncodeToString(h[:12])
}

// EnsurePortProxy makes the forwarder of spec run: an existing one with the
// same settings is kept (and started when it stopped), anything else is
// replaced. It returns ErrPortInUse when the host port is taken on the host.
func (c *Client) EnsurePortProxy(ctx context.Context, s PortProxySpec) error {
	if s.AppID == "" || s.HostPort <= 0 || s.Target == "" || s.Network == "" {
		return errors.New("dockerx: incomplete public port")
	}
	image, err := c.ensureDBHelperImage(ctx)
	if err != nil {
		return err
	}
	// The app network exists once the app was deployed; make it for an app
	// that never ran, so the forwarder can start (it answers nothing then).
	if err := c.EnsureNetwork(ctx, s.Network, c.ManagedLabels(s.AppID, s.Slug)); err != nil {
		return err
	}
	name := PortProxyName(s.AppID)
	want := s.hash(image)
	if info, err := c.InspectContainer(ctx, name); err == nil {
		if info.Config != nil && info.Config.Labels[labelPublicPortHash] == want && c.owned(info.Config.Labels) {
			if info.State != nil && info.State.Running {
				return nil
			}
			if err := c.StartContainer(ctx, name); err == nil {
				return nil
			}
		}
		if err := c.RemoveContainer(ctx, name); err != nil {
			return err
		}
	} else if !IsNotFound(err) {
		return err
	}

	labels := map[string]string{LabelManaged: "1", LabelPublicPort: s.AppID, LabelSlug: s.Slug, labelPublicPortHash: want}
	if c.Instance != "" {
		labels[LabelInstance] = c.Instance
	}
	port, err := network.ParsePort(strconv.Itoa(s.HostPort) + "/tcp")
	if err != nil {
		return err
	}
	pids := int64(portProxyPids)
	_, err = c.API.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: name,
		Config: &container.Config{
			Image: image, User: portProxyUser, Labels: labels, Cmd: s.cmd(),
			Env:          []string{"GOMAXPROCS=1", "GOMEMLIMIT=40MiB"},
			ExposedPorts: network.PortSet{port: {}},
		},
		HostConfig: &container.HostConfig{
			NetworkMode: container.NetworkMode(s.Network),
			// IPv4 only: an IPv6 client would reach the forwarder through
			// docker-proxy with the bridge address as its source, which the
			// allow list and the firewall could not tell apart.
			PortBindings: network.PortMap{port: {{HostIP: netip.IPv4Unspecified(), HostPort: strconv.Itoa(s.HostPort)}}},
			// Nothing to keep: no capabilities (the port is above 1024), no
			// writes, no new privileges.
			CapDrop:        []string{"ALL"},
			SecurityOpt:    []string{"no-new-privileges"},
			ReadonlyRootfs: true,
			Resources:      container.Resources{Memory: portProxyMemory, MemorySwap: portProxyMemory, PidsLimit: &pids, NanoCPUs: 250_000_000},
			LogConfig:      container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "1m", "max-file": "2"}},
			RestartPolicy:  container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
		},
		NetworkingConfig: &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{s.Network: {}},
		},
	})
	if err != nil {
		return err
	}
	if err := c.StartContainer(ctx, name); err != nil {
		_ = c.RemoveContainer(context.WithoutCancel(ctx), name)
		if portTaken(err) {
			return fmt.Errorf("%w: %s", ErrPortInUse, DescribeError(err))
		}
		return err
	}
	return nil
}

func portTaken(err error) bool {
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "port is already allocated") || strings.Contains(m, "address already in use")
}

// RemovePortProxy removes the forwarder of an app; a missing one is fine.
func (c *Client) RemovePortProxy(ctx context.Context, appID string) error {
	return c.RemoveContainer(ctx, PortProxyName(appID))
}

// ListPortProxies lists the forwarder containers of this installation.
func (c *Client) ListPortProxies(ctx context.Context) ([]PortProxyInfo, error) {
	f, err := c.ownedFilter(LabelPublicPort)
	if err != nil {
		return nil, err
	}
	res, err := c.API.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: f})
	if err != nil {
		return nil, err
	}
	out := []PortProxyInfo{}
	for _, it := range keepOwned(c, res.Items, func(s container.Summary) map[string]string { return s.Labels }) {
		out = append(out, PortProxyInfo{Name: SummaryName(it), AppID: it.Labels[LabelPublicPort], Running: string(it.State) == "running"})
	}
	return out, nil
}

// PortProxyRunning reports whether the forwarder of an app is running.
func (c *Client) PortProxyRunning(ctx context.Context, appID string) (bool, error) {
	info, err := c.InspectContainer(ctx, PortProxyName(appID))
	if IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.State != nil && info.State.Running, nil
}
