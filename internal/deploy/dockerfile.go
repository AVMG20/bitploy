package deploy

// OWNER: MCP agent (dockerfile checks + stack detection). Signatures are a
// shared contract: the deploy pipeline calls CheckDockerfile in the
// `validating` state and validate_dockerfile calls it directly.

import (
	"bytes"
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

	"github.com/arno/bitploy/internal/apperr"
	"github.com/arno/bitploy/internal/templates"
)

// Finding codes. image_too_large is checked by the deploy pipeline after the build.
const (
	CodeDockerfileMissing = "dockerfile_missing"
	CodeParseError        = "dockerfile_parse_error"
	CodeNoFrom            = "no_from"
	CodePortUnknown       = "port_unknown"
	CodePortAmbiguous     = "port_ambiguous"
	CodeImageTooLarge     = "image_too_large"
	CodeEnvFileInContext  = "env_file_in_context"
	CodeNoDockerignore    = "no_dockerignore"
	CodeUnpinnedBase      = "unpinned_base"
	CodeRunsAsRoot        = "runs_as_root"
	CodeNoHealthcheck     = "no_healthcheck"
)

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
	AppPort        int      // 0 when the app has no port setting
	AppType        string   // web | worker
}

// CheckResult summarises the checks.
type CheckResult struct {
	OK             bool      // no error-severity findings
	Findings       []Finding // sorted: errors, then warnings, then info
	Port           int       // resolved port: app port, else the single EXPOSE of the final stage
	HasHealthcheck bool      // final stage has a HEALTHCHECK (not NONE)
	Stack          string    // detected stack from Files (static, node, python, go, php) or ""
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
			Message:  fmt.Sprintf("There is no Dockerfile at %q. Bitploy builds every app from a Dockerfile and never guesses how to run your code.", path),
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
				Hint:     "Write a plain number in the last stage, for example `EXPOSE 8080`. Or set the port with update_app (port). Variables in EXPOSE are only understood when ARG or ENV gives them a default.",
			})
		case len(ports) == 0:
			add(Finding{
				Code:     CodePortUnknown,
				Severity: sevError,
				Line:     stageLine(stages[last]),
				Message:  "The last stage of the Dockerfile has no EXPOSE line, so bitploy does not know which port your app listens on.",
				Hint:     "Add `EXPOSE 8080` (your app's real port) to the last stage and make the app listen on 0.0.0.0 at that port ($PORT holds it). Or set the port with update_app (port). A bot or other app without a web port should be created with type worker.",
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

	// Files in the build context.
	if len(in.Files) > 0 {
		files := make([]string, 0, len(in.Files))
		for _, f := range in.Files {
			files = append(files, cleanRel(f))
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
		Hint:     "Start the file with a FROM line that names a base image with a version, for example `FROM node:24-alpine`. Call get_template for a complete example.",
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
	vars   map[string]string
	missed bool
}

func (e *envMap) Get(k string) (string, bool) {
	v, ok := e.vars[k]
	if !ok {
		e.missed = true
	}
	return v, ok
}

func (e *envMap) Keys() []string {
	keys := make([]string, 0, len(e.vars))
	for k := range e.vars {
		keys = append(keys, k)
	}
	return keys
}

var _ shell.EnvGetter = (*envMap)(nil)

// expand resolves $VAR with the given values. ok is false when a variable is unknown.
func (g *graph) expand(word string, vars map[string]string) (string, bool) {
	if !strings.Contains(word, "$") {
		return word, true
	}
	env := &envMap{vars: vars}
	out, _, err := shell.NewLex(g.escape).ProcessWord(word, env)
	if err != nil || env.missed {
		return word, false
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
			Hint:     fmt.Sprintf("Write a version, for example `FROM %s:<version>` (such as node:24-alpine or python:3.14-slim).", name),
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
			Severity: sevWarning,
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
		Severity: sevWarning,
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
	for _, f := range files {
		f = cleanRel(f)
		if !strings.Contains(f, "/") {
			top[strings.ToLower(f)] = true
		}
	}
	switch {
	case top["go.mod"]:
		return "go"
	case top["package.json"]:
		return "node"
	case top["requirements.txt"] || top["pyproject.toml"]:
		return "python"
	case top["composer.json"] || top["index.php"]:
		return "php"
	case top["index.html"]:
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
		e.Hint = fmt.Sprintf("Use the %q template. Save this as Dockerfile in the top folder of the project, adapt it, and save the second file as .dockerignore:\n\n"+
			"Dockerfile:\n%s\n.dockerignore:\n%s\n"+
			"Then call validate_dockerfile and deploy again. Call get_template for more stacks (%s).",
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
		return " This looks like a Node.js project (package.json)."
	case "python":
		return " This looks like a Python project."
	case "go":
		return " This looks like a Go project (go.mod)."
	case "php":
		return " This looks like a PHP project."
	}
	return " Bitploy could not tell what kind of project this is."
}
