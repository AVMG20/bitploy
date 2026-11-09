package app

// Delivering notifications. Events are put on a queue and sent by a few
// workers (RunNotifications), so a slow or broken channel never holds up a
// deployment, a backup or the crash handler. A full queue drops the message
// (logged). Each send has a time limit; senders retry on their own (Discord:
// 429 with retry_after, a few 5xx). The outcome is stored on the channel.

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/notify"
	"github.com/AVMG20/bitploy/internal/store/db"
)

const (
	notifyQueueSize = 512
	notifyWorkers   = 4
	// notifySendTimeout covers a send with its retries (Discord may ask
	// to wait up to 30 s once).
	notifySendTimeout = 75 * time.Second
	// notifyDrainTimeout is how long a stop of bitploy waits for queued
	// and running sends before it cuts them off.
	notifyDrainTimeout = 3 * time.Second
)

type notifyJob struct {
	channelID string
	msg       notify.Message
}

// notifyState is the zero-value-usable state of notifications on Service.
type notifyState struct {
	once    sync.Once
	senders notify.Registry // tests may set it before first use
	queue   chan notifyJob
}

func (s *Service) notifyInit() {
	s.notif.once.Do(func() {
		if s.notif.senders == nil {
			r := notify.Registry{}
			r.Register(&notify.Discord{})
			r.Register(&notify.WebPush{Keys: s.vapidKeys})
			s.notif.senders = r
		}
		s.notif.queue = make(chan notifyJob, notifyQueueSize)
	})
}

func (s *Service) notifySenders() notify.Registry {
	s.notifyInit()
	return s.notif.senders
}

// RunNotifications sends queued notifications until ctx ends. Then it
// sends what is left for notifyDrainTimeout and cuts off the sends still
// running, so it returns at most notifyDrainTimeout after ctx ends (main
// waits for it when bitploy stops).
func (s *Service) RunNotifications(ctx context.Context) {
	s.notifyInit()
	// Sends outlive ctx by notifyDrainTimeout at most.
	sendCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	stopped := context.AfterFunc(ctx, func() {
		t := time.AfterFunc(notifyDrainTimeout, cancel)
		<-sendCtx.Done()
		t.Stop()
	})
	defer stopped()
	var wg sync.WaitGroup
	for range notifyWorkers {
		wg.Go(func() {
			for {
				select {
				case j := <-s.notif.queue:
					s.sendQueued(sendCtx, j)
					continue
				case <-ctx.Done():
				}
				// Stopping: what is queued goes out while there is time.
				for sendCtx.Err() == nil {
					select {
					case j := <-s.notif.queue:
						s.sendQueued(sendCtx, j)
					default:
						return
					}
				}
				return
			}
		})
	}
	wg.Wait()
}

func (s *Service) sendQueued(ctx context.Context, j notifyJob) {
	defer func() {
		if r := recover(); r != nil {
			s.Log.Error("notification sender panicked", "channel", j.channelID, "panic", r)
		}
	}()
	if err := s.deliverNotification(ctx, j.channelID, j.msg); err != nil {
		s.Log.Warn("notification not sent", "channel", j.channelID, "event", j.msg.Event, "err", notify.ErrorText(err))
	}
}

// deliverNotification sends one message to one channel now and records
// the outcome on the channel. A destination that no longer exists (a
// deleted webhook, an ended browser subscription) turns the channel off.
func (s *Service) deliverNotification(ctx context.Context, channelID string, m notify.Message) error {
	row, err := s.Q.GetNotificationChannel(ctx, channelID)
	if err != nil {
		return err // deleted meanwhile
	}
	if row.Enabled == 0 && m.Event != eventTest {
		return nil // turned off meanwhile
	}
	snd := s.notifySenders()[row.Type]
	if snd == nil {
		return &notify.SendError{Msg: "bitploy cannot send to channels of type " + row.Type + "."}
	}
	cfg, err := s.Crypto.Open(row.ConfigEnc, channelAAD(row.ID))
	if err == nil {
		sctx, cancel := context.WithTimeout(ctx, notifySendTimeout)
		err = snd.Send(sctx, cfg, m)
		cancel()
	} else {
		err = &notify.SendError{Msg: "The channel's settings cannot be read (the server's secret key changed). Enter them again."}
	}
	bg := context.WithoutCancel(ctx)
	switch {
	case err == nil:
		_ = s.Q.SetNotificationChannelSuccess(bg, db.SetNotificationChannelSuccessParams{LastSuccessAt: nullInt(s.now(), true), ID: row.ID})
	case notify.IsGone(err):
		_ = s.Q.DisableNotificationChannel(bg, db.DisableNotificationChannelParams{
			LastError: apperr.Redact("Turned off: " + notify.ErrorText(err)), LastErrorAt: nullInt(s.now(), true), ID: row.ID,
		})
		s.Audit(bg, System, "notification_channel.disable", "notification_channel", row.ID,
			map[string]any{"name": row.Name, "type": row.Type, "user": row.UserID, "reason": "destination_gone"})
	default:
		_ = s.Q.SetNotificationChannelError(bg, db.SetNotificationChannelErrorParams{
			LastError: apperr.Redact(notify.ErrorText(err)), LastErrorAt: nullInt(s.now(), true), ID: row.ID,
		})
	}
	return err
}

// notifyApp queues m for every enabled channel, of every user who can use
// the app (its owner and the members of its project), that gets one of
// events. It never blocks and never fails the caller.
func (s *Service) notifyApp(ctx context.Context, app db.App, m notify.Message, events ...string) {
	s.notifyInit()
	ctx = context.WithoutCancel(ctx)
	for _, uid := range s.appAudience(ctx, app) {
		u, err := s.Q.GetUser(ctx, uid)
		if err != nil || u.Status != "active" {
			continue
		}
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

func subscribed(have, want []string) bool {
	for _, w := range want {
		for _, h := range have {
			if h == w {
				return true
			}
		}
	}
	return false
}

// ---- messages ----

// appPage is the dashboard page of an app; tab is "" or a tab like
// "deployments".
func (s *Service) appPage(app db.App, tab string) string {
	p := "/apps/" + url.PathEscape(app.Slug)
	if app.ProjectID != "" {
		p = "/projects/" + url.PathEscape(app.ProjectID) + p
	}
	if tab != "" {
		p += "/" + tab
	}
	return s.dashboardURL(p)
}

// appFields are the fields every message about an app has.
func (s *Service) appFields(ctx context.Context, app db.App) []notify.Field {
	label := app.Name
	if !strings.EqualFold(label, app.Slug) {
		label += " (" + app.Slug + ")"
	}
	f := []notify.Field{{Name: "App", Value: label, Inline: true}}
	if app.ProjectID != "" {
		if p, err := s.Q.GetProject(ctx, app.ProjectID); err == nil {
			f = append(f, notify.Field{Name: "Project", Value: p.Name, Inline: true})
		}
	}
	return f
}

// notifyDeployment sends the message about a deployment that changed:
// event is one of the deployment events. It reads the deployment as it is
// stored now, so call it after the write.
func (s *Service) notifyDeployment(ctx context.Context, depID, event string) {
	ctx = context.WithoutCancel(ctx)
	d, err := s.Q.GetDeployment(ctx, depID)
	if err != nil {
		return
	}
	app, err := s.Q.GetApp(ctx, d.AppID)
	if err != nil {
		return
	}
	m := notify.Message{Event: event, Severity: notify.Normal, Time: s.Now(), Tag: app.ID, URL: s.appPage(app, "deployments")}
	events := []string{event}
	switch event {
	case EventDeploymentStarted:
		m.Title = fmt.Sprintf("Deploying %s (deployment %d)", app.Name, d.Seq)
		m.Tone = notify.ToneProgress
	case EventDeploymentSucceeded:
		m.Title = fmt.Sprintf("%s is live (deployment %d)", app.Name, d.Seq)
		m.Tone = notify.ToneSuccess
		m.Body = s.appURL(app)
	case EventDeploymentCancelled:
		m.Title = fmt.Sprintf("Deployment %d of %s was cancelled", d.Seq, app.Name)
		m.Tone = notify.ToneNeutral
		m.Body = d.ErrorMessage.String
	case EventDeploymentFailed:
		m.Title = fmt.Sprintf("Deployment %d of %s failed", d.Seq, app.Name)
		m.Tone, m.Severity = notify.ToneFailure, notify.Emergency
		if d.ErrorCode.String == "interrupted" {
			// A restart of bitploy stopped it; the live version kept running.
			m.Severity = notify.Normal
		}
		m.Body, m.Hint = d.ErrorMessage.String, d.ErrorHint.String
		if strings.HasPrefix(d.ErrorCode.String, "before_deploy_") {
			events = append(events, EventDeployCommandFailed) // a channel with both gets one message
		}
	default:
		return
	}
	m.Fields = s.appFields(ctx, app)
	if src := s.deploymentSource(ctx, app, d); src != "" {
		m.Fields = append(m.Fields, notify.Field{Name: "Source", Value: src})
	}
	s.notifyApp(ctx, app, m, events...)
}

// notifyAfterDeployFailed sends the message about a failed after-deploy
// command; the deployment itself stays live.
func (s *Service) notifyAfterDeployFailed(ctx context.Context, depID, why string) {
	ctx = context.WithoutCancel(ctx)
	d, err := s.Q.GetDeployment(ctx, depID)
	if err != nil {
		return
	}
	app, err := s.Q.GetApp(ctx, d.AppID)
	if err != nil {
		return
	}
	m := notify.Message{
		Event: EventDeployCommandFailed, Severity: notify.Emergency, Tone: notify.ToneFailure, Time: s.Now(), Tag: app.ID,
		Title: fmt.Sprintf("After-deploy command of %s failed (deployment %d)", app.Name, d.Seq),
		Body:  why, URL: s.appPage(app, "deployments"),
		Hint:   "Read the lines marked [after-deploy] in the deploy log, then fix the command or the code. It runs again on the next deploy.",
		Fields: s.appFields(ctx, app),
	}
	s.notifyApp(ctx, app, m, EventDeployCommandFailed)
}

// deploymentSource says in a few words what a deployment deploys.
func (s *Service) deploymentSource(ctx context.Context, app db.App, d db.Deployment) string {
	switch d.SourceKind {
	case "git":
		out := "Git"
		if d.SourceRef != "" {
			out += ", branch " + d.SourceRef
		}
		if d.CommitSha.Valid {
			out += ", commit " + shortSHA(d.CommitSha.String)
		}
		if msg := firstLine(d.CommitMessage.String); msg != "" {
			out += ": " + msg
		}
		return out
	case "rollback":
		if t, err := s.Q.GetDeployment(ctx, d.SourceRef); err == nil && t.AppID == app.ID {
			return fmt.Sprintf("Rollback to deployment %d", t.Seq)
		}
		return "Rollback"
	case "upload", "files":
		return "Uploaded files"
	}
	return ""
}

// notifyCrash sends the message about an app that crashed so often that
// bitploy stopped it (metrics.Manager.Crashed).
func (s *Service) notifyCrash(ctx context.Context, appID, msg string) {
	ctx = context.WithoutCancel(ctx)
	app, err := s.Q.GetApp(ctx, appID)
	if err != nil {
		return
	}
	// "Crashed: the app stopped 5 times in 5 minutes, so bitploy stopped it.
	// Fix the error in the log below ..." keeps its first sentence.
	body, _, _ := strings.Cut(msg, " Fix the error")
	body, _, _ = strings.Cut(body, "\n")
	body = strings.TrimPrefix(body, "Crashed: ")
	if body != "" {
		body = strings.ToUpper(body[:1]) + body[1:]
	}
	m := notify.Message{
		Event: EventAppCrashed, Severity: notify.Emergency, Tone: notify.ToneFailure, Time: s.Now(), Tag: app.ID,
		Title: fmt.Sprintf("%s crashed and was stopped", app.Name), Body: strings.TrimSpace(body),
		Hint: "Read the app's logs, fix the error and deploy again, or start the app again.",
		URL:  s.appPage(app, "logs"), Fields: s.appFields(ctx, app),
	}
	s.notifyApp(ctx, app, m, EventAppCrashed)
}

// notifyUnhealthy tells the app's users that its HEALTHCHECK started
// failing while it ran. It goes to the channels of app.crashed: both mean
// the app stopped working.
func (s *Service) notifyUnhealthy(ctx context.Context, appID, msg string) {
	ctx = context.WithoutCancel(ctx)
	app, err := s.Q.GetApp(ctx, appID)
	if err != nil {
		return
	}
	body := strings.TrimPrefix(msg, "Health check failing: ")
	if body != "" {
		body = strings.ToUpper(body[:1]) + body[1:] + "."
	}
	m := notify.Message{
		Event: EventAppCrashed, Severity: notify.Emergency, Tone: notify.ToneFailure, Time: s.Now(), Tag: app.ID,
		Title: fmt.Sprintf("%s fails its health check", app.Name), Body: body,
		Hint: "The app may not answer its visitors. Read its logs and fix the error, then deploy again or restart it.",
		URL:  s.appPage(app, "logs"), Fields: s.appFields(ctx, app),
	}
	s.notifyApp(ctx, app, m, EventAppCrashed)
}

// notifyBackup sends the message about a finished backup of an app: made
// (err nil) or failed. Backups made before a restore are left out.
func (s *Service) notifyBackup(ctx context.Context, appID, backupID string) {
	ctx = context.WithoutCancel(ctx)
	app, err := s.Q.GetApp(ctx, appID)
	if err != nil {
		return
	}
	b, err := s.Q.GetBackup(ctx, db.GetBackupParams{ID: backupID, AppID: appID})
	if err != nil || b.Kind == BackupBeforeRestore || b.Interrupted != 0 {
		return
	}
	m := notify.Message{Time: s.Now(), Tag: app.ID, URL: s.appPage(app, "storage"), Fields: s.appFields(ctx, app)}
	kind := "Automatic"
	if b.Kind == BackupManual {
		kind = "Manual"
	}
	m.Fields = append(m.Fields, notify.Field{Name: "Backup", Value: kind, Inline: true})
	if isMulti(app) && b.Volume != "" {
		m.Fields = append(m.Fields, notify.Field{Name: "Volume", Value: b.Volume, Inline: true})
	}
	switch b.Status {
	case backupReady:
		m.Event, m.Severity, m.Tone = EventBackupSucceeded, notify.Normal, notify.ToneSuccess
		m.Title = fmt.Sprintf("Backup of %s made (%s)", app.Name, bytesText(b.SizeBytes))
	case backupFailed:
		m.Event, m.Severity, m.Tone = EventBackupFailed, notify.Emergency, notify.ToneFailure
		m.Title = fmt.Sprintf("Backup of %s failed", app.Name)
		m.Body = b.Error
	default:
		return
	}
	s.notifyApp(ctx, app, m, m.Event)
}

// bytesText is a size in plain words: 512 KB, 12.3 MB, 1.2 GB.
func bytesText(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%d KB", (n+1023)/1024)
	}
}
