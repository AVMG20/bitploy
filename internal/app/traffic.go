package app

// Traffic stats: reading Traefik's access log is internal/traffic; this file
// wires it into the service and answers GET /apps/{app}/traffic.

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/store/db"
	"github.com/AVMG20/bitploy/internal/traffic"
)

const (
	dayFormat      = "2006-01-02"
	trafficTopN    = 10
	trafficListMax = 500 // rows of one list asked for in full (GetTrafficList)
	trafficHours   = 24
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
		m.OnJob = s.jobRan // its flushes on the admin's Jobs tab (jobs.go)
		s.ops.traffic = m
	})
	return s.ops.traffic
}

// GetTraffic returns visitors, requests, top pages, countries, referrers and
// 404 paths for window 24h | 7d | 30d. 24h is hourly; the others are daily.
// Previous holds the totals of the equal period before, when stats go back
// that far. Window "today" is the cheap one for the Overview: today's totals
// and visitors_today, no series, no lists and no previous.
func (s *Service) GetTraffic(ctx context.Context, a Actor, ref, window string) (Traffic, error) {
	app, window, days, err := s.trafficStart(ctx, a, ref, window)
	if err != nil {
		return Traffic{}, err
	}
	now := s.Now().UTC()
	today := now.Truncate(24 * time.Hour)
	firstDay := today.AddDate(0, 0, -(days - 1))
	q := s.Q

	// Day rows from the start of the previous period: for 7d and 30d the days
	// before, for 24h the days the previous 24 hours touch.
	hourStart := now.Truncate(time.Hour).Add(-(trafficHours - 1) * time.Hour)
	prevHourStart := hourStart.Add(-trafficHours * time.Hour)
	prevFirstDay := firstDay.AddDate(0, 0, -days)
	switch window {
	case "today":
		prevFirstDay = firstDay
	case "24h":
		prevFirstDay = prevHourStart.Truncate(24 * time.Hour)
	}
	daily, err := q.TrafficDailySince(ctx, db.TrafficDailySinceParams{AppID: app.ID, Day: prevFirstDay.Format(dayFormat)})
	if err != nil {
		return Traffic{}, apperr.Internal(err)
	}
	byDay := make(map[string]traffic.Counts, len(daily))
	for _, d := range daily {
		byDay[d.Day] = traffic.Counts{Requests: d.Requests, Errors4xx: d.Errors4xx, Errors5xx: d.Errors5xx, Visitors: d.Visitors, Bots: d.Bots, Views: d.Views,
			Speed: traffic.Speed{d.Speed0, d.Speed1, d.Speed2, d.Speed3, d.Speed4, d.Speed5, d.Speed6, d.Speed7}}
	}
	// sumDays adds up the days from first to last, both included.
	sumDays := func(first, last time.Time) traffic.Counts {
		var c traffic.Counts
		for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
			c = c.Add(byDay[d.Format(dayFormat)])
		}
		return c
	}
	cur := sumDays(firstDay, today)

	out := Traffic{App: app.Slug, Window: window, Granularity: "day"}
	out.VisitorsToday = byDay[today.Format(dayFormat)].Visitors

	if window == "today" {
		out.Totals = trafficTotals(cur)
		out.Points, out.Pages, out.Countries, out.Referrers, out.NotFound = []TrafficPoint{}, []TrafficItem{}, []TrafficCountry{}, []TrafficItem{}, []TrafficItem{}
		return out, nil
	}

	// The current period ends now, partway into its last hour or day, so the
	// period before ends at the same point: its last hour or day counts for
	// the share that has passed of the current one (as if traffic were even
	// within it). Otherwise every morning would look like a drop.
	var prev traffic.Counts
	var comparable bool
	if window == "24h" {
		out.Granularity = "hour"
		rows, err := q.TrafficHourlySince(ctx, db.TrafficHourlySinceParams{AppID: app.ID, Hour: prevHourStart.Unix()})
		if err != nil {
			return Traffic{}, apperr.Internal(err)
		}
		byHour := make(map[int64]traffic.Counts, len(rows))
		var hours traffic.Counts
		prevLast := hourStart.Add(-time.Hour).Unix()
		for _, r := range rows {
			c := traffic.Counts{Requests: r.Requests, Errors4xx: r.Errors4xx, Errors5xx: r.Errors5xx, Visitors: r.Visitors, Bots: r.Bots, Views: r.Views,
				Speed: traffic.Speed{r.Speed0, r.Speed1, r.Speed2, r.Speed3, r.Speed4, r.Speed5, r.Speed6, r.Speed7}}
			byHour[r.Hour] = c
			switch {
			case r.Hour >= hourStart.Unix():
				hours = hours.Add(c)
			case r.Hour == prevLast:
				prev = prev.Add(partOf(c, now.Sub(now.Truncate(time.Hour)), time.Hour))
			default:
				prev = prev.Add(c)
			}
		}
		for i := 0; i < trafficHours; i++ {
			ts := hourStart.Add(time.Duration(i) * time.Hour)
			out.Points = append(out.Points, trafficPoint(ts, byHour[ts.Unix()]))
		}
		// Hourly visitors add up to more than the real number (someone seen in two hours counts
		// twice), and so do the days the hours touch. Both are upper bounds; the smaller one is closer.
		hours.Visitors = min(hours.Visitors, cur.Visitors)
		cur = hours
		prev.Visitors = min(prev.Visitors, sumDays(prevFirstDay, hourStart.Add(-time.Hour).Truncate(24*time.Hour)).Visitors)
		first, err := q.TrafficFirstHour(ctx)
		if err != nil {
			return Traffic{}, apperr.Internal(err)
		}
		comparable = first > 0 && first <= prevHourStart.Unix() && app.CreatedAt <= prevHourStart.Unix()
	} else {
		for i := 0; i < days; i++ {
			ts := firstDay.AddDate(0, 0, i)
			out.Points = append(out.Points, trafficPoint(ts, byDay[ts.Format(dayFormat)]))
		}
		prevLast := firstDay.AddDate(0, 0, -1)
		prev = sumDays(prevFirstDay, prevLast.AddDate(0, 0, -1)).Add(partOf(byDay[prevLast.Format(dayFormat)], now.Sub(today), 24*time.Hour))
		first, err := q.TrafficFirstDay(ctx)
		if err != nil {
			return Traffic{}, apperr.Internal(err)
		}
		comparable = first != "" && first <= prevFirstDay.Format(dayFormat) && app.CreatedAt <= prevFirstDay.Unix()
	}
	out.Totals = trafficTotals(cur)
	// Only when the whole previous period was counted: the app existed and the
	// stats go back that far (not pruned, not from before bitploy counted).
	if comparable {
		p := trafficTotals(prev)
		out.Previous = &p
	}

	if err := s.trafficLists(ctx, &out, app.ID, firstDay.Format(dayFormat), trafficTopN, "pages", "countries", "referrers", "not_found"); err != nil {
		return Traffic{}, err
	}
	return out, nil
}

// GetTrafficList returns one list (pages, countries, referrers or not_found)
// of a window in full, up to trafficListMax rows. Totals, points and the other
// lists stay empty.
func (s *Service) GetTrafficList(ctx context.Context, a Actor, ref, window, list string) (Traffic, error) {
	app, window, days, err := s.trafficStart(ctx, a, ref, window)
	if err != nil {
		return Traffic{}, err
	}
	list = strings.ToLower(strings.TrimSpace(list))
	if !trafficListNames[list] {
		return Traffic{}, apperr.BadRequest("invalid_list",
			"That list is not supported.", "Use pages, countries, referrers or not_found.")
	}
	from := s.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -(days - 1)).Format(dayFormat)
	out := Traffic{App: app.Slug, Window: window, Granularity: "day",
		Points: []TrafficPoint{}, Pages: []TrafficItem{}, Countries: []TrafficCountry{}, Referrers: []TrafficItem{}, NotFound: []TrafficItem{}}
	if window == "24h" {
		out.Granularity = "hour"
	}
	if err := s.trafficLists(ctx, &out, app.ID, from, trafficListMax, list); err != nil {
		return Traffic{}, err
	}
	return out, nil
}

var trafficListNames = map[string]bool{"pages": true, "countries": true, "referrers": true, "not_found": true}

// trafficStart loads the app, checks the window and says someone is looking.
func (s *Service) trafficStart(ctx context.Context, a Actor, ref, window string) (db.App, string, int, error) {
	app, err := s.LoadApp(ctx, a, ref, false)
	if err != nil {
		return db.App{}, "", 0, err
	}
	// Someone is looking: save new requests every few seconds for a while.
	s.trafficMgr().Watch()
	window = strings.ToLower(strings.TrimSpace(window))
	if window == "" {
		window = "24h"
	}
	days := map[string]int{"today": 1, "24h": 2, "7d": 7, "30d": 30}[window]
	if days == 0 {
		return db.App{}, "", 0, apperr.BadRequest("invalid_window",
			"The window "+quote(window)+" is not supported.", "Use 24h, 7d or 30d.")
	}
	return app, window, days, nil
}

// trafficLists fills the named lists of out with up to limit rows each,
// counted over the days from on.
func (s *Service) trafficLists(ctx context.Context, out *Traffic, appID, from string, limit int64, lists ...string) error {
	q := s.Q
	for _, list := range lists {
		switch list {
		case "pages":
			rows, err := q.TrafficTopPaths(ctx, db.TrafficTopPathsParams{AppID: appID, Day: from, Limit: limit})
			if err != nil {
				return apperr.Internal(err)
			}
			out.Pages = make([]TrafficItem, 0, len(rows))
			for _, r := range rows {
				out.Pages = append(out.Pages, TrafficItem{Name: r.Path, Count: r.Views})
			}
		case "countries":
			rows, err := q.TrafficTopCountries(ctx, db.TrafficTopCountriesParams{AppID: appID, Day: from, Limit: limit})
			if err != nil {
				return apperr.Internal(err)
			}
			out.Countries = make([]TrafficCountry, 0, len(rows))
			for _, r := range rows {
				out.Countries = append(out.Countries, TrafficCountry{Code: r.Country, Requests: r.Requests, Visitors: r.Visitors})
			}
		case "referrers":
			rows, err := q.TrafficTopReferrers(ctx, db.TrafficTopReferrersParams{AppID: appID, Day: from, Limit: limit})
			if err != nil {
				return apperr.Internal(err)
			}
			out.Referrers = make([]TrafficItem, 0, len(rows))
			for _, r := range rows {
				out.Referrers = append(out.Referrers, TrafficItem{Name: r.Host, Count: r.Visits})
			}
		case "not_found":
			rows, err := q.TrafficTopNotFound(ctx, db.TrafficTopNotFoundParams{AppID: appID, Day: from, Limit: limit})
			if err != nil {
				return apperr.Internal(err)
			}
			out.NotFound = make([]TrafficItem, 0, len(rows))
			for _, r := range rows {
				out.NotFound = append(out.NotFound, TrafficItem{Name: r.Path, Count: r.Hits})
			}
		}
	}
	return nil
}

// partOf returns elapsed/whole of c, rounded.
func partOf(c traffic.Counts, elapsed, whole time.Duration) traffic.Counts {
	f := float64(elapsed) / float64(whole)
	part := func(n int64) int64 { return int64(math.Round(float64(n) * f)) }
	var sp traffic.Speed
	for i, n := range c.Speed {
		sp[i] = part(n)
	}
	return traffic.Counts{Requests: part(c.Requests), Errors4xx: part(c.Errors4xx), Errors5xx: part(c.Errors5xx), Visitors: part(c.Visitors), Bots: part(c.Bots), Views: part(c.Views), Speed: sp}
}

// quantileMS is traffic.Speed.Quantile as a JSON value: null without answers.
func quantileMS(s traffic.Speed, q float64) *int64 {
	ms, ok := s.Quantile(q)
	if !ok {
		return nil
	}
	return &ms
}

func trafficPoint(ts time.Time, c traffic.Counts) TrafficPoint {
	return TrafficPoint{TS: ts, Requests: c.Requests, Visitors: c.Visitors, Errors: c.Errors5xx, ClientErrors: c.Errors4xx,
		Bots: c.Bots, PageViews: c.Views, MedianMS: quantileMS(c.Speed, 0.5), P95MS: quantileMS(c.Speed, 0.95)}
}

func trafficTotals(c traffic.Counts) TrafficTotals {
	t := TrafficTotals{Requests: c.Requests, Visitors: c.Visitors, Errors: c.Errors5xx, ClientErrors: c.Errors4xx, Bots: c.Bots,
		PageViews: c.Views, Timed: c.Speed.Total(), Slow: c.Speed.Slow(), MedianMS: quantileMS(c.Speed, 0.5), P95MS: quantileMS(c.Speed, 0.95)}
	if c.Requests > 0 {
		t.ErrorRate = float64(int64(float64(c.Errors5xx)/float64(c.Requests)*1000+0.5)) / 10
	}
	return t
}
