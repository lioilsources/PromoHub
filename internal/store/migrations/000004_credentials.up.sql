-- Wave 3 of Prompts/01-ACCOUNTS_PLAN.md: OAuth tokens, AES-256-GCM encrypted (internal/vault).
CREATE TABLE credentials (
    account_id         BIGINT PRIMARY KEY REFERENCES accounts (id) ON DELETE CASCADE,
    provider           TEXT NOT NULL,
    ciphertext         BYTEA NOT NULL,
    nonce              BYTEA NOT NULL,
    scopes             TEXT,
    expires_at         TIMESTAMPTZ,
    refresh_expires_at TIMESTAMPTZ,
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error         TEXT
);
