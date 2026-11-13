package mcp

import (
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/app"
)

// whoami reports every quota as used, limit and what is left, so an agent
// can plan a new app before it is refused. Deploys are counted, not limited.
func TestQuotaReport(t *testing.T) {
	me := app.Me{
		Quota: app.Quota{Apps: 3, MemoryMB: 1024, CPUMillis: 2000, DomainsPerApp: 5, VolumeMB: 5120},
		Usage: app.Usage{Apps: 3, MemoryReservedMB: 768, CPUReservedMillis: 2500, FilesMB: 100, BackupsMB: 50, VolumeReservedMB: 1024,
			DeploysLastHour: 9},
	}
	q, line := quotaReport(me)
	for _, c := range []struct {
		key             string
		used, limit, rm int64
	}{
		{"apps", 3, 3, 0},
		{"memory_mb", 768, 1024, 256},
		{"cpu_millis", 2500, 2000, 0}, // over: never negative
		{"storage_mb", 1174, 5120, 3946},
	} {
		m := q[c.key].(map[string]any)
		if m["used"] != c.used || m["limit"] != c.limit || m["remaining"] != c.rm {
			t.Errorf("%s = %v", c.key, m)
		}
	}
	if q["deploys_last_hour"] != int64(9) {
		t.Errorf("deploys_last_hour = %v", q["deploys_last_hour"])
	}
	if _, ok := q["deploys_per_hour"]; ok {
		t.Error("deploys are not limited, but whoami reports a limit")
	}
	if q["domains_per_app"].(map[string]any)["limit"] != int64(5) {
		t.Errorf("domains = %v", q["domains_per_app"])
	}
	for _, want := range []string{"apps 3 of 3 (0 left)", "memory 768 of 1024 MB reserved (256 MB left", "CPU 2500 of 2000 millis (0 left)",
		"storage 1174 of 5120 MB taken (3946 MB left)", "custom domains: 5 per app"} {
		if !strings.Contains(line, want) {
			t.Errorf("summary lacks %q: %s", want, line)
		}
	}
	if strings.Contains(line, "deploys") {
		t.Errorf("summary mentions a deploy limit: %s", line)
	}
	steps := strings.Join(quotaSteps(me), " ")
	if !strings.Contains(steps, "apps_quota_exceeded") || !strings.Contains(steps, "never delete an app on your own") {
		t.Errorf("steps at the app limit: %s", steps)
	}

	// Overallocate: memory and CPU have no limit; storage 0 is no limit.
	me.Quota.Overallocate, me.Quota.VolumeMB = true, 0
	q, line = quotaReport(me)
	if m := q["memory_mb"].(map[string]any); m["limit"] != nil || m["checked"] != false || m["used"] != int64(768) {
		t.Errorf("overallocate memory = %v", m)
	}
	if m := q["storage_mb"].(map[string]any); m["limit"] != nil || m["remaining"] != nil {
		t.Errorf("storage without limit = %v", m)
	}
	if !strings.Contains(line, "not checked for this user (overallocate is on)") || !strings.Contains(line, "storage 1174 MB taken (no limit)") {
		t.Errorf("summary: %s", line)
	}

	// Below the app limit: no steps.
	me.Usage.Apps = 1
	if steps := quotaSteps(me); len(steps) != 0 {
		t.Errorf("steps below every limit: %v", steps)
	}
}
