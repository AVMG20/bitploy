package app

// Backups are plain .tar.gz files of the /data folder. Everything that goes in
// or comes out passes through sanitizeTar: it rewrites every entry into a
// minimal, safe header, so an uploaded archive can never plant a file outside
// /data, a device node, a setuid file or extended attributes.

import (
	"archive/tar"
	"compress/flate"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	maxTarEntries        = 300_000
	maxTrustedTarEntries = 5_000_000
	maxTarNameLen        = 1024
	maxTrustedNameLen    = 4096
	maxTarNameBytes      = 32 << 20 // all names together: they are held in memory while checking
	maxSkippedNames      = 100_000
	whiteoutPrefix       = ".wh." // Docker's archive code treats these as "delete this file"
)

// tarLimits bound what sanitizeTar accepts.
type tarLimits struct {
	MaxBytes    int64 // total size of all entries in the archive; 0 means no limit
	SkipSpecial bool  // skip fifos and devices instead of refusing (reading a live volume)
	// Trusted is for archives that bitploy made from an app's own volume
	// (backups, and restoring them): absolute symlinks are normal there
	// (/data/current -> /data/releases/1) and there can be millions of files.
	// Uploads are never trusted. Nothing may be placed below a symlink either way.
	Trusted bool
	Ctx     context.Context // stops the copy when it ends (a client that went away); nil: never
}

type tarStats struct {
	Entries int
	Bytes   int64
}

// tarProblem is an archive that is not acceptable. The message is plain words,
// meant to be shown to the user.
type tarProblem struct{ msg string }

func (e *tarProblem) Error() string { return e.msg }

func problem(format string, a ...any) error { return &tarProblem{fmt.Sprintf(format, a...)} }

// isCorrupt tells a damaged archive from an I/O error of the reader itself
// (a closed connection, a size limit), which must be passed on unchanged.
func isCorrupt(err error) bool {
	var ce flate.CorruptInputError
	return errors.Is(err, tar.ErrHeader) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) ||
		errors.Is(err, gzip.ErrHeader) || errors.Is(err, gzip.ErrChecksum) || errors.As(err, &ce)
}

// cleanTarName turns an archive path into a path relative to /data. It returns
// "" for the root itself and an error for absolute paths and "..".
func cleanTarName(name string, maxLen int) (string, error) {
	if strings.ContainsRune(name, 0) {
		return "", problem("The archive contains a file name that is not allowed.")
	}
	if strings.HasPrefix(name, "/") {
		return "", problem("The archive contains a file with an absolute path (%s). Archive the folder contents with relative paths.", shorten(name))
	}
	var out []string
	for _, p := range strings.Split(name, "/") {
		switch p {
		case "", ".":
		case "..":
			return "", problem("The archive contains a path that leaves the folder (%s).", shorten(name))
		default:
			out = append(out, p)
		}
	}
	clean := strings.Join(out, "/")
	if len(clean) > maxLen {
		return "", problem("The archive contains a file name that is too long.")
	}
	return clean, nil
}

// checkSymlink accepts a link only when it stays inside the folder: relative,
// any ".." only at the start and no deeper than the link itself.
func checkSymlink(name, target string) error {
	bad := problem("The archive contains a link that points outside the folder (%s).", shorten(name))
	if target == "" || len(target) > maxTrustedNameLen || strings.ContainsRune(target, 0) || strings.HasPrefix(target, "/") {
		return bad
	}
	depth := strings.Count(name, "/")
	ups, rest := 0, false
	for _, c := range strings.Split(target, "/") {
		switch c {
		case "", ".":
		case "..":
			if rest {
				return bad
			}
			ups++
		default:
			rest = true
		}
	}
	if ups > depth {
		return bad
	}
	return nil
}

func shorten(s string) string {
	if r := []rune(s); len(r) > 60 {
		return string(r[:57]) + "..."
	}
	return s
}

func parentsOf(name string) []string {
	var out []string
	for i := 0; i < len(name); i++ {
		if name[i] == '/' {
			out = append(out, name[:i])
		}
	}
	return out
}

// ctxReader fails reads once ctx is done.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// sanitizeTar copies src to dst (nil: only check) entry by entry. Only
// directories, regular files, symlinks and hardlinks are kept. dst is not closed.
func sanitizeTar(dst *tar.Writer, src *tar.Reader, lim tarLimits) (tarStats, error) {
	var st tarStats
	ctx := lim.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	maxEntries, maxName := maxTarEntries, maxTarNameLen
	if lim.Trusted {
		maxEntries, maxName = maxTrustedTarEntries, maxTrustedNameLen
	}
	var nameBytes int
	seen := map[string]struct{}{}    // strict only: duplicates
	links := map[string]struct{}{}   // symlinks (strict: also files): nothing may be placed below them
	files := map[string]struct{}{}   // strict only: regular files, the only valid hardlink targets
	skipped := map[string]struct{}{} // names left out (.wh.*): links to them are left out too
	addName := func(name string) error {
		if lim.Trusted {
			return nil // names are not kept in memory in this mode
		}
		if nameBytes += len(name); nameBytes > maxTarNameBytes {
			return problem("The archive has too many or too long file names.")
		}
		return nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return st, err
		}
		h, err := src.Next()
		if errors.Is(err, io.EOF) {
			return st, nil
		}
		if err != nil {
			if isCorrupt(err) {
				return st, problem("The file is damaged or is not a .tar.gz archive.")
			}
			return st, err
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		// Everything below counts, also what is left out: a million skipped
		// entries cost time and memory like any other.
		if st.Entries++; st.Entries > maxEntries {
			return st, problem("The archive has more than %d files.", maxEntries)
		}
		if h.Size < 0 {
			return st, problem("The archive is damaged: an entry has a negative size.")
		}
		if lim.MaxBytes > 0 && h.Size > lim.MaxBytes-st.Bytes {
			return st, errTarTooBig
		}
		st.Bytes += h.Size
		for k := range h.PAXRecords {
			if strings.HasPrefix(k, "GNU.sparse.") {
				return st, problem("The archive contains sparse files (%s), which are not supported.", shorten(h.Name))
			}
		}
		switch h.Typeflag {
		case tar.TypeReg, tar.TypeDir, tar.TypeSymlink, tar.TypeLink:
		case tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
			if lim.SkipSpecial {
				continue
			}
			return st, problem("The archive contains a device or special file (%s), which is not allowed.", shorten(h.Name))
		default:
			return st, problem("The archive contains an entry of a kind that is not supported (%s).", shorten(h.Name))
		}
		name, err := cleanTarName(h.Name, maxName)
		if err != nil {
			return st, err
		}
		if name == "" {
			if h.Typeflag == tar.TypeDir {
				continue // /data itself keeps its own owner and mode
			}
			return st, problem("The archive contains an entry without a name.")
		}
		if err := addName(name); err != nil {
			return st, err
		}
		if i := strings.LastIndexByte(name, '/'); strings.HasPrefix(name[i+1:], whiteoutPrefix) {
			if len(skipped) < maxSkippedNames {
				skipped[name] = struct{}{}
			}
			continue
		}
		if !lim.Trusted {
			if _, dup := seen[name]; dup {
				return st, problem("The archive contains the same path twice (%s).", shorten(name))
			}
			seen[name] = struct{}{}
		}
		for _, p := range parentsOf(name) {
			if _, bad := links[p]; bad {
				return st, problem("The archive puts a file below a link or file (%s).", shorten(name))
			}
		}

		nh := &tar.Header{
			Typeflag: h.Typeflag, Name: name, Mode: h.Mode & 0o1777, // no setuid or setgid
			Uid: h.Uid, Gid: h.Gid, ModTime: h.ModTime,
		}
		switch h.Typeflag {
		case tar.TypeDir:
			nh.Name += "/"
		case tar.TypeSymlink:
			if lim.Trusted {
				if h.Linkname == "" || len(h.Linkname) > maxTrustedNameLen || strings.ContainsRune(h.Linkname, 0) {
					return st, problem("The archive contains a link that is not valid (%s).", shorten(name))
				}
			} else if err := checkSymlink(name, h.Linkname); err != nil {
				return st, err
			}
			nh.Linkname = h.Linkname
			links[name] = struct{}{}
		case tar.TypeLink:
			target, err := cleanTarName(h.Linkname, maxName)
			if err != nil {
				return st, err
			}
			if _, gone := skipped[target]; gone {
				continue // a link to a file that was left out
			}
			if lim.Trusted {
				if target == "" {
					return st, problem("The archive contains a hard link to nothing (%s).", shorten(name))
				}
			} else {
				if _, ok := files[target]; !ok || target == "" {
					return st, problem("The archive contains a hard link to a file that is not in it (%s).", shorten(name))
				}
				links[name] = struct{}{}
			}
			nh.Linkname = target
		case tar.TypeReg:
			nh.Size = h.Size
			if !lim.Trusted {
				files[name] = struct{}{}
				links[name] = struct{}{}
			}
		}
		if dst != nil {
			if err := dst.WriteHeader(nh); err != nil {
				return st, err
			}
		}
		if h.Typeflag == tar.TypeReg {
			var w io.Writer = io.Discard
			if dst != nil {
				w = dst
			}
			if _, err := io.Copy(w, &ctxReader{ctx: ctx, r: src}); err != nil {
				if isCorrupt(err) {
					return st, problem("The file is damaged or is not a .tar.gz archive.")
				}
				return st, err
			}
		}
	}
}

// errTarTooBig means the files in the archive add up to more than allowed.
var errTarTooBig = errors.New("archive too big")
