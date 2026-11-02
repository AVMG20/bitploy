-- +goose Up
-- How a deployment was built, as decided by PlanBuild: dockerfile (the user's
-- own Dockerfile), static, node-spa or node (bitploy's own templates).
-- static and node-spa run only nginx serving files, so an app whose live
-- deployment has one of these kinds is a static site: it does not count towards
-- the memory and CPU allowance and runs with fixed small limits. Empty: built
-- before this column existed, treated like dockerfile.
ALTER TABLE deployments ADD COLUMN build_kind TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE deployments DROP COLUMN build_kind;
