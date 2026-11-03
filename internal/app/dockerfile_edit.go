package app

import (
	"context"
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
	Reason   string `json:"reason"` // why it cannot be edited, else ""
}

// sourceSnapshot finds the stored source zip an edit starts from: the source of
// the current deployment, else the newest attempt (as a redeploy does). ok is
// false when no deployment has a stored zip.
func (s *Service) sourceSnapshot(ctx context.Context, row db.App) (zipPath string, strip bool, ok bool, err error) {
	deps, err := s.Q.ListAllDeploymentsByApp(ctx, row.ID) // newest first
	if err != nil {
		return "", false, false, apperr.Internal(err)
	}
	var pick *db.Deployment
	if row.CurrentDeploymentID.Valid {
		pick = sourceDeployment(deps, row.CurrentDeploymentID.String)
	}
	if pick == nil {
		for _, d := range deps {
			if d.State != "cancelled" {
				pick = sourceDeployment(deps, d.ID)
				break
			}
		}
	}
	if pick == nil || (pick.SourceKind != "files" && pick.SourceKind != "upload") {
		return "", false, false, nil
	}
	zp := s.sourceZip(pick.ID)
	if _, err := os.Stat(zp); err != nil {
		return "", false, false, nil
	}
	return zp, pick.SourceKind == "upload", true, nil
}

func isGitLinked(row db.App) bool { return row.GitRepoUrl.Valid && row.GitRepoUrl.String != "" }

// GetAppDockerfile returns the Dockerfile in the app's stored source snapshot.
func (s *Service) GetAppDockerfile(ctx context.Context, a Actor, ref string) (DockerfileView, error) {
	row, err := s.LoadApp(ctx, a, ref, false)
	if err != nil {
		return DockerfileView{}, err
	}
	p := path.Clean(row.DockerfilePath)
	v := DockerfileView{Path: p}
	if isGitLinked(row) {
		v.Reason = "This app builds from GitHub. Change the Dockerfile in the repository."
		return v, nil
	}
	zp, strip, ok, err := s.sourceSnapshot(ctx, row)
	if err != nil {
		return v, err
	}
	if !ok {
		v.Reason = "Nothing has been deployed from files yet, so there is no Dockerfile to edit. Deploy once first."
		return v, nil
	}
	_, contents, err := deploy.ZipSource(zp, strip, func(rel string) bool { return rel == p }, maxReadDockerfile)
	if err != nil {
		return v, err
	}
	b, found := contents[p]
	if !found {
		v.Reason = "The deployed files have no Dockerfile at " + p + ". The app may be built from a template, such as a static site."
		return v, nil
	}
	v.Content, v.Editable = string(b), true
	return v, nil
}

// PutAppDockerfile deploys the current source snapshot with its Dockerfile
// replaced (or added): the same checks as DeployFiles, queued as kind files.
func (s *Service) PutAppDockerfile(ctx context.Context, a Actor, ref, content string) (Deployment, error) {
	row, err := s.LoadApp(ctx, a, ref, true)
	if err != nil {
		return Deployment{}, err
	}
	if err := gitLinkedErr(row); err != nil {
		return Deployment{}, err
	}
	if strings.TrimSpace(content) == "" {
		return Deployment{}, apperr.BadRequest("dockerfile_empty", "The Dockerfile is empty.",
			"Write at least a FROM line, for example `FROM ghcr.io/owner/image:1.2`.")
	}
	if len(content) > MaxDockerfileBytes {
		return Deployment{}, apperr.New(http.StatusRequestEntityTooLarge, "dockerfile_too_large",
			"The Dockerfile is larger than 64 KB.", "Keep the Dockerfile short; put big files in the project and COPY them.")
	}
	d, err := s.deployWithDockerfile(ctx, a, row, content)
	if err == nil {
		s.Audit(ctx, a, "dockerfile.update", "app", row.ID, map[string]any{"app": row.Slug, "deployment": d.ID, "bytes": len(content)})
	}
	return d, err
}

// deployWithDockerfile is the shared part of PutAppDockerfile and
// UpdateCatalogApp: build a new snapshot, check it, queue it.
func (s *Service) deployWithDockerfile(ctx context.Context, a Actor, row db.App, content string) (Deployment, error) {
	zp, strip, ok, err := s.sourceSnapshot(ctx, row)
	if err != nil {
		return Deployment{}, err
	}
	if !ok {
		return Deployment{}, apperr.Conflict("source_missing", "There is no stored source to start from, so the Dockerfile cannot be replaced.",
			"Deploy the app once with deploy_files (all files, with the Dockerfile), then edit it.").WithNext("deploy_files")
	}
	if err := os.MkdirAll(s.sourcesDir(), 0o750); err != nil {
		return Deployment{}, apperr.Internal(err)
	}
	tmp := s.sourceZip("tmp-" + randHex(8))
	defer os.Remove(tmp)
	if err := deploy.RewriteZip(zp, tmp, strip, path.Clean(row.DockerfilePath), []byte(content)); err != nil {
		return Deployment{}, apperr.As(err)
	}
	// The copy is a files source: laid out without the top folder.
	res, err := s.checkZipSource(row, tmp, false)
	if err != nil {
		return Deployment{}, err
	}
	return s.queueDeployment(ctx, a, row, queueSpec{
		kind:     "files",
		port:     portOf(res.Port),
		hasHC:    res.HasHealthcheck,
		warnings: res.Findings,
		prepare:  func(depID string) error { return linkOrCopy(tmp, s.sourceZip(depID)) },
	})
}
