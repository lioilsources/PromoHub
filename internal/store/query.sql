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
INSERT INTO inbound_items (platform, kind, external_id, content_item_id, app_id, author, text, rating, url, created_at, account_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
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

-- name: InsertCampaign :one
INSERT INTO campaigns (app_id, source_content_item_id, lang, title, body, link, media, llm_status)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetCampaign :one
SELECT * FROM campaigns WHERE id = $1;

-- name: SetCampaignMessage :exec
UPDATE campaigns SET tg_chat_id = $2, tg_message_id = $3 WHERE id = $1;

-- name: InsertDraft :one
INSERT INTO drafts (campaign_id, account_id, via, text, extra)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (campaign_id, account_id) DO NOTHING
RETURNING *;

-- name: GetDraft :one
SELECT * FROM drafts WHERE id = $1;

-- name: CampaignDrafts :many
SELECT * FROM drafts WHERE campaign_id = $1 ORDER BY id;

-- name: SetDraftMessage :exec
UPDATE drafts SET tg_chat_id = $2, tg_message_id = $3 WHERE id = $1;

-- name: SetDraftText :one
-- Editing is allowed until the draft is handed to the publisher.
UPDATE drafts SET text = $2, extra = $3, updated_at = now()
WHERE id = $1 AND status IN ('draft', 'approved', 'failed')
RETURNING *;

-- name: ApproveDraft :one
UPDATE drafts SET status = 'approved', approved_at = sqlc.arg(now)::timestamptz, scheduled_at = sqlc.narg(scheduled_at), error = NULL, updated_at = now()
WHERE id = sqlc.arg(id) AND status IN ('draft', 'failed', 'approved')
RETURNING *;

-- name: SkipDraft :one
UPDATE drafts SET status = 'skipped', updated_at = now()
WHERE id = $1 AND status IN ('draft', 'approved', 'failed', 'unknown')
RETURNING *;

-- name: ApprovedSince :one
-- Drafts of an account approved or published in the window, for max_per_day.
SELECT count(*)::bigint FROM drafts
WHERE account_id = $1 AND id <> $2 AND status IN ('approved', 'publishing', 'published', 'sent', 'unknown')
  AND approved_at >= $3;

-- name: ClaimDueDraft :one
-- Hands one due draft to the publisher. The attempt key survives automatic retries so APIs
-- that dedup on it (Mastodon Idempotency-Key) see the same key.
UPDATE drafts SET status = 'publishing', attempts = attempts + 1, attempt_key = COALESCE(attempt_key, sqlc.arg(attempt_key)), updated_at = now()
WHERE id = (
    SELECT d.id FROM drafts d
    WHERE d.status = 'approved' AND (d.scheduled_at IS NULL OR d.scheduled_at <= sqlc.arg(now)::timestamptz)
    ORDER BY COALESCE(d.scheduled_at, d.approved_at), d.id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
RETURNING *;

-- name: FinishDraft :one
UPDATE drafts
SET status = sqlc.arg(status), published_at = sqlc.narg(published_at), published_external_id = sqlc.narg(external_id),
    published_url = sqlc.narg(url), content_item_id = COALESCE(sqlc.narg(content_item_id), content_item_id),
    error = sqlc.narg(error), updated_at = now(),
    -- a failed attempt posted nothing, so the next one must not be deduplicated against it
    attempt_key = CASE WHEN sqlc.arg(status) = 'failed' THEN NULL ELSE attempt_key END
WHERE id = sqlc.arg(id) AND status = 'publishing'
RETURNING *;

-- name: RetryDraft :one
UPDATE drafts SET status = 'approved', updated_at = now() WHERE id = $1 AND status = 'publishing' RETURNING *;

-- name: StrandedDrafts :many
-- Drafts left in publishing by a crash: the post may or may not exist.
UPDATE drafts SET status = 'unknown', error = 'hub restarted while publishing', updated_at = now()
WHERE status = 'publishing'
RETURNING *;

-- name: MarkDraftPublished :one
-- Olin confirmed a sent/unknown draft with its URL, or the next collect matched it.
UPDATE drafts SET status = 'published', published_url = COALESCE(sqlc.narg(url), published_url),
    published_external_id = COALESCE(sqlc.narg(external_id), published_external_id),
    content_item_id = COALESCE(sqlc.narg(content_item_id), content_item_id),
    published_at = COALESCE(published_at, now()), error = NULL, updated_at = now()
WHERE id = sqlc.arg(id) AND status IN ('sent', 'unknown', 'published')
RETURNING *;

-- name: RequeueUnknownDraft :one
UPDATE drafts SET status = 'approved', error = NULL, updated_at = now() WHERE id = $1 AND status = 'unknown' RETURNING *;

-- name: SentDraftsForAccount :many
SELECT * FROM drafts WHERE account_id = $1 AND status = 'sent' AND published_at >= $2 ORDER BY published_at;

-- name: InsertMediaAsset :one
INSERT INTO media_assets (token, path, name, mime, bytes, sha256, width, height, duration_ms, alt, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: GetMediaAsset :one
SELECT * FROM media_assets WHERE id = $1;

-- name: MediaByToken :one
SELECT * FROM media_assets WHERE token = $1 AND expires_at > $2;

-- name: ExtendMedia :exec
UPDATE media_assets SET expires_at = GREATEST(expires_at, $2) WHERE id = $1;

-- name: GetKV :one
SELECT value FROM kv WHERE key = $1;

-- name: SetKV :exec
INSERT INTO kv (key, value) VALUES ($1, $2)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now();

-- name: DeleteKV :exec
DELETE FROM kv WHERE key = $1;

-- name: GetAccount :one
SELECT * FROM accounts WHERE id = $1;

-- name: PutCredential :exec
INSERT INTO credentials (account_id, provider, ciphertext, nonce, scopes, expires_at, refresh_expires_at, updated_at, last_error)
VALUES ($1, $2, $3, $4, $5, $6, $7, now(), NULL)
ON CONFLICT (account_id) DO UPDATE
SET provider = EXCLUDED.provider, ciphertext = EXCLUDED.ciphertext, nonce = EXCLUDED.nonce, scopes = EXCLUDED.scopes,
    expires_at = EXCLUDED.expires_at, refresh_expires_at = EXCLUDED.refresh_expires_at, updated_at = now(), last_error = NULL;

-- name: GetCredential :one
SELECT * FROM credentials WHERE account_id = $1;

-- name: ListCredentials :many
SELECT * FROM credentials ORDER BY account_id;

-- name: CredentialError :exec
UPDATE credentials SET last_error = $2 WHERE account_id = $1;

-- name: AccountByKey :one
SELECT * FROM accounts WHERE platform = $1 AND handle = $2;

-- name: GetInboundItem :one
SELECT * FROM inbound_items WHERE id = $1;

-- name: MarkInboundReplied :exec
UPDATE inbound_items SET replied_at = now(), reply_url = $2, handled = true WHERE id = $1;

-- name: AccountContentStats :many
-- Posts of each account published since $1 and the mean engagement (likes + reposts + comments)
-- and views of their latest snapshots.
SELECT ci.account_id::bigint AS account_id, count(*)::bigint AS posts,
       COALESCE(avg(COALESCE(s.likes, 0) + COALESCE(s.reposts, 0) + COALESCE(s.comments, 0)), 0)::double precision AS engagement,
       COALESCE(avg(s.views), 0)::double precision AS views
FROM content_items ci
LEFT JOIN LATERAL (
    SELECT likes, reposts, comments, views FROM metric_snapshots ms
    WHERE ms.content_item_id = ci.id ORDER BY taken_at DESC LIMIT 1
) s ON true
WHERE ci.account_id IS NOT NULL AND ci.published_at >= $1
GROUP BY ci.account_id;

-- name: PendingLLMCampaigns :many
SELECT * FROM campaigns WHERE llm_status = 'pending' ORDER BY id;

-- name: SetCampaignLLMStatus :exec
UPDATE campaigns SET llm_status = $2 WHERE id = $1;
