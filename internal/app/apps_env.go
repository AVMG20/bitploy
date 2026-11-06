package app

// OWNER: deploy agent. Environment variables: encrypted at rest (AES-GCM,
// aad "env:<app id>:<name>"); the value of a hidden one is never returned.

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
	// An admin looking at another user's app sees no value: readable means
	// readable to the app's owner (and the owner's tokens).
	return s.listEnv(ctx, row.ID, row.UserID == a.UserID || a.Channel == ChannelSystem)
}

// listEnv lists the app's variables; reveal false leaves every value out.
func (s *Service) listEnv(ctx context.Context, appID string, reveal bool) ([]EnvVar, error) {
	rows, err := s.Q.ListEnvVars(ctx, appID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	out := make([]EnvVar, 0, len(rows))
	for _, r := range rows {
		ev := EnvVar{Name: r.Name, Service: r.Service, Masked: envMask, Hidden: r.Hidden != 0, UpdatedAt: ts(r.UpdatedAt)}
		// A reference names another app's variable, not a secret: it is shown.
		if v, err := s.Crypto.Open(r.ValueEnc, envAADFor(appID, r.Service, r.Name)); err == nil {
			for _, ref := range refsIn(string(v)) {
				ev.References = append(ev.References, ref.String())
			}
			if ev.References != nil {
				ev.Masked = maskAroundRefs(string(v))
			}
			if reveal && !ev.Hidden {
				val := string(v)
				ev.Value = &val
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
	return s.ChangeEnv(ctx, a, ref, EnvChange{Service: service, Set: set, Unset: unset})
}

// EnvChange is one change of an app's variables (ChangeEnv).
type EnvChange struct {
	Service string
	Set     map[string]string
	Unset   []string
	// Hidden says per name whether the value stays write-only (docs/decisions.md,
	// "Variables are readable unless hidden"). A name not in Set must be a
	// variable the app has; making a hidden one readable needs its value in
	// Set, since nobody can read the old one.
	Hidden map[string]bool
	// GuessHidden decides for a name in Set without an entry in Hidden; nil
	// keeps what the variable has (a new one is readable). A hidden variable
	// never turns readable by a guess.
	GuessHidden func(name string) bool
}

// ChangeEnv is SetServiceEnv that also says which variables are hidden.
func (s *Service) ChangeEnv(ctx context.Context, a Actor, ref string, c EnvChange) ([]EnvVar, error) {
	service, set, unset := c.Service, c.Set, c.Unset
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
	wasHidden := map[string]bool{} // the variables of this scope, by name: hidden
	for _, e := range existing {
		names[e.Service+"/"+e.Name] = true
		if e.Service == service {
			wasHidden[e.Name] = e.Hidden != 0
		}
	}
	hidden, ae := hiddenChanges(c, wasHidden)
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
		for n, h := range hidden {
			if err := q.SetEnvVarHidden(ctx, db.SetEnvVarHiddenParams{Hidden: b2i(h), AppID: row.ID, Service: service, Name: n}); err != nil {
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
	if len(hidden) > 0 {
		meta["hidden"] = hidden // names and flags, never values
	}
	if service != "" {
		meta["service"] = service
	}
	s.Audit(ctx, a, "env.set", "app", row.ID, meta)
	return s.listEnv(ctx, row.ID, true) // LoadApp with write: the owner
}

// hiddenChanges validates c.Hidden and returns the hidden flag to store per
// name, only where it changes. wasHidden are the variables the app has in
// the scope.
func hiddenChanges(c EnvChange, wasHidden map[string]bool) (map[string]bool, *apperr.Error) {
	gone := map[string]bool{}
	for _, n := range c.Unset {
		gone[n] = true
	}
	out := map[string]bool{}
	for n, h := range c.Hidden {
		_, inSet := c.Set[n]
		was, exists := wasHidden[n]
		switch {
		case gone[n]:
			return nil, apperr.BadRequest("env_conflict", "The variable "+quote(n)+" is in both unset and hidden.", "Send each name in only one of them.")
		case !inSet && !exists:
			return nil, apperr.NotFound("env_not_found", "The app has no variable "+quote(n)+".", "Set it in the same call: put the value in set and the flag in hidden.")
		case !inSet && was && !h:
			return nil, apperr.BadRequest("env_value_needed", "The value of "+quote(n)+" is hidden, so it cannot become readable without a new value.",
				"Send its value again in set, with hidden false.")
		}
		if h != was {
			out[n] = h
		}
	}
	for n := range c.Set {
		if _, given := c.Hidden[n]; given || c.GuessHidden == nil {
			continue
		}
		if h := wasHidden[n] || c.GuessHidden(n); h != wasHidden[n] {
			out[n] = h
		}
	}
	return out, nil
}
