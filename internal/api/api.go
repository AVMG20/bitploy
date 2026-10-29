package api

import (
	"context"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/arno/bitploy/internal/app"
	"github.com/arno/bitploy/internal/apperr"
	"github.com/arno/bitploy/internal/auth"
	"github.com/arno/bitploy/internal/config"
)

// API holds what the handlers need.
type API struct {
	svc *app.Service
	cfg *config.Config
	h   huma.API
}

// access says who may call an operation (the service layer still checks scope and ownership).
type access int

const (
	public      access = iota // no credentials needed
	anyAuth                   // session cookie or bearer token
	sessionOnly               // dashboard session only
)

const prefix = "/api/v1"

func newHuma(mux *http.ServeMux, cfg *config.Config) huma.API {
	installHumaErrors()
	c := huma.DefaultConfig("bitploy API", "1.0.0")
	c.Info.Description = "REST API of bitploy, a self-hosted deploy platform. It mirrors the MCP tools one to one: " +
		"same service calls, same error codes. Authenticate with `Authorization: Bearer bp_...` or, in the dashboard, the session cookie plus an `X-CSRF-Token` header."
	c.OpenAPIPath = "/openapi"
	c.DocsPath = ""
	c.SchemasPath = ""
	c.CreateHooks = nil
	c.Formats = map[string]huma.Format{"application/json": huma.DefaultJSONFormat, "json": huma.DefaultJSONFormat}
	c.DefaultFormat = "application/json"
	c.Servers = []*huma.Server{{URL: strings.TrimRight(cfg.PublicURL, "/") + prefix}}
	c.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"bearer":  {Type: "http", Scheme: "bearer", Description: "API token: `Authorization: Bearer bp_...`"},
		"session": {Type: "apiKey", In: "cookie", Name: auth.CookieName(cfg), Description: "Dashboard session cookie. Unsafe methods also need `Origin` equal to the public URL and `X-CSRF-Token` from `GET /session`."},
	}
	return humago.NewWithPrefix(mux, prefix, c)
}

// reg registers one operation. The handler gets the authenticated actor
// (zero for public operations) and returns errors as *apperr.Error.
func reg[I, O any](a *API, acc access, op huma.Operation, h func(ctx context.Context, act app.Actor, in *I) (*O, error)) {
	switch acc {
	case public:
		op.Security = []map[string][]string{}
	case anyAuth:
		op.Security = []map[string][]string{{"bearer": {}}, {"session": {}}}
	case sessionOnly:
		op.Security = []map[string][]string{{"session": {}}}
	}
	if op.OperationID == "" {
		op.OperationID = strings.ToLower(op.Method) + strings.NewReplacer("/", "-", "{", "", "}", "").Replace(op.Path)
	}
	huma.Register(a.h, op, func(ctx context.Context, in *I) (*O, error) {
		var actor app.Actor
		if acc == public {
			actor, _ = auth.ActorFrom(ctx)
		} else {
			var err error
			if actor, err = auth.Require(ctx); err != nil {
				return nil, toHuma(err)
			}
		}
		out, err := h(ctx, actor, in)
		if err != nil {
			return nil, toHuma(err)
		}
		return out, nil
	})
}

// Out wraps a JSON response body.
type Out[T any] struct {
	Body T
}

// Empty is a response without a body (204).
type Empty struct{}

// ---- request metadata (client IP, user agent) ----

type metaKey struct{}

type reqMeta struct{ IP, UserAgent string }

func withMeta(cfg *config.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := reqMeta{IP: auth.ClientIP(r, cfg), UserAgent: r.UserAgent()}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), metaKey{}, m)))
	})
}

func metaFrom(ctx context.Context) reqMeta {
	m, _ := ctx.Value(metaKey{}).(reqMeta)
	return m
}

// ---- pagination ----

// PageParams are the shared list query parameters.
type PageParams struct {
	Limit  int    `query:"limit" minimum:"0" doc:"Page size, 1 to 100 (default 50; larger values are capped)."`
	Cursor string `query:"cursor" doc:"Opaque cursor: the next_cursor of the previous page."`
}

func pageLimit(n int) int {
	switch {
	case n <= 0:
		return 50
	case n > 100:
		return 100
	}
	return n
}

// paginate slices an in-memory list. The cursor is an opaque offset.
func paginate[T any](items []T, p PageParams) (app.Page[T], error) {
	limit := pageLimit(p.Limit)
	offset := 0
	if p.Cursor != "" {
		b, err := base64.RawURLEncoding.DecodeString(p.Cursor)
		n, err2 := strconv.Atoi(strings.TrimPrefix(string(b), "o:"))
		if err != nil || err2 != nil || n < 0 || !strings.HasPrefix(string(b), "o:") {
			return app.Page[T]{}, apperr.BadRequest("invalid_cursor", "The cursor is not valid.", "Use the next_cursor value from the previous page, unchanged.")
		}
		offset = n
	}
	if offset > len(items) {
		offset = len(items)
	}
	end := offset + limit
	page := app.Page[T]{Items: []T{}}
	if end < len(items) {
		page.NextCursor = base64.RawURLEncoding.EncodeToString([]byte("o:" + strconv.Itoa(end)))
	} else {
		end = len(items)
	}
	page.Items = append(page.Items, items[offset:end]...)
	return page, nil
}

func pageOut[T any](items []T, err error, p PageParams) (*Out[app.Page[T]], error) {
	if err != nil {
		return nil, err
	}
	pg, err := paginate(items, p)
	if err != nil {
		return nil, err
	}
	return &Out[app.Page[T]]{Body: pg}, nil
}

func okOrNil[T any](v T, err error) (*Out[T], error) {
	if err != nil {
		return nil, err
	}
	return &Out[T]{Body: v}, nil
}
