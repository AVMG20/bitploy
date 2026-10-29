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
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/deploy"
	"github.com/AVMG20/bitploy/internal/ids"
	"github.com/AVMG20/bitploy/internal/store/db"
)

const (
	uploadTTL        = 15 * time.Minute
	uploadDeployTTL  = 24 * time.Hour // a received upload can be deployed for this long
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
	row, err := s.LoadApp(ctx, a, ref, true)
	if err != nil {
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
	res, ctxFiles := s.runChecks(row, paths, func(p string) []byte { return byPath[p] })
	if cerr := checkError(res, ctxFiles); cerr != nil {
		return Deployment{}, cerr
	}
	return s.queueDeployment(ctx, a, row, queueSpec{
		kind:     "files",
		port:     portOf(res.Port),
		hasHC:    res.HasHealthcheck,
		warnings: res.Findings,
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
	if _, err := s.activeUser(ctx, row.UserID); err != nil {
		return Upload{}, err
	}
	id := ids.New(ids.Upload)
	now := s.Now()
	exp := now.Add(uploadTTL)
	if err := s.Q.InsertUpload(ctx, db.InsertUploadParams{
		ID: id, AppID: row.ID, UserID: row.UserID, ExpiresAt: exp.Unix(), CreatedAt: now.Unix(),
	}); err != nil {
		return Upload{}, apperr.Internal(err)
	}
	url := s.Cfg.PublicURL + "/u/" + id
	s.Audit(ctx, a, "upload.create", "app", row.ID, map[string]any{"upload_id": id})
	return Upload{
		ID: id, URL: url,
		Curl:      "curl --fail --data-binary @app.zip -H 'Content-Type: application/zip' " + url,
		ExpiresAt: exp.UTC(), MaxBytes: deploy.MaxZipCompressed,
		NextSteps: []string{
			"Zip the project folder as app.zip (with the Dockerfile inside), then run the curl command within 15 minutes.",
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
			return Deployment{}, apperr.Conflict("upload_used", "This upload was already deployed.", "Call create_upload for a new upload, or call redeploy to rebuild the last one.").WithNext("create_upload")
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

func noSourceErr() *apperr.Error {
	return apperr.BadRequest("no_source", "This app has no source to deploy yet.",
		"Send the files with deploy_files, upload a zip (create_upload, then deploy with upload_id), or set a git repository with set_git_source.").WithNext("deploy_files")
}

// Redeploy queues a deployment that rebuilds the current deployment's source
// (git: latest commit; files/upload: the stored snapshot if still present).
func (s *Service) Redeploy(ctx context.Context, a Actor, ref string) (Deployment, error) {
	row, err := s.LoadApp(ctx, a, ref, true)
	if err != nil {
		return Deployment{}, err
	}
	deps, err := s.Q.ListAllDeploymentsByApp(ctx, row.ID)
	if err != nil {
		return Deployment{}, apperr.Internal(err)
	}
	var order []db.Deployment
	for _, d := range deps { // current deployment first, then newest first
		if row.CurrentDeploymentID.Valid && d.ID == row.CurrentDeploymentID.String {
			order = append([]db.Deployment{d}, order...)
		} else {
			order = append(order, d)
		}
	}
	for _, d := range order {
		switch d.SourceKind {
		case "git":
			if row.GitRepoUrl.Valid && row.GitRepoUrl.String != "" {
				return s.queueDeployment(ctx, a, row, queueSpec{kind: "git", ref: row.GitBranch.String})
			}
		case "files", "upload":
			if d.State == "cancelled" {
				continue
			}
			if _, err := os.Stat(s.sourceZip(d.ID)); err == nil {
				src := s.sourceZip(d.ID)
				return s.queueDeployment(ctx, a, row, queueSpec{
					kind: d.SourceKind, ref: d.SourceRef,
					prepare: func(depID string) error { return linkOrCopy(src, s.sourceZip(depID)) },
				})
			}
		}
	}
	if row.GitRepoUrl.Valid && row.GitRepoUrl.String != "" {
		return s.queueDeployment(ctx, a, row, queueSpec{kind: "git", ref: row.GitBranch.String})
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
		v := s.deploymentView(d, app.Slug, app.Type)
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
		page.Items = append(page.Items, s.deploymentView(d, row.Slug, row.Type))
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
	}
	return s.queueDeployment(ctx, a, row, queueSpec{
		kind: "rollback", ref: deploymentID, image: target.Image.String,
		port: target.Port, hasHC: target.HasHealthcheck != 0, warningsJSON: target.Warnings,
	})
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
	prepare      func(depID string) error // writes the source snapshot before the row exists
	inTx         func(q *db.Queries) error
}

func portOf(p int) sql.NullInt64 {
	if p <= 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(p), Valid: true}
}

func (s *Service) queueDeployment(ctx context.Context, a Actor, app db.App, qs queueSpec) (Deployment, error) {
	user, err := s.activeUser(ctx, app.UserID)
	if err != nil {
		return Deployment{}, err
	}
	if err := s.checkDeployRate(ctx, user); err != nil {
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
		return q.InsertDeployment(ctx, db.InsertDeploymentParams{
			ID: depID, AppID: app.ID, Seq: seq, SourceKind: qs.kind, SourceRef: qs.ref,
			Channel: string(a.Channel), CreatedBy: nullStr(a.UserID), TokenID: nullStr(a.TokenID),
			Image: nullStr(qs.image), Port: qs.port, HasHealthcheck: b2i(qs.hasHC), Warnings: warnings, QueuedAt: now,
		})
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
	s.Wake()
	d, err := s.Q.GetDeployment(ctx, depID)
	if err != nil {
		return Deployment{}, apperr.Internal(err)
	}
	return s.deploymentView(d, app.Slug, app.Type), nil
}

func (s *Service) checkDeployRate(ctx context.Context, user db.User) error {
	since := s.now() - 3600
	n, err := s.Q.CountUserDeploymentsSince(ctx, db.CountUserDeploymentsSinceParams{UserID: user.ID, QueuedAt: since})
	if err != nil {
		return apperr.Internal(err)
	}
	if n < user.QuotaDeploysPerHour {
		return nil
	}
	wait := int64(60)
	if oldest, err := s.Q.OldestUserDeploymentSince(ctx, db.OldestUserDeploymentSinceParams{UserID: user.ID, QueuedAt: since}); err == nil && oldest > 0 {
		wait = oldest + 3600 - s.now()
		if wait < 1 {
			wait = 1
		}
	}
	return apperr.TooMany("deploy_rate_limited",
		fmt.Sprintf("You have already started %d deployments in the last hour, which is the limit for your account.", n),
		fmt.Sprintf("Wait about %d minutes before deploying again. Fix problems locally first instead of deploying every small change.", (wait+59)/60)).
		WithDetail("retry_after_seconds", wait)
}

// ---- read side ----

func (s *Service) deploymentView(d db.Deployment, slug, appType string) Deployment {
	v := Deployment{
		ID: d.ID, AppID: d.AppID, AppSlug: slug, Seq: d.Seq, State: d.State,
		SourceKind: d.SourceKind, SourceRef: d.SourceRef, Channel: d.Channel,
		Warnings: []deploy.Finding{}, QueuedAt: ts(d.QueuedAt),
		StartedAt: tsPtr(d.StartedAt), FinishedAt: tsPtr(d.FinishedAt),
		CommitSHA: strPtr(d.CommitSha), CommitMessage: strPtr(d.CommitMessage), Image: strPtr(d.Image),
	}
	if appType != "worker" {
		v.URL = s.Cfg.AppURL(slug)
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
		v.Error = &DeployError{Code: d.ErrorCode.String, Message: d.ErrorMessage.String, Hint: d.ErrorHint.String, Next: d.ErrorNext.String}
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
