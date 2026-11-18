package templates

import (
	"regexp"
	"strings"
)

// The presets below are what auto mode needs to build a Go, Python or Django
// project without a Dockerfile from the same templates agents get: they swap
// exact, known lines of the templates adapted with ForFiles, and report
// ok=false when a line is gone.

var (
	// goPackageDir is a package folder inside the module: plain names only.
	goPackageDir = regexp.MustCompile(`^[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)*$`)
	// pyModule is a dotted Python module name (main, app.main).
	pyModule = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*$`)
	// pyPackage is a package name from PyPI as the dependency files write it.
	pyPackage = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	// pyTarget is what a Python server loads: module:variable, or
	// module:factory() for a Flask app factory.
	pyTarget = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*:[A-Za-z_][A-Za-z0-9_]*(?:\(\))?$`)
)

// GoProgram is the go template, adapted with ForFiles, that builds the main
// package in dir (a folder of the module such as cmd/server) instead of the
// top folder. ok is false for an unsafe dir or when the build line is gone.
func GoProgram(files []string, dir string) (Template, bool) {
	tpl, _ := ForFiles("go", files)
	if dir == "" || dir == "." {
		return tpl, true
	}
	if !goPackageDir.MatchString(dir) || strings.Contains("/"+dir+"/", "/../") || strings.Count(tpl.Dockerfile, goBuildLine) != 1 {
		return tpl, false
	}
	tpl.Dockerfile = strings.Replace(tpl.Dockerfile, goBuildLine, strings.TrimSuffix(goBuildLine, ".")+"./"+dir, 1)
	tpl.Readme += "\n- The program is built from `./" + dir + "`.\n"
	return tpl, true
}

// pythonInstall is the line that installs extra packages with the installer
// the adapted template uses: uv installs into /app/.venv, the others into the
// system Python the app runs with.
func pythonInstall(files []string, pkgs []string) (string, bool) {
	for _, p := range pkgs {
		if !pyPackage.MatchString(p) {
			return "", false
		}
	}
	top := topLevel(files)
	if top["uv.lock"] && top["pyproject.toml"] {
		return "RUN uv pip install " + strings.Join(pkgs, " "), true
	}
	return "RUN pip install " + strings.Join(pkgs, " "), true
}

// withPythonServer inserts the install line of the server packages the
// project does not list before marker.
func withPythonServer(tpl *Template, files, install []string, marker string) bool {
	if len(install) == 0 {
		return true
	}
	line, ok := pythonInstall(files, install)
	if !ok || strings.Count(tpl.Dockerfile, marker) != 1 {
		return false
	}
	tpl.Dockerfile = strings.Replace(tpl.Dockerfile, marker,
		"# The start command needs "+strings.Join(install, " and ")+", which the dependency files do not list.\n"+line+"\n\n"+marker, 1)
	tpl.Readme += "\n- " + strings.Join(install, " and ") + " is not in the dependency files, so the Dockerfile installs it. Add it to them to pin its version.\n"
	return true
}

const (
	pyUserMarker  = "# Run as an unprivileged user.\n"
	pyStartMarker = "# Change this to your start command."
)

// Python servers PythonApp can start.
const (
	PyServerUvicorn  = "uvicorn"  // an ASGI app (FastAPI)
	PyServerGunicorn = "gunicorn" // a WSGI app (Flask)
)

// PythonApp is the python template, adapted with ForFiles, that starts a web
// app with server (PyServerUvicorn or PyServerGunicorn) on target
// (module:variable, or module:factory() for gunicorn), listening on 0.0.0.0
// and $PORT. install are server packages the dependency files lack; they are
// installed after them. ok is false for an unsafe target or when the
// template's lines are gone.
func PythonApp(files []string, server, target string, install []string) (Template, bool) {
	tpl, _ := ForFiles("python", files)
	if !pyTarget.MatchString(target) {
		return tpl, false
	}
	var start string
	switch server {
	case PyServerUvicorn:
		if strings.HasSuffix(target, "()") {
			return tpl, false
		}
		start = "exec uvicorn " + target + " --host 0.0.0.0 --port ${PORT:-8000} --proxy-headers --forwarded-allow-ips '*'"
	case PyServerGunicorn:
		start = "exec gunicorn --bind 0.0.0.0:${PORT:-8000} --workers 2 --forwarded-allow-ips '*' '" + target + "'"
	default:
		return tpl, false
	}
	i := strings.Index(tpl.Dockerfile, pyStartMarker)
	if i < 0 || !withPythonServer(&tpl, files, install, pyUserMarker) {
		return tpl, false
	}
	i = strings.Index(tpl.Dockerfile, pyStartMarker)
	tpl.Dockerfile = tpl.Dockerfile[:i] + "# " + server + " serves " + target + " on 0.0.0.0 and $PORT.\n" +
		`CMD ["sh", "-c", "` + start + `"]` + "\n"
	tpl.Readme += "\n- The app starts with " + server + " on `" + target + "`.\n"
	return tpl, true
}

const (
	djangoProject      = "myproject.wsgi:application"
	djangoCollect      = "RUN DJANGO_SECRET_KEY=build-only python manage.py collectstatic --noinput\n"
	djangoCollectNote  = "# Collect static files into STATIC_ROOT (served by whitenoise). The key is only used during the build.\n"
	djangoCollectBlock = djangoCollectNote + djangoCollect
	djangoReplaceNote  = `# Replace "myproject" with the folder that contains settings.py and wsgi.py.` + "\n"
	djangoUserMarker   = "# Run as an unprivileged user. The SQLite database lives in /data.\n"
)

// Django is the django template, adapted with ForFiles, for the project whose
// wsgi.py is in module (mysite, or config for config/wsgi.py). collect false
// leaves out collectstatic, which fails without STATIC_ROOT in the settings.
// install are server packages the dependency files lack. ok is false for an
// unsafe module or when the template's lines are gone.
func Django(files []string, module string, collect bool, install []string) (Template, bool) {
	tpl, _ := ForFiles("django", files)
	if !pyModule.MatchString(module) || strings.Count(tpl.Dockerfile, djangoProject) != 1 || strings.Count(tpl.Dockerfile, djangoCollectBlock) != 1 {
		return tpl, false
	}
	df := strings.Replace(tpl.Dockerfile, djangoProject, module+".wsgi:application", 1)
	df = strings.Replace(df, djangoReplaceNote, "", 1)
	if !collect {
		df = strings.Replace(df, djangoCollectBlock+"\n", "", 1)
		tpl.Readme += "\n- The settings have no `STATIC_ROOT`, so `collectstatic` was left out.\n"
	}
	tpl.Dockerfile = df
	if !withPythonServer(&tpl, files, install, djangoUserMarker) {
		return tpl, false
	}
	return tpl, true
}
