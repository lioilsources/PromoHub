-- Wave 5 of Prompts/01-ACCOUNTS_PLAN.md: replies from Telegram need to know which of my
-- accounts an inbound item belongs to.
ALTER TABLE inbound_items ADD COLUMN account_id BIGINT REFERENCES accounts (id);
ALTER TABLE inbound_items ADD COLUMN replied_at TIMESTAMPTZ;
ALTER TABLE inbound_items ADD COLUMN reply_url TEXT;
