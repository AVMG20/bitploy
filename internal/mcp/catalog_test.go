package mcp

import (
	"strings"
	"testing"
)

func TestListCatalogAndDeployCatalogApp(t *testing.T) {
	e := newEnv(t)

	// list_catalog works with a read token.
	rs := e.connect(t, e.readToken)
	res := call(t, rs, "list_catalog", nil)
	if res.IsError {
		t.Fatal(text(res))
	}
	m := structured(t, res)
	items, _ := m["items"].([]any)
	if len(items) < 6 {
		t.Fatalf("items: %v", m)
	}
	first := items[0].(map[string]any)
	for _, k := range []string{"id", "name", "tagline", "version", "memory_mb", "volume", "category"} {
		if _, ok := first[k]; !ok {
			t.Errorf("item has no %s: %v", k, first)
		}
	}
	if steps, _ := m["next_steps"].([]any); len(steps) == 0 || !strings.Contains(steps[0].(string), "deploy_catalog_app") {
		t.Errorf("next_steps: %v", m["next_steps"])
	}

	// A read token cannot deploy.
	res = call(t, rs, "deploy_catalog_app", map[string]any{"id": "memos"})
	if !res.IsError || structured(t, res)["code"] != "token_read_only" {
		t.Errorf("read token: %s", text(res))
	}

	ds := e.connect(t, e.deployToken)
	res = call(t, ds, "deploy_catalog_app", map[string]any{"id": "nope"})
	m = structured(t, res)
	if !res.IsError || m["code"] != "catalog_unknown" || !strings.Contains(m["hint"].(string), "memos") {
		t.Errorf("unknown id: %v", m)
	}

	res = call(t, ds, "deploy_catalog_app", map[string]any{"id": "memos", "name": "Notes"})
	if res.IsError {
		t.Fatal(text(res))
	}
	m = structured(t, res)
	ap, _ := m["app"].(map[string]any)
	dep, _ := m["deployment"].(map[string]any)
	if ap["slug"] != "memos" || ap["catalog_id"] != "memos" || dep["state"] != "queued" {
		t.Fatalf("result: %v", m)
	}
	steps, _ := m["first_steps"].([]any)
	if len(steps) == 0 || strings.Contains(steps[0].(string), "{url}") {
		t.Errorf("first_steps: %v", steps)
	}
	next := strings.Join(toStrings(m["next_steps"]), "\n")
	if !strings.Contains(next, "get_deployment") || !strings.Contains(next, "first steps") {
		t.Errorf("next_steps: %s", next)
	}
}

func toStrings(v any) []string {
	var out []string
	l, _ := v.([]any)
	for _, x := range l {
		out = append(out, x.(string))
	}
	return out
}
