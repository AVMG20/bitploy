package app

// OWNER: deploy agent. The pipeline steps of a compose deployment
// (docs/compose-plan.md, "Phase 2"): validating, building every service, and
// what the app becomes when the deployment goes live.

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/deploy"
	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// validateCompose checks a compose deployment before anything is built:
// the file, each build service's Dockerfile, the volumes and the quota.
func (p *pipeline) validateCompose(ctx context.Context, ch composeChoice, files []string, read func(string) []byte) *apperr.Error {
	s := p.s
	proj := ch.proj
	names := make([]string, len(proj.Services))
	for i, sv := range proj.Services {
		names[i] = sv.Name
	}
	p.logf("Deploying %s: %d %s (%s).", ch.path, len(names), plural(len(names), "service", "services"), strings.Join(names, ", "))
	warns, ae := s.checkComposeServices(p.app, &proj, files, read)
	if ae != nil {
		return ae
	}
	specs := composeServiceSpecs(proj)
	if ae := s.checkServiceHostNames(ctx, p.app, specs); ae != nil {
		return ae
	}
	vols, notes, err := s.composeVolumes(ctx, p.app, specs, proj)
	if err != nil {
		return apperr.Internal(err)
	}
	warns = append(warns, notes...)
	for _, f := range warns {
		p.logf("check %s (%s): %s", f.Code, f.Severity, f.Message)
	}
	if ae := s.composeQuotaErr(ctx, p.app, specs, vols); ae != nil {
		return ae
	}
	p.compose, p.specs, p.vols, p.kind, p.findings = true, specs, vols, deploy.BuildKindCompose, warns
	if pub := publicSpec(specs); pub != nil {
		p.port = pub.Port
	}
	if ae := p.storeSpec(ctx); ae != nil {
		return ae
	}
	warn := sql.NullString{}
	if len(warns) > 0 {
		warn = nullStr(mustJSON(warns))
	}
	if err := s.Q.SetDeploymentValidated(ctx, db.SetDeploymentValidatedParams{
		Port: portOf(int(p.port)), HasHealthcheck: 0, Warnings: warn, BuildKind: p.kind, ID: p.dep.ID,
	}); err != nil {
		return apperr.Internal(err)
	}
	// A deployment of a single-service app got the row of its one service
	// when it was queued; this one has the services of the file instead.
	if err := s.Q.DeleteDeploymentService(ctx, db.DeleteDeploymentServiceParams{DeploymentID: p.dep.ID, Service: singleService}); err != nil {
		return apperr.Internal(err)
	}
	return nil
}

func (p *pipeline) storeSpec(ctx context.Context) *apperr.Error {
	raw := mustJSON(composeSpecDoc{Services: p.specs, Volumes: p.vols})
	if err := p.s.Q.SetDeploymentServicesSpec(ctx, db.SetDeploymentServicesSpecParams{ServicesSpec: raw, ID: p.dep.ID}); err != nil {
		return apperr.Internal(err)
	}
	return nil
}

// useSingleService builds the compose file's one service like an app with
// one Dockerfile (chooseCompose): its context, Dockerfile and port, its
// environment, and its volume at /data.
func (p *pipeline) useSingleService(ctx context.Context, ch composeChoice) {
	svc := ch.single
	p.singleFrom = svc
	p.app = singleFromCompose(p.app, svc)
	p.logf("%s has one service (%s): building it like an app with one Dockerfile (%s).", ch.path, svc.Name, svc.Dockerfile)
	for _, w := range ch.proj.Warnings {
		if w.Code == deploy.CodeComposeIgnored {
			continue
		}
		p.findings = append(p.findings, issueFinding(w))
	}
	shared, _ := p.s.sharedEnv(ctx, p.app)
	p.extraEnv = map[string]string{}
	for k, v := range svc.Environment {
		if _, ok := shared[k]; ok || v == deploy.EnvRef(k) {
			continue // the shared variable wins, or the entry only passes it on
		}
		p.extraEnv[k] = deploy.ExpandEnvRefs(v, lookupIn(shared))
	}
	for _, m := range svc.Mounts {
		if p.app.VolumeEnabled != 0 {
			break
		}
		size := int64(defaultVolumeMB)
		for _, v := range ch.proj.Volumes {
			if v.Name == m.Volume && v.SizeMB > 0 {
				size = v.SizeMB
			}
		}
		p.enableData = size
		p.app.VolumeEnabled, p.app.VolumeMb = 1, size
		p.logf("The %s volume at /data becomes the app's /data volume (%s).", m.Volume, mbText(size))
	}
}

// buildCompose builds the services that build the source, one at a time on
// the isolated builder, and pulls the images of the others. A service that
// runs the same image as the live deployment keeps it.
func (p *pipeline) buildCompose(ctx context.Context) *apperr.Error {
	s := p.s
	bld := s.buildBuilder()
	if err := bld.Ensure(ctx); err != nil {
		if ctx.Err() != nil {
			return errCancelled
		}
		return asApperr(err, "builder_unavailable")
	}
	prevImage := p.liveImages(ctx)
	var primary string
	var total int64
	hasHC := false
	for i := range p.specs {
		sp := &p.specs[i]
		if ctx.Err() != nil {
			return errCancelled
		}
		if ok, err := s.DiskHasRoom(); err == nil && !ok {
			return apperr.Unavailable("disk_full", "The server is almost out of disk space, so new builds are paused.",
				"Tell the bitploy admin; your running apps are not affected. Try again later.")
		}
		img, reused, ae := p.buildService(ctx, bld, i, sp, prevImage[sp.Name])
		if ae != nil {
			if ae == errCancelled {
				return ae
			}
			out := *ae
			out.Message = "Service " + sp.Name + ": " + ae.Message
			return out.WithDetail("service", sp.Name)
		}
		info, err := s.Docker.InspectImage(ctx, img)
		if err != nil {
			return (&apperr.Error{Code: "build_failed", Message: "Service " + sp.Name + ": the build finished but Docker has no image for it.",
				Hint: "Deploy again. If this repeats, tell the bitploy admin."}).WithDetail("service", sp.Name)
		}
		if info.SizeBytes > deploy.MaxImageBytes {
			f := deploy.ImageTooLargeFinding(info.SizeBytes)
			return apperr.Unprocessable(f.Code, "Service "+sp.Name+": "+f.Message, f.Hint).WithDetail("service", sp.Name)
		}
		if sp.Public && sp.Port == 0 {
			port, ae := portFromImageConfig(info.ExposedTCP, img)
			if ae != nil {
				out := *ae
				out.Message = "Service " + sp.Name + ": " + ae.Message
				out.Hint = "Say which port the " + sp.Name + " service listens on in the compose file (ports: [\"8080\"] or expose: [\"8080\"]), then deploy again."
				return out.WithDetail("service", sp.Name).WithNext("validate_compose")
			}
			sp.Port = int64(port)
			p.logf("Service %s: port %d comes from the image (EXPOSE %d).", sp.Name, port, port)
		}
		hc := info.Healthcheck
		if h := sp.Healthcheck.config(); h != nil {
			hc = h
		}
		if sp.Healthcheck.off() {
			hc = nil
		}
		if sp.Public {
			hasHC = hc != nil
		}
		if err := s.Q.UpsertDeploymentService(ctx, db.UpsertDeploymentServiceParams{
			DeploymentID: p.dep.ID, Service: sp.Name, Image: nullStr(img), Port: nullPort(sp.Port), HasHealthcheck: b2i(hc != nil),
		}); err != nil {
			return apperr.Internal(err)
		}
		if primary == "" || sp.Public {
			primary = img
		}
		if !reused {
			total += info.SizeBytes
		}
	}
	p.image = primary
	if pub := publicSpec(p.specs); pub != nil {
		p.port = pub.Port
	}
	if ae := p.storeSpec(ctx); ae != nil { // with the ports the images gave
		return ae
	}
	if err := s.Q.SetDeploymentBuilt(ctx, db.SetDeploymentBuiltParams{
		Image: nullStr(primary), ImageSizeBytes: sql.NullInt64{Int64: total, Valid: true},
		BuiltAt: sql.NullInt64{Int64: s.now(), Valid: true}, ID: p.dep.ID,
	}); err != nil {
		return apperr.Internal(err)
	}
	if err := s.Q.SetDeploymentPort(ctx, db.SetDeploymentPortParams{Port: portOf(int(p.port)), ID: p.dep.ID}); err != nil {
		return apperr.Internal(err)
	}
	p.hasHC = hasHC
	return nil
}

// liveImages are the images the services of the live deployment run, by
// service, for the services that ran a ready-made image: name -> [ref, image].
func (p *pipeline) liveImages(ctx context.Context) map[string][2]string {
	out := map[string][2]string{}
	if !p.app.CurrentDeploymentID.Valid {
		return out
	}
	d, err := p.s.Q.GetDeployment(ctx, p.app.CurrentDeploymentID.String)
	if err != nil || d.ServicesSpec == "" {
		return out
	}
	specs, _, err := parseServicesSpec(d.ServicesSpec)
	if err != nil {
		return out
	}
	rows, err := p.s.Q.ListDeploymentServices(ctx, d.ID)
	if err != nil {
		return out
	}
	img := map[string]string{}
	for _, r := range rows {
		img[r.Service] = r.Image.String
	}
	for _, sp := range specs {
		if sp.Kind == "image" && img[sp.Name] != "" {
			out[sp.Name] = [2]string{sp.Image, img[sp.Name]}
		}
	}
	return out
}

// buildService builds one service and returns its image. An image service
// whose reference the live deployment already pulled reuses that image
// (like compose's pull_policy missing); otherwise the image is pulled by a
// one-line build (FROM <image>) on the builder, which has the same timeout,
// cancel and memory limit as any build.
func (p *pipeline) buildService(ctx context.Context, bld *deploy.Builder, i int, sp *serviceSpec, live [2]string) (string, bool, *apperr.Error) {
	s := p.s
	n := len(p.specs)
	labels := s.Docker.ManagedLabels(p.app.ID, p.app.Slug)
	labels[dockerx.LabelDeployment] = p.dep.ID
	labels[dockerx.LabelSeq] = fmt.Sprint(p.dep.Seq)
	labels[dockerx.LabelService] = sp.Name
	tag := dockerx.ServiceImageName(p.app.Slug, sp.Name, p.dep.Seq)
	if sp.Kind == "image" {
		if live[0] == sp.Image && live[1] != "" {
			if _, err := s.Docker.InspectImage(ctx, live[1]); err == nil {
				p.logf("[%d/%d] Service %s: %s is already on the server (%s).", i+1, n, sp.Name, sp.Image, live[1])
				return live[1], true, nil
			}
		}
		p.logf("[%d/%d] Service %s: pulling %s.", i+1, n, sp.Name, sp.Image)
		dir := p.buildDir + "-image-" + sp.Name
		_ = os.RemoveAll(dir)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return "", false, apperr.Internal(err)
		}
		defer os.RemoveAll(dir)
		dockerfile := filepath.Join(dir, "Dockerfile")
		if err := os.WriteFile(dockerfile, []byte("FROM "+sp.Image+"\n"), 0o640); err != nil {
			return "", false, apperr.Internal(err)
		}
		p.images = append(p.images, tag)
		if err := deploy.RunBuild(ctx, deploy.BuildSpec{ContextDir: dir, Dockerfile: dockerfile, Tag: tag, Labels: labels, Builder: bld}, p.writeLog); err != nil {
			ae := p.buildFailed(ctx, err, bld)
			if ae != errCancelled && ae.Code == "build_failed" && !strings.Contains(ae.Message, "longer than") {
				return "", false, apperr.Unprocessable("image_pull_failed", "The image "+sp.Image+" could not be pulled.",
					"Check the image name and tag in the compose file (image:), and that the image is public: bitploy cannot log in to registries. Then deploy again.").WithNext("validate_compose")
			}
			return "", false, ae
		}
		return tag, false, nil
	}
	p.logf("[%d/%d] Service %s: building %s.", i+1, n, sp.Name, sp.DockerfilePath)
	ctxDir, dockerfile, perr := deploy.ResolveBuildPaths(p.srcRoot, sp.ContextDir, sp.DockerfilePath)
	if perr != nil {
		return "", false, perr
	}
	spec := deploy.BuildSpec{ContextDir: ctxDir, Dockerfile: dockerfile, Tag: tag, Labels: labels, Builder: bld,
		Args: maps.Clone(sp.BuildArgs), Target: sp.Target}
	cleanup, ae := p.applyBuildVars(ctx, &spec, sp.Name, false)
	defer cleanup()
	if ae != nil {
		return "", false, ae
	}
	p.images = append(p.images, tag)
	if err := deploy.RunBuild(ctx, spec, p.writeLog); err != nil {
		return "", false, p.buildFailed(ctx, err, bld)
	}
	return tag, false, nil
}

// overlayCompose makes p.app what it becomes when the compose deployment
// goes live: several services, its type, port and health path from the
// public service, and the sums of the limits.
func (p *pipeline) overlayCompose() {
	p.wasSingle = !isMulti(p.app)
	p.app.ServiceLayout = layoutMulti
	p.app.Type, p.app.Port, p.app.HealthPath = "worker", sql.NullInt64{}, "/"
	if pub := publicSpec(p.specs); pub != nil {
		p.app.Type, p.app.Port, p.app.HealthPath = "web", nullPort(pub.Port), pub.HealthPath
	}
	mem, cpu, vol := composeTotals(p.specs, p.vols)
	p.app.MemoryMb, p.app.CpuMillis, p.app.VolumeMb = mem, cpu, vol
	p.app.VolumeEnabled = b2i(len(p.vols) > 0)
}

// writeCompose stores the services and volumes of a compose deployment
// that goes live, in the swap transaction.
func (p *pipeline) writeCompose(ctx context.Context, q *db.Queries, now int64) error {
	id := p.app.ID
	fresh, err := q.GetApp(ctx, id)
	if err != nil {
		return err
	}
	if err := q.DeleteAppServices(ctx, id); err != nil {
		return err
	}
	for i, sp := range p.specs {
		port := sp.Port
		if r := p.run(sp.Name); r != nil && r.port > 0 && sp.Public {
			port = r.port
		}
		row := db.InsertAppServiceParams{
			AppID: id, Name: sp.Name, Kind: sp.Kind, Image: sp.Image, ContextDir: sp.ContextDir, DockerfilePath: sp.DockerfilePath,
			Public: b2i(sp.Public), Port: nullPort(port), HealthPath: sp.HealthPath, MemoryMb: sp.MemoryMB, CpuMillis: sp.CPUMillis,
			RunUser: sp.User, WorkingDir: sp.WorkingDir, Position: int64(i), CreatedAt: now, UpdatedAt: now,
		}
		if sp.Command != nil {
			row.Command = mustJSON(sp.Command)
		}
		if sp.Entrypoint != nil {
			row.Entrypoint = mustJSON(sp.Entrypoint)
		}
		if sp.Healthcheck != nil {
			row.Healthcheck = mustJSON(sp.Healthcheck)
		}
		if len(sp.DependsOn) > 0 {
			row.DependsOn = mustJSON(sp.DependsOn)
		}
		if len(sp.Environment) > 0 {
			row.Environment = mustJSON(sp.Environment)
		}
		if err := q.InsertAppService(ctx, row); err != nil {
			return err
		}
	}
	// Volumes: app_volumes.volume_name is unique, so a row whose Docker volume
	// moves to a volume of another name goes first. A volume the file no
	// longer has is kept, unmounted, with its data.
	want := map[string]bool{}
	wantDocker := map[string]bool{}
	for _, v := range p.vols {
		want[v.Name], wantDocker[v.VolumeName] = true, true
	}
	rows, err := q.ListAppVolumes(ctx, id)
	if err != nil {
		return err
	}
	for _, r := range rows {
		switch {
		case want[r.Name]:
		case wantDocker[r.VolumeName]:
			if err := q.DeleteAppVolumeRow(ctx, db.DeleteAppVolumeRowParams{AppID: id, Name: r.Name}); err != nil {
				return err
			}
		default:
			if err := q.DetachAppVolume(ctx, db.DetachAppVolumeParams{AppID: id, Name: r.Name}); err != nil {
				return err
			}
		}
	}
	for i, v := range p.vols {
		if err := q.UpsertAppVolume(ctx, db.UpsertAppVolumeParams{
			AppID: id, Name: v.Name, VolumeName: v.VolumeName, SizeMb: v.SizeMB, Service: v.Service, MountPath: v.MountPath,
			Position: int64(i), CreatedAt: now,
		}); err != nil {
			return err
		}
	}
	if err := q.SetAppServiceLayout(ctx, db.SetAppServiceLayoutParams{ServiceLayout: layoutMulti, UpdatedAt: now, ID: id}); err != nil {
		return err
	}
	if fresh.BuildMode == deploy.BuildModeAuto {
		// The app was detected as a compose app: from now on it is one.
		if err := q.SetAppBuildMode(ctx, db.SetAppBuildModeParams{BuildMode: deploy.BuildModeCompose, HomePage: fresh.HomePage, OutputDir: fresh.OutputDir, UpdatedAt: now, ID: id}); err != nil {
			return err
		}
	}
	if err := q.SetMultiAppTotals(ctx, id); err != nil {
		return err
	}
	return q.SetMultiAppPublic(ctx, db.SetMultiAppPublicParams{UpdatedAt: now, AppID: id})
}

// writeSingleFromCompose stores what the build of a compose file's one
// service took from it, so later deploys build the same way without reading
// the file: the context and Dockerfile, the port, the /data volume and the
// variables the app did not have yet.
func (p *pipeline) writeSingleFromCompose(ctx context.Context, q *db.Queries, now int64) error {
	fresh, err := q.GetApp(ctx, p.app.ID)
	if err != nil {
		return err
	}
	svc := p.singleFrom
	set := db.UpdateAppSettingsParams{
		Slug: fresh.Slug, Name: fresh.Name, Type: fresh.Type, Port: fresh.Port, HealthPath: fresh.HealthPath,
		DockerfilePath: svc.Dockerfile, ContextDir: svc.Context, VolumeEnabled: fresh.VolumeEnabled, VolumeMb: fresh.VolumeMb,
		MemoryMb: fresh.MemoryMb, CpuMillis: fresh.CpuMillis, BaseDomain: fresh.BaseDomain, UpdatedAt: now, ID: fresh.ID,
	}
	if !set.Port.Valid && p.port > 0 && fresh.Type == "web" {
		set.Port = portOf(int(p.port))
	}
	if svc.HealthPath != "" && svc.HealthPath != "/" && fresh.HealthPath == "/" {
		set.HealthPath = svc.HealthPath
	}
	if p.enableData > 0 && fresh.VolumeEnabled == 0 {
		set.VolumeEnabled, set.VolumeMb = 1, p.enableData
	}
	if p.setSize {
		set.MemoryMb, set.CpuMillis = p.sizeMem, p.sizeCPU
	}
	if err := q.UpdateAppSettings(ctx, set); err != nil {
		return err
	}
	if err := syncSingleLayout(ctx, q, fresh.ID); err != nil {
		return err
	}
	have := map[string]bool{}
	vars, err := q.ListEnvVars(ctx, fresh.ID)
	if err != nil {
		return err
	}
	for _, v := range vars {
		have[v.Name] = true
	}
	for k, v := range p.extraEnv {
		if have[k] {
			continue
		}
		if err := q.UpsertEnvVar(ctx, db.UpsertEnvVarParams{
			AppID: fresh.ID, Service: "", Name: k, ValueEnc: p.s.Crypto.Seal([]byte(v), envAAD(fresh.ID, k)), UpdatedAt: now,
		}); err != nil {
			return err
		}
	}
	return nil
}

// checkServiceHostNames refuses services whose name on the project network
// (<alias>-<service>) another app of the project already has, as alias or
// as the name of one of its services.
func (s *Service) checkServiceHostNames(ctx context.Context, app db.App, specs []serviceSpec) *apperr.Error {
	if app.ProjectID == "" || app.Alias == "" {
		return nil
	}
	held, err := projectHostNames(ctx, s.Q, app.ProjectID, app.ID)
	if err != nil {
		return apperr.Internal(err)
	}
	names := make([]string, len(specs))
	for i, sp := range specs {
		names[i] = sp.Name
	}
	c, clash := firstClash(held, app.Alias, names)
	if !clash {
		return nil
	}
	ae := c.err(app.Alias)
	return apperr.Unprocessable("network_name_taken", ae.Message,
		"Rename the service "+c.service+" in the compose file, or give this app another short name with update_app alias=..., then deploy again.").
		WithNext("update_app").WithDetail("service", c.service).WithDetail("name", c.name).WithDetail("app", c.holder.app.Slug)
}
