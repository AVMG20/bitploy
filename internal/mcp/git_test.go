package mcp

import (
	"context"
	"regexp"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/AVMG20/bitploy/internal/deploy"
)

func (e *env) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := e.svc.Store.DB.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// Uploading stays a first-class path. The rules prefer git when the project has
// a GitHub remote, and say in so many words that nobody needs a repository to deploy.
func TestRulesPreferGitAndKeepUploadsFirstClass(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.deployToken)
	ctx := context.Background()

	init := cs.InitializeResult().Instructions
	for _, s := range []string{
		"If it has a GitHub remote, prefer git",
		"set_git_source with the remote's HTTPS URL",
		"push",
		"get_deployment",
		"? Upload:",
		"deploy_files",
		"create_upload",
		"Never ask the user to\n   create a repository or install git only to deploy",
		"git_source_linked",
	} {
		if !strings.Contains(init, s) {
			t.Errorf("instructions lack %q:\n%s", s, init)
		}
	}
	if strings.Contains(strings.ToLower(init), "must use git") || strings.Contains(strings.ToLower(init), "always use git") {
		t.Errorf("git must be a preference, not a requirement:\n%s", init)
	}

	res := call(t, cs, "get_deploy_rules", nil)
	rules := structured(t, res)["rules"].(string)
	for _, s := range []string{
		"Code with a GitHub remote is best linked with set_git_source.", "Code without a GitHub remote is uploaded",
		"upload the files instead",
		"never ask the user to create a repository or install git only to deploy",
		"create_upload", "deploy_files", "git_source_linked", "set_git_source with remove=true", "github_access_missing",
		"Connect GitHub", "Settings > GitHub", "push_ready",
	} {
		if !strings.Contains(rules, s) {
			t.Errorf("rules lack %q", s)
		}
	}

	// The prompt walks both paths.
	p, err := cs.GetPrompt(ctx, &sdk.GetPromptParams{Name: "deploy_project"})
	if err != nil {
		t.Fatal(err)
	}
	pt := p.Messages[0].Content.(*sdk.TextContent).Text
	for _, s := range []string{"Prefer git", "No GitHub remote? Upload, which is fully supported", "Never ask the user to create a repository or install git only to deploy", "deploy_files", "create_upload"} {
		if !strings.Contains(pt, s) {
			t.Errorf("prompt lacks %q", s)
		}
	}

	// Tool descriptions: uploads are not discouraged, a linked app is the exception.
	desc := map[string]string{}
	for _, tl := range listTools(t, cs) {
		desc[tl.Name] = tl.Description
	}
	for _, n := range []string{"deploy_files", "create_upload"} {
		if !strings.Contains(desc[n], "git_source_linked") || !strings.Contains(desc[n], "linked to a git repository") {
			t.Errorf("%s must say that a linked app refuses it: %s", n, desc[n])
		}
	}
	if d := desc["deploy_files"]; !strings.Contains(d, "first deploys and for later updates") {
		t.Errorf("deploy_files stays the normal way to update an uploaded app: %s", d)
	}
	g := desc["set_git_source"]
	for _, s := range []string{"Prefer this", "has a GitHub remote", "commit and push first", "upload the files instead", "auto_deploy", "committing and pushing", "get_deployment", "Settings > GitHub", "Connect GitHub",
		"remove=true", "Not for code without a GitHub remote", "never ask the user to create a repository"} {
		if !strings.Contains(g, s) {
			t.Errorf("set_git_source description lacks %q: %s", s, g)
		}
	}
	if !strings.Contains(desc["deploy"], "only to build the first time, when auto-deploy is off, or to rebuild") {
		t.Errorf("deploy must say when it is needed for a linked repository: %s", desc["deploy"])
	}
	if !strings.Contains(desc["get_deployment"], "push started") {
		t.Errorf("get_deployment must say how to follow a push: %s", desc["get_deployment"])
	}

	// Nothing tells anyone to paste a token any more.
	all := init + rules + pt
	for _, d := range desc {
		all += d
	}
	for _, bad := range []string{"personal access token", "GitHub token", "github_token_missing", "github_repo_forbidden", "fine-grained"} {
		if strings.Contains(all, bad) {
			t.Errorf("the rules still mention %q", bad)
		}
	}
	// The only sentence that mentions creating a repository or installing git is the "never".
	flat := strings.Join(strings.Fields(all), " ")
	re := regexp.MustCompile(`(?i)(create a (git )?repo|install git)`)
	for _, m := range re.FindAllStringIndex(flat, -1) {
		before := strings.ToLower(flat[max(0, m[1]-110):m[1]])
		if !strings.Contains(before, "never ask the user to") {
			t.Errorf("a rule tells people to create a repository or install git: ...%s", flat[max(0, m[0]-80):min(len(flat), m[1]+40)])
		}
	}
}

func TestSetGitSourceOverMCP(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.deployToken)
	var seen deploy.GitSource
	e.svc.UseLsRemote(func(_ context.Context, src deploy.GitSource) (string, string, error) {
		seen = src
		b := src.Branch
		if b == "" {
			b = "main"
		}
		return strings.Repeat("a", 40), b, nil
	})
	call(t, cs, "create_app", map[string]any{"name": "Site", "slug": "site-x"})

	res := call(t, cs, "set_git_source", map[string]any{"app": "site-x", "repo_url": "https://github.com/ada/site"})
	if res.IsError {
		t.Fatalf("set_git_source: %s", text(res))
	}
	if seen.Token != "" {
		t.Fatalf("no GitHub connection, so the check is anonymous: %q", seen.Token)
	}
	g := structured(t, res)["app"].(map[string]any)["git_source"].(map[string]any)
	if g["repo"] != "ada/site" || g["branch"] != "main" || g["auto_deploy"] != true || g["push_ready"] != false || g["push_status"] != "app_missing" || g["last_push_at"] != nil {
		t.Fatalf("git_source: %v", g)
	}
	steps := strings.Join(nextSteps(t, "set_git_source", res), " ")
	for _, s := range []string{"deploy with app=site-x once", "GitHub is not set up on this bitploy", "Admin page", "deploy with app=site-x after each push", ".bitploy"} {
		if !strings.Contains(steps, s) {
			t.Errorf("next steps lack %q: %s", s, steps)
		}
	}
	if !strings.Contains(text(res), "ada/site") || !strings.Contains(text(res), "GitHub is not set up yet") {
		t.Errorf("summary: %s", text(res))
	}

	// auto_deploy=false and a branch.
	res = call(t, cs, "set_git_source", map[string]any{"app": "site-x", "repo_url": "https://github.com/ada/site", "branch": "dev", "auto_deploy": false})
	g = structured(t, res)["app"].(map[string]any)["git_source"].(map[string]any)
	if g["auto_deploy"] != false || g["branch"] != "dev" || g["push_status"] != "off" {
		t.Fatalf("auto_deploy=false: %v", g)
	}
	if steps := strings.Join(nextSteps(t, "set_git_source off", res), " "); !strings.Contains(steps, "Auto-deploy is off") {
		t.Errorf("next steps with auto-deploy off: %s", steps)
	}

	// get_app tells the same, with next steps.
	res = call(t, cs, "get_app", map[string]any{"app": "site-x"})
	g = structured(t, res)["app"].(map[string]any)["git_source"].(map[string]any)
	for _, k := range []string{"repo", "branch", "subdir", "auto_deploy", "push_ready", "push_status", "push_message", "last_push_at", "repo_html_url"} {
		if _, ok := g[k]; !ok && k != "subdir" {
			t.Errorf("get_app git_source lacks %q: %v", k, g)
		}
	}
	if !strings.Contains(strings.Join(nextSteps(t, "get_app", res), " "), "push") {
		t.Errorf("get_app next steps: %v", nextSteps(t, "get_app", res))
	}

	// The schema offers both new arguments.
	for _, tl := range listTools(t, cs) {
		if tl.Name != "set_git_source" {
			continue
		}
		props := toolSchema(t, tl).Properties
		if props["auto_deploy"] == nil || props["remove"] == nil {
			t.Errorf("set_git_source schema: %+v", props)
		}
	}
}

func TestLinkedAppRefusesUploadsOverMCP(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.deployToken)
	call(t, cs, "create_app", map[string]any{"name": "Site", "slug": "site-y"})
	// An upload made before linking.
	up := structured(t, call(t, cs, "create_upload", map[string]any{"app": "site-y"}))
	e.exec(t, `UPDATE apps SET git_repo_url = 'https://github.com/ada/site', git_branch = 'main', git_auto_deploy = 1 WHERE slug = 'site-y'`)

	files := []any{map[string]any{"path": "Dockerfile", "content": "FROM nginx:1.27-alpine\nEXPOSE 80\nUSER nginx\n"}}
	for name, args := range map[string]map[string]any{
		"deploy_files":  {"app": "site-y", "files": files},
		"create_upload": {"app": "site-y"},
		"deploy":        {"app": "site-y", "upload_id": up["id"]},
	} {
		res := call(t, cs, name, args)
		m := assertErrorShape(t, name, res)
		if m["code"] != "git_source_linked" {
			t.Fatalf("%s: %v", name, m)
		}
		hint, _ := m["hint"].(string)
		for _, s := range []string{"Push your change to main", "get_deployment", "remove=true"} {
			if !strings.Contains(hint, s) {
				t.Errorf("%s hint lacks %q: %s", name, s, hint)
			}
		}
		if !strings.Contains(text(res), "remove=true") {
			t.Errorf("%s: the text shows the way out: %s", name, text(res))
		}
	}
	// Building the repository is fine, and get_deployment with only app= follows it.
	res := call(t, cs, "deploy", map[string]any{"app": "site-y"})
	if res.IsError {
		t.Fatalf("deploy from git: %s", text(res))
	}
	e.exec(t, `UPDATE deployments SET channel = 'github', commit_sha = 'abc1234def', commit_message = 'Fix the footer'`)
	res = call(t, cs, "get_deployment", map[string]any{"app": "site-y"})
	d := structured(t, res)["deployment"].(map[string]any)
	if d["channel"] != "github" || d["commit_sha"] != "abc1234def" || d["source_kind"] != "git" {
		t.Fatalf("latest deployment of a push: %v", d)
	}

	// Unlink with set_git_source remove=true: uploads work again.
	res = call(t, cs, "set_git_source", map[string]any{"app": "site-y", "remove": true})
	if res.IsError {
		t.Fatalf("remove: %s", text(res))
	}
	if structured(t, res)["app"].(map[string]any)["git_source"] != nil {
		t.Fatalf("still linked: %v", structured(t, res))
	}
	if steps := strings.Join(nextSteps(t, "remove", res), " "); !strings.Contains(steps, "deploy_files") || !strings.Contains(steps, "create_upload") {
		t.Errorf("after unlinking: %s", steps)
	}
	if res := call(t, cs, "deploy_files", map[string]any{"app": "site-y", "files": files}); res.IsError {
		t.Fatalf("deploy_files after unlinking: %s", text(res))
	}
	if res := call(t, cs, "create_upload", map[string]any{"app": "site-y"}); res.IsError {
		t.Fatalf("create_upload after unlinking: %s", text(res))
	}
	// A read-only token cannot unlink.
	ro := e.connect(t, e.readToken)
	if res := call(t, ro, "set_git_source", map[string]any{"app": "site-y", "remove": true}); !res.IsError {
		t.Fatal("a read-only token unlinked an app")
	}
}

// An app that never had a repository keeps the old upload flow, untouched.
func TestPlainAppKeepsUploadFlow(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.deployToken)
	call(t, cs, "create_app", map[string]any{"name": "Plain", "slug": "plain-app"})
	files := []any{map[string]any{"path": "Dockerfile", "content": "FROM nginx:1.27-alpine\nEXPOSE 80\nUSER nginx\n"}, map[string]any{"path": "index.html", "content": "<h1>hi</h1>"}}
	if res := call(t, cs, "deploy_files", map[string]any{"app": "plain-app", "files": files}); res.IsError {
		t.Fatalf("deploy_files: %s", text(res))
	}
	res := call(t, cs, "create_upload", map[string]any{"app": "plain-app"})
	if res.IsError || !strings.Contains(strings.Join(nextSteps(t, "create_upload", res), " "), "upload_id=") {
		t.Fatalf("create_upload: %s", text(res))
	}
	res = call(t, cs, "get_app", map[string]any{"app": "plain-app"})
	if structured(t, res)["app"].(map[string]any)["git_source"] != nil {
		t.Fatal("plain app shows a git source")
	}
	if steps := strings.Join(nextSteps(t, "get_app", res), " "); strings.Contains(steps, "GitHub") {
		t.Errorf("a plain app is not nagged about GitHub: %s", steps)
	}
}
