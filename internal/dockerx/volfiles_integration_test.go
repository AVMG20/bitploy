//go:build integration

package dockerx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/ids"
)

// Needs a Docker daemon and the busybox image (pulled when missing).
// Run: go test -tags=integration -count=1 -run VolumeFiles ./internal/dockerx/
func TestVolumeFilesIntegration(t *testing.T) {
	ctx := context.Background()
	c, err := New(ctx)
	if err != nil {
		t.Skip("no docker: ", err)
	}
	defer c.Close()
	vol := "bp-test-files-" + strings.ToLower(ids.Hex(5)) + "-data"
	if _, err := c.EnsureVolume(ctx, vol, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.RemoveVolume(context.Background(), vol) })

	// A volume that does not exist is not created by the helpers.
	if _, err := c.ListVolumeDir(ctx, "bp-test-nope-data", "", 10); !errors.Is(err, ErrNoVolume) {
		t.Fatalf("missing volume: %v", err)
	}

	// Seed: /data is root 1777 like a prepared volume; app.db is owned by 1000.
	seed := `chmod 1777 /data && mkdir -p /data/sub && echo '{"a":1}' > /data/conf.json && echo db > /data/sub/app.db && chown 1000:1000 /data/sub /data/sub/app.db && ln -s /etc /data/link && ln -s conf.json /data/conf.lnk`
	if err := c.runHelper(ctx, vol, []string{"sh", "-c", seed}); err != nil {
		t.Fatal(err)
	}

	l, err := c.ListVolumeDir(ctx, vol, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, e := range l.Entries {
		kinds[e.Name] = e.Kind
	}
	if kinds["sub"] != "dir" || kinds["conf.json"] != "file" || kinds["link"] != "symlink" || kinds["conf.lnk"] != "symlink" || l.Entries[0].Kind != "dir" {
		t.Fatalf("root = %+v", l)
	}
	if l, err := c.ListVolumeDir(ctx, vol, "", 2); err != nil || len(l.Entries) != 2 || !l.Truncated {
		t.Fatalf("capped = %+v %v", l, err)
	}
	if _, err := c.ListVolumeDir(ctx, vol, "conf.json", 10); !errors.Is(err, ErrNotDir) {
		t.Errorf("list a file: %v", err)
	}
	if _, err := c.ListVolumeDir(ctx, vol, "nope", 10); !errors.Is(err, ErrNotFound) {
		t.Errorf("list missing: %v", err)
	}
	if _, err := c.ListVolumeDir(ctx, vol, "link", 10); !errors.Is(err, ErrLinkInPath) {
		t.Errorf("list through a link: %v", err)
	}

	// Download.
	f, err := c.OpenVolumeFile(ctx, vol, "conf.json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(f)
	_ = f.Close()
	if string(body) != "{\"a\":1}\n" || f.Size != int64(len(body)) {
		t.Errorf("download = %q size %d", body, f.Size)
	}
	for _, p := range []string{"sub", "conf.lnk", "link"} {
		if _, err := c.OpenVolumeFile(ctx, vol, p); !errors.Is(err, ErrWrongKind) {
			t.Errorf("download %s: %v", p, err)
		}
	}
	if f, err := c.OpenVolumeFile(ctx, vol, "link/passwd"); !errors.Is(err, ErrLinkInPath) {
		if err == nil {
			_ = f.Close()
		}
		t.Errorf("download through a link: %v", err)
	}
	if _, err := c.OpenVolumeFile(ctx, vol, "sub/none"); !errors.Is(err, ErrNotFound) {
		t.Errorf("download missing: %v", err)
	}

	// Upload: new file takes the owner of the folder, replacing keeps the owner of the file.
	put := func(dir, name, content string, ow bool) error {
		return c.PutVolumeFile(ctx, vol, dir, name, int64(len(content)), strings.NewReader(content), ow)
	}
	if err := put("sub", "new.json", "[1,2]", false); err != nil {
		t.Fatal(err)
	}
	if err := put("sub", "new.json", "x", false); !errors.Is(err, ErrExists) {
		t.Errorf("no overwrite: %v", err)
	}
	if err := put("sub", "app.db", "replaced", true); err != nil {
		t.Fatal(err)
	}
	if err := put("", "root.txt", "r", false); err != nil {
		t.Fatal(err)
	}
	if err := put("sub", "link2", "x", false); err != nil {
		t.Fatal(err)
	}
	// Spaces, quotes, unicode and a leading dash reach the scripts as arguments.
	odd := `-rf "it's" é 漢字 $(x).txt`
	if err := put("sub", odd, "odd", false); err != nil {
		t.Fatal(err)
	}
	if f, err := c.OpenVolumeFile(ctx, vol, "sub/"+odd); err != nil {
		t.Errorf("download %q: %v", odd, err)
	} else {
		b, _ := io.ReadAll(f)
		_ = f.Close()
		if string(b) != "odd" {
			t.Errorf("download %q = %q", odd, b)
		}
	}
	if l, err := c.ListVolumeDir(ctx, vol, "sub", 100); err != nil || !strings.Contains(fmt.Sprint(l.Entries), odd) {
		t.Errorf("list with %q: %+v %v", odd, l, err)
	}
	if err := c.RemoveVolumePath(ctx, vol, "sub", odd); err != nil {
		t.Errorf("delete %q: %v", odd, err)
	}
	if err := put("", "conf.lnk", "now a file", true); err != nil {
		t.Fatal(err)
	}
	if err := put("", "sub", "x", true); !errors.Is(err, ErrWrongKind) {
		t.Errorf("replace a folder: %v", err)
	}
	if err := put("nope", "a", "x", false); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing folder: %v", err)
	}
	if err := put("link", "a", "x", false); !errors.Is(err, ErrLinkInPath) {
		t.Errorf("through link: %v", err)
	}
	if err := put("conf.json", "a", "x", false); !errors.Is(err, ErrNotDir) {
		t.Errorf("into a file: %v", err)
	}
	if err := c.PutVolumeFile(ctx, vol, "sub", "short", 10, strings.NewReader("abc"), false); err == nil {
		t.Error("short body accepted")
	}
	out := inspect(t, c, vol, `stat -c '%n %u:%g %a %s' /data/sub/new.json /data/sub/app.db /data/root.txt /data/conf.lnk; ls -A /data /data/sub; cat /data/sub/app.db`)
	for _, want := range []string{
		"/data/sub/new.json 1000:1000 644 5",
		"/data/sub/app.db 1000:1000 644 8",
		"/data/root.txt 0:0 666 1",
		"/data/conf.lnk 0:0 666 10",
		"replaced",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("volume lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, ".bp-upload-") {
		t.Errorf("temporary file left behind:\n%s", out)
	}
	if b, _ := exec.Command("docker", "run", "--rm", "-v", vol+":/data", "busybox:1.37", "cat", "/data/conf.json").Output(); !bytes.Contains(b, []byte(`{"a":1}`)) {
		t.Errorf("link target was changed: %q", b)
	}

	// Folders and delete.
	if err := c.MkdirVolume(ctx, vol, "sub", "deep"); err != nil {
		t.Fatal(err)
	}
	if err := c.MkdirVolume(ctx, vol, "sub", "deep"); !errors.Is(err, ErrExists) {
		t.Errorf("mkdir twice: %v", err)
	}
	if err := c.MkdirVolume(ctx, vol, "nope", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("mkdir in missing: %v", err)
	}
	if err := c.MkdirVolume(ctx, vol, "link", "x"); !errors.Is(err, ErrLinkInPath) {
		t.Errorf("mkdir through link: %v", err)
	}
	if o := inspect(t, c, vol, `stat -c '%u:%g %a' /data/sub/deep`); strings.TrimSpace(o) != "1000:1000 755" {
		t.Errorf("new folder = %q", o)
	}
	if err := c.RemoveVolumePath(ctx, vol, "link", "x"); !errors.Is(err, ErrLinkInPath) {
		t.Errorf("delete through link: %v", err)
	}
	if err := c.RemoveVolumePath(ctx, vol, "", "link"); err != nil { // the link only, not /etc
		t.Fatal(err)
	}
	if err := c.RemoveVolumePath(ctx, vol, "", "sub"); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveVolumePath(ctx, vol, "", "sub"); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete twice: %v", err)
	}
	l, err = c.ListVolumeDir(ctx, vol, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range l.Entries {
		names = append(names, e.Name)
	}
	if strings.Join(names, ",") != "conf.json,conf.lnk,root.txt" {
		t.Errorf("after delete: %v", names)
	}
}

func inspect(t *testing.T, c *Client, vol, script string) string {
	t.Helper()
	out, err := c.runScript(context.Background(), vol, true, script)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
