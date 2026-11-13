-- +goose Up
-- One row per deployment that used a build slot (every deployment but a
-- rollback, which builds nothing), written when the deployment ends, for the
-- build history on Admin > Server and the admin's page of a person. No
-- foreign keys: a row outlives its app and its people. Kept 90 days (pruned
-- nightly with the metrics).
CREATE TABLE build_history (
    deployment_id TEXT PRIMARY KEY,
    app_id        TEXT NOT NULL,
    app_slug      TEXT NOT NULL,      -- at the time of the build
    app_name      TEXT NOT NULL,
    owner_id      TEXT NOT NULL,      -- the app's owner
    started_by    TEXT,               -- deployments.created_by; NULL for a git push
    person_id     TEXT NOT NULL,      -- started_by, else owner_id: who the build counts for
    channel       TEXT NOT NULL,      -- dashboard | api | mcp | system
    source_kind   TEXT NOT NULL,      -- files | upload | git
    queued_at     INTEGER NOT NULL,
    started_at    INTEGER NOT NULL,   -- took a build slot
    built_at      INTEGER,            -- the image was built; NULL when the build did not get there
    finished_at   INTEGER NOT NULL,   -- the deployment ended
    result        TEXT NOT NULL,      -- live | failed | cancelled
    error_code    TEXT,
    peak_cpu      REAL,               -- highest server CPU % from started_at to the build's end; NULL without samples
    peak_mem_mb   REAL                -- highest server memory used (MB) over the same time
);
CREATE INDEX build_history_finished ON build_history(finished_at);
CREATE INDEX build_history_person ON build_history(person_id, finished_at);

-- +goose Down
DROP TABLE build_history;
