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
			plan, err := PlanBuild(tc.mode, "Dockerfile", "", tc.files, nil)
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
	_, err := PlanBuild(BuildModeAuto, "Dockerfile", "", []string{"notes.txt"}, nil)
	if err == nil || err.Message != "No Dockerfile or index.html found." {
		t.Fatalf("plain miss: %+v", err)
	}
	if err.Next != "get_template" || err.Details["template"] == nil {
		t.Errorf("an AI assistant needs the template: next=%q details=%v", err.Next, err.Details)
	}
	_, err = PlanBuild(BuildModeAuto, "Dockerfile", "", []string{"index.html", "package.json"}, nil)
	if err == nil || !strings.Contains(err.Message, "Node.js") {
		t.Fatalf("project with index.html: %+v", err)
	}
}

func TestPlanBuildCustomDockerfilePath(t *testing.T) {
	plan, err := PlanBuild(BuildModeAuto, "docker/App.Dockerfile", "", []string{"docker/App.Dockerfile", "index.html"}, nil)
	if err != nil || plan.Kind != BuildKindDockerfile {
		t.Fatalf("custom path: %+v %v", plan, err)
	}
	// The default name is not the app's Dockerfile when another path is set.
	plan, err = PlanBuild(BuildModeAuto, "docker/App.Dockerfile", "", []string{"Dockerfile", "index.html"}, nil)
	if err != nil || plan.Kind != BuildKindStatic {
		t.Fatalf("other Dockerfile ignored: %+v %v", plan, err)
	}
}

func TestStaticPlanDockerignore(t *testing.T) {
	plan, err := PlanBuild(BuildModeStatic, "Dockerfile", "", []string{"index.html"}, []byte("secret.txt\r\n"))
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
	_, err := PlanBuild(BuildModeStatic, "Dockerfile", "", []string{"Index.html", "about.html"}, nil)
	if err == nil || err.Code != CodeHomePageUnclear || !strings.Contains(err.Hint, "Rename Index.html to index.html") {
		t.Errorf("static, Index.html: %+v", err)
	}
	_, err = PlanBuild(BuildModeAuto, "Dockerfile", "", []string{"INDEX.HTML", "a.html", "a.css"}, nil)
	if err == nil || err.Code != CodeHomePageUnclear || !strings.Contains(err.Hint, "Rename INDEX.HTML to index.html") {
		t.Errorf("auto, INDEX.HTML: %+v", err)
	}
	_, err = PlanBuild(BuildModeAuto, "Dockerfile", "", []string{"dockerfile", "main.go", "go.mod"}, nil)
	if err == nil || err.Code != CodeNothingToBuild || !strings.Contains(err.Hint, "Rename dockerfile to Dockerfile") {
		t.Errorf("auto, dockerfile: %+v", err)
	}
	// A real index.html still wins over a lowercase dockerfile.
	if plan, err := PlanBuild(BuildModeAuto, "Dockerfile", "", []string{"dockerfile", "index.html"}, nil); err != nil || plan.Kind != BuildKindStatic {
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
			plan, err := PlanBuild(tc.mode, "Dockerfile", tc.choice, tc.files, nil)
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
