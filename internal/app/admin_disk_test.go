package app

import (
	"context"
	"testing"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/diskuse"
)

// The thresholds on the disk page are saved, validated, and the builds
// threshold decides whether builds are refused.
func TestDiskSettingsAndBuildsThreshold(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	admin := e.addUser(t, "usr_admin", "admin@example.com", "admin")
	admin.Scope = ScopeSession

	auto, err := e.s.diskAuto(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !auto.Enabled || auto.MinFreePercent != 10 || auto.BuildsMinFreePercent != 15 || !auto.OtherImages || auto.BuildCacheDays != 7 {
		t.Fatalf("defaults: %+v", auto)
	}

	// 10% free: under the default 15% builds are refused.
	e.s.disk.space = func() (diskuse.Space, error) { return diskuse.Space{Total: 100, Free: 10}, nil }
	if ok, _ := e.s.DiskHasRoom(); ok {
		t.Fatal("10% free passed the 15% rule")
	}

	five, two, no, three := 5, 2, false, 3
	auto, err = e.s.AdminUpdateDiskCleanup(ctx, admin, UpdateDiskCleanupInput{
		BuildsMinFreePercent: &five, MinFreePercent: &two, OtherImages: &no, BuildCacheDays: &three,
	})
	if err != nil {
		t.Fatal(err)
	}
	if auto.BuildsMinFreePercent != 5 || auto.MinFreePercent != 2 || auto.OtherImages || auto.BuildCacheDays != 3 {
		t.Fatalf("after update: %+v", auto)
	}
	if ok, _ := e.s.DiskHasRoom(); !ok {
		t.Fatal("10% free refused under a 5% rule")
	}
	if st, err := e.s.AdminServerStats(ctx, admin); err != nil || st.DiskMinFreePercent != 5 {
		t.Fatalf("server stats: %v %v", st.DiskMinFreePercent, err)
	}

	for name, in := range map[string]UpdateDiskCleanupInput{
		"builds 0":       {BuildsMinFreePercent: new(0)},
		"builds 51":      {BuildsMinFreePercent: new(51)},
		"cleanup 1":      {MinFreePercent: new(1)},
		"cache days 91":  {BuildCacheDays: new(91)},
		"cache days -1":  {BuildCacheDays: new(-1)},
		"nothing at all": {},
	} {
		if _, err := e.s.AdminUpdateDiskCleanup(ctx, admin, in); apperr.As(err) == nil || apperr.As(err).HTTPStatus != 400 {
			t.Errorf("%s: want 400, got %v", name, err)
		}
	}
	if _, err := e.s.AdminUpdateDiskCleanup(ctx, e.user, UpdateDiskCleanupInput{BuildsMinFreePercent: &five}); err == nil {
		t.Error("a user changed the disk settings")
	}
}

func TestDiskAutoStepsFollowOtherImages(t *testing.T) {
	for _, on := range []bool{true, false} {
		has := false
		for _, k := range (diskSettings{otherImages: on}).autoSteps() {
			has = has || k == diskuse.OtherImages
		}
		if has != on {
			t.Errorf("other_images %v: steps include other images = %v", on, has)
		}
	}
}

// Admins hear once that the disk stays low, not every 30 minutes.
func TestDiskRunNews(t *testing.T) {
	cases := []struct {
		result                  string
		freed                   int64
		alerted                 bool
		record, announce, after bool
	}{
		{"still_low", 0, false, true, true, true},  // first time: tell
		{"still_low", 0, true, false, false, true}, // again, nothing freed: silence
		{"still_low", 5, true, true, false, true},  // freed some: keep the record, no message
		{"failed", 0, true, false, false, true},
		{"recovered", 9, true, true, true, false}, // back above: tell, and the next low is news
		{"recovered", 9, false, true, true, false},
	}
	for _, c := range cases {
		record, announce, after := diskRunNews(c.result, c.freed, c.alerted)
		if record != c.record || announce != c.announce || after != c.after {
			t.Errorf("%s freed=%d alerted=%v: got record=%v announce=%v after=%v", c.result, c.freed, c.alerted, record, announce, after)
		}
	}
}
