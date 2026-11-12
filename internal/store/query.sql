-- name: UpsertAccount :one
INSERT INTO accounts (platform, handle, external_id, auth_ref)
VALUES ($1, $2, $3, $4)
ON CONFLICT (platform, handle) DO UPDATE
SET external_id = COALESCE(EXCLUDED.external_id, accounts.external_id),
    auth_ref    = EXCLUDED.auth_ref
RETURNING id;

-- name: UpsertApp :one
INSERT INTO apps (slug, name, appstore_id, play_package, links)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (slug) DO UPDATE
SET name = EXCLUDED.name, appstore_id = EXCLUDED.appstore_id,
    play_package = EXCLUDED.play_package, links = EXCLUDED.links
RETURNING id;

-- name: UpsertContentItem :one
-- inserted is true only for the first sighting, so "new post" events fire once.
INSERT INTO content_items (account_id, app_id, source_platform, external_id, url, title, body, published_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (source_platform, external_id) DO UPDATE
SET url          = COALESCE(EXCLUDED.url, content_items.url),
    title        = COALESCE(EXCLUDED.title, content_items.title),
    body         = COALESCE(EXCLUDED.body, content_items.body),
    published_at = COALESCE(EXCLUDED.published_at, content_items.published_at),
    account_id   = COALESCE(content_items.account_id, EXCLUDED.account_id),
    app_id       = COALESCE(content_items.app_id, EXCLUDED.app_id)
RETURNING id, (xmax = 0)::boolean AS inserted;

-- name: InsertInboundItem :one
-- Returns no row (pgx.ErrNoRows) when the item was already stored.
INSERT INTO inbound_items (platform, kind, external_id, content_item_id, app_id, author, text, rating, url, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT DO NOTHING
RETURNING id;

-- name: InsertMetricSnapshot :one
-- Returns no row when this run already stored a snapshot for the same subject.
INSERT INTO metric_snapshots (content_item_id, account_id, platform, taken_at, likes, views, reposts, comments, followers, extra, source_run_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT DO NOTHING
RETURNING id;

-- name: LatestContentSnapshot :one
SELECT * FROM metric_snapshots
WHERE content_item_id = $1
ORDER BY taken_at DESC
LIMIT 1;

-- name: LatestAccountSnapshot :one
SELECT * FROM metric_snapshots
WHERE account_id = $1
ORDER BY taken_at DESC
LIMIT 1;

-- name: ContentSnapshotAtOrBefore :one
SELECT * FROM metric_snapshots
WHERE content_item_id = $1 AND taken_at <= $2
ORDER BY taken_at DESC
LIMIT 1;

-- name: AccountSnapshotAtOrBefore :one
SELECT * FROM metric_snapshots
WHERE account_id = $1 AND taken_at <= $2
ORDER BY taken_at DESC
LIMIT 1;

-- name: EarliestContentSnapshot :one
SELECT * FROM metric_snapshots
WHERE content_item_id = $1
ORDER BY taken_at ASC
LIMIT 1;

-- name: EarliestAccountSnapshot :one
SELECT * FROM metric_snapshots
WHERE account_id = $1
ORDER BY taken_at ASC
LIMIT 1;

-- name: InsertEvent :one
INSERT INTO events (type, rule, target, payload)
VALUES ($1, $2, $3, $4)
RETURNING id;

-- name: PendingEvents :many
SELECT * FROM events
WHERE sent_at IS NULL AND target IS NOT NULL AND attempts < sqlc.arg(max_attempts)::int AND next_attempt_at <= now()
ORDER BY id
LIMIT sqlc.arg(lim)::int;

-- name: MarkEventSent :exec
UPDATE events SET sent_at = now(), attempts = attempts + 1, last_error = NULL WHERE id = $1;

-- name: MarkEventFailed :exec
UPDATE events SET attempts = attempts + 1, last_error = $2, next_attempt_at = $3 WHERE id = $1;

-- name: InsertApifyRun :one
-- Returns no row when the run is already known (redelivered webhook).
INSERT INTO apify_runs (run_id, task_id, task_name, actor_id, status)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (run_id) DO NOTHING
RETURNING run_id;

-- name: GetApifyRun :one
SELECT * FROM apify_runs WHERE run_id = $1;

-- name: MarkApifyRunProcessed :exec
UPDATE apify_runs
SET state = 'processed', status = $2, dataset_id = $3, started_at = $4, finished_at = $5,
    items = $6, new_items = $7, cost_usd = $8, processed_at = now(),
    attempts = attempts + 1, last_error = NULL
WHERE run_id = $1;

-- name: MarkApifyRunFailed :exec
UPDATE apify_runs SET state = 'failed', attempts = attempts + 1, last_error = $2, next_attempt_at = $3 WHERE run_id = $1;

-- name: RetryableApifyRuns :many
SELECT * FROM apify_runs
WHERE state IN ('received', 'failed') AND attempts < sqlc.arg(max_attempts)::int AND next_attempt_at <= now()
ORDER BY received_at
LIMIT 20;

-- name: ApifySpendSince :one
SELECT COALESCE(SUM(cost_usd), 0)::double precision AS usd, COUNT(*)::bigint AS runs
FROM apify_runs WHERE received_at >= $1;

-- name: GiveUpEvent :exec
-- Permanent failure (4xx from the target): stop retrying but keep the row for inspection.
UPDATE events SET attempts = sqlc.arg(max_attempts)::int, last_error = $1 WHERE id = $2;

-- name: EnsureCollectorState :exec
INSERT INTO collector_state (account_id, capability) VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: DueCollectorStates :many
SELECT * FROM collector_state
WHERE next_run_at <= sqlc.arg(now)::timestamptz
ORDER BY next_run_at
LIMIT 50;

-- name: CollectorOK :exec
-- A NULL cursor keeps the previous one.
UPDATE collector_state
SET cursor = COALESCE(sqlc.narg(cursor)::jsonb, cursor), next_run_at = sqlc.arg(next_run_at),
    last_run_at = sqlc.arg(ran_at), last_ok_at = sqlc.arg(ran_at), fail_count = 0, last_error = NULL
WHERE account_id = sqlc.arg(account_id) AND capability = sqlc.arg(capability);

-- name: CollectorFailed :one
-- counts=false records the error without counting a failure (secret not set yet).
UPDATE collector_state
SET next_run_at = sqlc.arg(next_run_at), last_run_at = sqlc.arg(ran_at), last_error = sqlc.arg(last_error),
    fail_count = fail_count + CASE WHEN sqlc.arg(counts)::boolean THEN 1 ELSE 0 END
WHERE account_id = sqlc.arg(account_id) AND capability = sqlc.arg(capability)
RETURNING fail_count;

-- name: PostponeCollector :exec
UPDATE collector_state SET next_run_at = $3 WHERE account_id = $1 AND capability = $2;

-- name: ListCollectorStates :many
SELECT * FROM collector_state ORDER BY account_id, capability;

-- name: ListAccounts :many
SELECT * FROM accounts ORDER BY id;
