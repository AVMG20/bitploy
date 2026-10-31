-- +goose Up
-- Basic traffic stats per app, read from Traefik's access log. No IP addresses
-- and no user agents are stored, only counts. Days are UTC dates (YYYY-MM-DD),
-- hours are unix seconds at the start of the hour. Rows go when their app goes.

CREATE TABLE traffic_hourly (
    app_id     TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    hour       INTEGER NOT NULL,
    requests   INTEGER NOT NULL DEFAULT 0,
    errors_4xx INTEGER NOT NULL DEFAULT 0,
    errors_5xx INTEGER NOT NULL DEFAULT 0,
    visitors   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (app_id, hour)
) WITHOUT ROWID;
CREATE INDEX traffic_hourly_hour ON traffic_hourly(hour);

CREATE TABLE traffic_daily (
    app_id     TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    day        TEXT NOT NULL,
    requests   INTEGER NOT NULL DEFAULT 0,
    errors_4xx INTEGER NOT NULL DEFAULT 0,
    errors_5xx INTEGER NOT NULL DEFAULT 0,
    visitors   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (app_id, day)
) WITHOUT ROWID;
CREATE INDEX traffic_daily_day ON traffic_daily(day);

CREATE TABLE traffic_paths (
    app_id TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    day    TEXT NOT NULL,
    path   TEXT NOT NULL,
    views  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (app_id, day, path)
) WITHOUT ROWID;
CREATE INDEX traffic_paths_day ON traffic_paths(day);

CREATE TABLE traffic_countries (
    app_id   TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    day      TEXT NOT NULL,
    country  TEXT NOT NULL,            -- ISO 3166-1 alpha-2, or "unknown"
    requests INTEGER NOT NULL DEFAULT 0,
    visitors INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (app_id, day, country)
) WITHOUT ROWID;
CREATE INDEX traffic_countries_day ON traffic_countries(day);

CREATE TABLE traffic_referrers (
    app_id TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    day    TEXT NOT NULL,
    host   TEXT NOT NULL,
    visits INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (app_id, day, host)
) WITHOUT ROWID;
CREATE INDEX traffic_referrers_day ON traffic_referrers(day);

-- Where bitploy stopped reading Traefik's access log, saved with the counts it
-- belongs to, so a restart does not count the same lines twice. The head is a
-- hash of the first bytes of the file: when it differs, the file is a new one.
CREATE TABLE traffic_cursor (
    id        INTEGER PRIMARY KEY CHECK (id = 1),
    pos       INTEGER NOT NULL,
    head_len  INTEGER NOT NULL,
    head_hash TEXT NOT NULL
);

-- +goose Down
DROP TABLE traffic_cursor;
DROP TABLE traffic_referrers;
DROP TABLE traffic_countries;
DROP TABLE traffic_paths;
DROP TABLE traffic_daily;
DROP TABLE traffic_hourly;
