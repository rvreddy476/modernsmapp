package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ConnectionFilterStore is the read-only data-access layer used by the
// connection-request auto-filter (friends-sheets spec §5.1, §9.2). It only
// reads trust-safety's own tables (trust.user_trust_state, trust.reports);
// it never mutates state.
type ConnectionFilterStore struct {
	db *pgxpool.Pool
}

// NewConnectionFilterStore constructs a ConnectionFilterStore.
func NewConnectionFilterStore(db *pgxpool.Pool) *ConnectionFilterStore {
	return &ConnectionFilterStore{db: db}
}

// SenderTrustSignal is the minimal trust snapshot the auto-filter needs about a
// connection-request sender. HasState is false when the sender has no
// trust.user_trust_state row yet (cold-start) — callers must fail open in that
// case rather than treating a missing row as low trust.
type SenderTrustSignal struct {
	HasState     bool
	Shadowbanned bool
	TrustScore   int
}

// GetSenderTrustSignal returns the sender's shadowban flag and trust score from
// trust.user_trust_state. When no row exists it returns HasState=false with
// zero values — the caller MUST NOT filter on that (fail open).
func (s *ConnectionFilterStore) GetSenderTrustSignal(ctx context.Context, senderID uuid.UUID) (SenderTrustSignal, error) {
	var sig SenderTrustSignal
	err := s.db.QueryRow(ctx, `
		SELECT shadowbanned, trust_score
		FROM trust.user_trust_state
		WHERE user_id = $1
	`, senderID).Scan(&sig.Shadowbanned, &sig.TrustScore)
	if errors.Is(err, pgx.ErrNoRows) {
		// Cold-start sender — no trust state yet. Fail open.
		return SenderTrustSignal{HasState: false}, nil
	}
	if err != nil {
		return SenderTrustSignal{}, err
	}
	sig.HasState = true
	return sig, nil
}

// HasPriorReportAgainst returns true when reporterID has previously filed a
// report in trust.reports that targets targetUserID as a user (entity_type =
// 'user'). Any status counts — even a dismissed report is a signal that the
// receiver did not want contact from this sender.
func (s *ConnectionFilterStore) HasPriorReportAgainst(ctx context.Context, reporterID, targetUserID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM trust.reports
			WHERE reporter_id = $1
			  AND entity_type = 'user'
			  AND entity_id = $2
		)
	`, reporterID, targetUserID).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}
