-- +goose Up
-- All timestamps are unix seconds (UTC). IDs are prefixed random strings.

CREATE TABLE users (
    id                      TEXT PRIMARY KEY,               -- usr_…
    email                   TEXT NOT NULL UNIQUE COLLATE NOCASE,
    name                    TEXT NOT NULL,
    password_hash           TEXT NOT NULL,
    role                    TEXT NOT NULL DEFAULT 'user',   -- user | admin
    status                  TEXT NOT NULL DEFAULT 'active', -- active | suspended
    quota_apps              INTEGER NOT NULL DEFAULT 10,
    quota_memory_mb         INTEGER NOT NULL DEFAULT 2048,
    quota_cpu_millis        INTEGER NOT NULL DEFAULT 2000,  -- 1000 = 1 CPU
    quota_deploys_per_hour  INTEGER NOT NULL DEFAULT 20,
    quota_domains_per_app   INTEGER NOT NULL DEFAULT 5,
    quota_volume_mb         INTEGER NOT NULL DEFAULT 5120,
    github_token_enc        BLOB,
    github_login            TEXT,
    github_token_expires_at INTEGER,
    created_at              INTEGER NOT NULL,
    updated_at              INTEGER NOT NULL
);

CREATE TABLE sessions (
    id_hash             TEXT PRIMARY KEY,  -- hex SHA-256 of the cookie value
    user_id             TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf_token          TEXT NOT NULL,
    ip                  TEXT NOT NULL DEFAULT '',
    user_agent          TEXT NOT NULL DEFAULT '',
    created_at          INTEGER NOT NULL,
    last_seen_at        INTEGER NOT NULL,
    expires_at          INTEGER NOT NULL,  -- sliding, 14 days
    absolute_expires_at INTEGER NOT NULL   -- hard limit, 30 days
);
CREATE INDEX sessions_user ON sessions(user_id);

CREATE TABLE api_tokens (
    id           TEXT PRIMARY KEY,                -- tok_…
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    prefix       TEXT NOT NULL,                   -- first 8 chars, for display
    hash         TEXT NOT NULL UNIQUE,            -- hex SHA-256 of the full token
    scope        TEXT NOT NULL,                   -- read | deploy
    expires_at   INTEGER,
    last_used_at INTEGER,
    last_used_ip TEXT,
    revoked_at   INTEGER,
    created_at   INTEGER NOT NULL
);
CREATE INDEX api_tokens_user ON api_tokens(user_id);

CREATE TABLE invites (
    id         TEXT PRIMARY KEY,                  -- inv_…
    code_hash  TEXT NOT NULL UNIQUE,              -- hex SHA-256 of the invite code
    email      TEXT,                              -- optional: lock to one address
    role       TEXT NOT NULL DEFAULT 'user',
    created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    expires_at INTEGER NOT NULL,
    used_at    INTEGER,
    used_by    TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL
);

CREATE TABLE apps (
    id                    TEXT PRIMARY KEY,       -- app_…
    user_id               TEXT NOT NULL REFERENCES users(id),
    slug                  TEXT NOT NULL UNIQUE,
    name                  TEXT NOT NULL,
    type                  TEXT NOT NULL DEFAULT 'web',   -- web | worker
    port                  INTEGER,                        -- NULL: use the single EXPOSE
    health_path           TEXT NOT NULL DEFAULT '/',
    dockerfile_path       TEXT NOT NULL DEFAULT 'Dockerfile',
    context_dir           TEXT NOT NULL DEFAULT '.',
    volume_enabled        INTEGER NOT NULL DEFAULT 0,
    memory_mb             INTEGER NOT NULL DEFAULT 512,
    cpu_millis            INTEGER NOT NULL DEFAULT 500,
    git_repo_url          TEXT,
    git_branch            TEXT,
    git_subdir            TEXT,
    desired_state         TEXT NOT NULL DEFAULT 'running', -- running | stopped
    status                TEXT NOT NULL DEFAULT 'new',     -- new | deploying | running | stopped | failed | crashed
    status_message        TEXT NOT NULL DEFAULT '',
    current_deployment_id TEXT,
    deploy_seq            INTEGER NOT NULL DEFAULT 0,      -- last deployment number n
    created_at            INTEGER NOT NULL,
    updated_at            INTEGER NOT NULL
);
CREATE INDEX apps_user ON apps(user_id);

-- Old slugs stay reserved for their owner for 30 days after a rename or delete.
CREATE TABLE slug_reservations (
    slug       TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL,
    expires_at INTEGER NOT NULL
);

CREATE TABLE env_vars (
    app_id     TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    value_enc  BLOB NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (app_id, name)
);

CREATE TABLE deployments (
    id               TEXT PRIMARY KEY,           -- dep_…
    app_id           TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    seq              INTEGER NOT NULL,           -- n in bitploy/<slug>:<n> and bp-<slug>-<n>
    state            TEXT NOT NULL,              -- queued | fetching | validating | building | starting | health_check | swapping | running | superseded | failed | cancelled
    source_kind      TEXT NOT NULL,              -- files | upload | git | rollback
    source_ref       TEXT NOT NULL DEFAULT '',   -- upload id, git branch, or rolled-back deployment id
    commit_sha       TEXT,
    commit_message   TEXT,
    channel          TEXT NOT NULL,              -- dashboard | api | mcp | system
    created_by       TEXT,
    token_id         TEXT,
    image            TEXT,                       -- bitploy/<slug>:<n>
    image_size_bytes INTEGER,
    container_name   TEXT,                       -- bp-<slug>-<n>
    port             INTEGER,                    -- resolved port for this deployment
    has_healthcheck  INTEGER NOT NULL DEFAULT 0,
    error_code       TEXT,
    error_message    TEXT,
    error_hint       TEXT,
    error_next       TEXT,
    log_tail         TEXT,                       -- last 50 lines on failure
    warnings         TEXT,                       -- JSON array of dockerfile findings
    cancel_requested INTEGER NOT NULL DEFAULT 0,
    queued_at        INTEGER NOT NULL,
    started_at       INTEGER,
    built_at         INTEGER,
    finished_at      INTEGER,
    UNIQUE (app_id, seq)
);
CREATE INDEX deployments_app ON deployments(app_id, seq DESC);
CREATE INDEX deployments_state ON deployments(state, queued_at);

CREATE TABLE uploads (
    id          TEXT PRIMARY KEY,                -- upl_… (unguessable; the credential for POST /u/{id})
    app_id      TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    user_id     TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pending', -- pending | received | used | expired
    size_bytes  INTEGER,
    expires_at  INTEGER NOT NULL,
    received_at INTEGER,
    used_at     INTEGER,
    created_at  INTEGER NOT NULL
);

CREATE TABLE domains (
    id              TEXT PRIMARY KEY,           -- dom_…
    app_id          TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    hostname        TEXT NOT NULL UNIQUE COLLATE NOCASE,
    status          TEXT NOT NULL DEFAULT 'pending_dns', -- pending_dns | issuing_cert | active | failed
    txt_token       TEXT NOT NULL,
    last_error      TEXT NOT NULL DEFAULT '',
    last_checked_at INTEGER,
    check_until     INTEGER NOT NULL,           -- background checks stop after 48 h
    activated_at    INTEGER,
    created_at      INTEGER NOT NULL
);
CREATE INDEX domains_app ON domains(app_id);

CREATE TABLE metric_samples (
    app_id    TEXT NOT NULL,
    ts        INTEGER NOT NULL,
    cpu       REAL NOT NULL,      -- % of one core
    mem_bytes INTEGER NOT NULL,
    rx_bytes  INTEGER NOT NULL,   -- bytes received during the interval
    tx_bytes  INTEGER NOT NULL
);
CREATE INDEX metric_samples_app_ts ON metric_samples(app_id, ts);

CREATE TABLE metric_rollups (
    app_id      TEXT NOT NULL,
    ts          INTEGER NOT NULL, -- start of the 5-minute bucket
    cpu         REAL NOT NULL,
    mem_bytes   INTEGER NOT NULL,
    rx_bytes    INTEGER NOT NULL,
    tx_bytes    INTEGER NOT NULL,
    cpu_max     REAL NOT NULL,
    mem_max     INTEGER NOT NULL,
    PRIMARY KEY (app_id, ts)
);

-- Running seconds per app per day, for usage numbers.
CREATE TABLE usage_daily (
    app_id          TEXT NOT NULL,
    user_id         TEXT NOT NULL,
    day             TEXT NOT NULL,  -- YYYY-MM-DD UTC
    running_seconds INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (app_id, day)
);

-- Disk used per app (image size + volume size), measured nightly.
CREATE TABLE app_disk (
    app_id       TEXT PRIMARY KEY,
    image_bytes  INTEGER NOT NULL DEFAULT 0,
    volume_bytes INTEGER NOT NULL DEFAULT 0,
    measured_at  INTEGER NOT NULL
);

CREATE TABLE audit_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    ts          INTEGER NOT NULL,
    user_id     TEXT,
    channel     TEXT NOT NULL,   -- dashboard | api | mcp | system
    token_id    TEXT,
    ip          TEXT NOT NULL DEFAULT '',
    action      TEXT NOT NULL,   -- e.g. app.create, token.revoke, deployment.failed
    target_type TEXT NOT NULL DEFAULT '',
    target_id   TEXT NOT NULL DEFAULT '',
    details     TEXT NOT NULL DEFAULT '{}'  -- JSON, never secrets
);
CREATE INDEX audit_log_ts ON audit_log(ts);
CREATE INDEX audit_log_user ON audit_log(user_id, ts);

CREATE TABLE idempotency_keys (
    user_id     TEXT NOT NULL,
    key         TEXT NOT NULL,
    method      TEXT NOT NULL,
    path        TEXT NOT NULL,
    status      INTEGER NOT NULL,
    body        BLOB NOT NULL,
    created_at  INTEGER NOT NULL,
    PRIMARY KEY (user_id, key)
);

-- +goose Down
DROP TABLE idempotency_keys;
DROP TABLE audit_log;
DROP TABLE app_disk;
DROP TABLE usage_daily;
DROP TABLE metric_rollups;
DROP TABLE metric_samples;
DROP TABLE domains;
DROP TABLE uploads;
DROP TABLE deployments;
DROP TABLE env_vars;
DROP TABLE slug_reservations;
DROP TABLE apps;
DROP TABLE invites;
DROP TABLE api_tokens;
DROP TABLE sessions;
DROP TABLE users;
