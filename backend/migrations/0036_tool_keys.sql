-- A person's own API keys for web tools (TinyFish, Exa, Firecrawl, Tavily),
-- sealed with a key derived from the master key and used for nothing else
-- (internal/app/tool_keys.go). Only the last four characters are kept in the
-- clear, to tell the person which key is saved. The sealed key is bound to
-- the owner and the vendor, and is read only to make that person's own calls.
CREATE TABLE IF NOT EXISTS tool_keys (
    user_id    TEXT NOT NULL REFERENCES users(id),
    vendor     TEXT NOT NULL CHECK (vendor ~ '^[a-z][a-z0-9_-]{1,31}$'),
    sealed     BYTEA NOT NULL,
    nonce      BYTEA NOT NULL,
    hint       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, vendor)
);
