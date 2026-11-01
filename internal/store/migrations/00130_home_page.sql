-- +goose Up
-- The page served at "/" when a static site has no index.html but several
-- pages at the top, e.g. "Planning tool v3.html". Empty: decide from the files
-- (index.html, else the only page). Only used in build modes auto and static.
ALTER TABLE apps ADD COLUMN home_page TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE apps DROP COLUMN home_page;
