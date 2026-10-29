package app

// OWNER: deploy agent. Environment variables: encrypted at rest (AES-GCM,
// aad "env:<app id>:<name>"), values are never returned.

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/arno/bitploy/internal/apperr"
	"github.com/arno/bitploy/internal/store/db"
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
		out = append(out, EnvVar{Name: r.Name, Masked: envMask, UpdatedAt: ts(r.UpdatedAt)})
	}
	return out, nil
}

func reservedEnvName(n string) bool {
	u := strings.ToUpper(n)
	return u == "PORT" || strings.HasPrefix(u, "BITPLOY_")
}

// SetEnv sets and unsets env vars; they apply on the next deploy or restart.
func (s *Service) SetEnv(ctx context.Context, a Actor, ref string, set map[string]string, unset []string) ([]EnvVar, error) {
	row, err := s.LoadApp(ctx, a, ref, true)
	if err != nil {
		return nil, err
	}
	for n, v := range set {
		if !envNameRe.MatchString(n) {
			return nil, apperr.BadRequest("env_name_invalid", "The variable name "+quote(n)+" is not valid.",
				"Use letters, digits and underscores, not starting with a digit, for example DATABASE_URL.")
		}
		if reservedEnvName(n) {
			return nil, apperr.BadRequest("env_name_reserved", "The variable "+quote(n)+" is set by bitploy itself.",
				"PORT, BITPLOY_APP and BITPLOY_URL are injected into every app. Pick another name.")
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
	existing, err := s.Q.ListEnvVars(ctx, row.ID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	names := map[string]bool{}
	for _, e := range existing {
		names[e.Name] = true
	}
	for n := range set {
		names[n] = true
	}
	for _, n := range unset {
		delete(names, n)
	}
	if len(names) > maxEnvVars {
		return nil, apperr.BadRequest("env_too_many", "An app can have at most 100 environment variables.", "Remove variables you do not need with unset.")
	}

	now := s.now()
	err = s.Store.Tx(ctx, func(q *db.Queries) error {
		for n, v := range set {
			if err := q.UpsertEnvVar(ctx, db.UpsertEnvVarParams{
				AppID: row.ID, Name: n, ValueEnc: s.Crypto.Seal([]byte(v), envAAD(row.ID, n)), UpdatedAt: now,
			}); err != nil {
				return err
			}
		}
		for _, n := range unset {
			if err := q.DeleteEnvVar(ctx, db.DeleteEnvVarParams{AppID: row.ID, Name: n}); err != nil {
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
	s.Audit(ctx, a, "env.set", "app", row.ID, map[string]any{"set": setNames, "unset": unset})
	return s.listEnv(ctx, row.ID)
}
