package app

import (
	"context"
	"fmt"
	"time"

	"github.com/project-algebra/algebra/internal/domain/plugin"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// PluginStore persists each person's plugin choices and settings.
type PluginStore interface {
	// Choices returns what a user has set; plugins with no row are absent.
	Choices(ctx context.Context, userID string) (map[string]PluginChoice, error)
	SetChoice(ctx context.Context, userID, pluginID string, c PluginChoice, at time.Time) error
}

// PluginChoice is one person's setting for one plugin.
type PluginChoice struct {
	Enabled bool          `json:"enabled"`
	Config  plugin.Config `json:"config"`
}

// PluginState is a plugin as one person sees it: on or off for them, and
// whether this server can run it at all.
type PluginState struct {
	plugin.Plugin
	Enabled bool          `json:"enabled"`
	Config  plugin.Config `json:"config"`
	// Ready is false when the server lacks what the plugin needs (API
	// keys); Detail says what.
	Ready  bool   `json:"ready"`
	Detail string `json:"detail,omitempty"`
}

// PluginService lets a person switch the agent's sources on and off. Only
// the person does this — through their session, never an agent tool — and
// the server enforces the result wherever a plugin's data is read.
type PluginService struct {
	store  PluginStore
	agents AgentStore
	// unready maps plugin ID → why this server can't run it.
	unready map[string]string
	now     func() time.Time
}

func NewPluginService(store PluginStore, agents AgentStore, unready map[string]string) *PluginService {
	if unready == nil {
		unready = map[string]string{}
	}
	return &PluginService{store: store, agents: agents, unready: unready, now: time.Now}
}

// List is the whole catalog with this person's state.
func (s *PluginService) List(ctx context.Context, userID string) ([]PluginState, error) {
	choices, err := s.store.Choices(ctx, userID)
	if err != nil {
		return nil, err
	}
	on := plugin.Resolve(enabledMap(choices))
	out := make([]PluginState, 0, len(plugin.Catalog))
	for _, p := range plugin.Catalog {
		detail, unready := s.unready[p.ID]
		out = append(out, PluginState{Plugin: p, Enabled: on[p.ID], Config: choices[p.ID].Config, Ready: !unready, Detail: detail})
	}
	return out, nil
}

// Set switches one plugin on or off and, for the Reddit plugin, sets its
// subreddits (cfg nil leaves settings as they are). Core plugins can't be
// switched off.
func (s *PluginService) Set(ctx context.Context, userID, pluginID string, enabled bool, cfg *plugin.Config) (*PluginState, error) {
	p, ok := plugin.Get(pluginID)
	if !ok {
		return nil, fmt.Errorf("%w: no plugin %q", shared.ErrNotFound, pluginID)
	}
	if p.Core && !enabled {
		return nil, fmt.Errorf("%w: %s can't be turned off — the agent can't shop without it", shared.ErrConflict, p.Name)
	}
	choices, err := s.store.Choices(ctx, userID)
	if err != nil {
		return nil, err
	}
	c := choices[pluginID]
	c.Enabled = enabled
	if cfg != nil {
		if pluginID != plugin.RedditDeals && len(cfg.Subreddits) > 0 {
			return nil, fmt.Errorf("%w: only the Reddit plugin takes subreddits", shared.ErrConflict)
		}
		subs, err := plugin.NormalizeSubreddits(cfg.Subreddits)
		if err != nil {
			return nil, fmt.Errorf("%w: %s", shared.ErrConflict, err.Error())
		}
		c.Config.Subreddits = subs
	}
	if err := s.store.SetChoice(ctx, userID, pluginID, c, s.now()); err != nil {
		return nil, err
	}
	list, err := s.List(ctx, userID)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == pluginID {
			return &list[i], nil
		}
	}
	return nil, fmt.Errorf("%w: no plugin %q", shared.ErrNotFound, pluginID)
}

// ForAgent is what an agent may read from: the plugins its person has on
// and this server can run, with their settings.
func (s *PluginService) ForAgent(ctx context.Context, agentID string) (map[string]PluginState, error) {
	ag, err := s.agents.Get(ctx, agentID)
	if err != nil {
		return nil, err
	}
	list, err := s.List(ctx, ag.UserID)
	if err != nil {
		return nil, err
	}
	out := map[string]PluginState{}
	for _, st := range list {
		if st.Enabled && st.Ready {
			out[st.ID] = st
		}
	}
	return out, nil
}

func enabledMap(choices map[string]PluginChoice) map[string]bool {
	out := make(map[string]bool, len(choices))
	for id, c := range choices {
		out[id] = c.Enabled
	}
	return out
}
