package deploy

import (
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/templates"
)

// Every template, adapted to every kind of project it can meet, must pass
// bitploy's own Dockerfile checks without errors or warnings: an agent that
// calls get_template and then validate_dockerfile must not be told to fix
// what bitploy gave it.
func TestTemplatesPassTheChecks(t *testing.T) {
	variants := map[string][]string{
		"no file list":   nil,
		"npm":            {"package.json", "package-lock.json"},
		"npm, no lock":   {"package.json"},
		"pnpm":           {"package.json", "pnpm-lock.yaml"},
		"pnpm workspace": {"package.json", "pnpm-lock.yaml", "pnpm-workspace.yaml"},
		"yarn":           {"package.json", "yarn.lock"},
		"yarn berry":     {"package.json", "yarn.lock", ".yarnrc.yml"},
		"bun":            {"package.json", "bun.lock"},
		"bun.lockb":      {"package.json", "bun.lockb"},
		"requirements":   {"requirements.txt"},
		"uv":             {"pyproject.toml", "uv.lock"},
		"poetry":         {"pyproject.toml", "poetry.lock"},
		"pyproject":      {"pyproject.toml"},
		"composer":       {"composer.json", "composer.lock"},
		"go":             {"go.mod", "go.sum", "main.go"},
		"go, no go.sum":  {"go.mod", "main.go"},
	}
	stacks := append(append([]string{}, templates.Stacks...), "node+build")
	for _, stack := range stacks {
		for name, files := range variants {
			tpl, ok := templates.ForFiles(stack, files)
			if stack == "node+build" {
				// Auto mode's node template with a build step: every lock file must fit.
				if tpl, ok = templates.NodeWithBuild(files); !ok {
					t.Errorf("%s / %s: NodeWithBuild did not apply", stack, name)
					continue
				}
			}
			if !ok {
				t.Fatalf("%s: unknown stack", stack)
			}
			checkTemplate(t, stack+" / "+name, tpl, files)
		}
	}
}

// checkTemplate runs bitploy's own Dockerfile checks on a template: a port, no
// errors or warnings, no carriage returns.
func checkTemplate(t *testing.T, name string, tpl templates.Template, files []string) {
	t.Helper()
	ctx := append([]string{"Dockerfile", ".dockerignore"}, files...)
	res := CheckDockerfile(CheckInput{
		Dockerfile: []byte(tpl.Dockerfile), DockerfilePath: "Dockerfile",
		Files: ctx, Dockerignore: []byte(tpl.Dockerignore), AppType: "web",
	})
	if res.Port == 0 {
		t.Errorf("%s: no port found", name)
	}
	for _, f := range res.Findings {
		// A project without a lock file is told to add one: that is about
		// the code, not the template, which installs with npm install then.
		if f.Code == CodeNoLockfile && !hasLockFile(files) {
			continue
		}
		if f.Severity == sevError || f.Severity == sevWarning {
			t.Errorf("%s: %s %s: %s\n%s", name, f.Severity, f.Code, f.Message, tpl.Dockerfile)
		}
	}
	if strings.Contains(tpl.Dockerfile, "\r") {
		t.Errorf("%s: carriage return in the Dockerfile", name)
	}
}

func hasLockFile(files []string) bool {
	for _, f := range files {
		if containsString(jsLockFiles, f) {
			return true
		}
	}
	return false
}
