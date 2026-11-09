package app

// Notification channels (docs/decisions.md, "Notifications"): each user sets
// up places to hear about the apps they can use (their own and those of
// projects shared with them): Discord webhooks and browsers (Web Push, one
// channel per browser subscription). Every channel has
// its own list of events. The settings of a channel's type are JSON, checked
// and masked by its sender (internal/notify) and sealed at rest because they
// hold credentials. Managing channels needs a dashboard session, like tokens.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/ids"
	"github.com/AVMG20/bitploy/internal/notify"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// Event types. Emergencies are the failures.
const (
	EventDeploymentStarted   = "deployment.started"
	EventDeploymentSucceeded = "deployment.succeeded"
	EventDeploymentFailed    = "deployment.failed"
	EventDeploymentCancelled = "deployment.cancelled"
	EventDeployCommandFailed = "deploy_command.failed"
	EventBackupSucceeded     = "backup.succeeded"
	EventBackupFailed        = "backup.failed"
	EventAppCrashed          = "app.crashed"
	// eventTest is the "Send test message" button; no channel subscribes to it.
	eventTest = "test"
)

const maxNotificationChannels = 20

// notificationEvents is the list the dashboard shows, in its order.
var notificationEvents = []NotificationEvent{
	{Type: EventDeploymentStarted, Group: "deployments", Label: "Deployment started"},
	{Type: EventDeploymentSucceeded, Group: "deployments", Label: "Deployment is live", Default: true},
	{Type: EventDeploymentFailed, Group: "deployments", Label: "Deployment failed", Emergency: true, Default: true,
		Description: "The build failed, the app did not start, or its health check failed."},
	{Type: EventDeploymentCancelled, Group: "deployments", Label: "Deployment cancelled"},
	{Type: EventDeployCommandFailed, Group: "deployments", Label: "Deploy command failed", Emergency: true, Default: true,
		Description: "The before-deploy or after-deploy command of the app failed."},
	{Type: EventBackupSucceeded, Group: "backups", Label: "Backup made"},
	{Type: EventBackupFailed, Group: "backups", Label: "Backup failed", Emergency: true, Default: true},
	{Type: EventAppCrashed, Group: "app", Label: "App stopped working", Emergency: true, Default: true,
		Description: "The app crashed 5 times within 5 minutes, so bitploy stopped it, or its health check started failing."},
}

func notificationEvent(typ string) (NotificationEvent, bool) {
	for _, e := range notificationEvents {
		if e.Type == typ {
			return e, true
		}
	}
	return NotificationEvent{}, false
}

// notificationTypes are the channel types, with their names in the dashboard.
var notificationTypes = []NotificationType{{Type: notify.TypeDiscord, Name: "Discord"}, {Type: notify.TypeBrowser, Name: "Browser"}}

// destinationSender is a sender whose destination belongs to one channel
// (a browser's push endpoint): adding it again changes that channel.
type destinationSender interface {
	Destination(cfg json.RawMessage) string
}

func channelAAD(id string) string { return "notification-channel\x00" + id }

// NotificationOptions lists the events and channel types.
func (s *Service) NotificationOptions(ctx context.Context, a Actor) (NotificationOptions, error) {
	if err := RequireSession(a); err != nil {
		return NotificationOptions{}, err
	}
	out := NotificationOptions{Events: notificationEvents, Types: []NotificationType{}}
	for _, t := range notificationTypes {
		if s.notifySenders()[t.Type] == nil {
			continue
		}
		if t.Type == notify.TypeBrowser {
			if out.VAPIDPublicKey = s.vapidPublicKey(ctx); out.VAPIDPublicKey == "" {
				continue
			}
		}
		out.Types = append(out.Types, t)
	}
	return out, nil
}

// ListNotificationChannels returns the caller's channels, oldest first.
func (s *Service) ListNotificationChannels(ctx context.Context, a Actor) ([]NotificationChannel, error) {
	if err := RequireSession(a); err != nil {
		return nil, err
	}
	rows, err := s.Q.ListNotificationChannels(ctx, a.UserID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	out := make([]NotificationChannel, 0, len(rows))
	for _, r := range rows {
		out = append(out, s.channelView(r))
	}
	return out, nil
}

// CreateNotificationChannel adds a channel.
func (s *Service) CreateNotificationChannel(ctx context.Context, a Actor, in CreateNotificationChannelInput) (NotificationChannel, error) {
	if err := RequireSession(a); err != nil {
		return NotificationChannel{}, err
	}
	snd, err := s.channelSender(in.Type)
	if err != nil {
		return NotificationChannel{}, err
	}
	name, err := channelName(in.Name)
	if err != nil {
		return NotificationChannel{}, err
	}
	var events []string
	if in.Events == nil {
		for _, e := range notificationEvents {
			if e.Default {
				events = append(events, e.Type)
			}
		}
	} else if events, err = channelEvents(in.Events); err != nil {
		return NotificationChannel{}, err
	}
	cfg, err := snd.Prepare(orEmpty(in.Config), nil)
	if err != nil {
		return NotificationChannel{}, err
	}
	dest := s.destKey(snd, cfg)
	if dest != "" {
		if id, done, err := s.claimDestination(ctx, a, dest, name, events, in.Enabled, cfg); done || err != nil {
			if err != nil {
				return NotificationChannel{}, err
			}
			return s.loadChannelView(ctx, a, id)
		}
	}
	n, err := s.Q.CountNotificationChannels(ctx, a.UserID)
	if err != nil {
		return NotificationChannel{}, apperr.Internal(err)
	}
	if n >= maxNotificationChannels {
		return NotificationChannel{}, apperr.Forbidden("notification_channels_limit",
			"You have 20 notification channels, the most bitploy allows.", "Delete a channel you no longer use.")
	}
	enabled := in.Enabled == nil || *in.Enabled
	id := ids.New(ids.NotificationChannel)
	now := s.now()
	if err := s.Q.InsertNotificationChannel(ctx, db.InsertNotificationChannelParams{
		ID: id, UserID: a.UserID, Type: snd.Type(), Name: name, Enabled: b2i(enabled), Events: mustJSON(events),
		ConfigEnc: s.Crypto.Seal(cfg, channelAAD(id)), DestKey: dest, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		return NotificationChannel{}, apperr.Internal(err)
	}
	s.Audit(ctx, a, "notification_channel.create", "notification_channel", id,
		map[string]any{"name": name, "type": snd.Type(), "events": events, "enabled": enabled})
	return s.loadChannelView(ctx, a, id)
}

// UpdateNotificationChannel changes a channel. A secret left out of config
// keeps its stored value.
func (s *Service) UpdateNotificationChannel(ctx context.Context, a Actor, id string, in UpdateNotificationChannelInput) (NotificationChannel, error) {
	if err := RequireSession(a); err != nil {
		return NotificationChannel{}, err
	}
	row, err := s.loadChannel(ctx, a, id)
	if err != nil {
		return NotificationChannel{}, err
	}
	snd, err := s.channelSender(row.Type)
	if err != nil {
		return NotificationChannel{}, err
	}
	name, enabled, events := row.Name, row.Enabled != 0, parseEvents(row.Events)
	changed := []string{}
	if in.Name != nil {
		if name, err = channelName(*in.Name); err != nil {
			return NotificationChannel{}, err
		}
		changed = append(changed, "name")
	}
	if in.Enabled != nil {
		enabled = *in.Enabled
		changed = append(changed, "enabled")
	}
	if in.Events != nil {
		if events, err = channelEvents(*in.Events); err != nil {
			return NotificationChannel{}, err
		}
		changed = append(changed, "events")
	}
	sealed, dest := row.ConfigEnc, row.DestKey
	if _, ok := snd.(destinationSender); ok && len(in.Config) > 0 {
		// A browser is added from that browser (create), which also takes it
		// over from another user; it is never moved onto a channel.
		return NotificationChannel{}, apperr.BadRequest("channel_config_fixed", "The browser of a browser channel cannot be changed.",
			"Turn notifications on in the other browser instead: Settings, Notifications, This browser.")
	}
	if in.Config != nil {
		old, err := s.Crypto.Open(row.ConfigEnc, channelAAD(row.ID))
		if err != nil {
			old = nil // unreadable (a new secret key): the new settings must be complete
		}
		cfg, err := snd.Prepare(in.Config, old)
		if err != nil {
			return NotificationChannel{}, err
		}
		if old == nil || string(cfg) != string(old) {
			sealed, dest = s.Crypto.Seal(cfg, channelAAD(row.ID)), s.destKey(snd, cfg)
			changed = append(changed, "config")
		}
	}
	if err := s.Q.UpdateNotificationChannel(ctx, db.UpdateNotificationChannelParams{
		Name: name, Enabled: b2i(enabled), Events: mustJSON(events), ConfigEnc: sealed, DestKey: dest, UpdatedAt: s.now(),
		ID: row.ID, UserID: a.UserID,
	}); err != nil {
		return NotificationChannel{}, apperr.Internal(err)
	}
	s.Audit(ctx, a, "notification_channel.update", "notification_channel", row.ID,
		map[string]any{"name": name, "changed": changed, "events": events, "enabled": enabled})
	return s.loadChannelView(ctx, a, row.ID)
}

// DeleteNotificationChannel removes a channel.
func (s *Service) DeleteNotificationChannel(ctx context.Context, a Actor, id string) error {
	if err := RequireSession(a); err != nil {
		return err
	}
	row, err := s.loadChannel(ctx, a, id)
	if err != nil {
		return err
	}
	if _, err := s.Q.DeleteNotificationChannel(ctx, db.DeleteNotificationChannelParams{ID: row.ID, UserID: a.UserID}); err != nil {
		return apperr.Internal(err)
	}
	s.Audit(ctx, a, "notification_channel.delete", "notification_channel", row.ID,
		map[string]any{"name": row.Name, "type": row.Type})
	return nil
}

// TestNotificationChannel sends a test message now and waits for the
// answer. A failure is returned (422 notification_test_failed) and recorded
// on the channel like any other.
func (s *Service) TestNotificationChannel(ctx context.Context, a Actor, id string) (NotificationChannel, error) {
	if err := RequireSession(a); err != nil {
		return NotificationChannel{}, err
	}
	row, err := s.loadChannel(ctx, a, id)
	if err != nil {
		return NotificationChannel{}, err
	}
	m := notify.Message{
		Event: eventTest, Title: "Test message from bitploy", Severity: notify.Normal, Tone: notify.ToneNeutral,
		Body: "Notifications for " + row.Name + " arrive here.", URL: s.dashboardURL("/settings/notifications"), Time: s.Now(),
	}
	if err := s.deliverNotification(ctx, row.ID, m); err != nil {
		return NotificationChannel{}, apperr.Unprocessable("notification_test_failed", notify.ErrorText(err),
			"Check the channel's settings and send another test.")
	}
	return s.loadChannelView(ctx, a, row.ID)
}

// ---- helpers ----

// destKey is a keyed hash of the channel's destination, for senders where
// one destination is one channel; else "".
func (s *Service) destKey(snd notify.Sender, cfg json.RawMessage) string {
	ds, ok := snd.(destinationSender)
	if !ok {
		return ""
	}
	d := ds.Destination(cfg)
	if d == "" {
		return ""
	}
	return s.Crypto.MAC("notification-destination", snd.Type(), d)
}

// claimDestination handles a destination that is already a channel. The
// caller's own channel is updated (a browser that turns notifications on
// again) and done is true. A channel of another user is deleted: a browser
// gets the notifications of the user who turned them on last, so whoever
// used it before no longer sees theirs there.
func (s *Service) claimDestination(ctx context.Context, a Actor, dest, name string, events []string, enabledIn *bool, cfg json.RawMessage) (id string, done bool, err error) {
	rows, err := s.Q.ListNotificationChannelsByDest(ctx, dest)
	if err != nil {
		return "", false, apperr.Internal(err)
	}
	for _, r := range rows {
		if r.UserID == a.UserID {
			if done {
				continue
			}
			enabled := enabledIn == nil || *enabledIn
			if err := s.Q.UpdateNotificationChannel(ctx, db.UpdateNotificationChannelParams{
				Name: name, Enabled: b2i(enabled), Events: mustJSON(events), ConfigEnc: s.Crypto.Seal(cfg, channelAAD(r.ID)),
				DestKey: dest, UpdatedAt: s.now(), ID: r.ID, UserID: a.UserID,
			}); err != nil {
				return "", false, apperr.Internal(err)
			}
			// A new subscription: the old outcome no longer says anything.
			_ = s.Q.SetNotificationChannelSuccess(ctx, db.SetNotificationChannelSuccessParams{LastSuccessAt: r.LastSuccessAt, ID: r.ID})
			s.Audit(ctx, a, "notification_channel.update", "notification_channel", r.ID,
				map[string]any{"name": name, "changed": []string{"config"}, "events": events, "enabled": enabled})
			id, done = r.ID, true
			continue
		}
		if _, err := s.Q.DeleteNotificationChannel(ctx, db.DeleteNotificationChannelParams{ID: r.ID, UserID: r.UserID}); err != nil {
			return "", false, apperr.Internal(err)
		}
		s.Audit(ctx, System, "notification_channel.delete", "notification_channel", r.ID,
			map[string]any{"name": r.Name, "type": r.Type, "user": r.UserID, "reason": "browser_taken_over", "by": a.UserID})
	}
	return id, done, nil
}

func (s *Service) loadChannel(ctx context.Context, a Actor, id string) (db.NotificationChannel, error) {
	row, err := s.Q.GetNotificationChannel(ctx, strings.TrimSpace(id))
	if errors.Is(err, sql.ErrNoRows) || (err == nil && row.UserID != a.UserID) {
		return db.NotificationChannel{}, apperr.NotFound("notification_channel_not_found",
			"You have no notification channel with that id.", "Open Settings, Notifications to see your channels.")
	}
	if err != nil {
		return db.NotificationChannel{}, apperr.Internal(err)
	}
	return row, nil
}

func (s *Service) loadChannelView(ctx context.Context, a Actor, id string) (NotificationChannel, error) {
	row, err := s.loadChannel(ctx, a, id)
	if err != nil {
		return NotificationChannel{}, err
	}
	return s.channelView(row), nil
}

func (s *Service) channelView(r db.NotificationChannel) NotificationChannel {
	cfg := map[string]any{}
	if snd := s.notifySenders()[r.Type]; snd != nil {
		if plain, err := s.Crypto.Open(r.ConfigEnc, channelAAD(r.ID)); err == nil {
			cfg = snd.Public(plain)
		}
	}
	return NotificationChannel{
		ID: r.ID, Name: r.Name, Type: r.Type, Enabled: r.Enabled != 0, Events: parseEvents(r.Events), Config: cfg,
		LastSuccessAt: tsPtr(r.LastSuccessAt), LastError: r.LastError, LastErrorAt: tsPtr(r.LastErrorAt),
		CreatedAt: ts(r.CreatedAt), UpdatedAt: ts(r.UpdatedAt),
	}
}

func (s *Service) channelSender(typ string) (notify.Sender, error) {
	typ = strings.ToLower(strings.TrimSpace(typ))
	if snd := s.notifySenders()[typ]; snd != nil {
		return snd, nil
	}
	known := []string{}
	for _, t := range notificationTypes {
		known = append(known, t.Type)
	}
	return nil, apperr.BadRequest("notification_type_invalid", "bitploy cannot send notifications to "+quote(typ)+".",
		"Use type "+strings.Join(known, " or ")+".")
}

func channelName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 60 || strings.ContainsAny(name, "\r\n\t") {
		return "", apperr.BadRequest("channel_name_invalid", "Give the channel a name of 1 to 60 characters.",
			"Name it after where the messages go, like \"#deploys\".")
	}
	return name, nil
}

// channelEvents checks a list of event types and removes repeats.
func channelEvents(in []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, e := range in {
		e = strings.TrimSpace(e)
		if _, ok := notificationEvent(e); !ok {
			return nil, apperr.BadRequest("notification_event_invalid", "There is no notification event "+quote(e)+".",
				"Use the event types from GET /api/v1/notifications/options.")
		}
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	return out, nil
}

func parseEvents(raw string) []string {
	out := []string{}
	_ = json.Unmarshal([]byte(raw), &out)
	if out == nil {
		out = []string{}
	}
	return out
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// dashboardURL is a page of the dashboard, like "/settings".
func (s *Service) dashboardURL(path string) string {
	return strings.TrimRight(s.Cfg.PublicURL, "/") + path
}
