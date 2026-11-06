package app

import (
	"bytes"
	"context"
	"io"
	"runtime"
	"testing"

	"github.com/AVMG20/bitploy/internal/store/db"
)

// floodDB is a database container whose version and clean scripts print
// without end, as a user's own image can (a template app is redeployed with
// any Dockerfile).
type floodDB struct {
	*fakeDB
	mb int // MiB printed before the real answer
}

func (f *floodDB) Exec(ctx context.Context, app db.App, script string, stdin io.Reader, stdout io.Writer) (string, error) {
	if (script == f.eng.version || script == f.eng.clean) && stdout != nil {
		chunk := bytes.Repeat([]byte("y\n"), 512<<10) // 1 MiB
		for i := 0; i < f.mb; i++ {
			if _, err := stdout.Write(chunk); err != nil {
				return "", err
			}
		}
	}
	return f.fakeDB.Exec(ctx, app, script, stdin, stdout)
}

func TestDumpOutputOfTheVersionAndCleanStepsIsCapped(t *testing.T) {
	e, f := newDumpEnv(t)
	e.s.backup.run = &floodDB{fakeDB: f, mb: 256}
	ctx := context.Background()

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	b := e.makeBackup(t)
	if _, err := e.s.RestoreBackup(ctx, e.user, e.app.Slug, b.ID); err != nil {
		t.Fatal(err)
	}
	if r := e.waitRestore(t); r.State != "done" {
		t.Fatalf("restore = %+v", r)
	}
	runtime.ReadMemStats(&after)
	// Three version runs (backup, before-restore dump) and one clean printed
	// 256 MiB each; kept whole that is at least 1 GiB of allocations.
	if got := after.TotalAlloc - before.TotalAlloc; got > 128<<20 {
		t.Errorf("the backup and restore allocated %d MiB: the output of the container is not capped", got>>20)
	}
}
