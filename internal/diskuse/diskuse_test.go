package diskuse

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/dockerx"
)

var now = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
var old = now.Add(-48 * time.Hour)

func mine(appID string, extra ...string) map[string]string {
	l := map[string]string{dockerx.LabelManaged: "1", dockerx.LabelInstance: "inst1"}
	if appID != "" {
		l[dockerx.LabelAppID] = appID
	}
	for i := 0; i+1 < len(extra); i += 2 {
		l[extra[i]] = extra[i+1]
	}
	return l
}

func facts() Facts {
	return Facts{
		Instance: "inst1",
		Apps:     map[string]App{"app_shop": {ID: "app_shop", Slug: "shop"}, "app_blog": {ID: "app_blog", Slug: "blog"}},
		ImageApp: map[string]string{
			"bitploy/shop:12": "app_shop", "bitploy/shop:11": "app_shop", "bitploy/blog:3": "app_blog",
			"postgres:16": "app_blog", "postgres:15": "app_blog",
		},
		Protected:         map[string]bool{"bitploy/shop:12": true, "bitploy/blog:3": true, "postgres:16": true},
		CurrentContainers: map[string]bool{"bp-blog-3": true},
		ActiveDeployments: map[string]bool{},
		BusyApps:          map[string]bool{},
		AppVolumes:        map[string]string{"bp-shop-data": "app_shop"},
		BuilderVolume:     "buildx_buildkit_bitploy0_state",
		Now:               now,
	}
}

func img(id string, tags []string, labels map[string]string, created time.Time) dockerx.InvImage {
	return dockerx.InvImage{ID: id, Tags: tags, Size: 1000, SharedSize: 400, Created: created, Labels: labels}
}

func TestClassifyImages(t *testing.T) {
	f := facts()
	used := map[string]bool{"sha256:run": true}
	cases := []struct {
		name      string
		im        dockerx.InvImage
		cat       string
		app       string
		removable bool
	}{
		{"runs", img("sha256:run", []string{"bitploy/shop:12"}, mine("app_shop"), old), ImageInUse, "app_shop", false},
		{"live version of a stopped app, no container", img("sha256:blog3", []string{"bitploy/blog:3"}, mine("app_blog"), old), ImageInUse, "app_blog", false},
		{"older version", img("sha256:shop11", []string{"bitploy/shop:11"}, mine("app_shop"), old), ImageRollback, "app_shop", true},
		{"older version, just built", img("sha256:new", []string{"bitploy/shop:13"}, mine("app_shop"), now.Add(-time.Minute)), ImageRollback, "app_shop", false},
		{"registry image of a live service", img("sha256:pg16", []string{"postgres:16"}, nil, old), ImageInUse, "app_blog", false},
		{"registry image of an older version", img("sha256:pg15", []string{"postgres:15"}, nil, old), ImageRollback, "app_blog", true},
		{"dangling bitploy image", img("sha256:dang", nil, mine("app_shop"), old), ImageUnused, "app_shop", true},
		{"image of a deleted app", img("sha256:gone", []string{"bitploy/gone:4"}, mine("app_gone"), old), ImageUnused, "", true},
		{"not bitploy's", img("sha256:nginx", []string{"nginx:1.27"}, nil, old), ImageOther, "", true},
		{"not bitploy's, dangling", img("sha256:x", nil, nil, old), ImageOther, "", true},
		{"another installation's", img("sha256:o", []string{"bitploy/x:1"}, map[string]string{dockerx.LabelManaged: "1", dockerx.LabelInstance: "inst2", dockerx.LabelAppID: "app_x"}, old), ImageOther, "", false},
		{"from before installation ids", img("sha256:leg", []string{"bitploy/shop:9"}, map[string]string{dockerx.LabelManaged: "1", dockerx.LabelAppID: "app_shop"}, old), ImageRollback, "app_shop", true},
		{"database viewer helper", img("sha256:dbh", []string{"bitploy-dbhelper:abc"}, mine("", dockerx.LabelDBHelper, "1"), old), ImageUnused, "", false},
	}
	for _, c := range cases {
		got := f.ClassifyImage(c.im, used)
		if got.Category != c.cat || got.AppID != c.app || got.Removable != c.removable {
			t.Errorf("%s: got %s app=%q removable=%v, want %s app=%q removable=%v", c.name, got.Category, got.AppID, got.Removable, c.cat, c.app, c.removable)
		}
	}
}

func TestClassifyImageDeployingApp(t *testing.T) {
	f := facts()
	f.BusyApps["app_shop"] = true
	if got := f.ClassifyImage(img("sha256:shop11", []string{"bitploy/shop:11"}, mine("app_shop"), old), nil); got.Removable {
		t.Errorf("an image of an app that is deploying must stay: %+v", got)
	}
	f = facts()
	f.ActiveDeployments["dep_9"] = true
	if got := f.ClassifyImage(img("sha256:d", nil, mine("app_shop", dockerx.LabelDeployment, "dep_9"), old), nil); got.Removable {
		t.Errorf("the image of a deployment in flight must stay: %+v", got)
	}
	// Docker counted a container even though the list had none.
	im := img("sha256:c", []string{"nginx:1"}, nil, old)
	im.Containers = 1
	if got := f.ClassifyImage(im, nil); !got.InUse || got.Removable {
		t.Errorf("an image a container uses must stay: %+v", got)
	}
}

func TestNormalizeRef(t *testing.T) {
	for in, want := range map[string]string{
		"postgres":                      "postgres:latest",
		"docker.io/library/postgres:16": "postgres:16",
		"library/redis":                 "redis:latest",
		"ghcr.io/acme/app:1.2":          "ghcr.io/acme/app:1.2",
		"registry.local:5000/team/app":  "registry.local:5000/team/app:latest",
		"bitploy/shop:12":               "bitploy/shop:12",
		"postgres@sha256:abc":           "postgres@sha256:abc",
		"docker.io/acme/tool":           "acme/tool:latest",
	} {
		if got := NormalizeRef(in); got != want {
			t.Errorf("NormalizeRef(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClassifyContainers(t *testing.T) {
	f := facts()
	f.ActiveDeployments["dep_live"] = true
	ct := func(name, state string, labels map[string]string, created time.Time) dockerx.InvContainer {
		return dockerx.InvContainer{ID: name, Name: name, State: state, Labels: labels, Created: created, SizeRw: 10}
	}
	cases := []struct {
		c         dockerx.InvContainer
		cat       string
		removable bool
	}{
		{ct("bp-shop-12", "running", mine("app_shop"), old), ContainerRunning, false},
		{ct("bp-blog-3", "exited", mine("app_blog"), old), ContainerStopped, false},
		{ct("bp-shop-11", "exited", mine("app_shop"), old), ContainerLeftover, true},
		{ct("bp-shop-13", "created", mine("app_shop", dockerx.LabelDeployment, "dep_live"), old), ContainerLeftover, false},
		{ct("bp-shop-14", "exited", mine("app_shop"), now.Add(-5*time.Minute)), ContainerLeftover, false},
		{ct("bp-gone-2", "exited", mine("app_gone"), old), ContainerLeftover, true},
		{ct("bp-copy-abc", "created", mine(""), now.Add(-2*time.Hour)), ContainerLeftover, false},
		{ct("bp-copy-def", "created", mine(""), now.Add(-7*time.Hour)), ContainerLeftover, true},
		{ct("traefik", "running", nil, old), ContainerOther, false},
		{ct("someone-elses", "exited", nil, old), ContainerOther, false},
		{ct("bp-x-1", "exited", map[string]string{dockerx.LabelManaged: "1", dockerx.LabelInstance: "inst2", dockerx.LabelAppID: "app_x"}, old), ContainerOther, false},
	}
	for _, c := range cases {
		got := f.ClassifyContainer(c.c)
		if got.Category != c.cat || got.Removable != c.removable {
			t.Errorf("%s: got %s removable=%v, want %s removable=%v", c.c.Name, got.Category, got.Removable, c.cat, c.removable)
		}
	}
}

func TestClassifyVolumes(t *testing.T) {
	f := facts()
	vol := func(name string, labels map[string]string, ref int64) dockerx.InvVolume {
		return dockerx.InvVolume{Name: name, Labels: labels, RefCount: ref, Size: 100}
	}
	cases := []struct {
		v         dockerx.InvVolume
		cat       string
		removable bool
	}{
		{vol("bp-shop-data", mine("app_shop"), 1), VolumeApp, false},
		{vol("bp-shop-data", nil, 0), VolumeApp, false}, // known by name even without labels
		{vol("bp-blog_pg", mine("app_blog"), 0), VolumeApp, false},
		{vol("bp-gone-data", mine("app_gone"), 0), VolumeOrphan, true},
		{vol("bp-gone2-data", map[string]string{dockerx.LabelManaged: "1", dockerx.LabelAppID: "app_gone2"}, 0), VolumeOrphan, true},
		{vol("bp-gone3-data", mine("app_gone3"), 1), VolumeOrphan, false}, // still mounted
		{vol("bp-x-data", map[string]string{dockerx.LabelManaged: "1", dockerx.LabelInstance: "inst2", dockerx.LabelAppID: "app_x"}, 0), VolumeOther, false},
		{vol("buildx_buildkit_bitploy0_state", nil, 1), VolumeBuilder, false},
		{vol("pgdata", nil, 0), VolumeOther, false},
		{vol("bp-init-volume", mine(""), 0), VolumeOther, false},
	}
	for _, c := range cases {
		got := f.ClassifyVolume(c.v)
		if got.Category != c.cat || got.Removable != c.removable {
			t.Errorf("%s: got %s removable=%v, want %s removable=%v", c.v.Name, got.Category, got.Removable, c.cat, c.removable)
		}
	}
}

func TestTargets(t *testing.T) {
	f := facts()
	inv := dockerx.Inventory{
		Images: []dockerx.InvImage{
			img("sha256:shop11", []string{"bitploy/shop:11", "bitploy/shop:old"}, mine("app_shop"), old),
			img("sha256:shop12", []string{"bitploy/shop:12"}, mine("app_shop"), old),
			img("sha256:dang", nil, mine("app_shop"), old),
			img("sha256:nginx", []string{"nginx:1"}, nil, old),
		},
		Containers: []dockerx.InvContainer{
			{ID: "c1", Name: "bp-shop-12", State: "running", ImageID: "sha256:shop12", Labels: mine("app_shop"), Created: old},
			{ID: "c2", Name: "bp-shop-10", State: "exited", Labels: mine("app_shop"), Created: old, SizeRw: 77},
		},
		Volumes: []dockerx.InvVolume{
			{Name: "bp-shop-data", Labels: mine("app_shop"), RefCount: 1, Size: 5000},
			{Name: "bp-gone-data", Labels: mine("app_gone"), RefCount: 0, Size: 3000},
			{Name: "buildx_buildkit_bitploy0_state", RefCount: 1, Size: 9000},
		},
		BuildCacheBytes: 500,
		ImagesBytes:     3000,
	}
	r := f.Classify(inv)
	if got := r.Targets(RollbackImages); !reflect.DeepEqual(got.ImageRefs, []string{"bitploy/shop:11", "bitploy/shop:old"}) || got.Images != 1 || got.Bytes != 600 {
		t.Errorf("rollback targets = %+v", got)
	}
	if got := r.Targets(UnusedImages); !reflect.DeepEqual(got.ImageRefs, []string{"sha256:dang"}) {
		t.Errorf("unused targets = %+v", got)
	}
	if got := r.Targets(OtherImages); !reflect.DeepEqual(got.ImageRefs, []string{"nginx:1"}) {
		t.Errorf("other targets = %+v", got)
	}
	if got := r.Targets(StoppedContainers); !reflect.DeepEqual(got.Containers, []string{"c2"}) || got.Bytes != 77 {
		t.Errorf("container targets = %+v", got)
	}
	if got := r.Targets(OrphanVolumes); !reflect.DeepEqual(got.Volumes, []string{"bp-gone-data"}) || got.Bytes != 3000 {
		t.Errorf("volume targets = %+v", got)
	}
	if got := r.Targets(BuildCache); got.Bytes != 9500 {
		t.Errorf("build cache bytes = %d, want 9500", got.Bytes)
	}
	// 4 images of 600 unique bytes each: 2400; Docker counts 3000 in all.
	if r.SharedImageBytes != 600 {
		t.Errorf("shared = %d, want 600", r.SharedImageBytes)
	}
	// The automatic cleanup never picks volumes.
	for _, k := range AutoSteps {
		if k == OrphanVolumes {
			t.Fatal("AutoSteps holds orphan_volumes")
		}
	}
}

func TestWatchDebounce(t *testing.T) {
	var w Watch
	const thr = 0.10
	if w.Due(now, 0.12, thr) {
		t.Error("due above the threshold")
	}
	if !w.Due(now, 0.08, thr) {
		t.Error("not due below the threshold on the first run")
	}
	w.Ran(now, 0.085) // could not free enough
	if w.Due(now.Add(time.Minute), 0.085, thr) {
		t.Error("due again a minute later at the same free space")
	}
	if w.Due(now.Add(10*time.Minute), 0.08, thr) {
		t.Error("due again after a drop smaller than DropAgain")
	}
	if !w.Due(now.Add(10*time.Minute), 0.07, thr) {
		t.Error("not due after free space dropped further")
	}
	if !w.Due(now.Add(Debounce), 0.085, thr) {
		t.Error("not due after the debounce")
	}
}

func TestEscalateStopsWhenEnough(t *testing.T) {
	free := uint64(5)
	measure := func() (Space, error) { return Space{Total: 100, Free: free}, nil }
	var ran []string
	step := func(kind string, frees uint64, err error) Step {
		return Step{Kind: kind, Run: func(context.Context) (int64, int, error) {
			ran = append(ran, kind)
			free += frees
			return 0, 1, err
		}}
	}
	steps := []Step{step("a", 2, nil), step("b", 6, errors.New("partly")), step("c", 50, nil)}
	res, reached := Escalate(context.Background(), steps, measure, 0.12)
	if !reached || !reflect.DeepEqual(ran, []string{"a", "b"}) {
		t.Fatalf("reached=%v ran=%v, want true [a b]", reached, ran)
	}
	if res[0].FreedBytes != 2 || res[1].FreedBytes != 6 || res[1].Error != "partly" {
		t.Errorf("results = %+v", res)
	}

	// Already enough free: nothing runs.
	free, ran = 50, nil
	if res, reached := Escalate(context.Background(), steps, measure, 0.12); !reached || len(res) != 0 || len(ran) != 0 {
		t.Errorf("ran %v with enough free space", ran)
	}

	// Never enough: every step runs.
	free, ran = 0, nil
	steps = []Step{step("a", 1, nil), step("b", 1, nil)}
	if _, reached := Escalate(context.Background(), steps, measure, 0.5); reached || len(ran) != 2 {
		t.Errorf("reached=%v ran=%v", reached, ran)
	}

	// A cancelled context stops before the next step.
	ctx, cancel := context.WithCancel(context.Background())
	free, ran = 0, nil
	steps = []Step{{Kind: "a", Run: func(context.Context) (int64, int, error) { ran = append(ran, "a"); cancel(); return 0, 0, nil }}, step("b", 1, nil)}
	if _, reached := Escalate(ctx, steps, measure, 0.5); reached || !reflect.DeepEqual(ran, []string{"a"}) {
		t.Errorf("ran %v after cancel", ran)
	}
}

func TestFreed(t *testing.T) {
	if got := Freed(Space{Free: 100}, Space{Free: 300}, 50); got != 200 {
		t.Errorf("growth: %d", got)
	}
	if got := Freed(Space{Free: 100}, Space{Free: 90}, 0); got != 0 {
		t.Errorf("shrink: %d", got)
	}
	if got := Freed(Space{Free: 100}, Space{Free: 120}, 500); got != 500 {
		t.Errorf("reported more: %d", got)
	}
}

func TestStepOrder(t *testing.T) {
	// Cheapest first; data and other software's images last.
	want := []string{BuildCache, UnusedImages, StoppedContainers, RollbackImages, OtherImages}
	if !reflect.DeepEqual(AutoSteps, want) {
		t.Errorf("AutoSteps = %v", AutoSteps)
	}
	safe := append([]string(nil), SafeSteps...)
	sort.Strings(safe)
	if !reflect.DeepEqual(safe, []string{BuildCache, UnusedImages}) {
		t.Errorf("SafeSteps = %v", SafeSteps)
	}
}
