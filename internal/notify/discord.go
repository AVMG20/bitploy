package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
)

// TypeDiscord is the channel type of a Discord webhook.
const TypeDiscord = "discord"

// discordHosts are the only hosts a Discord webhook URL may have. A fixed
// list keeps a "webhook" from pointing anywhere else (SSRF).
var discordHosts = map[string]bool{
	"discord.com": true, "ptb.discord.com": true, "canary.discord.com": true,
	"discordapp.com": true, "ptb.discordapp.com": true, "canary.discordapp.com": true,
}

var (
	discordIDRe    = regexp.MustCompile(`^[0-9]{5,30}$`)
	discordTokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{20,200}$`)
)

// Embed colors: success green, failure red, started orange, other violet.
// Never blue.
var discordColors = map[Tone]int{
	ToneSuccess:  0x22c55e,
	ToneFailure:  0xef4444,
	ToneProgress: 0xf97316,
	ToneNeutral:  0x8b5cf6,
}

// Discord sends to a Discord webhook. The zero value is ready for use.
type Discord struct {
	// Client sends the requests; nil means SafeClient.
	Client *http.Client
	// BaseURL replaces https://discord.com when sending (tests point it at
	// an httptest server). The URL a user enters is always checked against
	// discordHosts.
	BaseURL string
	// Sleep waits between tries; nil means a timer that stops with ctx.
	Sleep func(ctx context.Context, d time.Duration) error
}

type discordConfig struct {
	WebhookURL string `json:"webhook_url"`
}

func (*Discord) Type() string { return TypeDiscord }

// ParseDiscordWebhook checks a webhook URL as Discord shows it
// (https://discord.com/api/webhooks/<id>/<token>) and returns the id, the
// token and the URL in its canonical form.
func ParseDiscordWebhook(raw string) (id, token, canonical string, err error) {
	bad := func(why string) error {
		return apperr.BadRequest("webhook_url_invalid", "This is not a Discord webhook URL: "+why,
			"In Discord open Server settings, Integrations, Webhooks, New webhook, then Copy webhook URL. It looks like https://discord.com/api/webhooks/123456789012345678/abc...")
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", "", apperr.BadRequest("webhook_url_required", "Paste the webhook URL of the Discord channel.",
			"In Discord open Server settings, Integrations, Webhooks, New webhook, then Copy webhook URL.")
	}
	u, perr := url.Parse(raw)
	if perr != nil {
		return "", "", "", bad("it cannot be read.")
	}
	if u.Scheme != "https" {
		return "", "", "", bad("it must start with https://.")
	}
	if u.User != nil || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", "", "", bad("it has parts a webhook URL does not have.")
	}
	if !discordHosts[strings.ToLower(u.Hostname())] || u.Host != u.Hostname() {
		return "", "", "", bad("the address must be discord.com.")
	}
	parts := strings.Split(strings.TrimPrefix(u.EscapedPath(), "/"), "/")
	if len(parts) != 4 || parts[0] != "api" || parts[1] != "webhooks" || !discordIDRe.MatchString(parts[2]) || !discordTokenRe.MatchString(parts[3]) {
		return "", "", "", bad("the path must be /api/webhooks/<id>/<token>.")
	}
	return parts[2], parts[3], "https://discord.com/api/webhooks/" + parts[2] + "/" + parts[3], nil
}

func (*Discord) Prepare(in map[string]any, old json.RawMessage) (json.RawMessage, error) {
	raw, given := str(in, "webhook_url")
	if !given || strings.TrimSpace(raw) == "" {
		if old != nil {
			return old, nil // editing without a new URL keeps the old one
		}
		raw = ""
	}
	_, _, canonical, err := ParseDiscordWebhook(raw)
	if err != nil {
		return nil, err
	}
	return json.Marshal(discordConfig{WebhookURL: canonical})
}

// Public shows the webhook id only: the token is the secret.
func (*Discord) Public(cfg json.RawMessage) map[string]any {
	var c discordConfig
	_ = json.Unmarshal(cfg, &c)
	out := map[string]any{"webhook_url_set": c.WebhookURL != ""}
	if id, _, _, err := ParseDiscordWebhook(c.WebhookURL); err == nil {
		out["webhook_id"] = id
	}
	return out
}

// Discord's limits for messages from a webhook.
const (
	discordContentMax     = 2000
	discordTitleMax       = 256
	discordDescriptionMax = 4096
	discordFieldNameMax   = 256
	discordFieldValueMax  = 1024
	discordFieldsMax      = 25
)

type discordEmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

type discordEmbed struct {
	Title       string              `json:"title"`
	URL         string              `json:"url,omitempty"`
	Description string              `json:"description,omitempty"`
	Color       int                 `json:"color"`
	Fields      []discordEmbedField `json:"fields,omitempty"`
	Timestamp   string              `json:"timestamp,omitempty"`
	Footer      *struct {
		Text string `json:"text"`
	} `json:"footer,omitempty"`
}

// DiscordPayload is the body of the webhook call.
type DiscordPayload struct {
	Content         string `json:"content"`
	Username        string `json:"username"`
	AllowedMentions struct {
		Parse []string `json:"parse"`
	} `json:"allowed_mentions"`
	Embeds []discordEmbed `json:"embeds"`
}

// DiscordMessage renders a message: @everyone for an emergency, @here for
// the rest, then the title; the details go in one embed.
func DiscordMessage(m Message) DiscordPayload {
	mention := "@here"
	if m.Severity == Emergency {
		mention = "@everyone"
	}
	var p DiscordPayload
	p.Content = truncate(mention+" "+escapeMarkdown(m.Title), discordContentMax)
	p.Username = "bitploy"
	// "everyone" lets both @everyone and @here ping; nothing else in the
	// text (a commit message with <@id>) can.
	p.AllowedMentions.Parse = []string{"everyone"}
	desc := m.Body
	if m.Hint != "" {
		if desc != "" {
			desc += "\n\n"
		}
		desc += "**What to do:** " + m.Hint
	}
	e := discordEmbed{
		Title:       truncate(m.Title, discordTitleMax),
		Description: truncate(desc, discordDescriptionMax),
		Color:       discordColors[m.Tone],
	}
	if e.Color == 0 {
		e.Color = discordColors[ToneNeutral]
	}
	if strings.HasPrefix(m.URL, "https://") || strings.HasPrefix(m.URL, "http://") {
		e.URL = m.URL
	}
	for _, f := range m.Fields {
		if len(e.Fields) == discordFieldsMax {
			break
		}
		if strings.TrimSpace(f.Value) == "" {
			continue
		}
		e.Fields = append(e.Fields, discordEmbedField{Name: truncate(f.Name, discordFieldNameMax), Value: truncate(f.Value, discordFieldValueMax), Inline: f.Inline})
	}
	if !m.Time.IsZero() {
		e.Timestamp = m.Time.UTC().Format(time.RFC3339)
	}
	e.Footer = &struct {
		Text string `json:"text"`
	}{Text: "bitploy"}
	p.Embeds = []discordEmbed{e}
	return p
}

// markdown makes user text in the message (app names, commit messages)
// plain: no formatting, no masked links, no @everyone of its own.
var markdown = strings.NewReplacer(`\`, `\\`, `*`, `\*`, `_`, `\_`, `~`, `\~`, "`", "\\`", `|`, `\|`, `[`, `\[`, `]`, `\]`, "@", "@\u200b")

func escapeMarkdown(s string) string { return markdown.Replace(s) }

const (
	discordTries   = 3
	discordMaxWait = 30 * time.Second // the longest retry_after bitploy waits for
)

func (d *Discord) Send(ctx context.Context, cfg json.RawMessage, m Message) error {
	var c discordConfig
	if err := json.Unmarshal(cfg, &c); err != nil {
		return &SendError{Msg: "The channel's settings cannot be read. Enter the webhook URL again."}
	}
	id, token, _, err := ParseDiscordWebhook(c.WebhookURL)
	if err != nil {
		return &SendError{Msg: "The webhook URL is not valid. Enter it again."}
	}
	base := "https://discord.com"
	if d.BaseURL != "" {
		base = strings.TrimRight(d.BaseURL, "/")
	}
	target := base + "/api/webhooks/" + id + "/" + token + "?wait=true"
	body, err := json.Marshal(DiscordMessage(m))
	if err != nil {
		return err
	}
	client := d.Client
	if client == nil {
		client = SafeClient(15 * time.Second)
	}
	var last error
	for try := 1; try <= discordTries; try++ {
		wait, err := d.post(ctx, client, target, body)
		if err == nil {
			return nil
		}
		last = err
		if wait < 0 || try == discordTries {
			break
		}
		if err := d.sleep(ctx, wait); err != nil {
			return last
		}
	}
	return last
}

// post makes one try. wait >= 0 means try again after wait.
func (d *Discord) post(ctx context.Context, client *http.Client, target string, body []byte) (time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return -1, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "bitploy (https://github.com/AVMG20/bitploy)")
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return -1, ctx.Err()
		}
		// The URL holds the token: never show the error with it.
		return time.Second, &SendError{Msg: "Discord could not be reached."}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return 0, nil
	}
	var ans struct {
		Message    string  `json:"message"`
		Code       int     `json:"code"`
		RetryAfter float64 `json:"retry_after"`
	}
	_ = json.Unmarshal(raw, &ans)
	reason := strings.TrimSpace(ans.Message)
	if reason == "" {
		reason = http.StatusText(resp.StatusCode)
	}
	reason = truncate(apperr.Redact(reason), 200)
	se := &SendError{Status: resp.StatusCode, Msg: fmt.Sprintf("Discord answered %d: %s", resp.StatusCode, reason)}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		wait := time.Duration(ans.RetryAfter * float64(time.Second))
		if wait <= 0 {
			if s, err := strconv.ParseFloat(resp.Header.Get("Retry-After"), 64); err == nil {
				wait = time.Duration(s * float64(time.Second))
			}
		}
		if wait <= 0 {
			wait = time.Second
		}
		se.Msg = "Discord is limiting how fast messages are sent to this webhook."
		if wait > discordMaxWait {
			return -1, se
		}
		return wait, se
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnauthorized:
		se.Gone = true
		se.Msg = "Discord says this webhook does not exist (" + reason + "). It was deleted or its URL changed: make a new webhook and paste its URL."
		return -1, se
	case resp.StatusCode >= 500:
		return 2 * time.Second, se
	}
	return -1, se
}

func (d *Discord) sleep(ctx context.Context, dur time.Duration) error {
	return sleepCtx(ctx, d.Sleep, dur)
}
