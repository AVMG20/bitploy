// Package api is the REST door (/api/v1) and the top-level HTTP router.
// OWNER: auth/API agent.
package api

import (
	"io/fs"
	"net/http"

	"github.com/arno/bitploy/internal/app"
)

// New returns bitploy's root handler: /healthz, /api/v1, /mcp (mcpHandler),
// /u/{id} uploads, /docs/api, the catch-all app pages and the embedded SPA.
func New(svc *app.Service, mcpHandler http.Handler, web fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.Handle("/mcp", mcpHandler)
	mux.Handle("/", http.FileServerFS(web))
	return mux
}
