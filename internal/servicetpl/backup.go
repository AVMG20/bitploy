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
)

// Backups reports whether bitploy makes backups of this template's data.
// Valkey, Redis, Meilisearch and Typesense keep a cache or a search index
// that can be rebuilt, and Mailpit keeps test email, so they get none; NoBackupsReason says why in plain words.
func (t Template) Backups() bool { return t.NoBackupsReason() == "" }

// NoBackupsReason is why bitploy makes no backups of this template's data,
// or "" when it does.
func (t Template) NoBackupsReason() string {
	switch t.ID {
	case "valkey":
		return "Valkey keeps a cache; its data can be rebuilt, so bitploy makes no backups of it."
	case "redis":
		return "Redis keeps a cache; its data can be rebuilt, so bitploy makes no backups of it."
	case "meilisearch":
		return "Meilisearch keeps a search index; it can be rebuilt from your data, so bitploy makes no backups of it."
	case "typesense":
		return "Typesense keeps a search index; it can be rebuilt from your data, so bitploy makes no backups of it."
	case "mailpit":
		return "Mailpit keeps test email; it is not worth keeping, so bitploy makes no backups of it."
	}
	return ""
}

// BackupEngine says how backups of this template's data are made. ""
// means a copy of the files (the app is paused while they are read): that is
// how ClickHouse and Garage are backed up.
func (t Template) BackupEngine() string {
	switch t.ID {
	case "postgres", "pgvector":
		return EnginePostgres
	case "mariadb":
		return EngineMariaDB
	case "mysql":
		return EngineMySQL
	}
	return ""
}
