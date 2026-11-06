package deploy

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/templates"
	"github.com/moby/buildkit/frontend/dockerfile/instructions"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
)

func TestBuildArgsSecretsNeverCarryValues(t *testing.T) {
	b := BuildSpec{ContextDir: "/src", Dockerfile: "/src/Dockerfile", Tag: "bitploy/x:1",
		Args:    map[string]string{"VITE_URL": "https://api.example.com", BuildSecretsArg: "f00d"},
		Secrets: map[string]string{"SENTRY_AUTH_TOKEN": "s3cr3t-token", "NPM_TOKEN": "npm-xyz"}}
	args := b.BuildArgs()
	got := strings.Join(args, " ")
	for _, v := range []string{"s3cr3t-token", "npm-xyz"} {
		if strings.Contains(got, v) {
			t.Fatalf("secret value on the command line: %s", got)
		}
	}
	want := "--build-arg BITPLOY_BUILD_SECRETS=f00d --build-arg VITE_URL=https://api.example.com " +
		"--secret id=NPM_TOKEN,env=BITPLOY_BUILD_SECRET_0 --secret id=SENTRY_AUTH_TOKEN,env=BITPLOY_BUILD_SECRET_1 /src"
	if !strings.HasSuffix(got, want) {
		t.Fatalf("args:\n%s\nwant suffix:\n%s", got, want)
	}
	env := b.SecretEnv()
	if !slices.Equal(env, []string{"BITPLOY_BUILD_SECRET_0=npm-xyz", "BITPLOY_BUILD_SECRET_1=s3cr3t-token"}) {
		t.Fatalf("env %v", env)
	}
	if len((BuildSpec{}).SecretEnv()) != 0 {
		t.Fatal("env without secrets")
	}
}

// parseRuns returns the RUN commands of a Dockerfile with their mounts and
// the ARG names declared in each stage before them.
func parseRuns(t *testing.T, df string) (runs []*instructions.RunCommand, argsBefore [][]string) {
	t.Helper()
	res, err := parser.Parse(strings.NewReader(df))
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, df)
	}
	stages, _, err := instructions.Parse(res.AST, nil)
	if err != nil {
		t.Fatalf("instructions: %v\n%s", err, df)
	}
	for _, st := range stages {
		var args []string
		for _, c := range st.Commands {
			switch c := c.(type) {
			case *instructions.ArgCommand:
				for _, a := range c.Args {
					args = append(args, a.Key)
				}
			case *instructions.RunCommand:
				if err := c.Expand(func(w string) (string, error) { return w, nil }); err != nil {
					t.Fatalf("mount options: %v", err)
				}
				runs = append(runs, c)
				argsBefore = append(argsBefore, slices.Clone(args))
			}
		}
	}
	return runs, argsBefore
}

func TestWithTemplateBuildVarsNodeTemplates(t *testing.T) {
	public := map[string]string{"VITE_SUPABASE_URL": "https://x.supabase.co", "NEXT_PUBLIC_A": "a"}
	secret := map[string]string{"SENTRY_AUTH_TOKEN": "tok"}
	pkg := func(lock string) []string { return []string{"package.json", lock, "index.html", "src/main.ts"} }
	cases := []struct {
		name  string
		tpl   func() (templates.Template, bool)
		build string // the command of the build step
	}{
		{"node-spa npm", func() (templates.Template, bool) {
			return templates.StaticBuild(pkg("package-lock.json"), "dist", true)
		}, "npm run build"},
		{"node-static pnpm", func() (templates.Template, bool) { return templates.StaticBuild(pkg("pnpm-lock.yaml"), "dist", false) }, "pnpm build"},
		{"node-spa yarn", func() (templates.Template, bool) { return templates.StaticBuild(pkg("yarn.lock"), "build", true) }, "yarn build"},
		{"node-spa bun", func() (templates.Template, bool) { return templates.StaticBuild(pkg("bun.lock"), "dist", true) }, "bun run build"},
		{"node with build", func() (templates.Template, bool) { return templates.NodeWithBuild(pkg("package-lock.json")) }, "npm run build"},
		{"nextjs standalone", func() (templates.Template, bool) {
			return templates.NextJS(pkg("package-lock.json"), true, true)
		}, "npm run build"},
		{"nextjs start", func() (templates.Template, bool) {
			return templates.NextJS(pkg("package-lock.json"), false, true)
		}, "npm run build"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tpl, ok := c.tpl()
			if !ok {
				t.Fatal("no template")
			}
			out, n := WithTemplateBuildVars(tpl.Dockerfile, public, secret)
			if n != 1 {
				t.Fatalf("changed %d build lines:\n%s", n, out)
			}
			runs, before := parseRuns(t, out)
			found := false
			for i, r := range runs {
				text := strings.Join(r.CmdLine, " ")
				mounts := instructions.GetMounts(r)
				if text != c.build {
					if len(mounts) > 0 {
						t.Errorf("step %q mounts secrets", text)
					}
					if !found && slices.Contains(before[i], "VITE_SUPABASE_URL") {
						t.Errorf("step %q before the build sees the build args (its cache would follow them)", text)
					}
					continue
				}
				found = true
				for _, a := range []string{"NEXT_PUBLIC_A", "VITE_SUPABASE_URL", BuildSecretsArg} {
					if !slices.Contains(before[i], a) {
						t.Errorf("no ARG %s before the build step:\n%s", a, out)
					}
				}
				if len(mounts) != 1 || mounts[0].Type != instructions.MountTypeSecret || mounts[0].CacheID != "SENTRY_AUTH_TOKEN" ||
					mounts[0].Env == nil || *mounts[0].Env != "SENTRY_AUTH_TOKEN" {
					t.Errorf("build step mounts %+v", *mounts[0])
				}
			}
			if !found {
				t.Fatalf("no build step %q:\n%s", c.build, out)
			}
			for _, v := range []string{"https://x.supabase.co", "tok"} {
				if strings.Contains(out, v) {
					t.Errorf("a value is in the Dockerfile: %s", v)
				}
			}
		})
	}
}

func TestWithTemplateBuildVarsPublicOnlyHasNoSecretArg(t *testing.T) {
	tpl, _ := templates.StaticBuild([]string{"package.json", "package-lock.json"}, "dist", true)
	out, n := WithTemplateBuildVars(tpl.Dockerfile, map[string]string{"VITE_A": "1"}, nil)
	if n != 1 || strings.Contains(out, BuildSecretsArg) || strings.Contains(out, "--mount") || !strings.Contains(out, "ARG VITE_A\nRUN npm run build\n") {
		t.Fatalf("%d\n%s", n, out)
	}
}

func TestWithTemplateBuildVarsNoBuildStep(t *testing.T) {
	for _, stack := range []string{"static", "node"} {
		tpl, _ := templates.ForFiles(stack, []string{"package.json", "package-lock.json"})
		out, n := WithTemplateBuildVars(tpl.Dockerfile, map[string]string{"A": "1"}, map[string]string{"B": "2"})
		if n != 0 || out != tpl.Dockerfile {
			t.Fatalf("%s: changed %d lines", stack, n)
		}
	}
	tpl, _ := templates.StaticBuild([]string{"package.json"}, "dist", true)
	if out, n := WithTemplateBuildVars(tpl.Dockerfile, nil, nil); n != 0 || out != tpl.Dockerfile {
		t.Fatal("changed without variables")
	}
}

func TestPrepareDockerfileBuildVars(t *testing.T) {
	df := "FROM node:22 AS build\n" +
		"ARG VITE_A=default\n" +
		"COPY . .\n" +
		"RUN npm ci\n" +
		"RUN --mount=type=secret,id=TOKEN,env=TOKEN \\\n" +
		"    npm run build\n" +
		"FROM nginx:1.27\n" +
		"RUN --mount=type=secret,id=OTHER cat /run/secrets/OTHER > /dev/null\n" +
		"COPY --from=build /app/dist /usr/share/nginx/html\n"
	got := PrepareDockerfileBuildVars([]byte(df), map[string]string{"VITE_A": "x", "VITE_B": "y"}, map[string]string{"TOKEN": "t"})
	if got.SecretSteps != 2 {
		t.Fatalf("secret steps %d", got.SecretSteps)
	}
	if !slices.Equal(got.Undeclared, []string{"VITE_B"}) {
		t.Fatalf("undeclared %v", got.Undeclared)
	}
	want := strings.Replace(df, "RUN --mount=type=secret,id=TOKEN", "ARG "+BuildSecretsArg+"\nRUN --mount=type=secret,id=TOKEN", 1)
	want = strings.Replace(want, "RUN --mount=type=secret,id=OTHER", "ARG "+BuildSecretsArg+"\nRUN --mount=type=secret,id=OTHER", 1)
	if string(got.Dockerfile) != want {
		t.Fatalf("Dockerfile:\n%s\nwant:\n%s", got.Dockerfile, want)
	}
	runs, before := parseRuns(t, string(got.Dockerfile))
	for i, r := range runs {
		has := slices.Contains(before[i], BuildSecretsArg)
		if secretStep := len(instructions.GetMounts(r)) > 0; has != secretStep {
			t.Errorf("step %q: fingerprint ARG %v, mounts a secret %v", strings.Join(r.CmdLine, " "), has, secretStep)
		}
	}
}

func TestPrepareDockerfileBuildVarsLeavesOthersAlone(t *testing.T) {
	df := []byte("FROM node:22\nARG VITE_A\nRUN npm run build\n")
	got := PrepareDockerfileBuildVars(df, map[string]string{"VITE_A": "1"}, map[string]string{"S": "2"})
	if got.Dockerfile != nil || got.SecretSteps != 0 || len(got.Undeclared) != 0 {
		t.Fatalf("%+v", got)
	}
	if got := PrepareDockerfileBuildVars([]byte("FROM node:22\nRUN --mount=type=secret,id=S x\n"), map[string]string{"A": "1"}, nil); got.Dockerfile != nil {
		t.Fatal("rewritten without secrets")
	}
	crlf := []byte("FROM node:22\r\nRUN --mount=type=secret,id=S,env=S npm run build\r\n")
	got = PrepareDockerfileBuildVars(crlf, nil, map[string]string{"S": "2"})
	if !bytes.Equal(got.Dockerfile, []byte("FROM node:22\r\nARG "+BuildSecretsArg+"\nRUN --mount=type=secret,id=S,env=S npm run build\r\n")) {
		t.Fatalf("%q", got.Dockerfile)
	}
}
