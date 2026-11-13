package mcp

import (
	"fmt"
	"strings"

	"github.com/AVMG20/bitploy/internal/app"
)

// quotaReport is whoami's view of every account limit: used, limit and what
// is left, so an agent can plan a new app before it is refused. It returns
// the structured form (whoami data "quotas") and one plain sentence. Deploys
// are not limited; deploys_last_hour is a count only.
//
// The rules follow the service's checks: apps, memory, CPU and domains per
// app are hard numbers (0 means none); storage of 0 or less means no limit;
// with overallocate on, memory and CPU are not checked.
func quotaReport(me app.Me) (map[string]any, string) {
	q, u := me.Quota, me.Usage
	left := func(limit, used int64) int64 { return max(limit-used, 0) }
	entry := func(used, limit int64) map[string]any {
		return map[string]any{"used": used, "limit": limit, "remaining": left(limit, used)}
	}
	out := map[string]any{
		"apps":            entry(u.Apps, q.Apps),
		"domains_per_app": map[string]any{"limit": q.DomainsPerApp},
	}
	var parts []string
	parts = append(parts, fmt.Sprintf("apps %d of %d (%d left)", u.Apps, q.Apps, left(q.Apps, u.Apps)))

	if q.Overallocate {
		out["memory_mb"] = map[string]any{"used": u.MemoryReservedMB, "limit": nil, "remaining": nil, "checked": false}
		out["cpu_millis"] = map[string]any{"used": u.CPUReservedMillis, "limit": nil, "remaining": nil, "checked": false}
		parts = append(parts, fmt.Sprintf("%d MB memory and %d CPU millis reserved; memory and CPU are not checked for this user (overallocate is on), so apps may ask for more than the %d MB / %d CPU millis quota",
			u.MemoryReservedMB, u.CPUReservedMillis, q.MemoryMB, q.CPUMillis))
	} else {
		out["memory_mb"] = entry(u.MemoryReservedMB, q.MemoryMB)
		out["cpu_millis"] = entry(u.CPUReservedMillis, q.CPUMillis)
		parts = append(parts,
			fmt.Sprintf("memory %d of %d MB reserved (%d MB left; static sites do not count)", u.MemoryReservedMB, q.MemoryMB, left(q.MemoryMB, u.MemoryReservedMB)),
			fmt.Sprintf("CPU %d of %d millis (%d left)", u.CPUReservedMillis, q.CPUMillis, left(q.CPUMillis, u.CPUReservedMillis)))
	}

	// Storage as the server checks a new volume: app files, backups and the
	// full size of every /data volume.
	taken := int64(u.FilesMB + u.BackupsMB + float64(u.VolumeReservedMB) + 0.5)
	if q.VolumeMB <= 0 {
		out["storage_mb"] = map[string]any{"used": taken, "limit": nil, "remaining": nil}
		parts = append(parts, fmt.Sprintf("storage %d MB taken (no limit)", taken))
	} else {
		out["storage_mb"] = entry(taken, q.VolumeMB)
		parts = append(parts, fmt.Sprintf("storage %d of %d MB taken (%d MB left)", taken, q.VolumeMB, left(q.VolumeMB, taken)))
	}

	out["deploys_last_hour"] = u.DeploysLastHour
	parts = append(parts, fmt.Sprintf("custom domains: %d per app", q.DomainsPerApp))
	return out, "Quota: " + strings.Join(parts, "; ") + "."
}

// quotaSteps are whoami's next steps for a quota that is used up.
func quotaSteps(me app.Me) []string {
	q, u := me.Quota, me.Usage
	var out []string
	if q.Apps > 0 && u.Apps >= q.Apps {
		out = append(out, "The app limit is reached: a new app is refused (apps_quota_exceeded). Updating an existing app still works. For a new one, ask the user which app they no longer need, or to ask an admin for a higher limit; never delete an app on your own.")
	}
	return out
}
