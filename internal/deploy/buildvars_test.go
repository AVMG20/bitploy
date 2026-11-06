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

// Every variable reaches the build script of bitploy's own Node builds as a
// secret mounted in its environment: never a build argument (its value would
// be in the image history), never in the steps before it.
func TestWithTemplateBuildVarsNodeTemplates(t *testing.T) {
	names := []string{"VITE_SUPABASE_URL", "NEXT_PUBLIC_A", "DATABASE_URL"}
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
			out, n := WithTemplateBuildVars(tpl.Dockerfile, names)
			if n != 1 {
				t.Fatalf("changed %d build lines:\n%s", n, out)
			}
			runs, before := parseRuns(t, out)
			found := false
			for i, r := range runs {
				text := strings.Join(r.CmdLine, " ")
				mounts := instructions.GetMounts(r)
				if text != c.build {
					if slices.ContainsFunc(mounts, func(m *instructions.Mount) bool { return m.Type == instructions.MountTypeSecret }) {
						t.Errorf("step %q mounts secrets", text)
					}
					if !found && slices.Contains(before[i], BuildSecretsArg) {
						t.Errorf("step %q before the build sees the fingerprint (its cache would follow the values)", text)
					}
					continue
				}
				found = true
				if !slices.Contains(before[i], BuildSecretsArg) {
					t.Errorf("no ARG %s before the build step:\n%s", BuildSecretsArg, out)
				}
				for _, a := range names {
					if slices.Contains(before[i], a) {
						t.Errorf("%s is a build argument, so its value would be in the image history", a)
					}
				}
				var got []string
				for _, m := range mounts {
					if m.Type != instructions.MountTypeSecret || m.Env == nil || *m.Env != m.CacheID {
						t.Errorf("build step mounts %+v", *m)
					}
					got = append(got, m.CacheID)
				}
				if want := slices.Sorted(slices.Values(names)); !slices.Equal(got, want) {
					t.Errorf("mounts %v, want %v", got, want)
				}
			}
			if !found {
				t.Fatalf("no build step %q:\n%s", c.build, out)
			}
		})
	}
}

func TestWithTemplateBuildVarsNoBuildStep(t *testing.T) {
	for _, stack := range []string{"static", "node"} {
		tpl, _ := templates.ForFiles(stack, []string{"package.json", "package-lock.json"})
		out, n := WithTemplateBuildVars(tpl.Dockerfile, []string{"A", "B"})
		if n != 0 || out != tpl.Dockerfile {
			t.Fatalf("%s: changed %d lines", stack, n)
		}
	}
	// Without variables the Dockerfile stays as it is; the build step is still counted.
	tpl, _ := templates.StaticBuild([]string{"package.json"}, "dist", true)
	if out, n := WithTemplateBuildVars(tpl.Dockerfile, nil); n != 1 || out != tpl.Dockerfile {
		t.Fatalf("changed without variables (%d)", n)
	}
}

func TestReadDockerfileBuildVars(t *testing.T) {
	df := "ARG BASE=node:22\n" +
		"FROM $BASE AS build\n" +
		"ARG VITE_A=default\n" +
		"COPY . .\n" +
		"RUN npm ci\n" +
		"RUN --mount=type=secret,id=TOKEN,env=TOKEN \\\n" +
		"    npm run build\n" +
		"FROM nginx:1.27\n" +
		"RUN --mount=type=secret,target=/run/secrets/OTHER cat /run/secrets/OTHER > /dev/null\n" +
		"RUN --mount=type=cache,target=/root/.cache true\n" +
		"COPY --from=build /app/dist /usr/share/nginx/html\n"
	got := ReadDockerfileBuildVars([]byte(df))
	if !slices.Equal(got.Args, []string{"BASE", "VITE_A"}) || !slices.Equal(got.Secrets, []string{"OTHER", "TOKEN"}) || got.AnySecret {
		t.Fatalf("%+v", got)
	}
	if !got.Reads("VITE_A") || !got.Reads("TOKEN") || got.Reads("DATABASE_URL") {
		t.Fatal("Reads")
	}
	if !slices.Equal(got.SecretSteps, []int{6, 9}) {
		t.Fatalf("secret steps %v", got.SecretSteps)
	}
	out := got.WithSecretFingerprint([]byte(df))
	want := strings.Replace(df, "RUN --mount=type=secret,id=TOKEN", "ARG "+BuildSecretsArg+"\nRUN --mount=type=secret,id=TOKEN", 1)
	want = strings.Replace(want, "RUN --mount=type=secret,target", "ARG "+BuildSecretsArg+"\nRUN --mount=type=secret,target", 1)
	if string(out) != want {
		t.Fatalf("Dockerfile:\n%s\nwant:\n%s", out, want)
	}
	// Every secret step sees the fingerprint; npm ci before them does not.
	runs, before := parseRuns(t, string(out))
	for i, r := range runs {
		has := slices.Contains(before[i], BuildSecretsArg)
		secretStep := slices.ContainsFunc(instructions.GetMounts(r), func(m *instructions.Mount) bool { return m.Type == instructions.MountTypeSecret })
		if secretStep && !has || strings.Join(r.CmdLine, " ") == "npm ci" && has {
			t.Errorf("step %q: fingerprint ARG %v, mounts a secret %v", strings.Join(r.CmdLine, " "), has, secretStep)
		}
	}
}

func TestReadDockerfileBuildVarsEdges(t *testing.T) {
	plain := ReadDockerfileBuildVars([]byte("FROM node:22\nARG VITE_A\nRUN npm run build\n"))
	if plain.WithSecretFingerprint(nil) != nil || len(plain.Secrets) != 0 || !slices.Equal(plain.Args, []string{"VITE_A"}) {
		t.Fatalf("%+v", plain)
	}
	// An id from a variable may be any of them.
	if got := ReadDockerfileBuildVars([]byte("FROM node:22\nARG S\nRUN --mount=type=secret,id=$S,env=X true\n")); !got.AnySecret || !got.Reads("ANYTHING") {
		t.Fatalf("%+v", got)
	}
	crlf := []byte("FROM node:22\r\nRUN --mount=type=secret,id=S,env=S npm run build\r\n")
	got := ReadDockerfileBuildVars(crlf)
	if !bytes.Equal(got.WithSecretFingerprint(crlf), []byte("FROM node:22\r\nARG "+BuildSecretsArg+"\nRUN --mount=type=secret,id=S,env=S npm run build\r\n")) {
		t.Fatalf("%q", got.WithSecretFingerprint(crlf))
	}
}

func TestReservedBuildArg(t *testing.T) {
	for _, n := range []string{"TARGETARCH", "BUILDPLATFORM", "BUILDKIT_SYNTAX", "SOURCE_DATE_EPOCH"} {
		if !ReservedBuildArg(n) {
			t.Errorf("%s not reserved", n)
		}
	}
	if ReservedBuildArg("VITE_A") || ReservedBuildArg("BUILD_ID") || !TemplateBuildEnv("NODE_OPTIONS") || TemplateBuildEnv("PATH") {
		t.Error("reserved too much")
	}
}
