package app

import (
	"context"
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
