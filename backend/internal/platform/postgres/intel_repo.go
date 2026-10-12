package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/intel"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// IntelRepo backs app.IntelStore — see migrations/0022_provider_intelligence.sql.
type IntelRepo struct{ db *DB }

func NewIntelRepo(db *DB) *IntelRepo { return &IntelRepo{db: db} }

var _ app.IntelStore = (*IntelRepo)(nil)

const endpointCols = `id, source, provider_id, provider_name, capability, method, url, network, protocol, asset,
	listed_price_minor, pay_to, pinned_pay_to, callable, trust, trust_reason, status, latency_ms, fingerprint,
	quoted_at, tested_at, verified_at, first_seen_at, last_seen_at, last_changed_at, updated_at`

func scanEndpoint(row pgx.Row) (*intel.Endpoint, error) {
	var e intel.Endpoint
	var trust, status string
	if err := row.Scan(&e.ID, &e.Source, &e.ProviderID, &e.ProviderName, &e.Capability, &e.Method, &e.URL, &e.Network, &e.Protocol, &e.Asset,
		&e.ListedPriceMinor, &e.PayTo, &e.PinnedPayTo, &e.Callable, &trust, &e.TrustReason, &status, &e.LatencyMS, &e.Fingerprint,
		&e.QuotedAt, &e.TestedAt, &e.VerifiedAt, &e.FirstSeenAt, &e.LastSeenAt, &e.LastChangedAt, &e.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, shared.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: scanning provider endpoint: %w", err)
	}
	e.Trust, e.Status = intel.Trust(trust), intel.Status(status)
	return &e, nil
}

func (r *IntelRepo) queryEndpoints(ctx context.Context, sql string, args ...any) ([]intel.Endpoint, error) {
	rows, err := r.db.Pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing provider endpoints: %w", err)
	}
	defer rows.Close()
	out := []intel.Endpoint{}
	for rows.Next() {
		e, err := scanEndpoint(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

func (r *IntelRepo) EndpointsBySource(ctx context.Context, source string) ([]intel.Endpoint, error) {
	return r.queryEndpoints(ctx, `SELECT `+endpointCols+` FROM provider_endpoints WHERE source = $1`, source)
}

func (r *IntelRepo) Endpoint(ctx context.Context, id string) (*intel.Endpoint, error) {
	return scanEndpoint(r.db.Pool.QueryRow(ctx, `SELECT `+endpointCols+` FROM provider_endpoints WHERE id = $1`, id))
}

func (r *IntelRepo) EndpointsByID(ctx context.Context, ids []string) (map[string]intel.Endpoint, error) {
	out := map[string]intel.Endpoint{}
	if len(ids) == 0 {
		return out, nil
	}
	eps, err := r.queryEndpoints(ctx, `SELECT `+endpointCols+` FROM provider_endpoints WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	for _, e := range eps {
		out[e.ID] = e
	}
	return out, nil
}

func (r *IntelRepo) SaveEndpoints(ctx context.Context, eps []intel.Endpoint, changes []intel.Change) error {
	if len(eps) == 0 && len(changes) == 0 {
		return nil
	}
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: beginning registry write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	batch := &pgx.Batch{}
	for _, e := range eps {
		batch.Queue(`
			INSERT INTO provider_endpoints (`+endpointCols+`)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26)
			ON CONFLICT (id) DO UPDATE SET
				source = EXCLUDED.source, provider_name = EXCLUDED.provider_name, capability = EXCLUDED.capability,
				method = EXCLUDED.method, protocol = EXCLUDED.protocol, asset = EXCLUDED.asset,
				listed_price_minor = EXCLUDED.listed_price_minor, pay_to = EXCLUDED.pay_to, pinned_pay_to = EXCLUDED.pinned_pay_to,
				callable = EXCLUDED.callable, trust = EXCLUDED.trust, trust_reason = EXCLUDED.trust_reason, status = EXCLUDED.status,
				latency_ms = EXCLUDED.latency_ms, fingerprint = EXCLUDED.fingerprint, quoted_at = EXCLUDED.quoted_at,
				tested_at = EXCLUDED.tested_at, verified_at = EXCLUDED.verified_at, last_seen_at = EXCLUDED.last_seen_at,
				last_changed_at = EXCLUDED.last_changed_at, updated_at = EXCLUDED.updated_at`,
			e.ID, e.Source, e.ProviderID, e.ProviderName, e.Capability, e.Method, e.URL, e.Network, e.Protocol, e.Asset,
			e.ListedPriceMinor, e.PayTo, e.PinnedPayTo, e.Callable, string(e.Trust), e.TrustReason, string(e.Status), e.LatencyMS, e.Fingerprint,
			e.QuotedAt, e.TestedAt, e.VerifiedAt, e.FirstSeenAt, e.LastSeenAt, e.LastChangedAt, e.UpdatedAt)
	}
	for _, c := range changes {
		before, after := jsonOrNull(c.Before), jsonOrNull(c.After)
		batch.Queue(`INSERT INTO provider_changes (id, endpoint_id, provider_id, source, kind, suspicious, before, after, actor, run_id, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8::jsonb,$9,NULLIF($10,''),$11)`,
			c.ID, c.EndpointID, c.ProviderID, c.Source, string(c.Kind), c.Suspicious, before, after, c.Actor, c.RunID, c.At)
	}
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("postgres: writing the provider registry: %w", err)
	}
	return tx.Commit(ctx)
}

func jsonOrNull(m map[string]any) *string {
	if m == nil {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	s := string(b)
	return &s
}

// ListEndpoints filters and sorts the registry for the dashboard.
func (r *IntelRepo) ListEndpoints(ctx context.Context, f app.IntelFilter) ([]intel.Endpoint, int, error) {
	var where []string
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	if f.Source != "" {
		where = append(where, "source = "+arg(f.Source))
	}
	if f.Network != "" {
		if f.Network == "base" {
			where = append(where, "network LIKE 'base%'")
		} else {
			where = append(where, "network = "+arg(f.Network))
		}
	}
	if f.Protocol != "" {
		where = append(where, "protocol = "+arg(f.Protocol))
	}
	if f.Trust != "" {
		where = append(where, "trust = "+arg(f.Trust))
	}
	switch f.Level {
	case "listed":
		where = append(where, "status <> 'removed'")
	case "quoted":
		where = append(where, "quoted_at IS NOT NULL")
	case "tested":
		where = append(where, "tested_at IS NOT NULL")
	case "verified":
		where = append(where, "trust = 'verified'")
	case "executable":
		where = append(where, "callable AND status = 'active' AND trust <> 'blocked' AND protocol = 'x402'")
	case "removed":
		where = append(where, "status = 'removed'")
	}
	if f.Capability != "" {
		where = append(where, "capability ILIKE "+arg("%"+likeEscape(f.Capability)+"%"))
	}
	if f.Query != "" {
		q := arg("%" + likeEscape(f.Query) + "%")
		where = append(where, "(provider_name ILIKE "+q+" OR provider_id ILIKE "+q+" OR url ILIKE "+q+" OR capability ILIKE "+q+")")
	}
	if !f.NewSince.IsZero() {
		where = append(where, "first_seen_at >= "+arg(f.NewSince))
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := r.db.Pool.QueryRow(ctx, `SELECT count(*) FROM provider_endpoints`+cond, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("postgres: counting provider endpoints: %w", err)
	}
	order := " ORDER BY provider_name, url, network"
	switch f.Sort {
	case "cheapest":
		order = " ORDER BY (listed_price_minor = 0), listed_price_minor, provider_name"
	case "fastest":
		order = " ORDER BY (latency_ms = 0), latency_ms, provider_name"
	case "newest":
		order = " ORDER BY first_seen_at DESC, provider_name"
	case "changed":
		order = " ORDER BY last_changed_at DESC, provider_name"
	}
	limit, offset := arg(f.Limit), arg(max(0, f.Offset))
	eps, err := r.queryEndpoints(ctx, `SELECT `+endpointCols+` FROM provider_endpoints`+cond+order+` LIMIT `+limit+` OFFSET `+offset, args...)
	return eps, total, err
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.TrimSpace(s))
}

func (r *IntelRepo) Changes(ctx context.Context, f app.ChangeFilter) ([]intel.Change, error) {
	var where []string
	var args []any
	if f.EndpointID != "" {
		args = append(args, f.EndpointID)
		where = append(where, "endpoint_id = $"+strconv.Itoa(len(args)))
	}
	if f.Kind != "" {
		args = append(args, f.Kind)
		where = append(where, "kind = $"+strconv.Itoa(len(args)))
	}
	if f.Suspicious {
		where = append(where, "suspicious")
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, f.Limit)
	rows, err := r.db.Pool.Query(ctx, `SELECT id, endpoint_id, provider_id, source, kind, suspicious, before, after, actor, COALESCE(run_id,''), created_at
		FROM provider_changes`+cond+` ORDER BY created_at DESC, id LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing provider changes: %w", err)
	}
	defer rows.Close()
	out := []intel.Change{}
	for rows.Next() {
		var c intel.Change
		var kind string
		var before, after []byte
		if err := rows.Scan(&c.ID, &c.EndpointID, &c.ProviderID, &c.Source, &kind, &c.Suspicious, &before, &after, &c.Actor, &c.RunID, &c.At); err != nil {
			return nil, fmt.Errorf("postgres: scanning provider change: %w", err)
		}
		c.Kind = intel.ChangeKind(kind)
		_ = json.Unmarshal(before, &c.Before)
		_ = json.Unmarshal(after, &c.After)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *IntelRepo) Summary(ctx context.Context, newSince time.Time) (app.IntelSummary, error) {
	s := app.IntelSummary{ByTrust: map[string]int{}, ByStatus: map[string]int{}, ByNetwork: map[string]int{}, BySource: map[string]int{}}
	err := r.db.Pool.QueryRow(ctx, `SELECT count(*), count(DISTINCT provider_id),
			count(*) FILTER (WHERE quoted_at IS NOT NULL), count(*) FILTER (WHERE tested_at IS NOT NULL),
			count(*) FILTER (WHERE callable AND status = 'active' AND trust <> 'blocked' AND protocol = 'x402'),
			count(*) FILTER (WHERE first_seen_at >= $1)
		FROM provider_endpoints`, newSince).Scan(&s.Endpoints, &s.Providers, &s.Quoted, &s.Tested, &s.Executable, &s.NewThisWeek)
	if err != nil {
		return s, fmt.Errorf("postgres: summarising the registry: %w", err)
	}
	for col, m := range map[string]map[string]int{"trust": s.ByTrust, "status": s.ByStatus, "network": s.ByNetwork, "source": s.BySource} {
		rows, err := r.db.Pool.Query(ctx, `SELECT `+col+`, count(*) FROM provider_endpoints GROUP BY 1`)
		if err != nil {
			return s, fmt.Errorf("postgres: summarising the registry by %s: %w", col, err)
		}
		for rows.Next() {
			var k string
			var n int
			if err := rows.Scan(&k, &n); err != nil {
				rows.Close()
				return s, err
			}
			m[k] = n
		}
		rows.Close()
	}
	return s, nil
}

func (r *IntelRepo) SaveRun(ctx context.Context, run app.SyncRun) error {
	sources, _ := json.Marshal(run.Sources)
	if run.Sources == nil {
		sources = []byte("[]")
	}
	_, err := r.db.Pool.Exec(ctx, `
		INSERT INTO catalog_sync_runs (id, trigger, status, sources, seen, added, changed, removed, error, started_at, finished_at)
		VALUES ($1,$2,$3,$4::jsonb,$5,$6,$7,$8,NULLIF($9,''),$10,$11)
		ON CONFLICT (id) DO UPDATE SET status = EXCLUDED.status, sources = EXCLUDED.sources, seen = EXCLUDED.seen,
			added = EXCLUDED.added, changed = EXCLUDED.changed, removed = EXCLUDED.removed, error = EXCLUDED.error,
			finished_at = EXCLUDED.finished_at`,
		run.ID, run.Trigger, run.Status, string(sources), run.Seen, run.Added, run.Changed, run.Removed, run.Error, run.StartedAt, run.FinishedAt)
	if err != nil {
		return fmt.Errorf("postgres: saving a sync run: %w", err)
	}
	return nil
}

func (r *IntelRepo) Runs(ctx context.Context, limit int) ([]app.SyncRun, error) {
	rows, err := r.db.Pool.Query(ctx, `SELECT id, trigger, status, sources, seen, added, changed, removed, COALESCE(error,''), started_at, finished_at
		FROM catalog_sync_runs ORDER BY started_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing sync runs: %w", err)
	}
	defer rows.Close()
	out := []app.SyncRun{}
	for rows.Next() {
		var run app.SyncRun
		var sources []byte
		if err := rows.Scan(&run.ID, &run.Trigger, &run.Status, &sources, &run.Seen, &run.Added, &run.Changed, &run.Removed, &run.Error, &run.StartedAt, &run.FinishedAt); err != nil {
			return nil, fmt.Errorf("postgres: scanning a sync run: %w", err)
		}
		_ = json.Unmarshal(sources, &run.Sources)
		out = append(out, run)
	}
	return out, rows.Err()
}

func (r *IntelRepo) EnsureJob(ctx context.Context, name, schedule string, next time.Time) error {
	if _, err := r.db.Pool.Exec(ctx, `INSERT INTO scheduled_jobs (name, schedule, next_run_at) VALUES ($1,$2,$3) ON CONFLICT (name) DO NOTHING`,
		name, schedule, next); err != nil {
		return fmt.Errorf("postgres: scheduling %s: %w", name, err)
	}
	return nil
}

// ClaimJob is one conditional UPDATE: of any number of instances asking at
// once, exactly one sees a row come back.
func (r *IntelRepo) ClaimJob(ctx context.Context, name, owner string, now time.Time, lease time.Duration, force bool) (bool, error) {
	tag, err := r.db.Pool.Exec(ctx, `
		UPDATE scheduled_jobs SET lease_owner = $2, lease_until = $3::timestamptz + $4 * interval '1 second', last_started_at = $3
		WHERE name = $1 AND ($5 OR next_run_at <= $3) AND (lease_until IS NULL OR lease_until < $3)`,
		name, owner, now, int64(lease/time.Second), force)
	if err != nil {
		return false, fmt.Errorf("postgres: claiming %s: %w", name, err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *IntelRepo) FinishJob(ctx context.Context, name, owner string, now, next time.Time, errText string, checkpoint map[string]app.SourceCheckpoint) error {
	cp, _ := json.Marshal(checkpoint)
	if checkpoint == nil {
		cp = []byte("{}")
	}
	_, err := r.db.Pool.Exec(ctx, `
		UPDATE scheduled_jobs SET lease_owner = NULL, lease_until = NULL, next_run_at = $3, last_finished_at = $4,
			last_success_at = CASE WHEN $5 = '' THEN $4 ELSE last_success_at END, last_error = NULLIF($5,''), checkpoint = $6::jsonb
		WHERE name = $1 AND lease_owner = $2`,
		name, owner, next, now, errText, string(cp))
	if err != nil {
		return fmt.Errorf("postgres: finishing %s: %w", name, err)
	}
	return nil
}

func (r *IntelRepo) Job(ctx context.Context, name string) (*app.ScheduledJob, error) {
	var j app.ScheduledJob
	var cp []byte
	var lastErr *string
	err := r.db.Pool.QueryRow(ctx, `SELECT name, schedule, next_run_at, COALESCE(lease_owner,''), lease_until, last_started_at,
			last_finished_at, last_success_at, last_error, checkpoint FROM scheduled_jobs WHERE name = $1`, name).
		Scan(&j.Name, &j.Schedule, &j.NextRunAt, &j.LeaseOwner, &j.LeaseUntil, &j.LastStartedAt, &j.LastFinishedAt, &j.LastSuccessAt, &lastErr, &cp)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, shared.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading %s: %w", name, err)
	}
	if lastErr != nil {
		j.LastError = *lastErr
	}
	_ = json.Unmarshal(cp, &j.Checkpoint)
	return &j, nil
}
