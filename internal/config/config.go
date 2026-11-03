// Package config reads bitploy's settings from BITPLOY_* environment variables.
package config

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Defaults for the isolated BuildKit container.
const (
	DefaultBuilderCPUs   = "2"
	DefaultBuilderMemory = "6g"
)

var memoryRe = regexp.MustCompile(`^[0-9]+([kKmMgG][bB]?)?$`)

// ValidateBuilderLimits checks BITPLOY_BUILDER_CPUS (a positive number) and
// BITPLOY_BUILDER_MEMORY (a size such as 3g or 2048m).
func ValidateBuilderLimits(cpus, memory string) error {
	if f, err := strconv.ParseFloat(cpus, 64); err != nil || f <= 0 || f > 256 {
		return fmt.Errorf("BITPLOY_BUILDER_CPUS must be a positive number such as 2, got %q", cpus)
	}
	if n := strings.TrimRight(memory, "kKmMgGbB"); !memoryRe.MatchString(memory) || strings.Trim(n, "0") == "" {
		return fmt.Errorf("BITPLOY_BUILDER_MEMORY must be a size such as 3g or 2048m, got %q", memory)
	}
	return nil
}

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
	MaxApps           int    // server-wide app limit (all users together)
	Listen            string // :8080
	TraefikContainer  string // container name of Traefik, joined to app networks
	TraefikInternal   string // Traefik's unpublished internal entrypoint, for candidate health checks
	InternalURL       string // how Traefik reaches bitploy (catch-all pages)
	BuilderCPUs       string // CPU limit of the isolated BuildKit container (default 2)
	BuilderMemory     string // memory limit of the isolated BuildKit container (default 3g)
	GeoIP             bool   // download the free DB-IP country database for traffic stats (BITPLOY_GEOIP=off turns it off)
	Dev               bool   // relaxes Secure cookies and HTTPS-only checks for localhost
	GitHubAPIURL      string // GitHub REST API (BITPLOY_GITHUB_API_URL); tests point it at a fake
	GitHubWebURL      string // github.com web address, for the app manifest, install and OAuth pages (BITPLOY_GITHUB_URL)
	DBHelperBin       string // a bitploy binary built for Linux, for the SQLite viewer's helper image (BITPLOY_DB_HELPER_BIN); only needed when bitploy does not run on Linux itself
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
		BuilderCPUs:       env("BITPLOY_BUILDER_CPUS", DefaultBuilderCPUs),
		BuilderMemory:     env("BITPLOY_BUILDER_MEMORY", DefaultBuilderMemory),
		GeoIP:             !offValue(env("BITPLOY_GEOIP", "")),
		Dev:               env("BITPLOY_DEV", "") == "1",
		GitHubAPIURL:      strings.TrimRight(env("BITPLOY_GITHUB_API_URL", "https://api.github.com"), "/"),
		GitHubWebURL:      strings.TrimRight(env("BITPLOY_GITHUB_URL", "https://github.com"), "/"),
		DBHelperBin:       env("BITPLOY_DB_HELPER_BIN", ""),
		MaxParallelBuilds: 2,
		MaxApps:           100,
	}
	if v := os.Getenv("BITPLOY_MAX_PARALLEL_BUILDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("BITPLOY_MAX_PARALLEL_BUILDS must be a positive number, got %q", v)
		}
		c.MaxParallelBuilds = n
	}
	if err := ValidateBuilderLimits(c.BuilderCPUs, c.BuilderMemory); err != nil {
		return nil, err
	}
	if v := os.Getenv("BITPLOY_MAX_APPS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("BITPLOY_MAX_APPS must be a positive number, got %q", v)
		}
		c.MaxApps = n
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

func offValue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "off", "false", "no", "0":
		return true
	}
	return false
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

// BuildxDir holds the buildx state (BUILDX_CONFIG) of the isolated builder.
func (c *Config) BuildxDir() string { return c.Path("buildx") }

func (c *Config) DBPath() string               { return c.Path("bitploy.db") }
func (c *Config) UploadsDir() string           { return c.Path("uploads") }
func (c *Config) BuildsDir() string            { return c.Path("builds") }
func (c *Config) LogsDir() string              { return c.Path("logs") }
func (c *Config) RoutesDir() string            { return c.Path("traefik", "dynamic") }
func (c *Config) TraefikLogsDir() string       { return c.Path("traefik", "logs") }
func (c *Config) AccessLogPath() string        { return c.Path("traefik", "logs", "access.log") }
func (c *Config) GeoIPDir() string             { return c.Path("geoip") }
func (c *Config) BuildDir(depID string) string { return c.Path("builds", depID) }
func (c *Config) BuildLog(depID string) string { return c.Path("logs", depID+".log") }

// EnsureDirs creates the data directory tree.
func (c *Config) EnsureDirs() error {
	for _, d := range []string{c.DataDir, c.UploadsDir(), c.BuildsDir(), c.LogsDir(), c.RoutesDir(), c.TraefikLogsDir()} {
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
