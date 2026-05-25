// Account risk store — §P0-7 Phase A fake-account risk scoring.
//
// The service layer (internal/service/risk.go) aggregates seven signals
// into a 0..100 score; this file is the data plane: a wide-projection
// AccountRisk row, an idempotent UpsertAccountRisk, paginated
// ListByLevel for the admin queue, and the three count helpers the
// scoring formula needs (sparks-last-N-minutes, reports-against-user,
// blocks-by-user). All queries are simple time-window WHERE clauses
// per the deliverables — no Phase 3 ML aggregation pipeline here.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Risk-level constants. Centralised so the scoring formula + enforcement
// hooks + admin queue never drift on the string literal. Order matches
// the severity ladder used by the scoring formula's threshold table.
const (
	RiskLevelAllow              = "allow"
	RiskLevelReduceReach        = "reduce_reach"
	RiskLevelRequireRecheck     = "require_recheck"
	RiskLevelHideFromDiscovery  = "hide_from_discovery"
	RiskLevelChatHold           = "chat_hold"
	RiskLevelAdminReview        = "admin_review"
	RiskLevelSuspend            = "suspend"
)

// AccountRisk is one row of dating_account_risk.
//
// Signals is the raw per-signal numeric contribution map written by the
// scoring service. The schema stores it as JSONB so Phase B can add
// new keys (device-reuse, ip_asn_velocity_real) without a migration.
type AccountRisk struct {
	UserID          uuid.UUID      `json:"user_id"`
	RiskScore       int            `json:"risk_score"`
	RiskLevel       string         `json:"risk_level"`
	Signals         map[string]any `json:"signals"`
	LastEvaluatedAt time.Time      `json:"last_evaluated_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

// GetAccountRisk returns the risk row for userID, or (nil, nil) when no
// row exists yet. Pre-evaluation users are implicitly RiskLevelAllow at
// the service layer — we deliberately don't return a zero-value row
// here so callers can distinguish "never evaluated" from "evaluated to
// 0".
func (s *Store) GetAccountRisk(ctx context.Context, userID uuid.UUID) (*AccountRisk, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user_id required")
	}
	row := s.db.QueryRow(ctx, `
        SELECT user_id, risk_score, risk_level, signals, last_evaluated_at, updated_at
        FROM dating_account_risk
        WHERE user_id = $1`, userID)
	r := &AccountRisk{}
	var raw []byte
	if err := row.Scan(&r.UserID, &r.RiskScore, &r.RiskLevel, &raw,
		&r.LastEvaluatedAt, &r.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan account risk: %w", err)
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &r.Signals); err != nil {
			// Treat a malformed JSON blob as empty signals so the row
			// itself is still usable for enforcement decisions.
			r.Signals = map[string]any{}
		}
	} else {
		r.Signals = map[string]any{}
	}
	return r, nil
}

// UpsertAccountRisk inserts or refreshes the user's risk row. The
// last_evaluated_at + updated_at stamps are server-side NOW() so the
// sweeper's "older than 1h" predicate stays honest regardless of the
// caller's clock.
func (s *Store) UpsertAccountRisk(ctx context.Context, r *AccountRisk) error {
	if r == nil || r.UserID == uuid.Nil {
		return fmt.Errorf("invalid: account risk row required")
	}
	if r.RiskScore < 0 {
		r.RiskScore = 0
	}
	if r.RiskScore > 100 {
		r.RiskScore = 100
	}
	if r.RiskLevel == "" {
		r.RiskLevel = RiskLevelAllow
	}
	raw, err := json.Marshal(r.Signals)
	if err != nil {
		return fmt.Errorf("marshal signals: %w", err)
	}
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	_, err = s.db.Exec(ctx, `
        INSERT INTO dating_account_risk
            (user_id, risk_score, risk_level, signals, last_evaluated_at, updated_at)
        VALUES ($1, $2, $3, $4::jsonb, NOW(), NOW())
        ON CONFLICT (user_id) DO UPDATE
            SET risk_score        = EXCLUDED.risk_score,
                risk_level        = EXCLUDED.risk_level,
                signals           = EXCLUDED.signals,
                last_evaluated_at = NOW(),
                updated_at        = NOW()`,
		r.UserID, r.RiskScore, r.RiskLevel, raw)
	if err != nil {
		return fmt.Errorf("upsert account risk: %w", err)
	}
	return nil
}

// ListByLevel returns risk rows at the supplied level, newest-evaluated
// first. Empty level returns every row regardless. Used by the admin
// queue's read-side; pagination is plain limit+offset because the
// table only carries rows we've actually evaluated, which never gets
// to feed-timeline volume.
func (s *Store) ListByLevel(ctx context.Context, level string, limit, offset int) ([]*AccountRisk, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var (
		rows pgx.Rows
		err  error
	)
	if level == "" {
		rows, err = s.db.Query(ctx, `
            SELECT user_id, risk_score, risk_level, signals, last_evaluated_at, updated_at
            FROM dating_account_risk
            ORDER BY last_evaluated_at DESC
            LIMIT $1 OFFSET $2`, limit, offset)
	} else {
		rows, err = s.db.Query(ctx, `
            SELECT user_id, risk_score, risk_level, signals, last_evaluated_at, updated_at
            FROM dating_account_risk
            WHERE risk_level = $1
            ORDER BY last_evaluated_at DESC
            LIMIT $2 OFFSET $3`, level, limit, offset)
	}
	if err != nil {
		return nil, fmt.Errorf("list account risk by level: %w", err)
	}
	defer rows.Close()

	out := make([]*AccountRisk, 0, limit)
	for rows.Next() {
		r := &AccountRisk{}
		var raw []byte
		if err := rows.Scan(&r.UserID, &r.RiskScore, &r.RiskLevel, &raw,
			&r.LastEvaluatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan account risk row: %w", err)
		}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &r.Signals)
		}
		if r.Signals == nil {
			r.Signals = map[string]any{}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListStaleForRecompute returns up to `limit` users whose
// last_evaluated_at is older than the supplied window. The sweeper
// uses this list to drive periodic recompute; cap at 500 per tick is
// enforced by the caller.
func (s *Store) ListStaleForRecompute(ctx context.Context, olderThan time.Duration, limit int) ([]uuid.UUID, error) {
	if limit <= 0 {
		limit = 500
	}
	cutoff := time.Now().Add(-olderThan)
	rows, err := s.db.Query(ctx, `
        SELECT user_id FROM dating_account_risk
        WHERE last_evaluated_at < $1
        ORDER BY last_evaluated_at ASC
        LIMIT $2`, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("list stale risk targets: %w", err)
	}
	defer rows.Close()
	out := make([]uuid.UUID, 0, limit)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan stale risk row: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// CountSparksLast returns how many sparks `userID` sent inside the
// supplied trailing window. Used by the spark-velocity signal in the
// scoring formula. We count sent-by (from_user_id) because the abuse
// vector is one account spraying sparks, not the receiver getting
// spammed.
func (s *Store) CountSparksLast(ctx context.Context, userID uuid.UUID, window time.Duration) (int, error) {
	if userID == uuid.Nil {
		return 0, fmt.Errorf("invalid: user_id required")
	}
	if window <= 0 {
		window = time.Hour
	}
	cutoff := time.Now().Add(-window)
	var n int
	err := s.db.QueryRow(ctx, `
        SELECT COUNT(*) FROM dating_sparks
        WHERE from_user_id = $1
          AND created_at >= $2`, userID, cutoff).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count sparks last: %w", err)
	}
	return n, nil
}

// CountReportsAgainst returns the total number of dating_reports rows
// whose target_id is userID. Used by the report-count signal in the
// scoring formula. Report quality (status==actioned vs dismissed) is
// considered separately at the service layer.
func (s *Store) CountReportsAgainst(ctx context.Context, userID uuid.UUID) (int, error) {
	if userID == uuid.Nil {
		return 0, fmt.Errorf("invalid: user_id required")
	}
	var n int
	err := s.db.QueryRow(ctx, `
        SELECT COUNT(*) FROM dating_reports
        WHERE target_id = $1`, userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count reports against: %w", err)
	}
	return n, nil
}

// CountActionedReportsAgainst returns the subset of CountReportsAgainst
// whose status indicates moderator action was taken
// ('actioned','resolved'). The scoring formula weighs these higher
// than raw report counts so a single targeted-harassment campaign
// doesn't surface a victim as high-risk.
func (s *Store) CountActionedReportsAgainst(ctx context.Context, userID uuid.UUID) (int, error) {
	if userID == uuid.Nil {
		return 0, fmt.Errorf("invalid: user_id required")
	}
	var n int
	err := s.db.QueryRow(ctx, `
        SELECT COUNT(*) FROM dating_reports
        WHERE target_id = $1
          AND status IN ('actioned','resolved')`, userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count actioned reports against: %w", err)
	}
	return n, nil
}

// CountBlocksOfUser returns the number of dating_blocks rows where
// `userID` is the blocker. A high block-rate-as-blocker is itself a
// risk signal — accounts that block heavily are often either spam
// bots burning through preview cycles or accounts probing the deck
// without reciprocity. The opposite direction (blocked-by-many) is
// covered indirectly by the report signal.
func (s *Store) CountBlocksOfUser(ctx context.Context, userID uuid.UUID) (int, error) {
	if userID == uuid.Nil {
		return 0, fmt.Errorf("invalid: user_id required")
	}
	var n int
	err := s.db.QueryRow(ctx, `
        SELECT COUNT(*) FROM dating_blocks
        WHERE user_id = $1`, userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count blocks of user: %w", err)
	}
	return n, nil
}

// CountPhotosByModerationStatus returns (approved, rejected, pending)
// for the user's photos. Drives the photo-approval-state signal.
func (s *Store) CountPhotosByModerationStatus(ctx context.Context, userID uuid.UUID) (approved, rejected, pending int, err error) {
	if userID == uuid.Nil {
		return 0, 0, 0, fmt.Errorf("invalid: user_id required")
	}
	row := s.db.QueryRow(ctx, `
        SELECT
            COUNT(*) FILTER (WHERE moderation_status = 'approved'),
            COUNT(*) FILTER (WHERE moderation_status = 'rejected'),
            COUNT(*) FILTER (WHERE moderation_status = 'pending')
        FROM dating_photos
        WHERE user_id = $1`, userID)
	if err = row.Scan(&approved, &rejected, &pending); err != nil {
		return 0, 0, 0, fmt.Errorf("count photos by status: %w", err)
	}
	return approved, rejected, pending, nil
}
