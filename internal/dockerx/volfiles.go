package dockerx

// A small file browser for the /data volume of an app. Everything runs in
// throwaway busybox helper containers that mount only that volume (never in
// the app container, which may have no shell). Paths are relative to /data,
// already cleaned by the caller; they reach the shell scripts below as
// arguments, never as part of the script text.

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"github.com/AVMG20/bitploy/internal/ids"
)

// What the helper scripts can say about a path. They are the exit codes 3 to 7.
var (
	ErrNotDir     = errors.New("the path is not a folder")
	ErrNotFound   = errors.New("there is no such file or folder")
	ErrLinkInPath = errors.New("the path goes through a symbolic link")
	ErrExists     = errors.New("a file or folder with this name exists already")
	ErrWrongKind  = errors.New("the path is not a regular file")
)

// FileEntry is one item of a folder in /data.
type FileEntry struct {
	Name  string
	Kind  string // file | dir | symlink | other
	Size  int64
	Mtime time.Time
}

// DirListing is the content of one folder, sorted folders first.
type DirListing struct {
	Entries   []FileEntry
	Truncated bool // there are more items than the entries returned
}

// filesPrelude is shared by all scripts. chk exits 5 when a part of the path
// $1 (relative to /data) is a symbolic link: nothing is ever read or written
// through a link, so a link in /data can not lead out of the volume.
const filesPrelude = `set -f
chk() ( p=/data; IFS=/; for c in $1; do p="$p/$c"; if [ -L "$p" ]; then exit 5; fi; done )
`

// $1 folder, $2 how many lines to print at most.
const listScript = filesPrelude + `chk "$1" || exit 5
d=/data${1:+/$1}
[ -e "$d" ] || exit 4
[ -d "$d" ] || exit 3
find "$d" -mindepth 1 -maxdepth 1 -exec stat -c '%f %s %Y %n' {} + | head -n "$2"
`

// $1 temporary file in /data, $2 folder, $3 name, $4 1 = replace an existing file.
// The new file gets the owner of the file it replaces, or else of the folder; a
// folder of root is world writable (like a restored backup), so the app can use it.
const putScript = filesPrelude + `t="/data/$1"
trap 'rm -f "$t"' EXIT
chk "$2" || exit 5
d=/data${2:+/$2}
[ -e "$d" ] || exit 4
[ -d "$d" ] || exit 3
f="$d/$3"
if [ -e "$f" ] || [ -L "$f" ]; then
  [ "$4" = 1 ] || exit 6
fi
if [ -f "$f" ] && [ ! -L "$f" ]; then
  o=$(stat -c %u:%g "$f"); m=$(stat -c %a "$f")
elif [ -e "$f" ] && [ ! -L "$f" ]; then
  exit 7
else
  o=$(stat -c %u:%g "$d"); m=644; [ "$o" = 0:0 ] && m=666
fi
chown "$o" "$t" && chmod "$m" "$t" && mv -f "$t" "$f"
`

// $1 folder, $2 name of the new folder.
const mkdirScript = filesPrelude + `chk "$1" || exit 5
p=/data${1:+/$1}
[ -e "$p" ] || exit 4
[ -d "$p" ] || exit 3
n="$p/$2"
if [ -e "$n" ] || [ -L "$n" ]; then exit 6; fi
o=$(stat -c %u:%g "$p"); m=755; [ "$o" = 0:0 ] && m=777
mkdir -m "$m" "$n" && chown "$o" "$n"
`

// $1 folder, $2 name. A symbolic link is removed, not what it points to.
const removeScript = filesPrelude + `chk "$1" || exit 5
p=/data${1:+/$1}
[ -d "$p" ] || exit 4
n="$p/$2"
if [ ! -e "$n" ] && [ ! -L "$n" ]; then exit 4; fi
rm -rf "$n"
`

// createFilesHelper creates (does not start) a helper like createHelper. It
// also pins the json-file log driver, because the scripts report through their
// output and not every Docker setup can read the logs of another driver.
func (c *Client) createFilesHelper(ctx context.Context, volume string, readOnly bool, cmd []string) (string, error) {
	if ok, err := c.VolumeExists(ctx, volume); err != nil {
		return "", err
	} else if !ok {
		return "", ErrNoVolume
	}
	if err := c.ensureInitImage(ctx); err != nil {
		return "", err
	}
	name := HelperPrefix + ids.Hex(10)
	pids, mem := int64(32), int64(64<<20)
	_, err := c.API.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: name,
		Config: &container.Config{
			Image: VolumeInitImage, User: "0", Cmd: cmd, Labels: c.initLabels(),
		},
		HostConfig: &container.HostConfig{
			NetworkMode: "none", CapDrop: []string{"ALL"}, CapAdd: helperCaps,
			SecurityOpt: []string{"no-new-privileges"},
			LogConfig:   container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "2m", "max-file": "1"}},
			Resources:   container.Resources{Memory: mem, MemorySwap: mem, PidsLimit: &pids},
			Mounts:      []mount.Mount{{Type: mount.TypeVolume, Source: volume, Target: DataMount, ReadOnly: readOnly}},
		},
	})
	if err != nil {
		return "", err
	}
	return name, nil
}

// filesTimeout bounds a helper that runs a script.
const filesTimeout = 10 * time.Minute

// runCreated starts the (created) helper, waits for it and maps the exit code
// of the script to the errors above. It returns what the script printed.
func (c *Client) runCreated(ctx context.Context, name string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, filesTimeout)
	defer cancel()
	if err := c.StartContainer(ctx, name); err != nil {
		return "", err
	}
	w := c.API.ContainerWait(ctx, name, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	var code int64
	select {
	case res := <-w.Result:
		code = res.StatusCode
	case err := <-w.Error:
		return "", err
	}
	stdout, stderr := c.helperOutput(ctx, name)
	switch code {
	case 0:
		return stdout, nil
	case 3:
		return "", ErrNotDir
	case 4:
		return "", ErrNotFound
	case 5:
		return "", ErrLinkInPath
	case 6:
		return "", ErrExists
	case 7:
		return "", ErrWrongKind
	}
	msg := strings.TrimSpace(stderr)
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return "", fmt.Errorf("helper container exited with status %d: %s", code, msg)
}

// helperOutput reads what a finished helper printed (at most 8 MB).
func (c *Client) helperOutput(ctx context.Context, name string) (stdout, stderr string) {
	rc, err := c.API.ContainerLogs(ctx, name, client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		return "", ""
	}
	defer rc.Close()
	var so, se bytes.Buffer
	_, _ = stdcopy.StdCopy(&so, &se, io.LimitReader(rc, 8<<20))
	return so.String(), se.String()
}

// runScript runs a script in a helper container and removes the helper.
func (c *Client) runScript(ctx context.Context, volume string, readOnly bool, script string, args ...string) (string, error) {
	cmd := append([]string{"sh", "-c", script, "sh"}, args...)
	name, err := c.createFilesHelper(ctx, volume, readOnly, cmd)
	if err != nil {
		return "", err
	}
	defer func() { _ = c.RemoveContainer(context.WithoutCancel(ctx), name) }()
	return c.runCreated(ctx, name)
}

// ListVolumeDir lists the folder rel (relative to /data, "" is /data itself).
// It returns at most max entries.
func (c *Client) ListVolumeDir(ctx context.Context, volume, rel string, max int) (DirListing, error) {
	out, err := c.runScript(ctx, volume, true, listScript, rel, strconv.Itoa(max+1))
	if err != nil {
		return DirListing{}, err
	}
	return parseListing(out, rel, max), nil
}

// parseListing reads the output of listScript: "<hex mode> <size> <mtime> <path>"
// per line. Lines that do not fit (a file name with a line break in it) are skipped.
func parseListing(out, rel string, max int) DirListing {
	prefix := "/data/"
	if rel != "" {
		prefix = "/data/" + rel + "/"
	}
	var l DirListing
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(line, " ", 4)
		if len(f) != 4 || !strings.HasPrefix(f[3], prefix) {
			continue
		}
		name := f[3][len(prefix):]
		if name == "" || strings.Contains(name, "/") {
			continue
		}
		mode, err1 := strconv.ParseUint(f[0], 16, 32)
		size, err2 := strconv.ParseInt(f[1], 10, 64)
		mtime, err3 := strconv.ParseInt(f[2], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		e := FileEntry{Name: name, Size: size, Mtime: time.Unix(mtime, 0).UTC()}
		switch mode & 0xF000 {
		case 0x4000:
			e.Kind, e.Size = "dir", 0
		case 0x8000:
			e.Kind = "file"
		case 0xA000:
			e.Kind, e.Size = "symlink", 0
		default:
			e.Kind, e.Size = "other", 0
		}
		l.Entries = append(l.Entries, e)
	}
	if len(l.Entries) > max {
		l.Entries, l.Truncated = l.Entries[:max], true
	}
	sort.SliceStable(l.Entries, func(i, j int) bool {
		a, b := l.Entries[i], l.Entries[j]
		if (a.Kind == "dir") != (b.Kind == "dir") {
			return a.Kind == "dir"
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return l
}

// VolumeFile is one regular file of the volume, being read.
type VolumeFile struct {
	Size    int64
	ModTime time.Time
	r       io.Reader
	closeFn func()
}

func (f *VolumeFile) Read(p []byte) (int, error) { return f.r.Read(p) }

// Close ends the read and removes the helper container.
func (f *VolumeFile) Close() error {
	f.closeFn()
	return nil
}

// OpenVolumeFile opens the regular file rel for reading. A folder, a symbolic
// link or a special file gives ErrWrongKind, a missing one ErrNotFound. The app
// is not paused, so a file that is being written may be cut off or inconsistent.
func (c *Client) OpenVolumeFile(ctx context.Context, volume, rel string) (*VolumeFile, error) {
	name, err := c.createFilesHelper(ctx, volume, true, []string{"true"})
	if err != nil {
		return nil, err
	}
	cleanup := func() { _ = c.RemoveContainer(context.WithoutCancel(ctx), name) }
	// Like chk in the scripts: no folder on the way may be a symbolic link.
	// The daemon looks at the last part of a path without following it, and
	// the parts before it were checked one step earlier.
	parts := strings.Split(rel, "/")
	for i := 1; i < len(parts); i++ {
		st, err := c.API.ContainerStatPath(ctx, name, client.ContainerStatPathOptions{Path: DataMount + "/" + strings.Join(parts[:i], "/")})
		if err != nil {
			cleanup()
			if IsNotFound(err) {
				return nil, ErrNotFound
			}
			return nil, err
		}
		if st.Stat.Mode&os.ModeSymlink != 0 {
			cleanup()
			return nil, ErrLinkInPath
		}
		if !st.Stat.Mode.IsDir() {
			cleanup()
			return nil, ErrNotFound
		}
	}
	src := DataMount + "/" + rel
	st, err := c.API.ContainerStatPath(ctx, name, client.ContainerStatPathOptions{Path: src})
	if err != nil {
		cleanup()
		if IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if !st.Stat.Mode.IsRegular() {
		cleanup()
		return nil, ErrWrongKind
	}
	res, err := c.API.CopyFromContainer(ctx, name, client.CopyFromContainerOptions{SourcePath: src})
	if err != nil {
		cleanup()
		if IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	tr := tar.NewReader(res.Content)
	h, err := tr.Next()
	if err != nil || h.Typeflag != tar.TypeReg {
		_ = res.Content.Close()
		cleanup()
		if err == nil {
			err = ErrWrongKind
		}
		return nil, err
	}
	return &VolumeFile{Size: h.Size, ModTime: h.ModTime, r: tr, closeFn: func() { _ = res.Content.Close(); cleanup() }}, nil
}

// PutVolumeFile writes size bytes from body as the file name in the folder dir.
// An existing file is only replaced with overwrite. The data goes to a
// temporary file first and is renamed at the end, so a failed upload never
// leaves half a file behind.
func (c *Client) PutVolumeFile(ctx context.Context, volume, dir, name string, size int64, body io.Reader, overwrite bool) error {
	tmp := ".bp-upload-" + ids.Hex(8)
	ow := "0"
	if overwrite {
		ow = "1"
	}
	helper, err := c.createFilesHelper(ctx, volume, false, []string{"sh", "-c", putScript, "sh", tmp, dir, name, ow})
	if err != nil {
		return err
	}
	defer func() { _ = c.RemoveContainer(context.WithoutCancel(ctx), helper) }()

	pr, pw := io.Pipe()
	go func() {
		tw := tar.NewWriter(pw)
		err := tw.WriteHeader(&tar.Header{Name: tmp, Typeflag: tar.TypeReg, Mode: 0o600, Size: size, ModTime: time.Now()})
		if err == nil {
			var n int64
			if n, err = io.Copy(tw, body); err == nil && n != size {
				err = io.ErrUnexpectedEOF
			}
		}
		if err == nil {
			err = tw.Close()
		}
		_ = pw.CloseWithError(err)
	}()
	_, err = c.API.CopyToContainer(ctx, helper, client.CopyToContainerOptions{DestinationPath: DataMount, Content: pr})
	_ = pr.Close()
	if err != nil {
		// The daemon may have unpacked part of the file: the script never ran to clean it up.
		_, _ = c.runScript(context.WithoutCancel(ctx), volume, false, removeScript, "", tmp)
		return fmt.Errorf("copying the file into the volume: %w", err)
	}
	// The file is in the volume now: let the script finish (it moves the file
	// into place or removes it) even when the caller has gone away.
	_, err = c.runCreated(context.WithoutCancel(ctx), helper)
	if err != nil && !scriptRefused(err) {
		// The script may not have run at all (the helper did not start).
		_, _ = c.runScript(context.WithoutCancel(ctx), volume, false, removeScript, "", tmp)
	}
	return err
}

// scriptRefused reports an error that a helper script gave on purpose: it ran
// to its end and cleaned up after itself.
func scriptRefused(err error) bool {
	for _, e := range []error{ErrNotDir, ErrNotFound, ErrLinkInPath, ErrExists, ErrWrongKind} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

// MkdirVolume creates the folder name inside the existing folder dir.
func (c *Client) MkdirVolume(ctx context.Context, volume, dir, name string) error {
	_, err := c.runScript(ctx, volume, false, mkdirScript, dir, name)
	return err
}

// RemoveVolumePath deletes the file, link or folder (with everything in it)
// name in the folder dir.
func (c *Client) RemoveVolumePath(ctx context.Context, volume, dir, name string) error {
	_, err := c.runScript(ctx, volume, false, removeScript, dir, name)
	return err
}
