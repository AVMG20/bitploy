package deploy

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/apperr"
)

func TestCleanRelPath(t *testing.T) {
	ok := map[string]string{
		"index.html":        "index.html",
		"./index.html":      "index.html",
		"src/app.js":        "src/app.js",
		`src\app.js`:        "src/app.js",
		"a//b.txt":          "a/b.txt",
		"a/./b.txt":         "a/b.txt",
		"docker/Dockerfile": "docker/Dockerfile",
	}
	for in, want := range ok {
		got, err := CleanRelPath(in)
		if err != nil || got != want {
			t.Errorf("CleanRelPath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "/etc/passwd", "../x", "a/../../x", "a/..", "..", "C:\\x", "dir/", ".", "a\x00b", "a\nb"} {
		if got, err := CleanRelPath(bad); err == nil {
			t.Errorf("CleanRelPath(%q) = %q, want an error", bad, got)
		}
	}
}

func TestDecodeFiles(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString([]byte{0, 1, 2, 255})
	many := make([]File, MaxInlineFiles+1)
	for i := range many {
		many[i] = File{Path: fmt.Sprintf("f%d.txt", i), Content: "x"}
	}
	tests := []struct {
		name     string
		files    []File
		wantCode string
	}{
		{name: "ok text and base64", files: []File{{Path: "index.html", Content: "<h1>hi</h1>"}, {Path: "logo.png", Content: b64, Encoding: "base64"}, {Path: "a.txt", Content: "x", Encoding: "utf8"}}},
		{name: "empty list", files: nil, wantCode: "files_empty"},
		{name: "too many files", files: many, wantCode: "files_too_many"},
		{name: "exactly the file limit", files: many[:MaxInlineFiles]},
		{name: "too large", files: []File{{Path: "big.txt", Content: strings.Repeat("a", MaxInlineBytes+1)}}, wantCode: "files_too_large"},
		{name: "too large across files", files: []File{{Path: "a", Content: strings.Repeat("a", MaxInlineBytes/2+1)}, {Path: "b", Content: strings.Repeat("a", MaxInlineBytes/2+1)}}, wantCode: "files_too_large"},
		{name: "absolute path", files: []File{{Path: "/etc/x", Content: "x"}}, wantCode: "files_invalid_path"},
		{name: "dotdot path", files: []File{{Path: "../x", Content: "x"}}, wantCode: "files_invalid_path"},
		{name: "duplicate", files: []File{{Path: "a", Content: "1"}, {Path: "./a", Content: "2"}}, wantCode: "files_duplicate_path"},
		{name: "file and folder clash", files: []File{{Path: "a", Content: "1"}, {Path: "a/b", Content: "2"}}, wantCode: "files_invalid_path"},
		{name: "bad base64", files: []File{{Path: "a", Content: "!!!", Encoding: "base64"}}, wantCode: "files_invalid_content"},
		{name: "unknown encoding", files: []File{{Path: "a", Content: "x", Encoding: "rot13"}}, wantCode: "files_invalid_encoding"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeFiles(tc.files)
			if tc.wantCode != "" {
				var ae *apperr.Error
				if !errors.As(err, &ae) || ae.Code != tc.wantCode {
					t.Fatalf("want %s, got %v", tc.wantCode, err)
				}
				if ae.Hint == "" {
					t.Error("missing hint")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.files) {
				t.Fatalf("got %d files", len(got))
			}
		})
	}
}

func TestDecodeFilesContent(t *testing.T) {
	got, err := DecodeFiles([]File{{Path: "./a.bin", Content: base64.StdEncoding.EncodeToString([]byte{9, 8, 7}), Encoding: "base64"}})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Path != "a.bin" || string(got[0].Data) != string([]byte{9, 8, 7}) {
		t.Fatalf("%+v", got[0])
	}
}

func TestWriteZipRoundTrip(t *testing.T) {
	files := []DecodedFile{{Path: "Dockerfile", Data: []byte("FROM x")}, {Path: "site/dir/index.html", Data: []byte("hi")}}
	zp := filepath.Join(t.TempDir(), "s.zip")
	if err := WriteZip(zp, files); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	// strip=false keeps a single top folder as written.
	res, err := ExtractZip(zp, dest, DefaultZipLimits, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 2 {
		t.Fatalf("%v", res.Files)
	}
	b, _ := os.ReadFile(filepath.Join(dest, "site", "dir", "index.html"))
	if string(b) != "hi" {
		t.Fatalf("content %q", b)
	}
}
