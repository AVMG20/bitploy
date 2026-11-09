-- +goose Up
-- The order a user put their projects in (drag and drop in the dashboard
-- sidebar). Per user: a shared project can sit at a different place for each
-- member. A project without a row (new, or newly shared) comes first, newest
-- first, then the rest by position.
CREATE TABLE project_positions (
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    position   INTEGER NOT NULL,
    PRIMARY KEY (user_id, project_id)
);

-- +goose Down
DROP TABLE project_positions;
