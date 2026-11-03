package deploy

import (
	"archive/zip"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
)

// RewriteZip copies the source zip src to dst with the file rel replaced by
// data (or added when it is not there). Entries are laid out as ExtractZip
// would (junk dropped, a single top folder stripped when strip is set), so the
// copy is a source snapshot that extracts without stripping. Every other file
// is copied as it is stored, compressed bytes, CRC, mode and time included,
// without unpacking it. The zip is vetted like an upload first (planZip:
// no absolute paths, "..", symlinks or special files) and must fit the upload
// limits; ExtractZip counts the real bytes again when the copy is built.
// A missing src returns an error satisfying errors.Is(err, fs.ErrNotExist).
func RewriteZip(src, dst string, strip bool, rel string, data []byte) error {
	zr, err := zip.OpenReader(src)
	if errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err != nil {
		return zipErr("source_zip_invalid", "The stored source is not a valid zip file ("+err.Error()+").", "Send the files again with deploy_files.")
	}
	defer zr.Close()
	if len(zr.File) > MaxZipFiles*4 {
		return tooMany(DefaultZipLimits)
	}
	entries, err := planZip(zr.File)
	if err != nil {
		return err
	}
	if strip {
		stripTopFolder(entries)
	}
	rel = path.Clean(rel)
	nfiles := 0
	var declared uint64
	for _, e := range entries {
		if !e.isDir && e.rel != "" && e.rel != rel {
			nfiles++
			declared += e.f.UncompressedSize64
		}
	}
	if nfiles+1 > MaxZipFiles {
		return tooMany(DefaultZipLimits)
	}
	if declared+uint64(len(data)) > MaxZipUnpacked {
		return tooBig(DefaultZipLimits)
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(out)
	fail := func(err error) error {
		out.Close()
		os.Remove(dst)
		return err
	}
	writeNew := func() error {
		h := &zip.FileHeader{Name: rel, Method: zip.Deflate}
		h.SetMode(0o644)
		w, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}
	replaced := false
	for _, e := range entries {
		switch {
		case e.rel == "":
			continue
		case e.isDir:
			h := &zip.FileHeader{Name: e.rel + "/", Method: zip.Store}
			h.SetMode(os.ModeDir | 0o755)
			if _, err := zw.CreateHeader(h); err != nil {
				return fail(err)
			}
		case e.rel == rel:
			if replaced { // ExtractZip refuses a path that is there twice; so does this
				return fail(zipErr("source_zip_invalid", "The stored source contains "+rel+" twice.", "Send the files again with deploy_files."))
			}
			replaced = true
			if err := writeNew(); err != nil {
				return fail(err)
			}
		default:
			if err := copyRaw(zw, e); err != nil {
				return fail(err)
			}
		}
	}
	if !replaced {
		if err := writeNew(); err != nil {
			return fail(err)
		}
	}
	if err := zw.Close(); err != nil {
		return fail(err)
	}
	return out.Close()
}

// copyRaw copies one entry under its laid-out name without decompressing it.
func copyRaw(zw *zip.Writer, e zipEntry) error {
	h := e.f.FileHeader
	h.Name = e.rel
	w, err := zw.CreateRaw(&h)
	if err != nil {
		return err
	}
	r, err := e.f.OpenRaw()
	if err != nil {
		return err
	}
	_, err = io.Copy(w, r)
	return err
}
