package app

import (
	"context"
	"strings"
	"testing"
)

// rootEnv: the admin owns two running web apps (shop and blog) on example.com,
// and the owner (no admin) has one (site) there too.
func rootEnv(t *testing.T) (*opsEnv, AppDomain) {
	t.Helper()
	e := newDomainEnv(t)
	ctx := context.Background()
	d := e.addAppDomain(t, "example.com")
	if _, err := e.svc.AdminSetUserAppDomains(ctx, e.admin, "usr_owner", []string{d.ID}); err != nil {
		t.Fatal(err)
	}
	e.addApp(t, "app_1", "shop", "usr_admin", "web")
	e.addApp(t, "app_2", "blog", "usr_admin", "web")
	e.addApp(t, "app_3", "site", "usr_owner", "web")
	e.exec(t, `UPDATE apps SET base_domain = 'example.com', current_deployment_id = 'dep_'||slug`)
	for _, slug := range []string{"shop", "blog", "site"} {
		e.exec(t, `INSERT INTO deployments (id, app_id, seq, state, source_kind, channel, image, container_name, port, queued_at)
			SELECT 'dep_'||slug, id, 1, 'running', 'files', 'api', 'bitploy/'||slug||':1', 'bp-'||slug||'-1', 8080, 1 FROM apps WHERE slug = ?`, slug)
	}
	return e, d
}

func rootSlug() *string { s := "@"; return &s }

func TestRootDomainOnlyForAdmins(t *testing.T) {
	e, _ := rootEnv(t)
	ctx := context.Background()
	_, err := e.svc.UpdateApp(ctx, e.owner, "site", UpdateAppInput{Slug: rootSlug()})
	wantCode(t, err, "root_admin_only")
	// An admin's API token is not enough: admin powers need the dashboard.
	token := Actor{UserID: "usr_admin", Role: "admin", Channel: ChannelMCP, Scope: ScopeDeploy}
	_, err = e.svc.UpdateApp(ctx, token, "shop", UpdateAppInput{Slug: rootSlug()})
	wantCode(t, err, "root_admin_only")
	// Non-admins never see the root fields.
	me, _ := e.svc.Me(ctx, e.owner)
	for _, c := range me.AppDomains {
		if c.RootAllowed || c.RootDNS != "" || c.RootApp != "" {
			t.Fatalf("owner sees root info: %+v", c)
		}
	}
	// A new app cannot start on @.
	_, err = e.svc.CreateApp(ctx, e.admin, CreateAppInput{Name: "X", Slug: "@", Domain: "example.com"})
	wantCode(t, err, "slug_invalid")
}

func TestRootDomainClaimAndFree(t *testing.T) {
	e, d := rootEnv(t)
	ctx := context.Background()

	ap, err := e.svc.UpdateApp(ctx, e.admin, "shop", UpdateAppInput{Slug: rootSlug()})
	if err != nil {
		t.Fatal(err)
	}
	if !ap.Root || ap.Slug != "shop" || ap.URL != "https://example.com" || ap.Domain != "example.com" {
		t.Fatalf("app after @: root %v slug %q url %q domain %q", ap.Root, ap.Slug, ap.URL, ap.Domain)
	}
	if ap.Current == nil || ap.Current.URL != "https://example.com" {
		t.Fatalf("deployment url: %+v", ap.Current)
	}
	row, _ := e.svc.Q.GetApp(ctx, "app_1")
	env, aerr := e.svc.containerEnv(ctx, row, 8080)
	if aerr != nil {
		t.Fatal(aerr)
	}
	if !containsStr(env, "BITPLOY_URL=https://example.com") {
		t.Fatalf("env = %v", env)
	}

	// The route serves both the subdomain and the domain itself; without a
	// stored certificate the domain itself gets Let's Encrypt.
	b, _ := e.svc.Routes.Read("shop")
	route := string(b)
	if !strings.Contains(route, "Host(`shop.example.com`)") || !strings.Contains(route, "Host(`example.com`)") || !strings.Contains(route, "certResolver: letsencrypt") {
		t.Fatalf("route:\n%s", route)
	}

	// The admin sees who holds it, in whoami and in Admin > Domains.
	me, _ := e.svc.Me(ctx, e.admin)
	if len(me.AppDomains) != 2 || !me.AppDomains[1].RootAllowed || me.AppDomains[1].RootApp != "shop" ||
		!strings.Contains(me.AppDomains[1].RootDNS, "203.0.113.10") || !strings.Contains(me.AppDomains[1].RootDNS, "DNS only") {
		t.Fatalf("admin app_domains = %+v", me.AppDomains)
	}
	if me.AppDomains[0].RootAllowed {
		t.Fatal("the default domain offers @")
	}
	list, _ := e.svc.AdminListAppDomains(ctx, e.admin)
	if list.Items[0].RootApp != nil || list.Items[1].RootApp == nil || list.Items[1].RootApp.Slug != "shop" {
		t.Fatalf("admin list = %+v", list.Items)
	}

	// One app per domain: the other gets a conflict naming the holder.
	_, err = e.svc.UpdateApp(ctx, e.admin, "blog", UpdateAppInput{Slug: rootSlug()})
	wantCode(t, err, "root_taken")
	if !strings.Contains(err.Error(), "shop") {
		t.Fatalf("conflict does not name the holder: %v", err)
	}
	// The unique index holds even without the check.
	if _, err := e.svc.Store.DB.Exec(`UPDATE apps SET on_root = 1 WHERE id = 'app_2'`); err == nil {
		t.Fatal("two apps on the root of one domain")
	}
	// Sending @ again is no change.
	if ap, err := e.svc.UpdateApp(ctx, e.admin, "shop", UpdateAppInput{Slug: rootSlug()}); err != nil || !ap.Root {
		t.Fatalf("@ again: %v %v", ap.Root, err)
	}

	// Its own name again frees the root at once, without a reservation.
	same := "shop"
	ap, err = e.svc.UpdateApp(ctx, e.admin, "shop", UpdateAppInput{Slug: &same})
	if err != nil {
		t.Fatal(err)
	}
	if ap.Root || ap.URL != "https://shop.example.com" {
		t.Fatalf("after freeing: root %v url %q", ap.Root, ap.URL)
	}
	if b, _ := e.svc.Routes.Read("shop"); strings.Contains(string(b), "Host(`example.com`)") {
		t.Fatalf("route still has the root:\n%s", b)
	}
	ap, err = e.svc.UpdateApp(ctx, e.admin, "blog", UpdateAppInput{Slug: rootSlug()})
	if err != nil || !ap.Root {
		t.Fatalf("blog after shop left: %v %v", ap.Root, err)
	}

	// A new name takes it off the root too (the old name is reserved as usual).
	renamed := "journal"
	ap, err = e.svc.UpdateApp(ctx, e.admin, "blog", UpdateAppInput{Slug: &renamed})
	if err != nil {
		t.Fatal(err)
	}
	if ap.Root || ap.URL != "https://journal.example.com" {
		t.Fatalf("after rename: root %v url %q", ap.Root, ap.URL)
	}
	if b, _ := e.svc.Routes.Read("journal"); strings.Contains(string(b), "Host(`example.com`)") {
		t.Fatalf("renamed route still has the root:\n%s", b)
	}

	// Moving to another domain without @ leaves the root; with @ it moves along.
	e.addAppDomain(t, "example.org")
	if _, err := e.svc.UpdateApp(ctx, e.admin, "shop", UpdateAppInput{Slug: rootSlug()}); err != nil {
		t.Fatal(err)
	}
	org := "example.org"
	ap, err = e.svc.UpdateApp(ctx, e.admin, "shop", UpdateAppInput{Domain: &org, Slug: rootSlug()})
	if err != nil || !ap.Root || ap.URL != "https://example.org" {
		t.Fatalf("move with @: %+v %v", ap, err)
	}
	if ap, err := e.svc.UpdateApp(ctx, e.admin, "journal", UpdateAppInput{Slug: rootSlug()}); err != nil || ap.URL != "https://example.com" {
		t.Fatalf("example.com after shop moved: %v %v", ap.URL, err)
	}
	com := "example.com"
	ap, err = e.svc.UpdateApp(ctx, e.admin, "shop", UpdateAppInput{Domain: &com})
	if err != nil || ap.Root || ap.URL != "https://shop.example.com" {
		t.Fatalf("move without @: root %v url %q %v", ap.Root, ap.URL, err)
	}
	if n, _ := e.svc.Q.ListRootApps(ctx); len(n) != 1 || n[0].Slug != "journal" {
		t.Fatalf("root apps = %+v", n)
	}

	// The domain cannot be removed while an app holds its root (it is on it).
	wantCode(t, e.svc.AdminRemoveAppDomain(ctx, e.admin, d.ID), "app_domain_in_use")
}

func TestRootDomainRefused(t *testing.T) {
	e, _ := rootEnv(t)
	ctx := context.Background()

	// The default domain has no root app.
	e.addApp(t, "app_4", "home", "usr_admin", "web")
	_, err := e.svc.UpdateApp(ctx, e.admin, "home", UpdateAppInput{Slug: rootSlug()})
	wantCode(t, err, "root_default_domain")
	org := "bitploy.test"
	_, err = e.svc.UpdateApp(ctx, e.admin, "shop", UpdateAppInput{Slug: rootSlug(), Domain: &org})
	wantCode(t, err, "root_default_domain")

	// An app without a web address.
	e.exec(t, `UPDATE apps SET type = 'worker' WHERE slug = 'blog'`)
	_, err = e.svc.UpdateApp(ctx, e.admin, "blog", UpdateAppInput{Slug: rootSlug()})
	wantCode(t, err, "root_no_address")

	// The dashboard's own host (BITPLOY_PUBLIC_URL moved onto the domain) and the edge host.
	e.cfgPublic(t, "https://example.com")
	_, err = e.svc.UpdateApp(ctx, e.admin, "shop", UpdateAppInput{Slug: rootSlug()})
	wantCode(t, err, "root_reserved")
	if me, _ := e.svc.Me(ctx, e.admin); me.AppDomains[1].RootAllowed {
		t.Fatal("@ offered on the dashboard's host")
	}
	e.cfgPublic(t, "https://bitploy.test")
	e.svc.Cfg.EdgeHost = "example.com"
	_, err = e.svc.UpdateApp(ctx, e.admin, "shop", UpdateAppInput{Slug: rootSlug()})
	wantCode(t, err, "root_reserved")
	e.svc.Cfg.EdgeHost = "edge.bitploy.test"

	// Already a custom domain of an app (added before the app domain was).
	e.exec(t, `INSERT INTO domains (id, app_id, hostname, status, txt_token, check_until, created_at) VALUES ('dom_1', 'app_3', 'example.com', 'active', 't', 1, 1)`)
	_, err = e.svc.UpdateApp(ctx, e.admin, "shop", UpdateAppInput{Slug: rootSlug()})
	wantCode(t, err, "root_custom_domain")
	if !strings.Contains(err.Error(), "site") {
		t.Fatalf("message does not name the app: %v", err)
	}
}

func TestRootDomainCertificate(t *testing.T) {
	e, d := rootEnv(t)
	ctx := context.Background()
	if _, err := e.svc.UpdateApp(ctx, e.admin, "shop", UpdateAppInput{Slug: rootSlug()}); err != nil {
		t.Fatal(err)
	}
	rootRouter := func() string {
		t.Helper()
		b, _ := e.svc.Routes.Read("shop")
		s := string(b)
		i := strings.Index(s, "app-shop_root:")
		if i < 0 {
			t.Fatalf("no root router:\n%s", s)
		}
		return s[i:]
	}
	if r := rootRouter(); !strings.Contains(r, "letsencrypt") {
		t.Fatalf("root router without a certificate:\n%s", r)
	}

	// A certificate for *.example.com only: still Let's Encrypt for the domain itself.
	cert, key := testCert(t, e.now, "*.example.com")
	if _, err := e.svc.AdminSetAppDomainCert(ctx, e.admin, d.ID, AppDomainCertInput{Certificate: cert, PrivateKey: key}); err != nil {
		t.Fatal(err)
	}
	if r := rootRouter(); !strings.Contains(r, "letsencrypt") {
		t.Fatalf("root router with a wildcard-only certificate:\n%s", r)
	}
	if me, _ := e.svc.Me(ctx, e.admin); !strings.Contains(me.AppDomains[1].RootDNS, "DNS only") {
		t.Fatalf("dns line: %q", me.AppDomains[1].RootDNS)
	}

	// The Origin certificate for example.com and *.example.com: the route uses it.
	cert, key = testCert(t, e.now, "example.com", "*.example.com")
	if _, err := e.svc.AdminSetAppDomainCert(ctx, e.admin, d.ID, AppDomainCertInput{Certificate: cert, PrivateKey: key}); err != nil {
		t.Fatal(err)
	}
	if b, _ := e.svc.Routes.Read("shop"); strings.Contains(string(b), "letsencrypt") || !strings.Contains(string(b), "Host(`example.com`)") {
		t.Fatalf("route with the apex certificate:\n%s", b)
	}
	if me, _ := e.svc.Me(ctx, e.admin); !strings.Contains(me.AppDomains[1].RootDNS, "proxied") {
		t.Fatalf("dns line: %q", me.AppDomains[1].RootDNS)
	}
}
