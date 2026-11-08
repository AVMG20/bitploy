-- +goose Up
-- Project members (docs/decisions.md, "Project members"): the owner of a
-- project (projects.user_id) shares it with other users. A member sees the
-- project and its apps and may do on them what a deploy token of the owner
-- may; the owner alone manages the members and deletes the project. The
-- owner is never a row here.
CREATE TABLE project_members (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       TEXT NOT NULL DEFAULT 'member',               -- member
    invited_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (project_id, user_id)
);
CREATE INDEX project_members_user ON project_members(user_id);

-- Invite links to a project: single use, 7 days. email locks the invite to
-- one address: only the account with it (or a signup with it) can accept.
CREATE TABLE project_invites (
    id         TEXT PRIMARY KEY,                             -- pin_…
    code_hash  TEXT NOT NULL UNIQUE,                         -- hex SHA-256 of the invite code
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    email      TEXT,
    role       TEXT NOT NULL DEFAULT 'member',
    created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    expires_at INTEGER NOT NULL,
    used_at    INTEGER,
    used_by    TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL
);
CREATE INDEX project_invites_project ON project_invites(project_id, created_at);

-- created_by: who made the app when that was not its owner (a member of the
-- owner's project); '' when the owner made it.
ALTER TABLE apps ADD COLUMN created_by TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE apps DROP COLUMN created_by;
DROP TABLE project_invites;
DROP TABLE project_members;
