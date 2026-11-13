// Package api is the REST door (/api/v1) and the top-level HTTP router.
// OWNER: auth/API agent.
package api

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"

	"github.com/AVMG20/bitploy/internal/app"
	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/auth"
)

const mcpBodyLimit = 4 << 20

// maxAPIBody caps every /api/v1 request body before huma's own per-operation
// limits (1 MiB unless an operation says otherwise). It is the biggest request
// of the SQLite viewer: a text of 4 MiB with every character escaped.
const maxAPIBody = 32 << 20

// New returns bitploy's root handler: /healthz, /api/v1, /mcp (mcpHandler),
// /u/{id} uploads, /docs/api, the catch-all app pages and the embedded SPA.
func New(svc *app.Service, mcpHandler http.Handler, web fs.FS) http.Handler {
	h, _ := NewWithStop(svc, mcpHandler, web)
	return h
}

// NewWithStop is New plus stop, which ends the long-lived requests at once:
// the live event streams, the log streams and the get_deployment long-polls
// (REST and MCP). Every other request runs to its end. Call stop when bitploy
// shuts down, before http.Server.Shutdown: that waits for all open requests
// and never cancels them.
func NewWithStop(svc *app.Service, mcpHandler http.Handler, web fs.FS) (http.Handler, func()) {
	a := &API{svc: svc, cfg: svc.Cfg}
	apiMux := http.NewServeMux()
	a.h = newHuma(apiMux, svc.Cfg)
	a.accountRoutes()
	a.appRoutes()
	a.backupRoutes()
	a.filesRoutes()
	a.catalogRoutes()
	a.projectRoutes()
	a.projectMemberRoutes()
	a.notificationRoutes()
	a.dbRoutes()
	a.adminDBRoutes()
	a.githubRoutes()
	a.adminRoutes()
	a.adminBuildRoutes()
	a.appDomainRoutes()
	apiMux.HandleFunc("GET "+prefix+"/events", a.events)
	apiMux.HandleFunc(prefix+"/", func(w http.ResponseWriter, r *http.Request) { notFoundJSON(w) })

	limiter := newRateLimiter()
	// Innermost first: follow -> idempotency -> annotate -> rate limit -> auth.
	var apiChain http.Handler = apiMux
	apiChain = a.hints(apiChain)
	apiChain = a.follow(apiChain)
	apiChain = idempotency(svc, apiChain)
	apiChain = annotate(apiChain)
	apiChain = limiter.middleware(apiChain)
	apiChain = auth.Middleware(svc, auth.Options{Channel: app.ChannelAPI, AllowCookie: true, CheckOrigin: true})(apiChain)
	apiChain = readDeadline(apiReadTimeout, limitBody(maxAPIBody, withMeta(svc.Cfg, apiChain)))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.Handle(prefix+"/", apiChain)
	// GitHub's webhook: no session or token, the signature is the credential.
	mux.Handle("POST "+prefix+"/github/webhook", readDeadline(apiReadTimeout, http.HandlerFunc(a.githubWebhook)))
	// File streams below /api/v1: same authentication, no 8 MB body cap.
	fileWrap := func(next http.Handler) http.Handler {
		next = annotate(next)
		next = limiter.middleware(next)
		next = auth.Middleware(svc, auth.Options{Channel: app.ChannelAPI, AllowCookie: true, CheckOrigin: true})(next)
		return withMeta(svc.Cfg, next)
	}
	a.backupFileRoutes(mux, fileWrap)
	a.fileStreamRoutes(mux, fileWrap)
	// A shell in the app's container, over a WebSocket (session only).
	a.terminalRoutes(mux, fileWrap)
	// Upload a zip and deploy it in one request (the dashboard's upload).
	mux.Handle("POST "+prefix+"/apps/{app}/upload", fileWrap(readDeadline(uploadReadTimeout, http.HandlerFunc(a.appUpload))))

	if mcpHandler != nil {
		// /mcp takes bearer tokens only (never cookies). The actor is in the
		// context for the MCP handler (auth.ActorFrom) with channel mcp.
		var m http.Handler = a.mcpLongPolls(mcpHandler)
		m = annotate(m)
		m = limiter.middleware(m)
		m = auth.Middleware(svc, auth.Options{Channel: app.ChannelMCP})(m)
		m = readDeadline(mcpReadTimeout, limitBody(mcpBodyLimit, withMeta(svc.Cfg, m)))
		mux.Handle("/mcp", m)
		mux.Handle("/mcp/", m)
	}

	mux.Handle("POST /u/{id}", readDeadline(uploadReadTimeout, http.HandlerFunc(a.upload)))
	mux.Handle("GET /docs/api", &docs{api: a.h, base: prefix})
	static := spa(web)
	mux.Handle("/", static)

	root := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if slug, domain, ok := a.appHostSlug(r.Host); ok {
			a.appPage(w, r, slug, domain)
			return
		}
		mux.ServeHTTP(w, r)
	})
	return logRequests(svc.Log, svc.Cfg, securityHeaders(svc.Cfg, root)), a.stop.stop
}

// mcpLongPolls gives a get_deployment call with wait_seconds on /mcp a
// context that also ends when bitploy stops, so it does not hold up the
// shutdown. Every other MCP request keeps the request's own context.
func (a *API) mcpLongPolls(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.Body != nil {
			body, err := io.ReadAll(r.Body)
			var rest io.Reader = bytes.NewReader(body)
			if err != nil {
				rest = io.MultiReader(rest, errReader{err}) // the MCP handler sees the same error
			}
			r.Body = io.NopCloser(rest)
			if err == nil && isLongPoll(body) {
				ctx, cancel := a.untilStopping(r.Context())
				defer cancel()
				r = r.WithContext(ctx)
			}
		}
		next.ServeHTTP(w, r)
	})
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

// isLongPoll reports whether a JSON-RPC body (one message or a batch) calls
// get_deployment with wait_seconds.
func isLongPoll(body []byte) bool {
	type call struct {
		Method string `json:"method"`
		Params struct {
			Name      string `json:"name"`
			Arguments struct {
				WaitSeconds float64 `json:"wait_seconds"`
			} `json:"arguments"`
		} `json:"params"`
	}
	var calls []call
	if b := bytes.TrimSpace(body); len(b) > 0 && b[0] == '[' {
		_ = json.Unmarshal(b, &calls)
	} else {
		var c call
		if json.Unmarshal(b, &c) == nil {
			calls = append(calls, c)
		}
	}
	for _, c := range calls {
		if c.Method == "tools/call" && c.Params.Name == "get_deployment" && c.Params.Arguments.WaitSeconds > 0 {
			return true
		}
	}
	return false
}

func notFoundJSON(w http.ResponseWriter) {
	auth.WriteError(w, apperr.NotFound("not_found", "There is nothing at this address.",
		"See the API reference at /docs/api or the spec at /api/v1/openapi.json."))
}
