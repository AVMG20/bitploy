package app

// OWNER: MCP agent. Templates, deploy rules, Dockerfile validation and git
// sources. Signatures are a shared contract; implement the bodies.

import (
	"context"
	"database/sql"
	"strings"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/deploy"
	"github.com/AVMG20/bitploy/internal/mcp/rules"
	"github.com/AVMG20/bitploy/internal/store/db"
	"github.com/AVMG20/bitploy/internal/templates"
)

func templateFrom(t templates.Template) Template {
	return Template{Stack: t.Stack, Dockerfile: t.Dockerfile, Dockerignore: t.Dockerignore, Readme: t.Readme}
}

func (s *Service) ListTemplates(ctx context.Context, a Actor) ([]Template, error) {
	out := make([]Template, 0, len(templates.Stacks))
	for _, st := range templates.Stacks {
		if t, ok := templates.Get(st); ok {
			out = append(out, templateFrom(t))
		}
	}
	return out, nil
}

// GetTemplate returns one stack; unknown stacks → template_unknown listing the stacks.
func (s *Service) GetTemplate(ctx context.Context, a Actor, stack string) (Template, error) {
	t, ok := templates.Get(stack)
	if !ok {
		return Template{}, apperr.NotFound("template_unknown",
			"There is no template called "+quote(strings.TrimSpace(stack))+".",
			"Choose one of: "+strings.Join(templates.Stacks, ", ")+". Use static for plain HTML files, node for a Node.js server, node-spa for a Vue/React/Vite site, and generic for anything else.").
			WithNext("get_template")
	}
	return templateFrom(t), nil
}

// DeployRules returns the server instructions plus the runtime contract (markdown).
func (s *Service) DeployRules(ctx context.Context, a Actor) (string, error) {
	return "# Bitploy deploy rules\n\n" +
		rules.Instructions(s.Cfg.AppURL("<slug>"), s.Cfg.AppsDomain) +
		"\n" + rules.RuntimeContract, nil
}

const maxDockerfileBytes = 256 << 10

// ValidateDockerfile runs the static checks without building.
func (s *Service) ValidateDockerfile(ctx context.Context, a Actor, dockerfile string, fileList []string, appPort int) (ValidateResult, error) {
	return s.validate(dockerfile, fileList, appPort, "web")
}

// ValidateDockerfileFor validates against an existing app: its port setting,
// type (workers skip port checks) and Dockerfile path are used.
func (s *Service) ValidateDockerfileFor(ctx context.Context, a Actor, ref, dockerfile string, fileList []string) (ValidateResult, error) {
	row, err := s.LoadApp(ctx, a, ref, false)
	if err != nil {
		return ValidateResult{}, err
	}
	port := 0
	if row.Port.Valid {
		port = int(row.Port.Int64)
	}
	return s.validate(dockerfile, fileList, port, row.Type)
}

func (s *Service) validate(dockerfile string, fileList []string, appPort int, appType string) (ValidateResult, error) {
	if len(dockerfile) > maxDockerfileBytes {
		return ValidateResult{}, apperr.BadRequest("dockerfile_too_large",
			"The Dockerfile is larger than 256 KB, which is far more than a Dockerfile needs.",
			"Send only the Dockerfile text, not other files.")
	}
	if len(fileList) > 20000 {
		fileList = fileList[:20000]
	}
	in := deploy.CheckInput{
		DockerfilePath: "Dockerfile",
		Files:          fileList,
		AppPort:        appPort,
		AppType:        appType,
	}
	if strings.TrimSpace(dockerfile) != "" {
		in.Dockerfile = []byte(dockerfile)
	}
	res := deploy.CheckDockerfile(in)
	out := ValidateResult{
		OK:             res.OK,
		Findings:       res.Findings,
		Port:           res.Port,
		HasHealthcheck: res.HasHealthcheck,
		Stack:          res.Stack,
	}
	if out.Findings == nil {
		out.Findings = []deploy.Finding{}
	}
	if res.OK {
		out.NextSteps = []string{
			"Deploy it: deploy_files with ALL project files, it replaces the whole source (up to 2 MB and 200 files), or create_upload for a zip.",
			"Read any warnings first; they do not block the deploy.",
		}
	} else {
		out.NextSteps = []string{"Fix every finding with severity error, then call validate_dockerfile again."}
		if e := deploy.FirstError(res, fileList); e != nil && e.Code == deploy.CodeDockerfileMissing {
			out.NextSteps = []string{"Call get_template with stack \"" + templateName(e) + "\", save its Dockerfile and .dockerignore into the project, adapt them, then call validate_dockerfile again."}
		}
	}
	return out, nil
}

func templateName(e *apperr.Error) string {
	if n, ok := e.Details["template"].(string); ok {
		return n
	}
	return "generic"
}

// SetGitSource saves repo/branch/subdir and checks access with ls-remote
// (using the user's GitHub token for github.com repos when set).
func (s *Service) SetGitSource(ctx context.Context, a Actor, ref string, src GitSource) (App, error) {
	row, err := s.LoadApp(ctx, a, ref, true)
	if err != nil {
		return App{}, err
	}
	repo, err := deploy.NormalizeGitURL(src.RepoURL)
	if err != nil {
		return App{}, err
	}
	subdir, err := deploy.CleanSubdir(src.Subdir)
	if err != nil {
		return App{}, err
	}
	gs := deploy.GitSource{
		URL:         repo,
		Branch:      strings.TrimSpace(src.Branch),
		Subdir:      subdir,
		SettingsURL: strings.TrimRight(s.Cfg.PublicURL, "/") + "/settings/github",
	}
	if deploy.IsGitHubURL(repo) {
		tok, err := s.GitHubTokenFor(ctx, row.UserID)
		if err != nil {
			return App{}, err
		}
		gs.Token = tok
	}
	sha, branch, err := deploy.LsRemoteInfo(ctx, gs)
	if err != nil {
		return App{}, err
	}
	err = s.Q.SetAppGitSource(ctx, db.SetAppGitSourceParams{
		GitRepoUrl: sql.NullString{String: repo, Valid: true},
		GitBranch:  sql.NullString{String: branch, Valid: true},
		GitSubdir:  nullStr(subdir),
		UpdatedAt:  s.now(),
		ID:         row.ID,
	})
	if err != nil {
		return App{}, apperr.Internal(err)
	}
	s.Audit(ctx, a, "app.git_source_set", "app", row.ID, map[string]any{
		"repo_url": repo, "branch": branch, "subdir": subdir, "commit": sha, "private_access": gs.Token != "",
	})
	return s.GetApp(ctx, a, row.Slug)
}
