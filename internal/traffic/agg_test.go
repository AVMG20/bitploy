package traffic

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 30, 14, 3, 0, 0, time.UTC)

const (
	firefox = "Mozilla/5.0 (X11; Linux x86_64) Firefox/130.0"
	safari  = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Safari/604.1"
)

func req(path, ip, ua string) Entry {
	return Entry{Method: "GET", Path: path, Status: 200, IP: ip, UserAgent: ua, Time: t0, Host: "a.bitploy.nl"}
}

func newAgg() *Aggregator { return NewAggregator(func() time.Time { return t0 }) }

func TestAggregateCounts(t *testing.T) {
	a := newAgg()
	home := req("/", "203.0.113.1", firefox)
	home.Country = "NL"
	a.Add("app_1", home)
	a.Add("app_1", home) // same visitor again
	other := req("/about", "203.0.113.2", safari)
	other.Country = "DE"
	other.Referer = "https://www.example.com/x"
	a.Add("app_1", other)
	a.Add("app_1", req("/app.js", "203.0.113.1", firefox)) // asset: counted, not a page
	notFound := req("/nope", "203.0.113.3", firefox)
	notFound.Status = 404
	a.Add("app_1", notFound)
	broken := req("/crash", "203.0.113.4", firefox)
	broken.Status = 502
	a.Add("app_1", broken)
	post := req("/form", "203.0.113.1", firefox)
	post.Method = "POST"
	a.Add("app_1", post)
	a.Add("app_1", req("/", "198.51.100.9", "Googlebot/2.1")) // bot: a request, no visitor, no page
	a.Add("app_2", req("/", "203.0.113.1", firefox))

	snap := a.Flush()
	d := snap["app_1"]
	hour := t0.Truncate(time.Hour).Unix()
	if got, want := d.Hours[hour], (Counts{Requests: 8, Errors4xx: 1, Errors5xx: 1, Visitors: 4, Bots: 1, Views: 3}); got != want {
		t.Errorf("hour = %+v, want %+v", got, want)
	}
	day := d.Days["2026-10-30"]
	if day.Counts != d.Hours[hour] {
		t.Errorf("day counts %+v differ from hour %+v", day.Counts, d.Hours[hour])
	}
	// Pages: GET, status below 400, no assets, no bots. "/" twice, "/about" once.
	if len(day.Paths) != 2 || day.Paths["/"] != 2 || day.Paths["/about"] != 1 {
		t.Errorf("paths = %v", day.Paths)
	}
	// "/" twice without a Referer: direct.
	if len(day.Referrers) != 2 || day.Referrers["example.com"] != 1 || day.Referrers[DirectName] != 2 {
		t.Errorf("referrers = %v", day.Referrers)
	}
	if len(day.NotFound) != 1 || day.NotFound["/nope"] != 1 {
		t.Errorf("not found = %v", day.NotFound)
	}
	if got := day.Countries["NL"]; got != (CountryCounts{Requests: 2, Visitors: 1}) {
		t.Errorf("NL = %+v", got)
	}
	if got := day.Countries["DE"]; got != (CountryCounts{Requests: 1, Visitors: 1}) {
		t.Errorf("DE = %+v", got)
	}
	if got := day.Countries[UnknownCountry]; got.Requests != 5 || got.Visitors != 2 {
		t.Errorf("unknown = %+v", got)
	}
	if snap["app_2"].Hours[hour].Visitors != 1 {
		t.Errorf("app_2 = %+v", snap["app_2"].Hours[hour])
	}

	// Flushing again with nothing new reports nothing.
	if again := a.Flush(); len(again) != 0 {
		t.Errorf("second flush: %v", again)
	}
}

func TestVisitorsStayUniqueAcrossFlushes(t *testing.T) {
	a := newAgg()
	a.Add("app_1", req("/", "203.0.113.1", firefox))
	if v := a.Flush()["app_1"].Days["2026-10-30"].Visitors; v != 1 {
		t.Fatalf("first flush: %d visitors", v)
	}
	// Same visitor a minute later: a request, but not a new visitor.
	a.Add("app_1", req("/b", "203.0.113.1", firefox))
	a.Add("app_1", req("/b", "203.0.113.5", firefox))
	d := a.Flush()["app_1"].Days["2026-10-30"]
	if d.Requests != 2 || d.Visitors != 1 {
		t.Errorf("second flush: %+v", d.Counts)
	}
}

func TestSaltChangesPerDay(t *testing.T) {
	a := newAgg()
	h1 := a.visitorHash("2026-10-30", "203.0.113.1", firefox)
	if h1 != a.visitorHash("2026-10-30", "203.0.113.1", firefox) {
		t.Error("same day, same visitor: different hash")
	}
	if h1 == a.visitorHash("2026-10-30", "203.0.113.2", firefox) || h1 == a.visitorHash("2026-10-30", "203.0.113.1", safari) {
		t.Error("different visitor: same hash")
	}
	if h1 == a.visitorHash("2026-10-31", "203.0.113.1", firefox) {
		t.Error("next day: same hash (salt did not change)")
	}
	// The salt is dropped with its day.
	a.now = func() time.Time { return t0.AddDate(0, 0, 3) }
	a.Flush()
	if len(a.salt) != 0 {
		t.Errorf("old salts kept: %d", len(a.salt))
	}
}

func TestDaysAndHoursFromLogTime(t *testing.T) {
	a := newAgg()
	late := req("/", "203.0.113.1", firefox)
	late.Time = time.Date(2026, 10, 29, 23, 59, 59, 0, time.UTC)
	a.Add("app_1", late)
	soon := req("/", "203.0.113.1", firefox)
	soon.Time = time.Date(2026, 10, 30, 0, 0, 1, 0, time.UTC)
	a.Add("app_1", soon)
	d := a.Flush()["app_1"]
	if len(d.Days) != 2 || len(d.Hours) != 2 {
		t.Fatalf("days %d, hours %d", len(d.Days), len(d.Hours))
	}
	// Salt rotates at midnight: the same person is a new visitor on the new day.
	if d.Days["2026-10-29"].Visitors != 1 || d.Days["2026-10-30"].Visitors != 1 {
		t.Errorf("visitors per day: %+v", d.Days)
	}
}

func TestPathCapFoldsIntoOther(t *testing.T) {
	a := newAgg()
	for round := 0; round < 2; round++ {
		for i := 0; i < MaxPathsPerDay+50; i++ {
			a.Add("app_1", req(fmt.Sprintf("/p/%d", i), "203.0.113.1", firefox))
		}
		if round == 0 {
			a.Flush() // the cap holds across flushes
		}
	}
	d := a.Flush()["app_1"].Days["2026-10-30"]
	if len(d.Paths) > MaxPathsPerDay {
		t.Errorf("%d paths in the second flush", len(d.Paths))
	}
	if d.Paths[OtherName] == 0 {
		t.Error("no (other) bucket")
	}
	var total int64
	for _, n := range d.Paths {
		total += n
	}
	if total != int64(MaxPathsPerDay+50) {
		t.Errorf("views lost: %d", total)
	}
}

func TestPathCapAcrossFlushes(t *testing.T) {
	a := newAgg()
	distinct := map[string]bool{}
	for i := 0; i < MaxPathsPerDay*2; i++ {
		a.Add("app_1", req(fmt.Sprintf("/p/%d", i), "203.0.113.1", firefox))
		if i%100 == 99 {
			for p := range a.Flush()["app_1"].Days["2026-10-30"].Paths {
				distinct[p] = true
			}
		}
	}
	if len(distinct) > MaxPathsPerDay {
		t.Errorf("%d distinct paths stored for one day", len(distinct))
	}
}

func TestReferrerCapDropsExtra(t *testing.T) {
	a := newAgg()
	for i := 0; i < MaxReferrersPerDay+20; i++ {
		e := req("/", "203.0.113.1", firefox)
		e.Referer = fmt.Sprintf("https://site%d.example/", i)
		a.Add("app_1", e)
	}
	a.Add("app_1", req("/", "203.0.113.1", firefox)) // direct, after the cap is full
	refs := a.Flush()["app_1"].Days["2026-10-30"].Referrers
	if n := len(refs); n != MaxReferrersPerDay+1 {
		t.Errorf("%d referrers", n)
	}
	if refs[DirectName] != 1 {
		t.Errorf("direct dropped by the cap: %v", refs[DirectName])
	}
}

func TestDirectSurvivesRestore(t *testing.T) {
	a := newAgg()
	a.Add("app_1", req("/", "203.0.113.1", firefox))
	a.Restore(a.Flush())
	for i := 0; i < MaxReferrersPerDay; i++ {
		e := req("/", "203.0.113.1", firefox)
		e.Referer = fmt.Sprintf("https://site%d.example/", i)
		a.Add("app_1", e)
	}
	refs := a.Flush()["app_1"].Days["2026-10-30"].Referrers
	if len(refs) != MaxReferrersPerDay+1 || refs[DirectName] != 1 {
		t.Errorf("direct took a slot of the cap: %d referrers, direct %d", len(refs), refs[DirectName])
	}
}

func TestNotFoundCountsAssetsNotBots(t *testing.T) {
	a := newAgg()
	for _, p := range []string{"/gone", "/gone", "/img/logo.png"} {
		e := req(p, "203.0.113.1", firefox)
		e.Status = 404
		a.Add("app_1", e)
	}
	bot := req("/wp-login.php", "198.51.100.9", "Googlebot/2.1")
	bot.Status = 404
	a.Add("app_1", bot)
	gone := req("/gone", "203.0.113.1", firefox)
	gone.Status = 410 // only 404 counts
	a.Add("app_1", gone)
	d := a.Flush()["app_1"].Days["2026-10-30"]
	if len(d.NotFound) != 2 || d.NotFound["/gone"] != 2 || d.NotFound["/img/logo.png"] != 1 {
		t.Errorf("not found = %v", d.NotFound)
	}
	if d.Bots != 1 || d.Errors4xx != 5 {
		t.Errorf("counts = %+v", d.Counts)
	}
	// A 404 is not a page view and not a "came from".
	if len(d.Paths) != 0 || len(d.Referrers) != 0 {
		t.Errorf("paths %v, referrers %v", d.Paths, d.Referrers)
	}
}

func TestNotFoundCap(t *testing.T) {
	a := newAgg()
	for i := 0; i < MaxNotFoundPerDay+10; i++ {
		e := req(fmt.Sprintf("/x/%d", i), "203.0.113.1", firefox)
		e.Status = 404
		a.Add("app_1", e)
	}
	d := a.Flush()["app_1"].Days["2026-10-30"]
	if len(d.NotFound) != MaxNotFoundPerDay || d.NotFound[OtherName] != 11 {
		t.Errorf("%d not found paths, other %d", len(d.NotFound), d.NotFound[OtherName])
	}
}

func TestOwnReferrerIgnored(t *testing.T) {
	a := newAgg()
	e := req("/b", "203.0.113.1", firefox)
	e.Referer = "https://a.bitploy.nl/"
	a.Add("app_1", e)
	if refs := a.Flush()["app_1"].Days["2026-10-30"].Referrers; len(refs) != 0 {
		t.Errorf("own site counted as referrer: %v", refs)
	}
}

func TestForget(t *testing.T) {
	a := newAgg()
	a.Add("app_1", req("/", "203.0.113.1", firefox))
	a.Forget("app_1")
	if len(a.Flush()) != 0 {
		t.Error("deleted app still flushed")
	}
}

// Nothing a visitor sends may end up in what is stored: not the IP, not the user agent.
func TestSnapshotHasNoIPOrUserAgent(t *testing.T) {
	a := newAgg()
	e := req("/", "203.0.113.77", "SecretBrowser/9.9 (unique-ua-string)")
	e.Referer = "https://ref.example/page?token=abc"
	e.Country = "NL"
	a.Add("app_1", e)
	dump := fmt.Sprintf("%+v", *a.Flush()["app_1"].Days["2026-10-30"])
	for _, secret := range []string{"203.0.113.77", "SecretBrowser", "unique-ua-string", "token=abc"} {
		if strings.Contains(dump, secret) {
			t.Errorf("snapshot contains %q: %s", secret, dump)
		}
	}
}

// Answer times count for people asking for pages or data: not files, not bots,
// not websockets (their duration is how long the connection stayed open).
func TestAggregateSpeed(t *testing.T) {
	a := newAgg()
	timed := func(path, ua string, status int, d time.Duration) Entry {
		e := req(path, "203.0.113.1", ua)
		e.Status, e.Duration = status, d
		return e
	}
	a.Add("app_1", timed("/", firefox, 200, 30*time.Millisecond))
	a.Add("app_1", timed("/api/items", firefox, 200, 1500*time.Millisecond))
	a.Add("app_1", timed("/missing", firefox, 404, 120*time.Millisecond)) // an answer all the same
	a.Add("app_1", timed("/app.js", firefox, 200, 9*time.Second))
	a.Add("app_1", timed("/", "Googlebot/2.1", 200, 9*time.Second))
	a.Add("app_1", timed("/ws", firefox, 101, time.Hour))
	a.Add("app_1", timed("/x", firefox, 200, 0)) // unknown

	snap := a.Flush()["app_1"]
	want := Speed{1, 0, 1, 0, 0, 1, 0, 0}
	if got := snap.Hours[t0.Truncate(time.Hour).Unix()].Speed; got != want {
		t.Errorf("hour speed = %v, want %v", got, want)
	}
	if got := snap.Days["2026-10-30"].Speed; got != want {
		t.Errorf("day speed = %v, want %v", got, want)
	}
	// Restoring a failed flush keeps them.
	a.Restore(Snapshot{"app_1": snap})
	if got := a.Flush()["app_1"].Days["2026-10-30"].Speed; got != want {
		t.Errorf("restored speed = %v, want %v", got, want)
	}
}
