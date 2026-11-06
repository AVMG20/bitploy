package servicetpl

// Backup engines: how bitploy makes a consistent backup of a template app's
// data (internal/app/backups_dump.go has the commands).
const (
	// EnginePostgres: pg_dumpall of the whole server, replayed with psql.
	EnginePostgres = "postgres"
	// EngineMariaDB and EngineMySQL: mariadb-dump / mysqldump of every
	// database, replayed with mariadb / mysql.
	EngineMariaDB = "mariadb"
	EngineMySQL   = "mysql"
	// EngineRedis: a BGSAVE before the files are copied (valkey and redis).
	EngineRedis = "redis"
)

// BackupEngine says how backups of this template's data are made. ""
// means a copy of the files (the app is paused while they are read), which
// is consistent for Meilisearch: its LMDB store is copy-on-write and a
// paused process writes nothing.
func (t Template) BackupEngine() string {
	switch t.ID {
	case "postgres", "pgvector":
		return EnginePostgres
	case "mariadb":
		return EngineMariaDB
	case "mysql":
		return EngineMySQL
	case "valkey", "redis":
		return EngineRedis
	}
	return ""
}

// PasswordVar is the template app's variable that holds the password of the
// database's superuser, which a dump and a restore log in as. Valkey and
// Redis: the AUTH password.
func (t Template) PasswordVar() string {
	switch t.ID {
	case "postgres", "pgvector":
		return "POSTGRES_PASSWORD"
	case "mariadb":
		return "MARIADB_ROOT_PASSWORD"
	case "mysql":
		return "MYSQL_ROOT_PASSWORD"
	case "valkey":
		return "VALKEY_PASSWORD"
	case "redis":
		return "REDIS_PASSWORD"
	}
	return ""
}
