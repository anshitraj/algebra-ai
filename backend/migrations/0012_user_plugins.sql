-- Which plugins (internal/domain/plugin) each person has switched on or off,
-- and their settings (e.g. the subreddits the Reddit plugin reads). A plugin
-- with no row uses its default. Human-set only: no agent can change these.
CREATE TABLE IF NOT EXISTS user_plugins (
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    plugin_id  TEXT NOT NULL,
    enabled    BOOLEAN NOT NULL,
    config     JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, plugin_id)
);
