package api

import (
	"bytes"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// spa serves the embedded dashboard. Files that exist are served as they
// are; every other non-API path gets index.html so vue-router can take over.
func spa(web fs.FS) http.Handler {
	index, _ := fs.ReadFile(web, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			notFoundJSON(w)
			return
		}
		p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if strings.HasPrefix(p, "api/") || p == "api" {
			notFoundJSON(w)
			return
		}
		if p != "" && p != "index.html" {
			if st, err := fs.Stat(web, p); err == nil && !st.IsDir() {
				f, err := web.Open(p)
				if err == nil {
					defer f.Close()
					if strings.HasPrefix(p, "assets/") {
						w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
					}
					if p == "sw.js" {
						// The service worker of browser notifications: a new
						// version must reach browsers on their next visit.
						w.Header().Set("Cache-Control", "no-cache")
					}
					if rs, ok := f.(io.ReadSeeker); ok {
						http.ServeContent(w, r, p, st.ModTime(), rs)
						return
					}
					b, _ := io.ReadAll(f)
					http.ServeContent(w, r, p, time.Time{}, bytes.NewReader(b))
					return
				}
			}
			// A path with a file extension that does not exist is a real 404,
			// not a client-side route.
			if path.Ext(p) != "" {
				http.NotFound(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}
