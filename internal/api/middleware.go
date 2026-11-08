package api

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AVMG20/bitploy/internal/app"
	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/auth"
	"github.com/AVMG20/bitploy/internal/config"
	"github.com/AVMG20/bitploy/internal/ids"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// ---- response recorder (status + bytes, still streams, still flushes) ----

type recorder struct {
	http.ResponseWriter
	status int
	bytes  int64
	body   *bytes.Buffer // only when capture is set
	limit  int
}

func (r *recorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if r.body != nil && r.body.Len()+len(b) <= r.limit {
		r.body.Write(b)
	} else if r.body != nil {
		r.body = nil // too big to replay
		r.limit = -1
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

func (r *recorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// ---- request logging, recovery, request id ----

type logInfo struct {
	mu      sync.Mutex
	userID  string
	channel string
	tokenID string
}

type logKey struct{}

func (l *logInfo) set(a app.Actor) {
	l.mu.Lock()
	l.userID, l.channel, l.tokenID = a.UserID, string(a.Channel), a.TokenID
	l.mu.Unlock()
}

// annotate copies the authenticated actor into the log record.
func annotate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if li, ok := r.Context().Value(logKey{}).(*logInfo); ok {
			if a, ok := auth.ActorFrom(r.Context()); ok {
				li.set(a)
			}
		}
		next.ServeHTTP(w, r)
	})
}

func logRequests(log *slog.Logger, cfg *config.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rid := r.Header.Get("X-Request-Id")
		if len(rid) == 0 || len(rid) > 64 || strings.ContainsAny(rid, "\r\n ") {
			rid = ids.New("req")
		}
		w.Header().Set("X-Request-Id", rid)
		li := &logInfo{}
		rec := &recorder{ResponseWriter: w}
		ctx := context.WithValue(r.Context(), logKey{}, li)
		defer func() {
			if p := recover(); p != nil {
				log.Error("panic in handler", "request_id", rid, "panic", p, "stack", string(debug.Stack()))
				if rec.status == 0 {
					auth.WriteError(rec, apperr.Internal(errors.New("panic")))
				}
			}
			if r.URL.Path == "/healthz" {
				return
			}
			status := rec.status
			if status == 0 {
				status = http.StatusOK
			}
			li.mu.Lock()
			attrs := []any{
				"request_id", rid, "method", r.Method, "path", logPath(r.URL.Path), "status", status,
				"duration_ms", time.Since(start).Milliseconds(), "bytes", rec.bytes,
				"ip", auth.ClientIP(r, cfg),
			}
			if li.userID != "" {
				attrs = append(attrs, "user_id", li.userID, "channel", li.channel)
			}
			if li.tokenID != "" {
				attrs = append(attrs, "token_id", li.tokenID)
			}
			li.mu.Unlock()
			log.Info("request", attrs...)
		}()
		next.ServeHTTP(rec, r.WithContext(ctx))
	})
}

// logPath hides the upload ID: for POST /u/{id} the ID is the credential.
func logPath(p string) string {
	if strings.HasPrefix(p, "/u/") {
		return "/u/[id]"
	}
	return p
}

// ---- security headers ----

// policy is the Content-Security-Policy. form-action also allows GitHub: the
// admin's "Create GitHub App" button posts the app manifest to github.com
// (and a form submit that is redirected is checked again at the target).
func policy(cfg *config.Config) string {
	formAction := "'self'"
	// The Terminal tab's WebSocket. CSP Level 3 lets 'self' cover ws: and
	// wss: on the page's own host, but older Safari versions do not, so the
	// public URL's ws(s) origin is named as well.
	connect := "'self'"
	if cfg != nil {
		if u, err := url.Parse(cfg.PublicURL); err == nil && u.Host != "" && (u.Scheme == "https" || u.Scheme == "http") {
			connect += " " + strings.Replace(u.Scheme, "http", "ws", 1) + "://" + u.Host
		}
	}
	if cfg != nil {
		web := cfg.GitHubWebURL
		if web == "" {
			web = "https://github.com"
		}
		if u, err := url.Parse(web); err == nil && u.Host != "" {
			formAction += " " + u.Scheme + "://" + u.Host
		}
	}
	return "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data:; font-src 'self' data:; connect-src " + connect + "; " +
		"frame-ancestors 'none'; base-uri 'none'; form-action " + formAction + "; object-src 'none'"
}

func securityHeaders(cfg *config.Config, next http.Handler) http.Handler {
	hsts := !cfg.Dev && strings.HasPrefix(cfg.PublicURL, "https://")
	csp := policy(cfg)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		if hsts {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// ---- body limit ----

func limitBody(n int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, n)
		}
		next.ServeHTTP(w, r)
	})
}

// ---- per-route read deadlines ----

// Read deadlines for request bodies. There is no server-wide ReadTimeout
// because it would also cut Server-Sent Events, long-polls and slow uploads.
const (
	apiReadTimeout    = 30 * time.Second
	mcpReadTimeout    = 60 * time.Second
	uploadReadTimeout = 15 * time.Minute
)

// readDeadline gives the request body d to arrive. It only applies to requests
// that have a body, and the deadline is lifted again when the body is done (or
// failed), so a long response (long-poll, deploy wait) is not cut off: an
// expired read deadline would cancel the request context.
func readDeadline(d time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Body != http.NoBody {
			rc := http.NewResponseController(w)
			if err := rc.SetReadDeadline(time.Now().Add(d)); err == nil {
				r.Body = &deadlineBody{ReadCloser: r.Body, rc: rc}
			}
		}
		next.ServeHTTP(w, r)
	})
}

type deadlineBody struct {
	io.ReadCloser
	rc   *http.ResponseController
	done bool
}

func (b *deadlineBody) lift() {
	if !b.done {
		b.done = true
		_ = b.rc.SetReadDeadline(time.Time{})
	}
}

func (b *deadlineBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.lift()
	}
	return n, err
}

func (b *deadlineBody) Close() error {
	b.lift()
	return b.ReadCloser.Close()
}

// ---- rate limit: 120 requests per minute per API token ----

const (
	rateLimit  = 120
	ratePeriod = time.Minute
)

type bucket struct {
	tokens float64
	last   time.Time
}

type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	now     func() time.Time
}

// rateClock is the limiter's clock; tests freeze it.
var rateClock = time.Now

func newRateLimiter() *rateLimiter {
	return &rateLimiter{buckets: map[string]*bucket{}, now: func() time.Time { return rateClock() }}
}

// allow takes one token from the bucket; otherwise it says how many seconds to wait.
func (l *rateLimiter) allow(key string) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b := l.buckets[key]
	if b == nil {
		if len(l.buckets) > 5000 {
			for k, v := range l.buckets {
				if now.Sub(v.last) > ratePeriod {
					delete(l.buckets, k)
				}
			}
		}
		b = &bucket{tokens: rateLimit, last: now}
		l.buckets[key] = b
	}
	perSec := float64(rateLimit) / ratePeriod.Seconds()
	b.tokens += now.Sub(b.last).Seconds() * perSec
	if b.tokens > rateLimit {
		b.tokens = rateLimit
	}
	b.last = now
	if b.tokens < 1 {
		wait := int((1-b.tokens)/perSec) + 1
		return false, wait
	}
	b.tokens--
	return true, 0
}

func (l *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a, ok := auth.ActorFrom(r.Context()); ok && a.TokenID != "" {
			if ok, wait := l.allow(a.TokenID); !ok {
				w.Header().Set("Retry-After", strconv.Itoa(wait))
				auth.WriteError(w, rateLimited(wait))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ---- Idempotency-Key on creating POSTs ----

const (
	idempotencyTTL     = 24 * time.Hour
	idempotencyMaxBody = 1 << 20
)

// idempotentPath says whether a POST path (below /api/v1) may be replayed from
// the stored response. It is an allow-list of resource-creating app routes:
// responses of account routes (/tokens holds a raw bp_ token, /admin/invites an
// invite URL, /auth/*, /me*, /sessions*) and of /apps/{app}/uploads (a one-time
// upload URL) are credentials and must never land in the database or backups.
func idempotentPath(p string) bool {
	rest := strings.TrimPrefix(p, prefix)
	if rest == "/apps" || rest == "/projects" {
		return true
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 3 && parts[0] == "catalog" && parts[1] != "" && parts[2] == "deploy" {
		return true // creates an app: a retry must not make a second one
	}
	if len(parts) == 3 && parts[0] == "projects" && parts[1] != "" && parts[2] == "services" {
		return true // add_service creates an app too
	}
	if len(parts) == 3 && parts[0] == "apps" && parts[1] != "" {
		switch parts[2] {
		case "files", "deployments", "domains", "rollback", "redeploy", "catalog-update":
			return true
		}
	}
	return false
}

// keyLocks serialises requests that carry the same Idempotency-Key, so a retry
// that arrives while the first request is still running waits for it and then
// replays its answer instead of doing the work a second time.
type keyLocks struct {
	mu sync.Mutex
	m  map[string]*keyLock
}

type keyLock struct {
	mu   sync.Mutex
	refs int
}

// lock blocks until the caller holds key; the returned func releases it.
func (k *keyLocks) lock(key string) func() {
	k.mu.Lock()
	if k.m == nil {
		k.m = map[string]*keyLock{}
	}
	l := k.m[key]
	if l == nil {
		l = &keyLock{}
		k.m[key] = l
	}
	l.refs++
	k.mu.Unlock()
	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		k.mu.Lock()
		if l.refs--; l.refs == 0 {
			delete(k.m, key)
		}
		k.mu.Unlock()
	}
}

func idempotency(svc *app.Service, next http.Handler) http.Handler {
	var inflight keyLocks
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if r.Method != http.MethodPost || key == "" || !idempotentPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		actor, ok := auth.ActorFrom(r.Context())
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		if len(key) > 200 {
			auth.WriteError(w, apperr.BadRequest("idempotency_key_invalid", "The Idempotency-Key is too long.", "Use at most 200 characters."))
			return
		}
		defer inflight.lock(actor.UserID + "\x00" + key)()
		ctx := r.Context()
		now := svc.Now().Unix()
		row, err := svc.Q.GetIdempotencyKey(ctx, db.GetIdempotencyKeyParams{UserID: actor.UserID, Key: key})
		if err == nil && now-row.CreatedAt >= int64(idempotencyTTL/time.Second) {
			_ = svc.Q.DeleteOldIdempotencyKeys(ctx, now-int64(idempotencyTTL/time.Second))
			err = sql.ErrNoRows
		}
		if err == nil {
			if row.Method != r.Method || row.Path != r.URL.Path {
				auth.WriteError(w, apperr.Unprocessable("idempotency_key_reused",
					"This Idempotency-Key was already used for a different request ("+row.Method+" "+row.Path+").",
					"Use a new unique key for each different request."))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Idempotent-Replayed", "true")
			w.WriteHeader(int(row.Status))
			_, _ = w.Write(row.Body)
			return
		}
		if !errors.Is(err, sql.ErrNoRows) {
			auth.WriteError(w, apperr.Internal(err))
			return
		}
		rec := &recorder{ResponseWriter: w, body: &bytes.Buffer{}, limit: idempotencyMaxBody}
		next.ServeHTTP(rec, r)
		if rec.status >= 200 && rec.status < 300 && rec.body != nil {
			if err := svc.Q.InsertIdempotencyKey(ctx, db.InsertIdempotencyKeyParams{
				UserID: actor.UserID, Key: key, Method: r.Method, Path: r.URL.Path,
				Status: int64(rec.status), Body: []byte(apperr.Redact(rec.body.String())), CreatedAt: now,
			}); err != nil {
				svc.Log.Warn("idempotency store failed", "err", err)
			}
		}
	})
}
