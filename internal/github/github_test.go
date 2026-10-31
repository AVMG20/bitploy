package github

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestVerifySignature(t *testing.T) {
	secret := []byte("whsec_test")
	body := []byte(`{"ref":"refs/heads/main"}`)
	good := Sign(secret, body)
	tests := []struct {
		name   string
		secret []byte
		body   []byte
		header string
		want   bool
	}{
		{"valid", secret, body, good, true},
		{"valid with spaces around", secret, body, "  " + good + " ", true},
		{"other body", secret, []byte(`{"ref":"refs/heads/evil"}`), good, false},
		{"other secret", []byte("another"), body, good, false},
		{"empty secret never verifies", nil, body, Sign(nil, body), false},
		{"missing header", secret, body, "", false},
		{"no sha256 prefix", secret, body, strings.TrimPrefix(good, "sha256="), false},
		{"sha1 header", secret, body, "sha1=" + strings.TrimPrefix(good, "sha256="), false},
		{"not hex", secret, body, "sha256=zzzz", false},
		{"too short", secret, body, good[:len(good)-2], false},
		{"upper case hex is still the same bytes", secret, body, "sha256=" + strings.ToUpper(strings.TrimPrefix(good, "sha256=")), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := VerifySignature(tt.secret, tt.body, tt.header); got != tt.want {
				t.Fatalf("VerifySignature = %v, want %v", got, tt.want)
			}
		})
	}
}

func testKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
	return k, string(p)
}

func TestAppJWT(t *testing.T) {
	key, pemText := testKey(t)
	parsed, err := ParsePrivateKey(pemText)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	tok, err := AppJWT(4242, parsed, now)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt has %d parts", len(parts))
	}
	enc := base64.RawURLEncoding
	var header map[string]string
	hb, _ := enc.DecodeString(parts[0])
	if err := json.Unmarshal(hb, &header); err != nil || header["alg"] != "RS256" || header["typ"] != "JWT" {
		t.Fatalf("header %s", hb)
	}
	var claims struct {
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
		Iss string `json:"iss"`
	}
	cb, _ := enc.DecodeString(parts[1])
	if err := json.Unmarshal(cb, &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Iss != "4242" {
		t.Errorf("iss = %q", claims.Iss)
	}
	if claims.Iat != now.Unix()-60 {
		t.Errorf("iat = %d, want now-60s", claims.Iat)
	}
	if claims.Exp-now.Unix() > 600 || claims.Exp <= now.Unix() {
		t.Errorf("exp is %d s from now, GitHub allows at most 600", claims.Exp-now.Unix())
	}
	sig, _ := enc.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("signature does not verify: %v", err)
	}
	// A token for another key must not verify.
	other, _ := testKey(t)
	if rsa.VerifyPKCS1v15(&other.PublicKey, crypto.SHA256, sum[:], sig) == nil {
		t.Fatal("signature verified with the wrong key")
	}
}

func TestParsePrivateKey(t *testing.T) {
	k, pkcs1 := testKey(t)
	if _, err := ParsePrivateKey(pkcs1); err != nil {
		t.Errorf("pkcs1: %v", err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	if _, err := ParsePrivateKey(string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))); err != nil {
		t.Errorf("pkcs8: %v", err)
	}
	for _, bad := range []string{"", "not pem", "-----BEGIN RSA PRIVATE KEY-----\nAAAA\n-----END RSA PRIVATE KEY-----\n"} {
		if _, err := ParsePrivateKey(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestRepoKey(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"owner/repo", "owner/repo", true},
		{"Owner/Repo", "owner/repo", true},
		{"https://github.com/Owner/Repo", "owner/repo", true},
		{"https://github.com/Owner/Repo/", "owner/repo", true},
		{"https://github.com/Owner/Repo.git", "owner/repo", true},
		{"https://www.github.com/owner/repo", "owner/repo", true},
		{"HTTPS://GITHUB.COM/owner/repo", "owner/repo", true},
		{"https://github.com/owner/repo.name", "owner/repo.name", true},
		{"https://gitlab.com/owner/repo", "", false},
		{"https://github.com.evil.com/owner/repo", "", false},
		{"https://github.com/owner", "", false},
		{"https://github.com/owner/repo/tree/main", "", false},
		{"owner", "", false},
		{"owner/repo/extra", "", false},
		{"owner/re po", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := RepoKey(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("RepoKey(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestPushBranch(t *testing.T) {
	zero := strings.Repeat("0", 40)
	sha := strings.Repeat("a", 40)
	tests := []struct {
		name string
		ev   PushEvent
		want string
		ok   bool
	}{
		{"branch", PushEvent{Ref: "refs/heads/main", After: sha}, "main", true},
		{"branch with slash", PushEvent{Ref: "refs/heads/feature/x", After: sha}, "feature/x", true},
		{"tag", PushEvent{Ref: "refs/tags/v1", After: sha}, "", false},
		{"deleted flag", PushEvent{Ref: "refs/heads/main", After: sha, Deleted: true}, "", false},
		{"zero after", PushEvent{Ref: "refs/heads/main", After: zero}, "", false},
		{"empty branch", PushEvent{Ref: "refs/heads/", After: sha}, "", false},
		{"other ref", PushEvent{Ref: "refs/pull/1/head", After: sha}, "", false},
	}
	for _, tt := range tests {
		got, ok := tt.ev.Branch()
		if got != tt.want || ok != tt.ok {
			t.Errorf("%s: Branch() = %q, %v", tt.name, got, ok)
		}
	}
}

func TestClientCalls(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == "/app-manifests/abc123/conversions":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":7,"slug":"bp","name":"bp","html_url":"https://github.com/apps/bp","client_id":"cid","client_secret":"sec","webhook_secret":"wh","pem":"p","owner":{"login":"acme","type":"Organization"}}`))
		case r.URL.Path == "/app/installations/5/access_tokens":
			if r.Header.Get("Authorization") != "Bearer jwt-here" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["repositories"] == nil || body["permissions"] == nil {
				t.Errorf("narrow token body = %v", body)
			}
			_, _ = w.Write([]byte(`{"token":"ghs_abc","expires_at":"2030-01-01T00:00:00Z"}`))
		case r.URL.Path == "/login/oauth/access_token":
			_ = r.ParseForm()
			if r.Form.Get("code") == "used" {
				_, _ = w.Write([]byte(`{"error":"bad_verification_code","error_description":"The code passed is incorrect or expired."}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"ghu_user"}`))
		case r.URL.Path == "/user/installations":
			if r.URL.Query().Get("page") == "1" {
				var items []string
				for i := 0; i < 100; i++ {
					items = append(items, `{"id":`+itoa(i+1)+`,"repository_selection":"all","account":{"login":"a","type":"User"}}`)
				}
				_, _ = w.Write([]byte(`{"total_count":101,"installations":[` + strings.Join(items, ",") + `]}`))
				return
			}
			_, _ = w.Write([]byte(`{"total_count":101,"installations":[{"id":101,"repository_selection":"selected","account":{"login":"b","type":"Organization"}}]}`))
		case r.URL.Path == "/repos/o/r/branches":
			_, _ = w.Write([]byte(`[{"name":"main"},{"name":"dev"}]`))
		case r.URL.Path == "/repos/o/missing":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := NewClient(srv.URL, srv.URL)
	ctx := context.Background()

	cred, err := c.ConvertManifest(ctx, "abc123")
	if err != nil || cred.ID != 7 || cred.Owner.Type != "Organization" || cred.WebhookSecret != "wh" {
		t.Fatalf("ConvertManifest: %+v %v", cred, err)
	}
	if _, err := c.ConvertManifest(ctx, "../etc/passwd"); Status(err) != 422 {
		t.Errorf("a code with path characters must not reach the network: %v", err)
	}
	tok, exp, err := c.InstallationToken(ctx, "jwt-here", 5, []string{"r"}, true)
	if err != nil || tok != "ghs_abc" || exp.Year() != 2030 {
		t.Fatalf("InstallationToken: %q %v %v", tok, exp, err)
	}
	if _, _, err := c.InstallationToken(ctx, "wrong", 5, []string{"r"}, true); Status(err) != 401 {
		t.Errorf("wrong jwt: %v", err)
	}
	if u, err := c.ExchangeOAuthCode(ctx, "cid", "sec", "good"); err != nil || u != "ghu_user" {
		t.Fatalf("ExchangeOAuthCode: %q %v", u, err)
	}
	if _, err := c.ExchangeOAuthCode(ctx, "cid", "sec", "used"); Status(err) != 401 {
		t.Errorf("a used code must fail: %v", err)
	}
	insts, err := c.UserInstallations(ctx, "ghu_user")
	if err != nil || len(insts) != 101 || insts[100].Account.Type != "Organization" {
		t.Fatalf("UserInstallations: %d %v", len(insts), err)
	}
	names, err := c.Branches(ctx, "t", "o", "r")
	if err != nil || len(names) != 2 || names[0] != "main" {
		t.Fatalf("Branches: %v %v", names, err)
	}
	if _, err := c.RepoInfo(ctx, "t", "o", "missing"); Status(err) != 404 || IsUnreachable(err) {
		t.Errorf("404: %v", err)
	}
	if _, err := c.RepoInfo(ctx, "t", "o/../x", "r"); Status(err) != 422 {
		t.Errorf("bad owner: %v", err)
	}
	srv.Close()
	if _, err := c.Branches(ctx, "t", "o", "r"); !IsUnreachable(err) {
		t.Errorf("closed server must be unreachable: %v", err)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
