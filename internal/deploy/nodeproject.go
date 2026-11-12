package deploy

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/templates"
)

// Codes of the errors for a Node project that cannot be built as asked.
const (
	CodePackageJSONMissing = "package_json_missing"
	CodePackageJSONInvalid = "package_json_invalid"
	CodeBuildScriptMissing = "build_script_missing"
	CodeStartScriptMissing = "start_script_missing"
	CodeOutputDirInvalid   = "output_dir_invalid"
)

// NextConfigNames are the files that can hold the Next.js config. PlanBuild
// reads the first one the source has, to see its output setting.
var NextConfigNames = []string{"next.config.js", "next.config.mjs", "next.config.cjs", "next.config.ts", "next.config.mts"}

// CleanOutputDir validates the output folder of a static build: empty (use the
// default of the framework) or a relative path of plain names. A leading ./ and
// a trailing / are removed. The error text is a short reason.
func CleanOutputDir(dir string) (string, error) {
	dir = strings.ReplaceAll(strings.TrimSpace(dir), "\\", "/")
	if dir == "" {
		return "", nil
	}
	for strings.HasPrefix(dir, "./") {
		dir = strings.TrimPrefix(dir, "./")
	}
	dir = strings.TrimRight(dir, "/")
	if strings.HasPrefix(dir, "/") {
		return "", fmt.Errorf("it must be a folder inside your code, not an absolute path")
	}
	for _, seg := range strings.Split(dir, "/") {
		if seg == ".." {
			return "", fmt.Errorf("it cannot contain ..")
		}
	}
	if !templates.ValidOutputDir(dir) {
		return "", fmt.Errorf("use only letters, numbers and . _ - /, like dist or build/client")
	}
	return dir, nil
}

// viteDevCmd matches a start script that only runs Vite's own dev or preview
// server (vite, vite dev, vite preview --port 3000, npx vite, pnpm vite): a
// website, not a server of its own. The command must end after Vite's flags, so
// vite-node, "vite build" and chained commands do not match.
var (
	viteDevCmd   = regexp.MustCompile(`^(?:[A-Za-z_][A-Za-z0-9_]*=\S+\s+)*(?:(?:npx|bunx)\s+(?:(?:--yes|-y)\s+)?|pnpm\s+(?:exec\s+)?|yarn\s+(?:exec\s+)?)?vite(?:@\S+)?(?:\s+-.*|\s+(?:dev|preview|serve)(?:\s.*)?)?$`)
	shellControl = regexp.MustCompile("[&|;<>$`()]")
)

func viteDevStart(start string) bool {
	s := strings.TrimSpace(start)
	return viteDevCmd.MatchString(s) && !shellControl.MatchString(s)
}

// project is what the planner reads from package.json.
type project struct {
	scripts  map[string]bool // names of the scripts that are not empty
	start    string          // the start script
	deps     map[string]bool // dependencies and devDependencies
	devDeps  map[string]bool // devDependencies only
	main     string
	files    []string
	hasIndex bool
	next     nextConfig
}

type nextConfig struct {
	export, standalone bool
}

var (
	lineComment  = regexp.MustCompile(`(?m)//.*$`)
	blockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	nextExport   = regexp.MustCompile("output\\s*:\\s*['\"`]export['\"`]")
	nextStandal  = regexp.MustCompile("output\\s*:\\s*['\"`]standalone['\"`]")
)

// parseNextConfig looks for output: 'export' or 'standalone' in the config,
// ignoring comments. Anything cleverer than a plain string (a variable, a
// condition) is not seen and counts as neither.
func parseNextConfig(src []byte) nextConfig {
	s := blockComment.ReplaceAllString(string(src), "")
	s = lineComment.ReplaceAllString(s, "")
	return nextConfig{export: nextExport.MatchString(s), standalone: nextStandal.MatchString(s)}
}

// readProject parses package.json. ok is false for a missing or unreadable file
// (malformed JSON, or a field of the wrong type).
func readProject(files []string, hasIndex bool, read func(string) []byte) (*project, bool) {
	raw := read("package.json")
	var pkg struct {
		Scripts         map[string]any `json:"scripts"`
		Dependencies    map[string]any `json:"dependencies"`
		DevDependencies map[string]any `json:"devDependencies"`
		Main            string         `json:"main"`
	}
	if json.Unmarshal(raw, &pkg) != nil {
		return nil, false
	}
	p := &project{scripts: map[string]bool{}, deps: map[string]bool{}, devDeps: map[string]bool{}, main: pkg.Main, files: files, hasIndex: hasIndex}
	for name, v := range pkg.Scripts {
		if s, _ := v.(string); strings.TrimSpace(s) != "" {
			p.scripts[name] = true
			if name == "start" {
				p.start = s
			}
		}
	}
	for _, m := range []map[string]any{pkg.Dependencies, pkg.DevDependencies} {
		for k := range m {
			p.deps[k] = true
		}
	}
	for k := range pkg.DevDependencies {
		if _, prod := pkg.Dependencies[k]; !prod {
			p.devDeps[k] = true
		}
	}
	has := map[string]bool{}
	for _, f := range files {
		has[cleanRel(f)] = true
	}
	for _, name := range NextConfigNames {
		if has[name] {
			p.next = parseNextConfig(read(name))
			break
		}
	}
	return p, true
}

func (p *project) hasDepPrefix(prefix string) bool {
	for d := range p.deps {
		if strings.HasPrefix(d, prefix) && d != "@remix-run/router" { // react-router-dom 6 depends on it
			return true
		}
	}
	return false
}

// Frameworks the planner knows by their dependencies.
const (
	fwNone        = ""
	fwSvelteKit   = "SvelteKit"
	fwNext        = "Next.js"
	fwRemix       = "Remix"
	fwReactRouter = "React Router"
	fwNuxt        = "Nuxt"
	fwServer      = "Node.js server" // NestJS, Express, Fastify, Hono, Koa, hapi
	fwAstro       = "Astro"
	fwCRA         = "Create React App"
	fwVite        = "Vite"
)

func (p *project) framework() string {
	switch {
	case p.deps["@sveltejs/kit"]:
		return fwSvelteKit
	case p.deps["next"]:
		return fwNext
	case p.hasDepPrefix("@remix-run/"):
		return fwRemix
	case p.deps["@react-router/dev"]:
		return fwReactRouter
	case p.deps["nuxt"]:
		return fwNuxt
	case p.deps["@nestjs/core"], p.deps["express"], p.deps["fastify"], p.deps["hono"], p.deps["koa"], p.deps["@hapi/hapi"]:
		return fwServer
	case p.deps["astro"]:
		return fwAstro
	case p.deps["react-scripts"]:
		return fwCRA
	case p.deps["vite"]:
		return fwVite
	}
	return fwNone
}

// Detection is what auto mode makes of a Node project. Type is BuildModeNode
// or BuildModeStaticBuild; empty means there is no answer.
type Detection struct {
	Type      string
	Framework string
	Output    string // static build: the default output folder
	SPA       bool   // static build: unknown paths load index.html
	Unsure    bool   // a guess; the person should check the type
	Why       string
}

// staticDefaults is where a framework writes its site and whether it is a
// single-page app (every route is index.html) or a site of real pages.
func staticDefaults(fw string) (output string, spa bool) {
	switch fw {
	case fwCRA:
		return "build", true
	case fwSvelteKit:
		return "build", false
	case fwNext:
		return "out", false
	case fwAstro:
		return "dist", false
	}
	return "dist", true
}

// startFallback is how to start a framework whose package.json has no start
// script (SvelteKit, Nuxt and Astro have none by default), and the ENV lines
// that make it listen where bitploy looks. cmd is nil when there is none.
func (p *project) startFallback(fw string) (cmd []string, env []string) {
	switch fw {
	case fwSvelteKit:
		return []string{"node", "build"}, nil
	case fwNuxt:
		return []string{"node", ".output/server/index.mjs"}, nil
	case fwRemix:
		return []string{"npx", "remix-serve", "./build/server/index.js"}, nil
	case fwReactRouter:
		return []string{"npx", "react-router-serve", "./build/server/index.js"}, nil
	case fwAstro:
		return []string{"node", "./dist/server/entry.mjs"}, []string{"HOST=0.0.0.0"}
	case fwNext:
		return []string{"npx", "next", "start"}, nil
	}
	if m := path.Clean(strings.TrimPrefix(p.main, "./")); p.main != "" && templates.ValidOutputDir(m) && !strings.HasPrefix(m, ".") {
		return []string{"node", m}, nil
	}
	return nil, nil
}

// canStart reports whether the project has a way to start: a start script or
// a known command.
func (p *project) canStart(fw string) bool {
	if p.scripts["start"] {
		return true
	}
	cmd, _ := p.startFallback(fw)
	return cmd != nil
}

// detect is auto mode's decision for a Node project without a Dockerfile.
func (p *project) detect() Detection {
	fw := p.framework()
	hasBuild := p.scripts["build"]
	static := func(why string) Detection {
		out, spa := staticDefaults(fw)
		return Detection{Type: BuildModeStaticBuild, Framework: fw, Output: out, SPA: spa, Why: why}
	}
	node := func(why string) Detection {
		return Detection{Type: BuildModeNode, Framework: fw, Why: why}
	}
	switch fw {
	case fwSvelteKit:
		switch {
		case p.deps["@sveltejs/adapter-static"] && hasBuild:
			return static("SvelteKit with adapter-static: building the site and serving the build folder with nginx.")
		case !p.canStart(fw):
			break
		case !p.deps["@sveltejs/adapter-node"]:
			return node("SvelteKit app: building it and running it as a Node.js app. Add @sveltejs/adapter-node to package.json and use it in svelte.config.js, or it will not start.")
		default:
			return node("SvelteKit with adapter-node: building it and running it as a Node.js app.")
		}
	case fwNext:
		switch {
		case p.next.export && hasBuild:
			return static("Next.js with output export: building the site and serving the out folder with nginx.")
		case hasBuild:
			return node("Next.js app: building it and running it as a Node.js app.")
		}
	case fwRemix, fwReactRouter, fwNuxt, fwServer:
		if p.canStart(fw) {
			if hasBuild {
				return node(fw + " app: building it and running it as a Node.js app.")
			}
			return node(fw + " app: running it as a Node.js app.")
		}
	case fwAstro:
		if p.deps["@astrojs/node"] {
			if p.canStart(fw) {
				return node("Astro with the Node adapter: building it and running it as a Node.js app.")
			}
		} else if hasBuild {
			return static("Astro without a server adapter: building the site and serving the dist folder with nginx.")
		}
	case fwCRA:
		if hasBuild {
			return static("Create React App: building the site and serving the build folder with nginx.")
		}
	case fwVite:
		if hasBuild && p.hasIndex {
			if p.scripts["start"] && !viteDevStart(p.start) {
				d := node("package.json uses Vite and has a start script, so this may be a server app and not a website: running it as a Node.js app. If it is a website, choose Static build as the type.")
				d.Unsure = true
				return d
			}
			return static("package.json has a build script and uses Vite: building the site and serving the dist folder with nginx.")
		}
	}
	switch {
	case p.scripts["start"] && hasBuild:
		return node("package.json has a build and a start script: building it and running it as a Node.js app.")
	case p.scripts["start"]:
		return node("package.json has a start script: running it as a Node.js app.")
	}
	return Detection{}
}

// buildNode makes the plan for a Node.js app: the node template, after the
// build script when there is one, the nextjs template for Next.js with output
// standalone. ok is false when the template cannot be adapted.
func (p *project) buildNode(userIgnore []byte, why string) (BuildPlan, bool) {
	fw := p.framework()
	hasBuild := p.scripts["build"]
	var tpl templates.Template
	var ok, startSet bool
	switch {
	case fw == fwNext && !hasBuild:
		return BuildPlan{}, false // a Next.js app is always built
	case fw == fwNext:
		// The standalone server only fits a project that starts with next start.
		standalone := p.next.standalone && (!p.scripts["start"] || strings.Contains(p.start, "next"))
		tpl, ok = templates.NextJS(p.files, standalone, p.scripts["start"], p.canPrune(fw))
	case fw == fwNuxt && hasBuild && (!p.scripts["start"] || strings.Contains(p.start, ".output/server/index.mjs")):
		// Nuxt 3+: the build is a server that runs by itself. Nuxt 2 starts
		// with nuxt start and has no .output; it takes the case below.
		tpl, ok = templates.NuxtServer(p.files)
		startSet = true
	case hasBuild:
		tpl, ok = templates.NodeWithBuild(p.files, p.canPrune(fw))
	default:
		tpl, ok = templates.ForFiles("node", p.files)
	}
	if !ok {
		return BuildPlan{}, false
	}
	if fw != fwNext && !startSet {
		cmd, env := p.startFallback(fw)
		if p.scripts["start"] {
			cmd = nil // the project's own start script
		}
		if fw == fwSvelteKit {
			// Behind the proxy the app must trust the forwarded address, or form posts fail.
			env = append(env, "PROTOCOL_HEADER=x-forwarded-proto", "HOST_HEADER=x-forwarded-host")
		}
		if cmd != nil || len(env) > 0 {
			if tpl, ok = templates.WithStart(tpl, cmd, env...); !ok {
				return BuildPlan{}, false
			}
		}
	}
	plan := templatePlan(BuildKindNode, NodeDockerfilePath, tpl, userIgnore, why)
	return plan, true
}

// prunable matches a start script that only runs node on a file: node
// dist/index.js, node build, NODE_ENV=production node --env-file=.env
// server.js. Anything else (tsc && node, nest start, node -r ts-node/register,
// node --import=tsx, dotenv -- node, npm run x) may need a dev dependency
// while it runs, so the dev dependencies stay.
var prunable = regexp.MustCompile(`^(?:[A-Za-z_][A-Za-z0-9_]*=\S*\s+)*node(?:\s+--?[A-Za-z][A-Za-z0-9-]*(?:=\S+)?)*\s+[A-Za-z0-9_./@-]+(?:\s+[A-Za-z0-9_./=:@-]+)*$`)

// nodeLoaderFlag matches node options that load code before the app: a
// TypeScript loader from the dev dependencies (ts-node/register, tsx).
var nodeLoaderFlag = regexp.MustCompile(`(?:^|\s)(?:-r|--require|--import|--loader|--experimental-loader)(?:[=\s]|$)`)

// canPrune reports whether the dev dependencies can be removed after the
// build. Only when the start command is plain node on a file (or, without a
// start script, the framework's own server from a dependency): then the build
// output and the dependencies are all it needs. Next.js without standalone is
// never pruned: next start loads next.config.ts with typescript and the
// config's plugins, often dev dependencies.
func (p *project) canPrune(fw string) bool {
	if fw == fwNext || p.scripts["prestart"] {
		return false
	}
	if !p.scripts["start"] {
		cmd, _ := p.startFallback(fw)
		switch {
		case len(cmd) == 0:
			return false
		case cmd[0] == "node":
			return true
		case cmd[0] == "npx" && len(cmd) > 1:
			// The server must come from a dependency, not a dev dependency.
			pkg := map[string]string{"remix-serve": "@remix-run/serve", "react-router-serve": "@react-router/serve"}[cmd[1]]
			return pkg != "" && p.deps[pkg] && !p.devDeps[pkg]
		}
		return false
	}
	start := strings.TrimSpace(p.start)
	return prunable.MatchString(start) && !nodeLoaderFlag.MatchString(start) && !shellControl.MatchString(start)
}

// buildStatic makes the plan for a static build: the site is built with Node
// and the output folder served with nginx. outputDir empty: the default of the
// framework.
func (p *project) buildStatic(userIgnore []byte, outputDir string, spa bool, why string) (BuildPlan, bool) {
	tpl, ok := templates.StaticBuild(p.files, outputDir, spa)
	if !ok {
		return BuildPlan{}, false
	}
	kind, dockerfile := BuildKindNodeStatic, NodeStaticDockerfilePath
	if spa {
		kind, dockerfile = BuildKindNodeSPA, NodeSPADockerfilePath
	}
	plan := templatePlan(kind, dockerfile, tpl, userIgnore, why)
	plan.OutputDir, plan.SPA = outputDir, spa
	return plan, true
}

// autoNodePlan is auto mode's way out for a Node project without a Dockerfile
// (see Detection). userOutput is the app's output folder setting, used when the
// project is a static build.
func autoNodePlan(files []string, hasIndex bool, userIgnore []byte, userOutput string, read func(string) []byte) (BuildPlan, bool) {
	p, ok := readProject(files, hasIndex, read)
	if !ok {
		return BuildPlan{}, false
	}
	d := p.detect()
	switch d.Type {
	case BuildModeStaticBuild:
		out := d.Output
		if userOutput != "" {
			out = userOutput
		}
		plan, ok := p.buildStatic(userIgnore, out, d.SPA, d.Why)
		plan.Unsure = d.Unsure
		return plan, ok
	case BuildModeNode:
		plan, ok := p.buildNode(userIgnore, d.Why)
		plan.Unsure = d.Unsure
		return plan, ok
	}
	return BuildPlan{}, false
}

func cannotBuild(code, msg, hint string) *apperr.Error {
	return apperr.Unprocessable(code, msg, hint)
}

// explicitProject reads package.json for the modes node and static_build,
// which want a Node project and say so when there is none.
func explicitProject(files []string, hasIndex bool, read func(string) []byte, what string) (*project, *apperr.Error) {
	has := false
	for _, f := range files {
		if cleanRel(f) == "package.json" {
			has = true
		}
	}
	if !has {
		return nil, cannotBuild(CodePackageJSONMissing,
			"There is no package.json at the top of your code, so it cannot be built as "+what+".",
			"Put package.json in the top folder, or choose another type.")
	}
	p, ok := readProject(files, hasIndex, read)
	if !ok {
		return nil, cannotBuild(CodePackageJSONInvalid,
			"package.json cannot be read.",
			"Check that package.json is valid JSON, then deploy again.")
	}
	return p, nil
}

// nodeModePlan is the plan for build mode node.
func nodeModePlan(files []string, hasIndex bool, userIgnore []byte, read func(string) []byte) (BuildPlan, *apperr.Error) {
	p, perr := explicitProject(files, hasIndex, read, "a Node.js app")
	if perr != nil {
		return BuildPlan{}, perr
	}
	fw := p.framework()
	if !p.canStart(fw) {
		return BuildPlan{}, cannotBuild(CodeStartScriptMissing,
			"package.json has no start script, so bitploy does not know how to start the app.",
			`Add a start script to package.json, like "start": "node server.js", then deploy again.`)
	}
	why := "The app is set to be a Node.js app: "
	if p.scripts["build"] {
		why += "building it and running it with its start command."
	} else {
		why += "running it with its start command."
	}
	plan, ok := p.buildNode(userIgnore, why)
	if !ok {
		return BuildPlan{}, cannotBuild(CodeStartScriptMissing,
			"bitploy cannot set up this code as a Node.js app.",
			"Add a Dockerfile and choose Own Dockerfile as the type.")
	}
	return plan, nil
}

// staticBuildPlan is the plan for build mode static_build.
func staticBuildPlan(files []string, hasIndex bool, userIgnore []byte, outputDir string, read func(string) []byte) (BuildPlan, *apperr.Error) {
	p, perr := explicitProject(files, hasIndex, read, "a static site")
	if perr != nil {
		return BuildPlan{}, perr
	}
	if !p.scripts["build"] {
		return BuildPlan{}, cannotBuild(CodeBuildScriptMissing,
			"package.json has no build script, so there is nothing to build.",
			`Add a build script to package.json, like "build": "vite build", or set the type to Plain website if the files are ready to serve.`)
	}
	out, spa := staticDefaults(p.framework())
	if outputDir != "" {
		out = outputDir
	}
	if !templates.ValidOutputDir(out) {
		return BuildPlan{}, cannotBuild(CodeOutputDirInvalid, "The output folder "+out+" is not allowed.",
			"Use the folder the build writes to, like dist, build or out.")
	}
	plan, ok := p.buildStatic(userIgnore, out, spa, "The app is set to be a static build: building the site and serving the "+out+" folder with nginx.")
	if !ok {
		return BuildPlan{}, cannotBuild(CodeBuildScriptMissing,
			"bitploy cannot set up this code as a static build.",
			"Add a Dockerfile and choose Own Dockerfile as the type.")
	}
	return plan, nil
}
