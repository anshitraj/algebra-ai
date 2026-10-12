package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
	"github.com/project-algebra/algebra/internal/domain/tenancy"
)

// TenancyStore persists organizations, members, projects, API keys and the
// platform audit trail (migrations/0021_platform_tenancy.sql).
type TenancyStore interface {
	// CreateOrganization stores the organization and its owner's membership
	// in one transaction.
	CreateOrganization(ctx context.Context, o *tenancy.Organization, owner tenancy.Member) error
	OrganizationsForUser(ctx context.Context, userID string) ([]OrgMembership, error)
	Organization(ctx context.Context, id string) (*tenancy.Organization, error)
	Member(ctx context.Context, orgID, userID string) (*tenancy.Member, error)

	CreateProject(ctx context.Context, p *tenancy.Project) error
	Projects(ctx context.Context, orgID string) ([]tenancy.Project, error)
	Project(ctx context.Context, id string) (*tenancy.Project, error)
	UpdateProject(ctx context.Context, p *tenancy.Project) error

	CreateAPIKey(ctx context.Context, k *tenancy.APIKey) error
	APIKeys(ctx context.Context, projectID string) ([]tenancy.APIKey, error)
	APIKey(ctx context.Context, id string) (*tenancy.APIKey, error)
	APIKeyByAgent(ctx context.Context, agentID string) (*tenancy.APIKey, error)
	RevokeAPIKey(ctx context.Context, id string, at time.Time) error
	TouchAPIKey(ctx context.Context, id string, at time.Time) error

	AddPlatformEvent(ctx context.Context, e PlatformEvent) error
	PlatformEvents(ctx context.Context, projectID string, limit int) ([]PlatformEvent, error)
}

// ProjectPassLister lists the Spend Passes of one project and environment.
type ProjectPassLister interface {
	ListByProject(ctx context.Context, projectID, env string) ([]spendpass.Pass, error)
}

// OrgMembership is an organization as one member sees it.
type OrgMembership struct {
	tenancy.Organization
	Role tenancy.Role `json:"role"`
}

// PlatformEvent is one entry of the platform audit trail. Data never holds a
// secret: keys are named by ID and prefix only.
type PlatformEvent struct {
	ID        string         `json:"id"`
	OrgID     string         `json:"org_id,omitempty"`
	ProjectID string         `json:"project_id,omitempty"`
	Actor     string         `json:"actor"`
	Event     string         `json:"event"`
	Data      map[string]any `json:"data,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// IssuedKey is a new API key with its secret, shown exactly once.
type IssuedKey struct {
	tenancy.APIKey
	Secret string `json:"secret"`
}

// KeyInput is what a person asks for when issuing a key.
type KeyInput struct {
	Name        string              `json:"name"`
	Environment tenancy.Environment `json:"environment"`
	Scopes      []tenancy.Scope     `json:"scopes"`
	// ExpiresInDays: 0 means the key does not expire.
	ExpiresInDays int `json:"expires_in_days"`
}

// APIKeyClientID marks the agent identity an API key signs in as.
const APIKeyClientID = "api-key"

// TenancyService runs the tenancy model: workspaces, projects and keys. Every
// method that acts for a person checks their membership first; someone
// else's organization, project or key is "not found", never "forbidden".
type TenancyService struct {
	store  TenancyStore
	agents AgentStore
	passes ProjectPassLister
	now    func() time.Time
}

func NewTenancyService(store TenancyStore, agents AgentStore, passes ProjectPassLister) *TenancyService {
	return &TenancyService{store: store, agents: agents, passes: passes, now: time.Now}
}

func (s *TenancyService) event(ctx context.Context, orgID, projectID, actor, name string, data map[string]any) {
	_ = s.store.AddPlatformEvent(ctx, PlatformEvent{
		ID: newID("pev"), OrgID: orgID, ProjectID: projectID, Actor: actor, Event: name, Data: data, CreatedAt: s.now().UTC(),
	})
}

// Workspace returns the person's organizations. Someone with none gets one,
// with a first project, so a new account can issue a key at once.
func (s *TenancyService) Workspace(ctx context.Context, userID, displayName string) ([]OrgMembership, error) {
	orgs, err := s.store.OrganizationsForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(orgs) > 0 {
		return orgs, nil
	}
	name := strings.TrimSpace(displayName)
	if name == "" {
		name = "My"
	}
	if _, err := s.CreateOrganization(ctx, userID, trimTo(name+"'s workspace", 80)); err != nil {
		return nil, err
	}
	return s.store.OrganizationsForUser(ctx, userID)
}

func trimTo(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// CreateOrganization makes an organization owned by userID, with a first
// project named "Default project".
func (s *TenancyService) CreateOrganization(ctx context.Context, userID, name string) (*OrgMembership, error) {
	name, err := tenancy.CleanName("organization name", name)
	if err != nil {
		return nil, invalid(err)
	}
	now := s.now().UTC()
	o := &tenancy.Organization{ID: newID("org"), Name: name, OwnerUserID: userID, Plan: "free", CreatedAt: now}
	if err := s.store.CreateOrganization(ctx, o, tenancy.Member{OrgID: o.ID, UserID: userID, Role: tenancy.RoleOwner, CreatedAt: now}); err != nil {
		return nil, err
	}
	s.event(ctx, o.ID, "", userID, "organization.created", map[string]any{"name": name})
	if _, err := s.createProject(ctx, userID, o.ID, "Default project"); err != nil {
		return nil, err
	}
	return &OrgMembership{Organization: *o, Role: tenancy.RoleOwner}, nil
}

func invalid(err error) error {
	return fmt.Errorf("%w: %s", ErrInvalidRequest, strings.TrimPrefix(err.Error(), "tenancy: invalid: "))
}

// member is the person's membership of orgID, or "not found".
func (s *TenancyService) member(ctx context.Context, orgID, userID string) (*tenancy.Member, error) {
	m, err := s.store.Member(ctx, orgID, userID)
	if errors.Is(err, shared.ErrNotFound) {
		return nil, fmt.Errorf("%w: organization %s", shared.ErrNotFound, orgID)
	}
	return m, err
}

// Projects lists an organization's projects for one of its members.
func (s *TenancyService) Projects(ctx context.Context, userID, orgID string) ([]tenancy.Project, error) {
	m, err := s.member(ctx, orgID, userID)
	if err != nil {
		return nil, err
	}
	if !m.Role.CanRead() {
		return nil, fmt.Errorf("%w: organization %s", shared.ErrNotFound, orgID)
	}
	return s.store.Projects(ctx, orgID)
}

// CreateProject adds a project; owners and admins only.
func (s *TenancyService) CreateProject(ctx context.Context, userID, orgID, name string) (*tenancy.Project, error) {
	m, err := s.member(ctx, orgID, userID)
	if err != nil {
		return nil, err
	}
	if !m.Role.CanManage() {
		return nil, fmt.Errorf("%w: only an organization's owners and admins create projects", shared.ErrUnauthorized)
	}
	return s.createProject(ctx, userID, orgID, name)
}

func (s *TenancyService) createProject(ctx context.Context, userID, orgID, name string) (*tenancy.Project, error) {
	name, err := tenancy.CleanName("project name", name)
	if err != nil {
		return nil, invalid(err)
	}
	p := &tenancy.Project{ID: newID("proj"), OrgID: orgID, Name: name, CreatedAt: s.now().UTC()}
	if err := s.store.CreateProject(ctx, p); err != nil {
		return nil, err
	}
	s.event(ctx, orgID, p.ID, userID, "project.created", map[string]any{"name": name})
	return p, nil
}

// ProjectFor returns a project and the person's role in its organization.
func (s *TenancyService) ProjectFor(ctx context.Context, userID, projectID string) (*tenancy.Project, tenancy.Role, error) {
	p, err := s.store.Project(ctx, projectID)
	if err != nil {
		if errors.Is(err, shared.ErrNotFound) {
			return nil, "", fmt.Errorf("%w: project %s", shared.ErrNotFound, projectID)
		}
		return nil, "", err
	}
	m, err := s.member(ctx, p.OrgID, userID)
	if err != nil {
		return nil, "", fmt.Errorf("%w: project %s", shared.ErrNotFound, projectID)
	}
	return p, m.Role, nil
}

// ManagedProject is ProjectFor for actions only owners and admins may take.
func (s *TenancyService) ManagedProject(ctx context.Context, userID, projectID string) (*tenancy.Project, error) {
	p, role, err := s.ProjectFor(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	if !role.CanManage() {
		return nil, fmt.Errorf("%w: only an organization's owners and admins can do this", shared.ErrUnauthorized)
	}
	return p, nil
}

// UpdateProject renames a project or sets its default routing strategy.
func (s *TenancyService) UpdateProject(ctx context.Context, userID, projectID string, name, strategy *string) (*tenancy.Project, error) {
	p, err := s.ManagedProject(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	if name != nil {
		n, err := tenancy.CleanName("project name", *name)
		if err != nil {
			return nil, invalid(err)
		}
		p.Name = n
	}
	if strategy != nil {
		st := strings.ToLower(strings.TrimSpace(*strategy))
		ok := false
		for _, v := range tenancy.Strategies {
			ok = ok || v == st
		}
		if !ok {
			return nil, fmt.Errorf("%w: default_strategy must be cheapest, fastest, balanced or empty", ErrInvalidRequest)
		}
		p.DefaultStrategy = st
	}
	if err := s.store.UpdateProject(ctx, p); err != nil {
		return nil, err
	}
	s.event(ctx, p.OrgID, p.ID, userID, "project.updated", map[string]any{"name": p.Name, "default_strategy": p.DefaultStrategy})
	return p, nil
}

// CreateKey issues an API key for a project and environment. The secret is
// returned once and never stored: the key signs in as its own agent identity,
// which holds only the secret's hash.
func (s *TenancyService) CreateKey(ctx context.Context, userID, projectID string, in KeyInput) (*IssuedKey, error) {
	p, err := s.ManagedProject(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	return s.issue(ctx, userID, p, in, "")
}

func (s *TenancyService) issue(ctx context.Context, userID string, p *tenancy.Project, in KeyInput, rotatedFrom string) (*IssuedKey, error) {
	name, err := tenancy.CleanName("key name", in.Name)
	if err != nil {
		return nil, invalid(err)
	}
	if !in.Environment.Valid() {
		return nil, fmt.Errorf("%w: environment must be sandbox, devnet or mainnet", ErrInvalidRequest)
	}
	scopes, err := tenancy.CleanScopes(in.Scopes)
	if err != nil {
		return nil, invalid(err)
	}
	if in.ExpiresInDays < 0 || in.ExpiresInDays > 366 {
		return nil, fmt.Errorf("%w: expires_in_days must be 0 (never) to 366", ErrInvalidRequest)
	}
	raw, hash, prefix, err := tenancy.GenerateKey(in.Environment)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	ag := &agent.Identity{
		ID: newID("agent"), UserID: userID, ClientID: APIKeyClientID, Name: "API key: " + name,
		Permissions: SpendPassPermissions, TokenHash: hash, CreatedAt: now,
	}
	if err := s.agents.Create(ctx, ag); err != nil {
		return nil, fmt.Errorf("app: creating the key's identity: %w", err)
	}
	k := &tenancy.APIKey{
		ID: newID("key"), ProjectID: p.ID, Environment: in.Environment, Name: name, Prefix: prefix, AgentID: ag.ID,
		Scopes: scopes, CreatedBy: userID, CreatedAt: now, RotatedFrom: rotatedFrom,
	}
	if in.ExpiresInDays > 0 {
		exp := now.Add(time.Duration(in.ExpiresInDays) * 24 * time.Hour)
		k.ExpiresAt = &exp
	}
	if err := s.store.CreateAPIKey(ctx, k); err != nil {
		_ = s.agents.Revoke(ctx, ag.ID, now)
		return nil, err
	}
	s.event(ctx, p.OrgID, p.ID, userID, "api_key.created", map[string]any{
		"key_id": k.ID, "prefix": k.Prefix, "environment": string(k.Environment), "rotated_from": rotatedFrom,
	})
	return &IssuedKey{APIKey: *k, Secret: raw}, nil
}

// Keys lists a project's keys (never their secrets).
func (s *TenancyService) Keys(ctx context.Context, userID, projectID string) ([]tenancy.APIKey, error) {
	if _, _, err := s.ProjectFor(ctx, userID, projectID); err != nil {
		return nil, err
	}
	return s.store.APIKeys(ctx, projectID)
}

// managedKey is a key the person may revoke or rotate.
func (s *TenancyService) managedKey(ctx context.Context, userID, keyID string) (*tenancy.APIKey, *tenancy.Project, error) {
	k, err := s.store.APIKey(ctx, keyID)
	if err != nil {
		if errors.Is(err, shared.ErrNotFound) {
			return nil, nil, fmt.Errorf("%w: key %s", shared.ErrNotFound, keyID)
		}
		return nil, nil, err
	}
	p, err := s.ManagedProject(ctx, userID, k.ProjectID)
	if err != nil {
		if errors.Is(err, shared.ErrNotFound) {
			return nil, nil, fmt.Errorf("%w: key %s", shared.ErrNotFound, keyID)
		}
		return nil, nil, err
	}
	return k, p, nil
}

// RevokeKey ends a key at once: its identity stops signing in.
func (s *TenancyService) RevokeKey(ctx context.Context, userID, keyID string) error {
	k, p, err := s.managedKey(ctx, userID, keyID)
	if err != nil {
		return err
	}
	if k.RevokedAt != nil {
		return nil
	}
	now := s.now().UTC()
	if err := s.agents.Revoke(ctx, k.AgentID, now); err != nil {
		return err
	}
	if err := s.store.RevokeAPIKey(ctx, k.ID, now); err != nil {
		return err
	}
	s.event(ctx, p.OrgID, p.ID, userID, "api_key.revoked", map[string]any{"key_id": k.ID, "prefix": k.Prefix})
	return nil
}

// RotateKey issues a replacement with the same name, environment and scopes,
// then revokes the old key.
func (s *TenancyService) RotateKey(ctx context.Context, userID, keyID string) (*IssuedKey, error) {
	k, p, err := s.managedKey(ctx, userID, keyID)
	if err != nil {
		return nil, err
	}
	if k.RevokedAt != nil {
		return nil, fmt.Errorf("%w: a revoked key can't be rotated; issue a new one", shared.ErrConflict)
	}
	in := KeyInput{Name: k.Name, Environment: k.Environment, Scopes: k.Scopes}
	if k.ExpiresAt != nil {
		in.ExpiresInDays = int(time.Until(*k.ExpiresAt).Hours()/24) + 1
		if in.ExpiresInDays > 366 {
			in.ExpiresInDays = 366
		}
	}
	issued, err := s.issue(ctx, userID, p, in, k.ID)
	if err != nil {
		return nil, err
	}
	if err := s.RevokeKey(ctx, userID, k.ID); err != nil {
		return nil, err
	}
	return issued, nil
}

// Events is a project's platform audit trail, newest first.
func (s *TenancyService) Events(ctx context.Context, userID, projectID string, limit int) ([]PlatformEvent, error) {
	if _, _, err := s.ProjectFor(ctx, userID, projectID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return s.store.PlatformEvents(ctx, projectID, limit)
}

// KeyForAgent returns the API key an agent identity belongs to, or nil when
// the agent is not a key (a Spend Pass token, the console). A key that is
// revoked or expired is refused.
func (s *TenancyService) KeyForAgent(ctx context.Context, ag *agent.Identity) (*tenancy.APIKey, error) {
	if ag == nil || ag.ClientID != APIKeyClientID {
		return nil, nil
	}
	k, err := s.store.APIKeyByAgent(ctx, ag.ID)
	if err != nil {
		return nil, fmt.Errorf("%w: this API key is not known", shared.ErrUnauthorized)
	}
	now := s.now()
	if !k.Active(now) {
		return nil, fmt.Errorf("%w: this API key is revoked or expired", shared.ErrUnauthorized)
	}
	if k.LastUsedAt == nil || now.Sub(*k.LastUsedAt) > time.Minute {
		_ = s.store.TouchAPIKey(ctx, k.ID, now.UTC())
	}
	return k, nil
}

// ExecutorFor resolves who spends for a request made with an API key: the
// agent of a Spend Pass in the key's project and environment, either the one
// named or the newest active one. A key never spends on its own, and never
// through another project's or another environment's pass. For an agent that
// is not a key it returns ("", nil, nil): the caller's own rules apply.
func (s *TenancyService) ExecutorFor(ctx context.Context, ag *agent.Identity, passID string, scope tenancy.Scope) (string, *tenancy.APIKey, error) {
	k, err := s.KeyForAgent(ctx, ag)
	if err != nil || k == nil {
		return "", nil, err
	}
	if !k.Has(scope) {
		return "", k, fmt.Errorf("%w: this API key lacks the %q scope", shared.ErrUnauthorized, scope)
	}
	if s.passes == nil {
		return "", k, fmt.Errorf("%w: Spend Passes are not available on this server", shared.ErrNotImplemented)
	}
	passes, err := s.passes.ListByProject(ctx, k.ProjectID, string(k.Environment))
	if err != nil {
		return "", k, err
	}
	now := s.now()
	passID = strings.TrimSpace(passID)
	for _, p := range passes {
		if passID != "" && p.ID != passID {
			continue
		}
		if !p.Active(now) {
			if passID != "" {
				return "", k, fmt.Errorf("%w: that Spend Pass is revoked or expired", shared.ErrUnauthorized)
			}
			continue
		}
		return p.AgentID, k, nil
	}
	if passID != "" {
		return "", k, fmt.Errorf("%w: pass %s (it must belong to this key's project and %s environment)", shared.ErrNotFound, passID, k.Environment)
	}
	return "", k, fmt.Errorf("%w: no active Spend Pass in this project's %s environment; create one under Spend passes", shared.ErrConflict, k.Environment)
}
