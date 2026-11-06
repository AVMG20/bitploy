package deploy

import (
	"bytes"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/templates"
)

// Build modes: how an app's source becomes an image. The mode is a setting of
// the app, so deploys from the dashboard, the REST API, MCP and GitHub pushes
// all decide the same way.
const (
	// BuildModeDockerfile needs a Dockerfile in the source. This is the default
	// and the rule of SPEC "The Dockerfile rule".
	BuildModeDockerfile = "dockerfile"
	// BuildModeAuto builds the Dockerfile when there is one, else serves a plain
	// website (index.html at the top, or another single page) with the static
	// template.
	BuildModeAuto = "auto"
	// BuildModeStatic always serves the source as a plain website with the
	// static template, even when the source holds a Dockerfile of its own.
	BuildModeStatic = "static"
	// BuildModeNode always runs the source as a Node.js app with the node
	// template (or nextjs), after its build script if it has one, even when the
	// source holds a Dockerfile of its own.
	BuildModeNode = "node"
	// BuildModeStaticBuild always builds the source with its build script and
	// serves the output folder with nginx (node-spa or node-static template).
	BuildModeStaticBuild = "static_build"
	// BuildModeCompose runs the services of the source's compose file
	// (compose.yaml, docker-compose.yml, ...): each one built from its
	// Dockerfile or pulled as an image (docs/compose-plan.md).
	BuildModeCompose = "compose"
)

// Kinds of build a plan can ask for.
const (
	BuildKindDockerfile = "dockerfile"
	BuildKindStatic     = "static"
	// BuildKindNodeSPA, BuildKindNodeStatic and BuildKindNode are the template
	// builds for a Node project without a Dockerfile: a single-page app built
	// and served with nginx (unknown paths load index.html), a site of real
	// pages built and served with nginx (unknown paths are a 404), or an app
	// started with its "start" script.
	BuildKindNodeSPA    = "node-spa"
	BuildKindNodeStatic = "node-static"
	BuildKindNode       = "node"
	// BuildKindCompose is a deployment of the services of a compose file.
	BuildKindCompose = "compose"
)

// IsStaticKind reports whether a build kind runs only bitploy's own nginx
// serving files (the static, node-spa and node-static templates). Such an app is a static
// site: it uses a few MB, so it is free of the memory and CPU allowance. A
// Dockerfile of the user never qualifies, even when it starts from nginx: it
// can run anything. The SQL queries that leave static sites out of the quota
// (SumAppResourcesByUser, OpsUsageAppTotals) list the same kinds.
func IsStaticKind(kind string) bool {
	return kind == BuildKindStatic || kind == BuildKindNodeSPA || kind == BuildKindNodeStatic
}

// AlwaysStaticMode reports whether an app in this build mode can only ever
// build a static site (a plain website or a static build), so it is one from
// the day it is created.
func AlwaysStaticMode(mode string) bool {
	return mode == BuildModeStatic || mode == BuildModeStaticBuild
}

// StaticDockerfilePath is where the static template is written in the source.
// It has its own name so a Dockerfile of the user is never overwritten.
const StaticDockerfilePath = "Dockerfile.bitploy-static"

// Where the Node templates are written in the source (see StaticDockerfilePath).
const (
	NodeSPADockerfilePath    = "Dockerfile.bitploy-node-spa"
	NodeStaticDockerfilePath = "Dockerfile.bitploy-node-static"
	NodeDockerfilePath       = "Dockerfile.bitploy-node"
)

// CodeNothingToBuild is the error code for a source in auto mode that has
// neither a Dockerfile nor a plain website to serve.
const CodeNothingToBuild = "nothing_to_build"

// ValidBuildMode reports whether m is one of the build modes.
func ValidBuildMode(m string) bool {
	switch m {
	case BuildModeDockerfile, BuildModeAuto, BuildModeStatic, BuildModeNode, BuildModeStaticBuild, BuildModeCompose:
		return true
	}
	return false
}

// BuildPlan is the outcome of PlanBuild.
type BuildPlan struct {
	Kind string // BuildKindDockerfile, BuildKindStatic, BuildKindNodeSPA or BuildKindNode
	// DockerfilePath is the injected Dockerfile of a template build (not set for BuildKindDockerfile).
	DockerfilePath string
	// For every kind but BuildKindDockerfile: the files to add to the source before building, as
	// clean relative paths with their content. Dockerignore holds the user's
	// own .dockerignore (if any) followed by the template's, so .env, .git and
	// node_modules never end up in the image.
	Inject map[string][]byte
	// HomePage is set when a static site has no index.html but one page to
	// open at "/": a root file such as "Planning tool v3.html". It is copied to
	// index.html in the build context (see app.writeInjected), so the name never
	// reaches the Dockerfile or the nginx config, and it still answers at its
	// own path too.
	HomePage string
	// OutputDir and SPA are set for a static build: the folder served and
	// whether unknown paths load index.html.
	OutputDir string
	SPA       bool
	// Unsure is set when auto mode guessed and the person should check the type.
	Unsure bool
	// Why says in one plain sentence which way was chosen (shown in the build log).
	Why string
}

// Codes for a static site without index.html whose home page is not clear.
const (
	CodeHomePageUnclear = "home_page_unclear"
	CodeHomePageMissing = "home_page_missing"
)

// IsHTMLName reports whether name ends in .html or .htm (any case).
func IsHTMLName(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	return ext == ".html" || ext == ".htm"
}

// rootPages lists the .html and .htm files at the top of the source, sorted.
func rootPages(has map[string]bool) []string {
	var out []string
	for f := range has {
		if !strings.Contains(f, "/") && IsHTMLName(f) {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

// homePage picks the page to serve at "/" when there is no index.html: the
// app's choice when the source has it, else the only page at the top. ok is
// false when there is no page at all; err is set when there are several and
// none is chosen (or the chosen one is gone).
func homePage(has map[string]bool, chosen string) (page string, ok bool, err *apperr.Error) {
	pages := rootPages(has)
	if strings.Contains(chosen, "/") || !IsHTMLName(chosen) {
		chosen = "" // only a page at the top can be the home page
	}
	if chosen != "" && has[chosen] {
		return chosen, true, nil
	}
	switch len(pages) {
	case 0:
		return "", false, nil
	case 1:
		return pages[0], true, nil
	}
	if other := otherCase(has, "index.html"); other != "" && chosen == "" {
		return "", false, renameErr(CodeHomePageUnclear, other, "index.html")
	}
	list := strings.Join(pages, ", ")
	if len(pages) > 5 {
		list = strings.Join(pages[:5], ", ") + fmt.Sprintf(" and %d more", len(pages)-5)
	}
	var e *apperr.Error
	if chosen != "" {
		e = apperr.Unprocessable(CodeHomePageMissing,
			fmt.Sprintf("The home page %s is not in these files. There are several pages at the top (%s) and no index.html.", chosen, list),
			"Rename the page people should see first to index.html, or choose the home page again when you upload in the dashboard.")
	} else {
		e = apperr.Unprocessable(CodeHomePageUnclear,
			fmt.Sprintf("There are several pages at the top (%s) and no index.html, so it is not clear which one people should see first.", list),
			"Rename the page people should see first to index.html, or choose it as the home page when you upload in the dashboard.")
	}
	e.WithDetail("pages", pages)
	return "", false, e
}

// PlanBuild decides how a source is built. files are all file paths of the
// source relative to its root; dockerfilePath is the app's setting; read
// returns the content of a file of the source (nil when it has none): the
// planner reads .dockerignore, and package.json and the Next.js config for a
// Node project (modes auto, node and static_build). outputDir is the app's
// setting for the folder a static build serves; empty means the default of the
// detected framework.
//
// homePage is the app's choice of page for "/" when a static site has no
// index.html and several pages; it is only used in auto and static mode.
//
// With mode dockerfile the plan is a Dockerfile build, unless the source has
// no Dockerfile and the auto rules find a static site (plain website files or
// a front end that builds to files): then it is that static plan. A missing
// Dockerfile is otherwise reported by CheckDockerfile. An unknown mode is a
// Dockerfile build.
func PlanBuild(mode, dockerfilePath, homePageChoice, outputDir string, files []string, read func(rel string) []byte) (BuildPlan, *apperr.Error) {
	if dockerfilePath == "" {
		dockerfilePath = "Dockerfile"
	}
	has := map[string]bool{}
	for _, f := range files {
		has[cleanRel(f)] = true
	}
	hasDockerfile := has[path.Clean(dockerfilePath)]
	hasIndex := has["index.html"]
	dockerignore := read(".dockerignore")

	switch mode {
	case BuildModeNode:
		return nodeModePlan(files, hasIndex, dockerignore, read)
	case BuildModeStaticBuild:
		return staticBuildPlan(files, hasIndex, dockerignore, outputDir, read)
	case BuildModeStatic:
		if !hasIndex {
			page, ok, herr := homePage(has, homePageChoice)
			if herr != nil {
				return BuildPlan{}, herr
			}
			if ok {
				return homePlan(dockerignore, page), nil
			}
			return BuildPlan{}, apperr.Unprocessable("static_index_missing",
				"There is no .html page at the top of your code, so there is no website to show.",
				"Put index.html, or your page, in the top folder next to your other website files, then deploy again.")
		}
		return staticPlan(dockerignore, "The app is set to be a static site, so it is served as plain website files with nginx."), nil
	case BuildModeAuto:
		if hasDockerfile {
			return BuildPlan{Kind: BuildKindDockerfile, Why: "Found a Dockerfile, building it."}, nil
		}
		stack := DetectStack(files)
		if hasIndex && stack == "static" {
			return staticPlan(dockerignore, "No Dockerfile, but there is an index.html: serving the files as a static site with nginx."), nil
		}
		if other := otherCase(has, path.Clean(dockerfilePath)); other != "" {
			return BuildPlan{}, renameErr(CodeNothingToBuild, other, path.Clean(dockerfilePath))
		}
		if (stack == "node" || stack == "nextjs" || stack == "node-static") && has["package.json"] {
			if plan, ok := autoNodePlan(files, hasIndex, dockerignore, outputDir, read); ok {
				return plan, nil
			}
		}
		if !hasIndex && stack == "static" {
			page, ok, herr := homePage(has, homePageChoice)
			if herr != nil {
				return BuildPlan{}, herr
			}
			if ok {
				return homePlan(dockerignore, page), nil
			}
		}
		return BuildPlan{}, nothingToBuild(files, len(rootPages(has)) > 0)
	case BuildModeDockerfile:
		// A static site needs no Dockerfile in any mode: an agent (MCP and
		// REST make apps in this mode) that sends plain website files, or a
		// Vite, React, Astro or other front end that builds to files, gets
		// bitploy's own static hosting, which is free, instead of an error or
		// a hand-written nginx image. Anything else still needs a Dockerfile.
		if !hasDockerfile {
			if plan, err := PlanBuild(BuildModeAuto, dockerfilePath, homePageChoice, outputDir, files, read); err == nil && IsStaticKind(plan.Kind) {
				return plan, nil
			}
		}
	}
	return BuildPlan{Kind: BuildKindDockerfile}, nil
}

// otherCase returns a file whose name differs from want only in upper and
// lower case ("Index.html" for "index.html"), or "". Linux, nginx and Docker
// match names exactly, so such a file is not found.
func otherCase(has map[string]bool, want string) string {
	for f := range has {
		if f != want && strings.EqualFold(f, want) {
			return f
		}
	}
	return ""
}

func renameErr(code, got, want string) *apperr.Error {
	return apperr.Unprocessable(code,
		fmt.Sprintf("Found %s, but the name must be exactly %s: upper and lower case matter.", got, want),
		fmt.Sprintf("Rename %s to %s, then deploy again.", got, want))
}

// homePlan is a static plan whose page for "/" is a root file not named index.html.
func homePlan(userIgnore []byte, page string) BuildPlan {
	p := staticPlan(userIgnore, "No Dockerfile or index.html: serving the files as a static site with nginx. The home page is "+page+".")
	p.HomePage = page
	return p
}

func staticPlan(userIgnore []byte, why string) BuildPlan {
	tpl, _ := templates.Get("static")
	return templatePlan(BuildKindStatic, StaticDockerfilePath, tpl, userIgnore, why)
}

// templatePlan injects a template as dockerfilePath, with the user's own
// .dockerignore followed by the template's.
func templatePlan(kind, dockerfilePath string, tpl templates.Template, userIgnore []byte, why string) BuildPlan {
	var ign bytes.Buffer
	if len(bytes.TrimSpace(userIgnore)) > 0 {
		ign.Write(bytes.TrimRight(userIgnore, "\r\n"))
		ign.WriteByte('\n')
	}
	ign.WriteString(tpl.Dockerignore)
	if !strings.HasSuffix(tpl.Dockerignore, "\n") {
		ign.WriteByte('\n')
	}
	ign.WriteString(dockerfilePath + "\n")
	return BuildPlan{
		Kind:           kind,
		DockerfilePath: dockerfilePath,
		Inject: map[string][]byte{
			dockerfilePath:  []byte(tpl.Dockerfile),
			".dockerignore": ign.Bytes(),
		},
		Why: why,
	}
}

// nothingToBuild is the plain-words error for auto mode. The details carry the
// matching template, so an AI assistant can fix the project without asking.
func nothingToBuild(files []string, hasPage bool) *apperr.Error {
	stack := DetectStack(files)
	name := templateStackFor(stack)
	tpl, _ := templates.Get(name)
	msg := "No Dockerfile or index.html found."
	hint := "Add a Dockerfile, or an index.html for a plain website, at the top of your code. Your AI assistant can write the Dockerfile."
	if hasPage {
		msg = "No Dockerfile found."
		if lang := stackLanguage(stack); lang != "" {
			msg = fmt.Sprintf("No Dockerfile found. This is a %s app, so it has to be built before it can be shown.", lang)
			hint = "Add a Dockerfile (your AI assistant can write one), or upload the built website instead, usually the dist or build folder."
		}
	}
	e := apperr.Unprocessable(CodeNothingToBuild, msg, hint)
	e.WithDetail("stack", stack)
	e.WithDetail("template", name)
	e.WithDetail("dockerfile", tpl.Dockerfile)
	e.WithDetail("dockerignore", tpl.Dockerignore)
	e.WithNext("get_template")
	return e
}

func stackLanguage(stack string) string {
	switch stack {
	case "node":
		return "Node.js"
	case "python":
		return "Python"
	case "go":
		return "Go"
	case "php":
		return "PHP"
	}
	return ""
}
