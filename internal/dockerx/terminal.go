package dockerx

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strconv"
	"sync"
	"time"

	"github.com/moby/moby/client"
)

// terminalShell is the command of a terminal session: bash when the image
// has it, else sh. It first prints its own process id (inside the
// container) between pidMarker and a BEL, which OpenTerminal reads and
// removes, so Close can stop the shell: Docker has no call to kill an exec,
// and a shell whose client went away keeps waiting for input. exec keeps
// the process id, so it is the shell's.
var terminalShell = []string{"/bin/sh", "-c",
	`printf '\033]bitploy-pid;%s\007' "$$"; if [ -x /bin/bash ]; then exec /bin/bash; fi; exec /bin/sh`}

const pidMarker = "\x1b]bitploy-pid;"

// ErrNoShell is OpenTerminal's error for a container without /bin/sh
// (an image built FROM scratch or distroless).
var ErrNoShell = errors.New("the container has no /bin/sh")

// TermSize is the size of a terminal in characters.
type TermSize struct {
	Cols, Rows uint
}

// Terminal is an interactive shell with a TTY in a running container. Read
// gives what the shell writes, Write sends keystrokes. Close ends it.
type Terminal struct {
	c   *Client
	id  string // the exec's id
	ctr string // the container's name
	att client.HijackedResponse
	out *bufio.Reader
	pid int // the shell's process id in the container; 0 when unknown

	closeOnce sync.Once
}

// OpenTerminal starts a shell with a TTY of the given size in the running
// container name, as the container's user, with TERM set and env added.
func (c *Client) OpenTerminal(ctx context.Context, name string, size TermSize, env []string) (*Terminal, error) {
	cs := client.ConsoleSize{Height: size.Rows, Width: size.Cols}
	created, err := c.API.ExecCreate(ctx, name, client.ExecCreateOptions{
		TTY: true, ConsoleSize: cs,
		AttachStdin: true, AttachStdout: true, AttachStderr: true,
		Env: append([]string{"TERM=xterm-256color"}, env...),
		Cmd: terminalShell,
	})
	if err != nil {
		return nil, err
	}
	att, err := c.API.ExecAttach(ctx, created.ID, client.ExecAttachOptions{TTY: true, ConsoleSize: cs})
	if err != nil {
		return nil, err
	}
	t := &Terminal{c: c, id: created.ID, ctr: name, att: att.HijackedResponse, out: att.Reader}
	// The marker comes first and in one piece. Without it the shell did not
	// start: Docker writes why into the stream and gives the exec exit code
	// 126 or 127 when /bin/sh is missing or cannot run.
	_ = att.Conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	t.pid = readPIDMarker(t.out)
	_ = att.Conn.SetReadDeadline(time.Time{})
	if t.pid == 0 && t.startFailed(ctx) {
		t.att.Close()
		return nil, ErrNoShell
	}
	return t, nil
}

// startFailed reports whether the exec ended at once with Docker's code for
// a command it could not run (126 or 127). It waits up to a second.
func (t *Terminal) startFailed(ctx context.Context) bool {
	for i := 0; i < 10; i++ {
		if code, exited := t.Exit(ctx); exited {
			return code == 126 || code == 127
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(100 * time.Millisecond):
		}
	}
	return false
}

// readPIDMarker takes the shell's process id marker off the start of r. It
// returns 0 and leaves r as it was when the output does not start with it.
func readPIDMarker(r *bufio.Reader) int {
	b, err := r.Peek(len(pidMarker))
	if err != nil || string(b) != pidMarker {
		return 0
	}
	// Up to 10 digits and the BEL.
	for n := len(pidMarker) + 1; n <= len(pidMarker)+11; n++ {
		b, err := r.Peek(n)
		if err != nil {
			return 0
		}
		c := b[n-1]
		if c == '\a' {
			pid, err := strconv.Atoi(string(b[len(pidMarker) : n-1]))
			if err != nil || pid <= 0 {
				return 0
			}
			_, _ = r.Discard(n)
			return pid
		}
		if c < '0' || c > '9' {
			return 0
		}
	}
	return 0
}

// Read reads what the shell wrote to its terminal.
func (t *Terminal) Read(p []byte) (int, error) { return t.out.Read(p) }

// Write sends input to the shell.
func (t *Terminal) Write(p []byte) (int, error) { return t.att.Conn.Write(p) }

// Resize changes the size of the shell's terminal.
func (t *Terminal) Resize(ctx context.Context, size TermSize) error {
	_, err := t.c.API.ExecResize(ctx, t.id, client.ExecResizeOptions{Height: size.Rows, Width: size.Cols})
	return err
}

// Exit reports the shell's exit code once it has exited.
func (t *Terminal) Exit(ctx context.Context) (code int, exited bool) {
	res, err := t.c.API.ExecInspect(ctx, t.id, client.ExecInspectOptions{})
	if err != nil || res.Running {
		return 0, false
	}
	return res.ExitCode, true
}

// Close ends the session: it closes the streams and, when the shell still
// runs, sends it SIGHUP (the kernel passes that on to the program in the
// foreground, bash to its jobs) and SIGKILL 2 seconds later if it is still
// there. It waits for that, at most 10 seconds.
func (t *Terminal) Close() error {
	var err error
	t.closeOnce.Do(func() {
		t.att.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if t.pid <= 0 {
			return
		}
		res, ierr := t.c.API.ExecInspect(ctx, t.id, client.ExecInspectOptions{})
		if IsNotFound(ierr) || (ierr == nil && !res.Running) {
			return // exited, or went with its container (a redeploy removed it)
		}
		p := strconv.Itoa(t.pid)
		script := "kill -HUP " + p + " 2>/dev/null || exit 0; sleep 2; kill -0 " + p + " 2>/dev/null && kill -KILL " + p + " 2>/dev/null; exit 0"
		_, err = t.c.Exec(ctx, t.ctr, []string{"/bin/sh", "-c", script}, ExecOptions{})
		var ee *ExecError
		if errors.As(err, &ee) || IsNotFound(err) || IsConflict(err) {
			err = nil // the shell is gone either way (a stopped container answers conflict)
		}
	})
	return err
}

var _ io.ReadWriteCloser = (*Terminal)(nil)
