// Matches store — spec §10 dating_matches. Match formation is implemented
// as a saga in the service layer (CreateMatchPending -> message-service
// conversation create -> MarkMatchActive on success / DeleteMatch on
// compensation). The store provides the primitives.
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

// Match is a row of dating_matches.
type Match struct {
	ID             uuid.UUID      `json:"id"`
	UserA          uuid.UUID      `json:"user_a"`
	UserB          uuid.UUID      `json:"user_b"`
	Status         string         `json:"status"`
	ConversationID *uuid.UUID     `json:"conversation_id,omitempty"`
	SparkTarget    map[string]any `json:"spark_target,omitempty"`
	MatchedAt      time.Time      `json:"matched_at"`
	FirstMessageAt *time.Time     `json:"first_message_at,omitempty"`
	LastMessageAt  *time.Time     `json:"last_message_at,omitempty"`
	ExpiresAt      *time.Time     `json:"expires_at,omitempty"`
	ClosedBy       *uuid.UUID     `json:"closed_by,omitempty"`
}

// ErrMatchNotFound is returned when a match id does not exist.
var ErrMatchNotFound = errors.New("not_found: match not found")

const matchSelectCols = `id, user_a, user_b, status, conversation_id, spark_target,
    matched_at, first_message_at, last_message_at, expires_at, closed_by`

func scanMatch(row pgx.Row) (*Match, error) {
	m := &Match{}
	var sparkRaw []byte
	if err := row.Scan(
		&m.ID, &m.UserA, &m.UserB, &m.Status, &m.ConversationID, &sparkRaw,
		&m.MatchedAt, &m.FirstMessageAt, &m.LastMessageAt, &m.ExpiresAt, &m.ClosedBy,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrMatchNotFound
		}
		return nil, fmt.Errorf("scan match: %w", err)
	}
	if len(sparkRaw) > 0 {
		_ = json.Unmarshal(sparkRaw, &m.SparkTarget)
	}
	return m, nil
}

// canonicalPair returns (a, b) such that a < b lexicographically. The
// dating_matches table has a CHECK (user_a < user_b) so all rows must be
// inserted with this ordering.
func canonicalPair(x, y uuid.UUID) (uuid.UUID, uuid.UUID) {
	if x.String() < y.String() {
		return x, y
	}
	return y, x
}

// CreateMatchPending inserts a `pending` match row inside the supplied tx
// (or with the pool if tx is nil). The CHECK on user_a < user_b is enforced
// at the schema level; we order the pair canonically before insert.
func (s *Store) CreateMatchPending(ctx context.Context, tx pgx.Tx, userA, userB uuid.UUID, sparkTarget map[string]any) (uuid.UUID, error) {
	a, b := canonicalPair(userA, userB)
	if a == b {
		return uuid.Nil, fmt.Errorf("invalid: cannot match a user with themselves")
	}

	var raw []byte
	if sparkTarget != nil {
		buf, err := json.Marshal(sparkTarget)
		if err != nil {
			return uuid.Nil, fmt.Errorf("marshal spark target: %w", err)
		}
		raw = buf
	}

	// pending is not a CHECK-allowed status; we use 'matched' and rely on
	// the active-vs-pending distinction via conversation_id IS NULL. This
	// keeps the schema's allowed-values list narrow while still letting the
	// saga compensate by deletion.
	const stmt = `
        INSERT INTO dating_matches (user_a, user_b, status, spark_target, matched_at)
        VALUES ($1, $2, 'matched', $3, now())
        RETURNING id`

	var id uuid.UUID
	var err error
	if tx != nil {
		err = tx.QueryRow(ctx, stmt, a, b, raw).Scan(&id)
	} else {
		err = s.db.QueryRow(ctx, stmt, a, b, raw).Scan(&id)
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("insert match pending: %w", err)
	}
	return id, nil
}

// MarkMatchActive sets status='matched', conversation_id and the 7-day
// expiry window. Idempotent: re-running is safe.
func (s *Store) MarkMatchActive(ctx context.Context, matchID, conversationID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
        UPDATE dating_matches
        SET conversation_id = $2,
            status = 'matched',
            matched_at = COALESCE(matched_at, now()),
            expires_at = now() + INTERVAL '7 days'
        WHERE id = $1`, matchID, conversationID)
	if err != nil {
		return fmt.Errorf("mark match active: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrMatchNotFound
	}
	return nil
}

// DeleteMatch hard-deletes a match. Used by the saga's compensation step
// when message-service refuses the conversation create.
func (s *Store) DeleteMatch(ctx context.Context, matchID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM dating_matches WHERE id = $1`, matchID)
	if err != nil {
		return fmt.Errorf("delete match: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrMatchNotFound
	}
	return nil
}

// GetMatch returns one match by id.
func (s *Store) GetMatch(ctx context.Context, id uuid.UUID) (*Match, error) {
	row := s.db.QueryRow(ctx, `SELECT `+matchSelectCols+` FROM dating_matches WHERE id = $1`, id)
	return scanMatch(row)
}

// GetMatchByUsers returns the match between two users (canonical order
// applied internally) or ErrMatchNotFound.
func (s *Store) GetMatchByUsers(ctx context.Context, userA, userB uuid.UUID) (*Match, error) {
	a, b := canonicalPair(userA, userB)
	row := s.db.QueryRow(ctx, `
        SELECT `+matchSelectCols+`
        FROM dating_matches
        WHERE user_a = $1 AND user_b = $2`, a, b)
	return scanMatch(row)
}

// ListMatchesForUser returns matches involving userID, optionally filtered
// by a status bucket. Recognised status filters: 'all', 'active', 'quiet',
// 'sparks-waiting'. Anything else is treated as 'all'.
func (s *Store) ListMatchesForUser(ctx context.Context, userID uuid.UUID, status string) ([]*Match, error) {
	var (
		rows pgx.Rows
		err  error
	)
	switch status {
	case "active":
		rows, err = s.db.Query(ctx, `
            SELECT `+matchSelectCols+`
            FROM dating_matches
            WHERE (user_a = $1 OR user_b = $1)
              AND status IN ('matched','conversing')
            ORDER BY COALESCE(last_message_at, matched_at) DESC
            LIMIT 200`, userID)
	case "quiet":
		rows, err = s.db.Query(ctx, `
            SELECT `+matchSelectCols+`
            FROM dating_matches
            WHERE (user_a = $1 OR user_b = $1)
              AND status = 'quiet'
            ORDER BY COALESCE(last_message_at, matched_at) DESC
            LIMIT 200`, userID)
	case "sparks-waiting":
		rows, err = s.db.Query(ctx, `
            SELECT `+matchSelectCols+`
            FROM dating_matches
            WHERE (user_a = $1 OR user_b = $1)
              AND status = 'matched'
              AND first_message_at IS NULL
            ORDER BY matched_at DESC
            LIMIT 200`, userID)
	default:
		rows, err = s.db.Query(ctx, `
            SELECT `+matchSelectCols+`
            FROM dating_matches
            WHERE user_a = $1 OR user_b = $1
            ORDER BY COALESCE(last_message_at, matched_at) DESC
            LIMIT 200`, userID)
	}
	if err != nil {
		return nil, fmt.Errorf("list matches: %w", err)
	}
	defer rows.Close()

	out := make([]*Match, 0, 16)
	for rows.Next() {
		m, err := scanMatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CloseMatch sets status='closed' and records the actor.
func (s *Store) CloseMatch(ctx context.Context, matchID, closedBy uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
        UPDATE dating_matches
        SET status = 'closed', closed_by = $2
        WHERE id = $1 AND status NOT IN ('closed','expired')`, matchID, closedBy)
	if err != nil {
		return fmt.Errorf("close match: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrMatchNotFound
	}
	return nil
}

// ExtendMatch pushes the expires_at out by `days`. Premium-only at the
// service layer; the store does not enforce the gate.
func (s *Store) ExtendMatch(ctx context.Context, matchID uuid.UUID, days int) error {
	if days <= 0 {
		days = 7
	}
	tag, err := s.db.Exec(ctx, fmt.Sprintf(`
        UPDATE dating_matches
        SET expires_at = COALESCE(expires_at, now()) + INTERVAL '%d days'
        WHERE id = $1`, days), matchID)
	if err != nil {
		return fmt.Errorf("extend match: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrMatchNotFound
	}
	return nil
}

// ExpireStaleMatches transitions un-replied matches past their expiry into
// status='expired'. Returns the list of matches that were expired so the
// caller can emit per-match events.
func (s *Store) ExpireStaleMatches(ctx context.Context) ([]*Match, error) {
	rows, err := s.db.Query(ctx, `
        UPDATE dating_matches
        SET status = 'expired'
        WHERE status = 'matched'
          AND first_message_at IS NULL
          AND expires_at IS NOT NULL
          AND expires_at < now()
        RETURNING `+matchSelectCols)
	if err != nil {
		return nil, fmt.Errorf("expire stale matches: %w", err)
	}
	defer rows.Close()

	out := make([]*Match, 0, 16)
	for rows.Next() {
		m, err := scanMatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MarkQuietMatches transitions matches that have been idle for 14 days into
// status='quiet'. Returns the affected matches for event emission.
func (s *Store) MarkQuietMatches(ctx context.Context) ([]*Match, error) {
	rows, err := s.db.Query(ctx, `
        UPDATE dating_matches
        SET status = 'quiet'
        WHERE status IN ('matched','conversing')
          AND last_message_at IS NOT NULL
          AND last_message_at < now() - INTERVAL '14 days'
        RETURNING `+matchSelectCols)
	if err != nil {
		return nil, fmt.Errorf("mark quiet matches: %w", err)
	}
	defer rows.Close()

	out := make([]*Match, 0, 16)
	for rows.Next() {
		m, err := scanMatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// RecordFirstMessage stamps first_message_at + last_message_at on the match
// row. Idempotent: re-running keeps the original first_message_at.
func (s *Store) RecordFirstMessage(ctx context.Context, matchID uuid.UUID, at time.Time) error {
	if at.IsZero() {
		at = time.Now()
	}
	tag, err := s.db.Exec(ctx, `
        UPDATE dating_matches
        SET first_message_at = COALESCE(first_message_at, $2),
            last_message_at = $2,
            status = CASE WHEN status = 'matched' THEN 'conversing' ELSE status END
        WHERE id = $1`, matchID, at)
	if err != nil {
		return fmt.Errorf("record first message: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrMatchNotFound
	}
	return nil
}

// IsPremium reports whether the user holds a non-expired premium row.
func (s *Store) IsPremium(ctx context.Context, userID uuid.UUID) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx, `
        SELECT EXISTS (
            SELECT 1 FROM dating_premium_subscriptions
            WHERE user_id = $1
              AND (expires_at IS NULL OR expires_at > now())
        )`, userID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("is premium: %w", err)
	}
	return ok, nil
}

// BeginTx exposes a transaction handle for the saga path.
func (s *Store) BeginTx(ctx context.Context) (pgx.Tx, error) {
	return s.db.Begin(ctx)
}
