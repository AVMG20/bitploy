package deploy

import (
	"github.com/AVMG20/bitploy/internal/apperr"
	"strings"
	"testing"
)

func TestPlanBuild(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		files    []string
		wantKind string
		wantCode string
	}{
		{"dockerfile mode, Dockerfile", BuildModeDockerfile, []string{"Dockerfile", "index.html"}, BuildKindDockerfile, ""},
		{"dockerfile mode serves a website without a Dockerfile", BuildModeDockerfile, []string{"index.html"}, BuildKindStatic, ""},
		{"dockerfile mode builds a Dockerfile it has", BuildModeDockerfile, []string{"index.html", "Dockerfile"}, BuildKindDockerfile, ""},
		{"dockerfile mode still wants a Dockerfile for a server", BuildModeDockerfile, []string{"main.go", "go.mod"}, BuildKindDockerfile, ""},
		{"empty mode is dockerfile mode", "", []string{"index.html"}, BuildKindDockerfile, ""},
		{"auto, Dockerfile wins over index.html", BuildModeAuto, []string{"Dockerfile", "index.html"}, BuildKindDockerfile, ""},
		{"auto, Dockerfile only", BuildModeAuto, []string{"Dockerfile", "app.py"}, BuildKindDockerfile, ""},
		{"auto, index.html only", BuildModeAuto, []string{"index.html", "css/site.css", "img/a.png"}, BuildKindStatic, ""},
		{"auto, index.html with a project marker", BuildModeAuto, []string{"index.html", "package.json", "src/main.js"}, "", CodeNothingToBuild},
		{"auto, neither", BuildModeAuto, []string{"README.md", "notes.txt"}, "", CodeNothingToBuild},
		{"auto, no files", BuildModeAuto, nil, "", CodeNothingToBuild},
		{"auto, index.html only in a subfolder", BuildModeAuto, []string{"site/index.html"}, "", CodeNothingToBuild},
		{"static, index.html", BuildModeStatic, []string{"index.html"}, BuildKindStatic, ""},
		{"static ignores a Dockerfile", BuildModeStatic, []string{"Dockerfile", "index.html"}, BuildKindStatic, ""},
		{"static without index.html", BuildModeStatic, []string{"Dockerfile", "app.py"}, "", "static_index_missing"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := planWith(tc.mode, "Dockerfile", "", tc.files, nil, nil)
			if tc.wantCode != "" {
				if err == nil || err.Code != tc.wantCode {
					t.Fatalf("want error %s, got plan %+v err %v", tc.wantCode, plan, err)
				}
				if err.Message == "" || err.Hint == "" {
					t.Errorf("error needs a message and a hint: %+v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if plan.Kind != tc.wantKind {
				t.Fatalf("kind = %q, want %q", plan.Kind, tc.wantKind)
			}
			if plan.Kind == BuildKindStatic {
				if !strings.Contains(string(plan.Inject[StaticDockerfilePath]), "nginx") {
					t.Errorf("static plan does not carry the nginx Dockerfile: %v", plan.Inject)
				}
				if plan.Why == "" {
					t.Error("static plan has no explanation for the build log")
				}
			} else if len(plan.Inject) != 0 {
				t.Errorf("a Dockerfile build injects nothing, got %v", plan.Inject)
			}
		})
	}
}

func TestPlanBuildMessages(t *testing.T) {
	_, err := planWith(BuildModeAuto, "Dockerfile", "", []string{"notes.txt"}, nil, nil)
	if err == nil || err.Message != "No Dockerfile or index.html found." {
		t.Fatalf("plain miss: %+v", err)
	}
	if err.Next != "get_template" || err.Details["template"] == nil {
		t.Errorf("an AI assistant needs the template: next=%q details=%v", err.Next, err.Details)
	}
	_, err = planWith(BuildModeAuto, "Dockerfile", "", []string{"index.html", "package.json"}, nil, nil)
	if err == nil || !strings.Contains(err.Message, "Node.js") {
		t.Fatalf("project with index.html: %+v", err)
	}
}

func TestPlanBuildCustomDockerfilePath(t *testing.T) {
	plan, err := planWith(BuildModeAuto, "docker/App.Dockerfile", "", []string{"docker/App.Dockerfile", "index.html"}, nil, nil)
	if err != nil || plan.Kind != BuildKindDockerfile {
		t.Fatalf("custom path: %+v %v", plan, err)
	}
	// The default name is not the app's Dockerfile when another path is set.
	plan, err = planWith(BuildModeAuto, "docker/App.Dockerfile", "", []string{"Dockerfile", "index.html"}, nil, nil)
	if err != nil || plan.Kind != BuildKindStatic {
		t.Fatalf("other Dockerfile ignored: %+v %v", plan, err)
	}
}

func TestStaticPlanDockerignore(t *testing.T) {
	plan, err := planWith(BuildModeStatic, "Dockerfile", "", []string{"index.html"}, []byte("secret.txt\r\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ign := string(plan.Inject[".dockerignore"])
	for _, want := range []string{"secret.txt", ".git", "node_modules", ".env", StaticDockerfilePath} {
		if !strings.Contains(ign, want) {
			t.Errorf(".dockerignore lacks %q:\n%s", want, ign)
		}
	}
}

func TestPlanBuildCaseMismatch(t *testing.T) {
	// One page is served whatever its name, so only several pages without a
	// choice ask for the rename.
	_, err := planWith(BuildModeStatic, "Dockerfile", "", []string{"Index.html", "about.html"}, nil, nil)
	if err == nil || err.Code != CodeHomePageUnclear || !strings.Contains(err.Hint, "Rename Index.html to index.html") {
		t.Errorf("static, Index.html: %+v", err)
	}
	_, err = planWith(BuildModeAuto, "Dockerfile", "", []string{"INDEX.HTML", "a.html", "a.css"}, nil, nil)
	if err == nil || err.Code != CodeHomePageUnclear || !strings.Contains(err.Hint, "Rename INDEX.HTML to index.html") {
		t.Errorf("auto, INDEX.HTML: %+v", err)
	}
	_, err = planWith(BuildModeAuto, "Dockerfile", "", []string{"dockerfile", "main.go", "go.mod"}, nil, nil)
	if err == nil || err.Code != CodeNothingToBuild || !strings.Contains(err.Hint, "Rename dockerfile to Dockerfile") {
		t.Errorf("auto, dockerfile: %+v", err)
	}
	// A real index.html still wins over a lowercase dockerfile.
	if plan, err := planWith(BuildModeAuto, "Dockerfile", "", []string{"dockerfile", "index.html"}, nil, nil); err != nil || plan.Kind != BuildKindStatic {
		t.Errorf("auto, dockerfile + index.html: %+v %v", plan, err)
	}
}

func TestPlanBuildHomePage(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		choice   string
		files    []string
		wantHome string // "" with wantCode "" means a plain index.html site
		wantCode string
	}{
		{"one page with a random name", BuildModeAuto, "", []string{"Planning tool v3.html"}, "Planning tool v3.html", ""},
		{"one .htm page", BuildModeAuto, "", []string{"report.HTM", "logo.png"}, "report.HTM", ""},
		{"one page, static mode", BuildModeStatic, "", []string{"tool.html", "Dockerfile"}, "tool.html", ""},
		{"Index.html alone is served", BuildModeAuto, "", []string{"Index.html"}, "Index.html", ""},
		{"spaces, unicode and quotes", BuildModeAuto, "", []string{`Plan "v3" – café's $(rm -rf).html`}, `Plan "v3" – café's $(rm -rf).html`, ""},
		{"a page in a subfolder does not count", BuildModeAuto, "", []string{"docs/page.html"}, "", CodeNothingToBuild},
		{"a page next to package.json is not static", BuildModeAuto, "", []string{"tool.html", "package.json"}, "", CodeNothingToBuild},
		{"index.html wins over the choice", BuildModeAuto, "b.html", []string{"index.html", "b.html"}, "", ""},
		{"several pages, no choice", BuildModeAuto, "", []string{"b.html", "a.html"}, "", CodeHomePageUnclear},
		{"several pages, chosen", BuildModeAuto, "b.html", []string{"b.html", "a.html"}, "b.html", ""},
		{"several pages, chosen one with odd characters", BuildModeStatic, "Ünïcode 'x'.htm", []string{"a.html", "Ünïcode 'x'.htm"}, "Ünïcode 'x'.htm", ""},
		{"several pages, chosen one is gone", BuildModeAuto, "old.html", []string{"b.html", "a.html"}, "", CodeHomePageMissing},
		{"one page, chosen one is gone: the only page", BuildModeAuto, "old.html", []string{"new.html"}, "new.html", ""},
		{"a choice outside the top folder is ignored", BuildModeAuto, "sub/x.html", []string{"sub/x.html", "a.html", "b.html"}, "", CodeHomePageUnclear},
		{"a choice that is not a page is ignored", BuildModeAuto, "notes.txt", []string{"notes.txt", "a.html"}, "a.html", ""},
		{"dockerfile mode serves the one page too", BuildModeDockerfile, "a.html", []string{"a.html"}, "a.html", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := planWith(tc.mode, "Dockerfile", tc.choice, tc.files, nil, nil)
			if tc.wantCode != "" {
				if err == nil || err.Code != tc.wantCode {
					t.Fatalf("want %s, got %+v %v", tc.wantCode, plan, err)
				}
				if err.Message == "" || err.Hint == "" {
					t.Errorf("error needs a message and a hint: %+v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if plan.HomePage != tc.wantHome {
				t.Fatalf("home page = %q, want %q", plan.HomePage, tc.wantHome)
			}
			if tc.wantHome != "" {
				if plan.Kind != BuildKindStatic || !strings.Contains(plan.Why, tc.wantHome) {
					t.Errorf("plan = %+v", plan)
				}
				// The name never reaches the generated files.
				for name, b := range plan.Inject {
					if strings.Contains(string(b), tc.wantHome) {
						t.Errorf("%s holds the page name:\n%s", name, b)
					}
				}
			}
		})
	}
}

func TestPlanBuildNodeTemplates(t *testing.T) {
	vite := []byte(`{"scripts":{"build":"vite build","dev":"vite"},"devDependencies":{"vite":"^5.0.0"}}`)
	viteDep := []byte(`{"scripts":{"build":"vite build"},"dependencies":{"vite":"^5.0.0"}}`)
	start := []byte(`{"scripts":{"start":"node server.js"},"dependencies":{"express":"^4"}}`)
	both := []byte(`{"scripts":{"build":"tsc","start":"node dist/server.js"},"dependencies":{"express":"^4"}}`)
	buildNoVite := []byte(`{"scripts":{"build":"webpack"},"devDependencies":{"webpack":"5"}}`)
	neither := []byte(`{"scripts":{"test":"jest"}}`)
	files := []string{"package.json", "package-lock.json", "src/main.js"}
	site := append([]string{"index.html"}, files...)
	sveltekit := []byte(`{"scripts":{"build":"vite build","dev":"vite dev"},"devDependencies":{"vite":"^7","@sveltejs/kit":"^2"}}`)
	viteStart := []byte(`{"scripts":{"build":"vite build","start":"node server.js"},"devDependencies":{"vite":"^7"}}`)
	viteDevStart := []byte(`{"scripts":{"build":"vite build","start":"vite preview"},"devDependencies":{"vite":"^7"}}`)
	next := []byte(`{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"^16"}}`)
	laravel := []byte(`{"scripts":{"build":"vite build","dev":"vite"},"devDependencies":{"vite":"^7","laravel-vite-plugin":"^2"}}`)

	tests := []struct {
		name     string
		mode     string
		files    []string
		pkg      []byte
		wantKind string
		wantFile string
		wantCode string
	}{
		{"vite and build (devDependencies)", BuildModeAuto, site, vite, BuildKindNodeSPA, NodeSPADockerfilePath, ""},
		{"vite and build (dependencies)", BuildModeAuto, site, viteDep, BuildKindNodeSPA, NodeSPADockerfilePath, ""},
		{"vite with a start script is a guess: node", BuildModeAuto, site, viteStart, BuildKindNode, NodeDockerfilePath, ""},
		{"vite whose start script is the Vite server is a website", BuildModeAuto, site, viteDevStart, BuildKindNodeSPA, NodeSPADockerfilePath, ""},
		{"vite without index.html is not a site (SvelteKit is a node app)", BuildModeAuto, files, sveltekit, BuildKindNode, NodeDockerfilePath, ""},
		{"vite app with a server is built and started", BuildModeAuto, files, viteStart, BuildKindNode, NodeDockerfilePath, ""},
		{"next.js with next.config is built and started", BuildModeAuto, append([]string{"next.config.mjs"}, files...), next, BuildKindNode, NodeDockerfilePath, ""},
		{"laravel is not a node project", BuildModeAuto, append([]string{"artisan", "composer.json", "index.html"}, files...), laravel, "", "", CodeNothingToBuild},
		{"start script", BuildModeAuto, files, start, BuildKindNode, NodeDockerfilePath, ""},
		{"build without vite and a start script", BuildModeAuto, files, both, BuildKindNode, NodeDockerfilePath, ""},
		{"build without vite or start", BuildModeAuto, files, buildNoVite, "", "", CodeNothingToBuild},
		{"neither script", BuildModeAuto, files, neither, "", "", CodeNothingToBuild},
		{"no package.json content", BuildModeAuto, files, nil, "", "", CodeNothingToBuild},
		{"malformed package.json", BuildModeAuto, files, []byte(`{"scripts": {`), "", "", CodeNothingToBuild},
		{"scripts is not an object", BuildModeAuto, files, []byte(`{"scripts":"build"}`), "", "", CodeNothingToBuild},
		{"Dockerfile present", BuildModeAuto, append([]string{"Dockerfile"}, site...), vite, BuildKindDockerfile, "", ""},
		{"index.html with package.json is still not a website", BuildModeAuto, append([]string{"index.html"}, files...), neither, "", "", CodeNothingToBuild},
		{"go.mod wins over package.json", BuildModeAuto, append([]string{"go.mod"}, files...), start, "", "", CodeNothingToBuild},
		{"dockerfile mode builds a static front end itself", BuildModeDockerfile, site, vite, BuildKindNodeSPA, NodeSPADockerfilePath, ""},
		{"dockerfile mode keeps a Node server on the Dockerfile path", BuildModeDockerfile, files, start, BuildKindDockerfile, "", ""},
		{"empty mode is unaffected", "", files, start, BuildKindDockerfile, "", ""},
		{"static mode is unaffected", BuildModeStatic, files, vite, "", "", "static_index_missing"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := planWith(tc.mode, "Dockerfile", "", tc.files, nil, tc.pkg)
			if tc.wantCode != "" {
				if err == nil || err.Code != tc.wantCode {
					t.Fatalf("want error %s, got plan %+v err %v", tc.wantCode, plan, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if plan.Kind != tc.wantKind {
				t.Fatalf("kind = %q, want %q", plan.Kind, tc.wantKind)
			}
			if tc.wantFile == "" {
				if len(plan.Inject) != 0 || plan.DockerfilePath != "" {
					t.Errorf("a Dockerfile build injects nothing, got %v", plan.Inject)
				}
				return
			}
			if plan.DockerfilePath != tc.wantFile || len(plan.Inject[tc.wantFile]) == 0 {
				t.Errorf("DockerfilePath %q, inject %v", plan.DockerfilePath, plan.Inject)
			}
			if plan.Why == "" {
				t.Error("no explanation for the build log")
			}
			ign := string(plan.Inject[".dockerignore"])
			for _, want := range []string{"node_modules", ".env", tc.wantFile} {
				if !strings.Contains(ign, want) {
					t.Errorf(".dockerignore lacks %q:\n%s", want, ign)
				}
			}
			if !strings.Contains(string(plan.Inject[tc.wantFile]), "EXPOSE") {
				t.Errorf("injected Dockerfile has no EXPOSE")
			}
		})
	}

	t.Run("a build script runs before the start script", func(t *testing.T) {
		plan, err := planWith(BuildModeAuto, "Dockerfile", "", files, nil, both)
		if err != nil {
			t.Fatal(err)
		}
		df := string(plan.Inject[NodeDockerfilePath])
		install, build, prune := strings.Index(df, "RUN npm ci\n"), strings.Index(df, "RUN npm run build\n"), strings.Index(df, "RUN npm prune --omit=dev")
		run, env, copyApp := strings.Index(df, "\nFROM node:22-bookworm-slim\n"), strings.Index(df, "ENV NODE_ENV=production"), strings.Index(df, "COPY --from=build /app ./\n")
		if install < 0 || build < install || prune < build || run < prune || env < run || copyApp < env || strings.Count(df, "COPY . .") != 1 {
			t.Errorf("want all dependencies, the build, the prune, then a second stage with the result:\n%s", df)
		}
		plan, _ = planWith(BuildModeAuto, "Dockerfile", "", []string{"package.json", "pnpm-lock.yaml"}, nil, both)
		df = string(plan.Inject[NodeDockerfilePath])
		if !strings.Contains(df, "RUN corepack enable && pnpm install --frozen-lockfile\nCOPY") && !strings.Contains(df, "RUN corepack enable && pnpm install --frozen-lockfile\n\nCOPY . .\nRUN pnpm build\n") {
			t.Errorf("pnpm with a build:\n%s", df)
		}
		plan, _ = planWith(BuildModeAuto, "Dockerfile", "", files, nil, start)
		if df := string(plan.Inject[NodeDockerfilePath]); strings.Contains(df, "npm run build") || !strings.Contains(df, "--omit=dev") {
			t.Errorf("no build script, no build:\n%s", df)
		}
	})

	t.Run("the image keeps only what the app runs", func(t *testing.T) {
		nuxt := []byte(`{"scripts":{"build":"nuxt build","dev":"nuxt dev"},"dependencies":{"nuxt":"^4.0.0","vue":"^3.5.0"}}`)
		plan, err := planWith(BuildModeAuto, "Dockerfile", "", files, nil, nuxt)
		if err != nil {
			t.Fatal(err)
		}
		df := string(plan.Inject[NodeDockerfilePath])
		if !strings.Contains(df, "COPY --from=build /app/.output ./.output\n") || strings.Contains(df, "COPY --from=build /app ./") ||
			!strings.Contains(df, `CMD ["node", ".output/server/index.mjs"]`) || strings.Contains(df, "prune") {
			t.Errorf("nuxt: only .output:\n%s", df)
		}
		nuxt2 := []byte(`{"scripts":{"build":"nuxt build","start":"nuxt start"},"dependencies":{"nuxt":"^2.17.0"}}`)
		plan, _ = planWith(BuildModeAuto, "Dockerfile", "", files, nil, nuxt2)
		if df := string(plan.Inject[NodeDockerfilePath]); strings.Contains(df, ".output") || !strings.Contains(df, `CMD ["npm", "start"]`) {
			t.Errorf("nuxt 2 runs its start script:\n%s", df)
		}

		for _, c := range []struct {
			name  string
			pkg   string
			prune bool
		}{
			{"tsc then node", `{"scripts":{"build":"tsc","start":"node dist/index.js"},"dependencies":{"express":"5"},"devDependencies":{"typescript":"5","tsx":"4","@types/node":"22"}}`, true},
			{"start runs tsx", `{"scripts":{"build":"tsc --noEmit","start":"tsx src/index.ts"},"dependencies":{"express":"5"},"devDependencies":{"typescript":"5","tsx":"4"}}`, false},
			{"start runs a script that runs vite", `{"scripts":{"build":"vite build","serve":"vite preview --host","start":"npm run serve"},"dependencies":{"express":"5"},"devDependencies":{"vite":"7"}}`, false},
			{"prestart runs ts-node", `{"scripts":{"build":"tsc","prestart":"ts-node migrate.ts","start":"node dist/index.js"},"dependencies":{"express":"5"},"devDependencies":{"ts-node":"10"}}`, false},
			{"react-router-serve from a scoped dev dependency", `{"scripts":{"build":"react-router build","start":"react-router-serve ./build/server/index.js"},"dependencies":{"react":"19"},"devDependencies":{"@react-router/dev":"7","@react-router/serve":"7"}}`, false},
			{"sveltekit node build", `{"scripts":{"build":"vite build","start":"node build"},"devDependencies":{"@sveltejs/kit":"2","@sveltejs/adapter-node":"5","vite":"7"}}`, true},
			{"node with an env file", `{"scripts":{"build":"tsc","start":"NODE_ENV=production node --env-file=.env dist/index.js"},"dependencies":{"express":"5"},"devDependencies":{"typescript":"5"}}`, true},
			{"node -r ts-node/register", `{"scripts":{"build":"tsc --noEmit","start":"node -r ts-node/register src/index.ts"},"dependencies":{"express":"5"},"devDependencies":{"ts-node":"10"}}`, false},
			{"node --import=tsx", `{"scripts":{"build":"tsc --noEmit","start":"node --import=tsx src/index.ts"},"dependencies":{"express":"5"},"devDependencies":{"tsx":"4"}}`, false},
			{"tsc in the start script", `{"scripts":{"build":"tsc","start":"tsc && node dist/index.js"},"dependencies":{"express":"5"},"devDependencies":{"typescript":"5"}}`, false},
			{"dotenv-cli", `{"scripts":{"build":"tsc","start":"dotenv -e .env -- node dist/index.js"},"dependencies":{"express":"5"},"devDependencies":{"dotenv-cli":"8"}}`, false},
			{"nest start", `{"scripts":{"build":"nest build","start":"nest start"},"dependencies":{"@nestjs/core":"11"},"devDependencies":{"@nestjs/cli":"11"}}`, false},
			{"react router without a start script, server a dependency", `{"scripts":{"build":"react-router build"},"dependencies":{"@react-router/serve":"7","react":"19"},"devDependencies":{"@react-router/dev":"7"}}`, true},
		} {
			plan, err := planWith(BuildModeAuto, "Dockerfile", "", files, nil, []byte(c.pkg))
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			df := string(plan.Inject[NodeDockerfilePath])
			if got := strings.Contains(df, "npm prune --omit=dev"); got != c.prune {
				t.Errorf("%s: prune = %v, want %v\n%s", c.name, got, c.prune, df)
			}
			if !strings.Contains(df, "COPY --from=build /app ./\n") {
				t.Errorf("%s: no second stage:\n%s", c.name, df)
			}
		}

		// next start loads next.config.ts with typescript, a dev dependency.
		next := []byte(`{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16","react":"19"},"devDependencies":{"typescript":"5"}}`)
		plan, _ = planWith(BuildModeAuto, "Dockerfile", "", files, nil, next)
		if df := string(plan.Inject[NodeDockerfilePath]); strings.Contains(df, "prune") || !strings.Contains(df, "RUN rm -rf .next/cache") {
			t.Errorf("next keeps its dev dependencies, not the cache:\n%s", df)
		}
	})

	t.Run("the user's .dockerignore is kept", func(t *testing.T) {
		plan, err := planWith(BuildModeAuto, "Dockerfile", "", files, []byte("secret.txt\r\n"), start)
		if err != nil || !strings.HasPrefix(string(plan.Inject[".dockerignore"]), "secret.txt\n") {
			t.Fatalf("%v %q", err, plan.Inject[".dockerignore"])
		}
	})
}

// planWith calls PlanBuild with the content of .dockerignore and package.json
// (and any extra files) as the source has them.
func planWith(mode, dockerfilePath, home string, files []string, ignore, pkg []byte, extra ...map[string]string) (BuildPlan, *apperr.Error) {
	return PlanBuild(mode, dockerfilePath, home, "", files, readOf(ignore, pkg, extra...))
}

func readOf(ignore, pkg []byte, extra ...map[string]string) func(string) []byte {
	return func(rel string) []byte {
		switch rel {
		case ".dockerignore":
			return ignore
		case "package.json":
			return pkg
		}
		for _, m := range extra {
			if s, ok := m[rel]; ok {
				return []byte(s)
			}
		}
		return nil
	}
}
