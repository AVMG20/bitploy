-- +goose Up
-- Nightly pruning deletes by timestamp only; the existing indexes lead with app_id.
CREATE INDEX metric_samples_ts ON metric_samples(ts);
CREATE INDEX metric_rollups_ts ON metric_rollups(ts);
CREATE INDEX domains_status ON domains(status);
CREATE INDEX uploads_created ON uploads(created_at);

-- +goose Down
DROP INDEX uploads_created;
DROP INDEX domains_status;
DROP INDEX metric_rollups_ts;
DROP INDEX metric_samples_ts;
