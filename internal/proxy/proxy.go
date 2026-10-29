// Package proxy writes Traefik file-provider route files, one per app.
// Files are written atomically (temp file + rename); Traefik watches the folder.
package proxy

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Route is everything Traefik needs to serve one app.
type Route struct {
	Slug     string
	Host     string   // quiet-otter-3f9a.bitploy.nl
	Custom   []string // active custom hostnames, served with certResolver letsencrypt
	Upstream string   // http://bp-quiet-otter-3f9a-7:8080; empty = app not serving (catch-all takes over)
	// Candidate is the upstream of a new container during its health check.
	// It is exposed only on the internal entrypoint under CandidateHost(slug),
	// so bitploy can probe it through Traefik before the swap.
	Candidate string
}

// CandidateHost is the Host header bitploy uses to health-check a new
// container through Traefik's internal entrypoint.
func CandidateHost(slug string) string { return "candidate." + slug + ".bitploy.internal" }

type Writer struct {
	Dir        string // BITPLOY_DATA_DIR/traefik/dynamic
	EntryPoint string // websecure (prod) or web (local dev)
	TLS        bool   // false in local dev (HTTP only)
	// InternalEntryPoint is an unpublished Traefik entrypoint (":8081") used
	// for candidate health checks. Default "internal".
	InternalEntryPoint string
}

func routerName(slug string) string { return "app-" + slug }

func hostRouterName(slug, host string) string {
	return "app-" + slug + "-" + strings.NewReplacer(".", "-", "*", "x").Replace(strings.ToLower(host))
}

type file struct {
	HTTP struct {
		Routers  map[string]router  `yaml:"routers,omitempty"`
		Services map[string]service `yaml:"services,omitempty"`
	} `yaml:"http"`
}

type router struct {
	Rule        string   `yaml:"rule"`
	EntryPoints []string `yaml:"entryPoints"`
	Service     string   `yaml:"service"`
	Priority    int      `yaml:"priority,omitempty"`
	TLS         *tlsConf `yaml:"tls,omitempty"`
}

type tlsConf struct {
	CertResolver string `yaml:"certResolver,omitempty"`
}

type service struct {
	LoadBalancer struct {
		Servers []struct {
			URL string `yaml:"url"`
		} `yaml:"servers"`
	} `yaml:"loadBalancer"`
}

// Render returns the YAML for one app.
func (w *Writer) Render(r Route) ([]byte, error) {
	var f file
	f.HTTP.Routers = map[string]router{}
	f.HTTP.Services = map[string]service{}
	name := routerName(r.Slug)
	if r.Candidate != "" {
		cname := name + "-candidate"
		f.HTTP.Services[cname] = newService(r.Candidate)
		ep := w.InternalEntryPoint
		if ep == "" {
			ep = "internal"
		}
		f.HTTP.Routers[cname] = router{Rule: fmt.Sprintf("Host(`%s`)", CandidateHost(r.Slug)), EntryPoints: []string{ep}, Service: cname}
	}
	if r.Upstream == "" {
		return yaml.Marshal(&f)
	}
	f.HTTP.Services[name] = newService(r.Upstream)

	rt := router{Rule: fmt.Sprintf("Host(`%s`)", r.Host), EntryPoints: []string{w.entry()}, Service: name}
	if w.TLS {
		rt.TLS = &tlsConf{}
	}
	f.HTTP.Routers[name] = rt
	custom := append([]string(nil), r.Custom...)
	sort.Strings(custom)
	for _, h := range custom {
		cr := router{Rule: fmt.Sprintf("Host(`%s`)", h), EntryPoints: []string{w.entry()}, Service: name}
		if w.TLS {
			cr.TLS = &tlsConf{CertResolver: "letsencrypt"}
		}
		f.HTTP.Routers[hostRouterName(r.Slug, h)] = cr
	}
	return yaml.Marshal(&f)
}

func (w *Writer) entry() string {
	if w.EntryPoint != "" {
		return w.EntryPoint
	}
	return "websecure"
}

func newService(url string) service {
	var svc service
	svc.LoadBalancer.Servers = append(svc.LoadBalancer.Servers, struct {
		URL string `yaml:"url"`
	}{URL: url})
	return svc
}

// Write stores the route file for an app. With neither Upstream nor
// Candidate the file is removed so the low-priority catch-all router answers.
func (w *Writer) Write(r Route) error {
	if r.Upstream == "" && r.Candidate == "" {
		return w.Remove(r.Slug)
	}
	b, err := w.Render(r)
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(w.Dir, r.Slug+".yml"), b)
}

// Remove deletes an app's route file.
func (w *Writer) Remove(slug string) error {
	err := os.Remove(filepath.Join(w.Dir, slug+".yml"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// List returns the slugs that currently have a route file (for the reconciler).
func (w *Writer) List() ([]string, error) {
	entries, err := os.ReadDir(w.Dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasSuffix(n, ".yml") && !strings.HasPrefix(n, "_") {
			out = append(out, strings.TrimSuffix(n, ".yml"))
		}
	}
	return out, nil
}

// Read returns the current file content for an app, or nil if none.
func (w *Writer) Read(slug string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(w.Dir, slug+".yml"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	return b, err
}

// WriteCatchAll writes _catchall.yml: a lowest-priority router for any host
// under appsDomain that sends unknown and stopped apps to bitploy's friendly
// "No app here" / "This app is stopped" page at bitployURL.
func (w *Writer) WriteCatchAll(appsDomain, bitployURL string) error {
	var f file
	f.HTTP.Routers = map[string]router{}
	f.HTTP.Services = map[string]service{}
	rule := fmt.Sprintf("HostRegexp(`^[a-z0-9-]+\\.%s$`)", strings.ReplaceAll(appsDomain, ".", `\.`))
	rt := router{Rule: rule, EntryPoints: []string{w.entry()}, Service: "bitploy-catchall", Priority: 1}
	if w.TLS {
		rt.TLS = &tlsConf{}
	}
	f.HTTP.Routers["bitploy-catchall"] = rt
	f.HTTP.Services["bitploy-catchall"] = newService(bitployURL)
	b, err := yaml.Marshal(&f)
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(w.Dir, "_catchall.yml"), b)
}

func atomicWrite(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
