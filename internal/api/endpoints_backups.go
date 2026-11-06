package api

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/AVMG20/bitploy/internal/app"
	"github.com/AVMG20/bitploy/internal/auth"
)

// backupUploadTimeout is how long a backup upload may take to arrive.
const backupUploadTimeout = time.Hour

type backupRef struct {
	AppRef
	ID string `path:"id" doc:"Backup ID (bak_...)"`
}

// backupRoutes registers the JSON endpoints of /data backups. The download and
// the upload are plain handlers (backupFileRoutes): they stream a file.
func (a *API) backupRoutes() {
	reg(a, anyAuth, huma.Operation{
		OperationID: "listBackups", Method: http.MethodGet, Path: "/apps/{app}/backups", Tags: []string{"Backups"},
		Summary: "List /data backups and the automatic backup settings",
		Description: "Newest first. `restore` tells how the latest restore is going. " +
			"A backup file is downloaded with GET /apps/{app}/backups/{id}/download and an archive is uploaded as the raw body of POST /apps/{app}/backups/upload (a .tar.gz). A database service's dumps (format dump) download as .sql.gz.",
	}, func(ctx context.Context, act app.Actor, in *AppRef) (*Out[app.BackupList], error) {
		return okOrNil(a.svc.ListBackups(ctx, act, in.App))
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "createBackup", Method: http.MethodPost, Path: "/apps/{app}/backups", Tags: []string{"Backups"},
		Summary: "Back up /data now",
		Description: "Returns at once with a backup in status running; poll the list until it is ready or failed. " +
			"A running PostgreSQL, MySQL or MariaDB service is dumped with its own tool (format dump) and keeps serving; other apps are frozen for a moment while the files are read.",
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		Volume string `query:"volume" doc:"Name of one volume of an app with several (see volumes in get app). Default: the app's first volume."`
	}) (*Out[app.Backup], error) {
		return okOrNil(a.svc.CreateBackup(ctx, act, in.App, in.Volume))
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "deleteBackup", Method: http.MethodDelete, Path: "/apps/{app}/backups/{id}", Tags: []string{"Backups"},
		Summary: "Delete a backup", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, act app.Actor, in *backupRef) (*Empty, error) {
		if err := a.svc.DeleteBackup(ctx, act, in.App, in.ID); err != nil {
			return nil, err
		}
		return &Empty{}, nil
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "restoreBackup", Method: http.MethodPost, Path: "/apps/{app}/backups/{id}/restore", Tags: []string{"Backups"},
		Summary: "Restore a backup into /data",
		Description: "Makes a backup of the current data first, stops the app, replaces the files and starts the app again. " +
			"A dump (format dump) is replayed into the running database instead: its databases are dropped and made again from the dump, with a dump of the current ones made first; the app is not stopped and must run (database_not_running). " +
			"Returns at once; follow it in `restore` of the backup list.",
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, act app.Actor, in *backupRef) (*Out[app.BackupRestore], error) {
		return okOrNil(a.svc.RestoreBackup(ctx, act, in.App, in.ID))
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "dismissBackupRestore", Method: http.MethodDelete, Path: "/apps/{app}/backup-restore", Tags: []string{"Backups"},
		Summary:       "Dismiss the result of the latest restore",
		Description:   "Forgets a finished restore (done or failed), so `restore` of the backup list is empty again. A restore that is still running cannot be dismissed.",
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, act app.Actor, in *AppRef) (*Empty, error) {
		if err := a.svc.DismissRestore(ctx, act, in.App); err != nil {
			return nil, err
		}
		return &Empty{}, nil
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "updateBackupSettings", Method: http.MethodPut, Path: "/apps/{app}/backup-settings", Tags: []string{"Backups"},
		Summary: "Set the automatic backups",
		Description: "schedule off, daily (every night at 03:00 UTC) or weekly (Sunday 03:00 UTC); keep the newest `keep` backups (keep_mode last) or `keep` days (keep_mode days). " +
			"Retention applies to automatic and before-restore backups. Fields left out stay unchanged.",
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		Body app.BackupSettingsInput
	}) (*Out[app.BackupSettings], error) {
		return okOrNil(a.svc.UpdateBackupSettings(ctx, act, in.App, in.Body))
	})
}

// backupFileRoutes registers the two streaming endpoints below /api/v1. They
// sit behind the same authentication as the JSON API (bearer token or session
// cookie, CSRF for the cookie upload) but without its small body limit.
func (a *API) backupFileRoutes(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	mux.Handle("GET "+prefix+"/apps/{app}/backups/{id}/download", wrap(http.HandlerFunc(a.backupDownload)))
	mux.Handle("POST "+prefix+"/apps/{app}/backups/upload",
		wrap(readDeadline(backupUploadTimeout, http.HandlerFunc(a.backupUpload))))
}

// backupDownload streams the .tar.gz (or a dump's .sql.gz) as an attachment, so a plain link works
// in the browser (cookie) and curl (bearer token). Range requests work too.
func (a *API) backupDownload(w http.ResponseWriter, r *http.Request) {
	act, err := auth.Require(r.Context())
	if err != nil {
		auth.WriteError(w, err)
		return
	}
	f, name, err := a.svc.OpenBackup(r.Context(), act, r.PathValue("app"), r.PathValue("id"))
	if err != nil {
		auth.WriteError(w, err)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		auth.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, name, fi.ModTime(), f)
}

// backupUpload takes the raw .tar.gz as the request body.
func (a *API) backupUpload(w http.ResponseWriter, r *http.Request) {
	act, err := auth.Require(r.Context())
	if err != nil {
		auth.WriteError(w, err)
		return
	}
	b, err := a.svc.UploadBackup(r.Context(), act, r.PathValue("app"), r.Body, r.ContentLength, r.URL.Query().Get("volume"))
	if err != nil {
		// An answer sent while the browser is still sending is often never
		// seen (the connection is reset): let the rest of a reasonable upload
		// arrive first.
		drainBody(w, r)
		auth.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(b)
}

// Most of a rejected upload that is read and thrown away, and for how long.
const (
	drainMaxBytes = 256 << 20
	drainMaxTime  = 30 * time.Second
)

// drainBody reads and discards what is left of the request body, within limits.
func drainBody(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(drainMaxTime))
	_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, drainMaxBytes))
}
