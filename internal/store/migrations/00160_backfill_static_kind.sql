-- +goose Up
-- Deployments from before 00140 have build_kind ''. An app in build mode
-- static only ever served plain website files with bitploy's nginx template,
-- so its live deployment is a static site: mark it, and give the app row the
-- fixed size of a static site (64 MB, 0.5 CPU; it takes effect when the
-- container is next recreated, which a deploy or restart does).
--
-- Skipped when the audit log shows the build mode changed after the live
-- deployment was queued: it may have been built from a Dockerfile.
--
-- Apps in build mode auto are not touched: nothing stored says whether their
-- deployment used the static template (the build log is a file, not a column).
-- They get their kind with the next deploy.
UPDATE deployments SET build_kind = 'static'
WHERE build_kind = '' AND id IN (
    SELECT a.current_deployment_id FROM apps a
    WHERE a.build_mode = 'static' AND a.current_deployment_id IS NOT NULL
      AND NOT EXISTS (
          SELECT 1 FROM audit_log l
          WHERE l.action = 'app.update' AND l.target_type = 'app' AND l.target_id = a.id
            AND l.details LIKE '%"build_mode"%'
            AND l.ts >= (SELECT d.queued_at FROM deployments d WHERE d.id = a.current_deployment_id)
      )
);

UPDATE apps SET memory_mb = 64, cpu_millis = 500
WHERE build_mode = 'static' AND current_deployment_id IN (SELECT id FROM deployments WHERE build_kind = 'static')
  AND (memory_mb != 64 OR cpu_millis != 500);

-- +goose Down
SELECT 1;
