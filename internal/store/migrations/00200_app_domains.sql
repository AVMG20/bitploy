-- +goose Up
-- Extra wildcard domains for app addresses, added by an admin. The default
-- domain (BITPLOY_APPS_DOMAIN) is not stored here: it always exists. Each
-- domain may carry a Cloudflare Origin certificate; its private key is
-- encrypted with the server's secret key, like env vars. bitploy writes both
-- as files for Traefik (proxy.Writer.WriteCerts).
CREATE TABLE app_domains (
    id             TEXT PRIMARY KEY,             -- apd_…
    domain         TEXT NOT NULL UNIQUE COLLATE NOCASE,
    cert_pem       TEXT NOT NULL DEFAULT '',     -- '' = no certificate: Traefik serves the default one
    key_enc        BLOB,                         -- sealed private key, NULL without a certificate
    cert_not_after INTEGER,                      -- when the certificate expires
    created_at     INTEGER NOT NULL
);

-- Which extra domains a user may put apps on. Admins may use every domain.
CREATE TABLE user_app_domains (
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    domain_id  TEXT NOT NULL REFERENCES app_domains(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, domain_id)
);
CREATE INDEX user_app_domains_domain ON user_app_domains(domain_id);

-- The domain an app's address is on. '' = the default domain. Slugs stay
-- unique across all domains: the slug names containers, routes and volumes.
ALTER TABLE apps ADD COLUMN base_domain TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE apps DROP COLUMN base_domain;
DROP TABLE user_app_domains;
DROP TABLE app_domains;
