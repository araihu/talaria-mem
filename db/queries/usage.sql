-- name: UpsertUsageDaily :exec
INSERT INTO usage_daily(memory_id, day, hits, opportunities)
VALUES (?, ?, ?, ?)
ON CONFLICT(memory_id, day) DO UPDATE SET
    hits = usage_daily.hits + excluded.hits,
    opportunities = usage_daily.opportunities + excluded.opportunities;

-- name: DeleteUsageBefore :execrows
DELETE FROM usage_daily WHERE day < ?;

-- name: UpsertUsageLifetime :exec
INSERT INTO usage_lifetime(memory_id, hits, opportunities)
VALUES (?, ?, ?)
ON CONFLICT(memory_id) DO UPDATE SET
    hits = usage_lifetime.hits + excluded.hits,
    opportunities = usage_lifetime.opportunities + excluded.opportunities;
