-- Book a Demo: business enquiries from the public site (internal/app/lead_service.go).
--
-- Written by an unauthenticated form, so every field is bounded here as well
-- as in the service, and nothing in a row is ever rendered as markup or run.
-- Operators read them with the operator token; nothing else reads this table.

CREATE TABLE IF NOT EXISTS demo_requests (
    id              TEXT PRIMARY KEY,
    business_name   TEXT NOT NULL CHECK (length(business_name) BETWEEN 1 AND 120),
    work_email      TEXT NOT NULL CHECK (length(work_email) BETWEEN 3 AND 254),
    website         TEXT NOT NULL DEFAULT '' CHECK (length(website) <= 200),
    use_case        TEXT NOT NULL CHECK (length(use_case) BETWEEN 1 AND 2000),
    estimated_usage TEXT NOT NULL DEFAULT '' CHECK (length(estimated_usage) <= 60),
    message         TEXT NOT NULL DEFAULT '' CHECK (length(message) <= 4000),
    -- The signed-in account that sent it, when there was one.
    user_id         TEXT REFERENCES users(id) ON DELETE SET NULL,
    source_ip       TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'new' CHECK (status IN ('new', 'contacted', 'closed')),
    created_at      TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_demo_requests_created ON demo_requests(created_at DESC);
