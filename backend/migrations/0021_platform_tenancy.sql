-- Organizations, projects and Algebra API keys: the B2B tenancy layer.
--
-- An API key authenticates an application inside one project and one
-- environment (sandbox, devnet or mainnet). It never authorises spending on its
-- own: a Spend Pass of the same project and environment does that. The key's
-- secret is never stored, only its SHA-256 hash (on the agent row the key
-- signs in as), exactly like a Spend Pass token.

CREATE TABLE IF NOT EXISTS organizations (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    owner_user_id  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- plan is the commercial plan; trial_* a bounded trial of a higher plan.
    plan           TEXT NOT NULL DEFAULT 'free' CHECK (plan IN ('free', 'builder', 'startup', 'business', 'enterprise')),
    trial_plan     TEXT CHECK (trial_plan IN ('startup')),
    trial_ends_at  TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_organizations_owner ON organizations(owner_user_id);

CREATE TABLE IF NOT EXISTS organization_members (
    org_id     TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       TEXT NOT NULL CHECK (role IN ('owner', 'admin', 'developer', 'viewer')),
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (org_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_org_members_user ON organization_members(user_id);

CREATE TABLE IF NOT EXISTS projects (
    id               TEXT PRIMARY KEY,
    org_id           TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name             TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    -- default_strategy applies when a request names none ('' = ask the caller).
    default_strategy TEXT NOT NULL DEFAULT '' CHECK (default_strategy IN ('', 'cheapest', 'fastest', 'balanced')),
    created_at       TIMESTAMPTZ NOT NULL,
    archived_at      TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_projects_org ON projects(org_id);

CREATE TABLE IF NOT EXISTS api_keys (
    id           TEXT PRIMARY KEY,
    project_id   TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    environment  TEXT NOT NULL CHECK (environment IN ('sandbox', 'devnet', 'mainnet')),
    name         TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    -- prefix is the first characters of the secret, safe to show.
    prefix       TEXT NOT NULL,
    agent_id     TEXT NOT NULL UNIQUE REFERENCES agents(id),
    scopes       TEXT[] NOT NULL,
    created_by   TEXT NOT NULL REFERENCES users(id),
    created_at   TIMESTAMPTZ NOT NULL,
    last_used_at TIMESTAMPTZ,
    expires_at   TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ,
    rotated_from TEXT REFERENCES api_keys(id)
);
CREATE INDEX IF NOT EXISTS idx_api_keys_project ON api_keys(project_id, created_at DESC);

-- A Spend Pass can belong to a project and an environment. Passes made before
-- this have neither and keep working as they did (the console, pass tokens).
ALTER TABLE spend_passes ADD COLUMN IF NOT EXISTS project_id TEXT REFERENCES projects(id);
ALTER TABLE spend_passes ADD COLUMN IF NOT EXISTS environment TEXT CHECK (environment IN ('sandbox', 'devnet', 'mainnet'));
CREATE INDEX IF NOT EXISTS idx_spend_passes_project ON spend_passes(project_id, environment, created_at DESC) WHERE project_id IS NOT NULL;

-- An intent records the project it was made for (from its pass).
ALTER TABLE economic_intents ADD COLUMN IF NOT EXISTS project_id TEXT REFERENCES projects(id);
CREATE INDEX IF NOT EXISTS idx_economic_intents_project ON economic_intents(project_id, created_at DESC) WHERE project_id IS NOT NULL;

-- Append-only record of platform actions: keys created, rotated and revoked,
-- projects created, provider verification decisions. Never secrets.
CREATE TABLE IF NOT EXISTS platform_events (
    id         TEXT PRIMARY KEY,
    org_id     TEXT,
    project_id TEXT,
    actor      TEXT NOT NULL,
    event      TEXT NOT NULL,
    data       JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_platform_events_project ON platform_events(project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_platform_events_org ON platform_events(org_id, created_at DESC);
