// Sparks store — spec §10 dating_sparks. A Spark is a typed, targeted
// interest signal aimed at one item on the recipient's profile (photo,
// prompt, tune-axis, or echo). Targeted-only per DECISIONS.md D2.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Spark is one row in dating_sparks.
type Spark struct {
	ID         uuid.UUID `json:"id"`
	FromUserID uuid.UUID `json:"from_user_id"`
	ToUserID   uuid.UUID `json:"to_user_id"`
	TargetKind string    `json:"target_kind"`
	TargetRef  string    `json:"target_ref"`
	Note       *string   `json:"note,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	// DeclinedAt is set when the recipient declined. Never serialised: the
	// sender must not learn of a decline.
	DeclinedAt *time.Time `json:"-"`
}

// ErrSparkNotFound is returned when a spark id does not exist.
var ErrSparkNotFound = errors.New("not_found: spark not found")

// ErrSparkRateLimited is returned when the sender has used their spark
// allowance for the rolling window.
var ErrSparkRateLimited = errors.New("rate_limited: spark limit reached")

// SparkQuotaWindow is the rolling window the spark limit counts over.
const SparkQuotaWindow = 24 * time.Hour

// DeclineCooldown is the default time a decline keeps its sender away from
// the decliner: the sender cannot spark them, does not see them in the deck,
// and their sparks do not count toward a mutual match. The recipient is
// unaffected. Overridable with DATING_DECLINE_COOLDOWN_DAYS (1-365).
const DeclineCooldown = 30 * 24 * time.Hour

// SetDeclineCooldown overrides the decline cooldown; d <= 0 keeps the current
// value. Boot validates the environment value before calling it.
func (s *Store) SetDeclineCooldown(d time.Duration) {
	if d > 0 {
		s.declineCooldown = d
	}
}

// DeclineCooldownDuration returns the cooldown in effect.
func (s *Store) DeclineCooldownDuration() time.Duration {
	if s.declineCooldown <= 0 {
		return DeclineCooldown
	}
	return s.declineCooldown
}

func (s *Store) declineCutoff() time.Time {
	return time.Now().Add(-s.DeclineCooldownDuration())
}

// recentDeclinePredicate is true when `recipient` declined a spark from
// `sender` after the cutoff expression and has not lifted that decline since.
// It is the one decline-cooldown rule: spark create (HasRecentDecline), the
// deck (FetchCandidates) and mutual-match counting (HasReverseSparks) all use
// it. Uses idx_dating_sparks_declined_pair.
func recentDeclinePredicate(sender, recipient, cutoff string) string {
	return `EXISTS (SELECT 1 FROM dating_sparks dcl
	    WHERE dcl.from_user_id = ` + sender + ` AND dcl.to_user_id = ` + recipient + `
	      AND dcl.declined_at IS NOT NULL AND dcl.declined_at > ` + cutoff + `
	      AND NOT ` + declineLiftedPredicate("dcl", sender, recipient) + `)`
}

// declineLiftedPredicate is true when `recipient` (the decliner) has a spark
// toward `sender` created after the decline row `dcl` was declined: sparking
// the person you declined lifts the cooldown. Computed from the sparks
// themselves, so there is no flag to drift. A spark made before the decline,
// or a repeat of one (its created_at is kept), does not lift it; revoking
// the lifting spark, with no other later spark, restores the cooldown.
func declineLiftedPredicate(dcl, sender, recipient string) string {
	return `EXISTS (SELECT 1 FROM dating_sparks lift
	    WHERE lift.from_user_id = ` + recipient + ` AND lift.to_user_id = ` + sender + `
	      AND lift.created_at > ` + dcl + `.declined_at)`
}

// HasRecentDecline reports whether recipientID declined any spark from
// senderID within the decline cooldown and has not lifted it by sparking
// senderID since.
func (s *Store) HasRecentDecline(ctx context.Context, senderID, recipientID uuid.UUID) (bool, error) {
	var declined bool
	err := s.db.QueryRow(ctx, `SELECT `+recentDeclinePredicate("$1::uuid", "$2::uuid", "$3::timestamptz"),
		senderID, recipientID, s.declineCutoff()).Scan(&declined)
	if err != nil {
		return false, fmt.Errorf("check recent decline: %w", err)
	}
	return declined, nil
}

const sparkSelectCols = `id, from_user_id, to_user_id, target_kind, target_ref, note, created_at, declined_at`

// rowQuerier is satisfied by both the pool and a transaction.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func scanSpark(row pgx.Row) (*Spark, error) {
	s := &Spark{}
	if err := row.Scan(&s.ID, &s.FromUserID, &s.ToUserID, &s.TargetKind, &s.TargetRef, &s.Note, &s.CreatedAt, &s.DeclinedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSparkNotFound
		}
		return nil, fmt.Errorf("scan spark: %w", err)
	}
	return s, nil
}

// pairLastClosedSQL is the time the pair's most recent match was closed or
// expired (matched_at for legacy rows without closed_at), or -infinity. A
// spark only counts toward a new match when it is newer than this.
func pairLastClosedSQL(x, y string) string {
	return `COALESCE((SELECT max(COALESCE(pm.closed_at, pm.matched_at)) FROM dating_matches pm
        WHERE pm.user_a = LEAST(` + x + `, ` + y + `)
          AND pm.user_b = GREATEST(` + x + `, ` + y + `)
          AND pm.status IN ('closed','expired')), '-infinity'::timestamptz)`
}

func validateSparkInput(fromUserID, toUserID uuid.UUID, targetKind, targetRef string) error {
	if fromUserID == uuid.Nil || toUserID == uuid.Nil {
		return fmt.Errorf("invalid: user ids required")
	}
	if fromUserID == toUserID {
		return fmt.Errorf("invalid: cannot spark yourself")
	}
	if targetKind == "" || targetRef == "" {
		return fmt.Errorf("invalid: target_kind and target_ref required")
	}
	switch targetKind {
	case "photo", "prompt", "tune_axis", "echo":
	default:
		return fmt.Errorf("invalid: unsupported target_kind %q", targetKind)
	}
	return nil
}

// upsertSpark writes the spark. The UNIQUE constraint (from_user_id,
// to_user_id, target_kind, target_ref) makes a repeat fall through to the
// existing row; a row older than the pair's last closure is renewed (fresh
// created_at, decline cleared) so it counts as new interest again.
func upsertSpark(ctx context.Context, q rowQuerier, fromUserID, toUserID uuid.UUID, targetKind, targetRef, note string) (*Spark, error) {
	var notePtr *string
	if note != "" {
		notePtr = &note
	}
	stale := `dating_sparks.created_at <= ` + pairLastClosedSQL("$1::uuid", "$2::uuid")
	row := q.QueryRow(ctx, `
        INSERT INTO dating_sparks (from_user_id, to_user_id, target_kind, target_ref, note)
        VALUES ($1, $2, $3, $4, $5)
        ON CONFLICT (from_user_id, to_user_id, target_kind, target_ref) DO UPDATE
            SET note        = COALESCE(EXCLUDED.note, dating_sparks.note),
                declined_at = CASE WHEN `+stale+` THEN NULL ELSE dating_sparks.declined_at END,
                created_at  = CASE WHEN `+stale+` THEN now() ELSE dating_sparks.created_at END
        RETURNING `+sparkSelectCols, fromUserID, toUserID, targetKind, targetRef, notePtr)
	return scanSpark(row)
}

// CreateSpark inserts a new dating_sparks row without the spark limit.
// Idempotent on (from_user_id, to_user_id, target_kind, target_ref).
func (s *Store) CreateSpark(ctx context.Context, fromUserID, toUserID uuid.UUID, targetKind, targetRef, note string) (*Spark, error) {
	if err := validateSparkInput(fromUserID, toUserID, targetKind, targetRef); err != nil {
		return nil, err
	}
	return upsertSpark(ctx, s.db, fromUserID, toUserID, targetKind, targetRef, note)
}

// CreateSparkWithQuota creates the spark only when the sender has fewer than
// `limit` new sparks in the last SparkQuotaWindow (ErrSparkRateLimited
// otherwise). Repeating an existing spark is free. Each new spark writes a
// dating_spark_ledger row, which revoke/unmatch/block never delete, so the
// allowance cannot be reset. A per-sender advisory lock serialises the
// count-then-insert. limit <= 0 disables the check.
func (s *Store) CreateSparkWithQuota(ctx context.Context, fromUserID, toUserID uuid.UUID, targetKind, targetRef, note string, limit int) (*Spark, error) {
	if err := validateSparkInput(fromUserID, toUserID, targetKind, targetRef); err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin spark: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 7021))`, fromUserID.String()); err != nil {
		return nil, fmt.Errorf("lock spark quota: %w", err)
	}
	var exists bool
	if err := tx.QueryRow(ctx, `
        SELECT EXISTS (SELECT 1 FROM dating_sparks
            WHERE from_user_id = $1 AND to_user_id = $2 AND target_kind = $3 AND target_ref = $4)`,
		fromUserID, toUserID, targetKind, targetRef).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check existing spark: %w", err)
	}
	if !exists && limit > 0 {
		window := time.Now().Add(-SparkQuotaWindow)
		if _, err := tx.Exec(ctx, `
            DELETE FROM dating_spark_ledger WHERE from_user_id = $1 AND sent_at <= $2`, fromUserID, window); err != nil {
			return nil, fmt.Errorf("trim spark ledger: %w", err)
		}
		var used int
		if err := tx.QueryRow(ctx, `
            SELECT COUNT(*)::int FROM dating_spark_ledger WHERE from_user_id = $1 AND sent_at > $2`,
			fromUserID, window).Scan(&used); err != nil {
			return nil, fmt.Errorf("count spark ledger: %w", err)
		}
		if used >= limit {
			return nil, ErrSparkRateLimited
		}
		if _, err := tx.Exec(ctx, `INSERT INTO dating_spark_ledger (from_user_id) VALUES ($1)`, fromUserID); err != nil {
			return nil, fmt.Errorf("record spark ledger: %w", err)
		}
	}
	sp, err := upsertSpark(ctx, tx, fromUserID, toUserID, targetKind, targetRef, note)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit spark: %w", err)
	}
	return sp, nil
}

// GetSpark returns a single spark by id.
func (s *Store) GetSpark(ctx context.Context, id uuid.UUID) (*Spark, error) {
	row := s.db.QueryRow(ctx, `SELECT `+sparkSelectCols+` FROM dating_sparks WHERE id = $1`, id)
	return scanSpark(row)
}

// ListIncomingSparks returns sparks aimed at userID (newest first) that the
// recipient may see: not declined, the pair not blocked either way, and the
// sender's profile neither deleted nor suspended.
func (s *Store) ListIncomingSparks(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*Spark, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.Query(ctx, `
        SELECT `+sparkSelectCols+`
        FROM dating_sparks sp
        WHERE sp.to_user_id = $1
          AND sp.declined_at IS NULL
          AND NOT `+blockedPairPredicate("sp.from_user_id", "sp.to_user_id")+`
          AND `+visibleProfilePredicate("sp.from_user_id")+`
        ORDER BY sp.created_at DESC
        LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list incoming sparks: %w", err)
	}
	return collectSparks(rows, limit)
}

func collectSparks(rows pgx.Rows, capacity int) ([]*Spark, error) {
	defer rows.Close()
	out := make([]*Spark, 0, capacity)
	for rows.Next() {
		sp, err := scanSpark(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// ListSparksSent returns sparks sent BY userID (newest first). Used by the
// DPDP data exporter.
func (s *Store) ListSparksSent(ctx context.Context, userID uuid.UUID) ([]*Spark, error) {
	rows, err := s.db.Query(ctx, `
        SELECT `+sparkSelectCols+`
        FROM dating_sparks
        WHERE from_user_id = $1
        ORDER BY created_at DESC
        LIMIT 500`, userID)
	if err != nil {
		return nil, fmt.Errorf("list sparks sent: %w", err)
	}
	return collectSparks(rows, 16)
}

// ListSparksReceived returns every spark aimed at userID for the DPDP data
// exporter: the user's own record, so no visibility filter applies.
func (s *Store) ListSparksReceived(ctx context.Context, userID uuid.UUID) ([]*Spark, error) {
	rows, err := s.db.Query(ctx, `
        SELECT `+sparkSelectCols+`
        FROM dating_sparks
        WHERE to_user_id = $1
        ORDER BY created_at DESC
        LIMIT 500`, userID)
	if err != nil {
		return nil, fmt.Errorf("list sparks received: %w", err)
	}
	return collectSparks(rows, 16)
}

// DeleteSpark removes a spark only when fromUserID matches the owner.
// Returns ErrSparkNotFound when no row matches.
func (s *Store) DeleteSpark(ctx context.Context, id, fromUserID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
        DELETE FROM dating_sparks
        WHERE id = $1 AND from_user_id = $2`, id, fromUserID)
	if err != nil {
		return fmt.Errorf("delete spark: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSparkNotFound
	}
	return nil
}

// DeclineSpark marks a spark declined by its recipient. Idempotent: a repeat
// keeps the first declined_at. Any caller other than the recipient gets
// ErrSparkNotFound, so a sender cannot probe the state.
func (s *Store) DeclineSpark(ctx context.Context, id, recipientID uuid.UUID) (*Spark, error) {
	row := s.db.QueryRow(ctx, `
        UPDATE dating_sparks
        SET declined_at = COALESCE(declined_at, now())
        WHERE id = $1 AND to_user_id = $2
        RETURNING `+sparkSelectCols, id, recipientID)
	return scanSpark(row)
}

// HasReverseSparks reports true if user b has Sparked user a with interest
// that still counts: not declined, created after the pair's last match
// closure or expiry, and a has no unlifted decline of b's sparks within the
// decline cooldown. A declined spark never counts, even once a lifts the
// decline by sparking b. Used by the spark service to detect mutual interest.
func (s *Store) HasReverseSparks(ctx context.Context, a, b uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
        SELECT EXISTS (
            SELECT 1 FROM dating_sparks sp
            WHERE sp.from_user_id = $2 AND sp.to_user_id = $1
              AND sp.declined_at IS NULL
              AND sp.created_at > `+pairLastClosedSQL("$1::uuid", "$2::uuid")+`
        ) AND NOT `+recentDeclinePredicate("$2::uuid", "$1::uuid", "$3::timestamptz"),
		a, b, s.declineCutoff()).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("has reverse sparks: %w", err)
	}
	return exists, nil
}

// deleteSparksBetween removes every spark between the two users, both
// directions, inside tx. Unmatch and block both call it.
func deleteSparksBetween(ctx context.Context, tx pgx.Tx, x, y uuid.UUID) (int64, error) {
	tag, err := tx.Exec(ctx, `
        DELETE FROM dating_sparks
        WHERE (from_user_id = $1 AND to_user_id = $2)
           OR (from_user_id = $2 AND to_user_id = $1)`, x, y)
	if err != nil {
		return 0, fmt.Errorf("delete sparks between pair: %w", err)
	}
	return tag.RowsAffected(), nil
}
