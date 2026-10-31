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
		Description: "Everything an agent needs: the Dockerfile rule, runtime contract (0.0.0.0:$PORT, /data), limits, how to update an app later, how env and setting changes apply, a fix per error code, custom domains.",
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
	md, err := s.rulesDoc(ctx, a)
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
	target := "Which app? (a) The project has a .bitploy file: update that app (the UPDATE path below). (b) No .bitploy, but the user says the project is already online: find it with list_apps and confirm with the user by name and url (ask which if several could match). (c) Otherwise call create_app with a short readable name for a NEW app, and never reuse an app only because its name looks similar. After creating, save {\"app\":\"<slug>\",\"url\":\"<url>\"} as .bitploy in the project (do not deploy that file)."
	if app != "" {
		target = fmt.Sprintf("Update the existing app %q (get_app) instead of creating a new one: this is the UPDATE path below.", app)
	}
	return `Put the project in the current folder online with bitploy. Follow these steps in order, and do not skip validation.

1. Look at the project: which files exist, what language, and what port the app listens on. Check for an existing Dockerfile and for a .bitploy file.
2. No Dockerfile? Call get_template with the stack that fits (static for plain HTML, node-spa for a Vite/React/Vue build, node, python, go, php, or generic). Save its Dockerfile and .dockerignore into the project and adapt them. The app must listen on 0.0.0.0 at the EXPOSE port (also in $PORT), never on localhost. A bot with no web port: create the app with type=worker.
3. Call validate_dockerfile with the Dockerfile text and the list of project files. Fix every error. Read the warnings.
4. ` + target + `
5. Secrets (API keys, passwords, bot tokens) never go into files. Set them with set_env. They reach the app after restart_app, or with the first deploy.
6. Send the code. The project already has a GitHub remote? Prefer git: set_git_source with its HTTPS URL, then deploy once; afterwards every push to the branch deploys by itself. A private repository needs the user to press Connect GitHub in the dashboard under Settings > GitHub; ask the user. No GitHub remote? Upload, which is fully supported: up to 2 MB and 200 files deploy_files with ALL files; bigger: zip the folder, call create_upload and run the curl command it returns, then deploy with the upload_id. Never ask the user to create a repository or install git just to deploy; at most mention once that connecting GitHub makes updates automatic.
7. deploy and deploy_files return at once, and a push to a linked repository starts a deployment too. Call get_deployment (id, or app=<slug> for the latest) with wait_seconds=30 until the state is running or failed. If it failed, read error.hint and the log tail, fix the cause and deploy again. After 3 failures with the same error code, stop and explain the problem to the user in plain words.
8. When it runs, give the user the URL and one or two plain sentences on what you did. Assume the user is not technical.

UPDATE path (the app already exists, also in a new session):
- Find the app: the .bitploy file. Without it, only if the user says the project is already online: list_apps and confirm by name and url with the user. Never take an app just because its name looks similar. Never create a second app for a project that has a .bitploy.
- Linked to a git repository (get_app shows git_source)? Commit and push: with push_ready true bitploy deploys it by itself, then follow it with get_deployment (app=<slug>). Call deploy only when push_ready is false or to rebuild. Uploads are refused for a linked app (git_source_linked).
- Otherwise deploy_files again with app=<slug> and ALL files. It is a full snapshot that replaces the old source, not a patch. For a zip source: create_upload first, then deploy.
- Only env vars or settings changed: set_env then restart_app (app running), or deploy with only app= (app failed or never ran). volume, memory and CPU (update_app) apply on restart_app.
- Then step 7 and 8 as above. The URL stays the same.

Never stop, delete or roll back an app the user did not name.

Reference (the same rules the server announces; get_deploy_rules has the full fix list per error code):

` + instructions
}
