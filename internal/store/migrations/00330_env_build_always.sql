-- +goose Up
-- Every app variable reaches the image build (docs/decisions.md, "Every
-- variable reaches the build"): the per-variable build flag of 00310 goes.
ALTER TABLE env_vars DROP COLUMN build;

-- The variables the build of a deployment read, so a change to one of them
-- asks for a new deploy and a change to any other for a restart: '' (none),
-- '*' (every variable: bitploy's own build with a build script) or the names,
-- comma-separated (the ARG names and secret ids of the app's own Dockerfile).
ALTER TABLE deployments ADD COLUMN build_reads TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE deployments DROP COLUMN build_reads;
ALTER TABLE env_vars ADD COLUMN build TEXT NOT NULL DEFAULT '' CHECK (build IN ('', 'public', 'secret'));
