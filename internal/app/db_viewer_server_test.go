package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/dbview"
)

// serverVol answers the viewer's requests for a database server with a
// canned result and keeps what it was sent.
type serverVol struct {
	*dbVol
	mu         sync.Mutex
	containers []string
	sent       []dbview.Request
}

func (v *serverVol) RunDBServer(_ context.Context, container string, request []byte) ([]byte, error) {
	var req dbview.Request
	_ = json.Unmarshal(request, &req)
	v.mu.Lock()
	v.containers = append(v.containers, container)
	v.sent = append(v.sent, req)
	v.mu.Unlock()
	res, _ := json.Marshal(dbview.Info{Path: req.Server.Database, Database: req.Server.Database, Engine: req.Server.Engine, Tables: []dbview.Table{}})
	return json.Marshal(dbview.Response{Result: res})
}

func TestDBViewerOfAPostgresApp(t *testing.T) {
	e, f := newDumpEnv(t)
	ctx := context.Background()
	if _, err := e.s.SetEnv(ctx, e.user, e.app.Slug, map[string]string{"POSTGRES_DB": "shop"}, nil); err != nil {
		t.Fatal(err)
	}
	v := &serverVol{dbVol: newDBVol(t)}
	e.s.backup.vol = v

	// No path: the database the app was made with.
	info, err := e.s.DBInfo(ctx, e.user, e.app.Slug, "")
	if err != nil {
		t.Fatal(err)
	}
	if info.Database != "shop" || len(v.sent) != 1 {
		t.Fatalf("info = %+v, sent %d", info, len(v.sent))
	}
	got := v.sent[0].Server
	if got == nil || got.Engine != "postgres" || got.User != "app" || got.Password != "S3cretPassw0rdS3cretPassw0rdXyZq" || got.Port != 5432 || got.Database != "shop" {
		t.Fatalf("login = %+v", got)
	}
	if len(v.dbVol.reqs) != 0 {
		t.Error("a database app's request went to the SQLite helper")
	}

	// Another database of the server, by name.
	if _, err := e.s.DBInfo(ctx, e.user, e.app.Slug, "postgres"); err != nil {
		t.Fatal(err)
	}
	if v.sent[1].Server.Database != "postgres" || v.sent[1].Path != "postgres" {
		t.Fatalf("second request = %+v", v.sent[1])
	}

	// A stopped database has nothing to look at.
	f.running = false
	_, err = e.s.DBInfo(ctx, e.user, e.app.Slug, "")
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != "db_not_running" {
		t.Fatalf("stopped: %v", err)
	}
}
