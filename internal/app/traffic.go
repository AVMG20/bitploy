package app

// Traffic stats: reading Traefik's access log is internal/traffic; this file
// wires it into the service and answers GET /apps/{app}/traffic.

import (
	"context"
	"strings"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/store/db"
	"github.com/AVMG20/bitploy/internal/traffic"
)

const (
	dayFormat    = "2006-01-02"
	trafficTopN  = 10
	trafficHours = 24
)

// trafficMgr returns the traffic manager, creating it on first use.
func (s *Service) trafficMgr() *traffic.Manager {
	s.ops.trafficOnce.Do(func() {
		var geo *traffic.Geo
		if s.Cfg.GeoIP {
			geo = &traffic.Geo{Dir: s.Cfg.GeoIPDir(), Log: s.Log, URL: traffic.DBIPURL}
		}
		m := traffic.New(s.Store, s.Log, s.Cfg.AccessLogPath(), geo)
		if s.Now != nil {
			m.Now = s.Now
		}
		s.ops.traffic = m
	})
	return s.ops.traffic
}

// GetTraffic returns visitors, requests, top pages, countries and referrers
// for window 24h | 7d | 30d. 24h is hourly; the others are daily. Window
// "today" is the cheap one for the Overview: today's totals and visitors_today,
// no series and no lists.
func (s *Service) GetTraffic(ctx context.Context, a Actor, ref, window string) (Traffic, error) {
	app, err := s.LoadApp(ctx, a, ref, false)
	if err != nil {
		return Traffic{}, err
	}
	// Someone is looking: save new requests every few seconds for a while.
	s.trafficMgr().Watch()
	window = strings.ToLower(strings.TrimSpace(window))
	if window == "" {
		window = "24h"
	}
	days := map[string]int{"today": 1, "24h": 2, "7d": 7, "30d": 30}[window]
	if days == 0 {
		return Traffic{}, apperr.BadRequest("invalid_window",
			"The window "+quote(window)+" is not supported.", "Use 24h, 7d or 30d.")
	}

	now := s.Now().UTC()
	today := now.Truncate(24 * time.Hour)
	firstDay := today.AddDate(0, 0, -(days - 1))
	from := firstDay.Format(dayFormat)
	q := s.Q

	daily, err := q.TrafficDailySince(ctx, db.TrafficDailySinceParams{AppID: app.ID, Day: from})
	if err != nil {
		return Traffic{}, apperr.Internal(err)
	}
	byDay := make(map[string]db.TrafficDailySinceRow, len(daily))
	var dayTotals traffic.Counts
	for _, d := range daily {
		byDay[d.Day] = d
		dayTotals.Requests += d.Requests
		dayTotals.Errors5xx += d.Errors5xx
		dayTotals.Visitors += d.Visitors
	}

	out := Traffic{App: app.Slug, Window: window, Granularity: "day"}
	out.VisitorsToday = byDay[today.Format(dayFormat)].Visitors
	out.Totals = TrafficTotals{Requests: dayTotals.Requests, Visitors: dayTotals.Visitors, Errors: dayTotals.Errors5xx}

	if window == "today" {
		out.Points, out.Pages, out.Countries, out.Referrers = []TrafficPoint{}, []TrafficItem{}, []TrafficCountry{}, []TrafficItem{}
		return out, nil
	}
	if window == "24h" {
		out.Granularity = "hour"
		start := now.Truncate(time.Hour).Add(-(trafficHours - 1) * time.Hour)
		rows, err := q.TrafficHourlySince(ctx, db.TrafficHourlySinceParams{AppID: app.ID, Hour: start.Unix()})
		if err != nil {
			return Traffic{}, apperr.Internal(err)
		}
		byHour := make(map[int64]db.TrafficHourlySinceRow, len(rows))
		var t traffic.Counts
		for _, r := range rows {
			byHour[r.Hour] = r
			t.Requests += r.Requests
			t.Errors5xx += r.Errors5xx
			t.Visitors += r.Visitors
		}
		for i := 0; i < trafficHours; i++ {
			ts := start.Add(time.Duration(i) * time.Hour)
			r := byHour[ts.Unix()]
			out.Points = append(out.Points, TrafficPoint{TS: ts, Requests: r.Requests, Visitors: r.Visitors, Errors: r.Errors5xx})
		}
		// Hourly visitors add up to more than the real number (someone seen in two hours counts
		// twice), and so do the two days. Both are upper bounds; the smaller one is closer.
		out.Totals = TrafficTotals{Requests: t.Requests, Visitors: min(t.Visitors, dayTotals.Visitors), Errors: t.Errors5xx}
	} else {
		for i := 0; i < days; i++ {
			ts := firstDay.AddDate(0, 0, i)
			r := byDay[ts.Format(dayFormat)]
			out.Points = append(out.Points, TrafficPoint{TS: ts, Requests: r.Requests, Visitors: r.Visitors, Errors: r.Errors5xx})
		}
	}
	if out.Totals.Requests > 0 {
		out.Totals.ErrorRate = float64(int64(float64(out.Totals.Errors)/float64(out.Totals.Requests)*1000+0.5)) / 10
	}

	pages, err := q.TrafficTopPaths(ctx, db.TrafficTopPathsParams{AppID: app.ID, Day: from, Limit: trafficTopN})
	if err != nil {
		return Traffic{}, apperr.Internal(err)
	}
	out.Pages = make([]TrafficItem, 0, len(pages))
	for _, p := range pages {
		out.Pages = append(out.Pages, TrafficItem{Name: p.Path, Count: p.Views})
	}
	countries, err := q.TrafficTopCountries(ctx, db.TrafficTopCountriesParams{AppID: app.ID, Day: from, Limit: trafficTopN})
	if err != nil {
		return Traffic{}, apperr.Internal(err)
	}
	out.Countries = make([]TrafficCountry, 0, len(countries))
	for _, c := range countries {
		out.Countries = append(out.Countries, TrafficCountry{Code: c.Country, Requests: c.Requests, Visitors: c.Visitors})
	}
	refs, err := q.TrafficTopReferrers(ctx, db.TrafficTopReferrersParams{AppID: app.ID, Day: from, Limit: trafficTopN})
	if err != nil {
		return Traffic{}, apperr.Internal(err)
	}
	out.Referrers = make([]TrafficItem, 0, len(refs))
	for _, r := range refs {
		out.Referrers = append(out.Referrers, TrafficItem{Name: r.Host, Count: r.Visits})
	}
	return out, nil
}
