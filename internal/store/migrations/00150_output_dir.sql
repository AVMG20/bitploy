-- +goose Up
-- The folder a static build serves, relative to the project (for example dist,
-- build or out). Empty: the default of the detected framework. Only used in
-- build modes auto and static_build.
ALTER TABLE apps ADD COLUMN output_dir TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE apps DROP COLUMN output_dir;
