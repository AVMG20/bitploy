package deploy

import (
	"bytes"
	"fmt"
	"path"
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
	// website (index.html at the top) with the static template.
	BuildModeAuto = "auto"
	// BuildModeStatic always serves the source as a plain website with the
	// static template, even when the source holds a Dockerfile of its own.
	BuildModeStatic = "static"
)

// Kinds of build a plan can ask for.
const (
	BuildKindDockerfile = "dockerfile"
	BuildKindStatic     = "static"
)

// StaticDockerfilePath is where the static template is written in the source.
// It has its own name so a Dockerfile of the user is never overwritten.
const StaticDockerfilePath = "Dockerfile.bitploy-static"

// CodeNothingToBuild is the error code for a source in auto mode that has
// neither a Dockerfile nor a plain website to serve.
const CodeNothingToBuild = "nothing_to_build"

// ValidBuildMode reports whether m is one of the three modes.
func ValidBuildMode(m string) bool {
	return m == BuildModeDockerfile || m == BuildModeAuto || m == BuildModeStatic
}

// BuildPlan is the outcome of PlanBuild.
type BuildPlan struct {
	Kind string // BuildKindDockerfile or BuildKindStatic
	// For BuildKindStatic: the files to add to the source before building, as
	// clean relative paths with their content. Dockerignore holds the user's
	// own .dockerignore (if any) followed by the template's, so .env, .git and
	// node_modules never end up in the image.
	Inject map[string][]byte
	// Why says in one plain sentence which way was chosen (shown in the build log).
	Why string
}

// PlanBuild decides how a source is built. files are all file paths of the
// source relative to its root; dockerfilePath is the app's setting; dockerignore
// is the content of the source's own .dockerignore, or nil.
//
// With mode dockerfile (or an unknown mode) the plan is always a Dockerfile
// build: a missing Dockerfile is reported by CheckDockerfile as before.
func PlanBuild(mode, dockerfilePath string, files []string, dockerignore []byte) (BuildPlan, *apperr.Error) {
	if dockerfilePath == "" {
		dockerfilePath = "Dockerfile"
	}
	has := map[string]bool{}
	for _, f := range files {
		has[cleanRel(f)] = true
	}
	hasDockerfile := has[path.Clean(dockerfilePath)]
	hasIndex := has["index.html"]

	switch mode {
	case BuildModeStatic:
		if !hasIndex {
			return BuildPlan{}, apperr.Unprocessable("static_index_missing",
				"There is no index.html at the top of the project, so there is no website to show.",
				"Put index.html in the top folder, next to your other website files, then deploy again.")
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
		return BuildPlan{}, nothingToBuild(files, hasIndex)
	}
	return BuildPlan{Kind: BuildKindDockerfile}, nil
}

func staticPlan(userIgnore []byte, why string) BuildPlan {
	tpl, _ := templates.Get("static")
	var ign bytes.Buffer
	if len(bytes.TrimSpace(userIgnore)) > 0 {
		ign.Write(bytes.TrimRight(userIgnore, "\r\n"))
		ign.WriteByte('\n')
	}
	ign.WriteString(tpl.Dockerignore)
	if !strings.HasSuffix(tpl.Dockerignore, "\n") {
		ign.WriteByte('\n')
	}
	ign.WriteString(StaticDockerfilePath + "\n")
	return BuildPlan{
		Kind: BuildKindStatic,
		Inject: map[string][]byte{
			StaticDockerfilePath: []byte(tpl.Dockerfile),
			".dockerignore":      ign.Bytes(),
		},
		Why: why,
	}
}

// nothingToBuild is the plain-words error for auto mode. The details carry the
// matching template, so an AI assistant can fix the project without asking.
func nothingToBuild(files []string, hasIndex bool) *apperr.Error {
	stack := DetectStack(files)
	name := templateStackFor(stack)
	tpl, _ := templates.Get(name)
	msg := "No Dockerfile or index.html found."
	if hasIndex {
		msg = "No Dockerfile found."
		if lang := stackLanguage(stack); lang != "" {
			msg = fmt.Sprintf("No Dockerfile found. This is a %s project, so it cannot be shown as a plain website.", lang)
		}
	}
	e := apperr.Unprocessable(CodeNothingToBuild, msg,
		"Add a Dockerfile, or an index.html for a plain website, at the top of the project. Your AI assistant can set this up for you.")
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
