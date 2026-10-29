package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/AVMG20/bitploy/internal/app"
	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/templates"
)

const (
	rulesURI         = "bitploy://rules"
	templateURIStart = "bitploy://templates/"
)

func (s *srv) registerResources() {
	s.mcp.AddResource(&mcp.Resource{
		URI:         rulesURI,
		Name:        "rules",
		Title:       "Bitploy deploy rules",
		Description: "The rules every deploy follows and the runtime contract (port, logs, /data).",
		MIMEType:    "text/markdown",
	}, s.readRules)

	handler := s.readTemplate
	s.mcp.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: templateURIStart + "{stack}",
		Name:        "template",
		Title:       "Dockerfile template",
		Description: "A Dockerfile, .dockerignore and notes for a stack: " + strings.Join(templates.Stacks, ", ") + ".",
		MIMEType:    "text/markdown",
	}, handler)
	// Concrete entries too, because many clients only list resources and ignore templates.
	for _, st := range templates.Stacks {
		s.mcp.AddResource(&mcp.Resource{
			URI:         templateURIStart + st,
			Name:        "template-" + st,
			Title:       "Dockerfile template: " + st,
			Description: "Dockerfile and .dockerignore for the " + st + " stack.",
			MIMEType:    "text/markdown",
		}, handler)
	}
}

func (s *srv) resourceActor(extra *mcp.RequestExtra) (app.Actor, error) {
	return actorFrom(extra)
}

func (s *srv) readRules(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	a, err := s.resourceActor(req.Extra)
	if err != nil {
		return nil, err
	}
	md, err := s.svc.DeployRules(ctx, a)
	if err != nil {
		return nil, err
	}
	return textResource(req.Params.URI, md), nil
}

func (s *srv) readTemplate(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	a, err := s.resourceActor(req.Extra)
	if err != nil {
		return nil, err
	}
	uri := req.Params.URI
	if !strings.HasPrefix(uri, templateURIStart) {
		return nil, mcp.ResourceNotFoundError(uri)
	}
	t, err := s.svc.GetTemplate(ctx, a, strings.TrimPrefix(uri, templateURIStart))
	if err != nil {
		if apperr.As(err).Code == "template_unknown" {
			return nil, mcp.ResourceNotFoundError(uri)
		}
		return nil, err
	}
	return textResource(uri, templateMarkdown(t)), nil
}

func templateMarkdown(t app.Template) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Template: %s\n\n", t.Stack)
	b.WriteString("Save this as `Dockerfile` in the top folder of the project:\n\n```dockerfile\n")
	b.WriteString(strings.TrimRight(t.Dockerfile, "\n"))
	b.WriteString("\n```\n\nSave this as `.dockerignore` next to it:\n\n```\n")
	b.WriteString(strings.TrimRight(t.Dockerignore, "\n"))
	b.WriteString("\n```\n\n")
	b.WriteString(t.Readme)
	return b.String()
}

func textResource(uri, text string) *mcp.ReadResourceResult {
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "text/markdown", Text: text}}}
}

func (s *srv) registerPrompts() {
	s.mcp.AddPrompt(&mcp.Prompt{
		Name:        "deploy_project",
		Title:       "Deploy this project",
		Description: "Walks through putting the current project online with bitploy, step by step.",
		Arguments: []*mcp.PromptArgument{
			{Name: "app", Title: "App name or slug", Description: "An existing app to update. Leave empty to create a new one."},
		},
	}, func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		target := strings.TrimSpace(req.Params.Arguments["app"])
		return &mcp.GetPromptResult{
			Description: "Deploy the current project with bitploy",
			Messages: []*mcp.PromptMessage{{
				Role:    "user",
				Content: &mcp.TextContent{Text: deployPrompt(target, Instructions(s.svc))},
			}},
		}, nil
	})
}

func deployPrompt(app, instructions string) string {
	target := "Create a new app for it (create_app, with a short readable name)."
	if app != "" {
		target = fmt.Sprintf("Update the existing app %q (get_app) instead of creating a new one.", app)
	}
	return `Put the project in the current folder online with bitploy. Follow these steps in order, and do not skip validation.

1. Look at the project: which files exist, what language, and what port the app listens on. Check for an existing Dockerfile.
2. No Dockerfile? Call get_template with the stack that fits (static for plain HTML, node, node-spa, python, go, php, or generic). Save its Dockerfile and .dockerignore into the project and adapt them. The app must listen on 0.0.0.0 at the EXPOSE port (also in $PORT), never on localhost.
3. Call validate_dockerfile with the Dockerfile text and the list of project files. Fix every error. Read the warnings.
4. ` + target + `
5. Secrets (API keys, passwords) never go into files. Set them with set_env.
6. Send the code. Up to 2 MB and 200 files: deploy_files. Bigger: zip the folder, call create_upload and run the curl command it returns, then deploy with the upload_id. A git repository: set_git_source, then deploy.
7. deploy and deploy_files return at once. Call get_deployment with wait_seconds=30 until the state is running or failed. If it failed, read error.hint and the log tail, fix the cause and deploy again. After 3 failures with the same error code, stop and explain the problem to the user in plain words.
8. When it runs, give the user the URL and one or two plain sentences on what you did. Assume the user is not technical.

Never stop, delete or roll back an app the user did not name.

Reference (the same rules the server announces):

` + instructions
}
