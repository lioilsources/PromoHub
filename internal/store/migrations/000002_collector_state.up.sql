-- Wave 1 of Prompts/01-ACCOUNTS_PLAN.md: the official-API read adapters' schedule and cursors.
-- One row per (account, capability); the row is both the schedule and the resume point.
CREATE TABLE collector_state (
    account_id  BIGINT NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    capability  TEXT NOT NULL CHECK (capability IN ('profile', 'inbound', 'search')),
    cursor      JSONB,
    next_run_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_run_at TIMESTAMPTZ,
    last_ok_at  TIMESTAMPTZ,
    fail_count  INTEGER NOT NULL DEFAULT 0,
    last_error  TEXT,
    PRIMARY KEY (account_id, capability)
);
CREATE INDEX collector_state_due_idx ON collector_state (next_run_at);
