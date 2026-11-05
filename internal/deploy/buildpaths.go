package deploy

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/AVMG20/bitploy/internal/apperr"
)

// ResolveBuildPaths joins the build context folder and the Dockerfile path onto
// srcRoot, resolves every symlink on disk (filepath.EvalSymlinks) and returns
// the resolved absolute paths. It fails with a source_* error unless both the
// context and the Dockerfile stay inside the resolved source root, whatever
// the source kind. The build must use the returned paths, not the inputs.
func ResolveBuildPaths(srcRoot, contextDir, dockerfilePath string) (ctxDir, dockerfile string, err *apperr.Error) {
	rootReal, rerr := filepath.EvalSymlinks(srcRoot)
	if rerr != nil {
		return "", "", apperr.Internal(rerr)
	}
	inside := func(p string) bool {
		return p == rootReal || strings.HasPrefix(p, rootReal+string(filepath.Separator))
	}
	escape := apperr.Unprocessable("source_path_escapes",
		"The build context or the Dockerfile points outside your code.",
		"Use update_app to set context_dir and dockerfile_path to folders and files inside your code, and remove links that lead elsewhere.").WithNext("update_app")

	ctxDir, rerr = filepath.EvalSymlinks(filepath.Join(srcRoot, filepath.FromSlash(contextDir)))
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return "", "", apperr.Unprocessable("source_context_not_found",
				"The build context folder does not exist in your code.",
				"Use update_app to set context_dir to a folder that exists in your code (or \".\" for the top folder).").WithNext("update_app")
		}
		return "", "", escape
	}
	if !inside(ctxDir) {
		return "", "", escape
	}
	if info, serr := os.Stat(ctxDir); serr != nil || !info.IsDir() {
		return "", "", apperr.Unprocessable("source_context_not_found",
			"The build context is not a folder.",
			"Use update_app to set context_dir to a folder in your code.").WithNext("update_app")
	}
	dockerfile, rerr = filepath.EvalSymlinks(filepath.Join(srcRoot, filepath.FromSlash(dockerfilePath)))
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return "", "", apperr.Unprocessable("source_dockerfile_not_found",
				"The Dockerfile does not exist in your code.",
				"Add the Dockerfile, or use update_app to set dockerfile_path to where it is.").WithNext("update_app")
		}
		return "", "", escape
	}
	if !inside(dockerfile) {
		return "", "", escape
	}
	if info, serr := os.Stat(dockerfile); serr != nil || !info.Mode().IsRegular() {
		return "", "", escape
	}
	return ctxDir, dockerfile, nil
}
