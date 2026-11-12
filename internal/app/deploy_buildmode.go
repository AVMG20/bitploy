package app

import (
	"context"
	"errors"
	"io"
	"os"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/deploy"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// planBuild applies the app's build mode to a source (SPEC "The Dockerfile
// rule"; docs/decisions.md "Build modes"). It is the one place that decides
// between "build the Dockerfile" and "serve a plain website": DeployFiles runs
// it for its early checks and the pipeline runs it on the real source.
//
// It returns the app as the build should see it (for a static site the
// Dockerfile is the injected template and the context is the source root), the
// file list including injected files, and a reader that serves them. The source
// itself is not changed here; see writeInjected.
//
// ownDockerfile (runsOwnDockerfile) is true when the app's live version was
// built from its own Dockerfile: in build mode dockerfile a source without
// one is then not served as a static site, so a push that lost its
// Dockerfile fails with dockerfile_missing instead of turning the server
// into public files at the static size.
func planBuild(app db.App, ownDockerfile bool, files []string, read func(rel string) []byte) (db.App, deploy.BuildPlan, []string, func(rel string) []byte, *apperr.Error) {
	plan, perr := deploy.PlanBuild(app.BuildMode, app.DockerfilePath, app.HomePage, app.OutputDir, files, read)
	if perr != nil {
		return app, plan, files, read, perr
	}
	if ownDockerfile && app.BuildMode == deploy.BuildModeDockerfile && deploy.IsStaticKind(plan.Kind) {
		plan = deploy.BuildPlan{Kind: deploy.BuildKindDockerfile}
	}
	if len(plan.Inject) == 0 {
		return app, plan, files, read, nil
	}
	eff := app
	eff.DockerfilePath = plan.DockerfilePath
	eff.ContextDir = "."
	all := append(append([]string{}, files...), plan.DockerfilePath)
	hasIgnore := false
	for _, f := range files {
		if f == ".dockerignore" {
			hasIgnore = true
		}
	}
	if !hasIgnore {
		all = append(all, ".dockerignore")
	}
	if plan.HomePage != "" {
		all = append(all, "index.html")
	}
	return eff, plan, all, func(rel string) []byte {
		if b, ok := plan.Inject[rel]; ok {
			return b
		}
		if rel == "index.html" && plan.HomePage != "" {
			return read(plan.HomePage)
		}
		return read(rel)
	}, nil
}

// runsOwnDockerfile: the app is in build mode dockerfile and its live version
// is a build of its own Dockerfile (not one of bitploy's static templates).
func (s *Service) runsOwnDockerfile(ctx context.Context, app db.App) bool {
	return app.BuildMode == deploy.BuildModeDockerfile && app.CurrentDeploymentID.Valid && !s.appIsStatic(ctx, app)
}

// templateFindings drops what the checks say about bitploy's own templates
// (they have no HEALTHCHECK on purpose: the HTTP check covers it), so a person
// who sent only HTML or a Node project does not read about a Dockerfile they
// never wrote.
func templateFindings(plan deploy.BuildPlan, res deploy.CheckResult) deploy.CheckResult {
	if len(plan.Inject) == 0 {
		return res
	}
	kept := res.Findings[:0:0]
	for _, f := range res.Findings {
		// The template keeps the dev dependencies only when it must (the start
		// command may need one, or the package manager cannot remove them).
		if f.Code != deploy.CodeNoHealthcheck && f.Code != deploy.CodeDevDepsInImage {
			kept = append(kept, f)
		}
	}
	res.Findings = kept
	return res
}

// writeInjected adds the files of a template plan to the extracted source.
// os.Root refuses to follow a symlink out of the tree, and an existing entry of
// the same name (a .dockerignore of the user, whose content is already merged
// into the new one) is replaced, not written through.
//
// A home page with another name is copied to index.html here, in Go, so its
// name (spaces, quotes, any character) never reaches a Dockerfile or the nginx
// config, and it keeps answering at its own path too. PlanBuild only sets
// HomePage when the source has no index.html, so nothing of the user's is
// overwritten.
func writeInjected(srcRoot string, plan deploy.BuildPlan) error {
	root, err := os.OpenRoot(srcRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	for name, data := range plan.Inject {
		if err := writeNew(root, name, func(w io.Writer) error { _, err := w.Write(data); return err }); err != nil {
			return err
		}
	}
	if plan.HomePage != "" {
		src, err := root.Open(plan.HomePage)
		if err != nil {
			return err
		}
		defer src.Close()
		return writeNew(root, "index.html", func(w io.Writer) error { _, err := io.Copy(w, src); return err })
	}
	return nil
}

func writeNew(root *os.Root, name string, fill func(io.Writer) error) error {
	if err := root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if err := fill(f); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
