package dockerx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moby/moby/client"
)

// An existing network is only reused when it carries the app (or project) id
// that was asked for: the builder's bp-builder, or another app's leftover
// network, must never be joined by an app.
func TestEnsureNetworkRefusesSomeoneElsesNetwork(t *testing.T) {
	existing := map[string]map[string]string{
		"bp-builder": {"nl.bitploy.builder": "1"},
		"bp-mine":    {LabelManaged: "1", LabelAppID: "app_mine"},
		"bp-p-prj_1": {LabelManaged: "1", LabelProject: "prj_1"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.47")
			w.Write([]byte("OK"))
		case strings.HasSuffix(r.URL.Path, "/networks/create"):
			var body struct{ Name string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			if _, ok := existing[body.Name]; ok {
				w.WriteHeader(http.StatusConflict)
				w.Write([]byte(`{"message":"network with name ` + body.Name + ` already exists"}`))
				return
			}
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"Id":"new"}`))
		case strings.Contains(r.URL.Path, "/networks/"):
			name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			json.NewEncoder(w).Encode(map[string]any{"Name": name, "Id": name, "Labels": existing[name]})
		default:
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"message":"no such route"}`))
		}
	}))
	t.Cleanup(srv.Close)
	api, err := client.New(client.WithHost("tcp://"+strings.TrimPrefix(srv.URL, "http://")), client.WithAPIVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{API: api, Instance: "inst"}
	ctx := context.Background()

	if err := c.EnsureNetwork(ctx, "bp-builder", c.ManagedLabels("app_x", "builder")); !errors.Is(err, ErrNetworkOwnedByOther) {
		t.Errorf("bp-builder for an app: err = %v, want ErrNetworkOwnedByOther", err)
	}
	if err := c.EnsureNetwork(ctx, "bp-mine", c.ManagedLabels("app_other", "mine")); !errors.Is(err, ErrNetworkOwnedByOther) {
		t.Errorf("another app's network: err = %v, want ErrNetworkOwnedByOther", err)
	}
	if err := c.EnsureNetwork(ctx, "bp-mine", c.ManagedLabels("app_mine", "mine")); err != nil {
		t.Errorf("the app's own network: %v", err)
	}
	if err := c.EnsureNetwork(ctx, "bp-p-prj_1", c.ProjectLabels("prj_2")); !errors.Is(err, ErrNetworkOwnedByOther) {
		t.Errorf("another project's network: err = %v, want ErrNetworkOwnedByOther", err)
	}
	if err := c.EnsureNetwork(ctx, "bp-p-prj_1", c.ProjectLabels("prj_1")); err != nil {
		t.Errorf("the project's own network: %v", err)
	}
	if err := c.EnsureNetwork(ctx, "bp-new", c.ManagedLabels("app_new", "new")); err != nil {
		t.Errorf("a new network: %v", err)
	}
}
