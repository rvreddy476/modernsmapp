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

// ErrMatchPairBlocked is returned when a match is requested for a pair where
// either user has blocked the other.
var ErrMatchPairBlocked = errors.New("forbidden: match pair is blocked")

// openMatchStatuses is the status set covered by uq_dating_matches_open_pair.
const openMatchStatuses = `('matched','conversing','quiet')`

// CreateOrGetOpenMatch is match formation's insert-or-get. It inserts a
// pending 'matched' row unless the pair already has an open match (the
// partial unique index uq_dating_matches_open_pair decides under
// concurrency) or is blocked. Returns the match id and whether this call
// created it: concurrent mutual sparks all get the same id and exactly one
// of them gets created=true and runs the chat saga.
func (s *Store) CreateOrGetOpenMatch(ctx context.Context, userA, userB uuid.UUID, sparkTarget map[string]any) (uuid.UUID, bool, error) {
	a, b := canonicalPair(userA, userB)
	if a == b {
		return uuid.Nil, false, fmt.Errorf("invalid: cannot match a user with themselves")
	}
	var raw []byte
	if sparkTarget != nil {
		buf, err := json.Marshal(sparkTarget)
		if err != nil {
			return uuid.Nil, false, fmt.Errorf("marshal spark target: %w", err)
		}
		raw = buf
	}
	for attempt := 0; attempt < 3; attempt++ {
		var id uuid.UUID
		err := s.db.QueryRow(ctx, `
            INSERT INTO dating_matches (user_a, user_b, status, spark_target, matched_at)
            SELECT $1::uuid, $2::uuid, 'matched', $3::jsonb, now()
            WHERE NOT `+blockedPairPredicate("$1::uuid", "$2::uuid")+`
            ON CONFLICT (user_a, user_b) WHERE status IN `+openMatchStatuses+` DO NOTHING
            RETURNING id`, a, b, raw).Scan(&id)
		if err == nil {
			return id, true, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, fmt.Errorf("insert match: %w", err)
		}
		err = s.db.QueryRow(ctx, `
            SELECT id FROM dating_matches
            WHERE user_a = $1 AND user_b = $2 AND status IN `+openMatchStatuses, a, b).Scan(&id)
		if err == nil {
			return id, false, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, fmt.Errorf("load open match: %w", err)
		}
		blocked, berr := s.IsBlockedEitherWay(ctx, a, b)
		if berr != nil {
			return uuid.Nil, false, berr
		}
		if blocked {
			return uuid.Nil, false, ErrMatchPairBlocked
		}
		// The conflicting open match closed between the two statements; retry.
	}
	return uuid.Nil, false, fmt.Errorf("insert match: open match for the pair kept changing")
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

// ListSagaPendingMatches returns up to `limit` matches that are still
// stuck without a conversation_id — the chat-side handshake failed or
// the dating-service crashed mid-saga. The SagaReconciler retries each
// one on its next tick. Filters to rows older than `minAge` so the
// reconciler doesn't race the live FormMatch path.
//
// P0-9 in dating/PRODUCTION_GAP_ANALYSIS.md.
func (s *Store) ListSagaPendingMatches(ctx context.Context, minAge time.Duration, limit int) ([]*Match, error) {
	if limit <= 0 {
		limit = 100
	}
	cutoff := time.Now().Add(-minAge)
	rows, err := s.db.Query(ctx, `
		SELECT id, user_a, user_b, status, conversation_id, spark_target,
		       matched_at, first_message_at, last_message_at, expires_at, closed_by
		FROM dating_matches
		WHERE conversation_id IS NULL
		  AND status = 'matched'
		  AND matched_at < $1
		ORDER BY matched_at ASC
		LIMIT $2
	`, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("list saga pending matches: %w", err)
	}
	defer rows.Close()

	out := make([]*Match, 0, limit)
	for rows.Next() {
		m, err := scanMatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetMatch returns one match by id.
func (s *Store) GetMatch(ctx context.Context, id uuid.UUID) (*Match, error) {
	row := s.db.QueryRow(ctx, `SELECT `+matchSelectCols+` FROM dating_matches WHERE id = $1`, id)
	return scanMatch(row)
}

// GetMatchByUsers returns the pair's match (canonical order applied
// internally), preferring the open one, else the most recent, or
// ErrMatchNotFound.
func (s *Store) GetMatchByUsers(ctx context.Context, userA, userB uuid.UUID) (*Match, error) {
	a, b := canonicalPair(userA, userB)
	row := s.db.QueryRow(ctx, `
        SELECT `+matchSelectCols+`
        FROM dating_matches
        WHERE user_a = $1 AND user_b = $2
        ORDER BY (status IN `+openMatchStatuses+`) DESC, matched_at DESC
        LIMIT 1`, a, b)
	return scanMatch(row)
}

// ListMatchesForUser returns matches involving userID that the user may
// see, optionally filtered by a status bucket. Recognised status filters:
// 'all', 'active', 'quiet', 'sparks-waiting'. Anything else is treated as
// 'all'. A match is hidden when the pair is blocked either way or the other
// participant's profile is deleted, suspended or gone.
func (s *Store) ListMatchesForUser(ctx context.Context, userID uuid.UUID, status string) ([]*Match, error) {
	bucket := ""
	order := `COALESCE(m.last_message_at, m.matched_at) DESC`
	switch status {
	case "active":
		bucket = `AND m.status IN ('matched','conversing')`
	case "quiet":
		bucket = `AND m.status = 'quiet'`
	case "sparks-waiting":
		bucket = `AND m.status = 'matched' AND m.first_message_at IS NULL`
		order = `m.matched_at DESC`
	}
	rows, err := s.db.Query(ctx, `
        SELECT `+matchSelectCols+`
        FROM dating_matches m
        WHERE (m.user_a = $1 OR m.user_b = $1)
          `+bucket+`
          AND NOT `+blockedPairPredicate("m.user_a", "m.user_b")+`
          AND `+visibleProfilePredicate("CASE WHEN m.user_a = $1 THEN m.user_b ELSE m.user_a END")+`
        ORDER BY `+order+`
        LIMIT 200`, userID)
	if err != nil {
		return nil, fmt.Errorf("list matches: %w", err)
	}
	return collectMatches(rows)
}

// ListMatchesForExport returns every match involving userID, unfiltered,
// for the DPDP data exporter (the user's own record).
func (s *Store) ListMatchesForExport(ctx context.Context, userID uuid.UUID) ([]*Match, error) {
	rows, err := s.db.Query(ctx, `
        SELECT `+matchSelectCols+`
        FROM dating_matches
        WHERE user_a = $1 OR user_b = $1
        ORDER BY COALESCE(last_message_at, matched_at) DESC
        LIMIT 500`, userID)
	if err != nil {
		return nil, fmt.Errorf("list matches for export: %w", err)
	}
	return collectMatches(rows)
}

func collectMatches(rows pgx.Rows) ([]*Match, error) {
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

// Close reasons recorded in dating_matches.close_reason.
const (
	CloseReasonUnmatch   = "unmatch"
	CloseReasonBlock     = "block"
	CloseReasonDuplicate = "duplicate"
)

// SystemActorID is closed_by for closures no user asked for (the duplicate
// open-match cleanup).
var SystemActorID = uuid.Nil

// CloseMatch (unmatch) sets status='closed', records the actor and the
// time, and in the same transaction deletes both users' sparks toward each
// other, so re-matching needs two fresh sparks.
func (s *Store) CloseMatch(ctx context.Context, matchID, closedBy uuid.UUID) error {
	_, err := s.CloseMatchWithReason(ctx, matchID, closedBy, CloseReasonUnmatch, nil)
	return err
}

// CloseMatchWithReason is the match close path. It closes one open match
// (status, closed_by, closed_at, close_reason) and returns the closed row.
//
//   - unmatch: also deletes the pair's sparks both ways.
//   - duplicate: closes only while another open match for the same pair
//     remains (so the last open match is never closed) and keeps the sparks,
//     which belong to the match that stays open.
//
// beforeCommit, when non-nil, runs inside the transaction after the update;
// an error rolls the close back. The duplicate cleanup publishes
// dating.match.closed there, so a failed publish leaves the row open for the
// next boot instead of closing it silently.
func (s *Store) CloseMatchWithReason(ctx context.Context, matchID, closedBy uuid.UUID, reason string, beforeCommit func(*Match) error) (*Match, error) {
	guard := `status NOT IN ('closed','expired')`
	switch reason {
	case CloseReasonUnmatch:
	case CloseReasonDuplicate:
		guard = `status IN ` + openMatchStatuses + ` AND EXISTS (
            SELECT 1 FROM dating_matches o
            WHERE o.user_a = dating_matches.user_a AND o.user_b = dating_matches.user_b
              AND o.id <> dating_matches.id AND o.status IN ` + openMatchStatuses + `)`
	default:
		return nil, fmt.Errorf("invalid: unsupported close reason %q", reason)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin close match: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	m, err := scanMatch(tx.QueryRow(ctx, `
        UPDATE dating_matches
        SET status = 'closed', closed_by = $2, closed_at = now(), close_reason = $3
        WHERE id = $1 AND `+guard+`
        RETURNING `+matchSelectCols, matchID, closedBy, reason))
	if err != nil {
		if errors.Is(err, ErrMatchNotFound) {
			return nil, ErrMatchNotFound
		}
		return nil, fmt.Errorf("close match: %w", err)
	}
	if reason == CloseReasonUnmatch {
		if _, err := deleteSparksBetween(ctx, tx, m.UserA, m.UserB); err != nil {
			return nil, err
		}
	}
	if beforeCommit != nil {
		if err := beforeCommit(m); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit close match: %w", err)
	}
	return m, nil
}

// --- Duplicate open matches --------------------------------------------------

// OpenPairIndexName is the partial unique index allowing one open match per pair.
const OpenPairIndexName = "uq_dating_matches_open_pair"

// OpenPairIndexDDL creates OpenPairIndexName. database/setup.sql carries the
// same statement inside a guard that skips it while duplicates exist
// (TestOpenPairIndexDDLMatchesSetupSQL keeps the two identical).
const OpenPairIndexDDL = `CREATE UNIQUE INDEX IF NOT EXISTS uq_dating_matches_open_pair
            ON dating_matches(user_a, user_b)
            WHERE status IN ('matched','conversing','quiet')`

// DuplicateOpenMatchCounts describes pairs holding more than one open match.
// Groups is the number of such pairs; ExtraRows the open rows beyond one per
// pair (the rows the cleanup would close).
type DuplicateOpenMatchCounts struct {
	Groups    int
	ExtraRows int
}

// CountDuplicateOpenMatches counts duplicate open-match groups and extra rows.
// scripts/count-duplicate-matches.sql runs the same aggregate.
func (s *Store) CountDuplicateOpenMatches(ctx context.Context) (DuplicateOpenMatchCounts, error) {
	var c DuplicateOpenMatchCounts
	err := s.db.QueryRow(ctx, `
        SELECT COUNT(*)::int, COALESCE(SUM(open_count - 1), 0)::int
        FROM (
            SELECT COUNT(*) AS open_count
            FROM dating_matches
            WHERE status IN `+openMatchStatuses+`
            GROUP BY user_a, user_b
            HAVING COUNT(*) > 1
        ) dup`).Scan(&c.Groups, &c.ExtraRows)
	if err != nil {
		return DuplicateOpenMatchCounts{}, fmt.Errorf("count duplicate open matches: %w", err)
	}
	return c, nil
}

// ListDuplicateOpenMatchExtras returns up to limit open matches that are NOT
// the one kept for their pair. Keep rule: the match with a conversation, then
// the most recent activity (last_message_at, else matched_at), then the
// lowest id.
func (s *Store) ListDuplicateOpenMatchExtras(ctx context.Context, limit int) ([]*Match, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.db.Query(ctx, `
        WITH ranked AS (
            SELECT id,
                   row_number() OVER (
                       PARTITION BY user_a, user_b
                       ORDER BY (conversation_id IS NOT NULL) DESC,
                                COALESCE(last_message_at, matched_at) DESC,
                                id) AS rn
            FROM dating_matches
            WHERE status IN `+openMatchStatuses+`
        )
        SELECT `+matchSelectCols+`
        FROM dating_matches
        WHERE id IN (SELECT id FROM ranked WHERE rn > 1)
        ORDER BY user_a, user_b, id
        LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list duplicate open matches: %w", err)
	}
	return collectMatches(rows)
}

// OpenPairIndexExists reports whether OpenPairIndexName exists.
func (s *Store) OpenPairIndexExists(ctx context.Context) (bool, error) {
	var exists bool
	if err := s.db.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, OpenPairIndexName).Scan(&exists); err != nil {
		return false, fmt.Errorf("check open pair index: %w", err)
	}
	return exists, nil
}

// EnsureOpenPairIndex creates OpenPairIndexName when missing and reports
// whether this call created it. Fails (unique violation) if a duplicate
// open match exists, so callers close duplicates first.
func (s *Store) EnsureOpenPairIndex(ctx context.Context) (bool, error) {
	exists, err := s.OpenPairIndexExists(ctx)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}
	if _, err := s.db.Exec(ctx, OpenPairIndexDDL); err != nil {
		return false, fmt.Errorf("create %s: %w", OpenPairIndexName, err)
	}
	return true, nil
}

// openMatchDedupeLockKey is the advisory lock serialising the boot cleanup
// across replicas.
const openMatchDedupeLockKey int64 = 0x0da7_1d0b_0001

// LockOpenMatchDedupe takes a session advisory lock so only one replica runs
// the duplicate cleanup at a time. The returned func releases it.
func (s *Store) LockOpenMatchDedupe(ctx context.Context) (func(), error) {
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire dedupe lock connection: %w", err)
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, openMatchDedupeLockKey); err != nil {
		conn.Release()
		return nil, fmt.Errorf("take dedupe lock: %w", err)
	}
	return func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, openMatchDedupeLockKey)
		conn.Release()
	}, nil
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
        SET status = 'expired', closed_at = now()
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

// ListMatchesQuietSince24h returns matches in 'conversing' status whose
// last (= only, by definition of quiet) message landed more than 24h
// ago and that haven't already been flagged as quiet-notified. The
// Phase 1 sweeper fires one dating.match.quiet_notify per row.
//
// We use a Redis dedup key (dating:match_quiet_notified:{match_id})
// rather than persisting a column to keep this opportunistic — at
// worst the user gets the same nudge twice across a Redis flush.
func (s *Store) ListMatchesQuietSince24h(ctx context.Context, limit int) ([]*Match, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
        SELECT `+matchSelectCols+`
        FROM dating_matches
        WHERE status IN ('matched','conversing')
          AND first_message_at IS NOT NULL
          AND last_message_at IS NOT NULL
          AND first_message_at = last_message_at
          AND last_message_at < now() - INTERVAL '24 hours'
        ORDER BY last_message_at ASC
        LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list matches quiet since 24h: %w", err)
	}
	defer rows.Close()
	out := make([]*Match, 0, limit)
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

// ClaimMatchesForQuietNotify atomically claims up to `limit` matches
// that have transitioned to 'quiet' but have not yet had a
// dating.match.quiet_notify event emitted (quiet_notified_at IS NULL).
// Stamps quiet_notified_at = NOW() on the claimed rows so a replica
// re-running on the next tick sees them as already-notified.
//
// FOR UPDATE SKIP LOCKED keeps the sweeper safe across replicas — each
// quiet match is emitted exactly once per cluster. §Phase 1 follow-up.
func (s *Store) ClaimMatchesForQuietNotify(ctx context.Context, limit int) ([]*Match, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.Query(ctx, `
        UPDATE dating_matches
        SET quiet_notified_at = NOW()
        WHERE id IN (
            SELECT id FROM dating_matches
            WHERE status = 'quiet'
              AND quiet_notified_at IS NULL
            ORDER BY last_message_at ASC NULLS LAST
            LIMIT $1
            FOR UPDATE SKIP LOCKED
        )
        RETURNING `+matchSelectCols, limit)
	if err != nil {
		return nil, fmt.Errorf("claim matches for quiet notify: %w", err)
	}
	defer rows.Close()
	out := make([]*Match, 0, limit)
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

// IsPremium reports whether the user holds an unexpired pass. Lane P2: a NULL
// expires_at is never "forever" (the column is NOT NULL and this compares it).
func (s *Store) IsPremium(ctx context.Context, userID uuid.UUID) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx, `
        SELECT EXISTS (
            SELECT 1 FROM dating_premium_subscriptions
            WHERE user_id = $1
              AND expires_at > now()
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
