package app

// Public ports: an app without a web address (a database from a service
// template, a worker, a database service of a compose app) can get a port on
// the server for a while, so its owner can connect with psql, TablePlus or
// redis-cli to look at it. bitploy picks a free port from BITPLOY_PUBLIC_PORTS
// and starts a forwarder container on the app's own network that Docker
// publishes on that port (dockerx/portproxy.go). The app container keeps no
// published port and never restarts for it. Off by default; closes by itself
// after 1 hour unless asked otherwise; at most BITPLOY_PUBLIC_PORTS_PER_USER
// per user. The reconciler closes expired ports, restarts missing forwarders
// and removes forwarders nobody wants.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/portproxy"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// Close-after choices of a public port.
const (
	CloseAfter1h    = "1h"
	CloseAfter24h   = "24h"
	CloseAfterNever = "never"
	// passwordMask stands in for a generated password in a connection URL.
	passwordMask = "********"
	// hostTakenFor is how long a port that something else on the host holds
	// is skipped when picking one.
	hostTakenFor = 10 * time.Minute
)

// PublicPort is App.public_port: whether the app can be reached on a port of
// the server, and how.
type PublicPort struct {
	// Available is false when this server hands out no public ports or this
	// app cannot have one; Reason says why.
	Available bool   `json:"available" doc:"True when this app can get a public port"`
	Reason    string `json:"reason,omitempty" doc:"Why not available: public_ports_off (the server has none), web_app (the app has a web address already)"`
	Enabled   bool   `json:"enabled" doc:"True while the port is open"`
	// Running is true when the forwarder runs; false while it starts or when
	// it failed (Error says why).
	Running bool   `json:"running" doc:"True when connections are forwarded right now"`
	Service string `json:"service,omitempty" doc:"The service it forwards to, for an app with several services"`
	Host    string `json:"host,omitempty" doc:"The server's address to connect to"`
	Port    int64  `json:"port,omitempty" doc:"The port on the server"`
	// Address is host:port.
	Address    string `json:"address,omitempty" doc:"host:port to put in psql, TablePlus or redis-cli"`
	TargetPort int64  `json:"target_port,omitempty" doc:"The port inside the app that connections go to"`
	// ConnectionURL is the template's main connection variable for the public
	// address, with the password replaced by ********. Only for apps from a
	// service template; reveal_public_connection gives it with the password.
	ConnectionURL string     `json:"connection_url,omitempty" doc:"Service template apps: the connection URL for this address, password shown as ********"`
	AllowFrom     []string   `json:"allow_from" doc:"Addresses that may connect (CIDRs); empty: everyone"`
	ExpiresAt     *time.Time `json:"expires_at" doc:"When bitploy closes it by itself; null: never"`
	OpenedAt      *time.Time `json:"opened_at,omitempty"`
	Error         string     `json:"error,omitempty" doc:"Why the forwarder is not running, when it is not"`
}

// PublicPortInput opens, changes or closes a public port.
type PublicPortInput struct {
	Enabled bool `json:"enabled" doc:"true opens the port (or changes it while open), false closes it"`
	// Service picks the service of an app with several services.
	Service *string `json:"service,omitempty" doc:"For an app with several services: the service to forward to, like db. Not for single apps."`
	// CloseAfter is when bitploy closes the port by itself, counted from now.
	CloseAfter *string `json:"close_after,omitempty" enum:"1h,24h,never" doc:"Close it by itself after 1h (default when opening), 24h, or never. While open, a new value starts counting from now."`
	// AllowFrom limits who may connect; nil keeps the current list.
	AllowFrom []string `json:"allow_from,omitempty" doc:"IPv4 addresses or networks that may connect, like 203.0.113.7 or 198.51.100.0/24 (at most 20). Leave out to keep the current list; [] lets everyone connect."`
	// OnlyMyIP adds the address of this request to AllowFrom.
	OnlyMyIP bool `json:"only_my_ip,omitempty" doc:"Add the IPv4 address this request comes from to allow_from"`
}

// PublicConnection is the connection URL of a public port with the password.
type PublicConnection struct {
	ConnectionURL string `json:"connection_url" doc:"Paste into psql, TablePlus or redis-cli. Contains the password: do not save it in files or chats."`
	Address       string `json:"address"`
	// Secrets are the template's generated passwords and keys by name (like
	// MEILI_MASTER_KEY, which is not part of MEILI_URL).
	Secrets map[string]string `json:"secrets" doc:"The generated passwords and keys of the service template, by variable name"`
}

// PublicPortsOverview is the admin view of the public ports.
type PublicPortsOverview struct {
	Enabled bool             `json:"enabled" doc:"False when BITPLOY_PUBLIC_PORTS is not set"`
	From    int              `json:"from"`
	To      int              `json:"to"`
	Host    string           `json:"host"`
	PerUser int              `json:"per_user"`
	Open    []OpenPublicPort `json:"open"`
}

// OpenPublicPort is one open public port in the admin view.
type OpenPublicPort struct {
	App        string     `json:"app"`
	AppName    string     `json:"app_name"`
	OwnerEmail string     `json:"owner_email"`
	Service    string     `json:"service,omitempty"`
	Port       int64      `json:"port"`
	AllowFrom  []string   `json:"allow_from"`
	ExpiresAt  *time.Time `json:"expires_at"`
	OpenedAt   time.Time  `json:"opened_at"`
	Error      string     `json:"error,omitempty"`
}

// portProxies runs the forwarder containers. The Docker client does it for
// real; tests use a fake.
type portProxies interface {
	EnsurePortProxy(ctx context.Context, s dockerx.PortProxySpec) error
	RemovePortProxy(ctx context.Context, appID string) error
	ListPortProxies(ctx context.Context) ([]dockerx.PortProxyInfo, error)
	PortProxyRunning(ctx context.Context, appID string) (bool, error)
}

// publicPortState serializes changes to forwarders and remembers ports that
// something else on the host holds.
type publicPortState struct {
	// run is held while a forwarder is started or removed together with the
	// row it belongs to, so a close and a reconcile cannot leave a forwarder
	// without a row behind. Taken before mu.
	run   sync.Mutex
	mu    sync.Mutex // guards taken
	fake  portProxies
	taken map[int64]time.Time
}

func (s *Service) proxies() portProxies {
	if s.pub.fake != nil {
		return s.pub.fake
	}
	if s.Docker == nil {
		return nil
	}
	return s.Docker
}

// usePortProxies replaces the forwarder runner (tests).
func (s *Service) usePortProxies(p portProxies) { s.pub.fake = p }

func publicPortsOff() *apperr.Error {
	return apperr.Unprocessable("public_ports_off", "This bitploy server does not hand out public ports.",
		"Ask the bitploy admin to set BITPLOY_PUBLIC_PORTS (like 30000-30099) and open those ports in the server's firewall.")
}

// publicTarget is where the forwarder of an app connects: the service's
// alias on bp-<slug> and the port inside.
type publicTarget struct {
	Service string // "" for a single app
	Alias   string
	Port    int64
}

// resolvePublicTarget checks that the app can have a public port and finds
// where it goes. service "" is the only service of a single app.
func (s *Service) resolvePublicTarget(ctx context.Context, r db.App, service string) (publicTarget, *apperr.Error) {
	if !isMulti(r) {
		if service != "" && service != singleService {
			return publicTarget{}, apperr.BadRequest("service_not_for_single_app",
				"The app "+r.Slug+" has one service, so there is no service to pick.", "Leave service out.").WithNext("set_public_port")
		}
		if r.Type == "web" {
			return publicTarget{}, publicPortWebApp(r)
		}
		port := int64(0)
		if tp, ok := templateOf(r); ok {
			port = tp.Port
		}
		if port == 0 {
			if svcs, _ := s.serviceViews(ctx, r); len(svcs) == 1 && svcs[0].Port != nil {
				port = *svcs[0].Port
			}
		}
		if port == 0 && r.Port.Valid {
			port = r.Port.Int64
		}
		if port <= 0 {
			return publicTarget{}, publicPortNoPort(r, "")
		}
		return publicTarget{Alias: singleService, Port: port}, nil
	}
	svcs, _ := s.serviceViews(ctx, r)
	if service == "" {
		cands := []string{}
		for _, sv := range svcs {
			if !(sv.Public && r.Type == "web") {
				cands = append(cands, sv.Name)
			}
		}
		if len(cands) != 1 {
			return publicTarget{}, apperr.BadRequest("service_required",
				"The app "+r.Slug+" has several services: say which one gets the public port.",
				"Pass service, one of: "+strings.Join(cands, ", ")+".").WithDetail("services", cands).WithNext("set_public_port")
		}
		service = cands[0]
	}
	for _, sv := range svcs {
		if sv.Name != service {
			continue
		}
		if sv.Public && r.Type == "web" {
			return publicTarget{}, publicPortWebApp(r)
		}
		if sv.Port == nil || *sv.Port <= 0 {
			return publicTarget{}, publicPortNoPort(r, service)
		}
		return publicTarget{Service: service, Alias: service, Port: *sv.Port}, nil
	}
	names := make([]string, len(svcs))
	for i, sv := range svcs {
		names[i] = sv.Name
	}
	return publicTarget{}, apperr.NotFound("service_not_found",
		fmt.Sprintf("The app %s has no service named %q. Its services are: %s.", r.Slug, service, strings.Join(names, ", ")),
		"Pass one of those names. get_app lists the services.").WithDetail("services", names).WithNext("get_app")
}

func publicPortWebApp(r db.App) *apperr.Error {
	return apperr.Unprocessable("public_port_web_app",
		"The app "+r.Slug+" has a web address, so it gets no public port: that would go around its HTTPS and password protection.",
		"Public ports are for databases and other apps without a web address. Open the app at its URL.")
}

func publicPortNoPort(r db.App, service string) *apperr.Error {
	what := "The app " + r.Slug
	hint := "Set the port the app listens on with update_app(port=...), then try again."
	if service != "" {
		what = "The service " + service + " of " + r.Slug
		hint = "Add the port to the service in the compose file (expose or ports), deploy, then try again."
	}
	return apperr.Unprocessable("public_port_no_port", what+" has no known port to forward to.", hint).WithNext("update_app")
}

// closeAfterSeconds turns a close_after choice into seconds; 0 is never.
func closeAfterSeconds(v string) (int64, *apperr.Error) {
	switch v {
	case CloseAfter1h:
		return 3600, nil
	case CloseAfter24h:
		return 86400, nil
	case CloseAfterNever:
		return 0, nil
	}
	return 0, apperr.BadRequest("close_after_invalid", "close_after must be 1h, 24h or never.",
		"Use 1h for a quick look, 24h for a day of work.").WithNext("set_public_port")
}

// parseAllowFrom checks an allow list and adds the caller's address when asked.
func parseAllowFrom(items []string, onlyMyIP bool, ip string) ([]string, *apperr.Error) {
	if onlyMyIP {
		a, err := netip.ParseAddr(strings.TrimSpace(ip))
		if err != nil {
			if ap, perr := netip.ParseAddrPort(strings.TrimSpace(ip)); perr == nil {
				a, err = ap.Addr(), nil
			}
		}
		a = a.Unmap()
		switch {
		case err != nil || !a.IsValid():
			return nil, apperr.BadRequest("my_ip_unknown", "bitploy could not tell the address this request comes from.",
				"Put your IPv4 address in allow_from instead (search the web for: what is my IP).").WithNext("set_public_port")
		case !a.Is4():
			return nil, apperr.Unprocessable("my_ip_not_ipv4",
				"This request comes from an IPv6 address ("+a.String()+"), and public ports are IPv4 only.",
				"Put your IPv4 address in allow_from instead (search the web for: what is my IPv4).").WithNext("set_public_port")
		}
		items = append(append([]string(nil), items...), a.String())
	}
	ps, err := portproxy.ParseAllow(items)
	if err != nil {
		return nil, apperr.BadRequest("allow_from_invalid", "allow_from: "+err.Error()+".",
			"Use IPv4 addresses like 203.0.113.7 or networks like 198.51.100.0/24.").WithNext("set_public_port")
	}
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.String()
	}
	return out, nil
}

func splitAllow(v string) []string {
	if v == "" {
		return []string{}
	}
	return strings.Split(v, ",")
}

// SetPublicPort opens, changes or closes the public port of an app. Opening
// needs the deploy scope, the server's range (BITPLOY_PUBLIC_PORTS) and a free
// place in the user's limit. While it is open the port stays the same.
func (s *Service) SetPublicPort(ctx context.Context, a Actor, ref string, in PublicPortInput) (App, error) {
	row, err := s.LoadApp(ctx, a, ref, true)
	if err != nil {
		return App{}, err
	}
	if !in.Enabled {
		if err := s.closePublicPort(ctx, a, row, "closed"); err != nil {
			return App{}, err
		}
		return s.GetApp(ctx, a, row.Slug)
	}
	if !s.Cfg.PublicPortsOn() {
		return App{}, publicPortsOff()
	}
	cur, curErr := s.Q.GetPublicPort(ctx, row.ID)
	exists := curErr == nil
	if curErr != nil && !errors.Is(curErr, sql.ErrNoRows) {
		return App{}, apperr.Internal(curErr)
	}
	service := ""
	if exists {
		service = cur.Service
	}
	if in.Service != nil {
		service = strings.TrimSpace(*in.Service)
	}
	target, ae := s.resolvePublicTarget(ctx, row, service)
	if ae != nil {
		return App{}, ae
	}
	allow := []string{}
	if exists {
		allow = splitAllow(cur.AllowFrom)
	}
	if in.AllowFrom != nil {
		allow = in.AllowFrom
	}
	if allow, ae = parseAllowFrom(allow, in.OnlyMyIP, a.IP); ae != nil {
		return App{}, ae
	}
	now := s.now()
	expires := nullInt(0, false)
	switch {
	case in.CloseAfter != nil:
		secs, ae := closeAfterSeconds(*in.CloseAfter)
		if ae != nil {
			return App{}, ae
		}
		expires = nullInt(now+secs, secs > 0)
	case exists:
		expires = cur.ExpiresAt
	default:
		expires = nullInt(now+3600, true)
	}

	var port int64
	err = s.Store.Tx(ctx, func(q *db.Queries) error {
		// Read again inside the transaction: the settings above were worked out
		// from the row as it was, and another open, a close or the reconciler
		// closing an expired port may have changed it since.
		fresh, err := q.GetPublicPort(ctx, row.ID)
		switch {
		case err != nil && !errors.Is(err, sql.ErrNoRows):
			return err
		case (err == nil) != exists || (exists && (fresh.Port != cur.Port || fresh.UpdatedAt != cur.UpdatedAt)):
			return apperr.Conflict("public_port_changed", "The public port of "+row.Slug+" was opened, changed or closed at the same moment, so nothing was changed.",
				"Call get_app to see it as it is now, then call set_public_port again.").WithNext("get_app")
		}
		if exists {
			port = cur.Port
			return q.UpdatePublicPortSettings(ctx, db.UpdatePublicPortSettingsParams{
				Service: target.Service, AllowFrom: strings.Join(allow, ","), ExpiresAt: expires, UpdatedAt: now, AppID: row.ID,
			})
		}
		n, err := q.CountPublicPortsByUser(ctx, row.UserID)
		if err != nil {
			return err
		}
		if limit := int64(s.Cfg.PublicPortsPerUser); n >= limit {
			return publicPortLimit(limit)
		}
		p, err := s.freePublicPort(ctx, q, 0)
		if err != nil {
			return err
		}
		port = p
		return q.InsertPublicPort(ctx, db.InsertPublicPortParams{
			AppID: row.ID, Port: p, Service: target.Service, AllowFrom: strings.Join(allow, ","),
			ExpiresAt: expires, CreatedAt: now, UpdatedAt: now,
		})
	})
	if err != nil {
		var e *apperr.Error
		if errors.As(err, &e) {
			return App{}, e
		}
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return App{}, apperr.Conflict("public_port_busy", "Someone else took that port at the same moment.", "Try again.").WithNext("set_public_port")
		}
		return App{}, apperr.Internal(err)
	}
	action := "app.public_port_opened"
	if exists {
		action = "app.public_port_changed"
	}
	details := map[string]any{"port": port, "allow_from": allow, "expires_at": nil}
	if target.Service != "" {
		details["service"] = target.Service
	}
	if expires.Valid {
		details["expires_at"] = ts(expires.Int64).Format(time.RFC3339)
	}
	s.Audit(ctx, a, action, "app", row.ID, details)

	if ae := s.ensurePublicPort(ctx, row.ID); ae != nil {
		return App{}, ae
	}
	return s.GetApp(ctx, a, row.Slug)
}

func publicPortLimit(limit int64) *apperr.Error {
	msg := fmt.Sprintf("You already have %d public ports open, the most this server allows.", limit)
	if limit == 0 {
		msg = "This server does not let users open public ports."
	}
	return apperr.Conflict("public_port_limit", msg,
		"Close a public port you no longer use with set_public_port(enabled=false), then try again.").WithNext("set_public_port")
}

// freePublicPort picks the lowest port of the range that no app holds and
// that the host did not refuse lately. skip is a port to leave out (0: none).
func (s *Service) freePublicPort(ctx context.Context, q *db.Queries, skip int64) (int64, error) {
	used, err := q.ListUsedPublicPorts(ctx)
	if err != nil {
		return 0, err
	}
	inUse := make(map[int64]bool, len(used))
	for _, p := range used {
		inUse[p] = true
	}
	s.pub.mu.Lock()
	defer s.pub.mu.Unlock()
	now := s.Now()
	for p := int64(s.Cfg.PublicPortsFrom); p <= int64(s.Cfg.PublicPortsTo); p++ {
		if inUse[p] || p == skip {
			continue
		}
		if t, ok := s.pub.taken[p]; ok && now.Sub(t) < hostTakenFor {
			continue
		}
		return p, nil
	}
	return 0, apperr.Unavailable("public_ports_full", "Every public port of this server is in use.",
		"Ask the bitploy admin for a bigger BITPLOY_PUBLIC_PORTS range, or try again later.")
}

func (s *Service) markHostTaken(port int64) {
	s.pub.mu.Lock()
	defer s.pub.mu.Unlock()
	if s.pub.taken == nil {
		s.pub.taken = map[int64]time.Time{}
	}
	s.pub.taken[port] = s.Now()
}

// ensurePublicPort makes the forwarder of an app's open public port run.
// A port that something else on the host holds is swapped for another one
// (up to 3 times). The reason it cannot run is stored in last_error.
func (s *Service) ensurePublicPort(ctx context.Context, appID string) *apperr.Error {
	px := s.proxies()
	if px == nil {
		return apperr.Unavailable("docker_unavailable", "bitploy cannot reach Docker right now.", "Try again in a minute. If it keeps happening, tell the bitploy admin.")
	}
	s.pub.run.Lock()
	defer s.pub.run.Unlock()
	for try := 0; try < 4; try++ {
		row, err := s.Q.GetApp(ctx, appID)
		if err != nil {
			return apperr.Internal(err)
		}
		pp, err := s.Q.GetPublicPort(ctx, appID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil // closed in the meantime
		}
		if err != nil {
			return apperr.Internal(err)
		}
		target, ae := s.resolvePublicTarget(ctx, row, pp.Service)
		if ae != nil {
			// The app changed (it got a web address, lost its port): nothing may
			// be forwarded until the owner sorts it out.
			_ = px.RemovePortProxy(ctx, appID)
			s.setPublicPortError(ctx, pp, ae.Message)
			return nil
		}
		if pp.Port < int64(s.Cfg.PublicPortsFrom) || pp.Port > int64(s.Cfg.PublicPortsTo) {
			// The admin changed the range: move to a port inside it.
			if ae := s.movePublicPort(ctx, pp); ae != nil {
				return ae
			}
			continue
		}
		spec := dockerx.PortProxySpec{
			AppID: row.ID, Slug: row.Slug, Network: dockerx.NetworkName(row.Slug), HostPort: int(pp.Port),
			Target: fmt.Sprintf("%s:%d", target.Alias, target.Port), Allow: splitAllow(pp.AllowFrom),
		}
		if pp.ExpiresAt.Valid {
			spec.Until = pp.ExpiresAt.Int64
		}
		err = px.EnsurePortProxy(ctx, spec)
		if errors.Is(err, dockerx.ErrPortInUse) {
			s.Log.Warn("public port taken on the host; picking another", "app", row.Slug, "port", pp.Port)
			s.markHostTaken(pp.Port)
			if ae := s.movePublicPort(ctx, pp); ae != nil {
				return ae
			}
			continue
		}
		if err != nil {
			msg := "bitploy could not start the forwarder: " + apperr.Redact(dockerx.DescribeError(err))
			s.Log.Error("start public port forwarder", "app", row.Slug, "err", err)
			s.setPublicPortError(ctx, pp, msg)
			return apperr.Unavailable("public_port_failed", "The public port is saved, but "+msg+".",
				"bitploy tries again every minute. If it keeps failing, tell the bitploy admin.")
		}
		if pp.LastError != "" {
			_ = s.Q.SetPublicPortError(ctx, db.SetPublicPortErrorParams{LastError: "", AppID: appID})
		}
		return nil
	}
	return apperr.Unavailable("public_port_failed", "bitploy could not find a free port on the server.",
		"Try again in a few minutes, or tell the bitploy admin that ports of BITPLOY_PUBLIC_PORTS are taken by other programs.")
}

func (s *Service) setPublicPortError(ctx context.Context, pp db.PublicPort, msg string) {
	if pp.LastError != msg {
		_ = s.Q.SetPublicPortError(ctx, db.SetPublicPortErrorParams{LastError: msg, AppID: pp.AppID})
	}
}

// movePublicPort gives an open public port another number.
func (s *Service) movePublicPort(ctx context.Context, pp db.PublicPort) *apperr.Error {
	var port int64
	err := s.Store.Tx(ctx, func(q *db.Queries) error {
		p, err := s.freePublicPort(ctx, q, pp.Port)
		if err != nil {
			return err
		}
		port = p
		return q.SetPublicPortNumber(ctx, db.SetPublicPortNumberParams{Port: p, UpdatedAt: s.now(), AppID: pp.AppID})
	})
	if err != nil {
		if e := apperr.As(err); e != nil && e.Code != "internal" {
			s.setPublicPortError(ctx, pp, e.Message)
			return e
		}
		return apperr.Internal(err)
	}
	s.Audit(ctx, System, "app.public_port_moved", "app", pp.AppID, map[string]any{"from": pp.Port, "port": port})
	return nil
}

// closePublicPort removes the public port of an app (no row: nothing to do).
// reason goes into the audit row: closed, expired, app_deleted, admin,
// public_ports_off, user_suspended.
func (s *Service) closePublicPort(ctx context.Context, a Actor, row db.App, reason string) error {
	s.pub.run.Lock()
	defer s.pub.run.Unlock()
	pp, err := s.Q.GetPublicPort(ctx, row.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return apperr.Internal(err)
	}
	// The forwarder goes first: a row without a forwarder is harmless, the
	// other way round would leave a port open that nobody sees.
	if px := s.proxies(); px != nil {
		if err := px.RemovePortProxy(ctx, row.ID); err != nil {
			s.Log.Error("remove public port forwarder", "app", row.Slug, "err", err)
			return apperr.Unavailable("public_port_close_failed", "bitploy could not close the public port: "+apperr.Redact(dockerx.DescribeError(err))+".",
				"Try again in a minute. If it keeps failing, tell the bitploy admin.")
		}
	}
	if _, err := s.Q.DeletePublicPort(ctx, row.ID); err != nil {
		return apperr.Internal(err)
	}
	s.Audit(ctx, a, "app.public_port_closed", "app", row.ID, map[string]any{"port": pp.Port, "reason": reason})
	return nil
}

// RevealPublicConnection returns the connection URL of an open public port
// with the password in it. Only the owner (deploy scope or the dashboard),
// only while the port is open, only for service template apps; every call is
// in the audit log.
func (s *Service) RevealPublicConnection(ctx context.Context, a Actor, ref string) (PublicConnection, error) {
	row, err := s.LoadApp(ctx, a, ref, true)
	if err != nil {
		return PublicConnection{}, err
	}
	pp, err := s.Q.GetPublicPort(ctx, row.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return PublicConnection{}, apperr.Conflict("public_port_closed", "The app "+row.Slug+" has no public port open.",
			"Open one with set_public_port(enabled=true) first.").WithNext("set_public_port")
	}
	if err != nil {
		return PublicConnection{}, apperr.Internal(err)
	}
	url, ae := s.publicConnectionURL(ctx, row, pp, false)
	if ae != nil {
		return PublicConnection{}, ae
	}
	if url == "" {
		return PublicConnection{}, apperr.Unprocessable("no_connection_url",
			"The app "+row.Slug+" is not from a service template, so bitploy does not know its login.",
			"Connect to the address with the login the app itself uses.")
	}
	secrets := map[string]string{}
	if tp, ok := templateOf(row); ok {
		if vars, ae := s.sharedVars(ctx, row.ID); ae == nil {
			for _, n := range tp.SecretNames() {
				if v, ok := vars[n]; ok {
					secrets[n] = v
				}
			}
		}
	}
	s.Audit(ctx, a, "app.public_port_revealed", "app", row.ID, map[string]any{"port": pp.Port})
	return PublicConnection{ConnectionURL: url, Address: s.publicAddress(pp.Port), Secrets: secrets}, nil
}

func (s *Service) publicAddress(port int64) string {
	return fmt.Sprintf("%s:%d", s.Cfg.PublicPortsHost, port)
}

// publicConnectionURL is the template's main connection variable at the
// public address; masked replaces the generated passwords with ********.
// "" when the app is not from a template.
func (s *Service) publicConnectionURL(ctx context.Context, row db.App, pp db.PublicPort, masked bool) (string, *apperr.Error) {
	tp, ok := templateOf(row)
	if !ok || tp.MainVar == "" {
		return "", nil
	}
	vars, ae := s.sharedVars(ctx, row.ID)
	if ae != nil {
		return "", ae
	}
	if masked {
		for _, n := range tp.SecretNames() {
			if _, ok := vars[n]; ok {
				vars[n] = passwordMask
			}
		}
	}
	url, _ := tp.ResolveAt(tp.MainVar, s.Cfg.PublicPortsHost, pp.Port, vars)
	return url, nil
}

// publicPortView is App.public_port.
func (s *Service) publicPortView(ctx context.Context, r db.App) PublicPort {
	v := PublicPort{AllowFrom: []string{}}
	switch {
	case !isMulti(r) && r.Type == "web":
		v.Reason = "web_app"
	case !s.Cfg.PublicPortsOn():
		v.Reason = "public_ports_off"
	default:
		v.Available = true
	}
	pp, err := s.Q.GetPublicPort(ctx, r.ID)
	if err != nil {
		return v
	}
	v.Enabled = true
	v.Service, v.Port, v.Host = pp.Service, pp.Port, s.Cfg.PublicPortsHost
	v.Address = s.publicAddress(pp.Port)
	v.AllowFrom = splitAllow(pp.AllowFrom)
	v.ExpiresAt = tsPtr(pp.ExpiresAt)
	opened := ts(pp.CreatedAt)
	v.OpenedAt = &opened
	v.Error = pp.LastError
	if t, ae := s.resolvePublicTarget(ctx, r, pp.Service); ae == nil {
		v.TargetPort = t.Port
	}
	if url, ae := s.publicConnectionURL(ctx, r, pp, true); ae == nil {
		v.ConnectionURL = url
	}
	if px := s.proxies(); px != nil && v.Error == "" {
		v.Running, _ = px.PortProxyRunning(ctx, r.ID)
	}
	return v
}

// ListPublicPorts is the admin overview: the range and every open port.
func (s *Service) ListPublicPorts(ctx context.Context, a Actor) (PublicPortsOverview, error) {
	if err := RequireAdmin(a); err != nil {
		return PublicPortsOverview{}, err
	}
	out := PublicPortsOverview{
		Enabled: s.Cfg.PublicPortsOn(), From: s.Cfg.PublicPortsFrom, To: s.Cfg.PublicPortsTo,
		Host: s.Cfg.PublicPortsHost, PerUser: s.Cfg.PublicPortsPerUser, Open: []OpenPublicPort{},
	}
	rows, err := s.Q.ListPublicPortsWithApps(ctx)
	if err != nil {
		return PublicPortsOverview{}, apperr.Internal(err)
	}
	for _, r := range rows {
		out.Open = append(out.Open, OpenPublicPort{
			App: r.Slug, AppName: r.Name, OwnerEmail: r.Email, Service: r.Service, Port: r.Port,
			AllowFrom: splitAllow(r.AllowFrom), ExpiresAt: tsPtr(r.ExpiresAt), OpenedAt: ts(r.CreatedAt), Error: r.LastError,
		})
	}
	return out, nil
}

// AdminClosePublicPort closes the public port of any app (admins, dashboard only).
func (s *Service) AdminClosePublicPort(ctx context.Context, a Actor, ref string) error {
	if err := RequireAdmin(a); err != nil {
		return err
	}
	row, err := s.LoadApp(ctx, a, ref, false)
	if err != nil {
		return err
	}
	return s.closePublicPort(ctx, a, row, "admin")
}

// reconcilePublicPorts closes expired public ports (and all of them when the
// server's range is gone), makes the forwarders of the open ones run, and
// removes forwarders that no open port wants. Runs with the reconciler.
func (s *Service) reconcilePublicPorts(ctx context.Context) {
	expired, err := s.Q.ListExpiredPublicPorts(ctx, nullInt(s.now(), true))
	if err == nil {
		for _, pp := range expired {
			if row, err := s.Q.GetApp(ctx, pp.AppID); err == nil {
				if err := s.closePublicPort(ctx, System, row, "expired"); err != nil {
					s.Log.Warn("close expired public port", "app", row.Slug, "err", err)
				}
			}
		}
	}
	open, err := s.Q.ListPublicPorts(ctx)
	if err != nil {
		return
	}
	if !s.Cfg.PublicPortsOn() {
		for _, pp := range open {
			if row, err := s.Q.GetApp(ctx, pp.AppID); err == nil {
				_ = s.closePublicPort(ctx, System, row, "public_ports_off")
			}
		}
		open = nil
	}
	px := s.proxies()
	if px == nil {
		return
	}
	want := map[string]bool{}
	for _, pp := range open {
		if ctx.Err() != nil {
			return
		}
		want[pp.AppID] = true
		if ae := s.ensurePublicPort(ctx, pp.AppID); ae != nil {
			s.Log.Warn("public port forwarder", "app", pp.AppID, "err", ae.Message)
		}
	}
	// Forwarders nobody wants: of a deleted app, of a port closed while Docker
	// was away. A port opened since the list above has its row by now (the
	// row is written before its forwarder starts), so it is looked up again
	// under the lock.
	if have, err := px.ListPortProxies(ctx); err == nil {
		for _, p := range have {
			if want[p.AppID] {
				continue
			}
			s.pub.run.Lock()
			if _, err := s.Q.GetPublicPort(ctx, p.AppID); errors.Is(err, sql.ErrNoRows) {
				s.Log.Info("removing public port forwarder nobody wants", "container", p.Name)
				_ = px.RemovePortProxy(ctx, p.AppID)
			}
			s.pub.run.Unlock()
		}
	}
}

// dropPublicPortForwarder takes the forwarder of an app away without closing
// the port (a rename replaces the app network).
func (s *Service) dropPublicPortForwarder(ctx context.Context, appID string) {
	s.pub.run.Lock()
	defer s.pub.run.Unlock()
	if px := s.proxies(); px != nil {
		if err := px.RemovePortProxy(ctx, appID); err != nil {
			s.Log.Warn("remove public port forwarder", "app", appID, "err", err)
		}
	}
}
