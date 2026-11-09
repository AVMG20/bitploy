package api

import (
	"database/sql"
	"errors"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/AVMG20/bitploy/internal/store/db"
)

var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>{{.Title}} - bitploy</title>
<style>
:root{color-scheme:light dark}
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;font:16px/1.5 system-ui,-apple-system,Segoe UI,sans-serif;background:#f6f6f6;color:#1f1f1f}a{color:#d9541e}
@media (prefers-color-scheme:dark){body{background:#131313;color:#ececec}.card p{color:#9a9a9a}a{color:#f4804f}}
.card{max-width:28rem;padding:2rem;text-align:center}
.mark{font-weight:700;letter-spacing:-.02em;font-size:1.1rem;margin-bottom:1.5rem}
h1{font-size:1.6rem;margin:0 0 .5rem}
.card p{margin:.4rem 0;color:#5c5c5c}
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

// appHostSlug reports whether host (no port) is a subdomain of an app
// domain that is not the dashboard's own host, and returns the part before it.
func (a *API) appHostSlug(hostHeader string) (slug, domain string, ok bool) {
	host := hostHeader
	if h, _, err := net.SplitHostPort(hostHeader); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return "", "", false
	}
	if u, err := url.Parse(a.cfg.PublicURL); err == nil && strings.EqualFold(u.Hostname(), host) {
		return "", "", false
	}
	return a.svc.AppHostSlug(host)
}

// appPage is the friendly catch-all for unknown and stopped app subdomains,
// and for an extra domain itself (slug ""), which an app can hold. domain is
// the app domain of the address: an app that lives on another domain is not
// here.
func (a *API) appPage(w http.ResponseWriter, r *http.Request, slug, domain string) {
	d := pageData{Home: a.cfg.PublicURL}
	status := http.StatusNotFound
	d.Title = "No app here"
	d.Lines = []string{"Nothing is deployed at this address yet.", "If you own it, ask your AI assistant to deploy it with bitploy."}
	if !strings.Contains(slug, ".") {
		var row db.App
		var err error
		if slug == "" {
			row, err = a.svc.Q.GetRootApp(r.Context(), domain)
		} else {
			row, err = a.svc.Q.GetAppBySlug(r.Context(), slug)
			if err == nil && !strings.EqualFold(a.svc.AppDomainOf(row), domain) {
				err = sql.ErrNoRows
			}
		}
		switch {
		case err == nil && row.Status == "running" && row.DesiredState == "running":
			status = http.StatusServiceUnavailable
			d.Title = "This app is not responding"
			d.Lines = []string{"The app should be running but did not answer. It may be restarting.", "Try again in a minute."}
		case err == nil:
			status = http.StatusServiceUnavailable
			d.Title = "This app is stopped"
			d.Lines = []string{"The owner stopped this app, or it is not running.", "If it is yours, start it from the bitploy dashboard."}
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
