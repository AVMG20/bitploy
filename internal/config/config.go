// Package config reads bitploy's settings from BITPLOY_* environment variables.
package config

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	PublicURL         string // https://bitploy.nl — dashboard, API, MCP; also the CSRF origin
	AppsDomain        string // bitploy.nl — parent domain for app subdomains
	AppsScheme        string // https in production, http for local dev
	AppsPort          string // optional port appended to app URLs (local dev: 8000)
	EdgeHost          string // edge.bitploy.nl — DNS-only CNAME target
	ServerIPv4        string
	ServerIPv6        string
	SecretKey         []byte // 32 bytes, AES-256-GCM
	DataDir           string
	ACMEEmail         string
	Signup            string // invite | open
	MaxParallelBuilds int
	Listen            string // :8080
	TraefikContainer  string // container name of Traefik, joined to app networks
	TraefikInternal   string // Traefik's unpublished internal entrypoint, for candidate health checks
	InternalURL       string // how Traefik reaches bitploy (catch-all pages)
	Dev               bool   // relaxes Secure cookies and HTTPS-only checks for localhost
}

func Load() (*Config, error) {
	c := &Config{
		PublicURL:         strings.TrimRight(env("BITPLOY_PUBLIC_URL", "http://localhost:8080"), "/"),
		AppsDomain:        env("BITPLOY_APPS_DOMAIN", "localhost"),
		AppsScheme:        env("BITPLOY_APPS_SCHEME", "https"),
		AppsPort:          env("BITPLOY_APPS_PORT", ""),
		EdgeHost:          env("BITPLOY_EDGE_HOST", ""),
		ServerIPv4:        env("BITPLOY_SERVER_IPV4", ""),
		ServerIPv6:        env("BITPLOY_SERVER_IPV6", ""),
		DataDir:           env("BITPLOY_DATA_DIR", "/var/lib/bitploy"),
		ACMEEmail:         env("BITPLOY_ACME_EMAIL", ""),
		Signup:            env("BITPLOY_SIGNUP", "invite"),
		Listen:            env("BITPLOY_LISTEN", ":8080"),
		TraefikContainer:  env("BITPLOY_TRAEFIK_CONTAINER", "bitploy-traefik"),
		TraefikInternal:   strings.TrimRight(env("BITPLOY_TRAEFIK_INTERNAL_URL", "http://bitploy-traefik:8081"), "/"),
		InternalURL:       env("BITPLOY_INTERNAL_URL", "http://bitploy:8080"),
		Dev:               env("BITPLOY_DEV", "") == "1",
		MaxParallelBuilds: 2,
	}
	if v := os.Getenv("BITPLOY_MAX_PARALLEL_BUILDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("BITPLOY_MAX_PARALLEL_BUILDS must be a positive number, got %q", v)
		}
		c.MaxParallelBuilds = n
	}
	if c.Signup != "invite" && c.Signup != "open" {
		return nil, fmt.Errorf("BITPLOY_SIGNUP must be invite or open, got %q", c.Signup)
	}
	if _, err := url.Parse(c.PublicURL); err != nil {
		return nil, fmt.Errorf("BITPLOY_PUBLIC_URL is not a URL: %w", err)
	}
	if c.EdgeHost == "" {
		c.EdgeHost = "edge." + c.AppsDomain
	}
	key := os.Getenv("BITPLOY_SECRET_KEY")
	if key == "" {
		return nil, fmt.Errorf("BITPLOY_SECRET_KEY is required: generate one with `openssl rand -base64 32`")
	}
	k, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(k) != 32 {
		return nil, fmt.Errorf("BITPLOY_SECRET_KEY must be 32 random bytes in base64 (`openssl rand -base64 32`)")
	}
	c.SecretKey = k
	return c, nil
}

// AppURL is the public URL of an app subdomain.
func (c *Config) AppURL(slug string) string {
	host := slug + "." + c.AppsDomain
	if c.AppsPort != "" {
		host += ":" + c.AppsPort
	}
	return c.AppsScheme + "://" + host
}

// AppHost is the Host header value Traefik routes for an app.
func (c *Config) AppHost(slug string) string { return slug + "." + c.AppsDomain }

func (c *Config) Path(parts ...string) string {
	return filepath.Join(append([]string{c.DataDir}, parts...)...)
}

func (c *Config) DBPath() string               { return c.Path("bitploy.db") }
func (c *Config) UploadsDir() string           { return c.Path("uploads") }
func (c *Config) BuildsDir() string            { return c.Path("builds") }
func (c *Config) LogsDir() string              { return c.Path("logs") }
func (c *Config) RoutesDir() string            { return c.Path("traefik", "dynamic") }
func (c *Config) BuildDir(depID string) string { return c.Path("builds", depID) }
func (c *Config) BuildLog(depID string) string { return c.Path("logs", depID+".log") }

// EnsureDirs creates the data directory tree.
func (c *Config) EnsureDirs() error {
	for _, d := range []string{c.DataDir, c.UploadsDir(), c.BuildsDir(), c.LogsDir(), c.RoutesDir()} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return err
		}
	}
	return nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
