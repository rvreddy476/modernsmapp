package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Owner matches (migration 075; founder decision 24 Oct 2026).
//
// A registered rights owner's match against one post — today a catalogue
// entry with auto_block, tomorrow a fingerprint provider's hit — lands here
// through service.ApplyOwnerMatch, in ONE transaction under the post's row
// lock, with what the owner asked for:
//
//	block  the post is held (if it was not) and decided BLOCKED at once by
//	       the system (systemBlockTx): reason owner_registered, no reviewer,
//	       counted by trust-safety like any upheld block, the author told
//	       in-app and by email.
//	mute   there is no audio muting yet, so the post is held for a reviewer
//	       with reason mute_requested (the author told it is in review).
//	review (internal) a block while the auto-block kill switch is off: an
//	       ordinary hold for a reviewer.
//	track  nothing is hidden: a copyright_owner_tracks row makes every
//	       search-eligibility event carry earnings_excluded, which the
//	       monetization projection reads as "earns nothing". No notice.
//
// A review a reviewer already CLEARED is left alone (a human decided it);
// one already blocked stays blocked.

// OwnerMatchPolicy is what the rights owner asked for.
type OwnerMatchPolicy string

const (
	OwnerPolicyBlock OwnerMatchPolicy = "block"
	OwnerPolicyMute  OwnerMatchPolicy = "mute"
	OwnerPolicyTrack OwnerMatchPolicy = "track"
	// OwnerPolicyReview is not an owner's request: it is a block that the
	// auto-block kill switch turned into an ordinary hold for a reviewer.
	OwnerPolicyReview OwnerMatchPolicy = "review"
)

// OwnerMatchOutcome says what an owner match did.
type OwnerMatchOutcome string

const (
	OwnerMatchBlocked         OwnerMatchOutcome = "blocked"          // the system blocked it
	OwnerMatchHeld            OwnerMatchOutcome = "held_for_review"  // mute: held for a reviewer
	OwnerMatchTracked         OwnerMatchOutcome = "tracked"          // earnings excluded, nothing hidden
	OwnerMatchAlreadyBlocked  OwnerMatchOutcome = "already_blocked"  // nothing to do
	OwnerMatchAlreadyTracked  OwnerMatchOutcome = "already_tracked"  // nothing to do
	OwnerMatchReviewerCleared OwnerMatchOutcome = "reviewer_cleared" // a reviewer cleared it: left alone
	OwnerMatchPostDeleted     OwnerMatchOutcome = "post_deleted"     // nothing to do
	// Decided by the service before the store is asked:
	OwnerMatchOwnerUpload   OwnerMatchOutcome = "owner_upload"   // the uploader's channel owns the title
	OwnerMatchEntryInactive OwnerMatchOutcome = "entry_inactive" // the entry is switched off
)

// MaxOwnerMatchReference bounds the provider's match reference.
const MaxOwnerMatchReference = 200

// OwnerMatchInput is one validated owner match.
type OwnerMatchInput struct {
	PostID       uuid.UUID
	TitleID      uuid.UUID // the owner's registered catalogue entry
	MatchedTitle string    // the entry's title, for the review and the notices
	RightsOwner  *string   // the entry's owner (required for block)
	Reference    string    // the provider's match id, for the history row ("" = none)
	Policy       OwnerMatchPolicy
}

func (in OwnerMatchInput) validate() error {
	if in.PostID == uuid.Nil || in.TitleID == uuid.Nil || strings.TrimSpace(in.MatchedTitle) == "" {
		return fmt.Errorf("%w: owner match names no post, entry or title", ErrTitleReviewInvalid)
	}
	switch in.Policy {
	case OwnerPolicyBlock, OwnerPolicyMute, OwnerPolicyTrack, OwnerPolicyReview:
	default:
		return fmt.Errorf("%w: owner policy %q", ErrTitleReviewInvalid, in.Policy)
	}
	if in.Policy == OwnerPolicyBlock && (in.RightsOwner == nil || strings.TrimSpace(*in.RightsOwner) == "") {
		return fmt.Errorf("%w: a block names the rights owner", ErrTitleReviewInvalid)
	}
	if utf8.RuneCountInString(in.Reference) > MaxOwnerMatchReference {
		return fmt.Errorf("%w: reference is longer than %d characters", ErrTitleReviewInvalid, MaxOwnerMatchReference)
	}
	return nil
}

// OwnerMatchResult is what ApplyOwnerMatch did.
type OwnerMatchResult struct {
	Outcome  OwnerMatchOutcome `json:"outcome"`
	ReviewID *uuid.UUID        `json:"review_id,omitempty"`
}

// CopyrightMatchPostAuthor reads a post's author (the service judges the
// owner-channel exemption before any write). ErrPostNotFound when absent.
func (s *Store) CopyrightMatchPostAuthor(ctx context.Context, postID uuid.UUID) (uuid.UUID, error) {
	var author uuid.UUID
	err := s.db.QueryRow(ctx, `SELECT author_id FROM posts WHERE id = $1`, postID).Scan(&author)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrPostNotFound
	}
	return author, err
}

// ApplyOwnerMatch applies one owner match in a transaction.
func (s *Store) ApplyOwnerMatch(ctx context.Context, in OwnerMatchInput) (*OwnerMatchResult, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	out, err := applyOwnerMatchTx(ctx, tx, in)
	if err != nil {
		return nil, err
	}
	return out, tx.Commit(ctx)
}

func applyOwnerMatchTx(ctx context.Context, tx pgx.Tx, in OwnerMatchInput) (*OwnerMatchResult, error) {
	// The post first (the restriction command's lock order: post, then row).
	var (
		authorID uuid.UUID
		deleted  bool
	)
	err := tx.QueryRow(ctx, `SELECT author_id, deleted_at IS NOT NULL FROM posts WHERE id = $1 FOR UPDATE`, in.PostID).
		Scan(&authorID, &deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPostNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("owner match: lock post: %w", err)
	}
	if deleted {
		return &OwnerMatchResult{Outcome: OwnerMatchPostDeleted}, nil
	}
	if in.Policy == OwnerPolicyTrack {
		return applyOwnerTrackTx(ctx, tx, in)
	}

	r, err := scanTitleReview(tx.QueryRow(ctx, `SELECT `+titleReviewCols+` FROM copyright_title_reviews WHERE post_id = $1 FOR UPDATE`, in.PostID))
	if errors.Is(err, pgx.ErrNoRows) {
		return openOwnerHoldTx(ctx, tx, in, authorID)
	}
	if err != nil {
		return nil, err
	}
	id := r.ID
	switch r.State {
	case TitleReviewBlocked:
		return &OwnerMatchResult{Outcome: OwnerMatchAlreadyBlocked, ReviewID: &id}, nil
	case TitleReviewCleared:
		return &OwnerMatchResult{Outcome: OwnerMatchReviewerCleared, ReviewID: &id}, nil
	}
	// Pending: the owner's request decides it now (block) or says why a
	// reviewer is deciding it (mute).
	if err := insertTitleReviewEventTx(ctx, tx, r.ID, in.PostID, "owner_match", nil, r.State, r.State, ownerMatchNote(in)); err != nil {
		return nil, err
	}
	if in.Policy == OwnerPolicyBlock {
		if _, err := tx.Exec(ctx, `UPDATE copyright_title_reviews SET rights_owner = COALESCE(rights_owner, $2) WHERE id = $1`,
			r.ID, in.RightsOwner); err != nil {
			return nil, err
		}
		if err := systemBlockTx(ctx, tx, r.ID, in.PostID, r.RestrictionID, r.State); err != nil {
			return nil, err
		}
		return &OwnerMatchResult{Outcome: OwnerMatchBlocked, ReviewID: &id}, nil
	}
	var reason *string
	if in.Policy == OwnerPolicyMute {
		mute := TitleReviewReasonMuteRequested
		reason = &mute
	}
	if _, err := tx.Exec(ctx, `
		UPDATE copyright_title_reviews
		SET reason = COALESCE(reason, $2), rights_owner = COALESCE(rights_owner, $3), updated_at = now()
		WHERE id = $1`, r.ID, reason, in.RightsOwner); err != nil {
		return nil, err
	}
	return &OwnerMatchResult{Outcome: OwnerMatchHeld, ReviewID: &id}, nil
}

// openOwnerHoldTx holds a post that has no review yet, for the owner: a
// measured hold (an owner match comes after the transcode), never announced
// on a clear (the post was published and announced already), then blocked
// at once (block) or queued with reason mute_requested (mute).
func openOwnerHoldTx(ctx context.Context, tx pgx.Tx, in OwnerMatchInput, authorID uuid.UUID) (*OwnerMatchResult, error) {
	var durationSec int
	if err := tx.QueryRow(ctx, `
		SELECT (COALESCE(MAX(COALESCE(ma.duration_ms, ma.duration_seconds * 1000, 0)), 0) / 1000)::int
		FROM media_assets ma JOIN post_media pm ON pm.media_id = ma.id
		WHERE pm.post_id = $1 AND pm.kind = 'video'`, in.PostID).Scan(&durationSec); err != nil {
		return nil, fmt.Errorf("owner match: duration: %w", err)
	}
	hold := &TitleReviewHold{
		ReviewID: uuid.New(), TitleID: in.TitleID, MatchedTitle: in.MatchedTitle, MatchedTerm: in.MatchedTitle,
		MatchedField: MatchedFieldOwnerMatch, DurationSeconds: &durationSec, RightsOwner: in.RightsOwner,
	}
	outcome := OwnerMatchHeld
	switch in.Policy {
	case OwnerPolicyBlock:
		hold.AutoBlock, outcome = true, OwnerMatchBlocked
	case OwnerPolicyMute:
		hold.Reason = TitleReviewReasonMuteRequested
	}
	placed, err := insertTitleReviewHoldTx(ctx, tx, in.PostID, authorID, hold)
	if err != nil {
		return nil, err
	}
	if !placed {
		// Unreachable under the post lock (the review was read as absent);
		// fail rather than report a hold that was not placed.
		return nil, fmt.Errorf("owner match: a review appeared for post %s", in.PostID)
	}
	state := TitleReviewPending
	if hold.AutoBlock {
		state = TitleReviewBlocked
	}
	if err := insertTitleReviewEventTx(ctx, tx, hold.ReviewID, in.PostID, "owner_match", nil, state, state, ownerMatchNote(in)); err != nil {
		return nil, err
	}
	id := hold.ReviewID
	return &OwnerMatchResult{Outcome: outcome, ReviewID: &id}, nil
}

// applyOwnerTrackTx records the track and, when it is new, restates the
// post's eligibility so the monetization projection stops its earnings
// from now. Nothing is hidden and nobody is told.
func applyOwnerTrackTx(ctx context.Context, tx pgx.Tx, in OwnerMatchInput) (*OwnerMatchResult, error) {
	tag, err := tx.Exec(ctx, `
		INSERT INTO copyright_owner_tracks (post_id, title_id, rights_owner, reference)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (post_id, title_id) DO NOTHING`,
		in.PostID, in.TitleID, in.RightsOwner, nilIfBlank(in.Reference))
	if err != nil {
		return nil, fmt.Errorf("owner match: track: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return &OwnerMatchResult{Outcome: OwnerMatchAlreadyTracked}, nil
	}
	if err := BumpSearchRevAndEmitTx(ctx, tx, in.PostID); err != nil {
		return nil, err
	}
	return &OwnerMatchResult{Outcome: OwnerMatchTracked}, nil
}

// ownerMatchNote is the history row's note: the policy and the provider's
// reference (never the owner's private data).
func ownerMatchNote(in OwnerMatchInput) string {
	note := "policy=" + string(in.Policy)
	if ref := strings.TrimSpace(in.Reference); ref != "" {
		note += " reference=" + ref
	}
	return note
}
