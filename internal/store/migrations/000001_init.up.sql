-- Phase 1 schema (PROMO_HUB_PLAN.md §7). Dedup lives here, never in IFTTT.

CREATE TABLE accounts (
    id          BIGSERIAL PRIMARY KEY,
    platform    TEXT NOT NULL,
    handle      TEXT NOT NULL,
    external_id TEXT,
    auth_ref    TEXT,
    UNIQUE (platform, handle)
);

CREATE TABLE apps (
    id           BIGSERIAL PRIMARY KEY,
    slug         TEXT NOT NULL UNIQUE,
    name         TEXT NOT NULL,
    appstore_id  TEXT,
    play_package TEXT,
    links        JSONB NOT NULL DEFAULT '{}'
);

-- My own posts, videos and releases.
CREATE TABLE content_items (
    id              BIGSERIAL PRIMARY KEY,
    app_id          BIGINT REFERENCES apps (id),
    account_id      BIGINT REFERENCES accounts (id),
    source_platform TEXT NOT NULL,
    external_id     TEXT NOT NULL,
    url             TEXT,
    title           TEXT,
    body            TEXT,
    media           JSONB NOT NULL DEFAULT '[]',
    published_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (source_platform, external_id)
);

CREATE TABLE metric_snapshots (
    id              BIGSERIAL PRIMARY KEY,
    content_item_id BIGINT REFERENCES content_items (id),
    account_id      BIGINT REFERENCES accounts (id),
    platform        TEXT NOT NULL,
    taken_at        TIMESTAMPTZ NOT NULL,
    likes           BIGINT,
    views           BIGINT,
    reposts         BIGINT,
    comments        BIGINT,
    followers       BIGINT,
    extra           JSONB NOT NULL DEFAULT '{}',
    -- Apify run that produced the snapshot; a redelivered webhook hits the unique index below.
    source_run_id   TEXT,
    CHECK ((content_item_id IS NULL) <> (account_id IS NULL))
);
CREATE UNIQUE INDEX metric_snapshots_run_uniq
    ON metric_snapshots (COALESCE(content_item_id, 0), COALESCE(account_id, 0), source_run_id)
    WHERE source_run_id IS NOT NULL;
CREATE INDEX metric_snapshots_content_idx ON metric_snapshots (content_item_id, taken_at DESC) WHERE content_item_id IS NOT NULL;
CREATE INDEX metric_snapshots_account_idx ON metric_snapshots (account_id, taken_at DESC) WHERE account_id IS NOT NULL;

-- Things other people wrote: comments, reviews, mentions, replies.
CREATE TABLE inbound_items (
    id              BIGSERIAL PRIMARY KEY,
    platform        TEXT NOT NULL,
    kind            TEXT NOT NULL CHECK (kind IN ('comment', 'review', 'mention', 'reply')),
    external_id     TEXT NOT NULL,
    content_item_id BIGINT REFERENCES content_items (id),
    app_id          BIGINT REFERENCES apps (id),
    author          TEXT,
    text            TEXT,
    rating          INTEGER,
    url             TEXT,
    created_at      TIMESTAMPTZ,
    collected_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    sentiment       TEXT,
    handled         BOOLEAN NOT NULL DEFAULT false,
    UNIQUE (platform, external_id)
);

-- Outbox: one row per (rule, target). Rows without a target are log-only.
CREATE TABLE events (
    id              BIGSERIAL PRIMARY KEY,
    type            TEXT NOT NULL,
    rule            TEXT,
    target          TEXT,
    payload         JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at         TIMESTAMPTZ,
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error      TEXT
);
CREATE INDEX events_pending_idx ON events (next_attempt_at) WHERE sent_at IS NULL AND target IS NOT NULL;

-- Every Apify run the hub has seen; doubles as the idempotence key for webhooks and as the spend log.
CREATE TABLE apify_runs (
    run_id       TEXT PRIMARY KEY,
    task_id      TEXT NOT NULL,
    task_name    TEXT NOT NULL,
    actor_id     TEXT,
    dataset_id   TEXT,
    status       TEXT,
    state        TEXT NOT NULL DEFAULT 'received' CHECK (state IN ('received', 'processed', 'failed')),
    attempts     INTEGER NOT NULL DEFAULT 0,
    last_error   TEXT,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at   TIMESTAMPTZ,
    finished_at  TIMESTAMPTZ,
    items        INTEGER,
    new_items    INTEGER,
    cost_usd     DOUBLE PRECISION,
    received_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at TIMESTAMPTZ
);
