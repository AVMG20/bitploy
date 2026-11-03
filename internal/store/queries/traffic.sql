-- Queries for traffic stats (internal/traffic). Writes add deltas.

-- name: TrafficListApps :many
SELECT id, slug FROM apps;

-- name: TrafficListDomains :many
-- Only domains that have a router (see ActiveHostnames).
SELECT app_id, hostname FROM domains WHERE status IN ('issuing_cert', 'active');

-- name: TrafficAddHourly :exec
INSERT INTO traffic_hourly (app_id, hour, requests, errors_4xx, errors_5xx, visitors, bots)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (app_id, hour) DO UPDATE SET
    requests = requests + excluded.requests,
    errors_4xx = errors_4xx + excluded.errors_4xx,
    errors_5xx = errors_5xx + excluded.errors_5xx,
    visitors = visitors + excluded.visitors,
    bots = bots + excluded.bots;

-- name: TrafficAddDaily :exec
INSERT INTO traffic_daily (app_id, day, requests, errors_4xx, errors_5xx, visitors, bots)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (app_id, day) DO UPDATE SET
    requests = requests + excluded.requests,
    errors_4xx = errors_4xx + excluded.errors_4xx,
    errors_5xx = errors_5xx + excluded.errors_5xx,
    visitors = visitors + excluded.visitors,
    bots = bots + excluded.bots;

-- name: TrafficAddPath :exec
INSERT INTO traffic_paths (app_id, day, path, views) VALUES (?, ?, ?, ?)
ON CONFLICT (app_id, day, path) DO UPDATE SET views = views + excluded.views;

-- name: TrafficAddCountry :exec
INSERT INTO traffic_countries (app_id, day, country, requests, visitors) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (app_id, day, country) DO UPDATE SET
    requests = requests + excluded.requests,
    visitors = visitors + excluded.visitors;

-- name: TrafficAddReferrer :exec
INSERT INTO traffic_referrers (app_id, day, host, visits) VALUES (?, ?, ?, ?)
ON CONFLICT (app_id, day, host) DO UPDATE SET visits = visits + excluded.visits;

-- name: TrafficAddNotFound :exec
INSERT INTO traffic_not_found (app_id, day, path, hits) VALUES (?, ?, ?, ?)
ON CONFLICT (app_id, day, path) DO UPDATE SET hits = hits + excluded.hits;

-- name: TrafficGetCursor :one
SELECT pos, head_len, head_hash FROM traffic_cursor WHERE id = 1;

-- name: TrafficSetCursor :exec
INSERT INTO traffic_cursor (id, pos, head_len, head_hash) VALUES (1, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET pos = excluded.pos, head_len = excluded.head_len, head_hash = excluded.head_hash;

-- name: TrafficHourlySince :many
SELECT hour, requests, errors_4xx, errors_5xx, visitors, bots
FROM traffic_hourly WHERE app_id = ? AND hour >= ? ORDER BY hour;

-- name: TrafficDailySince :many
SELECT day, requests, errors_4xx, errors_5xx, visitors, bots
FROM traffic_daily WHERE app_id = ? AND day >= ? ORDER BY day;

-- name: TrafficTopPaths :many
SELECT path, CAST(SUM(views) AS INTEGER) AS views FROM traffic_paths
WHERE app_id = ? AND day >= ? AND path <> '(other)'
GROUP BY path ORDER BY views DESC, path LIMIT ?;

-- name: TrafficTopCountries :many
SELECT country, CAST(SUM(requests) AS INTEGER) AS requests, CAST(SUM(visitors) AS INTEGER) AS visitors
FROM traffic_countries WHERE app_id = ? AND day >= ?
GROUP BY country ORDER BY visitors DESC, requests DESC, country LIMIT ?;

-- name: TrafficTopReferrers :many
SELECT host, CAST(SUM(visits) AS INTEGER) AS visits FROM traffic_referrers
WHERE app_id = ? AND day >= ? AND host <> '(other)'
GROUP BY host ORDER BY visits DESC, host LIMIT ?;

-- name: TrafficTopNotFound :many
SELECT path, CAST(SUM(hits) AS INTEGER) AS hits FROM traffic_not_found
WHERE app_id = ? AND day >= ? AND path <> '(other)'
GROUP BY path ORDER BY hits DESC, path LIMIT ?;

-- name: TrafficFirstHour :one
-- The oldest hour still stored for any app: stats go back no further.
SELECT CAST(COALESCE(MIN(hour), 0) AS INTEGER) FROM traffic_hourly;

-- name: TrafficFirstDay :one
SELECT CAST(COALESCE(MIN(day), '') AS TEXT) FROM traffic_daily;

-- name: TrafficPruneHourly :exec
DELETE FROM traffic_hourly WHERE hour < ?;

-- name: TrafficPruneDaily :exec
DELETE FROM traffic_daily WHERE day < ?;

-- name: TrafficPrunePaths :exec
DELETE FROM traffic_paths WHERE day < ?;

-- name: TrafficPruneCountries :exec
DELETE FROM traffic_countries WHERE day < ?;

-- name: TrafficPruneReferrers :exec
DELETE FROM traffic_referrers WHERE day < ?;

-- name: TrafficPruneNotFound :exec
DELETE FROM traffic_not_found WHERE day < ?;
