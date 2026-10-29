package api

import (
	"database/sql"
	"errors"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"strings"
)

var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>{{.Title}} - bitploy</title>
<style>
:root{color-scheme:light dark}
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;font:16px/1.5 system-ui,-apple-system,Segoe UI,sans-serif;background:#fafafa;color:#18181b}
@media (prefers-color-scheme:dark){body{background:#09090b;color:#fafafa}.card p{color:#a1a1aa}a{color:#93c5fd}}
.card{max-width:28rem;padding:2rem;text-align:center}
.mark{font-weight:700;letter-spacing:-.02em;font-size:1.1rem;margin-bottom:1.5rem}
h1{font-size:1.6rem;margin:0 0 .5rem}
.card p{margin:.4rem 0;color:#52525b}
code{font-family:ui-monospace,monospace}
</style></head><body><main class="card">
<div class="mark">bitploy</div>
<h1>{{.Title}}</h1>
{{range .Lines}}<p>{{.}}</p>{{end}}
<p><a href="{{.Home}}">Go to bitploy</a></p>
</main></body></html>`))

type pageData struct {
	Title string
	Lines []string
	Home  string
}

// appHostSlug reports whether host (no port) is a subdomain of the apps
// domain that is not the dashboard's own host, and returns the part before it.
func (a *API) appHostSlug(hostHeader string) (string, bool) {
	host := hostHeader
	if h, _, err := net.SplitHostPort(hostHeader); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return "", false
	}
	if u, err := url.Parse(a.cfg.PublicURL); err == nil && strings.EqualFold(u.Hostname(), host) {
		return "", false
	}
	suffix := "." + strings.ToLower(a.cfg.AppsDomain)
	if !strings.HasSuffix(host, suffix) {
		return "", false
	}
	return strings.TrimSuffix(host, suffix), true
}

// appPage is the friendly catch-all for unknown and stopped app subdomains.
func (a *API) appPage(w http.ResponseWriter, r *http.Request, slug string) {
	d := pageData{Home: a.cfg.PublicURL}
	status := http.StatusNotFound
	d.Title = "No app here"
	d.Lines = []string{"Nothing is deployed at this address yet.", "If you own it, ask your AI assistant to deploy it with bitploy."}
	if slug != "" && !strings.Contains(slug, ".") {
		row, err := a.svc.Q.GetAppBySlug(r.Context(), slug)
		switch {
		case err == nil && row.Status == "running" && row.DesiredState == "running":
			status = http.StatusServiceUnavailable
			d.Title = "This app is not responding"
			d.Lines = []string{"The app should be running but did not answer. It may be restarting.", "Try again in a minute."}
		case err == nil:
			status = http.StatusServiceUnavailable
			d.Title = "This app is stopped"
			d.Lines = []string{"The owner stopped this app, or it is not running right now.", "If it is yours, start it from the bitploy dashboard."}
		case errors.Is(err, sql.ErrNoRows):
		default:
			a.svc.Log.Error("catch-all lookup failed", "err", err)
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = pageTmpl.Execute(w, d)
}
