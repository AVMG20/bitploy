package app

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/deploy"
)

// fakeHooks stands in for Docker: it records the commands and what the
// deployment looked like when each ran.
type fakeHooks struct {
	mu        sync.Mutex
	calls     []string
	plans     []startPlan
	states    []string // deployments.state when After ran
	output    string
	beforeErr error
	afterErr  error
	block     bool // Before waits until its context ends
	s         *Service
	depID     string
}

func (f *fakeHooks) Before(ctx context.Context, plan startPlan, cmd string, out io.Writer) error {
	f.mu.Lock()
	f.calls = append(f.calls, "before:"+cmd)
	f.plans = append(f.plans, plan)
	f.mu.Unlock()
	_, _ = io.WriteString(out, f.output)
	if f.block {
		<-ctx.Done()
		return ctx.Err()
	}
	return f.beforeErr
}

func (f *fakeHooks) After(ctx context.Context, name, cmd string, out io.Writer) error {
	f.mu.Lock()
	f.calls = append(f.calls, "after:"+name+":"+cmd)
	if f.s != nil {
		d, _ := f.s.Q.GetDeployment(ctx, f.depID)
		f.states = append(f.states, d.State+"/"+d.AfterDeploy)
	}
	f.mu.Unlock()
	_, _ = io.WriteString(out, f.output)
	return f.afterErr
}

func (e *depEnv) setCommands(t *testing.T, a App, before, after string) {
	t.Helper()
	if _, err := e.s.UpdateApp(context.Background(), e.user, a.Slug, UpdateAppInput{BeforeDeployCommand: &before, AfterDeployCommand: &after}); err != nil {
		t.Fatalf("set deploy commands: %v", err)
	}
}

func ringHas(p *pipeline, want string) bool {
	for _, l := range p.ring.Lines() {
		if strings.Contains(l, want) {
			return true
		}
	}
	return false
}

func TestDeployCommandSettings(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	a := e.mustApp(t, CreateAppInput{Name: "shop", BeforeDeployCommand: "  php artisan migrate --force \n", AfterDeployCommand: "php artisan config:cache"})
	if a.BeforeDeployCommand != "php artisan migrate --force" || a.AfterDeployCommand != "php artisan config:cache" {
		t.Fatalf("created with %q / %q", a.BeforeDeployCommand, a.AfterDeployCommand)
	}
	// Only the field passed changes; "" clears it.
	got, err := e.s.UpdateApp(ctx, e.user, a.Slug, UpdateAppInput{AfterDeployCommand: ptr("")})
	if err != nil || got.BeforeDeployCommand != "php artisan migrate --force" || got.AfterDeployCommand != "" {
		t.Fatalf("clear after: %+v %v", got, err)
	}
	if got, _ := e.s.GetApp(ctx, e.user, a.Slug); got.BeforeDeployCommand != "php artisan migrate --force" {
		t.Errorf("get: %q", got.BeforeDeployCommand)
	}
	if _, err := e.s.UpdateApp(ctx, e.user, a.Slug, UpdateAppInput{BeforeDeployCommand: ptr(strings.Repeat("x", maxDeployCommand+1))}); depCode(err) != "deploy_command_invalid" {
		t.Errorf("too long: %v", err)
	}

	// Not offered for compose apps, static sites and ready-made services; a
	// refused update changes nothing else.
	comp := e.mustApp(t, CreateAppInput{BuildMode: "compose"})
	if _, err := e.s.UpdateApp(ctx, e.user, comp.Slug, UpdateAppInput{Name: ptr("renamed"), BeforeDeployCommand: ptr("migrate")}); depCode(err) != "deploy_commands_unsupported" {
		t.Errorf("compose: %v", err)
	}
	if got, _ := e.s.GetApp(ctx, e.user, comp.Slug); got.Name == "renamed" {
		t.Error("a refused update renamed the app")
	}
	if _, err := e.s.CreateApp(ctx, e.user, CreateAppInput{BuildMode: "static", AfterDeployCommand: "x"}); depCode(err) != "deploy_commands_unsupported" {
		t.Errorf("static create: %v", err)
	}
	if _, err := e.s.UpdateApp(ctx, e.user, a.Slug, UpdateAppInput{BuildMode: ptr("compose")}); err != nil {
		t.Errorf("switching to compose keeps working: %v", err)
	}
	pg := e.mustApp(t, CreateAppInput{})
	e.exec(t, `UPDATE apps SET template = 'postgres' WHERE id = ?`, pg.ID)
	if _, err := e.s.UpdateApp(ctx, e.user, pg.Slug, UpdateAppInput{BeforeDeployCommand: ptr("psql")}); depCode(err) != "deploy_commands_unsupported" {
		t.Errorf("template: %v", err)
	}
	// Clearing is always allowed.
	if _, err := e.s.UpdateApp(ctx, e.user, pg.Slug, UpdateAppInput{BeforeDeployCommand: ptr("")}); err != nil {
		t.Errorf("clear on a template app: %v", err)
	}
}

// The before-deploy command runs after the build, before any container of
// the new version starts; a failure stops the deployment there and the
// failure carries a clear code, hint and the command's output.
func TestBeforeDeployRunsFirstAndAFailureStopsTheDeploy(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	a := e.mustApp(t, CreateAppInput{Volume: true})
	e.setCommands(t, a, "php artisan migrate --force", "")
	fake := &fakeHooks{output: "Migrating: 2024_01_01_create_users\nSQLSTATE[HY000]: no such table", beforeErr: &hookExit{Code: 1}}
	e.s.hookRunner = fake

	p := e.pipelineFor(t, a, deploy.BuildKindDockerfile)
	p.image, p.port = "bitploy/"+a.Slug+":1", 8080
	ae := p.start(ctx)
	if ae == nil || ae.Code != "before_deploy_failed" {
		t.Fatalf("start: %v", ae)
	}
	if !strings.Contains(ae.Message, "exit code 1") || !strings.Contains(ae.Message, "keeps running") || ae.Next != "get_logs" {
		t.Errorf("error: %+v", ae)
	}
	if p.newName != "" || p.primary.created {
		t.Error("a container of the new version was started")
	}
	if len(fake.plans) != 1 {
		t.Fatalf("calls: %v", fake.calls)
	}
	plan := fake.plans[0]
	if plan.Image != p.image || plan.App.ID != a.ID || plan.Seq != p.dep.Seq || plan.Port != 8080 || plan.App.VolumeEnabled != 1 || plan.Service != nil {
		t.Errorf("plan: %+v", plan)
	}
	for _, want := range []string{"Running before-deploy command: php artisan migrate --force", "[before-deploy] Migrating: 2024_01_01_create_users", "[before-deploy] SQLSTATE[HY000]: no such table"} {
		if !ringHas(p, want) {
			t.Errorf("log lacks %q: %q", want, p.ring.Lines())
		}
	}

	p.finish(ctx, ae)
	d, _ := e.s.Q.GetDeployment(ctx, p.dep.ID)
	if d.State != "failed" || d.ErrorCode.String != "before_deploy_failed" || !strings.Contains(d.LogTail.String, "no such table") {
		t.Errorf("deployment: %s %s %q", d.State, d.ErrorCode.String, d.LogTail.String)
	}

	// Success: the command ran, then the pipeline went on to start the
	// container (here: no Docker).
	fake.beforeErr = nil
	fake.calls = nil
	p = e.pipelineFor(t, a, deploy.BuildKindDockerfile)
	p.image = "bitploy/" + a.Slug + ":2"
	if ae := p.start(ctx); ae == nil || ae.Code != "docker_unavailable" {
		t.Fatalf("start after a good command: %v", ae)
	}
	if len(fake.calls) != 1 || !ringHas(p, "Before-deploy command finished") {
		t.Errorf("calls %v, log %q", fake.calls, p.ring.Lines())
	}
}

func TestBeforeDeployTimeoutAndExitCodes(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	a := e.mustApp(t, CreateAppInput{})
	e.setCommands(t, a, "npx prisma migrate deploy", "")
	fake := &fakeHooks{block: true}
	e.s.hookRunner = fake
	old := beforeDeployTimeout
	beforeDeployTimeout = 50 * time.Millisecond
	t.Cleanup(func() { beforeDeployTimeout = old })

	p := e.pipelineFor(t, a, deploy.BuildKindDockerfile)
	if ae := p.start(ctx); ae == nil || ae.Code != "before_deploy_timeout" {
		t.Fatalf("timeout: %v", ae)
	}

	// A cancelled deployment is a cancel, not a failure of the command.
	cctx, cancel := context.WithCancel(ctx)
	p = e.pipelineFor(t, a, deploy.BuildKindDockerfile)
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	if ae := p.start(cctx); ae != errCancelled {
		t.Fatalf("cancel: %v", ae)
	}

	p = e.pipelineFor(t, a, deploy.BuildKindDockerfile)
	if ae := p.beforeDeployFailed(&hookExit{Code: 127}, false); ae.Code != "before_deploy_failed" || !strings.Contains(ae.Hint, "could not find the program") {
		t.Errorf("127: %+v", ae)
	}
	if ae := p.beforeDeployFailed(&hookExit{Code: 137, OOM: true}, false); !strings.Contains(ae.Message, "out of memory") || ae.Next != "update_app" {
		t.Errorf("oom: %+v", ae)
	}
	if ae := p.beforeDeployFailed(startFailed(`Docker could not start the container: exec: "sh": executable file not found in $PATH`, "x"), false); !strings.Contains(ae.Hint, "needs a shell") {
		t.Errorf("no shell: %+v", ae)
	}
}

// Rollbacks, static sites and apps with several services run no command.
func TestDeployCommandsSkipped(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	a := e.mustApp(t, CreateAppInput{})
	e.setCommands(t, a, "migrate", "warm")
	fake := &fakeHooks{beforeErr: fmt.Errorf("must not run")}
	e.s.hookRunner = fake

	p := e.pipelineFor(t, a, deploy.BuildKindDockerfile)
	p.dep.SourceKind = "rollback"
	if ae := p.beforeDeploy(ctx); ae != nil {
		t.Fatalf("rollback: %v", ae)
	}
	if p.deployCommand(ctx, "after") != "" || !ringHas(p, "A rollback does not run the before-deploy command") {
		t.Errorf("rollback log: %q", p.ring.Lines())
	}

	p = e.pipelineFor(t, a, deploy.BuildKindStatic)
	if p.deployCommand(ctx, "before") != "" || !ringHas(p, "static site") {
		t.Errorf("static: %q", p.ring.Lines())
	}
	p = e.pipelineFor(t, a, deploy.BuildKindDockerfile)
	p.compose = true
	if p.deployCommand(ctx, "before") != "" || !ringHas(p, "several services") {
		t.Errorf("compose: %q", p.ring.Lines())
	}
	if len(fake.calls) != 0 {
		t.Errorf("ran: %v", fake.calls)
	}
}

// The after-deploy command runs once the deployment is live; a failure is
// a warning and the new version stays live.
func TestAfterDeployRunsWhenLive(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	e.exec(t, `UPDATE users SET quota_apps = 20, quota_memory_mb = 4096, quota_cpu_millis = 4000 WHERE id = ?`, e.user.UserID)
	a := e.mustApp(t, CreateAppInput{Type: "worker"}) // no route: the swap needs no proxy
	e.setCommands(t, a, "", "php artisan queue:restart")
	fake := &fakeHooks{s: e.s, output: "Broadcasting queue restart signal.\n", afterErr: &hookExit{Code: 2}}
	e.s.hookRunner = fake

	p := e.pipelineFor(t, a, deploy.BuildKindDockerfile)
	p.newName = "bp-" + a.Slug + "-1"
	fake.depID = p.dep.ID
	if ae := p.goLive(ctx); ae != nil {
		t.Fatalf("goLive: %v", ae)
	}
	if len(fake.calls) != 1 || fake.calls[0] != "after:"+p.newName+":php artisan queue:restart" {
		t.Fatalf("calls: %v", fake.calls)
	}
	if fake.states[0] != "running/running" {
		t.Errorf("state when the command ran: %s", fake.states[0])
	}
	if row, _ := e.s.Q.GetApp(ctx, a.ID); row.CurrentDeploymentID.String != p.dep.ID {
		t.Error("the new version is not live")
	}
	v, err := e.s.GetDeployment(ctx, e.user, p.dep.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if v.State != "running" || v.AfterDeploy != afterDeployFailed || !v.Terminal() {
		t.Errorf("view: %s %s", v.State, v.AfterDeploy)
	}
	if len(v.Warnings) == 0 || v.Warnings[len(v.Warnings)-1].Code != codeAfterDeployFailed || !strings.Contains(v.Warnings[len(v.Warnings)-1].Message, "exit code 2") {
		t.Errorf("warnings: %+v", v.Warnings)
	}
	if len(v.NextSteps) == 0 || !strings.Contains(v.NextSteps[0], "after-deploy command failed") {
		t.Errorf("next: %v", v.NextSteps)
	}
	if !ringHas(p, "[after-deploy] Broadcasting queue restart signal.") || !ringHas(p, "Running after-deploy command in the live container: php artisan queue:restart") {
		t.Errorf("log: %q", p.ring.Lines())
	}

	// Rolling back to it does not copy the warning.
	if w := withoutAfterDeployWarning(sql.NullString{String: mustJSON(v.Warnings), Valid: true}); strings.Contains(w.String, codeAfterDeployFailed) {
		t.Errorf("rollback warnings: %q", w.String)
	}

	// Success.
	fake.afterErr = nil
	p = e.pipelineFor(t, a, deploy.BuildKindDockerfile)
	p.newName = "bp-" + a.Slug + "-2"
	fake.depID = p.dep.ID
	if ae := p.goLive(ctx); ae != nil {
		t.Fatalf("goLive: %v", ae)
	}
	if d, _ := e.s.Q.GetDeployment(ctx, p.dep.ID); d.AfterDeploy != afterDeploySucceeded {
		t.Errorf("after_deploy: %q", d.AfterDeploy)
	}
}

// A swap that is refused runs no after-deploy command and leaves nothing
// pending.
func TestAfterDeployNotRunWhenTheSwapFails(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	e.exec(t, `UPDATE users SET quota_apps = 20, quota_memory_mb = 768, quota_cpu_millis = 2000 WHERE id = ?`, e.user.UserID)
	a := e.mustApp(t, CreateAppInput{Type: "worker", MemoryMB: ptr(int64(512))})
	b := e.mustApp(t, CreateAppInput{Type: "worker", MemoryMB: ptr(int64(512))})
	e.setCommands(t, b, "", "echo hi")
	fake := &fakeHooks{}
	e.s.hookRunner = fake
	pa := e.pipelineFor(t, a, deploy.BuildKindDockerfile)
	pb := e.pipelineFor(t, b, deploy.BuildKindDockerfile)
	for _, p := range []*pipeline{pa, pb} {
		if ae := p.settleResources(ctx); ae != nil {
			t.Fatal(ae)
		}
	}
	if ae := pa.goLive(ctx); ae != nil {
		t.Fatal(ae)
	}
	if ae := pb.goLive(ctx); ae == nil || ae.Code != "memory_quota_exceeded" {
		t.Fatalf("second: %v", ae)
	}
	if len(fake.calls) != 0 {
		t.Errorf("ran: %v", fake.calls)
	}
	if d, _ := e.s.Q.GetDeployment(ctx, pb.dep.ID); d.AfterDeploy != "" {
		t.Errorf("after_deploy left at %q", d.AfterDeploy)
	}
}

// Waiting for a deployment includes its after-deploy command, and one that
// a restart interrupted is settled by the reconciler.
func TestAfterDeployPendingIsNotTerminal(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	a := e.mustApp(t, CreateAppInput{})
	p := e.pipelineFor(t, a, deploy.BuildKindDockerfile)
	e.exec(t, `UPDATE deployments SET state = 'running', after_deploy = 'running' WHERE id = ?`, p.dep.ID)
	v, _ := e.s.GetDeployment(ctx, e.user, p.dep.ID, 0)
	if v.Terminal() || !strings.Contains(v.NextSteps[0], "still runs") {
		t.Errorf("running command: terminal=%v next=%v", v.Terminal(), v.NextSteps)
	}
	e.s.reconcileAfterDeploy(ctx)
	v, _ = e.s.GetDeployment(ctx, e.user, p.dep.ID, 0)
	if !v.Terminal() || v.AfterDeploy != afterDeployFailed || len(v.Warnings) != 1 || !strings.Contains(v.Warnings[0].Message, "restarted") {
		t.Errorf("after reconcile: %s %+v", v.AfterDeploy, v.Warnings)
	}

	// The reconciler listed a deployment whose pipeline then finished the
	// command and ended: the result stays.
	r := e.pipelineFor(t, a, deploy.BuildKindDockerfile)
	e.exec(t, `UPDATE deployments SET state = 'running', after_deploy = 'succeeded' WHERE id = ?`, r.dep.ID)
	e.s.settleAfterDeploy(ctx, r.dep.ID)
	if d, _ := e.s.Q.GetDeployment(ctx, r.dep.ID); d.AfterDeploy != afterDeploySucceeded || d.Warnings.Valid {
		t.Errorf("finished command: %q %v", d.AfterDeploy, d.Warnings)
	}

	// A deployment that never went live: nothing pending is left.
	q := e.pipelineFor(t, a, deploy.BuildKindDockerfile)
	e.exec(t, `UPDATE deployments SET state = 'failed', after_deploy = 'pending' WHERE id = ?`, q.dep.ID)
	e.s.reconcileAfterDeploy(ctx)
	if d, _ := e.s.Q.GetDeployment(ctx, q.dep.ID); d.AfterDeploy != "" || d.Warnings.Valid {
		t.Errorf("failed deployment: %q %v", d.AfterDeploy, d.Warnings)
	}
}

func TestHookOutputIsCapped(t *testing.T) {
	e := depTestSvc(t)
	a := e.mustApp(t, CreateAppInput{})
	p := e.pipelineFor(t, a, deploy.BuildKindDockerfile)
	p.ring = deploy.NewLineRing(5)
	out := p.hookOutput("before-deploy")
	line := strings.Repeat("y", 1023) + "\n"
	for i := 0; i < hookOutputMax/1024+10; i++ {
		_, _ = io.WriteString(out, line)
	}
	_, _ = io.WriteString(out, "never shown")
	out.flush()
	lines := p.ring.Lines()
	if !strings.Contains(lines[len(lines)-1], "the rest is not shown") {
		t.Errorf("last lines: %q", lines)
	}
	// A long line without a newline is cut into pieces.
	p.ring = deploy.NewLineRing(5)
	out = p.hookOutput("after-deploy")
	_, _ = io.WriteString(out, strings.Repeat("z", hookLineMax+10))
	out.flush()
	if got := p.ring.Lines(); len(got) != 2 || !strings.HasPrefix(got[0], "[after-deploy] zzz") {
		t.Errorf("long line: %d lines", len(got))
	}
}
