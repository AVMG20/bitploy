package dockerx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
)

// ExecOptions are the streams and settings of a command run in a running
// container (Exec).
type ExecOptions struct {
	// User runs the command as user[:group]; "" is the container's user.
	User string
	// Env are extra KEY=value variables on top of the container's own. They
	// are not part of the command line, so they do not show up in ps.
	Env []string
	// Stdin is sent to the command and closed at its end; nil sends nothing.
	Stdin io.Reader
	// Stdout receives the command's output; nil throws it away. A write
	// error stops the command's streams and is returned.
	Stdout io.Writer
}

// ExecError is a command that ran and exited with a code other than 0.
// Tail is the end of what it wrote to stderr.
type ExecError struct {
	Code int
	Tail string
}

func (e *ExecError) Error() string {
	if e.Tail == "" {
		return fmt.Sprintf("the command exited with code %d", e.Code)
	}
	return fmt.Sprintf("the command exited with code %d: %s", e.Code, e.Tail)
}

// execTailBytes is how much of stderr an ExecError keeps.
const execTailBytes = 4 << 10

// Exec runs cmd inside the running container name and waits for it. It
// returns the end of stderr (also on success: tools warn there) and an
// *ExecError when the command exited with a non-zero code. When ctx ends the
// streams are closed and ctx's error is returned; Docker has no way to kill
// an exec, but a command whose stdin and stdout are gone stops soon after.
func (c *Client) Exec(ctx context.Context, name string, cmd []string, o ExecOptions) (string, error) {
	created, err := c.API.ExecCreate(ctx, name, client.ExecCreateOptions{
		User: o.User, Env: o.Env, Cmd: cmd,
		AttachStdin: o.Stdin != nil, AttachStdout: true, AttachStderr: true,
	})
	if err != nil {
		return "", err
	}
	att, err := c.API.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return "", err
	}
	var closeOnce sync.Once
	closeConn := func() { closeOnce.Do(att.Close) }
	defer closeConn()
	stop := context.AfterFunc(ctx, closeConn)
	defer stop()

	stdout := o.Stdout
	if stdout == nil {
		stdout = io.Discard
	}
	tail := &TailBuffer{Max: execTailBytes}
	// Only a failure to read Stdin matters: the command closing its end
	// early (it stopped on an error) shows up in its exit code. A read
	// failure must not look like the end of the input, so the connection is
	// closed instead of half-closed then.
	src := &srcReader{r: o.Stdin}
	inDone := make(chan struct{})
	if o.Stdin != nil {
		go func() {
			defer close(inDone)
			_, _ = io.Copy(att.Conn, src)
			if src.err != nil {
				closeConn()
				return
			}
			_ = att.CloseWrite()
		}()
	} else {
		close(inDone)
	}
	// Stdin is the caller's: the copier must be done with it on return.
	defer func() {
		closeConn()
		<-inDone
	}()
	ow := &errWriter{w: stdout}
	_, copyErr := stdcopy.StdCopy(ow, tail, att.Reader)
	// The output has ended (the command has, or nearly has, exited), or one
	// side failed: stop both streams and let the stdin copier finish.
	closeConn()
	<-inDone
	switch {
	case ow.err != nil: // our side failed (no room left, say)
		return tail.String(), ow.err
	case src.err != nil:
		return tail.String(), src.err
	case ctx.Err() != nil:
		return tail.String(), ctx.Err()
	case copyErr != nil:
		return tail.String(), copyErr
	}
	code, err := c.execExitCode(ctx, created.ID)
	if err != nil {
		return tail.String(), err
	}
	if code != 0 {
		return tail.String(), &ExecError{Code: code, Tail: tail.String()}
	}
	return tail.String(), nil
}

// execExitCode waits (briefly) until Docker has the exit code of an exec
// whose streams have ended.
func (c *Client) execExitCode(ctx context.Context, id string) (int, error) {
	for i := 0; ; i++ {
		res, err := c.API.ExecInspect(ctx, id, client.ExecInspectOptions{})
		if err != nil {
			return 0, err
		}
		if !res.Running {
			return res.ExitCode, nil
		}
		if i >= 100 {
			return 0, errors.New("the command did not exit after its output ended")
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// ContainerRunning reports whether the container exists and is running (not
// paused, not restarting).
func (c *Client) ContainerRunning(ctx context.Context, name string) (bool, error) {
	res, err := c.InspectContainer(ctx, name)
	if IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	st := res.State
	return st != nil && st.Running && !st.Paused && !st.Restarting, nil
}

// srcReader remembers a read error of r (io.EOF is not one).
type srcReader struct {
	r   io.Reader
	err error
}

func (s *srcReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		s.err = err
	}
	return n, err
}

// errWriter remembers the first write error of w, so a failure of the
// destination can be told apart from one of the stream.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) Write(p []byte) (int, error) {
	if e.err != nil {
		return 0, e.err
	}
	n, err := e.w.Write(p)
	if err != nil {
		e.err = err
	}
	return n, err
}

// TailBuffer keeps the last Max bytes written to it.
type TailBuffer struct {
	Max int
	mu  sync.Mutex
	buf []byte
}

func (t *TailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.Max; t.Max > 0 && over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return len(p), nil
}

func (t *TailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
