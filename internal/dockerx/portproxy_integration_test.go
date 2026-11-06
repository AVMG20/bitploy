//go:build integration

package dockerx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/AVMG20/bitploy/internal/ids"
)

// freeHostPort finds a TCP port nothing listens on.
func freeHostPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func getVia(port int) (string, error) {
	cl := http.Client{Timeout: 4 * time.Second}
	res, err := cl.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	return strings.TrimSpace(string(b)), err
}

// Needs a Docker daemon and Go (it builds the bitploy binary for the
// forwarder image). Run: go test -tags=integration -count=1 -run PortProxy ./internal/dockerx/
func TestPortProxyIntegration(t *testing.T) {
	ctx := context.Background()
	c, err := New(ctx)
	if err != nil {
		t.Skip("no docker: ", err)
	}
	defer c.Close()
	c.Instance = "test-" + strings.ToLower(ids.Hex(5))

	info, err := c.API.Info(ctx, client.InfoOptions{})
	if err != nil {
		t.Fatal(err)
	}
	goarch := map[string]string{"x86_64": "amd64", "aarch64": "arm64"}[info.Info.Architecture]
	if goarch == "" {
		t.Skip("unknown docker architecture ", info.Info.Architecture)
	}
	bin := filepath.Join(t.TempDir(), "bitploy")
	build := exec.Command("go", "build", "-trimpath", "-o", bin, "../../cmd/bitploy")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+goarch)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the binary: %v\n%s", err, out)
	}
	c.DBHelperBin = bin

	slug := "pp-" + strings.ToLower(ids.Hex(5))
	appID := "app_pp" + ids.Hex(6)
	netName := NetworkName(slug)
	target := "bp-" + slug + "-1"
	t.Cleanup(func() {
		bg := context.Background()
		_ = c.RemovePortProxy(bg, appID)
		_ = c.RemoveContainer(bg, target)
		_ = c.RemoveNetwork(bg, netName)
		_ = c.RemoveImage(bg, DBHelperImage+":"+c.dbh.tag)
	})

	// The "app": busybox httpd on port 8000 with the alias app, no published port.
	if err := c.EnsureNetwork(ctx, netName, c.ManagedLabels(appID, slug)); err != nil {
		t.Fatal(err)
	}
	if err := c.ensureInitImage(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.API.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: target,
		Config: &container.Config{Image: VolumeInitImage, Labels: c.ManagedLabels(appID, slug),
			Cmd: []string{"sh", "-c", "echo hello-from-app >/tmp/index.html; exec httpd -f -p 8000 -h /tmp"}},
		HostConfig:       &container.HostConfig{NetworkMode: container.NetworkMode(netName)},
		NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{netName: {Aliases: []string{"app"}}}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.StartContainer(ctx, target); err != nil {
		t.Fatal(err)
	}

	port := freeHostPort(t)
	spec := PortProxySpec{AppID: appID, Slug: slug, Network: netName, HostPort: port, Target: "app:8000"}
	if err := c.EnsurePortProxy(ctx, spec); err != nil {
		t.Fatal(err)
	}
	var body string
	for i := 0; i < 20; i++ {
		if body, err = getVia(port); err == nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if body != "hello-from-app" {
		t.Fatalf("through the forwarder: %q %v", body, err)
	}

	// Locked down, on the app network only, no app_id label.
	fw, err := c.InspectContainer(ctx, PortProxyName(appID))
	if err != nil {
		t.Fatal(err)
	}
	hc := fw.HostConfig
	if fw.Config.User != "65534:65534" || !hc.ReadonlyRootfs || len(hc.CapDrop) != 1 || hc.CapDrop[0] != "ALL" || hc.Privileged ||
		string(hc.NetworkMode) != netName || len(fw.NetworkSettings.Networks) != 1 {
		t.Fatalf("forwarder settings: user %q, ro %v, capdrop %v, network %s, %d networks", fw.Config.User, hc.ReadonlyRootfs, hc.CapDrop, hc.NetworkMode, len(fw.NetworkSettings.Networks))
	}
	if fw.Config.Labels[LabelAppID] != "" || fw.Config.Labels[LabelPublicPort] != appID {
		t.Fatalf("labels: %v", fw.Config.Labels)
	}
	for p, bs := range hc.PortBindings {
		for _, b := range bs {
			if b.HostIP.String() != "0.0.0.0" || b.HostPort != fmt.Sprint(port) {
				t.Fatalf("binding %v: %+v", p, b)
			}
		}
	}
	// The app container still publishes nothing.
	if app, err := c.InspectContainer(ctx, target); err != nil || len(app.HostConfig.PortBindings) != 0 {
		t.Fatalf("the app container publishes ports: %v", err)
	}

	// Same settings: the same container stays.
	if err := c.EnsurePortProxy(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if again, _ := c.InspectContainer(ctx, PortProxyName(appID)); again.ID != fw.ID {
		t.Fatal("an unchanged forwarder was replaced")
	}
	if ps, err := c.ListPortProxies(ctx); err != nil || len(ps) != 1 || ps[0].AppID != appID || !ps[0].Running {
		t.Fatalf("list: %+v %v", ps, err)
	}

	// An allow list without this machine: connections are refused.
	spec.Allow = []string{"192.0.2.0/24"}
	if err := c.EnsurePortProxy(ctx, spec); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	if body, err := getVia(port); err == nil {
		t.Fatalf("an address outside the allow list got through: %q", body)
	}

	// Expired: it no longer listens.
	spec.Allow, spec.Until = nil, time.Now().Add(-time.Minute).Unix()
	if err := c.EnsurePortProxy(ctx, spec); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	if body, err := getVia(port); err == nil {
		t.Fatalf("an expired forwarder answered: %q", body)
	}

	// A port something else holds on the host.
	ln, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	spec.Until, spec.HostPort = 0, ln.Addr().(*net.TCPAddr).Port
	if err := c.EnsurePortProxy(ctx, spec); !errors.Is(err, ErrPortInUse) {
		t.Fatalf("taken port: %v", err)
	}

	if err := c.RemovePortProxy(ctx, appID); err != nil {
		t.Fatal(err)
	}
	if running, err := c.PortProxyRunning(ctx, appID); err != nil || running {
		t.Fatalf("after remove: %v %v", running, err)
	}
}
