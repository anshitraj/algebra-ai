-- Billing for Algebra's own plans (internal/domain/billing), sold through a
-- payment gateway (Razorpay). Nothing here relates to the money for what
-- users buy — Algebra stays non-custodial for purchases.

-- One subscription per user; upgrading again after cancelling replaces it.
CREATE TABLE IF NOT EXISTS subscriptions (
    user_id                  TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    plan                     TEXT NOT NULL,
    provider                 TEXT NOT NULL,
    provider_subscription_id TEXT NOT NULL UNIQUE,
    status                   TEXT NOT NULL,
    current_period_start     TIMESTAMPTZ,
    current_period_end       TIMESTAMPTZ,
    cancel_at_period_end     BOOLEAN NOT NULL DEFAULT false,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Gateway plans Algebra created, keyed by the price they were created at so
-- changing the price creates a new plan instead of silently reusing the old.
CREATE TABLE IF NOT EXISTS billing_plans (
    plan              TEXT NOT NULL,
    amount_minor      BIGINT NOT NULL,
    currency          TEXT NOT NULL,
    provider_plan_id  TEXT NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (plan, amount_minor, currency)
);

-- Webhook replay guard: each gateway event ID is applied once.
CREATE TABLE IF NOT EXISTS billing_events (
    event_id    TEXT PRIMARY KEY,
    event       TEXT NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
