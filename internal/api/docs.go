package api

import (
	"bytes"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/danielgtaylor/huma/v2"
)

// docs renders a read-only reference page from the generated OpenAPI spec.
// It is plain HTML and CSS: no scripts, so the strict CSP holds.
type docs struct {
	api  huma.API
	base string
	once sync.Once
	html []byte
}

type docParam struct{ Name, In, Type, Required, Desc string }
type docField struct{ Name, Type, Required, Desc string }
type docResp struct{ Code, Desc, Body string }
type docOp struct {
	ID, Method, Path, Summary, Desc, Auth, BodyType string
	Params                                          []docParam
	Body                                            []docField
	Responses                                       []docResp
}
type docGroup struct {
	Tag string
	Ops []docOp
}
type docPage struct {
	Title, Intro, Base string
	Groups             []docGroup
}

var docsTmpl = template.Must(template.New("docs").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
:root{color-scheme:light dark;--bg:#fff;--fg:#1f1f1f;--muted:#5c5c5c;--line:#e6e6e6;--card:#f6f6f6;--accent:#d9541e}
@media (prefers-color-scheme:dark){:root{--bg:#131313;--fg:#ececec;--muted:#9a9a9a;--line:#2a2a2a;--card:#1b1b1b;--accent:#f4804f}}
body{margin:0;background:var(--bg);color:var(--fg);font:15px/1.55 system-ui,-apple-system,Segoe UI,sans-serif}
.wrap{display:flex;max-width:1200px;margin:0 auto}
nav{width:15rem;flex:none;position:sticky;top:0;align-self:flex-start;height:100vh;overflow:auto;padding:1.5rem 1rem;border-right:1px solid var(--line)}
nav a{display:block;color:var(--muted);text-decoration:none;padding:.15rem 0;font-size:.9rem}
nav a:hover{color:var(--fg)}
nav b{display:block;margin-top:1rem;font-size:.8rem;text-transform:uppercase;letter-spacing:.05em}
main{flex:1;min-width:0;padding:1.5rem 2rem 4rem}
h1{margin-top:0}
h2{margin-top:2.5rem;border-bottom:1px solid var(--line);padding-bottom:.4rem}
section.op{border:1px solid var(--line);border-radius:.5rem;padding:1rem 1.25rem;margin:1rem 0;background:var(--card)}
.sig{font:600 .95rem ui-monospace,SFMono-Regular,monospace;word-break:break-all}
.m{display:inline-block;min-width:3.6rem;padding:.05rem .4rem;margin-right:.5rem;border-radius:.25rem;color:#fff;text-align:center;font-size:.8rem}
.GET{background:#16a34a}.POST{background:#2563eb}.PUT{background:#d97706}.PATCH{background:#7c3aed}.DELETE{background:#dc2626}
table{border-collapse:collapse;width:100%;margin:.4rem 0 .8rem;font-size:.9rem}
th,td{text-align:left;padding:.25rem .5rem;border-bottom:1px solid var(--line);vertical-align:top}
th{color:var(--muted);font-weight:600}
code{font-family:ui-monospace,SFMono-Regular,monospace;font-size:.88em}
.muted{color:var(--muted)}
h4{margin:.9rem 0 .2rem;font-size:.8rem;text-transform:uppercase;letter-spacing:.05em;color:var(--muted)}
a{color:var(--accent)}
@media (max-width:800px){.wrap{display:block}nav{position:static;width:auto;height:auto;border:0}main{padding:1rem}}
</style></head><body><div class="wrap">
<nav><a href="/">bitploy dashboard</a><a href="{{.Base}}/openapi.json">openapi.json</a>
{{range .Groups}}<b>{{.Tag}}</b>{{range .Ops}}<a href="#{{.ID}}">{{.Summary}}</a>{{end}}{{end}}</nav>
<main>
<h1>{{.Title}}</h1>
<p>{{.Intro}}</p>
<p class="muted">Base URL: <code>{{.Base}}</code>. Machine-readable spec: <a href="{{.Base}}/openapi.json">openapi.json</a> (OpenAPI 3.1).</p>
<p>Errors always look like <code>{"error": {"code", "message", "hint", "next", "details"}}</code>. Lists return <code>{"items": [], "next_cursor": ""}</code> and take <code>?limit=</code> (max 100) and <code>?cursor=</code>. Requests that create things accept an <code>Idempotency-Key</code> header (replayed for 24 hours). Tokens may make 120 requests per minute; beyond that you get <code>429</code> with <code>Retry-After</code>.</p>
{{range .Groups}}<h2>{{.Tag}}</h2>
{{range .Ops}}<section class="op" id="{{.ID}}">
<div class="sig"><span class="m {{.Method}}">{{.Method}}</span>{{.Path}}</div>
<p><b>{{.Summary}}</b>{{if .Desc}}<br>{{.Desc}}{{end}}</p>
<p class="muted">Auth: {{.Auth}}</p>
{{if .Params}}<h4>Parameters</h4><table><tr><th>Name</th><th>In</th><th>Type</th><th>Required</th><th>Description</th></tr>
{{range .Params}}<tr><td><code>{{.Name}}</code></td><td>{{.In}}</td><td>{{.Type}}</td><td>{{.Required}}</td><td>{{.Desc}}</td></tr>{{end}}</table>{{end}}
{{if .Body}}<h4>Request body{{if .BodyType}} ({{.BodyType}}){{end}}</h4><table><tr><th>Field</th><th>Type</th><th>Required</th><th>Description</th></tr>
{{range .Body}}<tr><td><code>{{.Name}}</code></td><td>{{.Type}}</td><td>{{.Required}}</td><td>{{.Desc}}</td></tr>{{end}}</table>{{end}}
<h4>Responses</h4><table><tr><th>Status</th><th>Meaning</th><th>Body</th></tr>
{{range .Responses}}<tr><td><code>{{.Code}}</code></td><td>{{.Desc}}</td><td>{{.Body}}</td></tr>{{end}}</table>
</section>{{end}}{{end}}
</main></div></body></html>`))

func (d *docs) build() []byte {
	oapi := d.api.OpenAPI()
	b := &docBuilder{reg: oapi.Components.Schemas}
	byTag := map[string][]docOp{}
	var tagOrder []string
	for _, t := range oapi.Tags {
		tagOrder = append(tagOrder, t.Name)
	}
	paths := make([]string, 0, len(oapi.Paths))
	for p := range oapi.Paths {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		item := oapi.Paths[p]
		for _, m := range []struct {
			name string
			op   *huma.Operation
		}{{"GET", item.Get}, {"POST", item.Post}, {"PUT", item.Put}, {"PATCH", item.Patch}, {"DELETE", item.Delete}} {
			if m.op == nil {
				continue
			}
			tag := "Other"
			if len(m.op.Tags) > 0 {
				tag = m.op.Tags[0]
			}
			if _, ok := byTag[tag]; !ok && !contains(tagOrder, tag) {
				tagOrder = append(tagOrder, tag)
			}
			byTag[tag] = append(byTag[tag], b.op(m.name, d.base+p, m.op))
		}
	}
	page := docPage{
		Title: "bitploy API reference",
		Intro: "Everything an AI agent can do through MCP, a script can do with curl. This page is generated from the same spec that is served at /api/v1/openapi.json.",
		Base:  d.base,
	}
	for _, t := range tagOrder {
		if ops := byTag[t]; len(ops) > 0 {
			page.Groups = append(page.Groups, docGroup{Tag: t, Ops: ops})
		}
	}
	var buf bytes.Buffer
	if err := docsTmpl.Execute(&buf, page); err != nil {
		return []byte("could not render the API reference: " + err.Error())
	}
	return buf.Bytes()
}

func (d *docs) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.once.Do(func() { d.html = d.build() })
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(d.html)
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

type docBuilder struct{ reg huma.Registry }

func (b *docBuilder) op(method, path string, op *huma.Operation) docOp {
	out := docOp{ID: op.OperationID, Method: method, Path: path, Summary: op.Summary, Desc: op.Description}
	switch {
	case op.Security != nil && len(op.Security) == 0:
		out.Auth = "none"
	case len(op.Security) == 1:
		out.Auth = "dashboard session only (an API token cannot do this)"
	default:
		out.Auth = "API token (Authorization: Bearer bp_...) or dashboard session"
	}
	for _, p := range op.Parameters {
		if p == nil {
			continue
		}
		req := "no"
		if p.Required {
			req = "yes"
		}
		out.Params = append(out.Params, docParam{Name: p.Name, In: p.In, Type: b.typeName(p.Schema), Required: req, Desc: p.Description})
	}
	if op.RequestBody != nil {
		for ct, mt := range op.RequestBody.Content {
			if mt == nil || mt.Schema == nil {
				continue
			}
			out.BodyType = ct
			b.fields(mt.Schema, "", 0, &out.Body)
			break
		}
	}
	codes := make([]string, 0, len(op.Responses))
	for c := range op.Responses {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	for _, c := range codes {
		r := op.Responses[c]
		if r == nil {
			continue
		}
		body := ""
		for _, mt := range r.Content {
			if mt != nil && mt.Schema != nil {
				body = b.typeName(mt.Schema)
				break
			}
		}
		out.Responses = append(out.Responses, docResp{Code: c, Desc: r.Description, Body: body})
	}
	return out
}

func refName(ref string) string {
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		return ref[i+1:]
	}
	return ref
}

func (b *docBuilder) resolve(s *huma.Schema) *huma.Schema {
	for i := 0; s != nil && s.Ref != "" && i < 8; i++ {
		r := b.reg.SchemaFromRef(s.Ref)
		if r == nil {
			return s
		}
		s = r
	}
	return s
}

func (b *docBuilder) typeName(s *huma.Schema) string {
	if s == nil {
		return ""
	}
	if s.Ref != "" {
		return refName(s.Ref)
	}
	switch s.Type {
	case "array":
		return "array of " + b.typeName(s.Items)
	case "object":
		if s.AdditionalProperties != nil {
			if as, ok := s.AdditionalProperties.(*huma.Schema); ok {
				return "map of " + b.typeName(as)
			}
		}
		return "object"
	case "":
		return "any"
	}
	t := s.Type
	if s.Format != "" {
		t += " (" + s.Format + ")"
	}
	if len(s.Enum) > 0 {
		var vals []string
		for _, e := range s.Enum {
			if str, ok := e.(string); ok {
				vals = append(vals, str)
			}
		}
		if len(vals) > 0 {
			t += ": " + strings.Join(vals, " | ")
		}
	}
	return t
}

func (b *docBuilder) fields(s *huma.Schema, prefix string, depth int, out *[]docField) {
	s = b.resolve(s)
	if s == nil {
		return
	}
	if s.Type == "array" && s.Items != nil && prefix == "" {
		*out = append(*out, docField{Name: "(array)", Type: b.typeName(s), Required: "yes"})
		b.fields(s.Items, "[].", depth, out)
		return
	}
	names := make([]string, 0, len(s.Properties))
	for n := range s.Properties {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		p := s.Properties[n]
		req := "no"
		if contains(s.Required, n) {
			req = "yes"
		}
		rp := b.resolve(p)
		desc := ""
		if p != nil {
			desc = p.Description
		}
		if desc == "" && rp != nil {
			desc = rp.Description
		}
		*out = append(*out, docField{Name: prefix + n, Type: b.typeName(p), Required: req, Desc: desc})
		if depth < 2 && rp != nil {
			switch {
			case rp.Type == "object" && len(rp.Properties) > 0:
				b.fields(rp, prefix+n+".", depth+1, out)
			case rp.Type == "array" && rp.Items != nil:
				if it := b.resolve(rp.Items); it != nil && it.Type == "object" && len(it.Properties) > 0 {
					b.fields(it, prefix+n+"[].", depth+1, out)
				}
			}
		}
	}
}
