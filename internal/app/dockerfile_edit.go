package app

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/deploy"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// MaxDockerfileBytes is the size limit of a Dockerfile saved from the dashboard.
const MaxDockerfileBytes = 64 << 10

// maxReadDockerfile is how much of a stored Dockerfile is read for the editor.
const maxReadDockerfile = 1 << 20

// DockerfileView is the Dockerfile of an app as the editor shows it.
type DockerfileView struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Editable bool   `json:"editable"`
	Reason   string `json:"reason" doc:"Why the Dockerfile cannot be edited, else empty"`
}

// snapshot is the stored source an edit starts from.
type snapshot struct {
	zip   string
	strip bool           // an upload: a single top folder is stripped
	dep   *db.Deployment // the deployment whose source it is
}

// sourceSnapshot finds the stored source zip an edit starts from: the newest
// attempt that was not cancelled, as "deploy with only app=" (RetryLatest)
// does, so a Dockerfile that just failed can be fixed and the editor shows
// what was saved last. ok is false when that deployment has no stored zip
// (a git source, or the zip was pruned).
func (s *Service) sourceSnapshot(ctx context.Context, row db.App) (snap snapshot, ok bool, err error) {
	deps, err := s.Q.ListAllDeploymentsByApp(ctx, row.ID) // newest first
	if err != nil {
		return snap, false, apperr.Internal(err)
	}
	var pick *db.Deployment
	for _, d := range deps {
		if d.State != "cancelled" {
			pick = sourceDeployment(deps, d.ID)
			break
		}
	}
	if pick == nil || (pick.SourceKind != "files" && pick.SourceKind != "upload") {
		return snap, false, nil
	}
	zp := s.sourceZip(pick.ID)
	if _, err := os.Stat(zp); err != nil {
		return snap, false, nil
	}
	return snapshot{zip: zp, strip: pick.SourceKind == "upload", dep: pick}, true, nil
}

func isGitLinked(row db.App) bool { return row.GitRepoUrl.Valid && row.GitRepoUrl.String != "" }

// templateModeReason says why the app's own Dockerfile is not used in its
// build mode, or "" when it is.
func templateModeReason(row db.App) string {
	switch row.BuildMode {
	case deploy.BuildModeStatic:
		return "This app is set to be a static site, so bitploy builds it from its own template and does not use a Dockerfile. Change the type to Dockerfile to use one."
	case deploy.BuildModeNode, deploy.BuildModeStaticBuild:
		return "This app is built with bitploy's Node.js template and does not use a Dockerfile. Change the type to Dockerfile to use one."
	}
	return ""
}

// GetAppDockerfile returns the Dockerfile in the app's stored source snapshot.
func (s *Service) GetAppDockerfile(ctx context.Context, a Actor, ref string) (DockerfileView, error) {
	row, err := s.LoadApp(ctx, a, ref, false)
	if err != nil {
		return DockerfileView{}, err
	}
	p := path.Clean(row.DockerfilePath)
	v := DockerfileView{Path: p}
	if isGitLinked(row) {
		v.Reason = "This app builds from GitHub. Change the Dockerfile in the repository and push."
		return v, nil
	}
	if r := templateModeReason(row); r != "" {
		v.Reason = r
		return v, nil
	}
	snap, ok, err := s.sourceSnapshot(ctx, row)
	if err != nil {
		return v, err
	}
	if !ok {
		v.Reason = "Nothing has been deployed from files yet, so there is no Dockerfile to edit. Deploy once first."
		return v, nil
	}
	_, contents, err := deploy.ZipSource(snap.zip, snap.strip, func(rel string) bool { return rel == p }, maxReadDockerfile)
	if err != nil {
		return v, err
	}
	b, found := contents[p]
	switch {
	case found:
		v.Content, v.Editable = string(b), true
	case row.BuildMode == deploy.BuildModeAuto:
		// Without a Dockerfile, auto builds a static site or a Node.js app from
		// bitploy's template: there is no file of the user's to show.
		v.Reason = "The deployed files have no " + p + ", so bitploy builds them from its own template. Add a Dockerfile to the files to build your own."
	default:
		v.Reason = "The deployed files have no Dockerfile at " + p + ". Deploy the files again with a Dockerfile, or change the Dockerfile path."
	}
	return v, nil
}

// PutAppDockerfile deploys the newest source snapshot with its Dockerfile
// replaced: the same checks as DeployFiles, queued as kind files.
func (s *Service) PutAppDockerfile(ctx context.Context, a Actor, ref, content string) (Deployment, error) {
	row, err := s.LoadApp(ctx, a, ref, true)
	if err != nil {
		return Deployment{}, err
	}
	if err := gitLinkedErr(row); err != nil {
		return Deployment{}, err
	}
	if r := templateModeReason(row); r != "" {
		return Deployment{}, apperr.Conflict("dockerfile_not_used", r, "Set the type (build_mode) to dockerfile or auto with update_app, then save the Dockerfile again.").WithNext("update_app")
	}
	if strings.TrimSpace(content) == "" {
		return Deployment{}, apperr.BadRequest("dockerfile_empty", "The Dockerfile is empty.",
			"Write at least a FROM line, for example `FROM ghcr.io/owner/image:1.2`.")
	}
	if len(content) > MaxDockerfileBytes {
		return Deployment{}, apperr.New(http.StatusRequestEntityTooLarge, "dockerfile_too_large",
			"The Dockerfile is larger than 64 KB.", "Make it shorter: put large scripts or config in files of the project and COPY them.")
	}
	snap, ok, err := s.sourceSnapshot(ctx, row)
	if err != nil {
		return Deployment{}, err
	}
	if !ok {
		return Deployment{}, sourceMissingErr()
	}
	d, err := s.deployWithDockerfile(ctx, a, row, snap, content, snap.dep.CatalogVersion)
	if err != nil {
		return Deployment{}, err
	}
	s.Audit(ctx, a, "dockerfile.update", "app", row.ID, map[string]any{"app": row.Slug, "deployment": d.ID, "bytes": len(content)})
	return d, nil
}

func sourceMissingErr() *apperr.Error {
	return apperr.Conflict("source_missing", "There is no stored source to start from, so the Dockerfile cannot be replaced.",
		"Deploy the app once with deploy_files (all files, with the Dockerfile), then edit it.").WithNext("deploy_files")
}

// deployWithDockerfile is the shared part of PutAppDockerfile and
// UpdateCatalogApp: copy the snapshot with the Dockerfile replaced, check it,
// queue it as a files deployment.
func (s *Service) deployWithDockerfile(ctx context.Context, a Actor, row db.App, snap snapshot, content, catalogVersion string) (Deployment, error) {
	if err := os.MkdirAll(s.sourcesDir(), 0o750); err != nil {
		return Deployment{}, apperr.Internal(err)
	}
	tmp := s.sourceZip("tmp-" + randHex(8))
	defer os.Remove(tmp)
	if err := deploy.RewriteZip(snap.zip, tmp, snap.strip, path.Clean(row.DockerfilePath), []byte(content)); err != nil {
		if errors.Is(err, fs.ErrNotExist) { // a deployment queued meanwhile cancelled the snapshot's deployment
			return Deployment{}, apperr.Conflict("source_changed", "The stored source changed while the Dockerfile was being saved.", "Save it again.")
		}
		return Deployment{}, apperr.As(err)
	}
	// The copy is a files source: laid out without the top folder.
	res, err := s.checkZipSource(ctx, row, tmp, false)
	if err != nil {
		return Deployment{}, err
	}
	return s.queueDeployment(ctx, a, row, queueSpec{
		kind:           "files",
		port:           portOf(res.Port),
		hasHC:          res.HasHealthcheck,
		warnings:       res.Findings,
		catalogVersion: catalogVersion,
		prepare:        func(depID string) error { return linkOrCopy(tmp, s.sourceZip(depID)) },
	})
}
