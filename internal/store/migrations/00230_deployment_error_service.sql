-- +goose Up
-- The service a failed deployment of an app with several services failed in
-- ('' when the failure is not about one service). Deployment.error.service
-- and Deployment.services[].error read it.
ALTER TABLE deployments ADD COLUMN error_service TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE deployments DROP COLUMN error_service;
