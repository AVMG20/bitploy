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

// Lines of the go template that GoProgram swaps.
const (
	goStage1   = "# Stage 1: compile a static binary.\n"
	goFromAlp  = "FROM golang:1.26-alpine AS build\n"
	goWorkdir  = "WORKDIR /src\n"
	goStage2   = "# Stage 2: copy only the binary into a tiny image that runs as a non-root user.\n"
	goFromRun  = "FROM gcr.io/distroless/static-debian12:nonroot\n"
	goCopyBin  = "COPY --from=build /out/app /app\n"
	goNoCgo    = "RUN CGO_ENABLED=0 go build "
	goWithCgo  = "RUN CGO_ENABLED=1 go build "
	goFromDeb  = "FROM golang:1.26-bookworm AS build\n"
	goFromBase = "FROM gcr.io/distroless/base-debian12:nonroot\n"
)

// GoRuntimeDirs are folders at the top of a Go project that a program often
// reads at run time from the folder it starts in (html/template files, CSS).
// GoProgram copies the ones the project has next to the binary.
var GoRuntimeDirs = []string{"templates", "static", "public", "views", "assets", "migrations"}

// GoProgram is the go template, adapted with ForFiles, that builds the main
// package in dir (a folder of the module such as cmd/server, or "." for the
// top folder). It also copies GoRuntimeDirs the project has into the image,
// next to where the program starts, and gives the program a /data folder it
// can write to. cgo builds with a C compiler on Debian and runs on the
// distroless image with glibc, for modules such as github.com/mattn/go-sqlite3
// that do not work without cgo. ok is false for an unsafe dir or when the
// template's lines are gone.
func GoProgram(files []string, dir string, cgo bool) (Template, bool) {
	tpl, _ := ForFiles("go", files)
	df := tpl.Dockerfile
	for _, line := range []string{goStage1, goFromAlp, goWorkdir, goStage2, goFromRun, goCopyBin, goBuildLine} {
		if strings.Count(df, line) != 1 {
			return tpl, false
		}
	}
	if dir != "" && dir != "." {
		if !goPackageDir.MatchString(dir) || strings.Contains("/"+dir+"/", "/../") {
			return tpl, false
		}
		df = strings.Replace(df, goBuildLine, strings.TrimSuffix(goBuildLine, ".")+"./"+dir, 1)
		tpl.Readme += "\n- The program is built from `./" + dir + "`.\n"
	}
	if cgo {
		df = strings.Replace(df, goStage1, "# Stage 1: compile the binary with cgo: a module of the project needs a C compiler.\n", 1)
		df = strings.Replace(df, goFromAlp, goFromDeb, 1)
		df = strings.Replace(df, goNoCgo, goWithCgo, 1)
		df = strings.Replace(df, goStage2, "# Stage 2: copy only the binary into a small image with the C library it needs, as a non-root user.\n", 1)
		df = strings.Replace(df, goFromRun, goFromBase, 1)
		tpl.Readme += "\n- A module in `go.mod` needs cgo (like `github.com/mattn/go-sqlite3`), so the binary is built on Debian with `CGO_ENABLED=1` and runs on `distroless/base`, which has the C library. `modernc.org/sqlite` needs no cgo.\n"
	}
	// The volume takes the owner of /data from the image, and the final
	// image has no shell to create it.
	df = strings.Replace(df, goWorkdir, "RUN mkdir /data\n"+goWorkdir, 1)
	runtime := "COPY --from=build --chown=nonroot:nonroot /data /data\n"
	var dirs []string
	for _, d := range GoRuntimeDirs {
		for _, f := range files {
			if strings.HasPrefix(strings.TrimPrefix(f, "./"), d+"/") {
				dirs = append(dirs, d)
				break
			}
		}
	}
	if len(dirs) > 0 {
		runtime += "# The program reads these folders at run time, from the folder it starts in.\nWORKDIR /srv\n"
		for _, d := range dirs {
			runtime += "COPY --from=build /src/" + d + " ./" + d + "\n"
		}
		tpl.Readme += "\n- The folders " + strings.Join(dirs, ", ") + " are copied into the image next to where the program starts (`/srv`), so it can read them at run time.\n"
	}
	tpl.Dockerfile = strings.Replace(df, goCopyBin, goCopyBin+runtime, 1)
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
	pyUserLine    = "RUN useradd --system --uid 10001 --create-home app\n"
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
	if strings.Count(tpl.Dockerfile, pyStartMarker) != 1 || strings.Count(tpl.Dockerfile, pyUserMarker+pyUserLine) != 1 ||
		!withPythonServer(&tpl, files, install, pyUserMarker) {
		return tpl, false
	}
	// The volume takes the owner of /data from the image: the app user must
	// own it to write a SQLite file there.
	tpl.Dockerfile = strings.Replace(tpl.Dockerfile, pyUserMarker+pyUserLine,
		"# Run as an unprivileged user that can write to /data.\n"+strings.TrimSuffix(pyUserLine, "\n")+" \\\n && mkdir /data && chown app /data\n", 1)
	i := strings.Index(tpl.Dockerfile, pyStartMarker)
	tpl.Dockerfile = tpl.Dockerfile[:i] + "# " + server + " serves " + target + " on 0.0.0.0 and $PORT.\n" +
		`CMD ["sh", "-c", "` + start + `"]` + "\n"
	tpl.Readme += "\n- The app starts with " + server + " on `" + target + "`.\n"
	return tpl, true
}

// pyVersion is a Python version as major.minor, like 3.12.
var pyVersion = regexp.MustCompile(`^3\.[0-9]{1,2}$`)

const pyFrom = "FROM python:3.14-slim\n"

// WithPython makes a python or django template run on Python version (3.12),
// the version the project asks for (.python-version), instead of the newest:
// packages pinned for an older Python often have no wheels for the newest.
// ok is false for anything but major.minor or when the FROM line is gone.
func WithPython(tpl Template, version string) (Template, bool) {
	if !pyVersion.MatchString(version) || strings.Count(tpl.Dockerfile, pyFrom) != 1 {
		return tpl, false
	}
	tpl.Dockerfile = strings.Replace(tpl.Dockerfile, pyFrom, "FROM python:"+version+"-slim\n", 1)
	tpl.Readme += "\n- The project asks for Python " + version + ", so the image is `python:" + version + "-slim`.\n"
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

// phpChown is the line of the php template that hands the site to Apache's user.
const phpChown = "RUN chown -R www-data:www-data /var/www/html "

// PHPSite is the php template, adapted with ForFiles, with a /data folder that
// Apache's user owns, so the site can keep a SQLite file there. ok is false
// when the template's line is gone.
func PHPSite(files []string) (Template, bool) {
	tpl, _ := ForFiles("php", files)
	if strings.Count(tpl.Dockerfile, phpChown) != 1 {
		return tpl, false
	}
	tpl.Dockerfile = strings.Replace(tpl.Dockerfile, phpChown, "RUN mkdir /data \\\n && chown -R www-data:www-data /data /var/www/html ", 1)
	tpl.Readme += "\n- `/data` is made in the image and owned by `www-data`, so the site can keep its SQLite file there.\n"
	return tpl, true
}
