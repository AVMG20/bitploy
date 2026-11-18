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
	base, _ := ForFiles("go", []string{"go.mod", "go.sum"})
	if got, ok := GoProgram([]string{"go.mod", "go.sum"}, "."); !ok || got != base {
		t.Error("the top folder is the template as it is")
	}
	tpl, ok := GoProgram([]string{"go.mod", "go.sum"}, "cmd/server")
	if !ok || !strings.Contains(tpl.Dockerfile, "-o /out/app ./cmd/server\n") || strings.Contains(tpl.Dockerfile, "-o /out/app .\n") {
		t.Errorf("cmd/server: %v\n%s", ok, tpl.Dockerfile)
	}
	parses(t, "go cmd/server", tpl.Dockerfile)
	for _, bad := range []string{"../x", "cmd/../../x", "/abs", "a b", "x;rm", "cmd//x"} {
		if _, ok := GoProgram(nil, bad); ok {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestPythonApp(t *testing.T) {
	tpl, ok := PythonApp([]string{"requirements.txt", "main.py"}, PyServerUvicorn, "main:app", nil)
	parses(t, "fastapi", tpl.Dockerfile)
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
	tpl, ok := Django([]string{"requirements.txt", "manage.py"}, "mysite", true, nil)
	parses(t, "django", tpl.Dockerfile)
	if !ok || !strings.Contains(tpl.Dockerfile, "exec gunicorn mysite.wsgi:application --bind 0.0.0.0:${PORT:-8000}") ||
		strings.Contains(tpl.Dockerfile, "myproject") || !strings.Contains(tpl.Dockerfile, "collectstatic") || strings.Contains(tpl.Dockerfile, "pip install gunicorn") {
		t.Errorf("django: %v\n%s", ok, tpl.Dockerfile)
	}
	tpl, ok = Django([]string{"requirements.txt", "manage.py"}, "config", false, []string{"gunicorn"})
	if !ok || strings.Contains(tpl.Dockerfile, "collectstatic") || !strings.Contains(tpl.Dockerfile, "RUN pip install gunicorn\n") || !strings.Contains(tpl.Dockerfile, "config.wsgi") {
		t.Errorf("django without collectstatic: %v\n%s", ok, tpl.Dockerfile)
	}
	if _, ok := Django(nil, "my-site", true, nil); ok {
		t.Error("an unsafe module must be refused")
	}
}
