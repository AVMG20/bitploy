package app

// Variable references between the apps of a project (docs/compose-plan.md,
// "Phase 4b"): a variable value may contain ${{<alias>.VAR}}, which is
// replaced when the container starts by
//   - a connection variable of a template app (DATABASE_URL of postgres,
//     computed from its own variables and its alias, see servicetpl), or
//   - a shared variable of the app with that alias (which may hold
//     references itself, up to maxRefDepth levels).
// Values are never logged or audited; errors name the variable and the
// reference only.

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"sort"
	"strings"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/servicetpl"
	"github.com/AVMG20/bitploy/internal/store/db"
)

var refRe = regexp.MustCompile(`\$\{\{\s*([a-z0-9][a-z0-9-]*)\.([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)

const maxRefDepth = 3

// varRef is one ${{alias.VAR}}.
type varRef struct{ Alias, Var string }

func (r varRef) String() string { return "${{" + r.Alias + "." + r.Var + "}}" }

// refsIn lists the references in a value, in order, without duplicates.
func refsIn(v string) []varRef {
	if !strings.Contains(v, "${{") {
		return nil
	}
	var out []varRef
	seen := map[varRef]bool{}
	for _, m := range refRe.FindAllStringSubmatch(v, -1) {
		r := varRef{m[1], m[2]}
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

func refUnknownApp(name string, r varRef) *apperr.Error {
	return apperr.Unprocessable("variable_reference_unknown",
		"The variable "+name+" refers to "+r.String()+", but no app in this project has the short name "+quote(r.Alias)+".",
		"Call get_project (or list_projects) to see the short names (alias) of the apps in the project, then fix the variable with set_env. "+
			"To add a database, call add_service.").WithNext("set_env").WithDetail("variable", name).WithDetail("reference", r.String())
}

func refUnknownVar(name string, r varRef, known []string) *apperr.Error {
	hint := "Fix the variable with set_env."
	if len(known) > 0 {
		hint = "The app " + r.Alias + " has: " + strings.Join(known, ", ") + ". Fix the variable with set_env."
	}
	return apperr.Unprocessable("variable_reference_unknown",
		"The variable "+name+" refers to "+r.String()+", but the app "+quote(r.Alias)+" has no variable "+r.Var+".",
		hint).WithNext("set_env").WithDetail("variable", name).WithDetail("reference", r.String())
}

// refTarget is an app of the project as references see it.
type refTarget struct {
	app  db.App
	tpl  *servicetpl.Template
	vars map[string]string // its decrypted shared variables
}

// names lists what can be referenced in it: connection variables first.
func (t refTarget) names() []string {
	out := []string{}
	seen := map[string]bool{}
	if t.tpl != nil {
		for _, n := range t.tpl.ConnectionNames() {
			out = append(out, n)
			seen[n] = true
		}
	}
	rest := []string{}
	for n := range t.vars {
		if !seen[n] {
			rest = append(rest, n)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// resolver resolves references for the apps of one project, loading each
// target once.
type resolver struct {
	s       *Service
	project string
	self    string // the app id the variables belong to
	targets map[string]*refTarget
}

func (s *Service) newResolver(app db.App) *resolver {
	return &resolver{s: s, project: app.ProjectID, self: app.ID, targets: map[string]*refTarget{}}
}

func (rv *resolver) target(ctx context.Context, alias string) (*refTarget, *apperr.Error) {
	if t, ok := rv.targets[alias]; ok {
		return t, nil
	}
	if rv.project == "" {
		rv.targets[alias] = nil
		return nil, nil
	}
	row, err := rv.s.Q.GetAppByProjectAlias(ctx, db.GetAppByProjectAliasParams{ProjectID: rv.project, Alias: alias})
	if errors.Is(err, sql.ErrNoRows) {
		rv.targets[alias] = nil
		return nil, nil
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	vars, ae := rv.s.sharedVars(ctx, row.ID)
	if ae != nil {
		return nil, ae
	}
	t := &refTarget{app: row, vars: vars}
	if row.Template != "" {
		if tp, ok := servicetpl.Get(row.Template); ok {
			t.tpl = &tp
		}
	}
	rv.targets[alias] = t
	return t, nil
}

// value is what ${{alias.VAR}} stands for. depth counts nested references.
func (rv *resolver) value(ctx context.Context, name string, r varRef, depth int) (string, *apperr.Error) {
	t, ae := rv.target(ctx, r.Alias)
	if ae != nil {
		return "", ae
	}
	if t == nil {
		return "", refUnknownApp(name, r)
	}
	if t.app.ID == rv.self && depth == 0 {
		return "", apperr.Unprocessable("variable_reference_self",
			"The variable "+name+" refers to "+r.String()+", which is this app itself.",
			"Refer to another app of the project, or set the value itself with set_env.").WithNext("set_env").WithDetail("variable", name)
	}
	if t.tpl != nil {
		if v, ok := t.tpl.Resolve(r.Var, t.app.Alias, t.vars); ok {
			return v, nil
		}
	}
	v, ok := t.vars[r.Var]
	if !ok {
		return "", refUnknownVar(name, r, t.names())
	}
	return rv.expand(ctx, name, v, depth+1)
}

// expand replaces every reference in v.
func (rv *resolver) expand(ctx context.Context, name, v string, depth int) (string, *apperr.Error) {
	if !strings.Contains(v, "${{") {
		return v, nil
	}
	if depth > maxRefDepth {
		return "", apperr.Unprocessable("variable_reference_loop",
			"The variable "+name+" goes through more than 3 references in a row, or references loop back to each other.",
			"Point the variable at the app that holds the value, like ${{postgres.DATABASE_URL}}, with set_env.").WithNext("set_env").WithDetail("variable", name)
	}
	var fail *apperr.Error
	out := refRe.ReplaceAllStringFunc(v, func(m string) string {
		if fail != nil {
			return ""
		}
		sm := refRe.FindStringSubmatch(m)
		val, ae := rv.value(ctx, name, varRef{sm[1], sm[2]}, depth)
		if ae != nil {
			fail = ae
			return ""
		}
		return val
	})
	if fail != nil {
		return "", fail
	}
	return out, nil
}

// resolveAll replaces the references in every value of vars, in place.
func (rv *resolver) resolveAll(ctx context.Context, vars map[string]string) *apperr.Error {
	names := make([]string, 0, len(vars))
	for n, v := range vars {
		if strings.Contains(v, "${{") {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		v, ae := rv.expand(ctx, n, vars[n], 0)
		if ae != nil {
			return ae
		}
		vars[n] = v
	}
	return nil
}

// check validates the references of values about to be set: each alias is an
// app of the project and each variable exists there.
func (rv *resolver) check(ctx context.Context, set map[string]string) *apperr.Error {
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		for _, r := range refsIn(set[n]) {
			t, ae := rv.target(ctx, r.Alias)
			if ae != nil {
				return ae
			}
			if t == nil {
				return refUnknownApp(n, r)
			}
			if t.app.ID == rv.self {
				return apperr.BadRequest("variable_reference_self",
					"The variable "+n+" refers to "+r.String()+", which is this app itself.",
					"Refer to another app of the project, or set the value itself.").WithNext("set_env").WithDetail("variable", n)
			}
			known := false
			if t.tpl != nil {
				_, known = t.tpl.Resolve(r.Var, t.app.Alias, t.vars)
			}
			if _, ok := t.vars[r.Var]; ok {
				known = true
			}
			if !known {
				return refUnknownVar(n, r, t.names())
			}
		}
	}
	return nil
}

// sharedVars decrypts the shared variables (service "") of an app.
func (s *Service) sharedVars(ctx context.Context, appID string) (map[string]string, *apperr.Error) {
	rows, err := s.Q.ListServiceEnvVars(ctx, db.ListServiceEnvVarsParams{AppID: appID, Service: ""})
	if err != nil {
		return nil, apperr.Internal(err)
	}
	out := map[string]string{}
	for _, r := range rows {
		if r.Service != "" {
			continue
		}
		v, err := s.Crypto.Open(r.ValueEnc, envAADFor(appID, "", r.Name))
		if err != nil {
			continue
		}
		out[r.Name] = string(v)
	}
	return out, nil
}

// appReferences lists the aliases the app's variables (shared and per
// service) refer to, sorted.
func (s *Service) appReferences(ctx context.Context, appID string) []string {
	rows, err := s.Q.ListEnvVars(ctx, appID)
	if err != nil {
		return []string{}
	}
	seen := map[string]bool{}
	for _, r := range rows {
		v, err := s.Crypto.Open(r.ValueEnc, envAADFor(appID, r.Service, r.Name))
		if err != nil {
			continue
		}
		for _, ref := range refsIn(string(v)) {
			seen[ref.Alias] = true
		}
	}
	out := make([]string, 0, len(seen))
	for a := range seen {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// referrers lists the apps of the project (other than app) whose variables
// refer to alias, with the variable names.
func (s *Service) referrers(ctx context.Context, app db.App, alias string) (map[string][]string, error) {
	out := map[string][]string{}
	if app.ProjectID == "" {
		return out, nil
	}
	apps, err := s.Q.ListAppsByProject(ctx, app.ProjectID)
	if err != nil {
		return nil, err
	}
	for _, other := range apps {
		if other.ID == app.ID {
			continue
		}
		rows, err := s.Q.ListEnvVars(ctx, other.ID)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			v, err := s.Crypto.Open(r.ValueEnc, envAADFor(other.ID, r.Service, r.Name))
			if err != nil {
				continue
			}
			for _, ref := range refsIn(string(v)) {
				if ref.Alias == alias {
					out[other.Slug] = append(out[other.Slug], r.Name)
					break
				}
			}
		}
	}
	return out, nil
}

// rewriteRefs replaces ${{old.X}} with ${{new.X}} in the variables of the
// project's other apps (after an alias change). It returns the slugs of the
// apps it changed.
func (s *Service) rewriteRefs(ctx context.Context, q *db.Queries, app db.App, oldAlias, newAlias string) ([]string, error) {
	apps, err := q.ListAppsByProject(ctx, app.ProjectID)
	if err != nil {
		return nil, err
	}
	var changed []string
	now := s.now()
	for _, other := range apps {
		if other.ID == app.ID {
			continue
		}
		rows, err := q.ListEnvVars(ctx, other.ID)
		if err != nil {
			return nil, err
		}
		touched := false
		for _, r := range rows {
			aad := envAADFor(other.ID, r.Service, r.Name)
			v, err := s.Crypto.Open(r.ValueEnc, aad)
			if err != nil || !strings.Contains(string(v), "${{") {
				continue
			}
			nv := refRe.ReplaceAllStringFunc(string(v), func(m string) string {
				sm := refRe.FindStringSubmatch(m)
				if sm[1] != oldAlias {
					return m
				}
				return varRef{newAlias, sm[2]}.String()
			})
			if nv == string(v) {
				continue
			}
			if err := q.UpsertEnvVar(ctx, db.UpsertEnvVarParams{
				AppID: other.ID, Service: r.Service, Name: r.Name, ValueEnc: s.Crypto.Seal([]byte(nv), aad), UpdatedAt: now,
			}); err != nil {
				return nil, err
			}
			touched = true
		}
		if touched {
			changed = append(changed, other.Slug)
		}
	}
	return changed, nil
}
