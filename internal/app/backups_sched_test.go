package app

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/store/db"
)

func at(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestLatestSlot(t *testing.T) {
	// 2026-10-29 is a Thursday, 2026-11-01 a Sunday.
	cases := []struct{ schedule, now, want string }{
		{"daily", "2026-10-29 12:00", "2026-10-29 03:00"},
		{"daily", "2026-10-29 03:00", "2026-10-29 03:00"},
		{"daily", "2026-10-29 02:59", "2026-10-28 03:00"},
		{"weekly", "2026-10-29 12:00", "2026-10-25 03:00"},
		{"weekly", "2026-11-01 02:00", "2026-10-25 03:00"},
		{"weekly", "2026-11-01 03:00", "2026-11-01 03:00"},
		{"weekly", "2026-11-01 23:59", "2026-11-01 03:00"},
		{"every_6h", "2026-10-29 12:00", "2026-10-29 09:00"},
		{"every_6h", "2026-10-29 09:00", "2026-10-29 09:00"},
		{"every_6h", "2026-10-29 02:59", "2026-10-28 21:00"},
		{"every_6h", "2026-10-29 04:00", "2026-10-29 03:00"},
		{"every_12h", "2026-10-29 12:00", "2026-10-29 03:00"},
		{"every_12h", "2026-10-29 15:00", "2026-10-29 15:00"},
		{"every_12h", "2026-11-01 01:00", "2026-10-31 15:00"},
		{"monthly", "2026-10-29 12:00", "2026-10-01 03:00"},
		{"monthly", "2026-10-01 02:59", "2026-09-01 03:00"},
		{"monthly", "2026-11-01 03:00", "2026-11-01 03:00"},
		{"monthly", "2027-01-01 00:00", "2026-12-01 03:00"},
	}
	for _, c := range cases {
		if got := latestSlot(c.schedule, 180, at(c.now)); !got.Equal(at(c.want)) {
			t.Errorf("latestSlot(%s, %s) = %v, want %s", c.schedule, c.now, got, c.want)
		}
	}
}

func TestBackupDue(t *testing.T) {
	now := at("2026-10-29 12:00")
	slot := at("2026-10-29 03:00").Unix()
	cases := []struct {
		name     string
		schedule string
		last     int64
		want     bool
	}{
		{"never made", "daily", 0, true},
		{"made after today's slot", "daily", slot + 60, false},
		{"made exactly at the slot", "daily", slot, false},
		{"made yesterday", "daily", slot - 3600, true},
		{"weekly, made this week", "weekly", at("2026-10-26 03:05").Unix(), false},
		{"weekly, made last week", "weekly", at("2026-10-24 03:05").Unix(), true},
		{"off", "off", 0, false},
		{"unknown", "hourly", 0, false},
		{"6h, made after 09:00", "every_6h", at("2026-10-29 09:05").Unix(), false},
		{"6h, made before 09:00", "every_6h", at("2026-10-29 08:59").Unix(), true},
		{"12h, made after 03:00", "every_12h", at("2026-10-29 03:01").Unix(), false},
		{"monthly, made this month", "monthly", at("2026-10-01 03:05").Unix(), false},
		{"monthly, made last month", "monthly", at("2026-09-30 03:05").Unix(), true},
	}
	for _, c := range cases {
		if got := backupDue(c.schedule, 180, c.last, now); got != c.want {
			t.Errorf("%s: backupDue = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestNextBackupAt(t *testing.T) {
	now := at("2026-10-29 12:00")
	if got := nextBackupAt("daily", 180, at("2026-10-29 03:01").Unix(), now); !got.Equal(at("2026-10-30 03:00")) {
		t.Errorf("daily after today's backup: %v", got)
	}
	if got := nextBackupAt("daily", 180, 0, now); !got.Equal(at("2026-10-29 03:00")) {
		t.Errorf("daily, never made (due now): %v", got)
	}
	if got := nextBackupAt("weekly", 180, at("2026-10-25 03:01").Unix(), now); !got.Equal(at("2026-11-01 03:00")) {
		t.Errorf("weekly: %v", got)
	}
}

func TestNextBackupAtSubDailyAndMonthly(t *testing.T) {
	cases := []struct {
		schedule, now, since, want string
	}{
		{"every_6h", "2026-10-29 12:00", "2026-10-29 09:01", "2026-10-29 15:00"},
		{"every_6h", "2026-10-29 22:00", "2026-10-29 21:01", "2026-10-30 03:00"}, // over midnight
		{"every_12h", "2026-10-29 16:00", "2026-10-29 15:01", "2026-10-30 03:00"},
		{"every_12h", "2026-10-29 16:00", "2026-10-29 03:01", "2026-10-29 15:00"}, // missed one is due
		{"monthly", "2026-10-31 12:00", "2026-10-01 03:01", "2026-11-01 03:00"}, // 31st to the 1st
		{"monthly", "2026-12-15 12:00", "2026-12-01 03:01", "2027-01-01 03:00"}, // over the year
		{"monthly", "2026-10-29 12:00", "2026-09-01 03:01", "2026-10-01 03:00"}, // missed one is due
	}
	for _, c := range cases {
		if got := nextBackupAt(c.schedule, 180, at(c.since).Unix(), at(c.now)); !got.Equal(at(c.want)) {
			t.Errorf("nextBackupAt(%s, now %s, since %s) = %v, want %s", c.schedule, c.now, c.since, got, c.want)
		}
	}
}

func TestBackupMinuteSpread(t *testing.T) {
	seen := map[int]bool{}
	for i := 0; i < 200; i++ {
		id := fmt.Sprintf("app_%04d", i)
		m := backupMinute(id)
		if m < 120 || m >= 360 {
			t.Fatalf("backupMinute(%s) = %d, want 02:00-05:59", id, m)
		}
		if backupMinute(id) != m {
			t.Fatalf("backupMinute(%s) is not stable", id)
		}
		seen[m] = true
	}
	if len(seen) < 100 {
		t.Errorf("200 apps share only %d backup times", len(seen))
	}
	if got := backupClock(125); got != "02:05" {
		t.Errorf("backupClock(125) = %q", got)
	}
	// Another minute moves the slot.
	if got := latestSlot("daily", 4*60+30, at("2026-10-29 04:00")); !got.Equal(at("2026-10-28 04:30")) {
		t.Errorf("latestSlot at 04:30 = %v", got)
	}
}

func TestDefaultScheduleWaitsForItsTime(t *testing.T) {
	// The default turned on at 12:00: no backup until 03:00 the next day,
	// even though none was ever made.
	on := at("2026-10-29 12:00").Unix()
	app := db.App{BackupDefaultAt: on}
	if backupDue("daily", 180, backupSince(app, 0), at("2026-10-29 12:10")) {
		t.Error("due right after the default was turned on")
	}
	if !backupDue("daily", 180, backupSince(app, 0), at("2026-10-30 03:05")) {
		t.Error("not due at the first backup time")
	}
	if got := nextBackupAt("daily", 180, backupSince(app, 0), at("2026-10-29 12:10")); !got.Equal(at("2026-10-30 03:00")) {
		t.Errorf("next = %v", got)
	}
	// A backup made after that counts as usual.
	if backupDue("daily", 180, backupSince(app, at("2026-10-30 03:05").Unix()), at("2026-10-30 12:00")) {
		t.Error("due again after today's backup")
	}
	// Without a default (the user turned it on) the first one is due at once.
	if !backupDue("daily", 180, backupSince(db.App{}, 0), at("2026-10-29 12:10")) {
		t.Error("a schedule the user turned on is not due at once")
	}
}

func TestWantsDefaultBackups(t *testing.T) {
	cases := []struct {
		name string
		app  db.App
		want bool
	}{
		{"volume, never set", db.App{VolumeEnabled: 1}, true},
		{"no volume", db.App{VolumeEnabled: 0}, false},
		{"set by the user", db.App{VolumeEnabled: 1, BackupScheduleSet: 1}, false},
		{"postgres", db.App{VolumeEnabled: 1, Template: "postgres"}, true},
		{"valkey", db.App{VolumeEnabled: 1, Template: "valkey"}, false},
		{"meilisearch", db.App{VolumeEnabled: 1, Template: "meilisearch"}, false},
	}
	for _, c := range cases {
		if got := wantsDefaultBackups(c.app); got != c.want {
			t.Errorf("%s: wantsDefaultBackups = %v, want %v", c.name, got, c.want)
		}
	}
}

func ret(spec ...any) []retainable {
	var out []retainable
	for i := 0; i < len(spec); i += 2 {
		out = append(out, retainable{ID: spec[i].(string), CreatedAt: at(spec[i+1].(string)).Unix()})
	}
	return out
}

func TestBackupsToPrune(t *testing.T) {
	now := at("2026-10-29 12:00")
	items := ret("c", "2026-10-27 03:00", "a", "2026-10-25 03:00", "e", "2026-10-29 03:00", "b", "2026-10-26 03:00", "d", "2026-10-28 03:00")
	sorted := func(s []string) []string { sort.Strings(s); return s }

	cases := []struct {
		name  string
		items []retainable
		mode  string
		keep  int64
		want  []string
	}{
		{"keep last 3", items, "last", 3, []string{"a", "b"}},
		{"keep last 5 keeps everything", items, "last", 5, nil},
		{"keep last 1", items, "last", 1, []string{"a", "b", "c", "d"}},
		{"keep 2 days", items, "days", 2, []string{"a", "b", "c"}},
		{"keep 1 day still keeps the newest", ret("x", "2026-10-01 03:00", "y", "2026-09-01 03:00"), "days", 1, []string{"y"}},
		{"a single backup is never removed", ret("x", "2026-01-01 03:00"), "days", 1, nil},
		{"nothing", nil, "last", 7, nil},
		{"bad keep", items, "last", 0, nil},
	}
	for _, c := range cases {
		got := sorted(backupsToPrune(c.items, c.mode, c.keep, now))
		if len(got) == 0 && len(c.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: pruned %v, want %v", c.name, got, c.want)
		}
	}
	// Same time: the newer ID wins, so the result does not depend on input order.
	same := ret("a", "2026-10-29 03:00", "b", "2026-10-29 03:00")
	if got := backupsToPrune(same, "last", 1, now); !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("tie: pruned %v", got)
	}
}
