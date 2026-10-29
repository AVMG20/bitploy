-- +goose Up
-- One row: the identity of this bitploy installation. It is put on every Docker
-- container, network, volume and image bitploy creates (label nl.bitploy.instance),
-- so cleanup code never touches resources of another installation on the same daemon.
CREATE TABLE instance (
    id         TEXT PRIMARY KEY,
    created_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE instance;
