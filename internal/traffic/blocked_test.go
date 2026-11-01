package traffic

import (
	"fmt"
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
