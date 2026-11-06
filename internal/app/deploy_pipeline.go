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

	// runs are the services of the deployment in start order; one for a
	// single-layout app. newName, prevName, oldStopped, port, hasHC and hcCfg
	// above describe the primary run: the public service, else the first.
	runs    []*serviceRun
	primary *serviceRun
	failed  *serviceRun // the run a health check failed on

	// compose: the deployment runs the services of a compose file (or rolls
	// back to such a deployment). specs and vols are its services and volumes
	// (deployments.services_spec); the app gets them when it goes live.
	compose   bool
	specs     []serviceSpec
	vols      []volumeSpec
	wasSingle bool     // the app had one service before this deployment
	images    []string // images this deployment built, removed when it fails
	// singleFrom: the compose file's one service, built like a Dockerfile app
	// (auto mode, first deploy). Its environment (extraEnv) becomes the app's
	// variables and its volume the /data volume when the deployment goes live.
	singleFrom *deploy.ComposeService
	extraEnv   map[string]string
	enableData int64 // MB: /data is turned on when the deployment goes live

	// buildSecrets are the values of the build's secret variables, masked
	// in the build log (applyBuildVars).
	buildSecrets []string
}

// serviceRun is one service of a deployment on its way to running.
type serviceRun struct {
	spec       serviceSpec
	image      string
	port       int64
	hasHC      bool
	hcCfg      *container.HealthConfig
	hash       string // serviceConfigHash; multi-service apps only
	name       string // the container that runs the service for this deployment
	created    bool   // this deployment created name, so a failure removes it
	kept       bool   // name is the previous deployment's container, unchanged and still running
	healthy    bool   // its HEALTHCHECK already reported healthy
	prevName   string // the previous deployment's container of this service
	oldStopped bool   // recreate mode stopped prevName for us
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
	line = apperr.Redact(maskSecrets(line, p.buildSecrets))
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
		if p.dep.ServicesSpec != "" {
			specs, vols, err := parseServicesSpec(p.dep.ServicesSpec)
			if err != nil {
				return apperr.Internal(err)
			}
			p.compose, p.specs, p.vols = true, specs, vols
			p.logf("Rolling back to the services of deployment %s.", p.dep.SourceRef)
		} else {
			p.logf("Rolling back to the image of deployment %s (%s).", p.dep.SourceRef, p.image)
		}
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
		var ae *apperr.Error
		if p.compose {
			ae = p.buildCompose(ctx)
		} else {
			ae = p.build(ctx)
		}
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
	if ae := p.waitForReferences(ctx); ae != nil {
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
	// The compose file, when the deployment runs it (chooseCompose).
	ch, ae := p.s.chooseCompose(ctx, p.app, files, read)
	if ae != nil {
		return ae
	}
	if ch.multi {
		return p.validateCompose(ctx, ch, files, read)
	}
	if ch.single != nil {
		p.useSingleService(ctx, ch)
	}
	// The build mode decides between the app's Dockerfile and a static site.
	effApp, plan, files, read, perr := planBuild(p.app, p.s.runsOwnDockerfile(ctx, p.app), files, read)
	if perr != nil {
		return composeHintOnMissing(perr, files, p.app)
	}
	if plan.Why != "" {
		p.logf("%s", plan.Why)
	}
	if len(plan.Inject) > 0 {
		if err := writeInjected(p.srcRoot, plan); err != nil {
			what := "to build the app"
			if plan.Kind == deploy.BuildKindStatic {
				what = "to serve them as a website"
			}
			return apperr.Unprocessable("source_invalid", "bitploy could not prepare the files "+what+".",
				"Check that .dockerignore in the code is a plain file, then deploy again.").WithCause(err)
		}
		p.app = effApp // the build uses the generated Dockerfile at the source root
	}
	p.kind = plan.Kind
	res, ctxFiles := p.s.runChecks(p.app, files, read)
	res = templateFindings(plan, res)
	res.Findings = p.s.dropSingleServiceComposeWarning(ctx, p.app, res.Findings, files, read)
	res.Findings = append(res.Findings, p.findings...)
	for _, f := range res.Findings {
		p.logf("check %s (%s): %s", f.Code, f.Severity, f.Message)
	}
	if cerr := checkError(res, ctxFiles); cerr != nil {
		return composeHintOnMissing(apperr.As(cerr), files, p.app)
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
	if p.compose {
		if p.dep.SourceKind == "rollback" { // a build checked it before building
			return p.s.composeQuotaErr(ctx, fresh, p.specs, p.vols)
		}
		return nil
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
	if p.enableData > 0 && fresh.VolumeEnabled == 0 {
		// The compose file's one service keeps files in /data.
		u, err := p.s.activeUser(ctx, fresh.UserID)
		if err != nil {
			return apperr.As(err)
		}
		if err := p.s.checkVolumeQuota(ctx, u, fresh.ID, p.enableData); err != nil {
			return apperr.As(err)
		}
	}
	return nil
}

// quotaAtSwap checks the allowance again right before the deployment goes
// live. The checks before the build and in settleResources count only the
// apps that are live at that moment, so two deployments built at the same
// time could each fit alone and both go live over the quota.
func (p *pipeline) quotaAtSwap(ctx context.Context, fresh db.App) *apperr.Error {
	switch {
	case p.compose:
		return p.s.composeQuotaErr(ctx, fresh, p.specs, p.vols)
	case deploy.IsStaticKind(p.kind):
		return nil
	case p.setSize: // a static site that turns into an app starts to count
		return apperr.As(p.s.checkConsumeQuota(ctx, fresh, p.sizeMem, p.sizeCPU, p.sizeMem-staticMemoryMB))
	}
	return apperr.As(p.s.checkStartQuota(ctx, fresh))
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
		return apperr.Unprocessable("context_dir_invalid", "The build context folder is outside the source.", "Use update_app to set context_dir to a folder inside the source.")
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
	spec := deploy.BuildSpec{ContextDir: ctxDir, Dockerfile: dockerfile, Tag: p.image, Labels: labels, Builder: bld}
	cleanup, ae := p.applyBuildVars(ctx, &spec, singleService, p.kind != deploy.BuildKindDockerfile)
	defer cleanup()
	if ae != nil {
		return ae
	}
	p.logf("Building %s with docker buildx.", p.image)
	err := deploy.RunBuild(ctx, spec, p.writeLog)
	if err != nil {
		return p.buildFailed(ctx, err, bld)
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

// buildFailed is the error of a failed build.
func (p *pipeline) buildFailed(ctx context.Context, err error, bld *deploy.Builder) *apperr.Error {
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
			"Make the build use less memory (for example, build fewer things at once), or ask the bitploy admin to raise BITPLOY_BUILDER_MEMORY.")
	}
	summary := deploy.BuildFailureSummary(p.failTail)
	msg := "The build failed."
	if summary != "" {
		msg = "The build failed: " + summary
	}
	hint := deploy.BuildFailureHint(p.failTail)
	if hint == "" {
		hint = "Read log_tail for the failing step, fix the Dockerfile or the code, then deploy again."
	}
	return apperr.Unprocessable("build_failed", msg, hint)
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
	if p.compose {
		p.overlayCompose()
	} else if p.enableData > 0 && p.app.VolumeEnabled == 0 {
		p.app.VolumeEnabled, p.app.VolumeMb = 1, p.enableData
	}
	if p.app.CurrentDeploymentID.Valid && p.app.CurrentDeploymentID.String != p.dep.ID {
		if d, err := s.Q.GetDeployment(ctx, p.app.CurrentDeploymentID.String); err == nil && (d.ContainerName.Valid || isMulti(p.app)) {
			p.prev = &d
			p.prevName = d.ContainerName.String
		}
	}
	if ae := p.planRuns(ctx); ae != nil {
		return ae
	}
	if ae := p.startRuns(ctx); ae != nil {
		return ae
	}
	if err := s.Q.SetDeploymentContainer(ctx, db.SetDeploymentContainerParams{
		ContainerName: nullStr(p.newName), Port: portOf(int(p.port)), HasHealthcheck: b2i(p.hasHC), ID: p.dep.ID,
	}); err != nil {
		return apperr.Internal(err)
	}
	return nil
}

// planRuns decides what each service of the deployment runs: the image just
// built (a single-service app), or the images recorded per service (a
// rollback of an app with several services).
func (p *pipeline) planRuns(ctx context.Context) *apperr.Error {
	s := p.s
	if !isMulti(p.app) {
		r := &serviceRun{spec: singleSpec(p.app), image: p.image, port: p.port, hasHC: p.hasHC, hcCfg: p.hcCfg, prevName: p.prevName}
		p.runs, p.primary = []*serviceRun{r}, r
		return nil
	}
	specs := p.specs
	if !p.compose {
		var err error
		if specs, _, err = s.appSpecs(ctx, p.app); err != nil {
			return apperr.Internal(err)
		}
	}
	order, err := startOrder(specs)
	if err != nil {
		return apperr.Unprocessable("services_invalid", "The services of this app cannot be started: "+err.Error()+".",
			"Fix depends_on in the app's services and deploy again.").WithNext("get_app")
	}
	rows, err := s.Q.ListDeploymentServices(ctx, p.dep.ID)
	if err != nil {
		return apperr.Internal(err)
	}
	mine := map[string]db.DeploymentService{}
	for _, r := range rows {
		mine[r.Service] = r
	}
	prev := map[string]string{}
	if p.prev != nil {
		if prows, err := s.Q.ListDeploymentServices(ctx, p.prev.ID); err == nil {
			for _, r := range prows {
				prev[r.Service] = r.ContainerName.String
			}
		}
	}
	p.runs = nil
	for _, sp := range order {
		row, ok := mine[sp.Name]
		if !ok || !row.Image.Valid {
			return apperr.Conflict("image_missing", "This deployment has no image for the "+sp.Name+" service.",
				"Roll back to a deployment that ran every service of the app. get_app lists the services.").WithNext("get_app")
		}
		r := &serviceRun{spec: sp, image: row.Image.String, port: row.Port.Int64, prevName: prev[sp.Name]}
		if r.port == 0 {
			r.port = sp.Port
		}
		if info, err := s.Docker.InspectImage(ctx, r.image); err == nil {
			r.hcCfg = info.Healthcheck
		} else {
			return apperr.Conflict("image_missing", "The image "+r.image+" of the "+sp.Name+" service is gone from the server.",
				"Deploy the app again.").WithNext("get_app")
		}
		if hc := sp.Healthcheck.config(); hc != nil {
			r.hcCfg = hc
		}
		if sp.Healthcheck.off() {
			r.hcCfg = nil
		}
		r.hasHC = r.hcCfg != nil
		p.runs = append(p.runs, r)
		if sp.Public && p.primary == nil {
			p.primary = r
		}
	}
	if p.primary == nil && len(p.runs) > 0 {
		p.primary = p.runs[0]
	}
	if p.wasSingle && p.prev != nil && p.prev.ContainerName.Valid {
		// The container of the app's one service gives way to the service that
		// takes over its /data volume (stopped first: one writer at a time), or
		// else the public one.
		heir := p.primary
		for _, r := range p.runs {
			for _, m := range r.spec.Mounts {
				if m.VolumeName == appVolumeName(p.app) {
					heir = r
				}
			}
		}
		if heir != nil {
			heir.prevName = p.prev.ContainerName.String
		}
	}
	if p.primary != nil {
		p.port, p.hasHC, p.hcCfg, p.prevName = p.primary.port, p.primary.hasHC, p.primary.hcCfg, p.primary.prevName
	}
	return nil
}

// startRuns starts the services in dependency order. This is the seam for
// deploying a list of services: planRuns fills p.runs, startRuns brings them up.
func (p *pipeline) startRuns(ctx context.Context) *apperr.Error {
	s := p.s
	multi := isMulti(p.app)
	var prevHash map[string]string
	if multi && p.prev != nil {
		prevHash = map[string]string{}
		if prows, err := s.Q.ListDeploymentServices(ctx, p.prev.ID); err == nil {
			for _, r := range prows {
				prevHash[r.Service] = r.ConfigHash
			}
		}
	}
	for _, r := range p.runs {
		if ae := p.waitForDependencies(ctx, r); ae != nil {
			return ae
		}
		plan := startPlan{App: p.app, DepID: p.dep.ID, Seq: p.dep.Seq, Image: r.image, Port: r.port, ExtraEnv: p.extraEnv}
		if multi {
			sp := r.spec
			plan.Service = &sp
			plan.ExtraEnv = nil
		}
		rs, ae := s.runSpec(ctx, plan)
		if ae != nil {
			return p.serviceErr(r, ae)
		}
		if multi {
			if info, err := s.Docker.InspectImage(ctx, r.image); err == nil {
				id := info.Fingerprint
				if id == "" {
					id = info.ID
				}
				r.hash = serviceConfigHash(id, rs)
			}
			if r.hash != "" && r.prevName != "" && prevHash[r.spec.Name] == r.hash && p.running(ctx, r.prevName) {
				r.name, r.kept = r.prevName, true
				p.logf("Service %s is unchanged: keeping container %s.", r.spec.Name, r.name)
				if ae := p.recordRun(ctx, r); ae != nil {
					return ae
				}
				continue
			}
		}
		// Recreate mode: with a volume, never two writers at once.
		if len(r.spec.Mounts) > 0 && r.prevName != "" {
			if info, err := s.Docker.InspectContainer(ctx, r.prevName); err == nil {
				wasRunning := info.State != nil && (info.State.Running || info.State.Restarting)
				if multi {
					p.logf("Service %s has a volume: stopping its old container before starting the new one.", r.spec.Name)
				} else {
					p.logf("Saved files (/data) are on: stopping the old container before starting the new one.")
				}
				if err := s.Docker.StopContainerGrace(ctx, r.prevName, 10); err != nil {
					return p.serviceErr(r, startFailed("The old container could not be stopped: "+dockerx.DescribeError(err), "Try again in a minute."))
				}
				// A failed deploy restarts it only if it was running: a stopped or
				// crash-looping app stays as it was.
				r.oldStopped = wasRunning
			}
		}
		r.name, r.created = rs.Name, true
		if r == p.primary {
			p.newName, p.oldStopped = r.name, r.oldStopped
		}
		p.logf("Starting container %s.", r.name)
		if ae := s.startRunSpec(ctx, plan, rs); ae != nil {
			if ctx.Err() != nil {
				return errCancelled
			}
			return p.serviceErr(r, ae)
		}
		if ae := p.recordRun(ctx, r); ae != nil {
			return ae
		}
	}
	if p.primary != nil {
		p.newName, p.oldStopped = p.primary.name, p.primary.oldStopped
	}
	return nil
}

// recordRun stores the container of a service of a multi-service app.
// (A single-service deployment is its deployments row, see finish.)
func (p *pipeline) recordRun(ctx context.Context, r *serviceRun) *apperr.Error {
	if !isMulti(p.app) {
		return nil
	}
	if err := p.s.Q.UpsertDeploymentService(ctx, db.UpsertDeploymentServiceParams{
		DeploymentID: p.dep.ID, Service: r.spec.Name, Image: nullStr(r.image), ContainerName: nullStr(r.name),
		Port: nullPort(r.port), HasHealthcheck: b2i(r.hasHC), ConfigHash: r.hash,
	}); err != nil {
		return apperr.Internal(err)
	}
	return nil
}

func (p *pipeline) running(ctx context.Context, name string) bool {
	info, err := p.s.Docker.InspectContainer(ctx, name)
	return err == nil && info.State != nil && info.State.Running && !info.State.Restarting
}

// serviceErr names the service in an error of a multi-service app.
func (p *pipeline) serviceErr(r *serviceRun, ae *apperr.Error) *apperr.Error {
	if !isMulti(p.app) || ae == nil || ae == errCancelled {
		return ae
	}
	out := *ae
	out.Message = "Service " + r.spec.Name + ": " + ae.Message
	out.Details = make(map[string]any, len(ae.Details)+1)
	for k, v := range ae.Details {
		out.Details[k] = v
	}
	out.Details["service"] = r.spec.Name
	return &out
}

// errorService is the service an error of a multi-service deployment is
// about (details.service), or "".
func errorService(ae *apperr.Error) string {
	if ae == nil {
		return ""
	}
	name, _ := ae.Details["service"].(string)
	return name
}

// waitForDependencies waits for the services r depends on with condition
// service_healthy and a HEALTHCHECK to report healthy. Without a HEALTHCHECK
// the condition is the same as service_started: the service was started
// before r, as startOrder guarantees.
func (p *pipeline) waitForDependencies(ctx context.Context, r *serviceRun) *apperr.Error {
	for _, d := range r.spec.DependsOn {
		if d.Condition != condHealthy {
			continue
		}
		dep := p.run(d.Service)
		if dep == nil || dep.hcCfg == nil || dep.healthy {
			continue
		}
		p.logf("Waiting for service %s to report healthy before starting %s.", dep.spec.Name, r.spec.Name)
		if ae := p.waitHealthy(ctx, dep); ae != nil {
			return ae
		}
	}
	return nil
}

func (p *pipeline) run(service string) *serviceRun {
	for _, r := range p.runs {
		if r.spec.Name == service {
			return r
		}
	}
	return nil
}

// waitHealthy polls a run's Docker HEALTHCHECK until it reports healthy.
func (p *pipeline) waitHealthy(ctx context.Context, r *serviceRun) *apperr.Error {
	window := dockerx.HealthWindow(r.hcCfg)
	last, ok := deploy.Poll(ctx, window, time.Second, func(c context.Context) deploy.PollResult {
		info, err := p.s.Docker.InspectContainer(c, r.name)
		if err != nil {
			return deploy.PollResult{Detail: "inspect failed"}
		}
		if res := stateProblem(info); res.Fail {
			return res
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
		p.failed = r
		return p.unhealthy(last.Detail, "healthcheck")
	}
	r.healthy = true
	return nil
}

// ---- health check ----

func (p *pipeline) healthCheck(ctx context.Context) *apperr.Error {
	if isMulti(p.app) {
		// The other services first: each HEALTHCHECK must report healthy.
		for _, r := range p.runs {
			if r == p.primary || r.hcCfg == nil || r.healthy {
				continue
			}
			p.logf("Waiting for the HEALTHCHECK of service %s to report healthy (up to %s).", r.spec.Name, dockerx.HealthWindow(r.hcCfg))
			if ae := p.waitHealthy(ctx, r); ae != nil {
				return ae
			}
		}
	}
	if ae := p.checkPrimary(ctx); ae != nil {
		return ae
	}
	if len(p.runs) > 1 {
		if r := p.aliveCheck(ctx); r.Fail {
			return p.unhealthy(r.Detail, "worker")
		}
	}
	return nil
}

// checkPrimary is the health check of the primary service: a worker must
// keep running for 30 s, a HEALTHCHECK must report healthy, else GET
// health_path through the candidate route must answer below 500.
func (p *pipeline) checkPrimary(ctx context.Context) *apperr.Error {
	s := p.s
	app := p.app
	start := time.Now()

	// Workers: still running after 30 s. A service-template app (a database)
	// has a HEALTHCHECK, which says more: it waits for that below.
	if app.Type == "worker" && !(app.Template != "" && p.hcCfg != nil) {
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
			if r := p.aliveCheck(c); r.Fail {
				return r
			}
			info, err := s.Docker.InspectContainer(c, p.newName)
			if err != nil {
				return deploy.PollResult{Detail: "inspect failed"}
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

// aliveCheck reports a container of the deployment that exited or keeps
// restarting, and remembers its service in p.failed.
func (p *pipeline) aliveCheck(ctx context.Context) deploy.PollResult {
	names := []*serviceRun{p.primary}
	if len(p.runs) > 1 {
		names = p.runs
	}
	for _, r := range names {
		name := p.newName
		if r != nil {
			name = r.name
		}
		info, err := p.s.Docker.InspectContainer(ctx, name)
		if err != nil {
			return deploy.PollResult{Detail: "inspect failed"}
		}
		if res := stateProblem(info); res.Fail {
			p.failed = r
			return res
		}
	}
	return deploy.PollResult{}
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
	logsOf := p.newName
	if p.failed != nil && p.failed.name != "" {
		logsOf = p.failed.name
	}
	if entries, err := s.Docker.TailLogs(lc, logsOf, runtimeTailLines, time.Time{}); err == nil {
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
		if isMulti(p.app) && p.failed != nil {
			code, next = "out_of_memory", "validate_compose"
			hint = fmt.Sprintf("Give the %s service more memory in the compose file (deploy.resources.limits.memory; it has %d MB now, whoami shows your quota) and deploy again, or make it use less memory.", p.failed.spec.Name, p.failed.spec.MemoryMB)
		}
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
		hint = fmt.Sprintf("Make sure the app listens on 0.0.0.0:%d (the PORT variable), starts within a minute and does not fail on %s. To check another path, set health_path with update_app. If the logs show a different port, call update_app with port=<number> and deploy again.", p.port, p.app.HealthPath)
	}
	if deploy.MentionsLocalhost(lines) && detail != "oom" {
		hint = deploy.LocalhostHint
	}
	if isMulti(p.app) && p.failed != nil {
		msg = "Service " + p.failed.spec.Name + ": " + msg
		if detail != "oom" {
			hint += " Read the service's logs with get_logs service=" + p.failed.spec.Name + "."
		}
	}
	out := apperr.Unprocessable(code, msg, hint).WithNext(next)
	if isMulti(p.app) && p.failed != nil {
		out.WithDetail("service", p.failed.spec.Name)
	}
	return out
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
	fresh, err := s.Q.GetApp(ctx, p.app.ID)
	if err != nil {
		return errCancelled // app deleted
	}
	if ae := p.quotaAtSwap(ctx, fresh); ae != nil {
		return ae
	}
	if p.app.Type == "web" {
		s.waitForLiveRoute(ctx, p)
	}
	// Again under the lock, so two deployments that each fit alone cannot both
	// go live over the allowance.
	s.deployRT.quotaMu.Lock()
	defer s.deployRT.quotaMu.Unlock()
	if fresh, err = s.Q.GetApp(ctx, p.app.ID); err != nil {
		return errCancelled
	}
	if ae := p.quotaAtSwap(ctx, fresh); ae != nil {
		return ae
	}
	now := s.now()
	err = s.Store.Tx(ctx, func(q *db.Queries) error {
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
		if p.compose {
			if err := p.writeCompose(ctx, q, now); err != nil {
				return err
			}
		} else if p.singleFrom != nil {
			if err := p.writeSingleFromCompose(ctx, q, now); err != nil {
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
	// A compose file may have given the app its volumes.
	s.defaultBackups(context.WithoutCancel(ctx), p.app.ID)
	s.deployRT.mu.Lock()
	s.deployRT.init()
	delete(s.deployRT.restore, p.app.ID)
	s.deployRT.mu.Unlock()
	s.setCandidate(p.app.ID, "")
	if err := s.syncRouteLocked(context.WithoutCancel(ctx), p.app.ID); err != nil {
		s.Log.Error("write route after swap; the reconciler will retry", "app", p.app.Slug, "err", err)
	}
	// Retire the previous containers: after a drain delay for web apps.
	keep := map[string]bool{}
	for _, r := range p.runs {
		keep[r.name] = true
	}
	for _, r := range p.runs {
		if r.prevName != "" && !keep[r.prevName] {
			delay := oldStopDelay
			if p.app.Type == "worker" || r.oldStopped {
				delay = 0
			}
			s.retireContainer(r.prevName, delay)
			keep[r.prevName] = true
		}
	}
	if isMulti(p.app) && p.prev != nil {
		// Services the previous deployment had and this one does not.
		if prows, err := s.Q.ListDeploymentServices(context.WithoutCancel(ctx), p.prev.ID); err == nil {
			for _, r := range prows {
				if r.ContainerName.Valid && !keep[r.ContainerName.String] && p.run(r.Service) == nil {
					s.retireContainer(r.ContainerName.String, 0)
				}
			}
		}
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
		if !p.compose {
			_ = s.Q.SyncSingleDeploymentService(bg, p.dep.ID) // no-op for multi-service apps
		}
		s.events.flushBuild(p.dep.ID)
		s.NotifyDeployment(p.dep.ID)
	}()

	if ae == nil {
		s.runAfterLive(bg, p.dep.ID)
		s.pruneApp(bg, p.app.ID)
		s.metricsMgr().MeasureSoon() // the new image and source count towards storage
		return
	}
	if ae == errCancelled || ae.Code == "cancelled" {
		if ctx.Err() != nil && !s.cancelledByUser(p.dep.ID) {
			// bitploy is shutting down: the reconciler cleans up after restart,
			// and decides on the restarts of connected apps (reconcileRestarts).
			return
		}
	}
	s.forgetAfterLive(bg, p.dep.ID)
	if ae == errCancelled || ae.Code == "cancelled" {
		p.cleanupFailed(bg)
		if who, ok := s.cancelRequester(p.dep.ID); ok {
			// cancel_deployment: the live version stays, the app settles, and it is
			// audited. Not a failure: no error hint, no failure audit row.
			_ = s.Q.FinishCancelledDeployment(bg, db.FinishCancelledDeploymentParams{
				ErrorMessage: nullStr(cancelledMessage(who)), FinishedAt: sql.NullInt64{Int64: s.now(), Valid: true}, ID: p.dep.ID,
			})
			p.logf("%s", cancelledMessage(who))
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
		ErrorNext: nullStr(ae.Next), ErrorService: errorService(ae), LogTail: nullStr(apperr.Redact(strings.Join(tail, "\n"))),
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
	if dk != nil {
		for _, r := range p.runs {
			if r.created && r.name != "" {
				_ = dk.RemoveContainer(ctx, r.name)
			}
		}
		restarted := false
		for _, r := range p.runs {
			if !r.oldStopped || r.prevName == "" {
				continue
			}
			if !restarted {
				p.logf("Restarting the previous version.")
				restarted = true
			}
			if err := dk.StartContainer(ctx, r.prevName); err != nil {
				s.Log.Error("restart previous container after failed recreate", "container", r.prevName, "err", err)
			}
		}
	}
	_ = s.syncRouteLocked(ctx, p.app.ID)
	if dk != nil && p.builtHere && p.image != "" && !p.compose {
		_ = dk.RemoveImage(ctx, p.image)
	}
	if dk != nil {
		for _, img := range p.images {
			_ = dk.RemoveImage(ctx, img)
		}
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
