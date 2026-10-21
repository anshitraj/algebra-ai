-- Sign-in with an emailed code (internal/app/account_login_code.go).
--
-- One live code per address. The table holds no address and no code: the key
-- and the hash are both keyed hashes (account.LoginCodeAddressKey,
-- account.LoginCodeHash), so a copy of it can neither be read for who signs
-- in nor searched for the million possible codes without the server's secret.
-- A code is used up when it is accepted, and is gone after its tries or its
-- ten minutes, whichever comes first.
CREATE TABLE IF NOT EXISTS login_codes (
    address_key TEXT        PRIMARY KEY,
    code_hash   TEXT        NOT NULL,
    tries       INTEGER     NOT NULL DEFAULT 0 CHECK (tries >= 0),
    created_at  TIMESTAMPTZ NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_login_codes_expires ON login_codes (expires_at);
