package deploy

// Build-time variables (docs/decisions.md, "Build-time variables are app
// variables with a build flag"). A public one is a --build-arg; a secret one
// a BuildKit secret, mounted only into the RUN steps that ask for it.
//
// BuildKit leaves secrets out of the cache key of a step. On bitploy's one
// shared builder that would let a step that used a secret be reused by
// another app with the same Dockerfile and files (the output built with the
// first app's secret), and keep an old output after the secret changed. So
// every step that mounts a secret also sees the ARG BuildSecretsArg, a keyed
// fingerprint of the app and the secret values (store.Crypto.MAC): different
// per app and per value, and saying nothing about the values.

import (
	"bytes"
	"maps"
	"slices"
	"strings"

	"github.com/AVMG20/bitploy/internal/templates"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
)

// BuildSecretsArg is the build argument with the fingerprint of the build's
// secrets. It starts with BITPLOY_, so no app variable has its name.
const BuildSecretsArg = "BITPLOY_BUILD_SECRETS"

// WithTemplateBuildVars makes the build-time variables reach the build script
// of a Dockerfile bitploy generated (the Node templates): before every line
// that runs the build script it declares an ARG for each public name, so
// Vite, Next.js and others see them as environment variables while they
// build, and the ARG BuildSecretsArg when there are secrets; the RUN line
// mounts each secret as an environment variable of the same name. The install
// step before it sees none of them, so changing a value does not reinstall
// the dependencies. n is the number of build lines changed: 0 when the
// template has no build step (a plain website, a Node app without a build
// script), and then df is returned as it was.
func WithTemplateBuildVars(df string, public, secret map[string]string) (out string, n int) {
	if len(public) == 0 && len(secret) == 0 {
		return df, 0
	}
	build := map[string]bool{}
	for _, l := range templates.BuildScriptLines() {
		build[l] = true
	}
	var head strings.Builder
	head.WriteString("# Build-time variables, set in bitploy.\n")
	for _, name := range slices.Sorted(maps.Keys(public)) {
		head.WriteString("ARG " + name + "\n")
	}
	mounts := ""
	if len(secret) > 0 {
		head.WriteString("ARG " + BuildSecretsArg + "\n")
		for _, name := range slices.Sorted(maps.Keys(secret)) {
			mounts += "--mount=type=secret,id=" + name + ",env=" + name + " "
		}
	}
	lines := strings.Split(df, "\n")
	var b strings.Builder
	for i, l := range lines {
		if build[strings.TrimSpace(l)] {
			n++
			b.WriteString(head.String())
			l = "RUN " + mounts + strings.TrimPrefix(strings.TrimSpace(l), "RUN ")
		}
		b.WriteString(l)
		if i < len(lines)-1 {
			b.WriteByte('\n')
		}
	}
	if n == 0 {
		return df, 0
	}
	return b.String(), n
}

// DockerfileBuildVars is what a user's Dockerfile does with build-time
// variables.
type DockerfileBuildVars struct {
	// Dockerfile is the Dockerfile with the ARG BuildSecretsArg before every
	// RUN step that mounts a secret; nil when there is no such step (or no
	// secret to fingerprint) and the Dockerfile builds as it is.
	Dockerfile []byte
	// SecretSteps is the number of RUN steps that mount a secret.
	SecretSteps int
	// Undeclared are public names the Dockerfile has no ARG for: the build
	// does not see them.
	Undeclared []string
}

// PrepareDockerfileBuildVars reads a user's Dockerfile for the build-time
// variables. It never changes what a step runs: it only adds the ARG line
// that keeps the cache of secret steps per app and value (see the top of this
// file). A Dockerfile that does not parse is left as it is (the build reports
// it).
func PrepareDockerfileBuildVars(df []byte, public, secret map[string]string) DockerfileBuildVars {
	var out DockerfileBuildVars
	if len(public) == 0 && len(secret) == 0 {
		return out
	}
	res, err := parser.Parse(bytes.NewReader(df))
	if err != nil {
		return out
	}
	declared := map[string]bool{}
	var before []int // 1-based lines that get the ARG in front
	for _, node := range res.AST.Children {
		switch strings.ToLower(node.Value) {
		case "arg":
			for n := node.Next; n != nil; n = n.Next {
				name, _, _ := strings.Cut(n.Value, "=")
				declared[name] = true
			}
		case "run":
			for _, f := range node.Flags {
				low := strings.ToLower(f)
				if strings.HasPrefix(low, "--mount") && strings.Contains(low, "secret") {
					out.SecretSteps++
					before = append(before, node.StartLine)
					break
				}
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(public)) {
		if !declared[name] {
			out.Undeclared = append(out.Undeclared, name)
		}
	}
	if len(secret) == 0 || len(before) == 0 {
		return out
	}
	lines := bytes.SplitAfter(df, []byte("\n"))
	var b bytes.Buffer
	next := 0
	for i, l := range lines {
		for next < len(before) && before[next] == i+1 {
			b.WriteString("ARG " + BuildSecretsArg + "\n")
			next++
		}
		b.Write(l)
	}
	out.Dockerfile = b.Bytes()
	return out
}
