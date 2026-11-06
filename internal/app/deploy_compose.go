package app

// OWNER: deploy agent. Deploying a compose file (docs/compose-plan.md,
// "Phase 2"): which deployments use it, the checks before anything is
// queued or built, the volumes and the quota, and validate_compose.
//
// One Dockerfile with SQLite in /data stays the recommended shape of an app.
// A compose file is for a project that already has one, or really needs a
// database server or Redis next to the app.

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/deploy"
	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// composeChoice is what a deployment does with the compose file of its source.
type composeChoice struct {
	path string // the compose file, from the source root; "" when the deployment does not read one
	proj deploy.ComposeProject
	// multi: deploy every service of the file (service layout multi).
	multi bool
	// single: build mode auto, first deploy, and the file has one service
	// that builds the source. It is built like an app with one Dockerfile,
	// with the context, Dockerfile, port, environment and /data volume of
	// that service.
	single *deploy.ComposeService
}

// usesCompose reports whether every deployment of the app runs its compose
// file: build mode compose, or an app that already has several services.
func usesCompose(a db.App) bool {
	return isMulti(a) || a.BuildMode == deploy.BuildModeCompose
}

// composeSpecDoc is deployments.services_spec.
type composeSpecDoc struct {
	Services []serviceSpec `json:"services"`
	Volumes  []volumeSpec  `json:"volumes"`
}

func parseServicesSpec(raw string) ([]serviceSpec, []volumeSpec, error) {
	var doc composeSpecDoc
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, nil, err
	}
	return doc.Services, doc.Volumes, nil
}

// chooseCompose decides whether a deployment of app from these files runs
// the compose file (docs/decisions.md, "Compose apps"):
//
//   - build mode compose, or an app with several services: always. A source
//     without the compose file is an error.
//   - build mode auto on the first deploy (nothing ever went live): when the
//     file has two or more services, or one service that runs an image. One
//     service that builds the source and fits the app's settings
//     (singleMappable) is built like a Dockerfile app (single).
//     A file with one service and problems is left out (the Dockerfile is
//     built); with several services its problems are errors.
//   - anything else: never. The Dockerfile checks warn (compose_ignored) when
//     the file has several services.
func (s *Service) chooseCompose(ctx context.Context, app db.App, files []string, read func(string) []byte) (composeChoice, *apperr.Error) {
	var ch composeChoice
	forced := usesCompose(app)
	auto := app.BuildMode == deploy.BuildModeAuto && !app.CurrentDeploymentID.Valid
	if !forced && !auto {
		return ch, nil
	}
	file := deploy.FindComposeFile(files, app.ContextDir, app.ComposePath)
	if file == "" {
		if forced {
			return ch, composeFileMissing(app)
		}
		return ch, nil
	}
	content := read(file)
	if !forced && deploy.ComposeServiceCount(content) == 0 {
		return ch, nil
	}
	env, ae := s.sharedEnv(ctx, app)
	if ae != nil {
		return ch, ae
	}
	proj := deploy.ParseCompose(deploy.ComposeInput{Content: content, Path: file, Env: env, Read: read})
	ch.path, ch.proj = file, proj
	if forced {
		if e := proj.FirstError(); e != nil {
			return ch, e
		}
		ch.multi = true
		return ch, nil
	}
	if !proj.OK() {
		if deploy.ComposeServiceCount(content) <= 1 {
			return composeChoice{}, nil
		}
		e := proj.FirstError()
		e.Hint = strings.TrimSpace(e.Hint + " To build only the Dockerfile and leave the compose file out, call update_app with build_mode=\"dockerfile\".")
		return ch, e
	}
	if len(proj.Services) == 1 && singleMappable(proj.Services[0]) {
		svc := proj.Services[0]
		ch.single = &svc
		return ch, nil
	}
	ch.multi = true
	return ch, nil
}

// singleMappable reports whether a compose file's one service fits an app
// with one Dockerfile: it builds the source, and has nothing the app's
// settings cannot hold (build args or target, command, entrypoint, user,
// working_dir, a healthcheck, a volume anywhere but /data). Anything else is
// deployed as a compose app, so no setting is dropped without a word.
func singleMappable(svc deploy.ComposeService) bool {
	if svc.Kind != "build" || len(svc.Args) > 0 || svc.Target != "" || svc.Command != nil || svc.Entrypoint != nil ||
		svc.User != "" || svc.WorkingDir != "" || svc.Healthcheck != nil || len(svc.Mounts) > 1 {
		return false
	}
	for _, m := range svc.Mounts {
		if path.Clean(m.Target) != dockerx.DataMount {
			return false
		}
	}
	return true
}

func composeFileMissing(app db.App) *apperr.Error {
	msg := "The app deploys a compose file, but the source has no compose.yaml (nor compose.yml, docker-compose.yaml or docker-compose.yml)."
	if app.ComposePath != "" {
		msg = "The app deploys the compose file " + app.ComposePath + ", but the source has no such file."
	}
	hint := "Add the compose file to the code, or set compose_path with update_app when it has another name or folder."
	if !isMulti(app) {
		hint += " To build only the Dockerfile instead, call update_app with build_mode=\"dockerfile\"."
	}
	return apperr.Unprocessable(deploy.CodeComposeFileMissing, msg, hint).WithNext("update_app")
}

// sharedEnv decrypts the app's shared variables: the values ${VAR} in the
// compose file can take.
func (s *Service) sharedEnv(ctx context.Context, app db.App) (map[string]string, *apperr.Error) {
	out := map[string]string{}
	if app.ID == "" {
		return out, nil
	}
	rows, err := s.Q.ListServiceEnvVars(ctx, db.ListServiceEnvVarsParams{AppID: app.ID, Service: ""})
	if err != nil {
		return nil, apperr.Internal(err)
	}
	for _, r := range rows {
		if r.Service != "" {
			continue
		}
		v, err := s.Crypto.Open(r.ValueEnc, envAADFor(app.ID, "", r.Name))
		if err != nil {
			return nil, apperr.Unprocessable("env_unreadable", "The environment variable "+r.Name+" cannot be decrypted.",
				"Set it again with set_env. (This happens when the server's secret key changed.)").WithNext("set_env")
		}
		out[r.Name] = string(v)
	}
	return out, nil
}

// issueFinding is a compose warning as a deployment warning.
func issueFinding(i deploy.ComposeIssue) deploy.Finding {
	return deploy.Finding{Code: i.Code, Severity: "warning", Message: i.Message, Hint: i.Hint}
}

// checkComposeServices runs the Dockerfile checks of every service built
// from the source, as for an app with one Dockerfile, and fills in the port of
// a public service from its Dockerfile's EXPOSE. It returns the warnings of
// the file and of the checks; an error names the service.
func (s *Service) checkComposeServices(app db.App, proj *deploy.ComposeProject, files []string, read func(string) []byte) ([]deploy.Finding, *apperr.Error) {
	var out []deploy.Finding
	for _, w := range proj.Warnings {
		out = append(out, issueFinding(w))
	}
	for i := range proj.Services {
		svc := &proj.Services[i]
		if svc.Kind != "build" {
			continue
		}
		if read(svc.Dockerfile) == nil {
			return nil, (deploy.ComposeIssue{
				Code: deploy.CodeComposeSourceMissing, Service: svc.Name, Key: "build",
				Message: fmt.Sprintf("The %s service is built from %s, which is not in the source.", svc.Name, svc.Dockerfile),
				Hint:    "Send all the code (deploy_files with every file, a zip upload or a git repository), point build.context and build.dockerfile at the right place, or run a ready-made image with image: instead. A compose file on its own can only run image: services.",
			}).Err()
		}
		eff := app
		eff.ContextDir, eff.DockerfilePath = svc.Context, svc.Dockerfile
		eff.Type, eff.Port = "worker", portOf(0)
		if svc.Public {
			eff.Type, eff.Port = "web", portOf(svc.Port)
		}
		res, ctxFiles := s.runChecks(eff, files, read)
		if cerr := checkError(res, ctxFiles); cerr != nil {
			e := *apperr.As(cerr)
			e.Message = "Service " + svc.Name + ": " + e.Message
			e.WithDetail("service", svc.Name).WithNext("validate_compose")
			return nil, &e
		}
		if svc.Public && svc.Port == 0 && res.Port > 0 {
			svc.Port = res.Port
		}
		for _, f := range res.Findings {
			if f.Severity == "error" || f.Code == deploy.CodeComposeIgnored || (!svc.Public && f.Code == deploy.CodePortFromImage) {
				continue
			}
			f.Message = "Service " + svc.Name + ": " + f.Message
			out = append(out, f)
		}
	}
	return out, nil
}

// composeServiceSpecs turns the services of a compose file into the specs
// a deployment runs (without their volumes; see composeVolumes).
func composeServiceSpecs(proj deploy.ComposeProject) []serviceSpec {
	out := make([]serviceSpec, 0, len(proj.Services))
	for _, c := range proj.Services {
		sp := serviceSpec{
			Name: c.Name, Kind: c.Kind, Image: c.Image, ContextDir: c.Context, DockerfilePath: c.Dockerfile,
			BuildArgs: c.Args, Target: c.Target, Public: c.Public, Port: int64(c.Port), HealthPath: c.HealthPath,
			MemoryMB: c.MemoryMB, CPUMillis: c.CPUMillis, Command: c.Command, Entrypoint: c.Entrypoint,
			Environment: c.Environment, User: c.User, WorkingDir: c.WorkingDir,
		}
		if sp.HealthPath == "" {
			sp.HealthPath = "/"
		}
		if c.Healthcheck != nil {
			h := healthcheckSpec(*c.Healthcheck)
			sp.Healthcheck = &h
		}
		for _, d := range c.DependsOn {
			sp.DependsOn = append(sp.DependsOn, dependency{Service: d.Service, Condition: d.Condition})
		}
		out = append(out, sp)
	}
	return out
}

// composeVolumes decides the volumes of a compose deployment and mounts
// them into specs:
//
//   - a volume the app already has (same name) keeps its Docker volume, so
//     its data;
//   - the /data volume of an app with one service goes to the volume the file
//     mounts at /data (its size is at least the old one). When the file mounts
//     nothing at /data it stays at /data of the public service, so the data
//     the app had is still there. Without a public service it is kept but not
//     mounted (a warning says so);
//   - a new volume is named bp-<slug>_<volume>.
//
// Volumes the app has and the file no longer mounts are kept (detached at
// swap): their data stays until the app is deleted.
func (s *Service) composeVolumes(ctx context.Context, app db.App, specs []serviceSpec, proj deploy.ComposeProject) ([]volumeSpec, []deploy.Finding, error) {
	var notes []deploy.Finding
	existing := map[string]db.AppVolume{}
	if app.ID != "" {
		rows, err := s.Q.ListAppVolumes(ctx, app.ID)
		if err != nil {
			return nil, nil, err
		}
		for _, r := range rows {
			existing[r.Name] = r
		}
	}
	vols := make([]volumeSpec, 0, len(proj.Volumes)+1)
	inherit := ""
	if app.ID != "" && !isMulti(app) && app.VolumeEnabled != 0 {
		inherit = appVolumeName(app)
	}
	taken := map[string]bool{}
	if inherit != "" {
		// The file's volume at /data, the public service's first.
		pick := -1
		for i, v := range proj.Volumes {
			if v.MountPath == dockerx.DataMount && (pick < 0 || isPublic(specs, v.Service)) {
				pick = i
			}
		}
		if pick >= 0 {
			taken[proj.Volumes[pick].Name] = true
		}
		for i, v := range proj.Volumes {
			vs := volumeSpec{Name: v.Name, SizeMB: v.SizeMB, Service: v.Service, MountPath: v.MountPath}
			if i == pick {
				vs.VolumeName = inherit
				if app.VolumeMb > vs.SizeMB {
					vs.SizeMB = app.VolumeMb
				}
				notes = append(notes, deploy.Finding{Code: "data_volume_kept", Severity: "info",
					Message: fmt.Sprintf("The app's /data volume, with its files, becomes the %s volume of the %s service.", v.Name, v.Service)})
			}
			vols = append(vols, vs)
		}
		if pick < 0 {
			pub := publicSpec(specs)
			name := uniqueVolumeName(singleVolume, proj.Volumes)
			if pub != nil && !mountsAt(proj, pub.Name, dockerx.DataMount) {
				vols = append(vols, volumeSpec{Name: name, VolumeName: inherit, SizeMB: app.VolumeMb, Service: pub.Name, MountPath: dockerx.DataMount})
				notes = append(notes, deploy.Finding{Code: "data_volume_kept", Severity: "info",
					Message: fmt.Sprintf("The app's /data volume, with its files, stays mounted at /data of the %s service.", pub.Name)})
			} else {
				vols = append(vols, volumeSpec{Name: name, VolumeName: inherit, SizeMB: app.VolumeMb})
				notes = append(notes, deploy.Finding{Code: "data_volume_unmounted", Severity: "warning",
					Message: "The app's /data volume is kept with its files, but no service of the compose file mounts it.",
					Hint:    fmt.Sprintf("Mount it where a service keeps its files: add a volume named %s to the compose file and mount it (like - %s:/data), then deploy again.", name, name)})
			}
		}
	} else {
		for _, v := range proj.Volumes {
			vs := volumeSpec{Name: v.Name, SizeMB: v.SizeMB, Service: v.Service, MountPath: v.MountPath}
			if r, ok := existing[v.Name]; ok && isMulti(app) {
				vs.VolumeName = r.VolumeName
			}
			vols = append(vols, vs)
		}
	}
	for i := range vols {
		if vols[i].VolumeName != "" {
			continue
		}
		slug := app.Slug
		if slug == "" {
			slug = "app"
		}
		name := dockerx.ServiceVolumeName(slug, vols[i].Name)
		for n := 2; app.ID != "" && n < 100; n++ {
			c, err := s.Q.AppVolumeNameExists(ctx, name)
			if err != nil {
				return nil, nil, err
			}
			if c == 0 {
				break
			}
			name = fmt.Sprintf("%s-%d", dockerx.ServiceVolumeName(slug, vols[i].Name), n)
		}
		vols[i].VolumeName = name
	}
	for i := range specs {
		specs[i].Mounts = nil
		for _, v := range vols {
			if v.Service == specs[i].Name && v.MountPath != "" {
				specs[i].Mounts = append(specs[i].Mounts, v)
			}
		}
	}
	return vols, notes, nil
}

func isPublic(specs []serviceSpec, name string) bool {
	for _, sp := range specs {
		if sp.Name == name {
			return sp.Public
		}
	}
	return false
}

func mountsAt(proj deploy.ComposeProject, service, target string) bool {
	for _, sv := range proj.Services {
		if sv.Name != service {
			continue
		}
		for _, m := range sv.Mounts {
			if path.Clean(m.Target) == target {
				return true
			}
		}
	}
	return false
}

func uniqueVolumeName(want string, vols []deploy.ComposeVolume) string {
	used := map[string]bool{}
	for _, v := range vols {
		used[v.Name] = true
	}
	name := want
	for n := 2; used[name]; n++ {
		name = fmt.Sprintf("%s-%d", want, n)
	}
	return name
}

// ---- quota ----

// ComposeQuota is how a compose file fits the owner's allowance. The app's
// own current use is not counted: the deployment replaces it.
type ComposeQuota struct {
	MemoryMBLeft  int64  `json:"memory_mb_left" doc:"Memory left in the allowance, not counting this app"`
	CPUMillisLeft int64  `json:"cpu_millis_left" doc:"CPU left in the allowance (1000 = one core), not counting this app"`
	StorageMBLeft *int64 `json:"storage_mb_left" doc:"Storage left, not counting this app's volumes; null when there is no storage limit"`
	Fits          bool   `json:"fits"`
	Message       string `json:"message" doc:"Like: This needs 1.2 GB of memory of the 1.5 GB you have left."`
}

// composeRoom compares what a compose deployment needs with what the owner
// has left. excludeID is the app the deployment replaces ("" for none).
func (s *Service) composeRoom(ctx context.Context, u db.User, excludeID string, mem, cpu, volMB int64) (ComposeQuota, error) {
	sum, err := s.Q.SumAppResourcesByUser(ctx, db.SumAppResourcesByUserParams{UserID: u.ID, ID: excludeID})
	if err != nil {
		return ComposeQuota{}, err
	}
	q := ComposeQuota{MemoryMBLeft: max(u.QuotaMemoryMb-sum.MemoryMb, 0), CPUMillisLeft: max(u.QuotaCpuMillis-sum.CpuMillis, 0), Fits: true}
	var parts []string
	if u.Overallocate != 0 {
		// Memory and CPU are not checked for this user; storage still is.
		parts = append(parts, fmt.Sprintf("%s of memory and %s CPU (not checked: you may overallocate)", mbText(mem), cpuText(cpu)))
	} else {
		parts = append(parts, fmt.Sprintf("%s of memory of the %s you have left", mbText(mem), mbText(q.MemoryMBLeft)))
		parts = append(parts, fmt.Sprintf("%s CPU of the %s left", cpuText(cpu), cpuText(q.CPUMillisLeft)))
		if mem > q.MemoryMBLeft || cpu > q.CPUMillisLeft {
			q.Fits = false
		}
	}
	if u.QuotaVolumeMb > 0 {
		taken, err := s.storageTaken(ctx, u.ID, excludeID)
		if err != nil {
			return ComposeQuota{}, err
		}
		left := max(u.QuotaVolumeMb-taken, 0)
		q.StorageMBLeft = &left
		if volMB > 0 {
			parts = append(parts, fmt.Sprintf("%s of storage for volumes of the %s left", mbText(volMB), mbText(left)))
		}
		if volMB > left {
			q.Fits = false
		}
	}
	q.Message = "This needs " + joinAnd(parts) + "."
	return q, nil
}

// composeQuotaErr checks a compose deployment against the allowance before
// anything is built, naming what each service takes.
func (s *Service) composeQuotaErr(ctx context.Context, app db.App, specs []serviceSpec, vols []volumeSpec) *apperr.Error {
	u, err := s.activeUser(ctx, app.UserID)
	if err != nil {
		return apperr.As(err)
	}
	mem, cpu, vol := composeTotals(specs, vols)
	q, err := s.composeRoom(ctx, u, app.ID, mem, cpu, vol)
	if err != nil {
		return apperr.Internal(err)
	}
	if !q.Fits {
		code := "memory_quota_exceeded"
		switch {
		case mem <= q.MemoryMBLeft && cpu > q.CPUMillisLeft:
			code = "cpu_quota_exceeded"
		case mem <= q.MemoryMBLeft && cpu <= q.CPUMillisLeft:
			code = "storage_quota_exceeded"
		}
		return apperr.Forbidden(code, q.Message+" Per service: "+perServiceText(specs)+".",
			"Give the services less in the compose file (deploy.resources.limits.memory and cpus; a database or Redis often runs in 128 MB), make volumes smaller (x-bitploy: {size_mb: ...} on the volume), stop or delete another app, or ask a bitploy admin for a bigger quota. Then deploy again.").
			WithNext("validate_compose")
	}
	extra := mem
	if appConsumes(app) {
		extra = mem - app.MemoryMb
	}
	if extra > 0 && u.Overallocate == 0 {
		if err := s.checkServerRoom(ctx, extra); err != nil {
			return apperr.As(err)
		}
	}
	return nil
}

// composeTotals sums memory and CPU over the services and size over the volumes.
func composeTotals(specs []serviceSpec, vols []volumeSpec) (mem, cpu, vol int64) {
	for _, sp := range specs {
		mem += sp.MemoryMB
		cpu += sp.CPUMillis
	}
	for _, v := range vols {
		vol += v.SizeMB
	}
	return mem, cpu, vol
}

func perServiceText(specs []serviceSpec) string {
	parts := make([]string, len(specs))
	for i, sp := range specs {
		parts[i] = fmt.Sprintf("%s %d MB and %s CPU", sp.Name, sp.MemoryMB, cpuText(sp.CPUMillis))
	}
	return strings.Join(parts, ", ")
}

// mbText is a size for people: 512 MB, 1.5 GB.
func mbText(mb int64) string {
	if mb >= 1024 {
		return fmt.Sprintf("%s GB", strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", float64(mb)/1024), "0"), "."))
	}
	return fmt.Sprintf("%d MB", mb)
}

func joinAnd(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

// ---- checks before a deployment is queued ----

// checkSource is the early check of a source (deploy_files, an upload): the
// compose file when the deployment runs it, else the build mode and the
// Dockerfile. files are paths from the source root.
func (s *Service) checkSource(ctx context.Context, row db.App, files []string, read func(string) []byte) (deploy.CheckResult, *apperr.Error) {
	ch, ae := s.chooseCompose(ctx, row, files, read)
	if ae != nil {
		return deploy.CheckResult{}, ae
	}
	if ch.multi {
		warns, ae := s.checkComposeServices(row, &ch.proj, files, read)
		if ae != nil {
			return deploy.CheckResult{}, ae
		}
		res := deploy.CheckResult{OK: true, Findings: warns}
		if pub := ch.proj.Public(); pub != nil {
			res.Port = pub.Port
		}
		return res, nil
	}
	eff := row
	if ch.single != nil {
		eff = singleFromCompose(row, ch.single)
	}
	effApp, plan, allPaths, read2, perr := planBuild(eff, files, read)
	if perr != nil {
		return deploy.CheckResult{}, composeHintOnMissing(perr, files, row)
	}
	res, ctxFiles := s.runChecks(effApp, allPaths, read2)
	res = templateFindings(plan, res)
	res.Findings = s.dropSingleServiceComposeWarning(ctx, row, res.Findings, files, read)
	if cerr := checkError(res, ctxFiles); cerr != nil {
		return res, composeHintOnMissing(apperr.As(cerr), files, row)
	}
	return res, nil
}

// singleFromCompose is the app as the build of a compose file's one service
// sees it: the service's context and Dockerfile, its port when the app has none.
func singleFromCompose(app db.App, svc *deploy.ComposeService) db.App {
	app.ContextDir, app.DockerfilePath = svc.Context, svc.Dockerfile
	app.BuildMode = deploy.BuildModeDockerfile
	if !app.Port.Valid && svc.Port > 0 {
		app.Port = portOf(svc.Port)
	}
	return app
}

// composeHintOnMissing adds to "there is no Dockerfile" that the project has
// a compose file, which bitploy can deploy.
func composeHintOnMissing(ae *apperr.Error, files []string, app db.App) *apperr.Error {
	if ae == nil || usesCompose(app) {
		return ae
	}
	switch ae.Code {
	case deploy.CodeDockerfileMissing, "source_dockerfile_not_found", deploy.CodeNothingToBuild:
	default:
		return ae
	}
	f := deploy.FindComposeFile(files, app.ContextDir, app.ComposePath)
	if f == "" {
		return ae
	}
	out := *ae
	out.Hint = strings.TrimSpace(out.Hint + " The source has " + f + ": if the app needs the services in it, deploy that instead with update_app build_mode=\"compose\", then deploy again. For a simple app, one Dockerfile with SQLite in /data is easier to run.")
	return &out
}

// dropSingleServiceComposeWarning leaves out compose_ignored when the
// compose file has at most one service: nothing else would have run.
func (s *Service) dropSingleServiceComposeWarning(ctx context.Context, app db.App, findings []deploy.Finding, files []string, read func(string) []byte) []deploy.Finding {
	idx := -1
	for i, f := range findings {
		if f.Code == deploy.CodeComposeIgnored {
			idx = i
		}
	}
	if idx < 0 {
		return findings
	}
	file := deploy.FindComposeFile(files, app.ContextDir, app.ComposePath)
	if file == "" || deploy.ComposeServiceCount(read(file)) > 1 {
		return findings
	}
	return append(findings[:idx:idx], findings[idx+1:]...)
}

// ---- validate_compose ----

// ValidateComposeInput is the body of POST /api/v1/compose/validate.
type ValidateComposeInput struct {
	Compose string `json:"compose" doc:"The content of the compose file (compose.yaml)" maxLength:"262144"`
	// App is optional: its shared variables fill in ${VAR}, and its current
	// use is left out of the quota check (a deployment replaces it).
	App string `json:"app,omitempty" doc:"Optional app id or slug: its shared variables fill in ${VAR}, and its own use is left out of the quota check. Needs the deploy scope"`
}

// ComposePreview is what bitploy would run from a compose file.
type ComposePreview struct {
	OK             bool                    `json:"ok" doc:"No errors: the file can be deployed"`
	Services       []ComposePreviewService `json:"services"`
	Volumes        []ComposePreviewVolume  `json:"volumes"`
	MemoryMBTotal  int64                   `json:"memory_mb_total"`
	CPUMillisTotal int64                   `json:"cpu_millis_total"`
	VolumeMBTotal  int64                   `json:"volume_mb_total"`
	Quota          ComposeQuota            `json:"quota"`
	Warnings       []deploy.ComposeIssue   `json:"warnings"`
	Errors         []deploy.ComposeIssue   `json:"errors"`
}

// ComposePreviewService is one service of a ComposePreview.
type ComposePreviewService struct {
	Name       string               `json:"name"`
	Kind       string               `json:"kind" enum:"build,image" doc:"build: built from the source; image: a ready-made image"`
	Image      string               `json:"image,omitempty" doc:"The image an image service runs"`
	Build      *ComposePreviewBuild `json:"build" doc:"How a build service is built; null for an image service"`
	Public     bool                 `json:"public" doc:"The service the app's address points at"`
	Port       *int64               `json:"port" doc:"The port of the public service; null when it comes from the image"`
	HealthPath string               `json:"health_path,omitempty"`
	MemoryMB   int64                `json:"memory_mb"`
	CPUMillis  int64                `json:"cpu_millis"`
	DependsOn  []string             `json:"depends_on"`
	Volumes    []string             `json:"volumes" doc:"The names of the volumes it mounts"`
}

// ComposePreviewBuild is the build of a service, paths from the project root.
type ComposePreviewBuild struct {
	Context    string `json:"context"`
	Dockerfile string `json:"dockerfile"`
	Target     string `json:"target,omitempty"`
}

// ComposePreviewVolume is one volume of a ComposePreview.
type ComposePreviewVolume struct {
	Name      string `json:"name"`
	Service   string `json:"service"`
	MountPath string `json:"mount_path"`
	SizeMB    int64  `json:"size_mb"`
}

// ValidateCompose checks a compose file without a project: what bitploy
// would run, what it ignores and refuses, and whether it fits the quota.
// Problems with the file are in the answer, never an error: only a bad app
// reference or a missing account is.
func (s *Service) ValidateCompose(ctx context.Context, a Actor, in ValidateComposeInput) (ComposePreview, error) {
	var row db.App
	ownerID := a.UserID
	// With an app, ${VAR} is filled with its real variables, and the filled-in
	// image or target comes back in the answer: owner with deploy scope only, as
	// for a deploy (a read-only token or an admin must not read values this way).
	if strings.TrimSpace(in.App) != "" {
		r, err := s.LoadApp(ctx, a, in.App, true)
		if err != nil {
			return ComposePreview{}, err
		}
		row, ownerID = r, r.UserID
	}
	env, ae := s.sharedEnv(ctx, row)
	if ae != nil {
		return ComposePreview{}, ae
	}
	proj := deploy.ParseCompose(deploy.ComposeInput{Content: []byte(in.Compose), Path: "compose.yaml", Env: env})
	out := ComposePreview{Services: []ComposePreviewService{}, Volumes: []ComposePreviewVolume{},
		Warnings: append([]deploy.ComposeIssue{}, proj.Warnings...), Errors: append([]deploy.ComposeIssue{}, proj.Errors...)}
	specs := composeServiceSpecs(proj)
	for _, c := range proj.Services {
		v := ComposePreviewService{Name: c.Name, Kind: c.Kind, Public: c.Public, MemoryMB: c.MemoryMB, CPUMillis: c.CPUMillis,
			DependsOn: []string{}, Volumes: []string{}}
		if c.Kind == "image" {
			v.Image = c.Image
		} else {
			v.Build = &ComposePreviewBuild{Context: c.Context, Dockerfile: c.Dockerfile, Target: c.Target}
		}
		if c.Public {
			v.HealthPath = c.HealthPath
			if c.Port > 0 {
				p := int64(c.Port)
				v.Port = &p
			}
		}
		for _, d := range c.DependsOn {
			v.DependsOn = append(v.DependsOn, d.Service)
		}
		for _, m := range c.Mounts {
			v.Volumes = append(v.Volumes, m.Volume)
		}
		out.Services = append(out.Services, v)
	}
	vols := make([]volumeSpec, 0, len(proj.Volumes))
	if row.ID != "" && proj.OK() {
		if vs, notes, err := s.composeVolumes(ctx, row, specs, proj); err == nil {
			vols = vs
			for _, n := range notes {
				if n.Severity == "warning" {
					out.Warnings = append(out.Warnings, deploy.ComposeIssue{Code: n.Code, Message: n.Message, Hint: n.Hint})
				}
			}
		}
	} else {
		for _, v := range proj.Volumes {
			vols = append(vols, volumeSpec{Name: v.Name, SizeMB: v.SizeMB, Service: v.Service, MountPath: v.MountPath})
		}
	}
	for _, v := range vols {
		out.Volumes = append(out.Volumes, ComposePreviewVolume{Name: v.Name, Service: v.Service, MountPath: v.MountPath, SizeMB: v.SizeMB})
	}
	out.MemoryMBTotal, out.CPUMillisTotal, out.VolumeMBTotal = composeTotals(specs, vols)
	if u, err := s.Q.GetUser(ctx, ownerID); err == nil {
		q, err := s.composeRoom(ctx, u, row.ID, out.MemoryMBTotal, out.CPUMillisTotal, out.VolumeMBTotal)
		if err != nil {
			return ComposePreview{}, apperr.Internal(err)
		}
		out.Quota = q
		if !q.Fits && len(proj.Services) > 0 {
			out.Errors = append(out.Errors, deploy.ComposeIssue{Code: "quota_exceeded", Message: q.Message + " That is more than your account has left. Per service: " + perServiceText(specs) + ".",
				Hint: "Give the services less memory or CPU in the compose file (deploy.resources.limits; a database or Redis often runs in 128 MB), make volumes smaller (x-bitploy: {size_mb: ...}), stop or delete another app, or ask a bitploy admin for a bigger quota."})
		}
	}
	out.OK = len(out.Errors) == 0
	return out, nil
}
