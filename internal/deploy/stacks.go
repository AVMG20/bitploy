package deploy

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/templates"
)

// Kinds of the template builds of auto mode for the stacks DetectStack knows
// besides Node: the same templates an AI assistant gets from get_template,
// adapted to the project.
const (
	BuildKindGo      = "go"
	BuildKindPython  = "python"
	BuildKindDjango  = "django"
	BuildKindPHP     = "php"
	BuildKindLaravel = "laravel"
)

// stackDockerfilePath is where the template of a stack is written in the
// source (see StaticDockerfilePath).
func stackDockerfilePath(kind string) string { return "Dockerfile.bitploy-" + kind }

// assistantTail ends the explanation of a plan that is a guess.
const assistantTail = " If it does not start, add a Dockerfile: your AI assistant can write one."

// stackPlan is auto mode's plan for a Go, Python, Django, PHP or Laravel
// project without a Dockerfile. ok is false for any other stack. When the
// stack is known but bitploy cannot tell how to start it, err says what is
// missing.
func stackPlan(stack string, files []string, has map[string]bool, userIgnore []byte, read func(string) []byte) (plan BuildPlan, err *apperr.Error, ok bool) {
	switch stack {
	case "go":
		plan, err = goPlan(files, has, userIgnore, read)
	case "python":
		plan, err = pythonPlan(files, has, userIgnore, read)
	case "django":
		plan, err = djangoPlan(files, has, userIgnore, read)
	case "php":
		plan, err = phpPlan(files, has, userIgnore)
	case "laravel":
		plan, err = laravelPlan(files, has, userIgnore, read)
	default:
		return BuildPlan{}, nil, false
	}
	return plan, err, true
}

// stackErr is the plain-words error for a known stack that cannot be built
// by itself. The details carry the adapted template, so an AI assistant can
// write the Dockerfile without asking.
func stackErr(stack string, files []string, msg, hint string) *apperr.Error {
	name := templateStackFor(stack)
	tpl, _ := templates.ForFiles(name, files)
	e := apperr.Unprocessable(CodeNothingToBuild, msg, hint)
	e.WithDetail("stack", stack)
	e.WithDetail("template", name)
	e.WithDetail("dockerfile", tpl.Dockerfile)
	e.WithDetail("dockerignore", tpl.Dockerignore)
	e.WithNext("get_template")
	return e
}

// cannotAdapt is the error for a template whose lines changed: a bug of
// bitploy, not of the code, but the person can still deploy with a Dockerfile.
func cannotAdapt(stack string, files []string, what string) *apperr.Error {
	return stackErr(stack, files, "bitploy cannot set up this code as "+what+" by itself.",
		"Add a Dockerfile at the top of your code. Your AI assistant can write one.")
}

// ---- Go ----

var (
	goPackageMain = regexp.MustCompile(`(?m)^package\s+main\b`)
	// goIgnored matches the build constraint of a file that go build leaves
	// out, like a code generator of package main in a library.
	goIgnored = regexp.MustCompile(`(?m)^//\s*(?:go:build|\+build)\s+ignore\s*$`)
	// goCgoModule matches a module in go.mod that only works with cgo.
	goCgoModule = regexp.MustCompile(`(?m)^\s*(?:require\s+)?(?:github\.com/mattn/go-sqlite3|crawshaw\.io/sqlite|github\.com/mutecomm/go-sqlcipher(?:/v[0-9]+)?)\s+v`)
)

// maxPlanRead is how much of a code file the planner looks at.
const maxPlanRead = 1 << 20

// readText returns the start of a text file (up to maxPlanRead bytes) as
// UTF-8: a file saved as UTF-16 with a byte order mark, like requirements.txt
// from pip freeze in Windows PowerShell, is converted.
func readText(read func(string) []byte, name string) string {
	b := read(name)
	switch {
	case len(b) >= 2 && (b[0] == 0xFF && b[1] == 0xFE || b[0] == 0xFE && b[1] == 0xFF):
		le := b[0] == 0xFF
		b = b[2:]
		if len(b) > 2*maxPlanRead {
			b = b[:2*maxPlanRead]
		}
		u := make([]uint16, len(b)/2)
		for i := range u {
			if le {
				u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
			} else {
				u[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
			}
		}
		return string(utf16.Decode(u))
	case len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF:
		b = b[3:]
	}
	if len(b) > maxPlanRead {
		b = b[:maxPlanRead]
	}
	return string(b)
}

// maxPlanFiles is how many files the planner reads to find where an app starts.
const maxPlanFiles = 64

// goCmdName is a folder name in cmd that can go into the build line.
var goCmdName = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// goMainDir finds the main package to build: the top folder when it has
// main.go or another .go file of package main, else the one cmd/<name>/main.go.
// cmds lists the programs in cmd when there are several.
func goMainDir(files []string, has map[string]bool, read func(string) []byte) (dir string, cmds []string, ok bool) {
	if has["main.go"] {
		return ".", nil, true
	}
	var top []string
	seen := map[string]bool{}
	for f := range has {
		if !strings.Contains(f, "/") && strings.HasSuffix(f, ".go") && !strings.HasSuffix(f, "_test.go") {
			top = append(top, f)
		}
		if parts := strings.Split(f, "/"); len(parts) == 3 && parts[0] == "cmd" && parts[2] == "main.go" && !seen[parts[1]] && goCmdName.MatchString(parts[1]) {
			seen[parts[1]] = true
			cmds = append(cmds, parts[1])
		}
	}
	sort.Strings(top)
	if len(top) > maxPlanFiles {
		top = top[:maxPlanFiles]
	}
	for _, f := range top {
		if src := readText(read, f); goPackageMain.MatchString(src) && !goIgnored.MatchString(src) {
			return ".", nil, true
		}
	}
	sort.Strings(cmds)
	if len(cmds) == 1 {
		return "cmd/" + cmds[0], nil, true
	}
	return "", cmds, false
}

func goPlan(files []string, has map[string]bool, userIgnore []byte, read func(string) []byte) (BuildPlan, *apperr.Error) {
	dir, cmds, ok := goMainDir(files, has, read)
	if !ok {
		if len(cmds) > 1 {
			return BuildPlan{}, stackErr("go", files,
				fmt.Sprintf("Found a Go project with several programs in cmd (%s), so it is not clear which one is the web app.", strings.Join(cmds, ", ")),
				"Add a Dockerfile that builds the right one (your AI assistant can write it), or keep only that program in cmd.")
		}
		return BuildPlan{}, stackErr("go", files,
			"Found a Go project, but no program to build (like main.go at the top, or cmd/server/main.go).",
			"Put the main package in main.go at the top or in cmd/<name>/main.go, or add a Dockerfile (your AI assistant can write one).")
	}
	cgo := goCgoModule.MatchString(readText(read, "go.mod"))
	tpl, ok := templates.GoProgram(files, dir, cgo)
	if !ok {
		return BuildPlan{}, cannotAdapt("go", files, "a Go app")
	}
	why := "Go app, built from the top folder."
	if dir != "." {
		why = "Go app, built from " + dir + "."
	}
	if cgo {
		why += " It uses SQLite through cgo, so it is built with a C compiler."
	}
	return templatePlan(BuildKindGo, stackDockerfilePath(BuildKindGo), tpl, userIgnore, why), nil
}

// ---- Python ----

var (
	pyReqName   = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*)\s*(\[[^\]]*\])?`)
	pyQuoted    = regexp.MustCompile(`["']([A-Za-z0-9][A-Za-z0-9._-]*)\s*(\[[^\]]*\])?\s*(?:[<>=!~;@,]|["'\s])`)
	pyTOMLKey   = regexp.MustCompile(`(?m)^\s*([A-Za-z0-9][A-Za-z0-9._-]*)\s*=`)
	pySettings  = regexp.MustCompile(`DJANGO_SETTINGS_MODULE["']\s*,\s*["']([A-Za-z_][A-Za-z0-9_.]*)["']`)
	pyIdent     = `[A-Za-z_][A-Za-z0-9_]*`
	pyIdentRe   = regexp.MustCompile(`^` + pyIdent + `$`)
	fastAPIApp  = regexp.MustCompile(`(?m)^(` + pyIdent + `)\s*(?::\s*[A-Za-z_][A-Za-z0-9_.]*\s*)?=\s*(?:fastapi\.)?FastAPI\(`)
	flaskApp    = regexp.MustCompile(`(?m)^(` + pyIdent + `)\s*(?::\s*[A-Za-z_][A-Za-z0-9_.]*\s*)?=\s*(?:flask\.)?Flask\(`)
	flaskCreate = regexp.MustCompile(`(?m)^def\s+create_app\s*\(`)
)

func normPyName(n string) string {
	return strings.ReplaceAll(strings.ToLower(n), "_", "-")
}

// pythonDeps lists the packages named in requirements.txt (and the files it
// includes with -r) and pyproject.toml, in lower case with - for _.
// fastapi[standard] brings uvicorn with it.
func pythonDeps(read func(string) []byte) map[string]bool {
	deps := map[string]bool{}
	add := func(name, extras string) {
		n := normPyName(name)
		deps[n] = true
		if n == "fastapi" && (strings.Contains(extras, "standard") || strings.Contains(extras, "all")) {
			deps["uvicorn"] = true
		}
	}
	reqs := []string{"requirements.txt"}
	for i := 0; i < len(reqs) && i < 8; i++ {
		for _, line := range strings.Split(readText(read, reqs[i]), "\n") {
			line, _, _ = strings.Cut(line, "#")
			line = strings.TrimSpace(line)
			if inc, ok := pyInclude(line); ok {
				reqs = append(reqs, path.Join(path.Dir(reqs[i]), inc))
				continue
			}
			if line == "" || strings.HasPrefix(line, "-") {
				continue
			}
			if m := pyReqName.FindStringSubmatch(line); m != nil {
				add(m[1], m[2])
			}
		}
	}
	toml := readText(read, "pyproject.toml")
	for _, m := range pyQuoted.FindAllStringSubmatch(toml, -1) {
		add(m[1], m[2])
	}
	for _, m := range pyTOMLKey.FindAllStringSubmatch(toml, -1) {
		add(m[1], "")
	}
	return deps
}

// pyInclude is the file a requirements line includes (-r base.txt), when it
// is a plain relative path.
func pyInclude(line string) (string, bool) {
	var rest string
	switch {
	case strings.HasPrefix(line, "-r"):
		rest = strings.TrimPrefix(line, "-r")
	case strings.HasPrefix(line, "--requirement"):
		rest = strings.TrimPrefix(line, "--requirement")
	default:
		return "", false
	}
	rest = strings.TrimSpace(strings.TrimPrefix(rest, "="))
	if rest == "" || strings.HasPrefix(rest, "/") || strings.Contains(rest, "://") {
		return "", false
	}
	for _, part := range strings.Split(rest, "/") {
		if part == ".." {
			return "", false
		}
	}
	return rest, true
}

// pythonEntryCandidates lists the files that may start a Python web app, the
// usual names first, then the other files at the top, then the app package,
// then the other packages at the top (flaskr/__init__.py with create_app).
func pythonEntryCandidates(has map[string]bool) []string {
	first := []string{"main.py", "app.py", "server.py", "api.py", "application.py", "wsgi.py", "asgi.py", "run.py"}
	var out, rest, pkgs []string
	seen := map[string]bool{}
	for _, f := range first {
		if has[f] {
			out = append(out, f)
			seen[f] = true
		}
	}
	for f := range has {
		parts := strings.Split(f, "/")
		switch {
		case len(parts) == 1 && strings.HasSuffix(f, ".py") && !seen[f]:
			rest = append(rest, f)
		case len(parts) == 2 && parts[0] != "app" && parts[0] != "tests" && (parts[1] == "__init__.py" || parts[1] == "main.py" || parts[1] == "app.py"):
			pkgs = append(pkgs, f)
		}
	}
	sort.Strings(rest)
	out = append(out, rest...)
	for _, f := range []string{"app/main.py", "app/app.py", "app/__init__.py"} {
		if has[f] {
			out = append(out, f)
		}
	}
	sort.Strings(pkgs)
	out = append(out, pkgs...)
	if len(out) > maxPlanFiles {
		out = out[:maxPlanFiles]
	}
	return out
}

var pyVersionLine = regexp.MustCompile(`^(?:python-)?(3\.[0-9]{1,2})(?:\.[0-9]+)?$`)

// pythonVersion is the Python version, as major.minor, that the project asks
// for in .python-version (uv, pyenv) or runtime.txt (python-3.11.4); "" for
// none.
func pythonVersion(read func(string) []byte) string {
	for _, name := range []string{".python-version", "runtime.txt"} {
		for _, line := range strings.Split(readText(read, name), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if m := pyVersionLine.FindStringSubmatch(line); m != nil {
				return m[1]
			}
			break
		}
	}
	return ""
}

// withPythonVersion runs tpl on the Python version the project asks for.
func withPythonVersion(tpl templates.Template, read func(string) []byte) templates.Template {
	if v := pythonVersion(read); v != "" {
		if t, ok := templates.WithPython(tpl, v); ok {
			return t
		}
	}
	return tpl
}

// pyModuleOf is the module name of a Python file: app/main.py is app.main,
// app/__init__.py is app. ok is false for a name Python cannot import.
func pyModuleOf(file string) (string, bool) {
	m := strings.TrimSuffix(file, ".py")
	m = strings.TrimSuffix(m, "/__init__")
	m = strings.ReplaceAll(m, "/", ".")
	for _, part := range strings.Split(m, ".") {
		if !pyIdentRe.MatchString(part) {
			return "", false
		}
	}
	return m, true
}

// pythonEntry finds the file and variable of the web app: app = FastAPI(...)
// for FastAPI, app = Flask(...) or def create_app() for Flask.
func pythonEntry(has map[string]bool, read func(string) []byte, fastapi, flask bool) (file, target, server string, ok bool) {
	for _, f := range pythonEntryCandidates(has) {
		mod, ok := pyModuleOf(f)
		if !ok {
			continue
		}
		src := []byte(readText(read, f))
		if fastapi {
			if m := fastAPIApp.FindSubmatch(src); m != nil {
				return f, mod + ":" + string(m[1]), templates.PyServerUvicorn, true
			}
		}
		if flask {
			if m := flaskApp.FindSubmatch(src); m != nil {
				return f, mod + ":" + string(m[1]), templates.PyServerGunicorn, true
			}
			if flaskCreate.Match(src) {
				return f, mod + ":create_app()", templates.PyServerGunicorn, true
			}
		}
	}
	return "", "", "", false
}

func pythonPlan(files []string, has map[string]bool, userIgnore []byte, read func(string) []byte) (BuildPlan, *apperr.Error) {
	deps := pythonDeps(read)
	fastapi, flask := deps["fastapi"], deps["flask"]
	if !fastapi && !flask {
		return BuildPlan{}, stackErr("python", files,
			"Found a Python project, but its requirements.txt or pyproject.toml lists neither FastAPI nor Flask, so bitploy does not know how to start it.",
			"A FastAPI or Flask app needs fastapi or flask in requirements.txt. Any other app needs a Dockerfile: your AI assistant can write one.")
	}
	file, target, server, ok := pythonEntry(has, read, fastapi, flask)
	if !ok {
		hint := "Put the app in main.py or app.py as app = FastAPI() or app = Flask(__name__), and list fastapi or flask in requirements.txt. Or add a Dockerfile: your AI assistant can write one."
		return BuildPlan{}, stackErr("python", files,
			"Found a Python project, but no file that starts the web app (like main.py with app = FastAPI()).", hint)
	}
	var install []string
	if !deps[server] {
		install = append(install, server)
	}
	tpl, ok := templates.PythonApp(files, server, target, install)
	if !ok {
		return BuildPlan{}, cannotAdapt("python", files, "a Python app")
	}
	tpl = withPythonVersion(tpl, read)
	name := "Flask"
	if server == templates.PyServerUvicorn {
		name = "FastAPI"
	}
	why := fmt.Sprintf("%s app in %s: installing its packages and running it with %s.", name, file, server) + assistantTail
	plan := templatePlan(BuildKindPython, stackDockerfilePath(BuildKindPython), tpl, userIgnore, why)
	plan.Unsure = true
	return plan, nil
}

// djangoModule finds the folder of wsgi.py, as a module name: from the
// settings module manage.py sets (mysite.settings, config.settings.prod), else
// the only <folder>/wsgi.py.
func djangoModule(has map[string]bool, read func(string) []byte) (module, settings string, ok bool) {
	if m := pySettings.FindStringSubmatch(readText(read, "manage.py")); m != nil {
		settings = string(m[1])
		parts := strings.Split(settings, ".")
		for n := len(parts) - 1; n >= 1; n-- {
			if has[strings.Join(parts[:n], "/")+"/wsgi.py"] {
				return strings.Join(parts[:n], "."), settings, true
			}
		}
	}
	var found []string
	for f := range has {
		if parts := strings.Split(f, "/"); len(parts) == 2 && parts[1] == "wsgi.py" {
			found = append(found, parts[0])
		}
	}
	if len(found) == 1 {
		if mod, ok := pyModuleOf(found[0] + "/__init__.py"); ok {
			return mod, settings, true
		}
	}
	return "", settings, false
}

// djangoCollects reports whether collectstatic can run: false only when the
// settings file is there, sets no STATIC_ROOT and imports no other settings
// file that might.
func djangoCollects(settings string, read func(string) []byte) bool {
	if settings == "" {
		return true
	}
	s := readText(read, strings.ReplaceAll(settings, ".", "/")+".py")
	if s == "" {
		return true
	}
	return strings.Contains(s, "STATIC_ROOT") || strings.Contains(s, "import *")
}

func djangoPlan(files []string, has map[string]bool, userIgnore []byte, read func(string) []byte) (BuildPlan, *apperr.Error) {
	if !has["requirements.txt"] && !has["pyproject.toml"] {
		return BuildPlan{}, stackErr("django", files,
			"Found a Django project, but no requirements.txt that lists its packages.",
			"Add requirements.txt with django and the other packages it needs (pip freeze > requirements.txt), or add a Dockerfile: your AI assistant can write one.")
	}
	module, settings, ok := djangoModule(has, read)
	if !ok {
		return BuildPlan{}, stackErr("django", files,
			"Found a Django project, but no wsgi.py next to its settings, so bitploy does not know how to start it.",
			"Keep the wsgi.py that django-admin startproject made, or add a Dockerfile: your AI assistant can write one.")
	}
	collect := djangoCollects(settings, read)
	var install []string
	if !pythonDeps(read)["gunicorn"] {
		install = []string{"gunicorn"}
	}
	tpl, ok := templates.Django(files, module, settings, collect, install)
	if !ok {
		return BuildPlan{}, cannotAdapt("django", files, "a Django app")
	}
	tpl = withPythonVersion(tpl, read)
	why := "Django app (" + module + "): installing its packages and running it with gunicorn. Migrations run on every start."
	if !collect {
		why += " The settings have no STATIC_ROOT, so static files are not collected."
	}
	if settings != "" {
		why += " The image runs " + settings + " with the app's address added to ALLOWED_HOSTS and a SQLite file in the code folder moved to /data (" + templates.DjangoSettingsFile + "). The database survives deploys only with Keep saved files (the /data volume) on."
	} else {
		why += " manage.py names no settings module, so the settings must allow the app's address (ALLOWED_HOSTS) and keep the SQLite database in /data."
	}
	why += assistantTail
	plan := templatePlan(BuildKindDjango, stackDockerfilePath(BuildKindDjango), tpl, userIgnore, why)
	plan.Unsure = true
	return plan, nil
}

// ---- PHP ----

func phpPlan(files []string, has map[string]bool, userIgnore []byte) (BuildPlan, *apperr.Error) {
	if !has["index.php"] {
		return BuildPlan{}, stackErr("php", files,
			"Found a PHP project, but no index.php at the top to open.",
			"Put index.php in the top folder, or add a Dockerfile: your AI assistant can write one.")
	}
	tpl, ok := templates.PHPSite(files)
	if !ok {
		return BuildPlan{}, cannotAdapt("php", files, "a PHP site")
	}
	why := "PHP site: serving it with Apache."
	if has["composer.json"] {
		why = "PHP site: installing its Composer packages and serving it with Apache."
	}
	return templatePlan(BuildKindPHP, stackDockerfilePath(BuildKindPHP), tpl, userIgnore, why), nil
}

// viteArtisan matches a Vite config that runs php artisan during the build,
// like the Wayfinder plugin of the Laravel starter kits.
var viteArtisan = regexp.MustCompile(`vite-plugin-wayfinder|\bwayfinder\(|php artisan`)

// viteRunsArtisan reports whether the Vite build of a Laravel project runs
// php artisan: package.json lists the Wayfinder plugin, or the Vite config
// names it or php artisan.
func viteRunsArtisan(has map[string]bool, read func(string) []byte, p *project) bool {
	if p != nil && p.deps["@laravel/vite-plugin-wayfinder"] {
		return true
	}
	for _, name := range []string{"vite.config.ts", "vite.config.js", "vite.config.mts", "vite.config.mjs"} {
		if has[name] && viteArtisan.MatchString(readText(read, name)) {
			return true
		}
	}
	return false
}

func laravelPlan(files []string, has map[string]bool, userIgnore []byte, read func(string) []byte) (BuildPlan, *apperr.Error) {
	if !has["composer.json"] {
		return BuildPlan{}, stackErr("laravel", files,
			"Found a Laravel project, but no composer.json that lists its packages.",
			"Add composer.json and composer.lock from your project, or add a Dockerfile: your AI assistant can write one.")
	}
	// The template builds the assets with the build script of package.json;
	// without one there is nothing to build, so the stage is left out.
	assets, phpAssets := has["package.json"], false
	if assets {
		p, ok := readProject(files, false, read)
		if ok && !p.scripts["build"] {
			assets = false
		}
		phpAssets = assets && viteRunsArtisan(has, read, p)
	}
	tplFiles := files
	if !assets {
		tplFiles = nil
		for _, f := range files {
			if path.Clean(f) != "package.json" {
				tplFiles = append(tplFiles, f)
			}
		}
	}
	tpl, ok := templates.Laravel(tplFiles, phpAssets)
	if !ok {
		return BuildPlan{}, cannotAdapt("laravel", files, "a Laravel app")
	}
	why := "Laravel app: installing its Composer packages and serving it with Apache."
	switch {
	case phpAssets:
		why = "Laravel app: installing its Composer packages, building its assets with PHP next to Node (the Vite build runs php artisan) and serving it with Apache."
	case assets:
		why = "Laravel app: installing its Composer packages, building its assets and serving it with Apache."
	}
	why += " The database is SQLite in /data and migrations run on every start. The database survives deploys only with Keep saved files (the /data volume) on." +
		" Without APP_KEY in the app's variables, bitploy makes one and keeps it in /data."
	return templatePlan(BuildKindLaravel, stackDockerfilePath(BuildKindLaravel), tpl, userIgnore, why), nil
}
