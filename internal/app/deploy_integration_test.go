//go:build integration

package app

// Integration test against the local Docker daemon: builds and runs a real
// static nginx app behind a real Traefik. Every Docker object carries the
// nl.bitploy.managed=1 label and a bp-test- name, and is removed afterwards.
//
//	go test -tags=integration -count=1 -run TestIntegrationDeploy ./internal/app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/AVMG20/bitploy/internal/config"
	"github.com/AVMG20/bitploy/internal/deploy"
	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/ids"
	"github.com/AVMG20/bitploy/internal/proxy"
	"github.com/AVMG20/bitploy/internal/store"
)

type itEnv struct {
	*depEnv
	dk       *dockerx.Client
	traefik  string
	webPort  string // host port of Traefik's "web" entrypoint
	slugs    []string
	suffix   string
	cancelWk context.CancelFunc
}

func itSetup(t *testing.T) *itEnv {
	t.Helper()
	ctx := context.Background()
	dk, err := dockerx.New(ctx)
	if err != nil {
		t.Skipf("no Docker daemon: %v", err)
	}
	t.Cleanup(func() { dk.Close() })
	if out, err := exec.Command("docker", "buildx", "version").CombinedOutput(); err != nil {
		t.Skipf("docker buildx is not available: %v %s", err, out)
	}
	for _, img := range []string{"traefik:v3", "nginxinc/nginx-unprivileged:alpine", "alpine:3.20"} {
		if out, err := exec.Command("docker", "pull", "-q", img).CombinedOutput(); err != nil {
			t.Skipf("cannot pull %s: %v %s", img, err, out)
		}
	}

	suffix := ids.Hex(5)
	dir, err := os.MkdirTemp("", "bp-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	traefikName := "bp-test-traefik-" + suffix
	cfg := &config.Config{
		PublicURL: "http://localhost:8080", AppsDomain: "localhost", AppsScheme: "http", AppsPort: "8000",
		DataDir: dir, Signup: "invite", MaxParallelBuilds: 2, TraefikContainer: traefikName,
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(cfg.RoutesDir(), 0o755)

	// A real Traefik: file provider on the routes dir, "web" and "internal" entrypoints published on random localhost ports.
	lo := netip.MustParseAddr("127.0.0.1")
	p8000, p8081 := network.MustParsePort("8000/tcp"), network.MustParsePort("8081/tcp")
	_, err = dk.API.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: traefikName,
		Config: &container.Config{
			Image: "traefik:v3", Labels: map[string]string{dockerx.LabelManaged: "1"},
			Cmd: []string{"--entrypoints.web.address=:8000", "--entrypoints.internal.address=:8081",
				"--providers.file.directory=/dynamic", "--providers.file.watch=true", "--log.level=ERROR"},
			ExposedPorts: network.PortSet{p8000: {}, p8081: {}},
		},
		HostConfig: &container.HostConfig{
			Mounts:       []mount.Mount{{Type: mount.TypeBind, Source: cfg.RoutesDir(), Target: "/dynamic", ReadOnly: true}},
			PortBindings: network.PortMap{p8000: {{HostIP: lo}}, p8081: {{HostIP: lo}}},
		},
	})
	if err != nil {
		t.Fatalf("create traefik: %v", err)
	}
	t.Cleanup(func() { _ = dk.RemoveContainer(context.Background(), traefikName) })
	if err := dk.StartContainer(ctx, traefikName); err != nil {
		t.Fatalf("start traefik: %v", err)
	}
	info, err := dk.InspectContainer(ctx, traefikName)
	if err != nil {
		t.Fatal(err)
	}
	pb := func(p network.Port) string {
		if b := info.NetworkSettings.Ports[p]; len(b) > 0 {
			return b[0].HostPort
		}
		t.Fatalf("no host port for %s", p)
		return ""
	}
	cfg.TraefikInternal = "http://127.0.0.1:" + pb(p8081)

	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	crypto, _ := store.NewCrypto([]byte("0123456789abcdef0123456789abcdef"))
	routes := &proxy.Writer{Dir: cfg.RoutesDir(), EntryPoint: "web"}
	// Each test database has its own instance id, so these tests never touch other bitploy resources on the daemon.
	if dk.Instance, err = st.InstanceID(ctx); err != nil {
		t.Fatal(err)
	}
	s := New(cfg, st, crypto, dk, routes, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e := &itEnv{depEnv: &depEnv{s: s, dir: dir}, dk: dk, traefik: traefikName, webPort: pb(p8000), suffix: suffix}
	e.user = e.addUser(t, "usr_it", "it@example.com", "user")
	e.exec(t, `UPDATE users SET quota_deploys_per_hour = 1000, quota_apps = 20, quota_memory_mb = 8192, quota_cpu_millis = 8000 WHERE id = 'usr_it'`)

	healthTimeout, workerAliveFor, oldStopDelay = 12*time.Second, 3*time.Second, time.Second
	t.Cleanup(func() { healthTimeout, workerAliveFor, oldStopDelay = 60*time.Second, 30*time.Second, 10*time.Second })

	wctx, cancel := context.WithCancel(ctx)
	e.cancelWk = cancel
	go s.RunWorker(wctx)
	t.Cleanup(func() {
		cancel()
		time.Sleep(300 * time.Millisecond)
		for _, slug := range e.slugs { // anything a failed test left behind
			if app, err := s.Q.GetAppBySlug(ctx, slug); err == nil {
				_ = s.DeleteApp(ctx, e.user, app.Slug, app.Slug)
			}
		}
		e.assertNoLeftovers(t)
	})
	return e
}

func (e *itEnv) newApp(t *testing.T, in CreateAppInput) App {
	t.Helper()
	in.Slug = "test-" + e.suffix + "-" + ids.Hex(3)
	app := e.mustApp(t, in)
	e.slugs = append(e.slugs, app.Slug)
	return app
}

func (e *itEnv) assertNoLeftovers(t *testing.T) {
	ctx := context.Background()
	cs, _ := e.dk.ListManagedContainers(ctx)
	for _, c := range cs {
		if n := dockerx.SummaryName(c); strings.Contains(n, e.suffix) && n != e.traefik {
			t.Errorf("leftover container %s", n)
			_ = e.dk.RemoveContainer(ctx, n)
		}
	}
	ns, _ := e.dk.ListManagedNetworks(ctx)
	for _, n := range ns {
		if strings.Contains(n.Name, e.suffix) {
			t.Errorf("leftover network %s", n.Name)
			_ = e.dk.DisconnectNetwork(ctx, n.Name, e.traefik)
			_ = e.dk.RemoveNetwork(ctx, n.Name)
		}
	}
	vs, _ := e.dk.ListManagedVolumes(ctx)
	for _, v := range vs {
		if strings.Contains(v.Name, e.suffix) {
			t.Errorf("leftover volume %s", v.Name)
			_ = e.dk.RemoveVolume(ctx, v.Name)
		}
	}
	ims, _ := e.dk.ListManagedImages(ctx)
	for _, im := range ims {
		for _, tag := range im.Tags {
			if strings.Contains(tag, e.suffix) {
				t.Errorf("leftover image %s", tag)
				_ = e.dk.RemoveImage(ctx, tag)
			}
		}
	}
}

// get fetches path from the app through Traefik's public entrypoint.
func (e *itEnv) get(slug, path string) (int, string) {
	req, _ := http.NewRequest("GET", "http://127.0.0.1:"+e.webPort+path, nil)
	req.Host = slug + ".localhost"
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, string(b)
}

func (e *itEnv) waitServing(t *testing.T, slug, want string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var code int
	var body string
	for time.Now().Before(deadline) {
		code, body = e.get(slug, "/")
		if code == 200 && strings.Contains(body, want) {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("%s never served %q: last %d %.200s", slug, want, code, body)
}

func (e *itEnv) wait(t *testing.T, id string) Deployment {
	t.Helper()
	deadline := time.Now().Add(4 * time.Minute)
	for time.Now().Before(deadline) {
		d, err := e.s.GetDeployment(context.Background(), e.user, id, 20*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if d.Terminal() {
			return d
		}
	}
	t.Fatalf("deployment %s did not finish", id)
	return Deployment{}
}

func (e *itEnv) deployStatic(t *testing.T, slug, text string) Deployment {
	t.Helper()
	d, err := e.s.DeployFiles(context.Background(), e.user, slug, []FileInput{
		{Path: "Dockerfile", Content: "FROM nginxinc/nginx-unprivileged:alpine\nCOPY index.html /usr/share/nginx/html/index.html\nEXPOSE 8080\n"},
		{Path: "index.html", Content: "<h1>" + text + "</h1>"},
	})
	if err != nil {
		t.Fatalf("DeployFiles: %v", err)
	}
	return d
}

func TestIntegrationDeploy(t *testing.T) {
	e := itSetup(t)
	ctx := context.Background()
	s := e.s
	app := e.newApp(t, CreateAppInput{Name: "Integration"})
	slug := app.Slug

	// 1. A static site goes live.
	d1 := e.wait(t, e.deployStatic(t, slug, "version one").ID)
	if d1.State != "running" {
		t.Fatalf("first deployment: %s %+v\n%v", d1.State, d1.Error, d1.LogTail)
	}
	e.waitServing(t, slug, "version one")
	fresh, _ := s.GetApp(ctx, e.user, slug)
	if fresh.Status != "running" || fresh.Current == nil || fresh.Current.ID != d1.ID || fresh.StartedAt == nil {
		t.Fatalf("app after deploy: %+v", fresh)
	}
	if len(d1.LogTail) == 0 || d1.Image == nil || *d1.Image != "bitploy/"+slug+":1" {
		t.Errorf("deployment: %+v", d1)
	}

	// Container hardening from SPEC "Container defaults".
	c, err := e.dk.InspectContainer(ctx, "bp-"+slug+"-1")
	if err != nil {
		t.Fatal(err)
	}
	hc := c.HostConfig
	if hc.Memory != 512<<20 || hc.MemorySwap != 512<<20 || hc.NanoCPUs != 500_000_000 || hc.PidsLimit == nil || *hc.PidsLimit != 256 {
		t.Errorf("resources: mem=%d swap=%d cpus=%d pids=%v", hc.Memory, hc.MemorySwap, hc.NanoCPUs, hc.PidsLimit)
	}
	if hc.Privileged || len(hc.Binds) != 0 || len(hc.Mounts) != 0 || len(hc.PortBindings) != 0 {
		t.Errorf("host config too open: %+v", hc)
	}
	if !contains(hc.SecurityOpt, "no-new-privileges") || !contains(hc.CapDrop, "ALL") || (!contains(hc.CapAdd, "NET_BIND_SERVICE") && !contains(hc.CapAdd, "CAP_NET_BIND_SERVICE")) {
		t.Errorf("security: opt=%v capdrop=%v", hc.SecurityOpt, hc.CapDrop)
	}
	if hc.RestartPolicy.Name != container.RestartPolicyUnlessStopped || hc.LogConfig.Type != "json-file" || hc.LogConfig.Config["max-size"] != "10m" || hc.LogConfig.Config["max-file"] != "3" {
		t.Errorf("restart/log: %+v %+v", hc.RestartPolicy, hc.LogConfig)
	}
	if len(hc.Ulimits) != 1 || hc.Ulimits[0].Name != "nofile" || hc.Ulimits[0].Soft != 4096 {
		t.Errorf("ulimits: %+v", hc.Ulimits)
	}
	env := strings.Join(c.Config.Env, " ")
	for _, w := range []string{"PORT=8080", "BITPLOY_APP=" + slug, "BITPLOY_URL=http://" + slug + ".localhost:8000"} {
		if !strings.Contains(env, w) {
			t.Errorf("env lacks %s: %s", w, env)
		}
	}
	if c.Config.Labels[dockerx.LabelManaged] != "1" || c.Config.Labels[dockerx.LabelAppID] != app.ID {
		t.Errorf("labels: %v", c.Config.Labels)
	}
	if _, ok := c.NetworkSettings.Networks["bp-"+slug]; !ok || len(c.NetworkSettings.Networks) != 1 {
		t.Errorf("networks: %v", c.NetworkSettings.Networks)
	}

	// 2. A broken build leaves the working version serving.
	bad, err := s.DeployFiles(ctx, e.user, slug, []FileInput{
		{Path: "Dockerfile", Content: "FROM alpine:3.20\nRUN echo compiling && echo 'oh no: missing file' >&2 && exit 3\nEXPOSE 8080\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	bad = e.wait(t, bad.ID)
	if bad.State != "failed" || bad.Error == nil || bad.Error.Code != "build_failed" || bad.Error.Hint == "" {
		t.Fatalf("broken build: %s %+v", bad.State, bad.Error)
	}
	if !strings.Contains(strings.Join(bad.LogTail, "\n"), "oh no: missing file") {
		t.Errorf("log tail lacks the build output: %v", bad.LogTail)
	}
	if code, body := e.get(slug, "/"); code != 200 || !strings.Contains(body, "version one") {
		t.Errorf("old version must keep serving after a failed build: %d %.100s", code, body)
	}

	// 3. An app that listens on localhost fails the health check with the exact hint.
	local, err := s.DeployFiles(ctx, e.user, slug, []FileInput{
		{Path: "Dockerfile", Content: "FROM alpine:3.20\nEXPOSE 8080\nCMD [\"sh\", \"-c\", \"echo 'Listening on 127.0.0.1:8080'; sleep 600\"]\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	local = e.wait(t, local.ID)
	if local.State != "failed" || local.Error == nil || local.Error.Code != "unhealthy" || local.Error.Hint != deploy.LocalhostHint {
		t.Fatalf("localhost app: %s %+v", local.State, local.Error)
	}
	if !strings.Contains(strings.Join(local.LogTail, "\n"), "Listening on 127.0.0.1") {
		t.Errorf("runtime log tail missing: %v", local.LogTail)
	}
	if code, body := e.get(slug, "/"); code != 200 || !strings.Contains(body, "version one") {
		t.Errorf("old version must keep serving after an unhealthy deploy: %d %.100s", code, body)
	}
	if ok, _ := e.dk.ContainerExists(ctx, "bp-"+slug+"-3"); ok {
		t.Error("the failed deployment's container must be removed")
	}
	if rb, _ := s.Routes.Read(slug); strings.Contains(string(rb), "candidate") {
		t.Errorf("candidate route must be gone after a failure:\n%s", rb)
	}

	// 4. A new version swaps in; the old container goes away; the old deployment is superseded.
	d4 := e.wait(t, e.deployStatic(t, slug, "version two").ID)
	if d4.State != "running" {
		t.Fatalf("v2: %s %+v %v", d4.State, d4.Error, d4.LogTail)
	}
	e.waitServing(t, slug, "version two")
	if old, _ := s.GetDeployment(ctx, e.user, d1.ID, 0); old.State != "superseded" {
		t.Errorf("previous deployment is %s", old.State)
	}
	waitFor(t, 20*time.Second, "old container removed", func() bool {
		ok, _ := e.dk.ContainerExists(ctx, "bp-"+slug+"-1")
		return !ok
	})

	// 5. Rollback to v1 reuses the image and starts from `starting`.
	rb, err := s.Rollback(ctx, e.user, slug, d1.ID)
	if err != nil {
		t.Fatal(err)
	}
	rb = e.wait(t, rb.ID)
	if rb.State != "running" || rb.SourceKind != "rollback" {
		t.Fatalf("rollback: %s %+v", rb.State, rb.Error)
	}
	e.waitServing(t, slug, "version one")

	// 6. Env vars apply on restart; values are never returned.
	if _, err := s.SetEnv(ctx, e.user, slug, map[string]string{"GREETING": "hello"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestartApp(ctx, e.user, slug); err != nil {
		t.Fatal(err)
	}
	e.waitServing(t, slug, "version one")
	cur, _ := s.GetApp(ctx, e.user, slug)
	ci, _ := e.dk.InspectContainer(ctx, cur.Current.toContainer(slug))
	if !strings.Contains(strings.Join(ci.Config.Env, " "), "GREETING=hello") {
		t.Errorf("env var missing after restart: %v", ci.Config.Env)
	}

	// 7. Runtime logs through the Docker API.
	waitFor(t, 15*time.Second, "runtime logs", func() bool {
		lines, err := s.Logs(ctx, e.user, slug, LogOptions{Kind: "runtime", Tail: 50})
		return err == nil && len(lines) > 0
	})

	// 8. Stop removes the route; start brings the app back.
	if _, err := s.StopApp(ctx, e.user, slug); err != nil {
		t.Fatal(err)
	}
	if b, _ := s.Routes.Read(slug); b != nil {
		t.Error("route file must be gone when stopped")
	}
	waitFor(t, 20*time.Second, "container stopped", func() bool {
		ci, err := e.dk.InspectContainer(ctx, cur.Current.toContainer(slug))
		return err == nil && ci.State != nil && !ci.State.Running
	})
	waitFor(t, 20*time.Second, "route gone in traefik", func() bool { code, _ := e.get(slug, "/"); return code != 200 })
	if _, err := s.StartApp(ctx, e.user, slug); err != nil {
		t.Fatal(err)
	}
	e.waitServing(t, slug, "version one")

	// 9. The reconciler restores a container that vanished.
	if err := e.dk.RemoveContainer(ctx, cur.Current.toContainer(slug)); err != nil {
		t.Fatal(err)
	}
	s.Reconcile(ctx)
	e.waitServing(t, slug, "version one")
	// ... and removes a stray managed container of this app.
	stray := "bp-" + slug + "-99"
	if _, err := e.dk.CreateContainer(ctx, dockerx.RunSpec{
		Name: stray, Image: "alpine:3.20", Network: "bp-" + slug, MemoryMB: 64, CPUMillis: 100,
		Labels: map[string]string{dockerx.LabelManaged: "1", dockerx.LabelAppID: app.ID, dockerx.LabelDeployment: "dep_stray"},
	}); err != nil {
		t.Fatal(err)
	}
	s.Reconcile(ctx)
	if ok, _ := e.dk.ContainerExists(ctx, stray); ok {
		t.Error("reconciler must remove containers that are not current")
	}
	// Route files that do not match the database are rewritten.
	good, _ := s.Routes.Read(slug)
	_ = os.WriteFile(s.Cfg.RoutesDir()+"/"+slug+".yml", []byte("http: {}\n"), 0o644)
	s.Reconcile(ctx)
	if again, _ := s.Routes.Read(slug); string(again) != string(good) {
		t.Errorf("route not repaired:\n%s", again)
	}

	// 9b. Deploying a stopped app brings it back up with the new version.
	if _, err := s.StopApp(ctx, e.user, slug); err != nil {
		t.Fatal(err)
	}
	d6 := e.wait(t, e.deployStatic(t, slug, "version three").ID)
	if d6.State != "running" {
		t.Fatalf("deploy of a stopped app: %s %+v", d6.State, d6.Error)
	}
	e.waitServing(t, slug, "version three")
	if again, _ := s.GetApp(ctx, e.user, slug); again.DesiredState != "running" || again.Status != "running" {
		t.Errorf("app after deploying while stopped: %s/%s", again.DesiredState, again.Status)
	}

	// 10. Deleting removes every trace.
	if err := s.DeleteApp(ctx, e.user, slug, slug); err != nil {
		t.Fatal(err)
	}
	e.assertNoLeftovers(t)
}

func (d *Deployment) toContainer(slug string) string { return fmt.Sprintf("bp-%s-%d", slug, d.Seq) }

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, d time.Duration, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// A worker has no route and counts as healthy when it is still running after
// the alive window; one that exits is unhealthy.
func TestIntegrationWorker(t *testing.T) {
	e := itSetup(t)
	ctx := context.Background()
	w := e.newApp(t, CreateAppInput{Type: "worker"})
	d, err := e.s.DeployFiles(ctx, e.user, w.Slug, []FileInput{{Path: "Dockerfile", Content: "FROM alpine:3.20\nCMD [\"sh\", \"-c\", \"while true; do echo tick; sleep 1; done\"]\n"}})
	if err != nil {
		t.Fatal(err)
	}
	if d = e.wait(t, d.ID); d.State != "running" {
		t.Fatalf("%s %+v %v", d.State, d.Error, d.LogTail)
	}
	if b, _ := e.s.Routes.Read(w.Slug); b != nil {
		t.Error("worker must not get a route")
	}
	waitFor(t, 10*time.Second, "worker logs", func() bool {
		lines, err := e.s.Logs(ctx, e.user, w.Slug, LogOptions{Kind: "runtime"})
		return err == nil && len(lines) > 0 && lines[0].Stream == "stdout"
	})
	crash, _ := e.s.DeployFiles(ctx, e.user, w.Slug, []FileInput{{Path: "Dockerfile", Content: "FROM alpine:3.20\nCMD [\"sh\", \"-c\", \"echo crashing; exit 1\"]\n"}})
	crash = e.wait(t, crash.ID)
	if crash.State != "failed" || crash.Error == nil || crash.Error.Code != "app_crashed" || !strings.Contains(strings.Join(crash.LogTail, " "), "crashing") {
		t.Fatalf("crashing worker: %s %+v %v", crash.State, crash.Error, crash.LogTail)
	}
	if cur, _ := e.s.GetApp(ctx, e.user, w.Slug); cur.Current == nil || cur.Current.ID != d.ID {
		t.Errorf("the working worker must stay current: %+v", cur.Current)
	}
	if err := e.s.DeleteApp(ctx, e.user, w.Slug, w.Slug); err != nil {
		t.Fatal(err)
	}
}

// With a volume the old container stops before the new one starts, and the
// old one comes back when the new one fails.
func TestIntegrationVolumeRecreate(t *testing.T) {
	e := itSetup(t)
	ctx := context.Background()
	app := e.newApp(t, CreateAppInput{Volume: true})
	d1 := e.wait(t, e.deployStatic(t, app.Slug, "volume one").ID)
	if d1.State != "running" {
		t.Fatalf("%s %+v", d1.State, d1.Error)
	}
	e.waitServing(t, app.Slug, "volume one")
	if ok, _ := e.dk.VolumeExists(ctx, dockerx.VolumeName(app.Slug)); !ok {
		t.Fatal("volume missing")
	}
	c, _ := e.dk.InspectContainer(ctx, "bp-"+app.Slug+"-1")
	if len(c.Mounts) != 1 || c.Mounts[0].Destination != "/data" {
		t.Errorf("mounts: %+v", c.Mounts)
	}
	// A new volume must be writable by a non-root user (apps run with USER).
	if out, err := exec.Command("docker", "run", "--rm", "-u", "1000:1000", "-v", dockerx.VolumeName(app.Slug)+":/data",
		dockerx.VolumeInitImage, "sh", "-c", "touch /data/probe && ls -ld /data").CombinedOutput(); err != nil || !strings.Contains(string(out), "drwxrwxrwt") {
		t.Errorf("volume not writable for uid 1000: %v %s", err, out)
	}
	if cs, _ := e.dk.ListManagedContainers(ctx); true {
		for _, c := range cs {
			if strings.HasPrefix(dockerx.SummaryName(c), "bp-init-") && strings.Contains(dockerx.SummaryName(c), app.Slug) {
				t.Errorf("init container left behind: %s", dockerx.SummaryName(c))
			}
		}
	}

	// A failing new version: the old container is restarted automatically.
	bad, _ := e.s.DeployFiles(ctx, e.user, app.Slug, []FileInput{{Path: "Dockerfile", Content: "FROM alpine:3.20\nEXPOSE 8080\nCMD [\"sh\", \"-c\", \"exit 1\"]\n"}})
	bad = e.wait(t, bad.ID)
	if bad.State != "failed" {
		t.Fatalf("%s", bad.State)
	}
	e.waitServing(t, app.Slug, "volume one")
	ci, _ := e.dk.InspectContainer(ctx, "bp-"+app.Slug+"-1")
	if ci.State == nil || !ci.State.Running {
		t.Error("the old container must run again after a failed recreate")
	}

	// A good new version replaces it; never two containers at once.
	d3 := e.wait(t, e.deployStatic(t, app.Slug, "volume two").ID)
	if d3.State != "running" {
		t.Fatalf("%s %+v", d3.State, d3.Error)
	}
	e.waitServing(t, app.Slug, "volume two")
	waitFor(t, 20*time.Second, "old container removed", func() bool {
		ok, _ := e.dk.ContainerExists(ctx, "bp-"+app.Slug+"-1")
		return !ok
	})
	if err := e.s.DeleteApp(ctx, e.user, app.Slug, app.Slug); err != nil {
		t.Fatal(err)
	}
}

// Turning /data off needs a confirmation when there are files, then removes the
// volume, and the app runs on without it.
func TestIntegrationVolumeOff(t *testing.T) {
	e := itSetup(t)
	ctx := context.Background()
	app := e.newApp(t, CreateAppInput{Volume: true})
	d1 := e.wait(t, e.deployStatic(t, app.Slug, "keep going").ID)
	if d1.State != "running" {
		t.Fatalf("%s %+v", d1.State, d1.Error)
	}
	vol := dockerx.VolumeName(app.Slug)
	// A new volume is empty: off is allowed at once, and so is on again.
	off, yes := false, true
	if got, err := e.s.UpdateApp(ctx, e.user, app.Slug, UpdateAppInput{Volume: &off}); err != nil || got.Volume {
		t.Fatalf("empty volume off: %+v %v", got, err)
	}
	if ok, _ := e.dk.VolumeExists(ctx, vol); ok {
		t.Error("the volume of an empty /data must be removed as well")
	}
	e.waitServing(t, app.Slug, "keep going")
	if _, err := e.s.UpdateApp(ctx, e.user, app.Slug, UpdateAppInput{Volume: &yes}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.RestartApp(ctx, e.user, app.Slug); err != nil {
		t.Fatal(err)
	}
	e.waitServing(t, app.Slug, "keep going")
	c, _ := e.dk.InspectContainer(ctx, "bp-"+app.Slug+"-1")
	if len(c.Mounts) != 1 {
		t.Fatalf("mounts after turning /data on: %+v", c.Mounts)
	}

	// A file in /data, written behind the hourly measurement's back.
	if out, err := exec.Command("docker", "run", "--rm", "-v", vol+":/data", dockerx.VolumeInitImage, "sh", "-c", "echo hi > /data/note.txt").CombinedOutput(); err != nil {
		t.Fatalf("write: %v %s", err, out)
	}
	if _, err := e.s.UpdateApp(ctx, e.user, app.Slug, UpdateAppInput{Volume: &off}); depCode(err) != "volume_has_data" {
		t.Fatalf("with a file: %v", err)
	}
	if ok, _ := e.dk.VolumeExists(ctx, vol); !ok {
		t.Fatal("the refusal removed the volume")
	}
	if got, err := e.s.UpdateApp(ctx, e.user, app.Slug, UpdateAppInput{Volume: &off, DeleteData: &yes}); err != nil || got.Volume {
		t.Fatalf("confirmed: %+v %v", got, err)
	}
	if ok, _ := e.dk.VolumeExists(ctx, vol); ok {
		t.Error("the volume is still there")
	}
	e.waitServing(t, app.Slug, "keep going")
	c, _ = e.dk.InspectContainer(ctx, "bp-"+app.Slug+"-1")
	if len(c.Mounts) != 0 || c.State == nil || !c.State.Running {
		t.Errorf("the app must run without the volume: mounts %+v", c.Mounts)
	}
	if err := e.s.DeleteApp(ctx, e.user, app.Slug, app.Slug); err != nil {
		t.Fatal(err)
	}
}

// A static site runs with the fixed small limits and is free of the allowance;
// deploying the user's own Dockerfile turns it into an app with the defaults.
func TestIntegrationStaticSite(t *testing.T) {
	e := itSetup(t)
	ctx := context.Background()
	app := e.newApp(t, CreateAppInput{BuildMode: "auto"})
	d, err := e.s.DeployFiles(ctx, e.user, app.Slug, []FileInput{{Path: "index.html", Content: "<h1>plain site</h1>"}})
	if err != nil {
		t.Fatal(err)
	}
	if d = e.wait(t, d.ID); d.State != "running" {
		t.Fatalf("%s %+v", d.State, d.Error)
	}
	e.waitServing(t, app.Slug, "plain site")
	got, _ := e.s.GetApp(ctx, e.user, app.Slug)
	if !got.Static || got.MemoryMB != staticMemoryMB || got.CPUMillis != staticCPUMillis {
		t.Errorf("static site: %+v", got)
	}
	c, _ := e.dk.InspectContainer(ctx, "bp-"+app.Slug+"-1")
	if c.HostConfig == nil || c.HostConfig.Memory != staticMemoryMB<<20 || c.HostConfig.NanoCPUs != staticCPUMillis*1_000_000 {
		t.Errorf("container limits: %+v", c.HostConfig)
	}
	if u, _ := e.s.Usage(ctx, e.user.UserID); u.MemoryReservedMB != 0 || u.CPUReservedMillis != 0 {
		t.Errorf("usage: %+v", u)
	}

	// The user's own Dockerfile, even one that starts from nginx, is an app.
	d2 := e.wait(t, e.deployStatic(t, app.Slug, "own dockerfile").ID)
	if d2.State != "running" {
		t.Fatalf("%s %+v", d2.State, d2.Error)
	}
	e.waitServing(t, app.Slug, "own dockerfile")
	got, _ = e.s.GetApp(ctx, e.user, app.Slug)
	if got.Static || got.MemoryMB != defaultMemoryMB || got.CPUMillis != defaultCPUMillis {
		t.Errorf("app: %+v", got)
	}
	c, _ = e.dk.InspectContainer(ctx, "bp-"+app.Slug+"-2")
	if c.HostConfig == nil || c.HostConfig.Memory != defaultMemoryMB<<20 {
		t.Errorf("container limits: %+v", c.HostConfig)
	}
	if u, _ := e.s.Usage(ctx, e.user.UserID); u.MemoryReservedMB != defaultMemoryMB {
		t.Errorf("usage: %+v", u)
	}
	if err := e.s.DeleteApp(ctx, e.user, app.Slug, app.Slug); err != nil {
		t.Fatal(err)
	}
}

// Killing bitploy in the middle of a build leaves nothing behind after restart.
func TestIntegrationKilledMidBuild(t *testing.T) {
	e := itSetup(t)
	ctx := context.Background()
	app := e.newApp(t, CreateAppInput{})
	d, err := e.s.DeployFiles(ctx, e.user, app.Slug, []FileInput{{Path: "Dockerfile", Content: "FROM alpine:3.20\nRUN sleep 120\nEXPOSE 8080\n"}})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 60*time.Second, "building", func() bool {
		got, _ := e.s.GetDeployment(ctx, e.user, d.ID, 0)
		return got.State == "building"
	})
	// "kill -9": the worker context ends, the process state in memory is lost.
	e.cancelWk()
	time.Sleep(500 * time.Millisecond)
	e.s.deployRT.mu.Lock()
	e.s.deployRT.busy, e.s.deployRT.cancels = map[string]string{}, map[string]context.CancelFunc{}
	e.s.deployRT.mu.Unlock()

	e.s.Reconcile(ctx) // what RunReconciler does on startup
	got, _ := e.s.GetDeployment(ctx, e.user, d.ID, 0)
	if got.State != "failed" || got.Error == nil || got.Error.Code != "interrupted" {
		t.Fatalf("after restart: %s %+v", got.State, got.Error)
	}
	if _, err := os.Stat(e.s.Cfg.BuildDir(d.ID)); !os.IsNotExist(err) {
		t.Error("build dir left behind")
	}
	cs, _ := e.dk.ListAppContainers(ctx, app.ID)
	if len(cs) != 0 {
		t.Errorf("containers left behind: %d", len(cs))
	}
	if st, _ := s2(e.s.GetApp(ctx, e.user, app.Slug)); st.Status != "failed" {
		t.Errorf("app status %q", st.Status)
	}
	if err := e.s.DeleteApp(ctx, e.user, app.Slug, app.Slug); err != nil {
		t.Fatal(err)
	}
}

// Cancelling a slow build stops it for real: the deployment ends as cancelled,
// the docker build process and the step BuildKit runs for it are gone, the
// build slot is free and nothing is left behind.
func TestIntegrationCancelMidBuild(t *testing.T) {
	e := itSetup(t)
	ctx := context.Background()
	app := e.newApp(t, CreateAppInput{})
	const marker = "sleep 117" // a number nothing else on the machine sleeps for
	d, err := e.s.DeployFiles(ctx, e.user, app.Slug, []FileInput{{Path: "Dockerfile", Content: "FROM alpine:3.20\nRUN " + marker + "\nEXPOSE 8080\n"}})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 90*time.Second, "building", func() bool {
		got, _ := e.s.GetDeployment(ctx, e.user, d.ID, 0)
		return got.State == "building"
	})
	tag := "bitploy/" + app.Slug + ":" // in the arguments of the docker build process
	hostHas := func() bool {
		out, _ := exec.Command("pgrep", "-f", tag).Output()
		return strings.TrimSpace(string(out)) != ""
	}
	builderRuns := func() bool {
		out, _ := exec.Command("docker", "top", deploy.BuilderContainer).CombinedOutput()
		return strings.Contains(string(out), marker)
	}
	waitFor(t, 60*time.Second, "the build step to run", func() bool { return hostHas() && builderRuns() })

	start := time.Now()
	got, err := e.s.CancelDeployment(ctx, e.user, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "cancelled" || got.Error == nil || got.Error.Code != "cancelled" {
		t.Fatalf("after cancel: %s %+v (%v)", got.State, got.Error, time.Since(start))
	}
	t.Logf("cancelled in %v", time.Since(start))
	if hostHas() {
		t.Error("the docker build process is still running")
	}
	waitFor(t, 30*time.Second, "BuildKit to stop the step", func() bool { return !builderRuns() })
	if len(e.s.buildSlots()) != 0 || e.s.appBusy(app.ID) {
		t.Error("slot or busy flag left")
	}
	if _, err := os.Stat(e.s.Cfg.BuildDir(d.ID)); !os.IsNotExist(err) {
		t.Error("build dir left behind")
	}
	cs, _ := e.dk.ListAppContainers(ctx, app.ID)
	if len(cs) != 0 {
		t.Errorf("containers left behind: %d", len(cs))
	}
	if st, _ := s2(e.s.GetApp(ctx, e.user, app.Slug)); st.Status != "new" {
		t.Errorf("app status %q", st.Status)
	}
	// The app can deploy again at once.
	d2 := e.wait(t, e.deployStatic(t, app.Slug, "after the cancel").ID)
	if d2.State != "running" {
		t.Errorf("next deployment: %s", d2.State)
	}
	if err := e.s.DeleteApp(ctx, e.user, app.Slug, app.Slug); err != nil {
		t.Fatal(err)
	}
}

func s2(a App, err error) (App, error) { return a, err }
