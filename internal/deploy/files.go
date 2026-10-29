package deploy

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/arno/bitploy/internal/apperr"
)

// Limits for inline file sources (SPEC "Sources").
const (
	MaxInlineBytes = 2 << 20
	MaxInlineFiles = 200
)

// File is one inline file as sent by the client.
type File struct {
	Path     string
	Content  string
	Encoding string // utf8 (default) | base64
}

// DecodedFile is a validated file with its bytes.
type DecodedFile struct {
	Path string // clean relative slash path
	Data []byte
}

// CleanRelPath normalises a client-supplied path: backslashes become slashes,
// "./" prefixes go away, and absolute paths, "..", drive letters and control
// characters are rejected.
func CleanRelPath(p string) (string, error) {
	orig := p
	p = strings.ReplaceAll(p, "\\", "/")
	for strings.HasPrefix(p, "./") {
		p = p[2:]
	}
	if p == "" || strings.HasPrefix(p, "/") || (len(p) >= 2 && p[1] == ':') {
		return "", fmt.Errorf("%q is not a relative path", orig)
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("%q contains a control character", orig)
		}
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return "", fmt.Errorf("%q goes outside the project (it contains ..)", orig)
		}
	}
	c := path.Clean(p)
	if c == "." || strings.HasSuffix(p, "/") {
		return "", fmt.Errorf("%q is a folder, not a file", orig)
	}
	return c, nil
}

// DecodeFiles validates the inline file list (count, total size, safe unique
// paths, encodings) and decodes the contents.
func DecodeFiles(files []File) ([]DecodedFile, error) {
	if len(files) == 0 {
		return nil, apperr.BadRequest("files_empty",
			"No files were sent.",
			"Send at least a Dockerfile and your project files as a list of {path, content}.").WithNext("deploy_files")
	}
	if len(files) > MaxInlineFiles {
		return nil, apperr.BadRequest("files_too_many",
			fmt.Sprintf("%d files is more than the limit of %d for inline files.", len(files), MaxInlineFiles),
			"Zip the folder, call create_upload and run the curl command it returns, then call deploy.").WithNext("create_upload")
	}
	seen := map[string]bool{}
	out := make([]DecodedFile, 0, len(files))
	var total int64
	for _, f := range files {
		p, err := CleanRelPath(f.Path)
		if err != nil {
			return nil, apperr.BadRequest("files_invalid_path",
				"A file path is not allowed: "+err.Error()+".",
				"Use plain relative paths such as index.html or src/app.js: no leading /, no .. and no backslashes.")
		}
		if seen[p] {
			return nil, apperr.BadRequest("files_duplicate_path",
				"The file "+q(p)+" appears twice.", "Send each path once.")
		}
		seen[p] = true
		var data []byte
		switch strings.ToLower(f.Encoding) {
		case "", "utf8", "utf-8":
			data = []byte(f.Content)
		case "base64":
			data, err = base64.StdEncoding.DecodeString(f.Content)
			if err != nil {
				return nil, apperr.BadRequest("files_invalid_content",
					"The file "+q(p)+" is not valid base64.",
					"Encode the bytes with standard base64, or send text files with encoding utf8.")
			}
		default:
			return nil, apperr.BadRequest("files_invalid_encoding",
				"The file "+q(p)+" has the unknown encoding "+q(f.Encoding)+".",
				"Use encoding utf8 (default) for text or base64 for binary files.")
		}
		total += int64(len(data))
		if total > MaxInlineBytes {
			return nil, apperr.New(413, "files_too_large",
				"The files are bigger than the 2 MB limit for inline files.",
				"Zip the folder, call create_upload and run the curl command it returns, then call deploy.").WithNext("create_upload")
		}
		out = append(out, DecodedFile{Path: p, Data: data})
	}
	// A file may not also be a folder of another file ("a" and "a/b").
	for p := range seen {
		for dir := path.Dir(p); dir != "."; dir = path.Dir(dir) {
			if seen[dir] {
				return nil, apperr.BadRequest("files_invalid_path",
					"The path "+q(dir)+" is both a file and a folder.", "Rename one of them.")
			}
		}
	}
	return out, nil
}

// WriteZip stores decoded files as a zip (the source snapshot for redeploys).
func WriteZip(dest string, files []DecodedFile) error {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		h := &zip.FileHeader{Name: f.Path, Method: zip.Deflate}
		h.SetMode(0o644)
		w, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		if _, err := w.Write(f.Data); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile(dest, buf.Bytes(), 0o640)
}
