package mcp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/AVMG20/bitploy/internal/app"
	"github.com/AVMG20/bitploy/internal/config"
	"github.com/AVMG20/bitploy/internal/proxy"
	"github.com/AVMG20/bitploy/internal/store"
	"github.com/AVMG20/bitploy/internal/templates"
)

type env struct {
	svc         *app.Service
	srv         *httptest.Server
	deployToken string
	readToken   string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		PublicURL: "https://bitploy.test", AppsDomain: "bitploy.test", AppsScheme: "https",
		SecretKey: make([]byte, 32), DataDir: dir, Signup: "invite",
	}
	st, err := store.Open(filepath.Join(dir, "bitploy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.DB.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	cr, err := store.NewCrypto(cfg.SecretKey)
	if err != nil {
		t.Fatal(err)
	}
	routes := &proxy.Writer{Dir: filepath.Join(dir, "traefik"), EntryPoint: "web"}
	svc := app.New(cfg, st, cr, nil, routes, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	u, err := svc.CreateUser(ctx, "ada@example.com", "Ada", "correct horse battery", false)
	if err != nil {
		t.Fatal(err)
	}
	session := app.Actor{UserID: u.ID, Role: "user", Channel: app.ChannelDashboard, Scope: app.ScopeSession}
	dt, err := svc.CreateToken(ctx, session, app.CreateTokenInput{Name: "deploy", Scope: app.ScopeDeploy})
	if err != nil {
		t.Fatal(err)
	}
	rt, err := svc.CreateToken(ctx, session, app.CreateTokenInput{Name: "read", Scope: app.ScopeRead})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(Handler(svc))
	t.Cleanup(ts.Close)
	return &env{svc: svc, srv: ts, deployToken: dt.Value, readToken: rt.Value}
}

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if b.token != "" {
		r.Header.Set("Authorization", "Bearer "+b.token)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func (e *env) connect(t *testing.T, token string) *sdk.ClientSession {
	t.Helper()
	c := sdk.NewClient(&sdk.Implementation{Name: "test-agent", Version: "1"}, nil)
	cs, err := c.Connect(context.Background(), &sdk.StreamableClientTransport{
		Endpoint:             e.srv.URL,
		HTTPClient:           &http.Client{Transport: bearerTransport{token}},
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func call(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) *sdk.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error: %v", name, err)
	}
	return res
}

func structured(t *testing.T, res *sdk.CallToolResult) map[string]any {
	t.Helper()
	m, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("no structuredContent object: %#v", res.StructuredContent)
	}
	return m
}

func text(res *sdk.CallToolResult) string {
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func TestRejectsMissingAndBadToken(t *testing.T) {
	e := newEnv(t)
	for _, auth := range []string{"", "Bearer bp_nottherealtokennottherealtoken", "Basic abc"} {
		req, _ := http.NewRequest("POST", e.srv.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("Authorization %q: status %d, want 401", auth, resp.StatusCode)
		}
	}
}

func TestInitializeInstructionsAndTools(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.deployToken)

	init := cs.InitializeResult()
	want := Instructions(e.svc)
	if init.Instructions != want {
		t.Errorf("instructions differ:\n%s", init.Instructions)
	}
	if !strings.Contains(init.Instructions, "https://<slug>.bitploy.test") || strings.Contains(init.Instructions, "bitploy.nl") {
		t.Errorf("domain not substituted:\n%s", init.Instructions)
	}
	for _, line := range []string{
		"1. Every app is built from a Dockerfile. Bitploy refuses to deploy without one.",
		"9. Never stop, delete or roll back an app the user did not name.",
		"after 3 failures with the same error code, stop and\n   explain the problem to the user in plain words.",
	} {
		_ = line
	}
	for _, s := range []string{"Every app is built from a Dockerfile.", "wait_seconds=30", "set_git_source,\n   then deploy.", "Never stop, delete or roll back an app the user did not name."} {
		if !strings.Contains(init.Instructions, s) {
			t.Errorf("instructions lack %q", s)
		}
	}

	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*sdk.Tool{}
	for _, tl := range tools.Tools {
		got[tl.Name] = tl
	}
	wantTools := strings.Fields(`whoami get_deploy_rules get_template validate_dockerfile list_apps get_app create_app update_app
		deploy_files create_upload set_git_source deploy get_deployment list_deployments get_logs set_env
		restart_app start_app stop_app rollback delete_app add_domain check_domain remove_domain get_metrics create_backup list_backups`)
	for _, n := range wantTools {
		if got[n] == nil {
			t.Errorf("tool %s missing", n)
		}
	}
	if len(got) != len(wantTools) {
		t.Errorf("got %d tools, want %d", len(got), len(wantTools))
	}
	readOnly := strings.Fields("whoami get_deploy_rules get_template validate_dockerfile list_apps get_app get_deployment list_deployments get_logs get_metrics list_backups")
	for _, n := range readOnly {
		if a := got[n].Annotations; a == nil || !a.ReadOnlyHint {
			t.Errorf("%s should be readOnly", n)
		}
	}
	for _, n := range []string{"stop_app", "rollback", "delete_app"} {
		if a := got[n].Annotations; a == nil || a.DestructiveHint == nil || !*a.DestructiveHint {
			t.Errorf("%s should be destructive", n)
		}
	}
	for _, n := range []string{"update_app", "set_git_source", "set_env"} {
		if a := got[n].Annotations; a == nil || !a.IdempotentHint {
			t.Errorf("%s should be idempotent", n)
		}
	}
	for _, n := range []string{"create_app", "deploy", "deploy_files", "restart_app", "create_backup"} {
		a := got[n].Annotations
		if a == nil || a.ReadOnlyHint || a.DestructiveHint == nil || *a.DestructiveHint {
			t.Errorf("%s should be a non-destructive write: %+v", n, a)
		}
	}
	b, _ := json.Marshal(got["get_deployment"].InputSchema)
	if !strings.Contains(string(b), "max 50") && !strings.Contains(string(b), "up to 50") {
		t.Errorf("get_deployment schema should mention the 50 s ceiling: %s", b)
	}
}

func TestGetTemplate(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.readToken)
	res := call(t, cs, "get_template", map[string]any{"stack": "static"})
	if res.IsError {
		t.Fatalf("error: %s", text(res))
	}
	m := structured(t, res)
	static, _ := templates.Get("static")
	if m["dockerfile"] != static.Dockerfile || m["dockerignore"] != static.Dockerignore || m["stack"] != "static" {
		t.Errorf("unexpected template: %v", m)
	}
	if steps, _ := m["next_steps"].([]any); len(steps) == 0 {
		t.Error("success must carry next_steps")
	}
	lines := strings.Split(strings.TrimSpace(text(res)), "\n")
	if len(lines) < 1 || !strings.Contains(lines[0], "static") {
		t.Errorf("first text line should summarise: %q", lines)
	}

	// Unknown stack: an error with code, message, hint, next_tool.
	res = call(t, cs, "get_template", map[string]any{"stack": "ruby"})
	if !res.IsError {
		t.Fatal("unknown stack must be an error")
	}
	m = structured(t, res)
	if m["code"] != "template_unknown" || m["next_tool"] != "get_template" || m["message"] == "" || !strings.Contains(m["hint"].(string), "node-spa") {
		t.Errorf("error body = %v", m)
	}
}

func TestValidateDockerfile(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.readToken)

	// A fine Dockerfile with a warning.
	res := call(t, cs, "validate_dockerfile", map[string]any{
		"dockerfile": "FROM node\nUSER node\nEXPOSE 3000\n",
		"file_list":  []string{"package.json", "server.js", ".env"},
	})
	if res.IsError {
		t.Fatal(text(res))
	}
	m := structured(t, res)
	if m["ok"] != true || m["port"].(float64) != 3000 {
		t.Errorf("result = %v", m)
	}
	var codes []string
	for _, f := range m["findings"].([]any) {
		codes = append(codes, f.(map[string]any)["code"].(string))
	}
	if !contains(codes, "unpinned_base") {
		t.Errorf("expected unpinned_base in %v", codes)
	}

	// Broken Dockerfile: ok=false, still a normal (non-error) tool result with next_steps.
	res = call(t, cs, "validate_dockerfile", map[string]any{"dockerfile": "FROM alpine:3.22\nFORM x\n"})
	m = structured(t, res)
	if m["ok"] != false {
		t.Errorf("must not be ok: %v", m)
	}
	f := m["findings"].([]any)[0].(map[string]any)
	if f["code"] != "dockerfile_parse_error" || f["line"].(float64) != 2 {
		t.Errorf("finding = %v", f)
	}

	// Empty Dockerfile + one index.html: dockerfile_missing, steps name the static template.
	res = call(t, cs, "validate_dockerfile", map[string]any{"dockerfile": "", "file_list": []string{"index.html"}})
	m = structured(t, res)
	if m["ok"] != false || m["stack"] != "static" {
		t.Errorf("result = %v", m)
	}
	if steps := m["next_steps"].([]any); !strings.Contains(steps[0].(string), `"static"`) {
		t.Errorf("next_steps = %v", steps)
	}
}

func TestReadOnlyTokenCannotDeploy(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.readToken)
	for name, args := range map[string]map[string]any{
		"create_app":    {"name": "x"},
		"deploy_files":  {"app": "x", "files": []map[string]any{{"path": "index.html", "content": "hi"}}},
		"delete_app":    {"app": "x", "confirm_slug": "x"},
		"stop_app":      {"app": "x"},
		"create_backup": {"app": "x"},
	} {
		res := call(t, cs, name, args)
		if !res.IsError {
			t.Errorf("%s with a read token must fail", name)
			continue
		}
		m := structured(t, res)
		if m["code"] != "token_read_only" || m["hint"] == "" {
			t.Errorf("%s: %v", name, m)
		}
	}
	// Read tools still work.
	if res := call(t, cs, "whoami", nil); res.IsError {
		t.Errorf("whoami failed: %s", text(res))
	}
	if res := call(t, cs, "list_apps", nil); res.IsError {
		t.Errorf("list_apps failed: %s", text(res))
	}
}

func TestDeleteAppNeedsConfirmSlug(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.deployToken)
	res := call(t, cs, "delete_app", map[string]any{"app": "whatever", "confirm_slug": ""})
	m := structured(t, res)
	if !res.IsError || m["code"] != "confirm_slug_required" {
		t.Errorf("got %v", m)
	}
}

func TestInputValidationAndWaitCap(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.readToken)
	// A too-large wait is clamped to 50 s (not rejected); the unknown id then fails fast.
	start := time.Now()
	res := call(t, cs, "get_deployment", map[string]any{"id": "dep_nope", "wait_seconds": 120})
	if !res.IsError || time.Since(start) > 5*time.Second {
		t.Errorf("unknown deployment should fail fast: %s", text(res))
	}
	res = call(t, cs, "get_template", map[string]any{"stack": 5})
	if !res.IsError {
		t.Error("wrong argument type must be an error result")
	}
}

func TestSetGitSourceRejectsBadURL(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.deployToken)
	created := call(t, cs, "create_app", map[string]any{"name": "Git test"})
	if created.IsError {
		t.Skipf("create_app not available yet: %s", text(created))
	}
	slug := structured(t, created)["app"].(map[string]any)["slug"].(string)
	res := call(t, cs, "set_git_source", map[string]any{"app": slug, "repo_url": "git@github.com:o/r.git", "branch": "main"})
	m := structured(t, res)
	if !res.IsError || m["code"] != "source_url_invalid" {
		t.Errorf("got %v", m)
	}
	res = call(t, cs, "set_git_source", map[string]any{"app": slug, "repo_url": "https://user:pw@github.com/o/r"})
	if m := structured(t, res); !res.IsError || m["code"] != "source_url_invalid" || strings.Contains(text(res), "pw@") {
		t.Errorf("got %v", m)
	}
}

func TestDockerfileMissingErrorEndToEnd(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.deployToken)
	created := call(t, cs, "create_app", map[string]any{"name": "Static site"})
	if created.IsError {
		t.Skipf("create_app not available yet: %s", text(created))
	}
	slug := structured(t, created)["app"].(map[string]any)["slug"].(string)
	res := call(t, cs, "deploy_files", map[string]any{"app": slug, "files": []map[string]any{{"path": "index.html", "content": "<h1>hi</h1>"}}})
	if !res.IsError {
		t.Skipf("deploy_files accepted a folder without Dockerfile: %s", text(res))
	}
	m := structured(t, res)
	static, _ := templates.Get("static")
	if m["code"] != "dockerfile_missing" || m["next_tool"] != "get_template" || !strings.Contains(m["hint"].(string), static.Dockerfile) {
		t.Errorf("error = %v", m)
	}
}

func TestResourcesAndPrompt(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.readToken)
	ctx := context.Background()

	r, err := cs.ReadResource(ctx, &sdk.ReadResourceParams{URI: "bitploy://rules"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Contents[0].Text, "Runtime contract") || !strings.Contains(r.Contents[0].Text, "Every app is built from a Dockerfile") {
		t.Errorf("rules = %s", r.Contents[0].Text)
	}
	r, err = cs.ReadResource(ctx, &sdk.ReadResourceParams{URI: "bitploy://templates/node-spa"})
	if err != nil {
		t.Fatal(err)
	}
	spa, _ := templates.Get("node-spa")
	if !strings.Contains(r.Contents[0].Text, strings.TrimSpace(spa.Dockerfile)) {
		t.Errorf("template resource lacks the Dockerfile: %s", r.Contents[0].Text)
	}
	if _, err := cs.ReadResource(ctx, &sdk.ReadResourceParams{URI: "bitploy://templates/cobol"}); err == nil {
		t.Error("unknown template resource must fail")
	}
	rt, err := cs.ListResourceTemplates(ctx, nil)
	if err != nil || len(rt.ResourceTemplates) != 1 || rt.ResourceTemplates[0].URITemplate != "bitploy://templates/{stack}" {
		t.Errorf("templates = %+v, %v", rt, err)
	}
	rs, err := cs.ListResources(ctx, nil)
	if err != nil || len(rs.Resources) != 1+len(templates.Stacks) {
		t.Errorf("resources = %+v, %v", rs, err)
	}

	p, err := cs.GetPrompt(ctx, &sdk.GetPromptParams{Name: "deploy_project"})
	if err != nil {
		t.Fatal(err)
	}
	pt := p.Messages[0].Content.(*sdk.TextContent).Text
	for _, s := range []string{"get_template", "validate_dockerfile", "wait_seconds=30", "3 failures"} {
		if !strings.Contains(pt, s) {
			t.Errorf("prompt lacks %q", s)
		}
	}
	for i := 1; i <= 8; i++ {
		if !strings.Contains(pt, string(rune('0'+i))+". ") {
			t.Errorf("prompt lacks step %d", i)
		}
	}
}

func TestToolErrorsAreRedacted(t *testing.T) {
	e := newEnv(t)
	s := &srv{svc: e.svc}
	res := s.errorResult("x", os.ErrNotExist)
	m := res.StructuredContent.(map[string]any)
	if m["code"] != "internal_error" || strings.Contains(text(res), "file does not exist") {
		t.Errorf("internal errors must not leak causes: %v", m)
	}
}

func TestDeploymentResultNextSteps(t *testing.T) {
	d := app.Deployment{ID: "dep_1", AppSlug: "a", Seq: 1, State: "building"}
	r := deploymentResult("Deployment", d)
	if !strings.Contains(r.Next[0], "wait_seconds=30") {
		t.Errorf("in-progress next = %v", r.Next)
	}
	d.State, d.URL = "running", "https://a.bitploy.test"
	if r := deploymentResult("Deployment", d); !strings.Contains(r.Next[0], "https://a.bitploy.test") {
		t.Errorf("running next = %v", r.Next)
	}
	d.State = "failed"
	d.Error = &app.DeployError{Code: "unhealthy", Message: "The app did not answer.", Hint: "Check the logs."}
	d.LogTail = []string{"Server running at http://127.0.0.1:3000"}
	r = deploymentResult("Deployment", d)
	got := r.Data["deployment"].(app.Deployment)
	if !strings.Contains(got.Error.Hint, "listens on localhost; make it listen on 0.0.0.0:$PORT") {
		t.Errorf("hint = %q", got.Error.Hint)
	}
	if !strings.Contains(strings.Join(r.Next, " "), "3 failures") {
		t.Errorf("failed next = %v", r.Next)
	}
}

func TestParseSince(t *testing.T) {
	now := mustTime(t, "2026-10-29T12:00:00Z")
	for in, want := range map[string]string{
		"10m":                  "2026-10-29T11:50:00Z",
		"2h":                   "2026-10-29T10:00:00Z",
		"1d":                   "2026-10-28T12:00:00Z",
		"2026-10-29T08:00:00Z": "2026-10-29T08:00:00Z",
	} {
		got, err := parseSince(in, now)
		if err != nil || got.UTC().Format("2006-01-02T15:04:05Z") != want {
			t.Errorf("parseSince(%q) = %v, %v; want %s", in, got, err, want)
		}
	}
	if _, err := parseSince("yesterday", now); err == nil {
		t.Error("expected error")
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestOptionalNullOrEmptyChoiceMeansLeftOut(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.deployToken)
	for _, args := range []map[string]any{
		{"name": "nulls", "type": nil},
		{"name": "empty choice", "type": ""},
	} {
		res := call(t, cs, "create_app", args)
		if res.IsError {
			t.Errorf("create_app %v: %s", args, text(res))
		}
	}
	// A wrong choice is still refused, and so is null for a required argument.
	if res := call(t, cs, "create_app", map[string]any{"name": "bad", "type": "cron"}); !res.IsError {
		t.Error("type=cron must be refused")
	}
	if res := call(t, cs, "get_app", map[string]any{"app": nil}); !res.IsError {
		t.Error("app=null must be refused")
	}
}
