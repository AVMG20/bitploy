package deploy

import (
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
		{"dockerfile mode never switches to static", BuildModeDockerfile, []string{"index.html"}, BuildKindDockerfile, ""},
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
			plan, err := PlanBuild(tc.mode, "Dockerfile", "", tc.files, nil, nil)
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
	_, err := PlanBuild(BuildModeAuto, "Dockerfile", "", []string{"notes.txt"}, nil, nil)
	if err == nil || err.Message != "No Dockerfile or index.html found." {
		t.Fatalf("plain miss: %+v", err)
	}
	if err.Next != "get_template" || err.Details["template"] == nil {
		t.Errorf("an AI assistant needs the template: next=%q details=%v", err.Next, err.Details)
	}
	_, err = PlanBuild(BuildModeAuto, "Dockerfile", "", []string{"index.html", "package.json"}, nil, nil)
	if err == nil || !strings.Contains(err.Message, "Node.js") {
		t.Fatalf("project with index.html: %+v", err)
	}
}

func TestPlanBuildCustomDockerfilePath(t *testing.T) {
	plan, err := PlanBuild(BuildModeAuto, "docker/App.Dockerfile", "", []string{"docker/App.Dockerfile", "index.html"}, nil, nil)
	if err != nil || plan.Kind != BuildKindDockerfile {
		t.Fatalf("custom path: %+v %v", plan, err)
	}
	// The default name is not the app's Dockerfile when another path is set.
	plan, err = PlanBuild(BuildModeAuto, "docker/App.Dockerfile", "", []string{"Dockerfile", "index.html"}, nil, nil)
	if err != nil || plan.Kind != BuildKindStatic {
		t.Fatalf("other Dockerfile ignored: %+v %v", plan, err)
	}
}

func TestStaticPlanDockerignore(t *testing.T) {
	plan, err := PlanBuild(BuildModeStatic, "Dockerfile", "", []string{"index.html"}, []byte("secret.txt\r\n"), nil)
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
	_, err := PlanBuild(BuildModeStatic, "Dockerfile", "", []string{"Index.html", "about.html"}, nil, nil)
	if err == nil || err.Code != CodeHomePageUnclear || !strings.Contains(err.Hint, "Rename Index.html to index.html") {
		t.Errorf("static, Index.html: %+v", err)
	}
	_, err = PlanBuild(BuildModeAuto, "Dockerfile", "", []string{"INDEX.HTML", "a.html", "a.css"}, nil, nil)
	if err == nil || err.Code != CodeHomePageUnclear || !strings.Contains(err.Hint, "Rename INDEX.HTML to index.html") {
		t.Errorf("auto, INDEX.HTML: %+v", err)
	}
	_, err = PlanBuild(BuildModeAuto, "Dockerfile", "", []string{"dockerfile", "main.go", "go.mod"}, nil, nil)
	if err == nil || err.Code != CodeNothingToBuild || !strings.Contains(err.Hint, "Rename dockerfile to Dockerfile") {
		t.Errorf("auto, dockerfile: %+v", err)
	}
	// A real index.html still wins over a lowercase dockerfile.
	if plan, err := PlanBuild(BuildModeAuto, "Dockerfile", "", []string{"dockerfile", "index.html"}, nil, nil); err != nil || plan.Kind != BuildKindStatic {
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
		{"dockerfile mode ignores pages", BuildModeDockerfile, "a.html", []string{"a.html"}, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := PlanBuild(tc.mode, "Dockerfile", tc.choice, tc.files, nil, nil)
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
	viteStart := []byte(`{"scripts":{"build":"vite build","start":"vite"},"devDependencies":{"vite":"^7"}}`)
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
		{"vite site whose start script is the dev server", BuildModeAuto, site, viteStart, BuildKindNodeSPA, NodeSPADockerfilePath, ""},
		{"vite without index.html is not a site (SvelteKit)", BuildModeAuto, files, sveltekit, "", "", CodeNothingToBuild},
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
		{"dockerfile mode is unaffected", BuildModeDockerfile, site, vite, BuildKindDockerfile, "", ""},
		{"empty mode is unaffected", "", files, start, BuildKindDockerfile, "", ""},
		{"static mode is unaffected", BuildModeStatic, files, vite, "", "", "static_index_missing"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := PlanBuild(tc.mode, "Dockerfile", "", tc.files, nil, tc.pkg)
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
		plan, err := PlanBuild(BuildModeAuto, "Dockerfile", "", files, nil, both)
		if err != nil {
			t.Fatal(err)
		}
		df := string(plan.Inject[NodeDockerfilePath])
		install, build, env := strings.Index(df, "RUN npm ci\n"), strings.Index(df, "RUN npm run build\n"), strings.Index(df, "ENV NODE_ENV=production")
		if install < 0 || build < install || env < build || strings.Contains(df, "--omit=dev") {
			t.Errorf("want all dependencies, the build, then NODE_ENV=production:\n%s", df)
		}
		plan, _ = PlanBuild(BuildModeAuto, "Dockerfile", "", []string{"package.json", "pnpm-lock.yaml"}, nil, both)
		df = string(plan.Inject[NodeDockerfilePath])
		if !strings.Contains(df, "RUN corepack enable && pnpm install --frozen-lockfile\nCOPY") && !strings.Contains(df, "RUN corepack enable && pnpm install --frozen-lockfile\n\nCOPY . .\nRUN pnpm build\n") {
			t.Errorf("pnpm with a build:\n%s", df)
		}
		plan, _ = PlanBuild(BuildModeAuto, "Dockerfile", "", files, nil, start)
		if df := string(plan.Inject[NodeDockerfilePath]); strings.Contains(df, "npm run build") || !strings.Contains(df, "--omit=dev") {
			t.Errorf("no build script, no build:\n%s", df)
		}
	})

	t.Run("the user's .dockerignore is kept", func(t *testing.T) {
		plan, err := PlanBuild(BuildModeAuto, "Dockerfile", "", files, []byte("secret.txt\r\n"), start)
		if err != nil || !strings.HasPrefix(string(plan.Inject[".dockerignore"]), "secret.txt\n") {
			t.Fatalf("%v %q", err, plan.Inject[".dockerignore"])
		}
	})
}
