-- Public ports (migration 00270): a host port forwarded to an app without a web address.

-- name: GetPublicPort :one
SELECT * FROM public_ports WHERE app_id = ?;

-- name: ListPublicPorts :many
SELECT * FROM public_ports ORDER BY port;

-- name: ListPublicPortsWithApps :many
SELECT p.app_id, p.port, p.service, p.allow_from, p.expires_at, p.last_error, p.created_at,
       a.slug, a.name, a.user_id, u.email
FROM public_ports p
JOIN apps a ON a.id = p.app_id
JOIN users u ON u.id = a.user_id
ORDER BY p.port;

-- name: ListUsedPublicPorts :many
SELECT port FROM public_ports ORDER BY port;

-- name: CountPublicPortsByUser :one
SELECT COUNT(*) FROM public_ports p JOIN apps a ON a.id = p.app_id WHERE a.user_id = ?;

-- name: InsertPublicPort :exec
INSERT INTO public_ports (app_id, port, service, allow_from, expires_at, last_error, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, '', ?, ?);

-- name: UpdatePublicPortSettings :exec
UPDATE public_ports SET service = ?, allow_from = ?, expires_at = ?, updated_at = ? WHERE app_id = ?;

-- name: SetPublicPortNumber :exec
UPDATE public_ports SET port = ?, updated_at = ? WHERE app_id = ?;

-- name: SetPublicPortError :exec
UPDATE public_ports SET last_error = ? WHERE app_id = ?;

-- name: DeletePublicPort :execrows
DELETE FROM public_ports WHERE app_id = ?;

-- name: ListExpiredPublicPorts :many
SELECT * FROM public_ports WHERE expires_at IS NOT NULL AND expires_at <= ? ORDER BY port;
