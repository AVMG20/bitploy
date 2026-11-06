package traffic

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"sync"
	"time"
)

// Caps that keep memory and storage small however much traffic (or junk) arrives.
const (
	MaxPathsPerDay     = 500    // distinct paths per app and day; the rest become OtherName
	MaxNotFoundPerDay  = 500    // distinct 404 paths per app and day; the rest become OtherName
	MaxReferrersPerDay = 200    // distinct referrer hosts per app and day (DirectName aside); the rest are dropped
	MaxVisitorsPerSet  = 100000 // visitor hashes kept per app and hour or day
	OtherName          = "(other)"
	DirectName         = "(direct)" // page views without a Referer; no host has parentheses
	UnknownCountry     = "unknown"
)

// Counts are request totals for one bucket (an hour or a day). Views are the
// page views of people (what the top pages count, "(other)" included); Speed
// is how fast the app answered people (see Aggregator.Add).
type Counts struct {
	Requests, Errors4xx, Errors5xx, Visitors, Bots, Views int64
	Speed                                                 Speed
}

func (c Counts) Add(o Counts) Counts {
	return Counts{c.Requests + o.Requests, c.Errors4xx + o.Errors4xx, c.Errors5xx + o.Errors5xx, c.Visitors + o.Visitors, c.Bots + o.Bots, c.Views + o.Views, c.Speed.Add(o.Speed)}
}

// CountryCounts are the numbers per country and day.
type CountryCounts struct{ Requests, Visitors int64 }

// DayDelta is everything new for one app and day since the last Flush.
type DayDelta struct {
	Counts
	Paths     map[string]int64
	NotFound  map[string]int64 // 404 paths
	Referrers map[string]int64
	Countries map[string]CountryCounts
}

// AppDelta is everything new for one app since the last Flush.
type AppDelta struct {
	Hours map[int64]Counts // key: unix seconds at the start of the hour
	Days  map[string]*DayDelta
}

// Snapshot is what Flush hands to the database writer.
type Snapshot map[string]*AppDelta // key: app id

// Aggregator counts entries in memory. Visitor hashes, the daily salts and the
// path caps live only here and only for the current and previous day.
type Aggregator struct {
	mu   sync.Mutex
	now  func() time.Time
	salt map[string][]byte // by day; random, never written anywhere
	apps map[string]*appAgg
}

type appAgg struct {
	hours map[int64]*hourAgg
	days  map[string]*dayAgg
}

type hourAgg struct {
	delta Counts
	seen  map[uint64]struct{}
}

type dayAgg struct {
	delta      Counts
	seen       map[uint64]struct{}
	paths      map[string]int64
	knownPaths map[string]struct{}
	notFound   map[string]int64
	knownNF    map[string]struct{}
	refs       map[string]int64
	knownRefs  map[string]struct{}
	countries  map[string]CountryCounts
}

func (a *Aggregator) app(id string) *appAgg {
	ap := a.apps[id]
	if ap == nil {
		ap = &appAgg{hours: map[int64]*hourAgg{}, days: map[string]*dayAgg{}}
		a.apps[id] = ap
	}
	return ap
}

func (ap *appAgg) hour(h int64) *hourAgg {
	v := ap.hours[h]
	if v == nil {
		v = &hourAgg{seen: map[uint64]struct{}{}}
		ap.hours[h] = v
	}
	return v
}

func (ap *appAgg) day(day string) *dayAgg {
	d := ap.days[day]
	if d == nil {
		d = &dayAgg{
			seen: map[uint64]struct{}{}, paths: map[string]int64{}, knownPaths: map[string]struct{}{},
			notFound: map[string]int64{}, knownNF: map[string]struct{}{},
			refs: map[string]int64{}, knownRefs: map[string]struct{}{}, countries: map[string]CountryCounts{},
		}
		ap.days[day] = d
	}
	return d
}

// Restore adds a snapshot back, after it could not be saved.
func (a *Aggregator) Restore(snap Snapshot) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for id, delta := range snap {
		ap := a.app(id)
		for hour, c := range delta.Hours {
			h := ap.hour(hour)
			h.delta = h.delta.Add(c)
		}
		for day, dd := range delta.Days {
			d := ap.day(day)
			d.delta = d.delta.Add(dd.Counts)
			for p, n := range dd.Paths {
				d.paths[p] += n
				d.knownPaths[p] = struct{}{}
			}
			for p, n := range dd.NotFound {
				d.notFound[p] += n
				d.knownNF[p] = struct{}{}
			}
			for r, n := range dd.Referrers {
				d.refs[r] += n
				if r != DirectName {
					d.knownRefs[r] = struct{}{}
				}
			}
			for c, n := range dd.Countries {
				cc := d.countries[c]
				cc.Requests += n.Requests
				cc.Visitors += n.Visitors
				d.countries[c] = cc
			}
		}
	}
}

func NewAggregator(now func() time.Time) *Aggregator {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Aggregator{now: now, salt: map[string][]byte{}, apps: map[string]*appAgg{}}
}

// Add counts one request for an app. e.Country must already be resolved
// (empty means unknown).
func (a *Aggregator) Add(appID string, e Entry) {
	t := e.Time
	if t.IsZero() {
		t = a.now()
	}
	t = t.UTC()
	hour, day := t.Truncate(time.Hour).Unix(), t.Format("2006-01-02")
	country := e.Country
	if country == "" {
		country = UnknownCountry
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	ap := a.app(appID)
	h, d := ap.hour(hour), ap.day(day)

	h.delta.Requests++
	d.delta.Requests++
	switch {
	case e.Status >= 500:
		h.delta.Errors5xx++
		d.delta.Errors5xx++
	case e.Status >= 400:
		h.delta.Errors4xx++
		d.delta.Errors4xx++
	}
	cc := d.countries[country]
	cc.Requests++

	bot := IsBot(e.UserAgent)
	if bot {
		h.delta.Bots++
		d.delta.Bots++
	} else {
		v := a.visitorHash(day, e.IP, e.UserAgent)
		if _, ok := h.seen[v]; !ok && len(h.seen) < MaxVisitorsPerSet {
			h.seen[v] = struct{}{}
			h.delta.Visitors++
		}
		if _, ok := d.seen[v]; !ok && len(d.seen) < MaxVisitorsPerSet {
			d.seen[v] = struct{}{}
			d.delta.Visitors++
			cc.Visitors++
		}
	}
	d.countries[country] = cc

	asset := IsAsset(e.Path)
	// Answer times of people asking for pages or data, not files (fast and
	// many, they would hide a slow app) and not bots (an uptime check every
	// minute would too). A 101 is a websocket and a stream of server-sent
	// events stays open too: their duration is how long the connection stayed
	// open, not how long the answer took.
	if !bot && !asset && e.Duration > 0 && e.Status != 101 && !e.Stream {
		b := SpeedBucket(e.Duration)
		h.delta.Speed[b]++
		d.delta.Speed[b]++
	}

	if !bot && e.Method == "GET" && e.Status >= 200 && e.Status < 400 && !asset {
		h.delta.Views++
		d.delta.Views++
		d.paths[capKey(d.knownPaths, e.Path, MaxPathsPerDay-1, OtherName)]++
		// No Referer: typed in, a bookmark, an app. Outside the cap, so never dropped.
		// A link from the site itself is not "came from" at all.
		if e.Referer == "" {
			d.refs[DirectName]++
		} else if ref := ReferrerHost(e.Referer, e.Host); ref != "" {
			if k := capKey(d.knownRefs, ref, MaxReferrersPerDay, ""); k != "" {
				d.refs[k]++
			}
		}
	}
	// Broken links, also to files; any method, since a form posting to a
	// missing path is just as broken.
	if !bot && e.Status == 404 {
		d.notFound[capKey(d.knownNF, e.Path, MaxNotFoundPerDay-1, OtherName)]++
	}
}

// capKey returns key while fewer than limit distinct keys are known, then
// only keys already known; any other key becomes other (or "" to drop it).
func capKey(known map[string]struct{}, key string, limit int, other string) string {
	if _, ok := known[key]; ok {
		return key
	}
	if len(known) >= limit {
		return other
	}
	known[key] = struct{}{}
	return key
}

// visitorHash is the first 8 bytes of SHA-256(day salt, IP, user agent).
// The salt exists only in memory and only for its day, so a hash cannot be
// linked to a visitor on another day, nor reversed to an address once the
// salt is gone.
func (a *Aggregator) visitorHash(day, ip, ua string) uint64 {
	salt := a.salt[day]
	if salt == nil {
		salt = make([]byte, 16)
		_, _ = rand.Read(salt)
		a.salt[day] = salt
	}
	h := sha256.New()
	h.Write(salt)
	h.Write([]byte(ip))
	h.Write([]byte{0})
	h.Write([]byte(ua))
	return binary.BigEndian.Uint64(h.Sum(nil))
}

// Flush returns what is new since the last call and starts counting again from
// zero. Visitor sets stay for the current and previous day (and hour), then
// go; so do the salts.
func (a *Aggregator) Flush() Snapshot {
	now := a.now().UTC()
	oldHour := now.Truncate(time.Hour).Add(-time.Hour).Unix()
	oldDay := now.AddDate(0, 0, -1).Format("2006-01-02")

	a.mu.Lock()
	defer a.mu.Unlock()
	snap := Snapshot{}
	for id, ap := range a.apps {
		out := &AppDelta{Hours: map[int64]Counts{}, Days: map[string]*DayDelta{}}
		for hour, h := range ap.hours {
			if h.delta != (Counts{}) {
				out.Hours[hour] = h.delta
				h.delta = Counts{}
			}
			if hour < oldHour {
				delete(ap.hours, hour)
			}
		}
		for day, d := range ap.days {
			if len(d.paths) > 0 || len(d.notFound) > 0 || len(d.refs) > 0 || len(d.countries) > 0 || d.delta != (Counts{}) {
				out.Days[day] = &DayDelta{Counts: d.delta, Paths: d.paths, NotFound: d.notFound, Referrers: d.refs, Countries: d.countries}
				d.delta = Counts{}
				d.paths, d.notFound, d.refs, d.countries = map[string]int64{}, map[string]int64{}, map[string]int64{}, map[string]CountryCounts{}
			}
			if day < oldDay {
				delete(ap.days, day)
			}
		}
		if len(out.Hours) > 0 || len(out.Days) > 0 {
			snap[id] = out
		}
		if len(ap.hours) == 0 && len(ap.days) == 0 {
			delete(a.apps, id)
		}
	}
	for day := range a.salt {
		if day < oldDay {
			delete(a.salt, day)
		}
	}
	return snap
}

// Forget drops everything held for an app (it was deleted).
func (a *Aggregator) Forget(appID string) {
	a.mu.Lock()
	delete(a.apps, appID)
	a.mu.Unlock()
}
