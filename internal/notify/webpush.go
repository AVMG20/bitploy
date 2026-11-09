package notify

// Web Push (browser notifications that arrive with the tab closed). The
// browser subscribes at its push service (Google's for Chrome, Mozilla's
// for Firefox, Microsoft's for Edge, Apple's for Safari) and hands bitploy
// the endpoint and two keys. bitploy encrypts every message for that
// browser (RFC 8291, aes128gcm of RFC 8188) and signs the request with its
// VAPID key (RFC 8292), so only bitploy can push to the subscription and
// only the browser can read what it pushes.

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
)

// TypeBrowser is the channel type of one browser's push subscription.
const TypeBrowser = "browser"

// pushHosts are the push services bitploy sends to: an exact host, or a
// suffix that starts with a dot. A fixed list keeps a "subscription" from
// pointing anywhere else (SSRF).
var pushHosts = []string{
	"fcm.googleapis.com",         // Chrome, Opera, Brave, Samsung Internet
	".push.services.mozilla.com", // Firefox
	".notify.windows.com",        // Edge
	"web.push.apple.com",         // Safari
	".push.apple.com",            // Safari
}

// pushServices names the push services for the dashboard.
var pushServices = []struct{ suffix, name string }{
	{"fcm.googleapis.com", "Google"},
	{".push.services.mozilla.com", "Mozilla"},
	{".notify.windows.com", "Microsoft"},
	{"push.apple.com", "Apple"},
}

func pushHostAllowed(host string) bool {
	host = strings.ToLower(host)
	for _, h := range pushHosts {
		if strings.HasPrefix(h, ".") {
			if strings.HasSuffix(host, h) && len(host) > len(h) {
				return true
			}
		} else if host == h {
			return true
		}
	}
	return false
}

// VAPID is the server's key pair for Web Push (RFC 8292).
type VAPID struct {
	Key *ecdsa.PrivateKey
	// Subject is a contact address of the server: an https URL or mailto:.
	Subject string
}

// PublicKey is the public key in the form browsers want for
// pushManager.subscribe: base64url (no padding) of the uncompressed point.
func (v *VAPID) PublicKey() string {
	b, err := v.Key.PublicKey.Bytes()
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// NewVAPID makes a new key pair.
func NewVAPID() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}

// header is the Authorization header for a push to endpoint: a JWT signed
// with ES256 whose audience is the push service's origin.
func (v *VAPID) header(endpoint *url.URL, now time.Time) (string, error) {
	enc := base64.RawURLEncoding
	head := enc.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, err := json.Marshal(map[string]any{
		"aud": endpoint.Scheme + "://" + endpoint.Host,
		"exp": now.Add(12 * time.Hour).Unix(), // RFC 8292: at most 24 hours
		"sub": v.Subject,
	})
	if err != nil {
		return "", err
	}
	unsigned := head + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(unsigned))
	r, s, err := ecdsa.Sign(rand.Reader, v.Key, sum[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return "vapid t=" + unsigned + "." + enc.EncodeToString(sig) + ", k=" + v.PublicKey(), nil
}

// WebPush sends to one browser's push subscription.
type WebPush struct {
	// Keys returns the server's VAPID key pair.
	Keys func(ctx context.Context) (*VAPID, error)
	// Client sends the requests; nil means SafeClient.
	Client *http.Client
	// BaseURL replaces the scheme and host of the endpoint when sending
	// (tests point it at an httptest server). The endpoint a browser hands
	// in is always checked against pushHosts.
	BaseURL string
	// Sleep waits between tries; nil means a timer that stops with ctx.
	Sleep func(ctx context.Context, d time.Duration) error
}

// pushConfig is the subscription, in the shape of PushSubscription.toJSON().
type pushConfig struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

func (*WebPush) Type() string { return TypeBrowser }

func pushInvalid(why string) error {
	return apperr.BadRequest("push_subscription_invalid", "The browser's push subscription cannot be used: "+why,
		"Turn notifications on in this browser again from Settings, Notifications.")
}

// parsePushEndpoint checks an endpoint: https, on a known push service.
func parsePushEndpoint(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || raw == "" {
		return nil, pushInvalid("it has no endpoint.")
	}
	if u.Scheme != "https" {
		return nil, pushInvalid("the endpoint must use https.")
	}
	if u.User != nil || u.Opaque != "" || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return nil, pushInvalid("the endpoint has parts a push endpoint does not have.")
	}
	if !pushHostAllowed(u.Hostname()) {
		return nil, pushInvalid("bitploy does not send to the push service at " + u.Hostname() + ".")
	}
	if len(raw) > 2048 {
		return nil, pushInvalid("the endpoint is too long.")
	}
	return u, nil
}

func b64(s string) ([]byte, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "=")
	s = strings.NewReplacer("+", "-", "/", "_").Replace(s)
	return base64.RawURLEncoding.DecodeString(s)
}

// checkPushConfig checks a whole subscription and returns the browser's keys.
func checkPushConfig(c pushConfig) (*url.URL, *ecdh.PublicKey, []byte, error) {
	u, err := parsePushEndpoint(c.Endpoint)
	if err != nil {
		return nil, nil, nil, err
	}
	raw, err := b64(c.Keys.P256dh)
	if err != nil {
		return nil, nil, nil, pushInvalid("its key cannot be read.")
	}
	pub, err := ecdh.P256().NewPublicKey(raw)
	if err != nil {
		return nil, nil, nil, pushInvalid("its key is not a P-256 key.")
	}
	auth, err := b64(c.Keys.Auth)
	if err != nil || len(auth) != 16 {
		return nil, nil, nil, pushInvalid("its auth secret is not 16 bytes.")
	}
	return u, pub, auth, nil
}

func (*WebPush) Prepare(in map[string]any, old json.RawMessage) (json.RawMessage, error) {
	if _, given := in["endpoint"]; !given && old != nil {
		return old, nil // name or events changed; the subscription stays
	}
	var c pushConfig
	raw, _ := json.Marshal(in)
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, pushInvalid("it is not a PushSubscription.")
	}
	c.Endpoint = strings.TrimSpace(c.Endpoint)
	if _, _, _, err := checkPushConfig(c); err != nil {
		return nil, err
	}
	return json.Marshal(c)
}

// Destination is the endpoint: one subscription belongs to one channel.
func (*WebPush) Destination(cfg json.RawMessage) string {
	var c pushConfig
	_ = json.Unmarshal(cfg, &c)
	return c.Endpoint
}

// EndpointHash is how the dashboard recognises the channel of the browser
// it runs in without seeing the endpoint: hex SHA-256 of the endpoint.
func EndpointHash(endpoint string) string {
	sum := sha256.Sum256([]byte(endpoint))
	return hex.EncodeToString(sum[:])
}

// Public shows the push service and a hash of the endpoint.
func (*WebPush) Public(cfg json.RawMessage) map[string]any {
	var c pushConfig
	_ = json.Unmarshal(cfg, &c)
	out := map[string]any{}
	if c.Endpoint == "" {
		return out
	}
	out["endpoint_hash"] = EndpointHash(c.Endpoint)
	if u, err := url.Parse(c.Endpoint); err == nil {
		host := strings.ToLower(u.Hostname())
		for _, s := range pushServices {
			if strings.HasSuffix(host, s.suffix) {
				out["push_service"] = s.name
				break
			}
		}
	}
	return out
}

// PushPayload is what the service worker (web/public/sw.js) gets.
type PushPayload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
	// Tag groups notifications: a newer one about the same app replaces the
	// older one on screen.
	Tag string `json:"tag"`
	// RequireInteraction keeps an emergency on screen until it is clicked
	// or closed.
	RequireInteraction bool   `json:"requireInteraction"`
	Event              string `json:"event"`
}

// pushPlainMax keeps the encrypted message under the 4096 bytes every push
// service accepts (RFC 8291 section 4: 86 bytes of header, 16 of tag, 1
// delimiter), with room to spare.
const pushPlainMax = 3000

// BrowserMessage renders a message for the service worker.
func BrowserMessage(m Message) PushPayload {
	body := m.Body
	if body == "" {
		var parts []string
		for _, f := range m.Fields {
			if f.Name == "App" || f.Name == "Project" {
				parts = append(parts, f.Value)
			}
		}
		body = strings.Join(parts, " · ")
	}
	p := PushPayload{
		Title: truncate(m.Title, 120), Body: truncate(body, 400), Tag: m.Tag, Event: m.Event,
		RequireInteraction: m.Severity == Emergency,
	}
	if strings.HasPrefix(m.URL, "https://") || strings.HasPrefix(m.URL, "http://") {
		p.URL = m.URL
	}
	if p.Tag == "" {
		p.Tag = "bitploy"
	} else {
		p.Tag = "bitploy:" + p.Tag
	}
	return p
}

// EncryptPush encrypts plaintext for a browser (RFC 8291) as one aes128gcm
// record (RFC 8188).
func EncryptPush(plaintext []byte, uaPublic *ecdh.PublicKey, authSecret []byte) ([]byte, error) {
	as, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	return encryptPush(plaintext, uaPublic, authSecret, as, salt)
}

// encryptPush is EncryptPush with the sender's key pair and the salt given,
// for the test vector of RFC 8291 appendix A.
func encryptPush(plaintext []byte, uaPublic *ecdh.PublicKey, authSecret []byte, as *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	const rs = 4096
	if len(plaintext)+1+16 > rs {
		return nil, errors.New("push message too long")
	}
	secret, err := as.ECDH(uaPublic)
	if err != nil {
		return nil, err
	}
	asPub := as.PublicKey().Bytes()
	uaPub := uaPublic.Bytes()
	// IKM = HKDF(auth_secret, ecdh_secret, "WebPush: info" 0x00 ua_public as_public, 32)
	keyInfo := append(append([]byte("WebPush: info\x00"), uaPub...), asPub...)
	ikm, err := hkdf.Key(sha256.New, secret, authSecret, string(keyInfo), 32)
	if err != nil {
		return nil, err
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	// Header: salt, record size, key id (the sender's public key).
	out := make([]byte, 0, 16+4+1+len(asPub)+len(plaintext)+1+16)
	out = append(out, salt...)
	out = binary.BigEndian.AppendUint32(out, rs)
	out = append(out, byte(len(asPub)))
	out = append(out, asPub...)
	// One record, the last: the plaintext ends with the delimiter 0x02.
	record := append(append([]byte{}, plaintext...), 0x02)
	return gcm.Seal(out, nonce, record, nil), nil
}

const (
	pushTries   = 2
	pushMaxWait = 30 * time.Second
	pushTTL     = 24 * time.Hour // a push service keeps an undelivered message this long
)

func (w *WebPush) Send(ctx context.Context, cfg json.RawMessage, m Message) error {
	var c pushConfig
	if err := json.Unmarshal(cfg, &c); err != nil {
		return &SendError{Msg: "The subscription of this browser cannot be read. Turn notifications on in the browser again."}
	}
	endpoint, uaPub, auth, err := checkPushConfig(c)
	if err != nil {
		return &SendError{Msg: "The subscription of this browser is not valid. Turn notifications on in the browser again."}
	}
	if w.Keys == nil {
		return &SendError{Msg: "This server has no key for browser notifications."}
	}
	keys, err := w.Keys(ctx)
	if err != nil {
		return err
	}
	plain, err := json.Marshal(BrowserMessage(m))
	if err != nil {
		return err
	}
	if len(plain) > pushPlainMax {
		return errors.New("push message too long")
	}
	target := endpoint.String()
	if w.BaseURL != "" {
		target = strings.TrimRight(w.BaseURL, "/") + endpoint.EscapedPath()
		if endpoint.RawQuery != "" {
			target += "?" + endpoint.RawQuery
		}
	}
	client := w.Client
	if client == nil {
		client = SafeClient(15 * time.Second)
	}
	urgency := "normal"
	if m.Severity == Emergency {
		urgency = "high"
	}
	var last error
	for try := 1; try <= pushTries; try++ {
		// A new encryption per try: a key pair and salt are never used twice.
		body, err := EncryptPush(plain, uaPub, auth)
		if err != nil {
			return err
		}
		authz, err := keys.header(endpoint, time.Now())
		if err != nil {
			return err
		}
		wait, err := w.post(ctx, client, target, body, authz, urgency)
		if err == nil {
			return nil
		}
		last = err
		if wait < 0 || try == pushTries {
			break
		}
		if err := sleepCtx(ctx, w.Sleep, wait); err != nil {
			return last
		}
	}
	return last
}

// post makes one try. wait >= 0 means try again after wait.
func (w *WebPush) post(ctx context.Context, client *http.Client, target string, body []byte, authz, urgency string) (time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return -1, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("TTL", strconv.Itoa(int(pushTTL.Seconds())))
	req.Header.Set("Urgency", urgency)
	req.Header.Set("Authorization", authz)
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return -1, ctx.Err()
		}
		if errors.Is(err, ErrBlockedAddress) {
			return -1, err
		}
		// The endpoint is a capability: never show the error with it.
		return time.Second, &SendError{Msg: "The browser's push service could not be reached."}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return 0, nil
	}
	reason := strings.TrimSpace(string(raw))
	var ans struct {
		Reason  string `json:"reason"`
		Message string `json:"message"`
		Error   any    `json:"error"`
	}
	if json.Unmarshal(raw, &ans) == nil {
		switch {
		case ans.Reason != "":
			reason = ans.Reason
		case ans.Message != "":
			reason = ans.Message
		}
	}
	if reason == "" || strings.ContainsAny(reason, "<{") {
		reason = http.StatusText(resp.StatusCode)
	}
	reason = truncate(apperr.Redact(reason), 200)
	se := &SendError{Status: resp.StatusCode, Msg: fmt.Sprintf("The push service answered %d: %s", resp.StatusCode, reason)}
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		se.Gone = true
		se.Msg = "The browser ended this subscription (notifications were turned off, or the browser's data was cleared)."
		return -1, se
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		se.Msg = fmt.Sprintf("The push service refused bitploy's key (%d: %s). Turn notifications on in this browser again.", resp.StatusCode, reason)
		return -1, se
	case resp.StatusCode == http.StatusTooManyRequests:
		wait := time.Second
		if s, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && s > 0 {
			wait = time.Duration(s) * time.Second
		}
		se.Msg = "The push service is limiting how fast messages are sent to this browser."
		if wait > pushMaxWait {
			return -1, se
		}
		return wait, se
	case resp.StatusCode >= 500:
		return 2 * time.Second, se
	}
	return -1, se
}
