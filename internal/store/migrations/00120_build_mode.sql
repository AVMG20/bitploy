-- +goose Up
-- How an app's source becomes an image:
--   dockerfile  the source must hold a Dockerfile (the default, and the only mode before this migration)
--   auto        a Dockerfile if there is one, else a plain website (index.html at the top) served by nginx
--   static      always serve the source as a plain website, even if it holds a Dockerfile
ALTER TABLE apps ADD COLUMN build_mode TEXT NOT NULL DEFAULT 'dockerfile';

-- +goose Down
ALTER TABLE apps DROP COLUMN build_mode;
