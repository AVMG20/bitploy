package app

// OWNER: deploy agent. Sources (inline files, zip uploads, git), the deploy
// queue and the read side of deployments.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/deploy"
	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/ids"
	"github.com/AVMG20/bitploy/internal/store/db"
)

const (
	uploadTTL        = 15 * time.Minute
	uploadDeployTTL  = 24 * time.Hour // a received upload can be deployed for this long
	maxParkedUploads = 5              // uploads per user that wait to be deployed (each up to 95 MB)
	maxStoredLogTail = 50
)

func (s *Service) sourcesDir() string         { return s.Cfg.Path("sources") }
func (s *Service) sourceZip(id string) string { return filepath.Join(s.sourcesDir(), id+".zip") }
func (s *Service) uploadPath(id string) string {
	return filepath.Join(s.Cfg.UploadsDir(), id+".zip")
}

// ---- sources & deployments ----

// DeployFiles stores inline files (2 MB, 200 files), runs the Dockerfile
// checks synchronously (so the agent gets errors at once), and queues a deployment.
func (s *Service) DeployFiles(ctx context.Context, a Actor, ref string, files []FileInput) (Deployment, error) {
	return s.deployFiles(ctx, a, ref, files, "")
}

// deployFiles is DeployFiles; catalogVersion is set when the files are a
// catalog Dockerfile (see queueSpec.catalogVersion).
func (s *Service) deployFiles(ctx context.Context, a Actor, ref string, files []FileInput, catalogVersion string) (Deployment, error) {
	row, err := s.LoadApp(ctx, a, ref, true)
	if err != nil {
		return Deployment{}, err
	}
	if err := gitLinkedErr(row); err != nil {
		return Deployment{}, err
	}
	in := make([]deploy.File, len(files))
	for i, f := range files {
		in[i] = deploy.File{Path: f.Path, Content: f.Content, Encoding: f.Encoding}
	}
	decoded, err := deploy.DecodeFiles(in)
	if err != nil {
		return Deployment{}, err
	}
	byPath := make(map[string][]byte, len(decoded))
	paths := make([]string, len(decoded))
	for i, f := range decoded {
		byPath[f.Path] = f.Data
		paths[i] = f.Path
	}
	// The compose file, or the build mode (the app's Dockerfile or a static site).
	res, ae := s.checkSource(ctx, row, paths, func(p string) []byte { return byPath[path.Clean(p)] })
	if ae != nil {
		return Deployment{}, ae
	}
	return s.queueDeployment(ctx, a, row, queueSpec{
		kind:           "files",
		port:           portOf(res.Port),
		hasHC:          res.HasHealthcheck,
		warnings:       res.Findings,
		catalogVersion: catalogVersion,
		prepare: func(depID string) error {
			if err := os.MkdirAll(s.sourcesDir(), 0o750); err != nil {
				return err
			}
			return deploy.WriteZip(s.sourceZip(depID), decoded)
		},
	})
}

// CreateUpload makes a one-time upload URL (15 min) and the exact curl command.
func (s *Service) CreateUpload(ctx context.Context, a Actor, ref string) (Upload, error) {
	row, err := s.LoadApp(ctx, a, ref, true)
	if err != nil {
		return Upload{}, err
	}
	if err := gitLinkedErr(row); err != nil {
		return Upload{}, err
	}
	user, err := s.activeUser(ctx, row.UserID)
	if err != nil {
		return Upload{}, err
	}
	// An upload is the first step of a deployment: the same limits apply, and
	// a user can keep only a few uploads waiting, since they take disk space
	// that is not counted anywhere else.
	if err := s.checkDeployRate(ctx, s.Q, user); err != nil {
		return Upload{}, err
	}
	if err := s.checkStorageRoom(ctx, user); err != nil {
		return Upload{}, err
	}
	id := ids.New(ids.Upload)
	now := s.Now()
	exp := now.Add(uploadTTL)
	err = s.Store.Tx(ctx, func(q *db.Queries) error {
		n, err := q.CountParkedUploads(ctx, db.CountParkedUploadsParams{
			UserID: user.ID, Now: now.Unix(), ReceivedAfter: nullInt(now.Add(-uploadDeployTTL).Unix(), true),
		})
		if err != nil {
			return err
		}
		if n >= maxParkedUploads {
			return apperr.TooMany("too_many_uploads",
				fmt.Sprintf("You already have %d uploads waiting to be deployed, which is the limit.", n),
				"Call deploy with one of the upload_ids you already have, or wait: an upload URL that was not used expires after 15 minutes, an uploaded zip after 24 hours.").WithNext("deploy")
		}
		return q.InsertUpload(ctx, db.InsertUploadParams{
			ID: id, AppID: row.ID, UserID: row.UserID, ExpiresAt: exp.Unix(), CreatedAt: now.Unix(),
		})
	})
	if err != nil {
		return Upload{}, apperr.As(err)
	}
	url := s.Cfg.PublicURL + "/u/" + id
	s.Audit(ctx, a, "upload.create", "app", row.ID, map[string]any{"upload_id": id})
	return Upload{
		ID: id, URL: url,
		Curl:      "curl -sS --fail-with-body --data-binary @app.zip -H 'Content-Type: application/zip' " + url,
		ExpiresAt: exp.UTC(), MaxBytes: deploy.MaxZipCompressed,
		NextSteps: []string{
			"Run `zip -r app.zip . -x 'node_modules/*' '*/node_modules/*' '.git/*' '*/.git/*' '.env' '.env.*' '*/.env' '*/.env.*' '.bitploy'` in the code folder, then run the curl command in that same folder, within 15 minutes.",
			"After curl succeeds, call deploy with app=" + row.Slug + " and upload_id=" + id + ".",
		},
	}, nil
}

// ReceiveUpload stores the raw zip body for POST /u/{id} (no actor: the ID is
// the credential). Max 95 MB. Returns what to do next.
func (s *Service) ReceiveUpload(ctx context.Context, uploadID string, body io.Reader) (map[string]any, error) {
	u, err := s.Q.GetUpload(ctx, uploadID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperr.NotFound("upload_not_found", "This upload URL does not exist.", "Call create_upload to get a new one.").WithNext("create_upload")
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if u.Status != "pending" {
		return nil, apperr.Conflict("upload_used", "This upload URL was already used.", "Upload URLs work once. Call create_upload for a new one.").WithNext("create_upload")
	}
	if u.ExpiresAt < s.now() {
		return nil, apperr.New(410, "upload_expired", "This upload URL has expired (they last 15 minutes).", "Call create_upload to get a new one.").WithNext("create_upload")
	}
	// An upload is the first step of a deployment: with the disk almost full
	// (the same 15% rule) refuse it before anything is written, otherwise a
	// stream of 95 MB uploads could fill the disk that every app depends on.
	if err := s.checkDiskRoom(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.Cfg.UploadsDir(), 0o750); err != nil {
		return nil, apperr.Internal(err)
	}
	tmp := s.uploadPath(uploadID) + ".part-" + randHex(4)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	n, cerr := io.Copy(f, io.LimitReader(body, deploy.MaxZipCompressed+1))
	f.Close()
	if cerr != nil {
		os.Remove(tmp)
		var tooBig *http.MaxBytesError // the HTTP layer's own size cap fired before ours
		if errors.As(cerr, &tooBig) {
			return nil, apperr.New(413, "upload_too_large", "The zip is bigger than 95 MB.", "Leave out node_modules, .git, build output and videos, zip again and upload again.")
		}
		return nil, apperr.BadRequest("upload_interrupted", "The upload was cut off before it finished.", "Run the curl command again.")
	}
	if n > deploy.MaxZipCompressed {
		os.Remove(tmp)
		return nil, apperr.New(413, "upload_too_large", "The zip is bigger than 95 MB.", "Leave out node_modules, .git, build output and videos, zip again and upload again.")
	}
	files, zerr := deploy.InspectZip(tmp, deploy.DefaultZipLimits)
	if zerr != nil {
		os.Remove(tmp)
		return nil, zerr
	}
	rows, err := s.Q.MarkUploadReceived(ctx, db.MarkUploadReceivedParams{
		SizeBytes: sql.NullInt64{Int64: n, Valid: true}, ReceivedAt: sql.NullInt64{Int64: s.now(), Valid: true}, ID: uploadID,
	})
	if err != nil || rows == 0 {
		os.Remove(tmp)
		if err != nil {
			return nil, apperr.Internal(err)
		}
		return nil, apperr.Conflict("upload_used", "This upload URL was already used.", "Call create_upload for a new one.").WithNext("create_upload")
	}
	if err := os.Rename(tmp, s.uploadPath(uploadID)); err != nil {
		return nil, apperr.Internal(err)
	}
	slug := ""
	if app, err := s.Q.GetApp(ctx, u.AppID); err == nil {
		slug = app.Slug
	}
	s.Audit(ctx, Actor{UserID: u.UserID, Channel: ChannelAPI, Scope: ScopeDeploy}, "upload.receive", "app", u.AppID, map[string]any{"upload_id": uploadID, "bytes": n})
	return map[string]any{
		"upload_id":  uploadID,
		"app":        slug,
		"size_bytes": n,
		"files":      files,
		"next_steps": []string{"Call deploy with app=" + slug + " and upload_id=" + uploadID + " to build and publish it."},
	}, nil
}

// Deploy queues a deployment from an upload (uploadID != "") or the app's git source.
func (s *Service) Deploy(ctx context.Context, a Actor, ref, uploadID string) (Deployment, error) {
	row, err := s.LoadApp(ctx, a, ref, true)
	if err != nil {
		return Deployment{}, err
	}
	if uploadID != "" {
		if err := gitLinkedErr(row); err != nil {
			return Deployment{}, err
		}
		u, err := s.Q.GetUpload(ctx, uploadID)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && u.AppID != row.ID) {
			return Deployment{}, apperr.NotFound("upload_not_found", "There is no upload "+quote(uploadID)+" for this app.", "Call create_upload to get an upload URL for this app.").WithNext("create_upload")
		}
		if err != nil {
			return Deployment{}, apperr.Internal(err)
		}
		switch u.Status {
		case "pending":
			return Deployment{}, apperr.Conflict("upload_not_received", "Nothing was uploaded to this URL yet.", "Run the curl command from create_upload first, then call deploy again.").WithNext("create_upload")
		case "used":
			return Deployment{}, apperr.Conflict("upload_used", "This upload was already deployed.", "Call create_upload for a new upload, or call deploy with only app= to rebuild the last source.").WithNext("create_upload")
		case "received":
		default:
			return Deployment{}, apperr.New(410, "upload_expired", "This upload has expired.", "Call create_upload for a new one.").WithNext("create_upload")
		}
		if u.ReceivedAt.Valid && s.now()-u.ReceivedAt.Int64 > int64(uploadDeployTTL.Seconds()) {
			return Deployment{}, apperr.New(410, "upload_expired", "This upload is older than 24 hours.", "Call create_upload and upload again.").WithNext("create_upload")
		}
		if _, err := os.Stat(s.uploadPath(uploadID)); err != nil {
			return Deployment{}, apperr.New(410, "upload_expired", "The uploaded file is gone.", "Call create_upload and upload again.").WithNext("create_upload")
		}
		return s.queueDeployment(ctx, a, row, queueSpec{
			kind: "upload", ref: uploadID,
			prepare: func(depID string) error {
				if err := os.MkdirAll(s.sourcesDir(), 0o750); err != nil {
					return err
				}
				return linkOrCopy(s.uploadPath(uploadID), s.sourceZip(depID))
			},
			inTx: func(q *db.Queries) error {
				n, err := q.MarkUploadUsed(ctx, db.MarkUploadUsedParams{UsedAt: sql.NullInt64{Int64: s.now(), Valid: true}, ID: uploadID})
				if err != nil {
					return err
				}
				if n == 0 {
					return apperr.Conflict("upload_used", "This upload was already deployed.", "Call create_upload for a new one.").WithNext("create_upload")
				}
				return nil
			},
		})
	}
	if row.GitRepoUrl.Valid && row.GitRepoUrl.String != "" {
		return s.queueDeployment(ctx, a, row, queueSpec{kind: "git", ref: row.GitBranch.String})
	}
	return Deployment{}, noSourceErr()
}

// sourceDeployment returns the deployment whose source built id: id itself,
// or for a rollback the deployment it rolled back to (following chains).
// deps is every deployment of the app. Nil when id is unknown.
func sourceDeployment(deps []db.Deployment, id string) *db.Deployment {
	byID := make(map[string]*db.Deployment, len(deps))
	for i := range deps {
		byID[deps[i].ID] = &deps[i]
	}
	d := byID[id]
	for hops := 0; d != nil && d.SourceKind == "rollback" && hops < len(deps); hops++ {
		d = byID[d.SourceRef]
	}
	if d != nil && d.SourceKind == "rollback" {
		return nil
	}
	return d
}

func noSourceErr() *apperr.Error {
	return apperr.BadRequest("no_source", "This app has no source to deploy yet.",
		"Send the files with deploy_files, upload a zip (create_upload, then deploy with upload_id), or set a git repository with set_git_source.").WithNext("deploy_files")
}

// Redeploy queues a deployment that rebuilds the live version's source (git:
// latest commit; files/upload: the stored snapshot). After a rollback that is
// the source of the version rolled back to. Used by the dashboard and REST.
func (s *Service) Redeploy(ctx context.Context, a Actor, ref string) (Deployment, error) {
	return s.redeploy(ctx, a, ref, false)
}

// RetryLatest rebuilds the newest attempt's source, even when that deployment
// failed: "fix a setting with update_app, then deploy with only app=" must
// retry the new files, not rebuild the older version that is still live.
func (s *Service) RetryLatest(ctx context.Context, a Actor, ref string) (Deployment, error) {
	return s.redeploy(ctx, a, ref, true)
}

func (s *Service) redeploy(ctx context.Context, a Actor, ref string, latest bool) (Deployment, error) {
	row, err := s.LoadApp(ctx, a, ref, true)
	if err != nil {
		return Deployment{}, err
	}
	deps, err := s.Q.ListAllDeploymentsByApp(ctx, row.ID) // newest first
	if err != nil {
		return Deployment{}, apperr.Internal(err)
	}
	if row.GitRepoUrl.Valid && row.GitRepoUrl.String != "" {
		// A linked repository is the source: rebuild its branch, not an older upload.
		return s.queueDeployment(ctx, a, row, queueSpec{kind: "git", ref: row.GitBranch.String})
	}
	var pick *db.Deployment
	if !latest && row.CurrentDeploymentID.Valid {
		pick = sourceDeployment(deps, row.CurrentDeploymentID.String)
	}
	if pick == nil { // newest attempt, or no live version yet
		for _, d := range deps {
			if d.State != "cancelled" {
				pick = sourceDeployment(deps, d.ID)
				break
			}
		}
	}
	if pick != nil {
		switch pick.SourceKind {
		case "git":
			if row.GitRepoUrl.Valid && row.GitRepoUrl.String != "" {
				return s.queueDeployment(ctx, a, row, queueSpec{kind: "git", ref: row.GitBranch.String})
			}
		case "files", "upload":
			if _, err := os.Stat(s.sourceZip(pick.ID)); err == nil {
				src := s.sourceZip(pick.ID)
				return s.queueDeployment(ctx, a, row, queueSpec{
					kind: pick.SourceKind, ref: pick.SourceRef, catalogVersion: pick.CatalogVersion,
					prepare: func(depID string) error { return linkOrCopy(src, s.sourceZip(depID)) },
				})
			}
		}
	}
	if row.GitRepoUrl.Valid && row.GitRepoUrl.String != "" {
		return s.queueDeployment(ctx, a, row, queueSpec{kind: "git", ref: row.GitBranch.String})
	}
	if len(deps) == 0 {
		return Deployment{}, apperr.Conflict("app_not_deployed",
			"This app has never been deployed, so there is nothing to redeploy.",
			"Send the code first: deploy_files with all files, create_upload and deploy for a zip, or set_git_source and deploy for a git repository.").WithNext("deploy_files")
	}
	return Deployment{}, apperr.Conflict("source_gone",
		"The source of the last deployment is no longer stored, so there is nothing to rebuild.",
		"Send the files again with deploy_files, or upload a zip with create_upload.").WithNext("deploy_files")
}

// GetDeployment returns a deployment; with wait > 0 (max 50 s) it long-polls
// until the state changes to a terminal state or wait runs out.
func (s *Service) GetDeployment(ctx context.Context, a Actor, id string, wait time.Duration) (Deployment, error) {
	if wait > 50*time.Second {
		wait = 50 * time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		d, app, err := s.loadDeployment(ctx, a, id)
		if err != nil {
			return Deployment{}, err
		}
		v := s.deploymentView(ctx, d, app)
		left := time.Until(deadline)
		if v.Terminal() || left <= 0 || ctx.Err() != nil {
			return v, nil
		}
		// Short slices: a state change between the read and the wait is
		// picked up on the next loop at the latest.
		if left > 2*time.Second {
			left = 2 * time.Second
		}
		s.WaitDeployment(ctx, id, left)
	}
}

func (s *Service) loadDeployment(ctx context.Context, a Actor, id string) (db.Deployment, db.App, error) {
	d, err := s.Q.GetDeployment(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Deployment{}, db.App{}, deploymentNotFound(id)
	}
	if err != nil {
		return db.Deployment{}, db.App{}, apperr.Internal(err)
	}
	app, err := s.LoadApp(ctx, a, d.AppID, false)
	if err != nil {
		var ae *apperr.Error
		if errors.As(err, &ae) && ae.Code == "app_not_found" {
			return db.Deployment{}, db.App{}, deploymentNotFound(id)
		}
		return db.Deployment{}, db.App{}, err
	}
	return d, app, nil
}

func deploymentNotFound(id string) *apperr.Error {
	return apperr.NotFound("deployment_not_found", "There is no deployment "+quote(id)+" in your account.",
		"Call list_deployments with the app to see its deployments.").WithNext("list_deployments")
}

func (s *Service) ListDeployments(ctx context.Context, a Actor, ref string, limit int, cursor string) (Page[Deployment], error) {
	row, err := s.LoadApp(ctx, a, ref, false)
	if err != nil {
		return Page[Deployment]{}, err
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	before := int64(1<<62 - 1)
	if cursor != "" {
		n, err := strconv.ParseInt(cursor, 10, 64)
		if err != nil || n < 1 {
			return Page[Deployment]{}, apperr.BadRequest("cursor_invalid", "The cursor is not valid.", "Use the next_cursor value from the previous page, or leave it out.")
		}
		before = n
	}
	rows, err := s.Q.ListDeploymentsByApp(ctx, db.ListDeploymentsByAppParams{AppID: row.ID, Seq: before, Limit: int64(limit) + 1})
	if err != nil {
		return Page[Deployment]{}, apperr.Internal(err)
	}
	page := Page[Deployment]{Items: []Deployment{}}
	for i, d := range rows {
		if i == limit {
			page.NextCursor = strconv.FormatInt(rows[limit-1].Seq, 10)
			break
		}
		page.Items = append(page.Items, s.deploymentView(ctx, d, row))
	}
	return page, nil
}

// Rollback reruns a previous deployment's image from `starting`.
func (s *Service) Rollback(ctx context.Context, a Actor, ref, deploymentID string) (Deployment, error) {
	row, err := s.LoadApp(ctx, a, ref, true)
	if err != nil {
		return Deployment{}, err
	}
	target, err := s.Q.GetDeployment(ctx, deploymentID)
	if err != nil || target.AppID != row.ID {
		return Deployment{}, deploymentNotFound(deploymentID)
	}
	if !target.Image.Valid || target.Image.String == "" || (target.State != "running" && target.State != "superseded") {
		return Deployment{}, apperr.Conflict("rollback_unavailable",
			"Deployment "+quote(deploymentID)+" never ran successfully, so there is nothing to roll back to.",
			"Call list_deployments and pick a deployment whose state is running or superseded.").WithNext("list_deployments")
	}
	if s.Docker != nil {
		if _, err := s.Docker.InspectImage(ctx, target.Image.String); err != nil {
			return Deployment{}, apperr.Conflict("rollback_unavailable",
				"The image of that deployment was removed (bitploy keeps the last 3).",
				"Pick a more recent deployment, or deploy the source again.").WithNext("list_deployments")
		}
		if isMulti(row) {
			svcs, err := s.Q.ListDeploymentServices(ctx, target.ID)
			if err != nil {
				return Deployment{}, apperr.Internal(err)
			}
			if target.ServicesSpec == "" && !coversServices(ctx, s, row, svcs) {
				return Deployment{}, apperr.Conflict("rollback_unavailable",
					"Deployment "+quote(deploymentID)+" ran before the app had its current services, so bitploy cannot go back to it.",
					"An app with several services cannot go back to one service. Pick a deployment of the compose file from list_deployments, or create a new app for the single-service version.").WithNext("list_deployments")
			}
			for _, sv := range svcs {
				if !sv.Image.Valid {
					continue
				}
				if _, err := s.Docker.InspectImage(ctx, sv.Image.String); err != nil {
					return Deployment{}, apperr.Conflict("rollback_unavailable",
						"The image of the "+sv.Service+" service of that deployment was removed (bitploy keeps the last 3).",
						"Pick a more recent deployment.").WithNext("list_deployments")
				}
			}
		}
	}
	return s.queueDeployment(ctx, a, row, queueSpec{
		kind: "rollback", ref: deploymentID, image: target.Image.String,
		port: target.Port, hasHC: target.HasHealthcheck != 0, warningsJSON: target.Warnings, buildKind: target.BuildKind,
		catalogVersion: target.CatalogVersion, servicesSpec: target.ServicesSpec,
	})
}

// coversServices reports whether a deployment without a services spec has
// an image for every service the app has now.
func coversServices(ctx context.Context, s *Service, app db.App, rows []db.DeploymentService) bool {
	specs, _, err := s.appSpecs(ctx, app)
	if err != nil {
		return false
	}
	have := map[string]bool{}
	for _, r := range rows {
		have[r.Service] = r.Image.Valid && r.Image.String != ""
	}
	for _, sp := range specs {
		if !have[sp.Name] {
			return false
		}
	}
	return true
}

// ---- queueing ----

type queueSpec struct {
	kind         string
	ref          string
	image        string
	port         sql.NullInt64
	hasHC        bool
	warnings     []deploy.Finding
	warningsJSON sql.NullString
	buildKind    string                   // a rollback has the kind of the deployment it returns to; others get theirs when validated
	prepare      func(depID string) error // writes the source snapshot before the row exists
	inTx         func(q *db.Queries) error
	// catalogVersion is the catalog version this deployment builds ("" when
	// it is not a catalog Dockerfile). The app's catalog_version takes it when
	// the deployment goes live (swap), so a failed or cancelled update does
	// not claim a version the app does not run.
	catalogVersion string
	// commitSHA and commitMessage are known when a push started the deployment;
	// the clone overwrites them with the commit it really built.
	commitSHA, commitMessage string
	// servicesSpec: a rollback to a compose deployment restores its services
	// and volumes (deployments.services_spec).
	servicesSpec string
}

func portOf(p int) sql.NullInt64 {
	if p <= 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(p), Valid: true}
}

// checkDiskRoom refuses new deployments (and rollbacks) while the server's
// disk is almost full, before anything is stored or counted.
func (s *Service) checkDiskRoom() error {
	if ok, err := s.DiskHasRoom(); err == nil && !ok {
		return apperr.Unavailable("server_disk_full",
			"The server is almost out of disk space, so new deployments are paused. Nothing was queued.",
			"Tell the bitploy admin that the server disk is full. Your running apps are not affected. Try again once the admin has freed space.")
	}
	return nil
}

func (s *Service) queueDeployment(ctx context.Context, a Actor, app db.App, qs queueSpec) (Deployment, error) {
	if err := s.checkDiskRoom(); err != nil {
		return Deployment{}, err
	}
	user, err := s.activeUser(ctx, app.UserID)
	if err != nil {
		return Deployment{}, err
	}
	if err := s.checkDeployRate(ctx, s.Q, user); err != nil {
		return Deployment{}, err
	}
	if qs.kind != "rollback" { // a rollback reuses an image that is already counted
		if err := s.checkStorageRoom(ctx, user); err != nil {
			return Deployment{}, err
		}
	}
	if err := s.checkDeployQuota(ctx, app, qs.buildKind); err != nil { // first deploy of an app, or a stopped one
		return Deployment{}, err
	}
	depID := ids.New(ids.Deployment)
	if qs.prepare != nil {
		if err := qs.prepare(depID); err != nil {
			os.Remove(s.sourceZip(depID))
			return Deployment{}, apperr.Internal(err)
		}
	}
	warnings := qs.warningsJSON
	if !warnings.Valid {
		var nonErr []deploy.Finding
		for _, f := range qs.warnings {
			if f.Severity != "error" {
				nonErr = append(nonErr, f)
			}
		}
		if len(nonErr) > 0 {
			b, _ := json.Marshal(nonErr)
			warnings = sql.NullString{String: string(b), Valid: true}
		}
	}
	var cancelled []string
	var seq int64
	now := s.now()
	err = s.Store.Tx(ctx, func(q *db.Queries) error {
		// Checked again inside the transaction: parallel deploy calls would
		// otherwise all pass the check above before any of them is stored.
		if err := s.checkDeployRate(ctx, q, user); err != nil {
			return err
		}
		if qs.inTx != nil {
			if err := qs.inTx(q); err != nil {
				return err
			}
		}
		var err error
		cancelled, err = q.CancelQueuedDeployments(ctx, db.CancelQueuedDeploymentsParams{FinishedAt: sql.NullInt64{Int64: now, Valid: true}, AppID: app.ID})
		if err != nil {
			return err
		}
		seq, err = q.NextDeploySeq(ctx, db.NextDeploySeqParams{UpdatedAt: now, ID: app.ID})
		if err != nil {
			return err
		}
		if err := q.InsertDeployment(ctx, db.InsertDeploymentParams{
			ID: depID, AppID: app.ID, Seq: seq, SourceKind: qs.kind, SourceRef: qs.ref,
			Channel: string(a.Channel), CreatedBy: nullStr(a.UserID), TokenID: nullStr(a.TokenID),
			Image: nullStr(qs.image), Port: qs.port, HasHealthcheck: b2i(qs.hasHC), Warnings: warnings, BuildKind: qs.buildKind, QueuedAt: now,
		}); err != nil {
			return err
		}
		if isMulti(app) {
			// A rollback runs the images every service of the target ran.
			if err := q.CopyDeploymentServices(ctx, db.CopyDeploymentServicesParams{NewID: depID, FromID: qs.ref}); err != nil {
				return err
			}
		} else if err := q.SyncSingleDeploymentService(ctx, depID); err != nil {
			return err
		}
		if qs.servicesSpec != "" {
			if err := q.SetDeploymentServicesSpec(ctx, db.SetDeploymentServicesSpecParams{ServicesSpec: qs.servicesSpec, ID: depID}); err != nil {
				return err
			}
		}
		if qs.catalogVersion != "" {
			if err := q.SetDeploymentCatalogVersion(ctx, db.SetDeploymentCatalogVersionParams{CatalogVersion: qs.catalogVersion, ID: depID}); err != nil {
				return err
			}
		}
		if qs.commitSHA != "" {
			return q.SetDeploymentCommit(ctx, db.SetDeploymentCommitParams{CommitSha: nullStr(qs.commitSHA), CommitMessage: nullStr(qs.commitMessage), ID: depID})
		}
		return nil
	})
	if err != nil {
		os.Remove(s.sourceZip(depID))
		var ae *apperr.Error
		if errors.As(err, &ae) {
			return Deployment{}, ae
		}
		return Deployment{}, apperr.Internal(err)
	}
	for _, id := range cancelled {
		_ = os.Remove(s.sourceZip(id))
		s.NotifyDeployment(id)
	}
	if app.Status == "new" || app.Status == "failed" {
		_ = s.Q.SetAppStatus(ctx, db.SetAppStatusParams{Status: "deploying", StatusMessage: "", UpdatedAt: now, ID: app.ID})
	}
	s.Audit(ctx, a, "deployment.queue", "deployment", depID, map[string]any{"app": app.Slug, "seq": seq, "source": qs.kind})
	s.publishDeployment(depID)
	s.Wake()
	d, err := s.Q.GetDeployment(ctx, depID)
	if err != nil {
		return Deployment{}, apperr.Internal(err)
	}
	return s.deploymentView(ctx, d, app), nil
}

func (s *Service) checkDeployRate(ctx context.Context, q *db.Queries, user db.User) error {
	since := s.now() - 3600
	n, err := q.CountUserDeploymentsSince(ctx, db.CountUserDeploymentsSinceParams{UserID: user.ID, QueuedAt: since})
	if err != nil {
		return apperr.Internal(err)
	}
	if n < user.QuotaDeploysPerHour {
		return nil
	}
	wait := int64(60)
	if oldest, err := q.OldestUserDeploymentSince(ctx, db.OldestUserDeploymentSinceParams{UserID: user.ID, QueuedAt: since}); err == nil && oldest > 0 {
		wait = oldest + 3600 - s.now()
		if wait < 1 {
			wait = 1
		}
	}
	return apperr.TooMany("deploy_rate_limited",
		fmt.Sprintf("You have already started %d deployments in the last hour, which is the limit for your account.", n),
		deployAgainIn((wait+59)/60)).
		WithNext("get_app").WithDetail("retry_after_seconds", wait)
}

// deployAgainIn is the rate limit hint, worded for people; the MCP adds its own advice for assistants.
func deployAgainIn(mins int64) string {
	if mins == 1 {
		return "You can deploy again in about 1 minute."
	}
	return fmt.Sprintf("You can deploy again in about %d minutes.", mins)
}

// ---- read side ----

// deploymentView builds the public Deployment. baseDomain is the app's
// apps.base_domain (empty for the default domain), for its URL.
func (s *Service) deploymentView(ctx context.Context, d db.Deployment, app db.App) Deployment {
	v := s.deploymentBase(d, app.Slug, app.Type, app.BaseDomain)
	if isMulti(app) || d.ServicesSpec != "" {
		if rows, err := s.Q.ListDeploymentServices(ctx, d.ID); err == nil {
			v.Services = deploymentServices(v, rows, app, d.Seq)
		}
	}
	return v
}

// deploymentServices is Deployment.services: one entry per service row, in
// the order of the rows (by name).
func deploymentServices(v Deployment, rows []db.DeploymentService, app db.App, seq int64) []DeploymentService {
	out := make([]DeploymentService, 0, len(rows))
	for _, r := range rows {
		ds := DeploymentService{Service: r.Service, Image: strPtr(r.Image), State: v.State}
		own := dockerx.ServiceContainerName(app.Slug, r.Service, seq)
		ds.Reused = r.ContainerName.Valid && r.ContainerName.String != "" && r.ContainerName.String != own &&
			r.ContainerName.String != dockerx.ContainerName(app.Slug, seq)
		if v.State == "failed" && v.Error != nil && v.Error.Service != "" {
			if v.Error.Service == r.Service {
				ds.State, ds.Error = "failed", v.Error
			} else {
				ds.State = "cancelled"
			}
		}
		out = append(out, ds)
	}
	return out
}

// deploymentBase is the Deployment without its per-service rows.
func (s *Service) deploymentBase(d db.Deployment, slug, appType, baseDomain string) Deployment {
	v := Deployment{
		ID: d.ID, AppID: d.AppID, AppSlug: slug, Seq: d.Seq, State: d.State,
		SourceKind: d.SourceKind, SourceRef: d.SourceRef, Channel: d.Channel,
		Warnings: []deploy.Finding{}, QueuedAt: ts(d.QueuedAt),
		StartedAt: tsPtr(d.StartedAt), FinishedAt: tsPtr(d.FinishedAt),
		CommitSHA: strPtr(d.CommitSha), CommitMessage: strPtr(d.CommitMessage), Image: strPtr(d.Image),
	}
	if appType != "worker" {
		v.URL = s.Cfg.AppURLOn(slug, baseDomain)
	}
	if d.StartedAt.Valid && d.FinishedAt.Valid {
		sec := float64(d.FinishedAt.Int64 - d.StartedAt.Int64)
		v.DurationSec = &sec
	}
	if d.Warnings.Valid && d.Warnings.String != "" {
		_ = json.Unmarshal([]byte(d.Warnings.String), &v.Warnings)
		if v.Warnings == nil {
			v.Warnings = []deploy.Finding{}
		}
	}
	if d.ErrorCode.Valid {
		v.Error = &DeployError{Code: d.ErrorCode.String, Message: d.ErrorMessage.String, Hint: d.ErrorHint.String, Next: d.ErrorNext.String, Service: d.ErrorService}
	}
	if d.LogTail.Valid && d.LogTail.String != "" {
		v.LogTail = strings.Split(d.LogTail.String, "\n")
	} else if d.State != "queued" && d.State != "cancelled" {
		v.LogTail = tailFile(s.Cfg.BuildLog(d.ID), maxStoredLogTail)
	}
	v.NextSteps = s.nextSteps(v, appType)
	return v
}

func (s *Service) nextSteps(d Deployment, appType string) []string {
	switch d.State {
	case "running":
		if appType == "worker" {
			return []string{"The worker is running. Call get_logs with kind=runtime to see what it prints."}
		}
		return []string{
			"Give the user the link " + d.URL + " and one or two plain sentences on what you did.",
			"If the page looks wrong, call get_logs with kind=runtime.",
		}
	case "failed":
		steps := []string{}
		if d.Error != nil && d.Error.Hint != "" {
			steps = append(steps, "Fix this: "+d.Error.Hint)
		}
		steps = append(steps,
			"Read error and log_tail, fix the problem, then call deploy_files (or deploy) again.",
			"After 3 failures with the same error code, stop and explain the problem to the user in plain words.")
		return steps
	case "superseded":
		return []string{"A newer deployment replaced this one. Call get_app to see the current one."}
	case "cancelled":
		if d.Error != nil && d.Error.Code == "cancelled" {
			return []string{"This deployment was cancelled on request. It is not a failure and nothing needs fixing. The version that was live keeps running. Call get_app to see the current state."}
		}
		return []string{"A newer deployment replaced this one before it started, or the app was stopped. Call get_app to see the current state."}
	}
	return []string{"Call get_deployment with wait_seconds=30 until the state is running or failed."}
}

func tailFile(p string, n int) []string {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil
	}
	window := int64(n) * 400
	if window < 128<<10 {
		window = 128 << 10
	}
	if window > 8<<20 {
		window = 8 << 20
	}
	off := st.Size() - window
	if off < 0 {
		off = 0
	}
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return nil
	}
	s := string(buf)
	if off > 0 { // drop the partial first line
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
	}
	return deploy.TailLines(s, n)
}

// ---- small helpers ----

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// linkOrCopy hard-links src to dst, falling back to a copy.
func linkOrCopy(src, dst string) error {
	_ = os.Remove(dst)
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}

// buildCheckInput prepares the static Dockerfile checks. allFiles are paths
// relative to the source root; read returns a file's content (nil if absent).
// The file list handed to the checks is relative to the build context.
func buildCheckInput(app db.App, allFiles []string, read func(rel string) []byte) deploy.CheckInput {
	ctxDir := path.Clean(app.ContextDir)
	var files []string
	for _, f := range allFiles {
		if ctxDir == "." {
			files = append(files, f)
		} else if rest, ok := strings.CutPrefix(f, ctxDir+"/"); ok {
			files = append(files, rest)
		}
	}
	in := deploy.CheckInput{
		Dockerfile:     read(app.DockerfilePath),
		DockerfilePath: app.DockerfilePath,
		Files:          files,
		AppType:        app.Type,
	}
	if ctxDir == "." {
		in.Dockerignore = read(".dockerignore")
	} else {
		in.Dockerignore = read(ctxDir + "/.dockerignore")
	}
	if app.Port.Valid {
		in.AppPort = int(app.Port.Int64)
	}
	return in
}

func (s *Service) runChecks(app db.App, allFiles []string, read func(rel string) []byte) (deploy.CheckResult, []string) {
	in := buildCheckInput(app, allFiles, read)
	return deploy.CheckDockerfile(in), in.Files
}

// checkError turns a failed check into the error to return, or nil when OK.
func checkError(res deploy.CheckResult, files []string) error {
	if res.OK {
		return nil
	}
	if e := deploy.FirstError(res, files); e != nil {
		return e
	}
	for _, f := range res.Findings {
		if f.Severity == "error" {
			return apperr.Unprocessable(f.Code, f.Message, f.Hint).WithNext("validate_dockerfile")
		}
	}
	return apperr.Unprocessable("dockerfile_invalid", "The Dockerfile checks failed.", "Call validate_dockerfile for details.").WithNext("validate_dockerfile")
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// UploadAndDeploy takes a zip as the request body and deploys it in one step:
// the same as create_upload, the upload and deploy, for callers that already
// have a session (the dashboard). All the usual checks apply: git-linked apps
// refuse it, a full disk, the deploys per hour and the storage and start
// quotas are checked before the body is read where they can be, and the zip
// itself is inspected as for any upload.
func (s *Service) UploadAndDeploy(ctx context.Context, a Actor, ref string, body io.Reader) (Deployment, error) {
	row, err := s.LoadApp(ctx, a, ref, true)
	if err != nil {
		return Deployment{}, err
	}
	if err := gitLinkedErr(row); err != nil {
		return Deployment{}, err
	}
	if err := s.checkDiskRoom(); err != nil {
		return Deployment{}, err
	}
	user, err := s.activeUser(ctx, row.UserID)
	if err != nil {
		return Deployment{}, err
	}
	// Refuse before a large body is read, not after.
	if err := s.checkDeployRate(ctx, s.Q, user); err != nil {
		return Deployment{}, err
	}
	if err := s.checkStorageRoom(ctx, user); err != nil {
		return Deployment{}, err
	}
	if err := s.checkDeployQuota(ctx, row, ""); err != nil {
		return Deployment{}, err
	}
	up, err := s.CreateUpload(ctx, a, ref)
	if err != nil {
		return Deployment{}, err
	}
	drop := func() {
		_ = os.Remove(s.uploadPath(up.ID))
		_ = s.Q.DeleteUpload(ctx, up.ID)
	}
	if _, err := s.ReceiveUpload(ctx, up.ID, body); err != nil {
		drop()
		return Deployment{}, err
	}
	// The same early checks as deploy_files (build mode, Dockerfile), so a
	// source that cannot build is refused here and not by a failed deployment.
	if err := s.checkUploadedSource(ctx, row, s.uploadPath(up.ID)); err != nil {
		drop()
		return Deployment{}, err
	}
	d, err := s.Deploy(ctx, a, ref, up.ID)
	if err != nil {
		drop()
	}
	return d, err
}

// checkUploadedSource runs planBuild and the Dockerfile checks on a stored zip
// without extracting it: the file list as the pipeline will see it, and the
// content of the few small files the checks read.
func (s *Service) checkUploadedSource(ctx context.Context, row db.App, zipPath string) error {
	_, err := s.checkZipSource(ctx, row, zipPath, true)
	return err
}

// checkZipSource is checkUploadedSource for any stored zip (strip: remove a
// single top folder, as for uploads). It returns the check result as well.
func (s *Service) checkZipSource(ctx context.Context, row db.App, zipPath string, strip bool) (deploy.CheckResult, error) {
	ctxDir := path.Clean(row.ContextDir)
	wanted := map[string]bool{path.Clean(row.DockerfilePath): true, "package.json": true, ".dockerignore": true, path.Join(ctxDir, ".dockerignore"): true}
	for _, n := range deploy.NextConfigNames {
		wanted[n] = true
	}
	if row.ComposePath != "" {
		wanted[path.Clean(row.ComposePath)] = true
	}
	files, contents, err := deploy.ZipSource(zipPath, strip, func(rel string) bool { return wanted[rel] || deploy.IsComposeFileName(rel) }, 1<<20)
	if err != nil {
		return deploy.CheckResult{}, err
	}
	// A compose file reads more files (each service's Dockerfile, env_file):
	// those are fetched from the zip when asked for.
	has := make(map[string]bool, len(files))
	for _, f := range files {
		has[f] = true
	}
	read := func(rel string) []byte {
		rel = path.Clean(rel)
		if b, ok := contents[rel]; ok {
			return b
		}
		if !has[rel] {
			return nil
		}
		_, more, err := deploy.ZipSource(zipPath, strip, func(r string) bool { return r == rel }, 1<<20)
		if err != nil {
			return nil
		}
		contents[rel] = more[rel]
		return more[rel]
	}
	res, ae := s.checkSource(ctx, row, files, read)
	if ae != nil {
		return res, ae
	}
	return res, nil
}

// ---- cancel ----

// cancelledMessage is the error_message of a deployment its user cancelled.
const cancelledMessage = "Cancelled by the user."

// CancelDeployment cancels a deployment of the actor's own app.
//
//   - queued: cancelled at once.
//   - fetching, validating, building, starting, health_check: the pipeline's
//     context is cancelled (the build process is stopped, the candidate container
//     removed, the build slot freed) and the call waits up to 5 s for it to settle,
//     so the answer usually says cancelled; otherwise it shows the state as it is.
//   - swapping: 409 too_late_to_cancel. Finished: 409 deployment_finished.
//
// The live version is never touched.
func (s *Service) CancelDeployment(ctx context.Context, a Actor, id string) (Deployment, error) {
	d, err := s.Q.GetDeployment(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Deployment{}, deploymentNotFound(id)
	}
	if err != nil {
		return Deployment{}, apperr.Internal(err)
	}
	app, err := s.LoadApp(ctx, a, d.AppID, true)
	if err != nil {
		var ae *apperr.Error
		if errors.As(err, &ae) && ae.Code == "app_not_found" {
			return Deployment{}, deploymentNotFound(id)
		}
		return Deployment{}, err
	}
	view := func(d db.Deployment) (Deployment, error) {
		return s.deploymentView(ctx, d, app), nil
	}
	reload := func() error {
		var err error
		if d, err = s.Q.GetDeployment(ctx, id); err != nil {
			return apperr.Internal(err)
		}
		return nil
	}
	for range 3 {
		switch d.State {
		case "queued":
			n, err := s.Q.CancelQueuedDeployment(ctx, db.CancelQueuedDeploymentParams{
				ErrorMessage: nullStr(cancelledMessage), FinishedAt: sql.NullInt64{Int64: s.now(), Valid: true}, ID: id,
			})
			if err != nil {
				return Deployment{}, apperr.Internal(err)
			}
			if n == 0 { // the worker claimed it just now: look again
				if err := reload(); err != nil {
					return Deployment{}, err
				}
				continue
			}
			_ = os.Remove(s.sourceZip(id))
			s.auditCancel(ctx, a, app.Slug, id, d.Seq, "queued")
			s.NotifyDeployment(id)
			s.settleStatusAfterCancel(ctx, app.ID)
			if err := reload(); err != nil {
				return Deployment{}, err
			}
			return view(d)
		case "fetching", "validating", "building", "starting", "health_check":
			found, tooLate := s.requestCancel(id, a)
			if tooLate {
				return Deployment{}, tooLateToCancel()
			}
			if found {
				deadline := time.Now().Add(5 * time.Second)
				for s.activeDeployment(id) && time.Now().Before(deadline) && ctx.Err() == nil {
					time.Sleep(50 * time.Millisecond)
				}
			}
			// Not found: no pipeline runs it in this process (the reconciler ends such a
			// deployment); show it as it is.
			if err := reload(); err != nil {
				return Deployment{}, err
			}
			if !found && d.State != "fetching" && d.State != "validating" && d.State != "building" && d.State != "starting" && d.State != "health_check" {
				continue
			}
			return view(d)
		case "swapping":
			return Deployment{}, tooLateToCancel()
		default:
			return Deployment{}, apperr.Conflict("deployment_finished", "This deployment is already finished ("+d.State+"), so it cannot be cancelled.",
				"Only a queued or in-progress deployment can be cancelled. Call get_app to see the current deployment.").WithNext("get_app")
		}
	}
	return view(d)
}

func tooLateToCancel() *apperr.Error {
	return apperr.Conflict("too_late_to_cancel", "This deployment is already going live, so it cannot be cancelled.", "Wait until it is running; to undo it, call rollback with an older deployment.")
}

// CancelAppDeployment cancels the queued or in-progress deployment of an app
// (the newest one when more than one is open). It answers not_found-style
// "no_deployment_to_cancel" when there is none.
func (s *Service) CancelAppDeployment(ctx context.Context, a Actor, ref string) (Deployment, error) {
	app, err := s.LoadApp(ctx, a, ref, true)
	if err != nil {
		return Deployment{}, err
	}
	var id string
	err = s.Store.DB.QueryRowContext(ctx, `SELECT id FROM deployments WHERE app_id = ?
		AND state IN ('queued','fetching','validating','building','starting','health_check') ORDER BY seq DESC LIMIT 1`, app.ID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Deployment{}, apperr.Conflict("no_deployment_to_cancel", "App "+quote(app.Slug)+" has no deployment that is queued or in progress.",
			"Call get_app to see the current deployment.").WithNext("get_app")
	}
	if err != nil {
		return Deployment{}, apperr.Internal(err)
	}
	return s.CancelDeployment(ctx, a, id)
}

func (s *Service) auditCancel(ctx context.Context, a Actor, slug, depID string, seq int64, was string) {
	s.Audit(ctx, a, "deployment.cancelled", "deployment", depID, map[string]any{"app": slug, "seq": seq, "was": was})
	s.publishDeployment(depID)
}

// settleStatusAfterCancel is settleStatusAfterFailure for a cancel: nothing
// failed. Queueing moves a new or failed app to "deploying"; when that
// deployment is cancelled the app goes back to what it was: running when a
// version is live, stopped when it is meant to be, else (never deployed)
// "new", or "failed" when an earlier deployment failed. A live app keeps its
// status, and so does an app with another deployment still to come.
func (s *Service) settleStatusAfterCancel(ctx context.Context, appID string) {
	defer s.publishApp(ctx, appID)
	app, err := s.Q.GetApp(ctx, appID)
	if err != nil || app.Status != "deploying" && app.Status != "new" {
		return
	}
	var more int
	_ = s.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM deployments WHERE app_id = ? AND state NOT IN ('running','failed','superseded','cancelled')`, appID).Scan(&more)
	if more > 0 {
		return // another deployment is queued or in progress and will settle it
	}
	set := func(status, msg string) {
		_ = s.Q.SetAppStatus(ctx, db.SetAppStatusParams{Status: status, StatusMessage: msg, UpdatedAt: s.now(), ID: appID})
	}
	switch {
	case app.CurrentDeploymentID.Valid && app.DesiredState == "running":
		set("running", "")
	case app.CurrentDeploymentID.Valid:
		set("stopped", "")
	default:
		var msg sql.NullString
		err := s.Store.DB.QueryRowContext(ctx, `SELECT error_message FROM deployments WHERE app_id = ? AND state = 'failed' ORDER BY seq DESC LIMIT 1`, appID).Scan(&msg)
		if err == nil {
			set("failed", msg.String)
		} else {
			set("new", "")
		}
	}
}
