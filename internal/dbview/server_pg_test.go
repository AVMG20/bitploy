package dbview

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgproto3"
)

// fakePG is a PostgreSQL server that answers every statement with one row
// and records how the viewer talked to it.
type fakePG struct {
	mu       sync.Mutex
	params   map[string]string
	simple   []string // statements sent with the simple protocol (several may run per message)
	extended []string // statements sent with Parse (one per message)
}

func startFakePG(t *testing.T) (*fakePG, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	f := &fakePG{}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f, ln.Addr().(*net.TCPAddr).Port
}

func (f *fakePG) serve(c net.Conn) {
	defer c.Close()
	be := pgproto3.NewBackend(c, c)
	msg, err := be.ReceiveStartupMessage()
	if err != nil {
		return
	}
	if sm, ok := msg.(*pgproto3.StartupMessage); ok {
		f.mu.Lock()
		f.params = sm.Parameters
		f.mu.Unlock()
	}
	tx := byte('I')
	be.Send(&pgproto3.AuthenticationOk{})
	be.Send(&pgproto3.ParameterStatus{Name: "server_version", Value: "17.0"})
	be.Send(&pgproto3.BackendKeyData{ProcessID: 1, SecretKey: []byte{0, 0, 0, 1}})
	be.Send(&pgproto3.ReadyForQuery{TxStatus: tx})
	if be.Flush() != nil {
		return
	}
	row := func() {
		be.Send(&pgproto3.RowDescription{Fields: []pgproto3.FieldDescription{{Name: []byte("x"), DataTypeOID: 25, DataTypeSize: -1, TypeModifier: -1}}})
		be.Send(&pgproto3.DataRow{Values: [][]byte{[]byte("1")}})
		be.Send(&pgproto3.CommandComplete{CommandTag: []byte("SELECT 1")})
	}
	for {
		msg, err := be.Receive()
		if err != nil {
			return
		}
		switch m := msg.(type) {
		case *pgproto3.Query:
			f.mu.Lock()
			f.simple = append(f.simple, m.String)
			f.mu.Unlock()
			if strings.HasPrefix(m.String, "BEGIN") {
				tx = 'T'
			}
			be.Send(&pgproto3.CommandComplete{CommandTag: []byte("SET")})
			be.Send(&pgproto3.ReadyForQuery{TxStatus: tx})
		case *pgproto3.Parse:
			f.mu.Lock()
			f.extended = append(f.extended, m.Query)
			f.mu.Unlock()
			be.Send(&pgproto3.ParseComplete{})
		case *pgproto3.Bind:
			be.Send(&pgproto3.BindComplete{})
		case *pgproto3.Describe:
		case *pgproto3.Execute:
			row()
		case *pgproto3.Sync:
			be.Send(&pgproto3.ReadyForQuery{TxStatus: tx})
		case *pgproto3.Terminate:
			return
		}
		if be.Flush() != nil {
			return
		}
	}
}

func TestPostgresConsoleOneStatementPerCall(t *testing.T) {
	f, port := startFakePG(t)
	call := func(sql string, write bool) Response {
		raw, _ := json.Marshal(Request{Op: OpSQL, SQL: sql, Write: write,
			Server: &ServerConn{Engine: EnginePostgres, Port: port, User: "app", Password: "pw", Database: "app"}})
		return Handle(context.Background(), raw, HelperOptions{})
	}

	r := call("SELECT 1; SELECT 2 /* a /* nested */ comment */", false)
	if r.Error != nil {
		t.Fatalf("console: %+v", r.Error)
	}
	f.mu.Lock()
	params, simple, extended := f.params, f.simple, f.extended
	f.extended = nil
	f.mu.Unlock()
	if params["default_transaction_read_only"] != "on" || params["statement_timeout"] == "" || params["statement_timeout"] == "0" {
		t.Fatalf("session params of a read session: %v", params)
	}
	if len(extended) != 2 || extended[0] != "SELECT 1" || !strings.HasPrefix(extended[1], "SELECT 2") {
		t.Fatalf("the user's statements must go one per Parse: %q", extended)
	}
	for _, q := range simple {
		if strings.Contains(q, "SELECT") {
			t.Fatalf("a user statement went over the simple protocol: %q", q)
		}
	}

	// The texts of the audit: the splitter sees the statements behind the
	// comment, so read-only mode refuses them before anything is sent.
	for _, sql := range []string{
		"SELECT 1 /* /* */ ' */; COMMIT; DELETE FROM users; SELECT ''",
		"SELECT 1 /* /* /* */ */ ' */; END; DELETE FROM users; SELECT ''",
	} {
		r := call(sql, false)
		if r.Error == nil || r.Error.Code != "db_read_only" {
			t.Fatalf("%q: want db_read_only, got %+v", sql, r.Error)
		}
	}
	f.mu.Lock()
	refused := f.extended
	f.params = nil
	f.mu.Unlock()
	if len(refused) != 0 {
		t.Fatalf("a refused text reached the server: %q", refused)
	}

	// A write session has no read-only default but keeps the time limit.
	if r := call("SELECT 1", true); r.Error != nil {
		t.Fatalf("write console: %+v", r.Error)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.params["default_transaction_read_only"]; ok || f.params["statement_timeout"] == "" {
		t.Fatalf("session params of a write session: %v", f.params)
	}
}
