package templates

import (
	"os"
	"strings"
	"testing"

	"github.com/moby/buildkit/frontend/dockerfile/parser"
	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"
)

func TestAllStacksEmbedded(t *testing.T) {
	for _, s := range Stacks {
		tpl, ok := Get(s)
		if !ok {
			t.Fatalf("%s: not found", s)
		}
		if tpl.Stack != s || tpl.Dockerfile == "" || tpl.Dockerignore == "" || tpl.Readme == "" {
			t.Errorf("%s: incomplete template %+v", s, tpl)
		}
		if !strings.Contains(tpl.Dockerfile, "FROM ") || !strings.Contains(tpl.Dockerfile, "EXPOSE ") {
			t.Errorf("%s: Dockerfile lacks FROM or EXPOSE", s)
		}
		if strings.Contains(tpl.Dockerfile, ":latest") {
			t.Errorf("%s: uses :latest", s)
		}
		if !strings.Contains(tpl.Dockerignore, ".env") {
			t.Errorf("%s: .dockerignore must exclude .env", s)
		}
	}
}

func TestDockerignoreExcludesNestedSecrets(t *testing.T) {
	for _, s := range Stacks {
		tpl, _ := Get(s)
		patterns, err := ignorefile.ReadAll(strings.NewReader(tpl.Dockerignore))
		if err != nil {
			t.Fatal(err)
		}
		pm, err := patternmatcher.New(patterns)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range []string{".env", ".env.local", "api/.env", "api/.env.production", "a/b/.env", ".git/config", "sub/.git/config"} {
			if ok, _ := pm.MatchesOrParentMatches(p); !ok {
				t.Errorf("%s: .dockerignore does not exclude %s", s, p)
			}
		}
	}
}

func TestNginxHidesDotfiles(t *testing.T) {
	for _, s := range Stacks {
		tpl, _ := Get(s)
		if !strings.Contains(tpl.Dockerfile, "nginx") {
			continue
		}
		if !strings.Contains(tpl.Dockerfile, "location ~ /\\.(?!well-known/) {\n        deny all;\n    }") {
			t.Errorf("%s: the nginx config must deny dotfiles but keep /.well-known/", s)
		}
	}
}

func TestStaticIsTheSpecExample(t *testing.T) {
	tpl, _ := Get("static")
	spec, err := os.ReadFile("../../SPEC.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(spec), "```dockerfile\n"+tpl.Dockerfile+"```") {
		t.Errorf("SPEC.md does not show the static Dockerfile:\n%s", tpl.Dockerfile)
	}
	for _, line := range []string{"listen 8080;", "absolute_redirect off;", "try_files $uri $uri/ $uri.html =404;", "COPY . /usr/share/nginx/html\n", "EXPOSE 8080\n"} {
		if !strings.Contains(tpl.Dockerfile, line) {
			t.Errorf("static Dockerfile lacks %q", line)
		}
	}
}

func TestUnknown(t *testing.T) {
	if _, ok := Get("ruby"); ok {
		t.Error("ruby should be unknown")
	}
	if _, ok := Get("  Node-SPA "); !ok {
		t.Error("lookup should ignore case and spaces")
	}
}

func parses(t *testing.T, name, dockerfile string) *parser.Result {
	t.Helper()
	res, err := parser.Parse(strings.NewReader(dockerfile))
	if err != nil {
		t.Fatalf("%s: Dockerfile does not parse: %v\n%s", name, err, dockerfile)
	}
	return res
}

func TestStackOrder(t *testing.T) {
	want := "static node-spa node-static node nextjs python django go php laravel generic"
	if got := strings.Join(Stacks, " "); got != want {
		t.Errorf("Stacks = %q", got)
	}
}

func TestAllBaseTemplatesParse(t *testing.T) {
	for _, s := range Stacks {
		tpl, _ := Get(s)
		res := parses(t, s, tpl.Dockerfile)
		// One EXPOSE in the final stage.
		expose := 0
		for _, n := range res.AST.Children {
			switch strings.ToUpper(n.Value) {
			case "FROM":
				expose = 0
			case "EXPOSE":
				expose++
			}
		}
		if expose != 1 {
			t.Errorf("%s: want exactly one EXPOSE in the final stage, got %d", s, expose)
		}
		final := tpl.Dockerfile[strings.LastIndex(tpl.Dockerfile, "\nFROM ")+1:]
		if !strings.Contains(final, "\nUSER ") && !strings.HasPrefix(final, "FROM nginxinc/nginx-unprivileged") {
			t.Errorf("%s: final stage must run as a non-root USER", s)
		}
		if strings.Contains(tpl.Dockerfile, "USER root") {
			t.Errorf("%s: USER root", s)
		}
	}
}

var nodeStacks = []string{"node", "node-spa", "node-static", "nextjs"}

func TestForFilesNode(t *testing.T) {
	tests := []struct {
		name    string
		files   []string
		want    []string // must appear in the Dockerfile
		wantAny map[string][]string
		absent  []string
	}{
		{"npm", []string{"package.json", "package-lock.json"}, []string{"npm ci", "COPY package*.json ./"}, nil, []string{"pnpm", "yarn", "bun"}},
		{"pnpm", []string{"package.json", "pnpm-lock.yaml"}, []string{"COPY package.json pnpm-lock.yaml ./", "corepack enable && pnpm install --frozen-lockfile"}, nil, []string{"npm ci", "RUN npm install", "package*.json"}},
		{"pnpm prefixed", []string{"./package.json", ".\\pnpm-lock.yaml", "src/index.js"}, []string{"pnpm install --frozen-lockfile"}, nil, []string{"npm ci"}},
		{"pnpm workspace", []string{"package.json", "pnpm-lock.yaml", "pnpm-workspace.yaml"}, []string{"COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./"}, nil, nil},
		{"yarn classic", []string{"package.json", "yarn.lock"}, []string{"COPY package.json yarn.lock ./", "yarn install --frozen-lockfile"}, nil, []string{"npm ci", "--immutable"}},
		{"yarn berry", []string{"package.json", "yarn.lock", ".yarnrc.yml"}, []string{"COPY package.json yarn.lock .yarnrc.yml ./", "yarn install --immutable"}, nil, []string{"npm ci", "--frozen-lockfile"}},
		{"bun.lock", []string{"package.json", "bun.lock"}, []string{"COPY package.json bun.lock ./", "bun install --frozen-lockfile"}, nil, []string{"npm ci", "package*.json"}},
		{"bun.lockb", []string{"package.json", "bun.lockb"}, []string{"COPY package.json bun.lockb ./", "bun install --frozen-lockfile"}, nil, []string{"npm ci"}},
		{"no lock", []string{"package.json", "src/index.js"}, []string{"npm install"}, nil, []string{"npm ci"}},
		{"sub-folder lock ignored", []string{"package.json", "app/pnpm-lock.yaml"}, []string{"npm install"}, nil, []string{"pnpm", "npm ci"}},
		{"several locks", []string{"package.json", "package-lock.json", "yarn.lock"}, []string{"yarn install --frozen-lockfile"}, nil, []string{"npm ci"}},
	}
	for _, stack := range nodeStacks {
		for _, tc := range tests {
			t.Run(stack+"/"+tc.name, func(t *testing.T) {
				tpl, ok := ForFiles(stack, tc.files)
				if !ok {
					t.Fatal("unknown stack")
				}
				parses(t, stack, tpl.Dockerfile)
				for _, w := range tc.want {
					if !strings.Contains(tpl.Dockerfile, w) {
						t.Errorf("missing %q in\n%s", w, tpl.Dockerfile)
					}
				}
				for _, a := range tc.absent {
					if strings.Contains(tpl.Dockerfile, a) {
						t.Errorf("unexpected %q in\n%s", a, tpl.Dockerfile)
					}
				}
				if tc.name != "npm" && !strings.Contains(tpl.Readme, "\n- ") {
					t.Errorf("readme lost its list")
				}
			})
		}
	}
}

func TestForFilesNodeDetails(t *testing.T) {
	prod := map[string]string{
		"pnpm-lock.yaml": "pnpm install --frozen-lockfile --prod",
		"yarn.lock":      "yarn install --frozen-lockfile --production",
		"bun.lock":       "bun install --frozen-lockfile --production",
	}
	for lock, want := range prod {
		tpl, _ := ForFiles("node", []string{"package.json", lock})
		if !strings.Contains(tpl.Dockerfile, want) {
			t.Errorf("node + %s: want %q in\n%s", lock, want, tpl.Dockerfile)
		}
	}
	tpl, _ := ForFiles("node", []string{"package.json"})
	if !strings.Contains(tpl.Dockerfile, "RUN npm install --omit=dev") {
		t.Errorf("node without lock:\n%s", tpl.Dockerfile)
	}

	// bun: the node stack runs on the bun image; build stages keep node.
	tpl, _ = ForFiles("node", []string{"package.json", "bun.lock"})
	for _, w := range []string{"FROM oven/bun:1-alpine", "USER bun", `CMD ["bun", "run", "start"]`} {
		if !strings.Contains(tpl.Dockerfile, w) {
			t.Errorf("node + bun: missing %q in\n%s", w, tpl.Dockerfile)
		}
	}
	for _, stack := range []string{"node-spa", "node-static", "nextjs"} {
		tpl, _ = ForFiles(stack, []string{"package.json", "bun.lock"})
		if !strings.Contains(tpl.Dockerfile, "FROM node:24-alpine AS build\nCOPY --from=oven/bun:1-alpine") ||
			!strings.Contains(tpl.Dockerfile, "RUN bun run build") {
			t.Errorf("%s + bun:\n%s", stack, tpl.Dockerfile)
		}
	}
	tpl, _ = ForFiles("nextjs", []string{"package.json", "pnpm-lock.yaml"})
	if !strings.Contains(tpl.Dockerfile, "RUN pnpm build") || strings.Contains(tpl.Dockerfile, "npm run build") {
		t.Errorf("nextjs + pnpm:\n%s", tpl.Dockerfile)
	}
}

func TestForFilesPython(t *testing.T) {
	tests := []struct {
		name   string
		files  []string
		want   []string
		absent []string
	}{
		{"requirements", []string{"requirements.txt", "app.py"}, []string{"pip install -r requirements.txt"}, []string{"astral-sh", "poetry"}},
		{"requirements and pyproject", []string{"requirements.txt", "pyproject.toml"}, []string{"pip install -r requirements.txt"}, []string{"pip install ."}},
		{"uv", []string{"pyproject.toml", "uv.lock"}, []string{"COPY --from=ghcr.io/astral-sh/uv:0.9.5 /uv /bin/uv", "uv sync --frozen --no-dev", "/app/.venv/bin"}, []string{"COPY requirements.txt", "pip install -r"}},
		{"uv beats requirements", []string{"pyproject.toml", "uv.lock", "requirements.txt"}, []string{"uv sync --frozen --no-dev"}, []string{"COPY requirements.txt", "pip install -r"}},
		{"poetry", []string{"pyproject.toml", "poetry.lock"}, []string{"pip install poetry && poetry install --only main --no-root", "POETRY_VIRTUALENVS_CREATE=false"}, []string{"COPY requirements.txt", "pip install -r"}},
		{"pyproject only", []string{"pyproject.toml"}, []string{"COPY . .\nRUN pip install ."}, []string{"COPY requirements.txt", "pip install -r"}},
		{"nothing known", []string{"main.py"}, []string{"pip install -r requirements.txt"}, nil},
	}
	for _, stack := range []string{"python", "django"} {
		for _, tc := range tests {
			t.Run(stack+"/"+tc.name, func(t *testing.T) {
				tpl, _ := ForFiles(stack, tc.files)
				res := parses(t, stack, tpl.Dockerfile)
				_ = res
				for _, w := range tc.want {
					if !strings.Contains(tpl.Dockerfile, w) {
						t.Errorf("missing %q in\n%s", w, tpl.Dockerfile)
					}
				}
				for _, a := range tc.absent {
					if strings.Contains(tpl.Dockerfile, a) {
						t.Errorf("unexpected %q in\n%s", a, tpl.Dockerfile)
					}
				}
				if stack == "django" && !strings.Contains(tpl.Dockerfile, "collectstatic") {
					t.Error("django lost collectstatic")
				}
				// The install must come before collectstatic / the app copy.
				if i, j := strings.Index(tpl.Dockerfile, "COPY . ."), strings.Index(tpl.Dockerfile, "USER app"); i < 0 || j < i {
					t.Errorf("COPY . . must come before USER app:\n%s", tpl.Dockerfile)
				}
			})
		}
	}
}

func TestForFilesLaravel(t *testing.T) {
	tpl, _ := ForFiles("laravel", []string{"composer.json", "composer.lock", "package.json", "pnpm-lock.yaml"})
	parses(t, "laravel", tpl.Dockerfile)
	if !strings.Contains(tpl.Dockerfile, "pnpm install --frozen-lockfile") || !strings.Contains(tpl.Dockerfile, "COPY --from=assets") {
		t.Errorf("laravel + pnpm:\n%s", tpl.Dockerfile)
	}
	tpl, _ = ForFiles("laravel", []string{"composer.json", "composer.lock"})
	parses(t, "laravel", tpl.Dockerfile)
	if strings.Contains(tpl.Dockerfile, "node:") || strings.Contains(tpl.Dockerfile, "assets") || !strings.Contains(tpl.Dockerfile, "# Stage 2: Apache") {
		t.Errorf("laravel without package.json should drop the Vite stage:\n%s", tpl.Dockerfile)
	}
}

func TestForFilesOtherStacksUnchanged(t *testing.T) {
	for _, s := range []string{"static", "go", "php", "generic"} {
		base, _ := Get(s)
		got, ok := ForFiles(s, []string{"package.json", "pnpm-lock.yaml", "uv.lock"})
		if !ok || got != base {
			t.Errorf("%s: ForFiles should equal Get", s)
		}
	}
	// No file list at all: nothing to adapt to.
	base, _ := Get("node")
	if got, _ := ForFiles("node", nil); got != base {
		t.Error("node without files should equal Get")
	}
	if _, ok := ForFiles("ruby", []string{"Gemfile"}); ok {
		t.Error("ruby should be unknown")
	}
}
