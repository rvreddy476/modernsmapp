package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Copyright title review (migration 073; founder quick wins 2026-10-24).
//
// The hold is a post_restrictions row with source 'title_review', issued by
// post-service itself, so posts.effective_review_status reads 'restricted'
// and every viewer path that honours restrictions (056) hides the post. This
// file is the ONLY writer of that source: the hold is placed inside the
// publishing transaction (CreatePostWithEventIdempotent, the scheduled
// flip), and lifted only by a reviewer's clear or the duration rule below.
// trust-safety's restriction command refuses the source (ApplyPostRestriction
// validates source copyright|safety), and ModeratePost, appeals, resubmit
// and restore never touch restrictions.

const (
	RestrictionSourceTitleReview = "title_review"
	titleReviewIssuer            = "post-service"
	// TitleReviewPolicyVersion is stamped on the restriction row.
	TitleReviewPolicyVersion = "title-review-v1"
	// Restriction reason codes for this source.
	titleReviewReasonMatch   = "title_match"
	titleReviewReasonCleared = "review_cleared"
	titleReviewReasonBlocked = "review_blocked"
	titleReviewReasonShort   = "duration_under_threshold"
	titleReviewReasonKind    = "not_long_video"
	// titleReviewReasonOwnerBlock is the restriction reason of a block the
	// system made for a registered rights owner (migration 075).
	titleReviewReasonOwnerBlock = "owner_registered"
)

// Why the system acted on a review (copyright_title_reviews.reason,
// migration 075; the event's reason).
const (
	// TitleReviewReasonOwnerRegistered: blocked at once, no reviewer, because
	// the rights owner registered the title and asked for copies blocked.
	TitleReviewReasonOwnerRegistered = events.CopyrightReasonOwnerRegistered
	// TitleReviewReasonMuteRequested: the owner asked for the audio to be
	// muted; there is no audio muting yet, so a reviewer decides.
	TitleReviewReasonMuteRequested = events.CopyrightReasonMuteRequested
)

// MatchedFieldOwnerMatch is the matched_field of a review opened by an owner
// match (ApplyOwnerMatch) rather than by the title text.
const MatchedFieldOwnerMatch = "owner_match"

// Title review states (the wire's copyright_review.state).
const (
	TitleReviewPending = events.CopyrightReviewPending
	TitleReviewCleared = events.CopyrightReviewCleared
	TitleReviewBlocked = events.CopyrightReviewBlocked
)

// Decisions a reviewer may take.
const (
	TitleReviewDecisionClear = "clear"
	TitleReviewDecisionBlock = "block"
)

var (
	// ErrTitleReviewNotFound: no review with that id (404 REVIEW_NOT_FOUND).
	ErrTitleReviewNotFound = errors.New("copyright review not found")
	// ErrTitleReviewDecided: the review is no longer pending (409 REVIEW_ALREADY_DECIDED).
	ErrTitleReviewDecided = errors.New("copyright review already decided")
	// ErrCopyrightTitleNotFound: no catalogue entry with that id (404).
	ErrCopyrightTitleNotFound = errors.New("copyright title not found")
	// ErrTitleReviewInvalid: a write failed the store's own shape check.
	ErrTitleReviewInvalid = errors.New("invalid copyright review write")
	// ErrPostCopyrightHeld: the post is under an active hold, so a
	// members-only tier may not be set or kept on it (409 COPYRIGHT_REVIEW).
	ErrPostCopyrightHeld = errors.New("post is under copyright review")
)

// TitleReviewHold, set on a post being published, makes the publishing
// transaction hold it for review (CreatePostWithEventIdempotent and
// PublishScheduledPostHeld). Never on the wire or in the cache.
type TitleReviewHold struct {
	ReviewID     uuid.UUID
	TitleID      uuid.UUID
	MatchedTitle string
	MatchedTerm  string
	MatchedField string
	// DurationSeconds is nil when the length is not known yet; then the
	// hold waits for the transcode (AwaitingDuration) before it is queued
	// and the author told.
	DurationSeconds  *int
	AwaitingDuration bool
	// AnnounceOnClear: the publication's PostCreated was withheld.
	AnnounceOnClear bool
	// RightsOwner is the matched entry's registered owner (a snapshot for
	// the review, the event and the author's email); nil when it names none.
	RightsOwner *string
	// AutoBlock: the matched entry's owner asked for copies to be blocked.
	// A measured hold is then decided BLOCKED in the same transaction, by
	// the system, with reason owner_registered; an unmeasured one waits for
	// its length like any other hold (ResolveTitleReviewDuration decides).
	AutoBlock bool
	// Reason is the hold's own reason: "" (a title match) or
	// TitleReviewReasonMuteRequested (an owner asked for a mute).
	Reason string
}

func (h *TitleReviewHold) validate() error {
	if h == nil || h.ReviewID == uuid.Nil || h.TitleID == uuid.Nil ||
		strings.TrimSpace(h.MatchedTitle) == "" || strings.TrimSpace(h.MatchedTerm) == "" {
		return fmt.Errorf("%w: hold", ErrTitleReviewInvalid)
	}
	switch h.MatchedField {
	case "title", "description", "hashtag", MatchedFieldOwnerMatch:
	default:
		return fmt.Errorf("%w: matched_field %q", ErrTitleReviewInvalid, h.MatchedField)
	}
	if h.AwaitingDuration != (h.DurationSeconds == nil) {
		return fmt.Errorf("%w: awaiting_duration must mean an unknown duration", ErrTitleReviewInvalid)
	}
	switch h.Reason {
	case "", TitleReviewReasonMuteRequested:
	default:
		return fmt.Errorf("%w: hold reason %q", ErrTitleReviewInvalid, h.Reason)
	}
	if h.AutoBlock && h.Reason != "" {
		return fmt.Errorf("%w: an automatic block carries no hold reason", ErrTitleReviewInvalid)
	}
	if h.AutoBlock && (h.RightsOwner == nil || strings.TrimSpace(*h.RightsOwner) == "") {
		return fmt.Errorf("%w: an automatic block names the rights owner", ErrTitleReviewInvalid)
	}
	return nil
}

// TitleReview is one review row.
type TitleReview struct {
	ID                uuid.UUID  `json:"id"`
	PostID            uuid.UUID  `json:"post_id"`
	AuthorID          uuid.UUID  `json:"author_id"`
	RestrictionID     uuid.UUID  `json:"restriction_id"`
	TitleID           uuid.UUID  `json:"title_id"`
	MatchedTitle      string     `json:"matched_title"`
	MatchedTerm       string     `json:"matched_term"`
	MatchedField      string     `json:"matched_field"`
	DurationSeconds   *int       `json:"duration_seconds"`
	AwaitingDuration  bool       `json:"awaiting_duration"`
	State             string     `json:"state"`
	AnnounceOnClear   bool       `json:"-"`
	AnnouncedAt       *time.Time `json:"-"`
	AuthorNotified    bool       `json:"-"`
	AuthorNote        *string    `json:"author_note"`
	AuthorRequestedAt *time.Time `json:"author_requested_at"`
	DecidedBy         *uuid.UUID `json:"decided_by"`
	DecidedAt         *time.Time `json:"decided_at"`
	DecisionNote      *string    `json:"decision_note"`
	// RightsOwner is the owner the matched entry named (migration 075).
	RightsOwner *string `json:"rights_owner"`
	// Reason: owner_registered (the system blocked it) or mute_requested
	// (an owner asked for a mute); nil for an ordinary title match.
	Reason    *string   `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

const titleReviewCols = `id, post_id, author_id, restriction_id, title_id, matched_title, matched_term, matched_field,
	duration_seconds, awaiting_duration, state, announce_on_clear, announced_at, author_notified,
	author_note, author_requested_at, decided_by, decided_at, decision_note, rights_owner, reason, created_at, updated_at`

// titleReviewColsR is titleReviewCols qualified with the alias r.
const titleReviewColsR = `r.id, r.post_id, r.author_id, r.restriction_id, r.title_id, r.matched_title, r.matched_term,
	r.matched_field, r.duration_seconds, r.awaiting_duration, r.state, r.announce_on_clear, r.announced_at,
	r.author_notified, r.author_note, r.author_requested_at, r.decided_by, r.decided_at, r.decision_note,
	r.rights_owner, r.reason, r.created_at, r.updated_at`

// titleReviewDest is the scan destinations of titleReviewCols, in order.
func titleReviewDest(r *TitleReview) []any {
	return []any{&r.ID, &r.PostID, &r.AuthorID, &r.RestrictionID, &r.TitleID, &r.MatchedTitle, &r.MatchedTerm,
		&r.MatchedField, &r.DurationSeconds, &r.AwaitingDuration, &r.State, &r.AnnounceOnClear, &r.AnnouncedAt,
		&r.AuthorNotified, &r.AuthorNote, &r.AuthorRequestedAt, &r.DecidedBy, &r.DecidedAt, &r.DecisionNote,
		&r.RightsOwner, &r.Reason, &r.CreatedAt, &r.UpdatedAt}
}

func scanTitleReview(row pgx.Row) (*TitleReview, error) {
	var r TitleReview
	if err := row.Scan(titleReviewDest(&r)...); err != nil {
		return nil, err
	}
	return &r, nil
}

// ── The hold ────────────────────────────────────────────────────────────

// insertTitleReviewHoldTx places the hold on a post that this transaction
// is publishing: the review row, the restriction row, the recount, the
// eligibility event (the post leaves search the way a rejected one does),
// the history row and — when the length is known — the author's
// "in copyright review" event. A post that already has a review is left
// alone (false): one review per post.
//
// A measured hold on an entry whose owner asked for blocking (AutoBlock) is
// decided BLOCKED by the system in the same transaction (systemBlockTx):
// the author hears of the block, never of a review that nobody will make.
func insertTitleReviewHoldTx(ctx context.Context, tx pgx.Tx, postID, authorID uuid.UUID, hold *TitleReviewHold) (bool, error) {
	if err := hold.validate(); err != nil {
		return false, err
	}
	restrictionID := uuid.New()
	tag, err := tx.Exec(ctx, `
		INSERT INTO copyright_title_reviews
			(id, post_id, author_id, restriction_id, title_id, matched_title, matched_term, matched_field,
			 duration_seconds, awaiting_duration, state, announce_on_clear, author_notified, rights_owner, reason)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'pending',$11,$12,$13,$14)
		ON CONFLICT (post_id) DO NOTHING`,
		hold.ReviewID, postID, authorID, restrictionID, hold.TitleID, hold.MatchedTitle, hold.MatchedTerm,
		hold.MatchedField, hold.DurationSeconds, hold.AwaitingDuration, hold.AnnounceOnClear, !hold.AwaitingDuration,
		hold.RightsOwner, nilIfBlank(hold.Reason))
	if err != nil {
		return false, fmt.Errorf("title review: insert review: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO post_restrictions
			(restriction_id, post_id, source, case_id, issuer, scope, state, case_revision,
			 last_decision_id, policy_version, reason_code, first_placed_at, placed_at, released_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,'global','active',1,$6,$7,$8,now(),now(),NULL,now())`,
		restrictionID, postID, RestrictionSourceTitleReview, hold.ReviewID, titleReviewIssuer,
		uuid.New(), TitleReviewPolicyVersion, titleReviewReasonMatch); err != nil {
		return false, fmt.Errorf("title review: insert restriction: %w", err)
	}
	if err := recountRestrictionsTx(ctx, tx, postID); err != nil {
		return false, err
	}
	if err := BumpSearchRevAndEmitTx(ctx, tx, postID); err != nil {
		return false, err
	}
	if err := insertTitleReviewEventTx(ctx, tx, hold.ReviewID, postID, "hold", nil, "", TitleReviewPending, hold.Reason); err != nil {
		return false, err
	}
	if hold.AwaitingDuration {
		return true, nil // nobody is told until the transcode measures it
	}
	if hold.AutoBlock {
		return true, systemBlockTx(ctx, tx, hold.ReviewID, postID, restrictionID, TitleReviewPending)
	}
	if err := emitTitleReviewTx(ctx, tx, hold.ReviewID); err != nil {
		return false, err
	}
	return true, nil
}

// systemBlockTx decides a PENDING review blocked by the system for a
// registered rights owner: the hold stays (its restriction row records
// why), the review is blocked with decided_by NULL and reason
// owner_registered (the 075 decision-shape CHECK admits exactly that), the
// history row is the audit (actor NULL = the system), and the blocked event
// goes out — trust-safety counts it toward the repeat-uploader pause and
// notification-service tells the author, in-app and by email. No reviewer
// is involved and nothing waits for one.
func systemBlockTx(ctx context.Context, tx pgx.Tx, reviewID, postID, restrictionID uuid.UUID, prevState string) error {
	if err := markTitleReviewBlockedTx(ctx, tx, reviewID, restrictionID, titleReviewReasonOwnerBlock); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE copyright_title_reviews
		SET state = 'blocked', decided_by = NULL, decided_at = now(), reason = $2,
		    awaiting_duration = FALSE, author_notified = TRUE, updated_at = now()
		WHERE id = $1 AND state = 'pending'`, reviewID, TitleReviewReasonOwnerRegistered)
	if err != nil {
		return fmt.Errorf("title review: system block: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("title review: system block: review %s is not pending", reviewID)
	}
	if err := insertTitleReviewEventTx(ctx, tx, reviewID, postID, "auto_block", nil, prevState, TitleReviewBlocked,
		TitleReviewReasonOwnerRegistered); err != nil {
		return err
	}
	return emitTitleReviewTx(ctx, tx, reviewID)
}

// recountRestrictionsTx is the one count write: a recount of the active
// rows, never ±1 (the 056 rule; the deferred trigger checks it at commit).
func recountRestrictionsTx(ctx context.Context, tx pgx.Tx, postID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `
		UPDATE posts
		SET active_restriction_count = (SELECT COUNT(*) FROM post_restrictions WHERE post_id = $1 AND state = 'active')
		WHERE id = $1`, postID); err != nil {
		return fmt.Errorf("title review: recount: %w", err)
	}
	return nil
}

func insertTitleReviewEventTx(ctx context.Context, tx pgx.Tx, reviewID, postID uuid.UUID, action string, actor *uuid.UUID, prev, next, note string) error {
	var prevPtr, notePtr *string
	if prev != "" {
		prevPtr = &prev
	}
	if n := strings.TrimSpace(note); n != "" {
		notePtr = &n
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO copyright_title_review_events (id, review_id, post_id, action, actor_id, prev_state, new_state, note)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		uuid.New(), reviewID, postID, action, actor, prevPtr, next, notePtr); err != nil {
		return fmt.Errorf("title review: history: %w", err)
	}
	return nil
}

// emitTitleReviewTx writes the review's CURRENT state to the outbox as
// post.copyright_review.changed, read back inside tx so the event says
// what committed.
func emitTitleReviewTx(ctx context.Context, tx pgx.Tx, reviewID uuid.UUID) error {
	var (
		p         events.PostCopyrightReviewPayload
		postID    uuid.UUID
		authorID  uuid.UUID
		decidedAt *time.Time
		decidedBy *uuid.UUID
	)
	if err := tx.QueryRow(ctx, `
		SELECT r.post_id, r.author_id, r.state, r.matched_title, COALESCE(p.title, ''), p.content_type,
		       r.created_at, r.decided_at, r.decided_by, COALESCE(r.rights_owner, ''), COALESCE(r.reason, '')
		FROM copyright_title_reviews r JOIN posts p ON p.id = r.post_id
		WHERE r.id = $1`, reviewID).
		Scan(&postID, &authorID, &p.State, &p.MatchedTitle, &p.PostTitle, &p.ContentType, &p.CreatedAt, &decidedAt, &decidedBy,
			&p.RightsOwner, &p.Reason); err != nil {
		return fmt.Errorf("title review: read for event: %w", err)
	}
	p.ReviewID, p.PostID, p.AuthorID = reviewID.String(), postID.String(), authorID.String()
	p.DecidedAt = decidedAt
	if decidedBy != nil {
		p.DecidedBy = decidedBy.String()
	}
	p.OccurredAt = time.Now().UTC()
	return InsertOutboxEventTx(ctx, tx, events.EventPostCopyrightReview, "post", postID, p)
}

// ── Decisions ───────────────────────────────────────────────────────────

// TitleReviewAnnounce builds the withheld PostCreated for a cleared post,
// given the search revision the clear stamped (the caller holds the post,
// read with its media before the transaction). ("", nil, nil) emits none.
type TitleReviewAnnounce func(rev int64) (eventType string, payload interface{}, err error)

// TitleReviewOutcome is what a decision did.
type TitleReviewOutcome struct {
	Review *TitleReview `json:"review"`
	// Announced is true when this decision emitted the withheld PostCreated.
	Announced bool `json:"announced"`
}

// DecideTitleReviewInput is one reviewer decision.
type DecideTitleReviewInput struct {
	ReviewID uuid.UUID
	Decision string // clear | block
	ActorID  uuid.UUID
	Note     string
}

// DecideTitleReview applies a reviewer's clear or block in one
// transaction: the compare-and-set on the pending row, then for a clear the
// restriction release, the recount, the eligibility event and the withheld
// PostCreated (once), or for a block the restriction's reason; then the
// author's event, the history row and the admin audit row.
func (s *Store) DecideTitleReview(ctx context.Context, in DecideTitleReviewInput, announce TitleReviewAnnounce) (*TitleReviewOutcome, error) {
	if in.ReviewID == uuid.Nil || in.ActorID == uuid.Nil {
		return nil, fmt.Errorf("%w: review and actor are required", ErrTitleReviewInvalid)
	}
	if in.Decision != TitleReviewDecisionClear && in.Decision != TitleReviewDecisionBlock {
		return nil, fmt.Errorf("%w: decision %q", ErrTitleReviewInvalid, in.Decision)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	r, err := scanTitleReview(tx.QueryRow(ctx, `SELECT `+titleReviewCols+` FROM copyright_title_reviews WHERE id = $1 FOR UPDATE`, in.ReviewID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTitleReviewNotFound
	}
	if err != nil {
		return nil, err
	}
	if r.State != TitleReviewPending {
		return nil, ErrTitleReviewDecided
	}
	actor := in.ActorID
	out := &TitleReviewOutcome{}
	next := TitleReviewCleared
	reason := titleReviewReasonCleared
	if in.Decision == TitleReviewDecisionBlock {
		next, reason = TitleReviewBlocked, titleReviewReasonBlocked
	}
	if next == TitleReviewCleared {
		announced, err := releaseTitleReviewTx(ctx, tx, r, reason, announce)
		if err != nil {
			return nil, err
		}
		out.Announced = announced
	} else if err := markTitleReviewBlockedTx(ctx, tx, r.ID, r.RestrictionID, titleReviewReasonBlocked); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE copyright_title_reviews
		SET state = $2, decided_by = $3, decided_at = now(), decision_note = $4,
		    awaiting_duration = FALSE, author_notified = TRUE, updated_at = now()
		WHERE id = $1`, r.ID, next, actor, nilIfBlank(in.Note)); err != nil {
		return nil, fmt.Errorf("title review: decide: %w", err)
	}
	if err := insertTitleReviewEventTx(ctx, tx, r.ID, r.PostID, in.Decision, &actor, r.State, next, in.Note); err != nil {
		return nil, err
	}
	if err := emitTitleReviewTx(ctx, tx, r.ID); err != nil {
		return nil, err
	}
	if err := insertAdminAudit(ctx, tx, actor, "copyright_review.decide", "copyright_review", r.ID, r.State, next, in.Note); err != nil {
		return nil, err
	}
	decided, err := scanTitleReview(tx.QueryRow(ctx, `SELECT `+titleReviewCols+` FROM copyright_title_reviews WHERE id = $1`, r.ID))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	out.Review = decided
	return out, nil
}

func nilIfBlank(s string) *string {
	if t := strings.TrimSpace(s); t != "" {
		return &t
	}
	return nil
}

// releaseTitleReviewTx lifts the hold of r (the row is locked): the
// restriction row only, the recount, the eligibility event; then, when the
// publication's PostCreated was withheld and the post is still live, that
// event — exactly once, because announced_at is set in this transaction
// and the review row's lock serialises every decision.
func releaseTitleReviewTx(ctx context.Context, tx pgx.Tx, r *TitleReview, reason string, announce TitleReviewAnnounce) (bool, error) {
	// Lock the post first (the restriction command's order: post, then row).
	var deleted bool
	if err := tx.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM posts WHERE id = $1 FOR UPDATE`, r.PostID).Scan(&deleted); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrTitleReviewNotFound
		}
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE post_restrictions
		SET state = 'released', case_revision = case_revision + 1, last_decision_id = $2,
		    reason_code = $3, released_at = now(), updated_at = now()
		WHERE restriction_id = $1 AND source = $4 AND state = 'active'`,
		r.RestrictionID, uuid.New(), reason, RestrictionSourceTitleReview); err != nil {
		return false, fmt.Errorf("title review: release: %w", err)
	}
	if err := recountRestrictionsTx(ctx, tx, r.PostID); err != nil {
		return false, err
	}
	rev, err := BumpSearchRevAndEmitTxRev(ctx, tx, r.PostID)
	if err != nil {
		return false, err
	}
	if !r.AnnounceOnClear || r.AnnouncedAt != nil || deleted || announce == nil {
		return false, nil
	}
	// Still held by another case (a trust-safety copyright hold): the post
	// is not visible yet, so it is not announced. (Known gap: that case's
	// later release does not announce it either.)
	var stillHeld bool
	if err := tx.QueryRow(ctx, `SELECT active_restriction_count > 0 FROM posts WHERE id = $1`, r.PostID).Scan(&stillHeld); err != nil {
		return false, err
	}
	if stillHeld {
		return false, nil
	}
	eventType, payload, err := announce(rev)
	if err != nil {
		return false, err
	}
	if eventType == "" {
		return false, nil
	}
	if err := InsertOutboxEventTx(ctx, tx, eventType, "post", r.PostID, payload); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE copyright_title_reviews SET announced_at = now() WHERE id = $1`, r.ID); err != nil {
		return false, err
	}
	// A rights owner tracks this post (migration 075): the eligibility event
	// above carried earnings_excluded, but it reached monetization BEFORE the
	// ownership row this PostCreated creates. One more revision after it
	// restates the exclusion, so the cleared post never starts earning.
	var tracked bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM copyright_owner_tracks WHERE post_id = $1)`, r.PostID).Scan(&tracked); err != nil {
		return false, err
	}
	if tracked {
		if err := BumpSearchRevAndEmitTx(ctx, tx, r.PostID); err != nil {
			return false, err
		}
	}
	return true, nil
}

// markTitleReviewBlockedTx keeps the hold and records why on its row.
func markTitleReviewBlockedTx(ctx context.Context, tx pgx.Tx, reviewID, restrictionID uuid.UUID, reasonCode string) error {
	tag, err := tx.Exec(ctx, `
		UPDATE post_restrictions
		SET case_revision = case_revision + 1, last_decision_id = $2, reason_code = $3, updated_at = now()
		WHERE restriction_id = $1 AND source = $4 AND state = 'active'`,
		restrictionID, uuid.New(), reasonCode, RestrictionSourceTitleReview)
	if err != nil {
		return fmt.Errorf("title review: block: %w", err)
	}
	if tag.RowsAffected() != 1 {
		// A pending review always has its active hold; anything else is a
		// data fault, and a block must never be recorded without one.
		return fmt.Errorf("title review: block: no active hold for review %s", reviewID)
	}
	return nil
}

// ── The duration rule ───────────────────────────────────────────────────

// TitleReviewDurationOutcome says what the measurement did.
type TitleReviewDurationOutcome string

const (
	TitleReviewDurationNone    TitleReviewDurationOutcome = ""        // no review waiting
	TitleReviewDurationQueued  TitleReviewDurationOutcome = "queued"  // over the threshold: queued, author told
	TitleReviewDurationCleared TitleReviewDurationOutcome = "cleared" // under it (or no longer a long video): lifted
	TitleReviewDurationBlocked TitleReviewDurationOutcome = "blocked" // over it, and the owner asked for blocking
)

// ResolveTitleReviewDuration settles a hold that was waiting for the
// video's length. hold=false lifts it as the system (no reviewer, no
// notice: the author was never told of it); hold=true queues it for a
// reviewer and tells the author — unless autoBlock is allowed and the
// matched entry's owner (read now, in this transaction) still asks for
// blocking: then the system blocks it at once (systemBlockTx). A review
// that is not waiting is left alone (TitleReviewDurationNone).
func (s *Store) ResolveTitleReviewDuration(ctx context.Context, postID uuid.UUID, durationSec int, hold, autoBlock bool, reason string, announce TitleReviewAnnounce) (TitleReviewDurationOutcome, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return TitleReviewDurationNone, err
	}
	defer tx.Rollback(ctx)
	r, err := scanTitleReview(tx.QueryRow(ctx, `SELECT `+titleReviewCols+` FROM copyright_title_reviews
		WHERE post_id = $1 AND state = 'pending' AND awaiting_duration FOR UPDATE`, postID))
	if errors.Is(err, pgx.ErrNoRows) {
		return TitleReviewDurationNone, tx.Commit(ctx)
	}
	if err != nil {
		return TitleReviewDurationNone, err
	}
	if hold {
		if _, err := tx.Exec(ctx, `
			UPDATE copyright_title_reviews
			SET duration_seconds = $2, awaiting_duration = FALSE, author_notified = TRUE, updated_at = now()
			WHERE id = $1`, r.ID, durationSec); err != nil {
			return TitleReviewDurationNone, err
		}
		if err := insertTitleReviewEventTx(ctx, tx, r.ID, postID, "duration_measured", nil, r.State, TitleReviewPending, ""); err != nil {
			return TitleReviewDurationNone, err
		}
		if autoBlock {
			// The entry as it is NOW: an owner who withdrew the request (or
			// an entry switched off) since the upload sends it to a reviewer.
			var ownerBlocks bool
			var owner *string
			if err := tx.QueryRow(ctx, `SELECT active AND auto_block, rights_owner FROM copyright_review_titles WHERE id = $1`,
				r.TitleID).Scan(&ownerBlocks, &owner); err != nil {
				return TitleReviewDurationNone, fmt.Errorf("title review: entry for block: %w", err)
			}
			if ownerBlocks && owner != nil && strings.TrimSpace(*owner) != "" {
				if _, err := tx.Exec(ctx, `UPDATE copyright_title_reviews SET rights_owner = COALESCE(rights_owner, $2) WHERE id = $1`,
					r.ID, owner); err != nil {
					return TitleReviewDurationNone, err
				}
				if err := systemBlockTx(ctx, tx, r.ID, postID, r.RestrictionID, r.State); err != nil {
					return TitleReviewDurationNone, err
				}
				return TitleReviewDurationBlocked, tx.Commit(ctx)
			}
		}
		if err := emitTitleReviewTx(ctx, tx, r.ID); err != nil {
			return TitleReviewDurationNone, err
		}
		return TitleReviewDurationQueued, tx.Commit(ctx)
	}
	if reason == "" {
		reason = titleReviewReasonShort
	}
	if _, err := releaseTitleReviewTx(ctx, tx, r, reason, announce); err != nil {
		return TitleReviewDurationNone, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE copyright_title_reviews
		SET state = 'cleared', duration_seconds = $2, awaiting_duration = FALSE, decided_at = now(),
		    decision_note = $3, updated_at = now()
		WHERE id = $1`, r.ID, durationSec, reason); err != nil {
		return TitleReviewDurationNone, err
	}
	if err := insertTitleReviewEventTx(ctx, tx, r.ID, postID, "auto_clear", nil, r.State, TitleReviewCleared, reason); err != nil {
		return TitleReviewDurationNone, err
	}
	return TitleReviewDurationCleared, tx.Commit(ctx)
}

// TitleReviewReasonNotLongVideo is the auto-clear reason when the transcode
// reclassified the post out of long video.
const TitleReviewReasonNotLongVideo = titleReviewReasonKind

// ── The author ──────────────────────────────────────────────────────────

// RequestTitleReview records the author's "I own the rights" note on their
// pending review. ErrTitleReviewNotFound when the post has no review of
// theirs; ErrTitleReviewDecided once it is decided.
func (s *Store) RequestTitleReview(ctx context.Context, postID, authorID uuid.UUID, note string) (*TitleReview, error) {
	note = strings.TrimSpace(note)
	if note == "" {
		return nil, fmt.Errorf("%w: note is required", ErrTitleReviewInvalid)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	r, err := scanTitleReview(tx.QueryRow(ctx, `SELECT `+titleReviewCols+` FROM copyright_title_reviews
		WHERE post_id = $1 AND author_id = $2 FOR UPDATE`, postID, authorID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTitleReviewNotFound
	}
	if err != nil {
		return nil, err
	}
	if r.State != TitleReviewPending {
		return nil, ErrTitleReviewDecided
	}
	if _, err := tx.Exec(ctx, `
		UPDATE copyright_title_reviews
		SET author_note = $2, author_requested_at = now(), updated_at = now()
		WHERE id = $1`, r.ID, note); err != nil {
		return nil, err
	}
	// The note itself stays on the review row only (the author's words are
	// not copied into the history).
	if err := insertTitleReviewEventTx(ctx, tx, r.ID, postID, "author_request", &authorID, r.State, r.State, ""); err != nil {
		return nil, err
	}
	out, err := scanTitleReview(tx.QueryRow(ctx, `SELECT `+titleReviewCols+` FROM copyright_title_reviews WHERE id = $1`, r.ID))
	if err != nil {
		return nil, err
	}
	return out, tx.Commit(ctx)
}

// TitleReviewsByPost returns the review of each post that has one, for the
// author's own reads.
func (s *Store) TitleReviewsByPost(ctx context.Context, postIDs []uuid.UUID) (map[uuid.UUID]*TitleReview, error) {
	out := make(map[uuid.UUID]*TitleReview, len(postIDs))
	if len(postIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `SELECT `+titleReviewCols+` FROM copyright_title_reviews WHERE post_id = ANY($1)`, postIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		r, err := scanTitleReview(rows)
		if err != nil {
			return nil, err
		}
		out[r.PostID] = r
	}
	return out, rows.Err()
}

// GetTitleReview reads one review by id.
func (s *Store) GetTitleReview(ctx context.Context, id uuid.UUID) (*TitleReview, error) {
	r, err := scanTitleReview(s.db.QueryRow(ctx, `SELECT `+titleReviewCols+` FROM copyright_title_reviews WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTitleReviewNotFound
	}
	return r, err
}

// ── The reviewer queue ──────────────────────────────────────────────────

// TitleReviewQueueItem is one queue row: the review with the post facts a
// reviewer needs to judge it.
type TitleReviewQueueItem struct {
	TitleReview
	PostTitle       string     `json:"post_title"`
	PostText        string     `json:"post_description"`
	PostHashtags    []string   `json:"post_hashtags"`
	ContentType     string     `json:"content_type"`
	PostVisibility  string     `json:"post_visibility"`
	PostReview      string     `json:"post_review_status"`
	PostDeletedAt   *time.Time `json:"post_deleted_at"`
	PostCreatedAt   time.Time  `json:"post_created_at"`
	TitleLanguage   *string    `json:"title_language"`
	TitleAliases    []string   `json:"title_aliases"`
	TitleOwnerCount int        `json:"title_owner_channel_count"`
	// TitleAutoBlock: the entry's owner currently asks for blocking.
	TitleAutoBlock bool `json:"title_auto_block"`
}

// MaxTitleReviewPage caps one queue or catalogue page.
const MaxTitleReviewPage = 200

// ListTitleReviews pages reviews in one state, OLDEST first (the queue
// order). pending lists only measured holds; the cursor is the last row's
// "created_at(RFC3339Nano)_id".
func (s *Store) ListTitleReviews(ctx context.Context, state, cursor string, limit int) ([]TitleReviewQueueItem, string, error) {
	switch state {
	case TitleReviewPending, TitleReviewCleared, TitleReviewBlocked:
	default:
		return nil, "", fmt.Errorf("%w: state %q", ErrTitleReviewInvalid, state)
	}
	if limit <= 0 || limit > MaxTitleReviewPage {
		limit = 50
	}
	args := []any{state, limit + 1}
	where := `r.state = $1`
	if state == TitleReviewPending {
		where += ` AND NOT r.awaiting_duration`
	}
	if cursor != "" {
		at, id, err := parseRestrictionCursor(cursor)
		if err != nil {
			return nil, "", fmt.Errorf("%w: cursor", ErrTitleReviewInvalid)
		}
		args = append(args, at, id)
		where += ` AND (r.created_at, r.id) > ($3, $4)`
	}
	rows, err := s.db.Query(ctx, `
		SELECT `+titleReviewColsR+`,
		       COALESCE(p.title, ''), p.text, p.hashtags, p.content_type, p.visibility, p.review_status,
		       p.deleted_at, p.created_at, t.language, t.aliases, cardinality(t.owner_channel_ids), t.auto_block
		FROM copyright_title_reviews r
		JOIN posts p ON p.id = r.post_id
		JOIN copyright_review_titles t ON t.id = r.title_id
		WHERE `+where+`
		ORDER BY r.created_at ASC, r.id ASC
		LIMIT $2`, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := make([]TitleReviewQueueItem, 0, limit)
	for rows.Next() {
		var it TitleReviewQueueItem
		dest := append(titleReviewDest(&it.TitleReview),
			&it.PostTitle, &it.PostText, &it.PostHashtags, &it.ContentType, &it.PostVisibility, &it.PostReview,
			&it.PostDeletedAt, &it.PostCreatedAt, &it.TitleLanguage, &it.TitleAliases, &it.TitleOwnerCount, &it.TitleAutoBlock)
		if err := rows.Scan(dest...); err != nil {
			return nil, "", err
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		last := out[len(out)-1]
		next = last.CreatedAt.UTC().Format(time.RFC3339Nano) + "_" + last.ID.String()
	}
	return out, next, nil
}

// ── The catalogue ───────────────────────────────────────────────────────

// CopyrightTitle is one catalogue entry.
type CopyrightTitle struct {
	ID              uuid.UUID   `json:"id"`
	Title           string      `json:"title"`
	Aliases         []string    `json:"aliases"`
	Language        *string     `json:"language"`
	OwnerChannelIDs []uuid.UUID `json:"owner_channel_ids"`
	Active          bool        `json:"active"`
	// AutoBlock (migration 075): the rights owner registered this title and
	// asked for copies to be blocked; a matching upload is blocked at once.
	AutoBlock bool `json:"auto_block"`
	// RightsOwner is the owner's display name (required with AutoBlock).
	RightsOwner *string   `json:"rights_owner"`
	CreatedBy   uuid.UUID `json:"created_by"`
	UpdatedBy   uuid.UUID `json:"updated_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

const copyrightTitleCols = `id, title, aliases, language, owner_channel_ids, active, auto_block, rights_owner,
	created_by, updated_by, created_at, updated_at`

func scanCopyrightTitle(row pgx.Row) (*CopyrightTitle, error) {
	var t CopyrightTitle
	if err := row.Scan(&t.ID, &t.Title, &t.Aliases, &t.Language, &t.OwnerChannelIDs, &t.Active, &t.AutoBlock, &t.RightsOwner,
		&t.CreatedBy, &t.UpdatedBy, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	if t.Aliases == nil {
		t.Aliases = []string{}
	}
	if t.OwnerChannelIDs == nil {
		t.OwnerChannelIDs = []uuid.UUID{}
	}
	return &t, nil
}

// ListActiveCopyrightTitles is the matcher's snapshot: every active entry.
func (s *Store) ListActiveCopyrightTitles(ctx context.Context) ([]CopyrightTitle, error) {
	rows, err := s.db.Query(ctx, `SELECT `+copyrightTitleCols+` FROM copyright_review_titles WHERE active ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CopyrightTitle
	for rows.Next() {
		t, err := scanCopyrightTitle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// CopyrightTitleFilter pages the catalogue, newest first.
type CopyrightTitleFilter struct {
	Active *bool
	Query  string
	Cursor string
	Limit  int
}

// ListCopyrightTitles pages the catalogue newest first; Query matches the
// title or an alias case-insensitively. The cursor is the last row's
// "created_at(RFC3339Nano)_id".
func (s *Store) ListCopyrightTitles(ctx context.Context, f CopyrightTitleFilter) ([]CopyrightTitle, string, error) {
	if f.Limit <= 0 || f.Limit > MaxTitleReviewPage {
		f.Limit = 50
	}
	args := []any{f.Limit + 1}
	where := `TRUE`
	if f.Active != nil {
		args = append(args, *f.Active)
		where += fmt.Sprintf(` AND active = $%d`, len(args))
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		args = append(args, "%"+escapeLike(q)+"%")
		n := len(args)
		where += fmt.Sprintf(` AND (title ILIKE $%d ESCAPE '\' OR EXISTS (SELECT 1 FROM unnest(aliases) a WHERE a ILIKE $%d ESCAPE '\'))`, n, n)
	}
	if f.Cursor != "" {
		at, id, err := parseRestrictionCursor(f.Cursor)
		if err != nil {
			return nil, "", fmt.Errorf("%w: cursor", ErrTitleReviewInvalid)
		}
		args = append(args, at, id)
		where += fmt.Sprintf(` AND (created_at, id) < ($%d, $%d)`, len(args)-1, len(args))
	}
	rows, err := s.db.Query(ctx, `SELECT `+copyrightTitleCols+` FROM copyright_review_titles WHERE `+where+
		` ORDER BY created_at DESC, id DESC LIMIT $1`, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := make([]CopyrightTitle, 0, f.Limit)
	for rows.Next() {
		t, err := scanCopyrightTitle(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > f.Limit {
		out = out[:f.Limit]
		last := out[len(out)-1]
		next = last.CreatedAt.UTC().Format(time.RFC3339Nano) + "_" + last.ID.String()
	}
	return out, next, nil
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// CopyrightTitleWrite is a validated create or full update (the service
// validates; the store writes what it is given).
type CopyrightTitleWrite struct {
	Title           string
	Aliases         []string
	Language        *string
	OwnerChannelIDs []uuid.UUID
	Active          bool
	AutoBlock       bool
	RightsOwner     *string
}

// CreateCopyrightTitle inserts an entry and its audit row together.
func (s *Store) CreateCopyrightTitle(ctx context.Context, id uuid.UUID, w CopyrightTitleWrite, actor uuid.UUID) (*CopyrightTitle, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	t, err := scanCopyrightTitle(tx.QueryRow(ctx, `
		INSERT INTO copyright_review_titles (id, title, aliases, language, owner_channel_ids, active, created_by, updated_by,
		                                     auto_block, rights_owner)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$7,$8,$9)
		RETURNING `+copyrightTitleCols,
		id, w.Title, nonNilStrings(w.Aliases), w.Language, nonNilUUIDs(w.OwnerChannelIDs), w.Active, actor,
		w.AutoBlock, w.RightsOwner))
	if err != nil {
		return nil, err
	}
	if err := insertAdminAudit(ctx, tx, actor, "copyright_title.create", "copyright_title", t.ID, "", titleAuditValue(t), ""); err != nil {
		return nil, err
	}
	return t, tx.Commit(ctx)
}

// UpdateCopyrightTitle replaces an entry's fields (switching it off is
// Active=false) and audits the change in the same transaction.
func (s *Store) UpdateCopyrightTitle(ctx context.Context, id uuid.UUID, w CopyrightTitleWrite, actor uuid.UUID, note string) (*CopyrightTitle, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	before, err := scanCopyrightTitle(tx.QueryRow(ctx, `SELECT `+copyrightTitleCols+` FROM copyright_review_titles WHERE id = $1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCopyrightTitleNotFound
	}
	if err != nil {
		return nil, err
	}
	t, err := scanCopyrightTitle(tx.QueryRow(ctx, `
		UPDATE copyright_review_titles
		SET title = $2, aliases = $3, language = $4, owner_channel_ids = $5, active = $6, updated_by = $7,
		    auto_block = $8, rights_owner = $9, updated_at = now()
		WHERE id = $1
		RETURNING `+copyrightTitleCols,
		id, w.Title, nonNilStrings(w.Aliases), w.Language, nonNilUUIDs(w.OwnerChannelIDs), w.Active, actor,
		w.AutoBlock, w.RightsOwner))
	if err != nil {
		return nil, err
	}
	if err := insertAdminAudit(ctx, tx, actor, "copyright_title.update", "copyright_title", t.ID, titleAuditValue(before), titleAuditValue(t), note); err != nil {
		return nil, err
	}
	return t, tx.Commit(ctx)
}

// GetCopyrightTitle reads one entry.
func (s *Store) GetCopyrightTitle(ctx context.Context, id uuid.UUID) (*CopyrightTitle, error) {
	t, err := scanCopyrightTitle(s.db.QueryRow(ctx, `SELECT `+copyrightTitleCols+` FROM copyright_review_titles WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCopyrightTitleNotFound
	}
	return t, err
}

// titleAuditValue is the audit row's compact value: the entry's state, not
// its full text (aliases are counted, owners counted).
func titleAuditValue(t *CopyrightTitle) string {
	if t == nil {
		return ""
	}
	lang := ""
	if t.Language != nil {
		lang = *t.Language
	}
	owner := ""
	if t.RightsOwner != nil {
		owner = *t.RightsOwner
	}
	v := fmt.Sprintf("title=%q aliases=%d owners=%d language=%q active=%t auto_block=%t rights_owner=%q",
		t.Title, len(t.Aliases), len(t.OwnerChannelIDs), lang, t.Active, t.AutoBlock, owner)
	if len(v) > 1000 {
		v = v[:1000]
	}
	return v
}

func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func nonNilUUIDs(v []uuid.UUID) []uuid.UUID {
	if v == nil {
		return []uuid.UUID{}
	}
	return v
}

// ── Members-only and the hold ───────────────────────────────────────────

// editResultTier is the tier a post will carry after patch.
func editResultTier(before *Post, patch PostEditPatch) *uuid.UUID {
	switch {
	case patch.ClearTierRequired:
		return nil
	case patch.TierRequiredID != nil:
		return patch.TierRequiredID
	}
	return before.TierRequiredID
}

// PostHeldForCopyright reports whether the post has any active restriction
// (a copyright case or a title review): a members-only tier may then be
// neither set nor kept.
func (s *Store) PostHeldForCopyright(ctx context.Context, postID uuid.UUID) (bool, error) {
	var held bool
	err := s.db.QueryRow(ctx, `SELECT active_restriction_count > 0 FROM posts WHERE id = $1`, postID).Scan(&held)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrPostNotFound
	}
	return held, err
}
