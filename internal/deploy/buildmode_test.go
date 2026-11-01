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
			plan, err := PlanBuild(tc.mode, "Dockerfile", tc.files, nil)
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
	_, err := PlanBuild(BuildModeAuto, "Dockerfile", []string{"notes.txt"}, nil)
	if err == nil || err.Message != "No Dockerfile or index.html found." {
		t.Fatalf("plain miss: %+v", err)
	}
	if err.Next != "get_template" || err.Details["template"] == nil {
		t.Errorf("an AI assistant needs the template: next=%q details=%v", err.Next, err.Details)
	}
	_, err = PlanBuild(BuildModeAuto, "Dockerfile", []string{"index.html", "package.json"}, nil)
	if err == nil || !strings.Contains(err.Message, "Node.js") {
		t.Fatalf("project with index.html: %+v", err)
	}
}

func TestPlanBuildCustomDockerfilePath(t *testing.T) {
	plan, err := PlanBuild(BuildModeAuto, "docker/App.Dockerfile", []string{"docker/App.Dockerfile", "index.html"}, nil)
	if err != nil || plan.Kind != BuildKindDockerfile {
		t.Fatalf("custom path: %+v %v", plan, err)
	}
	// The default name is not the app's Dockerfile when another path is set.
	plan, err = PlanBuild(BuildModeAuto, "docker/App.Dockerfile", []string{"Dockerfile", "index.html"}, nil)
	if err != nil || plan.Kind != BuildKindStatic {
		t.Fatalf("other Dockerfile ignored: %+v %v", plan, err)
	}
}

func TestStaticPlanDockerignore(t *testing.T) {
	plan, err := PlanBuild(BuildModeStatic, "Dockerfile", []string{"index.html"}, []byte("secret.txt\r\n"))
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
