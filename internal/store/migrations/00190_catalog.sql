-- +goose Up
-- Apps deployed from the one-click catalog remember which entry and version
-- they were made from, so the dashboard can offer an update. Empty: not a
-- catalog app.
ALTER TABLE apps ADD COLUMN catalog_id TEXT NOT NULL DEFAULT '';
ALTER TABLE apps ADD COLUMN catalog_version TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE apps DROP COLUMN catalog_version;
ALTER TABLE apps DROP COLUMN catalog_id;
