package dockerx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"
)

// fakeDaemon answers the list calls with a mix of resources: this installation's,
// another installation's, and ones without an instance label (an older version).
// It records the label filters it was asked for.
type fakeDaemon struct {
	mu      sync.Mutex
	filters []string // "path: decoded filters"
	removed []string
}

func (f *fakeDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.filters = append(f.filters, r.URL.Path+": "+r.URL.Query().Get("filters"))
	f.mu.Unlock()
	lbl := func(app, inst string) map[string]string {
		m := map[string]string{LabelManaged: "1", LabelAppID: app}
		if inst != "" {
			m[LabelInstance] = inst
		}
		return m
	}
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.Path
	switch {
	case strings.HasSuffix(path, "/_ping"):
		w.Header().Set("API-Version", "1.47")
		w.Write([]byte("OK"))
	case strings.HasSuffix(path, "/containers/json"):
		json.NewEncoder(w).Encode([]map[string]any{
			{"Id": "c-mine", "Names": []string{"/bp-a-1"}, "Labels": lbl("app_a", "inst-me")},
			{"Id": "c-other", "Names": []string{"/bp-b-1"}, "Labels": lbl("app_b", "inst-other")},
			{"Id": "c-legacy", "Names": []string{"/bp-c-1"}, "Labels": lbl("app_c", "")},
		})
	case strings.HasSuffix(path, "/networks"):
		json.NewEncoder(w).Encode([]map[string]any{
			{"Name": "bp-a", "Id": "n1", "Labels": lbl("app_a", "inst-me")},
			{"Name": "bp-b", "Id": "n2", "Labels": lbl("app_b", "inst-other")},
			{"Name": "bp-c", "Id": "n3", "Labels": lbl("app_c", "")},
		})
	case strings.HasSuffix(path, "/volumes"):
		json.NewEncoder(w).Encode(map[string]any{"Volumes": []map[string]any{
			{"Name": "bp-a-data", "Labels": lbl("app_a", "inst-me")},
			{"Name": "bp-b-data", "Labels": lbl("app_b", "inst-other")},
			{"Name": "bp-c-data", "Labels": lbl("app_c", "")},
		}})
	case strings.HasSuffix(path, "/images/json"):
		json.NewEncoder(w).Encode([]map[string]any{
			{"Id": "i1", "RepoTags": []string{"bitploy/a:1"}, "Labels": lbl("app_a", "inst-me")},
			{"Id": "i2", "RepoTags": []string{"bitploy/b:1"}, "Labels": lbl("app_b", "inst-other")},
			{"Id": "i3", "RepoTags": []string{"bitploy/c:1"}, "Labels": lbl("app_c", "")},
		})
	case strings.HasSuffix(path, "/images/prune"):
		json.NewEncoder(w).Encode(map[string]any{"SpaceReclaimed": 1})
	default:
		w.WriteHeader(404)
		w.Write([]byte(`{"message":"no such route in the fake daemon"}`))
	}
}

func newFakeClient(t *testing.T, instance string) (*Client, *fakeDaemon) {
	t.Helper()
	fd := &fakeDaemon{}
	srv := httptest.NewServer(fd)
	t.Cleanup(srv.Close)
	api, err := client.New(client.WithHost("tcp://"+strings.TrimPrefix(srv.URL, "http://")), client.WithAPIVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	return &Client{API: api, Instance: instance}, fd
}

func (f *fakeDaemon) sawFilter(want ...string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, line := range f.filters {
		ok := true
		for _, w := range want {
			ok = ok && strings.Contains(line, w)
		}
		if ok {
			return true
		}
	}
	return false
}

// Everything that lists, prunes or watches asks the daemon for this installation's
// label only, and drops anything else that comes back.
func TestListsOnlyReturnOwnResources(t *testing.T) {
	ctx := context.Background()
	c, fd := newFakeClient(t, "inst-me")

	cs, err := c.ListManagedContainers(ctx)
	if err != nil || len(cs) != 1 || cs[0].ID != "c-mine" {
		t.Errorf("containers = %+v, %v", cs, err)
	}
	if !fd.sawFilter("/containers/json", LabelInstance+"=inst-me") {
		t.Errorf("container list must filter on the instance label: %v", fd.filters)
	}
	ms, err := c.ManagedContainers(ctx, true)
	if err != nil || len(ms) != 1 || ms[0].AppID != "app_a" {
		t.Errorf("managed containers = %+v, %v", ms, err)
	}
	ns, err := c.ListManagedNetworks(ctx)
	if err != nil || len(ns) != 1 || ns[0].Name != "bp-a" {
		t.Errorf("networks = %+v, %v", ns, err)
	}
	vs, err := c.ListManagedVolumes(ctx)
	if err != nil || len(vs) != 1 || vs[0].Name != "bp-a-data" {
		t.Errorf("volumes = %+v, %v", vs, err)
	}
	ims, err := c.ListManagedImages(ctx)
	if err != nil || len(ims) != 1 || ims[0].AppID != "app_a" {
		t.Errorf("images = %+v, %v", ims, err)
	}
	if _, err := c.PruneDanglingImages(ctx); err != nil {
		t.Fatal(err)
	}
	if !fd.sawFilter("/images/prune", LabelInstance+"=inst-me", "dangling") {
		t.Errorf("image prune must filter on the instance label: %v", fd.filters)
	}
	if n, err := c.CountForeign(ctx); err != nil || n != 2 {
		t.Errorf("foreign = %d, %v (other installation + legacy)", n, err)
	}

	// By app id (DeleteApp, orphan images): this installation's and legacy resources, never another one's.
	acs, err := c.ListAppContainers(ctx, "app_x")
	if err != nil || len(acs) != 2 {
		t.Errorf("app containers = %+v, %v", acs, err)
	}
	for _, it := range acs {
		if it.ID == "c-other" {
			t.Error("another installation's container returned by app id")
		}
	}
	tags, err := c.ListAppImages(ctx, "app_x")
	if err != nil || len(tags) != 2 || tags[0] == "bitploy/b:1" || tags[1] == "bitploy/b:1" {
		t.Errorf("app images = %v, %v", tags, err)
	}
}

// Without an instance id nothing is listed, pruned or watched.
func TestNoInstanceRefusesEverything(t *testing.T) {
	ctx := context.Background()
	c, fd := newFakeClient(t, "")
	if _, err := c.ListManagedContainers(ctx); err != ErrNoInstance {
		t.Errorf("containers: %v", err)
	}
	if _, err := c.ManagedContainers(ctx, true); err != ErrNoInstance {
		t.Errorf("managed containers: %v", err)
	}
	if _, err := c.ListManagedNetworks(ctx); err != ErrNoInstance {
		t.Errorf("networks: %v", err)
	}
	if _, err := c.ListManagedVolumes(ctx); err != ErrNoInstance {
		t.Errorf("volumes: %v", err)
	}
	if _, err := c.ListManagedImages(ctx); err != ErrNoInstance {
		t.Errorf("images: %v", err)
	}
	if _, err := c.ListAppContainers(ctx, "app_x"); err != ErrNoInstance {
		t.Errorf("app containers: %v", err)
	}
	if _, err := c.PruneDanglingImages(ctx); err != ErrNoInstance {
		t.Errorf("prune: %v", err)
	}
	if err := c.WatchEvents(ctx, time.Time{}, func(Event) {}); err != ErrNoInstance {
		t.Errorf("events: %v", err)
	}
	if len(fd.filters) != 0 {
		t.Errorf("the daemon must not be asked anything: %v", fd.filters)
	}
}

func TestManagedLabelsCarryTheInstance(t *testing.T) {
	c := &Client{Instance: "inst-me"}
	l := c.ManagedLabels("app_1", "my-app")
	if l[LabelManaged] != "1" || l[LabelAppID] != "app_1" || l[LabelSlug] != "my-app" || l[LabelInstance] != "inst-me" {
		t.Errorf("labels = %v", l)
	}
	if _, ok := ManagedLabels("app_1", "my-app")[LabelInstance]; ok {
		t.Error("the package-level helper has no instance")
	}
}

func TestEventCarriesInstance(t *testing.T) {
	m := events.Message{Type: events.ContainerEventType, Action: "die", Actor: events.Actor{ID: "c1", Attributes: map[string]string{
		LabelManaged: "1", LabelAppID: "app_1", LabelInstance: "inst-other", "name": "bp-x-1",
	}}}
	ev, ok := ParseEvent(m)
	if !ok || ev.Instance != "inst-other" {
		t.Errorf("event = %+v %v", ev, ok)
	}
}
