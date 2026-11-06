-- +goose Up
-- Public ports: a host port (from BITPLOY_PUBLIC_PORTS) forwarded to an app
-- that has no web address, like a database, so the owner can connect to it
-- with psql or TablePlus. One row per app while the port is open; the row goes
-- when it is closed, expires or the app is deleted, and the port is free again.
--  port        the host port, unique on the server
--  service     the service of an app with several services; '' for a single app
--  allow_from  the addresses that may connect, CIDRs separated by commas; '' is everyone
--  expires_at  when bitploy closes it by itself (unix seconds); NULL is never
--  last_error  why the forwarder could not be started the last time; '' when it runs
CREATE TABLE public_ports (
    app_id     TEXT PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    port       INTEGER NOT NULL UNIQUE,
    service    TEXT NOT NULL DEFAULT '',
    allow_from TEXT NOT NULL DEFAULT '',
    expires_at INTEGER,
    last_error TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE public_ports;
