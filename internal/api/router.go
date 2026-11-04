// Package api is the REST door (/api/v1) and the top-level HTTP router.
// OWNER: auth/API agent.
package api

import (
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
	a := &API{svc: svc, cfg: svc.Cfg}
	apiMux := http.NewServeMux()
	a.h = newHuma(apiMux, svc.Cfg)
	a.accountRoutes()
	a.appRoutes()
	a.backupRoutes()
	a.filesRoutes()
	a.catalogRoutes()
	a.dbRoutes()
	a.adminDBRoutes()
	a.githubRoutes()
	a.adminRoutes()
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
	// Upload a zip and deploy it in one request (the dashboard's upload).
	mux.Handle("POST "+prefix+"/apps/{app}/upload", fileWrap(readDeadline(uploadReadTimeout, http.HandlerFunc(a.appUpload))))

	if mcpHandler != nil {
		// /mcp takes bearer tokens only (never cookies). The actor is in the
		// context for the MCP handler (auth.ActorFrom) with channel mcp.
		var m http.Handler = mcpHandler
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
	return logRequests(svc.Log, svc.Cfg, securityHeaders(svc.Cfg, root))
}

func notFoundJSON(w http.ResponseWriter) {
	auth.WriteError(w, apperr.NotFound("not_found", "There is nothing at this address.",
		"See the API reference at /docs/api or the spec at /api/v1/openapi.json."))
}
