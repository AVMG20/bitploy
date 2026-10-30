// Package mcp is the MCP door: tools, resources, prompts and instructions.
// OWNER: MCP agent.
//
// Every tool calls one service method in internal/app and renders the answer
// the same way: structuredContent (a JSON object with next_steps on success,
// {code, message, hint, next_tool} with isError on failure) plus text.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/AVMG20/bitploy/internal/app"
	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/auth"
	"github.com/AVMG20/bitploy/internal/mcp/rules"
)

const (
	maxBodyBytes = 4 << 20 // request body limit
	callTimeout  = 55 * time.Second
	maxWaitSec   = 50 // get_deployment wait_seconds ceiling; Cloudflare cuts at 125 s
	actorKey     = "bitploy_actor"
)

// Instructions returns the server instructions for a config: the SPEC block
// with the example address pointing at the real apps domain.
func Instructions(svc *app.Service) string {
	return rules.Instructions(svc.Cfg.AppURL("<slug>"), svc.Cfg.AppsDomain)
}

// Handler returns the Streamable HTTP MCP endpoint (stateless), wrapped in
// bearer-token auth that resolves tokens through svc.AuthenticateToken.
func Handler(svc *app.Service) http.Handler {
	server := NewServer(svc)
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: false})
	verify := verifier(svc)
	secured := sdkauth.RequireBearerToken(verify, &sdkauth.RequireBearerTokenOptions{AllowMissingExpiration: true})(h)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		secured.ServeHTTP(w, r)
	})
}

// verifier resolves a bearer token into an actor with channel mcp. When the
// router's auth middleware already did that, its actor is reused.
func verifier(svc *app.Service) sdkauth.TokenVerifier {
	return func(ctx context.Context, token string, req *http.Request) (*sdkauth.TokenInfo, error) {
		a, ok := auth.ActorFrom(ctx)
		if !ok {
			var err error
			a, err = svc.AuthenticateToken(ctx, token, auth.ClientIP(req, svc.Cfg))
			if err != nil {
				return nil, fmt.Errorf("%w: %s", sdkauth.ErrInvalidToken, apperr.As(err).Message)
			}
		}
		a.Channel = app.ChannelMCP
		return &sdkauth.TokenInfo{
			Scopes: []string{string(a.Scope)},
			UserID: a.UserID,
			Extra:  map[string]any{actorKey: a},
		}, nil
	}
}

func actorFrom(extra *mcp.RequestExtra) (app.Actor, error) {
	if extra != nil && extra.TokenInfo != nil {
		if a, ok := extra.TokenInfo.Extra[actorKey].(app.Actor); ok {
			return a, nil
		}
	}
	return app.Actor{}, apperr.Unauthorized("token_invalid", "This request has no valid API token.",
		"Send the header Authorization: Bearer bp_... with a token from the bitploy dashboard under Tokens.")
}

// NewServer builds the MCP server with all tools, resources and the prompt.
func NewServer(svc *app.Service) *mcp.Server {
	s := &srv{svc: svc}
	s.mcp = mcp.NewServer(&mcp.Implementation{Name: "bitploy", Title: "Bitploy", Version: "1.0.0"},
		&mcp.ServerOptions{Instructions: Instructions(svc)})
	s.registerTools()
	s.registerResources()
	s.registerPrompts()
	return s.mcp
}

type srv struct {
	svc *app.Service
	mcp *mcp.Server
}

// result is what a tool returns on success.
type result struct {
	Summary string         // one line
	Data    map[string]any // becomes structuredContent
	Next    []string       // next_steps
}

func toMap(v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]any{}
	}
	m := map[string]any{}
	_ = json.Unmarshal(b, &m)
	return m
}

func data(kv ...any) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

func (r *result) toolResult() *mcp.CallToolResult {
	m := r.Data
	if m == nil {
		m = map[string]any{}
	}
	if len(r.Next) > 0 {
		m["next_steps"] = r.Next
	}
	text := []mcp.Content{&mcp.TextContent{Text: apperr.Redact(r.Summary)}}
	if b, err := json.Marshal(m); err == nil {
		// Some clients show only text content to the model, so the data goes there too.
		text = append(text, &mcp.TextContent{Text: apperr.Redact(string(b))})
	}
	return &mcp.CallToolResult{Content: text, StructuredContent: m}
}

// errorResult renders any error as {code, message, hint, next_tool} with isError.
// next_tool is never empty: when the service layer names none, defaultNextTool picks one.
func (s *srv) errorResult(tool string, err error) *mcp.CallToolResult {
	if errors.Is(err, context.DeadlineExceeded) {
		err = apperr.Unavailable("tool_timeout", "The call took longer than allowed and was stopped.",
			"Call get_deployment with wait_seconds=30 to see where things stand, or try the call again.").WithNext("get_deployment")
	}
	ae := apperr.As(err)
	if ae.HTTPStatus >= 500 {
		s.svc.Log.Error("mcp tool failed", "tool", tool, "err", err)
	}
	next := defaultNextTool(tool, ae)
	hint := ae.Hint + hintSuffix(tool, ae.Code)
	m := map[string]any{
		"code":      ae.Code,
		"message":   apperr.Redact(ae.Message),
		"hint":      apperr.Redact(hint),
		"next_tool": next,
	}
	if len(ae.Details) > 0 {
		d := map[string]any{}
		for k, v := range ae.Details {
			switch k {
			case "dockerfile", "dockerignore": // already spelled out in the hint
			default:
				d[k] = v
			}
		}
		if len(d) > 0 {
			m["details"] = d
		}
	}
	text := ae.Code + ": " + ae.Message
	if hint != "" {
		text += "\nFix: " + hint
	}
	text += "\nNext tool: " + next
	return &mcp.CallToolResult{
		IsError:           true,
		Content:           []mcp.Content{&mcp.TextContent{Text: apperr.Redact(text)}},
		StructuredContent: m,
	}
}

// hintSuffix adds tool-specific advice the service layer cannot know (it does
// not know which door the call came through).
func hintSuffix(tool, code string) string {
	switch {
	case code == "dockerfile_missing" && tool == "deploy_files":
		return "\n\nAfter saving the two files, call deploy_files again with ALL project files, including the new Dockerfile and .dockerignore."
	case code == "dockerfile_missing" && tool == "deploy":
		return "\n\nAdd the two files to the project: for a git source commit and push them, for a zip create it again (create_upload), for files use deploy_files with ALL files."
	}
	return ""
}

var validatingPrefix = regexp.MustCompile(`validating (/properties/)?`)

// friendlySchemaError shortens the JSON-schema validator's wording.
func friendlySchemaError(msg string) string {
	msg = strings.ReplaceAll(msg, "validating root: ", "")
	msg = validatingPrefix.ReplaceAllString(msg, "")
	return strings.ReplaceAll(msg, "/items", "[]")
}

// defaultNextTool names the tool an agent should call after an error. The
// service layer sets Next for most errors; this covers the rest so that
// next_tool is always present. By default an input problem points back at the
// same tool (fix the arguments and call it again).
func defaultNextTool(tool string, ae *apperr.Error) string {
	if ae.Next != "" {
		return ae.Next
	}
	switch ae.Code {
	case "token_read_only", "token_invalid", "account_suspended", "session_required":
		return "whoami"
	case "app_busy":
		return "list_deployments"
	case "deploy_rate_limited":
		return "get_app"
	case "domain_not_found", "domain_taken":
		return "get_app"
	case "no_container":
		return "get_deployment"
	}
	switch {
	case ae.HTTPStatus == 401 || ae.HTTPStatus == 403:
		return "whoami"
	case ae.HTTPStatus == 404:
		return "list_apps"
	case ae.HTTPStatus == 429:
		return "get_app"
	}
	return tool
}

// usage lists a tool's arguments for invalid_arguments errors.
func usage(schema *jsonschema.Schema) string {
	req := map[string]bool{}
	for _, r := range schema.Required {
		req[r] = true
	}
	names := make([]string, 0, len(schema.Properties))
	for n := range schema.Properties {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if req[names[i]] != req[names[j]] {
			return req[names[i]]
		}
		return names[i] < names[j]
	})
	if len(names) == 0 {
		return "This tool takes no arguments: send {}."
	}
	parts := make([]string, len(names))
	for i, n := range names {
		typ := schema.Properties[n].Type
		for _, t := range schema.Properties[n].Types {
			if typ == "" && t != "null" {
				typ = t
			}
		}
		kind := "optional"
		if req[n] {
			kind = "required"
		}
		parts[i] = fmt.Sprintf("%s (%s, %s)", n, typ, kind)
	}
	return "Arguments: " + strings.Join(parts, ", ") + "."
}

// addTool registers a typed tool. deploy=true tools need a deploy-scope token.
// Arguments are validated here instead of by the SDK, and results are built
// here too, so that every failure (also a wrong argument) is returned as
// {code, message, hint, next_tool} structured content.
func addTool[In any](s *srv, name, title, desc string, ann *mcp.ToolAnnotations, deploy bool,
	tweak func(*jsonschema.Schema), fn func(ctx context.Context, a app.Actor, in In) (*result, error)) {
	schema, err := jsonschema.For[In](nil)
	if err != nil {
		panic(fmt.Sprintf("tool %s: %v", name, err))
	}
	if tweak != nil {
		tweak(schema)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		panic(fmt.Sprintf("tool %s: %v", name, err))
	}
	if ann != nil {
		ann.Title = title
	}
	tool := &mcp.Tool{Name: name, Title: title, Description: desc, Annotations: ann, InputSchema: schema}
	s.mcp.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		a, err := actorFrom(req.Extra)
		if err == nil && deploy {
			err = app.RequireDeploy(a)
		}
		var in In
		if err == nil {
			err = parseArgs(name, schema, resolved, req.Params.Arguments, &in)
		}
		var res *result
		if err == nil {
			res, err = fn(ctx, a, in)
		}
		if err != nil {
			return s.errorResult(name, err), nil
		}
		return res.toolResult(), nil
	})
}

// parseArgs validates raw arguments against the tool schema and decodes them.
func parseArgs(tool string, schema *jsonschema.Schema, resolved *jsonschema.Resolved, raw json.RawMessage, in any) error {
	bad := func(why string) error {
		return apperr.BadRequest("invalid_arguments",
			"The arguments for "+tool+" are not valid: "+why,
			usage(schema)+" Fix the arguments and call "+tool+" again.").WithNext(tool)
	}
	v := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &v); err != nil {
			return bad("the arguments must be a JSON object.")
		}
	}
	// Models often send null (or "" for a choice) for an optional argument they
	// mean to leave out. Treat it as left out instead of failing the schema.
	required := map[string]bool{}
	for _, r := range schema.Required {
		required[r] = true
	}
	dropped := false
	for k, val := range v {
		p := schema.Properties[k]
		if required[k] || p == nil {
			continue
		}
		if val == nil || (val == "" && len(p.Enum) > 0) {
			delete(v, k)
			dropped = true
		}
	}
	if err := resolved.Validate(v); err != nil {
		return bad(friendlySchemaError(oneLine(err.Error())))
	}
	if dropped {
		raw, _ = json.Marshal(v)
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, in); err != nil {
			return bad(oneLine(err.Error()))
		}
	}
	return nil
}

func boolPtr(b bool) *bool { return &b }

// Annotation helpers. DestructiveHint defaults to true in MCP, so every
// non-destructive write sets it to false explicitly.
func readOnly() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(false)}
}
func write() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false)}
}
func idempotent() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), IdempotentHint: true, OpenWorldHint: boolPtr(false)}
}
func destructive() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(false)}
}
func openWorld(a *mcp.ToolAnnotations) *mcp.ToolAnnotations {
	a.OpenWorldHint = boolPtr(true)
	return a
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
