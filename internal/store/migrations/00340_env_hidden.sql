-- +goose Up
-- Readable variables (docs/decisions.md, "Variables are readable unless hidden"):
-- the API returns the value of a variable unless it is hidden. Every value
-- stays encrypted at rest. Existing variables become readable.
ALTER TABLE env_vars ADD COLUMN hidden INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE env_vars DROP COLUMN hidden;
