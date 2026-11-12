package deploy

// OWNER: MCP agent (dockerfile checks + stack detection). Signatures are a
// shared contract: the deploy pipeline calls CheckDockerfile in the
// `validating` state and validate_dockerfile calls it directly.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/moby/buildkit/frontend/dockerfile/instructions"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/templates"
)

// Finding codes. image_too_large is checked by the deploy pipeline after the build.
const (
	CodeDockerfileMissing = "dockerfile_missing"
	CodeParseError        = "dockerfile_parse_error"
	CodeNoFrom            = "no_from"
	CodePortUnknown       = "port_unknown"
	CodePortAmbiguous     = "port_ambiguous"
	CodePortFromImage     = "port_from_image"
	CodeImageTooLarge     = "image_too_large"
	CodeEnvFileInContext  = "env_file_in_context"
	CodeNoDockerignore    = "no_dockerignore"
	CodeUnpinnedBase      = "unpinned_base"
	CodeRunsAsRoot        = "runs_as_root"
	CodeNoHealthcheck     = "no_healthcheck"
	CodeBaseForbidden     = "base_image_forbidden"
	CodePortReserved      = "port_reserved"
	CodeComposeIgnored    = "compose_ignored"
	CodeDatabaseInImage   = "database_server_in_image"
	CodeLockfileMismatch  = "lockfile_mismatch"
	CodeCacheMount        = "cache_mount_forbidden"
	CodeNoLockfile        = "no_lockfile"
	CodeNativeOnAlpine    = "native_module_on_alpine"
)

// ReservedPort is Traefik's internal entrypoint. firewall.sh drops connections
// to it from the app networks, so an app listening on it would be unreachable.
const ReservedPort = 8081

// MaxImageBytes is the image size limit enforced after the build (image_too_large).
const MaxImageBytes = 2 << 30

// ImageTooLargeFinding is the finding the deploy pipeline reports when the built image is over 2 GB.
func ImageTooLargeFinding(sizeBytes int64) Finding {
	return Finding{
		Code:     CodeImageTooLarge,
		Severity: "error",
		Message:  fmt.Sprintf("The built image is %.1f GB; bitploy allows at most 2 GB.", float64(sizeBytes)/(1<<30)),
		Hint:     "Use a smaller base image (an -alpine or -slim tag), build in two stages and copy only the result into the final stage, add a .dockerignore for node_modules, .git and other big folders, and do not download large files during the build.",
	}
}

// Finding is one Dockerfile check result.
type Finding struct {
	Code     string `json:"code"`     // e.g. port_unknown
	Severity string `json:"severity"` // error | warning | info
	Message  string `json:"message"`
	Hint     string `json:"hint,omitempty"`
	Line     int    `json:"line,omitempty"`
}

// CheckInput is everything the static checks look at.
type CheckInput struct {
	Dockerfile     []byte   // nil when the file does not exist
	DockerfilePath string   // as configured on the app, for messages
	Files          []string // relative slash paths in the build context (may be empty if unknown)
	Dockerignore   []byte   // nil when there is no .dockerignore
	PackageJSON    []byte   // package.json of the build context; nil when unknown or absent
	AppPort        int      // 0 when the app has no port setting
	AppType        string   // web | worker
}

// CheckResult summarises the checks.
type CheckResult struct {
	OK             bool      // no error-severity findings
	Findings       []Finding // sorted: errors, then warnings, then info
	Port           int       // resolved port: app port, else the single EXPOSE of the final stage
	HasHealthcheck bool      // final stage has a HEALTHCHECK (not NONE)
	Stack          string    // detected stack from Files (see DetectStack) or ""
	PortFromImage  bool      // web app, no port setting and no EXPOSE: the pipeline reads the port from the built image
}

const (
	sevError   = "error"
	sevWarning = "warning"
	sevInfo    = "info"
)

// CheckDockerfile runs all static checks. It never builds.
func CheckDockerfile(in CheckInput) CheckResult {
	res := CheckResult{Stack: DetectStack(in.Files)}
	path := in.DockerfilePath
	if path == "" {
		path = "Dockerfile"
	}
	worker := in.AppType == "worker"
	var fs []Finding
	add := func(f Finding) { fs = append(fs, f) }
	finish := func() CheckResult {
		sortFindings(fs)
		res.Findings = fs
		res.OK = true
		for _, f := range fs {
			if f.Severity == sevError {
				res.OK = false
			}
		}
		if res.Findings == nil {
			res.Findings = []Finding{}
		}
		return res
	}

	if in.Dockerfile == nil {
		add(Finding{
			Code:     CodeDockerfileMissing,
			Severity: sevError,
			Message:  fmt.Sprintf("There is no Dockerfile at %q. bitploy builds every app from a Dockerfile.", path),
			Hint:     fmt.Sprintf("Call get_template with stack %q, save its Dockerfile as %q (and its .dockerignore next to it), adapt it, then deploy again.", templateStackFor(res.Stack), path),
		})
		return finish()
	}

	parsed, err := parser.Parse(bytes.NewReader(in.Dockerfile))
	if err != nil {
		if strings.Contains(err.Error(), "file with no instructions") {
			add(noFromFinding(path, true))
			return finish()
		}
		add(parseErrorFinding(err))
		return finish()
	}
	stages, metaArgs, err := instructions.Parse(parsed.AST, nil)
	if err != nil {
		if strings.Contains(err.Error(), "no build stage in current context") {
			add(noFromFinding(path, false))
		} else {
			add(parseErrorFinding(err))
		}
		return finish()
	}
	if len(stages) == 0 {
		add(noFromFinding(path, false))
		return finish()
	}

	g := newGraph(stages, metaArgs, parsed.EscapeToken)
	last := len(stages) - 1

	// Images that bitploy built for apps (bitploy/<slug>:<n>) are never allowed
	// as a base, COPY --from or RUN --mount from source: they belong to users.
	for _, f := range g.forbiddenImages() {
		add(f)
	}

	// All apps build on one BuildKit builder, so a cache mount would be shared
	// with every other app's builds.
	for _, f := range g.cacheMounts() {
		add(f)
	}

	// Ports.
	if worker {
		res.Port = in.AppPort
	} else if in.AppPort > 0 {
		res.Port = in.AppPort
	} else {
		ports, bad, line := g.exposedPorts(last)
		switch {
		case len(bad) > 0 && len(ports) == 0:
			add(Finding{
				Code:     CodePortUnknown,
				Severity: sevError,
				Line:     line,
				Message:  fmt.Sprintf("The EXPOSE line has %q, which is not a port number bitploy can read.", bad[0]),
				Hint:     "Write a plain number in the last stage, for example `EXPOSE 8080`. Or set the port with update_app (port). A variable in EXPOSE works only when ARG or ENV gives it a default.",
			})
		case len(ports) == 0:
			// No EXPOSE and nothing unreadable: the image may still declare a
			// port (a one-line FROM image Dockerfile). The pipeline asks the
			// built image and fails with port_unknown if it declares none.
			res.PortFromImage = true
			add(Finding{
				Code:     CodePortFromImage,
				Severity: sevInfo,
				Line:     stageLine(stages[last]),
				Message:  "The Dockerfile has no EXPOSE line, so bitploy will use the port the image declares after the build.",
				Hint:     "Nothing to do if the image declares one port. Otherwise add `EXPOSE 8080` (the real port) to the last stage, or set the port with update_app (port). For a bot or another app without a web port, create the app with type=worker.",
			})
		case len(ports) > 1:
			strs := make([]string, len(ports))
			for i, p := range ports {
				strs[i] = strconv.Itoa(p)
			}
			add(Finding{
				Code:     CodePortAmbiguous,
				Severity: sevError,
				Line:     line,
				Message:  fmt.Sprintf("The last stage exposes several ports (%s) and bitploy serves only one.", strings.Join(strs, ", ")),
				Hint:     fmt.Sprintf("Keep only the EXPOSE line for the port that serves the website, or choose one with update_app (port=%d, for example).", ports[0]),
			})
		default:
			res.Port = ports[0]
		}
	}

	if res.Port == ReservedPort && !worker {
		add(Finding{
			Code:     CodePortReserved,
			Severity: sevError,
			Message:  fmt.Sprintf("Port %d is reserved for bitploy's own proxy, so an app cannot listen on it.", ReservedPort),
			Hint:     "Make the app listen on another port (for example 8080), change EXPOSE to match, or set it with update_app (port).",
		})
	}

	// Healthcheck.
	res.HasHealthcheck = g.hasHealthcheck(last)
	if !res.HasHealthcheck && !worker {
		add(Finding{
			Code:     CodeNoHealthcheck,
			Severity: sevInfo,
			Message:  "The Dockerfile has no HEALTHCHECK, so bitploy will use its own HTTP check on the app's health path.",
			Hint:     "Nothing to do. The check requests health_path (default /) and accepts any status below 500. Change it with update_app (health_path) if / is slow or needs a login.",
		})
	}

	// Base images.
	for i := range stages {
		if f, ok := g.unpinned(i); ok {
			add(f)
		}
	}

	// Root user.
	if f, ok := g.runsAsRoot(last, stages[last]); ok {
		add(f)
	}

	// Database servers installed into the image.
	for _, f := range g.databaseServers() {
		add(f)
	}

	// Native Node modules installed on Alpine.
	if f, ok := g.nativeOnAlpine(in.PackageJSON); ok {
		add(f)
	}

	// Files in the build context.
	if len(in.Files) > 0 {
		files := make([]string, 0, len(in.Files))
		for _, f := range in.Files {
			files = append(files, cleanRel(f))
		}
		if f, ok := composeFinding(files); ok {
			add(f)
		}
		if f, ok := g.lockfileMismatch(files); ok {
			add(f)
		}
		if f, ok := g.noLockfile(files); ok {
			add(f)
		}
		ignored := newIgnorer(in.Dockerignore)
		hasIgnoreFile := in.Dockerignore != nil || containsString(files, ".dockerignore")
		if !hasIgnoreFile {
			if dir := heavyDir(files); dir != "" {
				add(Finding{
					Code:     CodeNoDockerignore,
					Severity: sevWarning,
					Message:  fmt.Sprintf("The folder has %s but no .dockerignore, so it is sent to the build and can end up in the image.", dir),
					Hint:     "Add a file named .dockerignore with the lines node_modules, .git, venv, .venv and .env (the get_template result includes one).",
				})
			}
		}
		// A .dockerignore we cannot read (only its name is known) is trusted.
		if in.Dockerignore != nil || !hasIgnoreFile {
			for _, f := range files {
				if !isEnvFile(f) || ignored(f) || !g.copiesPath(f) {
					continue
				}
				add(Finding{
					Code:     CodeEnvFileInContext,
					Severity: sevWarning,
					Message:  fmt.Sprintf("The file %s would be copied into the image, and anyone who can pull the image could read its secrets.", f),
					Hint:     fmt.Sprintf("Add %s to .dockerignore and store the values with set_env instead (they reach the app as environment variables).", f),
				})
				break
			}
		}
	}

	return finish()
}

func noFromFinding(path string, empty bool) Finding {
	msg := fmt.Sprintf("The Dockerfile at %q has no FROM line.", path)
	if empty {
		msg = fmt.Sprintf("The Dockerfile at %q is empty or only has comments, so it has no FROM line.", path)
	}
	return Finding{
		Code:     CodeNoFrom,
		Severity: sevError,
		Message:  msg,
		Hint:     "Start the file with a FROM line that names a base image with a version, for example `FROM node:22-bookworm-slim`. Call get_template for a complete example.",
	}
}

var onLineRe = regexp.MustCompile(`on line (\d+)`)

func parseErrorFinding(err error) Finding {
	line := 0
	var le *parser.LocationError
	var ui *instructions.UnknownInstructionError
	switch {
	case errors.As(err, &ui):
		line = ui.Line
	case errors.As(err, &le):
		if len(le.Locations) > 0 && len(le.Locations[0]) > 0 {
			line = le.Locations[0][0].Start.Line
		}
	}
	msg := err.Error()
	if m := onLineRe.FindStringSubmatch(msg); m != nil {
		if line == 0 {
			line, _ = strconv.Atoi(m[1])
		}
		if k := strings.Index(msg, ": "); k >= 0 {
			msg = msg[k+2:]
		}
	}
	msg = strings.TrimSpace(apperr.Redact(msg))
	hint := "Fix that line (instruction names are FROM, RUN, COPY, ADD, ENV, ARG, WORKDIR, USER, EXPOSE, CMD, ENTRYPOINT, HEALTHCHECK, VOLUME, LABEL and a few more), then call validate_dockerfile again."
	if ui != nil {
		hint = fmt.Sprintf("%q is not a Dockerfile instruction. Check the spelling, or put the text after RUN if it is a shell command.", ui.Instruction)
	}
	where := ""
	if line > 0 {
		where = fmt.Sprintf(" on line %d", line)
	}
	return Finding{
		Code:     CodeParseError,
		Severity: sevError,
		Line:     line,
		Message:  fmt.Sprintf("The Dockerfile cannot be read%s: %s.", where, strings.TrimSuffix(msg, ".")),
		Hint:     hint,
	}
}

// ---- stage graph ----

type graph struct {
	stages  []instructions.Stage
	meta    map[string]string // global ARG defaults
	escape  rune
	byName  map[string]int
	baseIdx []int // index of the earlier stage a stage builds on, or -1
}

func newGraph(stages []instructions.Stage, metaArgs []instructions.ArgCommand, esc rune) *graph {
	if esc == 0 {
		esc = '\\'
	}
	g := &graph{stages: stages, meta: map[string]string{}, escape: esc, byName: map[string]int{}}
	for _, a := range metaArgs {
		for _, kv := range a.Args {
			if kv.Value != nil {
				g.meta[kv.Key] = *kv.Value
			}
		}
	}
	for i, s := range stages {
		g.baseIdx = append(g.baseIdx, -1)
		base := strings.ToLower(s.BaseName)
		if j, ok := g.byName[base]; ok && base != "" {
			g.baseIdx[i] = j
		}
		if s.Name != "" {
			g.byName[strings.ToLower(s.Name)] = i
		}
	}
	return g
}

type envMap struct {
	vars map[string]string
}

func (e *envMap) Get(k string) (string, bool) {
	v, ok := e.vars[k]
	return v, ok
}

// plainVarRef matches $VAR and ${VAR}: references without a default.
var plainVarRef = regexp.MustCompile(`\$(?:\{([A-Za-z_][A-Za-z0-9_]*)\}|([A-Za-z_][A-Za-z0-9_]*))`)

func (e *envMap) Keys() []string {
	keys := make([]string, 0, len(e.vars))
	for k := range e.vars {
		keys = append(keys, k)
	}
	return keys
}

var _ shell.EnvGetter = (*envMap)(nil)

// expand resolves $VAR with the given values. ok is false when a variable is
// unknown and has no default: ${VAR:-x} and ${VAR-x} resolve to x, as in Docker.
func (g *graph) expand(word string, vars map[string]string) (string, bool) {
	if !strings.Contains(word, "$") {
		return word, true
	}
	out, unmatched, err := shell.NewLex(g.escape).ProcessWord(word, &envMap{vars: vars})
	if err != nil {
		return word, false
	}
	// The lexer also reports variables whose default was used; only a plain
	// reference to an unknown variable leaves the value open.
	for _, m := range plainVarRef.FindAllStringSubmatch(word, -1) {
		name := m[1] + m[2]
		if _, missing := unmatched[name]; missing {
			return word, false
		}
	}
	return out, true
}

// chain lists the stage and the local stages it builds on, base first.
func (g *graph) chain(i int) []int {
	var rev []int
	seen := map[int]bool{}
	for i >= 0 && !seen[i] {
		seen[i] = true
		rev = append(rev, i)
		i = g.baseIdx[i]
	}
	for l, r := 0, len(rev)-1; l < r; l, r = l+1, r-1 {
		rev[l], rev[r] = rev[r], rev[l]
	}
	return rev
}

// scopeVars returns the ARG/ENV values visible at the end of a stage.
func (g *graph) scopeVars(i int) map[string]string {
	vars := map[string]string{}
	for _, idx := range g.chain(i) {
		for _, c := range g.stages[idx].Commands {
			switch c := c.(type) {
			case *instructions.ArgCommand:
				for _, kv := range c.Args {
					if kv.Value != nil {
						if v, ok := g.expand(*kv.Value, vars); ok {
							vars[kv.Key] = v
						}
					} else if v, ok := g.meta[kv.Key]; ok {
						vars[kv.Key] = v
					}
				}
			case *instructions.EnvCommand:
				for _, kv := range c.Env {
					if v, ok := g.expand(kv.Value, vars); ok {
						vars[kv.Key] = v
					}
				}
			}
		}
	}
	return vars
}

// exposedPorts lists the distinct TCP ports exposed by the stage chain.
func (g *graph) exposedPorts(i int) (ports []int, bad []string, line int) {
	vars := g.scopeVars(i)
	seen := map[int]bool{}
	for _, idx := range g.chain(i) {
		for _, c := range g.stages[idx].Commands {
			e, ok := c.(*instructions.ExposeCommand)
			if !ok {
				continue
			}
			for _, raw := range e.Ports {
				word, ok := g.expand(raw, vars)
				if !ok {
					bad = append(bad, raw)
					continue
				}
				for _, w := range strings.Fields(word) {
					spec, proto, _ := strings.Cut(w, "/")
					if strings.EqualFold(proto, "udp") {
						continue
					}
					lo, hi, isRange := strings.Cut(spec, "-")
					start, err1 := strconv.Atoi(lo)
					end := start
					var err2 error
					if isRange {
						end, err2 = strconv.Atoi(hi)
					}
					if err1 != nil || err2 != nil || start < 1 || end > 65535 || end < start || end-start > 1000 {
						bad = append(bad, raw)
						continue
					}
					for p := start; p <= end; p++ {
						if !seen[p] {
							seen[p] = true
							ports = append(ports, p)
						}
					}
					if line == 0 {
						line = firstLine(e.Location())
					}
				}
			}
		}
	}
	sort.Ints(ports)
	return ports, bad, line
}

func (g *graph) hasHealthcheck(i int) bool {
	has := false
	for _, idx := range g.chain(i) {
		for _, c := range g.stages[idx].Commands {
			if h, ok := c.(*instructions.HealthCheckCommand); ok {
				has = h.Health != nil && len(h.Health.Test) > 0 && !strings.EqualFold(h.Health.Test[0], "NONE")
			}
		}
	}
	return has
}

// externalBase returns the resolved external image the chain starts from.
func (g *graph) externalBase(i int) (string, bool) {
	chain := g.chain(i)
	first := chain[0]
	name := g.stages[first].BaseName
	resolved, ok := g.expand(name, g.meta)
	return resolved, ok
}

func (g *graph) unpinned(i int) (Finding, bool) {
	if g.baseIdx[i] >= 0 {
		return Finding{}, false
	}
	name, ok := g.expand(g.stages[i].BaseName, g.meta)
	if !ok || name == "" {
		return Finding{}, false
	}
	ref := strings.ToLower(name)
	if ref == "scratch" || strings.Contains(ref, "@sha256:") {
		return Finding{}, false
	}
	last := ref[strings.LastIndex(ref, "/")+1:]
	tag := ""
	if k := strings.Index(last, ":"); k >= 0 {
		tag = last[k+1:]
	}
	line := stageLine(g.stages[i])
	switch tag {
	case "":
		return Finding{
			Code:     CodeUnpinnedBase,
			Severity: sevWarning,
			Line:     line,
			Message:  fmt.Sprintf("The base image %q has no version tag, so it means :latest and can change under you.", name),
			Hint:     fmt.Sprintf("Write a version, for example `FROM %s:<version>` (such as node:22-bookworm-slim or python:3.14-slim).", name),
		}, true
	case "latest":
		return Finding{
			Code:     CodeUnpinnedBase,
			Severity: sevWarning,
			Line:     line,
			Message:  fmt.Sprintf("The base image %q uses :latest, which can change under you and break a later deploy.", name),
			Hint:     fmt.Sprintf("Replace :latest with a version, for example `FROM %s:<version>`.", strings.TrimSuffix(name, ":latest")),
		}, true
	}
	return Finding{}, false
}

func (g *graph) runsAsRoot(i int, final instructions.Stage) (Finding, bool) {
	user, found := "", false
	var userLine int
	vars := g.scopeVars(i)
	for _, idx := range g.chain(i) {
		for _, c := range g.stages[idx].Commands {
			if u, ok := c.(*instructions.UserCommand); ok {
				user, found = u.User, true
				userLine = firstLine(u.Location())
			}
		}
	}
	if found {
		resolved, ok := g.expand(user, vars)
		if !ok {
			return Finding{}, false
		}
		name, _, _ := strings.Cut(resolved, ":")
		if name != "root" && name != "0" {
			return Finding{}, false
		}
		return Finding{
			Code:     CodeRunsAsRoot,
			Severity: sevInfo,
			Line:     userLine,
			Message:  "The last stage runs as root (USER root), so a bug in the app has full rights inside the container.",
			Hint:     "Create a normal user and switch to it before CMD, for example `RUN adduser -D app` then `USER app` on Alpine, or `USER node` on node images.",
		}, true
	}
	if base, ok := g.externalBase(i); ok && imageRunsNonRoot(base) {
		return Finding{}, false
	}
	return Finding{
		Code:     CodeRunsAsRoot,
		Severity: sevInfo,
		Line:     stageLine(final),
		Message:  "The last stage has no USER line, so the app runs as root inside the container.",
		Hint:     "Add a USER line before CMD: `USER node` on node images, or create one with `RUN adduser -D app` and `USER app` on Alpine, or `RUN useradd -r app` and `USER app` on Debian.",
	}, true
}

// imageRunsNonRoot knows base images whose default user is not root.
func imageRunsNonRoot(image string) bool {
	ref := strings.ToLower(image)
	return strings.Contains(ref, "unprivileged") ||
		strings.Contains(ref, "nonroot") ||
		strings.HasPrefix(ref, "cgr.dev/chainguard/")
}

// copiesPath reports whether a COPY or ADD from the build context would include the file.
func (g *graph) copiesPath(file string) bool {
	for _, s := range g.stages {
		for _, c := range s.Commands {
			var srcs []string
			switch c := c.(type) {
			case *instructions.CopyCommand:
				if c.From != "" {
					continue
				}
				srcs = c.SourcePaths
			case *instructions.AddCommand:
				srcs = c.SourcePaths
			default:
				continue
			}
			for _, src := range srcs {
				if strings.Contains(src, "://") {
					continue
				}
				src = cleanRel(strings.TrimPrefix(src, "/"))
				if src == "." || src == "" || src == file || strings.HasPrefix(file, src+"/") {
					return true
				}
				if ok, _ := path.Match(src, file); ok {
					return true
				}
			}
		}
	}
	return false
}

func stageLine(s instructions.Stage) int {
	if len(s.Location) > 0 {
		return s.Location[0].Start.Line
	}
	return 0
}

func firstLine(loc []parser.Range) int {
	if len(loc) > 0 {
		return loc[0].Start.Line
	}
	return 0
}

// ---- context files ----

func cleanRel(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	p = path.Clean(p)
	return strings.TrimPrefix(p, "./")
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// newIgnorer returns a matcher for .dockerignore content; nil content ignores nothing.
func newIgnorer(content []byte) func(string) bool {
	if content == nil {
		return func(string) bool { return false }
	}
	patterns, err := ignorefile.ReadAll(bytes.NewReader(content))
	if err != nil || len(patterns) == 0 {
		return func(string) bool { return false }
	}
	pm, err := patternmatcher.New(patterns)
	if err != nil {
		return func(string) bool { return false }
	}
	return func(p string) bool {
		ok, err := pm.MatchesOrParentMatches(p)
		return err == nil && ok
	}
}

func isEnvFile(p string) bool {
	base := path.Base(p)
	if base == ".env" {
		return true
	}
	if !strings.HasPrefix(base, ".env.") {
		return false
	}
	switch strings.ToLower(strings.TrimPrefix(base, ".env.")) {
	case "example", "sample", "template", "dist", "defaults", "default":
		return false
	}
	return true
}

// heavyDir returns the first of node_modules, .git, venv or .venv found as a top-level folder.
func heavyDir(files []string) string {
	found := map[string]bool{}
	for _, f := range files {
		if top, _, ok := strings.Cut(f, "/"); ok {
			found[top] = true
		}
	}
	for _, d := range []string{"node_modules", ".git", "venv", ".venv"} {
		if found[d] {
			return d
		}
	}
	return ""
}

func sortFindings(fs []Finding) {
	rank := map[string]int{sevError: 0, sevWarning: 1, sevInfo: 2}
	sort.SliceStable(fs, func(i, j int) bool { return rank[fs[i].Severity] < rank[fs[j].Severity] })
}

// ---- stack detection and error conversion ----

// DetectStack guesses the stack from file names, only to make errors helpful.
// Only top-level files count. index.html alone (plus assets) means static.
func DetectStack(files []string) string {
	top := map[string]bool{}
	page := false
	for _, f := range files {
		f = cleanRel(f)
		if !strings.Contains(f, "/") {
			top[strings.ToLower(f)] = true
			page = page || IsHTMLName(f)
		}
	}
	hasTop := func(prefix string) bool {
		for _, ext := range []string{".js", ".mjs", ".cjs", ".ts", ".mts"} {
			if top[prefix+ext] {
				return true
			}
		}
		return false
	}
	switch {
	case top["go.mod"]:
		return "go"
	case top["artisan"]:
		return "laravel"
	case top["manage.py"]:
		return "django"
	case hasTop("next.config"):
		return "nextjs"
	case hasTop("astro.config"):
		return "node-static"
	case top["package.json"]:
		return "node"
	case top["requirements.txt"] || top["pyproject.toml"]:
		return "python"
	case top["composer.json"] || top["index.php"]:
		return "php"
	case page: // index.html, or any other page at the top
		return "static"
	}
	return ""
}

// templateStackFor picks the template that fits a detected stack; generic when unknown.
func templateStackFor(stack string) string {
	if _, ok := templates.Get(stack); ok {
		return stack
	}
	return "generic"
}

// FirstError converts the first error finding of a result into an *apperr.Error
// (dockerfile_missing includes the matching template text in Details). Returns
// nil when the result is OK.
func FirstError(res CheckResult, files []string) *apperr.Error {
	var first *Finding
	for i := range res.Findings {
		if res.Findings[i].Severity == sevError {
			first = &res.Findings[i]
			break
		}
	}
	if first == nil {
		return nil
	}
	e := apperr.Unprocessable(first.Code, first.Message, first.Hint)
	e.WithDetail("findings", res.Findings)
	if first.Line > 0 {
		e.WithDetail("line", first.Line)
	}
	switch first.Code {
	case CodeDockerfileMissing:
		stack := res.Stack
		if stack == "" {
			stack = DetectStack(files)
		}
		name := templateStackFor(stack)
		tpl, _ := templates.Get(name)
		e.Message = first.Message + describeStack(stack)
		e.Hint = fmt.Sprintf("Use the %q template. Save this as Dockerfile in the top folder of your code, adapt it, and save the second file as .dockerignore:\n\n"+
			"Dockerfile:\n%s\n.dockerignore:\n%s\n"+
			"Then call validate_dockerfile and deploy again with ALL files including these two (deploy_files replaces the whole source). Call get_template for more stacks (%s).",
			name, tpl.Dockerfile, tpl.Dockerignore, strings.Join(templates.Stacks, ", "))
		e.WithDetail("stack", stack)
		e.WithDetail("template", name)
		e.WithDetail("dockerfile", tpl.Dockerfile)
		e.WithDetail("dockerignore", tpl.Dockerignore)
		e.WithNext("get_template")
	case CodeImageTooLarge:
		// nothing to call: the agent must change the Dockerfile
	default:
		e.WithNext("validate_dockerfile")
	}
	return e
}

func describeStack(stack string) string {
	switch stack {
	case "static":
		return " This looks like a plain website (index.html, no build step)."
	case "node":
		return " This looks like a Node.js app (package.json)."
	case "python":
		return " This looks like a Python app."
	case "go":
		return " This looks like a Go app (go.mod)."
	case "php":
		return " This looks like a PHP app."
	case "laravel":
		return " This looks like a Laravel app (artisan)."
	case "django":
		return " This looks like a Django app (manage.py)."
	case "nextjs":
		return " This looks like a Next.js app (next.config)."
	case "node-static":
		return " This looks like a static site built with Node (astro.config)."
	}
	return " bitploy could not tell what kind of app this is."
}

// forbiddenImage reports whether an image reference points at an image bitploy
// built for an app (bitploy/<slug>:<n>), with or without a Docker Hub host.
func forbiddenImage(ref string) bool {
	ref = strings.ToLower(strings.TrimSpace(ref))
	for _, host := range []string{"index.docker.io/", "registry-1.docker.io/", "registry.hub.docker.com/", "docker.io/"} {
		ref = strings.TrimPrefix(ref, host)
	}
	return strings.HasPrefix(ref, "bitploy/")
}

// forbiddenImages finds every FROM, COPY --from and RUN --mount from= that
// names a bitploy/* image (defence in depth: the isolated builder cannot see
// local images anyway).
func (g *graph) forbiddenImages() []Finding {
	var out []Finding
	seen := map[string]bool{}
	report := func(ref string, line int) {
		if seen[ref] {
			return
		}
		seen[ref] = true
		out = append(out, Finding{
			Code:     CodeBaseForbidden,
			Severity: sevError,
			Line:     line,
			Message:  fmt.Sprintf("The Dockerfile uses the image %q, which is an app image built by bitploy. Apps cannot use each other's images.", ref),
			Hint:     "Start from a public image instead, such as node:22-bookworm-slim, python:3.14-slim or nginx:1.29-alpine, and copy your own files into it. Remove every FROM, COPY --from and RUN --mount from= that names bitploy/....",
		})
	}
	check := func(raw string, vars map[string]string, line int) {
		ref := raw
		if strings.Contains(raw, "$") {
			r, ok := g.expand(raw, vars)
			if !ok {
				return
			}
			ref = r
		}
		if _, isStage := g.byName[strings.ToLower(ref)]; isStage {
			return
		}
		if forbiddenImage(ref) {
			report(ref, line)
		}
	}
	for i, st := range g.stages {
		if g.baseIdx[i] < 0 {
			check(st.BaseName, g.meta, stageLine(st))
		}
		vars := g.scopeVars(i)
		for _, c := range st.Commands {
			switch cmd := c.(type) {
			case *instructions.CopyCommand:
				if cmd.From != "" {
					check(cmd.From, vars, firstLine(cmd.Location()))
				}
			case *instructions.RunCommand:
				for _, m := range instructions.GetMounts(cmd) {
					if m.From != "" {
						check(m.From, vars, firstLine(cmd.Location()))
					}
				}
			}
		}
	}
	return out
}

// cacheMounts finds every RUN --mount=type=cache. The builder is shared by all
// apps and a cache mount's id defaults to its target, so one app could fill the
// cache another app's build reads from. Mount options are read as written, so a
// type hidden in a variable (type=$T) does not parse and is refused too.
func (g *graph) cacheMounts() []Finding {
	var out []Finding
	hint := "Remove --mount=type=cache from the RUN line and install the packages in a plain RUN step, for example `RUN npm ci`."
	for _, st := range g.stages {
		for _, c := range st.Commands {
			cmd, ok := c.(*instructions.RunCommand)
			if !ok {
				continue
			}
			line := firstLine(cmd.Location())
			if err := cmd.Expand(func(w string) (string, error) { return w, nil }); err != nil {
				out = append(out, Finding{
					Code:     CodeCacheMount,
					Severity: sevError,
					Line:     line,
					Message:  fmt.Sprintf("bitploy cannot read the --mount options of this RUN line (%s).", err),
					Hint:     "Write the mount options as plain text, without variables. " + hint,
				})
				continue
			}
			for _, m := range instructions.GetMounts(cmd) {
				if m.Type == instructions.MountTypeCache {
					out = append(out, Finding{
						Code:     CodeCacheMount,
						Severity: sevError,
						Line:     line,
						Message:  "The RUN line uses --mount=type=cache. bitploy does not support cache mounts: all apps build on one shared build server, so other users' builds would share the cache.",
						Hint:     hint,
					})
					break
				}
			}
		}
	}
	return out
}

// ---- compose, database servers, lock files ----

var composeNames = map[string]bool{"docker-compose.yml": true, "docker-compose.yaml": true, "compose.yml": true, "compose.yaml": true}

func composeFinding(files []string) (Finding, bool) {
	for _, f := range files {
		if !strings.Contains(f, "/") && composeNames[strings.ToLower(f)] {
			return Finding{
				Code:     CodeComposeIgnored,
				Severity: sevWarning,
				Message:  fmt.Sprintf("Your code has %s, but this app builds only the Dockerfile, so the other services in that file (a database, Redis) do not run.", f),
				Hint:     "Use SQLite in /data (turn on saved files for the app) instead of a database server, and an in-process or file cache instead of Redis. If the app needs those services, deploy the compose file instead: update_app with build_mode=\"compose\", then deploy again.",
			}, true
		}
	}
	return Finding{}, false
}

// dbServerPkg matches packages that install a database server, not client libraries.
var dbServerPkg = regexp.MustCompile(`^(postgresql(-?\d+(\.\d+)?)?|postgresql\d*-server|postgresql-server|(default-)?mysql-server|mysql-community-server|mariadb|mariadb-server|redis|redis-server|valkey|valkey-server|mongodb|mongodb-org(-server)?)$`)

var shellSplit = regexp.MustCompile(`&&|\|\||[;|\n]`)

// installedPackages returns the packages named by apt-get/apt/apk/yum/dnf/microdnf install commands in a RUN line.
func installedPackages(cmd string) []string {
	cmd = strings.ReplaceAll(cmd, "\\\n", " ")
	var pkgs []string
	for _, seg := range shellSplit.Split(cmd, -1) {
		toks := strings.Fields(seg)
		tool := -1
		for i, t := range toks {
			switch path.Base(t) {
			case "apt-get", "apt", "apk", "yum", "dnf", "microdnf":
				tool = i
			}
			if tool >= 0 {
				break
			}
		}
		if tool < 0 {
			continue
		}
		verb := false
		for _, t := range toks[tool+1:] {
			if !verb {
				verb = t == "install" || t == "add"
				continue
			}
			if strings.HasPrefix(t, "-") || strings.Contains(t, "$") {
				continue
			}
			t = strings.Trim(t, "\"'")
			if i := strings.IndexAny(t, "=<>~"); i > 0 {
				t = t[:i]
			}
			pkgs = append(pkgs, t)
		}
	}
	return pkgs
}

// runText is the command of a RUN line, including heredoc bodies.
func runText(c *instructions.RunCommand) string {
	parts := append([]string{}, c.CmdLine...)
	for _, f := range c.Files {
		parts = append(parts, f.Data)
	}
	return strings.Join(parts, "\n")
}

// databaseServers reports RUN lines in any stage that install a database server.
func (g *graph) databaseServers() []Finding {
	var out []Finding
	seen := map[string]bool{}
	for _, st := range g.stages {
		for _, c := range st.Commands {
			run, ok := c.(*instructions.RunCommand)
			if !ok {
				continue
			}
			for _, p := range installedPackages(runText(run)) {
				if !dbServerPkg.MatchString(p) || seen[p] {
					continue
				}
				seen[p] = true
				out = append(out, Finding{
					Code:     CodeDatabaseInImage,
					Severity: sevWarning,
					Line:     firstLine(run.Location()),
					Message:  fmt.Sprintf("The Dockerfile installs %s, but an app is one container with one process: a database server in the image does not run next to the app.", p),
					Hint:     "Remove the install and use SQLite with the file in /data, like /data/app.db (turn on saved files for the app). Instead of Redis, use an in-process cache or a SQLite-backed queue. If the code needs the server, add it as an app of its own in the app's project (add_service, or Add an app or a database on the project page), or use a hosted database and store its URL as an environment variable.",
				})
			}
		}
	}
	return out
}

// runsNpmCi reports whether any RUN line calls `npm ci`.
func (g *graph) runsNpmCi() (int, bool) {
	for _, st := range g.stages {
		for _, c := range st.Commands {
			run, ok := c.(*instructions.RunCommand)
			if !ok {
				continue
			}
			for _, seg := range shellSplit.Split(runText(run), -1) {
				toks := strings.Fields(seg)
				for i := 0; i+1 < len(toks); i++ {
					if toks[i] == "npm" && toks[i+1] == "ci" {
						return firstLine(run.Location()), true
					}
				}
			}
		}
	}
	return 0, false
}

// lockfileMismatch flags `npm ci` in a project whose lock file belongs to another package manager.
func (g *graph) lockfileMismatch(files []string) (Finding, bool) {
	line, ok := g.runsNpmCi()
	if !ok {
		return Finding{}, false
	}
	top := map[string]bool{}
	for _, f := range files {
		if !strings.Contains(f, "/") {
			top[f] = true
		}
	}
	if top["package-lock.json"] {
		return Finding{}, false
	}
	var lock, hint string
	switch {
	case top["pnpm-lock.yaml"]:
		lock, hint = "pnpm-lock.yaml", "Use `RUN corepack enable && pnpm install --frozen-lockfile` and `COPY package.json pnpm-lock.yaml ./`."
	case top["yarn.lock"]:
		lock, hint = "yarn.lock", "Use `RUN corepack enable && yarn install --frozen-lockfile` and `COPY package.json yarn.lock ./`."
	case top["bun.lock"]:
		lock, hint = "bun.lock", "Use an oven/bun base image with `bun install --frozen-lockfile` (its user is bun, not node: `USER bun`)."
	case top["bun.lockb"]:
		lock, hint = "bun.lockb", "Use an oven/bun base image with `bun install --frozen-lockfile` (its user is bun, not node: `USER bun`)."
	default:
		return Finding{}, false
	}
	return Finding{
		Code:     CodeLockfileMismatch,
		Severity: sevWarning,
		Line:     line,
		Message:  fmt.Sprintf("The Dockerfile runs npm ci, but your code has %s and no package-lock.json, so npm ci will fail.", lock),
		Hint:     hint + " Or call get_template with file_list so the Dockerfile matches your code.",
	}, true
}

// jsLockFiles are the lock files of npm, pnpm, yarn and bun.
var jsLockFiles = []string{"package-lock.json", "npm-shrinkwrap.json", "pnpm-lock.yaml", "yarn.lock", "bun.lock", "bun.lockb"}

// jsInstall is the package manager a shell command installs JavaScript
// packages with ("npm", "pnpm", "yarn" or "bun"), or "".
func jsInstall(seg string) string {
	toks := strings.Fields(seg)
	for i, t := range toks {
		next := ""
		if i+1 < len(toks) {
			next = toks[i+1]
		}
		switch path.Base(t) {
		case "npm":
			// `npm install -g pnpm` installs a tool, not the project's packages.
			if (next == "ci" || next == "install" || next == "i") && !containsString(toks, "-g") && !containsString(toks, "--global") {
				return "npm"
			}
		case "pnpm", "bun":
			if next == "install" || next == "i" {
				return path.Base(t)
			}
		case "yarn":
			// A bare `yarn` installs too.
			if next == "" || next == "install" || strings.HasPrefix(next, "-") {
				return "yarn"
			}
		}
	}
	return ""
}

// jsInstallLine is the first RUN line of stage i that installs JavaScript
// packages, with the package manager it uses.
func (g *graph) jsInstallLine(i int) (line int, pm string) {
	for _, c := range g.stages[i].Commands {
		run, ok := c.(*instructions.RunCommand)
		if !ok {
			continue
		}
		for _, seg := range shellSplit.Split(runText(run), -1) {
			if pm := jsInstall(seg); pm != "" {
				return firstLine(run.Location()), pm
			}
		}
	}
	return 0, ""
}

// noLockfile flags a JavaScript project that installs its packages without a
// lock file. The versions in package.json are ranges (^3.13.0), so every
// build installs whatever is newest at that moment: an agent saw a Nuxt app
// break on a new release although its code had not changed.
func (g *graph) noLockfile(files []string) (Finding, bool) {
	top := map[string]bool{}
	for _, f := range files {
		if !strings.Contains(f, "/") {
			top[f] = true
		}
	}
	if !top["package.json"] {
		return Finding{}, false
	}
	for _, l := range jsLockFiles {
		if top[l] {
			return Finding{}, false
		}
	}
	line, pm := 0, ""
	for i := range g.stages {
		if line, pm = g.jsInstallLine(i); pm != "" {
			break
		}
	}
	if pm == "" {
		return Finding{}, false // nothing is installed during the build
	}
	msg := "Your code has package.json but no lock file (package-lock.json, bun.lock, pnpm-lock.yaml or yarn.lock), so every build installs the newest versions package.json allows: a build can break although your code did not change."
	if _, ci := g.runsNpmCi(); ci {
		msg += " npm ci also fails without package-lock.json."
	}
	hint := "Ship the lock file: run the install once on your computer (npm install, bun install, pnpm install or yarn) and send the file it writes with the code. Or pin exact versions in package.json (\"3.13.0\", not \"^3.13.0\")."
	if pm == "bun" {
		hint += " With bun: send bun.lock and use `bun install --frozen-lockfile`. The oven/bun image's user is bun, not node (`USER bun`, `chown bun:bun /data`)."
	}
	return Finding{Code: CodeNoLockfile, Severity: sevWarning, Line: line, Message: msg, Hint: hint}, true
}

// nativeModules are npm packages with compiled code. Their ready-made
// binaries are built for Debian (glibc) first; on Alpine (musl) npm often has
// to compile them, which needs python3, make and g++ and can take many
// minutes or fail. sharp is not here: it ships ready-made Alpine builds.
var nativeModules = []string{"better-sqlite3", "sqlite3", "bcrypt", "argon2", "canvas", "node-pty", "re2"}

// nativeOnAlpine flags native modules in package.json when a stage that
// installs packages builds on an Alpine image. Only the deploy pipeline (and
// validate_dockerfile when the caller passes package.json) knows the file.
func (g *graph) nativeOnAlpine(pkgJSON []byte) (Finding, bool) {
	if len(pkgJSON) == 0 {
		return Finding{}, false
	}
	var pkg struct {
		Dependencies         map[string]any `json:"dependencies"`
		DevDependencies      map[string]any `json:"devDependencies"`
		OptionalDependencies map[string]any `json:"optionalDependencies"`
	}
	if json.Unmarshal(pkgJSON, &pkg) != nil {
		return Finding{}, false
	}
	var found []string
	for _, m := range nativeModules {
		if pkg.Dependencies[m] != nil || pkg.DevDependencies[m] != nil || pkg.OptionalDependencies[m] != nil {
			found = append(found, m)
		}
	}
	if len(found) == 0 {
		return Finding{}, false
	}
	for i := range g.stages {
		if _, pm := g.jsInstallLine(i); pm == "" {
			continue
		}
		base, ok := g.externalBase(i)
		if !ok || !strings.Contains(strings.ToLower(base), "alpine") {
			continue
		}
		return Finding{
			Code:     CodeNativeOnAlpine,
			Severity: sevWarning,
			Line:     stageLine(g.stages[i]),
			Message:  fmt.Sprintf("package.json uses %s, which has compiled code, and the Dockerfile installs it on %s. Alpine often has no ready-made build of it, so it is compiled from source: that needs python3, make and g++ and can take many minutes or fail.", strings.Join(found, ", "), base),
			Hint:     "Use a Debian image in every stage that installs packages, for example `FROM node:22-bookworm-slim` (the node templates of get_template use it). Do not add compilers with apk add.",
		}, true
	}
	return Finding{}, false
}
