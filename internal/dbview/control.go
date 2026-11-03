package dbview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The admin viewer of bitploy's own database (the control plane). It is the
// app viewer with ControlPolicy: read-only always, secrets always NULL. The
// request is the same JSON; only the operations info, rows and sql exist.

// Errors of running the control helper as a process.
var (
	ErrTimeout     = errors.New("the database helper took too long and was stopped")
	ErrOutOfMemory = errors.New("the database helper ran out of memory")
)

// ProcessGrace is how long ControlProcess lets the helper run beyond
// QueryTimeout, which the helper keeps itself, before it kills the process.
// A variable for the tests.
var ProcessGrace = 5 * time.Second

// ControlMemory is the address space the control helper process may use (Linux).
const ControlMemory = 1 << 30

// controlOps are the operations of the admin viewer.
func controlOp(op string) bool { return op == OpInfo || op == OpRows || op == OpSQL }

// RunControl does a request on the control-plane database file under
// ControlPolicy. Anything that would change the database is refused.
func RunControl(ctx context.Context, file string, req Request) Response {
	if !controlOp(req.Op) {
		return Response{Error: errf("db_read_only", "The bitploy database is read-only here.", "Only the schema, rows and read-only SQL are available.")}
	}
	if req.Write {
		return Response{Error: errf("db_read_only", "The bitploy database is read-only here: write is not accepted.", "Leave write out.")}
	}
	req.Path = filepath.Base(file)
	return runWith(ctx, file, req, ControlPolicy())
}

// HandleControl is Handle for the control-plane database: the file is given
// (by bitploy itself, never by the request) instead of a path below /data.
func HandleControl(ctx context.Context, raw []byte, file string) Response {
	if len(raw) > MaxRequest {
		return Response{Error: errf("request_too_large", "The request is too big.", "Send fewer or smaller values.")}
	}
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return Response{Error: errf("request_invalid", "The request could not be read: "+err.Error(), "")}
	}
	st, err := os.Stat(file)
	if err != nil {
		return Response{Error: fileErr(err)}
	}
	if !st.Mode().IsRegular() {
		return Response{Error: errf("not_a_file", "The database is not a regular file.", "")}
	}
	return RunControl(ctx, file, req)
}

// ControlProcess runs `<argv0> <pre...> db-helper --control <file> -` as a
// process of its own: the request goes in on standard input, the response comes
// out on standard output. It gives the viewer what the container gives the
// app viewer, without Docker: a process that can crash, run out of memory
// (limited, see ControlMemory) or hang without taking bitploy with it, a time
// limit (QueryTimeout, then ProcessGrace before the process is killed), a limit on
// the answer, and an empty environment (none of bitploy's secrets).
func ControlProcess(ctx context.Context, argv0 string, pre []string, file string, request []byte) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, QueryTimeout+ProcessGrace)
	defer cancel()
	args := append(append([]string{}, pre...), "db-helper", "--control", file, "-")
	cmd := exec.CommandContext(cctx, argv0, args...)
	cmd.Dir = "/"
	cmd.Env = []string{"GOMAXPROCS=2", "GOMEMLIMIT=512MiB", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(request)
	out := &capBuffer{max: MaxResponse + (64 << 10)}
	var stderr capBuffer
	stderr.max = 4 << 10
	cmd.Stdout, cmd.Stderr = out, &stderr
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	switch {
	case ctx.Err() != nil:
		return nil, ctx.Err()
	case cctx.Err() != nil:
		return nil, ErrTimeout
	case out.over:
		return nil, errors.New("the database helper printed more than it may")
	case err != nil:
		if strings.Contains(stderr.String(), "out of memory") || strings.Contains(stderr.String(), "cannot allocate memory") {
			return nil, ErrOutOfMemory
		}
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("database helper failed: %v: %s", err, msg)
	}
	return out.Bytes(), nil
}

// capBuffer keeps at most max bytes; more makes over true and is dropped (and
// the write fails, so that a helper that prints without end is stopped). It
// does not embed bytes.Buffer: io.Copy would then use its ReadFrom and never
// call Write.
type capBuffer struct {
	buf  bytes.Buffer
	max  int
	over bool
}

func (b *capBuffer) Write(p []byte) (int, error) {
	if b.buf.Len()+len(p) > b.max {
		b.over = true
		return 0, io.ErrShortWrite
	}
	return b.buf.Write(p)
}

func (b *capBuffer) Bytes() []byte  { return b.buf.Bytes() }
func (b *capBuffer) String() string { return b.buf.String() }
