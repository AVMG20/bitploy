package app

// OWNER: deploy agent. Starting app containers, env, routes. Shared by the
// pipeline, restart/start, rename and the reconciler.

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/arno/bitploy/internal/apperr"
	"github.com/arno/bitploy/internal/dockerx"
	"github.com/arno/bitploy/internal/proxy"
	"github.com/arno/bitploy/internal/store/db"
)

type startPlan struct {
	App   db.App
	DepID string
	Seq   int64
	Image string
	Port  int64
}

func startFailed(msg, hint string) *apperr.Error {
	return apperr.Unprocessable("start_failed", msg, hint)
}

// containerEnv decrypts the app's env vars and adds PORT, BITPLOY_APP and BITPLOY_URL.
func (s *Service) containerEnv(ctx context.Context, app db.App, port int64) ([]string, *apperr.Error) {
	rows, err := s.Q.ListEnvVars(ctx, app.ID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	vars := map[string]string{}
	for _, r := range rows {
		v, err := s.Crypto.Open(r.ValueEnc, envAAD(app.ID, r.Name))
		if err != nil {
			return nil, startFailed("The environment variable "+r.Name+" cannot be decrypted.",
				"Set it again with set_env. (This happens when the server's secret key changed.)")
		}
		vars[r.Name] = string(v)
	}
	if port > 0 {
		vars["PORT"] = fmt.Sprint(port)
	}
	vars["BITPLOY_APP"] = app.Slug
	vars["BITPLOY_URL"] = s.Cfg.AppURL(app.Slug)
	names := make([]string, 0, len(vars))
	for n := range vars {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, n+"="+vars[n])
	}
	return out, nil
}

func envAAD(appID, name string) string { return "env:" + appID + ":" + name }

// startContainer creates and starts bp-<slug>-<n> on network bp-<slug> with
// the app's limits. A container with the same name (a leftover) is replaced.
func (s *Service) startContainer(ctx context.Context, p startPlan) *apperr.Error {
	dk := s.Docker
	if dk == nil {
		return apperr.Unavailable("docker_unavailable", "bitploy cannot reach Docker right now.", "Tell the bitploy admin.")
	}
	app := p.App
	labels := dockerx.ManagedLabels(app.ID, app.Slug)
	netName := dockerx.NetworkName(app.Slug)
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
	env, ae := s.containerEnv(ctx, app, p.Port)
	if ae != nil {
		return ae
	}
	volume := ""
	if app.VolumeEnabled != 0 {
		volume = dockerx.VolumeName(app.Slug)
		if err := dk.EnsureVolume(ctx, volume, labels); err != nil {
			return startFailed("Docker could not create the data volume: "+apperr.Redact(dockerx.DescribeError(err)), "Try again in a minute, or tell the bitploy admin.")
		}
	}
	name := dockerx.ContainerName(app.Slug, p.Seq)
	if exists, _ := dk.ContainerExists(ctx, name); exists {
		_ = dk.RemoveContainer(ctx, name)
	}
	cl := dockerx.ManagedLabels(app.ID, app.Slug)
	cl[dockerx.LabelDeployment] = p.DepID
	cl[dockerx.LabelSeq] = fmt.Sprint(p.Seq)
	if _, err := dk.CreateContainer(ctx, dockerx.RunSpec{
		Name: name, Image: p.Image, Env: env, Labels: cl, Network: netName,
		MemoryMB: app.MemoryMb, CPUMillis: app.CpuMillis, Volume: volume,
	}); err != nil {
		return startFailed("Docker could not create the container: "+apperr.Redact(dockerx.DescribeError(err)),
			"Check the Dockerfile's CMD and ENTRYPOINT, then deploy again. If this repeats, tell the bitploy admin.")
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
	r := proxy.Route{Slug: app.Slug, Host: s.Cfg.AppHost(app.Slug)}
	if app.Type == "web" {
		if hs, err := s.ActiveHostnames(ctx, app.ID); err == nil {
			r.Custom = hs
		}
		if app.DesiredState == "running" && app.CurrentDeploymentID.Valid {
			if d, err := s.Q.GetDeployment(ctx, app.CurrentDeploymentID.String); err == nil && d.ContainerName.Valid && d.Port.Valid {
				r.Upstream = fmt.Sprintf("http://%s:%d", dockerx.ContainerName(app.Slug, d.Seq), d.Port.Int64)
			}
		}
		r.Candidate = s.candidate(app.ID)
	}
	return r
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

// startCurrentLocked (re)creates the container of the app's current
// deployment from its image and points the route at it. The caller holds the
// app lock. Used by start, restart, rename and the reconciler.
func (s *Service) startCurrentLocked(ctx context.Context, app db.App) *apperr.Error {
	if !app.CurrentDeploymentID.Valid {
		return apperr.Conflict("app_not_deployed", "This app has not been deployed yet, so there is nothing to start.",
			"Deploy it first with deploy_files, or upload a zip and call deploy.").WithNext("deploy_files")
	}
	d, err := s.Q.GetDeployment(ctx, app.CurrentDeploymentID.String)
	if err != nil {
		return apperr.Internal(err)
	}
	if !d.Image.Valid {
		return apperr.Conflict("app_not_deployed", "The current deployment has no image.", "Deploy the app again.").WithNext("deploy_files")
	}
	if s.Docker == nil {
		return apperr.Unavailable("docker_unavailable", "bitploy cannot reach Docker right now.", "Tell the bitploy admin.")
	}
	if _, err := s.Docker.InspectImage(ctx, d.Image.String); err != nil {
		return apperr.Conflict("image_missing", "The image of the current deployment is gone from the server.", "Deploy the app again.").WithNext("deploy_files")
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
	}
	_ = s.syncRouteLocked(ctx, app.ID)
	return nil
}
