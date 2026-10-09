-- AgenticPaymentIntent: the primitive a tenant's agent uses to request one
-- agentic payment — see internal/domain/paymentintent's package doc for why
-- this is separate from purchase_intents rather than a rename of it.
-- provider_transaction_id/provider_status/final_amount/final_currency are
-- inlined here rather than a separate ledger table, the same way orders
-- already inlines its own result fields.
CREATE TABLE IF NOT EXISTS agentic_payment_intents (
    id                       TEXT PRIMARY KEY,
    tenant_id                TEXT NOT NULL REFERENCES tenants(id),
    user_id                  TEXT NOT NULL REFERENCES users(id),
    agent_id                 TEXT NOT NULL REFERENCES agents(id),
    purpose                  TEXT,
    merchant                 TEXT NOT NULL,
    merchant_domain          TEXT,
    category                 TEXT,
    international            BOOLEAN NOT NULL DEFAULT false,
    amount_minor_units       BIGINT NOT NULL,
    currency                 TEXT NOT NULL,
    tolerance_minor_units    BIGINT NOT NULL DEFAULT 0,
    product_ref              TEXT,
    payment_source_alias     TEXT NOT NULL,
    requested_capability     TEXT,
    status                   TEXT NOT NULL,
    policy_version            TEXT,
    provider_transaction_id  TEXT,
    provider_status          TEXT,
    final_amount_minor_units BIGINT,
    final_currency           TEXT,
    metadata                 JSONB,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at                TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_agentic_payment_intents_tenant_id ON agentic_payment_intents(tenant_id);
CREATE INDEX IF NOT EXISTS idx_agentic_payment_intents_user_id ON agentic_payment_intents(user_id);
CREATE INDEX IF NOT EXISTS idx_agentic_payment_intents_status ON agentic_payment_intents(status);

-- Approvals are reused for AgenticPaymentIntent rather than reinvented (see
-- internal/domain/approval.Approval's doc comment) — exactly one of
-- intent_id / agentic_payment_intent_id is set per row. A payment-intent-
-- sourced approval has no quote (no discovery step), so quote_id becomes
-- nullable too; ItemsHash for that case is computed over an empty item
-- list, still binding merchant+currency+payment-alias.
ALTER TABLE approvals ALTER COLUMN intent_id DROP NOT NULL;
ALTER TABLE approvals ALTER COLUMN quote_id DROP NOT NULL;
ALTER TABLE approvals ADD COLUMN IF NOT EXISTS agentic_payment_intent_id TEXT REFERENCES agentic_payment_intents(id);
ALTER TABLE approvals ADD CONSTRAINT approvals_exactly_one_parent CHECK (
    (intent_id IS NOT NULL)::int + (agentic_payment_intent_id IS NOT NULL)::int = 1
);
CREATE INDEX IF NOT EXISTS idx_approvals_agentic_payment_intent_id ON approvals(agentic_payment_intent_id);

-- Audit events gain the same two optional columns the Go Event struct does
-- (internal/domain/audit.Event) — additive, append-only trigger unaffected
-- (it fires on row UPDATE/DELETE, not on this schema DDL).
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS agentic_payment_intent_id TEXT;
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS tenant_id TEXT;
CREATE INDEX IF NOT EXISTS idx_audit_events_agentic_payment_intent_id ON audit_events(agentic_payment_intent_id);
CREATE INDEX IF NOT EXISTS idx_audit_events_tenant_id ON audit_events(tenant_id);
