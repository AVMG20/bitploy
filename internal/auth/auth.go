// Package auth is bitploy's HTTP authentication: it turns a bearer token or a
// session cookie into an app.Actor, enforces CSRF for cookie requests and
// works out the client IP. Password hashing lives in auth/passwd and token
// generation in auth/tokens.
package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	"github.com/arno/bitploy/internal/app"
	"github.com/arno/bitploy/internal/apperr"
	"github.com/arno/bitploy/internal/config"
)

type ctxKey int

const (
	actorKey ctxKey = iota
	cookieKey
	csrfKey
)

// ActorFrom returns the actor the middleware put in the request context.
func ActorFrom(ctx context.Context) (app.Actor, bool) {
	a, ok := ctx.Value(actorKey).(app.Actor)
	return a, ok
}

// WithActor returns ctx carrying the actor (used by the middleware and tests).
func WithActor(ctx context.Context, a app.Actor) context.Context {
	return context.WithValue(ctx, actorKey, a)
}

// SessionCookieFrom returns the raw session cookie value of a cookie-authenticated request.
func SessionCookieFrom(ctx context.Context) string {
	v, _ := ctx.Value(cookieKey).(string)
	return v
}

// CSRFTokenFrom returns the session's CSRF token of a cookie-authenticated request.
func CSRFTokenFrom(ctx context.Context) string {
	v, _ := ctx.Value(csrfKey).(string)
	return v
}

// CookieName is the session cookie name: __Host-bitploy_session in
// production, bitploy_session in dev (plain HTTP on localhost).
func CookieName(cfg *config.Config) string {
	if cfg.Dev {
		return "bitploy_session"
	}
	return "__Host-bitploy_session"
}

// NewSessionCookie builds the session cookie: HttpOnly, SameSite=Lax, Path=/,
// no Domain, and Secure outside dev.
func NewSessionCookie(cfg *config.Config, value string) *http.Cookie {
	return &http.Cookie{
		Name: CookieName(cfg), Value: value, Path: "/",
		MaxAge: 30 * 24 * 3600, HttpOnly: true, Secure: !cfg.Dev, SameSite: http.SameSiteLaxMode,
	}
}

// ExpiredSessionCookie is the cookie that deletes the session cookie.
func ExpiredSessionCookie(cfg *config.Config) *http.Cookie {
	c := NewSessionCookie(cfg, "")
	c.MaxAge = -1
	return c
}

// SetSessionCookie writes the session cookie.
func SetSessionCookie(w http.ResponseWriter, cfg *config.Config, value string) {
	http.SetCookie(w, NewSessionCookie(cfg, value))
}

// ClearSessionCookie expires the session cookie.
func ClearSessionCookie(w http.ResponseWriter, cfg *config.Config) {
	http.SetCookie(w, ExpiredSessionCookie(cfg))
}

// WriteError renders an error as {"error":{code,message,hint,next,details}}
// with the status from apperr. Unknown errors become a generic 500.
func WriteError(w http.ResponseWriter, err error) {
	e := apperr.As(err)
	status := e.HTTPStatus
	if status == 0 {
		status = http.StatusInternalServerError
	}
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Bearer realm="bitploy"`)
	}
	if status == http.StatusTooManyRequests {
		if secs, ok := e.Details["retry_after_seconds"]; ok {
			switch v := secs.(type) {
			case int64:
				w.Header().Set("Retry-After", strconv.FormatInt(v, 10))
			case int:
				w.Header().Set("Retry-After", strconv.Itoa(v))
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
		"code": e.Code, "message": apperr.Redact(e.Message), "hint": apperr.Redact(e.Hint), "next": e.Next, "details": e.Details,
	}})
}

// ---- client IP ----

// ClientIP is the address of the caller. X-Real-IP and X-Forwarded-For are
// only believed when the TCP peer is a private or loopback address (Traefik
// on the Docker network); otherwise anyone could fake their IP and dodge rate
// limits.
func ClientIP(r *http.Request, cfg *config.Config) string {
	peer := r.RemoteAddr
	if host, _, err := net.SplitHostPort(peer); err == nil {
		peer = host
	}
	pa, err := netip.ParseAddr(peer)
	if err != nil {
		return peer
	}
	pa = pa.Unmap()
	if !(pa.IsLoopback() || pa.IsPrivate() || pa.IsLinkLocalUnicast()) {
		return pa.String()
	}
	// Walk X-Forwarded-For from the right, skipping our own proxies (private
	// addresses) and Cloudflare. Hops left of the first untrusted address were
	// written by the client and are ignored, so the IP cannot be faked.
	// X-Real-IP is not used: Traefik passes it through from trusted peers.
	hops := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		a = a.Unmap()
		if a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || isCloudflare(a) {
			continue
		}
		return a.String()
	}
	return pa.String()
}

// ---- middleware ----

// Options tune the middleware.
type Options struct {
	// Channel stamped on token actors (api for /api/v1, mcp for /mcp).
	Channel app.Channel
	// AllowCookie accepts the dashboard session cookie (with CSRF checks).
	AllowCookie bool
	// CheckOrigin rejects unsafe requests without bearer auth whose Origin
	// header is present but is not the public URL (login CSRF).
	CheckOrigin bool
}

// Middleware builds the actor for each request.
//
//   - Authorization: Bearer bp_...  -> svc.AuthenticateToken; an invalid token
//     is rejected at once with 401. CSRF is skipped.
//   - session cookie (when allowed) -> svc.AuthenticateSession; unsafe methods
//     need Origin == PublicURL and X-CSRF-Token == the session's token.
//   - neither: the request goes on without an actor; handlers that need one
//     answer 401.
func Middleware(svc *app.Service, opt Options) func(http.Handler) http.Handler {
	cfg := svc.Cfg
	origin := strings.TrimRight(cfg.PublicURL, "/")
	cookieName := CookieName(cfg)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			ip := ClientIP(r, cfg)
			unsafe := !(r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions)

			if h := r.Header.Get("Authorization"); h != "" {
				raw, ok := bearer(h)
				if !ok {
					WriteError(w, apperr.Unauthorized("token_invalid", "The Authorization header is not a Bearer token.",
						"Send: Authorization: Bearer bp_..."))
					return
				}
				actor, err := svc.AuthenticateToken(ctx, raw, ip)
				if err != nil {
					WriteError(w, err)
					return
				}
				actor.Channel = opt.Channel
				if actor.Channel == "" {
					actor.Channel = app.ChannelAPI
				}
				next.ServeHTTP(w, r.WithContext(WithActor(ctx, actor)))
				return
			}

			if opt.AllowCookie {
				if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
					actor, csrf, err := svc.AuthenticateSession(ctx, c.Value, ip)
					switch {
					case err == nil:
						if unsafe {
							if cerr := CheckCSRF(r, origin, csrf); cerr != nil {
								WriteError(w, cerr)
								return
							}
						}
						ctx = context.WithValue(WithActor(ctx, actor), cookieKey, c.Value)
						ctx = context.WithValue(ctx, csrfKey, csrf)
						next.ServeHTTP(w, r.WithContext(ctx))
						return
					case apperr.As(err).Code == "account_suspended":
						WriteError(w, err)
						return
					case apperr.As(err).HTTPStatus == http.StatusUnauthorized:
						// Stale cookie: drop it and carry on without an actor.
						ClearSessionCookie(w, cfg)
					default:
						WriteError(w, err)
						return
					}
				}
			}

			if opt.CheckOrigin && unsafe {
				if o := r.Header.Get("Origin"); o != "" && o != origin {
					WriteError(w, errBadOrigin())
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func bearer(h string) (string, bool) {
	scheme, val, ok := strings.Cut(strings.TrimSpace(h), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	val = strings.TrimSpace(val)
	return val, val != ""
}

func errBadOrigin() *apperr.Error {
	return apperr.Forbidden("csrf_origin",
		"This request came from a different website than the bitploy dashboard, so it was blocked.",
		"Use the dashboard at the bitploy address itself, or use an API token with the Authorization header.")
}

// CheckCSRF applies the two CSRF checks for a cookie-authenticated unsafe
// request: Origin must equal the public URL exactly, and X-CSRF-Token must
// match the session's token.
func CheckCSRF(r *http.Request, publicOrigin, sessionToken string) error {
	if r.Header.Get("Origin") != publicOrigin {
		return errBadOrigin()
	}
	got := r.Header.Get("X-CSRF-Token")
	if got == "" || sessionToken == "" || subtle.ConstantTimeCompare([]byte(got), []byte(sessionToken)) != 1 {
		return apperr.Forbidden("csrf_token",
			"The CSRF token is missing or wrong.",
			"Fetch it with GET /api/v1/session and send it in the X-CSRF-Token header on every change.")
	}
	return nil
}

// Require returns the actor or a 401 error; handy for plain handlers.
func Require(ctx context.Context) (app.Actor, error) {
	a, ok := ActorFrom(ctx)
	if !ok {
		return app.Actor{}, apperr.Unauthorized("not_logged_in", "You are not logged in.",
			"Log in to the dashboard, or send an API token: Authorization: Bearer bp_...")
	}
	return a, nil
}
