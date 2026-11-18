package templates

import (
	"strings"
	"testing"
)

func TestForFilesPHP(t *testing.T) {
	base, _ := Get("php")
	if got, _ := ForFiles("php", []string{"index.php"}); got != base {
		t.Error("php without composer.json should equal Get")
	}
	tpl, _ := ForFiles("php", []string{"index.php", "composer.json", "composer.lock"})
	parses(t, "php + composer", tpl.Dockerfile)
	vendor, copyApp := strings.Index(tpl.Dockerfile, "FROM composer:2 AS vendor\n"), strings.Index(tpl.Dockerfile, "COPY --from=vendor /app /var/www/html/\n")
	if !strings.HasPrefix(tpl.Dockerfile, "# Stage 1:") || vendor < 0 || copyApp < vendor || strings.Contains(tpl.Dockerfile, "COPY . /var/www/html/") {
		t.Errorf("php + composer needs the vendor stage first and the app copied from it:\n%s", tpl.Dockerfile)
	}
	if !strings.Contains(tpl.Readme, "composer.json") {
		t.Errorf("notes:\n%s", tpl.Readme)
	}
}

func TestGoProgram(t *testing.T) {
	top, ok := GoProgram([]string{"go.mod", "go.sum", "main.go"}, ".", false)
	parses(t, "go", top.Dockerfile)
	want := `# Stage 1: compile a static binary.
FROM golang:1.26-alpine AS build
# A module that needs a newer Go than this image makes Go download that version
# instead of failing with "requires go >= ...".
ENV GOTOOLCHAIN=auto
RUN mkdir /data
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app .

# Stage 2: copy only the binary into a tiny image that runs as a non-root user.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
COPY --from=build --chown=nonroot:nonroot /data /data
USER nonroot:nonroot
`
	if !ok || !strings.HasPrefix(top.Dockerfile, want) || !strings.HasSuffix(top.Dockerfile, "EXPOSE 8080\nENTRYPOINT [\"/app\"]\n") {
		t.Errorf("top folder: %v\n%s", ok, top.Dockerfile)
	}

	tpl, ok := GoProgram([]string{"go.mod", "go.sum"}, "cmd/server", false)
	if !ok || !strings.Contains(tpl.Dockerfile, "-o /out/app ./cmd/server\n") || strings.Contains(tpl.Dockerfile, "-o /out/app .\n") {
		t.Errorf("cmd/server: %v\n%s", ok, tpl.Dockerfile)
	}
	parses(t, "go cmd/server", tpl.Dockerfile)
	for _, bad := range []string{"../x", "cmd/../../x", "/abs", "a b", "x;rm", "cmd//x", "cmd/x\nRUN id", `cmd/"x"`} {
		if _, ok := GoProgram(nil, bad, false); ok {
			t.Errorf("%q must be refused", bad)
		}
	}

	// go-sqlite3 needs cgo: Debian with gcc to build, glibc to run.
	tpl, ok = GoProgram([]string{"go.mod", "main.go"}, ".", true)
	parses(t, "go cgo", tpl.Dockerfile)
	df := tpl.Dockerfile
	for _, w := range []string{
		"FROM golang:1.26-bookworm AS build\n",
		"RUN go mod tidy\nRUN CGO_ENABLED=1 go build -trimpath -ldflags=\"-s -w\" -o /out/app .\n",
		"FROM gcr.io/distroless/base-debian12:nonroot\n",
	} {
		if !ok || !strings.Contains(df, w) {
			t.Errorf("cgo lacks %q: %v\n%s", w, ok, df)
		}
	}
	if strings.Contains(df, "alpine") || strings.Contains(df, "CGO_ENABLED=0") || strings.Contains(df, "distroless/static") || !strings.Contains(tpl.Readme, "go-sqlite3") {
		t.Errorf("cgo:\n%s\n%s", df, tpl.Readme)
	}

	// Folders the program reads at run time sit next to where it starts.
	tpl, _ = GoProgram([]string{"go.mod", "go.sum", "main.go", "templates/index.html", "static/css/app.css", "./public/robots.txt", "internal/templates/x.go", "templatesx"}, ".", false)
	parses(t, "go folders", tpl.Dockerfile)
	if !strings.Contains(tpl.Dockerfile, "COPY --from=build --chown=nonroot:nonroot /data /data\n"+
		"# The program reads these folders at run time, from the folder it starts in.\n"+
		"WORKDIR /srv\n"+
		"COPY --from=build /src/templates ./templates\n"+
		"COPY --from=build /src/static ./static\n"+
		"COPY --from=build /src/public ./public\n"+
		"USER nonroot:nonroot\n") {
		t.Errorf("folders:\n%s", tpl.Dockerfile)
	}
}

func TestPHPSite(t *testing.T) {
	for _, files := range [][]string{{"index.php"}, {"index.php", "composer.json"}} {
		tpl, ok := PHPSite(files)
		parses(t, "php", tpl.Dockerfile)
		if !ok || !strings.Contains(tpl.Dockerfile, "RUN mkdir /data \\\n && chown -R www-data:www-data /data /var/www/html /var/run/apache2") {
			t.Errorf("%v: %v\n%s", files, ok, tpl.Dockerfile)
		}
	}
}

func TestWithPython(t *testing.T) {
	base, _ := PythonApp([]string{"requirements.txt", "main.py"}, PyServerUvicorn, "main:app", nil)
	tpl, ok := WithPython(base, "3.11")
	if !ok || !strings.HasPrefix(tpl.Dockerfile, "FROM python:3.11-slim\n") {
		t.Errorf("3.11: %v\n%s", ok, tpl.Dockerfile)
	}
	for _, bad := range []string{"", "3", "3.11.4", "2.7", "3.12\nRUN id", "latest"} {
		if _, ok := WithPython(base, bad); ok {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestPythonApp(t *testing.T) {
	tpl, ok := PythonApp([]string{"requirements.txt", "main.py"}, PyServerUvicorn, "main:app", nil)
	parses(t, "fastapi", tpl.Dockerfile)
	// The app user owns /data, so the volume it gets is writable.
	if !strings.Contains(tpl.Dockerfile, "RUN useradd --system --uid 10001 --create-home app \\\n && mkdir /data && chown app /data\nUSER app\n") {
		t.Errorf("fastapi /data:\n%s", tpl.Dockerfile)
	}
	if !ok || !strings.HasSuffix(tpl.Dockerfile, `CMD ["sh", "-c", "exec uvicorn main:app --host 0.0.0.0 --port ${PORT:-8000} --proxy-headers --forwarded-allow-ips '*'"]`+"\n") ||
		strings.Contains(tpl.Dockerfile, `"python", "app.py"`) || strings.Contains(tpl.Dockerfile, "pip install uvicorn") {
		t.Errorf("fastapi: %v\n%s", ok, tpl.Dockerfile)
	}
	tpl, ok = PythonApp([]string{"pyproject.toml", "uv.lock"}, PyServerGunicorn, "app:create_app()", []string{"gunicorn"})
	install, user := strings.Index(tpl.Dockerfile, "RUN uv pip install gunicorn\n"), strings.Index(tpl.Dockerfile, "USER app")
	if !ok || install < 0 || user < install || !strings.Contains(tpl.Dockerfile, "--bind 0.0.0.0:${PORT:-8000}") || !strings.Contains(tpl.Dockerfile, "'app:create_app()'") {
		t.Errorf("flask factory with uv: %v\n%s", ok, tpl.Dockerfile)
	}
	tpl, _ = PythonApp([]string{"requirements.txt"}, PyServerGunicorn, "app:app", []string{"gunicorn"})
	if !strings.Contains(tpl.Dockerfile, "RUN pip install gunicorn\n") {
		t.Errorf("pip installs the server:\n%s", tpl.Dockerfile)
	}
	for _, c := range []struct{ server, target string }{
		{PyServerUvicorn, "main:create_app()"}, // uvicorn needs --factory for that
		{PyServerUvicorn, "main"},
		{PyServerUvicorn, "main:app; rm -rf /"},
		{PyServerGunicorn, "1main:app"},
		{"hypercorn", "main:app"},
	} {
		if _, ok := PythonApp(nil, c.server, c.target, nil); ok {
			t.Errorf("%s %q must be refused", c.server, c.target)
		}
	}
	if _, ok := PythonApp(nil, PyServerUvicorn, "main:app", []string{"uvicorn; rm"}); ok {
		t.Error("an unsafe package name must be refused")
	}
}

func TestDjango(t *testing.T) {
	tpl, ok := Django([]string{"requirements.txt", "manage.py"}, "mysite", "", true, nil)
	parses(t, "django", tpl.Dockerfile)
	if !ok || !strings.Contains(tpl.Dockerfile, "exec gunicorn mysite.wsgi:application --bind 0.0.0.0:${PORT:-8000}") ||
		strings.Contains(tpl.Dockerfile, "myproject") || !strings.Contains(tpl.Dockerfile, "collectstatic") || strings.Contains(tpl.Dockerfile, "pip install gunicorn") {
		t.Errorf("django: %v\n%s", ok, tpl.Dockerfile)
	}
	if strings.Contains(tpl.Dockerfile, DjangoSettingsFile) || strings.Contains(tpl.Dockerfile, "DJANGO_SETTINGS_MODULE") {
		t.Errorf("django without a settings module must not override it:\n%s", tpl.Dockerfile)
	}
	tpl, ok = Django([]string{"requirements.txt", "manage.py"}, "mysite", "mysite.settings", true, nil)
	parses(t, "django with settings", tpl.Dockerfile)
	if !ok || !strings.Contains(tpl.Dockerfile, "COPY . .\n\n# The project's settings") ||
		!strings.Contains(tpl.Dockerfile, `importlib.import_module("mysite.settings")`) ||
		!strings.Contains(tpl.Dockerfile, "ENV DJANGO_SETTINGS_MODULE=bitploy_settings\n") ||
		strings.Index(tpl.Dockerfile, "DJANGO_SETTINGS_MODULE=bitploy_settings") > strings.Index(tpl.Dockerfile, "collectstatic") {
		t.Errorf("django with settings: %v\n%s", ok, tpl.Dockerfile)
	}
	if _, ok := Django(nil, "mysite", "mysite.settings; rm", true, nil); ok {
		t.Error("an unsafe settings module must be refused")
	}
	tpl, ok = Django([]string{"requirements.txt", "manage.py"}, "config", "", false, []string{"gunicorn"})
	if !ok || strings.Contains(tpl.Dockerfile, "collectstatic") || !strings.Contains(tpl.Dockerfile, "RUN pip install gunicorn\n") || !strings.Contains(tpl.Dockerfile, "config.wsgi") {
		t.Errorf("django without collectstatic: %v\n%s", ok, tpl.Dockerfile)
	}
	if _, ok := Django(nil, "my-site", "", true, nil); ok {
		t.Error("an unsafe module must be refused")
	}
}
