package deploy

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/AVMG20/bitploy/internal/apperr"
)

// Zip upload limits (SPEC "Sources").
const (
	MaxZipCompressed = 95 << 20
	MaxZipUnpacked   = 500 << 20
	MaxZipFiles      = 20000
)

// ZipLimits bounds extraction; tests use small values.
type ZipLimits struct {
	MaxUnpacked int64
	MaxFiles    int
}

var DefaultZipLimits = ZipLimits{MaxUnpacked: MaxZipUnpacked, MaxFiles: MaxZipFiles}

// ExtractResult lists what was extracted.
type ExtractResult struct {
	Files    []string // relative slash paths, sorted
	Bytes    int64
	Stripped string // the single top folder that was removed, if any
}

type zipEntry struct {
	f     *zip.File
	rel   string
	isDir bool
}

func zipErr(code, msg, hint string) *apperr.Error {
	return apperr.Unprocessable(code, msg, hint)
}

// ExtractZip unpacks a zip into dest (an existing, empty directory).
//
//   - A single top-level folder holding everything is stripped (people zip the
//     folder, not its contents).
//   - __MACOSX/ entries and .DS_Store files are dropped.
//   - Absolute paths, "..", symlinks and device files reject the whole zip.
//   - Bytes are counted while extracting; the real count, not the sizes in the
//     zip headers, decides when to abort (zip bombs lie).
//
// strip=false keeps the layout as is (used for snapshots of inline files).
func ExtractZip(zipPath, dest string, lim ZipLimits, strip bool) (ExtractResult, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return ExtractResult{}, zipErr("source_zip_invalid",
			"The upload is not a valid zip file ("+err.Error()+").",
			"Create it with `zip -r app.zip .` inside your project folder, then upload it again.")
	}
	defer zr.Close()
	if lim.MaxFiles <= 0 {
		lim.MaxFiles = MaxZipFiles
	}
	if lim.MaxUnpacked <= 0 {
		lim.MaxUnpacked = MaxZipUnpacked
	}
	if len(zr.File) > lim.MaxFiles*4 {
		return ExtractResult{}, tooMany(lim)
	}

	entries, err := planZip(zr.File)
	if err != nil {
		return ExtractResult{}, err
	}
	var res ExtractResult
	if strip {
		res.Stripped = stripTopFolder(entries)
	}
	nfiles := 0
	var declared uint64
	for _, e := range entries {
		if e.isDir || e.rel == "" {
			continue
		}
		nfiles++
		declared += e.f.UncompressedSize64
	}
	if nfiles > lim.MaxFiles {
		return ExtractResult{}, tooMany(lim)
	}
	if declared > uint64(lim.MaxUnpacked) {
		return ExtractResult{}, tooBig(lim)
	}

	seen := map[string]bool{}
	for _, e := range entries {
		if e.rel == "" {
			continue
		}
		target := filepath.Join(dest, filepath.FromSlash(e.rel))
		if !filepath.IsLocal(filepath.FromSlash(e.rel)) {
			return ExtractResult{}, unsafeZip(e.f.Name)
		}
		if e.isDir {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return ExtractResult{}, zipErr("source_zip_invalid", "The zip has a conflicting path: "+e.rel+".", "Check that no name is used both as a file and as a folder.")
			}
			continue
		}
		if seen[e.rel] {
			return ExtractResult{}, zipErr("source_zip_invalid", "The zip contains "+e.rel+" twice.", "Zip the folder again so every path appears once.")
		}
		seen[e.rel] = true
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return ExtractResult{}, zipErr("source_zip_invalid", "The zip has a conflicting path: "+e.rel+".", "Check that no name is used both as a file and as a folder.")
		}
		n, err := extractOne(e, target, lim.MaxUnpacked-res.Bytes)
		res.Bytes += n
		if err != nil {
			return ExtractResult{}, err
		}
		res.Files = append(res.Files, e.rel)
	}
	sort.Strings(res.Files)
	return res, nil
}

func extractOne(e zipEntry, target string, budget int64) (int64, error) {
	rc, err := e.f.Open()
	if err != nil {
		return 0, zipErr("source_zip_invalid", "Cannot read "+e.rel+" from the zip ("+err.Error()+").", "Zip the folder again.")
	}
	defer rc.Close()
	mode := fs.FileMode(0o644)
	if e.f.Mode()&0o111 != 0 {
		mode = 0o755
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return 0, zipErr("source_zip_invalid", "The zip has a conflicting path: "+e.rel+".", "Check that no name is used both as a file and as a folder.")
	}
	defer out.Close()
	// Read one byte past the budget so an overrun is detected, not truncated.
	n, err := io.Copy(out, io.LimitReader(rc, budget+1))
	if err != nil {
		return n, zipErr("source_zip_invalid", "Cannot unpack "+e.rel+" ("+err.Error()+").", "Zip the folder again.")
	}
	if n > budget {
		return n, tooBig(ZipLimits{MaxUnpacked: budget})
	}
	if e.f.Mode()&0o111 != 0 {
		_ = os.Chmod(target, 0o755)
	}
	return n, nil
}

// planZip normalises and vets every entry. Junk (__MACOSX, .DS_Store) is
// dropped silently; anything dangerous rejects the whole archive.
func planZip(files []*zip.File) ([]zipEntry, error) {
	var out []zipEntry
	for _, f := range files {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		isDir := strings.HasSuffix(name, "/") || f.Mode().IsDir()
		trimmed := strings.Trim(name, "/")
		parts := strings.Split(trimmed, "/")
		if isJunk(parts) {
			continue
		}
		if strings.HasPrefix(name, "/") || (len(name) >= 2 && name[1] == ':') {
			return nil, unsafeZip(f.Name)
		}
		for _, p := range parts {
			if p == ".." {
				return nil, unsafeZip(f.Name)
			}
			if strings.ContainsRune(p, 0) {
				return nil, unsafeZip(f.Name)
			}
		}
		mode := f.Mode()
		if mode&fs.ModeSymlink != 0 {
			return nil, zipErr("source_zip_unsafe",
				"The zip contains a symbolic link ("+f.Name+"), which bitploy does not accept.",
				"Zip again without symlinks (do not use `zip -y`), or replace the link with a real copy of the file.")
		}
		if !isDir && !mode.IsRegular() {
			return nil, zipErr("source_zip_unsafe",
				"The zip contains a special file ("+f.Name+") such as a device or socket.",
				"Remove it from the folder and zip again.")
		}
		rel := path.Clean(trimmed)
		if rel == "." {
			rel = ""
		}
		out = append(out, zipEntry{f: f, rel: rel, isDir: isDir})
	}
	return out, nil
}

func isJunk(parts []string) bool {
	if len(parts) == 0 {
		return false
	}
	if parts[0] == "__MACOSX" {
		return true
	}
	return parts[len(parts)-1] == ".DS_Store"
}

// stripTopFolder removes the first path component when every entry lives in
// the same top-level folder (and at least one file sits below it).
func stripTopFolder(entries []zipEntry) string {
	top := ""
	hasNested := false
	for _, e := range entries {
		if e.rel == "" {
			continue
		}
		first, rest, nested := strings.Cut(e.rel, "/")
		if !nested && !e.isDir {
			return "" // a file at the root: nothing to strip
		}
		if top == "" {
			top = first
		} else if top != first {
			return ""
		}
		if nested && !(e.isDir && rest == "") {
			hasNested = true
		}
	}
	if top == "" || !hasNested {
		return ""
	}
	for i := range entries {
		e := &entries[i]
		if e.rel == top {
			e.rel = ""
			continue
		}
		if e.rel != "" {
			e.rel = strings.TrimPrefix(e.rel, top+"/")
		}
	}
	return top
}

func unsafeZip(name string) *apperr.Error {
	return zipErr("source_zip_unsafe",
		"The zip contains a path that is not allowed: "+q(name)+".",
		"Paths must be relative and stay inside the project (no leading / and no ..). Zip the folder again with `zip -r app.zip .`.")
}

func tooMany(lim ZipLimits) *apperr.Error {
	return apperr.New(413, "source_too_many_files",
		fmt.Sprintf("The zip has more than %d files.", lim.MaxFiles),
		"Leave out node_modules, .git, build output and other generated folders, then zip again.")
}

func tooBig(lim ZipLimits) *apperr.Error {
	return apperr.New(413, "source_too_large",
		fmt.Sprintf("The zip unpacks to more than %d MB.", lim.MaxUnpacked>>20),
		"Leave out node_modules, .git, build output, videos and other big or generated files, then zip again.")
}

// InspectZip vets a zip's central directory without extracting: valid
// archive, no unsafe paths, entry and declared-size limits. Used when an
// upload arrives so the curl caller gets an error at once.
func InspectZip(zipPath string, lim ZipLimits) (files int, err error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return 0, zipErr("source_zip_invalid",
			"The upload is not a valid zip file ("+err.Error()+").",
			"Create it with `zip -r app.zip .` inside your project folder, then upload it again.")
	}
	defer zr.Close()
	if lim.MaxFiles <= 0 {
		lim.MaxFiles = MaxZipFiles
	}
	if lim.MaxUnpacked <= 0 {
		lim.MaxUnpacked = MaxZipUnpacked
	}
	if len(zr.File) > lim.MaxFiles*4 {
		return 0, tooMany(lim)
	}
	entries, err := planZip(zr.File)
	if err != nil {
		return 0, err
	}
	var declared uint64
	for _, e := range entries {
		if !e.isDir {
			files++
			declared += e.f.UncompressedSize64
		}
	}
	if files > lim.MaxFiles {
		return 0, tooMany(lim)
	}
	if declared > uint64(lim.MaxUnpacked) {
		return 0, tooBig(lim)
	}
	if files == 0 {
		return 0, zipErr("source_zip_empty", "The zip contains no files.", "Zip the folder with your project files and a Dockerfile, then upload again.")
	}
	return files, nil
}

// ListFiles returns every regular file below dir as relative slash paths
// (sorted), skipping nothing. Used for the Dockerfile checks. Stops after max.
func ListFiles(dir string, max int) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if len(out) >= max {
			return fs.SkipAll
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(out)
	return out, err
}
