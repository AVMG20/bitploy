package deploy

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

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

var goPackageMain = regexp.MustCompile(`(?m)^package\s+main\b`)

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
		if parts := strings.Split(f, "/"); len(parts) == 3 && parts[0] == "cmd" && parts[2] == "main.go" && !seen[parts[1]] {
			seen[parts[1]] = true
			cmds = append(cmds, parts[1])
		}
	}
	sort.Strings(top)
	for _, f := range top {
		if goPackageMain.Match(read(f)) {
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
	tpl, ok := templates.GoProgram(files, dir)
	if !ok {
		return BuildPlan{}, cannotAdapt("go", files, "a Go app")
	}
	why := "Go app, built from the top folder."
	if dir != "." {
		why = "Go app, built from " + dir + "."
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

// pythonDeps lists the packages named in requirements.txt and pyproject.toml,
// in lower case with - for _. fastapi[standard] brings uvicorn with it.
func pythonDeps(read func(string) []byte) map[string]bool {
	deps := map[string]bool{}
	add := func(name, extras string) {
		n := normPyName(name)
		deps[n] = true
		if n == "fastapi" && (strings.Contains(extras, "standard") || strings.Contains(extras, "all")) {
			deps["uvicorn"] = true
		}
	}
	for _, line := range strings.Split(string(read("requirements.txt")), "\n") {
		line, _, _ = strings.Cut(line, "#")
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "-") {
			continue
		}
		if m := pyReqName.FindStringSubmatch(line); m != nil {
			add(m[1], m[2])
		}
	}
	toml := string(read("pyproject.toml"))
	for _, m := range pyQuoted.FindAllStringSubmatch(toml, -1) {
		add(m[1], m[2])
	}
	for _, m := range pyTOMLKey.FindAllStringSubmatch(toml, -1) {
		add(m[1], "")
	}
	return deps
}

// pythonEntryCandidates lists the files that may start a Python web app, the
// usual names first, then the other files at the top, then the app package.
func pythonEntryCandidates(has map[string]bool) []string {
	first := []string{"main.py", "app.py", "server.py", "api.py", "application.py", "wsgi.py", "asgi.py", "run.py"}
	var out, rest []string
	seen := map[string]bool{}
	for _, f := range first {
		if has[f] {
			out = append(out, f)
			seen[f] = true
		}
	}
	for f := range has {
		if !strings.Contains(f, "/") && strings.HasSuffix(f, ".py") && !seen[f] {
			rest = append(rest, f)
		}
	}
	sort.Strings(rest)
	out = append(out, rest...)
	for _, f := range []string{"app/main.py", "app/app.py", "app/__init__.py"} {
		if has[f] {
			out = append(out, f)
		}
	}
	return out
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
		src := read(f)
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
	if m := pySettings.FindSubmatch(read("manage.py")); m != nil {
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
	src := read(strings.ReplaceAll(settings, ".", "/") + ".py")
	if src == nil {
		return true
	}
	s := string(src)
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
	tpl, ok := templates.Django(files, module, collect, install)
	if !ok {
		return BuildPlan{}, cannotAdapt("django", files, "a Django app")
	}
	why := "Django app (" + module + "): installing its packages and running it with gunicorn. Migrations run on every start."
	if !collect {
		why += " The settings have no STATIC_ROOT, so static files are not collected."
	}
	why += " The settings must keep the database in /data and allow the app's address (ALLOWED_HOSTS)." + assistantTail
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
	tpl, _ := templates.ForFiles("php", files)
	why := "PHP site: serving it with Apache."
	if has["composer.json"] {
		why = "PHP site: installing its Composer packages and serving it with Apache."
	}
	return templatePlan(BuildKindPHP, stackDockerfilePath(BuildKindPHP), tpl, userIgnore, why), nil
}

func laravelPlan(files []string, has map[string]bool, userIgnore []byte, read func(string) []byte) (BuildPlan, *apperr.Error) {
	if !has["composer.json"] {
		return BuildPlan{}, stackErr("laravel", files,
			"Found a Laravel project, but no composer.json that lists its packages.",
			"Add composer.json and composer.lock from your project, or add a Dockerfile: your AI assistant can write one.")
	}
	// The template builds the assets with the build script of package.json;
	// without one there is nothing to build, so the stage is left out.
	assets := has["package.json"]
	if assets {
		if p, ok := readProject(files, false, read); ok && !p.scripts["build"] {
			assets = false
		}
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
	tpl, _ := templates.ForFiles("laravel", tplFiles)
	why := "Laravel app: installing its Composer packages and serving it with Apache. Migrations run on every start."
	if assets {
		why = "Laravel app: installing its Composer packages, building its assets and serving it with Apache. Migrations run on every start."
	}
	why += " Set APP_KEY in the app's variables before it opens (php artisan key:generate --show makes one)."
	return templatePlan(BuildKindLaravel, stackDockerfilePath(BuildKindLaravel), tpl, userIgnore, why), nil
}
