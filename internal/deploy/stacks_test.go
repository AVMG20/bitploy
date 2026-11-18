package deploy

import (
	"strings"
	"testing"
	"unicode/utf16"
)

// pip freeze > requirements.txt in Windows PowerShell writes UTF-16 with a
// byte order mark; pip reads it, and so must the planner.
func TestPythonDepsUTF16(t *testing.T) {
	text := "Flask==3.1.0\r\ngunicorn==23.0.0\r\n"
	u := utf16.Encode([]rune(text))
	le, be := []byte{0xFF, 0xFE}, []byte{0xFE, 0xFF}
	for _, c := range u {
		le = append(le, byte(c), byte(c>>8))
		be = append(be, byte(c>>8), byte(c))
	}
	for name, raw := range map[string][]byte{"utf-16le": le, "utf-16be": be, "utf-8 bom": append([]byte{0xEF, 0xBB, 0xBF}, text...)} {
		deps := pythonDeps(func(f string) []byte {
			if f == "requirements.txt" {
				return raw
			}
			return nil
		})
		if !deps["flask"] || !deps["gunicorn"] {
			t.Errorf("%s: %v", name, deps)
		}
	}
}

func TestPyInclude(t *testing.T) {
	for line, want := range map[string]string{
		"-r base.txt":                  "base.txt",
		"-rbase.txt":                   "base.txt",
		"--requirement=req/prod.txt":   "req/prod.txt",
		"--requirement req/prod.txt":   "req/prod.txt",
		"-r ../secrets.txt":            "",
		"-r /etc/passwd":               "",
		"-r https://example.com/r.txt": "",
		"-e .":                         "",
		"flask":                        "",
	} {
		got, ok := pyInclude(line)
		if got != want || ok != (want != "") {
			t.Errorf("%q: %q %v, want %q", line, got, ok, want)
		}
	}
	// An include of itself does not loop.
	n := 0
	pythonDeps(func(f string) []byte {
		n++
		return []byte("-r requirements.txt\nflask\n")
	})
	if n > 10 {
		t.Errorf("read %d files", n)
	}
}

// A huge file is only looked at up to maxPlanRead.
func TestReadTextLimit(t *testing.T) {
	big := strings.Repeat("x", maxPlanRead+10)
	if got := readText(func(string) []byte { return []byte(big) }, "a.py"); len(got) != maxPlanRead {
		t.Errorf("len = %d", len(got))
	}
	if got := readText(func(string) []byte { return nil }, "a.py"); got != "" {
		t.Errorf("missing file: %q", got)
	}
}

// Names from the files never reach the Dockerfile unchecked: a cmd folder or
// settings module that is not a plain name is left alone.
func TestStackNamesStayOutOfTheDockerfile(t *testing.T) {
	plan, err := PlanBuild(BuildModeAuto, "Dockerfile", "", "", []string{"go.mod", "go.sum", "cmd/a b\nRUN id/main.go"}, readOf(nil, nil))
	if err == nil || strings.Contains(string(plan.Inject[plan.DockerfilePath]), "RUN id") {
		t.Errorf("go: %+v %v", plan, err)
	}
	read := readOf(nil, nil, map[string]string{
		"manage.py":        `os.environ.setdefault("DJANGO_SETTINGS_MODULE", "my site.settings")`,
		"requirements.txt": "django\n",
	})
	plan, err = PlanBuild(BuildModeAuto, "Dockerfile", "", "", []string{"manage.py", "requirements.txt", "my site/wsgi.py"}, read)
	if err == nil || strings.Contains(string(plan.Inject[plan.DockerfilePath]), "my site") {
		t.Errorf("django: %+v %v", plan, err)
	}
	read = readOf(nil, nil, map[string]string{
		"requirements.txt": "flask\n",
		"my-app.py":        "from flask import Flask\napp = Flask(__name__)\n",
	})
	if _, err := PlanBuild(BuildModeAuto, "Dockerfile", "", "", []string{"requirements.txt", "my-app.py"}, read); err == nil {
		t.Error("python: a file Python cannot import is not the app")
	}
}
