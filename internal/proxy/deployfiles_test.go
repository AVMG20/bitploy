package proxy

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Traefik joins every app network, and the routers on its internal entrypoint
// (candidate and live) have no password check. Inside one Docker bridge the
// firewall does not see the traffic, so the entrypoint must listen only on
// Traefik's fixed address on the "bitploy" network: an app that connects to
// Traefik's address on its own network then finds nothing on port 8081. This
// keeps deploy/docker-compose.yml and deploy/traefik/traefik.yml in step.
func TestInternalEntryPointOnlyOnTheBitployNetwork(t *testing.T) {
	root := filepath.Join("..", "..", "deploy")
	var compose struct {
		Services map[string]struct {
			Ports    []string `yaml:"ports"`
			Networks yaml.Node `yaml:"networks"` // a list for bitploy, a mapping for traefik
		} `yaml:"services"`
		Networks map[string]struct {
			IPAM struct {
				Config []struct {
					Subnet string `yaml:"subnet"`
				} `yaml:"config"`
			} `yaml:"ipam"`
		} `yaml:"networks"`
	}
	var static struct {
		EntryPoints map[string]struct {
			Address string `yaml:"address"`
		} `yaml:"entryPoints"`
		Ping struct {
			EntryPoint string `yaml:"entryPoint"`
		} `yaml:"ping"`
	}
	read := func(name string, v any) {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := yaml.Unmarshal(b, v); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	read("docker-compose.yml", &compose)
	read(filepath.Join("traefik", "traefik.yml"), &static)

	tr, ok := compose.Services["traefik"]
	if !ok {
		t.Fatal("docker-compose.yml has no traefik service")
	}
	var nets map[string]struct {
		IPv4 string `yaml:"ipv4_address"`
	}
	if err := tr.Networks.Decode(&nets); err != nil {
		t.Fatalf("traefik networks must be a mapping with an ipv4_address: %v", err)
	}
	fixed, err := netip.ParseAddr(nets["bitploy"].IPv4)
	if err != nil {
		t.Fatalf("traefik needs a fixed ipv4_address on the bitploy network: %v", err)
	}
	cfg := compose.Networks["bitploy"].IPAM.Config
	if len(cfg) != 1 {
		t.Fatalf("the bitploy network needs one fixed subnet, got %+v", cfg)
	}
	subnet, err := netip.ParsePrefix(cfg[0].Subnet)
	if err != nil || !subnet.Contains(fixed) {
		t.Fatalf("Traefik's address %s is not inside the bitploy subnet %q", fixed, cfg[0].Subnet)
	}
	if netip.MustParsePrefix("10.200.0.0/16").Overlaps(subnet) {
		t.Fatalf("the bitploy subnet %s overlaps the app pool", subnet)
	}

	ep, ok := static.EntryPoints["internal"]
	if !ok {
		t.Fatal("traefik.yml has no internal entrypoint")
	}
	host, port, found := strings.Cut(ep.Address, ":")
	if !found || port != "8081" {
		t.Fatalf("internal entrypoint address %q, want <address>:8081", ep.Address)
	}
	if host != fixed.String() {
		t.Fatalf("internal entrypoint listens on %q, want only Traefik's address on the bitploy network %s", ep.Address, fixed)
	}
	for name, e := range static.EntryPoints {
		if name != "internal" && strings.HasSuffix(e.Address, ":8081") {
			t.Errorf("entrypoint %s also uses port 8081", name)
		}
	}
	if static.Ping.EntryPoint != "internal" {
		t.Errorf("ping is on %q; the compose healthcheck expects it on the internal entrypoint", static.Ping.EntryPoint)
	}
	for _, p := range tr.Ports {
		if strings.Contains(p, "8081") {
			t.Errorf("docker-compose.yml publishes %q: the internal entrypoint must stay unpublished", p)
		}
	}
}
