package notify

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func mustB64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// decryptPush is the browser's side of RFC 8291, written out separately
// from encryptPush so the test checks one against the other.
func decryptPush(t *testing.T, body []byte, ua *ecdh.PrivateKey, auth []byte) []byte {
	t.Helper()
	if len(body) < 21 {
		t.Fatal("body too short")
	}
	salt := body[:16]
	if rs := binary.BigEndian.Uint32(body[16:20]); rs != 4096 {
		t.Fatalf("rs = %d", rs)
	}
	idlen := int(body[20])
	asPubRaw := body[21 : 21+idlen]
	asPub, err := ecdh.P256().NewPublicKey(asPubRaw)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := ua.ECDH(asPub)
	if err != nil {
		t.Fatal(err)
	}
	prkKey, _ := hkdf.Extract(sha256.New, secret, auth)
	info := "WebPush: info\x00" + string(ua.PublicKey().Bytes()) + string(asPubRaw)
	ikm, _ := hkdf.Expand(sha256.New, prkKey, info, 32)
	prk, _ := hkdf.Extract(sha256.New, ikm, salt)
	cek, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, body[21+idlen:], nil)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if len(plain) == 0 || plain[len(plain)-1] != 0x02 {
		t.Fatalf("no last-record delimiter: %x", plain)
	}
	return plain[:len(plain)-1]
}

// RFC 8291, appendix A.
func TestEncryptPushRFC8291Vector(t *testing.T) {
	as, err := ecdh.P256().NewPrivateKey(mustB64(t, "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"))
	if err != nil {
		t.Fatal(err)
	}
	ua, err := ecdh.P256().NewPrivateKey(mustB64(t, "q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94"))
	if err != nil {
		t.Fatal(err)
	}
	if got := base64.RawURLEncoding.EncodeToString(ua.PublicKey().Bytes()); got != "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4" {
		t.Fatalf("ua public = %s", got)
	}
	auth := mustB64(t, "BTBZMqHH6r4Tts7J_aSIgg")
	salt := mustB64(t, "DGv6ra1nlYgDCS1FRnbzlw")
	plain := []byte("When I grow up, I want to be a watermelon")
	body, err := encryptPush(plain, ua.PublicKey(), auth, as, salt)
	if err != nil {
		t.Fatal(err)
	}
	want := "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
	if got := base64.RawURLEncoding.EncodeToString(body); got != want {
		t.Fatalf("body =\n%s\nwant\n%s", got, want)
	}
	if got := decryptPush(t, body, ua, auth); string(got) != string(plain) {
		t.Fatalf("decrypted %q", got)
	}
}

func TestEncryptPushRoundTrip(t *testing.T) {
	ua, _ := ecdh.P256().GenerateKey(rand.Reader)
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	a, _ := EncryptPush([]byte("hello"), ua.PublicKey(), auth)
	b, _ := EncryptPush([]byte("hello"), ua.PublicKey(), auth)
	if string(a) == string(b) {
		t.Fatal("two encryptions are the same: key pair or salt reused")
	}
	if got := decryptPush(t, a, ua, auth); string(got) != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestPushEndpointAllowlist(t *testing.T) {
	for _, ok := range []string{
		"https://fcm.googleapis.com/fcm/send/abc:def",
		"https://updates.push.services.mozilla.com/wpush/v2/gAAAA",
		"https://wns2-par02p.notify.windows.com/w/?token=BQYAAA",
		"https://web.push.apple.com/QGx_abc",
		"https://api.push.apple.com/3/device/abc",
		"https://fcm.googleapis.com:443/fcm/send/abc",
	} {
		if _, err := parsePushEndpoint(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"",
		"http://fcm.googleapis.com/fcm/send/abc",
		"https://fcm.googleapis.com.evil.test/fcm/send/abc",
		"https://evil.test/fcm.googleapis.com",
		"https://push.services.mozilla.com/wpush", // the suffix alone is no host of theirs
		"https://user@fcm.googleapis.com/fcm/send/abc",
		"https://fcm.googleapis.com:8443/fcm/send/abc",
		"https://127.0.0.1/fcm/send/abc",
		"https://localhost/x",
		"https://169.254.169.254/latest/meta-data",
		"https://evilnotify.windows.com.attacker.test/x",
		"ftp://fcm.googleapis.com/x",
	} {
		if _, err := parsePushEndpoint(bad); err == nil {
			t.Errorf("%s: allowed", bad)
		}
	}
}

type testBrowser struct {
	key  *ecdh.PrivateKey
	auth []byte
	sub  map[string]any
}

func newTestBrowser(t *testing.T, endpoint string) *testBrowser {
	t.Helper()
	k, _ := ecdh.P256().GenerateKey(rand.Reader)
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	return &testBrowser{key: k, auth: auth, sub: map[string]any{
		"endpoint": endpoint,
		"keys": map[string]any{
			"p256dh": base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()),
			"auth":   base64.RawURLEncoding.EncodeToString(auth),
		},
	}}
}

func TestWebPushPrepare(t *testing.T) {
	w := &WebPush{}
	b := newTestBrowser(t, "https://fcm.googleapis.com/fcm/send/abc")
	cfg, err := w.Prepare(b.sub, nil)
	if err != nil {
		t.Fatal(err)
	}
	pub := w.Public(cfg)
	if pub["push_service"] != "Google" || pub["endpoint_hash"] != EndpointHash("https://fcm.googleapis.com/fcm/send/abc") {
		t.Errorf("public = %v", pub)
	}
	raw, _ := json.Marshal(pub)
	if strings.Contains(string(raw), "fcm/send") || strings.Contains(string(raw), b.sub["keys"].(map[string]any)["auth"].(string)) {
		t.Errorf("public shows the subscription: %s", raw)
	}
	if w.Destination(cfg) != "https://fcm.googleapis.com/fcm/send/abc" {
		t.Errorf("destination = %q", w.Destination(cfg))
	}
	// Changing only the name keeps the subscription.
	if kept, err := w.Prepare(map[string]any{}, cfg); err != nil || string(kept) != string(cfg) {
		t.Errorf("kept = %s, %v", kept, err)
	}
	for name, in := range map[string]map[string]any{
		"no keys":   {"endpoint": "https://fcm.googleapis.com/fcm/send/abc"},
		"bad host":  newTestBrowser(t, "https://evil.test/x").sub,
		"bad point": {"endpoint": "https://fcm.googleapis.com/fcm/send/abc", "keys": map[string]any{"p256dh": base64.RawURLEncoding.EncodeToString(make([]byte, 65)), "auth": "AAAAAAAAAAAAAAAAAAAAAA"}},
		"bad auth":  {"endpoint": "https://fcm.googleapis.com/fcm/send/abc", "keys": map[string]any{"p256dh": b.sub["keys"].(map[string]any)["p256dh"], "auth": "AAAA"}},
	} {
		if _, err := w.Prepare(in, nil); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

type fakePush struct {
	mu      sync.Mutex
	answers []func(http.ResponseWriter)
	reqs    []*http.Request
	bodies  [][]byte
}

func (f *fakePush) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, _ := io.ReadAll(r.Body)
	f.reqs = append(f.reqs, r)
	f.bodies = append(f.bodies, b)
	if i := len(f.reqs) - 1; i < len(f.answers) {
		f.answers[i](w)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func testPush(t *testing.T, f *fakePush) (*WebPush, *testBrowser, json.RawMessage, *ecdsa.PrivateKey) {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	key, _ := NewVAPID()
	w := &WebPush{
		Keys:    func(context.Context) (*VAPID, error) { return &VAPID{Key: key, Subject: "https://bitploy.test"}, nil },
		Client:  srv.Client(),
		BaseURL: srv.URL,
		Sleep:   func(context.Context, time.Duration) error { return nil },
	}
	b := newTestBrowser(t, "https://updates.push.services.mozilla.com/wpush/v2/gAAAAB")
	cfg, err := w.Prepare(b.sub, nil)
	if err != nil {
		t.Fatal(err)
	}
	return w, b, cfg, key
}

func TestWebPushSend(t *testing.T) {
	f := &fakePush{}
	w, b, cfg, key := testPush(t, f)
	m := Message{Event: "deployment.failed", Title: "Deployment 3 of Shop failed", Body: "npm ci failed", Severity: Emergency,
		URL: "https://bitploy.test/apps/shop/deployments", Tag: "app_1"}
	if err := w.Send(context.Background(), cfg, m); err != nil {
		t.Fatal(err)
	}
	if len(f.reqs) != 1 {
		t.Fatalf("%d requests", len(f.reqs))
	}
	r := f.reqs[0]
	if r.URL.Path != "/wpush/v2/gAAAAB" || r.Header.Get("Content-Encoding") != "aes128gcm" || r.Header.Get("TTL") != "86400" || r.Header.Get("Urgency") != "high" {
		t.Errorf("request %s %v", r.URL, r.Header)
	}
	var p PushPayload
	if err := json.Unmarshal(decryptPush(t, f.bodies[0], b.key, b.auth), &p); err != nil {
		t.Fatal(err)
	}
	if p.Title != m.Title || p.Body != "npm ci failed" || p.URL != m.URL || p.Tag != "bitploy:app_1" || !p.RequireInteraction {
		t.Errorf("payload = %+v", p)
	}

	// The VAPID header: a JWT for the push service's origin, signed with
	// the server's key, and that key.
	authz := r.Header.Get("Authorization")
	tok, k, ok := strings.Cut(strings.TrimPrefix(authz, "vapid t="), ", k=")
	if !ok || !strings.HasPrefix(authz, "vapid t=") {
		t.Fatalf("authorization = %q", authz)
	}
	if k != (&VAPID{Key: key}).PublicKey() {
		t.Errorf("k = %s", k)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt = %s", tok)
	}
	var claims struct {
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Sub string `json:"sub"`
	}
	_ = json.Unmarshal(mustB64(t, parts[1]), &claims)
	if claims.Aud != "https://updates.push.services.mozilla.com" || claims.Sub != "https://bitploy.test" ||
		claims.Exp <= time.Now().Unix() || claims.Exp > time.Now().Add(24*time.Hour).Unix() {
		t.Errorf("claims = %+v", claims)
	}
	sig := mustB64(t, parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if len(sig) != 64 || !ecdsa.Verify(&key.PublicKey, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Error("the JWT signature does not verify")
	}

	// A normal message is normal urgency and may go away by itself.
	m.Severity = Normal
	if err := w.Send(context.Background(), cfg, m); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(decryptPush(t, f.bodies[1], b.key, b.auth), &p)
	if f.reqs[1].Header.Get("Urgency") != "normal" || p.RequireInteraction {
		t.Errorf("normal: urgency %s, payload %+v", f.reqs[1].Header.Get("Urgency"), p)
	}
}

func TestWebPushGone(t *testing.T) {
	for _, status := range []int{http.StatusGone, http.StatusNotFound} {
		f := &fakePush{answers: []func(http.ResponseWriter){func(w http.ResponseWriter) { w.WriteHeader(status) }}}
		w, _, cfg, _ := testPush(t, f)
		err := w.Send(context.Background(), cfg, Message{Title: "x"})
		if !IsGone(err) || len(f.reqs) != 1 {
			t.Errorf("%d: err %v, %d tries", status, err, len(f.reqs))
		}
		if strings.Contains(ErrorText(err), "gAAAAB") {
			t.Errorf("the error shows the endpoint: %s", ErrorText(err))
		}
	}
}

func TestWebPushRetries(t *testing.T) {
	f := &fakePush{answers: []func(http.ResponseWriter){
		func(w http.ResponseWriter) {
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusTooManyRequests)
		},
	}}
	w, _, cfg, _ := testPush(t, f)
	var waits []time.Duration
	w.Sleep = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
	if err := w.Send(context.Background(), cfg, Message{Title: "x"}); err != nil || len(f.reqs) != 2 || len(waits) != 1 || waits[0] != 3*time.Second {
		t.Fatalf("err %v, %d tries, waits %v", err, len(f.reqs), waits)
	}
	// Each try is encrypted anew.
	if string(f.bodies[0]) == string(f.bodies[1]) {
		t.Error("the retry reused the encryption")
	}

	f = &fakePush{answers: []func(http.ResponseWriter){func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"reason":"VapidPkHashMismatch"}`))
	}}}
	w, _, cfg, _ = testPush(t, f)
	err := w.Send(context.Background(), cfg, Message{Title: "x"})
	if err == nil || IsGone(err) || len(f.reqs) != 1 || !strings.Contains(ErrorText(err), "VapidPkHashMismatch") {
		t.Fatalf("403: err %v, %d tries", err, len(f.reqs))
	}
}

func TestBrowserMessageFitsAPush(t *testing.T) {
	long := strings.Repeat("é", 5000)
	p := BrowserMessage(Message{Title: long, Body: long, URL: "javascript:alert(1)", Tag: "app_1"})
	raw, _ := json.Marshal(p)
	if len(raw) > pushPlainMax || p.URL != "" {
		t.Fatalf("payload %d bytes, url %q", len(raw), p.URL)
	}
	p = BrowserMessage(Message{Title: "t", Fields: []Field{{Name: "App", Value: "Shop"}, {Name: "Project", Value: "Store"}, {Name: "Deployment", Value: "3"}}})
	if p.Body != "Shop · Store" || p.Tag != "bitploy" {
		t.Errorf("payload = %+v", p)
	}
}
