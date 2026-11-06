package app

// OWNER: deploy agent. Environment variables: encrypted at rest (AES-GCM,
// aad "env:<app id>:<name>"), values are never returned.

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/store/db"
)

const (
	maxEnvVars     = 100
	maxEnvValueLen = 32 << 10
	envMask        = "••••••••"
)

var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

func (s *Service) ListEnv(ctx context.Context, a Actor, ref string) ([]EnvVar, error) {
	row, err := s.LoadApp(ctx, a, ref, false)
	if err != nil {
		return nil, err
	}
	return s.listEnv(ctx, row.ID)
}

func (s *Service) listEnv(ctx context.Context, appID string) ([]EnvVar, error) {
	rows, err := s.Q.ListEnvVars(ctx, appID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	out := make([]EnvVar, 0, len(rows))
	for _, r := range rows {
		ev := EnvVar{Name: r.Name, Service: r.Service, Masked: envMask, Build: r.Build, UpdatedAt: ts(r.UpdatedAt)}
		// A reference names another app's variable, not a secret: it is shown.
		if v, err := s.Crypto.Open(r.ValueEnc, envAADFor(appID, r.Service, r.Name)); err == nil {
			for _, ref := range refsIn(string(v)) {
				ev.References = append(ev.References, ref.String())
			}
			if ev.References != nil {
				ev.Masked = maskAroundRefs(string(v))
			}
		}
		out = append(out, ev)
	}
	return out, nil
}

// maskAroundRefs keeps the references of a value and masks the text around
// them, so "http://${{api.HOST}}:8080" reads "••••${{api.HOST}}••••": the
// list shows where the reference sits without showing the rest.
func maskAroundRefs(v string) string {
	var b strings.Builder
	last := 0
	for _, m := range refRe.FindAllStringIndex(v, -1) {
		if m[0] > last {
			b.WriteString("••••")
		}
		b.WriteString(v[m[0]:m[1]])
		last = m[1]
	}
	if last < len(v) {
		b.WriteString("••••")
	}
	return b.String()
}

func reservedEnvName(n string) bool {
	u := strings.ToUpper(n)
	return u == "PORT" || strings.HasPrefix(u, "BITPLOY_")
}

// Values of EnvVar.Build: the image build gets the variable too.
const (
	// BuildPublic: a --build-arg (deploy.BuildSpec.Args), and an ARG before
	// the build script in bitploy's own Node builds.
	BuildPublic = "public"
	// BuildSecret: a BuildKit secret (deploy.BuildSpec.Secrets).
	BuildSecret = "secret"
)

// SetEnv sets and unsets the app's shared env vars; they apply on the next
// deploy or restart.
func (s *Service) SetEnv(ctx context.Context, a Actor, ref string, set map[string]string, unset []string) ([]EnvVar, error) {
	return s.SetServiceEnv(ctx, a, ref, "", set, unset)
}

// SetServiceEnv sets and unsets env vars of one service of an app with
// several services ("" for the variables every service gets). A service's
// own variable wins over a shared one of the same name. A single-service app
// accepts its own service name (app) and stores shared variables.
func (s *Service) SetServiceEnv(ctx context.Context, a Actor, ref, service string, set map[string]string, unset []string) ([]EnvVar, error) {
	return s.SetServiceEnvBuild(ctx, a, ref, service, set, unset, nil)
}

// SetServiceEnvBuild is SetServiceEnv that also says, per variable, whether
// the image build gets it (docs/decisions.md, "Build-time variables are app
// variables with a build flag"): build maps a name in set, or one the app
// already has, to BuildPublic, BuildSecret or "none" (also "") for only the
// running app. A variable keeps its flag when its value changes; a new one
// starts without. The build sees the change on the next deploy.
func (s *Service) SetServiceEnvBuild(ctx context.Context, a Actor, ref, service string, set map[string]string, unset []string, build map[string]string) ([]EnvVar, error) {
	row, err := s.LoadApp(ctx, a, ref, true)
	if err != nil {
		return nil, err
	}
	service = strings.TrimSpace(service)
	if service != "" {
		if _, _, err := s.pickService(ctx, row, service); err != nil {
			return nil, err
		}
		if !isMulti(row) {
			service = ""
		}
	}
	for n, v := range set {
		if !envNameRe.MatchString(n) {
			return nil, apperr.BadRequest("env_name_invalid", "The variable name "+quote(n)+" is not valid.",
				"Use letters, digits and underscores, not starting with a digit, for example DATABASE_URL.")
		}
		if reservedEnvName(n) {
			return nil, apperr.BadRequest("env_name_reserved", "The variable "+quote(n)+" is set by bitploy itself.",
				"bitploy sets PORT, BITPLOY_APP and BITPLOY_URL in every app. Pick another name.")
		}
		if len(v) > maxEnvValueLen || strings.ContainsRune(v, 0) {
			return nil, apperr.BadRequest("env_value_invalid", "The value of "+quote(n)+" is too long (max 32 KB) or contains a NUL byte.", "Store big files elsewhere and keep only a short value here.")
		}
	}
	for _, n := range unset {
		if _, both := set[n]; both {
			return nil, apperr.BadRequest("env_conflict", "The variable "+quote(n)+" is in both set and unset.", "Send each name in only one of them.")
		}
	}
	// ${{alias.VAR}}: the app and the variable must exist in the project.
	withRefs := map[string]string{}
	for n, v := range set {
		if strings.Contains(v, "${{") {
			withRefs[n] = v
		}
	}
	if len(withRefs) > 0 {
		if ae := s.newResolver(row).check(ctx, withRefs); ae != nil {
			return nil, ae
		}
	}
	existing, err := s.Q.ListEnvVars(ctx, row.ID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	names := map[string]bool{}
	for _, e := range existing {
		names[e.Service+"/"+e.Name] = true
	}
	flags, ae := checkBuildFlags(build, set, unset, func(n string) bool { return names[service+"/"+n] })
	if ae != nil {
		return nil, ae
	}
	for n := range set {
		names[service+"/"+n] = true
	}
	for _, n := range unset {
		delete(names, service+"/"+n)
	}
	if len(names) > maxEnvVars {
		return nil, apperr.BadRequest("env_too_many", "An app can have at most 100 environment variables.", "Remove variables you do not need with unset.")
	}

	now := s.now()
	err = s.Store.Tx(ctx, func(q *db.Queries) error {
		for n, v := range set {
			if err := q.UpsertEnvVar(ctx, db.UpsertEnvVarParams{
				AppID: row.ID, Service: service, Name: n, ValueEnc: s.Crypto.Seal([]byte(v), envAADFor(row.ID, service, n)), UpdatedAt: now,
			}); err != nil {
				return err
			}
		}
		for _, n := range unset {
			if err := q.DeleteEnvVar(ctx, db.DeleteEnvVarParams{AppID: row.ID, Service: service, Name: n}); err != nil {
				return err
			}
		}
		for n, f := range flags {
			if _, err := q.SetEnvVarBuild(ctx, db.SetEnvVarBuildParams{Build: f, UpdatedAt: now, AppID: row.ID, Service: service, Name: n}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, apperr.Internal(err)
	}
	setNames := make([]string, 0, len(set))
	for n := range set {
		setNames = append(setNames, n)
	}
	sort.Strings(setNames)
	meta := map[string]any{"set": setNames, "unset": unset}
	if len(flags) > 0 {
		meta["build"] = flags // names and flags, never values
	}
	if service != "" {
		meta["service"] = service
	}
	s.Audit(ctx, a, "env.set", "app", row.ID, meta)
	return s.listEnv(ctx, row.ID)
}

// checkBuildFlags validates the build argument of SetServiceEnvBuild and
// returns it with "none" as "". exists tells a variable the app already has.
func checkBuildFlags(build, set map[string]string, unset []string, exists func(string) bool) (map[string]string, *apperr.Error) {
	if len(build) == 0 {
		return nil, nil
	}
	gone := map[string]bool{}
	for _, n := range unset {
		gone[n] = true
	}
	out := make(map[string]string, len(build))
	for n, f := range build {
		f = strings.ToLower(strings.TrimSpace(f))
		switch f {
		case BuildPublic, BuildSecret:
		case "", "none":
			f = ""
		default:
			return nil, apperr.BadRequest("env_build_invalid", "The build setting "+quote(f)+" of "+quote(n)+" is not valid.",
				"Use public (the build may show it, like VITE_API_URL), secret (an API key the build needs) or none (only the running app gets it).")
		}
		_, inSet := set[n]
		switch {
		case gone[n]:
			return nil, apperr.BadRequest("env_conflict", "The variable "+quote(n)+" is in both unset and build.", "Send each name in only one of them.")
		case !inSet && !exists(n):
			return nil, apperr.NotFound("env_not_found", "The app has no variable "+quote(n)+".",
				"Set it in the same call: put the value in set and the build setting in build.")
		}
		out[n] = f
	}
	return out, nil
}
