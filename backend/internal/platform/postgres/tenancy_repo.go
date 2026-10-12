package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/domain/tenancy"
)

// TenancyRepo backs app.TenancyStore — see migrations/0021_platform_tenancy.sql.
type TenancyRepo struct{ db *DB }

func NewTenancyRepo(db *DB) *TenancyRepo { return &TenancyRepo{db: db} }

var _ app.TenancyStore = (*TenancyRepo)(nil)

func (r *TenancyRepo) CreateOrganization(ctx context.Context, o *tenancy.Organization, owner tenancy.Member) error {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: beginning organization insert: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO organizations (id, name, owner_user_id, plan, trial_plan, trial_ends_at, created_at)
		VALUES ($1,$2,$3,$4,NULLIF($5,''),$6,$7)`,
		o.ID, o.Name, o.OwnerUserID, o.Plan, o.TrialPlan, o.TrialEndsAt, o.CreatedAt); err != nil {
		return fmt.Errorf("postgres: inserting organization: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO organization_members (org_id, user_id, role, created_at) VALUES ($1,$2,$3,$4)`,
		owner.OrgID, owner.UserID, string(owner.Role), owner.CreatedAt); err != nil {
		return fmt.Errorf("postgres: inserting organization owner: %w", err)
	}
	return tx.Commit(ctx)
}

const orgCols = `o.id, o.name, o.owner_user_id, o.plan, COALESCE(o.trial_plan,''), o.trial_ends_at, o.created_at`

func scanOrg(row pgx.Row, extra ...any) (*tenancy.Organization, error) {
	var o tenancy.Organization
	dest := append([]any{&o.ID, &o.Name, &o.OwnerUserID, &o.Plan, &o.TrialPlan, &o.TrialEndsAt, &o.CreatedAt}, extra...)
	if err := row.Scan(dest...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, shared.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: scanning organization: %w", err)
	}
	return &o, nil
}

func (r *TenancyRepo) OrganizationsForUser(ctx context.Context, userID string) ([]app.OrgMembership, error) {
	rows, err := r.db.Pool.Query(ctx, `SELECT `+orgCols+`, m.role FROM organizations o
		JOIN organization_members m ON m.org_id = o.id WHERE m.user_id = $1 ORDER BY o.created_at`, userID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing organizations: %w", err)
	}
	defer rows.Close()
	out := []app.OrgMembership{}
	for rows.Next() {
		var role string
		o, err := scanOrg(rows, &role)
		if err != nil {
			return nil, err
		}
		out = append(out, app.OrgMembership{Organization: *o, Role: tenancy.Role(role)})
	}
	return out, rows.Err()
}

func (r *TenancyRepo) Organization(ctx context.Context, id string) (*tenancy.Organization, error) {
	return scanOrg(r.db.Pool.QueryRow(ctx, `SELECT `+orgCols+` FROM organizations o WHERE o.id = $1`, id))
}

func (r *TenancyRepo) Member(ctx context.Context, orgID, userID string) (*tenancy.Member, error) {
	var m tenancy.Member
	var role string
	err := r.db.Pool.QueryRow(ctx, `SELECT org_id, user_id, role, created_at FROM organization_members WHERE org_id = $1 AND user_id = $2`,
		orgID, userID).Scan(&m.OrgID, &m.UserID, &role, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, shared.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading membership: %w", err)
	}
	m.Role = tenancy.Role(role)
	return &m, nil
}

func (r *TenancyRepo) CreateProject(ctx context.Context, p *tenancy.Project) error {
	if _, err := r.db.Pool.Exec(ctx, `INSERT INTO projects (id, org_id, name, default_strategy, created_at) VALUES ($1,$2,$3,$4,$5)`,
		p.ID, p.OrgID, p.Name, p.DefaultStrategy, p.CreatedAt); err != nil {
		return fmt.Errorf("postgres: inserting project: %w", err)
	}
	return nil
}

const projectCols = `id, org_id, name, default_strategy, created_at, archived_at`

func scanProject(row pgx.Row) (*tenancy.Project, error) {
	var p tenancy.Project
	if err := row.Scan(&p.ID, &p.OrgID, &p.Name, &p.DefaultStrategy, &p.CreatedAt, &p.ArchivedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, shared.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: scanning project: %w", err)
	}
	return &p, nil
}

func (r *TenancyRepo) Projects(ctx context.Context, orgID string) ([]tenancy.Project, error) {
	rows, err := r.db.Pool.Query(ctx, `SELECT `+projectCols+` FROM projects WHERE org_id = $1 AND archived_at IS NULL ORDER BY created_at`, orgID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing projects: %w", err)
	}
	defer rows.Close()
	out := []tenancy.Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (r *TenancyRepo) Project(ctx context.Context, id string) (*tenancy.Project, error) {
	return scanProject(r.db.Pool.QueryRow(ctx, `SELECT `+projectCols+` FROM projects WHERE id = $1 AND archived_at IS NULL`, id))
}

func (r *TenancyRepo) UpdateProject(ctx context.Context, p *tenancy.Project) error {
	tag, err := r.db.Pool.Exec(ctx, `UPDATE projects SET name = $2, default_strategy = $3 WHERE id = $1`, p.ID, p.Name, p.DefaultStrategy)
	if err != nil {
		return fmt.Errorf("postgres: updating project: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return shared.ErrNotFound
	}
	return nil
}

func (r *TenancyRepo) CreateAPIKey(ctx context.Context, k *tenancy.APIKey) error {
	scopes := make([]string, len(k.Scopes))
	for i, s := range k.Scopes {
		scopes[i] = string(s)
	}
	_, err := r.db.Pool.Exec(ctx, `
		INSERT INTO api_keys (id, project_id, environment, name, prefix, agent_id, scopes, created_by, created_at, expires_at, rotated_from)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,''))`,
		k.ID, k.ProjectID, string(k.Environment), k.Name, k.Prefix, k.AgentID, scopes, k.CreatedBy, k.CreatedAt, k.ExpiresAt, k.RotatedFrom)
	if err != nil {
		return fmt.Errorf("postgres: inserting api key: %w", err)
	}
	return nil
}

const keyCols = `id, project_id, environment, name, prefix, agent_id, scopes, created_by, created_at, last_used_at, expires_at, revoked_at, COALESCE(rotated_from,'')`

func scanKey(row pgx.Row) (*tenancy.APIKey, error) {
	var k tenancy.APIKey
	var env string
	var scopes []string
	if err := row.Scan(&k.ID, &k.ProjectID, &env, &k.Name, &k.Prefix, &k.AgentID, &scopes, &k.CreatedBy, &k.CreatedAt,
		&k.LastUsedAt, &k.ExpiresAt, &k.RevokedAt, &k.RotatedFrom); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, shared.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: scanning api key: %w", err)
	}
	k.Environment = tenancy.Environment(env)
	for _, s := range scopes {
		k.Scopes = append(k.Scopes, tenancy.Scope(s))
	}
	return &k, nil
}

func (r *TenancyRepo) APIKeys(ctx context.Context, projectID string) ([]tenancy.APIKey, error) {
	rows, err := r.db.Pool.Query(ctx, `SELECT `+keyCols+` FROM api_keys WHERE project_id = $1 ORDER BY created_at DESC LIMIT 200`, projectID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing api keys: %w", err)
	}
	defer rows.Close()
	out := []tenancy.APIKey{}
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *k)
	}
	return out, rows.Err()
}

func (r *TenancyRepo) APIKey(ctx context.Context, id string) (*tenancy.APIKey, error) {
	return scanKey(r.db.Pool.QueryRow(ctx, `SELECT `+keyCols+` FROM api_keys WHERE id = $1`, id))
}

func (r *TenancyRepo) APIKeyByAgent(ctx context.Context, agentID string) (*tenancy.APIKey, error) {
	return scanKey(r.db.Pool.QueryRow(ctx, `SELECT `+keyCols+` FROM api_keys WHERE agent_id = $1`, agentID))
}

func (r *TenancyRepo) RevokeAPIKey(ctx context.Context, id string, at time.Time) error {
	if _, err := r.db.Pool.Exec(ctx, `UPDATE api_keys SET revoked_at = COALESCE(revoked_at, $2) WHERE id = $1`, id, at); err != nil {
		return fmt.Errorf("postgres: revoking api key: %w", err)
	}
	return nil
}

func (r *TenancyRepo) TouchAPIKey(ctx context.Context, id string, at time.Time) error {
	if _, err := r.db.Pool.Exec(ctx, `UPDATE api_keys SET last_used_at = $2 WHERE id = $1`, id, at); err != nil {
		return fmt.Errorf("postgres: touching api key: %w", err)
	}
	return nil
}

func (r *TenancyRepo) AddPlatformEvent(ctx context.Context, e app.PlatformEvent) error {
	data, err := json.Marshal(e.Data)
	if err != nil || e.Data == nil {
		data = []byte("{}")
	}
	if _, err := r.db.Pool.Exec(ctx, `INSERT INTO platform_events (id, org_id, project_id, actor, event, data, created_at)
		VALUES ($1,NULLIF($2,''),NULLIF($3,''),$4,$5,$6::jsonb,$7)`,
		e.ID, e.OrgID, e.ProjectID, e.Actor, e.Event, string(data), e.CreatedAt); err != nil {
		return fmt.Errorf("postgres: inserting platform event: %w", err)
	}
	return nil
}

func (r *TenancyRepo) PlatformEvents(ctx context.Context, projectID string, limit int) ([]app.PlatformEvent, error) {
	rows, err := r.db.Pool.Query(ctx, `SELECT id, COALESCE(org_id,''), COALESCE(project_id,''), actor, event, data, created_at
		FROM platform_events WHERE project_id = $1 ORDER BY created_at DESC LIMIT $2`, projectID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing platform events: %w", err)
	}
	defer rows.Close()
	out := []app.PlatformEvent{}
	for rows.Next() {
		var e app.PlatformEvent
		var data []byte
		if err := rows.Scan(&e.ID, &e.OrgID, &e.ProjectID, &e.Actor, &e.Event, &data, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("postgres: scanning platform event: %w", err)
		}
		_ = json.Unmarshal(data, &e.Data)
		out = append(out, e)
	}
	return out, rows.Err()
}

// StartTrial starts an organization's one trial. It reports false when the
// organization has already had one: the condition is in the UPDATE, so two
// requests at once can't both start it.
func (r *TenancyRepo) StartTrial(ctx context.Context, orgID, plan string, endsAt time.Time) (bool, error) {
	tag, err := r.db.Pool.Exec(ctx, `UPDATE organizations SET trial_plan = $2, trial_ends_at = $3 WHERE id = $1 AND trial_ends_at IS NULL`,
		orgID, plan, endsAt)
	if err != nil {
		return false, fmt.Errorf("postgres: starting trial: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ProjectUsage counts a project's requests since a moment, by state, and what
// its committed ones spent, by payment network.
func (r *TenancyRepo) ProjectUsage(ctx context.Context, projectID string, since time.Time) (*app.ProjectUsage, error) {
	u := &app.ProjectUsage{ProjectID: projectID, Since: since, ByState: map[string]int{}, ByNetwork: []app.NetworkUsage{}}
	rows, err := r.db.Pool.Query(ctx, `SELECT state, count(*) FROM economic_intents WHERE project_id = $1 AND created_at >= $2 GROUP BY state`, projectID, since)
	if err != nil {
		return nil, fmt.Errorf("postgres: counting project requests: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		var n int
		if err := rows.Scan(&state, &n); err != nil {
			return nil, fmt.Errorf("postgres: scanning project requests: %w", err)
		}
		u.ByState[state] = n
		u.Requests += n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: counting project requests: %w", err)
	}
	spent, err := r.db.Pool.Query(ctx, `
		SELECT COALESCE(NULLIF(r.evidence->>'network', ''), r.rail, ''), count(*), COALESCE(sum(i.committed_minor), 0)
		FROM economic_intents i JOIN economic_reservations r ON r.intent_id = i.id AND r.state = 'COMMITTED'
		WHERE i.project_id = $1 AND i.created_at >= $2 GROUP BY 1 ORDER BY 1`, projectID, since)
	if err != nil {
		return nil, fmt.Errorf("postgres: summing project spend: %w", err)
	}
	defer spent.Close()
	for spent.Next() {
		var n app.NetworkUsage
		if err := spent.Scan(&n.Network, &n.Executions, &n.SpentMinor); err != nil {
			return nil, fmt.Errorf("postgres: scanning project spend: %w", err)
		}
		u.ByNetwork = append(u.ByNetwork, n)
	}
	return u, spent.Err()
}
