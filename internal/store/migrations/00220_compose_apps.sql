-- +goose Up
-- Compose apps (docs/compose-plan.md, "Phase 2").

-- Where the compose file is in the source; '' = the first of compose.yaml,
-- compose.yml, docker-compose.yaml, docker-compose.yml (in context_dir, then
-- at the root).
ALTER TABLE apps ADD COLUMN compose_path TEXT NOT NULL DEFAULT '';

-- compose user and working_dir; '' = the image's.
ALTER TABLE app_services ADD COLUMN run_user TEXT NOT NULL DEFAULT '';
ALTER TABLE app_services ADD COLUMN working_dir TEXT NOT NULL DEFAULT '';

-- The services and volumes a deployment of a compose app runs, as JSON
-- ({services: [...], volumes: [...]}, the app_services and app_volumes rows it
-- writes when it goes live). A rollback restores them. '' for single-service
-- deployments and for deployments from before compose.
ALTER TABLE deployments ADD COLUMN services_spec TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE deployments DROP COLUMN services_spec;
ALTER TABLE app_services DROP COLUMN working_dir;
ALTER TABLE app_services DROP COLUMN run_user;
ALTER TABLE apps DROP COLUMN compose_path;
