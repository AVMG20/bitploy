//go:build integration

package app

// Compose apps end to end, on a real Docker daemon:
//
//	go test -tags=integration -count=1 -run TestIntegrationCompose ./internal/app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/dockerx"
)

// webDockerfile serves /data with nginx and writes text into it once, so a
// kept volume shows the text of the deployment that made it.
func webDockerfile(text string) string {
	return "FROM nginxinc/nginx-unprivileged:alpine\n" +
		"USER root\n" +
		"RUN mkdir /data && chown nginx /data && sed -i 's#/usr/share/nginx/html#/data#' /etc/nginx/conf.d/default.conf\n" +
		"USER nginx\n" +
		"EXPOSE 8080\n" +
		"CMD [\"sh\", \"-c\", \"[ -f /data/index.html ] || echo '" + text + "' > /data/index.html; exec nginx -g 'daemon off;'\"]\n"
}

// Two services: web is built from the project and public, worker is a
// ready-made image that reaches web by its service name and keeps a volume.
const composeTwo = `services:
  web:
    build: ./web
    ports: ["8080:8080"]
    deploy: {resources: {limits: {memory: 128M}}}
  worker:
    image: alpine:3.20
    command: ["sh", "-c", "until wget -q -O /dev/null http://web:8080/; do sleep 1; done; echo reached-web; sleep 3600"]
    depends_on: [web]
    volumes: [cache:/cache]
    deploy: {resources: {limits: {memory: 64M}}}
volumes:
  cache:
    x-bitploy: {size_mb: 100}
`

func TestIntegrationComposeBuildAndImage(t *testing.T) {
	e := itSetup(t)
	ctx := context.Background()
	ap := e.newApp(t, CreateAppInput{BuildMode: "compose"})
	slug := ap.Slug
	files := func(text string) []FileInput {
		return []FileInput{{Path: "compose.yaml", Content: composeTwo}, {Path: "web/Dockerfile", Content: webDockerfile(text)}}
	}

	d, err := e.s.DeployFiles(ctx, e.user, slug, files("compose one"))
	if err != nil {
		t.Fatal(err)
	}
	if d = e.wait(t, d.ID); d.State != "running" {
		t.Fatalf("%s %+v %v", d.State, d.Error, d.LogTail)
	}
	e.waitServing(t, slug, "compose one")
	webName, workerName := dockerx.ServiceContainerName(slug, "web", d.Seq), dockerx.ServiceContainerName(slug, "worker", d.Seq)
	for svc, name := range map[string]string{"web": webName, "worker": workerName} {
		info, err := e.dk.InspectContainer(ctx, name)
		if err != nil || info.State == nil || !info.State.Running {
			t.Fatalf("%s: %v", name, err)
		}
		if got := info.Config.Labels[dockerx.LabelService]; got != svc {
			t.Errorf("%s service label = %q", name, got)
		}
	}
	if info, _ := e.dk.InspectContainer(ctx, workerName); len(info.Mounts) != 1 || info.Mounts[0].Name != dockerx.ServiceVolumeName(slug, "cache") {
		t.Errorf("worker mounts = %+v", info.Mounts)
	}
	waitFor(t, 30*time.Second, "worker reaching web", func() bool {
		lines, _ := e.s.Logs(ctx, e.user, slug, LogOptions{Kind: "runtime", Service: "worker"})
		for _, l := range lines {
			if strings.Contains(l.Text, "reached-web") {
				return true
			}
		}
		return false
	})
	got, err := e.s.GetApp(ctx, e.user, slug)
	if err != nil || len(got.Services) != 2 || len(got.Volumes) != 1 || got.MemoryMB != 192 {
		t.Fatalf("get app: %+v %v", got, err)
	}
	if l, err := e.s.ListBackups(ctx, e.user, slug); err != nil || len(l.Volumes) != 1 || l.Volumes[0].Service != "worker" {
		t.Errorf("backup volumes: %+v %v", l.Volumes, err)
	}

	// A new web build: web is replaced, the unchanged worker keeps running.
	d2, err := e.s.DeployFiles(ctx, e.user, slug, files("compose two"))
	if err != nil {
		t.Fatal(err)
	}
	if d2 = e.wait(t, d2.ID); d2.State != "running" {
		t.Fatalf("%s %+v %v", d2.State, d2.Error, d2.LogTail)
	}
	row, _ := e.s.Q.GetApp(ctx, ap.ID)
	cur := e.s.currentContainers(ctx, row)
	if cur["worker"] != workerName || cur["web"] != dockerx.ServiceContainerName(slug, "web", d2.Seq) {
		t.Errorf("current = %v", cur)
	}
	// web has no volume, so its /data is new: the new text shows.
	e.waitServing(t, slug, "compose two")

	// A broken worker image fails the deployment and keeps the old version.
	bad := strings.Replace(composeTwo, "alpine:3.20", "alpine:does-not-exist-bitploy", 1)
	d3, err := e.s.DeployFiles(ctx, e.user, slug, []FileInput{{Path: "compose.yaml", Content: bad}, {Path: "web/Dockerfile", Content: webDockerfile("compose two")}})
	if err != nil {
		t.Fatal(err)
	}
	if d3 = e.wait(t, d3.ID); d3.State != "failed" || d3.Error == nil || d3.Error.Code != "image_pull_failed" {
		t.Fatalf("broken image: %s %+v", d3.State, d3.Error)
	}
	e.waitServing(t, slug, "compose two")

	if err := e.s.DeleteApp(ctx, e.user, slug, slug); err != nil {
		t.Fatal(err)
	}
}

// An app with one Dockerfile and /data becomes a compose app: its files in
// /data move with it to the web service.
func TestIntegrationComposeFromSingle(t *testing.T) {
	e := itSetup(t)
	ctx := context.Background()
	ap := e.newApp(t, CreateAppInput{Volume: true})
	slug := ap.Slug
	d, err := e.s.DeployFiles(ctx, e.user, slug, []FileInput{{Path: "Dockerfile", Content: webDockerfile("single files")}})
	if err != nil {
		t.Fatal(err)
	}
	if d = e.wait(t, d.ID); d.State != "running" {
		t.Fatalf("%s %+v %v", d.State, d.Error, d.LogTail)
	}
	e.waitServing(t, slug, "single files")
	row, _ := e.s.Q.GetApp(ctx, ap.ID)
	oldVolume := appVolumeName(row)

	mode := "compose"
	if _, err := e.s.UpdateApp(ctx, e.user, slug, UpdateAppInput{BuildMode: &mode}); err != nil {
		t.Fatal(err)
	}
	compose := "services:\n  web:\n    build: .\n    ports: [\"8080:8080\"]\n    volumes: [files:/data]\n  helper:\n    image: alpine:3.20\n    command: [\"sleep\", \"3600\"]\n    deploy: {resources: {limits: {memory: 64M}}}\nvolumes:\n  files: {}\n"
	d2, err := e.s.DeployFiles(ctx, e.user, slug, []FileInput{{Path: "compose.yaml", Content: compose}, {Path: "Dockerfile", Content: webDockerfile("compose files")}})
	if err != nil {
		t.Fatal(err)
	}
	if d2 = e.wait(t, d2.ID); d2.State != "running" {
		t.Fatalf("%s %+v %v", d2.State, d2.Error, d2.LogTail)
	}
	// The kept volume still has the file the single app wrote.
	e.waitServing(t, slug, "single files")
	got, err := e.s.GetApp(ctx, e.user, slug)
	if err != nil || len(got.Services) != 2 || len(got.Volumes) != 1 || got.Volumes[0].Name != "files" || got.Volumes[0].Service != "web" {
		t.Fatalf("get app: %+v %v", got, err)
	}
	if info, _ := e.dk.InspectContainer(ctx, dockerx.ServiceContainerName(slug, "web", d2.Seq)); len(info.Mounts) != 1 || info.Mounts[0].Name != oldVolume {
		t.Errorf("web mounts = %+v, want the old volume %s", info.Mounts, oldVolume)
	}
	if _, err := e.dk.InspectContainer(ctx, dockerx.ContainerName(slug, d.Seq)); err == nil {
		t.Error("the single container must be gone")
	}
	// No way back to one service.
	back := "dockerfile"
	if _, err := e.s.UpdateApp(ctx, e.user, slug, UpdateAppInput{BuildMode: &back}); depCode(err) != "set_per_service" {
		t.Errorf("back to one service: %v", err)
	}

	if err := e.s.DeleteApp(ctx, e.user, slug, slug); err != nil {
		t.Fatal(err)
	}
}
