package templates

import (
	"strings"
	"testing"
)

func TestValidOutputDir(t *testing.T) {
	for _, ok := range []string{"dist", "build", "out", "_site", "build/client", "docs/.vitepress/dist", "a-b_c.d"} {
		if !ValidOutputDir(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", ".", "..", "/dist", "dist/", "a//b", "a/../b", "a b", "a\nb", "a;b", "$x", "a\\b", strings.Repeat("a", 201)} {
		if ValidOutputDir(bad) {
			t.Errorf("%q should be refused", bad)
		}
	}
}

func TestStaticBuild(t *testing.T) {
	tpl, ok := StaticBuild([]string{"package.json", "pnpm-lock.yaml"}, "build/client", true)
	if !ok {
		t.Fatal("not applied")
	}
	for _, want := range []string{"COPY --from=build /app/build/client /usr/share/nginx/html", "pnpm install --frozen-lockfile", "$uri/ /index.html", `("build/client")`} {
		if !strings.Contains(tpl.Dockerfile, want) {
			t.Errorf("Dockerfile lacks %q:\n%s", want, tpl.Dockerfile)
		}
	}
	if strings.Contains(tpl.Dockerfile, "/app/dist") || !strings.Contains(tpl.Dockerignore, "\nbuild/client\n") {
		t.Errorf("dist is still in the Dockerfile or the folder is not ignored:\n%s\n%s", tpl.Dockerfile, tpl.Dockerignore)
	}
	parses(t, "static build", tpl.Dockerfile)

	site, _ := StaticBuild(nil, "out", false)
	if !strings.Contains(site.Dockerfile, "=404") || strings.Contains(site.Dockerfile, "/index.html;") {
		t.Errorf("multi-page variant:\n%s", site.Dockerfile)
	}
	// "dist" is already ignored: no second line.
	dist, _ := StaticBuild(nil, "dist", true)
	if strings.Count(dist.Dockerignore, "dist\n") != 1 {
		t.Errorf("dist listed twice:\n%s", dist.Dockerignore)
	}
	// The base templates are untouched.
	base, _ := Get("node-spa")
	if !strings.Contains(base.Dockerfile, "/app/dist") {
		t.Error("the base template changed")
	}
	for _, bad := range []string{"", "../x", "a b"} {
		if _, ok := StaticBuild(nil, bad, true); ok {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestNextJS(t *testing.T) {
	std, ok := NextJS([]string{"package.json", "package-lock.json"}, true, true)
	if !ok || !strings.Contains(std.Dockerfile, ".next/standalone") {
		t.Fatalf("standalone: %v\n%s", ok, std.Dockerfile)
	}
	fb, ok := NextJS([]string{"package.json", "package-lock.json"}, false, true)
	if !ok {
		t.Fatal("fallback not applied")
	}
	build, chown, user := strings.Index(fb.Dockerfile, "RUN npm run build"), strings.Index(fb.Dockerfile, "RUN chown -R node:node /app/.next"), strings.Index(fb.Dockerfile, "USER node")
	if build < 0 || chown < build || user < chown || !strings.Contains(fb.Dockerfile, `CMD ["npm", "start"]`) || strings.Contains(fb.Dockerfile, "standalone") {
		t.Errorf("fallback order: build %d chown %d user %d\n%s", build, chown, user, fb.Dockerfile)
	}
	parses(t, "next fallback", fb.Dockerfile)
	noStart, ok := NextJS(nil, false, false)
	if !ok || !strings.Contains(noStart.Dockerfile, `CMD ["npx", "next", "start"]`) {
		t.Errorf("no start script: %v\n%s", ok, noStart.Dockerfile)
	}
	bun, ok := NextJS([]string{"package.json", "bun.lock"}, false, true)
	if !ok || !strings.Contains(bun.Dockerfile, "RUN chown -R bun:bun /app/.next") {
		t.Errorf("bun: %v\n%s", ok, bun.Dockerfile)
	}
}

func TestWithStart(t *testing.T) {
	tpl, _ := ForFiles("node", []string{"package.json", "package-lock.json"})
	got, ok := WithStart(tpl, []string{"node", "build"}, "HOST=0.0.0.0")
	if !ok || !strings.Contains(got.Dockerfile, "ENV HOST=0.0.0.0\nCMD [\"node\", \"build\"]\n") || strings.Contains(got.Dockerfile, "npm") && strings.Contains(got.Dockerfile, `"npm", "start"`) {
		t.Errorf("%v\n%s", ok, got.Dockerfile)
	}
	parses(t, "with start", got.Dockerfile)
	envOnly, ok := WithStart(tpl, nil, "A=b")
	if !ok || !strings.Contains(envOnly.Dockerfile, "ENV A=b\nCMD [\"npm\", \"start\"]") {
		t.Errorf("env only: %v\n%s", ok, envOnly.Dockerfile)
	}
	bun, _ := ForFiles("node", []string{"package.json", "bun.lock"})
	got, ok = WithStart(bun, []string{"node", "build"})
	if !ok || !strings.Contains(got.Dockerfile, `CMD ["bun", "build"]`) {
		t.Errorf("bun: %v\n%s", ok, got.Dockerfile)
	}
	for _, bad := range [][]string{{"node", "a b"}, {"node", "x;rm"}, {"node", `"`}} {
		if _, ok := WithStart(tpl, bad); ok {
			t.Errorf("%q must be refused", bad)
		}
	}
	if _, ok := WithStart(tpl, nil, "no equals"); ok {
		t.Error("an env entry must be NAME=value")
	}
	static, _ := Get("static")
	if _, ok := WithStart(static, []string{"node"}); ok {
		t.Error("a template without a start line is not changed")
	}
}
