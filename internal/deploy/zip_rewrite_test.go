package deploy

import (
	"archive/zip"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/AVMG20/bitploy/internal/apperr"
)

// rawEntries reads every entry of a zip: name, mode, CRC and the stored
// (compressed) bytes.
type rawEntry struct {
	mode fs.FileMode
	crc  uint32
	raw  []byte
	body []byte
}

func readRaw(t *testing.T, p string) map[string]rawEntry {
	t.Helper()
	zr, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	out := map[string]rawEntry{}
	for _, f := range zr.File {
		r, err := f.OpenRaw()
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(r)
		var body []byte
		if !f.Mode().IsDir() {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			body, err = io.ReadAll(rc)
			rc.Close()
			if err != nil {
				t.Fatalf("%s: %v", f.Name, err)
			}
		}
		out[f.Name] = rawEntry{mode: f.Mode(), crc: f.CRC32, raw: raw, body: body}
	}
	return out
}

func TestRewriteZip(t *testing.T) {
	src := makeZip(t, []zf{
		{name: "proj/", dir: true},
		{name: "proj/Dockerfile", body: "FROM alpine:3.22\n"},
		{name: "proj/run.sh", body: "#!/bin/sh\necho hi\n", mode: 0o755},
		{name: "proj/docker/", dir: true},
		{name: "proj/docker/Dockerfile.prod", body: "FROM old:1\n"},
		{name: "proj/empty/", dir: true},
		{name: "proj/.DS_Store", body: "junk"},
		{name: "__MACOSX/proj/._run.sh", body: "junk"},
	})
	before := readRaw(t, src)
	tests := []struct {
		name  string
		strip bool
		rel   string
		want  map[string]string // file -> where its bytes come from in src ("" = the new content)
	}{
		{"strip, replace at the top", true, "Dockerfile", map[string]string{"Dockerfile": "", "run.sh": "proj/run.sh", "docker/Dockerfile.prod": "proj/docker/Dockerfile.prod"}},
		{"strip, subfolder path", true, "docker/Dockerfile.prod", map[string]string{"Dockerfile": "proj/Dockerfile", "run.sh": "proj/run.sh", "docker/Dockerfile.prod": ""}},
		{"strip, add a new file", true, "./build/Dockerfile", map[string]string{"Dockerfile": "proj/Dockerfile", "run.sh": "proj/run.sh", "docker/Dockerfile.prod": "proj/docker/Dockerfile.prod", "build/Dockerfile": ""}},
		{"no strip", false, "Dockerfile", map[string]string{"Dockerfile": "", "proj/Dockerfile": "proj/Dockerfile", "proj/run.sh": "proj/run.sh", "proj/docker/Dockerfile.prod": "proj/docker/Dockerfile.prod"}},
	}
	const next = "FROM alpine:3.23\nUSER 1000\n"
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := filepath.Join(t.TempDir(), "out.zip")
			if err := RewriteZip(src, dst, tt.strip, tt.rel, []byte(next)); err != nil {
				t.Fatal(err)
			}
			got := readRaw(t, dst)
			files := map[string]bool{}
			for name, e := range got {
				if !e.mode.IsDir() {
					files[name] = true
				}
			}
			wantFiles := map[string]bool{}
			for name := range tt.want {
				wantFiles[name] = true
			}
			if !reflect.DeepEqual(files, wantFiles) {
				t.Fatalf("files = %v, want %v", files, wantFiles)
			}
			for name, from := range tt.want {
				e := got[name]
				if from == "" {
					if string(e.body) != next || e.mode.Perm() != 0o644 {
						t.Errorf("%s: %q %v", name, e.body, e.mode)
					}
					continue
				}
				o := before[from]
				// Byte-identical: the same stored bytes, CRC and mode.
				if string(e.raw) != string(o.raw) || e.crc != o.crc || e.mode != o.mode || string(e.body) != string(o.body) {
					t.Errorf("%s changed: mode %v -> %v", name, o.mode, e.mode)
				}
			}
			// The copy extracts without stripping and keeps the empty folder.
			out := t.TempDir()
			if _, err := ExtractZip(dst, out, DefaultZipLimits, false); err != nil {
				t.Fatal(err)
			}
			emptyDir := "empty"
			if !tt.strip {
				emptyDir = "proj/empty"
			}
			if st, err := os.Stat(filepath.Join(out, emptyDir)); err != nil || !st.IsDir() {
				t.Errorf("empty folder lost: %v", err)
			}
			if st, err := os.Stat(filepath.Join(out, filepath.Dir(firstKey(tt.want, "run.sh")), "run.sh")); err != nil || st.Mode().Perm()&0o100 == 0 {
				t.Errorf("run.sh lost its executable bit: %v", err)
			}
		})
	}
}

func firstKey(m map[string]string, base string) string {
	for k := range m {
		if filepath.Base(k) == base {
			return k
		}
	}
	return base
}

func TestRewriteZipRefuses(t *testing.T) {
	for name, c := range map[string]struct {
		entries []zf
		code    string
	}{
		"zip slip":  {[]zf{{name: "../evil", body: "x"}, {name: "Dockerfile", body: "FROM a:1\n"}}, "source_zip_unsafe"},
		"absolute":  {[]zf{{name: "/etc/passwd", body: "x"}}, "source_zip_unsafe"},
		"symlink":   {[]zf{{name: "link", body: "/etc/passwd", mode: fs.ModeSymlink | 0o777}}, "source_zip_unsafe"},
		"duplicate": {[]zf{{name: "Dockerfile", body: "a"}, {name: "Dockerfile", body: "b"}}, "source_zip_invalid"},
	} {
		t.Run(name, func(t *testing.T) {
			dst := filepath.Join(t.TempDir(), "out.zip")
			err := RewriteZip(makeZip(t, c.entries), dst, true, "Dockerfile", []byte("FROM alpine:3.22\n"))
			var ae *apperr.Error
			if !errors.As(err, &ae) || ae.Code != c.code {
				t.Fatalf("want %s, got %v", c.code, err)
			}
			if _, err := os.Stat(dst); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("a refused rewrite left %s behind", dst)
			}
		})
	}
	if err := RewriteZip(filepath.Join(t.TempDir(), "gone.zip"), filepath.Join(t.TempDir(), "out.zip"), false, "Dockerfile", nil); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing source: %v", err)
	}
}
