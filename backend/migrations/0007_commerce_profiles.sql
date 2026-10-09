-- CommerceProfile: a portable, low-sensitivity preference profile (sizes,
-- colors, style, dietary flags, ...) the shopping agent reads before
-- asking clarifying questions, plus pointers to the user's default
-- shipping/payment aliases. Deliberately NOT encrypted, unlike
-- private_profiles — this data is safe to hand directly to an agent/LLM,
-- a different sensitivity tier from the alias-resolved address/payment
-- system in internal/domain/privacy. One row per user.
CREATE TABLE IF NOT EXISTS commerce_profiles (
    user_id                 TEXT PRIMARY KEY REFERENCES users(id),
    default_shipping_alias  TEXT,
    default_payment_alias   TEXT,
    preferences             JSONB NOT NULL DEFAULT '{}',
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);
