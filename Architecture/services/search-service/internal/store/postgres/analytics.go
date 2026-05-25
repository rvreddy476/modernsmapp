package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AnalyticsStore writes append-only rows to search_queries +
// search_clicks. Powers offline CTR-rerank training and dashboards.
// All writes are best-effort — the search request never fails on
// insert error (the caller logs at WARN and continues).
type AnalyticsStore struct {
	db *pgxpool.Pool
}

func NewAnalyticsStore(db *pgxpool.Pool) *AnalyticsStore {
	return &AnalyticsStore{db: db}
}

// LogQuery records one search request. Returns the row's UUID so the
// handler can echo it in the response (clients pass it back on
// /v1/search/click for join-key tracking). viewerID may be uuid.Nil
// for anonymous searches.
func (s *AnalyticsStore) LogQuery(
	ctx context.Context,
	viewerID uuid.UUID,
	query string,
	types []string,
	resultCounts map[string]int,
) (uuid.UUID, error) {
	id := uuid.New()
	counts, err := json.Marshal(resultCounts)
	if err != nil {
		return id, err
	}
	var viewer any
	if viewerID != uuid.Nil {
		viewer = viewerID
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO search_queries (id, viewer_id, query, types, result_counts, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, id, viewer, query, types, counts, time.Now().UTC())
	return id, err
}

// LogClick records a result click. queryID is the row id returned by
// LogQuery. Best-effort.
func (s *AnalyticsStore) LogClick(
	ctx context.Context,
	queryID uuid.UUID,
	viewerID uuid.UUID,
	entityType, entityID string,
	position int,
) error {
	var viewer any
	if viewerID != uuid.Nil {
		viewer = viewerID
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO search_clicks (query_id, viewer_id, entity_type, entity_id, position, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, queryID, viewer, entityType, entityID, position, time.Now().UTC())
	return err
}
