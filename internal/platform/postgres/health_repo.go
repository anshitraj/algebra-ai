package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/project-algebra/algebra/internal/app"
)

// HealthRepo backs app.HealthStore — see migrations/0018_provider_health.sql.
type HealthRepo struct{ db *DB }

func NewHealthRepo(db *DB) *HealthRepo { return &HealthRepo{db: db} }

var _ app.HealthStore = (*HealthRepo)(nil)

const healthCols = `candidate_id, provider, capability, endpoint, COALESCE(network,''), status, COALESCE(http_status,0), latency_ms,
	listed_price_minor, live_price_minor, overcharges, checks, ups, consecutive_failures, COALESCE(error,''), checked_at`

func scanHealth(row pgx.Row) (app.EndpointHealth, error) {
	var h app.EndpointHealth
	var status string
	var checked time.Time
	err := row.Scan(&h.CandidateID, &h.Provider, &h.Capability, &h.Endpoint, &h.Network, &status, &h.HTTPStatus, &h.LatencyMS,
		&h.ListedPriceMinor, &h.LivePriceMinor, &h.Overcharges, &h.Checks, &h.Ups, &h.Failures, &h.Error, &checked)
	h.Status, h.CheckedAt = app.HealthStatus(status), checked.UTC()
	return h, err
}

func (r *HealthRepo) SaveHealth(ctx context.Context, h app.EndpointHealth) error {
	var httpStatus any
	if h.HTTPStatus > 0 {
		httpStatus = h.HTTPStatus
	}
	_, err := r.db.Pool.Exec(ctx, `
		INSERT INTO provider_health (candidate_id, provider, capability, endpoint, network, status, http_status, latency_ms,
		    listed_price_minor, live_price_minor, overcharges, checks, ups, consecutive_failures, error, checked_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		ON CONFLICT (candidate_id) DO UPDATE SET
		    provider = EXCLUDED.provider, capability = EXCLUDED.capability, endpoint = EXCLUDED.endpoint, network = EXCLUDED.network,
		    status = EXCLUDED.status, http_status = EXCLUDED.http_status, latency_ms = EXCLUDED.latency_ms,
		    listed_price_minor = EXCLUDED.listed_price_minor, live_price_minor = EXCLUDED.live_price_minor,
		    overcharges = EXCLUDED.overcharges, checks = EXCLUDED.checks, ups = EXCLUDED.ups,
		    consecutive_failures = EXCLUDED.consecutive_failures, error = EXCLUDED.error, checked_at = EXCLUDED.checked_at`,
		h.CandidateID, h.Provider, h.Capability, h.Endpoint, nullable(h.Network), string(h.Status), httpStatus, h.LatencyMS,
		h.ListedPriceMinor, h.LivePriceMinor, h.Overcharges, h.Checks, h.Ups, h.Failures, nullable(h.Error), h.CheckedAt.UTC())
	if err != nil {
		return fmt.Errorf("postgres: saving provider health: %w", err)
	}
	return nil
}

func (r *HealthRepo) HealthFor(ctx context.Context, ids []string) (map[string]app.EndpointHealth, error) {
	out := map[string]app.EndpointHealth{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.db.Pool.Query(ctx, `SELECT `+healthCols+` FROM provider_health WHERE candidate_id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading provider health: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		h, err := scanHealth(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning provider health: %w", err)
		}
		out[h.CandidateID] = h
	}
	return out, rows.Err()
}

func (r *HealthRepo) HealthForCapability(ctx context.Context, capability string) ([]app.EndpointHealth, error) {
	rows, err := r.db.Pool.Query(ctx, `SELECT `+healthCols+` FROM provider_health WHERE capability = $1 ORDER BY checked_at DESC LIMIT 500`, capability)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing provider health: %w", err)
	}
	defer rows.Close()
	out := []app.EndpointHealth{}
	for rows.Next() {
		h, err := scanHealth(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning provider health: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
