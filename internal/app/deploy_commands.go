package app

// OWNER: deploy agent. Deploy commands (docs/decisions.md, "Deploy
// commands"): a shell command run before the new version takes traffic, in a
// one-off container of the new image (database migrations), and one run in
// the live container once the new version serves.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/deploy"
	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/store/db"
	"github.com/moby/moby/api/types/container"
)

const (
	// maxDeployCommand is the longest command bitploy stores.
	maxDeployCommand = 2000
	// hookOutputMax is how much of a command's output goes into the deploy
	// log; the rest is dropped (the command still runs to its end).
	hookOutputMax = 1 << 20
	// hookLineMax cuts a line without a newline.
	hookLineMax = 4 << 10

	// after_deploy of a deployment.
	afterDeployPending   = "pending" // runs once the deployment is live
	afterDeployRunning   = "running"
	afterDeploySucceeded = "succeeded"
	afterDeployFailed    = "failed"

	codeAfterDeployFailed = "after_deploy_failed"
)

// Timeouts of the commands; variables so tests can shorten them.
var (
	beforeDeployTimeout = 10 * time.Minute
	afterDeployTimeout  = 5 * time.Minute
)

// deployCommands are the before- and after-deploy commands of an app; ""
// when it has none.
type deployCommands struct {
	Before string
	After  string
}

func (s *Service) deployCommandsOf(ctx context.Context, appID string) deployCommands {
	row, err := s.Q.GetAppDeployCommands(ctx, appID)
	if err != nil {
		return deployCommands{}
	}
	return deployCommands{Before: row.BeforeDeploy, After: row.AfterDeploy}
}

// normalizeDeployCommand trims a command; "" clears it.
func normalizeDeployCommand(field, v string) (string, error) {
	v = strings.TrimSpace(strings.ReplaceAll(v, "\r\n", "\n"))
	if len(v) > maxDeployCommand {
		return "", apperr.BadRequest("deploy_command_invalid",
			fmt.Sprintf("The %s is longer than %d characters.", field, maxDeployCommand),
			"Put the steps in a script in the code (like scripts/migrate.sh) and run that script.").WithNext("update_app")
	}
	if strings.ContainsRune(v, 0) {
		return "", apperr.BadRequest("deploy_command_invalid", "The "+field+" contains a NUL character.", "Send the command as plain text.").WithNext("update_app")
	}
	return v, nil
}

// deployCommandsRefused says why an app cannot have deploy commands, or nil.
// buildMode is the build mode the app will have.
func deployCommandsRefused(row db.App, buildMode string) error {
	switch {
	case row.Template != "" || row.CatalogID != "":
		return apperr.Unprocessable("deploy_commands_unsupported",
			"This app is a ready-made service (a database or one-click app), so it has no deploy commands.",
			"Set before_deploy_command on the app whose code needs it, like the app that runs the database migrations.").WithNext("get_app")
	case isMulti(row) || buildMode == deploy.BuildModeCompose:
		return apperr.Unprocessable("deploy_commands_unsupported",
			"This app runs the services of a compose file, and deploy commands are not supported for such apps yet.",
			"Run the migrations when the service starts (in its command in the compose file), or deploy the code that needs them as an app with one Dockerfile.").WithNext("get_app")
	case deploy.AlwaysStaticMode(buildMode):
		return apperr.Unprocessable("deploy_commands_unsupported",
			"This app is a static site: it runs no code of its own, so it has no deploy commands.",
			"Leave before_deploy_command and after_deploy_command out for a static site.").WithNext("get_app")
	}
	return nil
}

// planDeployCommands checks the commands an update sets (nil leaves one as
// it is) and returns what to store, or nil when nothing changes. The
// commands that change go into changed, for the audit log.
func (s *Service) planDeployCommands(ctx context.Context, row db.App, buildMode string, before, after *string, changed map[string]any) (*deployCommands, error) {
	if before == nil && after == nil {
		return nil, nil
	}
	cur := s.deployCommandsOf(ctx, row.ID)
	next := cur
	if before != nil {
		v, err := normalizeDeployCommand("before-deploy command", *before)
		if err != nil {
			return nil, err
		}
		next.Before = v
	}
	if after != nil {
		v, err := normalizeDeployCommand("after-deploy command", *after)
		if err != nil {
			return nil, err
		}
		next.After = v
	}
	if (next.Before != "" && next.Before != cur.Before) || (next.After != "" && next.After != cur.After) {
		if err := deployCommandsRefused(row, buildMode); err != nil {
			return nil, err
		}
	}
	if next == cur {
		return nil, nil
	}
	if next.Before != cur.Before {
		changed["before_deploy_command"] = next.Before
	}
	if next.After != cur.After {
		changed["after_deploy_command"] = next.After
	}
	return &next, nil
}

// checkDeployCommands validates the commands of a new app before it is
// created (create_app).
func checkDeployCommands(slug, buildMode string, before, after string) (deployCommands, error) {
	b, err := normalizeDeployCommand("before-deploy command", before)
	if err != nil {
		return deployCommands{}, err
	}
	a, err := normalizeDeployCommand("after-deploy command", after)
	if err != nil {
		return deployCommands{}, err
	}
	if b != "" || a != "" {
		if err := deployCommandsRefused(db.App{Slug: slug}, buildMode); err != nil {
			return deployCommands{}, err
		}
	}
	return deployCommands{Before: b, After: a}, nil
}

// ---- running the commands ----

// deployHooks runs deploy commands. Docker does it for real (dockerHooks);
// tests use a fake.
type deployHooks interface {
	// Before runs cmd with sh -c in a one-off container of plan: the new
	// image with the variables, limits, networks and volumes the app runs
	// with. The container is removed afterwards. Output goes to out.
	Before(ctx context.Context, plan startPlan, cmd string, out io.Writer) error
	// After runs cmd with sh -c in the running container name.
	After(ctx context.Context, name, cmd string, out io.Writer) error
}

// hookExit is a deploy command that ran and exited with a code other than 0.
type hookExit struct {
	Code int
	OOM  bool // killed for using more memory than the app may
}

func (e *hookExit) Error() string { return fmt.Sprintf("exit code %d", e.Code) }

type dockerHooks struct{ s *Service }

func (h dockerHooks) Before(ctx context.Context, plan startPlan, cmd string, out io.Writer) error {
	s := h.s
	rs, ae := s.runSpec(ctx, plan)
	if ae != nil {
		return ae
	}
	// The same container as the app's, except: its own name, no DNS
	// aliases (nobody may reach it as the app), no HEALTHCHECK, no restart,
	// and the command instead of the image's ENTRYPOINT and CMD.
	rs.Name = dockerx.BeforeDeployName(plan.App.Slug, plan.Seq)
	rs.Aliases = nil
	extra := make([]dockerx.NetworkAttach, len(rs.Extra))
	for i, x := range rs.Extra {
		extra[i] = dockerx.NetworkAttach{Network: x.Network}
	}
	rs.Extra = extra
	rs.Healthcheck = &container.HealthConfig{Test: []string{"NONE"}}
	rs.OneOff = true
	rs.Entrypoint, rs.Cmd = []string{"sh", "-c", cmd}, nil
	defer func() { _ = s.Docker.RemoveContainer(context.WithoutCancel(ctx), rs.Name) }()
	if ae := s.startRunSpec(ctx, plan, rs); ae != nil {
		return ae
	}
	code, oom, err := s.Docker.WaitWithOutput(ctx, rs.Name, out)
	if err != nil {
		return err
	}
	if code != 0 {
		return &hookExit{Code: int(code), OOM: oom}
	}
	return nil
}

func (h dockerHooks) After(ctx context.Context, name, cmd string, out io.Writer) error {
	_, err := h.s.Docker.Exec(ctx, name, []string{"sh", "-c", cmd}, dockerx.ExecOptions{Stdout: out, Stderr: out})
	var ee *dockerx.ExecError
	if errors.As(err, &ee) {
		return &hookExit{Code: ee.Code}
	}
	return err
}

// hooks returns what runs deploy commands, or nil without Docker.
func (s *Service) hooks() deployHooks {
	if s.hookRunner != nil {
		return s.hookRunner
	}
	if s.Docker == nil {
		return nil
	}
	return dockerHooks{s}
}

// hookOutput writes a command's output into the deploy log line by line,
// each line marked with the command, up to hookOutputMax bytes.
type hookOutput struct {
	p       *pipeline
	prefix  string
	mu      sync.Mutex
	buf     []byte
	written int
	cut     bool
}

func (p *pipeline) hookOutput(which string) *hookOutput {
	return &hookOutput{p: p, prefix: "[" + which + "] "}
}

func (o *hookOutput) Write(b []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := len(b)
	if o.cut {
		return n, nil
	}
	o.buf = append(o.buf, b...)
	for {
		i := bytes.IndexByte(o.buf, '\n')
		if i < 0 && len(o.buf) < hookLineMax {
			break
		}
		if i < 0 {
			i = hookLineMax
			o.line(o.buf[:i])
			o.buf = o.buf[i:]
		} else {
			o.line(o.buf[:i])
			o.buf = o.buf[i+1:]
		}
		if o.cut {
			o.buf = nil
			break
		}
	}
	return n, nil
}

func (o *hookOutput) line(b []byte) {
	o.written += len(b) + 1
	if o.written > hookOutputMax {
		o.cut = true
		o.p.logf("The output is longer than %d KB; the rest is not shown.", hookOutputMax>>10)
		return
	}
	o.p.writeLog(o.prefix + strings.TrimRight(string(b), "\r"))
}

// flush writes a last line without a newline.
func (o *hookOutput) flush() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.buf) > 0 && !o.cut {
		o.line(o.buf)
	}
	o.buf = nil
}

// deployCommand is the command of the deployment for which ("before" or
// "after"), or "" when there is none or it does not run for this deployment.
func (p *pipeline) deployCommand(ctx context.Context, which string) string {
	cmds := p.s.deployCommandsOf(ctx, p.app.ID)
	cmd, label := cmds.Before, "before-deploy"
	if which == "after" {
		cmd, label = cmds.After, "after-deploy"
	}
	if cmd == "" {
		return ""
	}
	switch {
	case p.dep.SourceKind == "rollback":
		// The code goes back, the data does not: migrations of the newer
		// version stay applied, and the commands are those of the newer code.
		p.logf("A rollback does not run the %s command.", label)
		return ""
	case p.app.Template != "" || p.app.CatalogID != "":
		return ""
	case p.compose || isMulti(p.app):
		p.logf("The %s command is skipped: deploy commands are not supported for an app with several services.", label)
		return ""
	case deploy.IsStaticKind(p.kind):
		p.logf("The %s command is skipped: a static site runs no code of its own.", label)
		return ""
	}
	return cmd
}

// beforeDeploy runs the before-deploy command in a one-off container of the
// new version, before the new app container starts and before a /data app's
// old container is stopped: the live version keeps serving while it runs,
// and a failure stops the deployment.
func (p *pipeline) beforeDeploy(ctx context.Context) *apperr.Error {
	cmd := p.deployCommand(ctx, "before")
	if cmd == "" || p.primary == nil {
		return nil
	}
	h := p.s.hooks()
	if h == nil {
		return apperr.Unavailable("docker_unavailable", "bitploy cannot reach Docker right now.", "Tell the bitploy admin.")
	}
	plan := startPlan{App: p.app, DepID: p.dep.ID, Seq: p.dep.Seq, Image: p.primary.image, Port: p.primary.port, ExtraEnv: p.extraEnv}
	p.logf("Running before-deploy command: %s", cmd)
	p.logf("It runs in a one-off container of the new version (up to %s).", durationText(beforeDeployTimeout))
	out := p.hookOutput("before-deploy")
	rctx, cancel := context.WithTimeout(ctx, beforeDeployTimeout)
	defer cancel()
	start := time.Now()
	err := h.Before(rctx, plan, cmd, out)
	out.flush()
	if ctx.Err() != nil {
		return errCancelled
	}
	if err == nil {
		p.logf("Before-deploy command finished in %s.", durationText(time.Since(start)))
		return nil
	}
	return p.beforeDeployFailed(err, rctx.Err() != nil)
}

func (p *pipeline) beforeDeployFailed(err error, timedOut bool) *apperr.Error {
	const keep = " The new version was not started; the version that was live keeps running."
	readLog := "Read the command's output in the deploy log (get_logs kind=build, the lines marked [before-deploy])."
	var ex *hookExit
	var ae *apperr.Error
	switch {
	case timedOut:
		return apperr.Unprocessable("before_deploy_timeout",
			fmt.Sprintf("The before-deploy command did not finish within %s, so it was stopped.", durationText(beforeDeployTimeout))+keep,
			readLog+" A command that waits for a database it cannot reach hangs: check the variables it uses (like DATABASE_URL). Make the command finish sooner, then deploy again.").WithNext("get_logs")
	case errors.As(err, &ex) && ex.OOM:
		return apperr.Unprocessable("before_deploy_failed",
			"The before-deploy command ran out of memory and was killed."+keep,
			fmt.Sprintf("It gets the app's memory (%d MB now). Raise memory_mb with update_app (whoami shows your quota), or make the command use less memory, then deploy again.", p.app.MemoryMb)).WithNext("update_app")
	case errors.As(err, &ex):
		hint := readLog + " Fix the code or the command (update_app before_deploy_command=...), then deploy again."
		if ex.Code == 127 {
			hint = "The shell could not find the program the command starts (exit code 127). Check the spelling, and that the image has it installed (it runs in the app's own image, from its WORKDIR). " + readLog
		}
		return apperr.Unprocessable("before_deploy_failed",
			fmt.Sprintf("The before-deploy command failed with exit code %d.", ex.Code)+keep, hint).WithNext("get_logs")
	case errors.As(err, &ae):
		hint := ae.Hint
		if strings.Contains(ae.Message, "executable file not found") {
			hint = "The command runs with sh -c, so the image needs a shell (sh). Images built FROM scratch or distroless images have none: use a base image with a shell, or remove the before-deploy command."
		}
		return apperr.Unprocessable("before_deploy_failed",
			"bitploy could not run the before-deploy command: "+ae.Message+keep, hint).WithNext("get_logs")
	}
	return apperr.Unprocessable("before_deploy_failed",
		"bitploy could not run the before-deploy command: "+apperr.Redact(dockerx.DescribeError(err))+"."+keep,
		"Deploy again in a minute. If it repeats, tell the bitploy admin.").WithNext("deploy")
}

// markAfterDeploy records that the deployment runs its after-deploy command
// once it is live, so that waiting for the deployment includes the command.
func (p *pipeline) markAfterDeploy(ctx context.Context, state string) {
	_ = p.s.Q.SetDeploymentAfterDeploy(context.WithoutCancel(ctx), db.SetDeploymentAfterDeployParams{AfterDeploy: state, ID: p.dep.ID})
	p.s.NotifyDeployment(p.dep.ID)
}

// afterDeploy runs the after-deploy command in the live container. A
// failure is a warning on the deployment: the new version stays live.
func (p *pipeline) afterDeploy(ctx context.Context, cmd string) {
	bg := context.WithoutCancel(ctx)
	h := p.s.hooks()
	if h == nil || p.newName == "" {
		p.afterDeployFailed(bg, "bitploy could not reach Docker to run it.")
		return
	}
	p.markAfterDeploy(ctx, afterDeployRunning)
	p.logf("Running after-deploy command in the live container: %s", cmd)
	out := p.hookOutput("after-deploy")
	rctx, cancel := context.WithTimeout(ctx, afterDeployTimeout)
	defer cancel()
	start := time.Now()
	err := h.After(rctx, p.newName, cmd, out)
	out.flush()
	var ex *hookExit
	switch {
	case err == nil:
		p.logf("After-deploy command finished in %s.", durationText(time.Since(start)))
		p.markAfterDeploy(bg, afterDeploySucceeded)
	case ctx.Err() != nil:
		p.afterDeployFailed(bg, "It was stopped before it finished (the app was stopped, or bitploy restarted).")
	case rctx.Err() != nil:
		p.afterDeployFailed(bg, fmt.Sprintf("It did not finish within %s.", durationText(afterDeployTimeout)))
	case errors.As(err, &ex):
		p.afterDeployFailed(bg, fmt.Sprintf("It stopped with exit code %d.", ex.Code))
	default:
		p.afterDeployFailed(bg, "bitploy could not run it: "+apperr.Redact(dockerx.DescribeError(err))+".")
	}
}

func (p *pipeline) afterDeployFailed(ctx context.Context, why string) {
	msg := "The after-deploy command failed. " + why + " The new version stays live."
	p.logf("WARNING: %s", msg)
	p.addWarning(ctx, deploy.Finding{Code: codeAfterDeployFailed, Severity: "warning", Message: msg,
		Hint: "Read its output in the deploy log (get_logs kind=build, the lines marked [after-deploy]). Fix the command (update_app after_deploy_command=...) or the code; it runs again on the next deploy."})
	p.markAfterDeploy(ctx, afterDeployFailed)
	p.s.notifyAfterDeployFailed(ctx, p.dep.ID, why)
}

// afterDeployBusy reports an after-deploy command that has yet to finish.
func afterDeployBusy(state string) bool {
	return state == afterDeployPending || state == afterDeployRunning
}

// withoutAfterDeployWarning drops the after-deploy warning from the warnings
// of a deployment that is rolled back to: the rollback runs no command.
func withoutAfterDeployWarning(w sql.NullString) sql.NullString {
	if !w.Valid || !strings.Contains(w.String, codeAfterDeployFailed) {
		return w
	}
	var list []deploy.Finding
	if json.Unmarshal([]byte(w.String), &list) != nil {
		return w
	}
	keep := list[:0]
	for _, f := range list {
		if f.Code != codeAfterDeployFailed {
			keep = append(keep, f)
		}
	}
	if len(keep) == 0 {
		return sql.NullString{}
	}
	return nullStr(mustJSON(keep))
}

// reconcileAfterDeploy settles after-deploy commands that no pipeline runs
// any more (bitploy restarted): failed on a deployment that went live,
// nothing on one that did not.
func (s *Service) reconcileAfterDeploy(ctx context.Context) {
	ids, err := s.Q.ListAfterDeployBusy(ctx)
	if err != nil {
		return
	}
	for _, id := range ids {
		s.settleAfterDeploy(ctx, id)
	}
}

// settleAfterDeploy settles the after-deploy command of one deployment that
// ListAfterDeployBusy returned, unless a pipeline still runs it.
func (s *Service) settleAfterDeploy(ctx context.Context, id string) {
	if s.activeDeployment(id) {
		return
	}
	// Read again now that no pipeline runs it: the pipeline may have
	// finished the command (and ended) since the list was read, and its
	// result must stay.
	d, err := s.Q.GetDeployment(ctx, id)
	if err != nil || !afterDeployBusy(d.AfterDeploy) {
		return
	}
	p := &pipeline{s: s, dep: d, ring: deploy.NewLineRing(1)}
	if d.State != "running" && d.State != "superseded" {
		p.markAfterDeploy(ctx, "")
		return
	}
	if a, err := s.Q.GetApp(ctx, d.AppID); err == nil {
		p.app = a
	}
	if f, err := os.OpenFile(s.Cfg.BuildLog(d.ID), os.O_APPEND|os.O_WRONLY, 0o640); err == nil {
		p.logFile = f
	}
	p.afterDeployFailed(ctx, "bitploy restarted while it ran.")
	if p.logFile != nil {
		p.logFile.Close()
	}
	s.events.flushBuild(d.ID)
}

// durationText is a duration in plain words: 3 s, 2 min 5 s, 10 min.
func durationText(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%d s", int(d.Seconds()))
	}
	m, sec := int(d.Minutes()), int(d.Seconds())%60
	if sec == 0 {
		return fmt.Sprintf("%d min", m)
	}
	return fmt.Sprintf("%d min %d s", m, sec)
}
