package traffic

import (
	"strings"
	"testing"
	"time"
)

// A line as Traefik writes it with the fields from deploy/traefik/traefik.yml.
const sampleLine = `{"ClientHost":"203.0.113.7","DownstreamStatus":200,"Duration":1234567,"EntryPointName":"websecure","RequestHost":"quiet-otter-3f9a.bitploy.nl","RequestMethod":"GET","RequestPath":"/blog/hello/?utm=x#top","RouterName":"app-quiet-otter-3f9a@file","StartUTC":"2026-10-30T14:03:05.123456789Z","request_Cf-Ipcountry":"nl","request_Referer":"https://www.Example.com/post?id=1","request_User-Agent":"Mozilla/5.0 (X11; Linux x86_64) Firefox/130.0"}`

func TestParseLine(t *testing.T) {
	e, ok := ParseLine([]byte(sampleLine))
	if !ok {
		t.Fatal("line not parsed")
	}
	want := Entry{
		Router: "app-quiet-otter-3f9a", EntryPoint: "websecure", Host: "quiet-otter-3f9a.bitploy.nl", Method: "GET",
		Path: "/blog/hello", Status: 200, IP: "203.0.113.7", UserAgent: "Mozilla/5.0 (X11; Linux x86_64) Firefox/130.0",
		Referer: "https://www.Example.com/post?id=1", Country: "NL",
		Time: time.Date(2026, 10, 30, 14, 3, 5, 123456789, time.UTC),
	}
	if e != want {
		t.Errorf("got  %+v\nwant %+v", e, want)
	}
}

func TestParseLineOther(t *testing.T) {
	for name, line := range map[string]string{
		"not json":    `time="2026" level=info msg=hello`,
		"no router":   `{"ClientHost":"1.2.3.4","DownstreamStatus":404}`,
		"empty":       ``,
		"cut off":     `{"RouterName":"app-x@file","Down`,
		"wrong types": `{"RouterName":"app-x@file","DownstreamStatus":"ok"}`,
	} {
		if _, ok := ParseLine([]byte(line)); ok {
			t.Errorf("%s: parsed", name)
		}
	}
	// A router of another provider is parsed but has no app router name.
	e, ok := ParseLine([]byte(`{"RouterName":"acme-http@internal","DownstreamStatus":200}`))
	if !ok || e.Router != "" {
		t.Errorf("internal router: ok=%v router=%q", ok, e.Router)
	}
	// Cloudflare's "unknown" and "Tor" are not countries.
	for _, c := range []string{"XX", "T1", "", "nld", "1A"} {
		if got := cleanCountry(c); got != "" {
			t.Errorf("cleanCountry(%q) = %q", c, got)
		}
	}
}

func TestCleanPath(t *testing.T) {
	long := "/" + strings.Repeat("é", 300)
	for in, want := range map[string]string{
		"":                    "/",
		"/":                   "/",
		"//":                  "/",
		"/a/b/":               "/a/b",
		"/a?x=1&y=2":          "/a",
		"/a#frag":             "/a",
		"/caf%C3%A9":          "/café",
		"/bad%0Aline":         "/bad%0Aline", // control characters stay encoded
		"/bad%zz":             "/bad%zz",
		"no-leading-slash":    "/no-leading-slash",
		"/index.html?q=a/b/c": "/index.html",
	} {
		if got := CleanPath(in); got != want {
			t.Errorf("CleanPath(%q) = %q, want %q", in, got, want)
		}
	}
	got := CleanPath(long)
	if len(got) > MaxPathLen || !strings.HasPrefix(got, "/é") || strings.ContainsRune(got, '�') {
		t.Errorf("long path: %d bytes %q", len(got), got)
	}
}

func TestReferrerHost(t *testing.T) {
	for _, c := range []struct{ ref, own, want string }{
		{"https://www.Example.com/post?id=1", "app.bitploy.nl", "example.com"},
		{"http://news.example.org:8080/", "app.bitploy.nl", "news.example.org"},
		{"https://app.bitploy.nl/other", "app.bitploy.nl", ""}, // own site
		{"https://www.app.bitploy.nl/", "app.bitploy.nl", ""},  // own site with www
		{"https://app.bitploy.nl/", "APP.bitploy.nl:443", ""},  // port and case
		{"android-app://com.google.android.gm", "x.nl", ""},    // not a web page
		{"", "x.nl", ""},
		{"not a url", "x.nl", ""},
		{"https://" + strings.Repeat("a", 3000) + ".com", "x.nl", ""},
	} {
		if got := ReferrerHost(c.ref, c.own); got != c.want {
			t.Errorf("ReferrerHost(%q, %q) = %q, want %q", c.ref, c.own, got, c.want)
		}
	}
}

func TestIsAsset(t *testing.T) {
	for p, want := range map[string]bool{
		"/app.js": true, "/static/css/site.CSS": true, "/logo.svg": true, "/favicon.ico": true, "/f.woff2": true,
		"/main.js.map": true, "/a/b/photo.PNG": true,
		"/": false, "/about": false, "/v1.2/docs": false, "/blog/post.html": false, "/.hidden": false,
	} {
		if IsAsset(p) != want {
			t.Errorf("IsAsset(%q) = %v", p, !want)
		}
	}
}

func TestIsBot(t *testing.T) {
	for ua, want := range map[string]bool{
		"": true, "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)": true,
		"Mozilla/5.0 (compatible; AhrefsBot/7.0)": true, "Baiduspider/2.0": true, "curl/8.5.0": true,
		"Go-http-client/2.0": true, "Mozilla/5.0 HeadlessChrome/120": true, "UptimeRobot/2.0": true,
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 Safari/604.1": false,
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126.0 Safari/537.36":  false,
	} {
		if IsBot(ua) != want {
			t.Errorf("IsBot(%q) = %v", ua, !want)
		}
	}
}

func TestClientIP(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.7":               "203.0.113.7",
		"203.0.113.7, 198.51.100.2": "198.51.100.2", // X-Forwarded-For: the last entry is the client
		"1.1.1.1,  2001:db8::1 ":    "2001:db8::1",
		"::ffff:203.0.113.7":        "203.0.113.7",
		"203.0.113.7:5000":          "203.0.113.7",
		"[2001:db8::1]:443":         "2001:db8::1",
		"unknown":                   "unknown", // not an address: kept as it is
		"":                          "",
	} {
		if got := clientIP(in); got != want {
			t.Errorf("clientIP(%q) = %q, want %q", in, got, want)
		}
	}
	line := `{"RouterName":"app-x@file","ClientHost":"9.9.9.9, 203.0.113.7","DownstreamStatus":200}`
	if e, _ := ParseLine([]byte(line)); e.IP != "203.0.113.7" {
		t.Errorf("ParseLine IP = %q", e.IP)
	}
}
