package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/proxy"
)

// testCert makes a self-signed certificate for names, valid around now.
func testCert(t *testing.T, now time.Time, names ...string) (certPEM, keyPEM string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(365 * 24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb}))
}

// newDomainEnv is the ops test env with a real route writer.
func newDomainEnv(t *testing.T) *opsEnv {
	t.Helper()
	e := newOpsEnv(t)
	e.svc.Routes = &proxy.Writer{Dir: t.TempDir(), EntryPoint: "websecure", TLS: true}
	return e
}

func (e *opsEnv) addAppDomain(t *testing.T, domain string) AppDomain {
	t.Helper()
	d, err := e.svc.AdminAddAppDomain(context.Background(), e.admin, AddAppDomainInput{Domain: domain})
	if err != nil {
		t.Fatalf("add app domain %s: %v", domain, err)
	}
	return d
}

func TestAppDomainOnlyForAdmins(t *testing.T) {
	e := newDomainEnv(t)
	ctx := context.Background()
	if _, err := e.svc.AdminAddAppDomain(ctx, e.owner, AddAppDomainInput{Domain: "example.com"}); err == nil {
		t.Fatal("a user added an app domain")
	}
	if _, err := e.svc.AdminListAppDomains(ctx, e.owner); err == nil {
		t.Fatal("a user listed app domains")
	}
	if _, err := e.svc.AdminSetUserAppDomains(ctx, e.owner, "usr_owner", nil); err == nil {
		t.Fatal("a user granted domains")
	}
}

func TestAppDomainPermissions(t *testing.T) {
	e := newDomainEnv(t)
	ctx := context.Background()
	d := e.addAppDomain(t, "*.Apps.Example.com")
	if d.Domain != "apps.example.com" || d.Certificate != "none" {
		t.Fatalf("added %+v", d)
	}

	// Not granted: create, catalog and update are refused.
	_, err := e.svc.CreateApp(ctx, e.owner, CreateAppInput{Name: "Site", Slug: "site-one", Domain: "apps.example.com"})
	wantCode(t, err, "domain_not_allowed")
	_, err = e.svc.DeployCatalogApp(ctx, e.owner, "memos", CatalogDeployInput{Domain: "apps.example.com"})
	wantCode(t, err, "domain_not_allowed")
	_, err = e.svc.CreateApp(ctx, e.owner, CreateAppInput{Name: "Site", Slug: "site-one", Domain: "nope.example.org"})
	wantCode(t, err, "domain_not_allowed")
	me, err := e.svc.Me(ctx, e.owner)
	if err != nil || len(me.AppDomains) != 1 || !me.AppDomains[0].Default || me.AppDomains[0].Domain != "bitploy.test" {
		t.Fatalf("me.app_domains = %+v, %v", me.AppDomains, err)
	}

	// Granted: the app lives on the domain.
	au, err := e.svc.AdminSetUserAppDomains(ctx, e.admin, "usr_owner", []string{d.ID})
	if err != nil || len(au.AppDomainIDs) != 1 || au.AppDomainIDs[0] != d.ID {
		t.Fatalf("grant: %+v, %v", au.AppDomainIDs, err)
	}
	me, _ = e.svc.Me(ctx, e.owner)
	if len(me.AppDomains) != 2 || me.AppDomains[1].Domain != "apps.example.com" || me.AppDomains[1].URLTemplate != "https://{slug}.apps.example.com" {
		t.Fatalf("me.app_domains = %+v", me.AppDomains)
	}
	ap, err := e.svc.CreateApp(ctx, e.owner, CreateAppInput{Name: "Site", Slug: "site-one", Domain: "Apps.Example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if ap.URL != "https://site-one.apps.example.com" || ap.Domain != "apps.example.com" {
		t.Fatalf("app url %q domain %q", ap.URL, ap.Domain)
	}
	// Slugs stay unique across domains.
	_, err = e.svc.CreateApp(ctx, e.other, CreateAppInput{Name: "Site", Slug: "site-one"})
	wantCode(t, err, "slug_taken")
	// Another user without the grant still cannot use it.
	_, err = e.svc.CreateApp(ctx, e.other, CreateAppInput{Name: "Other", Slug: "other-one", Domain: "apps.example.com"})
	wantCode(t, err, "domain_not_allowed")

	// Revoking keeps the app where it is; other changes still work, moving
	// back onto the domain does not.
	if _, err := e.svc.AdminSetUserAppDomains(ctx, e.admin, "usr_owner", []string{}); err != nil {
		t.Fatal(err)
	}
	name := "Renamed"
	same := "apps.example.com"
	if ap, err = e.svc.UpdateApp(ctx, e.owner, "site-one", UpdateAppInput{Name: &name, Domain: &same}); err != nil || ap.URL != "https://site-one.apps.example.com" {
		t.Fatalf("update after revoke: %+v, %v", ap.URL, err)
	}
	def := ""
	if ap, err = e.svc.UpdateApp(ctx, e.owner, "site-one", UpdateAppInput{Domain: &def}); err != nil || ap.URL != "https://site-one.bitploy.test" || ap.Domain != "bitploy.test" {
		t.Fatalf("move to default: %+v, %v", ap.URL, err)
	}
	_, err = e.svc.UpdateApp(ctx, e.owner, "site-one", UpdateAppInput{Domain: &same})
	wantCode(t, err, "domain_not_allowed")

	// Admins may use every domain without a grant.
	e.exec(t, `UPDATE users SET quota_apps = 50 WHERE id = 'usr_admin'`)
	if ap, err := e.svc.CreateApp(ctx, e.admin, CreateAppInput{Name: "Admin", Slug: "admin-site", Domain: "apps.example.com"}); err != nil || ap.Domain != "apps.example.com" {
		t.Fatalf("admin create: %+v, %v", ap, err)
	}

	// Every change is in the audit log.
	for _, action := range []string{"admin.app_domain_add", "admin.user_app_domains", "app.update"} {
		var n int
		if err := e.svc.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = ?`, action).Scan(&n); err != nil || n == 0 {
			t.Errorf("no audit row %s (%v)", action, err)
		}
	}
	var details string
	_ = e.svc.Store.DB.QueryRow(`SELECT details FROM audit_log WHERE action = 'app.update' AND details LIKE '%old_domain%' LIMIT 1`).Scan(&details)
	if !strings.Contains(details, `"domain":"bitploy.test"`) {
		t.Errorf("domain move not audited: %s", details)
	}
}

func TestAppDomainMoveRewritesRoute(t *testing.T) {
	e := newDomainEnv(t)
	ctx := context.Background()
	d := e.addAppDomain(t, "example.com")
	if _, err := e.svc.AdminSetUserAppDomains(ctx, e.admin, "usr_owner", []string{d.ID}); err != nil {
		t.Fatal(err)
	}
	e.addApp(t, "app_1", "shop", "usr_owner", "web")
	e.exec(t, `INSERT INTO deployments (id, app_id, seq, state, source_kind, channel, image, container_name, port, queued_at) VALUES ('dep_seed', 'app_1', 1, 'running', 'files', 'api', 'bitploy/shop:1', 'bp-shop-1', 8080, 1)`)
	if err := e.svc.SyncRoute(ctx, "app_1"); err != nil {
		t.Fatal(err)
	}
	b, _ := e.svc.Routes.Read("shop")
	if !strings.Contains(string(b), "Host(`shop.bitploy.test`)") {
		t.Fatalf("route before the move:\n%s", b)
	}
	to := "example.com"
	ap, err := e.svc.UpdateApp(ctx, e.owner, "shop", UpdateAppInput{Domain: &to})
	if err != nil {
		t.Fatal(err)
	}
	b, _ = e.svc.Routes.Read("shop")
	if !strings.Contains(string(b), "Host(`shop.example.com`)") || strings.Contains(string(b), "bitploy.test`") {
		t.Fatalf("route after the move:\n%s", b)
	}
	if ap.URL != "https://shop.example.com" || ap.Current == nil || ap.Current.URL != "https://shop.example.com" {
		t.Fatalf("app url %q, deployment %+v", ap.URL, ap.Current)
	}
	row, _ := e.svc.Q.GetApp(ctx, "app_1")
	env, aerr := e.svc.containerEnv(ctx, row, 8080)
	if aerr != nil {
		t.Fatal(aerr)
	}
	if !containsStr(env, "BITPLOY_URL=https://shop.example.com") {
		t.Fatalf("env = %v", env)
	}
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestRemoveAppDomainInUse(t *testing.T) {
	e := newDomainEnv(t)
	ctx := context.Background()
	d := e.addAppDomain(t, "example.com")
	e.addApp(t, "app_1", "shop", "usr_owner", "web")
	e.addApp(t, "app_2", "blog", "usr_owner", "web")
	e.exec(t, `UPDATE apps SET base_domain = 'example.com'`)

	list, err := e.svc.AdminListAppDomains(ctx, e.admin)
	if err != nil || len(list.Items) != 2 || !list.Items[0].Default || list.Items[1].AppCount != 2 || list.ServerIPv4 != "203.0.113.10" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	err = e.svc.AdminRemoveAppDomain(ctx, e.admin, d.ID)
	wantCode(t, err, "app_domain_in_use")
	if !strings.Contains(err.Error(), "2 apps still use example.com") {
		t.Fatalf("message: %v", err)
	}
	wantCode(t, e.svc.AdminRemoveAppDomain(ctx, e.admin, DefaultAppDomainID), "app_domain_default")

	e.exec(t, `UPDATE apps SET base_domain = ''`)
	catch := filepath.Join(e.svc.Routes.Dir, "_catchall.yml")
	if b, _ := os.ReadFile(catch); !strings.Contains(string(b), `example\.com`) {
		t.Fatalf("catch-all lacks the domain:\n%s", b)
	}
	if err := e.svc.AdminRemoveAppDomain(ctx, e.admin, d.ID); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(catch); strings.Contains(string(b), "example") || !strings.Contains(string(b), `bitploy\.test`) {
		t.Fatalf("catch-all after removal:\n%s", b)
	}
	if got := e.svc.AllAppDomains(); len(got) != 1 {
		t.Fatalf("domains after removal: %v", got)
	}
}

func TestAppDomainValidationInService(t *testing.T) {
	e := newDomainEnv(t)
	ctx := context.Background()
	e.cfgPublic(t, "https://console.example.net")
	e.addApp(t, "app_1", "shop", "usr_owner", "web")
	e.exec(t, `INSERT INTO domains (id, app_id, hostname, txt_token, check_until, created_at) VALUES ('dom_1', 'app_1', 'www.shop.example.org', 't', 1, 1)`)

	for _, tc := range []struct{ in, code string }{
		{"bitploy.test", "app_domain_exists"},
		{"x.bitploy.test", "app_domain_invalid"},
		{"console.example.net", "app_domain_invalid"},
		{"not a domain", "app_domain_invalid"},
		{"localhost", "app_domain_invalid"},
		{"shop.example.org", "app_domain_custom_in_use"},
		{"example.org", "app_domain_custom_in_use"},
	} {
		_, err := e.svc.AdminAddAppDomain(ctx, e.admin, AddAppDomainInput{Domain: tc.in})
		wantCode(t, err, tc.code)
	}
	e.addAppDomain(t, "example.net")
	_, err := e.svc.AdminAddAppDomain(ctx, e.admin, AddAppDomainInput{Domain: "EXAMPLE.net."})
	wantCode(t, err, "app_domain_exists")

	// The dashboard lives directly under the new domain: its label is reserved there only.
	e.exec(t, `UPDATE users SET quota_apps = 50 WHERE id = 'usr_admin'`)
	_, err = e.svc.CreateApp(ctx, e.admin, CreateAppInput{Name: "C", Slug: "console", Domain: "example.net"})
	wantCode(t, err, "slug_reserved")
	if _, err := e.svc.CreateApp(ctx, e.admin, CreateAppInput{Name: "C", Slug: "console"}); err != nil {
		t.Fatalf("console on the default domain: %v", err)
	}

	// Custom domains may not be added below an app domain.
	_, err = e.svc.AddDomain(ctx, e.owner, "shop", "www.example.net")
	wantCode(t, err, "domain_reserved")
}

func (e *opsEnv) cfgPublic(t *testing.T, u string) {
	t.Helper()
	e.svc.Cfg.PublicURL = u
}

func TestAppDomainCertificate(t *testing.T) {
	e := newDomainEnv(t)
	ctx := context.Background()
	wrongCert, wrongKey := testCert(t, e.now, "other.org", "*.other.org")
	_, err := e.svc.AdminAddAppDomain(ctx, e.admin, AddAppDomainInput{Domain: "example.com", Certificate: wrongCert, PrivateKey: wrongKey})
	wantCode(t, err, "cert_wrong_domain")
	cert, key := testCert(t, e.now, "example.com", "*.example.com")
	_, otherKey := testCert(t, e.now, "example.com", "*.example.com")
	_, err = e.svc.AdminAddAppDomain(ctx, e.admin, AddAppDomainInput{Domain: "example.com", Certificate: cert, PrivateKey: otherKey})
	wantCode(t, err, "cert_invalid")
	_, err = e.svc.AdminAddAppDomain(ctx, e.admin, AddAppDomainInput{Domain: "example.com", Certificate: cert})
	wantCode(t, err, "cert_incomplete")

	d, err := e.svc.AdminAddAppDomain(ctx, e.admin, AddAppDomainInput{Domain: "example.com", Certificate: cert, PrivateKey: key})
	if err != nil {
		t.Fatal(err)
	}
	if d.Certificate != "own" || d.CertExpiresAt == nil {
		t.Fatalf("domain %+v", d)
	}
	dir := e.svc.Routes.Dir
	for _, f := range []string{"certs/example.com.crt", "certs/example.com.key"} {
		st, err := os.Stat(filepath.Join(dir, f))
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", f, st, err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "certs/example.com.key")); string(b) != strings.TrimSpace(key)+"\n" {
		t.Fatalf("key file differs")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "_certs.yml")); !strings.Contains(string(b), "/dynamic/certs/example.com.key") {
		t.Fatalf("_certs.yml:\n%s", b)
	}
	// The key is stored encrypted and never returned.
	var raw []byte
	_ = e.svc.Store.DB.QueryRow(`SELECT key_enc FROM app_domains`).Scan(&raw)
	if strings.Contains(string(raw), "PRIVATE KEY") {
		t.Fatal("key stored in plain text")
	}
	list, _ := e.svc.AdminListAppDomains(ctx, e.admin)
	if b, _ := json.Marshal(list); strings.Contains(string(b), "PRIVATE KEY") || strings.Contains(string(b), "BEGIN") {
		t.Fatalf("list leaks the certificate or key: %s", b)
	}

	// Files are written again from the database (a fresh data dir).
	e.svc.Routes.Dir = t.TempDir()
	if err := e.svc.ApplyAppDomains(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.svc.Routes.Dir, "certs/example.com.key")); err != nil {
		t.Fatal(err)
	}
}

func TestAppHostSlug(t *testing.T) {
	e := newDomainEnv(t)
	e.addAppDomain(t, "example.com")
	for host, want := range map[string]string{"shop.bitploy.test": "shop|bitploy.test", "shop.example.com": "shop|example.com", "shop.example.org": "|", "example.com": "|example.com", "bitploy.test": "|"} {
		slug, domain, _ := e.svc.AppHostSlug(host)
		if got := slug + "|" + domain; got != want {
			t.Errorf("AppHostSlug(%q) = %q, want %q", host, got, want)
		}
	}
}
