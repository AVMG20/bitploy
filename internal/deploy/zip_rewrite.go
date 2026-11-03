package deploy

import (
	"archive/zip"
	"io"
	"os"
)

// RewriteZip copies the source zip src to dst with one file replaced (or added
// when it is not there). Entries are laid out as ExtractZip would (junk
// dropped, a single top folder stripped when strip is set) and keep their file
// mode, so the copy is a source snapshot that extracts without stripping.
func RewriteZip(src, dst string, strip bool, rel string, data []byte) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return zipErr("source_zip_invalid", "The stored source is not a valid zip file ("+err.Error()+").", "Send the files again with deploy_files.")
	}
	defer zr.Close()
	entries, err := planZip(zr.File)
	if err != nil {
		return err
	}
	if strip {
		stripTopFolder(entries)
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
	put := func(name string, mode os.FileMode, write func(w io.Writer) error) error {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		return write(w)
	}
	replaced := false
	for _, e := range entries {
		if e.isDir || e.rel == "" {
			continue
		}
		if e.rel == rel {
			replaced = true
			err = put(e.rel, 0o644, func(w io.Writer) error { _, err := w.Write(data); return err })
		} else {
			err = put(e.rel, e.f.Mode().Perm(), func(w io.Writer) error {
				rc, err := e.f.Open()
				if err != nil {
					return err
				}
				defer rc.Close()
				_, err = io.Copy(w, rc)
				return err
			})
		}
		if err != nil {
			return fail(err)
		}
	}
	if !replaced {
		if err := put(rel, 0o644, func(w io.Writer) error { _, err := w.Write(data); return err }); err != nil {
			return fail(err)
		}
	}
	if err := zw.Close(); err != nil {
		return fail(err)
	}
	return out.Close()
}
