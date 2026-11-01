package deploy

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The dashboard packs a dropped folder into a zip in the browser
// (web/src/lib/zip.ts). testdata/browser-made.zip was made by that code: from
// internal/deploy/testdata run `node make-browser-zip.mjs` (Node 22.18 or
// newer). This checks that the server's own reader accepts it and gets the
// content back (the wrapping folder is removed in the browser already).
func TestBrowserMadeZip(t *testing.T) {
	zp := filepath.Join("testdata", "browser-made.zip")
	n, err := InspectZip(zp, DefaultZipLimits)
	if err != nil || n != 4 {
		t.Fatalf("InspectZip: %d files, %v", n, err)
	}
	dest := t.TempDir()
	res, err := ExtractZip(zp, dest, DefaultZipLimits, true)
	if err != nil {
		t.Fatalf("ExtractZip: %v", err)
	}
	want := []string{"css/site.css", "empty.txt", "img/ü ñ.bin", "index.html"}
	if !reflect.DeepEqual(res.Files, want) {
		t.Errorf("files = %v, want %v", res.Files, want)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "index.html")); err != nil || string(b) != "<h1>héllo ✓</h1>" {
		t.Errorf("index.html: %q %v", b, err)
	}
	css, err := os.ReadFile(filepath.Join(dest, "css", "site.css"))
	if err != nil || len(css) != len("hello world ")*10000 {
		t.Errorf("site.css: %d bytes, %v", len(css), err)
	}
	fi, err := os.Stat(filepath.Join(dest, "index.html"))
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm()&0o400 == 0 {
		t.Errorf("mode: %v %v", fi, err)
	}
}
