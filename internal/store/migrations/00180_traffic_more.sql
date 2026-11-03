-- +goose Up
-- Bot requests per hour and day (they count as requests, not as visitors), and
-- the top paths answered 404 per day (assets included: broken links to files
-- are worth knowing). Kept like traffic_paths.
ALTER TABLE traffic_hourly ADD COLUMN bots INTEGER NOT NULL DEFAULT 0;
ALTER TABLE traffic_daily ADD COLUMN bots INTEGER NOT NULL DEFAULT 0;

CREATE TABLE traffic_not_found (
    app_id TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    day    TEXT NOT NULL,
    path   TEXT NOT NULL,
    hits   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (app_id, day, path)
) WITHOUT ROWID;
CREATE INDEX traffic_not_found_day ON traffic_not_found(day);

-- +goose Down
DROP TABLE traffic_not_found;
ALTER TABLE traffic_daily DROP COLUMN bots;
ALTER TABLE traffic_hourly DROP COLUMN bots;
