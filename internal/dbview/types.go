// Package dbview runs the SQLite database viewer's work: the schema of a
// database, a page of rows, row changes and a SQL console. It has no idea of
// Docker: the helper container runs `bitploy db-helper`, which calls Handle
// (helper.go), and the tests call Run against temp files.
//
// A request is JSON (Request), the answer is JSON too (Response). Values go in
// and out in the shapes of value.go, so every SQLite value survives JSON.
package dbview

import (
	"bytes"
	"encoding/json"
	"time"
)

// Limits shared by the helper, the service and the API docs.
const (
	MaxSQLBytes    = 64 << 10 // the SQL of one console request
	MaxRequest     = 32 << 20 // the whole request, with the values of a row in it
	MaxRows        = 1000     // rows of a page, and of one console statement
	DefaultRows    = 100
	MaxSearch      = 200  // characters of a search term
	MaxKeys        = 1000 // rows removed in one delete
	MaxTextBytes   = 64 << 10
	MaxBlobPreview = 4 << 10
	MaxCellBytes   = 4 << 20 // the biggest text or blob the cell operation sends whole
)

// MaxResponse is the most JSON the helper prints (the log of its container
// holds it). A variable only for tests.
var MaxResponse = 8 << 20

// Timeouts. Variables only so that tests need not wait for the real ones.
var (
	BusyTimeout  = 5 * time.Second  // wait for the lock another connection holds
	QueryTimeout = 15 * time.Second // the helper stops itself after this
	CountTimeout = 2 * time.Second  // all row counts of the schema together
)

// Operations of a Request.
const (
	OpInfo       = "info"
	OpRows       = "rows"
	OpInsert     = "insert"
	OpUpdate     = "update"
	OpDelete     = "delete"
	OpUpdateMany = "update_many"
	OpSQL        = "sql"
	OpCell       = "cell"
)

// Request is what the helper is asked to do. Path is relative to /data.
type Request struct {
	Op   string `json:"op"`
	Path string `json:"path"`

	Table   string   `json:"table,omitempty"`
	Column  string   `json:"column,omitempty"` // cell
	Limit   int      `json:"limit,omitempty"`
	Offset  int      `json:"offset,omitempty"`
	OrderBy string   `json:"order_by,omitempty"`
	Desc    bool     `json:"desc,omitempty"`
	Filters []Filter `json:"filters,omitempty"`
	Search  string   `json:"search,omitempty"` // free text over all columns (rows)

	Values map[string]json.RawMessage   `json:"values,omitempty"`
	Key    map[string]json.RawMessage   `json:"key,omitempty"`
	Keys   []map[string]json.RawMessage `json:"keys,omitempty"`
	// Match are values the row must still have (update) and one such set per key
	// (delete): a stale key then changes nothing and answers row_changed.
	Match   map[string]json.RawMessage   `json:"match,omitempty"`
	Matches []map[string]json.RawMessage `json:"matches,omitempty"`

	SQL   string `json:"sql,omitempty"`
	Write bool   `json:"write,omitempty"`
}

// Filter is one condition of the rows request. Value is in the value shapes.
type Filter struct {
	Column string          `json:"column"`
	Op     string          `json:"op"`
	Value  json.RawMessage `json:"value,omitempty"`
}

// Response is the one JSON document the helper prints: a result or an error.
type Response struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// Error is a failure in plain words. Code is one of the codes of the contract.
type Error struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Hint    string         `json:"hint,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func errf(code, msg, hint string) *Error { return &Error{Code: code, Message: msg, Hint: hint} }

// ---- results ----

// Info is the schema of a database.
type Info struct {
	Path          string  `json:"path"`
	Size          int64   `json:"size"`
	JournalMode   string  `json:"journal_mode"`
	PageSize      int64   `json:"page_size"`
	SQLiteVersion string  `json:"sqlite_version"`
	Tables        []Table `json:"tables"`
}

// Table is a table or a view.
type Table struct {
	Name         string   `json:"name"`
	Type         string   `json:"type"` // table | view
	WithoutRowid bool     `json:"without_rowid"`
	Editable     bool     `json:"editable"`
	Rows         *int64   `json:"rows"` // nil: counting took too long
	SQL          string   `json:"sql"`
	Columns      []Column `json:"columns"`
	Indexes      []Index  `json:"indexes"`
}

// Column is one column of a table or view.
type Column struct {
	Name      string  `json:"name"`
	Type      string  `json:"type"`
	NotNull   bool    `json:"notnull"`
	Default   *string `json:"default"` // the SQL text of the default
	PK        int     `json:"pk"`      // position in the primary key, 0 when it is not part of it
	Hidden    bool    `json:"hidden"`  // a hidden column of a virtual table
	Generated bool    `json:"generated"`
	Redacted  bool    `json:"redacted"` // a secret of the control-plane database: always reads as NULL
}

// Index is one index of a table.
type Index struct {
	Name    string   `json:"name"`
	Unique  bool     `json:"unique"`
	Columns []string `json:"columns"`
	SQL     *string  `json:"sql"` // nil for the index of a PRIMARY KEY or UNIQUE constraint
}

// Row is one row of a grid. Key is nil when the row can not be changed.
type Row struct {
	Key    map[string]any `json:"key"`
	Values []any          `json:"values"`
}

// RowsResult is a page of rows.
type RowsResult struct {
	Columns    []string `json:"columns"`
	KeyColumns []string `json:"key_columns"`
	Editable   bool     `json:"editable"`
	Rows       []Row    `json:"rows"`
	Total      *int64   `json:"total"` // nil: counting took too long
	Offset     int      `json:"offset"`
	Limit      int      `json:"limit"`
}

// InsertResult is the new row.
type InsertResult struct {
	Key map[string]any `json:"key"`
	Row Row            `json:"row"`
}

// UpdateResult is the changed row.
type UpdateResult struct {
	Row Row `json:"row"`
}

// CellResult is one whole value (see cell.go).
type CellResult struct {
	Value any `json:"value"`
}

// UpdateManyResult says how many rows were changed.
type UpdateManyResult struct {
	Updated int64 `json:"updated"`
}

// DeleteResult says how many rows went.
type DeleteResult struct {
	Deleted int64 `json:"deleted"`
}

// StatementResult is the result of one statement of the console.
type StatementResult struct {
	SQL             string   `json:"sql"`
	Columns         []string `json:"columns"`
	Rows            [][]any  `json:"rows"`
	Truncated       bool     `json:"truncated"`
	RowsAffected    *int64   `json:"rows_affected"`
	LastInsertRowid *int64   `json:"last_insert_rowid"`
	TookMS          int64    `json:"took_ms"`
}

// SQLResult is the answer of the console.
type SQLResult struct {
	Results []StatementResult `json:"results"`
	TookMS  int64             `json:"took_ms"`
}

// Decode reads a JSON document keeping numbers as json.Number, so a value read
// from a result and written again is not changed on the way.
func Decode(raw []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return d.Decode(v)
}

// encode is json.Marshal without the escaping of <, > and & that would only
// make big results bigger.
func encode(v any) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}
