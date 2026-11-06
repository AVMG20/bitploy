package app

import (
	"context"
	"testing"

	"github.com/AVMG20/bitploy/internal/deploy"
)

// Two deployments that each fit alone are both checked before their build;
// the swap checks again, so the second cannot go live over the quota.
func TestSwapChecksTheQuotaAgain(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	e.exec(t, `UPDATE users SET quota_apps = 20, quota_memory_mb = 768, quota_cpu_millis = 2000 WHERE id = ?`, e.user.UserID)
	a := e.mustApp(t, CreateAppInput{Type: "worker", MemoryMB: ptr(int64(512))})
	b := e.mustApp(t, CreateAppInput{Type: "worker", MemoryMB: ptr(int64(512))})
	pa := e.pipelineFor(t, a, deploy.BuildKindDockerfile)
	pb := e.pipelineFor(t, b, deploy.BuildKindDockerfile)
	for _, p := range []*pipeline{pa, pb} {
		if ae := p.settleResources(ctx); ae != nil {
			t.Fatalf("settle: %v", ae)
		}
	}
	if ae := pa.swap(ctx); ae != nil {
		t.Fatalf("first swap: %v", ae)
	}
	if ae := pb.swap(ctx); ae == nil || ae.Code != "memory_quota_exceeded" {
		t.Fatalf("second swap: %v", ae)
	}
	if row, _ := e.s.Q.GetApp(ctx, b.ID); row.CurrentDeploymentID.Valid {
		t.Error("the refused deployment went live")
	}

	// A compose deployment is checked at the swap too.
	c := e.mustApp(t, CreateAppInput{Type: "worker", MemoryMB: ptr(int64(128))})
	pc := e.pipelineFor(t, c, deploy.BuildKindDockerfile)
	pc.compose = true
	pc.specs = []serviceSpec{{Name: "web", MemoryMB: 256, CPUMillis: 250}, {Name: "db", MemoryMB: 128, CPUMillis: 250}}
	fresh, _ := e.s.Q.GetApp(ctx, c.ID)
	if ae := pc.quotaAtSwap(ctx, fresh); ae == nil || ae.Code != "memory_quota_exceeded" {
		t.Fatalf("compose over the quota: %v", ae)
	}
	pc.specs = pc.specs[:1]
	if ae := pc.quotaAtSwap(ctx, fresh); ae != nil {
		t.Fatalf("compose within the quota: %v", ae)
	}
}
