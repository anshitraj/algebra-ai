-- Email notifications: a sign-in alert after each sign-in, and the receipt
-- of each payment that lands on a chain.
--
-- They go to the address the person signs in with. Someone who signed in
-- with a wallet has none (their account's email is an undeliverable
-- placeholder), so they can add one here. An added address gets nothing but
-- its confirmation link until the person opens that link: otherwise anyone
-- could point another person's inbox at their own receipts.
--
-- No row means the defaults: both kinds on, no added address.

CREATE TABLE IF NOT EXISTS user_notifications (
    user_id            TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    -- The confirmed address, and when it was confirmed.
    email              TEXT,
    email_verified_at  TIMESTAMPTZ,
    -- An address waiting for its confirmation link. pending_token_hash is
    -- SHA-256 of the token in the link; the raw token is never stored.
    pending_email      TEXT,
    pending_token_hash TEXT,
    pending_expires_at TIMESTAMPTZ,
    pending_sent_at    TIMESTAMPTZ,
    sign_ins           BOOLEAN NOT NULL DEFAULT TRUE,
    receipts           BOOLEAN NOT NULL DEFAULT TRUE,
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
