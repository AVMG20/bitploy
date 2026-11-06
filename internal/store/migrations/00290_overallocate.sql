-- +goose Up
-- overallocate: the user's memory and CPU are not checked against a quota
-- (the account's or the server's room), so the apps may ask for more than the
-- server has. Set by an admin, meant for the people who run the server.
ALTER TABLE users ADD COLUMN overallocate INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE users DROP COLUMN overallocate;
