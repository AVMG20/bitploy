// Package app is bitploy's service layer: the only place with business rules.
// The REST API, MCP server and dashboard (through the API) all call these
// methods. Every method takes an Actor and writes an audit row for changes.
package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/config"
	"github.com/AVMG20/bitploy/internal/deploy"
	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/proxy"
	"github.com/AVMG20/bitploy/internal/store"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// Channel is the door a request came through.
type Channel string

const (
	ChannelDashboard Channel = "dashboard"
	ChannelAPI       Channel = "api"
	ChannelMCP       Channel = "mcp"
	ChannelSystem    Channel = "system" // worker, reconciler, background jobs
	ChannelGitHub    Channel = "github" // a push to a linked repository (webhook)
)

// Scope of an API token. Session (dashboard) actors have ScopeSession.
type Scope string

const (
	ScopeRead    Scope = "read"
	ScopeDeploy  Scope = "deploy"
	ScopeSession Scope = "session" // logged in via cookie: full access to own account
)

// Actor is who is calling. Doors build it from the session or token.
type Actor struct {
	UserID  string
	Role    string // user | admin
	Channel Channel
	Scope   Scope
	TokenID string // empty for sessions
	IP      string
}

func (a Actor) IsAdmin() bool { return a.Role == "admin" }

// System is the actor for background work.
var System = Actor{Channel: ChannelSystem, Scope: ScopeDeploy, Role: "admin"}

// Service holds every dependency of the service layer.
type Service struct {
	Cfg    *config.Config
	Store  *store.Store
	Q      *db.Queries
	Crypto *store.Crypto
	Docker *dockerx.Client // nil when Docker is unavailable (tests); check before use
	Routes *proxy.Writer
	Log    *slog.Logger
	Now    func() time.Time
	wakeCh chan struct{} // nudges the deploy worker when a deployment is queued
	hub    *hub          // deployment state change notifications for long-polls
	events *Broker       // live events for the dashboard (events.go)

	deployRT deployRuntime // in-memory coordination of deploy pipelines (deploy_runtime.go)
	ops      opsState      // metrics manager and domain-check settings (ops.go)
	backup   backupState   // locks and restore state of /data backups (backups.go)
	dbv      dbViewerState // helpers of the SQLite viewer that run (db_viewer.go)
	adb      adminDBState  // the admin viewer of bitploy's own database (admin_db.go)
	github   githubState   // installation token and repository caches (github.go)

	// lsRemote checks a repository (deploy.LsRemoteInfo); tests replace it so no
	// network is needed.
	lsRemote func(ctx context.Context, src deploy.GitSource) (sha, branch string, err error)
}

func New(cfg *config.Config, st *store.Store, crypto *store.Crypto, dk *dockerx.Client, routes *proxy.Writer, log *slog.Logger) *Service {
	return &Service{
		Cfg: cfg, Store: st, Q: st.Q, Crypto: crypto, Docker: dk, Routes: routes, Log: log,
		Now:      func() time.Time { return time.Now().UTC() },
		wakeCh:   make(chan struct{}, 1),
		hub:      newHub(),
		events:   newBroker(),
		lsRemote: deploy.LsRemoteInfo,
	}
}

func (s *Service) now() int64 { return s.Now().Unix() }

// Wake nudges the deploy worker (non-blocking).
func (s *Service) Wake() {
	select {
	case s.wakeCh <- struct{}{}:
	default:
	}
}

// WakeCh is read by the deploy worker.
func (s *Service) WakeCh() <-chan struct{} { return s.wakeCh }

// NotifyDeployment is called after a deployment changed: it wakes long-polls
// waiting on it and tells the owner's dashboard (live event "deployment").
func (s *Service) NotifyDeployment(id string) {
	s.hub.notify(id)
	s.publishDeployment(id)
}

// WaitDeployment blocks until the deployment changes or ctx/timeout ends.
func (s *Service) WaitDeployment(ctx context.Context, id string, d time.Duration) {
	s.hub.wait(ctx, id, d)
}

// Audit writes one audit row. details must never contain secrets.
func (s *Service) Audit(ctx context.Context, a Actor, action, targetType, targetID string, details map[string]any) {
	d := "{}"
	if len(details) > 0 {
		if b, err := json.Marshal(details); err == nil {
			d = apperr.Redact(string(b))
		}
	}
	err := s.Q.InsertAudit(ctx, db.InsertAuditParams{
		Ts:         s.now(),
		UserID:     nullStr(a.UserID),
		Channel:    string(a.Channel),
		TokenID:    nullStr(a.TokenID),
		Ip:         a.IP,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Details:    d,
	})
	if err != nil {
		s.Log.Error("audit write failed", "action", action, "err", err)
	}
}

// RequireDeploy rejects read-only tokens.
func RequireDeploy(a Actor) error {
	if a.Scope == ScopeRead {
		return apperr.Forbidden("token_read_only",
			"This token can only read; this action changes something.",
			"Ask the user to create a token with the deploy scope in the bitploy dashboard under Tokens, put it in the MCP client config and reconnect. You cannot do this yourself.").WithNext("whoami")
	}
	return nil
}

// RequireSession allows only dashboard sessions (tokens can never manage tokens or account settings).
func RequireSession(a Actor) error {
	if a.Scope != ScopeSession {
		return apperr.Forbidden("session_required",
			"This action is only available in the bitploy dashboard, not with an API token.",
			"Log in to the dashboard to do this.")
	}
	return nil
}

// RequireAdmin allows only admins.
func RequireAdmin(a Actor) error {
	if !a.IsAdmin() || a.Scope != ScopeSession {
		return apperr.Forbidden("admin_only", "Only bitploy admins can do this.", "Ask an admin.")
	}
	return nil
}

// LoadApp finds an app by slug or app ID that the actor may see.
// Admins may read every app, but only with a dashboard session (not with an
// API token of any scope). write=true additionally requires ownership
// (admins may still stop any app — callers that allow that pass write=false
// and check themselves).
func (s *Service) LoadApp(ctx context.Context, a Actor, ref string, write bool) (db.App, error) {
	ref = strings.TrimSpace(ref)
	var row db.App
	var err error
	if strings.HasPrefix(ref, "app_") {
		// IDs are case-sensitive base62; slugs are lowercase.
		row, err = s.Q.GetApp(ctx, ref)
	} else {
		ref = strings.ToLower(ref)
		row, err = s.Q.GetAppBySlug(ctx, ref)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return db.App{}, appNotFound(ref)
	}
	if err != nil {
		return db.App{}, apperr.Internal(err)
	}
	if a.Channel == ChannelSystem {
		return row, nil
	}
	if row.UserID != a.UserID {
		// Only a logged-in dashboard session may look at other users' apps:
		// a leaked admin API token (even read-only) must not expose every user.
		if a.IsAdmin() && !write && a.Scope == ScopeSession {
			return row, nil
		}
		return db.App{}, appNotFound(ref)
	}
	if write {
		if err := RequireDeploy(a); err != nil {
			return db.App{}, err
		}
	}
	return row, nil
}

func appNotFound(ref string) *apperr.Error {
	return apperr.NotFound("app_not_found",
		"There is no app called "+quote(ref)+" in your account.",
		"Call list_apps to see your apps, or create_app to make a new one.").WithNext("list_apps")
}

func quote(s string) string { return "\"" + s + "\"" }

func nullStr(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func nullInt(v int64, valid bool) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: valid} }

func ts(v int64) time.Time { return time.Unix(v, 0).UTC() }

func tsPtr(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := ts(v.Int64)
	return &t
}

func strPtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}
