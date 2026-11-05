-- +goose Up
-- Apps to restart once a deployment is live: add_service with connect_to
-- writes ${{<alias>.<main_var>}} into the connected apps, which restart when
-- the new service runs. Kept here (not in memory) so a bitploy restart in
-- between does not lose them: the reconciler restarts them for a deployment
-- that went live and drops them for one that failed.
CREATE TABLE deployment_restarts (
    deployment_id TEXT NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    app_id        TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    created_at    INTEGER NOT NULL,
    PRIMARY KEY (deployment_id, app_id)
);
CREATE INDEX deployment_restarts_app ON deployment_restarts(app_id);

-- +goose Down
DROP TABLE deployment_restarts;
