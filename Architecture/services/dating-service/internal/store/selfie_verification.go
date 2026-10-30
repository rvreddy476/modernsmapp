package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ── Selfie verification (Dating plan lane D5) ───────────────────────────────
//
// The client asks for a challenge ("blink_twice"), records a short video,
// uploads it to media-service and submits the video's media id with the
// challenge. media-service checks the blinks, that one face stays the same
// person, and compares that face with the approved primary photo; dating
// records only the outcome, the blink count, the rounded similarity and the
// provider. No face embedding is stored anywhere.
//
// Every write that issues a challenge or starts an attempt runs under a
// per-user advisory lock, so the attempt limit and the "already passed /
// awaiting review" checks cannot be raced by parallel submissions.

// SelfieInstructionBlinkTwice is the only liveness instruction issued.
const SelfieInstructionBlinkTwice = "blink_twice"

// Selfie statuses on dating_verifications.selfie_status.
const (
	SelfieStatusPending       = "pending"
	SelfieStatusPendingReview = "pending_review"
	SelfieStatusPassed        = "passed"
	SelfieStatusFailed        = "failed"
)

// Attempt outcomes on dating_selfie_attempts.outcome. An attempt is
// 'submitted' until decided; 'error' means no verdict (media, provider or an
// over-long video) and leaves dating_verifications untouched.
const (
	SelfieOutcomeSubmitted     = "submitted"
	SelfieOutcomePassed        = SelfieStatusPassed
	SelfieOutcomeFailed        = SelfieStatusFailed
	SelfieOutcomePendingReview = SelfieStatusPendingReview
	SelfieOutcomeError         = "error"
)

// SelfieAttemptWindow is the rolling window the attempt limit counts over.
const SelfieAttemptWindow = 24 * time.Hour

var (
	// ErrSelfieChallengeInvalid: the challenge is unknown, expired, already
	// used, or belongs to someone else. One error for all four.
	ErrSelfieChallengeInvalid = errors.New("selfie challenge is unknown, expired, already used or not yours")
	// ErrSelfieAttemptsExceeded: the user used the attempt limit in the window.
	ErrSelfieAttemptsExceeded = errors.New("selfie verification attempt limit reached")
	// ErrSelfieAlreadyPassed: a passed selfie is never re-scored.
	ErrSelfieAlreadyPassed = errors.New("selfie verification already passed")
	// ErrSelfieReviewPending: no new attempt while a moderator review is open.
	ErrSelfieReviewPending = errors.New("selfie verification is awaiting moderator review")
	// ErrSelfieNotPendingReview: an admin decision on a selfie not in review.
	ErrSelfieNotPendingReview = errors.New("selfie verification is not awaiting review")
)

// SelfieChallenge is one issued liveness challenge.
type SelfieChallenge struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	Instruction   string
	MaxDurationMs int
	ExpiresAt     time.Time
}

// SelfieAttemptStart is what BeginSelfieAttempt recorded.
type SelfieAttemptStart struct {
	AttemptID   uuid.UUID
	Instruction string
	// PriorVerdicts counts this user's earlier attempts that reached a
	// verdict (passed, failed or pending_review). Zero = first attempt.
	PriorVerdicts int
	// UsedInWindow counts attempts in the window including this one.
	UsedInWindow int
}

// SelfieDecision is the outcome FinishSelfieAttempt records.
type SelfieDecision struct {
	Outcome        string
	Similarity     *float64 // rounded 0-100; nil when there is no score
	Blinks         *int
	FramesAnalysed *int
	Provider       string
	Reason         string
	// ReferenceMediaID is the approved primary photo the video was compared
	// with; a pass is bound to it (audit M1, CheckSelfieBinding).
	ReferenceMediaID *uuid.UUID
}

func lockSelfieUser(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "dating:selfie:"+userID.String()); err != nil {
		return fmt.Errorf("selfie: lock user: %w", err)
	}
	return nil
}

// selfieStatusGate refuses when the user already passed or is in review.
func selfieStatusGate(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	var status *string
	err := tx.QueryRow(ctx, `SELECT selfie_status FROM dating_verifications WHERE user_id = $1`, userID).Scan(&status)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("selfie: read status: %w", err)
	}
	if status != nil {
		switch *status {
		case SelfieStatusPassed:
			return ErrSelfieAlreadyPassed
		case SelfieStatusPendingReview:
			return ErrSelfieReviewPending
		}
	}
	return nil
}

func countSelfieAttemptsInWindow(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (int, error) {
	var n int
	if err := tx.QueryRow(ctx, `
        SELECT COUNT(*)::int FROM dating_selfie_attempts
        WHERE user_id = $1 AND created_at > now() - make_interval(secs => $2)`,
		userID, SelfieAttemptWindow.Seconds()).Scan(&n); err != nil {
		return 0, fmt.Errorf("selfie: count attempts: %w", err)
	}
	return n, nil
}

// CreateSelfieChallenge issues a single-use challenge valid for ttl (database
// clock). It refuses with ErrSelfieAttemptsExceeded when the user already used
// maxAttempts attempts, or was issued maxIssued challenges, in the window; and
// with ErrSelfieAlreadyPassed / ErrSelfieReviewPending.
func (s *Store) CreateSelfieChallenge(ctx context.Context, userID uuid.UUID, instruction string, maxDurationMs int,
	ttl time.Duration, maxAttempts, maxIssued int) (*SelfieChallenge, error) {
	if userID == uuid.Nil || instruction == "" || maxDurationMs <= 0 || ttl <= 0 || maxAttempts <= 0 || maxIssued <= 0 {
		return nil, fmt.Errorf("invalid: selfie challenge arguments")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("selfie challenge: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockSelfieUser(ctx, tx, userID); err != nil {
		return nil, err
	}
	if err := selfieStatusGate(ctx, tx, userID); err != nil {
		return nil, err
	}
	used, err := countSelfieAttemptsInWindow(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	if used >= maxAttempts {
		return nil, ErrSelfieAttemptsExceeded
	}
	var issued int
	if err := tx.QueryRow(ctx, `
        SELECT COUNT(*)::int FROM dating_selfie_challenges
        WHERE user_id = $1 AND created_at > now() - make_interval(secs => $2)`,
		userID, SelfieAttemptWindow.Seconds()).Scan(&issued); err != nil {
		return nil, fmt.Errorf("selfie challenge: count issued: %w", err)
	}
	if issued >= maxIssued {
		return nil, ErrSelfieAttemptsExceeded
	}
	ch := &SelfieChallenge{}
	if err := tx.QueryRow(ctx, `
        INSERT INTO dating_selfie_challenges (user_id, instruction, max_duration_ms, expires_at)
        VALUES ($1, $2, $3, now() + make_interval(secs => $4))
        RETURNING id, user_id, instruction, max_duration_ms, expires_at`,
		userID, instruction, maxDurationMs, ttl.Seconds()).Scan(&ch.ID, &ch.UserID, &ch.Instruction, &ch.MaxDurationMs, &ch.ExpiresAt); err != nil {
		return nil, fmt.Errorf("selfie challenge: insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("selfie challenge: commit: %w", err)
	}
	return ch, nil
}

// BeginSelfieAttempt, in one transaction under the per-user lock: refuses a
// user who already passed or is in review, refuses when maxAttempts are used
// in the window (the attempt limit), consumes the challenge (the caller's,
// unexpired, unused) and records the attempt. The attempt counts toward the
// limit whatever happens after, so every consumed challenge — and every
// provider call — is limited. attemptID is the new row's id (uuid.Nil: the
// database chooses); the service chooses it so the video can be claimed for
// the attempt before the row exists.
func (s *Store) BeginSelfieAttempt(ctx context.Context, attemptID, userID, challengeID, videoMediaID uuid.UUID, maxAttempts int) (*SelfieAttemptStart, error) {
	if userID == uuid.Nil || videoMediaID == uuid.Nil || maxAttempts <= 0 {
		return nil, fmt.Errorf("invalid: selfie attempt arguments")
	}
	if challengeID == uuid.Nil {
		return nil, ErrSelfieChallengeInvalid
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("selfie attempt: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockSelfieUser(ctx, tx, userID); err != nil {
		return nil, err
	}
	if err := selfieStatusGate(ctx, tx, userID); err != nil {
		return nil, err
	}
	usedSoFar, err := countSelfieAttemptsInWindow(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	if usedSoFar >= maxAttempts {
		return nil, ErrSelfieAttemptsExceeded
	}
	out := &SelfieAttemptStart{UsedInWindow: usedSoFar + 1}
	err = tx.QueryRow(ctx, `
        UPDATE dating_selfie_challenges
        SET used_at = now()
        WHERE id = $1 AND user_id = $2 AND used_at IS NULL AND expires_at > now()
        RETURNING instruction`, challengeID, userID).Scan(&out.Instruction)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSelfieChallengeInvalid
		}
		return nil, fmt.Errorf("selfie attempt: consume challenge: %w", err)
	}
	if err := tx.QueryRow(ctx, `
        SELECT COUNT(*)::int FROM dating_selfie_attempts
        WHERE user_id = $1 AND outcome IN ('passed','failed','pending_review')`, userID).Scan(&out.PriorVerdicts); err != nil {
		return nil, fmt.Errorf("selfie attempt: count verdicts: %w", err)
	}
	if err := tx.QueryRow(ctx, `
        INSERT INTO dating_selfie_attempts (id, user_id, challenge_id, video_media_id, instruction)
        VALUES (COALESCE($5::uuid, gen_random_uuid()), $1, $2, $3, $4)
        RETURNING id`, userID, challengeID, videoMediaID, out.Instruction, optionalID(attemptID)).Scan(&out.AttemptID); err != nil {
		return nil, fmt.Errorf("selfie attempt: insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("selfie attempt: commit: %w", err)
	}
	return out, nil
}

// FinishSelfieAttempt records the decision on the attempt and, for a verdict,
// on dating_verifications — in one transaction. A passed selfie is never
// overwritten. It does not touch profile_status: a pass reaches 'active' only
// through TransitionProfileStatus.
func (s *Store) FinishSelfieAttempt(ctx context.Context, userID, attemptID, videoMediaID uuid.UUID, d SelfieDecision) error {
	switch d.Outcome {
	case SelfieOutcomePassed, SelfieOutcomeFailed, SelfieOutcomePendingReview, SelfieOutcomeError:
	default:
		return fmt.Errorf("invalid: selfie outcome %q", d.Outcome)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("selfie finish: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
        UPDATE dating_selfie_attempts
        SET outcome = $3, similarity = $4, blinks_detected = $5, frames_analysed = $6,
            provider = NULLIF($7, ''), reason = NULLIF($8, ''), decided_at = now()
        WHERE id = $1 AND user_id = $2 AND outcome = 'submitted'`,
		attemptID, userID, d.Outcome, d.Similarity, d.Blinks, d.FramesAnalysed, d.Provider, d.Reason)
	if err != nil {
		return fmt.Errorf("selfie finish: attempt: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("conflict: selfie attempt %s is not open", attemptID)
	}
	if d.Outcome != SelfieOutcomeError {
		var reviewReason *string
		if d.Outcome == SelfieOutcomePendingReview && d.Reason != "" {
			reviewReason = &d.Reason
		}
		if _, err := tx.Exec(ctx, `
            INSERT INTO dating_verifications
                (user_id, selfie_status, selfie_score, selfie_at, selfie_provider, selfie_media_id, selfie_blinks, selfie_review_reason,
                 selfie_reference_media_id)
            VALUES ($1, $2, $3, now(), NULLIF($4, ''), $5, $6, $7, $8)
            ON CONFLICT (user_id) DO UPDATE
                SET selfie_status        = EXCLUDED.selfie_status,
                    selfie_score         = EXCLUDED.selfie_score,
                    selfie_at            = EXCLUDED.selfie_at,
                    selfie_provider      = EXCLUDED.selfie_provider,
                    selfie_media_id      = EXCLUDED.selfie_media_id,
                    selfie_blinks        = EXCLUDED.selfie_blinks,
                    selfie_review_reason = EXCLUDED.selfie_review_reason,
                    selfie_reviewed_by   = NULL,
                    selfie_reviewed_at   = NULL,
                    selfie_reference_media_id = EXCLUDED.selfie_reference_media_id,
                    selfie_rebind_media_id    = NULL,
                    selfie_rebind_status      = NULL
                WHERE dating_verifications.selfie_status IS DISTINCT FROM 'passed'`,
			userID, d.Outcome, d.Similarity, d.Provider, videoMediaID, d.Blinks, reviewReason, d.ReferenceMediaID); err != nil {
			return fmt.Errorf("selfie finish: verification: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// SelfieReview is one row of the moderator selfie review queue.
type SelfieReview struct {
	UserID              uuid.UUID  `json:"user_id"`
	ProfileStatus       string     `json:"profile_status"`
	Similarity          *float64   `json:"similarity,omitempty"`
	Blinks              *int       `json:"blinks_detected,omitempty"`
	Provider            *string    `json:"provider,omitempty"`
	Reason              *string    `json:"reason,omitempty"`
	Instruction         *string    `json:"instruction,omitempty"`
	SelfieVideoMediaID  *uuid.UUID `json:"selfie_video_media_id,omitempty"`
	PrimaryPhotoMediaID *uuid.UUID `json:"primary_photo_media_id,omitempty"`
	SubmittedAt         *time.Time `json:"submitted_at,omitempty"`
}

// ListPendingSelfieReviews returns selfies awaiting a moderator, oldest first.
func (s *Store) ListPendingSelfieReviews(ctx context.Context, limit int) ([]*SelfieReview, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
        SELECT v.user_id, COALESCE(p.profile_status, ''), v.selfie_score, v.selfie_blinks, v.selfie_provider,
               CASE WHEN v.selfie_rebind_status = 'pending' THEN '`+SelfieReasonPrimaryPhotoChanged+`'
                    ELSE v.selfie_review_reason END,
               (SELECT a.instruction FROM dating_selfie_attempts a
                 WHERE a.user_id = v.user_id AND a.outcome = 'pending_review'
                 ORDER BY a.created_at DESC LIMIT 1),
               v.selfie_media_id,
               (SELECT ph.media_id FROM dating_photos ph
                 WHERE ph.user_id = v.user_id AND ph.is_primary
                 ORDER BY ph.created_at DESC LIMIT 1),
               v.selfie_at
        FROM dating_verifications v
        LEFT JOIN dating_profiles p ON p.user_id = v.user_id
        WHERE v.selfie_status = 'pending_review'
           OR (v.selfie_status = 'passed' AND v.selfie_rebind_status = 'pending')
        ORDER BY v.selfie_at ASC NULLS LAST
        LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list selfie reviews: %w", err)
	}
	defer rows.Close()
	out := make([]*SelfieReview, 0, limit)
	for rows.Next() {
		r := &SelfieReview{}
		if err := rows.Scan(&r.UserID, &r.ProfileStatus, &r.Similarity, &r.Blinks, &r.Provider, &r.Reason,
			&r.Instruction, &r.SelfieVideoMediaID, &r.PrimaryPhotoMediaID, &r.SubmittedAt); err != nil {
			return nil, fmt.Errorf("scan selfie review: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ResolveSelfieReview applies a moderator decision to a selfie in review:
// approve → passed, reject → failed. ErrSelfieNotPendingReview when the
// selfie is not in review.
func (s *Store) ResolveSelfieReview(ctx context.Context, userID, adminID uuid.UUID, approve bool) error {
	if userID == uuid.Nil || adminID == uuid.Nil {
		return fmt.Errorf("invalid: user_id and admin id required")
	}
	status, decision := SelfieStatusFailed, "rejected"
	if approve {
		status, decision = SelfieStatusPassed, "approved"
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("selfie review: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockSelfieUser(ctx, tx, userID); err != nil {
		return err
	}
	// Audit M1: a pass bound to the primary photo the moderator saw.
	tag, err := tx.Exec(ctx, `
        UPDATE dating_verifications
        SET selfie_status = $3, selfie_at = now(), selfie_reviewed_by = $2, selfie_reviewed_at = now(),
            selfie_reference_media_id = CASE WHEN $3 = 'passed' THEN `+approvedPrimaryMediaSQL("$1")+` ELSE NULL END
        WHERE user_id = $1 AND selfie_status = 'pending_review'`, userID, adminID, status)
	if err != nil {
		return fmt.Errorf("selfie review: verification: %w", err)
	}
	if tag.RowsAffected() != 1 {
		// Audit M1: or a re-review after the primary photo changed. The
		// liveness pass stands either way (selfie_status stays 'passed');
		// approve binds it to the current primary, reject keeps the badge
		// off until the primary changes again.
		rebind := `selfie_rebind_status = 'rejected'`
		if approve {
			rebind = `selfie_reference_media_id = ` + approvedPrimaryMediaSQL("$1") + `,
                selfie_rebind_media_id = NULL, selfie_rebind_status = NULL`
		}
		tag, err = tx.Exec(ctx, `
            UPDATE dating_verifications
            SET selfie_reviewed_by = $2, selfie_reviewed_at = now(), `+rebind+`
            WHERE user_id = $1 AND selfie_status = 'passed' AND selfie_rebind_status = 'pending'`, userID, adminID)
		if err != nil {
			return fmt.Errorf("selfie review: rebind: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return ErrSelfieNotPendingReview
		}
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `
        UPDATE dating_selfie_attempts
        SET review_decision = $3, reviewed_by = $2, reviewed_at = now()
        WHERE id = (SELECT id FROM dating_selfie_attempts
                    WHERE user_id = $1 AND outcome = 'pending_review' AND reviewed_at IS NULL
                    ORDER BY created_at DESC LIMIT 1)`, userID, adminID, decision); err != nil {
		return fmt.Errorf("selfie review: attempt: %w", err)
	}
	return tx.Commit(ctx)
}

// CountSelfieAttemptsInWindow returns how many selfie attempts the user made
// inside SelfieAttemptWindow — the number the attempt limit counts. Read-only
// (no lock): the status route reports it, the limit itself is still enforced
// under the per-user lock inside BeginSelfieAttempt.
func (s *Store) CountSelfieAttemptsInWindow(ctx context.Context, userID uuid.UUID) (int, error) {
	var n int
	if err := s.db.QueryRow(ctx, `
        SELECT COUNT(*)::int FROM dating_selfie_attempts
        WHERE user_id = $1 AND created_at > now() - make_interval(secs => $2)`,
		userID, SelfieAttemptWindow.Seconds()).Scan(&n); err != nil {
		return 0, fmt.Errorf("selfie: count attempts in window: %w", err)
	}
	return n, nil
}

// SelfieReasonPrimaryPhotoChanged is the review-queue reason for a passed
// selfie whose approved primary photo changed since (audit M1).
const SelfieReasonPrimaryPhotoChanged = "PRIMARY_PHOTO_CHANGED"

// Selfie binding states (dating_verifications.selfie_rebind_status).
const (
	SelfieRebindPending  = "pending"
	SelfieRebindRejected = "rejected"
	// SelfieRebindAwaitingPhoto: no approved primary photo right now; the
	// badge is off and the re-review starts once one is approved.
	SelfieRebindAwaitingPhoto = "awaiting_photo"
)

// approvedPrimaryMediaSQL is the media id of the user's approved primary
// photo (NULL when there is none); user is a SQL uuid expression.
func approvedPrimaryMediaSQL(user string) string {
	return `(SELECT ph.media_id FROM dating_photos ph
                WHERE ph.user_id = ` + user + ` AND ph.is_primary AND ph.moderation_status = 'approved'
                ORDER BY ph.created_at DESC LIMIT 1)`
}

// SelfieBinding is what CheckSelfieBinding did.
type SelfieBinding string

const (
	// SelfieBindingNone: no passed selfie, or nothing changed.
	SelfieBindingNone SelfieBinding = ""
	// SelfieBindingAdopted: a pass from before binding existed was bound to
	// the current primary photo (first sight).
	SelfieBindingAdopted SelfieBinding = "adopted"
	// SelfieBindingWithdrawn: the primary photo changed; the badge is off.
	SelfieBindingWithdrawn SelfieBinding = "withdrawn"
	// SelfieBindingRestored: the primary is the bound photo again.
	SelfieBindingRestored SelfieBinding = "restored"
)

// selfieBindingStep is the pure decision CheckSelfieBinding applies, given
// the bound photo (reference), the current approved primary (current, nil
// when none), the photo a pending/rejected re-review is about (rebindMedia)
// and its status. It returns the new reference, rebind media and rebind
// status, and the outcome.
func selfieBindingStep(reference, current, rebindMedia *uuid.UUID, rebindStatus string) (newRef, newRebindMedia *uuid.UUID, newStatus string, out SelfieBinding) {
	same := func(a, b *uuid.UUID) bool { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }
	switch {
	case reference == nil && current == nil:
		return nil, nil, "", SelfieBindingNone
	case reference == nil:
		// A pass from before binding: adopt today's primary.
		return current, nil, "", SelfieBindingAdopted
	case same(reference, current):
		if rebindStatus == "" {
			return reference, nil, "", SelfieBindingNone
		}
		return reference, nil, "", SelfieBindingRestored
	case current == nil:
		// No approved primary: badge off, nothing to review yet.
		return reference, nil, SelfieRebindAwaitingPhoto, withdrawnUnless(rebindStatus != "")
	case same(rebindMedia, current) && rebindStatus != "":
		// Already queued (or declined) for this very photo.
		return reference, rebindMedia, rebindStatus, SelfieBindingNone
	default:
		return reference, current, SelfieRebindPending, SelfieBindingWithdrawn
	}
}

func withdrawnUnless(already bool) SelfieBinding {
	if already {
		return SelfieBindingNone
	}
	return SelfieBindingWithdrawn
}

// CheckSelfieBinding keeps the selfie badge bound to the photo it was
// checked against (audit M1). Call it after any change to the user's photos.
// For a passed selfie: when the approved primary photo is no longer the
// bound one, the badge is withdrawn at once (trust_tier → phone) and the
// stored video goes to the moderator queue against the new primary
// (selfie_rebind_status 'pending', reason PRIMARY_PHOTO_CHANGED); when the
// bound photo is primary again, the badge comes back. selfie_status stays
// 'passed' throughout — the person did pass liveness; what is in question is
// whether the photos are theirs. Not a passed selfie: nothing.
func (s *Store) CheckSelfieBinding(ctx context.Context, userID uuid.UUID) (SelfieBinding, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return SelfieBindingNone, fmt.Errorf("selfie binding: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockSelfieUser(ctx, tx, userID); err != nil {
		return SelfieBindingNone, err
	}
	var status, rebindStatus *string
	var reference, rebindMedia, current *uuid.UUID
	err = tx.QueryRow(ctx, `
        SELECT v.selfie_status, v.selfie_reference_media_id, v.selfie_rebind_media_id, v.selfie_rebind_status,
               `+approvedPrimaryMediaSQL("v.user_id")+`
        FROM dating_verifications v WHERE v.user_id = $1
        FOR UPDATE`, userID).Scan(&status, &reference, &rebindMedia, &rebindStatus, &current)
	if errors.Is(err, pgx.ErrNoRows) {
		return SelfieBindingNone, nil
	}
	if err != nil {
		return SelfieBindingNone, fmt.Errorf("selfie binding: read: %w", err)
	}
	if status == nil || *status != SelfieStatusPassed {
		return SelfieBindingNone, nil
	}
	rs := ""
	if rebindStatus != nil {
		rs = *rebindStatus
	}
	newRef, newRebind, newStatus, out := selfieBindingStep(reference, current, rebindMedia, rs)
	if _, err := tx.Exec(ctx, `
        UPDATE dating_verifications
        SET selfie_reference_media_id = $2, selfie_rebind_media_id = $3, selfie_rebind_status = NULLIF($4, ''),
            selfie_at = CASE WHEN $5 THEN now() ELSE selfie_at END
        WHERE user_id = $1`, userID, newRef, newRebind, newStatus, out == SelfieBindingWithdrawn); err != nil {
		return SelfieBindingNone, fmt.Errorf("selfie binding: write: %w", err)
	}
	if newStatus != "" {
		// The badge is off while the binding is in question.
		if _, err := tx.Exec(ctx, `
            UPDATE dating_profiles SET trust_tier = 'phone', updated_at = now()
            WHERE user_id = $1 AND trust_tier IN ('selfie','aadhaar')`, userID); err != nil {
			return SelfieBindingNone, fmt.Errorf("selfie binding: withdraw badge: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return SelfieBindingNone, fmt.Errorf("selfie binding: commit: %w", err)
	}
	return out, nil
}

// SelfieBadgeBound reports whether the user's badge may show: false while a
// passed selfie's binding to the primary photo is in question (audit M1).
func (s *Store) SelfieBadgeBound(ctx context.Context, userID uuid.UUID) (bool, error) {
	var inQuestion bool
	err := s.db.QueryRow(ctx, `
        SELECT selfie_rebind_status IS NOT NULL FROM dating_verifications WHERE user_id = $1`, userID).Scan(&inQuestion)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("selfie badge bound: %w", err)
	}
	return !inQuestion, nil
}
