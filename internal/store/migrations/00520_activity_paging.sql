-- +goose Up
-- The Activity page reads its rows branch by branch (activity.sql), each
-- newest first with a LIMIT, so a page reads about one page of rows. The
-- index on user_id alone is ordered by id (the rowid), which the user's own
-- rows need; audit_log_user is ordered by ts.
CREATE INDEX audit_log_user_id ON audit_log(user_id);

-- +goose Down
DROP INDEX audit_log_user_id;
