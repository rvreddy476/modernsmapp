package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// DispatchAttempt is one row in rider_dispatch_attempts.
type DispatchAttempt struct {
	ID                uuid.UUID  `json:"id"`
	RideID            uuid.UUID  `json:"ride_id"`
	Generation        int        `json:"generation"`
	SearchRadiusKM    float64    `json:"search_radius_km"`
	StrategyVersion   string     `json:"strategy_version"`
	StartedAt         time.Time  `json:"started_at"`
	EndedAt           *time.Time `json:"ended_at,omitempty"`
	Outcome           string     `json:"outcome"`
	CandidatesScanned int        `json:"candidates_scanned"`
	OffersSent        int        `json:"offers_sent"`
	CreatedAt         time.Time  `json:"created_at"`
}

// RecordDispatchAttempt inserts or updates a dispatch attempt row.
func (s *Store) RecordDispatchAttempt(ctx context.Context, rideID uuid.UUID, gen int, radiusKM float64, strategy string, outcome string, scanned, sent int) error {
	const q = `
        INSERT INTO rider_dispatch_attempts (
            ride_id, generation, search_radius_km, strategy_version, outcome, candidates_scanned, offers_sent
        ) VALUES (
            $1, $2, $3, $4, $5, $6, $7
        )
        ON CONFLICT (ride_id, generation) DO UPDATE SET
            outcome = EXCLUDED.outcome,
            candidates_scanned = EXCLUDED.candidates_scanned,
            offers_sent = EXCLUDED.offers_sent,
            ended_at = CASE WHEN EXCLUDED.outcome != 'pending' THEN NOW() ELSE rider_dispatch_attempts.ended_at END`
	_, err := s.db.Exec(ctx, q, rideID, gen, radiusKM, strategy, outcome, scanned, sent)
	if err != nil {
		return fmt.Errorf("record dispatch attempt: %w", err)
	}
	return nil
}
