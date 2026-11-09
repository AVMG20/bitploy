package app

// OWNER: deploy agent. Starting app containers, env, routes. Shared by the
// pipeline, restart/start, rename and the reconciler.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/deploy"
	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/proxy"
	"github.com/AVMG20/bitploy/internal/store/db"
)

type startPlan struct {
	App   db.App
	DepID string
	Seq   int64
	Image string
	Port  int64
	// Service is the service to start. nil: the only service of a
	// single-layout app, built from App.
	Service *serviceSpec
	// ExtraEnv has the lowest precedence: the environment of the compose
	// file a single-service app is built from on its first deploy (it is
	// stored as the app's variables when that deployment goes live).
	ExtraEnv map[string]string
}

// spec is the service the plan starts.
func (p startPlan) spec() serviceSpec {
	if p.Service != nil {
		return *p.Service
	}
	return singleSpec(p.App)
}

// name is the container the plan creates.
func (p startPlan) name() string { return containerNameOf(p.App, p.spec().Name, p.Seq) }

func startFailed(msg, hint string) *apperr.Error {
	return apperr.Unprocessable("start_failed", msg, hint)
}

// containerEnv decrypts the app's env vars and adds PORT, BITPLOY_APP and BITPLOY_URL.
func (s *Service) containerEnv(ctx context.Context, app db.App, port int64) ([]string, *apperr.Error) {
	env, _, ae := s.serviceEnv(ctx, app, nil, nil, port)
	return env, ae
}

// serviceEnv is the environment of one service. From low to high
// precedence: extra (see startPlan.ExtraEnv), the service's environment from
// the compose file (its ${VAR} references filled in from the shared
// variables), the app's shared variables (set_env), the service's own
// variables, then PORT, BITPLOY_APP and BITPLOY_URL. svc nil is the only
// service of a single-layout app. shared are the decrypted shared variables,
// for the references in the service's command and healthcheck.
func (s *Service) serviceEnv(ctx context.Context, app db.App, svc *serviceSpec, extra map[string]string, port int64) (env []string, shared map[string]string, ae *apperr.Error) {
	service := singleService
	if svc != nil {
		service = svc.Name
	}
	rows, err := s.Q.ListServiceEnvVars(ctx, db.ListServiceEnvVarsParams{AppID: app.ID, Service: service})
	if err != nil {
		return nil, nil, apperr.Internal(err)
	}
	shared = map[string]string{}
	own := map[string]string{}
	for _, r := range rows {
		v, err := s.Crypto.Open(r.ValueEnc, envAADFor(app.ID, r.Service, r.Name))
		if err != nil {
			return nil, nil, startFailed("The environment variable "+r.Name+" cannot be decrypted.",
				"Set it again with set_env. (This happens when the server's secret key changed.)")
		}
		if r.Service == "" {
			shared[r.Name] = string(v)
		} else {
			own[r.Name] = string(v)
		}
	}
	// ${{alias.VAR}} references to the project's other apps (references.go).
	if app.ProjectID != "" {
		rv := s.newResolver(app)
		if ae := rv.resolveAll(ctx, shared); ae != nil {
			return nil, nil, ae
		}
		if ae := rv.resolveAll(ctx, own); ae != nil {
			return nil, nil, ae
		}
	}
	vars := map[string]string{}
	for k, v := range extra {
		vars[k] = v
	}
	if svc != nil {
		for k, v := range svc.Environment {
			vars[k] = deploy.ExpandEnvRefs(v, lookupIn(shared))
		}
	}
	for k, v := range shared {
		vars[k] = v
	}
	for k, v := range own {
		vars[k] = v
	}
	if port > 0 {
		vars["PORT"] = fmt.Sprint(port)
	}
	vars["BITPLOY_APP"] = app.Slug
	vars["BITPLOY_URL"] = s.appURL(app)
	names := make([]string, 0, len(vars))
	for n := range vars {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, n+"="+vars[n])
	}
	return out, shared, nil
}

func lookupIn(m map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := m[name]
		return v, ok
	}
}

// expandAll fills in the shared variable references of a command.
func expandAll(list []string, shared map[string]string) []string {
	if list == nil {
		return nil
	}
	out := make([]string, len(list))
	for i, v := range list {
		out[i] = deploy.ExpandEnvRefs(v, lookupIn(shared))
	}
	return out
}

func envAAD(appID, name string) string { return "env:" + appID + ":" + name }

// envAADFor is the AAD of a variable of one service; shared variables
// (service "") keep the format from before services.
func envAADFor(appID, service, name string) string {
	if service == "" {
		return envAAD(appID, name)
	}
	return "env:" + appID + ":" + service + ":" + name
}

// runSpec is the container a plan creates: name, image, env, limits, volumes
// and the service name as network alias. It has no side effects, so a
// deployment can compare it with the running container (serviceConfigHash).
func (s *Service) runSpec(ctx context.Context, p startPlan) (dockerx.RunSpec, *apperr.Error) {
	app := p.App
	spec := p.spec()
	env, shared, ae := s.serviceEnv(ctx, app, p.Service, p.ExtraEnv, p.Port)
	if ae != nil {
		return dockerx.RunSpec{}, ae
	}
	cl := s.Docker.ManagedLabels(app.ID, app.Slug)
	cl[dockerx.LabelDeployment] = p.DepID
	cl[dockerx.LabelSeq] = fmt.Sprint(p.Seq)
	cl[dockerx.LabelService] = spec.Name
	rs := dockerx.RunSpec{
		Name: p.name(), Image: p.Image, Env: env, Labels: cl, Network: dockerx.NetworkName(app.Slug),
		Aliases: []string{spec.Name}, MemoryMB: app.MemoryMb, CPUMillis: app.CpuMillis,
		Extra: s.projectAttach(ctx, app, spec),
	}
	if deploy.ReservedServiceName(spec.Name) {
		// A deployment stored before such names were refused: no alias that
		// could answer for another app's container in Traefik's DNS.
		rs.Aliases = nil
	}
	if !isMulti(app) {
		if app.VolumeEnabled != 0 {
			rs.Volume = appVolumeName(app)
		}
		if tp, ok := templateOf(app); ok {
			rs.ShmMB = tp.ShmMB
		}
		return rs, nil
	}
	rs.MemoryMB, rs.CPUMillis = spec.MemoryMB, spec.CPUMillis
	rs.Cmd, rs.Entrypoint = expandAll(spec.Command, shared), expandAll(spec.Entrypoint, shared)
	if hc := spec.Healthcheck.config(); hc != nil {
		hc.Test = expandAll(hc.Test, shared)
		rs.Healthcheck = hc
	}
	rs.User, rs.WorkingDir = spec.User, spec.WorkingDir
	for _, m := range spec.Mounts {
		rs.Mounts = append(rs.Mounts, dockerx.VolumeMount{Source: m.VolumeName, Target: m.MountPath})
	}
	return rs, nil
}

// startContainer creates and starts the container of a plan on network
// bp-<slug> with the app's limits: bp-<slug>-<n> for a single-service app,
// bp-<slug>_<service>-<n> otherwise. A container with the same name (a
// leftover) is replaced.
func (s *Service) startContainer(ctx context.Context, p startPlan) *apperr.Error {
	dk := s.Docker
	if dk == nil {
		return apperr.Unavailable("docker_unavailable", "bitploy cannot reach Docker right now.", "Tell the bitploy admin.")
	}
	rs, ae := s.runSpec(ctx, p)
	if ae != nil {
		return ae
	}
	return s.startRunSpec(ctx, p, rs)
}

// startRunSpec starts a container prepared by runSpec.
func (s *Service) startRunSpec(ctx context.Context, p startPlan, rs dockerx.RunSpec) *apperr.Error {
	dk := s.Docker
	app := p.App
	labels := dk.ManagedLabels(app.ID, app.Slug)
	netName := rs.Network
	if err := dk.EnsureNetwork(ctx, netName, labels); err != nil {
		return startFailed("Docker could not create the network for this app: "+apperr.Redact(dockerx.DescribeError(err)),
			"Try again in a minute. If it keeps happening, tell the bitploy admin (the Docker address pool may be full).")
	}
	if app.Type == "web" {
		if err := dk.ConnectNetwork(ctx, netName, s.Cfg.TraefikContainer); err != nil {
			return startFailed("bitploy could not connect its proxy ("+s.Cfg.TraefikContainer+") to the app network: "+apperr.Redact(dockerx.DescribeError(err)),
				"This is a server problem, not your app. Tell the bitploy admin.")
		}
	}
	volumes := []string{}
	if rs.Volume != "" {
		volumes = append(volumes, rs.Volume)
	}
	for _, m := range rs.Mounts {
		volumes = append(volumes, m.Source)
	}
	for _, volume := range volumes {
		created, err := dk.EnsureVolume(ctx, volume, labels)
		if err == nil && created {
			// A new volume is root:root 755; make it writable for the app's own user.
			if perr := dk.PrepareVolume(ctx, volume); perr != nil {
				_ = dk.RemoveVolume(context.WithoutCancel(ctx), volume) // so the next start prepares it again
				err = perr
			}
		}
		if err != nil {
			if errors.Is(err, dockerx.ErrVolumeOwnedByOther) {
				return startFailed("A data volume named "+volume+" already exists on this server and belongs to a different app, so bitploy will not use it.",
					"This is a server problem, not your app. Tell the bitploy admin to check or remove that Docker volume, or pick another app subdomain.")
			}
			return startFailed("Docker could not create the data volume: "+apperr.Redact(dockerx.DescribeError(err)), "Try again in a minute, or tell the bitploy admin.")
		}
	}
	name := rs.Name
	if exists, _ := dk.ContainerExists(ctx, name); exists {
		_ = dk.RemoveContainer(ctx, name)
	}
	if _, err := dk.CreateContainer(ctx, rs); err != nil {
		return startFailed("Docker could not create the container: "+apperr.Redact(dockerx.DescribeError(err)),
			"Check the Dockerfile's CMD and ENTRYPOINT, then deploy again. If this repeats, tell the bitploy admin.")
	}
	// The project network (bp-p-<id>), where the project's other apps reach
	// this one at its short name.
	for _, x := range rs.Extra {
		err := dk.EnsureNetwork(ctx, x.Network, dk.ProjectLabels(app.ProjectID))
		if err == nil {
			err = dk.ConnectNetworkAliases(ctx, x.Network, name, x.Aliases)
		}
		if err != nil {
			_ = dk.RemoveContainer(context.WithoutCancel(ctx), name)
			return startFailed("Docker could not connect the app to its project's network: "+apperr.Redact(dockerx.DescribeError(err)),
				"Try again in a minute. If it keeps happening, tell the bitploy admin (the Docker address pool may be full).")
		}
	}
	if err := dk.StartContainer(ctx, name); err != nil {
		_ = dk.RemoveContainer(context.WithoutCancel(ctx), name)
		return startFailed("Docker could not start the container: "+apperr.Redact(dockerx.DescribeError(err)),
			"Check that the Dockerfile's CMD or ENTRYPOINT starts your program and the file it points to exists.")
	}
	return nil
}

// desiredRoute computes what the app's route file should contain from DB
// state: the current container (if the app should be running), active custom
// hostnames, and a candidate while a health check is in progress.
func (s *Service) desiredRoute(ctx context.Context, app db.App) proxy.Route {
	r := proxy.Route{Slug: app.Slug, Host: s.Cfg.AppHostOn(app.Slug, app.BaseDomain)}
	// A candidate on a worker: the first compose deployment of the app has a
	// public service, and apps.type follows it once that deployment is live.
	if app.Type == "web" || s.candidate(app.ID) != "" {
		if hs, err := s.ActiveHostnames(ctx, app.ID); err == nil {
			r.Custom = hs
		}
		if root := appRoot(app); root != "" {
			r.Root, r.RootOwnCert = root, s.apexHasCert(root)
		}
		if app.DesiredState == "running" && app.CurrentDeploymentID.Valid {
			if d, err := s.Q.GetDeployment(ctx, app.CurrentDeploymentID.String); err == nil {
				r.Upstream = s.publicUpstream(ctx, app, d)
			}
		}
		r.Candidate = s.candidate(app.ID)
		if app.ProtectEnabled != 0 {
			r.Auth = &proxy.BasicAuth{User: app.ProtectUsername, Hash: app.ProtectHash}
			if r.Auth.User == "" || r.Auth.Hash == "" {
				// Cannot happen through the service. Fail closed: nobody gets in
				// until the owner sets a password again.
				r.Auth = &proxy.BasicAuth{User: "locked", Hash: proxy.NeverMatchHash}
			}
		}
	}
	return r
}

// publicUpstream is the address of a deployment's public service, or "".
func (s *Service) publicUpstream(ctx context.Context, app db.App, d db.Deployment) string {
	if !isMulti(app) {
		if d.ContainerName.Valid && d.Port.Valid {
			return fmt.Sprintf("http://%s:%d", dockerx.ContainerName(app.Slug, d.Seq), d.Port.Int64)
		}
		return ""
	}
	specs, _, err := s.appSpecs(ctx, app)
	if err != nil {
		return ""
	}
	pub := publicSpec(specs)
	if pub == nil {
		return ""
	}
	rows, err := s.depServices(ctx, app, d)
	if err != nil {
		return ""
	}
	for _, r := range rows {
		if r.Service == pub.Name && r.ContainerName.Valid && r.Port.Valid {
			return fmt.Sprintf("http://%s:%d", r.ContainerName.String, r.Port.Int64)
		}
	}
	return ""
}

// SyncRoute rewrites an app's Traefik route file from the database: the
// current running container plus every active custom domain. Called after
// swaps, stops, starts, and by the domains code when a domain becomes active.
func (s *Service) SyncRoute(ctx context.Context, appID string) error {
	l := s.appLock(appID)
	l.Lock()
	defer l.Unlock()
	return s.syncRouteLocked(ctx, appID)
}

func (s *Service) syncRouteLocked(ctx context.Context, appID string) error {
	app, err := s.Q.GetApp(ctx, appID)
	if err != nil {
		return nil // deleted apps have no route
	}
	if err := s.Routes.Write(s.desiredRoute(ctx, app)); err != nil {
		return apperr.Internal(err)
	}
	return nil
}

// retireContainer stops and removes a container after delay (zero = now).
// Safe to lose on a crash: the reconciler removes non-current containers.
func (s *Service) retireContainer(name string, delay time.Duration) {
	if s.Docker == nil || name == "" {
		return
	}
	s.setDraining(name, true)
	go func() {
		defer s.setDraining(name, false)
		if delay > 0 {
			time.Sleep(delay)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := s.Docker.StopContainerGrace(ctx, name, 10); err != nil {
			s.Log.Warn("stop old container", "container", name, "err", err)
		}
		if err := s.Docker.RemoveContainer(ctx, name); err != nil {
			s.Log.Warn("remove old container", "container", name, "err", err)
		}
	}()
}

// startCurrentLocked (re)creates the containers of the app's current
// deployment from their images, in dependency order, and points the route at
// them. only names one service to restart on its own. The caller holds the
// app lock. Used by start, restart, rename and the reconciler.
func (s *Service) startCurrentLocked(ctx context.Context, app db.App, only ...string) *apperr.Error {
	if !app.CurrentDeploymentID.Valid {
		return apperr.Conflict("app_not_deployed", "This app has not been deployed yet, so there is nothing to start.",
			"Deploy it first with deploy_files, or upload a zip and call deploy. If a deploy was started before and failed, call deploy with only app= to retry without resending files.").WithNext("deploy_files")
	}
	d, err := s.Q.GetDeployment(ctx, app.CurrentDeploymentID.String)
	if err != nil {
		return apperr.Internal(err)
	}
	if !isMulti(app) {
		return s.startCurrentSingle(ctx, app, d)
	}
	if s.Docker == nil {
		return apperr.Unavailable("docker_unavailable", "bitploy cannot reach Docker right now.", "Tell the bitploy admin.")
	}
	specs, _, err := s.appSpecs(ctx, app)
	if err != nil {
		return apperr.Internal(err)
	}
	order, err := startOrder(specs)
	if err != nil {
		return apperr.Unprocessable("services_invalid", "The services of this app cannot be started: "+err.Error()+".",
			"Fix depends_on in the app's services and deploy again.").WithNext("get_app")
	}
	rows, err := s.depServices(ctx, app, d)
	if err != nil {
		return apperr.Internal(err)
	}
	byService := map[string]db.DeploymentService{}
	for _, r := range rows {
		byService[r.Service] = r
	}
	want := firstOpt(only)
	for i := range order {
		sp := order[i]
		if want != "" && sp.Name != want {
			continue
		}
		r, ok := byService[sp.Name]
		if !ok || !r.Image.Valid {
			return apperr.Conflict("app_not_deployed", "The current deployment has no image for the "+sp.Name+" service.", "Deploy the app again.").WithNext("deploy")
		}
		if _, err := s.Docker.InspectImage(ctx, r.Image.String); err != nil {
			return apperr.Conflict("image_missing", "The image of the "+sp.Name+" service is gone from the server.", "Deploy the app again.").WithNext("deploy")
		}
		plan := startPlan{App: app, DepID: d.ID, Seq: d.Seq, Image: r.Image.String, Port: r.Port.Int64, Service: &sp}
		name := plan.name()
		if r.ContainerName.Valid && r.ContainerName.String != name {
			// A container kept from an older deployment: replaced by one named after this one.
			_ = s.Docker.RemoveContainer(ctx, r.ContainerName.String)
		}
		_ = s.Docker.StopContainerGrace(ctx, name, 10)
		if ae := s.startContainer(ctx, plan); ae != nil {
			ae.Message = "Service " + sp.Name + ": " + ae.Message
			return ae
		}
		if !r.ContainerName.Valid || r.ContainerName.String != name {
			r.ContainerName = nullStr(name)
			if err := s.Q.UpsertDeploymentService(ctx, db.UpsertDeploymentServiceParams(r)); err != nil {
				return apperr.Internal(err)
			}
			if sp.Public {
				_ = s.Q.SetDeploymentContainer(ctx, db.SetDeploymentContainerParams{ContainerName: nullStr(name), Port: r.Port, HasHealthcheck: r.HasHealthcheck, ID: d.ID})
			}
		}
	}
	_ = s.syncRouteLocked(ctx, app.ID)
	return nil
}

// startCurrentSingle is startCurrentLocked for a single-layout app.
func (s *Service) startCurrentSingle(ctx context.Context, app db.App, d db.Deployment) *apperr.Error {
	if !d.Image.Valid {
		return apperr.Conflict("app_not_deployed", "The current deployment has no image.", "Deploy the app again.").WithNext("deploy")
	}
	if s.Docker == nil {
		return apperr.Unavailable("docker_unavailable", "bitploy cannot reach Docker right now.", "Tell the bitploy admin.")
	}
	if _, err := s.Docker.InspectImage(ctx, d.Image.String); err != nil {
		return apperr.Conflict("image_missing", "The image of the current deployment is gone from the server.", "Deploy the app again.").WithNext("deploy")
	}
	port := int64(0)
	if d.Port.Valid {
		port = d.Port.Int64
	}
	name := dockerx.ContainerName(app.Slug, d.Seq)
	_ = s.Docker.StopContainerGrace(ctx, name, 10)
	if ae := s.startContainer(ctx, startPlan{App: app, DepID: d.ID, Seq: d.Seq, Image: d.Image.String, Port: port}); ae != nil {
		return ae
	}
	if !d.ContainerName.Valid || d.ContainerName.String != name {
		_ = s.Q.SetDeploymentContainer(ctx, db.SetDeploymentContainerParams{
			ContainerName: nullStr(name), Port: d.Port, HasHealthcheck: d.HasHealthcheck, ID: d.ID,
		})
		_ = s.Q.SyncSingleDeploymentService(ctx, d.ID)
	}
	_ = s.syncRouteLocked(ctx, app.ID)
	return nil
}
