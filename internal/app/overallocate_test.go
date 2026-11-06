package app

import (
	"context"
	"testing"
)

// A user who may overallocate is never refused for memory or CPU: not for one
// app bigger than the quota, not when the running apps already fill it.
func TestOverallocateSkipsMemoryAndCPU(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	e.exec(t, `UPDATE users SET quota_apps = 3, quota_memory_mb = 1024, quota_cpu_millis = 1200, overallocate = 1 WHERE id = ?`, e.user.UserID)
	first := e.mustApp(t, CreateAppInput{})
	second := e.mustApp(t, CreateAppInput{})
	third := e.mustApp(t, CreateAppInput{})

	huge, bigCPU := int64(8000), int64(8000)
	if _, err := e.s.UpdateApp(ctx, e.user, third.Slug, UpdateAppInput{MemoryMB: &huge, CPUMillis: &bigCPU}); err != nil {
		t.Fatalf("bigger than the quota: %v", err)
	}
	e.exec(t, `UPDATE apps SET desired_state = 'running', current_deployment_id = 'dep_x' WHERE id IN (?, ?)`, first.ID, second.ID)
	if _, err := e.s.DeployFiles(ctx, e.user, third.Slug, staticFiles()); err != nil {
		t.Fatalf("deploy over the quota: %v", err)
	}
	if err := e.s.checkResourceQuota(ctx, userRow(t, e), "", 1<<20, 1<<20); err != nil {
		t.Fatalf("checkResourceQuota: %v", err)
	}
	// The app count is still a limit.
	small := int64(64)
	if _, err := e.s.CreateApp(ctx, e.user, CreateAppInput{MemoryMB: &small}); depCode(err) != "apps_quota_exceeded" {
		t.Errorf("count: %v", err)
	}

	// Off again: the quota bites.
	e.exec(t, `UPDATE users SET overallocate = 0 WHERE id = ?`, e.user.UserID)
	if _, err := e.s.UpdateApp(ctx, e.user, second.Slug, UpdateAppInput{MemoryMB: &huge}); depCode(err) != "memory_quota_exceeded" {
		t.Errorf("without overallocate: %v", err)
	}
}

// The server room guard leaves out the apps of a user who may overallocate,
// so their limits do not make every other user's app fail with server_full.
// The admin's server overview still shows everything.
func TestServerRoomLeavesOutOverallocators(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	a := e.mustApp(t, CreateAppInput{})
	e.exec(t, `UPDATE apps SET desired_state = 'running', current_deployment_id = 'dep_x', memory_mb = 64000 WHERE id = ?`, a.ID)
	checked, _ := e.s.Q.OpsSumCheckedMemoryLimitsMB(ctx)
	all, _ := e.s.Q.OpsSumMemoryLimitsMB(ctx)
	if checked != 64000 || all != 64000 {
		t.Fatalf("without overallocate: checked %d, all %d", checked, all)
	}
	e.exec(t, `UPDATE users SET overallocate = 1 WHERE id = ?`, e.user.UserID)
	checked, _ = e.s.Q.OpsSumCheckedMemoryLimitsMB(ctx)
	all, _ = e.s.Q.OpsSumMemoryLimitsMB(ctx)
	if checked != 0 || all != 64000 {
		t.Fatalf("with overallocate: checked %d, all %d", checked, all)
	}
}
