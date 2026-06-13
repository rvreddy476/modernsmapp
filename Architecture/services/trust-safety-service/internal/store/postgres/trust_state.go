package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// UserTrustState mirrors a row of trust.user_trust_state (spec §8.11).
// Phase 1 is read-only: this state is recomputed periodically but is not used
// to gate or change any behavior.
type UserTrustState struct {
	UserID                uuid.UUID  `json:"user_id"`
	TrustScore            int        `json:"trust_score"`
	TrustTier             string     `json:"trust_tier"`
	AccountAgeDays        int        `json:"account_age_days"`
	ReportsReceived       int        `json:"reports_received"`
	BlocksReceived        int        `json:"blocks_received"`
	ConnectionAcceptRatio *float64   `json:"connection_accept_ratio,omitempty"`
	LastRecomputedAt      time.Time  `json:"last_recomputed_at"`
	Shadowbanned          bool       `json:"shadowbanned"`
	SuspendedUntil        *time.Time `json:"suspended_until,omitempty"`
}

// TrustStateStore is the data-access layer for trust.user_trust_state.
type TrustStateStore struct {
	db *pgxpool.Pool
}

// NewTrustStateStore constructs a TrustStateStore.
func NewTrustStateStore(db *pgxpool.Pool) *TrustStateStore {
	return &TrustStateStore{db: db}
}

// GetTrustState returns a single user's trust state, or nil if no row exists.
func (s *TrustStateStore) GetTrustState(ctx context.Context, userID uuid.UUID) (*UserTrustState, error) {
	var st UserTrustState
	err := s.db.QueryRow(ctx, `
		SELECT user_id, trust_score, trust_tier, account_age_days,
		       reports_received, blocks_received, connection_accept_ratio,
		       last_recomputed_at, shadowbanned, suspended_until
		FROM trust.user_trust_state
		WHERE user_id = $1
	`, userID).Scan(
		&st.UserID, &st.TrustScore, &st.TrustTier, &st.AccountAgeDays,
		&st.ReportsReceived, &st.BlocksReceived, &st.ConnectionAcceptRatio,
		&st.LastRecomputedAt, &st.Shadowbanned, &st.SuspendedUntil,
	)
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// TrustRecomputeInput is the per-user signal bundle the recompute job derives
// locally before applying the §10.1 formula.
type TrustRecomputeInput struct {
	UserID              uuid.UUID
	AccountAgeDays      int
	ReportsReceived     int
	BlocksReceived      int
	ReportsUpheld30d    int
	ReportsPending30d   int
	BlocksReceived30d   int
}

// ListTrustStateUserIDs returns the user IDs that currently have a trust-state
// row, ordered stalest-first so the recompute job can prioritise them.
func (s *TrustStateStore) ListTrustStateUserIDs(ctx context.Context, limit int) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT user_id
		FROM trust.user_trust_state
		ORDER BY last_recomputed_at ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// CollectRecomputeInputs derives the locally-available trust signals for a set
// of users from trust.user_trust_state (account age / counters) and
// trust.reports (pending vs upheld in the last 30 days). Signals the
// trust-safety-service cannot cheaply obtain are left at zero by the caller.
func (s *TrustStateStore) CollectRecomputeInputs(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]*TrustRecomputeInput, error) {
	out := make(map[uuid.UUID]*TrustRecomputeInput, len(userIDs))
	if len(userIDs) == 0 {
		return out, nil
	}

	// Base counters + account age from the trust-state rows themselves.
	stateRows, err := s.db.Query(ctx, `
		SELECT user_id, account_age_days, reports_received, blocks_received
		FROM trust.user_trust_state
		WHERE user_id = ANY($1)
	`, userIDs)
	if err != nil {
		return nil, err
	}
	for stateRows.Next() {
		in := &TrustRecomputeInput{}
		if err := stateRows.Scan(&in.UserID, &in.AccountAgeDays, &in.ReportsReceived, &in.BlocksReceived); err != nil {
			stateRows.Close()
			return nil, err
		}
		out[in.UserID] = in
	}
	stateRows.Close()
	if err := stateRows.Err(); err != nil {
		return nil, err
	}

	// Reports filed AGAINST these users in the last 30 days, split by outcome.
	// entity_id holds the reported user when entity_type = 'user'.
	reportRows, err := s.db.Query(ctx, `
		SELECT entity_id,
		       COUNT(*) FILTER (WHERE status = 'resolved') AS upheld,
		       COUNT(*) FILTER (WHERE status IN ('open', 'reviewing')) AS pending
		FROM trust.reports
		WHERE entity_type = 'user'
		  AND entity_id = ANY($1)
		  AND created_at >= NOW() - INTERVAL '30 days'
		GROUP BY entity_id
	`, userIDs)
	if err != nil {
		return nil, err
	}
	for reportRows.Next() {
		var entityID uuid.UUID
		var upheld, pending int
		if err := reportRows.Scan(&entityID, &upheld, &pending); err != nil {
			reportRows.Close()
			return nil, err
		}
		in, ok := out[entityID]
		if !ok {
			in = &TrustRecomputeInput{UserID: entityID}
			out[entityID] = in
		}
		in.ReportsUpheld30d = upheld
		in.ReportsPending30d = pending
	}
	reportRows.Close()
	if err := reportRows.Err(); err != nil {
		return nil, err
	}

	return out, nil
}

// UpsertTrustScore writes back a freshly computed score and tier. Only the
// derived fields are touched; manual flags (shadowbanned, suspended_until) and
// a manually-set 'verified' tier are preserved.
func (s *TrustStateStore) UpsertTrustScore(ctx context.Context, st *UserTrustState) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO trust.user_trust_state
			(user_id, trust_score, trust_tier, account_age_days,
			 reports_received, blocks_received, connection_accept_ratio,
			 last_recomputed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			trust_score             = EXCLUDED.trust_score,
			-- never auto-downgrade a manually-assigned 'verified' tier.
			trust_tier              = CASE WHEN trust.user_trust_state.trust_tier = 'verified'
			                               THEN 'verified'
			                               ELSE EXCLUDED.trust_tier END,
			account_age_days        = EXCLUDED.account_age_days,
			reports_received        = EXCLUDED.reports_received,
			blocks_received         = EXCLUDED.blocks_received,
			connection_accept_ratio = EXCLUDED.connection_accept_ratio,
			last_recomputed_at      = NOW()
	`, st.UserID, st.TrustScore, st.TrustTier, st.AccountAgeDays,
		st.ReportsReceived, st.BlocksReceived, st.ConnectionAcceptRatio)
	return err
}
