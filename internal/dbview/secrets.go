package dbview

import (
	"regexp"
	"strings"

	sqlite "modernc.org/sqlite"
)

// The control-plane database of bitploy itself (the admin viewer, see
// control.go) holds secrets: password hashes, the key-encrypted values of env
// vars, session ids, tokens. They are never readable, whatever the SQL: the
// SQLite authorizer answers SQLITE_IGNORE to every read of such a column, and
// SQLite then uses NULL in its place, in expressions, joins, views, CTEs, WHERE
// clauses and SELECT * alike (see session.authorize).
//
// THIS IS THE ONE PLACE that lists them. A test (TestControlSecretsCoverMigrations)
// migrates a fresh database and fails when a column has a name that looks like a
// secret (see LooksSecret) and is in neither list, and when a list names a
// column that does not exist. A new migration with such a column therefore
// forces a decision here:
//
//   - controlSecrets: the column is a credential or derived from one. It reads
//     as NULL and the schema marks it "redacted".
//   - controlSafe: the name only looks secret; the value is not a credential.
//     Say why.
//
// A secret whose name does not look like one (uploads.id) is listed in
// controlSecrets all the same; the test cannot find those, review must.
var controlSecrets = map[string][]string{
	"users":               {"password_hash"},                                              // argon2 hash of the password
	"sessions":            {"id_hash", "csrf_token"},                                      // the session cookie (hashed) and its CSRF secret
	"api_tokens":          {"hash"},                                                       // hash of an API token
	"invites":             {"code_hash"},                                                  // hash of the invite link code
	"project_invites":     {"code_hash"},                                                  // hash of the project invite link code
	"env_vars":            {"value_enc"},                                                  // the app's secrets, encrypted
	"uploads":             {"id"},                                                         // the id IS the credential of /u/{id}
	"github_app":          {"client_secret_enc", "webhook_secret_enc", "private_key_enc"}, // the GitHub App's secrets, encrypted
	"apps":                {"protect_hash"},                                               // hash of the password of a protected app
	"domains":             {"txt_token"},                                                  // DNS ownership token, derived from the server secret
	"app_domains":         {"key_enc"},                                                    // private key of an app domain's certificate, encrypted
	"idempotency_keys":    {"key", "body"},                                                // a client's key, and the stored answer (may hold a token)
	"sqlite_stat4":        {"sample"},                                                     // ANALYZE keeps samples of indexed values (hashes) here
	"deployment_services": {"config_hash"},                                                // sha256 over a container's settings, its env vars (secrets) included
}

// controlSafe: names that look secret and are not.
var controlSafe = map[string][]string{
	"audit_log":      {"token_id"},  // the id of the API token used, not its value
	"deployments":    {"token_id"},  // the same
	"traffic_cursor": {"head_hash"}, // hash of the first bytes of the access log, to notice rotation
}

// deniedTables can not be read at all: sqlite_dbpage shows the raw pages of the
// file, secrets included, without going through any column.
var deniedTables = []string{"sqlite_dbpage"}

// secretName is what LooksSecret looks for in a column name.
var secretName = regexp.MustCompile(`(?i)hash|secret|token|key|enc|password|credential|csrf|private|cipher|salt`)

// LooksSecret reports whether a column name suggests a credential.
func LooksSecret(column string) bool { return secretName.MatchString(column) }

// Policy says what a session may not do. The zero value (nil) is the viewer of
// an app's database: no limits.
type Policy struct {
	// ReadOnly: nothing may change, rows have no key, only the schema, rows and
	// sql operations exist.
	ReadOnly bool
	redacted map[string]map[string]bool // lower case table, lower case column
	denied   map[string]bool            // lower case table
}

// ControlPolicy is the policy of the admin viewer of bitploy's own database.
func ControlPolicy() *Policy {
	p := &Policy{ReadOnly: true, redacted: map[string]map[string]bool{}, denied: map[string]bool{}}
	for t, cols := range controlSecrets {
		for _, c := range cols {
			p.redact(t, c)
		}
	}
	for _, t := range deniedTables {
		p.denied[strings.ToLower(t)] = true
	}
	return p
}

func (p *Policy) redact(table, column string) {
	t := strings.ToLower(table)
	if p.redacted[t] == nil {
		p.redacted[t] = map[string]bool{}
	}
	p.redacted[t][strings.ToLower(column)] = true
}

// Redacted: the column is a secret. Safe on a nil Policy.
func (p *Policy) Redacted(table, column string) bool {
	if p == nil {
		return false
	}
	return p.redacted[strings.ToLower(table)][strings.ToLower(column)]
}

// RedactedColumns lists the secret columns as table.column, for the docs and tests.
func RedactedColumns() []string {
	var out []string
	for t, cols := range controlSecrets {
		for _, c := range cols {
			out = append(out, t+"."+c)
		}
	}
	return out
}

// authorize is the part of the authorizer that belongs to a policy. decided is
// false when the general rules (session.authorize) are to answer.
//
//   - A read of a secret column is SQLITE_IGNORE: SQLite makes it NULL.
//   - A table in deniedTables can not be read.
//   - ReadOnly: only what reads is allowed; every change (insert, update, delete,
//     CREATE, DROP, ALTER, REINDEX, ANALYZE, ATTACH, ...) is denied, and so is every
//     PRAGMA that is not a plain look at the schema or settings.
func (p *Policy) authorize(action sqlite.AuthorizerActionCode, arg1, arg2 string) (rc sqlite.AuthorizerReturnCode, decided bool) {
	switch action {
	case sqlite.AuthRead:
		if p.denied[strings.ToLower(arg1)] {
			return sqlite.AuthorizerDeny, true
		}
		if p.Redacted(arg1, arg2) {
			return sqlite.AuthorizerIgnore, true
		}
		return sqlite.AuthorizerOK, true
	}
	if !p.ReadOnly {
		return 0, false
	}
	switch action {
	case sqlite.AuthSelect, sqlite.AuthFunction, sqlite.AuthRecursive, sqlite.AuthTransaction, sqlite.AuthSavepoint:
		if action == sqlite.AuthFunction {
			switch strings.ToLower(arg2) {
			case "load_extension", "fts3_tokenizer":
				return sqlite.AuthorizerDeny, true
			}
		}
		return sqlite.AuthorizerOK, true
	case sqlite.AuthPragma:
		if readOnlyPragma(arg1, arg2) {
			return sqlite.AuthorizerOK, true
		}
	}
	return sqlite.AuthorizerDeny, true
}

// why names what was denied, for the message.
func (p *Policy) why(action sqlite.AuthorizerActionCode, arg1 string) string {
	switch action {
	case sqlite.AuthRead:
		return "reading " + arg1
	case sqlite.AuthPragma:
		return "PRAGMA " + arg1
	}
	return "a change"
}

// readOnlyPragmas look at the schema or at a setting; with an argument they
// still only look (the argument says which table). Pragmas that set something
// when they get an argument (user_version, journal_mode, ...) are in
// readOnlyPragmasNoArg.
var readOnlyPragmas = map[string]bool{
	"table_info": true, "table_xinfo": true, "table_list": true, "index_list": true, "index_info": true, "index_xinfo": true,
	"foreign_key_list": true, "database_list": true, "collation_list": true,
}

var readOnlyPragmasNoArg = map[string]bool{
	"page_count": true, "page_size": true, "freelist_count": true, "journal_mode": true, "encoding": true,
	"schema_version": true, "user_version": true, "data_version": true, "foreign_keys": true, "application_id": true,
	"compile_options": true, "function_list": true, "module_list": true, "pragma_list": true,
}

func readOnlyPragma(name, arg string) bool {
	n := strings.ToLower(name)
	return readOnlyPragmas[n] || (readOnlyPragmasNoArg[n] && arg == "")
}
