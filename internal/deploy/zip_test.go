package deploy

import (
	"archive/zip"
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/arno/bitploy/internal/apperr"
)

type zf struct {
	name string
	body string
	mode fs.FileMode // 0 = regular 0644
	dir  bool
}

func makeZip(t *testing.T, entries []zf) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		switch {
		case e.dir:
			h.Name = strings.TrimSuffix(e.name, "/") + "/"
			h.SetMode(fs.ModeDir | 0o755)
		case e.mode != 0:
			h.SetMode(e.mode)
		default:
			h.SetMode(0o644)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if !e.dir {
			if _, err := w.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "in.zip")
	if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExtractZip(t *testing.T) {
	small := ZipLimits{MaxUnpacked: 1 << 20, MaxFiles: 10}
	tests := []struct {
		name     string
		entries  []zf
		lim      ZipLimits
		strip    bool
		wantCode string
		want     []string
		stripped string
	}{
		{name: "plain files", entries: []zf{{name: "index.html", body: "hi"}, {name: "css/app.css", body: "x"}}, lim: small, strip: true,
			want: []string{"css/app.css", "index.html"}},
		{name: "single top folder is stripped", entries: []zf{{name: "site/", dir: true}, {name: "site/index.html", body: "hi"}, {name: "site/img/a.png", body: "p"}},
			lim: small, strip: true, want: []string{"img/a.png", "index.html"}, stripped: "site"},
		{name: "top folder kept when strip is off", entries: []zf{{name: "site/index.html", body: "hi"}}, lim: small, strip: false,
			want: []string{"site/index.html"}},
		{name: "two top folders are kept", entries: []zf{{name: "a/x", body: "1"}, {name: "b/y", body: "2"}}, lim: small, strip: true,
			want: []string{"a/x", "b/y"}},
		{name: "file at root prevents strip", entries: []zf{{name: "Dockerfile", body: "FROM x"}, {name: "src/a", body: "1"}}, lim: small, strip: true,
			want: []string{"Dockerfile", "src/a"}},
		{name: "macosx junk and DS_Store dropped", entries: []zf{
			{name: "app/index.html", body: "hi"}, {name: "__MACOSX/app/._index.html", body: "junk"},
			{name: "app/.DS_Store", body: "junk"}, {name: "__MACOSX/", dir: true}},
			lim: small, strip: true, want: []string{"index.html"}, stripped: "app"},
		{name: "backslash paths from windows tools", entries: []zf{{name: `src\a.txt`, body: "1"}, {name: "b.txt", body: "2"}}, lim: small, strip: true,
			want: []string{"b.txt", "src/a.txt"}},
		{name: "zip slip with ..", entries: []zf{{name: "../evil.txt", body: "x"}}, lim: small, wantCode: "source_zip_unsafe"},
		{name: "zip slip nested", entries: []zf{{name: "a/../../evil.txt", body: "x"}}, lim: small, wantCode: "source_zip_unsafe"},
		{name: "absolute path", entries: []zf{{name: "/etc/passwd", body: "x"}}, lim: small, wantCode: "source_zip_unsafe"},
		{name: "windows drive path", entries: []zf{{name: "C:/Windows/x", body: "x"}}, lim: small, wantCode: "source_zip_unsafe"},
		{name: "symlink", entries: []zf{{name: "index.html", body: "hi"}, {name: "link", body: "/etc/passwd", mode: fs.ModeSymlink | 0o777}}, lim: small, wantCode: "source_zip_unsafe"},
		{name: "device file", entries: []zf{{name: "dev", mode: fs.ModeDevice | 0o666}}, lim: small, wantCode: "source_zip_unsafe"},
		{name: "named pipe", entries: []zf{{name: "pipe", mode: fs.ModeNamedPipe | 0o666}}, lim: small, wantCode: "source_zip_unsafe"},
		{name: "zip bomb: real bytes over the limit", entries: []zf{{name: "zeros", body: strings.Repeat("\x00", 3<<20)}},
			lim: ZipLimits{MaxUnpacked: 1 << 20, MaxFiles: 10}, wantCode: "source_too_large"},
		{name: "bomb spread over files", entries: []zf{
			{name: "a", body: strings.Repeat("a", 600<<10)}, {name: "b", body: strings.Repeat("b", 600<<10)}},
			lim: ZipLimits{MaxUnpacked: 1 << 20, MaxFiles: 10}, wantCode: "source_too_large"},
		{name: "too many files", entries: func() []zf {
			var e []zf
			for i := 0; i < 11; i++ {
				e = append(e, zf{name: string(rune('a'+i)) + ".txt", body: "x"})
			}
			return e
		}(), lim: small, wantCode: "source_too_many_files"},
		{name: "duplicate entry", entries: []zf{{name: "a.txt", body: "1"}, {name: "a.txt", body: "2"}}, lim: small, wantCode: "source_zip_invalid"},
		{name: "file and folder with the same name", entries: []zf{{name: "a", body: "1"}, {name: "a/b", body: "2"}}, lim: small, wantCode: "source_zip_invalid"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			zp := makeZip(t, tc.entries)
			dest := t.TempDir()
			res, err := ExtractZip(zp, dest, tc.lim, tc.strip)
			if tc.wantCode != "" {
				var ae *apperr.Error
				if !errors.As(err, &ae) || ae.Code != tc.wantCode {
					t.Fatalf("want error %s, got %v", tc.wantCode, err)
				}
				if ae.Hint == "" || ae.Message == "" {
					t.Errorf("error needs message and hint: %+v", ae)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(res.Files, tc.want) {
				t.Errorf("files = %v, want %v", res.Files, tc.want)
			}
			if res.Stripped != tc.stripped {
				t.Errorf("stripped = %q, want %q", res.Stripped, tc.stripped)
			}
			for _, f := range tc.want {
				if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(f))); err != nil {
					t.Errorf("missing %s: %v", f, err)
				}
			}
		})
	}
}

func TestExtractZipNothingEscapes(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "dest")
	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	zp := makeZip(t, []zf{{name: "ok.txt", body: "1"}, {name: "../escape.txt", body: "x"}})
	if _, err := ExtractZip(zp, dest, DefaultZipLimits, true); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := os.Stat(filepath.Join(root, "escape.txt")); err == nil {
		t.Fatal("file escaped the destination")
	}
}

func TestExtractZipKeepsExecutableBit(t *testing.T) {
	zp := makeZip(t, []zf{{name: "run.sh", body: "#!/bin/sh", mode: 0o755}, {name: "a.txt", body: "x"}})
	dest := t.TempDir()
	if _, err := ExtractZip(zp, dest, DefaultZipLimits, false); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(filepath.Join(dest, "run.sh"))
	if st.Mode()&0o100 == 0 {
		t.Errorf("run.sh lost its executable bit: %v", st.Mode())
	}
	st, _ = os.Stat(filepath.Join(dest, "a.txt"))
	if st.Mode()&0o111 != 0 {
		t.Errorf("a.txt should not be executable: %v", st.Mode())
	}
}

func TestExtractZipNotAZip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.zip")
	_ = os.WriteFile(p, []byte("this is not a zip"), 0o600)
	_, err := ExtractZip(p, t.TempDir(), DefaultZipLimits, true)
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != "source_zip_invalid" {
		t.Fatalf("got %v", err)
	}
}

func TestInspectZip(t *testing.T) {
	lim := ZipLimits{MaxUnpacked: 1 << 20, MaxFiles: 5}
	n, err := InspectZip(makeZip(t, []zf{{name: "a/x", body: "1"}, {name: "a/y", body: "2"}, {name: "__MACOSX/a/._x", body: "j"}}), lim)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	for name, entries := range map[string][]zf{
		"slip":    {{name: "../x", body: "1"}},
		"symlink": {{name: "l", mode: fs.ModeSymlink | 0o777}},
		"empty":   {{name: "only-dir", dir: true}},
	} {
		if _, err := InspectZip(makeZip(t, entries), lim); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestListFiles(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "a", "b", "c.txt"), nil, 0o644)
	_ = os.WriteFile(filepath.Join(dir, "z.txt"), nil, 0o644)
	got, err := ListFiles(dir, 100)
	if err != nil || !reflect.DeepEqual(got, []string{"a/b/c.txt", "z.txt"}) {
		t.Fatalf("got %v err %v", got, err)
	}
}
