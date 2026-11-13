package mcp

import (
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/app"
)

// whoami reports every quota as used, limit and what is left, so an agent
// can plan a deploy before it is refused.
func TestQuotaReport(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	reset := now.Add(7 * time.Minute)
	me := app.Me{
		Quota: app.Quota{Apps: 3, MemoryMB: 1024, CPUMillis: 2000, DeploysPerHour: 10, DomainsPerApp: 5, VolumeMB: 5120},
		Usage: app.Usage{Apps: 3, MemoryReservedMB: 768, CPUReservedMillis: 2500, FilesMB: 100, BackupsMB: 50, VolumeReservedMB: 1024,
			DeploysLastHour: 9, DeploysResetAt: &reset},
	}
	q, line := quotaReport(me, now)
	for _, c := range []struct {
		key             string
		used, limit, rm int64
	}{
		{"apps", 3, 3, 0},
		{"memory_mb", 768, 1024, 256},
		{"cpu_millis", 2500, 2000, 0}, // over: never negative
		{"storage_mb", 1174, 5120, 3946},
		{"deploys_per_hour", 9, 10, 1},
	} {
		m := q[c.key].(map[string]any)
		if m["used"] != c.used || m["limit"] != c.limit || m["remaining"] != c.rm {
			t.Errorf("%s = %v", c.key, m)
		}
	}
	if d := q["deploys_per_hour"].(map[string]any); d["resets_in_seconds"] != int64(420) || d["resets_at"] != "2026-11-13T12:07:00Z" {
		t.Errorf("deploys reset = %v", d)
	}
	if q["domains_per_app"].(map[string]any)["limit"] != int64(5) {
		t.Errorf("domains = %v", q["domains_per_app"])
	}
	for _, want := range []string{"apps 3 of 3 (0 left)", "memory 768 of 1024 MB reserved (256 MB left", "CPU 2500 of 2000 millis (0 left)",
		"storage 1174 of 5120 MB taken (3946 MB left)", "deploys 9 of 10 in the last hour (1 left; one more is allowed in about 7 minutes)", "custom domains: 5 per app"} {
		if !strings.Contains(line, want) {
			t.Errorf("summary lacks %q: %s", want, line)
		}
	}
	steps := strings.Join(quotaSteps(me, now), " ")
	if !strings.Contains(steps, "apps_quota_exceeded") || !strings.Contains(steps, "never delete an app on your own") || strings.Contains(steps, "deploy_rate_limited") {
		t.Errorf("steps at the app limit: %s", steps)
	}

	// Overallocate: memory and CPU have no limit; storage 0 is no limit;
	// plenty of deploys left: no reset time in the sentence.
	me.Quota.Overallocate, me.Quota.VolumeMB = true, 0
	me.Usage.DeploysLastHour = 2
	q, line = quotaReport(me, now)
	if m := q["memory_mb"].(map[string]any); m["limit"] != nil || m["checked"] != false || m["used"] != int64(768) {
		t.Errorf("overallocate memory = %v", m)
	}
	if m := q["storage_mb"].(map[string]any); m["limit"] != nil || m["remaining"] != nil {
		t.Errorf("storage without limit = %v", m)
	}
	if !strings.Contains(line, "not checked for this user (overallocate is on)") || !strings.Contains(line, "storage 1174 MB taken (no limit)") ||
		strings.Contains(line, "one more is allowed") {
		t.Errorf("summary: %s", line)
	}

	// Out of deploys: whoami says when the next one is allowed.
	me.Usage.Apps, me.Usage.DeploysLastHour = 1, 10
	if steps := strings.Join(quotaSteps(me, now), " "); !strings.Contains(steps, "No deploys left this hour") || !strings.Contains(steps, "about 7 minutes") || strings.Contains(steps, "app limit") {
		t.Errorf("steps out of deploys: %s", steps)
	}
}
