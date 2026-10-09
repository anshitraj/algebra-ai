package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/plugin"
)

// PluginRepo backs app.PluginStore — see migrations/0012_user_plugins.sql.
type PluginRepo struct{ db *DB }

func NewPluginRepo(db *DB) *PluginRepo { return &PluginRepo{db: db} }

var _ app.PluginStore = (*PluginRepo)(nil)

func (r *PluginRepo) Choices(ctx context.Context, userID string) (map[string]app.PluginChoice, error) {
	rows, err := r.db.Pool.Query(ctx, `SELECT plugin_id, enabled, config FROM user_plugins WHERE user_id = $1`, userID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing plugin choices: %w", err)
	}
	defer rows.Close()
	out := map[string]app.PluginChoice{}
	for rows.Next() {
		var id string
		var c app.PluginChoice
		var raw []byte
		if err := rows.Scan(&id, &c.Enabled, &raw); err != nil {
			return nil, fmt.Errorf("postgres: scanning plugin choice: %w", err)
		}
		if len(raw) > 0 {
			var cfg plugin.Config
			if err := json.Unmarshal(raw, &cfg); err == nil {
				c.Config = cfg
			}
		}
		out[id] = c
	}
	return out, rows.Err()
}

func (r *PluginRepo) SetChoice(ctx context.Context, userID, pluginID string, c app.PluginChoice, at time.Time) error {
	cfg, err := json.Marshal(c.Config)
	if err != nil {
		return fmt.Errorf("postgres: encoding plugin config: %w", err)
	}
	_, err = r.db.Pool.Exec(ctx, `
		INSERT INTO user_plugins (user_id, plugin_id, enabled, config, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id, plugin_id) DO UPDATE SET enabled = EXCLUDED.enabled, config = EXCLUDED.config, updated_at = EXCLUDED.updated_at`,
		userID, pluginID, c.Enabled, cfg, at)
	if err != nil {
		return fmt.Errorf("postgres: saving plugin choice: %w", err)
	}
	return nil
}
