// Package traffic turns Traefik's access log into small per-app traffic
// counts: visitors, requests, pages, countries and referrers. It never keeps
// an IP address or a user agent; both only feed a one-way daily hash.
package traffic

import (
	"encoding/json"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// Entry is one request from the access log, reduced to what the stats need.
type Entry struct {
	Router     string // router name without the "@file" suffix; empty when not a file-provider router
	EntryPoint string
	Host       string
	Method     string
	Path       string // no query string, trailing slash trimmed, capped
	Status     int
	// Blocked is true when Traefik itself answered 401 (the password prompt of a
	// protected app) and the request never reached the app. It is not traffic.
	Blocked   bool
	IP        string
	UserAgent string
	Referer   string
	Country   string // ISO code from Cf-Ipcountry, or empty
	Time      time.Time
	// Duration is how long the app took to answer. It is 0 when unknown (an
	// older traefik.yml) or when Traefik answered by itself, since then it says
	// nothing about the app.
	Duration time.Duration
	// Stream is an answer that stays open and sends as it goes (server-sent
	// events): its duration is how long it was open, not an answer time.
	Stream bool
}

// rawLine holds the fields bitploy asks Traefik to keep (deploy/traefik/traefik.yml).
type rawLine struct {
	RouterName     string `json:"RouterName"`
	EntryPointName string `json:"EntryPointName"`
	RequestHost    string `json:"RequestHost"`
	RequestPath    string `json:"RequestPath"`
	RequestMethod  string `json:"RequestMethod"`
	Status         int    `json:"DownstreamStatus"`
	OriginStatus   *int   `json:"OriginStatus"`
	Duration       int64  `json:"Duration"` // nanoseconds
	ClientHost     string `json:"ClientHost"`
	StartUTC       string `json:"StartUTC"`
	UserAgent      string `json:"request_User-Agent"`
	Referer        string `json:"request_Referer"`
	Country        string `json:"request_Cf-Ipcountry"`
	ContentType    string `json:"downstream_Content-Type"`
}

// MaxPathLen caps stored paths (bytes).
const MaxPathLen = 200

// ParseLine reads one JSON access log line. It reports false for lines that
// are not JSON or have no router (Traefik's own entries).
func ParseLine(b []byte) (Entry, bool) {
	var r rawLine
	if err := json.Unmarshal(b, &r); err != nil || r.RouterName == "" {
		return Entry{}, false
	}
	e := Entry{
		EntryPoint: r.EntryPointName,
		Host:       strings.ToLower(r.RequestHost),
		Method:     r.RequestMethod,
		Path:       CleanPath(r.RequestPath),
		Status:     r.Status,
		IP:         clientIP(r.ClientHost),
		UserAgent:  r.UserAgent,
		Referer:    r.Referer,
		Country:    cleanCountry(r.Country),
		Stream:     strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.ContentType)), "text/event-stream"),
	}
	// OriginStatus is 0 when Traefik answered by itself. The only 401 it sends
	// on an app router is the basic-auth prompt. A 401 from the app has its own
	// OriginStatus. Without the field (an older traefik.yml) nothing is blocked.
	e.Blocked = r.Status == 401 && r.OriginStatus != nil && *r.OriginStatus == 0
	if r.Duration > 0 && (r.OriginStatus == nil || *r.OriginStatus != 0) {
		e.Duration = time.Duration(r.Duration)
	}
	if name, provider, ok := strings.Cut(r.RouterName, "@"); ok && provider == "file" {
		e.Router = name
	}
	if t, err := time.Parse(time.RFC3339Nano, r.StartUTC); err == nil {
		e.Time = t.UTC()
	}
	return e, true
}

// clientIP reads ClientHost. When a proxy sent X-Forwarded-For, Traefik logs the
// raw header ("1.2.3.4, 5.6.7.8"); the last entry is the one the nearest trusted
// proxy added, so it is the real client. A value that is not an address is
// returned as it is.
func clientIP(host string) string {
	last := strings.TrimSpace(host[strings.LastIndexByte(host, ',')+1:])
	if a, err := netip.ParseAddr(last); err == nil {
		return a.Unmap().String()
	}
	if ap, err := netip.ParseAddrPort(last); err == nil {
		return ap.Addr().Unmap().String()
	}
	return host
}

// CleanPath drops the query string and fragment, decodes %XX when that gives
// printable text, trims a trailing slash and caps the length.
func CleanPath(p string) string {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	if u, err := url.PathUnescape(p); err == nil && printable(u) {
		p = u
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
		if p == "" {
			p = "/"
		}
	}
	if len(p) > MaxPathLen {
		p = p[:MaxPathLen]
		for !utf8.ValidString(p) {
			p = p[:len(p)-1]
		}
	}
	return p
}

func printable(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// cleanCountry accepts a two-letter code and rejects Cloudflare's "XX" (unknown) and "T1" (Tor).
func cleanCountry(c string) string {
	c = strings.ToUpper(strings.TrimSpace(c))
	if len(c) != 2 || c == "XX" || c[0] < 'A' || c[0] > 'Z' || c[1] < 'A' || c[1] > 'Z' {
		return ""
	}
	return c
}

// ReferrerHost is the host of an external referrer: lower case, no port, no
// "www.". It is empty for a missing or malformed referrer, for non-web
// schemes, and when the referrer is the site itself (own is the request host).
func ReferrerHost(ref, own string) string {
	if ref == "" || len(ref) > 2048 {
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	h := normHost(u.Hostname())
	if h == "" || h == normHost(own) {
		return ""
	}
	return h
}

// normHost lower-cases a host, drops a port and a leading "www.".
func normHost(h string) string {
	h = strings.ToLower(h)
	if i := strings.LastIndexByte(h, ':'); i >= 0 && strings.Count(h, ":") == 1 {
		h = h[:i]
	}
	return strings.TrimPrefix(h, "www.")
}

var assetExts = map[string]bool{
	".js": true, ".mjs": true, ".css": true, ".map": true, ".png": true, ".jpg": true, ".jpeg": true,
	".gif": true, ".webp": true, ".avif": true, ".svg": true, ".ico": true, ".woff": true, ".woff2": true,
	".ttf": true, ".otf": true, ".eot": true, ".mp4": true, ".webm": true, ".mp3": true, ".txt": true,
	".xml": true, ".json": true, ".webmanifest": true,
}

// IsAsset reports whether a path looks like a static file. Assets count in the
// totals but stay out of the top pages.
func IsAsset(path string) bool {
	slash := strings.LastIndexByte(path, '/')
	dot := strings.LastIndexByte(path, '.')
	if dot <= slash {
		return false
	}
	return assetExts[strings.ToLower(path[dot:])]
}

var botWords = []string{
	"bot", "crawl", "spider", "slurp", "scrape", "monitor", "uptime", "preview", "headless",
	"curl/", "wget/", "python-requests", "go-http-client", "okhttp", "java/", "libwww", "httpclient",
}

// IsBot reports whether a user agent looks like a crawler or a script. An
// empty user agent counts as one: browsers always send one.
func IsBot(ua string) bool {
	if ua == "" {
		return true
	}
	ua = strings.ToLower(ua)
	for _, w := range botWords {
		if strings.Contains(ua, w) {
			return true
		}
	}
	return false
}
