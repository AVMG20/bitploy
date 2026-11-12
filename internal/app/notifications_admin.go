package app

// Notifications for admins (docs/decisions.md, "Admin notifications"): what
// happens to the server rather than to an app. They go to the channels admins
// already have (Settings, Notifications); only admins can pick these events.
// The disk messages are in admin_disk.go.

import (
	"context"
	"strings"

	"github.com/AVMG20/bitploy/internal/notify"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// Admin-only event types (EventDiskLow is in admin_disk.go).
const (
	EventServerUpdated  = "server.updated"
	EventAccountCreated = "account.created"
)

// notifyAdmins queues m for every enabled channel of every active admin
// that gets one of events. It never blocks and never fails the caller.
func (s *Service) notifyAdmins(ctx context.Context, m notify.Message, events ...string) {
	s.notifyInit()
	ctx = context.WithoutCancel(ctx)
	ids, err := s.Q.ListAdminUserIDs(ctx)
	if err != nil {
		return
	}
	for _, uid := range ids {
		chans, err := s.Q.ListEnabledNotificationChannels(ctx, uid)
		if err != nil {
			continue
		}
		for _, c := range chans {
			if !subscribed(parseEvents(c.Events), events) {
				continue
			}
			select {
			case s.notif.queue <- notifyJob{channelID: c.ID, msg: m}:
			default:
				s.Log.Warn("notification queue full; message dropped", "channel", c.ID, "event", m.Event)
			}
		}
	}
}

// NotifyVersion is called once when bitploy starts. When the version differs
// from the one that ran last, it stores this one and tells the admins
// (server.updated). A build without a version ("dev") is never stored or
// announced, so a development server does not message on every restart.
func (s *Service) NotifyVersion(ctx context.Context) {
	v := strings.TrimSpace(s.Version)
	if v == "" || v == "dev" {
		return
	}
	st, err := s.serverSettings(ctx, s.Q)
	if err != nil || st.LastVersion == v {
		return
	}
	if err := s.Q.SetLastVersion(ctx, v); err != nil {
		s.Log.Warn("store bitploy version", "err", err)
		return
	}
	s.Log.Info("bitploy runs a new version", "from", st.LastVersion, "to", v)
	m := notify.Message{
		Event: EventServerUpdated, Time: s.Now(), Tag: "server-version", URL: s.dashboardURL("/admin"),
		Title: "bitploy was updated to " + v, Tone: notify.ToneSuccess, Severity: notify.Normal,
		Body:   "bitploy restarted on the new version. Apps kept running.",
		Fields: []notify.Field{{Name: "Version", Value: v, Inline: true}},
	}
	if st.LastVersion != "" {
		m.Fields = append(m.Fields, notify.Field{Name: "Was", Value: st.LastVersion, Inline: true})
	}
	s.notifyAdmins(ctx, m, EventServerUpdated)
}

// notifyAccountCreated tells the admins about an account made with an
// invite link or open signup (account.created).
func (s *Service) notifyAccountCreated(ctx context.Context, u db.User, r signupReq) {
	ctx = context.WithoutCancel(ctx)
	m := notify.Message{
		Event: EventAccountCreated, Time: s.Now(), Tag: "accounts", URL: s.dashboardURL("/admin"),
		Title: "New account: " + u.Name, Tone: notify.ToneNeutral, Severity: notify.Normal,
		Fields: []notify.Field{
			{Name: "Email", Value: u.Email, Inline: true},
			{Name: "Joined with", Value: s.joinedText(ctx, r), Inline: true},
		},
	}
	if u.Role == "admin" {
		m.Fields = append(m.Fields, notify.Field{Name: "Role", Value: "Admin", Inline: true})
	}
	s.notifyAdmins(ctx, m, EventAccountCreated)
}

// joinedText says how an account joined, like "Project invite to Shop from Ada".
func (s *Service) joinedText(ctx context.Context, r signupReq) string {
	var out string
	switch r.via {
	case joinedOpen:
		return "Open signup"
	case joinedAdminInvite:
		out = "Admin invite"
	case joinedProjectInvite:
		out = "Project invite"
		if p, err := s.Q.GetProject(ctx, r.project); err == nil {
			out += " to " + p.Name
		}
	default:
		return r.via
	}
	if r.by != "" {
		if u, err := s.Q.GetUser(ctx, r.by); err == nil {
			out += " from " + u.Name
		}
	}
	return out
}
