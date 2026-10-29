package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/arno/bitploy/internal/apperr"
	"github.com/arno/bitploy/internal/store/db"
)

func (s *Service) adminUser(ctx context.Context, u db.User) (AdminUser, error) {
	usage, err := s.Usage(ctx, u.ID)
	if err != nil {
		return AdminUser{}, apperr.As(err)
	}
	n, err := s.Q.CountAppsOfUser(ctx, u.ID)
	if err != nil {
		return AdminUser{}, apperr.Internal(err)
	}
	if usage.Apps < n {
		usage.Apps = n
	}
	return AdminUser{User: acctUserFromRow(u), Quota: acctQuotaFromRow(u), Usage: usage, AppCount: n}, nil
}

func (s *Service) AdminListUsers(ctx context.Context, a Actor) ([]AdminUser, error) {
	if err := RequireAdmin(a); err != nil {
		return nil, err
	}
	rows, err := s.Q.ListUsers(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	out := make([]AdminUser, 0, len(rows))
	for _, r := range rows {
		au, err := s.adminUser(ctx, r)
		if err != nil {
			return nil, err
		}
		out = append(out, au)
	}
	return out, nil
}

func acctUserNotFound() *apperr.Error {
	return apperr.NotFound("user_not_found", "There is no user with that ID.", "List users with GET /api/v1/admin/users.")
}

func (s *Service) AdminSetQuota(ctx context.Context, a Actor, userID string, q Quota) (AdminUser, error) {
	if err := RequireAdmin(a); err != nil {
		return AdminUser{}, err
	}
	for _, v := range []int64{q.Apps, q.MemoryMB, q.CPUMillis, q.DeploysPerHour, q.DomainsPerApp, q.VolumeMB} {
		if v < 0 || v > math.MaxInt32 {
			return AdminUser{}, apperr.BadRequest("invalid_quota", "Quota values must be zero or a positive number.",
				"Send non-negative whole numbers for every quota field.")
		}
	}
	if _, err := s.Q.GetUser(ctx, userID); errors.Is(err, sql.ErrNoRows) {
		return AdminUser{}, acctUserNotFound()
	} else if err != nil {
		return AdminUser{}, apperr.Internal(err)
	}
	if err := s.Q.UpdateUserQuota(ctx, db.UpdateUserQuotaParams{
		QuotaApps: q.Apps, QuotaMemoryMb: q.MemoryMB, QuotaCpuMillis: q.CPUMillis,
		QuotaDeploysPerHour: q.DeploysPerHour, QuotaDomainsPerApp: q.DomainsPerApp, QuotaVolumeMb: q.VolumeMB,
		UpdatedAt: s.now(), ID: userID,
	}); err != nil {
		return AdminUser{}, apperr.Internal(err)
	}
	s.Audit(ctx, a, "admin.quota_set", "user", userID, map[string]any{
		"apps": q.Apps, "memory_mb": q.MemoryMB, "cpu_millis": q.CPUMillis,
		"deploys_per_hour": q.DeploysPerHour, "domains_per_app": q.DomainsPerApp, "volume_mb": q.VolumeMB,
	})
	u, err := s.Q.GetUser(ctx, userID)
	if err != nil {
		return AdminUser{}, apperr.Internal(err)
	}
	return s.adminUser(ctx, u)
}

// AdminSetUserStatus suspends (stops their apps, revokes nothing) or reactivates a user.
func (s *Service) AdminSetUserStatus(ctx context.Context, a Actor, userID, status string) error {
	if err := RequireAdmin(a); err != nil {
		return err
	}
	if status != "active" && status != "suspended" {
		return apperr.BadRequest("invalid_status", "The status must be \"active\" or \"suspended\".", "Use one of those two values.")
	}
	if _, err := s.Q.GetUser(ctx, userID); errors.Is(err, sql.ErrNoRows) {
		return acctUserNotFound()
	} else if err != nil {
		return apperr.Internal(err)
	}
	if status == "suspended" && userID == a.UserID {
		return apperr.BadRequest("cannot_suspend_self", "You cannot suspend your own account.", "Ask another admin to do it.")
	}
	if err := s.Q.UpdateUserStatus(ctx, db.UpdateUserStatusParams{Status: status, UpdatedAt: s.now(), ID: userID}); err != nil {
		return apperr.Internal(err)
	}
	action := "admin.user_reactivate"
	if status == "suspended" {
		action = "admin.user_suspend"
	}
	s.Audit(ctx, a, action, "user", userID, nil)
	if status == "suspended" {
		if err := s.StopAllForUser(ctx, userID); err != nil {
			if e := apperr.As(err); e.Code == "not_implemented" {
				s.Log.Warn("stop-all is not implemented yet; user suspended without stopping apps", "user", userID)
				return nil
			}
			return apperr.As(err)
		}
	}
	return nil
}

func (s *Service) AdminListAudit(ctx context.Context, a Actor, limit int, before time.Time) ([]AuditEntry, error) {
	if err := RequireAdmin(a); err != nil {
		return nil, err
	}
	limit = acctClampLimit(limit)
	var rows []db.AuditLog
	var err error
	if before.IsZero() {
		rows, err = s.Q.ListAuditPage(ctx, db.ListAuditPageParams{ID: math.MaxInt64, Limit: int64(limit)})
	} else {
		rows, err = s.Q.ListAuditBefore(ctx, db.ListAuditBeforeParams{Ts: before.Unix(), Limit: int64(limit)})
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return acctAuditEntries(rows), nil
}

// AdminListAuditPage lists audit rows newest first, older than beforeID
// (0 = from the newest). The API uses it for cursor pagination.
func (s *Service) AdminListAuditPage(ctx context.Context, a Actor, limit int, beforeID int64) ([]AuditEntry, error) {
	if err := RequireAdmin(a); err != nil {
		return nil, err
	}
	if beforeID <= 0 {
		beforeID = math.MaxInt64
	}
	rows, err := s.Q.ListAuditPage(ctx, db.ListAuditPageParams{ID: beforeID, Limit: int64(acctClampLimit(limit))})
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return acctAuditEntries(rows), nil
}

func acctClampLimit(n int) int {
	if n <= 0 {
		return 50
	}
	if n > 100 {
		return 100
	}
	return n
}

func acctAuditEntries(rows []db.AuditLog) []AuditEntry {
	out := make([]AuditEntry, 0, len(rows))
	for _, r := range rows {
		d := map[string]any{}
		_ = json.Unmarshal([]byte(r.Details), &d)
		out = append(out, AuditEntry{
			ID: r.ID, TS: ts(r.Ts), UserID: strPtr(r.UserID), Channel: r.Channel, TokenID: strPtr(r.TokenID),
			IP: r.Ip, Action: r.Action, TargetType: r.TargetType, TargetID: r.TargetID, Details: d,
		})
	}
	return out
}
