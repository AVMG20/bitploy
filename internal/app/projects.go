package app

// Projects: spaces that hold apps (docs/compose-plan.md, "Phase 4b"). Every
// app is in exactly one project. The apps of a project share a private
// Docker network (bp-p-<id>, created once the project has two apps), reach
// each other at their short name (alias), and refer to each other's
// variables as ${{<alias>.VAR}} (references.go). A project made for one app
// (auto) goes away with its last app; a project created or renamed on
// purpose stays when it is empty.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/ids"
	"github.com/AVMG20/bitploy/internal/store/db"
)

const (
	projectPrefix      = "prj"
	maxProjectsPerUser = 100
	maxAliasLen        = 40
)

var aliasRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)

// normalizeAlias lowercases and validates a short name.
func normalizeAlias(in string) (string, *apperr.Error) {
	a := strings.ToLower(strings.TrimSpace(in))
	if !aliasRe.MatchString(a) {
		return "", apperr.BadRequest("alias_invalid", "The short name "+quote(in)+" is not allowed.",
			"Use 1 to 40 lowercase letters, digits and dashes, starting and ending with a letter or digit, like db or api.")
	}
	return a, nil
}

func aliasTaken(alias string) *apperr.Error {
	return apperr.Conflict("alias_taken", "Another app in this project already has the short name "+quote(alias)+".",
		"Pick another short name, or call get_project to see the names in use.").WithNext("get_project")
}

func projectNotFound(ref string) *apperr.Error {
	return apperr.NotFound("project_not_found", "There is no project called "+quote(ref)+" in your account.",
		"Call list_projects to see your projects, or create_project to make one.").WithNext("list_projects")
}

func validProjectName(in string) (string, *apperr.Error) {
	n := strings.TrimSpace(in)
	if n == "" || len([]rune(n)) > 80 || strings.ContainsAny(n, "\r\n\t\x00") {
		return "", apperr.BadRequest("project_name_invalid", "The project name must be 1 to 80 characters on one line.",
			"Use a short name such as \"Shop\" or \"Family photos\".")
	}
	return n, nil
}

// trimAlias cuts a default alias (from a slug or template id) to a valid one.
func trimAlias(base string) string {
	b := strings.ToLower(base)
	if len(b) > maxAliasLen-3 {
		b = b[:maxAliasLen-3]
	}
	return strings.Trim(b, "-")
}

// freeAlias returns base, or base-2, base-3, ... when that (or a name it
// gives the services, see hostNames) is taken on the project network by
// another app than exceptID.
func freeAlias(ctx context.Context, q *db.Queries, projectID, base, exceptID string, services []string) (string, error) {
	base = trimAlias(base)
	if base == "" {
		base = "app"
	}
	held, err := projectHostNames(ctx, q, projectID, exceptID)
	if err != nil {
		return "", err
	}
	for i := 1; i < 100; i++ {
		cand := base
		if i > 1 {
			cand = fmt.Sprintf("%s-%d", base, i)
		}
		if _, clash := firstClash(held, cand, services); !clash {
			return cand, nil
		}
	}
	return "", errors.New("no free alias")
}

// ---- names on the project network ----

// hostNames are the names an app takes on its project network: its alias,
// and for an app with several services <alias>-<service> for each (see
// projectAliases).
func hostNames(alias string, services []string) []string {
	out := []string{alias}
	for _, sv := range services {
		out = append(out, alias+"-"+sv)
	}
	return out
}

// heldName is a name on a project network and the app that holds it.
type heldName struct {
	app     db.App
	service string // "" for the bare alias
}

// networkServices are the service names of an app with several services
// (nil for an app with one: it only has its alias).
func networkServices(ctx context.Context, q *db.Queries, app db.App) ([]string, error) {
	if !isMulti(app) {
		return nil, nil
	}
	rows, err := q.ListAppServices(ctx, app.ID)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Name
	}
	return out, nil
}

// projectHostNames maps every name on the project network to the app that
// holds it, for the apps other than exceptID.
func projectHostNames(ctx context.Context, q *db.Queries, projectID, exceptID string) (map[string]heldName, error) {
	out := map[string]heldName{}
	if projectID == "" {
		return out, nil
	}
	apps, err := q.ListAppsByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for _, ap := range apps {
		if ap.ID == exceptID || ap.Alias == "" {
			continue
		}
		svcs, err := networkServices(ctx, q, ap)
		if err != nil {
			return nil, err
		}
		out[ap.Alias] = heldName{app: ap}
		for _, sv := range svcs {
			out[ap.Alias+"-"+sv] = heldName{app: ap, service: sv}
		}
	}
	return out, nil
}

// nameClash is a name an app would take that another app holds.
type nameClash struct {
	name    string
	service string // the app's own service that would take it, "" for its alias
	holder  heldName
}

func firstClash(held map[string]heldName, alias string, services []string) (nameClash, bool) {
	if h, ok := held[alias]; ok {
		return nameClash{name: alias, holder: h}, true
	}
	for _, sv := range services {
		n := alias + "-" + sv
		if h, ok := held[n]; ok {
			return nameClash{name: n, service: sv, holder: h}, true
		}
	}
	return nameClash{}, false
}

// checkHostNames refuses an alias (with the app's services) that would give
// a container on the project network a name another app already has: Docker's
// DNS would then answer with either container.
func checkHostNames(ctx context.Context, q *db.Queries, projectID, exceptID, alias string, services []string) error {
	held, err := projectHostNames(ctx, q, projectID, exceptID)
	if err != nil {
		return err
	}
	if c, clash := firstClash(held, alias, services); clash {
		return c.err(alias)
	}
	return nil
}

// err is 409 alias_taken, saying who holds the name.
func (c nameClash) err(alias string) *apperr.Error {
	if c.service == "" && c.holder.service == "" {
		return aliasTaken(alias)
	}
	msg := ""
	if c.service == "" {
		msg = "The short name " + quote(alias) + " is already a name on the project network: the app " + quote(c.holder.app.Alias) +
			" reaches its service " + c.holder.service + " under it."
	} else {
		other := "the short name of the app " + quote(c.holder.app.Alias)
		if c.holder.service != "" {
			other = "the name of the service " + c.holder.service + " of the app " + quote(c.holder.app.Alias)
		}
		msg = "With the short name " + quote(alias) + ", this app's service " + c.service + " would be reached as " + c.name +
			" on the project network, which is already " + other + "."
	}
	return apperr.Conflict("alias_taken", msg,
		"Pick another short name, or call get_project to see the names in use.").WithNext("get_project").
		WithDetail("name", c.name).WithDetail("app", c.holder.app.Slug)
}

// loadProject finds a project by id, or by its name among the caller's
// projects (case-insensitive). Ownership works like LoadApp.
func (s *Service) loadProject(ctx context.Context, a Actor, ref string, write bool) (db.Project, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return db.Project{}, apperr.BadRequest("project_required", "No project was given.",
			"Pass the project id from list_projects.").WithNext("list_projects")
	}
	var p db.Project
	var err error
	if strings.HasPrefix(ref, projectPrefix+"_") {
		p, err = s.Q.GetProject(ctx, ref)
	} else {
		var rows []db.Project
		rows, err = s.Q.ListProjectsByUser(ctx, a.UserID)
		if err != nil {
			return db.Project{}, apperr.Internal(err)
		}
		var hits []db.Project
		for _, r := range rows {
			if strings.EqualFold(r.Name, ref) {
				hits = append(hits, r)
			}
		}
		switch len(hits) {
		case 0:
			err = sql.ErrNoRows
		case 1:
			p = hits[0]
		default:
			return db.Project{}, apperr.Conflict("project_ambiguous", "You have several projects called "+quote(ref)+".",
				"Pass the project id from list_projects instead of the name.").WithNext("list_projects")
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return db.Project{}, projectNotFound(ref)
	}
	if err != nil {
		return db.Project{}, apperr.Internal(err)
	}
	if a.Channel == ChannelSystem {
		return p, nil
	}
	if p.UserID != a.UserID {
		if a.IsAdmin() && !write && a.Scope == ScopeSession {
			return p, nil
		}
		return db.Project{}, projectNotFound(ref)
	}
	if write {
		if err := RequireDeploy(a); err != nil {
			return db.Project{}, err
		}
	}
	return p, nil
}

func (s *Service) projectView(ctx context.Context, a Actor, p db.Project) (Project, error) {
	rows, err := s.Q.ListAppsByProject(ctx, p.ID)
	if err != nil {
		return Project{}, apperr.Internal(err)
	}
	return Project{
		ID: p.ID, Name: p.Name, CreatedAt: ts(p.CreatedAt), UpdatedAt: ts(p.UpdatedAt),
		Network: dockerx.ProjectNetworkName(p.ID), Apps: s.appViews(ctx, a, rows),
	}, nil
}

// ListProjects returns the caller's projects, newest first, with their apps.
func (s *Service) ListProjects(ctx context.Context, a Actor) ([]Project, error) {
	rows, err := s.Q.ListProjectsByUser(ctx, a.UserID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	out := make([]Project, 0, len(rows))
	for _, p := range rows {
		v, err := s.projectView(ctx, a, p)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// GetProject returns one project (by id or name) with its apps.
func (s *Service) GetProject(ctx context.Context, a Actor, ref string) (Project, error) {
	p, err := s.loadProject(ctx, a, ref, false)
	if err != nil {
		return Project{}, err
	}
	return s.projectView(ctx, a, p)
}

// CreateProject makes an empty project. It stays when it is empty.
func (s *Service) CreateProject(ctx context.Context, a Actor, in CreateProjectInput) (Project, error) {
	if err := RequireDeploy(a); err != nil {
		return Project{}, err
	}
	user, err := s.activeUser(ctx, a.UserID)
	if err != nil {
		return Project{}, err
	}
	name, ae := validProjectName(in.Name)
	if ae != nil {
		return Project{}, ae
	}
	rows, err := s.Q.ListProjectsByUser(ctx, user.ID)
	if err != nil {
		return Project{}, apperr.Internal(err)
	}
	if len(rows) >= maxProjectsPerUser {
		return Project{}, apperr.Forbidden("projects_limit", fmt.Sprintf("You already have %d projects, the maximum.", len(rows)),
			"Delete a project you no longer need with delete_project.").WithNext("list_projects")
	}
	now := s.now()
	p := db.Project{ID: ids.New(projectPrefix), UserID: user.ID, Name: name, CreatedAt: now, UpdatedAt: now}
	if err := s.Q.InsertProject(ctx, db.InsertProjectParams{ID: p.ID, UserID: p.UserID, Name: p.Name, Auto: 0, CreatedAt: now, UpdatedAt: now}); err != nil {
		return Project{}, apperr.Internal(err)
	}
	s.Audit(ctx, a, "project.create", "project", p.ID, map[string]any{"name": name})
	return s.projectView(ctx, a, p)
}

// UpdateProject renames a project. A renamed project stays when it is empty.
func (s *Service) UpdateProject(ctx context.Context, a Actor, ref string, in UpdateProjectInput) (Project, error) {
	p, err := s.loadProject(ctx, a, ref, true)
	if err != nil {
		return Project{}, err
	}
	if in.Name != nil {
		name, ae := validProjectName(*in.Name)
		if ae != nil {
			return Project{}, ae
		}
		now := s.now()
		if err := s.Store.Tx(ctx, func(q *db.Queries) error {
			if err := q.RenameProject(ctx, db.RenameProjectParams{Name: name, UpdatedAt: now, ID: p.ID}); err != nil {
				return err
			}
			return q.SetProjectManual(ctx, db.SetProjectManualParams{UpdatedAt: now, ID: p.ID})
		}); err != nil {
			return Project{}, apperr.Internal(err)
		}
		s.Audit(ctx, a, "project.update", "project", p.ID, map[string]any{"name": name, "old_name": p.Name})
		p.Name, p.UpdatedAt = name, now
	}
	return s.projectView(ctx, a, p)
}

// DeleteProject deletes a project. confirm must be its name. A project that
// still has apps is refused unless withApps, which deletes them (and their
// data) first.
func (s *Service) DeleteProject(ctx context.Context, a Actor, ref, confirm string, withApps bool) error {
	p, err := s.loadProject(ctx, a, ref, true)
	if err != nil {
		return err
	}
	if strings.TrimSpace(confirm) != p.Name {
		return apperr.BadRequest("confirm_mismatch", "The confirmation does not match the project's name, so nothing was deleted.",
			"Call delete_project again with confirm set to exactly "+quote(p.Name)+".").WithNext("delete_project")
	}
	apps, err := s.Q.ListAppsByProject(ctx, p.ID)
	if err != nil {
		return apperr.Internal(err)
	}
	if len(apps) > 0 && !withApps {
		slugs := make([]string, len(apps))
		for i, ap := range apps {
			slugs[i] = ap.Slug
		}
		return apperr.Conflict("project_not_empty",
			fmt.Sprintf("The project has %d apps: %s.", len(apps), strings.Join(slugs, ", ")),
			"Move them to another project (update_app with project), delete them, or call delete_project with with_apps=true to delete the apps and their data too.").
			WithNext("delete_project").WithDetail("apps", slugs)
	}
	for _, ap := range apps {
		if err := s.deleteApp(ctx, a, ap.ID, ap.Slug, false); err != nil {
			return err
		}
	}
	if err := s.Q.DeleteProjectRow(ctx, p.ID); err != nil {
		return apperr.Internal(err)
	}
	s.removeProjectNetwork(context.WithoutCancel(ctx), p.ID)
	meta := map[string]any{"name": p.Name}
	if len(apps) > 0 {
		meta["apps"] = len(apps)
	}
	s.Audit(ctx, a, "project.delete", "project", p.ID, meta)
	return nil
}

// ---- the project network ----

// projectAliases are the names a container of app has on its project
// network: the alias for an app with one service; <alias>-<service> for each
// service of an app with several, plus the bare alias on its public service.
func projectAliases(app db.App, spec serviceSpec) []string {
	if !isMulti(app) {
		return []string{app.Alias}
	}
	out := []string{app.Alias + "-" + spec.Name}
	if spec.Public {
		out = append(out, app.Alias)
	}
	return out
}

// projectAttach is the project network a container of app joins, or nil
// while the project has fewer than two apps.
func (s *Service) projectAttach(ctx context.Context, app db.App, spec serviceSpec) []dockerx.NetworkAttach {
	if app.ProjectID == "" || app.Alias == "" {
		return nil
	}
	n, err := s.Q.CountAppsByProject(ctx, app.ProjectID)
	if err != nil || n < 2 {
		return nil
	}
	return []dockerx.NetworkAttach{{Network: dockerx.ProjectNetworkName(app.ProjectID), Aliases: projectAliases(app, spec)}}
}

// removeProjectNetwork disconnects whatever is on a project network and
// removes it.
func (s *Service) removeProjectNetwork(ctx context.Context, projectID string) {
	dk := s.Docker
	if dk == nil || projectID == "" {
		return
	}
	name := dockerx.ProjectNetworkName(projectID)
	members, err := dk.NetworkMembers(ctx, name)
	if err != nil {
		return
	}
	for c := range members {
		_ = dk.DisconnectNetwork(ctx, name, c)
	}
	if err := dk.RemoveNetwork(ctx, name); err != nil {
		s.Log.Warn("remove project network", "network", name, "err", err)
	}
}

// wireProject brings the project network in line with the project: removed
// while it has fewer than two apps, else created with every current
// container of its apps attached under its aliases. The containers of the
// apps in realias are reattached, so a new alias applies at once.
func (s *Service) wireProject(ctx context.Context, projectID string, realias ...string) {
	dk := s.Docker
	if dk == nil || projectID == "" {
		return
	}
	apps, err := s.Q.ListAppsByProject(ctx, projectID)
	if err != nil {
		return
	}
	if len(apps) < 2 {
		s.removeProjectNetwork(ctx, projectID)
		return
	}
	name := dockerx.ProjectNetworkName(projectID)
	if err := dk.EnsureNetwork(ctx, name, dk.ProjectLabels(projectID)); err != nil {
		s.Log.Warn("create project network", "network", name, "err", dockerx.DescribeError(err))
		return
	}
	members, err := dk.NetworkMembers(ctx, name)
	if err != nil {
		return
	}
	again := map[string]bool{}
	for _, id := range realias {
		again[id] = true
	}
	for _, app := range apps {
		if !app.CurrentDeploymentID.Valid || app.DesiredState != "running" {
			continue
		}
		specs, _, err := s.appSpecs(ctx, app)
		if err != nil {
			continue
		}
		byName := map[string]serviceSpec{}
		for _, sp := range specs {
			byName[sp.Name] = sp
		}
		for svc, c := range s.currentContainers(ctx, app) {
			sp, ok := byName[svc]
			if !ok {
				sp = serviceSpec{Name: svc}
			}
			if members[c] && !again[app.ID] {
				continue
			}
			if members[c] {
				_ = dk.DisconnectNetwork(ctx, name, c)
			}
			if err := dk.ConnectNetworkAliases(ctx, name, c, projectAliases(app, sp)); err != nil && !dockerx.IsNotFound(err) {
				s.Log.Warn("connect to project network", "network", name, "container", c, "err", dockerx.DescribeError(err))
			}
		}
	}
}

// unwireApp takes the containers of an app off a project network (the app
// moved to another project).
func (s *Service) unwireApp(ctx context.Context, app db.App, projectID string) {
	dk := s.Docker
	if dk == nil || projectID == "" {
		return
	}
	name := dockerx.ProjectNetworkName(projectID)
	if cs, err := dk.ListAppContainers(ctx, app.ID); err == nil {
		for _, c := range cs {
			_ = dk.DisconnectNetwork(ctx, name, dockerx.SummaryName(c))
		}
	}
}

// reconcileProjectNetworks removes project networks whose project is gone
// or has fewer than two apps, and attaches containers that are missing.
func (s *Service) reconcileProjectNetworks(ctx context.Context, apps []db.App) {
	dk := s.Docker
	count := map[string]int{}
	for _, a := range apps {
		if a.ProjectID != "" {
			count[a.ProjectID]++
		}
	}
	if ns, err := dk.ListManagedNetworks(ctx); err == nil {
		for _, n := range ns {
			pid := n.Labels[dockerx.LabelProject]
			if pid == "" || count[pid] >= 2 {
				continue
			}
			if c, err := s.Q.CountAppsByProject(ctx, pid); err != nil || c >= 2 {
				continue // apps added after our snapshot
			}
			s.removeProjectNetwork(ctx, pid)
		}
	}
	pids := make([]string, 0, len(count))
	for pid, n := range count {
		if n >= 2 {
			pids = append(pids, pid)
		}
	}
	sort.Strings(pids)
	for _, pid := range pids {
		if ctx.Err() != nil {
			return
		}
		s.wireProject(ctx, pid)
	}
}

// ---- projects of apps ----

// newAutoProject is the project a new app gets when none is given: named
// after the app, removed with its last app.
func newAutoProject(ctx context.Context, q *db.Queries, userID, name string, now int64) (string, error) {
	id := ids.New(projectPrefix)
	return id, q.InsertProject(ctx, db.InsertProjectParams{ID: id, UserID: userID, Name: name, Auto: 1, CreatedAt: now, UpdatedAt: now})
}

// projectChange is a move to another project and/or a new alias, checked
// before UpdateApp changes anything.
type projectChange struct {
	from, to         string
	oldAlias, alias  string
	rewrite, realias bool
}

// planProjectChange validates update_app's project and alias.
func (s *Service) planProjectChange(ctx context.Context, a Actor, row db.App, in UpdateAppInput) (*projectChange, error) {
	if in.Project == nil && in.Alias == nil {
		return nil, nil
	}
	pc := &projectChange{from: row.ProjectID, to: row.ProjectID, oldAlias: row.Alias, alias: row.Alias}
	if in.Project != nil && strings.TrimSpace(*in.Project) != "" && strings.TrimSpace(*in.Project) != row.ProjectID {
		p, err := s.loadProject(ctx, a, *in.Project, true)
		if err != nil {
			return nil, err
		}
		if p.UserID != row.UserID {
			return nil, projectNotFound(*in.Project)
		}
		if p.ID != row.ProjectID {
			pc.to = p.ID
		}
	}
	if in.Alias != nil {
		al, ae := normalizeAlias(*in.Alias)
		if ae != nil {
			return nil, ae
		}
		pc.alias = al
	}
	moving := pc.to != pc.from
	if moving {
		// References would break: the apps that use this one stay behind.
		deps, err := s.dependents(ctx, row, row.Alias)
		if err != nil {
			return nil, apperr.Internal(err)
		}
		if len(deps) > 0 {
			return nil, apperr.Conflict("app_referenced",
				"Other apps in this project refer to this app's variables: "+describeDependents(deps)+".",
				"Change those variables with set_env first, or move those apps too.").WithNext("set_env").
				WithDetail("apps", dependentSlugs(deps)).WithDetail("dependents", deps)
		}
		if mine := s.appReferences(ctx, row.ID); len(mine) > 0 {
			return nil, apperr.Conflict("app_has_references",
				"This app's variables refer to "+strings.Join(mine, ", ")+" in its project, which the other project does not have.",
				"Change those variables with set_env first, then move the app.").WithNext("set_env").WithDetail("references", mine)
		}
	}
	if pc.alias == pc.oldAlias && !moving {
		return nil, nil
	}
	if in.Alias != nil || moving {
		svcs, err := networkServices(ctx, s.Q, row)
		if err != nil {
			return nil, apperr.Internal(err)
		}
		if err := checkHostNames(ctx, s.Q, pc.to, row.ID, pc.alias, svcs); err != nil {
			if in.Alias != nil || apperr.As(err).Code != "alias_taken" {
				return nil, apperr.As(err)
			}
			al, err := freeAlias(ctx, s.Q, pc.to, pc.alias, row.ID, svcs)
			if err != nil {
				return nil, apperr.Internal(err)
			}
			pc.alias = al
		}
	}
	pc.rewrite = !moving && pc.alias != pc.oldAlias
	pc.realias = pc.alias != pc.oldAlias
	return pc, nil
}

// applyProjectChange stores a planned change, rewrites references to the old
// alias, rewires the networks and restarts the apps whose variables changed.
func (s *Service) applyProjectChange(ctx context.Context, a Actor, row db.App, pc *projectChange) error {
	now := s.now()
	var rewritten []string
	err := s.Store.Tx(ctx, func(q *db.Queries) error {
		if err := q.SetAppProject(ctx, db.SetAppProjectParams{ProjectID: pc.to, Alias: pc.alias, UpdatedAt: now, ID: row.ID}); err != nil {
			return err
		}
		if pc.to != pc.from {
			// Adding an app to a project on purpose makes it a lasting one.
			if err := q.SetProjectManual(ctx, db.SetProjectManualParams{UpdatedAt: now, ID: pc.to}); err != nil {
				return err
			}
			if err := q.DeleteAutoProjectIfEmpty(ctx, pc.from); err != nil {
				return err
			}
		}
		if pc.rewrite {
			var err error
			rewritten, err = s.rewriteRefs(ctx, q, row, pc.oldAlias, pc.alias)
			return err
		}
		return nil
	})
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return aliasTaken(pc.alias)
		}
		return apperr.Internal(err)
	}
	meta := map[string]any{"slug": row.Slug}
	if pc.to != pc.from {
		meta["project"], meta["old_project"] = pc.to, pc.from
	}
	if pc.realias {
		meta["alias"], meta["old_alias"] = pc.alias, pc.oldAlias
	}
	if len(rewritten) > 0 {
		meta["rewritten"] = rewritten
	}
	s.Audit(ctx, a, "app.project", "app", row.ID, meta)

	bg := context.WithoutCancel(ctx)
	if pc.to != pc.from {
		s.unwireApp(bg, row, pc.from)
		s.wireProject(bg, pc.from)
		s.wireProject(bg, pc.to, row.ID)
	} else {
		s.wireProject(bg, pc.to, row.ID)
	}
	for _, slug := range rewritten {
		s.restartIfRunning(bg, row.UserID, slug)
	}
	return nil
}

// restartIfRunning restarts an app that runs, so changed variables apply.
// Failures are logged: the change itself is stored.
func (s *Service) restartIfRunning(ctx context.Context, userID, ref string) {
	row, err := s.Q.GetAppBySlug(ctx, ref)
	if err != nil {
		if row, err = s.Q.GetApp(ctx, ref); err != nil {
			return
		}
	}
	if row.DesiredState != "running" || !row.CurrentDeploymentID.Valid || s.Docker == nil {
		return
	}
	sys := Actor{UserID: userID, Channel: ChannelSystem, Scope: ScopeDeploy}
	if _, err := s.RestartApp(ctx, sys, row.ID); err != nil {
		s.Log.Warn("restart after a variable change", "app", row.Slug, "err", err)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// projectFields fills the project part of an App.
func (s *Service) projectFields(ctx context.Context, v *App, r db.App) {
	v.Alias = r.Alias
	v.References = s.appReferences(ctx, r.ID)
	if r.ProjectID != "" {
		v.Project.ID = r.ProjectID
		if p, err := s.Q.GetProject(ctx, r.ProjectID); err == nil {
			v.Project.Name = p.Name
		}
	}
	port := int64(0)
	if r.Template != "" {
		t := r.Template
		v.Template, v.TemplateVersion = &t, r.TemplateVersion
		if tp, ok := templateOf(r); ok {
			port = tp.Port
			v.ConnectionVars, v.MainVar = tp.ConnectionNames(), tp.MainVar
		}
	}
	if port == 0 && r.Port.Valid {
		port = r.Port.Int64
	}
	for _, sv := range v.Services { // the service the alias points at
		if port == 0 && (sv.Public || len(v.Services) == 1) && sv.Port != nil {
			port = *sv.Port
		}
	}
	if r.Alias != "" && port > 0 {
		v.InternalHost = fmt.Sprintf("%s:%d", r.Alias, port)
	}
}
