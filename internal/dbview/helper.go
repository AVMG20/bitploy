package dbview

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// DataRoot is where the helper container mounts the volume.
const DataRoot = "/data"

// HelperOptions tune Handle: tests point Root at a temp folder and leave Drop
// out, the helper container uses the defaults of HelperMain.
type HelperOptions struct {
	Root string                      // where /data is
	Drop func(uid, gid uint32) error // become the owner of the database file; nil: stay as we are
}

// HelperMain is `bitploy db-helper <request file>` (or `-` for standard input):
// it reads one JSON request, does it and prints one JSON response. Every
// failure that has to do with the request is in the response; the exit status
// is only not 0 when the helper can not even do that.
//
// `bitploy db-helper --control <database file> <request file | ->` is the
// helper of the admin viewer (control.go): the same, for bitploy's own database,
// read-only and with the secrets hidden.
func HelperMain(args []string, in io.Reader, out io.Writer) int {
	control := ""
	if len(args) == 3 && args[0] == "--control" {
		control, args = args[1], args[2:]
	}
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: bitploy db-helper [--control <database file>] <request.json | ->")
		return 2
	}
	var r io.Reader = in
	if args[0] != "-" {
		f, err := os.Open(args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, "db-helper:", err)
			return 2
		}
		defer f.Close()
		r = f
	}
	raw, err := io.ReadAll(io.LimitReader(r, MaxRequest+1))
	if err != nil {
		fmt.Fprintln(os.Stderr, "db-helper:", err)
		return 2
	}
	var resp Response
	if control != "" {
		// A process of bitploy's own user: it keeps no power to change user, and
		// (on Linux) may not use more than ControlMemory.
		if err := dropPrivileges(uint32(os.Geteuid()), uint32(os.Getegid())); err != nil {
			fmt.Fprintln(os.Stderr, "db-helper:", err)
			return 2
		}
		limitMemory(ControlMemory)
		resp = HandleControl(context.Background(), raw, control)
	} else {
		resp = Handle(context.Background(), raw, HelperOptions{Root: DataRoot, Drop: dropPrivileges})
	}
	b, err := encode(resp)
	if err != nil {
		fmt.Fprintln(os.Stderr, "db-helper:", err)
		return 2
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		return 2
	}
	return 0
}

// Handle does one request, given as JSON, and returns the response.
func Handle(ctx context.Context, raw []byte, opts HelperOptions) Response {
	if len(raw) > MaxRequest {
		return Response{Error: errf("request_too_large", "The request is too big.", "Send fewer or smaller values.")}
	}
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return Response{Error: errf("request_invalid", "The request could not be read: "+err.Error(), "")}
	}
	if req.Server != nil {
		return runServer(ctx, req)
	}
	file, uid, gid, e := resolve(opts.Root, req.Path)
	if e != nil {
		return Response{Error: e}
	}
	if opts.Drop != nil {
		// The database is opened as the user that owns the file, so that the
		// -wal and -shm files SQLite makes next to it belong to the app too:
		// root-owned ones would lock the app out of its own database.
		if err := opts.Drop(uid, gid); err != nil {
			return Response{Error: errf("db_failed", "The helper could not become the owner of the database: "+err.Error(), "")}
		}
	}
	return Run(ctx, file, req)
}

// resolve turns the path of the request into a file below root, and refuses
// a path that goes through a symbolic link or does not end at a regular file.
// The same rule as the file browser: nothing is opened through a link.
func resolve(root, rel string) (file string, uid, gid uint32, e *Error) {
	if rel == "" || strings.ContainsRune(rel, 0) {
		return "", 0, 0, errf("path_invalid", "There is no path to a database.", "Use a path inside /data, like app.db.")
	}
	rel = strings.TrimPrefix(path.Clean("/"+rel), "/")
	segs := strings.Split(rel, "/")
	cur := root
	for i, seg := range segs {
		if seg == ".." || seg == "" {
			return "", 0, 0, errf("path_invalid", `A path may not contain "..".`, "Use a path inside /data, like app.db.")
		}
		cur = filepath.Join(cur, seg)
		st, err := os.Lstat(cur)
		switch {
		case err != nil && os.IsNotExist(err):
			return "", 0, 0, errf("db_not_found", "There is no such file in /data.", "Reload the list of files: it may have been deleted.")
		case err != nil:
			return "", 0, 0, fileErr(err)
		case st.Mode()&os.ModeSymlink != 0:
			return "", 0, 0, errf("file_symlink", "This path goes through a symbolic link. bitploy does not follow links in /data.", "Use the real file, not the link.")
		case i < len(segs)-1 && !st.IsDir():
			return "", 0, 0, errf("db_not_found", "There is no such file in /data.", "Reload the list of files: it may have been deleted.")
		case i == len(segs)-1:
			if !st.Mode().IsRegular() {
				return "", 0, 0, errf("not_a_file", "Only a regular file can be opened as a database, not a folder or a special file.", "Pick a file.")
			}
			uid, gid = ownerOf(st)
		}
	}
	return cur, uid, gid, nil
}
