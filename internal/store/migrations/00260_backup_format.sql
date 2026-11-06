-- +goose Up
-- How a backup holds the data: volume (a .tar.gz of the files, the only kind
-- before this) or dump (a gzipped logical dump of a database service, made
-- with the database's own tool while it runs: <backup id>.sql.gz). tool is
-- the dump tool and its version, like "pg_dumpall (PostgreSQL) 18.1".
ALTER TABLE app_backups ADD COLUMN format TEXT NOT NULL DEFAULT 'volume';
ALTER TABLE app_backups ADD COLUMN tool   TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE app_backups DROP COLUMN tool;
ALTER TABLE app_backups DROP COLUMN format;
