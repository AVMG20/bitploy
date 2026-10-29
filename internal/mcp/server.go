// Package mcp is the MCP door: tools, resources, prompts and instructions.
// OWNER: MCP agent.
//
// Every tool calls one service method in internal/app and renders the answer
// the same way: structuredContent (a JSON object with next_steps on success,
// {code, message, hint, next_tool} with isError on failure) plus text.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
func (s *srv) errorResult(tool string, err error) *mcp.CallToolResult {
	if errors.Is(err, context.DeadlineExceeded) {
		err = apperr.Unavailable("tool_timeout", "The call took longer than allowed and was stopped.",
			"Call get_deployment with wait_seconds=30 to see where things stand, or try the call again.").WithNext("get_deployment")
	}
	ae := apperr.As(err)
	if ae.HTTPStatus >= 500 {
		s.svc.Log.Error("mcp tool failed", "tool", tool, "err", err)
	}
	m := map[string]any{
		"code":      ae.Code,
		"message":   apperr.Redact(ae.Message),
		"hint":      apperr.Redact(ae.Hint),
		"next_tool": ae.Next,
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
	if ae.Hint != "" {
		text += "\nFix: " + ae.Hint
	}
	if ae.Next != "" {
		text += "\nNext tool: " + ae.Next
	}
	return &mcp.CallToolResult{
		IsError:           true,
		Content:           []mcp.Content{&mcp.TextContent{Text: apperr.Redact(text)}},
		StructuredContent: m,
	}
}

// addTool registers a typed tool. deploy=true tools need a deploy-scope token.
// Results are built here instead of by the SDK so that failures can carry
// {code, message, hint, next_tool} as structured content.
func addTool[In any](s *srv, name, title, desc string, ann *mcp.ToolAnnotations, deploy bool,
	tweak func(*jsonschema.Schema), fn func(ctx context.Context, a app.Actor, in In) (*result, error)) {
	schema, err := jsonschema.For[In](nil)
	if err != nil {
		panic(fmt.Sprintf("tool %s: %v", name, err))
	}
	if tweak != nil {
		tweak(schema)
	}
	if ann != nil {
		ann.Title = title
	}
	tool := &mcp.Tool{Name: name, Title: title, Description: desc, Annotations: ann, InputSchema: schema}
	mcp.AddTool(s.mcp, tool, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		ctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		a, err := actorFrom(req.Extra)
		if err == nil && deploy {
			err = app.RequireDeploy(a)
		}
		var res *result
		if err == nil {
			res, err = fn(ctx, a, in)
		}
		if err != nil {
			return s.errorResult(name, err), nil, nil
		}
		return res.toolResult(), nil, nil
	})
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
