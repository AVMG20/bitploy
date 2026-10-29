package app

// OWNER: deploy agent. Apps, deployments, sources (files, uploads), env vars,
// lifecycle, logs, the deploy worker and the reconciler. Signatures are a
// shared contract used by internal/api and internal/mcp; implement the
// bodies, keep the signatures.
//
// Files: apps.go (app CRUD, quotas, slugs), apps_env.go, apps_lifecycle.go,
// apps_logs.go, deploy_queue.go (sources and queueing), deploy_worker.go,
// deploy_pipeline.go, deploy_container.go, deploy_reconcile.go,
// deploy_cleanup.go, deploy_runtime.go.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/arno/bitploy/internal/apperr"
	"github.com/arno/bitploy/internal/deploy"
	"github.com/arno/bitploy/internal/dockerx"
	"github.com/arno/bitploy/internal/ids"
	"github.com/arno/bitploy/internal/store/db"
)

const (
	defaultMemoryMB  = 512
	defaultCPUMillis = 500
	minMemoryMB      = 64
	minCPUMillis     = 50
	slugReserveDays  = 30
)

// ---- apps ----

func (s *Service) CreateApp(ctx context.Context, a Actor, in CreateAppInput) (App, error) {
	if err := RequireDeploy(a); err != nil {
		return App{}, err
	}
	user, err := s.activeUser(ctx, a.UserID)
	if err != nil {
		return App{}, err
	}

	typ := strings.ToLower(strings.TrimSpace(in.Type))
	if typ == "" {
		typ = "web"
	}
	if typ != "web" && typ != "worker" {
		return App{}, apperr.BadRequest("app_type_invalid", "The app type "+quote(in.Type)+" is not known.", "Use web (an HTTP site or API, the default) or worker (a bot or queue consumer without a port).")
	}
	var port sql.NullInt64
	if in.Port != nil && *in.Port != 0 {
		if err := validPort(*in.Port); err != nil {
			return App{}, err
		}
		port = sql.NullInt64{Int64: *in.Port, Valid: true}
	}
	mem, cpu := int64(defaultMemoryMB), int64(defaultCPUMillis)
	if in.MemoryMB != nil {
		mem = *in.MemoryMB
	}
	if in.CPUMillis != nil {
		cpu = *in.CPUMillis
	}
	if err := validResources(mem, cpu); err != nil {
		return App{}, err
	}

	n, err := s.Q.CountAppsByUser(ctx, user.ID)
	if err != nil {
		return App{}, apperr.Internal(err)
	}
	if n >= user.QuotaApps {
		return App{}, apperr.Forbidden("apps_quota_exceeded",
			fmt.Sprintf("You already have %d apps, which is the maximum for your account.", n),
			"Delete an app you no longer need with delete_app, or ask a bitploy admin to raise your quota.").WithNext("list_apps")
	}
	if err := s.checkResourceQuota(ctx, user, "", mem, cpu); err != nil {
		return App{}, err
	}
	if err := s.checkServerRoom(ctx, mem); err != nil {
		return App{}, err
	}

	slug := strings.ToLower(strings.TrimSpace(in.Slug))
	generated := slug == ""
	if !generated {
		if err := s.checkSlugFree(ctx, slug, user.ID); err != nil {
			return App{}, err
		}
	} else {
		for i := 0; ; i++ {
			slug = deploy.GenerateSlug()
			if s.checkSlugFree(ctx, slug, user.ID) == nil {
				break
			}
			if i > 50 {
				return App{}, apperr.Internal(errors.New("no free generated slug"))
			}
		}
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = slug
	}
	if len([]rune(name)) > 80 {
		return App{}, apperr.BadRequest("app_name_invalid", "The app name is longer than 80 characters.", "Use a shorter name.")
	}

	now := s.now()
	id := ids.New(ids.App)
	err = s.Store.Tx(ctx, func(q *db.Queries) error {
		if err := q.InsertApp(ctx, db.InsertAppParams{
			ID: id, UserID: user.ID, Slug: slug, Name: name, Type: typ, Port: port,
			VolumeEnabled: b2i(in.Volume), MemoryMb: mem, CpuMillis: cpu, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		// The slug is now really in use; the owner's old reservation is spent.
		return q.DeleteSlugReservation(ctx, slug)
	})
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: apps.slug") {
			return App{}, slugTaken(slug)
		}
		return App{}, apperr.Internal(err)
	}
	s.Audit(ctx, a, "app.create", "app", id, map[string]any{"slug": slug, "type": typ})
	row, err := s.Q.GetApp(ctx, id)
	if err != nil {
		return App{}, apperr.Internal(err)
	}
	return s.appView(ctx, a, row), nil
}

func (s *Service) ListApps(ctx context.Context, a Actor) ([]App, error) {
	rows, err := s.Q.ListAppsByUser(ctx, a.UserID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return s.appViews(ctx, a, rows), nil
}

// ListAllApps is admin-only: every app on the server.
func (s *Service) ListAllApps(ctx context.Context, a Actor) ([]App, error) {
	if err := RequireAdmin(a); err != nil {
		return nil, err
	}
	rows, err := s.Q.ListAllApps(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return s.appViews(ctx, a, rows), nil
}

func (s *Service) GetApp(ctx context.Context, a Actor, ref string) (App, error) {
	row, err := s.LoadApp(ctx, a, ref, false)
	if err != nil {
		return App{}, err
	}
	return s.appView(ctx, a, row), nil
}

func (s *Service) UpdateApp(ctx context.Context, a Actor, ref string, in UpdateAppInput) (App, error) {
	row, err := s.LoadApp(ctx, a, ref, true)
	if err != nil {
		return App{}, err
	}
	owner, err := s.Q.GetUser(ctx, row.UserID)
	if err != nil {
		return App{}, apperr.Internal(err)
	}
	if owner.Status != "active" {
		return App{}, suspendedErr()
	}
	p := db.UpdateAppSettingsParams{
		ID: row.ID, Slug: row.Slug, Name: row.Name, Type: row.Type, Port: row.Port,
		HealthPath: row.HealthPath, DockerfilePath: row.DockerfilePath, ContextDir: row.ContextDir,
		VolumeEnabled: row.VolumeEnabled, MemoryMb: row.MemoryMb, CpuMillis: row.CpuMillis, UpdatedAt: s.now(),
	}
	changed := map[string]any{}

	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" || len([]rune(n)) > 80 {
			return App{}, apperr.BadRequest("app_name_invalid", "The app name must be 1 to 80 characters.", "Use a short name such as \"Family photos\".")
		}
		p.Name = n
		changed["name"] = n
	}
	if in.Type != nil {
		t := strings.ToLower(strings.TrimSpace(*in.Type))
		if t != "web" && t != "worker" {
			return App{}, apperr.BadRequest("app_type_invalid", "The app type "+quote(*in.Type)+" is not known.", "Use web or worker.")
		}
		p.Type = t
		changed["type"] = t
	}
	if in.Port != nil {
		if *in.Port == 0 {
			p.Port = sql.NullInt64{}
		} else {
			if err := validPort(*in.Port); err != nil {
				return App{}, err
			}
			p.Port = sql.NullInt64{Int64: *in.Port, Valid: true}
		}
		changed["port"] = *in.Port
	}
	if in.HealthPath != nil {
		hp := strings.TrimSpace(*in.HealthPath)
		if !strings.HasPrefix(hp, "/") || len(hp) > 200 || strings.ContainsAny(hp, " \t\r\n") {
			return App{}, apperr.BadRequest("health_path_invalid", "The health path must start with / and contain no spaces.", "Use something like / or /healthz.")
		}
		p.HealthPath = hp
		changed["health_path"] = hp
	}
	if in.DockerfilePath != nil {
		dp, derr := deploy.CleanRelPath(strings.TrimSpace(*in.DockerfilePath))
		if derr != nil {
			return App{}, apperr.BadRequest("dockerfile_path_invalid", "The Dockerfile path is not allowed: "+derr.Error()+".", "Use a relative path such as Dockerfile or docker/Dockerfile.prod.")
		}
		p.DockerfilePath = dp
		changed["dockerfile_path"] = dp
	}
	if in.ContextDir != nil {
		cd := strings.ReplaceAll(strings.TrimSpace(*in.ContextDir), "\\", "/")
		if cd == "" {
			cd = "."
		}
		if strings.HasPrefix(cd, "/") || cd == ".." || strings.HasPrefix(cd, "../") || strings.Contains(cd, "/../") || strings.HasSuffix(cd, "/..") {
			return App{}, apperr.BadRequest("context_dir_invalid", "The build context folder must be inside the project.", "Use a relative folder such as . or backend.")
		}
		p.ContextDir = path.Clean(cd)
		changed["context_dir"] = p.ContextDir
	}
	if in.Volume != nil {
		p.VolumeEnabled = b2i(*in.Volume)
		changed["volume"] = *in.Volume
	}
	if in.MemoryMB != nil || in.CPUMillis != nil {
		if in.MemoryMB != nil {
			p.MemoryMb = *in.MemoryMB
		}
		if in.CPUMillis != nil {
			p.CpuMillis = *in.CPUMillis
		}
		if err := validResources(p.MemoryMb, p.CpuMillis); err != nil {
			return App{}, err
		}
		if err := s.checkResourceQuota(ctx, owner, row.ID, p.MemoryMb, p.CpuMillis); err != nil {
			return App{}, err
		}
		if delta := p.MemoryMb - row.MemoryMb; delta > 0 {
			if err := s.checkServerRoom(ctx, delta); err != nil {
				return App{}, err
			}
		}
		changed["memory_mb"], changed["cpu_millis"] = p.MemoryMb, p.CpuMillis
	}

	renamed := false
	if in.Slug != nil {
		ns := strings.ToLower(strings.TrimSpace(*in.Slug))
		if ns != row.Slug {
			if err := s.checkSlugFree(ctx, ns, row.UserID); err != nil {
				return App{}, err
			}
			p.Slug = ns
			renamed = true
			changed["slug"] = ns
		}
	}

	if renamed {
		if err := s.renameApp(ctx, row, p); err != nil {
			return App{}, err
		}
	} else if err := s.Q.UpdateAppSettings(ctx, p); err != nil {
		return App{}, apperr.Internal(err)
	}
	if len(changed) > 0 {
		changed["old_slug"] = row.Slug
		s.Audit(ctx, a, "app.update", "app", row.ID, changed)
	}
	// Port and health path changes apply to the route at once.
	if !renamed && (in.Port != nil || in.Type != nil) {
		_ = s.SyncRoute(ctx, row.ID)
	}
	fresh, err := s.Q.GetApp(ctx, row.ID)
	if err != nil {
		return App{}, apperr.Internal(err)
	}
	return s.appView(ctx, a, fresh), nil
}

// renameApp changes an app's slug. Simplest correct approach (decisions.md):
// the old container and network are replaced, not renamed. Images are
// re-tagged to the new name first, then the database changes, then the old
// container, network and route go and the current image starts again under
// the new names. The app is down for a few seconds. Apps with a volume are
// refused because Docker cannot rename a volume.
func (s *Service) renameApp(ctx context.Context, row db.App, p db.UpdateAppSettingsParams) error {
	oldSlug, newSlug := row.Slug, p.Slug
	if !s.settled(row.ID) {
		return busyErr()
	}
	if n, err := s.Q.CountPendingDeploymentsByApp(ctx, row.ID); err == nil && n > 0 {
		return busyErr()
	}
	if row.VolumeEnabled != 0 {
		return apperr.Conflict("rename_with_volume",
			"This app has a volume, and Docker cannot rename volumes, so the subdomain cannot change.",
			"Create a new app with the new name, deploy there, and delete this one when you no longer need its data.")
	}
	lock := s.appLock(row.ID)
	lock.Lock()
	defer lock.Unlock()

	// 1. Add the new image tags (non-destructive).
	var oldTags []string
	if s.Docker != nil {
		tags, err := s.Docker.ListAppImages(ctx, row.ID)
		if err != nil {
			return apperr.Internal(err)
		}
		for _, t := range tags {
			if v, ok := strings.CutPrefix(t, "bitploy/"+oldSlug+":"); ok {
				if err := s.Docker.TagImage(ctx, t, "bitploy/"+newSlug+":"+v); err != nil {
					return apperr.Internal(err)
				}
				oldTags = append(oldTags, t)
			}
		}
	}

	// 2. Database: new slug, deployment names, reservation of the old slug.
	now := s.now()
	err := s.Store.Tx(ctx, func(q *db.Queries) error {
		if err := q.UpdateAppSettings(ctx, p); err != nil {
			return err
		}
		if err := q.RewriteDeploymentNames(ctx, db.RewriteDeploymentNamesParams{Slug: nullStr(newSlug), AppID: row.ID}); err != nil {
			return err
		}
		if err := q.UpsertSlugReservation(ctx, db.UpsertSlugReservationParams{
			Slug: oldSlug, UserID: row.UserID, ExpiresAt: now + slugReserveDays*86400,
		}); err != nil {
			return err
		}
		return q.DeleteSlugReservation(ctx, newSlug)
	})
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return slugTaken(newSlug)
		}
		return apperr.Internal(err)
	}

	// 3. Remove everything named after the old slug.
	_ = s.Routes.Remove(oldSlug)
	if s.Docker != nil {
		bg := context.WithoutCancel(ctx)
		if cs, err := s.Docker.ListAppContainers(bg, row.ID); err == nil {
			for _, c := range cs {
				_ = s.Docker.RemoveContainer(bg, dockerx.SummaryName(c))
			}
		}
		_ = s.Docker.DisconnectNetwork(bg, dockerx.NetworkName(oldSlug), s.Cfg.TraefikContainer)
		_ = s.Docker.RemoveNetwork(bg, dockerx.NetworkName(oldSlug))
		for _, t := range oldTags {
			_ = s.Docker.RemoveImage(bg, t)
		}
	}

	// 4. Start the current image again under the new names.
	fresh, err := s.Q.GetApp(ctx, row.ID)
	if err != nil {
		return apperr.Internal(err)
	}
	if fresh.DesiredState == "running" && fresh.CurrentDeploymentID.Valid && s.Docker != nil {
		if rerr := s.startCurrentLocked(ctx, fresh); rerr != nil {
			s.Log.Warn("start after rename failed; the reconciler will retry", "app", newSlug, "err", rerr)
		}
	}
	return nil
}

// DeleteApp requires confirmSlug == the app's slug.
func (s *Service) DeleteApp(ctx context.Context, a Actor, ref, confirmSlug string) error {
	row, err := s.LoadApp(ctx, a, ref, true)
	if err != nil {
		return err
	}
	if strings.TrimSpace(strings.ToLower(confirmSlug)) != row.Slug {
		return apperr.BadRequest("confirm_mismatch",
			"The confirmation does not match this app's subdomain, so nothing was deleted.",
			"Call delete_app again with confirm_slug set to exactly "+quote(row.Slug)+".").WithNext("delete_app")
	}
	s.cancelInflight(row.ID, 30*time.Second)
	lock := s.appLock(row.ID)
	lock.Lock()
	defer lock.Unlock()

	bg := context.WithoutCancel(ctx)
	deps, _ := s.Q.ListAllDeploymentsByApp(bg, row.ID)
	_ = s.Routes.Remove(row.Slug)
	if s.Docker != nil {
		if cs, err := s.Docker.ListAppContainers(bg, row.ID); err == nil {
			for _, c := range cs {
				_ = s.Docker.RemoveContainer(bg, dockerx.SummaryName(c))
			}
		}
		_ = s.Docker.DisconnectNetwork(bg, dockerx.NetworkName(row.Slug), s.Cfg.TraefikContainer)
		if err := s.Docker.RemoveNetwork(bg, dockerx.NetworkName(row.Slug)); err != nil {
			s.Log.Warn("remove network", "app", row.Slug, "err", err)
		}
		if tags, err := s.Docker.ListAppImages(bg, row.ID); err == nil {
			for _, t := range tags {
				_ = s.Docker.RemoveImage(bg, t)
			}
		}
		_ = s.Docker.RemoveVolume(bg, dockerx.VolumeName(row.Slug))
	}
	for _, d := range deps {
		_ = os.Remove(s.Cfg.BuildLog(d.ID))
		_ = os.RemoveAll(s.Cfg.BuildDir(d.ID))
		_ = os.Remove(s.sourceZip(d.ID))
	}
	if err := s.Store.Tx(ctx, func(q *db.Queries) error {
		if err := q.DeleteAppRow(ctx, row.ID); err != nil {
			return err
		}
		return q.UpsertSlugReservation(ctx, db.UpsertSlugReservationParams{
			Slug: row.Slug, UserID: row.UserID, ExpiresAt: s.now() + slugReserveDays*86400,
		})
	}); err != nil {
		return apperr.Internal(err)
	}
	for _, d := range deps {
		s.NotifyDeployment(d.ID)
	}
	s.Audit(ctx, a, "app.delete", "app", row.ID, map[string]any{"slug": row.Slug})
	return nil
}

// ---- helpers ----

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func busyErr() *apperr.Error {
	return apperr.Conflict("app_busy",
		"A deployment of this app is running or queued right now.",
		"Wait until it finishes (call get_deployment with wait_seconds=30), then try again.")
}

func suspendedErr() *apperr.Error {
	return apperr.Forbidden("account_suspended", "This account is suspended.", "Ask a bitploy admin to reactivate it.")
}

func slugTaken(slug string) *apperr.Error {
	return apperr.Conflict("slug_taken",
		"The subdomain "+quote(slug)+" is already taken or reserved.",
		"Pick another name, or leave the slug empty to get a random one like quiet-otter-3f9a.")
}

func validPort(p int64) *apperr.Error {
	if p < 1 || p > 65535 {
		return apperr.BadRequest("port_invalid", fmt.Sprintf("The port %d is not valid.", p), "Use a number between 1 and 65535, the port your app listens on.")
	}
	return nil
}

func validResources(memMB, cpuMillis int64) *apperr.Error {
	if memMB < minMemoryMB || memMB > 64*1024 {
		return apperr.BadRequest("memory_invalid", fmt.Sprintf("%d MB of memory is not a valid amount.", memMB), fmt.Sprintf("Use at least %d MB.", minMemoryMB))
	}
	if cpuMillis < minCPUMillis || cpuMillis > 64*1000 {
		return apperr.BadRequest("cpu_invalid", fmt.Sprintf("%d CPU millis is not a valid amount.", cpuMillis), "cpu_millis is thousandths of a CPU: 500 is half a CPU, 1000 is one. Use at least 50.")
	}
	return nil
}

// activeUser loads the acting user and refuses suspended accounts.
func (s *Service) activeUser(ctx context.Context, userID string) (db.User, error) {
	u, err := s.Q.GetUser(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.User{}, apperr.Unauthorized("not_logged_in", "This account does not exist.", "Log in again.")
	}
	if err != nil {
		return db.User{}, apperr.Internal(err)
	}
	if u.Status != "active" {
		return db.User{}, suspendedErr()
	}
	return u, nil
}

// checkResourceQuota verifies the sum of the user's app memory and CPU,
// with this app (excludeID, may be "") counted at mem and cpu.
func (s *Service) checkResourceQuota(ctx context.Context, u db.User, excludeID string, mem, cpu int64) error {
	sum, err := s.Q.SumAppResourcesByUser(ctx, db.SumAppResourcesByUserParams{UserID: u.ID, ID: excludeID})
	if err != nil {
		return apperr.Internal(err)
	}
	if sum.MemoryMb+mem > u.QuotaMemoryMb {
		return apperr.Forbidden("memory_quota_exceeded",
			fmt.Sprintf("This needs %d MB of memory, but your other apps already use %d MB of your %d MB quota.", mem, sum.MemoryMb, u.QuotaMemoryMb),
			"Lower the memory of this app or your other apps with update_app, delete an app, or ask a bitploy admin for a bigger quota.").WithNext("update_app")
	}
	if sum.CpuMillis+cpu > u.QuotaCpuMillis {
		return apperr.Forbidden("cpu_quota_exceeded",
			fmt.Sprintf("This needs %d CPU millis, but your other apps already use %d of your %d quota.", cpu, sum.CpuMillis, u.QuotaCpuMillis),
			"Lower cpu_millis on this app or your other apps with update_app, or ask a bitploy admin for a bigger quota.").WithNext("update_app")
	}
	return nil
}

func (s *Service) checkServerRoom(ctx context.Context, extraMB int64) error {
	ok, err := s.ServerHasRoom(ctx, extraMB)
	if err != nil {
		s.Log.Warn("server room check failed", "err", err)
		return nil
	}
	if !ok {
		return apperr.Unavailable("server_full",
			fmt.Sprintf("The server has no room for another %d MB of app memory right now.", extraMB),
			"Tell the bitploy admin that the server is full, or try a smaller memory_mb.")
	}
	return nil
}

// checkSlugFree validates a slug and checks it is not taken or reserved for someone else.
func (s *Service) checkSlugFree(ctx context.Context, slug, userID string) error {
	if e := deploy.ValidateSlug(slug); e != nil {
		return e
	}
	n, err := s.Q.AppSlugExists(ctx, slug)
	if err != nil {
		return apperr.Internal(err)
	}
	if n > 0 {
		return slugTaken(slug)
	}
	res, err := s.Q.GetSlugReservation(ctx, slug)
	if err == nil && res.ExpiresAt > s.now() && res.UserID != userID {
		return slugTaken(slug)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return apperr.Internal(err)
	}
	return nil
}

func (s *Service) appViews(ctx context.Context, a Actor, rows []db.App) []App {
	out := make([]App, 0, len(rows))
	for _, r := range rows {
		out = append(out, s.appView(ctx, a, r))
	}
	return out
}

// appView builds the public App: URL, current deployment, domains, and the
// container's start time and restart count from Docker.
func (s *Service) appView(ctx context.Context, a Actor, r db.App) App {
	v := App{
		ID: r.ID, Slug: r.Slug, Name: r.Name, Type: r.Type, OwnerID: r.UserID,
		Status: r.Status, StatusMessage: r.StatusMessage, DesiredState: r.DesiredState,
		HealthPath: r.HealthPath, DockerfilePath: r.DockerfilePath, ContextDir: r.ContextDir,
		Volume: r.VolumeEnabled != 0, MemoryMB: r.MemoryMb, CPUMillis: r.CpuMillis,
		Domains: []Domain{}, CreatedAt: ts(r.CreatedAt), UpdatedAt: ts(r.UpdatedAt),
	}
	if r.Type == "web" {
		v.URL = s.Cfg.AppURL(r.Slug)
	}
	if r.Port.Valid {
		p := r.Port.Int64
		v.Port = &p
	}
	if r.GitRepoUrl.Valid && r.GitRepoUrl.String != "" {
		v.GitSource = &GitSource{RepoURL: r.GitRepoUrl.String, Branch: r.GitBranch.String, Subdir: r.GitSubdir.String}
	}
	if r.CurrentDeploymentID.Valid {
		if d, err := s.Q.GetDeployment(ctx, r.CurrentDeploymentID.String); err == nil {
			dv := s.deploymentView(d, r.Slug, r.Type)
			v.Current = &dv
			if s.Docker != nil && r.DesiredState == "running" && d.ContainerName.Valid {
				if c, err := s.Docker.InspectContainer(ctx, d.ContainerName.String); err == nil && c.State != nil {
					v.RestartCount = int64(c.RestartCount)
					if t, err := time.Parse(time.RFC3339Nano, c.State.StartedAt); err == nil && c.State.Running {
						t = t.UTC()
						v.StartedAt = &t
					}
				}
			}
		}
	}
	if ds, err := s.ListDomains(ctx, a, r.Slug); err == nil && ds != nil {
		v.Domains = ds
	}
	return v
}
