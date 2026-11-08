-- +goose Up
-- CPU and memory of the whole server, for the charts on the admin Server tab.
-- Sampled with the app metrics (every 15 s) and rolled up and pruned by the
-- same rules: raw samples for 24 hours, 5-minute rollups for 30 days.
CREATE TABLE host_samples (
    ts              INTEGER NOT NULL,
    cpu             REAL NOT NULL,    -- % of the whole server (all cores), 0-100
    mem_used_bytes  INTEGER NOT NULL, -- excluding reclaimable cache
    mem_total_bytes INTEGER NOT NULL
);
CREATE INDEX host_samples_ts ON host_samples(ts);

CREATE TABLE host_rollups (
    ts              INTEGER PRIMARY KEY, -- start of the 5-minute bucket
    cpu             REAL NOT NULL,
    mem_used_bytes  INTEGER NOT NULL,
    mem_total_bytes INTEGER NOT NULL,
    cpu_max         REAL NOT NULL,
    mem_max         INTEGER NOT NULL
);

-- +goose Down
DROP TABLE host_rollups;
DROP TABLE host_samples;
