package deploy

// Build-time variables (docs/decisions.md, "Every variable reaches the
// build"). Every app variable reaches the image build: bitploy's own Node
// builds mount them all as BuildKit secrets into the step that runs the
// build script; a user's Dockerfile gets the ones it declares as ARG as build
// arguments and the ones its RUN steps mount as secrets.
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
	"encoding/csv"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/AVMG20/bitploy/internal/templates"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
)

// BuildSecretsArg is the build argument with the fingerprint of the build's
// secrets. It starts with BITPLOY_, so no app variable has its name.
const BuildSecretsArg = "BITPLOY_BUILD_SECRETS"

// templateEnvSkip are variables not mounted into the build script of
// bitploy's own builds: the shell of the build step needs its own.
var templateEnvSkip = map[string]bool{"PATH": true, "HOME": true, "PWD": true}

// TemplateBuildEnv reports whether bitploy's own builds give the variable
// name to the build script.
func TemplateBuildEnv(name string) bool { return !templateEnvSkip[name] }

// WithTemplateBuildVars makes the app's variables reach the build script of
// a Dockerfile bitploy generated (the Node templates): before every line that
// runs the build script it declares the ARG BuildSecretsArg, and the RUN line
// mounts each variable as a BuildKit secret in an environment variable of the
// same name, so Vite, Next.js and others read them from process.env while
// they build and nothing of them is in the image or its history. The install
// step before it sees none of them, so changing a value does not reinstall
// the dependencies. n is the number of build lines: 0 when the template has
// no build step (a plain website, a Node app without a build script). With no
// names, or n 0, df is returned as it was.
func WithTemplateBuildVars(df string, names []string) (out string, n int) {
	build := map[string]bool{}
	for _, l := range templates.BuildScriptLines() {
		build[l] = true
	}
	var mounts strings.Builder
	for _, name := range slices.Sorted(slices.Values(names)) {
		mounts.WriteString("--mount=type=secret,id=" + name + ",env=" + name + " ")
	}
	lines := strings.Split(df, "\n")
	var b strings.Builder
	for i, l := range lines {
		if build[strings.TrimSpace(l)] {
			n++
			if mounts.Len() > 0 {
				b.WriteString("# The app's variables, set in bitploy.\nARG " + BuildSecretsArg + "\n")
				l = "RUN " + mounts.String() + strings.TrimPrefix(strings.TrimSpace(l), "RUN ")
			}
		}
		b.WriteString(l)
		if i < len(lines)-1 {
			b.WriteByte('\n')
		}
	}
	if n == 0 || mounts.Len() == 0 {
		return df, n
	}
	return b.String(), n
}

// DockerfileBuildVars is what a user's Dockerfile reads of the build-time
// variables.
type DockerfileBuildVars struct {
	// Args are the names the Dockerfile declares with ARG, in any stage.
	Args []string
	// Secrets are the ids of the secrets its RUN steps mount.
	Secrets []string
	// AnySecret is true when a RUN step mounts a secret whose id is not
	// plain text (like id=$NAME): it may be any variable.
	AnySecret bool
	// SecretSteps are the 1-based lines of the RUN steps that mount a secret.
	SecretSteps []int
}

// Reads reports whether the build reads the variable name.
func (d DockerfileBuildVars) Reads(name string) bool {
	return d.AnySecret || slices.Contains(d.Args, name) || slices.Contains(d.Secrets, name)
}

// ReadDockerfileBuildVars reads which build arguments a user's Dockerfile
// declares and which secrets it mounts. A Dockerfile that does not parse
// reads nothing (the build reports it).
func ReadDockerfileBuildVars(df []byte) DockerfileBuildVars {
	var out DockerfileBuildVars
	res, err := parser.Parse(bytes.NewReader(df))
	if err != nil {
		return out
	}
	args, secrets := map[string]bool{}, map[string]bool{}
	for _, node := range res.AST.Children {
		switch strings.ToLower(node.Value) {
		case "arg":
			for n := node.Next; n != nil; n = n.Next {
				name, _, _ := strings.Cut(n.Value, "=")
				args[name] = true
			}
		case "run":
			step := false
			for _, f := range node.Flags {
				id, ok := secretMountID(f)
				if !ok {
					continue
				}
				step = true
				if id == "" {
					out.AnySecret = true
				} else {
					secrets[id] = true
				}
			}
			if step {
				out.SecretSteps = append(out.SecretSteps, node.StartLine)
			}
		}
	}
	out.Args = slices.Sorted(maps.Keys(args))
	out.Secrets = slices.Sorted(maps.Keys(secrets))
	return out
}

// secretMountID reads one flag of a RUN step: ok when it mounts a secret,
// with the secret's id ("" when it is not plain text). The id defaults to the
// base name of the target, like in BuildKit.
func secretMountID(flag string) (id string, ok bool) {
	v, found := strings.CutPrefix(flag, "--mount=")
	if !found {
		return "", false
	}
	fields, err := csv.NewReader(strings.NewReader(v)).Read()
	if err != nil {
		return "", strings.Contains(strings.ToLower(v), "secret")
	}
	var typ, target string
	for _, f := range fields {
		k, val, _ := strings.Cut(f, "=")
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "type":
			typ = strings.ToLower(val)
		case "id":
			id = val
		case "target", "dst", "destination":
			target = val
		}
	}
	if typ != "secret" {
		return "", false
	}
	if id == "" && target != "" {
		id = path.Base(target)
	}
	if strings.ContainsAny(id, "$") {
		id = ""
	}
	return id, true
}

// WithSecretFingerprint returns df with the ARG BuildSecretsArg before every
// RUN step that mounts a secret (see the top of this file); nil when there is
// no such step. It never changes what a step runs.
func (d DockerfileBuildVars) WithSecretFingerprint(df []byte) []byte {
	if len(d.SecretSteps) == 0 {
		return nil
	}
	lines := bytes.SplitAfter(df, []byte("\n"))
	var b bytes.Buffer
	next := 0
	for i, l := range lines {
		for next < len(d.SecretSteps) && d.SecretSteps[next] == i+1 {
			b.WriteString("ARG " + BuildSecretsArg + "\n")
			next++
		}
		b.Write(l)
	}
	return b.Bytes()
}

// ReservedBuildArg reports a build argument that BuildKit itself gives a
// meaning (the platform, BUILDKIT_SYNTAX, SOURCE_DATE_EPOCH): an app variable
// of that name is never passed as one, even when the Dockerfile declares it.
func ReservedBuildArg(name string) bool {
	switch name {
	case "TARGETPLATFORM", "TARGETOS", "TARGETARCH", "TARGETVARIANT",
		"BUILDPLATFORM", "BUILDOS", "BUILDARCH", "BUILDVARIANT", "SOURCE_DATE_EPOCH":
		return true
	}
	return strings.HasPrefix(name, "BUILDKIT_")
}
