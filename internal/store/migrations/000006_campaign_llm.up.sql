-- Night LLM window: campaigns wait (pending) until the window composes them.
ALTER TABLE campaigns ADD COLUMN llm_status TEXT NOT NULL DEFAULT 'none'
    CHECK (llm_status IN ('none', 'pending', 'done'));
CREATE INDEX campaigns_llm_pending_idx ON campaigns (id) WHERE llm_status = 'pending';
