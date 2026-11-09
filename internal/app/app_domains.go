package app

// App domains: the default domain (BITPLOY_APPS_DOMAIN) plus extra wildcard
// domains an admin adds. An app lives at <slug>.<its domain>. Slugs stay
// unique across all domains, because the slug also names the app's
// containers, network, route file and volume; only the address changes.
//
// The admin decides who may use which extra domain (admins may use all).
// Each extra domain may carry a TLS certificate (a Cloudflare Origin
// certificate for <domain> and *.<domain>); bitploy writes it for Traefik,
// which picks it by SNI. The private key is stored encrypted, like env vars,
// and is never returned.
//
// An admin may give one app per extra domain the domain itself (address @,
// apps.on_root): it then answers on https://<domain> as well, and that is its
// address everywhere. The slug still names everything, and <slug>.<domain>
// keeps working.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/domains"
	"github.com/AVMG20/bitploy/internal/ids"
	"github.com/AVMG20/bitploy/internal/proxy"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// DefaultAppDomainID stands for the default domain in the admin list.
const DefaultAppDomainID = "default"

// AppDomain is one domain apps can live under, as the admin sees it.
type AppDomain struct {
	ID      string `json:"id" doc:"apd_..., or default for the domain from BITPLOY_APPS_DOMAIN"`
	Domain  string `json:"domain"`
	Default bool   `json:"default" doc:"The domain from the server settings: every user may use it and it cannot be removed"`
	// Certificate is install (the default domain: the certificate set up when
	// bitploy was installed), own (a certificate was added for this domain) or
	// none (Traefik serves the default certificate, which does not match).
	Certificate   string     `json:"certificate" enum:"install,own,none"`
	CertExpiresAt *time.Time `json:"cert_expires_at"`
	AppCount      int64      `json:"app_count" doc:"Apps whose address is on this domain"`
	UserCount     int64      `json:"user_count" doc:"People (admins aside) allowed to use it; 0 for the default domain, which everyone may use"`
	RootApp       *RootApp   `json:"root_app" doc:"The app on the domain itself (address @); null for none"`
	CreatedAt     *time.Time `json:"created_at"`
}

// RootApp is the app that holds the root of an app domain.
type RootApp struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// AppDomainList is the admin's list of domains plus what DNS needs.
type AppDomainList struct {
	Items      []AppDomain `json:"items"`
	ServerIPv4 string      `json:"server_ipv4" doc:"BITPLOY_SERVER_IPV4: the address for the A record; empty when not set"`
	ServerIPv6 string      `json:"server_ipv6" doc:"BITPLOY_SERVER_IPV6: the address for the AAAA record; empty when not set"`
}

// AddAppDomainInput adds a wildcard domain for app addresses.
type AddAppDomainInput struct {
	Domain      string `json:"domain" doc:"Like example.com. Apps get <name>.example.com."`
	Certificate string `json:"certificate,omitempty" doc:"PEM certificate for the domain and *.domain (Cloudflare Origin certificate). Optional."`
	PrivateKey  string `json:"private_key,omitempty" doc:"PEM private key of the certificate. Stored encrypted, never returned."`
}

// AppDomainCertInput sets or replaces the certificate of an extra domain.
type AppDomainCertInput struct {
	Certificate string `json:"certificate" doc:"PEM certificate for the domain and *.domain"`
	PrivateKey  string `json:"private_key" doc:"PEM private key of the certificate. Stored encrypted, never returned."`
}

// AppDomainChoice is a domain the user may put apps on (whoami / me).
type AppDomainChoice struct {
	Domain      string `json:"domain"`
	Default     bool   `json:"default"`
	URLTemplate string `json:"url_template" doc:"An app's address on this domain with {slug} in place of its name"`
	// The domain itself (address @): only for admins with a dashboard session.
	RootAllowed bool   `json:"root_allowed,omitempty" doc:"Admins: an app can get the domain itself as its address (@ in place of the name)"`
	RootApp     string `json:"root_app,omitempty" doc:"Admins: the slug of the app on the domain itself"`
	RootDNS     string `json:"root_dns,omitempty" doc:"Admins: the DNS record the domain itself needs for that"`
}

// appDomainCache holds the names of the extra domains, for the hot paths
// (catch-all pages on every request, custom-domain rules). It is filled on
// first use and refreshed after every change.
type appDomainCache struct {
	mu     sync.RWMutex
	loaded bool
	extra  []string
	// apexCert: the stored certificate of the domain also covers the domain
	// itself, not only *.domain.
	apexCert map[string]bool
}

func (s *Service) defaultAppDomain() string { return strings.ToLower(s.Cfg.AppsDomain) }

// extraAppDomains returns the extra domains, sorted.
func (s *Service) extraAppDomains() []string {
	c := &s.appDomains
	c.mu.RLock()
	if c.loaded {
		out := c.extra
		c.mu.RUnlock()
		return out
	}
	c.mu.RUnlock()
	if err := s.reloadAppDomains(context.Background()); err != nil {
		s.Log.Error("loading app domains failed", "err", err)
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.extra
}

func (s *Service) reloadAppDomains(ctx context.Context) error {
	rows, err := s.Q.ListAppDomains(ctx)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(rows))
	apex := map[string]bool{}
	for _, r := range rows {
		d := strings.ToLower(r.Domain)
		names = append(names, d)
		apex[d] = certCovers(r.CertPem, d)
	}
	sort.Strings(names)
	s.appDomains.mu.Lock()
	s.appDomains.extra, s.appDomains.apexCert, s.appDomains.loaded = names, apex, true
	s.appDomains.mu.Unlock()
	return nil
}

// certCovers reports whether the first certificate in certPEM is valid for host.
func certCovers(certPEM, host string) bool {
	b, _ := pem.Decode([]byte(certPEM))
	if b == nil || b.Type != "CERTIFICATE" {
		return false
	}
	c, err := x509.ParseCertificate(b.Bytes)
	return err == nil && c.VerifyHostname(host) == nil
}

// apexHasCert says the stored certificate of an extra domain covers the
// domain itself, so its root route can use it.
func (s *Service) apexHasCert(domain string) bool {
	s.extraAppDomains() // loads the cache
	s.appDomains.mu.RLock()
	defer s.appDomains.mu.RUnlock()
	return s.appDomains.apexCert[strings.ToLower(domain)]
}

// AllAppDomains is the default domain followed by the extra ones.
func (s *Service) AllAppDomains() []string {
	return append([]string{s.defaultAppDomain()}, s.extraAppDomains()...)
}

// AppHostSlug reports whether host (lower case, no port) is <label>.<an app
// domain> and returns the label and the domain. The longest matching domain
// wins. An extra domain itself, which an app can hold (address @), comes back
// with an empty label.
func (s *Service) AppHostSlug(host string) (slug, domain string, ok bool) {
	for _, d := range s.extraAppDomains() {
		if host == d {
			return "", d, true
		}
	}
	best := ""
	for _, d := range s.AllAppDomains() {
		if d != "" && strings.HasSuffix(host, "."+d) && len(d) > len(best) {
			best = d
		}
	}
	if best == "" {
		return "", "", false
	}
	return strings.TrimSuffix(host, "."+best), best, true
}

// AppDomainOf is the full domain of an app (an empty base_domain means the default).
func (s *Service) AppDomainOf(r db.App) string { return s.appDomainOf(r) }

func (s *Service) appDomainOf(r db.App) string {
	if r.BaseDomain == "" {
		return s.Cfg.AppsDomain
	}
	return r.BaseDomain
}

// appURL is the public address of an app on its own domain: the domain
// itself for the app that holds its root.
func (s *Service) appURL(r db.App) string {
	if root := appRoot(r); root != "" {
		return s.Cfg.AppRootURL(root)
	}
	return s.Cfg.AppURLOn(r.Slug, r.BaseDomain)
}

// appRoot is the domain whose root the app holds, or "".
func appRoot(r db.App) string {
	if r.OnRoot != 0 && r.BaseDomain != "" {
		return r.BaseDomain
	}
	return ""
}

// userExtraDomains lists the extra domains a user may put apps on. Admins may
// use every domain.
func (s *Service) userExtraDomains(ctx context.Context, u db.User) ([]string, error) {
	if u.Role == "admin" {
		return s.extraAppDomains(), nil
	}
	names, err := s.Q.ListUserAppDomainNames(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	for i := range names {
		names[i] = strings.ToLower(names[i])
	}
	return names, nil
}

// appDomainChoices is the whoami list: the default domain first. Admins with
// a dashboard session also see whether each extra domain can be an app's
// address itself, which app has it, and the DNS record it needs.
func (s *Service) appDomainChoices(ctx context.Context, a Actor, u db.User) []AppDomainChoice {
	out := []AppDomainChoice{{Domain: s.Cfg.AppsDomain, Default: true, URLTemplate: s.Cfg.AppURL("{slug}")}}
	extra, err := s.userExtraDomains(ctx, u)
	if err != nil {
		s.Log.Warn("listing the user's app domains failed", "user", u.ID, "err", err)
	}
	roots := map[string]string{}
	if mayClaimRoot(a) {
		if rows, err := s.Q.ListRootApps(ctx); err == nil {
			for _, r := range rows {
				roots[strings.ToLower(r.BaseDomain)] = r.Slug
			}
		}
	}
	for _, d := range extra {
		c := AppDomainChoice{Domain: d, URLTemplate: s.Cfg.AppURLOn("{slug}", d)}
		if mayClaimRoot(a) && s.rootBlocked(ctx, d) == nil {
			c.RootAllowed, c.RootApp, c.RootDNS = true, roots[d], s.rootDNS(d)
		}
		out = append(out, c)
	}
	return out
}

// mayClaimRoot: only admins, signed in to the dashboard, give an app the root
// of a domain. Everyone else uses a name (or adds their own custom domain).
func mayClaimRoot(a Actor) bool { return a.IsAdmin() && a.Scope == ScopeSession }

// rootBlocked refuses the root of a domain that is bitploy's own or already
// someone's custom domain. The default domain never has a root app: it is
// usually the dashboard's own host, and its certificate comes from the install.
func (s *Service) rootBlocked(ctx context.Context, domain string) error {
	if domain == "" || domain == s.defaultAppDomain() {
		return apperr.Unprocessable("root_default_domain",
			"The address @ works on the extra app domains only, not on "+s.Cfg.AppsDomain+".",
			"Pick another domain next to the address, or type a name.")
	}
	rules := s.domainRules()
	if domain == rules.PublicHost || (rules.EdgeHost != "" && domain == rules.EdgeHost) {
		return apperr.Unprocessable("root_reserved", domain+" is used by bitploy itself.", "Pick another domain, or type a name.")
	}
	if d, err := s.Q.OpsGetDomainByHostname(ctx, domain); err == nil {
		holder := "another app"
		if r, err := s.Q.GetApp(ctx, d.AppID); err == nil {
			holder = r.Slug
		}
		return apperr.Conflict("root_custom_domain", domain+" is a custom domain of "+holder+".",
			"Remove it from that app's Domains tab first.")
	} else if !errors.Is(err, sql.ErrNoRows) {
		return apperr.Internal(err)
	}
	return nil
}

// checkRootClaim runs before an app gets the root of domain (address @).
func (s *Service) checkRootClaim(ctx context.Context, a Actor, row db.App, domain string) error {
	if !mayClaimRoot(a) {
		return apperr.Forbidden("root_admin_only",
			"Only a bitploy admin can give an app the domain itself as its address.",
			"Type a name for the address instead, like "+row.Slug+".")
	}
	if err := s.rootBlocked(ctx, domain); err != nil {
		return err
	}
	holder, err := s.Q.GetRootApp(ctx, domain)
	if err == nil && holder.ID != row.ID {
		return rootTaken(domain, holder)
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return apperr.Internal(err)
	}
	return nil
}

func rootNoAddress() error {
	return apperr.BadRequest("root_no_address", "This app has no web address, so it cannot use the domain itself.",
		"Only an app with a web address can have the domain itself as its address.")
}

func rootTaken(domain string, holder db.App) error {
	return apperr.Conflict("root_taken",
		domain+" is already the address of "+holder.Name+" ("+holder.Slug+").",
		"Give that app another address in its Settings first.")
}

// rootDNS is the one line of DNS the domain itself needs. With a stored
// certificate that covers it, Cloudflare's proxy can stay on, like for the
// subdomains; without one the server gets a Let's Encrypt certificate, which
// needs the record to point straight at it.
func (s *Service) rootDNS(domain string) string {
	v4, v6 := s.Cfg.ServerIPv4, s.Cfg.ServerIPv6
	var target string
	switch {
	case v4 != "" && v6 != "":
		target = "an A record to " + v4 + " and an AAAA record to " + v6
	case v4 != "":
		target = "an A record to " + v4
	case v6 != "":
		target = "an AAAA record to " + v6
	case s.Cfg.EdgeHost != "":
		target = "a CNAME to " + s.Cfg.EdgeHost + " (CNAME flattening)"
	default:
		target = "an A record to this server's IP address"
	}
	if s.apexHasCert(domain) {
		return "DNS: " + domain + " needs " + target + ", proxied in Cloudflare like the subdomains."
	}
	return "DNS: " + domain + " needs " + target + ", DNS only (grey cloud in Cloudflare)."
}

// pickAppDomain checks that the user may put an app on the requested domain
// and returns what goes into apps.base_domain: empty for the default domain.
func (s *Service) pickAppDomain(ctx context.Context, u db.User, requested string) (string, error) {
	d := domains.NormalizeAppDomain(requested)
	if d == "" || d == s.defaultAppDomain() {
		return "", nil
	}
	allowed, err := s.userExtraDomains(ctx, u)
	if err != nil {
		return "", apperr.Internal(err)
	}
	for _, a := range allowed {
		if a == d {
			return d, nil
		}
	}
	choices := append([]string{s.Cfg.AppsDomain}, allowed...)
	return "", apperr.Forbidden("domain_not_allowed",
		quote(d)+" is not a domain you can put apps on.",
		"Use one of: "+strings.Join(choices, ", ")+" (leave domain out for "+s.Cfg.AppsDomain+"). A bitploy admin can give you more domains.").WithNext("whoami")
}

// normalizeBaseDomain is what a requested domain becomes in apps.base_domain,
// without any permission check: empty for the default domain.
func (s *Service) normalizeBaseDomain(requested string) string {
	d := domains.NormalizeAppDomain(requested)
	if d == s.defaultAppDomain() {
		return ""
	}
	return d
}

// checkDomainStillThere runs inside the transaction that puts an app on an
// extra domain, so an admin removing that domain at the same moment cannot
// leave an app on a domain that no longer exists.
func checkDomainStillThere(ctx context.Context, q *db.Queries, baseDomain string) error {
	if baseDomain == "" {
		return nil
	}
	if _, err := q.GetAppDomainByName(ctx, baseDomain); errors.Is(err, sql.ErrNoRows) {
		return apperr.Conflict("domain_not_allowed", "An admin has removed the domain "+quote(baseDomain)+".", "Pick another domain.")
	} else if err != nil {
		return err
	}
	return nil
}

// reservedHostErr refuses an address that is bitploy's own: the dashboard and
// the edge host may live directly under an app domain.
func (s *Service) reservedHostErr(slug, baseDomain string) error {
	rules := s.domainRules()
	host := s.Cfg.AppHostOn(slug, baseDomain)
	if host == rules.PublicHost || (rules.EdgeHost != "" && host == rules.EdgeHost) {
		return apperr.Unprocessable("slug_reserved",
			"The subdomain "+quote(slug)+" is reserved by bitploy.",
			"Pick another name. bitploy uses "+host+" itself.")
	}
	return nil
}

// ---- Traefik files ----

// ApplyAppDomains writes the catch-all route for every app domain and the
// certificates of the extra domains. Called at startup and after every
// change to the domains.
func (s *Service) ApplyAppDomains(ctx context.Context) error {
	if err := s.reloadAppDomains(ctx); err != nil {
		return err
	}
	if s.Routes == nil { // tests without Traefik files
		return nil
	}
	if err := s.Routes.WriteCatchAll(s.AllAppDomains(), s.extraAppDomains(), s.Cfg.InternalURL); err != nil {
		return err
	}
	rows, err := s.Q.ListAppDomains(ctx)
	if err != nil {
		return err
	}
	var certs []proxy.Cert
	for _, r := range rows {
		if r.CertPem == "" || len(r.KeyEnc) == 0 {
			continue
		}
		key, err := s.Crypto.Open(r.KeyEnc, appDomainKeyAAD(r.ID))
		if err != nil {
			s.Log.Error("the private key of an app domain cannot be decrypted; add the certificate again", "domain", r.Domain, "err", err)
			continue
		}
		certs = append(certs, proxy.Cert{Domain: strings.ToLower(r.Domain), Cert: r.CertPem, Key: string(key)})
	}
	if err := s.Routes.WriteCerts(certs); err != nil {
		return err
	}
	// A new certificate may cover the domain itself or no longer do so: the
	// route of the app on it picks the stored certificate or Let's Encrypt.
	roots, err := s.Q.ListRootApps(ctx)
	if err != nil {
		return err
	}
	for _, r := range roots {
		if err := s.SyncRoute(ctx, r.ID); err != nil {
			s.Log.Warn("rewriting the route of a root app failed", "app", r.Slug, "err", err)
		}
	}
	return nil
}

func appDomainKeyAAD(id string) string { return "app-domain-key\x00" + id }

// applyAppDomainsAfterChange is ApplyAppDomains for admin actions: the change
// is saved either way, so a failure is logged and reported, not rolled back.
func (s *Service) applyAppDomainsAfterChange(ctx context.Context) error {
	if err := s.ApplyAppDomains(ctx); err != nil {
		s.Log.Error("writing the Traefik files for app domains failed", "err", err)
		return apperr.Internal(err)
	}
	return nil
}

// ---- certificates ----

type parsedCert struct {
	certPEM  string
	keyPEM   string
	notAfter time.Time
}

// parseDomainCert checks a pasted certificate and key: they belong together,
// the certificate covers <domain> and *.<domain>, and it has not expired.
func (s *Service) parseDomainCert(domain, certPEM, keyPEM string) (*parsedCert, error) {
	certPEM, keyPEM = strings.TrimSpace(certPEM), strings.TrimSpace(keyPEM)
	if certPEM == "" && keyPEM == "" {
		return nil, nil
	}
	if certPEM == "" || keyPEM == "" {
		return nil, apperr.BadRequest("cert_incomplete", "Paste both the certificate and its private key.",
			"Cloudflare shows both on the same page when you create the Origin certificate.")
	}
	if len(certPEM) > 64<<10 || len(keyPEM) > 16<<10 {
		return nil, apperr.BadRequest("cert_invalid", "The certificate or key is far too long.", "Paste only the Origin certificate and its private key.")
	}
	pair, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return nil, apperr.BadRequest("cert_invalid",
			"The certificate and private key could not be read, or they do not belong together.",
			"Paste them exactly as Cloudflare shows them, including the -----BEGIN and -----END lines.")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, apperr.BadRequest("cert_invalid", "The certificate could not be read.", "Paste it exactly as Cloudflare shows it.")
	}
	if leaf.VerifyHostname("app."+domain) != nil {
		return nil, apperr.BadRequest("cert_wrong_domain",
			"This certificate is not for *."+domain+".",
			"In Cloudflare, create the Origin certificate for "+domain+" and *."+domain+".")
	}
	if s.Now().After(leaf.NotAfter) {
		return nil, apperr.BadRequest("cert_expired",
			fmt.Sprintf("This certificate expired on %s.", leaf.NotAfter.UTC().Format("2 January 2006")),
			"Create a new Origin certificate in Cloudflare and paste that one.")
	}
	return &parsedCert{certPEM: certPEM + "\n", keyPEM: keyPEM + "\n", notAfter: leaf.NotAfter}, nil
}

// ---- admin ----

func appDomainNotFound() *apperr.Error {
	return apperr.NotFound("app_domain_not_found", "There is no such app domain.", "Reload the list of domains.")
}

// AdminListAppDomains lists every app domain, the default one first.
func (s *Service) AdminListAppDomains(ctx context.Context, a Actor) (AppDomainList, error) {
	if err := RequireAdmin(a); err != nil {
		return AppDomainList{}, err
	}
	rows, err := s.Q.ListAppDomains(ctx)
	if err != nil {
		return AppDomainList{}, apperr.Internal(err)
	}
	counts, err := s.Q.CountAppsByBaseDomain(ctx)
	if err != nil {
		return AppDomainList{}, apperr.Internal(err)
	}
	apps := map[string]int64{}
	for _, c := range counts {
		apps[strings.ToLower(c.BaseDomain)] += c.Apps
	}
	grants, err := s.Q.ListAllUserAppDomains(ctx)
	if err != nil {
		return AppDomainList{}, apperr.Internal(err)
	}
	users := map[string]int64{}
	for _, g := range grants {
		users[g.DomainID]++
	}
	roots, err := s.Q.ListRootApps(ctx)
	if err != nil {
		return AppDomainList{}, apperr.Internal(err)
	}
	rootOf := map[string]*RootApp{}
	for _, r := range roots {
		rootOf[strings.ToLower(r.BaseDomain)] = &RootApp{Slug: r.Slug, Name: r.Name}
	}
	out := AppDomainList{
		Items:      []AppDomain{{ID: DefaultAppDomainID, Domain: s.Cfg.AppsDomain, Default: true, Certificate: "install", AppCount: apps[""] + apps[s.defaultAppDomain()]}},
		ServerIPv4: s.Cfg.ServerIPv4, ServerIPv6: s.Cfg.ServerIPv6,
	}
	for _, r := range rows {
		v := s.appDomainView(r)
		v.AppCount, v.UserCount, v.RootApp = apps[strings.ToLower(r.Domain)], users[r.ID], rootOf[strings.ToLower(r.Domain)]
		out.Items = append(out.Items, v)
	}
	return out, nil
}

func (s *Service) appDomainView(r db.AppDomain) AppDomain {
	created := ts(r.CreatedAt)
	v := AppDomain{ID: r.ID, Domain: r.Domain, Certificate: "none", CreatedAt: &created}
	if r.CertPem != "" {
		v.Certificate = "own"
		v.CertExpiresAt = tsPtr(r.CertNotAfter)
	}
	return v
}

func (s *Service) appDomainRules() domains.AppDomainRules {
	r := s.domainRules()
	return domains.AppDomainRules{Existing: s.AllAppDomains(), PublicHost: r.PublicHost, EdgeHost: r.EdgeHost}
}

// AdminAddAppDomain adds a wildcard domain for app addresses, with an
// optional certificate.
func (s *Service) AdminAddAppDomain(ctx context.Context, a Actor, in AddAppDomainInput) (AppDomain, error) {
	if err := RequireAdmin(a); err != nil {
		return AppDomain{}, err
	}
	if err := s.reloadAppDomains(ctx); err != nil {
		return AppDomain{}, apperr.Internal(err)
	}
	d, verr := domains.ValidateAppDomain(in.Domain, s.appDomainRules())
	if verr != nil {
		return AppDomain{}, verr
	}
	custom, err := s.Q.CustomDomainsUnder(ctx, d)
	if err != nil {
		return AppDomain{}, apperr.Internal(err)
	}
	if len(custom) > 0 {
		return AppDomain{}, apperr.Conflict("app_domain_custom_in_use",
			quote(custom[0])+" is already a custom domain of an app.",
			"Remove it from that app first (its Domains tab), or use another domain.")
	}
	cert, err := s.parseDomainCert(d, in.Certificate, in.PrivateKey)
	if err != nil {
		return AppDomain{}, err
	}
	id := ids.New(ids.AppDomain)
	p := db.InsertAppDomainParams{ID: id, Domain: d, CreatedAt: s.now()}
	if cert != nil {
		p.CertPem, p.KeyEnc = cert.certPEM, s.Crypto.Seal([]byte(cert.keyPEM), appDomainKeyAAD(id))
		p.CertNotAfter = sql.NullInt64{Int64: cert.notAfter.Unix(), Valid: true}
	}
	if err := s.Q.InsertAppDomain(ctx, p); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return AppDomain{}, apperr.Conflict("app_domain_exists", quote(d)+" is already an app domain.", "")
		}
		return AppDomain{}, apperr.Internal(err)
	}
	s.Audit(ctx, a, "admin.app_domain_add", "app_domain", id, map[string]any{"domain": d, "certificate": cert != nil})
	if err := s.applyAppDomainsAfterChange(ctx); err != nil {
		return AppDomain{}, err
	}
	row, err := s.Q.GetAppDomain(ctx, id)
	if err != nil {
		return AppDomain{}, apperr.Internal(err)
	}
	return s.appDomainView(row), nil
}

// AdminSetAppDomainCert sets or replaces the certificate of an extra domain.
func (s *Service) AdminSetAppDomainCert(ctx context.Context, a Actor, id string, in AppDomainCertInput) (AppDomain, error) {
	if err := RequireAdmin(a); err != nil {
		return AppDomain{}, err
	}
	row, err := s.Q.GetAppDomain(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return AppDomain{}, appDomainNotFound()
	} else if err != nil {
		return AppDomain{}, apperr.Internal(err)
	}
	cert, err := s.parseDomainCert(strings.ToLower(row.Domain), in.Certificate, in.PrivateKey)
	if err != nil {
		return AppDomain{}, err
	}
	if cert == nil {
		return AppDomain{}, apperr.BadRequest("cert_incomplete", "Paste both the certificate and its private key.",
			"Cloudflare shows both on the same page when you create the Origin certificate.")
	}
	if err := s.Q.SetAppDomainCert(ctx, db.SetAppDomainCertParams{
		CertPem: cert.certPEM, KeyEnc: s.Crypto.Seal([]byte(cert.keyPEM), appDomainKeyAAD(row.ID)),
		CertNotAfter: sql.NullInt64{Int64: cert.notAfter.Unix(), Valid: true}, ID: row.ID,
	}); err != nil {
		return AppDomain{}, apperr.Internal(err)
	}
	s.Audit(ctx, a, "admin.app_domain_cert", "app_domain", row.ID, map[string]any{"domain": row.Domain, "replaced": row.CertPem != "", "expires": cert.notAfter.Unix()})
	if err := s.applyAppDomainsAfterChange(ctx); err != nil {
		return AppDomain{}, err
	}
	row, err = s.Q.GetAppDomain(ctx, id)
	if err != nil {
		return AppDomain{}, apperr.Internal(err)
	}
	return s.appDomainView(row), nil
}

// AdminRemoveAppDomain removes an extra domain. Refused while any app uses it.
func (s *Service) AdminRemoveAppDomain(ctx context.Context, a Actor, id string) error {
	if err := RequireAdmin(a); err != nil {
		return err
	}
	if id == DefaultAppDomainID {
		return apperr.BadRequest("app_domain_default", "The default domain cannot be removed.",
			"It comes from BITPLOY_APPS_DOMAIN in the server settings.")
	}
	row, err := s.Q.GetAppDomain(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return appDomainNotFound()
	} else if err != nil {
		return apperr.Internal(err)
	}
	var grants int64
	err = s.Store.Tx(ctx, func(q *db.Queries) error {
		n, err := q.CountAppsOnDomain(ctx, strings.ToLower(row.Domain))
		if err != nil {
			return err
		}
		if n > 0 {
			what := fmt.Sprintf("%d apps still use", n)
			if n == 1 {
				what = "1 app still uses"
			}
			return apperr.Conflict("app_domain_in_use",
				fmt.Sprintf("%s %s, so it cannot be removed.", what, row.Domain),
				"Move those apps to another domain in their Settings, or delete them, then try again.")
		}
		if grants, err = q.CountUsersOnAppDomain(ctx, row.ID); err != nil {
			return err
		}
		return q.DeleteAppDomain(ctx, row.ID)
	})
	if err != nil {
		var ae *apperr.Error
		if errors.As(err, &ae) {
			return ae
		}
		return apperr.Internal(err)
	}
	s.Audit(ctx, a, "admin.app_domain_remove", "app_domain", row.ID, map[string]any{"domain": row.Domain, "users_with_access": grants})
	return s.applyAppDomainsAfterChange(ctx)
}

// AdminSetUserAppDomains replaces the extra domains a user may use. Taking
// a domain away does not move the user's apps that already live on it.
func (s *Service) AdminSetUserAppDomains(ctx context.Context, a Actor, userID string, domainIDs []string) (AdminUser, error) {
	if err := RequireAdmin(a); err != nil {
		return AdminUser{}, err
	}
	u, err := s.Q.GetUser(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminUser{}, acctUserNotFound()
	} else if err != nil {
		return AdminUser{}, apperr.Internal(err)
	}
	rows, err := s.Q.ListAppDomains(ctx)
	if err != nil {
		return AdminUser{}, apperr.Internal(err)
	}
	byID := map[string]string{}
	for _, r := range rows {
		byID[r.ID] = r.Domain
	}
	want := map[string]bool{}
	for _, id := range domainIDs {
		if id == DefaultAppDomainID {
			continue // everyone has it
		}
		if _, ok := byID[id]; !ok {
			return AdminUser{}, appDomainNotFound()
		}
		want[id] = true
	}
	have, err := s.Q.ListUserAppDomainIDs(ctx, u.ID)
	if err != nil {
		return AdminUser{}, apperr.Internal(err)
	}
	had := map[string]bool{}
	var removed []string
	for _, id := range have {
		had[id] = true
		if !want[id] {
			removed = append(removed, id)
		}
	}
	var added []string
	for id := range want {
		if !had[id] {
			added = append(added, id)
		}
	}
	sort.Strings(added)
	if len(added) > 0 || len(removed) > 0 {
		now := s.now()
		err = s.Store.Tx(ctx, func(q *db.Queries) error {
			for _, id := range added {
				if err := q.GrantUserAppDomain(ctx, db.GrantUserAppDomainParams{UserID: u.ID, DomainID: id, CreatedAt: now}); err != nil {
					return err
				}
			}
			for _, id := range removed {
				if err := q.RevokeUserAppDomain(ctx, db.RevokeUserAppDomainParams{UserID: u.ID, DomainID: id}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return AdminUser{}, apperr.Internal(err)
		}
		names := func(list []string) []string {
			out := make([]string, len(list))
			for i, id := range list {
				out[i] = byID[id]
			}
			return out
		}
		s.Audit(ctx, a, "admin.user_app_domains", "user", u.ID, map[string]any{"added": names(added), "removed": names(removed)})
	}
	return s.adminUser(ctx, u)
}
