package app

// Build-time variables: app variables flagged public or secret also reach
// the image build (docs/decisions.md, "Build-time variables are app variables
// with a build flag"; deploy/buildvars.go for how the build gets them).

import (
	"context"
	"errors"
	"io/fs"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/deploy"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// buildVars decrypts the variables the build of one service gets: the shared
// ones and the service's own (which win), with their ${{alias.VAR}}
// references filled in like at run time.
func (s *Service) buildVars(ctx context.Context, app db.App, service string) (public, secret map[string]string, ae *apperr.Error) {
	rows, err := s.Q.ListServiceEnvVars(ctx, db.ListServiceEnvVarsParams{AppID: app.ID, Service: service})
	if err != nil {
		return nil, nil, apperr.Internal(err)
	}
	hasOwn := map[string]bool{}
	for _, r := range rows {
		if r.Service != "" {
			hasOwn[r.Name] = true
		}
	}
	shared, own := map[string]string{}, map[string]string{}
	flag := map[string]string{}
	for _, r := range rows {
		// The service's own variable replaces a shared one of the same name,
		// build flag included.
		if r.Build == "" || (r.Service == "" && hasOwn[r.Name]) {
			continue
		}
		v, err := s.Crypto.Open(r.ValueEnc, envAADFor(app.ID, r.Service, r.Name))
		if err != nil {
			return nil, nil, apperr.Unprocessable("env_unreadable", "The build variable "+r.Name+" cannot be decrypted.",
				"Set it again with set_env. (This happens when the server's secret key changed.)").WithNext("set_env")
		}
		if r.Service == "" {
			shared[r.Name] = string(v)
		} else {
			own[r.Name] = string(v)
		}
		flag[r.Name] = r.Build
	}
	if app.ProjectID != "" {
		rv := s.newResolver(app)
		if ae := rv.resolveAll(ctx, shared); ae != nil {
			return nil, nil, ae
		}
		if ae := rv.resolveAll(ctx, own); ae != nil {
			return nil, nil, ae
		}
	}
	public, secret = map[string]string{}, map[string]string{}
	for _, m := range []map[string]string{shared, own} {
		for n, v := range m {
			if flag[n] == BuildSecret {
				secret[n] = v
			} else {
				public[n] = v
			}
		}
	}
	return public, secret, nil
}

// minMaskedSecret is the shortest secret value masked in the build log;
// shorter ones would mask ordinary text.
const minMaskedSecret = 4

// applyBuildVars hands the build-time variables of one service to its build:
// public ones as build arguments, secret ones as BuildKit secrets with their
// fingerprint (deploy.BuildSecretsArg). generated says the Dockerfile is one
// of bitploy's templates, which gets the ARG lines and mounts before its
// build script; a user's Dockerfile is only given the fingerprint ARG before
// its secret steps, in a copy outside the build context. cleanup removes
// that copy.
func (p *pipeline) applyBuildVars(ctx context.Context, spec *deploy.BuildSpec, service string, generated bool) (cleanup func(), ae *apperr.Error) {
	cleanup = func() {}
	public, secret, ae := p.s.buildVars(ctx, p.app, service)
	if ae != nil || len(public)+len(secret) == 0 {
		return cleanup, ae
	}
	var names []string
	if len(public) > 0 {
		names = append(names, strings.Join(slices.Sorted(maps.Keys(public)), ", ")+" (public)")
	}
	if len(secret) > 0 {
		names = append(names, strings.Join(slices.Sorted(maps.Keys(secret)), ", ")+" (secret)")
	}
	p.logf("Build-time variables: %s.", strings.Join(names, "; "))
	for _, v := range secret {
		if len(v) >= minMaskedSecret {
			p.buildSecrets = append(p.buildSecrets, v)
		}
	}

	// pass hands the variables to the build; withSecrets also the secrets
	// and their fingerprint, for steps that mount them.
	pass := func(withSecrets bool) {
		if spec.Args == nil {
			spec.Args = map[string]string{}
		}
		for n, v := range public {
			spec.Args[n] = v
		}
		if !withSecrets || len(secret) == 0 {
			return
		}
		spec.Secrets = secret
		parts := []string{p.app.ID, service}
		for _, n := range slices.Sorted(maps.Keys(secret)) {
			parts = append(parts, n, secret[n])
		}
		spec.Args[deploy.BuildSecretsArg] = p.s.Crypto.MAC("build-secrets", parts...)
	}

	df, err := os.ReadFile(spec.Dockerfile)
	if err != nil {
		return cleanup, apperr.Internal(err)
	}
	if generated {
		out, n := deploy.WithTemplateBuildVars(string(df), public, secret)
		if n == 0 {
			p.logf("This build has no build step (a plain website, or a Node.js app without a build script), so the build-time variables are not used. The running app still gets them.")
			return cleanup, nil
		}
		if err := os.WriteFile(spec.Dockerfile, []byte(out), 0o644); err != nil {
			return cleanup, apperr.Internal(err)
		}
		pass(true)
		return cleanup, nil
	}

	prep := deploy.PrepareDockerfileBuildVars(df, public, secret)
	for _, n := range prep.Undeclared {
		p.logf("The Dockerfile has no ARG %s, so the build does not see it. Add ARG %s in the stage that needs it, before the step that uses it.", n, n)
	}
	if len(secret) > 0 && prep.SecretSteps == 0 {
		n := slices.Sorted(maps.Keys(secret))[0]
		p.logf("No RUN step in the Dockerfile mounts a secret, so the secret build variables are not used. Mount one in the step that needs it, like RUN --mount=type=secret,id=%s,env=%s npm run build", n, n)
	}
	pass(prep.Dockerfile != nil)
	if prep.Dockerfile == nil {
		return cleanup, nil
	}
	// A copy outside the build context, so the files the build sees (and
	// their cache) stay as they are. Docker reads <Dockerfile>.dockerignore
	// next to the Dockerfile first, so that one moves along.
	name := service
	if name == "" {
		name = singleService
	}
	copyPath := p.buildDir + "-" + name + ".Dockerfile"
	files := []string{copyPath}
	cleanup = func() {
		for _, f := range files {
			_ = os.Remove(f)
		}
	}
	if err := os.WriteFile(copyPath, prep.Dockerfile, 0o640); err != nil {
		return cleanup, apperr.Internal(err)
	}
	if ign, err := os.ReadFile(spec.Dockerfile + ".dockerignore"); err == nil {
		files = append(files, copyPath+".dockerignore")
		if err := os.WriteFile(copyPath+".dockerignore", ign, 0o640); err != nil {
			return cleanup, apperr.Internal(err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return cleanup, apperr.Internal(err)
	}
	spec.Dockerfile = copyPath
	return cleanup, nil
}

// maskSecrets replaces the values of the build's secrets in a log line.
func maskSecrets(line string, secrets []string) string {
	for _, v := range secrets {
		if strings.Contains(line, v) {
			line = strings.ReplaceAll(line, v, envMask)
		}
	}
	return line
}
