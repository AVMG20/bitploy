package deploy

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/templates"
)

// detectCase is one row of testdata/detect_cases.json: every rule of auto mode's
// Node detection and of the modes node and static_build. web/tests/detect.test.ts
// reads the same file for the preview in the browser, so the two stay in agreement.
type detectCase struct {
	Name    string   `json:"name"`
	Mode    string   `json:"mode"`
	Files   []string `json:"files"`
	Package *struct {
		Scripts json.RawMessage `json:"scripts"` // a list of names, or a map of name to command
		Deps    []string        `json:"deps"`
		Main    string          `json:"main"`
	} `json:"package"`
	RawPackage string `json:"raw_package"`
	NextConfig string `json:"next_config"`
	Output     string `json:"output"`
	Expect     struct {
		Type       string `json:"type"`
		Output     string `json:"output"`
		SPA        bool   `json:"spa"`
		Unsure     bool   `json:"unsure"`
		Warn       bool   `json:"warn"`
		None       bool   `json:"none"`
		Code       string `json:"code"`
		Dockerfile bool   `json:"dockerfile"`
	} `json:"expect"`
	DF     []string `json:"df"`
	NotDF  []string `json:"not_df"`
	WhyHas string   `json:"why_has"`
}

func (c detectCase) packageJSON(t *testing.T) []byte {
	t.Helper()
	if c.RawPackage != "" {
		return []byte(c.RawPackage)
	}
	if c.Package == nil {
		return nil
	}
	scripts := map[string]string{}
	var names []string
	if json.Unmarshal(c.Package.Scripts, &names) == nil {
		for _, n := range names {
			scripts[n] = "x"
			if n == "start" && slices.Contains(c.Package.Deps, "next") {
				scripts[n] = "next start"
			}
		}
	} else if err := json.Unmarshal(c.Package.Scripts, &scripts); err != nil {
		t.Fatal(err)
	}
	deps := map[string]string{}
	for _, d := range c.Package.Deps {
		deps[d] = "^1"
	}
	b, _ := json.Marshal(map[string]any{"scripts": scripts, "dependencies": deps, "main": c.Package.Main})
	return b
}

func TestPlanBuildNodeDetection(t *testing.T) {
	raw, err := os.ReadFile("testdata/detect_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []detectCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 50 {
		t.Fatalf("only %d cases", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			extra := map[string]string{}
			for _, n := range NextConfigNames {
				extra[n] = c.NextConfig
			}
			read := readOf(nil, c.packageJSON(t), extra)
			plan, perr := PlanBuild(c.Mode, "Dockerfile", "", c.Output, c.Files, read)
			want := c.Expect
			switch {
			case want.None:
				if perr == nil || perr.Code != want.Code {
					t.Fatalf("want error %s, got plan %s err %v", want.Code, plan.Kind, perr)
				}
				if perr.Message == "" || perr.Hint == "" {
					t.Errorf("error needs a message and a hint: %+v", perr)
				}
				return
			case perr != nil:
				t.Fatalf("unexpected error: %+v", perr)
			case want.Dockerfile:
				if plan.Kind != BuildKindDockerfile {
					t.Fatalf("kind = %q, want dockerfile", plan.Kind)
				}
				return
			}
			isStatic := plan.Kind == BuildKindNodeSPA || plan.Kind == BuildKindNodeStatic
			got := BuildModeNode
			if isStatic {
				got = BuildModeStaticBuild
			}
			if (plan.Kind != BuildKindNode && !isStatic) || got != want.Type {
				t.Fatalf("kind = %q, want type %s (%s)", plan.Kind, want.Type, plan.Why)
			}
			if plan.Unsure != want.Unsure {
				t.Errorf("unsure = %v, want %v", plan.Unsure, want.Unsure)
			}
			if warned := strings.Contains(plan.Why, "Add @sveltejs/adapter-node"); warned != want.Warn {
				t.Errorf("warns = %v, want %v: %s", warned, want.Warn, plan.Why)
			}
			if isStatic {
				if plan.OutputDir != want.Output || plan.SPA != want.SPA {
					t.Errorf("output %q spa %v, want %q %v", plan.OutputDir, plan.SPA, want.Output, want.SPA)
				}
				if (plan.Kind == BuildKindNodeSPA) != want.SPA {
					t.Errorf("kind %s does not match spa=%v", plan.Kind, want.SPA)
				}
			}
			df := string(plan.Inject[plan.DockerfilePath])
			for _, w := range c.DF {
				if !strings.Contains(df, w) {
					t.Errorf("Dockerfile lacks %q:\n%s", w, df)
				}
			}
			for _, no := range c.NotDF {
				if strings.Contains(df, no) {
					t.Errorf("Dockerfile has %q:\n%s", no, df)
				}
			}
			if c.WhyHas != "" && !strings.Contains(plan.Why, c.WhyHas) {
				t.Errorf("Why lacks %q: %s", c.WhyHas, plan.Why)
			}
			if plan.Why == "" {
				t.Error("no explanation for the build log")
			}
			ign := string(plan.Inject[".dockerignore"])
			if !strings.Contains(ign, plan.DockerfilePath) {
				t.Errorf(".dockerignore does not list %s", plan.DockerfilePath)
			}
			if isStatic {
				if !strings.Contains(ign, plan.OutputDir) {
					t.Errorf(".dockerignore does not list the output folder %s", plan.OutputDir)
				}
				if fallback := strings.Contains(df, "$uri/ /index.html"); fallback != want.SPA {
					t.Errorf("index.html fallback = %v, want %v:\n%s", fallback, want.SPA, df)
				}
			}
		})
	}
}

func TestStaticKindsAreStatic(t *testing.T) {
	for kind, want := range map[string]bool{
		BuildKindStatic: true, BuildKindNodeSPA: true, BuildKindNodeStatic: true,
		BuildKindNode: false, BuildKindDockerfile: false, "": false,
	} {
		if IsStaticKind(kind) != want {
			t.Errorf("IsStaticKind(%q) = %v", kind, !want)
		}
	}
	for _, m := range []string{BuildModeStatic, BuildModeStaticBuild} {
		if !AlwaysStaticMode(m) {
			t.Errorf("%s is always static", m)
		}
	}
	for _, m := range []string{BuildModeAuto, BuildModeNode, BuildModeDockerfile} {
		if AlwaysStaticMode(m) {
			t.Errorf("%s is not always static", m)
		}
	}
}

func TestCleanOutputDir(t *testing.T) {
	for in, want := range map[string]string{
		"": "", "  ": "", "dist": "dist", "./dist/": "dist", "build/client": "build/client",
		"docs/.vitepress/dist": "docs/.vitepress/dist", `web\out`: "web/out", "_site": "_site",
	} {
		got, err := CleanOutputDir(in)
		if err != nil || got != want {
			t.Errorf("CleanOutputDir(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"/dist", "../dist", "a/../b", "a/..", ".", "./", "a b", "a;b", "dist\nRUN x", "$(x)", "a//b", "~/x", "dist*", strings.Repeat("a", 201)} {
		if got, err := CleanOutputDir(bad); err == nil && got != "" {
			t.Errorf("CleanOutputDir(%q) = %q, want an error", bad, got)
		}
	}
}

// The output-folder variants of the static templates must pass the Dockerfile
// checks like every other template, for every package manager.
func TestStaticBuildVariantsPassTheChecks(t *testing.T) {
	variants := map[string][]string{
		"npm": {"package.json", "package-lock.json"}, "npm, no lock": {"package.json"}, "pnpm": {"package.json", "pnpm-lock.yaml"},
		"yarn": {"package.json", "yarn.lock"}, "bun": {"package.json", "bun.lock"}, "no file list": nil,
	}
	for _, out := range []string{"dist", "build", "out", "_site", "web/dist", "docs/.vitepress/dist"} {
		for _, spa := range []bool{true, false} {
			for name, files := range variants {
				tpl, ok := templates.StaticBuild(files, out, spa)
				if !ok {
					t.Fatalf("%s %v %s: StaticBuild did not apply", out, spa, name)
				}
				checkTemplate(t, out+"/"+name, tpl, files, false)
			}
		}
	}
	if _, ok := templates.StaticBuild(nil, "../x", true); ok {
		t.Error("an unsafe output folder must be refused")
	}
}
