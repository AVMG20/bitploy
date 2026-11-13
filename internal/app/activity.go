package app

// Activity (docs/decisions.md, "Activity for every user"): every user sees
// the audit rows about their own stuff on the Activity page and with
// GET /activity and list_activity. Admin > Activity log stays the full view.
//
// Who may see a row is decided when it is written: Audit stores the app, the
// project and the owner the row is about (migration 00510). A user sees a row
// when they own what it is about, when it is about a project they are a
// member of now, or when they did it themselves to an app or project (not
// as an admin). Rows about the server have no owner and never show.
//
// What they see of a row is cut down here: the IP address only of what they
// did themselves, never a token id, other people only by name when they
// share the project ("An admin" or "Another user" otherwise), and no email
// addresses or user ids of others in the details.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// auditScope is whose stuff an audit row is about.
type auditScope struct {
	AppID, ProjectID, OwnerID string
}

// auditScopeOf finds what a new audit row is about from its target. Lookups
// that fail leave the fields empty: the row then shows only in the admin log.
func (s *Service) auditScopeOf(ctx context.Context, a Actor, targetType, targetID string, details map[string]any) auditScope {
	str := func(k string) string { v, _ := details[k].(string); return v }
	switch targetType {
	case "app":
		return s.appAuditScope(ctx, targetID)
	case "deployment":
		// No fallback by slug: a deleted app's slug may belong to a new
		// app by now. Callers that may outlive the deployment row pass the
		// scope themselves (auditAbout).
		if d, err := s.Q.GetDeployment(ctx, targetID); err == nil {
			return s.appAuditScope(ctx, d.AppID)
		}
	case "domain":
		if appID, err := s.Q.GetDomainAppID(ctx, targetID); err == nil {
			return s.appAuditScope(ctx, appID)
		}
		if appID := str("app_id"); appID != "" { // removed: the details name the app
			return s.appAuditScope(ctx, appID)
		}
	case "project":
		if p, err := s.Q.GetProject(ctx, targetID); err == nil {
			return auditScope{ProjectID: p.ID, OwnerID: p.UserID}
		}
		return auditScope{ProjectID: targetID}
	case "user":
		return auditScope{OwnerID: targetID}
	case "token", "session", "github_installation":
		return auditScope{OwnerID: a.UserID}
	case "notification_channel":
		if ch, err := s.Q.GetNotificationChannel(ctx, targetID); err == nil {
			return auditScope{OwnerID: ch.UserID}
		}
		if u := str("user"); u != "" {
			return auditScope{OwnerID: u}
		}
		return auditScope{OwnerID: a.UserID}
	}
	return auditScope{}
}

func (s *Service) appAuditScope(ctx context.Context, appID string) auditScope {
	if appID == "" {
		return auditScope{}
	}
	row, err := s.Q.GetApp(ctx, appID)
	if err != nil {
		return auditScope{AppID: appID}
	}
	return appScope(row)
}

// appScope is the scope of a row about an app the caller already holds (an
// app being deleted is gone before its row is written).
func appScope(row db.App) auditScope {
	return auditScope{AppID: row.ID, ProjectID: row.ProjectID, OwnerID: row.UserID}
}

// ListActivity lists the audit rows about the caller's own stuff, newest
// first. appRef (optional) keeps those about one app; cursor is the
// next_cursor of the previous page. Any credential may read it.
func (s *Service) ListActivity(ctx context.Context, a Actor, appRef string, limit int, cursor string) (Page[ActivityEntry], error) {
	if a.UserID == "" {
		return Page[ActivityEntry]{}, apperr.Unauthorized("not_logged_in", "You are not logged in.", "Log in to the dashboard, or send an API token.")
	}
	before := int64(math.MaxInt64)
	if cursor != "" {
		n, err := strconv.ParseInt(strings.TrimPrefix(cursor, "a:"), 10, 64)
		if err != nil || !strings.HasPrefix(cursor, "a:") || n <= 0 {
			return Page[ActivityEntry]{}, apperr.BadRequest("invalid_cursor", "The cursor is not valid.", "Use the next_cursor value from the previous page, unchanged.")
		}
		before = n
	}
	limit = acctClampLimit(limit)
	me := nullStr(a.UserID)
	var rows []db.AuditLog
	var err error
	if strings.TrimSpace(appRef) != "" {
		row, lerr := s.LoadApp(ctx, a, appRef, false)
		if lerr != nil {
			return Page[ActivityEntry]{}, lerr
		}
		rows, err = s.Q.ListActivityForApp(ctx, db.ListActivityForAppParams{AppID: nullStr(row.ID), BeforeID: before, UserID: me, Lim: int64(limit) + 1})
	} else {
		rows, err = s.Q.ListActivity(ctx, db.ListActivityParams{BeforeID: before, UserID: me, Lim: int64(limit) + 1})
	}
	if err != nil {
		return Page[ActivityEntry]{}, apperr.Internal(err)
	}
	page := Page[ActivityEntry]{Items: make([]ActivityEntry, 0, len(rows))}
	if len(rows) > limit {
		rows = rows[:limit]
		page.NextCursor = "a:" + strconv.FormatInt(rows[limit-1].ID, 10)
	}
	v := s.newActivityView(a)
	for _, r := range rows {
		page.Items = append(page.Items, v.entry(ctx, r))
	}
	return page, nil
}

// activityView turns audit rows into what one user may see of them. It
// caches the users, apps and projects it looks up for one page.
type activityView struct {
	s        *Service
	a        Actor
	users    map[string]*db.User // nil: no such user (deleted)
	apps     map[string]*db.App
	projects map[string]*db.Project
	members  map[string]map[string]bool // project -> its members now
}

func (s *Service) newActivityView(a Actor) *activityView {
	return &activityView{s: s, a: a, users: map[string]*db.User{}, apps: map[string]*db.App{},
		projects: map[string]*db.Project{}, members: map[string]map[string]bool{}}
}

func (v *activityView) user(ctx context.Context, id string) *db.User {
	if u, ok := v.users[id]; ok {
		return u
	}
	var p *db.User
	if u, err := v.s.Q.GetUser(ctx, id); err == nil {
		p = &u
	}
	v.users[id] = p
	return p
}

func (v *activityView) app(ctx context.Context, id string) *db.App {
	if r, ok := v.apps[id]; ok {
		return r
	}
	var p *db.App
	if r, err := v.s.Q.GetApp(ctx, id); err == nil {
		p = &r
	}
	v.apps[id] = p
	return p
}

func (v *activityView) project(ctx context.Context, id string) *db.Project {
	if r, ok := v.projects[id]; ok {
		return r
	}
	var p *db.Project
	if r, err := v.s.Q.GetProject(ctx, id); err == nil {
		p = &r
	}
	v.projects[id] = p
	return p
}

func (v *activityView) isMember(ctx context.Context, projectID, userID string) bool {
	if projectID == "" || userID == "" {
		return false
	}
	m, ok := v.members[projectID]
	if !ok {
		m = map[string]bool{}
		if rows, err := v.s.Q.ListProjectMembers(ctx, projectID); err == nil {
			for _, r := range rows {
				m[r.UserID] = true
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			v.s.Log.Warn("activity: listing project members failed", "project", projectID, "err", err)
		}
		v.members[projectID] = m
	}
	return m[userID]
}

// shares reports whether userID is one of the people of the row's app or
// project as the viewer knows them: its owner or a member now.
func (v *activityView) shares(ctx context.Context, r db.AuditLog, userID string) bool {
	if userID == "" {
		return false
	}
	if r.OwnerID.Valid && r.OwnerID.String == userID {
		return true
	}
	if r.ProjectID.Valid {
		if p := v.project(ctx, r.ProjectID.String); p != nil && p.UserID == userID {
			return true
		}
		return v.isMember(ctx, r.ProjectID.String, userID)
	}
	return false
}

// mayOpenApp reports whether the viewer may open the app's page now.
func (v *activityView) mayOpenApp(ctx context.Context, row *db.App) bool {
	if row.UserID == v.a.UserID || v.isMember(ctx, row.ProjectID, v.a.UserID) {
		return true
	}
	return v.a.IsAdmin() && v.a.Scope == ScopeSession
}

// person is how the viewer sees another user: their name when they share
// the row's project, "An admin" for an admin, else "Another user".
func (v *activityView) person(ctx context.Context, r db.AuditLog, userID string) (kind, name string) {
	if userID == v.a.UserID {
		return actorYou, "You"
	}
	u := v.user(ctx, userID)
	switch {
	case u == nil:
		return actorOther, "A removed user"
	case v.shares(ctx, r, userID):
		if u.Name != "" {
			return actorPerson, u.Name
		}
		return actorPerson, "A member"
	case u.Role == "admin":
		return actorAdmin, "An admin"
	}
	return actorOther, "Another user"
}

func (v *activityView) entry(ctx context.Context, r db.AuditLog) ActivityEntry {
	e := ActivityEntry{ID: r.ID, TS: ts(r.Ts), Action: r.Action, Channel: r.Channel, TargetType: r.TargetType, TargetID: r.TargetID}
	// A token (an AI assistant, a script) reads less than a dashboard
	// session: no IP addresses and no session or token ids, which only a
	// session could list before.
	session := v.a.Scope == ScopeSession
	switch {
	case r.Action == "auth.login_failed":
		// Written with the account's user id, but typed by whoever tried.
		e.Actor, e.ActorName = actorOther, "Someone"
		if session {
			e.IP = r.Ip
		}
	case r.Channel == string(ChannelSystem) || !r.UserID.Valid:
		e.Actor, e.ActorName = actorSystem, "bitploy"
	default:
		e.Actor, e.ActorName = v.person(ctx, r, r.UserID.String)
	}
	if e.Actor == actorYou && session {
		e.IP = r.Ip // never anyone else's
	}
	if (r.TargetType == "user" && r.TargetID != v.a.UserID) || (!session && (r.TargetType == "session" || r.TargetType == "token")) {
		e.TargetID = ""
	}
	d := map[string]any{}
	_ = json.Unmarshal([]byte(r.Details), &d)
	e.Details = v.details(ctx, r, e.Actor == actorYou, d)

	if r.AppID.Valid {
		e.App = v.appRef(ctx, r, d)
	}
	if r.ProjectID.Valid && (r.TargetType == "project" || e.App != nil) {
		if p := v.project(ctx, r.ProjectID.String); p != nil && (p.UserID == v.a.UserID || v.isMember(ctx, p.ID, v.a.UserID) || (v.a.IsAdmin() && v.a.Scope == ScopeSession)) {
			e.Project = &ActivityProject{ID: p.ID, Name: p.Name}
		} else if name, _ := d["name"].(string); name != "" && r.TargetType == "project" {
			e.Project = &ActivityProject{Name: name, Gone: true}
		}
	}
	return e
}

// appRef names the row's app: a link while the viewer may open it, else the
// slug the row was written with.
func (v *activityView) appRef(ctx context.Context, r db.AuditLog, d map[string]any) *ActivityApp {
	if row := v.app(ctx, r.AppID.String); row != nil && v.mayOpenApp(ctx, row) {
		return &ActivityApp{ID: row.ID, Slug: row.Slug, Name: row.Name, ProjectID: row.ProjectID}
	}
	for _, k := range []string{"slug", "app", "old_slug"} {
		if slug, _ := d[k].(string); slug != "" {
			return &ActivityApp{Slug: slug, Name: slug, Gone: true}
		}
	}
	return &ActivityApp{Gone: true}
}

// details keeps what the viewer may read of a row's details: no IP
// addresses, token or user ids, and other people's emails only when the row
// is about the viewer's own account or they wrote it themselves.
func (v *activityView) details(ctx context.Context, r db.AuditLog, own bool, d map[string]any) map[string]any {
	ownAccount := r.TargetType == "user" && r.TargetID == v.a.UserID
	out := make(map[string]any, len(d))
	for k, val := range d {
		switch {
		case strings.HasSuffix(k, "_id"), k == "ip", k == "token", k == "by", k == "created_by", k == "invited_by", k == "owner":
			continue
		case (k == "email" || k == "old_email" || k == "new_email" || k == "from" || k == "to") && r.Action == "user.email_change" && v.a.Scope != ScopeSession:
			continue // addresses stay with the dashboard
		case k == "email" && (!own && !ownAccount || v.a.Scope != ScopeSession):
			continue
		case k == "user":
			id, _ := val.(string)
			if id == "" {
				continue
			}
			kind, name := v.person(ctx, r, id)
			if kind != actorYou && kind != actorPerson {
				continue
			}
			out["member"] = name
			continue
		}
		if sv, ok := val.(string); ok && strings.HasPrefix(sv, "usr_") {
			continue // a user id under another name
		}
		out[k] = val
	}
	return out
}
