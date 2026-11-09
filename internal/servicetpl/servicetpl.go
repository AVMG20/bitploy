// Package servicetpl is the list of service templates: databases and other
// ready services (PostgreSQL, MySQL, MariaDB, Valkey, Redis, Meilisearch,
// Typesense, ClickHouse, Garage, Mailpit) that
// are added to a project as an app of their own (docs/compose-plan.md,
// "Phase 4b"). Each template pins an exact image version.
//
// A template app is a private worker app (no public address) built from a
// one-line Dockerfile on top of the pinned image (Dockerfile below). It keeps
// its data on the app's /data volume, runs as the image's own non-root user
// (999:999, 101:101 for ClickHouse, or 10001:10001 for Meilisearch, Garage,
// Typesense and Mailpit) under bitploy's normal container
// sandbox, and reports healthy through a HEALTHCHECK the deploy waits for.
//
// Variables:
//   - Env are set on the template app itself when it is created. Values
//     marked Generate are 32 random letters and digits (or random hex
//     characters, see EnvSpec), stored encrypted.
//   - Connection are the variables the template publishes to the other apps
//     of its project, through references like ${{postgres.DATABASE_URL}}.
//     They are not variables of the template app (the image would read them);
//     they are computed when a referencing app starts, from the template app's
//     own variables (${NAME}) and its address in the project ({host}, {port}).
//   - MainVar is the connection variable "Connect to" writes into an app, as
//     MainVar=${{<alias>.MainVar}}.
package servicetpl

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/AVMG20/bitploy/internal/ids"
)

// EnvSpec is a variable of the template app.
type EnvSpec struct {
	Name  string
	Value string
	// Generate "secret": Value is ignored; 32 random base62 characters.
	// Generate "hex": Value is ignored; Prefix followed by HexLength random
	// lowercase hex characters, for tools that insist on a format (Garage's
	// RPC secret and S3 keys).
	Generate    string
	HexLength   int
	Prefix      string
	Description string
}

// NewValue is the value of a variable at creation: Value, or a freshly
// generated one.
func (e EnvSpec) NewValue() string {
	switch e.Generate {
	case "secret":
		return ids.Base62(32)
	case "hex":
		return e.Prefix + ids.Hex(e.HexLength)
	}
	return e.Value
}

// ConnVar is a variable the template publishes to its project. Value may
// contain {host} (the app's alias in the project), {port} and ${NAME} (a
// variable of the template app).
type ConnVar struct {
	Name  string
	Value string
}

// Template is one service template.
type Template struct {
	ID          string // also the default alias in the project
	Name        string
	Category    string // database | cache | search | storage | email
	Description string // two plain sentences
	Image       string // repository, like postgres
	Version     string // the exact tag, like 18.1
	Port        int64
	MemoryMB    int64
	CPUMillis   int64
	VolumeMB    int64
	// ShmMB is the size of /dev/shm; 0 keeps Docker's 64 MB. PostgreSQL
	// needs more for parallel queries.
	ShmMB int64
	// User is the user:group the container runs as. The pinned images' own
	// entrypoints skip their chown and gosu steps when they do not start as
	// root, and bitploy makes a new /data volume writable for every user
	// (mode 1777), so no extra capability is needed.
	User string
	// Env goes into the Dockerfile (fixed values) or is set as encrypted
	// app variables (Generate).
	Env []EnvSpec
	// DockerEnv are fixed ENV lines of the Dockerfile, like PGDATA.
	DockerEnv [][2]string
	// Extra are Dockerfile lines after the ENV lines, like a COPY with a
	// heredoc that writes a config file (an image may have no shell).
	Extra       string
	Cmd         []string // CMD in exec form; nil keeps the image's
	Workdir     string   // WORKDIR; "" keeps the image's
	Healthcheck string   // the HEALTHCHECK CMD, in shell form
	// HealthStartS is the HEALTHCHECK --start-period in seconds.
	HealthStartS int
	Connection   []ConnVar
	MainVar      string
	// Hidden templates are only used when asked for by id (redis next to
	// valkey, the default Redis-compatible store).
	Hidden bool
	// Keywords help match what people ask for ("redis", "postgresql").
	Keywords []string
}

// ImageRef is image:version.
func (t Template) ImageRef() string { return t.Image + ":" + t.Version }

// SecretNames are the variables of the template app that bitploy generates
// (the passwords and keys).
func (t Template) SecretNames() []string {
	out := []string{}
	for _, e := range t.Env {
		if e.Generate != "" {
			out = append(out, e.Name)
		}
	}
	return out
}

// ConnectionNames are the names of the connection variables.
func (t Template) ConnectionNames() []string {
	out := make([]string, len(t.Connection))
	for i, c := range t.Connection {
		out[i] = c.Name
	}
	return out
}

// Dockerfile is the one-line-and-settings Dockerfile the template app is
// built from.
func (t Template) Dockerfile() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# bitploy service template %s (%s %s).\n", t.ID, t.Name, t.Version)
	fmt.Fprintf(&b, "FROM %s\n", t.ImageRef())
	if len(t.DockerEnv) > 0 {
		b.WriteString("ENV")
		for i, kv := range t.DockerEnv {
			sep := " \\\n    "
			if i == 0 {
				sep = " "
			}
			fmt.Fprintf(&b, "%s%s=%s", sep, kv[0], kv[1])
		}
		b.WriteString("\n")
	}
	if t.Extra != "" {
		b.WriteString(strings.TrimRight(t.Extra, "\n") + "\n")
	}
	if t.Workdir != "" {
		fmt.Fprintf(&b, "WORKDIR %s\n", t.Workdir)
	}
	fmt.Fprintf(&b, "USER %s\n", t.User)
	fmt.Fprintf(&b, "EXPOSE %d\n", t.Port)
	if t.Healthcheck != "" {
		fmt.Fprintf(&b, "HEALTHCHECK --interval=5s --timeout=5s --start-period=%ds --retries=6 CMD %s\n", t.HealthStartS, t.Healthcheck)
	}
	if len(t.Cmd) > 0 {
		q := make([]string, len(t.Cmd))
		for i, c := range t.Cmd {
			q[i] = fmt.Sprintf("%q", c)
		}
		fmt.Fprintf(&b, "CMD [%s]\n", strings.Join(q, ", "))
	}
	return b.String()
}

var ownRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Resolve computes the value of connection variable name for a template app
// at host (its alias) with its own variables vars. ok is false when the
// template has no such variable.
func (t Template) Resolve(name, host string, vars map[string]string) (string, bool) {
	return t.ResolveAt(name, host, t.Port, vars)
}

// ResolveAt is Resolve for another address than the one in the project,
// like a public port: {host} becomes host and {port} becomes port.
func (t Template) ResolveAt(name, host string, port int64, vars map[string]string) (string, bool) {
	for _, c := range t.Connection {
		if c.Name != name {
			continue
		}
		v := strings.ReplaceAll(c.Value, "{host}", host)
		v = strings.ReplaceAll(v, "{port}", fmt.Sprint(port))
		v = ownRef.ReplaceAllStringFunc(v, func(m string) string {
			return vars[ownRef.FindStringSubmatch(m)[1]]
		})
		return v, true
	}
	return "", false
}

var (
	pgConn = func(user, pass, db string) []ConnVar {
		return []ConnVar{
			{Name: "DATABASE_URL", Value: "postgresql://${" + user + "}:${" + pass + "}@{host}:{port}/${" + db + "}"},
			{Name: "PGHOST", Value: "{host}"},
			{Name: "PGPORT", Value: "{port}"},
			{Name: "PGUSER", Value: "${" + user + "}"},
			{Name: "PGPASSWORD", Value: "${" + pass + "}"},
			{Name: "PGDATABASE", Value: "${" + db + "}"},
		}
	}
	pgEnv = []EnvSpec{
		{Name: "POSTGRES_USER", Value: "app", Description: "The database user."},
		{Name: "POSTGRES_DB", Value: "app", Description: "The database the user owns."},
		{Name: "POSTGRES_PASSWORD", Generate: "secret", Description: "The user's password."},
	}
	// PGDATA in a folder of /data: postgres wants an empty directory of its own.
	pgDockerEnv = [][2]string{{"PGDATA", "/data/pgdata"}}
	pgHealth    = `pg_isready -h 127.0.0.1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" || exit 1`

	redisConn = func(pass string) []ConnVar {
		return []ConnVar{
			{Name: "REDIS_URL", Value: "redis://default:${" + pass + "}@{host}:{port}"},
			{Name: "REDIS_HOST", Value: "{host}"},
			{Name: "REDIS_PORT", Value: "{port}"},
			{Name: "REDIS_PASSWORD", Value: "${" + pass + "}"},
		}
	}
)

var templates = []Template{
	{
		ID: "postgres", Name: "PostgreSQL", Category: "database",
		Description: "The open-source SQL database most frameworks support. Apps in the project connect with DATABASE_URL.",
		Image:       "postgres", Version: "18.6", Port: 5432, MemoryMB: 256, CPUMillis: 500, VolumeMB: 2048, User: "999:999",
		Env: pgEnv, DockerEnv: pgDockerEnv, Healthcheck: pgHealth, HealthStartS: 30, ShmMB: 128,
		Connection: pgConn("POSTGRES_USER", "POSTGRES_PASSWORD", "POSTGRES_DB"), MainVar: "DATABASE_URL",
		Keywords: []string{"postgresql", "postgres", "pg", "sql"},
	},
	{
		ID: "pgvector", Name: "PostgreSQL with pgvector", Category: "database",
		Description: "PostgreSQL with the pgvector extension for embeddings and similarity search. Run CREATE EXTENSION vector; once in your database.",
		Image:       "pgvector/pgvector", Version: "0.8.7-pg18", Port: 5432, MemoryMB: 256, CPUMillis: 500, VolumeMB: 2048, User: "999:999",
		Env: pgEnv, DockerEnv: pgDockerEnv, Healthcheck: pgHealth, HealthStartS: 30, ShmMB: 128,
		Connection: pgConn("POSTGRES_USER", "POSTGRES_PASSWORD", "POSTGRES_DB"), MainVar: "DATABASE_URL",
		Keywords: []string{"pgvector", "vector", "embeddings"},
	},
	{
		ID: "mariadb", Name: "MariaDB", Category: "database",
		Description: "A MySQL-compatible SQL database. Apps written for MySQL connect to it with DATABASE_URL or the MYSQL_ variables.",
		Image:       "mariadb", Version: "11.8.9", Port: 3306, MemoryMB: 256, CPUMillis: 500, VolumeMB: 2048, User: "999:999",
		Env: []EnvSpec{
			{Name: "MARIADB_USER", Value: "app", Description: "The database user."},
			{Name: "MARIADB_DATABASE", Value: "app", Description: "The database the user owns."},
			{Name: "MARIADB_PASSWORD", Generate: "secret", Description: "The user's password."},
			{Name: "MARIADB_ROOT_PASSWORD", Generate: "secret", Description: "The password of the root user."},
		},
		Cmd:          []string{"mariadbd", "--datadir=/data/mysql"},
		Healthcheck:  `mariadb-admin ping -h 127.0.0.1 -u"$MARIADB_USER" -p"$MARIADB_PASSWORD" --silent || exit 1`,
		HealthStartS: 30,
		Connection: []ConnVar{
			{Name: "DATABASE_URL", Value: "mysql://${MARIADB_USER}:${MARIADB_PASSWORD}@{host}:{port}/${MARIADB_DATABASE}"},
			{Name: "MYSQL_HOST", Value: "{host}"},
			{Name: "MYSQL_PORT", Value: "{port}"},
			{Name: "MYSQL_USER", Value: "${MARIADB_USER}"},
			{Name: "MYSQL_PASSWORD", Value: "${MARIADB_PASSWORD}"},
			{Name: "MYSQL_DATABASE", Value: "${MARIADB_DATABASE}"},
		},
		MainVar:  "DATABASE_URL",
		Keywords: []string{"mariadb", "mysql", "sql"},
	},
	{
		ID: "mysql", Name: "MySQL", Category: "database",
		Description: "Oracle's MySQL server, for apps that need MySQL itself. MariaDB uses less memory and suits most apps that ask for MySQL.",
		Image:       "mysql", Version: "8.4.13", Port: 3306, MemoryMB: 512, CPUMillis: 500, VolumeMB: 2048, User: "999:999",
		Env: []EnvSpec{
			{Name: "MYSQL_USER", Value: "app", Description: "The database user."},
			{Name: "MYSQL_DATABASE", Value: "app", Description: "The database the user owns."},
			{Name: "MYSQL_PASSWORD", Generate: "secret", Description: "The user's password."},
			{Name: "MYSQL_ROOT_PASSWORD", Generate: "secret", Description: "The password of the root user."},
		},
		Cmd:          []string{"mysqld", "--datadir=/data/mysql"},
		Healthcheck:  `mysqladmin ping -h 127.0.0.1 -u"$MYSQL_USER" -p"$MYSQL_PASSWORD" --silent || exit 1`,
		HealthStartS: 60,
		Connection: []ConnVar{
			{Name: "DATABASE_URL", Value: "mysql://${MYSQL_USER}:${MYSQL_PASSWORD}@{host}:{port}/${MYSQL_DATABASE}"},
			{Name: "MYSQL_HOST", Value: "{host}"},
			{Name: "MYSQL_PORT", Value: "{port}"},
			{Name: "MYSQL_USER", Value: "${MYSQL_USER}"},
			{Name: "MYSQL_PASSWORD", Value: "${MYSQL_PASSWORD}"},
			{Name: "MYSQL_DATABASE", Value: "${MYSQL_DATABASE}"},
		},
		MainVar:  "DATABASE_URL",
		Keywords: []string{"mysql", "sql"},
	},
	{
		ID: "valkey", Name: "Valkey (Redis-compatible)", Category: "cache",
		Description: "An in-memory key-value store that speaks the Redis protocol, for caches, sessions and job queues. Apps in the project connect with REDIS_URL.",
		Image:       "valkey/valkey", Version: "9.1.3", Port: 6379, MemoryMB: 128, CPUMillis: 250, VolumeMB: 512, User: "999:999",
		Env: []EnvSpec{{Name: "VALKEY_PASSWORD", Generate: "secret", Description: "The password clients send (AUTH)."}},
		// A shell so the password comes from the variable, not the Dockerfile.
		Cmd:          []string{"sh", "-c", `exec valkey-server --dir /data --appendonly yes --requirepass "$VALKEY_PASSWORD"`},
		Healthcheck:  `valkey-cli -a "$VALKEY_PASSWORD" --no-auth-warning ping | grep -q PONG || exit 1`,
		HealthStartS: 10,
		Connection: append(redisConn("VALKEY_PASSWORD"),
			ConnVar{Name: "VALKEY_URL", Value: "redis://default:${VALKEY_PASSWORD}@{host}:{port}"}),
		MainVar:  "REDIS_URL",
		Keywords: []string{"valkey", "redis", "cache", "queue"},
	},
	{
		ID: "redis", Name: "Redis", Category: "cache",
		Description: "Redis itself, for apps that need it by name. Valkey speaks the same protocol and is the default.",
		Image:       "redis", Version: "8.8.4", Port: 6379, MemoryMB: 128, CPUMillis: 250, VolumeMB: 512, User: "999:999",
		Env:          []EnvSpec{{Name: "REDIS_PASSWORD", Generate: "secret", Description: "The password clients send (AUTH)."}},
		Cmd:          []string{"sh", "-c", `exec redis-server --dir /data --appendonly yes --requirepass "$REDIS_PASSWORD"`},
		Healthcheck:  `redis-cli -a "$REDIS_PASSWORD" --no-auth-warning ping | grep -q PONG || exit 1`,
		HealthStartS: 10,
		Connection:   redisConn("REDIS_PASSWORD"),
		MainVar:      "REDIS_URL",
		Hidden:       true,
		Keywords:     []string{"redis"},
	},
	{
		ID: "meilisearch", Name: "Meilisearch", Category: "search",
		Description: "A search engine with typo tolerance and filters, for search boxes. Apps in the project connect with MEILI_URL and MEILI_MASTER_KEY.",
		Image:       "getmeili/meilisearch", Version: "v1.56.1", Port: 7700, MemoryMB: 512, CPUMillis: 500, VolumeMB: 2048, User: "10001:10001",
		Env: []EnvSpec{{Name: "MEILI_MASTER_KEY", Generate: "secret", Description: "The key that protects the search API."}},
		DockerEnv: [][2]string{
			{"MEILI_DB_PATH", "/data/data.ms"}, {"MEILI_DUMP_DIR", "/data/dumps"}, {"MEILI_SNAPSHOT_DIR", "/data/snapshots"},
			{"MEILI_ENV", "production"}, {"MEILI_NO_ANALYTICS", "true"}, {"MEILI_HTTP_ADDR", "0.0.0.0:7700"},
		},
		Workdir:      "/data",
		Healthcheck:  "curl -fsS -o /dev/null http://127.0.0.1:7700/health || exit 1",
		HealthStartS: 20,
		Connection: []ConnVar{
			{Name: "MEILI_URL", Value: "http://{host}:{port}"},
			{Name: "MEILI_HOST", Value: "http://{host}:{port}"},
			{Name: "MEILI_MASTER_KEY", Value: "${MEILI_MASTER_KEY}"},
		},
		MainVar:  "MEILI_URL",
		Keywords: []string{"meilisearch", "meili", "search"},
	},
	{
		ID: "typesense", Name: "Typesense", Category: "search",
		Description: "A search engine with typo tolerance, filters and facets, for search boxes. It holds its index in memory, so give it two to three times the size of the fields you search. Apps in the project connect with TYPESENSE_URL and TYPESENSE_API_KEY.",
		Image:       "typesense/typesense", Version: "30.2", Port: 8108, MemoryMB: 512, CPUMillis: 500, VolumeMB: 1024, User: "10001:10001",
		Env: []EnvSpec{{Name: "TYPESENSE_API_KEY", Generate: "secret", Description: "The admin key that protects the search API."}},
		DockerEnv: [][2]string{
			{"TYPESENSE_DATA_DIR", "/data"}, {"TYPESENSE_API_ADDRESS", "0.0.0.0"}, {"TYPESENSE_API_PORT", "8108"},
		},
		// The image has no curl or wget, so bash asks /health over TCP.
		Healthcheck:  `bash -c 'exec 3<>/dev/tcp/127.0.0.1/8108 && printf "GET /health HTTP/1.0\r\n\r\n" >&3 && grep -q ok.:true <&3' || exit 1`,
		HealthStartS: 30,
		Connection: []ConnVar{
			{Name: "TYPESENSE_URL", Value: "http://{host}:{port}"},
			{Name: "TYPESENSE_HOST", Value: "{host}"},
			{Name: "TYPESENSE_PORT", Value: "{port}"},
			{Name: "TYPESENSE_PROTOCOL", Value: "http"},
			{Name: "TYPESENSE_API_KEY", Value: "${TYPESENSE_API_KEY}"},
		},
		MainVar:  "TYPESENSE_URL",
		Keywords: []string{"typesense", "search", "algolia"},
	},
	{
		ID: "clickhouse", Name: "ClickHouse", Category: "database",
		Description: "A column-oriented SQL database for analytics: events, logs and metrics, queried in bulk. Apps in the project connect with CLICKHOUSE_URL (HTTP) or CLICKHOUSE_NATIVE_URL. It needs more memory than the other databases.",
		Image:       "clickhouse/clickhouse-server", Version: "26.8.36.3", Port: 8123, MemoryMB: 1024, CPUMillis: 1000, VolumeMB: 5120, User: "101:101",
		Env: []EnvSpec{
			{Name: "CLICKHOUSE_USER", Value: "app", Description: "The database user."},
			{Name: "CLICKHOUSE_DB", Value: "app", Description: "The database created at the first start."},
			{Name: "CLICKHOUSE_PASSWORD", Generate: "secret", Description: "The user's password."},
		},
		// Data and logs move to /data (the image keeps them in /var/lib and
		// /var/log), and the settings that make ClickHouse small are set:
		// smaller caches, fewer background threads, and none of the system
		// log tables that fill memory and disk.
		Extra: `COPY <<EOF /etc/clickhouse-server/config.d/bitploy.xml
<clickhouse>
  <path>/data/clickhouse/</path>
  <tmp_path>/data/clickhouse/tmp/</tmp_path>
  <user_files_path>/data/clickhouse/user_files/</user_files_path>
  <format_schema_path>/data/clickhouse/format_schemas/</format_schema_path>
  <logger>
    <level>warning</level>
    <console>1</console>
    <log>/data/log/clickhouse-server.log</log>
    <errorlog>/data/log/clickhouse-server.err.log</errorlog>
    <size>10M</size>
    <count>2</count>
  </logger>
  <max_server_memory_usage_to_ram_ratio>0.8</max_server_memory_usage_to_ram_ratio>
  <mark_cache_size>134217728</mark_cache_size>
  <uncompressed_cache_size>0</uncompressed_cache_size>
  <max_concurrent_queries>32</max_concurrent_queries>
  <background_pool_size>4</background_pool_size>
  <background_schedule_pool_size>8</background_schedule_pool_size>
  <query_log remove="1"/>
  <query_thread_log remove="1"/>
  <query_views_log remove="1"/>
  <part_log remove="1"/>
  <trace_log remove="1"/>
  <metric_log remove="1"/>
  <asynchronous_metric_log remove="1"/>
  <text_log remove="1"/>
  <session_log remove="1"/>
  <processors_profile_log remove="1"/>
  <error_log remove="1"/>
  <blob_storage_log remove="1"/>
</clickhouse>
EOF`,
		Healthcheck:  "wget -q --spider http://127.0.0.1:8123/ping || exit 1",
		HealthStartS: 60,
		Connection: []ConnVar{
			{Name: "CLICKHOUSE_URL", Value: "http://${CLICKHOUSE_USER}:${CLICKHOUSE_PASSWORD}@{host}:{port}/${CLICKHOUSE_DB}"},
			{Name: "CLICKHOUSE_NATIVE_URL", Value: "clickhouse://${CLICKHOUSE_USER}:${CLICKHOUSE_PASSWORD}@{host}:9000/${CLICKHOUSE_DB}"},
			{Name: "CLICKHOUSE_HOST", Value: "{host}"},
			{Name: "CLICKHOUSE_PORT", Value: "{port}"},
			{Name: "CLICKHOUSE_NATIVE_PORT", Value: "9000"},
			{Name: "CLICKHOUSE_USER", Value: "${CLICKHOUSE_USER}"},
			{Name: "CLICKHOUSE_PASSWORD", Value: "${CLICKHOUSE_PASSWORD}"},
			{Name: "CLICKHOUSE_DB", Value: "${CLICKHOUSE_DB}"},
		},
		MainVar:  "CLICKHOUSE_URL",
		Keywords: []string{"clickhouse", "analytics", "olap", "sql", "events"},
	},
	{
		ID: "garage", Name: "Garage (S3 storage)", Category: "storage",
		Description: "S3-compatible object storage for uploads, media and backups, with one bucket and one access key made at the first start. Apps in the project use S3_ENDPOINT with S3_ACCESS_KEY_ID and S3_SECRET_ACCESS_KEY (or the AWS_ variables) and path-style addressing.",
		Image:       "dxflrs/garage", Version: "v2.5.0", Port: 3900, MemoryMB: 256, CPUMillis: 500, VolumeMB: 4096, User: "10001:10001",
		Env: []EnvSpec{
			{Name: "GARAGE_RPC_SECRET", Generate: "hex", HexLength: 64, Description: "Secret of the node's internal RPC port."},
			{Name: "GARAGE_DEFAULT_ACCESS_KEY", Generate: "hex", Prefix: "GK", HexLength: 32, Description: "The access key id of the bucket."},
			{Name: "GARAGE_DEFAULT_SECRET_KEY", Generate: "hex", HexLength: 64, Description: "The secret access key of the bucket."},
			{Name: "GARAGE_DEFAULT_BUCKET", Value: "app", Description: "The bucket created at the first start."},
		},
		// The image is empty but for the garage binary: no shell, so the
		// config file comes from a heredoc and the secrets from variables.
		// --single-node sets up the one-node layout and --default-bucket makes
		// the bucket and key from the GARAGE_DEFAULT_ variables (Garage 2.3+).
		Extra: `COPY <<EOF /etc/garage.toml
metadata_dir = "/data/meta"
data_dir = "/data/blocks"
db_engine = "sqlite"
replication_factor = 1
rpc_bind_addr = "127.0.0.1:3901"
rpc_public_addr = "127.0.0.1:3901"

[s3_api]
s3_region = "garage"
api_bind_addr = "0.0.0.0:3900"
EOF`,
		Cmd:          []string{"/garage", "server", "--single-node", "--default-bucket"},
		Healthcheck:  `["/garage", "status"]`,
		HealthStartS: 20,
		Connection: []ConnVar{
			{Name: "S3_ENDPOINT", Value: "http://{host}:{port}"},
			{Name: "S3_REGION", Value: "garage"},
			{Name: "S3_BUCKET", Value: "${GARAGE_DEFAULT_BUCKET}"},
			{Name: "S3_ACCESS_KEY_ID", Value: "${GARAGE_DEFAULT_ACCESS_KEY}"},
			{Name: "S3_SECRET_ACCESS_KEY", Value: "${GARAGE_DEFAULT_SECRET_KEY}"},
			{Name: "S3_FORCE_PATH_STYLE", Value: "true"},
			{Name: "AWS_ENDPOINT_URL", Value: "http://{host}:{port}"},
			{Name: "AWS_REGION", Value: "garage"},
			{Name: "AWS_ACCESS_KEY_ID", Value: "${GARAGE_DEFAULT_ACCESS_KEY}"},
			{Name: "AWS_SECRET_ACCESS_KEY", Value: "${GARAGE_DEFAULT_SECRET_KEY}"},
		},
		MainVar:  "S3_ENDPOINT",
		Keywords: []string{"garage", "s3", "object storage", "minio", "bucket", "uploads"},
	},
	{
		ID: "mailpit", Name: "Mailpit (test inbox)", Category: "email",
		Description: "Catches the email your apps send and shows it in a web inbox, so tests and staging never reach real people. Apps in the project send to SMTP_HOST and SMTP_PORT without a login.",
		Image:       "axllent/mailpit", Version: "v1.33.1", Port: 8025, MemoryMB: 128, CPUMillis: 250, VolumeMB: 512, User: "10001:10001",
		DockerEnv: [][2]string{
			{"MP_DATABASE", "/data/mailpit.db"}, {"MP_SMTP_AUTH_ACCEPT_ANY", "1"}, {"MP_SMTP_AUTH_ALLOW_INSECURE", "1"},
		},
		Healthcheck:  "/mailpit readyz || exit 1",
		HealthStartS: 10,
		Connection: []ConnVar{
			{Name: "SMTP_URL", Value: "smtp://{host}:1025"},
			{Name: "SMTP_HOST", Value: "{host}"},
			{Name: "SMTP_PORT", Value: "1025"},
			{Name: "MAIL_HOST", Value: "{host}"},
			{Name: "MAIL_PORT", Value: "1025"},
			{Name: "MAILPIT_API_URL", Value: "http://{host}:{port}"},
		},
		MainVar:  "SMTP_URL",
		Keywords: []string{"mailpit", "mailhog", "email", "smtp", "mail", "inbox"},
	},
}

// All returns every template (hidden ones too), in display order.
func All() []Template { return append([]Template(nil), templates...) }

// Listed returns the templates to offer: All without the hidden ones.
func Listed() []Template {
	out := make([]Template, 0, len(templates))
	for _, t := range templates {
		if !t.Hidden {
			out = append(out, t)
		}
	}
	return out
}

// Get returns the template with this id.
func Get(id string) (Template, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, t := range templates {
		if t.ID == id {
			return t, true
		}
	}
	return Template{}, false
}

// IDs lists the template ids, sorted.
func IDs() []string {
	out := make([]string, 0, len(templates))
	for _, t := range templates {
		out = append(out, t.ID)
	}
	sort.Strings(out)
	return out
}
