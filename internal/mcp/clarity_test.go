package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/AVMG20/bitploy/internal/app"
	"github.com/AVMG20/bitploy/internal/apperr"
)

// schemaNode is the part of a JSON schema these tests look at.
type schemaNode struct {
	Type        any                    `json:"type"`
	Description string                 `json:"description"`
	Properties  map[string]*schemaNode `json:"properties"`
	Items       *schemaNode            `json:"items"`
	Enum        []any                  `json:"enum"`
}

func toolSchema(t *testing.T, tl *sdk.Tool) *schemaNode {
	t.Helper()
	b, err := json.Marshal(tl.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var n schemaNode
	if err := json.Unmarshal(b, &n); err != nil {
		t.Fatal(err)
	}
	return &n
}

func checkDescribed(t *testing.T, path string, n *schemaNode) {
	t.Helper()
	for name, p := range n.Properties {
		if strings.TrimSpace(p.Description) == "" {
			t.Errorf("%s.%s has no description", path, name)
		}
		checkDescribed(t, path+"."+name, p)
	}
	if n.Items != nil {
		checkDescribed(t, path+"[]", n.Items)
	}
}

func listTools(t *testing.T, cs *sdk.ClientSession) []*sdk.Tool {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return res.Tools
}

func TestEveryToolAndArgumentIsDescribed(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.deployToken)
	for _, tl := range listTools(t, cs) {
		if len(strings.TrimSpace(tl.Description)) < 40 {
			t.Errorf("tool %s needs a real description (when to use it, what it returns): %q", tl.Name, tl.Description)
		}
		if tl.Title == "" {
			t.Errorf("tool %s has no title", tl.Name)
		}
		if tl.Annotations == nil {
			t.Errorf("tool %s has no annotations", tl.Name)
		}
		checkDescribed(t, tl.Name, toolSchema(t, tl))
	}
}

func TestEnumsAreInTheSchema(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.deployToken)
	want := map[string]map[string]int{
		"get_logs":    {"kind": 2},
		"get_metrics": {"window": 3},
		"create_app":  {"type": 2},
	}
	for _, tl := range listTools(t, cs) {
		for prop, n := range want[tl.Name] {
			p := toolSchema(t, tl).Properties[prop]
			if p == nil || len(p.Enum) != n {
				t.Errorf("%s.%s should list %d allowed values: %+v", tl.Name, prop, n, p)
			}
		}
	}
}

// assertErrorShape checks the contract: every error has code, message, hint and next_tool.
func assertErrorShape(t *testing.T, label string, res *sdk.CallToolResult) map[string]any {
	t.Helper()
	if !res.IsError {
		t.Errorf("%s: expected isError, got %s", label, text(res))
		return nil
	}
	m, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Errorf("%s: error has no structuredContent: %s", label, text(res))
		return nil
	}
	for _, k := range []string{"code", "message", "hint", "next_tool"} {
		if v, _ := m[k].(string); strings.TrimSpace(v) == "" {
			t.Errorf("%s: error lacks %s: %v", label, k, m)
		}
	}
	if !strings.Contains(text(res), "Next tool:") {
		t.Errorf("%s: text lacks the next tool: %s", label, text(res))
	}
	return m
}

func TestEveryErrorHasCodeMessageHintAndNextTool(t *testing.T) {
	e := newEnv(t)
	dep := e.connect(t, e.deployToken)
	read := e.connect(t, e.readToken)

	cases := []struct {
		label string
		cs    *sdk.ClientSession
		tool  string
		args  map[string]any
		code  string
		next  string
	}{
		{"missing required argument", dep, "get_app", map[string]any{}, "invalid_arguments", "get_app"},
		{"wrong argument type", dep, "create_app", map[string]any{"name": 5}, "invalid_arguments", "create_app"},
		{"unknown argument", dep, "get_app", map[string]any{"app": "x", "slug": "y"}, "invalid_arguments", "get_app"},
		{"bad enum", dep, "get_logs", map[string]any{"app": "x", "kind": "errors"}, "invalid_arguments", "get_logs"},
		{"bad window", dep, "get_metrics", map[string]any{"app": "x", "window": "week"}, "invalid_arguments", "get_metrics"},
		{"unknown app", dep, "get_app", map[string]any{"app": "nope-app"}, "app_not_found", "list_apps"},
		{"unknown template", read, "get_template", map[string]any{"stack": "ruby"}, "template_unknown", "get_template"},
		{"read-only token", read, "create_app", map[string]any{"name": "x"}, "token_read_only", "whoami"},
		{"read-only token on env", read, "set_env", map[string]any{"app": "x", "set": map[string]any{"A": "b"}}, "token_read_only", "whoami"},
		{"delete without confirm", dep, "delete_app", map[string]any{"app": "x", "confirm_slug": ""}, "confirm_slug_required", "delete_app"},
		{"deployment needs id or app", dep, "get_deployment", map[string]any{}, "invalid_arguments", "list_apps"},
		{"unknown deployment", dep, "get_deployment", map[string]any{"id": "dep_nope"}, "deployment_not_found", "list_deployments"},
		{"bad slug", dep, "create_app", map[string]any{"name": "x", "slug": "Bad_Slug"}, "slug_invalid", ""},
		{"bad git url", dep, "set_git_source", map[string]any{"app": "x", "repo_url": "git@github.com:o/r.git"}, "", ""},
	}
	for _, c := range cases {
		res := call(t, c.cs, c.tool, c.args)
		m := assertErrorShape(t, c.label, res)
		if m == nil {
			continue
		}
		if c.code != "" && m["code"] != c.code {
			t.Errorf("%s: code = %v, want %s", c.label, m["code"], c.code)
		}
		if c.next != "" && m["next_tool"] != c.next {
			t.Errorf("%s: next_tool = %v, want %s", c.label, m["next_tool"], c.next)
		}
	}
}

// Errors from the service layer that name no next tool still get one.
func TestErrorsWithoutNextToolGetADefault(t *testing.T) {
	e := newEnv(t)
	s := &srv{svc: e.svc}
	for _, c := range []struct {
		err  error
		tool string
		want string
	}{
		{apperr.TooMany("deploy_rate_limited", "Too many deployments.", "Wait 10 minutes."), "deploy_files", "get_app"},
		{apperr.Conflict("app_busy", "A deployment is running.", "Wait."), "restart_app", "list_deployments"},
		{apperr.Forbidden("token_read_only", "Read only.", "Use a deploy token."), "create_app", "whoami"},
		{apperr.NotFound("domain_not_found", "No such domain.", "Check the name."), "check_domain", "get_app"},
		{apperr.BadRequest("env_name_invalid", "Bad name.", "Use letters."), "set_env", "set_env"},
		{apperr.Unprocessable("build_failed", "It failed.", "Read the log."), "deploy", "deploy"},
		{apperr.Unavailable("server_full", "No room.", "Tell the admin."), "create_app", "create_app"},
		{apperr.Internal(fmt.Errorf("boom")), "get_app", "get_app"},
		{fmt.Errorf("plain error"), "get_app", "get_app"},
	} {
		res := s.errorResult(c.tool, c.err)
		assertErrorShape(t, c.err.Error(), res)
		if m := res.StructuredContent.(map[string]any); m["next_tool"] != c.want {
			t.Errorf("%v on %s: next_tool = %v, want %s", c.err, c.tool, m["next_tool"], c.want)
		}
	}
}

func nextSteps(t *testing.T, label string, res *sdk.CallToolResult) []string {
	t.Helper()
	if res.IsError {
		t.Fatalf("%s failed: %s", label, text(res))
	}
	raw, _ := structured(t, res)["next_steps"].([]any)
	var out []string
	for _, r := range raw {
		out = append(out, r.(string))
	}
	if len(out) == 0 {
		t.Errorf("%s: success must carry next_steps", label)
	}
	if !strings.Contains(text(res), "next_steps") {
		t.Errorf("%s: the text content must repeat the data for clients that only show text", label)
	}
	return out
}

func TestKeyFlowsReturnNextSteps(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.deployToken)

	// whoami and the rules tell the agent the base domain, limits and how changes apply.
	res := call(t, cs, "whoami", nil)
	steps := nextSteps(t, "whoami", res)
	if !strings.Contains(strings.Join(steps, " "), ".bitploy") {
		t.Errorf("whoami next steps should explain which app a project belongs to: %v", steps)
	}
	m := structured(t, res)
	lim, _ := m["limits"].(map[string]any)
	if m["apps_domain"] != "bitploy.test" || m["app_url_pattern"] != "https://<slug>.bitploy.test" ||
		m["token_can_deploy"] != true || lim["deploy_files_max_files"] != float64(200) || lim["deploy_files_max_bytes"] != float64(2<<20) {
		t.Errorf("whoami data = %v", m)
	}
	if !strings.Contains(text(res), "restart_app") || !strings.Contains(text(res), "bitploy.test") {
		t.Errorf("whoami summary: %s", text(res))
	}

	res = call(t, cs, "get_deploy_rules", nil)
	nextSteps(t, "get_deploy_rules", res)
	rules := structured(t, res)["rules"].(string)
	for _, s := range []string{
		"https://<slug>.bitploy.test", "This server", "2 MB", "95 MB", "restart_app", ".bitploy", "Never reuse an app just because its name looks similar",
		"full snapshot", "out of memory", "/data", "DNS", "deploy_rate_limited", "token_read_only", "github_access_missing", "git_source_linked",
	} {
		if !strings.Contains(rules, s) {
			t.Errorf("rules lack %q", s)
		}
	}

	nextSteps(t, "get_template", call(t, cs, "get_template", map[string]any{"stack": "node"}))
	nextSteps(t, "validate_dockerfile", call(t, cs, "validate_dockerfile", map[string]any{"dockerfile": "FROM node:24-alpine\nUSER node\nEXPOSE 3000\n"}))

	// No apps yet: list_apps says so and names create_app.
	res = call(t, cs, "list_apps", nil)
	if steps := nextSteps(t, "list_apps (empty)", res); !strings.Contains(steps[0], "create_app") {
		t.Errorf("empty list_apps next = %v", steps)
	}

	res = call(t, cs, "create_app", map[string]any{"name": "Bakery", "slug": "bakery-shop"})
	steps = nextSteps(t, "create_app", res)
	all := strings.Join(steps, " ")
	for _, s := range []string{".bitploy", "bakery-shop", "deploy_files", "ALL files", "https://bakery-shop.bitploy.test"} {
		if !strings.Contains(all, s) {
			t.Errorf("create_app next steps lack %q: %v", s, steps)
		}
	}

	res = call(t, cs, "list_apps", nil)
	steps = nextSteps(t, "list_apps", res)
	if !strings.Contains(strings.Join(steps, " "), "Never overwrite") {
		t.Errorf("list_apps must warn against reusing similar names: %v", steps)
	}
	m = structured(t, res)
	apps := m["apps"].([]any)
	first := apps[0].(map[string]any)
	for _, k := range []string{"slug", "name", "url", "status", "healthy", "latest_deployment", "memory_limit_mb"} {
		if _, ok := first[k]; !ok {
			t.Errorf("list_apps entry lacks %s: %v", k, first)
		}
	}
	if first["slug"] != "bakery-shop" || first["name"] != "Bakery" || first["url"] != "https://bakery-shop.bitploy.test" || first["status"] != "new" {
		t.Errorf("entry = %v", first)
	}
	if !strings.Contains(text(res), `- bakery-shop "Bakery" [new] https://bakery-shop.bitploy.test`) {
		t.Errorf("summary lines should be easy to match by name and url: %s", text(res))
	}

	nextSteps(t, "get_app", call(t, cs, "get_app", map[string]any{"app": "bakery-shop"}))
	nextSteps(t, "list_deployments", call(t, cs, "list_deployments", map[string]any{"app": "bakery-shop"}))
	nextSteps(t, "get_metrics", call(t, cs, "get_metrics", map[string]any{"app": "bakery-shop", "window": "1h"}))

	res = call(t, cs, "set_env", map[string]any{"app": "bakery-shop", "set": map[string]any{"API_KEY": "secret"}})
	steps = nextSteps(t, "set_env", res)
	if !strings.Contains(strings.Join(steps, " "), "first deploy") {
		t.Errorf("set_env on a new app should say the first deploy uses it: %v", steps)
	}
	if strings.Contains(text(res), "secret") {
		t.Error("env values must never be returned")
	}

	res = call(t, cs, "update_app", map[string]any{"app": "bakery-shop", "memory_mb": 256, "port": 3000})
	steps = nextSteps(t, "update_app", res)
	if !strings.Contains(strings.Join(steps, " "), "first deploy") {
		t.Errorf("update_app next = %v", steps)
	}

	// Restarting an app that never ran fails with a next_tool.
	res = call(t, cs, "restart_app", map[string]any{"app": "bakery-shop"})
	assertErrorShape(t, "restart before deploy", res)

	// A Dockerfile-less folder: the error carries the template and next_tool.
	res = call(t, cs, "deploy_files", map[string]any{"app": "bakery-shop", "files": []map[string]any{{"path": "index.html", "content": "<h1>hi</h1>"}}})
	if m := assertErrorShape(t, "deploy without Dockerfile", res); m != nil {
		if m["code"] != "dockerfile_missing" || m["next_tool"] != "get_template" || !strings.Contains(m["hint"].(string), "deploy_files again with ALL project files") {
			t.Errorf("dockerfile_missing = %v", m)
		}
	}

	// Renaming is exposed and tells the agent the URL changed.
	res = call(t, cs, "update_app", map[string]any{"app": "bakery-shop", "slug": "bakery-main"})
	if res.IsError {
		t.Logf("rename not possible in this environment: %s", text(res))
	} else {
		steps = nextSteps(t, "update_app slug", res)
		if !strings.Contains(strings.Join(steps, " "), "https://bakery-main.bitploy.test") {
			t.Errorf("rename next = %v", steps)
		}
	}
}

func TestDeployWithoutSourceExplainsWhatToSend(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.deployToken)
	call(t, cs, "create_app", map[string]any{"name": "Empty", "slug": "empty-app"})
	res := call(t, cs, "deploy", map[string]any{"app": "empty-app"})
	m := assertErrorShape(t, "deploy of an empty app", res)
	if m != nil && (m["code"] != "no_source" || m["next_tool"] != "deploy_files") {
		t.Errorf("error = %v", m)
	}
	// get_deployment by app when nothing was deployed.
	res = call(t, cs, "get_deployment", map[string]any{"app": "empty-app"})
	assertErrorShape(t, "get_deployment without deployments", res)
	// rollback without history.
	res = call(t, cs, "rollback", map[string]any{"app": "empty-app"})
	if m := assertErrorShape(t, "rollback without history", res); m != nil && m["next_tool"] != "list_deployments" {
		t.Errorf("rollback error = %v", m)
	}
}

func TestDeploymentResultGuidesEachState(t *testing.T) {
	joined := func(d app.Deployment) string {
		return strings.Join(deploymentResult("Deployment", d).Next, "\n")
	}
	base := app.Deployment{ID: "dep_1", AppSlug: "shop", Seq: 3, SourceKind: "files"}

	d := base
	d.State = "building"
	if s := joined(d); !strings.Contains(s, "get_deployment with id=dep_1 and wait_seconds=30") {
		t.Errorf("in progress: %s", s)
	}

	d = base
	d.State, d.URL = "running", "https://shop.bitploy.test"
	s := joined(d)
	for _, want := range []string{"https://shop.bitploy.test", "deploy_files", "app=shop", "ALL project files", ".bitploy", `"app":"shop"`} {
		if !strings.Contains(s, want) {
			t.Errorf("running lacks %q:\n%s", want, s)
		}
	}
	d.SourceKind = "git"
	if s := joined(d); !strings.Contains(s, "commit and push") || strings.Contains(s, "ALL project files") {
		t.Errorf("git update path: %s", s)
	}
	d.SourceKind = "upload"
	if s := joined(d); !strings.Contains(s, "create_upload") {
		t.Errorf("upload update path: %s", s)
	}
	d.SourceKind, d.URL = "files", ""
	if s := joined(d); !strings.Contains(s, "worker is running") {
		t.Errorf("worker: %s", s)
	}

	failed := func(code, msg, hint string) string {
		d := base
		d.State = "failed"
		d.Error = &app.DeployError{Code: code, Message: msg, Hint: hint}
		return joined(d)
	}
	for _, c := range []struct{ code, msg, hint, want string }{
		{"unhealthy", "Your app ran out of memory and was killed.", "Give it more memory.", "update_app with a higher memory_mb"},
		{"unhealthy", "Your app stopped right after starting (exited with code 1).", "Read log_tail.", "set_env"},
		{"unhealthy", "Your app did not answer GET / with a status below 500 within 60 seconds.", "Make sure.", "health_path"},
		{"out_of_memory", "Your app ran out of memory and was killed.", "Raise memory_mb.", "update_app with a higher memory_mb"},
		{"app_crashed", "Your app stopped right after starting (exited with code 1).", "Read log_tail.", "set_env"},
		{"unhealthy", "Your app did not answer.", "Your app listens on localhost; make it listen on 0.0.0.0:$PORT", "listens on 0.0.0.0 and the PORT variable"},
		{"build_failed", "The build failed.", "Read log_tail.", "first error line"},
		{"dockerfile_missing", "There is no Dockerfile.", "Use the template.", "get_template"},
		{"port_unknown", "No port.", "Add EXPOSE.", "update_app with port"},
		{"source_repo_not_found", "Not found.", "Check it.", "set_git_source"},
	} {
		got := failed(c.code, c.msg, c.hint)
		if !strings.Contains(got, c.want) {
			t.Errorf("%s/%s: want %q in:\n%s", c.code, c.msg, c.want, got)
		}
		if !strings.Contains(got, "After 3 failures") {
			t.Errorf("%s: failure must mention the 3-failure rule", c.code)
		}
	}
}

func TestRulesAndPromptCoverUpdatesAndAppChoice(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.deployToken)
	ctx := context.Background()
	p, err := cs.GetPrompt(ctx, &sdk.GetPromptParams{Name: "deploy_project"})
	if err != nil {
		t.Fatal(err)
	}
	pt := p.Messages[0].Content.(*sdk.TextContent).Text
	for _, s := range []string{".bitploy", "UPDATE path", "ALL files", "full snapshot", "never reuse an app only because its name looks similar", "restart_app", "list_apps"} {
		if !strings.Contains(pt, s) {
			t.Errorf("prompt lacks %q", s)
		}
	}
	p, err = cs.GetPrompt(ctx, &sdk.GetPromptParams{Name: "deploy_project", Arguments: map[string]string{"app": "bakery-shop"}})
	if err != nil {
		t.Fatal(err)
	}
	if pt := p.Messages[0].Content.(*sdk.TextContent).Text; !strings.Contains(pt, `"bakery-shop"`) {
		t.Errorf("prompt should name the app: %s", pt)
	}

	// Descriptions of the tools that pick an app say how to choose one.
	byName := map[string]string{}
	for _, tl := range listTools(t, cs) {
		byName[tl.Name] = tl.Description
	}
	for _, n := range []string{"create_app", "deploy_files"} {
		if !strings.Contains(byName[n], ".bitploy") {
			t.Errorf("%s description should mention .bitploy: %s", n, byName[n])
		}
	}
	if !strings.Contains(byName["create_app"], "similar") || !strings.Contains(byName["list_apps"], "already online") {
		t.Errorf("create_app/list_apps must say not to reuse apps by similar name:\n%s\n%s", byName["create_app"], byName["list_apps"])
	}
	if !strings.Contains(byName["deploy_files"], "FULL SNAPSHOT") {
		t.Errorf("deploy_files must say it replaces the whole source: %s", byName["deploy_files"])
	}
	if !strings.Contains(byName["set_env"], "restart_app") || !strings.Contains(byName["update_app"], "restart_app") {
		t.Error("set_env and update_app must say when changes apply")
	}
}

func TestListAppsHealthSummary(t *testing.T) {
	apps := []app.App{
		{Slug: "good", Name: "Good", Type: "web", URL: "https://good.x", Status: "running"},
		{Slug: "bad", Name: "Bad", Type: "web", URL: "https://bad.x", Status: "failed", StatusMessage: "Build broke."},
		{Slug: "old", Name: "Old", Type: "web", URL: "https://old.x", Status: "running"},
	}
	briefs := []appBrief{
		newAppBrief(apps[0], &deploymentBrief{ID: "dep_1", State: "running"}),
		newAppBrief(apps[1], &deploymentBrief{ID: "dep_2", State: "failed", ErrorCode: "build_failed"}),
		newAppBrief(apps[2], &deploymentBrief{ID: "dep_3", State: "failed", ErrorCode: "unhealthy"}),
	}
	r := listAppsResult(briefs)
	if !strings.HasPrefix(r.Summary, "3 app(s): 1 healthy, 2 need attention,") {
		t.Errorf("summary = %s", r.Summary)
	}
	if !strings.Contains(r.Summary, "PROBLEM") || !briefs[0].Healthy || briefs[1].Healthy || briefs[2].Healthy {
		t.Errorf("health flags wrong: %+v", briefs)
	}
	if !strings.Contains(briefs[2].Problem, "previous version is still live") {
		t.Errorf("a running app with a failed newest deploy should say so: %q", briefs[2].Problem)
	}
	if !strings.Contains(strings.Join(r.Next, " "), "get_deployment with app=bad") {
		t.Errorf("next = %v", r.Next)
	}
}

func TestDomainMessageIsPlainLanguage(t *testing.T) {
	d := app.Domain{Hostname: "www.example.com", Status: "pending_dns", Records: []app.DNSRecord{
		{Type: "CNAME", Name: "www.example.com", Value: "edge.bitploy.nl"},
		{Type: "TXT", Name: "_bitploy-challenge.www.example.com", Value: "bitploy-abc"},
	}, Notes: []string{"Set the CNAME to DNS only."}}
	msg := dnsMessage(d)
	for _, s := range []string{"www.example.com", "CNAME record, name www.example.com, value edge.bitploy.nl", "TXT record", "DNS settings", "5 to 30 minutes", "DNS only"} {
		if !strings.Contains(msg, s) {
			t.Errorf("message lacks %q:\n%s", s, msg)
		}
	}
}
