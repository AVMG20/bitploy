// Package templates embeds the Dockerfile templates handed to agents.
// OWNER: MCP agent. Layout: <stack>/Dockerfile, <stack>/.dockerignore, <stack>/README.md
package templates

// Stacks in the order shown to agents.
var Stacks = []string{"static", "node", "node-spa", "python", "go", "php", "generic"}

type Template struct {
	Stack        string
	Dockerfile   string
	Dockerignore string
	Readme       string
}

// Get returns a template by stack name; ok is false for unknown stacks.
func Get(stack string) (Template, bool) { return Template{}, false }
