package api

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/AVMG20/bitploy/internal/app"
	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/auth"
)

// fileUploadTimeout is how long one file upload may take to arrive.
const fileUploadTimeout = 15 * time.Minute

// filesRoutes registers the JSON endpoints of the /data file browser. The
// download and the upload are plain handlers (fileStreamRoutes): they stream a
// file.
func (a *API) filesRoutes() {
	reg(a, anyAuth, huma.Operation{
		OperationID: "listFiles", Method: http.MethodGet, Path: "/apps/{app}/files", Tags: []string{"Files"},
		Summary: "List a folder of /data",
		Description: "`path` is relative to /data; leave it out for /data itself. Folders first, then by name, at most 1000 items (`truncated` says there are more). " +
			"Needs /data turned on and created (the app has started once). A file is downloaded with GET /apps/{app}/files/download?path=... and uploaded as the raw body of " +
			"POST /apps/{app}/files/upload?path=<folder>&name=<file>[&overwrite=true] (up to 100 MB).",
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		Path   string `query:"path" doc:"Folder, relative to /data (default: /data itself)"`
		Volume string `query:"volume" doc:"Name of one volume of an app with several (see volumes in get app). Default: the app's first volume."`
	}) (*Out[app.FileList], error) {
		return okOrNil(a.svc.ListFiles(ctx, act, in.App, in.Path, in.Volume))
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "createFolder", Method: http.MethodPost, Path: "/apps/{app}/files/folder", Tags: []string{"Files"},
		Summary:       "Make a folder in /data",
		Description:   "Makes the folder `name` inside the existing folder `path` (relative to /data). It gets the owner of the folder around it.",
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		Volume string `query:"volume" doc:"Name of one volume of an app with several (see volumes in get app). Default: the app's first volume."`
		Body   struct {
			Path string `json:"path,omitempty" doc:"Folder to make it in, relative to /data (default: /data itself)"`
			Name string `json:"name" doc:"Name of the new folder"`
		}
	}) (*Out[app.FileEntry], error) {
		return okOrNil(a.svc.MakeFolder(ctx, act, in.App, in.Body.Path, in.Body.Name, in.Volume))
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "deleteFile", Method: http.MethodDelete, Path: "/apps/{app}/files", Tags: []string{"Files"},
		Summary:       "Delete a file or folder in /data",
		Description:   "A folder goes with everything in it. A symbolic link is removed, not what it points to. /data itself cannot be deleted. This cannot be undone: make a backup first if unsure.",
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		Path   string `query:"path" required:"true" doc:"File or folder, relative to /data"`
		Volume string `query:"volume" doc:"Name of one volume of an app with several (see volumes in get app). Default: the app's first volume."`
	}) (*Empty, error) {
		if err := a.svc.DeleteFile(ctx, act, in.App, in.Path, in.Volume); err != nil {
			return nil, err
		}
		return &Empty{}, nil
	})
}

// fileStreamRoutes registers the two streaming endpoints below /api/v1. They
// sit behind the same authentication as the JSON API (bearer token or session
// cookie, CSRF for the cookie upload) but without its small body limit.
func (a *API) fileStreamRoutes(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	mux.Handle("GET "+prefix+"/apps/{app}/files/download", wrap(http.HandlerFunc(a.fileDownload)))
	mux.Handle("POST "+prefix+"/apps/{app}/files/upload",
		wrap(readDeadline(fileUploadTimeout, http.HandlerFunc(a.fileUpload))))
}

// fileDownload streams one file as an attachment, so a plain link works in the
// browser (cookie) and curl (bearer token).
func (a *API) fileDownload(w http.ResponseWriter, r *http.Request) {
	act, err := auth.Require(r.Context())
	if err != nil {
		auth.WriteError(w, err)
		return
	}
	d, err := a.svc.OpenFile(r.Context(), act, r.PathValue("app"), r.URL.Query().Get("path"), r.URL.Query().Get("volume"))
	if err != nil {
		auth.WriteError(w, err)
		return
	}
	defer d.Body.Close()
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": d.Name}))
	h.Set("Content-Length", strconv.FormatInt(d.Size, 10))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	if !d.ModTime.IsZero() {
		h.Set("Last-Modified", d.ModTime.UTC().Format(http.TimeFormat))
	}
	_, _ = io.Copy(w, d.Body)
}

// fileUpload takes the raw file as the request body.
func (a *API) fileUpload(w http.ResponseWriter, r *http.Request) {
	act, err := auth.Require(r.Context())
	if err != nil {
		auth.WriteError(w, err)
		return
	}
	q := r.URL.Query()
	overwrite := false
	if v := q.Get("overwrite"); v != "" {
		if overwrite, err = strconv.ParseBool(v); err != nil {
			drainBody(w, r)
			auth.WriteError(w, apperr.BadRequest("overwrite_invalid", "overwrite must be true or false.", "Send overwrite=true to replace an existing file."))
			return
		}
	}
	e, err := a.svc.UploadFile(r.Context(), act, r.PathValue("app"), q.Get("path"), q.Get("name"), overwrite, r.Body, r.ContentLength, q.Get("volume"))
	if err != nil {
		// See backupUpload: let the rest of a reasonable upload arrive first.
		drainBody(w, r)
		auth.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(e)
}
