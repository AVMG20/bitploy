package app

// OWNER: deploy agent. The deployment pipeline (SPEC "Deploy model"):
// fetching, validating, building, starting, health_check, swapping, running.
// A failed deploy never takes the working version down: the old container is
// only touched after the new one is healthy (or, with a volume, restarted if
// the new one fails).

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/deploy"
	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/proxy"
	"github.com/AVMG20/bitploy/internal/store/db"
	"github.com/moby/moby/api/types/container"
)

const (
	maxGitBytes      = 500 << 20
	runtimeTailLines = 50
)

// Timings from SPEC "Deploy model"; variables so integration tests can shorten them.
var (
	healthTimeout  = 60 * time.Second // HTTP health check window
	workerAliveFor = 30 * time.Second // a worker must still run after this long
	oldStopDelay   = 10 * time.Second // old container keeps serving this long after a swap
)

// errCancelled is returned by pipeline steps when the deployment was
// cancelled (stop, delete) or bitploy is shutting down.
var errCancelled = &apperr.Error{Code: "cancelled", Message: "The deployment was cancelled."}

type pipeline struct {
	s       *Service
	app     db.App
	dep     db.Deployment
	logFile *os.File
	logMu   sync.Mutex
	ring    *deploy.LineRing

	buildDir string
	srcRoot  string

	port int64
	// portFromImage: a web app whose port is not known before the build; set
	// in validating, resolved by resolveImagePort once the image exists.
	portFromImage bool
	imageExposed  []int // TCP ports the built image declares
	hasHC         bool
	hcCfg         *container.HealthConfig
	findings      []deploy.Finding

	kind    string // how this deployment is built (deploy.BuildKind*); a rollback has the kind of its target
	setSize bool   // the app gets the limits below when this deployment goes live
	sizeMem int64
	sizeCPU int64

	image      string
	builtHere  bool   // we built the image, so a failure removes it
	newName    string // container created for this deployment
	prev       *db.Deployment
	prevName   string
	oldStopped bool // recreate mode: the old container was stopped for us
	failTail   []string
	slotHeld   bool
	slotOnce   sync.Once
}

// runPipeline runs one claimed deployment to its end. slotHeld says the
// caller took a build slot, which is released after the build step.
func (s *Service) runPipeline(ctx context.Context, depID string, slotHeld bool) {
	p := &pipeline{s: s, slotHeld: slotHeld, ring: deploy.NewLineRing(runtimeTailLines)}
	defer p.releaseSlot()

	n, err := s.Q.ClaimDeployment(ctx, db.ClaimDeploymentParams{StartedAt: sql.NullInt64{Int64: s.now(), Valid: true}, ID: depID})
	if err != nil || n == 0 {
		return // cancelled while queued, or claimed elsewhere
	}
	dep, err := s.Q.GetDeployment(ctx, depID)
	if err != nil {
		s.Log.Error("load deployment", "id", depID, "err", err)
		return
	}
	app, err := s.Q.GetApp(ctx, dep.AppID)
	if err != nil {
		s.Log.Error("load app for deployment", "id", depID, "err", err)
		return
	}
	p.dep, p.app = dep, app
	s.NotifyDeployment(depID)

	_ = os.MkdirAll(s.Cfg.LogsDir(), 0o750)
	if f, err := os.OpenFile(s.Cfg.BuildLog(depID), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640); err == nil {
		p.logFile = f
		defer f.Close()
	}
	p.logf("Deployment %d of %s started (%s source).", dep.Seq, app.Slug, dep.SourceKind)

	ae := p.execute(ctx)
	p.finish(ctx, ae)
}

func (p *pipeline) releaseSlot() {
	p.slotOnce.Do(func() {
		if p.slotHeld {
			<-p.s.buildSlots()
		}
	})
}

// logf writes a bitploy line into the build log.
func (p *pipeline) logf(format string, args ...any) {
	p.writeLog("==> " + fmt.Sprintf(format, args...))
}

func (p *pipeline) writeLog(line string) {
	line = apperr.Redact(line)
	p.logMu.Lock()
	defer p.logMu.Unlock()
	p.ring.Add(line)
	if p.logFile != nil {
		_, _ = p.logFile.WriteString(line + "\n")
	}
	p.s.events.buildLine(p.app.UserID, p.app.Slug, p.dep.ID, line) // live build log, batched
}

func (p *pipeline) setState(ctx context.Context, state string) *apperr.Error {
	if ctx.Err() != nil {
		return errCancelled
	}
	if state == "swapping" && !p.s.commitSwap(p.dep.ID) {
		return errCancelled
	}
	if err := p.s.Q.SetDeploymentState(ctx, db.SetDeploymentStateParams{State: state, ID: p.dep.ID}); err != nil {
		return apperr.Internal(err)
	}
	p.dep.State = state
	p.s.NotifyDeployment(p.dep.ID)
	return nil
}

func (p *pipeline) execute(ctx context.Context) *apperr.Error {
	if p.dep.SourceKind == "rollback" {
		p.releaseSlot()
		p.image = p.dep.Image.String
		p.kind = p.dep.BuildKind // copied from the target when the rollback was queued
		// The port the target ran on: the app's port setting may have changed
		// since, for newer code.
		p.port = p.dep.Port.Int64
		if !p.dep.Port.Valid && p.app.Port.Valid {
			p.port = p.app.Port.Int64
		}
		p.hasHC = p.dep.HasHealthcheck != 0
		if info, err := p.s.Docker.InspectImage(ctx, p.image); err == nil {
			p.hcCfg = info.Healthcheck
		}
		p.logf("Rolling back to the image of deployment %s (%s).", p.dep.SourceRef, p.image)
	} else {
		if ae := p.setState(ctx, "fetching"); ae != nil {
			return ae
		}
		if ae := p.fetch(ctx); ae != nil {
			return ae
		}
		if ae := p.setState(ctx, "validating"); ae != nil {
			return ae
		}
		if ae := p.validate(ctx); ae != nil {
			return ae
		}
		if ae := p.setState(ctx, "building"); ae != nil {
			return ae
		}
		ae := p.build(ctx)
		p.releaseSlot()
		if ae != nil {
			return ae
		}
		_ = os.RemoveAll(p.buildDir) // the context is no longer needed
		if ae := p.resolveImagePort(ctx); ae != nil {
			return ae
		}
	}

	// The kind of build is known now: a static site is free and gets fixed
	// limits, anything else needs room in the quota (several first deploys can
	// be queued at once, and a static site may turn into an app).
	if ae := p.settleResources(ctx); ae != nil {
		return ae
	}
	if ae := p.setState(ctx, "starting"); ae != nil {
		return ae
	}
	if ae := p.start(ctx); ae != nil {
		return ae
	}
	if ae := p.setState(ctx, "health_check"); ae != nil {
		return ae
	}
	if ae := p.healthCheck(ctx); ae != nil {
		return ae
	}
	if ae := p.setState(ctx, "swapping"); ae != nil {
		return ae
	}
	return p.swap(ctx)
}

// ---- fetching ----

func (p *pipeline) fetch(ctx context.Context) *apperr.Error {
	s := p.s
	p.buildDir = s.Cfg.BuildDir(p.dep.ID)
	_ = os.RemoveAll(p.buildDir)
	if err := os.MkdirAll(p.buildDir, 0o750); err != nil {
		return apperr.Internal(err)
	}
	switch p.dep.SourceKind {
	case "files", "upload":
		p.logf("Unpacking the %s source.", p.dep.SourceKind)
		if _, err := os.Stat(s.sourceZip(p.dep.ID)); err != nil {
			return apperr.Conflict("source_gone", "The stored source for this deployment is gone.", "Send the files again with deploy_files, or upload a zip.").WithNext("deploy_files")
		}
		res, err := deploy.ExtractZip(s.sourceZip(p.dep.ID), p.buildDir, deploy.DefaultZipLimits, p.dep.SourceKind == "upload")
		if err != nil {
			return asApperr(err, "source_fetch_failed")
		}
		if res.Stripped != "" {
			p.logf("Removed the top-level folder %q from the zip.", res.Stripped)
		}
		p.logf("%d files, %d KB.", len(res.Files), res.Bytes/1024)
		p.srcRoot = p.buildDir
	case "git":
		repo := p.app.GitRepoUrl.String
		if repo == "" {
			return noSourceErr()
		}
		p.logf("Cloning %s (branch %s).", redactURL(repo), p.app.GitBranch.String)
		token := ""
		if isGitHub(repo) {
			token = s.gitCloneToken(ctx, p.app.UserID, repo)
		}
		cr, err := deploy.Clone(ctx, deploy.GitSource{URL: repo, Branch: p.app.GitBranch.String, Subdir: p.app.GitSubdir.String, Token: token, SettingsURL: strings.TrimRight(s.Cfg.PublicURL, "/") + "/settings/github", AccessHint: s.gitAccessHint(ctx, Actor{Role: "user"})}, p.buildDir, maxGitBytes)
		if err != nil {
			if ctx.Err() != nil {
				return errCancelled
			}
			return asApperr(err, "source_fetch_failed")
		}
		p.srcRoot = cr.ContextDir
		if p.srcRoot == "" {
			p.srcRoot = p.buildDir
		}
		_ = s.Q.SetDeploymentCommit(ctx, db.SetDeploymentCommitParams{
			CommitSha: nullStr(cr.CommitSHA), CommitMessage: nullStr(firstLine(cr.CommitMessage)), ID: p.dep.ID,
		})
		p.logf("Commit %s.", shortSHA(cr.CommitSHA))
	default:
		return apperr.Internal(fmt.Errorf("unknown source kind %q", p.dep.SourceKind))
	}
	return nil
}

// ---- validating ----

func (p *pipeline) validate(ctx context.Context) *apperr.Error {
	root, err := os.OpenRoot(p.srcRoot)
	if err != nil {
		return apperr.Internal(err)
	}
	defer root.Close()
	files, err := deploy.ListFiles(p.srcRoot, deploy.MaxZipFiles*2)
	if err != nil {
		return apperr.Internal(err)
	}
	read := func(rel string) []byte {
		b, err := root.ReadFile(rel) // os.Root refuses to follow symlinks out of the tree
		if err != nil {
			return nil
		}
		return b
	}
	// The build mode decides between the app's Dockerfile and a static site.
	effApp, plan, files, read, perr := planBuild(p.app, files, read)
	if perr != nil {
		return perr
	}
	if plan.Why != "" {
		p.logf("%s", plan.Why)
	}
	if len(plan.Inject) > 0 {
		if err := writeInjected(p.srcRoot, plan); err != nil {
			what := "to build your project"
			if plan.Kind == deploy.BuildKindStatic {
				what = "to serve them as a website"
			}
			return apperr.Unprocessable("source_invalid", "Bitploy could not prepare the files "+what+".",
				"Check that .dockerignore in the project is a plain file, then deploy again.").WithCause(err)
		}
		p.app = effApp // the build uses the generated Dockerfile at the source root
	}
	p.kind = plan.Kind
	res, ctxFiles := p.s.runChecks(p.app, files, read)
	res = templateFindings(plan, res)
	for _, f := range res.Findings {
		p.logf("check %s (%s): %s", f.Code, f.Severity, f.Message)
	}
	if cerr := checkError(res, ctxFiles); cerr != nil {
		return apperr.As(cerr)
	}
	p.port = int64(res.Port)
	p.hasHC = res.HasHealthcheck
	p.findings = res.Findings
	// A web app with no port setting and no EXPOSE gets its port from the
	// built image (resolveImagePort, after the build).
	p.portFromImage = p.app.Type == "web" && p.port == 0
	var nonErr []deploy.Finding
	for _, f := range res.Findings {
		if f.Severity != "error" {
			nonErr = append(nonErr, f)
		}
	}
	warn := sql.NullString{}
	if len(nonErr) > 0 {
		warn = nullStr(mustJSON(nonErr))
	}
	if err := p.s.Q.SetDeploymentValidated(ctx, db.SetDeploymentValidatedParams{
		Port: portOf(int(p.port)), HasHealthcheck: b2i(p.hasHC), Warnings: warn, BuildKind: p.kind, ID: p.dep.ID,
	}); err != nil {
		return apperr.Internal(err)
	}
	return nil
}

// settleResources decides the limits this deployment starts with, once its
// kind is known, and checks the quota where the app starts to count.
//
//   - A static site (deploy.IsStaticKind) gets the fixed limits and is free:
//     no quota check, whatever the app had before.
//   - An app that was a static site and is now built from another kind of
//     source starts to count: it gets the default limits and they must fit.
//   - Any other app keeps its limits; a new or stopped app must fit.
//
// The limits are written to the app when the deployment goes live (swap), so a
// failed deployment leaves the live version as it was. start() uses them for
// the new container.
func (p *pipeline) settleResources(ctx context.Context) *apperr.Error {
	fresh, err := p.s.Q.GetApp(ctx, p.app.ID)
	if err != nil {
		return errCancelled // app deleted
	}
	if deploy.IsStaticKind(p.kind) {
		p.setSize, p.sizeMem, p.sizeCPU = true, staticMemoryMB, staticCPUMillis
		if fresh.MemoryMb != staticMemoryMB || fresh.CpuMillis != staticCPUMillis {
			p.logf("A static site runs with %d MB of memory and %s CPU and does not count towards your memory and CPU allowance.", staticMemoryMB, cpuText(staticCPUMillis))
		}
		return nil
	}
	if p.s.appIsStatic(ctx, fresh) {
		p.setSize, p.sizeMem, p.sizeCPU = true, defaultMemoryMB, defaultCPUMillis
		p.logf("This app is no longer a static site, so it now counts towards your memory and CPU allowance: %d MB of memory and %s CPU.", defaultMemoryMB, cpuText(defaultCPUMillis))
		err := p.s.checkConsumeQuota(ctx, fresh, defaultMemoryMB, defaultCPUMillis, defaultMemoryMB-staticMemoryMB)
		var ae *apperr.Error
		if errors.As(err, &ae) && (ae.Code == "memory_quota_exceeded" || ae.Code == "cpu_quota_exceeded") {
			return apperr.Forbidden(ae.Code, "This app is no longer a static site, so it counts towards your memory and CPU allowance. "+ae.Message,
				"Stop or delete another app, lower the memory of your other apps with update_app, or ask a bitploy admin for a bigger quota. Then deploy again.").WithNext("update_app")
		}
		if err != nil {
			return apperr.As(err)
		}
		return nil
	}
	if err := p.s.checkStartQuota(ctx, fresh); err != nil {
		return apperr.As(err)
	}
	return nil
}

func cpuText(millis int64) string {
	return strconv.FormatFloat(float64(millis)/1000, 'f', -1, 64)
}

// ---- building ----

func (p *pipeline) build(ctx context.Context) *apperr.Error {
	s := p.s
	if ok, err := s.DiskHasRoom(); err == nil && !ok {
		return apperr.Unavailable("disk_full", "The server is almost out of disk space, so new builds are paused.",
			"Tell the bitploy admin; your running apps are not affected. Try again later.")
	}
	if !filepath.IsLocal(filepath.FromSlash(p.app.ContextDir)) && p.app.ContextDir != "." {
		return apperr.Unprocessable("context_dir_invalid", "The build context folder is outside the project.", "Use update_app to set context_dir to a folder inside the project.")
	}
	// Resolve symlinks on disk: the context and the Dockerfile must stay inside the source.
	ctxDir, dockerfile, perr := deploy.ResolveBuildPaths(p.srcRoot, p.app.ContextDir, p.app.DockerfilePath)
	if perr != nil {
		return perr
	}
	bld := s.buildBuilder()
	if err := bld.Ensure(ctx); err != nil {
		if ctx.Err() != nil {
			return errCancelled
		}
		return asApperr(err, "builder_unavailable")
	}
	p.image = dockerx.ImageName(p.app.Slug, p.dep.Seq)
	labels := s.Docker.ManagedLabels(p.app.ID, p.app.Slug)
	labels[dockerx.LabelDeployment] = p.dep.ID
	labels[dockerx.LabelSeq] = fmt.Sprint(p.dep.Seq)
	p.builtHere = true
	p.logf("Building %s with docker buildx.", p.image)
	err := deploy.RunBuild(ctx, deploy.BuildSpec{ContextDir: ctxDir, Dockerfile: dockerfile, Tag: p.image, Labels: labels, Builder: bld}, p.writeLog)
	if err != nil {
		if ctx.Err() != nil {
			return errCancelled
		}
		p.failTail = p.ring.Lines()
		var ae *apperr.Error
		switch {
		case errors.Is(err, deploy.ErrBuildTimeout):
			return apperr.Unprocessable("build_failed", fmt.Sprintf("The build took longer than %d minutes and was stopped.", int(deploy.BuildTimeout.Minutes())),
				"Make the build faster: use a smaller base image, order the Dockerfile so dependency installs are cached, and leave big files out with .dockerignore.")
		case errors.As(err, &ae):
			return ae
		}
		if deploy.BuildOutOfMemory(p.failTail) {
			return apperr.Unprocessable("build_failed",
				"The build ran out of memory. Builds on this server can use up to "+bld.MemoryLimit()+".",
				"Make the build use less memory, for example by building fewer things at once, or ask the bitploy admin to raise BITPLOY_BUILDER_MEMORY.")
		}
		summary := deploy.BuildFailureSummary(p.failTail)
		msg := "The build failed."
		if summary != "" {
			msg = "The build failed: " + summary
		}
		return apperr.Unprocessable("build_failed", msg,
			"Read log_tail for the failing step, fix the Dockerfile or the code, then deploy again.")
	}
	info, err := s.Docker.InspectImage(ctx, p.image)
	if err != nil {
		return apperr.Unprocessable("build_failed", "The build finished but Docker has no image for it.", "Deploy again. If this repeats, tell the bitploy admin.")
	}
	p.hcCfg = info.Healthcheck
	p.imageExposed = info.ExposedTCP
	if info.SizeBytes > deploy.MaxImageBytes {
		f := deploy.ImageTooLargeFinding(info.SizeBytes)
		return apperr.Unprocessable(f.Code, f.Message, f.Hint)
	}
	if err := s.Q.SetDeploymentBuilt(ctx, db.SetDeploymentBuiltParams{
		Image: nullStr(p.image), ImageSizeBytes: sql.NullInt64{Int64: info.SizeBytes, Valid: true},
		BuiltAt: sql.NullInt64{Int64: s.now(), Valid: true}, ID: p.dep.ID,
	}); err != nil {
		return apperr.Internal(err)
	}
	p.logf("Built %s (%d MB).", p.image, info.SizeBytes>>20)
	return nil
}

// resolveImagePort picks the port of a web app that had none before the build
// (no port setting, no EXPOSE in the Dockerfile): the one TCP port the built
// image declares. It is stored on the deployment, so a rollback starts the
// same port.
func (p *pipeline) resolveImagePort(ctx context.Context) *apperr.Error {
	if !p.portFromImage || p.port != 0 {
		return nil
	}
	port, ae := portFromImageConfig(p.imageExposed, p.image)
	if ae != nil {
		return ae
	}
	p.port = int64(port)
	p.logf("Port %d comes from the image (EXPOSE %d in %s).", port, port, p.image)
	if err := p.s.Q.SetDeploymentPort(ctx, db.SetDeploymentPortParams{Port: portOf(port), ID: p.dep.ID}); err != nil {
		return apperr.Internal(err)
	}
	return nil
}

// portFromImageConfig decides the port from the TCP ports an image declares.
func portFromImageConfig(exposed []int, image string) (int, *apperr.Error) {
	switch {
	case len(exposed) == 0:
		return 0, apperr.Unprocessable("port_unknown", "Neither the Dockerfile nor the image declares a port, so bitploy does not know which port the app listens on.",
			"Set the port with update_app (port), or add `EXPOSE <port>` to the last stage of the Dockerfile. A bot or other app without a web port should be created with type worker.").WithNext("update_app")
	case len(exposed) > 1:
		strs := make([]string, len(exposed))
		for i, e := range exposed {
			strs[i] = strconv.Itoa(e)
		}
		return 0, apperr.Unprocessable("port_ambiguous", fmt.Sprintf("The image %s declares several ports (%s) and bitploy serves only one.", image, strings.Join(strs, ", ")),
			fmt.Sprintf("Choose the port that serves the website with update_app (port=%d, for example) and deploy again, or add a single EXPOSE line to the Dockerfile.", exposed[0])).WithNext("update_app")
	case exposed[0] == deploy.ReservedPort:
		return 0, apperr.Unprocessable("port_reserved", fmt.Sprintf("The image declares port %d, which is reserved for bitploy's own proxy, so an app cannot listen on it.", deploy.ReservedPort),
			"Make the app listen on another port and set it with update_app (port), or add a matching EXPOSE line to the Dockerfile.").WithNext("update_app")
	}
	return exposed[0], nil
}

// ---- starting ----

func (p *pipeline) start(ctx context.Context) *apperr.Error {
	s := p.s
	if app, err := s.Q.GetApp(ctx, p.app.ID); err == nil {
		p.app = app // settings may have changed during the build
	} else {
		return errCancelled // app deleted
	}
	if p.setSize {
		p.app.MemoryMb, p.app.CpuMillis = p.sizeMem, p.sizeCPU
	}
	if p.app.CurrentDeploymentID.Valid && p.app.CurrentDeploymentID.String != p.dep.ID {
		if d, err := s.Q.GetDeployment(ctx, p.app.CurrentDeploymentID.String); err == nil && d.ContainerName.Valid {
			p.prev = &d
			p.prevName = d.ContainerName.String
		}
	}
	// Recreate mode: with a volume, never two writers at once.
	if p.app.VolumeEnabled != 0 && p.prevName != "" {
		if info, err := s.Docker.InspectContainer(ctx, p.prevName); err == nil {
			wasRunning := info.State != nil && (info.State.Running || info.State.Restarting)
			p.logf("Volume attached: stopping the old container before starting the new one.")
			if err := s.Docker.StopContainerGrace(ctx, p.prevName, 10); err != nil {
				return startFailed("The old container could not be stopped: "+dockerx.DescribeError(err), "Try again in a minute.")
			}
			// A failed deploy restarts it only if it was running: a stopped or
			// crash-looping app stays as it was.
			p.oldStopped = wasRunning
		}
	}
	p.newName = dockerx.ContainerName(p.app.Slug, p.dep.Seq)
	p.logf("Starting container %s.", p.newName)
	if ae := s.startContainer(ctx, startPlan{App: p.app, DepID: p.dep.ID, Seq: p.dep.Seq, Image: p.image, Port: p.port}); ae != nil {
		if ctx.Err() != nil {
			return errCancelled
		}
		return ae
	}
	if err := s.Q.SetDeploymentContainer(ctx, db.SetDeploymentContainerParams{
		ContainerName: nullStr(p.newName), Port: portOf(int(p.port)), HasHealthcheck: b2i(p.hasHC), ID: p.dep.ID,
	}); err != nil {
		return apperr.Internal(err)
	}
	return nil
}

// ---- health check ----

func (p *pipeline) healthCheck(ctx context.Context) *apperr.Error {
	s := p.s
	app := p.app
	start := time.Now()

	// Workers: still running after 30 s.
	if app.Type == "worker" {
		p.logf("Worker: waiting %s to see that it keeps running.", workerAliveFor)
		last, ok := deploy.Poll(ctx, workerAliveFor+10*time.Second, time.Second, func(c context.Context) deploy.PollResult {
			if r := p.aliveCheck(c); r.Fail {
				return r
			}
			if time.Since(start) >= workerAliveFor {
				return deploy.PollResult{Done: true}
			}
			return deploy.PollResult{}
		})
		if ctx.Err() != nil {
			return errCancelled
		}
		if !ok {
			return p.unhealthy(last.Detail, "worker")
		}
		return nil
	}

	// Dockerfile HEALTHCHECK replaces the HTTP check.
	if p.hcCfg != nil {
		window := dockerx.HealthWindow(p.hcCfg)
		p.logf("Waiting for the Docker HEALTHCHECK to report healthy (up to %s).", window)
		last, ok := deploy.Poll(ctx, window, time.Second, func(c context.Context) deploy.PollResult {
			info, err := s.Docker.InspectContainer(c, p.newName)
			if err != nil {
				return deploy.PollResult{Detail: "inspect failed"}
			}
			if r := stateProblem(info); r.Fail {
				return r
			}
			if info.State != nil && info.State.Health != nil {
				switch info.State.Health.Status {
				case container.Healthy:
					return deploy.PollResult{Done: true}
				case container.Unhealthy:
					return deploy.PollResult{Fail: true, Detail: "the HEALTHCHECK reported unhealthy"}
				}
			}
			return deploy.PollResult{Detail: "HEALTHCHECK still starting"}
		})
		if ctx.Err() != nil {
			return errCancelled
		}
		if !ok {
			return p.unhealthy(last.Detail, "healthcheck")
		}
		return nil
	}

	// Plain HTTP check through Traefik's internal entrypoint.
	upstream := fmt.Sprintf("http://%s:%d", p.newName, p.port)
	s.setCandidate(app.ID, upstream)
	if err := s.SyncRoute(ctx, app.ID); err != nil {
		return startFailed("bitploy could not write the route for the new container: "+apperr.Redact(err.Error()), "This is a server problem. Tell the bitploy admin.")
	}
	probe := deploy.Prober{
		Client: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		Base:   s.Cfg.TraefikInternal, Host: proxy.CandidateHost(app.Slug), Header: proxy.CandidateHeader, Marker: app.Slug,
	}
	p.logf("Waiting for GET %s to answer below 500 (up to %s).", app.HealthPath, healthTimeout)
	last, ok := deploy.Poll(ctx, healthTimeout, time.Second, func(c context.Context) deploy.PollResult {
		if r := p.aliveCheck(c); r.Fail {
			return r
		}
		res := probe.Probe(c, app.HealthPath)
		switch {
		case res.OK():
			return deploy.PollResult{Done: true}
		case res.Err != nil:
			return deploy.PollResult{Detail: "no answer"}
		case !res.Marked:
			return deploy.PollResult{Detail: "route not loaded"}
		default:
			return deploy.PollResult{Detail: fmt.Sprintf("status %d", res.Status)}
		}
	})
	if ctx.Err() != nil {
		return errCancelled
	}
	if !ok {
		if last.Detail == "route not loaded" {
			return apperr.Unavailable("proxy_unavailable",
				"bitploy's proxy did not pick up the route for the new container within a minute.",
				"This is a server problem, not your app. Deploy again; if it repeats, tell the bitploy admin.")
		}
		return p.unhealthy(last.Detail, "http")
	}
	p.logf("Healthy.")
	return nil
}

func (p *pipeline) aliveCheck(ctx context.Context) deploy.PollResult {
	info, err := p.s.Docker.InspectContainer(ctx, p.newName)
	if err != nil {
		return deploy.PollResult{Detail: "inspect failed"}
	}
	return stateProblem(info)
}

// stateProblem reports a container that exited or keeps restarting.
func stateProblem(info container.InspectResponse) deploy.PollResult {
	st := info.State
	if st == nil {
		return deploy.PollResult{}
	}
	if st.OOMKilled {
		return deploy.PollResult{Fail: true, Detail: "oom"}
	}
	if !st.Running || st.Restarting || info.RestartCount > 0 {
		if st.ExitCode != 0 || !st.Running {
			return deploy.PollResult{Fail: true, Detail: fmt.Sprintf("exited with code %d", st.ExitCode)}
		}
		return deploy.PollResult{Fail: true, Detail: "keeps restarting"}
	}
	return deploy.PollResult{}
}

func (p *pipeline) unhealthy(detail, kind string) *apperr.Error {
	s := p.s
	// Runtime log tail for the error, with a fresh context: the pipeline's may be done.
	lc, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var lines []string
	if entries, err := s.Docker.TailLogs(lc, p.newName, runtimeTailLines, time.Time{}); err == nil {
		for _, e := range entries {
			lines = append(lines, e.Text)
		}
	}
	for _, l := range lines {
		p.writeLog("[runtime] " + l)
	}
	p.failTail = lines

	var msg, hint string
	code, next := "unhealthy", "get_logs"
	switch {
	case detail == "oom":
		code, next = "out_of_memory", "update_app"
		msg = "Your app ran out of memory and was killed."
		hint = fmt.Sprintf("Raise memory_mb with update_app (it has %d MB now; whoami shows your quota) and call deploy with only app=, or make the app use less memory.", p.app.MemoryMb)
	case strings.HasPrefix(detail, "exited") || detail == "keeps restarting":
		code = "app_crashed"
		if kind == "worker" {
			msg = "Your worker stopped before it ran for 30 seconds (" + detail + ")."
		} else {
			msg = "Your app stopped right after starting (" + detail + ")."
		}
		hint = "Read log_tail for the error. A worker or server must keep running in the foreground. If the log shows a missing environment variable, add it with set_env and call deploy with only app= (no files needed)."
	case kind == "healthcheck":
		msg = "The Docker HEALTHCHECK in your Dockerfile never reported healthy (" + detail + ")."
		hint = "Check that the HEALTHCHECK command works inside the container and that the app starts in time."
	default:
		msg = fmt.Sprintf("Your app did not answer GET %s with a status below 500 within %d seconds (%s).", p.app.HealthPath, int(healthTimeout.Seconds()), detail)
		hint = fmt.Sprintf("Make sure the app listens on 0.0.0.0:%d (the PORT variable), starts within a minute and does not fail on %s. Use update_app to set another health_path if needed. If the logs show a different port, call update_app with port=<number> and deploy again.", p.port, p.app.HealthPath)
	}
	if deploy.MentionsLocalhost(lines) && detail != "oom" {
		hint = deploy.LocalhostHint
	}
	return apperr.Unprocessable(code, msg, hint).WithNext(next)
}

// ---- swapping ----

func (p *pipeline) swap(ctx context.Context) *apperr.Error {
	s := p.s
	l := s.appLock(p.app.ID)
	l.Lock()
	defer l.Unlock()
	if ctx.Err() != nil {
		return errCancelled
	}
	if _, err := s.Q.GetApp(ctx, p.app.ID); err != nil {
		return errCancelled // app deleted
	}
	if p.app.Type == "web" {
		s.waitForLiveRoute(ctx, p)
	}
	now := s.now()
	err := s.Store.Tx(ctx, func(q *db.Queries) error {
		if err := q.SupersedeDeployments(ctx, db.SupersedeDeploymentsParams{AppID: p.app.ID, ID: p.dep.ID}); err != nil {
			return err
		}
		if err := q.FinishDeployment(ctx, db.FinishDeploymentParams{State: "running", FinishedAt: sql.NullInt64{Int64: now, Valid: true}, ID: p.dep.ID}); err != nil {
			return err
		}
		if p.dep.CatalogVersion != "" {
			if err := q.SetAppCatalogVersion(ctx, db.SetAppCatalogVersionParams{CatalogVersion: p.dep.CatalogVersion, UpdatedAt: now, ID: p.app.ID}); err != nil {
				return err
			}
		}
		if p.setSize {
			if err := q.SetAppResources(ctx, db.SetAppResourcesParams{MemoryMb: p.sizeMem, CpuMillis: p.sizeCPU, UpdatedAt: now, ID: p.app.ID}); err != nil {
				return err
			}
			if err := syncSingleLayout(ctx, q, p.app.ID); err != nil {
				return err
			}
		}
		return q.SetAppCurrentDeployment(ctx, db.SetAppCurrentDeploymentParams{
			CurrentDeploymentID: nullStr(p.dep.ID), UpdatedAt: now, ID: p.app.ID,
		})
	})
	if err != nil {
		return apperr.Internal(err)
	}
	p.dep.State = "running"
	s.deployRT.mu.Lock()
	s.deployRT.init()
	delete(s.deployRT.restore, p.app.ID)
	s.deployRT.mu.Unlock()
	s.setCandidate(p.app.ID, "")
	if err := s.syncRouteLocked(context.WithoutCancel(ctx), p.app.ID); err != nil {
		s.Log.Error("write route after swap; the reconciler will retry", "app", p.app.Slug, "err", err)
	}
	// Retire the previous container: after a drain delay for web apps.
	if p.prevName != "" && p.prevName != p.newName {
		delay := oldStopDelay
		if p.app.Type == "worker" || p.oldStopped {
			delay = 0
		}
		s.retireContainer(p.prevName, delay)
	}
	p.logf("Live: %s", s.appURL(p.app))
	s.NotifyDeployment(p.dep.ID)
	s.Audit(ctx, Actor{UserID: p.app.UserID, Channel: ChannelSystem, Scope: ScopeDeploy}, "deployment.running", "deployment", p.dep.ID, map[string]any{"app": p.app.Slug, "seq": p.dep.Seq})
	return nil
}

// waitForLiveRoute points the route at the new container and waits (up to
// 10 s) until Traefik serves it, so "running" means the URL works. A timeout
// is only logged: the route file is correct and Traefik will catch up.
func (s *Service) waitForLiveRoute(ctx context.Context, p *pipeline) {
	app, err := s.Q.GetApp(ctx, p.app.ID)
	if err != nil {
		return
	}
	r := s.desiredRoute(ctx, app)
	r.Candidate = ""
	r.Upstream = fmt.Sprintf("http://%s:%d", p.newName, p.port)
	if app.DesiredState != "running" {
		return
	}
	if err := s.Routes.Write(r); err != nil {
		s.Log.Error("write route before swap", "app", app.Slug, "err", err)
		return
	}
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		req, _ := http.NewRequestWithContext(ctx, http.MethodHead, s.Cfg.TraefikInternal+"/", nil)
		req.Host = proxy.LiveHost(app.Slug)
		if resp, err := client.Do(req); err == nil {
			resp.Body.Close()
			if resp.Header.Get(proxy.UpstreamHeader) == r.Upstream {
				return
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	s.Log.Warn("proxy did not confirm the new route within 10 s", "app", app.Slug)
}

// ---- finishing ----

func (p *pipeline) finish(ctx context.Context, ae *apperr.Error) {
	s := p.s
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	defer func() {
		if p.buildDir != "" {
			_ = os.RemoveAll(p.buildDir)
		}
		s.events.flushBuild(p.dep.ID)
		s.NotifyDeployment(p.dep.ID)
	}()

	if ae == nil {
		s.pruneApp(bg, p.app.ID)
		s.metricsMgr().MeasureSoon() // the new image and source count towards storage
		return
	}
	if ae == errCancelled || ae.Code == "cancelled" {
		if ctx.Err() != nil && !s.cancelledByUser(p.dep.ID) {
			return // bitploy is shutting down: the reconciler cleans up after restart
		}
		p.cleanupFailed(bg)
		if who, ok := s.cancelRequester(p.dep.ID); ok {
			// cancel_deployment: the live version stays, the app settles, and it is
			// audited. Not a failure: no error hint, no failure audit row.
			_ = s.Q.FinishCancelledDeployment(bg, db.FinishCancelledDeploymentParams{
				ErrorMessage: nullStr(cancelledMessage), FinishedAt: sql.NullInt64{Int64: s.now(), Valid: true}, ID: p.dep.ID,
			})
			p.logf("Cancelled by the user.")
			s.settleStatusAfterCancel(bg, p.app.ID)
			s.auditCancel(bg, who, p.app.Slug, p.dep.ID, p.dep.Seq, p.dep.State)
			s.metricsMgr().MeasureSoon() // the image of the build is gone
			return
		}
		_ = s.Q.FinishDeployment(bg, db.FinishDeploymentParams{State: "cancelled", FinishedAt: sql.NullInt64{Int64: s.now(), Valid: true}, ID: p.dep.ID})
		p.logf("Cancelled.")
		return
	}

	p.logf("FAILED: %s", ae.Message)
	if ae.Hint != "" {
		p.logf("Hint: %s", ae.Hint)
	}
	tail := p.failTail
	if len(tail) == 0 {
		tail = p.ring.Lines()
	}
	p.cleanupFailed(bg)
	if err := s.Q.FailDeployment(bg, db.FailDeploymentParams{
		ErrorCode: nullStr(ae.Code), ErrorMessage: nullStr(apperr.Redact(ae.Message)), ErrorHint: nullStr(apperr.Redact(ae.Hint)),
		ErrorNext: nullStr(ae.Next), LogTail: nullStr(apperr.Redact(strings.Join(tail, "\n"))),
		FinishedAt: sql.NullInt64{Int64: s.now(), Valid: true}, ID: p.dep.ID,
	}); err != nil {
		s.Log.Error("store deployment failure", "id", p.dep.ID, "err", err)
	}
	s.settleStatusAfterFailure(bg, p.app.ID, apperr.Redact(ae.Message))
	s.Audit(bg, Actor{UserID: p.app.UserID, Channel: ChannelSystem, Scope: ScopeDeploy}, "deployment.failed", "deployment", p.dep.ID,
		map[string]any{"app": p.app.Slug, "seq": p.dep.Seq, "code": ae.Code})
	s.pruneApp(bg, p.app.ID)
	s.metricsMgr().MeasureSoon()
}

// cleanupFailed removes what a failed or cancelled deployment created and
// restores the previous version (candidate route, volume recreate).
func (p *pipeline) cleanupFailed(ctx context.Context) {
	s := p.s
	dk := s.Docker
	l := s.appLock(p.app.ID)
	l.Lock()
	defer l.Unlock()
	s.setCandidate(p.app.ID, "")
	if dk != nil && p.newName != "" {
		_ = dk.RemoveContainer(ctx, p.newName)
	}
	if dk != nil && p.oldStopped && p.prevName != "" {
		p.logf("Restarting the previous version.")
		if err := dk.StartContainer(ctx, p.prevName); err != nil {
			s.Log.Error("restart previous container after failed recreate", "container", p.prevName, "err", err)
		}
	}
	_ = s.syncRouteLocked(ctx, p.app.ID)
	if dk != nil && p.builtHere && p.image != "" {
		_ = dk.RemoveImage(ctx, p.image)
	}
}

// ---- helpers ----

func asApperr(err error, fallbackCode string) *apperr.Error {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return ae
	}
	return apperr.Unprocessable(fallbackCode, "Fetching the source failed: "+apperr.Redact(err.Error()), "Check the source and deploy again.")
}

func isGitHub(repo string) bool {
	u, err := url.Parse(repo)
	return err == nil && strings.EqualFold(u.Hostname(), "github.com")
}

// redactURL drops credentials from a URL before it is logged.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return apperr.Redact(raw)
	}
	u.User = nil
	return u.String()
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}

func shortSHA(s string) string {
	if len(s) > 10 {
		return s[:10]
	}
	return s
}

// settleStatusAfterFailure sets the app status after a deployment failed or
// was interrupted. Queueing moves a new or failed app to "deploying"; without
// this it could stay there. A running app with a live version keeps its status.
func (s *Service) settleStatusAfterFailure(ctx context.Context, appID, msg string) {
	defer s.publishApp(ctx, appID)
	app, err := s.Q.GetApp(ctx, appID)
	if err != nil {
		return
	}
	unsettled := app.Status == "new" || app.Status == "deploying"
	switch {
	case app.DesiredState == "running" && (unsettled || !app.CurrentDeploymentID.Valid):
		_ = s.Q.SetAppStatus(ctx, db.SetAppStatusParams{Status: "failed", StatusMessage: msg, UpdatedAt: s.now(), ID: appID})
	case unsettled && app.CurrentDeploymentID.Valid:
		// Meant to be stopped and a version exists: nothing runs, as before.
		_ = s.Q.SetAppStatus(ctx, db.SetAppStatusParams{Status: "stopped", StatusMessage: "", UpdatedAt: s.now(), ID: appID})
	case unsettled:
		_ = s.Q.SetAppStatus(ctx, db.SetAppStatusParams{Status: "failed", StatusMessage: msg, UpdatedAt: s.now(), ID: appID})
	}
}
