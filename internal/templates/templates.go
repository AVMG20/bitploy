// Package templates embeds the Dockerfile templates handed to agents.
// OWNER: MCP agent. Layout: <stack>/Dockerfile, <stack>/.dockerignore, <stack>/README.md
package templates

import (
	"embed"
	"strings"
)

// The dotfile is named explicitly because go:embed skips dotfiles in globs.
//
//go:embed static/Dockerfile static/.dockerignore static/README.md
//go:embed node/Dockerfile node/.dockerignore node/README.md
//go:embed node-spa/Dockerfile node-spa/.dockerignore node-spa/README.md
//go:embed node-static/Dockerfile node-static/.dockerignore node-static/README.md
//go:embed nextjs/Dockerfile nextjs/.dockerignore nextjs/README.md
//go:embed python/Dockerfile python/.dockerignore python/README.md
//go:embed django/Dockerfile django/.dockerignore django/README.md
//go:embed go/Dockerfile go/.dockerignore go/README.md
//go:embed php/Dockerfile php/.dockerignore php/README.md
//go:embed laravel/Dockerfile laravel/.dockerignore laravel/README.md
//go:embed generic/Dockerfile generic/.dockerignore generic/README.md
var files embed.FS

// Stacks in the order shown to agents.
var Stacks = []string{"static", "node-spa", "node-static", "node", "nextjs", "python", "django", "go", "php", "laravel", "generic"}

type Template struct {
	Stack        string
	Dockerfile   string
	Dockerignore string
	Readme       string
}

// Get returns a template by stack name; ok is false for unknown stacks.
// The name is matched case-insensitively.
func Get(stack string) (Template, bool) {
	stack = strings.ToLower(strings.TrimSpace(stack))
	known := false
	for _, s := range Stacks {
		if s == stack {
			known = true
			break
		}
	}
	if !known {
		return Template{}, false
	}
	read := func(name string) string {
		b, err := files.ReadFile(stack + "/" + name)
		if err != nil {
			return ""
		}
		return string(b)
	}
	return Template{
		Stack:        stack,
		Dockerfile:   read("Dockerfile"),
		Dockerignore: read(".dockerignore"),
		Readme:       read("README.md"),
	}, true
}
