package app

// Build-time variables: every app variable also reaches the image build
// (docs/decisions.md, "Every variable reaches the build"; deploy/buildvars.go
// for how the build gets them).

import (
	"context"
	"errors"
	"io/fs"
	"maps"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/deploy"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// buildVars decrypts the variables the build of one service gets: the shared
// ones and the service's own (which win), with their ${{alias.VAR}}
// references filled in like at run time.
func (s *Service) buildVars(ctx context.Context, app db.App, service string) (map[string]string, *apperr.Error) {
	rows, err := s.Q.ListServiceEnvVars(ctx, db.ListServiceEnvVarsParams{AppID: app.ID, Service: service})
	if err != nil {
		return nil, apperr.Internal(err)
	}
	shared, own := map[string]string{}, map[string]string{}
	for _, r := range rows {
		v, err := s.Crypto.Open(r.ValueEnc, envAADFor(app.ID, r.Service, r.Name))
		if err != nil {
			return nil, apperr.Unprocessable("env_unreadable", "The environment variable "+r.Name+" cannot be decrypted.",
				"Set it again with set_env. (This happens when the server's secret key changed.)").WithNext("set_env")
		}
		if r.Service == "" {
			shared[r.Name] = string(v)
		} else {
			own[r.Name] = string(v)
		}
	}
	if app.ProjectID != "" {
		rv := s.newResolver(app)
		if ae := rv.resolveAll(ctx, shared); ae != nil {
			return nil, ae
		}
		if ae := rv.resolveAll(ctx, own); ae != nil {
			return nil, ae
		}
	}
	maps.Copy(shared, own)
	return shared, nil
}

// maxBuildVarBytes caps the values one build gets together: they are in the
// docker process's environment and command line, which Linux limits to a
// few MB. Above it the largest values stay out of the build.
const maxBuildVarBytes = 1 << 20

// fitBuildVars returns names without the largest values while the values of
// names are over maxBuildVarBytes together, and the names it left out.
func fitBuildVars(vars map[string]string, names []string) (kept, dropped []string) {
	total := 0
	for _, n := range names {
		total += len(n) + len(vars[n])
	}
	if total <= maxBuildVarBytes {
		return names, nil
	}
	bySize := slices.Clone(names)
	sort.SliceStable(bySize, func(i, j int) bool { return len(vars[bySize[i]]) > len(vars[bySize[j]]) })
	out := map[string]bool{}
	for _, n := range bySize {
		if total <= maxBuildVarBytes {
			break
		}
		out[n] = true
		total -= len(n) + len(vars[n])
		dropped = append(dropped, n)
	}
	for _, n := range names {
		if !out[n] {
			kept = append(kept, n)
		}
	}
	sort.Strings(dropped)
	return kept, dropped
}

// maskedInBuildLog reports a value the build log masks: one that may be a
// secret. Short values, plain words (production) and the values front ends
// put into the pages anyway (VITE_*, NEXT_PUBLIC_*) would only hide
// ordinary text.
func maskedInBuildLog(name, v string) bool {
	if len(v) < 6 || strings.IndexFunc(v, func(r rune) bool { return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') }) < 0 {
		return false
	}
	for _, p := range publicBuildPrefixes {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	return true
}

// publicBuildPrefixes are the names front-end builds put into the files
// they build.
var publicBuildPrefixes = []string{"VITE_", "NEXT_PUBLIC_", "PUBLIC_", "REACT_APP_", "NUXT_PUBLIC_", "GATSBY_", "EXPO_PUBLIC_", "VUE_APP_"}

// applyBuildVars hands the app's variables to the build of one service and
// records which ones the build reads (deployments.build_reads), so a later
// change to one of them asks for a deploy instead of a restart. generated
// says the Dockerfile is one of bitploy's templates: every variable is a
// secret mounted into its build script. A user's Dockerfile gets the
// variables it declares as ARG as build arguments (an argument given in the
// compose file wins) and the ones its RUN steps mount as secrets, with the
// fingerprint ARG before those steps in a copy outside the build context.
// cleanup removes that copy.
func (p *pipeline) applyBuildVars(ctx context.Context, spec *deploy.BuildSpec, service string, generated bool) (cleanup func(), ae *apperr.Error) {
	cleanup = func() {}
	vars, ae := p.s.buildVars(ctx, p.app, service)
	if ae != nil {
		return cleanup, ae
	}
	df, err := os.ReadFile(spec.Dockerfile)
	if err != nil {
		return cleanup, apperr.Internal(err)
	}

	// The variables the build gets: args as build arguments, secrets as
	// BuildKit secrets. newDF is the Dockerfile to build instead, if any.
	var args, secrets []string
	var newDF []byte
	if generated {
		var names []string
		for n := range vars {
			if deploy.TemplateBuildEnv(n) {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		names, dropped := fitBuildVars(vars, names)
		p.logDropped(dropped)
		out, n := deploy.WithTemplateBuildVars(string(df), names)
		if n == 0 {
			return cleanup, nil
		}
		if ae := p.recordBuildReads(ctx, nil, true); ae != nil {
			return cleanup, ae
		}
		if len(names) == 0 {
			return cleanup, nil
		}
		if err := os.WriteFile(spec.Dockerfile, []byte(out), 0o644); err != nil {
			return cleanup, apperr.Internal(err)
		}
		secrets = names
		p.logf("The build script gets the app's variables: %s.", strings.Join(names, ", "))
	} else {
		rd := deploy.ReadDockerfileBuildVars(df)
		reads := slices.Concat(rd.Args, rd.Secrets)
		if ae := p.recordBuildReads(ctx, reads, rd.AnySecret); ae != nil {
			return cleanup, ae
		}
		for _, n := range rd.Args {
			if _, ok := vars[n]; ok && !deploy.ReservedBuildArg(n) {
				if _, set := spec.Args[n]; !set {
					args = append(args, n)
				}
			}
		}
		if rd.AnySecret {
			secrets = slices.Sorted(maps.Keys(vars))
		} else {
			for _, n := range rd.Secrets {
				if _, ok := vars[n]; ok {
					secrets = append(secrets, n)
				}
			}
		}
		all := slices.Compact(slices.Sorted(slices.Values(slices.Concat(args, secrets))))
		kept, dropped := fitBuildVars(vars, all)
		p.logDropped(dropped)
		args = slices.DeleteFunc(args, func(n string) bool { return !slices.Contains(kept, n) })
		secrets = slices.DeleteFunc(secrets, func(n string) bool { return !slices.Contains(kept, n) })
		if len(secrets) > 0 {
			newDF = rd.WithSecretFingerprint(df)
		}
		var said []string
		if len(args) > 0 {
			said = append(said, strings.Join(args, ", ")+" (ARG)")
		}
		if len(secrets) > 0 && !rd.AnySecret {
			said = append(said, strings.Join(secrets, ", ")+" (secret)")
		} else if len(secrets) > 0 {
			said = append(said, "every variable as a secret")
		}
		if len(said) > 0 {
			p.logf("The build gets these variables: %s.", strings.Join(said, "; "))
		}
	}

	for _, n := range slices.Concat(args, secrets) {
		if maskedInBuildLog(n, vars[n]) && !slices.Contains(p.buildSecrets, vars[n]) {
			p.buildSecrets = append(p.buildSecrets, vars[n])
		}
	}
	if len(args) > 0 && spec.Args == nil {
		spec.Args = map[string]string{}
	}
	for _, n := range args {
		spec.Args[n] = vars[n]
	}
	if len(secrets) > 0 {
		spec.Secrets = map[string]string{}
		parts := []string{p.app.ID, service}
		for _, n := range secrets {
			spec.Secrets[n] = vars[n]
			parts = append(parts, n, vars[n])
		}
		if spec.Args == nil {
			spec.Args = map[string]string{}
		}
		spec.Args[deploy.BuildSecretsArg] = p.s.Crypto.MAC("build-secrets", parts...)
	}
	if newDF == nil {
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
	if err := os.WriteFile(copyPath, newDF, 0o640); err != nil {
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

func (p *pipeline) logDropped(names []string) {
	if len(names) > 0 {
		p.logf("Left out of the build, the values are too big together (over 1 MB): %s. The running app still gets them.", strings.Join(names, ", "))
	}
}

// recordBuildReads adds names (or every variable, all) to what this
// deployment's build reads, kept in deployments.build_reads.
func (p *pipeline) recordBuildReads(ctx context.Context, names []string, all bool) *apperr.Error {
	if p.buildReads == nil {
		p.buildReads = map[string]bool{}
	}
	if all {
		p.buildReads["*"] = true
	}
	for _, n := range names {
		p.buildReads[n] = true
	}
	if p.dep.ID == "" {
		return nil
	}
	if err := p.s.Q.SetDeploymentBuildReads(ctx, db.SetDeploymentBuildReadsParams{BuildReads: encodeBuildReads(p.buildReads), ID: p.dep.ID}); err != nil {
		return apperr.Internal(err)
	}
	return nil
}

// encodeBuildReads is deployments.build_reads: "*" when the build reads
// every variable, else the names, comma-separated.
func encodeBuildReads(set map[string]bool) string {
	if set["*"] {
		return "*"
	}
	return strings.Join(slices.Sorted(maps.Keys(set)), ",")
}

// decodeBuildReads is Deployment.BuildReads: nil for none.
func decodeBuildReads(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
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
