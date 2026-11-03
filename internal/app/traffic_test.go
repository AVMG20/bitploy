package app

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestGetTraffic(t *testing.T) {
	e := newOpsEnv(t)
	ctx := context.Background()
	e.addApp(t, "app_t", "traffic-app", "usr_owner", "web")
	now := e.now // 2026-10-29 12:00 UTC
	hour := func(back int) int64 { return now.Truncate(time.Hour).Add(-time.Duration(back) * time.Hour).Unix() }
	day := func(back int) string { return now.AddDate(0, 0, -back).Format("2006-01-02") }

	e.exec(t, `INSERT INTO traffic_hourly (app_id, hour, requests, errors_5xx, visitors) VALUES ('app_t', ?, 40, 2, 10), ('app_t', ?, 60, 0, 8), ('app_t', ?, 99, 9, 99)`,
		hour(0), hour(5), hour(30)) // the last one is outside 24 hours
	e.exec(t, `INSERT INTO traffic_daily (app_id, day, requests, errors_5xx, visitors) VALUES ('app_t', ?, 100, 2, 12), ('app_t', ?, 500, 5, 70), ('app_t', ?, 900, 0, 90)`,
		day(0), day(1), day(20))
	e.exec(t, `INSERT INTO traffic_paths (app_id, day, path, views) VALUES ('app_t', ?, '/', 30), ('app_t', ?, '/', 20), ('app_t', ?, '/about', 5), ('app_t', ?, '/old', 7), ('app_t', ?, '(other)', 50)`,
		day(0), day(1), day(0), day(20), day(0))
	e.exec(t, `INSERT INTO traffic_countries (app_id, day, country, requests, visitors) VALUES ('app_t', ?, 'NL', 80, 9), ('app_t', ?, 'unknown', 20, 3)`, day(0), day(0))
	e.exec(t, `INSERT INTO traffic_referrers (app_id, day, host, visits) VALUES ('app_t', ?, 'example.com', 4)`, day(0))

	tr, err := e.svc.GetTraffic(ctx, e.owner, "traffic-app", "24h")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Granularity != "hour" || len(tr.Points) != 24 {
		t.Fatalf("24h: %s, %d points", tr.Granularity, len(tr.Points))
	}
	if last := tr.Points[23]; !last.TS.Equal(now.Truncate(time.Hour)) || last.Requests != 40 || last.Visitors != 10 || last.Errors != 2 {
		t.Errorf("last point: %+v", last)
	}
	if tr.Points[0].Requests != 0 {
		t.Errorf("empty hours are zero: %+v", tr.Points[0])
	}
	// 100 requests and 2 errors in the last 24 hours: 2%. Hourly visitors (18) and the two days
	// (82) are both upper bounds, the smaller one is shown.
	if tr.Totals.Requests != 100 || tr.Totals.Errors != 2 || tr.Totals.ErrorRate != 2 || tr.Totals.Visitors != 18 {
		t.Errorf("24h totals: %+v", tr.Totals)
	}
	if tr.VisitorsToday != 12 {
		t.Errorf("visitors today = %d", tr.VisitorsToday)
	}
	// Lists cover today and yesterday; "(other)" is not a page.
	if len(tr.Pages) != 2 || tr.Pages[0] != (TrafficItem{Name: "/", Count: 50}) || tr.Pages[1].Name != "/about" {
		t.Errorf("pages: %+v", tr.Pages)
	}
	if len(tr.Countries) != 2 || tr.Countries[0] != (TrafficCountry{Code: "NL", Requests: 80, Visitors: 9}) {
		t.Errorf("countries: %+v", tr.Countries)
	}
	if len(tr.Referrers) != 1 || tr.Referrers[0] != (TrafficItem{Name: "example.com", Count: 4}) {
		t.Errorf("referrers: %+v", tr.Referrers)
	}

	tr, err = e.svc.GetTraffic(ctx, e.owner, "traffic-app", "7d")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Granularity != "day" || len(tr.Points) != 7 || tr.Points[6].Requests != 100 || tr.Points[5].Visitors != 70 {
		t.Errorf("7d points: %+v", tr.Points)
	}
	if tr.Totals.Requests != 600 || tr.Totals.Visitors != 82 || tr.Totals.Errors != 7 || tr.Totals.ErrorRate != 1.2 {
		t.Errorf("7d totals: %+v", tr.Totals)
	}

	tr, err = e.svc.GetTraffic(ctx, e.owner, "traffic-app", "30d")
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Points) != 30 || tr.Totals.Requests != 1500 || tr.Totals.Visitors != 172 {
		t.Errorf("30d: %d points, totals %+v", len(tr.Points), tr.Totals)
	}
	if len(tr.Pages) != 3 {
		t.Errorf("30d pages: %+v", tr.Pages)
	}
}

func TestGetTrafficToday(t *testing.T) {
	e := newOpsEnv(t)
	e.addApp(t, "app_t", "traffic-app", "usr_owner", "web")
	e.exec(t, `INSERT INTO traffic_daily (app_id, day, requests, visitors) VALUES ('app_t', ?, 10, 4), ('app_t', ?, 99, 50)`,
		e.now.Format("2006-01-02"), e.now.AddDate(0, 0, -1).Format("2006-01-02"))
	e.exec(t, `INSERT INTO traffic_paths (app_id, day, path, views) VALUES ('app_t', ?, '/', 10)`, e.now.Format("2006-01-02"))
	tr, err := e.svc.GetTraffic(context.Background(), e.owner, "traffic-app", "today")
	if err != nil {
		t.Fatal(err)
	}
	if tr.VisitorsToday != 4 || tr.Totals.Requests != 10 || tr.Totals.Visitors != 4 {
		t.Errorf("today: %+v", tr)
	}
	if len(tr.Points) != 0 || len(tr.Pages) != 0 || tr.Points == nil || tr.Pages == nil {
		t.Errorf("today must carry no series or lists, as empty arrays: %+v", tr)
	}
}

func TestGetTrafficEmptyAndErrors(t *testing.T) {
	e := newOpsEnv(t)
	ctx := context.Background()
	e.addApp(t, "app_t", "traffic-app", "usr_owner", "web")

	tr, err := e.svc.GetTraffic(ctx, e.owner, "traffic-app", "")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Window != "24h" || len(tr.Points) != 24 || tr.Totals.Requests != 0 || tr.Totals.ErrorRate != 0 {
		t.Errorf("empty: %+v", tr)
	}
	if tr.Pages == nil || tr.Countries == nil || tr.Referrers == nil {
		t.Error("empty lists must be [] not null")
	}
	if _, err := e.svc.GetTraffic(ctx, e.owner, "traffic-app", "1y"); depCode(err) != "invalid_window" {
		t.Errorf("bad window: %v", err)
	}
	if _, err := e.svc.GetTraffic(ctx, e.other, "traffic-app", "24h"); depCode(err) != "app_not_found" {
		t.Errorf("other user's app: %v", err)
	}
	if _, err := e.svc.GetTraffic(ctx, e.admin, "traffic-app", "24h"); err != nil {
		t.Errorf("admin session: %v", err)
	}
}

func TestGetTrafficPreviousAndNewCounts(t *testing.T) {
	e := newOpsEnv(t)
	ctx := context.Background()
	e.addApp(t, "app_t", "traffic-app", "usr_owner", "web")
	now := e.now // 2026-10-29 12:00 UTC
	hour := func(back int) int64 { return now.Truncate(time.Hour).Add(-time.Duration(back) * time.Hour).Unix() }
	day := func(back int) string { return now.AddDate(0, 0, -back).Format("2006-01-02") }

	// Current 24 hours: hour 0. Previous 24 hours: hours 24 to 47.
	e.exec(t, `INSERT INTO traffic_hourly (app_id, hour, requests, errors_4xx, errors_5xx, visitors, bots) VALUES
		('app_t', ?, 100, 7, 1, 10, 40), ('app_t', ?, 50, 3, 5, 20, 10), ('app_t', ?, 30, 0, 0, 30, 0), ('app_t', ?, 9, 0, 0, 9, 0)`,
		hour(0), hour(47), hour(30), hour(48))
	e.exec(t, `INSERT INTO traffic_daily (app_id, day, requests, errors_4xx, errors_5xx, visitors, bots) VALUES
		('app_t', ?, 100, 7, 1, 8, 40), ('app_t', ?, 30, 0, 0, 20, 0), ('app_t', ?, 50, 3, 5, 15, 10),
		('app_t', ?, 30, 0, 0, 5, 0), ('app_t', ?, 70, 0, 0, 7, 0), ('app_t', ?, 999, 0, 0, 99, 0)`,
		day(0), day(1), day(2), day(7), day(13), day(14))
	e.exec(t, `INSERT INTO traffic_not_found (app_id, day, path, hits) VALUES ('app_t', ?, '/gone', 3), ('app_t', ?, '/old.png', 5), ('app_t', ?, '(other)', 9)`,
		day(0), day(1), day(0))
	e.exec(t, `INSERT INTO traffic_referrers (app_id, day, host, visits) VALUES ('app_t', ?, '(direct)', 6), ('app_t', ?, 'example.com', 2)`, day(0), day(0))

	tr, err := e.svc.GetTraffic(ctx, e.owner, "traffic-app", "24h")
	if err != nil {
		t.Fatal(err)
	}
	if want := (TrafficTotals{Requests: 100, Visitors: 10, Errors: 1, ErrorRate: 1, ClientErrors: 7, Bots: 40}); tr.Totals != want {
		t.Errorf("24h totals: %+v, want %+v", tr.Totals, want)
	}
	if last := tr.Points[23]; last.ClientErrors != 7 {
		t.Errorf("last point: %+v", last)
	}
	// Hours 47 and 30 (not 48). Visitors: the hours say 50, the two days they touch say 35.
	if tr.Previous == nil {
		t.Fatal("24h: no previous")
	}
	if want := (TrafficTotals{Requests: 80, Visitors: 35, Errors: 5, ErrorRate: 6.3, ClientErrors: 3, Bots: 10}); *tr.Previous != want {
		t.Errorf("24h previous: %+v, want %+v", *tr.Previous, want)
	}
	if len(tr.NotFound) != 2 || tr.NotFound[0] != (TrafficItem{Name: "/old.png", Count: 5}) {
		t.Errorf("not found: %+v", tr.NotFound)
	}
	if len(tr.Referrers) != 2 || tr.Referrers[0] != (TrafficItem{Name: "(direct)", Count: 6}) {
		t.Errorf("referrers: %+v", tr.Referrers)
	}

	// 7 days: the 7 before are days 7 to 13.
	tr, err = e.svc.GetTraffic(ctx, e.owner, "traffic-app", "7d")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Totals.Requests != 180 || tr.Totals.Bots != 50 || tr.Totals.ClientErrors != 10 {
		t.Errorf("7d totals: %+v", tr.Totals)
	}
	// At 12:00 half of today has passed, so half of day 7 counts: 70 + 15 requests, 7 + 3 visitors.
	if tr.Previous == nil || tr.Previous.Requests != 85 || tr.Previous.Visitors != 10 {
		t.Errorf("7d previous: %+v", tr.Previous)
	}
	// 30 days: stats go back 14 days only, so there is nothing to compare with.
	if tr, err = e.svc.GetTraffic(ctx, e.owner, "traffic-app", "30d"); err != nil || tr.Previous != nil {
		t.Errorf("30d previous: %+v, %v", tr.Previous, err)
	}
	if tr, err = e.svc.GetTraffic(ctx, e.owner, "traffic-app", "today"); err != nil || tr.Previous != nil || tr.NotFound == nil {
		t.Errorf("today: %+v, %v", tr, err)
	}

	// Half an hour into the current hour, half of hour 24 counts too.
	e.exec(t, `INSERT INTO traffic_hourly (app_id, hour, requests, visitors) VALUES ('app_t', ?, 10, 2)`, hour(24))
	e.now = now.Add(30 * time.Minute)
	if tr, err = e.svc.GetTraffic(ctx, e.owner, "traffic-app", "24h"); err != nil || tr.Previous == nil || tr.Previous.Requests != 85 {
		t.Errorf("24h previous at half past: %+v, %v", tr.Previous, err)
	}
	e.now = now

	// An app younger than the previous period has nothing to compare with either.
	e.exec(t, `UPDATE apps SET created_at = ? WHERE id = 'app_t'`, now.AddDate(0, 0, -10).Unix())
	if tr, err = e.svc.GetTraffic(ctx, e.owner, "traffic-app", "7d"); err != nil || tr.Previous != nil {
		t.Errorf("young app 7d previous: %+v, %v", tr.Previous, err)
	}
	if tr, err = e.svc.GetTraffic(ctx, e.owner, "traffic-app", "24h"); err != nil || tr.Previous == nil {
		t.Errorf("young app 24h previous: %+v, %v", tr.Previous, err)
	}
}

func TestGetTrafficList(t *testing.T) {
	e := newOpsEnv(t)
	ctx := context.Background()
	e.addApp(t, "app_t", "traffic-app", "usr_owner", "web")
	for i := 0; i < 15; i++ {
		e.exec(t, `INSERT INTO traffic_paths (app_id, day, path, views) VALUES ('app_t', ?, ?, ?)`, e.now.Format("2006-01-02"), fmt.Sprintf("/p%d", i), i+1)
	}
	tr, err := e.svc.GetTraffic(ctx, e.owner, "traffic-app", "7d")
	if err != nil || len(tr.Pages) != 10 {
		t.Fatalf("top: %d pages, %v", len(tr.Pages), err)
	}
	tr, err = e.svc.GetTrafficList(ctx, e.owner, "traffic-app", "7d", "pages")
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Pages) != 15 || tr.Pages[0].Name != "/p14" || tr.Totals.Requests != 0 || tr.Points == nil || tr.Countries == nil || tr.NotFound == nil {
		t.Errorf("full list: %+v", tr)
	}
	if tr, err = e.svc.GetTrafficList(ctx, e.owner, "traffic-app", "7d", "not_found"); err != nil || tr.NotFound == nil || len(tr.Pages) != 0 {
		t.Errorf("not found list: %+v, %v", tr, err)
	}
	if _, err := e.svc.GetTrafficList(ctx, e.owner, "traffic-app", "7d", "users"); depCode(err) != "invalid_list" {
		t.Errorf("bad list: %v", err)
	}
	if _, err := e.svc.GetTrafficList(ctx, e.owner, "traffic-app", "1y", "pages"); depCode(err) != "invalid_window" {
		t.Errorf("bad window: %v", err)
	}
	if _, err := e.svc.GetTrafficList(ctx, e.other, "traffic-app", "7d", "pages"); depCode(err) != "app_not_found" {
		t.Errorf("other user's app: %v", err)
	}
}
