-- Wave 2 of Prompts/01-ACCOUNTS_PLAN.md: campaigns, per-account drafts and media.

-- One message that should go out, e.g. "new SwypeKids version".
CREATE TABLE campaigns (
    id                     BIGSERIAL PRIMARY KEY,
    app_id                 BIGINT REFERENCES apps (id),
    source_content_item_id BIGINT REFERENCES content_items (id),
    lang                   TEXT NOT NULL DEFAULT 'en',
    title                  TEXT,
    body                   TEXT NOT NULL,
    link                   TEXT,
    media                  JSONB NOT NULL DEFAULT '[]', -- media_assets ids
    status                 TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed')),
    tg_chat_id             BIGINT,
    tg_message_id          BIGINT,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The text for one account. State machine: Prompts/01-ACCOUNTS_PLAN.md §8.3.
CREATE TABLE drafts (
    id                    BIGSERIAL PRIMARY KEY,
    campaign_id           BIGINT NOT NULL REFERENCES campaigns (id) ON DELETE CASCADE,
    account_id            BIGINT NOT NULL REFERENCES accounts (id),
    via                   TEXT NOT NULL CHECK (via IN ('api', 'ifttt', 'manual')),
    text                  TEXT NOT NULL,
    extra                 JSONB NOT NULL DEFAULT '{}', -- title, subreddit, board, …
    status                TEXT NOT NULL DEFAULT 'draft'
                          CHECK (status IN ('draft', 'approved', 'publishing', 'published', 'sent', 'failed', 'skipped', 'unknown')),
    scheduled_at          TIMESTAMPTZ,
    approved_at           TIMESTAMPTZ,
    attempt_key           TEXT,
    attempts              INTEGER NOT NULL DEFAULT 0,
    published_at          TIMESTAMPTZ,
    published_external_id TEXT,
    published_url         TEXT,
    content_item_id       BIGINT REFERENCES content_items (id),
    error                 TEXT,
    tg_chat_id            BIGINT,
    tg_message_id         BIGINT,
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (campaign_id, account_id)
);
CREATE INDEX drafts_due_idx ON drafts (scheduled_at) WHERE status = 'approved';
CREATE INDEX drafts_sent_idx ON drafts (account_id, published_at) WHERE status = 'sent';

-- Files served at /media/{token} for platforms that pull media from a URL.
CREATE TABLE media_assets (
    id          BIGSERIAL PRIMARY KEY,
    token       TEXT NOT NULL UNIQUE,
    path        TEXT NOT NULL,
    name        TEXT NOT NULL,
    mime        TEXT NOT NULL,
    bytes       BIGINT NOT NULL,
    sha256      TEXT NOT NULL,
    width       INTEGER,
    height      INTEGER,
    duration_ms INTEGER,
    alt         TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL
);

-- Small process state: Telegram getUpdates offset, pending ForceReply prompts.
CREATE TABLE kv (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
