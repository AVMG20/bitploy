package app

// Custom domains (M6): add, check, remove, and the background checker.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/domains"
	"github.com/AVMG20/bitploy/internal/ids"
	"github.com/AVMG20/bitploy/internal/store/db"
)

const (
	domainWatchFor     = 48 * time.Hour
	domainCheckTimeout = 20 * time.Second
)

func (s *Service) domainRules() domains.Rules {
	host := ""
	if u, err := url.Parse(s.Cfg.PublicURL); err == nil {
		host = strings.ToLower(u.Hostname())
	}
	return domains.Rules{AppsDomain: strings.ToLower(s.Cfg.AppsDomain), PublicHost: host, EdgeHost: strings.ToLower(s.Cfg.EdgeHost)}
}

func (s *Service) dnsResolvers() []domains.Named {
	if s.ops.resolvers != nil {
		return s.ops.resolvers
	}
	return domains.PublicResolvers()
}

func (s *Service) domainView(d db.Domain) Domain {
	recs, notes := domains.RecordsFor(d.Hostname, d.TxtToken, domains.Targets{
		EdgeHost: s.Cfg.EdgeHost, IPv4: s.Cfg.ServerIPv4, IPv6: s.Cfg.ServerIPv6,
	})
	v := Domain{
		ID: d.ID, Hostname: d.Hostname, Status: d.Status, LastError: d.LastError,
		Records: make([]DNSRecord, len(recs)), Notes: notes,
		LastCheckedAt: tsPtr(d.LastCheckedAt), CreatedAt: ts(d.CreatedAt), ActivatedAt: tsPtr(d.ActivatedAt),
	}
	for i, r := range recs {
		v.Records[i] = DNSRecord{Type: r.Type, Name: r.Name, Value: r.Value}
	}
	return v
}

// ListDomains returns the custom domains of an app with the DNS records to create.
func (s *Service) ListDomains(ctx context.Context, a Actor, ref string) ([]Domain, error) {
	app, err := s.LoadApp(ctx, a, ref, false)
	if err != nil {
		return nil, err
	}
	rows, err := s.Q.OpsListDomainsByApp(ctx, app.ID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	out := make([]Domain, 0, len(rows))
	for _, r := range rows {
		out = append(out, s.domainView(r))
	}
	return out, nil
}

// AddDomain reserves a hostname for an app as pending_dns and returns the
// DNS records the user must create. Adding the same hostname to the same app
// again returns the existing entry.
func (s *Service) AddDomain(ctx context.Context, a Actor, ref, hostname string) (Domain, error) {
	app, err := s.LoadApp(ctx, a, ref, true)
	if err != nil {
		return Domain{}, err
	}
	host, verr := domains.Validate(hostname, s.domainRules())
	if verr != nil {
		return Domain{}, verr
	}
	if app.Type == "worker" {
		return Domain{}, apperr.BadRequest("domain_worker_app",
			"This app is a worker, which has no web address, so a custom domain would show nothing.",
			"Add the domain to a web app instead.").WithNext("list_apps")
	}

	// A claim that is still waiting for DNS (or gave up) proves nothing, so it
	// must not block the real owner of the hostname: it is replaced below.
	// Hostnames that passed the DNS check (issuing_cert, active) stay taken.
	var stale *db.Domain
	if existing, err := s.Q.OpsGetDomainByHostname(ctx, host); err == nil {
		if existing.AppID == app.ID {
			return s.domainView(existing), nil
		}
		if existing.Status != "pending_dns" && existing.Status != "failed" {
			return Domain{}, apperr.Conflict("domain_taken",
				quote(host)+" is already used by another app.",
				"A hostname can belong to one app only. If it is yours, ask the owner to remove it, or use a different hostname.")
		}
		stale = &existing
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Domain{}, apperr.Internal(err)
	}

	owner, err := s.Q.GetUser(ctx, app.UserID)
	if err != nil {
		return Domain{}, apperr.Internal(err)
	}
	n, err := s.Q.OpsCountDomainsByApp(ctx, app.ID)
	if err != nil {
		return Domain{}, apperr.Internal(err)
	}
	if n >= owner.QuotaDomainsPerApp {
		return Domain{}, apperr.Forbidden("domain_limit",
			fmt.Sprintf("This app already has %d custom domains, which is the limit.", n),
			"Remove one with remove_domain, or ask a bitploy admin to raise the limit.").WithNext("remove_domain")
	}

	now := s.Now()
	id := ids.New(ids.Domain)
	err = s.Store.Tx(ctx, func(q *db.Queries) error {
		if stale != nil {
			n, err := q.OpsDeleteStaleDomain(ctx, stale.ID)
			if err != nil {
				return err
			}
			if n == 0 { // it passed DNS in the meantime
				return apperr.Conflict("domain_taken", quote(host)+" is already used by another app.",
					"A hostname can belong to one app only.")
			}
		}
		return q.OpsInsertDomain(ctx, db.OpsInsertDomainParams{
			ID: id, AppID: app.ID, Hostname: host, TxtToken: "bitploy-" + ids.Hex(32),
			CheckUntil: now.Add(domainWatchFor).Unix(), CreatedAt: now.Unix(),
		})
	})
	if err != nil {
		var ae *apperr.Error
		if errors.As(err, &ae) {
			return Domain{}, ae
		}
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return Domain{}, apperr.Conflict("domain_taken", quote(host)+" is already used by another app.",
				"A hostname can belong to one app only.")
		}
		return Domain{}, apperr.Internal(err)
	}
	details := map[string]any{"hostname": host, "app_id": app.ID, "app": app.Slug}
	if stale != nil {
		details["replaced_domain"] = stale.ID
		details["replaced_app_id"] = stale.AppID
		s.Audit(ctx, a, "domain.claim_replaced", "domain", stale.ID, map[string]any{
			"hostname": host, "app_id": stale.AppID, "status": stale.Status, "claimed_by_app_id": app.ID, "claimed_by_user_id": app.UserID,
		})
	}
	s.Audit(ctx, a, "domain.add", "domain", id, details)
	s.publishDomain(ctx, app.ID)
	row, err := s.Q.OpsGetDomainByHostname(ctx, host)
	if err != nil {
		return Domain{}, apperr.Internal(err)
	}
	return s.domainView(row), nil
}

// loadDomain finds a hostname that belongs to the app.
func (s *Service) loadDomain(ctx context.Context, app db.App, hostname string) (db.Domain, error) {
	host := domains.Normalize(hostname)
	d, err := s.Q.OpsGetDomainByHostname(ctx, host)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && d.AppID != app.ID) {
		return db.Domain{}, apperr.NotFound("domain_not_found",
			quote(host)+" is not a custom domain of "+app.Slug+".",
			"Call get_app to see the domains of this app, or add_domain to add one.").WithNext("add_domain")
	}
	if err != nil {
		return db.Domain{}, apperr.Internal(err)
	}
	return d, nil
}

// CheckDomain runs the DNS check now. When it passes the domain gets its
// route and certificate and becomes active.
func (s *Service) CheckDomain(ctx context.Context, a Actor, ref, hostname string) (Domain, error) {
	app, err := s.LoadApp(ctx, a, ref, true)
	if err != nil {
		return Domain{}, err
	}
	d, err := s.loadDomain(ctx, app, hostname)
	if err != nil {
		return Domain{}, err
	}
	d, err = s.runDomainCheck(ctx, a, d, true)
	if err != nil {
		return Domain{}, err
	}
	return s.domainView(d), nil
}

// RemoveDomain deletes a custom domain and takes its router out of the route file.
func (s *Service) RemoveDomain(ctx context.Context, a Actor, ref, hostname string) error {
	app, err := s.LoadApp(ctx, a, ref, true)
	if err != nil {
		return err
	}
	d, err := s.loadDomain(ctx, app, hostname)
	if err != nil {
		return err
	}
	if _, err := s.Q.OpsDeleteDomain(ctx, d.ID); err != nil {
		return apperr.Internal(err)
	}
	s.Audit(ctx, a, "domain.remove", "domain", d.ID, map[string]any{"hostname": d.Hostname, "app_id": app.ID, "app": app.Slug})
	s.publishDomain(ctx, app.ID)
	if err := s.opsSyncRoute(ctx, app.ID); err != nil {
		// The row is gone, so the next reconcile drops the router; do not fail the request.
		s.Log.Warn("route sync after domain removal failed", "app", app.Slug, "domain", d.Hostname, "err", err)
	}
	return nil
}

// ActiveHostnames returns the custom hostnames of an app whose DNS passed
// (issuing_cert or active); SyncRoute adds a router with certResolver
// letsencrypt for each.
func (s *Service) ActiveHostnames(ctx context.Context, appID string) ([]string, error) {
	return s.Q.OpsListHostnamesByStatuses(ctx, db.OpsListHostnamesByStatusesParams{
		AppID: appID, Statuses: []string{"issuing_cert", "active"},
	})
}

// runDomainCheck advances one domain. manual is true for check_domain, which
// also restarts the 48-hour watch of a domain that gave up.
func (s *Service) runDomainCheck(ctx context.Context, a Actor, d db.Domain, manual bool) (db.Domain, error) {
	now := s.Now()

	if d.Status == "issuing_cert" {
		return s.finishDomain(ctx, a, d) // DNS already passed; the route write failed or is retried
	}

	cctx, cancel := context.WithTimeout(ctx, domainCheckTimeout)
	res := domains.Check(cctx, s.dnsResolvers(), domains.Expect{
		Host: d.Hostname, Token: d.TxtToken, EdgeHost: s.Cfg.EdgeHost, IPv4: s.Cfg.ServerIPv4, IPv6: s.Cfg.ServerIPv6,
	})
	cancel()

	status, checkUntil, lastErr := d.Status, d.CheckUntil, ""
	if !res.OK {
		lastErr = res.Problem
		switch {
		case d.Status == "active":
			lastErr = "DNS no longer matches: " + res.Problem
		case manual && d.Status == "failed":
			status, checkUntil = "pending_dns", now.Add(domainWatchFor).Unix()
		case d.Status == "pending_dns" && now.Unix() >= d.CheckUntil:
			status = "failed"
			lastErr = res.Problem + ". Bitploy stopped checking after 48 hours; fix the DNS records and run check_domain again."
		}
		return s.saveDomain(ctx, a, d, status, lastErr, checkUntil, d.ActivatedAt)
	}

	if d.Status == "active" {
		return s.saveDomain(ctx, a, d, "active", "", d.CheckUntil, d.ActivatedAt)
	}
	d, err := s.saveDomain(ctx, a, d, "issuing_cert", "", d.CheckUntil, d.ActivatedAt)
	if err != nil {
		return d, err
	}
	return s.finishDomain(ctx, a, d)
}

// finishDomain writes the route (Traefik requests the certificate when it
// sees the router) and marks the domain active. If the route cannot be
// written the domain stays issuing_cert and the background checker retries.
func (s *Service) finishDomain(ctx context.Context, a Actor, d db.Domain) (db.Domain, error) {
	if err := s.opsSyncRoute(ctx, d.AppID); err != nil {
		s.Log.Warn("writing route for custom domain failed", "domain", d.Hostname, "err", err)
		return s.saveDomain(ctx, a, d, "issuing_cert",
			"DNS is correct, but the route could not be written yet; bitploy retries every 5 minutes.", d.CheckUntil, d.ActivatedAt)
	}
	return s.saveDomain(ctx, a, d, "active", "", d.CheckUntil, sql.NullInt64{Int64: s.now(), Valid: true})
}

// saveDomain stores the result of a check and audits status changes.
func (s *Service) saveDomain(ctx context.Context, a Actor, d db.Domain, status, lastErr string, checkUntil int64, activated sql.NullInt64) (db.Domain, error) {
	now := s.now()
	err := s.Q.OpsSetDomainStatus(ctx, db.OpsSetDomainStatusParams{
		Status: status, LastError: lastErr, LastCheckedAt: sql.NullInt64{Int64: now, Valid: true},
		CheckUntil: checkUntil, ActivatedAt: activated, ID: d.ID,
	})
	if err != nil {
		return d, apperr.Internal(err)
	}
	if status != d.Status {
		s.Audit(ctx, a, "domain."+status, "domain", d.ID, map[string]any{"hostname": d.Hostname, "app_id": d.AppID, "from": d.Status})
	}
	d.Status, d.LastError, d.CheckUntil, d.ActivatedAt = status, lastErr, checkUntil, activated
	s.publishDomain(ctx, d.AppID)
	d.LastCheckedAt = sql.NullInt64{Int64: now, Valid: true}
	return d, nil
}

// runDomainChecker re-checks pending domains every 5 minutes. A domain is
// watched for 48 hours after it was added; then it turns failed.
func (s *Service) runDomainChecker(ctx context.Context) {
	t := time.NewTicker(domainCheckEvery)
	defer t.Stop()
	first := time.After(30 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return
		case <-first:
		case <-t.C:
		}
		s.checkPendingDomains(ctx)
	}
}

// checkPendingDomains is one round of the background check.
func (s *Service) checkPendingDomains(ctx context.Context) {
	rows, err := s.Q.OpsListDomainsByStatuses(ctx, []string{"pending_dns", "issuing_cert"})
	if err != nil {
		s.Log.Error("listing pending domains failed", "err", err)
		return
	}
	for _, d := range rows {
		if ctx.Err() != nil {
			return
		}
		if _, err := s.runDomainCheck(ctx, System, d, false); err != nil {
			s.Log.Warn("domain check failed", "domain", d.Hostname, "err", err)
		}
	}
}
