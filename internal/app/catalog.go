package app

// OWNER: MCP agent. Templates, deploy rules, Dockerfile validation and git
// sources. Signatures are a shared contract; implement the bodies.

import (
	"context"
	"database/sql"
	"strings"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/deploy"
	"github.com/AVMG20/bitploy/internal/github"
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
	return s.GetTemplateForFiles(ctx, a, stack, nil)
}

// GetTemplateForFiles is GetTemplate with the project's file paths: the
// Dockerfile then matches the project's package manager (npm, pnpm, yarn, bun).
func (s *Service) GetTemplateForFiles(ctx context.Context, a Actor, stack string, files []string) (Template, error) {
	var t templates.Template
	var ok bool
	if len(files) > 0 {
		t, ok = templates.ForFiles(stack, files)
	} else {
		t, ok = templates.Get(stack)
	}
	if !ok {
		return Template{}, apperr.NotFound("template_unknown",
			"There is no template called "+quote(strings.TrimSpace(stack))+".",
			"Choose one of: "+strings.Join(templates.Stacks, ", ")+". Use static for plain HTML files, node for a Node.js server, node-spa for a Vue/React/Vite site, node-static for Astro/Eleventy, nextjs, django, laravel, and generic for anything else.").
			WithNext("get_template")
	}
	return templateFrom(t), nil
}

// DeployRules returns the server instructions plus the runtime contract (markdown).
func (s *Service) DeployRules(ctx context.Context, a Actor) (string, error) {
	return "# bitploy deploy rules\n\n" +
		rules.Instructions(s.Cfg.AppURL("<slug>"), s.Cfg.AppsDomain) +
		"\n" + rules.RuntimeContract, nil
}

const maxDockerfileBytes = 256 << 10

// ValidateDockerfile runs the static checks without building. packageJSON is
// the text of the code's package.json, or "" when the caller did not send it;
// with it the checks can warn about native modules on an Alpine image.
func (s *Service) ValidateDockerfile(ctx context.Context, a Actor, dockerfile string, fileList []string, appPort int, packageJSON string) (ValidateResult, error) {
	return s.validate(dockerfile, fileList, appPort, "web", packageJSON)
}

// ValidateDockerfileFor validates against an existing app: its port setting,
// type (workers skip port checks) and Dockerfile path are used.
func (s *Service) ValidateDockerfileFor(ctx context.Context, a Actor, ref, dockerfile string, fileList []string, packageJSON string) (ValidateResult, error) {
	row, err := s.LoadApp(ctx, a, ref, false)
	if err != nil {
		return ValidateResult{}, err
	}
	port := 0
	if row.Port.Valid {
		port = int(row.Port.Int64)
	}
	return s.validate(dockerfile, fileList, port, row.Type, packageJSON)
}

func (s *Service) validate(dockerfile string, fileList []string, appPort int, appType, packageJSON string) (ValidateResult, error) {
	if len(dockerfile) > maxDockerfileBytes {
		return ValidateResult{}, apperr.BadRequest("dockerfile_too_large",
			"The Dockerfile is larger than 256 KB, which is far more than a Dockerfile needs.",
			"Send only the Dockerfile text, not other files.")
	}
	if len(packageJSON) > maxDockerfileBytes {
		return ValidateResult{}, apperr.BadRequest("package_json_too_large",
			"The package.json is larger than 256 KB.",
			"Send only the text of package.json, or leave package_json out.")
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
	if strings.TrimSpace(packageJSON) != "" {
		in.PackageJSON = []byte(packageJSON)
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
			"Deploy it: deploy_files with ALL files of the code (it replaces the whole source; up to 2 MB and 200 files), or create_upload for a zip.",
			"Read any warnings first; they do not block the deploy.",
		}
	} else {
		out.NextSteps = []string{"Fix every finding with severity error, then call validate_dockerfile again."}
		if e := deploy.FirstError(res, fileList); e != nil && e.Code == deploy.CodeDockerfileMissing {
			out.NextSteps = []string{"Call get_template with stack \"" + templateName(e) + "\", save its Dockerfile and .dockerignore in the code folder, adapt them, then call validate_dockerfile again."}
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

// SetGitSource links a repository: it saves repo, branch and subdir and
// checks that bitploy can read it with ls-remote. For github.com repositories
// the owner's GitHub App installation is used when it covers the repo,
// otherwise the check is anonymous (public repositories). Pushes to the
// branch deploy by themselves unless AutoDeploy is false (GitHub only).
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
		AccessHint:  s.gitAccessHint(ctx, a),
	}
	isGitHub := deploy.IsGitHubURL(repo)
	if isGitHub {
		gs.Token = s.gitCloneToken(ctx, row.UserID, repo)
	}
	sha, branch, err := s.lsRemote(ctx, gs)
	if err != nil {
		return App{}, err
	}
	auto := isGitHub && (src.AutoDeploy == nil || *src.AutoDeploy)
	err = s.Q.SetAppGitSource(ctx, db.SetAppGitSourceParams{
		GitRepoUrl:    sql.NullString{String: repo, Valid: true},
		GitBranch:     sql.NullString{String: branch, Valid: true},
		GitSubdir:     nullStr(subdir),
		GitAutoDeploy: b2i(auto),
		UpdatedAt:     s.now(),
		ID:            row.ID,
	})
	if err != nil {
		return App{}, apperr.Internal(err)
	}
	if row.GitRepoUrl.String != repo || row.GitBranch.String != branch {
		_ = s.Q.SetAppGitLastPush(ctx, db.SetAppGitLastPushParams{ID: row.ID}) // a new repository or branch has no push yet
	}
	s.Audit(ctx, a, "app.git_source_set", "app", row.ID, map[string]any{
		"repo_url": repo, "branch": branch, "subdir": subdir, "commit": sha, "auto_deploy": auto, "app_access": gs.Token != "",
	})
	return s.GetApp(ctx, a, row.Slug)
}

// ClearGitSource unlinks the repository, so the app takes uploads again. The
// running version stays as it is. Unlinking an app without a repository does nothing.
func (s *Service) ClearGitSource(ctx context.Context, a Actor, ref string) (App, error) {
	row, err := s.LoadApp(ctx, a, ref, true)
	if err != nil {
		return App{}, err
	}
	if row.GitRepoUrl.Valid && row.GitRepoUrl.String != "" {
		if err := s.Q.ClearAppGitSource(ctx, db.ClearAppGitSourceParams{UpdatedAt: s.now(), ID: row.ID}); err != nil {
			return App{}, apperr.Internal(err)
		}
		s.Audit(ctx, a, "app.git_source_clear", "app", row.ID, map[string]any{"repo_url": row.GitRepoUrl.String, "branch": row.GitBranch.String})
	}
	return s.GetApp(ctx, a, row.Slug)
}

// SetGitAutoDeploy turns "deploy on every push" on or off for a linked repository.
func (s *Service) SetGitAutoDeploy(ctx context.Context, a Actor, ref string, on bool) (App, error) {
	row, err := s.LoadApp(ctx, a, ref, true)
	if err != nil {
		return App{}, err
	}
	if !row.GitRepoUrl.Valid || row.GitRepoUrl.String == "" {
		return App{}, apperr.Conflict("git_source_missing", "This app has no repository linked.",
			"Link one first with set_git_source.").WithNext("set_git_source")
	}
	if on && !deploy.IsGitHubURL(row.GitRepoUrl.String) {
		return App{}, apperr.Unprocessable("git_auto_deploy_unsupported", "Only GitHub repositories can deploy on every push.",
			"Deploy this one by hand after you push, or move the repository to GitHub.")
	}
	if err := s.Q.SetAppGitAutoDeploy(ctx, db.SetAppGitAutoDeployParams{GitAutoDeploy: b2i(on), UpdatedAt: s.now(), ID: row.ID}); err != nil {
		return App{}, apperr.Internal(err)
	}
	s.Audit(ctx, a, "app.git_auto_deploy", "app", row.ID, map[string]any{"auto_deploy": on})
	return s.GetApp(ctx, a, row.Slug)
}

// gitLinkedErr refuses uploads and inline files for an app that deploys from
// a repository: they would be overwritten by the next push, and two sources
// for one app is a trap for beginners.
func gitLinkedErr(row db.App) error {
	if !row.GitRepoUrl.Valid || row.GitRepoUrl.String == "" {
		return nil
	}
	where := row.GitRepoUrl.String
	if key, ok := github.RepoKey(where); ok {
		where = key
	}
	hint := "Push your change to " + row.GitBranch.String + ": bitploy builds it by itself, then follow it with get_deployment (app=" + row.Slug + "). "
	if row.GitAutoDeploy == 0 {
		hint = "Push your change to " + row.GitBranch.String + ", then call deploy with app=" + row.Slug + " to build it (auto-deploy is off). "
	}
	hint += "To go back to uploading files, unlink the repository first: set_git_source with remove=true, or Disconnect repo in the app's Settings."
	return apperr.Conflict("git_source_linked",
		"This app deploys from "+where+" (branch "+row.GitBranch.String+"), so files cannot be sent to it.",
		hint).WithNext("get_deployment")
}

// UseLsRemote replaces the repository check that SetGitSource runs
// (deploy.LsRemoteInfo). Tests use it so that no network is needed.
func (s *Service) UseLsRemote(fn func(ctx context.Context, src deploy.GitSource) (sha, branch string, err error)) {
	s.lsRemote = fn
}
