package app

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/notify"
)

const (
	testHookID    = "112233445566778899"
	testHookToken = "tok3n-SECRET_value-abcdefghijklmnop"
	testHookURL   = "https://discord.com/api/webhooks/" + testHookID + "/" + testHookToken
)

// recordingDiscord validates and masks like the Discord sender but records
// what it would send; err is what Send returns.
type recordingDiscord struct {
	notify.Discord
	mu   sync.Mutex
	sent []notify.Message
	err  error
}

func (r *recordingDiscord) Send(_ context.Context, _ json.RawMessage, m notify.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, m)
	return r.err
}

func (r *recordingDiscord) messages() []notify.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]notify.Message(nil), r.sent...)
}

func withRecorder(s *Service) *recordingDiscord {
	r := &recordingDiscord{}
	reg := notify.Registry{}
	reg.Register(r)
	s.notif.senders = reg
	return r
}

func (m *membersEnv) channel(t *testing.T, a Actor, name string, events ...string) NotificationChannel {
	t.Helper()
	in := CreateNotificationChannelInput{Type: "discord", Name: name, Config: map[string]any{"webhook_url": testHookURL}}
	if events != nil {
		in.Events = events
	}
	c, err := m.s.CreateNotificationChannel(context.Background(), a, in)
	if err != nil {
		t.Fatalf("CreateNotificationChannel: %v", err)
	}
	return c
}

// queued takes everything off the queue: channel id → message.
func queued(s *Service) map[string]notify.Message {
	out := map[string]notify.Message{}
	for {
		select {
		case j := <-s.notif.queue:
			out[j.channelID] = j.msg
		default:
			return out
		}
	}
}

func TestNotificationChannelCRUD(t *testing.T) {
	m := newMembersEnv(t)
	withRecorder(m.s)
	ctx := context.Background()

	c := m.channel(t, m.alice, "#deploys")
	if !strings.HasPrefix(c.ID, "nch_") || !c.Enabled || c.Type != "discord" {
		t.Fatalf("channel = %+v", c)
	}
	want := []string{EventDeploymentSucceeded, EventDeploymentFailed, EventDeployCommandFailed, EventBackupFailed, EventAppCrashed}
	if strings.Join(c.Events, ",") != strings.Join(want, ",") {
		t.Errorf("default events = %v, want %v", c.Events, want)
	}
	if c.Config["webhook_id"] != testHookID || c.Config["webhook_url_set"] != true {
		t.Errorf("config = %v", c.Config)
	}
	raw, _ := json.Marshal(c)
	if strings.Contains(string(raw), testHookToken) {
		t.Fatalf("the webhook token is in the API view: %s", raw)
	}

	// Validation.
	for _, in := range []CreateNotificationChannelInput{
		{Type: "discord", Name: "x", Config: map[string]any{"webhook_url": "https://evil.example/api/webhooks/1/abc"}},
		{Type: "discord", Name: "x", Config: map[string]any{"webhook_url": "http://discord.com/api/webhooks/1/abc"}},
		{Type: "discord", Name: "x"},
		{Type: "slack", Name: "x", Config: map[string]any{"webhook_url": testHookURL}},
		{Type: "discord", Name: "", Config: map[string]any{"webhook_url": testHookURL}},
		{Type: "discord", Name: "x", Events: []string{"deployment.exploded"}, Config: map[string]any{"webhook_url": testHookURL}},
	} {
		if _, err := m.s.CreateNotificationChannel(ctx, m.alice, in); depCode(err) == "" || !strings.Contains("webhook_url_invalid webhook_url_required notification_type_invalid channel_name_invalid notification_event_invalid", depCode(err)) {
			t.Errorf("create %+v: %v", in, err)
		}
	}
	tok := m.alice
	tok.Channel, tok.Scope = ChannelAPI, ScopeDeploy
	if _, err := m.s.ListNotificationChannels(ctx, tok); err == nil {
		t.Error("a token listed the channels")
	}

	// Editing without a URL keeps it; the stored config is sealed.
	name := "#ops"
	none := []string{}
	u, err := m.s.UpdateNotificationChannel(ctx, m.alice, c.ID, UpdateNotificationChannelInput{Name: &name, Events: &none, Config: map[string]any{"webhook_url": ""}})
	if err != nil || u.Name != "#ops" || len(u.Events) != 0 || u.Config["webhook_id"] != testHookID {
		t.Fatalf("update = %+v, %v", u, err)
	}
	row, _ := m.s.Q.GetNotificationChannel(ctx, c.ID)
	if strings.Contains(string(row.ConfigEnc), testHookToken) {
		t.Fatal("the webhook URL is stored in plain text")
	}
	plain, err := m.s.Crypto.Open(row.ConfigEnc, channelAAD(c.ID))
	if err != nil || !strings.Contains(string(plain), testHookToken) {
		t.Fatalf("stored config = %s, %v", plain, err)
	}
	u, err = m.s.UpdateNotificationChannel(ctx, m.alice, c.ID, UpdateNotificationChannelInput{Config: map[string]any{"webhook_url": "https://canary.discord.com/api/webhooks/4242424242/other-token-abcdefghijklmnop"}})
	if err != nil || u.Config["webhook_id"] != "4242424242" {
		t.Fatalf("new URL = %+v, %v", u, err)
	}

	// Someone else's channel does not exist for bob.
	if _, err := m.s.UpdateNotificationChannel(ctx, m.bob, c.ID, UpdateNotificationChannelInput{Name: &name}); depCode(err) != "notification_channel_not_found" {
		t.Errorf("bob updates alice's channel: %v", err)
	}
	if err := m.s.DeleteNotificationChannel(ctx, m.bob, c.ID); depCode(err) != "notification_channel_not_found" {
		t.Errorf("bob deletes alice's channel: %v", err)
	}
	if _, err := m.s.TestNotificationChannel(ctx, m.bob, c.ID); depCode(err) != "notification_channel_not_found" {
		t.Errorf("bob tests alice's channel: %v", err)
	}
	if list, _ := m.s.ListNotificationChannels(ctx, m.bob); len(list) != 0 {
		t.Errorf("bob sees %d channels", len(list))
	}

	if err := m.s.DeleteNotificationChannel(ctx, m.alice, c.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := m.s.ListNotificationChannels(ctx, m.alice); len(list) != 0 {
		t.Errorf("%d channels left", len(list))
	}

	rows, err := m.s.Store.DB.Query(`SELECT action, details FROM audit_log WHERE target_id = ? ORDER BY id`, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actions []string
	for rows.Next() {
		var a, d string
		_ = rows.Scan(&a, &d)
		actions = append(actions, a)
		if strings.Contains(d, testHookToken) || strings.Contains(d, "other-token-abcdefghijklmnop") {
			t.Errorf("audit %s holds the webhook URL: %s", a, d)
		}
	}
	if strings.Join(actions, " ") != "notification_channel.create notification_channel.update notification_channel.update notification_channel.delete" {
		t.Errorf("audit = %v", actions)
	}
}

func TestNotificationsReachTheAppsUsers(t *testing.T) {
	m := newMembersEnv(t)
	withRecorder(m.s)
	ctx := context.Background()
	m.join(t, m.bob)

	alice := m.channel(t, m.alice, "alice")
	aliceOff := m.channel(t, m.alice, "alice off")
	off := false
	if _, err := m.s.UpdateNotificationChannel(ctx, m.alice, aliceOff.ID, UpdateNotificationChannelInput{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	aliceStarted := m.channel(t, m.alice, "alice started", EventDeploymentStarted)
	bob := m.channel(t, m.bob, "bob")
	bobHooks := m.channel(t, m.bob, "bob hooks", EventDeployCommandFailed)
	carol := m.channel(t, m.carol, "carol")

	app, _ := m.s.Q.GetApp(ctx, m.app.ID)
	m.insertDep(t, m.app, "dep_fail1", 1, "failed")
	m.exec(t, `UPDATE deployments SET error_code = 'build_failed', error_message = 'npm ci failed', error_hint = 'Fix package.json.' WHERE id = 'dep_fail1'`)
	m.s.notifyDeployment(ctx, "dep_fail1", EventDeploymentFailed)
	got := queued(m.s)
	if len(got) != 2 {
		t.Fatalf("queued for %v, want alice and bob", keysOf(got))
	}
	msg, ok := got[alice.ID]
	if _, ok2 := got[bob.ID]; !ok || !ok2 {
		t.Fatalf("queued for %v", keysOf(got))
	}
	for _, id := range []string{aliceOff.ID, aliceStarted.ID, bobHooks.ID, carol.ID} {
		if _, ok := got[id]; ok {
			t.Errorf("queued for %s", id)
		}
	}
	if msg.Severity != notify.Emergency || msg.Tone != notify.ToneFailure || msg.Body != "npm ci failed" || msg.Hint != "Fix package.json." {
		t.Errorf("message = %+v", msg)
	}
	if msg.URL != "https://bitploy.test/projects/"+app.ProjectID+"/apps/shop/deployments" {
		t.Errorf("url = %s", msg.URL)
	}
	fields := map[string]string{}
	for _, f := range msg.Fields {
		fields[f.Name] = f.Value
	}
	if fields["App"] != "Shop" || fields["Project"] != "Shop" || fields["Source"] != "Uploaded files" {
		t.Errorf("fields = %v", fields)
	}

	// A failed before-deploy command reaches a channel that only has the
	// deploy command event, once.
	m.insertDep(t, m.app, "dep_fail2", 2, "failed")
	m.exec(t, `UPDATE deployments SET error_code = 'before_deploy_failed', error_message = 'migrate failed' WHERE id = 'dep_fail2'`)
	m.s.notifyDeployment(ctx, "dep_fail2", EventDeploymentFailed)
	if got := queued(m.s); len(got) != 3 || got[bobHooks.ID].Event != EventDeploymentFailed {
		t.Errorf("before-deploy failure queued for %v", keysOf(got))
	}

	// A deployment that a restart of bitploy stopped failed, but nothing
	// broke: no @everyone.
	m.insertDep(t, m.app, "dep_int", 3, "failed")
	m.exec(t, `UPDATE deployments SET error_code = 'interrupted', error_message = 'bitploy restarted' WHERE id = 'dep_int'`)
	m.s.notifyDeployment(ctx, "dep_int", EventDeploymentFailed)
	if got := queued(m.s); got[alice.ID].Severity != notify.Normal || got[alice.ID].Tone != notify.ToneFailure {
		t.Errorf("interrupted = %+v", got[alice.ID])
	}

	// Started is a normal (not emergency) message.
	m.s.notifyDeployment(ctx, "dep_fail2", EventDeploymentStarted)
	if got := queued(m.s); len(got) != 1 || got[aliceStarted.ID].Severity != notify.Normal {
		t.Errorf("started queued for %v", keysOf(got))
	}

	// A suspended member gets nothing; a member who left neither.
	m.exec(t, `UPDATE users SET status = 'suspended' WHERE id = ?`, m.bob.UserID)
	m.s.notifyDeployment(ctx, "dep_fail1", EventDeploymentFailed)
	if got := queued(m.s); len(got) != 1 || got[alice.ID].Event == "" {
		t.Errorf("with bob suspended: queued for %v", keysOf(got))
	}
}

func TestNotificationDeliveryRecordsTheOutcome(t *testing.T) {
	m := newMembersEnv(t)
	rec := withRecorder(m.s)
	ctx := context.Background()
	c := m.channel(t, m.alice, "alice", EventAppCrashed, EventBackupFailed)

	rec.err = &notify.SendError{Status: 502, Msg: "Discord answered 502: Bad Gateway"}
	m.s.notifyCrash(ctx, m.app.ID, "Crashed: the app stopped 5 times in 5 minutes, so bitploy stopped it. Fix the error in the log below and redeploy, or start the app again.\n\nLast log lines:\nboom")
	got := queued(m.s)
	msg := got[c.ID]
	if msg.Event != EventAppCrashed || msg.Severity != notify.Emergency || msg.Body != "The app stopped 5 times in 5 minutes, so bitploy stopped it." {
		t.Fatalf("crash message = %+v", msg)
	}
	m.s.sendQueued(ctx, notifyJob{channelID: c.ID, msg: msg})
	list, _ := m.s.ListNotificationChannels(ctx, m.alice)
	if list[0].LastError != "Discord answered 502: Bad Gateway" || list[0].LastErrorAt == nil || list[0].LastSuccessAt != nil || !list[0].Enabled {
		t.Errorf("after a failure: %+v", list[0])
	}

	// The test button reports the error, then a success clears it.
	if _, err := m.s.TestNotificationChannel(ctx, m.alice, c.ID); depCode(err) != "notification_test_failed" || !strings.Contains(err.Error(), "502") {
		t.Errorf("test with a failing sender: %v", err)
	}
	rec.err = nil
	v, err := m.s.TestNotificationChannel(ctx, m.alice, c.ID)
	if err != nil || v.LastError != "" || v.LastSuccessAt == nil {
		t.Fatalf("test = %+v, %v", v, err)
	}
	sent := rec.messages()
	if last := sent[len(sent)-1]; last.Event != eventTest || last.URL != "https://bitploy.test/settings/notifications" {
		t.Errorf("test message = %+v", last)
	}

	// A webhook that no longer exists turns the channel off, with the reason.
	rec.err = &notify.SendError{Status: 404, Msg: "Discord says this webhook does not exist.", Gone: true}
	m.s.sendQueued(ctx, notifyJob{channelID: c.ID, msg: msg})
	list, _ = m.s.ListNotificationChannels(ctx, m.alice)
	if list[0].Enabled || list[0].LastError != "Turned off: Discord says this webhook does not exist." {
		t.Errorf("after the webhook was deleted: %+v", list[0])
	}
	m.s.notifyCrash(ctx, m.app.ID, "Crashed: again")
	if got := queued(m.s); len(got) != 0 {
		t.Errorf("a turned-off channel got %v", keysOf(got))
	}
}

func TestRunNotificationsSendsAndStops(t *testing.T) {
	m := newMembersEnv(t)
	rec := withRecorder(m.s)
	ctx, cancel := context.WithCancel(context.Background())
	m.channel(t, m.alice, "alice", EventAppCrashed)
	done := make(chan struct{})
	go func() {
		m.s.RunNotifications(ctx)
		close(done)
	}()
	m.s.notifyCrash(context.Background(), m.app.ID, "Crashed: the app stopped 5 times in 5 minutes, so bitploy stopped it.")
	deadline := time.Now().Add(5 * time.Second)
	for len(rec.messages()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(rec.messages()) != 1 {
		t.Fatalf("sent %d messages", len(rec.messages()))
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunNotifications did not stop")
	}
}

func keysOf(m map[string]notify.Message) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}

// testSubscription is a PushSubscription.toJSON() of a made-up browser.
func testSubscription(t *testing.T, endpoint string) (map[string]any, *ecdh.PrivateKey, []byte) {
	t.Helper()
	k, _ := ecdh.P256().GenerateKey(rand.Reader)
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	return map[string]any{"endpoint": endpoint, "keys": map[string]any{
		"p256dh": base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()),
		"auth":   base64.RawURLEncoding.EncodeToString(auth),
	}}, k, auth
}

func TestBrowserChannels(t *testing.T) {
	m := newMembersEnv(t)
	ctx := context.Background()

	// The options offer browsers with a VAPID key that stays the same, also
	// for a new process (it is stored, sealed).
	opts, err := m.s.NotificationOptions(ctx, m.alice)
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.Types) != 2 || opts.Types[1].Type != "browser" || len(opts.VAPIDPublicKey) != 87 {
		t.Fatalf("options = %+v", opts)
	}
	m.s.vapid.keys = nil
	if again, _ := m.s.NotificationOptions(ctx, m.bob); again.VAPIDPublicKey != opts.VAPIDPublicKey {
		t.Errorf("the VAPID key changed: %s, %s", opts.VAPIDPublicKey, again.VAPIDPublicKey)
	}
	row, err := m.s.Q.GetVapidKeys(ctx)
	if err != nil || row.PublicKey != opts.VAPIDPublicKey {
		t.Fatalf("stored keys = %+v, %v", row, err)
	}
	if _, err := m.s.Crypto.Open(row.PrivateKeyEnc, vapidAAD); err != nil {
		t.Errorf("private key not sealed with the server key: %v", err)
	}

	endpoint := "https://fcm.googleapis.com/fcm/send/dGVzdC1lbmRwb2ludA:APA91b"
	sub, _, _ := testSubscription(t, endpoint)
	c, err := m.s.CreateNotificationChannel(ctx, m.alice, CreateNotificationChannelInput{Type: "browser", Name: "Chrome on macOS", Config: sub})
	if err != nil {
		t.Fatal(err)
	}
	if c.Type != "browser" || c.Config["push_service"] != "Google" || c.Config["endpoint_hash"] != notify.EndpointHash(endpoint) {
		t.Errorf("channel = %+v", c)
	}
	raw, _ := json.Marshal(c)
	if strings.Contains(string(raw), "fcm/send") || strings.Contains(string(raw), sub["keys"].(map[string]any)["auth"].(string)) {
		t.Errorf("the API shows the subscription: %s", raw)
	}

	// Turning it on again in the same browser changes that channel.
	sub2, _, _ := testSubscription(t, endpoint)
	again, err := m.s.CreateNotificationChannel(ctx, m.alice, CreateNotificationChannelInput{Type: "browser", Name: "Chrome", Events: []string{EventAppCrashed}, Config: sub2})
	if err != nil || again.ID != c.ID || again.Name != "Chrome" || strings.Join(again.Events, ",") != EventAppCrashed {
		t.Fatalf("again = %+v, %v", again, err)
	}
	if list, _ := m.s.ListNotificationChannels(ctx, m.alice); len(list) != 1 {
		t.Errorf("alice has %d channels", len(list))
	}

	// Another browser is another channel.
	other, _, _ := testSubscription(t, "https://updates.push.services.mozilla.com/wpush/v2/gAAAAA")
	if _, err := m.s.CreateNotificationChannel(ctx, m.alice, CreateNotificationChannelInput{Type: "browser", Name: "Firefox on Linux", Config: other}); err != nil {
		t.Fatal(err)
	}

	// Bob signs in on alice's Chrome and turns notifications on: the
	// browser is his now, alice's channel for it is gone.
	bobSub, _, _ := testSubscription(t, endpoint)
	bc, err := m.s.CreateNotificationChannel(ctx, m.bob, CreateNotificationChannelInput{Type: "browser", Name: "Chrome on macOS", Config: bobSub})
	if err != nil {
		t.Fatal(err)
	}
	list, _ := m.s.ListNotificationChannels(ctx, m.alice)
	if len(list) != 1 || list[0].Name != "Firefox on Linux" {
		t.Errorf("alice keeps %+v", list)
	}
	if bl, _ := m.s.ListNotificationChannels(ctx, m.bob); len(bl) != 1 || bl[0].ID != bc.ID {
		t.Errorf("bob has %+v", bl)
	}

	// A browser channel is not moved to another subscription by an edit.
	if _, err := m.s.UpdateNotificationChannel(ctx, m.bob, bc.ID, UpdateNotificationChannelInput{Config: other}); depCode(err) != "channel_config_fixed" {
		t.Errorf("moving a browser channel: %v", err)
	}

	// A subscription at a host that is no push service is refused.
	evil, _, _ := testSubscription(t, "https://169.254.169.254/latest")
	if _, err := m.s.CreateNotificationChannel(ctx, m.alice, CreateNotificationChannelInput{Type: "browser", Name: "x", Config: evil}); depCode(err) != "push_subscription_invalid" {
		t.Errorf("metadata endpoint: %v", err)
	}
}

// A push service that says the subscription is gone (410) turns the
// browser's channel off; a working one gets a message it can decrypt.
func TestBrowserPushDelivery(t *testing.T) {
	m := newMembersEnv(t)
	ctx := context.Background()
	var mu sync.Mutex
	status := http.StatusCreated
	var bodies [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, b)
		if !strings.HasPrefix(r.Header.Get("Authorization"), "vapid t=") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()
	reg := notify.Registry{}
	reg.Register(&notify.WebPush{Keys: m.s.vapidKeys, Client: srv.Client(), BaseURL: srv.URL})
	m.s.notif.senders = reg

	sub, _, _ := testSubscription(t, "https://web.push.apple.com/QGx_test")
	c, err := m.s.CreateNotificationChannel(ctx, m.alice, CreateNotificationChannelInput{Type: "browser", Name: "Safari on macOS", Config: sub})
	if err != nil {
		t.Fatal(err)
	}
	v, err := m.s.TestNotificationChannel(ctx, m.alice, c.ID)
	if err != nil || v.LastSuccessAt == nil || len(bodies) != 1 {
		t.Fatalf("test = %+v, %v (%d pushes)", v, err, len(bodies))
	}

	status = http.StatusGone
	m.insertDep(t, m.app, "dep_push", 1, "failed")
	m.s.notifyDeployment(ctx, "dep_push", EventDeploymentFailed)
	for _, j := range queuedJobs(m.s) {
		m.s.sendQueued(ctx, j)
	}
	list, _ := m.s.ListNotificationChannels(ctx, m.alice)
	if len(list) != 1 || list[0].Enabled || !strings.HasPrefix(list[0].LastError, "Turned off: The browser ended this subscription") {
		t.Fatalf("after 410: %+v", list)
	}
}

func queuedJobs(s *Service) []notifyJob {
	var out []notifyJob
	for {
		select {
		case j := <-s.notif.queue:
			out = append(out, j)
		default:
			return out
		}
	}
}

// stuckSender never answers until its context ends.
type stuckSender struct {
	notify.Discord
	started chan struct{}
}

func (s *stuckSender) Send(ctx context.Context, _ json.RawMessage, _ notify.Message) error {
	close(s.started)
	<-ctx.Done()
	return ctx.Err()
}

func TestRunNotificationsCutsOffSendsOnStop(t *testing.T) {
	m := newMembersEnv(t)
	stuck := &stuckSender{started: make(chan struct{})}
	reg := notify.Registry{}
	reg.Register(stuck)
	m.s.notif.senders = reg
	m.channel(t, m.alice, "alice", EventAppCrashed)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.s.RunNotifications(ctx)
		close(done)
	}()
	m.s.notifyCrash(context.Background(), m.app.ID, "Crashed: x")
	<-stuck.started
	cancel()
	select {
	case <-done:
	case <-time.After(notifyDrainTimeout + 3*time.Second):
		t.Fatal("RunNotifications waited for a stuck send")
	}
	list, _ := m.s.ListNotificationChannels(context.Background(), m.alice)
	if list[0].LastError == "" {
		t.Errorf("the cut-off send is not recorded: %+v", list[0])
	}
}
