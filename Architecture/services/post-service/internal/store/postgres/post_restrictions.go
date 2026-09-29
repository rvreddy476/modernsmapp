package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Case-specific post restrictions (Copyright Match plan, sections 6.2 and
// 9.2; migration 056).
//
// A restriction is the hold ONE case places on ONE post. trust-safety
// places and releases it through a signed command; post-service records
// the row, recounts posts.active_restriction_count and emits the
// eligibility change — and writes NOTHING else. In particular a command
// never touches review_status, visibility, distribution, age_restricted,
// deleted_at, publish_at, published_at, created_at or updated_at: those
// belong to other authorities, and the P-2 invariants (a release never
// undeletes, publishes or changes an audience) hold because this file has
// no statement that could.

var (
	// ErrRestrictionSourceNotEnabled: source=safety is reserved (403 SOURCE_NOT_ENABLED).
	ErrRestrictionSourceNotEnabled = errors.New("restriction source is not enabled")
	// ErrRestrictionDecisionConflict: the decision_id was already used with different claims (409 DECISION_CONFLICT).
	ErrRestrictionDecisionConflict = errors.New("restriction decision id was already used with different claims")
	// ErrRestrictionSubjectNotFound: no such post (404 SUBJECT_NOT_FOUND).
	ErrRestrictionSubjectNotFound = errors.New("restriction subject not found")
	// ErrRestrictionSubjectMismatch: the post exists but has another author (409 SUBJECT_MISMATCH).
	ErrRestrictionSubjectMismatch = errors.New("restriction subject author mismatch")
	// ErrRestrictionStaleRevision: case_revision does not exceed the row's (409 STALE_CASE_REVISION).
	ErrRestrictionStaleRevision = errors.New("restriction case revision is stale")
	// ErrRestrictionStateMismatch: expected_state differs from the row's state (409 STATE_MISMATCH).
	ErrRestrictionStateMismatch = errors.New("restriction state mismatch")
	// ErrRestrictionInvalidInput: the command failed the store's own shape check.
	ErrRestrictionInvalidInput = errors.New("invalid restriction command")
)

const (
	RestrictionActionPlaceHold   = "place_hold"
	RestrictionActionReleaseHold = "release_hold"
	RestrictionSourceCopyright   = "copyright"
	RestrictionSourceSafety      = "safety"
	RestrictionStateAbsent       = "absent"
	RestrictionStateActive       = "active"
	RestrictionStateReleased     = "released"
	restrictionIssuer            = "trust-safety-service"
	// restrictionLockSpace keeps the decision advisory locks apart from the
	// moderation authority's (707).
	restrictionLockSpace = 708
)

// RestrictionCommand is one verified place_hold / release_hold command.
// ClaimsDigest is moderationcap.RestrictionClaims.Digest() of the signed
// claims; it is what a replay is compared against.
type RestrictionCommand struct {
	Action          string
	Source          string
	CaseID          uuid.UUID
	CaseRevision    int64
	PostID          uuid.UUID
	SubjectAuthorID uuid.UUID
	ExpectedState   string
	DecisionID      uuid.UUID
	PolicyVersion   string
	ReasonCode      string
	ActorID         uuid.UUID
	ClaimsDigest    []byte
}

func (in RestrictionCommand) validate() error {
	if in.Action != RestrictionActionPlaceHold && in.Action != RestrictionActionReleaseHold {
		return fmt.Errorf("%w: action %q", ErrRestrictionInvalidInput, in.Action)
	}
	switch in.Source {
	case RestrictionSourceCopyright:
	case RestrictionSourceSafety:
		return ErrRestrictionSourceNotEnabled
	default:
		return fmt.Errorf("%w: source %q", ErrRestrictionInvalidInput, in.Source)
	}
	switch in.ExpectedState {
	case RestrictionStateAbsent, RestrictionStateActive, RestrictionStateReleased:
	default:
		return fmt.Errorf("%w: expected_state %q", ErrRestrictionInvalidInput, in.ExpectedState)
	}
	if in.CaseID == uuid.Nil || in.PostID == uuid.Nil || in.SubjectAuthorID == uuid.Nil ||
		in.DecisionID == uuid.Nil || in.ActorID == uuid.Nil || in.CaseRevision <= 0 ||
		strings.TrimSpace(in.PolicyVersion) == "" || strings.TrimSpace(in.ReasonCode) == "" ||
		len(in.ClaimsDigest) == 0 {
		return fmt.Errorf("%w: required field missing", ErrRestrictionInvalidInput)
	}
	return nil
}

// PostRestriction is one restriction row as trust-safety's reconciliation
// and the owner's Hub read it.
type PostRestriction struct {
	RestrictionID  uuid.UUID  `json:"restriction_id"`
	PostID         uuid.UUID  `json:"post_id"`
	Source         string     `json:"source"`
	CaseID         uuid.UUID  `json:"case_id"`
	Scope          string     `json:"scope"`
	State          string     `json:"state"`
	CaseRevision   int64      `json:"case_revision"`
	LastDecisionID uuid.UUID  `json:"last_decision_id"`
	PolicyVersion  string     `json:"policy_version"`
	ReasonCode     string     `json:"reason_code"`
	FirstPlacedAt  time.Time  `json:"first_placed_at"`
	PlacedAt       time.Time  `json:"placed_at"`
	ReleasedAt     *time.Time `json:"released_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// RestrictionOutcome is the command's answer (section 6.2, step 8).
type RestrictionOutcome struct {
	RestrictionID          uuid.UUID `json:"restriction_id"`
	PostID                 uuid.UUID `json:"post_id"`
	CaseID                 uuid.UUID `json:"case_id"`
	Source                 string    `json:"source"`
	State                  string    `json:"state"`
	CaseRevision           int64     `json:"case_revision"`
	ActiveRestrictionCount int       `json:"active_restriction_count"`
	EffectiveReviewStatus  string    `json:"effective_review_status"`
	// Changed is false when the command re-affirmed the state the row was
	// already in (expected_state named it); nothing was emitted then.
	Changed bool `json:"changed"`
	// Replayed is true when this decision_id had already been applied and
	// the stored outcome is being returned.
	Replayed bool `json:"replayed"`
}

const postRestrictionCols = `restriction_id, post_id, source, case_id, scope, state, case_revision,
	last_decision_id, policy_version, reason_code, first_placed_at, placed_at, released_at, updated_at`

func scanPostRestriction(row pgx.Row) (PostRestriction, error) {
	var r PostRestriction
	err := row.Scan(&r.RestrictionID, &r.PostID, &r.Source, &r.CaseID, &r.Scope, &r.State, &r.CaseRevision,
		&r.LastDecisionID, &r.PolicyVersion, &r.ReasonCode, &r.FirstPlacedAt, &r.PlacedAt, &r.ReleasedAt, &r.UpdatedAt)
	return r, err
}

// ApplyPostRestriction runs one command in one transaction (section 6.2,
// steps 2–8): decision lock and replay check, post lock, row lock,
// revision and state guards, the row change, the recount, the eligibility
// event, the audit event. Deleted and scheduled posts are accepted: the
// hold must outlive an author's restore or a schedule's publication.
func (s *Store) ApplyPostRestriction(ctx context.Context, in RestrictionCommand) (*RestrictionOutcome, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// 2. Serialise on the decision id, then replay or conflict.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, $2))`, in.DecisionID.String(), restrictionLockSpace); err != nil {
		return nil, err
	}
	if prior, err := replayRestrictionOutcomeTx(ctx, tx, in); err != nil {
		return nil, err
	} else if prior != nil {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return prior, nil
	}

	// 3. The post. No deleted_at / publish_at filter, on purpose.
	var authorID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT author_id FROM posts WHERE id = $1 FOR UPDATE`, in.PostID).Scan(&authorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRestrictionSubjectNotFound
	}
	if err != nil {
		return nil, err
	}
	if authorID != in.SubjectAuthorID {
		return nil, ErrRestrictionSubjectMismatch
	}

	// 4. This case's row, if any.
	var (
		restrictionID uuid.UUID
		current       = RestrictionStateAbsent
		rowRevision   int64
	)
	err = tx.QueryRow(ctx, `
		SELECT restriction_id, state, case_revision FROM post_restrictions
		WHERE post_id = $1 AND source = $2 AND case_id = $3 FOR UPDATE
	`, in.PostID, in.Source, in.CaseID).Scan(&restrictionID, &current, &rowRevision)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if current != RestrictionStateAbsent && in.CaseRevision <= rowRevision {
		return nil, ErrRestrictionStaleRevision
	}
	if in.ExpectedState != current {
		return nil, fmt.Errorf("%w: row is %s, command expected %s", ErrRestrictionStateMismatch, current, in.ExpectedState)
	}

	// 5. Apply to THIS row only.
	var next string
	switch in.Action {
	case RestrictionActionPlaceHold:
		next = RestrictionStateActive
	case RestrictionActionReleaseHold:
		if current == RestrictionStateAbsent {
			return nil, fmt.Errorf("%w: nothing to release", ErrRestrictionStateMismatch)
		}
		next = RestrictionStateReleased
	}
	changed := current != next
	if current == RestrictionStateAbsent {
		restrictionID = uuid.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO post_restrictions
				(restriction_id, post_id, source, case_id, issuer, scope, state, case_revision,
				 last_decision_id, policy_version, reason_code, first_placed_at, placed_at, released_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,'global','active',$6,$7,$8,$9,now(),now(),NULL,now())
		`, restrictionID, in.PostID, in.Source, in.CaseID, restrictionIssuer, in.CaseRevision,
			in.DecisionID, in.PolicyVersion, in.ReasonCode); err != nil {
			return nil, err
		}
	} else {
		if _, err := tx.Exec(ctx, `
			UPDATE post_restrictions
			SET state = $2, case_revision = $3, last_decision_id = $4, policy_version = $5, reason_code = $6,
			    placed_at = CASE WHEN $2 = 'active' AND state <> 'active' THEN now() ELSE placed_at END,
			    released_at = CASE WHEN $2 = 'released' THEN COALESCE(CASE WHEN state = 'released' THEN released_at END, now()) ELSE NULL END,
			    updated_at = now()
			WHERE restriction_id = $1
		`, restrictionID, next, in.CaseRevision, in.DecisionID, in.PolicyVersion, in.ReasonCode); err != nil {
			return nil, err
		}
	}

	// 5b. The count — a recount of the active rows, never ±1 — and the
	// eligibility projection. The ONLY statement in this file that writes
	// posts, and it names one column.
	var (
		count     int
		effective string
	)
	if changed {
		if err := tx.QueryRow(ctx, `
			UPDATE posts
			SET active_restriction_count = (SELECT COUNT(*) FROM post_restrictions WHERE post_id = $1 AND state = 'active')
			WHERE id = $1
			RETURNING active_restriction_count, effective_review_status
		`, in.PostID).Scan(&count, &effective); err != nil {
			return nil, err
		}
		if err := BumpSearchRevAndEmitTx(ctx, tx, in.PostID); err != nil {
			return nil, err
		}
	} else if err := tx.QueryRow(ctx, `SELECT active_restriction_count, effective_review_status FROM posts WHERE id = $1`, in.PostID).Scan(&count, &effective); err != nil {
		return nil, err
	}

	// 6. The audit event, carrying the outcome for replays.
	var prev *string
	if current != RestrictionStateAbsent {
		prev = &current
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO post_restriction_events
			(decision_id, restriction_id, post_id, action, case_revision, prev_state, new_state, actor_id,
			 policy_version, reason_code, claims_digest, changed, active_restriction_count_after, effective_review_status_after)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
	`, in.DecisionID, restrictionID, in.PostID, in.Action, in.CaseRevision, prev, next, in.ActorID,
		in.PolicyVersion, in.ReasonCode, in.ClaimsDigest, changed, count, effective); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &RestrictionOutcome{
		RestrictionID: restrictionID, PostID: in.PostID, CaseID: in.CaseID, Source: in.Source,
		State: next, CaseRevision: in.CaseRevision, ActiveRestrictionCount: count,
		EffectiveReviewStatus: effective, Changed: changed, Replayed: false,
	}, nil
}

// replayRestrictionOutcomeTx returns the stored outcome for a decision_id
// already applied with the same claims digest, nil when the id is new, and
// ErrRestrictionDecisionConflict when the id was used with other claims.
func replayRestrictionOutcomeTx(ctx context.Context, tx pgx.Tx, in RestrictionCommand) (*RestrictionOutcome, error) {
	var (
		digest []byte
		out    RestrictionOutcome
	)
	err := tx.QueryRow(ctx, `
		SELECT e.claims_digest, e.restriction_id, e.post_id, r.case_id, r.source, e.new_state, e.case_revision,
		       e.active_restriction_count_after, e.effective_review_status_after, e.changed
		FROM post_restriction_events e
		JOIN post_restrictions r ON r.restriction_id = e.restriction_id
		WHERE e.decision_id = $1
	`, in.DecisionID).Scan(&digest, &out.RestrictionID, &out.PostID, &out.CaseID, &out.Source, &out.State,
		&out.CaseRevision, &out.ActiveRestrictionCount, &out.EffectiveReviewStatus, &out.Changed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if string(digest) != string(in.ClaimsDigest) {
		return nil, ErrRestrictionDecisionConflict
	}
	out.Replayed = true
	return &out, nil
}

// RestrictionListFilter selects rows for trust-safety's reconciliation
// sweep (section 9.5) or a targeted read. Source is required. CaseIDs and
// PostID narrow; UpdatedAfter and Cursor page the incremental sweep.
type RestrictionListFilter struct {
	Source       string
	CaseIDs      []uuid.UUID
	PostID       *uuid.UUID
	UpdatedAfter *time.Time
	Cursor       string
	Limit        int
}

// MaxRestrictionListLimit caps one reconciliation page.
const MaxRestrictionListLimit = 500

// ListPostRestrictions pages restriction rows ordered by (updated_at,
// restriction_id), the shape idx_post_restrictions_sync serves. The cursor
// is the last row's "updated_at(RFC3339Nano)_restriction_id".
func (s *Store) ListPostRestrictions(ctx context.Context, f RestrictionListFilter) ([]PostRestriction, string, error) {
	if f.Source != RestrictionSourceCopyright && f.Source != RestrictionSourceSafety {
		return nil, "", fmt.Errorf("%w: source %q", ErrRestrictionInvalidInput, f.Source)
	}
	if f.Limit <= 0 || f.Limit > MaxRestrictionListLimit {
		f.Limit = 100
	}
	args := []any{f.Source, f.Limit + 1}
	where := `source = $1`
	if len(f.CaseIDs) > 0 {
		args = append(args, f.CaseIDs)
		where += fmt.Sprintf(` AND case_id = ANY($%d)`, len(args))
	}
	if f.PostID != nil {
		args = append(args, *f.PostID)
		where += fmt.Sprintf(` AND post_id = $%d`, len(args))
	}
	if f.UpdatedAfter != nil {
		args = append(args, *f.UpdatedAfter)
		where += fmt.Sprintf(` AND updated_at > $%d`, len(args))
	}
	if f.Cursor != "" {
		at, id, err := parseRestrictionCursor(f.Cursor)
		if err != nil {
			return nil, "", fmt.Errorf("%w: cursor", ErrRestrictionInvalidInput)
		}
		args = append(args, at, id)
		where += fmt.Sprintf(` AND (updated_at, restriction_id) > ($%d, $%d)`, len(args)-1, len(args))
	}
	rows, err := s.db.Query(ctx, `SELECT `+postRestrictionCols+` FROM post_restrictions WHERE `+where+
		` ORDER BY updated_at ASC, restriction_id ASC LIMIT $2`, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := make([]PostRestriction, 0, f.Limit)
	for rows.Next() {
		r, err := scanPostRestriction(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > f.Limit {
		out = out[:f.Limit]
		last := out[len(out)-1]
		next = last.UpdatedAt.UTC().Format(time.RFC3339Nano) + "_" + last.RestrictionID.String()
	}
	return out, next, nil
}

func parseRestrictionCursor(cursor string) (time.Time, uuid.UUID, error) {
	i := strings.LastIndex(cursor, "_")
	if i <= 0 {
		return time.Time{}, uuid.Nil, errors.New("malformed cursor")
	}
	at, err := time.Parse(time.RFC3339Nano, cursor[:i])
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	id, err := uuid.Parse(cursor[i+1:])
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	return at, id, nil
}

// ActiveRestrictionsByPost returns every ACTIVE restriction on the given
// posts, keyed by post, oldest placement first. The owner's Hub rows and
// the moderation subject read it; viewers never see restriction rows.
func (s *Store) ActiveRestrictionsByPost(ctx context.Context, postIDs []uuid.UUID) (map[uuid.UUID][]PostRestriction, error) {
	out := make(map[uuid.UUID][]PostRestriction, len(postIDs))
	if len(postIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `SELECT `+postRestrictionCols+` FROM post_restrictions
		WHERE post_id = ANY($1) AND state = 'active'
		ORDER BY first_placed_at ASC, restriction_id ASC`, postIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		r, err := scanPostRestriction(rows)
		if err != nil {
			return nil, err
		}
		out[r.PostID] = append(out[r.PostID], r)
	}
	return out, rows.Err()
}

// RepairRestrictionCounts is the nightly self-check (section 9.5): every
// post whose active_restriction_count disagrees with its active rows is
// re-locked, recounted and its eligibility re-emitted. Returns the post
// ids repaired so the caller can alert on a non-empty answer.
func (s *Store) RepairRestrictionCounts(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT p.id FROM posts p
		LEFT JOIN (SELECT post_id, COUNT(*) AS n FROM post_restrictions WHERE state = 'active' GROUP BY post_id) r
		  ON r.post_id = p.id
		WHERE p.active_restriction_count <> COALESCE(r.n, 0)`)
	if err != nil {
		return nil, err
	}
	var drifted []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		drifted = append(drifted, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var repaired []uuid.UUID
	for _, id := range drifted {
		if err := s.repairOneRestrictionCount(ctx, id); err != nil {
			return repaired, err
		}
		repaired = append(repaired, id)
	}
	return repaired, nil
}

func (s *Store) repairOneRestrictionCount(ctx context.Context, postID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT 1 FROM posts WHERE id = $1 FOR UPDATE`, postID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE posts
		SET active_restriction_count = (SELECT COUNT(*) FROM post_restrictions WHERE post_id = $1 AND state = 'active')
		WHERE id = $1
		  AND active_restriction_count <> (SELECT COUNT(*) FROM post_restrictions WHERE post_id = $1 AND state = 'active')
	`, postID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// Repaired by a concurrent command since the scan; nothing to emit.
		return tx.Commit(ctx)
	}
	if err := BumpSearchRevAndEmitTx(ctx, tx, postID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
