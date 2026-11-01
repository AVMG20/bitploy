package app

import (
	"errors"
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
func planBuild(app db.App, files []string, read func(rel string) []byte) (db.App, deploy.BuildPlan, []string, func(rel string) []byte, *apperr.Error) {
	plan, perr := deploy.PlanBuild(app.BuildMode, app.DockerfilePath, files, read(".dockerignore"))
	if perr != nil {
		return app, plan, files, read, perr
	}
	if plan.Kind != deploy.BuildKindStatic {
		return app, plan, files, read, nil
	}
	eff := app
	eff.DockerfilePath = deploy.StaticDockerfilePath
	eff.ContextDir = "."
	all := append(append([]string{}, files...), deploy.StaticDockerfilePath)
	hasIgnore := false
	for _, f := range files {
		if f == ".dockerignore" {
			hasIgnore = true
		}
	}
	if !hasIgnore {
		all = append(all, ".dockerignore")
	}
	return eff, plan, all, func(rel string) []byte {
		if b, ok := plan.Inject[rel]; ok {
			return b
		}
		return read(rel)
	}, nil
}

// writeInjected adds the files of a static plan to the extracted source.
// os.Root refuses to follow a symlink out of the tree, and an existing entry of
// the same name (a .dockerignore of the user, whose content is already merged
// into the new one) is replaced, not written through.
func writeInjected(srcRoot string, inject map[string][]byte) error {
	root, err := os.OpenRoot(srcRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	for name, data := range inject {
		if err := root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		if _, err := f.Write(data); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	return nil
}
