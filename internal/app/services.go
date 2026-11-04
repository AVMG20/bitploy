package app

// OWNER: deploy agent. An app's services and volumes (docs/compose-plan.md).
//
// Every app has a service layout (apps.service_layout):
//   - single: one service named "app" whose settings are the apps row; the
//     app_services and app_volumes rows mirror it. Docker names have no
//     service part (bp-<slug>-<n>, bitploy/<slug>:<n>, apps.volume_name), so
//     apps from before services run exactly as they did.
//   - multi: up to maxServices services and their volumes live in
//     app_services and app_volumes. apps.memory_mb and cpu_millis are the
//     sums; apps.type, port and health_path follow the public service.
//     Docker names carry the service: bp-<slug>_<service>-<n>.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/deploy"
	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/metrics"
	"github.com/AVMG20/bitploy/internal/store/db"
	"github.com/moby/moby/api/types/container"
)

const (
	layoutSingle = "single"
	layoutMulti  = "multi"
	// singleService is the one service of a single-layout app.
	singleService = metrics.SingleService
	// singleVolume is the /data volume of a single-layout app.
	singleVolume = "data"
	maxServices  = 6
)

// serviceSpec is one service of an app as the deploy code runs it. The JSON
// form is deployments.services_spec (the services of a compose deployment).
type serviceSpec struct {
	Name           string            `json:"name"`
	Kind           string            `json:"kind"`            // build | image
	Image          string            `json:"image,omitempty"` // image services: the reference to pull
	ContextDir     string            `json:"context_dir,omitempty"`
	DockerfilePath string            `json:"dockerfile_path,omitempty"`
	BuildArgs      map[string]string `json:"build_args,omitempty"` // compose build.args (deployment only)
	Target         string            `json:"target,omitempty"`     // compose build.target (deployment only)
	Public         bool              `json:"public,omitempty"`
	Port           int64             `json:"port,omitempty"` // 0: not known before the image exists
	HealthPath     string            `json:"health_path,omitempty"`
	MemoryMB       int64             `json:"memory_mb"`
	CPUMillis      int64             `json:"cpu_millis"`
	Command        []string          `json:"command,omitempty"`
	Entrypoint     []string          `json:"entrypoint,omitempty"`
	Healthcheck    *healthcheckSpec  `json:"healthcheck,omitempty"`
	DependsOn      []dependency      `json:"depends_on,omitempty"`
	Environment    map[string]string `json:"environment,omitempty"`
	User           string            `json:"user,omitempty"`
	WorkingDir     string            `json:"working_dir,omitempty"`
	Mounts         []volumeSpec      `json:"mounts,omitempty"`
}

// healthcheckSpec is app_services.healthcheck (a compose healthcheck).
type healthcheckSpec struct {
	Test         []string `json:"test"`
	IntervalS    int      `json:"interval_s,omitempty"`
	TimeoutS     int      `json:"timeout_s,omitempty"`
	Retries      int      `json:"retries,omitempty"`
	StartPeriodS int      `json:"start_period_s,omitempty"`
}

// off reports a healthcheck that turns off the image's (compose disable: true).
func (h *healthcheckSpec) off() bool {
	return h != nil && len(h.Test) > 0 && strings.EqualFold(h.Test[0], "NONE")
}

func (h *healthcheckSpec) config() *container.HealthConfig {
	if h == nil || len(h.Test) == 0 {
		return nil
	}
	sec := func(n int) time.Duration { return time.Duration(n) * time.Second }
	return &container.HealthConfig{Test: h.Test, Interval: sec(h.IntervalS), Timeout: sec(h.TimeoutS),
		Retries: h.Retries, StartPeriod: sec(h.StartPeriodS)}
}

// dependency is one entry of app_services.depends_on.
type dependency struct {
	Service   string `json:"service"`
	Condition string `json:"condition"` // service_started | service_healthy
}

const (
	condStarted = "service_started"
	condHealthy = "service_healthy"
)

// volumeSpec is one volume of an app.
type volumeSpec struct {
	Name       string `json:"name"`        // like data or pgdata
	VolumeName string `json:"volume_name"` // the Docker volume
	SizeMB     int64  `json:"size_mb"`
	Service    string `json:"service"`
	MountPath  string `json:"mount_path"`
	UsedBytes  int64  `json:"-"`
	Measured   bool   `json:"-"` // UsedBytes comes from a measurement
}

func isMulti(a db.App) bool { return a.ServiceLayout == layoutMulti }

// appSpecs returns the app's services in their order and its volumes.
func (s *Service) appSpecs(ctx context.Context, a db.App) ([]serviceSpec, []volumeSpec, error) {
	if !isMulti(a) {
		svc := singleSpec(a)
		if len(svc.Mounts) > 0 {
			if d, err := s.Q.GetAppDisk(ctx, a.ID); err == nil {
				svc.Mounts[0].UsedBytes, svc.Mounts[0].Measured = d.VolumeBytes, true
			}
		}
		return []serviceSpec{svc}, svc.Mounts, nil
	}
	rows, err := s.Q.ListAppServices(ctx, a.ID)
	if err != nil {
		return nil, nil, err
	}
	vrows, err := s.Q.ListAppVolumes(ctx, a.ID)
	if err != nil {
		return nil, nil, err
	}
	vols := make([]volumeSpec, 0, len(vrows))
	for _, v := range vrows {
		vols = append(vols, volumeSpec{Name: v.Name, VolumeName: v.VolumeName, SizeMB: v.SizeMb, Service: v.Service, MountPath: v.MountPath, UsedBytes: v.UsedBytes, Measured: v.UsedBytes > 0})
	}
	specs := make([]serviceSpec, 0, len(rows))
	for _, r := range rows {
		sp, err := specFromRow(r)
		if err != nil {
			return nil, nil, err
		}
		for _, v := range vols {
			if v.Service == r.Name {
				sp.Mounts = append(sp.Mounts, v)
			}
		}
		specs = append(specs, sp)
	}
	return specs, vols, nil
}

// singleSpec is the one service of a single-layout app, from its apps row.
func singleSpec(a db.App) serviceSpec {
	svc := serviceSpec{
		Name: singleService, Kind: "build", ContextDir: a.ContextDir, DockerfilePath: a.DockerfilePath,
		Public: a.Type == "web", HealthPath: a.HealthPath, MemoryMB: a.MemoryMb, CPUMillis: a.CpuMillis,
	}
	if a.Port.Valid {
		svc.Port = a.Port.Int64
	}
	if a.VolumeEnabled != 0 {
		svc.Mounts = []volumeSpec{{Name: singleVolume, VolumeName: appVolumeName(a), SizeMB: a.VolumeMb, Service: singleService, MountPath: dockerx.DataMount}}
	}
	return svc
}

func specFromRow(r db.AppService) (serviceSpec, error) {
	sp := serviceSpec{
		Name: r.Name, Kind: r.Kind, Image: r.Image, ContextDir: r.ContextDir, DockerfilePath: r.DockerfilePath,
		Public: r.Public != 0, HealthPath: r.HealthPath, MemoryMB: r.MemoryMb, CPUMillis: r.CpuMillis,
		User: r.RunUser, WorkingDir: r.WorkingDir,
	}
	if r.Port.Valid {
		sp.Port = r.Port.Int64
	}
	for _, f := range []struct {
		raw  string
		into any
		col  string
	}{
		{r.Command, &sp.Command, "command"},
		{r.Entrypoint, &sp.Entrypoint, "entrypoint"},
		{r.DependsOn, &sp.DependsOn, "depends_on"},
		{r.Environment, &sp.Environment, "environment"},
	} {
		if f.raw == "" {
			continue
		}
		if err := json.Unmarshal([]byte(f.raw), f.into); err != nil {
			return sp, fmt.Errorf("service %s: bad %s: %w", r.Name, f.col, err)
		}
	}
	if r.Healthcheck != "" {
		var h healthcheckSpec
		if err := json.Unmarshal([]byte(r.Healthcheck), &h); err != nil {
			return sp, fmt.Errorf("service %s: bad healthcheck: %w", r.Name, err)
		}
		sp.Healthcheck = &h
	}
	return sp, nil
}

// syncSingleLayout makes the app_services and app_volumes rows of a
// single-layout app match its apps row. Call it in the same transaction as
// any change to the settings it mirrors; it does nothing for multi apps.
func syncSingleLayout(ctx context.Context, q *db.Queries, appID string) error {
	if err := q.SyncSingleAppService(ctx, appID); err != nil {
		return err
	}
	if err := q.SyncSingleAppVolume(ctx, appID); err != nil {
		return err
	}
	return q.DeleteSingleAppVolumeIfOff(ctx, appID)
}

// dockerVolumesOf names the Docker volumes of an app. A single-layout app
// always has its data volume name, whether or not the volume is on.
func (s *Service) dockerVolumesOf(ctx context.Context, a db.App) []string {
	if !isMulti(a) {
		return []string{appVolumeName(a)}
	}
	rows, err := s.Q.ListAppVolumes(ctx, a.ID)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.VolumeName)
	}
	return out
}

// containerNameOf is the container of a service for deployment seq.
func containerNameOf(a db.App, service string, seq int64) string {
	if !isMulti(a) {
		return dockerx.ContainerName(a.Slug, seq)
	}
	return dockerx.ServiceContainerName(a.Slug, service, seq)
}

// imageNameOf is the image bitploy tags for a service of deployment seq.
func imageNameOf(a db.App, service string, seq int64) string {
	if !isMulti(a) {
		return dockerx.ImageName(a.Slug, seq)
	}
	return dockerx.ServiceImageName(a.Slug, service, seq)
}

// startOrder sorts services so each one comes after the services it depends
// on, otherwise keeping their order. Unknown dependencies and cycles are errors.
func startOrder(specs []serviceSpec) ([]serviceSpec, error) {
	byName := map[string]int{}
	for i, sp := range specs {
		byName[sp.Name] = i
	}
	for _, sp := range specs {
		for _, d := range sp.DependsOn {
			if _, ok := byName[d.Service]; !ok {
				return nil, fmt.Errorf("service %s depends on %s, which is not a service of this app", sp.Name, d.Service)
			}
		}
	}
	const (
		unvisited = iota
		visiting
		done
	)
	state := make([]int, len(specs))
	out := make([]serviceSpec, 0, len(specs))
	var visit func(i int, path []string) error
	visit = func(i int, path []string) error {
		switch state[i] {
		case done:
			return nil
		case visiting:
			return fmt.Errorf("services depend on each other in a circle: %s", strings.Join(append(path, specs[i].Name), " -> "))
		}
		state[i] = visiting
		for _, d := range specs[i].DependsOn {
			if err := visit(byName[d.Service], append(path, specs[i].Name)); err != nil {
				return err
			}
		}
		state[i] = done
		out = append(out, specs[i])
		return nil
	}
	for i := range specs {
		if err := visit(i, nil); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// publicSpec is the service the app's routes point at, or nil.
func publicSpec(specs []serviceSpec) *serviceSpec {
	for i := range specs {
		if specs[i].Public {
			return &specs[i]
		}
	}
	return nil
}

// depServices returns the containers of a deployment, one per service. A
// single-layout deployment is described by its deployments row (the source
// of truth for those apps, mirrored into deployment_services).
func (s *Service) depServices(ctx context.Context, a db.App, d db.Deployment) ([]db.DeploymentService, error) {
	if !isMulti(a) {
		return []db.DeploymentService{singleDepService(d)}, nil
	}
	return s.Q.ListDeploymentServices(ctx, d.ID)
}

func singleDepService(d db.Deployment) db.DeploymentService {
	return db.DeploymentService{
		DeploymentID: d.ID, Service: singleService, Image: d.Image, ContainerName: d.ContainerName,
		Port: d.Port, HasHealthcheck: d.HasHealthcheck,
	}
}

// currentContainers maps each service of the app's current deployment to
// the container that runs it. A service without a recorded container gets
// the name the deployment would give it, so a missing container is noticed.
func (s *Service) currentContainers(ctx context.Context, a db.App) map[string]string {
	out := map[string]string{}
	if !a.CurrentDeploymentID.Valid {
		return out
	}
	d, err := s.Q.GetDeployment(ctx, a.CurrentDeploymentID.String)
	if err != nil {
		return out
	}
	if !isMulti(a) {
		out[singleService] = dockerx.ContainerName(a.Slug, d.Seq)
		return out
	}
	rows, err := s.Q.ListDeploymentServices(ctx, d.ID)
	if err != nil {
		return out
	}
	for _, r := range rows {
		name := r.ContainerName.String
		if name == "" {
			name = containerNameOf(a, r.Service, d.Seq)
		}
		out[r.Service] = name
	}
	return out
}

// serviceNotFound is the error for a service parameter that names no
// service of the app.
func serviceNotFound(a db.App, name string, specs []serviceSpec) *apperr.Error {
	names := make([]string, len(specs))
	for i, sp := range specs {
		names[i] = sp.Name
	}
	return apperr.NotFound("service_not_found",
		fmt.Sprintf("The app %s has no service named %q. Its services are: %s.", a.Slug, name, strings.Join(names, ", ")),
		"Leave service out to use the whole app, or pass one of those names. get_app lists the services.").
		WithDetail("services", names).WithNext("get_app")
}

// pickService resolves an optional service parameter. "" means the whole
// app and returns nil. A single-service app also accepts its own service name.
func (s *Service) pickService(ctx context.Context, a db.App, name string) (*serviceSpec, []serviceSpec, error) {
	specs, _, err := s.appSpecs(ctx, a)
	if err != nil {
		return nil, nil, apperr.Internal(err)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, specs, nil
	}
	for i := range specs {
		if specs[i].Name == name {
			return &specs[i], specs, nil
		}
	}
	return nil, specs, serviceNotFound(a, name, specs)
}

// volumeNotFound is the error for a volume parameter that names no volume
// of the app.
func volumeNotFound(a db.App, name string, vols []volumeSpec) *apperr.Error {
	names := make([]string, len(vols))
	for i, v := range vols {
		names[i] = v.Name
	}
	msg := fmt.Sprintf("The app %s has no volume named %q.", a.Slug, name)
	if len(names) > 0 {
		msg += " Its volumes are: " + strings.Join(names, ", ") + "."
	}
	return apperr.NotFound("volume_not_found", msg,
		"Leave volume out to use the app's first volume, or pass one of those names. get_app lists the volumes.").
		WithDetail("volumes", names).WithNext("get_app")
}

// pickVolume resolves an optional volume parameter: "" is the app's first
// volume. ok is false when the app has no volume at all (the caller keeps its
// own "volume is off" error); err is set for an unknown name.
func (s *Service) pickVolume(ctx context.Context, a db.App, name string) (v volumeSpec, ok bool, err error) {
	_, vols, lerr := s.appSpecs(ctx, a)
	if lerr != nil {
		return volumeSpec{}, false, apperr.Internal(lerr)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		if len(vols) == 0 {
			return volumeSpec{}, false, nil
		}
		return vols[0], true, nil
	}
	for _, v := range vols {
		if v.Name == name {
			return v, true, nil
		}
	}
	if len(vols) == 0 && !isMulti(a) && name == singleVolume {
		return volumeSpec{}, false, nil // /data is off: the caller says so
	}
	return volumeSpec{}, false, volumeNotFound(a, name, vols)
}

// volumeOf is the volume called name. A single-service app has one volume,
// whatever the name (backups made before volumes had names say "data"); ok is
// false when an app with several services has no such volume any more.
func (s *Service) volumeOf(ctx context.Context, a db.App, name string) (volumeSpec, bool) {
	if !isMulti(a) {
		return volumeSpec{Name: singleVolume, VolumeName: appVolumeName(a), SizeMB: a.VolumeMb, Service: singleService, MountPath: dockerx.DataMount}, true
	}
	rows, err := s.Q.ListAppVolumes(ctx, a.ID)
	if err != nil {
		return volumeSpec{}, false
	}
	for _, v := range rows {
		if v.Name == name {
			return volumeSpec{Name: v.Name, VolumeName: v.VolumeName, SizeMB: v.SizeMb, Service: v.Service, MountPath: v.MountPath, UsedBytes: v.UsedBytes}, true
		}
	}
	return volumeSpec{}, false
}

// perServiceSettings refuses app-wide settings that belong to one service
// of an app with several services: there, each service has its own port,
// size and volumes.
func perServiceSettings(a db.App, in UpdateAppInput) error {
	if !isMulti(a) {
		return nil
	}
	var fields []string
	add := func(set bool, name string) {
		if set {
			fields = append(fields, name)
		}
	}
	add(in.Type != nil, "type")
	add(in.Port != nil, "port")
	add(in.HealthPath != nil, "health_path")
	add(in.DockerfilePath != nil, "dockerfile_path")
	add(in.ContextDir != nil, "context_dir")
	// build_mode compose is what an app with several services already is.
	add(in.BuildMode != nil && strings.ToLower(strings.TrimSpace(*in.BuildMode)) != deploy.BuildModeCompose, "build_mode")
	add(in.Volume != nil, "volume")
	add(in.VolumeMB != nil, "volume_mb")
	add(in.MemoryMB != nil, "memory_mb")
	add(in.CPUMillis != nil, "cpu_millis")
	if len(fields) == 0 {
		return nil
	}
	return apperr.Unprocessable("set_per_service",
		fmt.Sprintf("The app %s has several services, so %s cannot be set for the whole app. Nothing was changed.", a.Slug, strings.Join(fields, ", ")),
		"Each service has its own settings, and they come from the compose file: change them there (ports, deploy.resources.limits, volumes, x-bitploy) and deploy again; get_app lists them. Leave these fields out to change the name, address or domain of the app. An app with several services cannot go back to one service: create a new app for that.").
		WithDetail("fields", fields).WithNext("get_app")
}

// syncMultiTotals stores the sums over the services and volumes of an app
// with several services on its apps row: memory_mb and cpu_millis count
// towards the memory and CPU allowance, volume_mb (with volume_enabled) towards
// the storage allowance, exactly like a single-service app's.
func syncMultiTotals(ctx context.Context, q *db.Queries, appID string) error {
	return q.SetMultiAppTotals(ctx, appID)
}

// firstOpt returns the first optional string argument, or "".
func firstOpt(opt []string) string {
	if len(opt) > 0 {
		return opt[0]
	}
	return ""
}

// serviceConfigHash sums up everything a container is created from. Two
// deployments of a multi-service app with the same hash for a service run the
// same container, so the newer one keeps the running container.
//
// image is the image's fingerprint (dockerx.BuiltImage.Fingerprint, else its
// ID): two builds of unchanged code differ in their labels only.
func serviceConfigHash(image string, spec dockerx.RunSpec) string {
	env := append([]string(nil), spec.Env...)
	sort.Strings(env)
	mounts := append([]dockerx.VolumeMount(nil), spec.Mounts...)
	sort.Slice(mounts, func(i, j int) bool { return mounts[i].Target < mounts[j].Target })
	b, _ := json.Marshal(struct {
		Image      string
		Env        []string
		Network    string
		Aliases    []string
		Memory     int64
		CPU        int64
		Volume     string
		Mounts     []dockerx.VolumeMount
		Cmd        []string
		Entrypoint []string
		Health     *container.HealthConfig
		User       string `json:",omitempty"`
		WorkingDir string `json:",omitempty"`
	}{image, env, spec.Network, spec.Aliases, spec.MemoryMB, spec.CPUMillis, spec.Volume, mounts, spec.Cmd, spec.Entrypoint, spec.Healthcheck, spec.User, spec.WorkingDir})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ---- views ----

// serviceViews and volumeViews are App.services and App.volumes.
func (s *Service) serviceViews(ctx context.Context, a db.App) ([]AppService, []AppVolume) {
	specs, vols, err := s.appSpecs(ctx, a)
	if err != nil {
		s.Log.Warn("read app services", "app", a.Slug, "err", err)
		return []AppService{}, []AppVolume{}
	}
	cur := map[string]db.DeploymentService{}
	if a.CurrentDeploymentID.Valid {
		if d, err := s.Q.GetDeployment(ctx, a.CurrentDeploymentID.String); err == nil {
			if rows, err := s.depServices(ctx, a, d); err == nil {
				for _, r := range rows {
					cur[r.Service] = r
				}
			}
		}
	}
	out := make([]AppService, 0, len(specs))
	for _, sp := range specs {
		v := AppService{
			Name: sp.Name, Kind: sp.Kind, Public: sp.Public, HealthPath: sp.HealthPath,
			MemoryMB: sp.MemoryMB, CPUMillis: sp.CPUMillis, Volumes: []string{}, DependsOn: []string{},
		}
		if sp.Kind == "image" {
			v.Image = sp.Image
		}
		if sp.Port > 0 {
			p := sp.Port
			v.Port = &p
		}
		if r, ok := cur[sp.Name]; ok {
			if r.ContainerName.Valid {
				v.Container = r.ContainerName.String
			}
			if v.Port == nil && r.Port.Valid {
				p := r.Port.Int64
				v.Port = &p
			}
		}
		for _, m := range sp.Mounts {
			v.Volumes = append(v.Volumes, m.Name)
		}
		for _, d := range sp.DependsOn {
			v.DependsOn = append(v.DependsOn, d.Service)
		}
		out = append(out, v)
	}
	vout := make([]AppVolume, 0, len(vols))
	for _, v := range vols {
		av := AppVolume{Name: v.Name, Service: v.Service, MountPath: v.MountPath, SizeMB: v.SizeMB}
		if v.Measured {
			mb := round1(float64(v.UsedBytes) / mib)
			av.UsedMB = &mb
		}
		vout = append(vout, av)
	}
	return out, vout
}

// nullPort is a port for sql.NullInt64; 0 is NULL.
func nullPort(p int64) sql.NullInt64 { return sql.NullInt64{Int64: p, Valid: p > 0} }
