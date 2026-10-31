package app

import (
	"reflect"
	"sort"
	"testing"
	"time"
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
	}
	for _, c := range cases {
		if got := latestSlot(c.schedule, at(c.now)); !got.Equal(at(c.want)) {
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
	}
	for _, c := range cases {
		if got := backupDue(c.schedule, c.last, now); got != c.want {
			t.Errorf("%s: backupDue = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestNextBackupAt(t *testing.T) {
	now := at("2026-10-29 12:00")
	if got := nextBackupAt("daily", at("2026-10-29 03:01").Unix(), now); !got.Equal(at("2026-10-30 03:00")) {
		t.Errorf("daily after today's backup: %v", got)
	}
	if got := nextBackupAt("daily", 0, now); !got.Equal(at("2026-10-29 03:00")) {
		t.Errorf("daily, never made (due now): %v", got)
	}
	if got := nextBackupAt("weekly", at("2026-10-25 03:01").Unix(), now); !got.Equal(at("2026-11-01 03:00")) {
		t.Errorf("weekly: %v", got)
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
