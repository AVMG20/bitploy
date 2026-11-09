package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
)

const (
	testID    = "123456789012345678"
	testToken = "AbCdEfGhIjKlMnOpQrStUvWxYz_0123456789-abcdefghijklmnopqrstuvwxyzABCDEF"
)

func TestParseDiscordWebhook(t *testing.T) {
	good := "https://discord.com/api/webhooks/" + testID + "/" + testToken
	cases := []struct {
		url  string
		ok   bool
		code string
	}{
		{good, true, ""},
		{"  " + good + "  ", true, ""},
		{"https://discordapp.com/api/webhooks/" + testID + "/" + testToken, true, ""},
		{"https://ptb.discord.com/api/webhooks/" + testID + "/" + testToken, true, ""},
		{"https://canary.discord.com/api/webhooks/" + testID + "/" + testToken, true, ""},
		{"https://canary.discordapp.com/api/webhooks/" + testID + "/" + testToken, true, ""},
		{"https://DISCORD.com/api/webhooks/" + testID + "/" + testToken, true, ""},
		{"", false, "webhook_url_required"},
		{"http://discord.com/api/webhooks/" + testID + "/" + testToken, false, "webhook_url_invalid"},
		{"https://discord.com:8443/api/webhooks/" + testID + "/" + testToken, false, "webhook_url_invalid"},
		{"https://user@discord.com/api/webhooks/" + testID + "/" + testToken, false, "webhook_url_invalid"},
		{"https://evil.com/api/webhooks/" + testID + "/" + testToken, false, "webhook_url_invalid"},
		{"https://discord.com.evil.com/api/webhooks/" + testID + "/" + testToken, false, "webhook_url_invalid"},
		{"https://evildiscord.com/api/webhooks/" + testID + "/" + testToken, false, "webhook_url_invalid"},
		{"https://127.0.0.1/api/webhooks/" + testID + "/" + testToken, false, "webhook_url_invalid"},
		{"https://discord.com/api/webhooks/" + testID + "/" + testToken + "?wait=true", false, "webhook_url_invalid"},
		{"https://discord.com/api/webhooks/" + testID + "/" + testToken + "#x", false, "webhook_url_invalid"},
		{"https://discord.com/api/webhooks/" + testID + "/" + testToken + "/github", false, "webhook_url_invalid"},
		{"https://discord.com/api/webhooks/" + testID, false, "webhook_url_invalid"},
		{"https://discord.com/api/webhooks/abc/" + testToken, false, "webhook_url_invalid"},
		{"https://discord.com/api/webhooks/" + testID + "/short", false, "webhook_url_invalid"},
		{"https://discord.com/api/webhooks/" + testID + "/" + testToken[:30] + "%2F..%2F" + testToken[:30], false, "webhook_url_invalid"},
		{"https://discord.com/api/v10/webhooks/" + testID + "/" + testToken, false, "webhook_url_invalid"},
		{"https://discord.com/../api/webhooks/" + testID + "/" + testToken, false, "webhook_url_invalid"},
		{"discord.com/api/webhooks/" + testID + "/" + testToken, false, "webhook_url_invalid"},
	}
	for _, c := range cases {
		id, token, canonical, err := ParseDiscordWebhook(c.url)
		if c.ok {
			if err != nil || id != testID || token != testToken || canonical != good {
				t.Errorf("%q: got %q %q %q %v", c.url, id, token, canonical, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%q: accepted", c.url)
			continue
		}
		if e := asCode(err); e != c.code {
			t.Errorf("%q: code %q, want %q", c.url, e, c.code)
		}
	}
}

func asCode(err error) string {
	if ae := apperr.As(err); ae != nil {
		return ae.Code
	}
	return ""
}

func TestDiscordPrepareKeepsOldURL(t *testing.T) {
	d := &Discord{}
	good := "https://ptb.discord.com/api/webhooks/" + testID + "/" + testToken
	cfg, err := d.Prepare(map[string]any{"webhook_url": good}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), "https://discord.com/api/webhooks/"+testID) {
		t.Fatalf("stored %s", cfg)
	}
	kept, err := d.Prepare(map[string]any{}, cfg)
	if err != nil || string(kept) != string(cfg) {
		t.Fatalf("edit without URL: %s %v", kept, err)
	}
	kept, err = d.Prepare(map[string]any{"webhook_url": ""}, cfg)
	if err != nil || string(kept) != string(cfg) {
		t.Fatalf("edit with empty URL: %s %v", kept, err)
	}
	if _, err := d.Prepare(map[string]any{}, nil); asCode(err) != "webhook_url_required" {
		t.Fatalf("new without URL: %v", err)
	}
	if _, err := d.Prepare(map[string]any{"webhook_url": "https://evil.com/x"}, cfg); asCode(err) != "webhook_url_invalid" {
		t.Fatalf("bad new URL: %v", err)
	}
	pub := d.Public(cfg)
	b, _ := json.Marshal(pub)
	if strings.Contains(string(b), testToken) || pub["webhook_id"] != testID || pub["webhook_url_set"] != true {
		t.Fatalf("public = %s", b)
	}
}

func TestDiscordMessageShape(t *testing.T) {
	m := Message{
		Event: "deployment.failed", Title: "shop: deployment 12 failed", Body: "The build failed.", Hint: "Fix the Dockerfile.",
		Severity: Emergency, Tone: ToneFailure, URL: "https://bitploy.test/projects/prj_1/apps/shop/deployments",
		Fields: []Field{{Name: "App", Value: "shop", Inline: true}, {Name: "Empty", Value: " "}},
		Time:   time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC),
	}
	b, _ := json.Marshal(DiscordMessage(m))
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	if got["content"] != "@here shop: deployment 12 failed" {
		t.Errorf("content = %v", got["content"])
	}
	am := got["allowed_mentions"].(map[string]any)["parse"].([]any)
	if len(am) != 1 || am[0] != "everyone" {
		t.Errorf("allowed_mentions = %v", am)
	}
	e := got["embeds"].([]any)[0].(map[string]any)
	if e["color"].(float64) != 0xef4444 || e["url"] != m.URL || e["timestamp"] != "2026-11-08T12:00:00Z" {
		t.Errorf("embed = %v", e)
	}
	if d := e["description"].(string); !strings.Contains(d, "The build failed.") || !strings.Contains(d, "Fix the Dockerfile.") {
		t.Errorf("description = %q", d)
	}
	if f := e["fields"].([]any); len(f) != 1 {
		t.Errorf("fields = %v (empty values are left out)", f)
	}

	m.Severity, m.Tone = Normal, ToneSuccess
	p := DiscordMessage(m)
	if p.Content != "shop: deployment 12 failed" || len(p.AllowedMentions.Parse) != 0 || p.Embeds[0].Color != 0x22c55e {
		t.Errorf("normal = %+v", p)
	}
	if b, _ := json.Marshal(p); !strings.Contains(string(b), `"allowed_mentions":{"parse":[]}`) {
		t.Errorf("normal must send an empty parse list, got %s", b)
	}
	m.Tone = ToneProgress
	if c := DiscordMessage(m).Embeds[0].Color; c != 0xf97316 {
		t.Errorf("progress color %x", c)
	}
	m.Title = strings.Repeat("x", 3000)
	m.Body = strings.Repeat("y", 5000)
	p = DiscordMessage(m)
	if n := len([]rune(p.Content)); n > discordContentMax {
		t.Errorf("content %d runes", n)
	}
	if n := len([]rune(p.Embeds[0].Title)); n > discordTitleMax {
		t.Errorf("title %d runes", n)
	}
	if n := len([]rune(p.Embeds[0].Description)); n > discordDescriptionMax {
		t.Errorf("description %d runes", n)
	}
}

// fakeDiscord answers with the given statuses in turn and records the bodies.
type fakeDiscord struct {
	mu      sync.Mutex
	answers []func(w http.ResponseWriter)
	paths   []string
	bodies  []string
}

func (f *fakeDiscord) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, _ := io.ReadAll(r.Body)
	f.paths = append(f.paths, r.URL.RequestURI())
	f.bodies = append(f.bodies, string(b))
	i := len(f.paths) - 1
	if i < len(f.answers) {
		f.answers[i](w)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"id":"1"}`))
}

func testSender(t *testing.T, f *fakeDiscord) (*Discord, *[]time.Duration, json.RawMessage) {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	var waits []time.Duration
	d := &Discord{Client: srv.Client(), BaseURL: srv.URL, Sleep: func(_ context.Context, dur time.Duration) error {
		waits = append(waits, dur)
		return nil
	}}
	cfg, err := d.Prepare(map[string]any{"webhook_url": "https://discord.com/api/webhooks/" + testID + "/" + testToken}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return d, &waits, cfg
}

func TestDiscordSendRetriesAfter429(t *testing.T) {
	f := &fakeDiscord{answers: []func(http.ResponseWriter){
		func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"message":"You are being rate limited.","retry_after":1.5,"global":false}`))
		},
	}}
	d, waits, cfg := testSender(t, f)
	if err := d.Send(context.Background(), cfg, Message{Title: "hi", Severity: Normal}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(f.paths) != 2 || len(*waits) != 1 || (*waits)[0] != 1500*time.Millisecond {
		t.Fatalf("paths %v waits %v", f.paths, *waits)
	}
	if f.paths[0] != "/api/webhooks/"+testID+"/"+testToken+"?wait=true" {
		t.Errorf("path = %s", f.paths[0])
	}
	if !strings.Contains(f.bodies[1], `"content":"hi"`) {
		t.Errorf("body = %s", f.bodies[1])
	}
}

func TestDiscordSendGivesUpOnLongRetryAfter(t *testing.T) {
	f := &fakeDiscord{answers: []func(http.ResponseWriter){
		func(w http.ResponseWriter) {
			w.Header().Set("Retry-After", "600")
			w.WriteHeader(http.StatusTooManyRequests)
		},
	}}
	d, waits, cfg := testSender(t, f)
	err := d.Send(context.Background(), cfg, Message{Title: "hi"})
	if err == nil || len(f.paths) != 1 || len(*waits) != 0 || !strings.Contains(ErrorText(err), "limiting") {
		t.Fatalf("err %v paths %v waits %v", err, f.paths, *waits)
	}
}

func TestDiscordSendRetries5xxThenFails(t *testing.T) {
	fail := func(w http.ResponseWriter) { w.WriteHeader(http.StatusBadGateway) }
	f := &fakeDiscord{answers: []func(http.ResponseWriter){fail, fail, fail, fail}}
	d, waits, cfg := testSender(t, f)
	err := d.Send(context.Background(), cfg, Message{Title: "hi"})
	if err == nil || len(f.paths) != discordTries || len(*waits) != discordTries-1 {
		t.Fatalf("err %v, %d tries, waits %v", err, len(f.paths), *waits)
	}
	if !strings.Contains(ErrorText(err), "502") {
		t.Errorf("text = %q", ErrorText(err))
	}
}

func TestDiscordSendUnknownWebhook(t *testing.T) {
	f := &fakeDiscord{answers: []func(http.ResponseWriter){func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Unknown Webhook","code":10015}`))
	}}}
	d, _, cfg := testSender(t, f)
	err := d.Send(context.Background(), cfg, Message{Title: "hi"})
	if !IsGone(err) || len(f.paths) != 1 || !strings.Contains(ErrorText(err), "no longer exists") {
		t.Fatalf("err %v, %d tries", err, len(f.paths))
	}
	if strings.Contains(ErrorText(err), testToken) {
		t.Fatal("the error shows the token")
	}
}

func TestPublicAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"1.1.1.1": true, "162.159.135.232": true, "2606:4700::1111": true,
		"127.0.0.1": false, "10.1.2.3": false, "172.17.0.1": false, "192.168.1.1": false, "169.254.169.254": false,
		"100.64.0.1": false, "0.0.0.0": false, "::1": false, "fe80::1": false, "fd00::1": false, "::ffff:127.0.0.1": false,
		"::ffff:10.0.0.1": false, "224.0.0.1": false, "::7f00:1": false, "2002:7f00:1::1": false, "64:ff9b::a00:1": false,
	} {
		if got := PublicAddr(netip.MustParseAddr(addr)); got != want {
			t.Errorf("%s: %v, want %v", addr, got, want)
		}
	}
}

func TestSafeClientRefusesLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	_, err := SafeClient(5 * time.Second).Get(srv.URL)
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("err = %v, want ErrBlockedAddress", err)
	}
}

func TestDiscordContentIsPlainText(t *testing.T) {
	p := DiscordMessage(Message{Title: "[click](https://evil.test) @everyone *x*", Severity: Normal})
	if strings.HasPrefix(p.Content, "@here") || len(p.AllowedMentions.Parse) != 0 || strings.Contains(p.Content, "[click]") ||
		strings.Contains(p.Content, " @everyone") || strings.Contains(p.Content, " *x*") {
		t.Fatalf("content = %q", p.Content)
	}
}

func TestDiscordEmbedHasNoMaskedLinksAndFits(t *testing.T) {
	m := Message{
		Title: "Deployment 3 of shop failed", Body: "see [the fix](https://evil.test)", Hint: "[here](https://evil.test)",
		Fields: []Field{{Name: "Source", Value: "Git, commit abc: [login](https://evil.test)"}},
	}
	e := DiscordMessage(m).Embeds[0]
	if strings.Contains(e.Description, "[the fix]") || strings.Contains(e.Description, "[here]") || strings.Contains(e.Fields[0].Value, "[login]") {
		t.Errorf("embed = %+v", e)
	}
	// Everything together stays under Discord's 6000 characters.
	m.Body = strings.Repeat("y", 5000)
	m.Fields = nil
	for i := range 8 {
		m.Fields = append(m.Fields, Field{Name: fmt.Sprintf("f%d", i), Value: strings.Repeat("v", 2000)})
	}
	e = DiscordMessage(m).Embeds[0]
	n := len([]rune(e.Title)) + len([]rune(e.Description)) + len(e.Footer.Text)
	for _, f := range e.Fields {
		n += len([]rune(f.Name)) + len([]rune(f.Value))
	}
	if n > discordEmbedMax {
		t.Errorf("embed has %d characters", n)
	}
}
