package app

// Backups are plain .tar.gz files of the /data folder. Everything that goes in
// or comes out passes through sanitizeTar: it rewrites every entry into a
// minimal, safe header, so an uploaded archive can never plant a file outside
// /data, a device node, a setuid file or extended attributes.

import (
	"archive/tar"
	"compress/flate"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	maxTarEntries  = 300_000
	maxTarNameLen  = 4096
	whiteoutPrefix = ".wh." // Docker's archive code treats these as "delete this file"
)

// tarLimits bound what sanitizeTar accepts.
type tarLimits struct {
	MaxBytes    int64 // total size of all files in the archive; 0 means no limit
	SkipSpecial bool  // skip fifos and devices instead of refusing (reading a live volume)
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
func cleanTarName(name string) (string, error) {
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
	if len(clean) > maxTarNameLen {
		return "", problem("The archive contains a file name that is too long.")
	}
	return clean, nil
}

// checkSymlink accepts a link only when it stays inside the folder: relative,
// any ".." only at the start and no deeper than the link itself.
func checkSymlink(name, target string) error {
	bad := problem("The archive contains a link that points outside the folder (%s).", shorten(name))
	if target == "" || strings.ContainsRune(target, 0) || strings.HasPrefix(target, "/") {
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

// sanitizeTar copies src to dst (nil: only check) entry by entry. Only
// directories, regular files, symlinks and hardlinks are kept. dst is not closed.
func sanitizeTar(dst *tar.Writer, src *tar.Reader, lim tarLimits) (tarStats, error) {
	var st tarStats
	seen := map[string]struct{}{}
	links := map[string]struct{}{} // symlinks and regular files: nothing may be placed below them
	files := map[string]struct{}{} // regular files: the only valid hardlink targets
	for {
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
		switch h.Typeflag {
		case tar.TypeXGlobalHeader:
			continue
		case tar.TypeReg, tar.TypeDir, tar.TypeSymlink, tar.TypeLink:
		case tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
			if lim.SkipSpecial {
				continue
			}
			return st, problem("The archive contains a device or special file (%s), which is not allowed.", shorten(h.Name))
		default:
			return st, problem("The archive contains an entry of a kind that is not supported (%s).", shorten(h.Name))
		}
		name, err := cleanTarName(h.Name)
		if err != nil {
			return st, err
		}
		if name == "" {
			if h.Typeflag == tar.TypeDir {
				continue // /data itself keeps its own owner and mode
			}
			return st, problem("The archive contains an entry without a name.")
		}
		if i := strings.LastIndexByte(name, '/'); strings.HasPrefix(name[i+1:], whiteoutPrefix) {
			continue
		}
		if st.Entries++; st.Entries > maxTarEntries {
			return st, problem("The archive has more than %d files.", maxTarEntries)
		}
		if _, dup := seen[name]; dup {
			return st, problem("The archive contains the same path twice (%s).", shorten(name))
		}
		seen[name] = struct{}{}
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
			if err := checkSymlink(name, h.Linkname); err != nil {
				return st, err
			}
			nh.Linkname = h.Linkname
			links[name] = struct{}{}
		case tar.TypeLink:
			target, err := cleanTarName(h.Linkname)
			if err != nil {
				return st, err
			}
			if _, ok := files[target]; !ok || target == "" {
				return st, problem("The archive contains a hard link to a file that is not in it (%s).", shorten(name))
			}
			nh.Linkname = target
			links[name] = struct{}{}
		case tar.TypeReg:
			if lim.MaxBytes > 0 && st.Bytes+h.Size > lim.MaxBytes {
				return st, errTarTooBig
			}
			st.Bytes += h.Size
			nh.Size = h.Size
			files[name] = struct{}{}
			links[name] = struct{}{}
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
			if _, err := io.Copy(w, src); err != nil {
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
