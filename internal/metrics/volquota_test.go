package metrics

import (
	"context"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/dockerx"
)

func TestEnforceVolumeQuota(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, id := range []string{"app_over", "app_warn", "app_ok"} {
		e.addApp(t, id, "bp-"+id+"-1", "running")
		e.exec(t, `UPDATE apps SET volume_enabled = 1, desired_state = 'running' WHERE id = ?`, id)
	}
	e.exec(t, `UPDATE users SET quota_volume_mb = 1024 WHERE id = 'usr_1'`)
	e.dk.diskUsage = dockerx.DiskUsage{Volumes: []dockerx.DiskVolume{
		{AppID: "app_over", Name: "v1", Size: 1500 << 20},
		{AppID: "app_warn", Name: "v2", Size: 900 << 20},
		{AppID: "app_ok", Name: "v3", Size: 100 << 20},
	}}
	e.dk.managed = []dockerx.Managed{{Name: "bp-app_over-1", AppID: "app_over"}, {Name: "bp-app_ok-1", AppID: "app_ok"}}

	if err := e.m.EnforceVolumeQuota(ctx); err != nil {
		t.Fatal(err)
	}
	st, msg, desired := e.app(t, "app_over")
	if st != "failed" || desired != "stopped" || !strings.Contains(msg, "over its 1 GB limit") {
		t.Errorf("over: %s %s %q", st, desired, msg)
	}
	if len(e.dk.stopped) != 1 || e.dk.stopped[0] != "bp-app_over-1" {
		t.Errorf("stopped = %v", e.dk.stopped)
	}
	if got := e.actions(); len(got) != 1 || got[0] != "app.volume_quota_exceeded" {
		t.Errorf("audits = %v", got)
	}
	st, msg, desired = e.app(t, "app_warn")
	if st != "running" || desired != "running" || !strings.HasPrefix(msg, msgVolumeWarn) {
		t.Errorf("warn: %s %s %q", st, desired, msg)
	}
	if st, msg, _ = e.app(t, "app_ok"); st != "running" || msg != "" {
		t.Errorf("ok: %s %q", st, msg)
	}

	// Space freed: the warning goes away; a second run does not stop the stopped app again.
	e.dk.diskUsage.Volumes[1].Size = 10 << 20
	if err := e.m.EnforceVolumeQuota(ctx); err != nil {
		t.Fatal(err)
	}
	if _, msg, _ = e.app(t, "app_warn"); msg != "" {
		t.Errorf("warning not cleared: %q", msg)
	}
	if len(e.dk.stopped) != 1 || len(e.audits) != 1 {
		t.Errorf("stopped again: %v %v", e.dk.stopped, e.audits)
	}
}
