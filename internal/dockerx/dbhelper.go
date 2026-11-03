package dockerx

// The SQLite viewer runs SQLite in throwaway helper containers, like the file
// browser runs its scripts, but with a program of its own: `bitploy db-helper`
// (internal/dbview). The image for it is built here, once, from busybox and
// the bitploy binary (a static Linux program), and tagged with a hash of that
// binary so that a new version of bitploy builds a new image by itself.

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"github.com/AVMG20/bitploy/internal/ids"
)

const (
	// DBHelperImage is the repository of the helper image; the tag is a hash of the binary.
	DBHelperImage = "bitploy/db-helper"
	// LabelDBHelper marks the helper image (and its containers) as bitploy's own.
	LabelDBHelper = "nl.bitploy.db_helper"

	dbHelperBinary = "/bitploy"
	dbHelperReq    = "/req.json"
	// dbHelperTimeout is how long a helper may run. The program stops itself
	// after 15 seconds; this is the backstop that kills the container.
	dbHelperTimeout = 20 * time.Second
	// dbHelperOutputMax is how much output is read from a helper.
	dbHelperOutputMax = 12 << 20
)

// What can go wrong with the database helper besides what it answers itself.
var (
	// ErrNoDBHelper: there is no Linux binary to put in the helper image.
	ErrNoDBHelper = errors.New("there is no Linux bitploy binary to run the database helper with")
	// ErrDBTimeout: the helper did not finish in time and was killed.
	ErrDBTimeout = errors.New("the database helper took too long and was stopped")
	// ErrDBOutOfMemory: the helper used more memory than it may.
	ErrDBOutOfMemory = errors.New("the database helper ran out of memory")
)

// dbHelperState caches the hash of the helper binary, and serializes building the image.
type dbHelperState struct {
	mu    sync.Mutex
	path  string
	size  int64
	mtime time.Time
	tag   string // the image tag the binary above gives
}

// dbHelperBin is the file to put in the helper image: the setting
// (BITPLOY_DB_HELPER_BIN, for a bitploy that does not run on Linux, like dev
// on a Mac), else the running binary itself when that is a Linux one.
func (c *Client) dbHelperBin() (string, error) {
	p := c.DBHelperBin
	if p == "" {
		if runtime.GOOS != "linux" {
			return "", fmt.Errorf("%w: set BITPLOY_DB_HELPER_BIN to a bitploy binary built for Linux", ErrNoDBHelper)
		}
		var err error
		if p, err = os.Executable(); err != nil {
			return "", fmt.Errorf("%w: %v", ErrNoDBHelper, err)
		}
	}
	f, err := os.Open(p)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNoDBHelper, err)
	}
	defer f.Close()
	magic := make([]byte, 4)
	if _, err := io.ReadFull(f, magic); err != nil || string(magic) != "\x7fELF" {
		return "", fmt.Errorf("%w: %s is not a Linux program (build it with GOOS=linux CGO_ENABLED=0)", ErrNoDBHelper, p)
	}
	return p, nil
}

// ensureDBHelperImage makes sure the helper image for the current binary
// exists, and returns its name. Older helper images are removed.
func (c *Client) ensureDBHelperImage(ctx context.Context) (string, error) {
	bin, err := c.dbHelperBin()
	if err != nil {
		return "", err
	}
	st, err := os.Stat(bin)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNoDBHelper, err)
	}
	h := &c.dbh
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.path != bin || h.size != st.Size() || !h.mtime.Equal(st.ModTime()) || h.tag == "" {
		sum, err := fileHash(bin)
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrNoDBHelper, err)
		}
		h.path, h.size, h.mtime, h.tag = bin, st.Size(), st.ModTime(), sum[:16]
	}
	ref := DBHelperImage + ":" + h.tag
	if _, err := c.API.ImageInspect(ctx, ref); err == nil {
		return ref, nil
	} else if !IsNotFound(err) {
		return "", err
	}
	if err := c.buildDBHelperImage(ctx, bin, ref); err != nil {
		return "", fmt.Errorf("building the database helper image: %w", err)
	}
	c.removeOldDBHelperImages(ctx, ref)
	return ref, nil
}

func fileHash(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// buildDBHelperImage makes the image: a container from busybox with the binary
// copied in, committed under ref.
func (c *Client) buildDBHelperImage(ctx context.Context, bin, ref string) error {
	if err := c.ensureInitImage(ctx); err != nil {
		return err
	}
	f, err := os.Open(bin)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	labels := c.initLabels()
	labels[LabelDBHelper] = "1"
	name := HelperPrefix + ids.Hex(10)
	if _, err := c.API.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name:   name,
		Config: &container.Config{Image: VolumeInitImage, Cmd: []string{"true"}, Labels: labels},
	}); err != nil {
		return err
	}
	defer func() { _ = c.RemoveContainer(context.WithoutCancel(ctx), name) }()

	pr, pw := io.Pipe()
	go func() {
		tw := tar.NewWriter(pw)
		err := tw.WriteHeader(&tar.Header{Name: strings.TrimPrefix(dbHelperBinary, "/"), Typeflag: tar.TypeReg, Mode: 0o755, Size: st.Size(), ModTime: time.Now()})
		if err == nil {
			_, err = io.Copy(tw, f)
		}
		if err == nil {
			err = tw.Close()
		}
		_ = pw.CloseWithError(err)
	}()
	_, err = c.API.CopyToContainer(ctx, name, client.CopyToContainerOptions{DestinationPath: "/", Content: pr})
	_ = pr.Close()
	if err != nil {
		return err
	}
	_, err = c.API.ContainerCommit(ctx, name, client.ContainerCommitOptions{
		Reference: ref, Comment: "bitploy database helper", NoPause: true,
		Config: &container.Config{Labels: labels},
	})
	return err
}

// removeOldDBHelperImages removes helper images of earlier bitploy versions.
// One that is in use is left (the removal is refused) and goes next time.
func (c *Client) removeOldDBHelperImages(ctx context.Context, keep string) {
	f, err := c.ownedFilter(LabelDBHelper + "=1")
	if err != nil {
		return
	}
	res, err := c.API.ImageList(ctx, client.ImageListOptions{Filters: f})
	if err != nil {
		return
	}
	for _, im := range res.Items {
		if !c.owned(im.Labels) {
			continue
		}
		for _, t := range im.RepoTags {
			if t != keep && strings.HasPrefix(t, DBHelperImage+":") {
				_ = c.RemoveImage(ctx, t)
			}
		}
	}
}

// RunDBHelper runs `bitploy db-helper` in a fresh locked-down container that
// mounts only the volume, with the JSON request, and returns what it printed.
// The volume is mounted read-write: SQLite needs that even to read a database
// in WAL mode (the helper makes the database read-only for reads itself). The
// helper is root only to look at the owner of the file and then becomes that
// user, so it never creates files the app can not use. The container has no
// network and is removed at the end, also when the helper has to be killed.
func (c *Client) RunDBHelper(ctx context.Context, volume string, request []byte) ([]byte, error) {
	if ok, err := c.VolumeExists(ctx, volume); err != nil {
		return nil, err
	} else if !ok {
		return nil, ErrNoVolume
	}
	image, err := c.ensureDBHelperImage(ctx)
	if err != nil {
		return nil, err
	}
	labels := c.initLabels()
	labels[LabelDBHelper] = "1"
	name := HelperPrefix + ids.Hex(10)
	pids, mem := int64(64), int64(256<<20)
	_, err = c.API.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: name,
		Config: &container.Config{
			Image: image, User: "0", Labels: labels,
			Cmd: []string{dbHelperBinary, "db-helper", dbHelperReq},
			Env: []string{"GOMAXPROCS=2", "GOMEMLIMIT=192MiB"},
		},
		HostConfig: &container.HostConfig{
			NetworkMode: "none",
			// SETUID and SETGID to become the owner of the file; the helper gives
			// them up itself right after (and checks). A user other than root loses
			// all of them; for a database of root CHOWN and FOWNER stay.
			CapDrop:     []string{"ALL"},
			CapAdd:      append([]string{"SETUID", "SETGID"}, helperCaps...),
			SecurityOpt: []string{"no-new-privileges"},
			LogConfig:   container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "16m", "max-file": "1"}},
			Resources:   container.Resources{Memory: mem, MemorySwap: mem, PidsLimit: &pids, NanoCPUs: 1_000_000_000},
			Tmpfs:       map[string]string{"/tmp": "size=64m,mode=1777"},
			Mounts:      []mount.Mount{{Type: mount.TypeVolume, Source: volume, Target: DataMount}},
		},
	})
	if err != nil {
		return nil, err
	}
	// Whatever happens, the container goes (a running one is killed).
	defer func() { _ = c.RemoveContainer(context.WithoutCancel(ctx), name) }()

	var tb bytes.Buffer
	tw := tar.NewWriter(&tb)
	if err := tw.WriteHeader(&tar.Header{Name: strings.TrimPrefix(dbHelperReq, "/"), Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(request)), ModTime: time.Now()}); err != nil {
		return nil, err
	}
	if _, err := tw.Write(request); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if _, err := c.API.CopyToContainer(ctx, name, client.CopyToContainerOptions{DestinationPath: "/", Content: &tb}); err != nil {
		return nil, err
	}

	wctx, cancel := context.WithTimeout(ctx, dbHelperTimeout)
	defer cancel()
	if err := c.StartContainer(wctx, name); err != nil {
		return nil, ctxOr(wctx, ctx, err)
	}
	w := c.API.ContainerWait(wctx, name, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	var code int64
	select {
	case res := <-w.Result:
		code = res.StatusCode
	case err := <-w.Error:
		return nil, ctxOr(wctx, ctx, err)
	}
	octx := context.WithoutCancel(ctx)
	stdout, stderr, err := c.helperOutputMax(octx, name, dbHelperOutputMax)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		if st, err := c.InspectContainer(octx, name); err == nil && st.State != nil && st.State.OOMKilled {
			return nil, ErrDBOutOfMemory
		}
		msg := strings.TrimSpace(stderr)
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("database helper exited with status %d: %s", code, msg)
	}
	return []byte(stdout), nil
}

// ctxOr tells a helper that ran out of time (killed by the caller's deferred
// removal) from a caller that gave up.
func ctxOr(wctx, ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if wctx.Err() != nil {
		return ErrDBTimeout
	}
	return err
}

// helperOutputMax reads what a finished helper printed, at most max bytes of stdout.
func (c *Client) helperOutputMax(ctx context.Context, name string, max int64) (stdout, stderr string, err error) {
	rc, err := c.API.ContainerLogs(ctx, name, client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		return "", "", err
	}
	defer rc.Close()
	var so, se bytes.Buffer
	if _, err := stdcopy.StdCopy(&limitWriter{w: &so, left: max}, &limitWriter{w: &se, left: 64 << 10}, rc); err != nil {
		return "", "", err
	}
	return so.String(), se.String(), nil
}

// limitWriter keeps the first bytes and drops the rest, without an error.
type limitWriter struct {
	w    io.Writer
	left int64
}

func (l *limitWriter) Write(p []byte) (int, error) {
	n := len(p)
	if int64(len(p)) > l.left {
		p = p[:l.left]
	}
	l.left -= int64(len(p))
	if len(p) > 0 {
		if _, err := l.w.Write(p); err != nil {
			return 0, err
		}
	}
	return n, nil
}
