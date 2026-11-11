package app

// Server settings (docs/decisions.md, "Signups and new accounts"): who can
// make an account, and the limits new accounts get. One row in
// server_settings (migration 00380), changed by an admin in Admin > Signups
// and read on every signup, so a change applies to the next one. Accounts
// that exist keep their limits.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strings"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/metrics"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// The ways an account was made (users.joined_via).
const (
	joinedSetup         = "setup"
	joinedAdminInvite   = "admin_invite"
	joinedProjectInvite = "project_invite"
	joinedOpen          = "open"
)

// serverSettings reads the settings row. The migration inserts it; should it
// be missing anyway, the defaults apply.
func (s *Service) serverSettings(ctx context.Context, q *db.Queries) (db.ServerSetting, error) {
	row, err := q.GetServerSettings(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return db.ServerSetting{
			ID: 1, Signups: SignupAnyInvite, ProjectInviteApps: 1, QuotaApps: 10, QuotaMemoryMb: 2048,
			QuotaCpuMillis: 2000, QuotaDeploysPerHour: 20, QuotaDomainsPerApp: 5, QuotaVolumeMb: 25600,
			DiskAutoCleanup: 1, DiskCleanupMinFreePercent: 10, DiskBuildsMinFreePercent: 15, DiskBuildCacheDays: 7,
			DiskWarnFreePercent: 20, RollbackVersions: 3,
		}, nil
	}
	if err != nil {
		return db.ServerSetting{}, apperr.Internal(err)
	}
	return row, nil
}

// newAccountQuota is what an account made now gets: the defaults, or for an
// account from a project invite with project_invite_apps off, no apps, memory
// or CPU of its own. Its storage stays at the default: with no apps it holds
// nothing, and 0 would mean no limit once an admin gives it apps.
func newAccountQuota(st db.ServerSetting, via string) NewAccountQuota {
	q := NewAccountQuota{
		Apps: st.QuotaApps, MemoryMB: st.QuotaMemoryMb, CPUMillis: st.QuotaCpuMillis,
		DeploysPerHour: st.QuotaDeploysPerHour, DomainsPerApp: st.QuotaDomainsPerApp, VolumeMB: st.QuotaVolumeMb,
	}
	if via == joinedProjectInvite && st.ProjectInviteApps == 0 {
		q.Apps, q.MemoryMB, q.CPUMillis = 0, 0, 0
	}
	return q
}

// serverCapacity is the server's memory, CPU and disk (of the data
// directory); 0 for what is not known.
func (s *Service) serverCapacity() ServerCapacity {
	c := ServerCapacity{CPUMillis: int64(runtime.NumCPU()) * 1000}
	if total, ok := metrics.HostMemTotal(); ok {
		c.MemoryMB = int64(total / mib)
	}
	if total, _, err := metrics.DiskUsage(s.Cfg.DataDir); err == nil {
		c.DiskMB = int64(total / mib)
	}
	return c
}

// MaxParallelBuildsLimit is the most build slots an admin can set.
const MaxParallelBuildsLimit = 16

// defaultParallelBuilds is the build slots when no admin set them:
// BITPLOY_MAX_PARALLEL_BUILDS, 2 unless set.
func (s *Service) defaultParallelBuilds() int {
	if n := s.Cfg.MaxParallelBuilds; n > 0 {
		return n
	}
	return 2
}

// parallelBuilds is the build slots of the settings row st.
func (s *Service) parallelBuilds(st db.ServerSetting) int {
	if st.MaxParallelBuilds > 0 {
		return int(st.MaxParallelBuilds)
	}
	return s.defaultParallelBuilds()
}

// maxParallelBuilds is how many builds may run at once now; the default
// when the settings cannot be read.
func (s *Service) maxParallelBuilds(ctx context.Context) int {
	st, err := s.serverSettings(ctx, s.Q)
	if err != nil {
		return s.defaultParallelBuilds()
	}
	return s.parallelBuilds(st)
}

func (s *Service) settingsView(st db.ServerSetting, c ServerCapacity) ServerSettings {
	out := ServerSettings{
		Signups:                  st.Signups,
		ProjectInviteApps:        st.ProjectInviteApps != 0,
		NewAccountQuota:          newAccountQuota(st, ""),
		MaxParallelBuilds:        s.parallelBuilds(st),
		DefaultMaxParallelBuilds: s.defaultParallelBuilds(),
		Server:                   c,
	}
	if st.UpdatedAt > 0 {
		t := ts(st.UpdatedAt)
		out.UpdatedAt = &t
	}
	return out
}

// GetServerSettings returns the settings (admin).
func (s *Service) GetServerSettings(ctx context.Context, a Actor) (ServerSettings, error) {
	if err := RequireAdmin(a); err != nil {
		return ServerSettings{}, err
	}
	st, err := s.serverSettings(ctx, s.Q)
	if err != nil {
		return ServerSettings{}, err
	}
	return s.settingsView(st, s.serverCapacity()), nil
}

func validSignups(v string) error {
	switch v {
	case SignupAdminInvites, SignupAnyInvite, SignupOpen:
		return nil
	}
	return apperr.BadRequest("invalid_signups", "signups must be admin_invites, any_invite or open.",
		"admin_invites: only people an admin invites; any_invite: project invites can also create accounts; open: anyone.")
}

// validNewAccountQuota checks the limits for new accounts like the Limits of
// a user in Admin > People, and against what the server has: a limit above
// that can never be used.
func validNewAccountQuota(q NewAccountQuota, c ServerCapacity) error {
	bad := func(msg, hint string) error { return apperr.BadRequest("invalid_quota", msg, hint) }
	for _, v := range []int64{q.Apps, q.MemoryMB, q.CPUMillis, q.DeploysPerHour, q.DomainsPerApp, q.VolumeMB} {
		if v < 0 || v > math.MaxInt32 {
			return bad("Limits must be zero or a positive whole number.", "Send whole numbers for every limit.")
		}
	}
	if q.DeploysPerHour < 1 {
		return bad("Deploys per hour must be at least 1.", "Use 1 or more; 0 apps is the way to let new accounts deploy nothing of their own.")
	}
	if q.VolumeMB < minVolumeMB {
		return bad(fmt.Sprintf("Storage must be at least %d MB.", minVolumeMB), "Give new accounts some storage, like 25600 for 25 GB.")
	}
	if c.MemoryMB > 0 && q.MemoryMB > c.MemoryMB {
		return bad(fmt.Sprintf("%d MB of memory is more than this server has (%d MB).", q.MemoryMB, c.MemoryMB), "Use at most what the server has.")
	}
	if c.CPUMillis > 0 && q.CPUMillis > c.CPUMillis {
		return bad(fmt.Sprintf("%d CPU millis is more than this server has (%d, %d cores).", q.CPUMillis, c.CPUMillis, c.CPUMillis/1000), "Use at most what the server has.")
	}
	if c.DiskMB > 0 && q.VolumeMB > c.DiskMB {
		return bad(fmt.Sprintf("%d MB of storage is more than this server's disk (%d MB).", q.VolumeMB, c.DiskMB), "Use at most what the disk holds.")
	}
	return nil
}

// UpdateServerSettings changes the settings (admin, session) and logs what
// changed. What is left out stays as it is.
func (s *Service) UpdateServerSettings(ctx context.Context, a Actor, in UpdateServerSettingsInput) (ServerSettings, error) {
	if err := RequireAdmin(a); err != nil {
		return ServerSettings{}, err
	}
	if in.Signups == nil && in.ProjectInviteApps == nil && in.NewAccountQuota == nil && in.MaxParallelBuilds == nil {
		return ServerSettings{}, apperr.BadRequest("nothing_to_change", "Send signups, project_invite_apps, new_account_quota and/or max_parallel_builds.",
			"Example: {\"signups\": \"admin_invites\"}.")
	}
	cur, err := s.serverSettings(ctx, s.Q)
	if err != nil {
		return ServerSettings{}, err
	}
	capacity := s.serverCapacity()
	next := cur
	changes := map[string]any{}
	if in.Signups != nil {
		v := strings.TrimSpace(*in.Signups)
		if err := validSignups(v); err != nil {
			return ServerSettings{}, err
		}
		if v != cur.Signups {
			changes["signups"] = map[string]any{"from": cur.Signups, "to": v}
		}
		next.Signups = v
	}
	if in.ProjectInviteApps != nil {
		v := b2i(*in.ProjectInviteApps)
		if v != cur.ProjectInviteApps {
			changes["project_invite_apps"] = map[string]any{"from": cur.ProjectInviteApps != 0, "to": *in.ProjectInviteApps}
		}
		next.ProjectInviteApps = v
	}
	if in.NewAccountQuota != nil {
		q := *in.NewAccountQuota
		if err := validNewAccountQuota(q, capacity); err != nil {
			return ServerSettings{}, err
		}
		if old := newAccountQuota(cur, ""); old != q {
			changes["new_account_quota"] = map[string]any{"from": old, "to": q}
		}
		next.QuotaApps, next.QuotaMemoryMb, next.QuotaCpuMillis = q.Apps, q.MemoryMB, q.CPUMillis
		next.QuotaDeploysPerHour, next.QuotaDomainsPerApp, next.QuotaVolumeMb = q.DeploysPerHour, q.DomainsPerApp, q.VolumeMB
	}
	if in.MaxParallelBuilds != nil {
		v := *in.MaxParallelBuilds
		if v < 1 || v > MaxParallelBuildsLimit {
			return ServerSettings{}, apperr.BadRequest("invalid_max_parallel_builds",
				fmt.Sprintf("Builds at the same time must be 1 to %d.", MaxParallelBuildsLimit),
				"Each build uses the builder's CPU and memory; 2 suits most servers.")
		}
		if old := s.parallelBuilds(cur); v != old {
			changes["max_parallel_builds"] = map[string]any{"from": old, "to": v}
		}
		next.MaxParallelBuilds = int64(v)
	}
	if len(changes) == 0 {
		return s.settingsView(cur, capacity), nil
	}
	next.UpdatedAt = s.now()
	if err := s.Q.UpdateServerSettings(ctx, db.UpdateServerSettingsParams{
		Signups: next.Signups, ProjectInviteApps: next.ProjectInviteApps,
		QuotaApps: next.QuotaApps, QuotaMemoryMb: next.QuotaMemoryMb, QuotaCpuMillis: next.QuotaCpuMillis,
		QuotaDeploysPerHour: next.QuotaDeploysPerHour, QuotaDomainsPerApp: next.QuotaDomainsPerApp, QuotaVolumeMb: next.QuotaVolumeMb,
		MaxParallelBuilds: next.MaxParallelBuilds, UpdatedAt: next.UpdatedAt,
	}); err != nil {
		return ServerSettings{}, apperr.Internal(err)
	}
	s.Audit(ctx, a, "admin.settings_update", "server", "settings", changes)
	if _, ok := changes["max_parallel_builds"]; ok {
		s.Wake() // more slots: start what waits now
	}
	return s.settingsView(next, capacity), nil
}

// GetSignupRules says what the login, invite and Members pages need to know
// (no login needed).
func (s *Service) GetSignupRules(ctx context.Context) (SignupRules, error) {
	st, err := s.serverSettings(ctx, s.Q)
	if err != nil {
		return SignupRules{}, err
	}
	return SignupRules{OpenSignup: st.Signups == SignupOpen, ProjectInviteSignup: st.Signups != SignupAdminInvites}, nil
}

// ---- every user's project invites, for an admin ----

// AdminListProjectInvites lists the open project invites of every project,
// newest first (admin).
func (s *Service) AdminListProjectInvites(ctx context.Context, a Actor) ([]AdminProjectInvite, error) {
	if err := RequireAdmin(a); err != nil {
		return nil, err
	}
	rows, err := s.Q.ListAllPendingProjectInvites(ctx, s.now())
	if err != nil {
		return nil, apperr.Internal(err)
	}
	out := make([]AdminProjectInvite, 0, len(rows))
	for _, r := range rows {
		inv := AdminProjectInvite{
			ID: r.ID, Project: ProjectRef{ID: r.ProjectID, Name: r.ProjectName}, Email: strPtr(r.Email),
			CreatedAt: ts(r.CreatedAt), ExpiresAt: ts(r.ExpiresAt),
		}
		if r.CreatedBy.Valid && r.CreatedByEmail != "" {
			inv.InvitedBy = &UserBrief{ID: r.CreatedBy.String, Name: r.CreatedByName, Email: r.CreatedByEmail}
		}
		out = append(out, inv)
	}
	return out, nil
}

// AdminRevokeProjectInvite deletes an unused invite of any project (admin).
func (s *Service) AdminRevokeProjectInvite(ctx context.Context, a Actor, id string) error {
	if err := RequireAdmin(a); err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	notFound := apperr.NotFound("invite_not_found", "There is no unused project invite with that id.",
		"Used invites cannot be revoked. List the open ones with GET /api/v1/admin/project-invites.")
	inv, err := s.Q.GetProjectInviteByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return notFound
	} else if err != nil {
		return apperr.Internal(err)
	}
	n, err := s.Q.DeleteUnusedProjectInvite(ctx, db.DeleteUnusedProjectInviteParams{ID: inv.ID, ProjectID: inv.ProjectID})
	if err != nil {
		return apperr.Internal(err)
	}
	if n == 0 {
		return notFound
	}
	s.Audit(ctx, a, "admin.project_invite_revoke", "project", inv.ProjectID, map[string]any{"invite": inv.ID, "created_by": inv.CreatedBy.String, "email": inv.Email.String})
	return nil
}

// accountOrigin is how u was made, for Admin > People; nil before it was recorded.
func (s *Service) accountOrigin(ctx context.Context, u db.User) *AccountOrigin {
	if u.JoinedVia == "" {
		return nil
	}
	o := &AccountOrigin{Via: u.JoinedVia}
	if u.JoinedBy != "" {
		if by, err := s.Q.GetUser(ctx, u.JoinedBy); err == nil {
			o.By = &UserBrief{ID: by.ID, Name: by.Name, Email: by.Email}
		}
	}
	if u.JoinedProject != "" {
		if p, err := s.Q.GetProject(ctx, u.JoinedProject); err == nil {
			o.Project = &ProjectRef{ID: p.ID, Name: p.Name}
		} else {
			o.ProjectDeleted = true
		}
	}
	return o
}
