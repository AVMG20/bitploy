package traffic

import (
	"fmt"
	"strings"
	"testing"
)

// withOrigin builds a log line with an OriginStatus field (origin < 0 leaves it out).
func withOrigin(router string, status, origin int) string {
	o := ""
	if origin >= 0 {
		o = fmt.Sprintf(`"OriginStatus":%d,`, origin)
	}
	return fmt.Sprintf(`{"ClientHost":"203.0.113.7",%s"DownstreamStatus":%d,"EntryPointName":"websecure","RequestMethod":"GET","RequestPath":"/","RouterName":%q,"request_User-Agent":"Mozilla/5.0 Firefox/130"}`,
		o, status, router)
}

// A 401 Traefik sends itself (OriginStatus 0) is the password prompt. A 401 from
// the app, or a line without OriginStatus (an older traefik.yml), is not touched.
func TestParseBlockedIsOnlyTheLoginPrompt(t *testing.T) {
	for _, c := range []struct {
		name    string
		line    string
		blocked bool
	}{
		{"prompt", withOrigin("app-x@file", 401, 0), true},
		{"401 from the app", withOrigin("app-x@file", 401, 401), false},
		{"401 without origin field", withOrigin("app-x@file", 401, -1), false},
		{"200", withOrigin("app-x@file", 200, 200), false},
		{"404 from traefik", withOrigin("app-x@file", 404, 0), false},
	} {
		e, ok := ParseLine([]byte(c.line))
		if !ok {
			t.Fatalf("%s: not parsed", c.name)
		}
		if e.Blocked != c.blocked {
			t.Errorf("%s: Blocked = %v, want %v", c.name, e.Blocked, c.blocked)
		}
	}
}

// Visitors get one 401 per page load before the browser sends the password.
// None of those count as requests, errors or visitors; the real visits do.
func TestLoginPromptIsNotCountedAsTraffic(t *testing.T) {
	e := newMgrEnv(t)
	e.write(
		withOrigin("app-quiet-otter-3f9a@file", 401, 0)+"\n",
		withOrigin("app-quiet-otter-3f9a-www-example-com@file", 401, 0)+"\n", // custom domain
		withOrigin("app-quiet-otter-3f9a@file", 200, 200)+"\n",
		withOrigin("app-other-app-1234@file", 401, 401)+"\n", // the app's own 401 still counts
	)
	e.run()
	if got := e.count(`SELECT COALESCE(SUM(requests), 0) FROM traffic_hourly WHERE app_id = 'app_1'`); got != 1 {
		t.Errorf("app_1 requests = %d, want 1 (only the real visit)", got)
	}
	if got := e.count(`SELECT COALESCE(SUM(errors_4xx), 0) FROM traffic_hourly WHERE app_id = 'app_1'`); got != 0 {
		t.Errorf("app_1 4xx errors = %d, want 0", got)
	}
	if got := e.count(`SELECT COALESCE(SUM(requests), 0) FROM traffic_hourly WHERE app_id = 'app_2'`); got != 1 {
		t.Errorf("app_2 requests = %d, want 1", got)
	}
	if got := e.count(`SELECT COALESCE(SUM(errors_4xx), 0) FROM traffic_hourly WHERE app_id = 'app_2'`); got != 1 {
		t.Errorf("app_2 4xx errors = %d, want 1 (a 401 the app sent itself)", got)
	}
}

// Lines written by a real Traefik v3.7.17 with the route file bitploy renders
// for a protected app: no login, a wrong login, the right login, a 401 the app
// sent itself, the custom domain's prompt, and the internal live router.
func TestParseRealTraefikAuthLines(t *testing.T) {
	for _, c := range []struct {
		line    string
		status  int
		blocked bool
	}{
		{`{"ClientHost":"127.0.0.1","DownstreamStatus":401,"Duration":170308,"OriginStatus":0,"RequestHost":"shop.localhost","RequestMethod":"GET","RequestPath":"/","RouterName":"app-shop@file","StartUTC":"2026-11-01T07:25:45.283319029Z","entryPointName":"websecure","level":"info","msg":"","request_User-Agent":"curl/8.5.0","time":"2026-11-01T07:25:45Z"}`, 401, true},
		{`{"ClientHost":"127.0.0.1","DownstreamStatus":401,"Duration":19276686,"OriginStatus":0,"RequestHost":"shop.localhost","RequestMethod":"GET","RequestPath":"/","RouterName":"app-shop@file","StartUTC":"2026-11-01T07:25:45.336332166Z","entryPointName":"websecure","level":"info","msg":"","request_User-Agent":"curl/8.5.0","time":"2026-11-01T07:25:45Z"}`, 401, true},
		{`{"ClientHost":"127.0.0.1","DownstreamStatus":200,"Duration":23038186,"OriginStatus":200,"RequestHost":"shop.localhost","RequestMethod":"GET","RequestPath":"/hello","RouterName":"app-shop@file","StartUTC":"2026-11-01T07:25:45.299276149Z","entryPointName":"websecure","level":"info","msg":"","request_User-Agent":"curl/8.5.0","time":"2026-11-01T07:25:45Z"}`, 200, false},
		{`{"ClientHost":"127.0.0.1","DownstreamStatus":401,"Duration":29329857,"OriginStatus":401,"RequestHost":"shop.localhost","RequestMethod":"GET","RequestPath":"/secret","RouterName":"app-shop@file","StartUTC":"2026-11-01T07:25:45.373378806Z","entryPointName":"websecure","level":"info","msg":"","request_User-Agent":"curl/8.5.0","time":"2026-11-01T07:25:45Z"}`, 401, false},
		{`{"ClientHost":"127.0.0.1","DownstreamStatus":401,"Duration":174445,"OriginStatus":0,"RequestHost":"shop.example.test","RequestMethod":"GET","RequestPath":"/","RouterName":"app-shop-shop-example-test@file","StartUTC":"2026-11-01T07:25:45.418745372Z","entryPointName":"websecure","level":"info","msg":"","request_User-Agent":"curl/8.5.0","time":"2026-11-01T07:25:45Z"}`, 401, true},
		{`{"ClientHost":"127.0.0.1","DownstreamStatus":200,"Duration":3111937,"OriginStatus":200,"RequestHost":"live.shop.bitploy.internal","RequestMethod":"GET","RequestPath":"/live","RouterName":"app-shop-live@file","StartUTC":"2026-11-01T07:25:45.601194783Z","entryPointName":"internal","level":"info","msg":"","request_User-Agent":"curl/8.5.0","time":"2026-11-01T07:25:45Z"}`, 200, false},
	} {
		e, ok := ParseLine([]byte(c.line))
		if !ok || e.Status != c.status || e.Blocked != c.blocked || e.Time.IsZero() || !strings.HasPrefix(e.Router, "app-shop") {
			t.Errorf("%+v ok=%v, want status %d blocked %v\n%s", e, ok, c.status, c.blocked, c.line)
		}
	}
}
