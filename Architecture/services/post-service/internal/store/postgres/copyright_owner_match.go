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
	// The author appealed the block and a reviewer is deciding it (migration
	// 076): the match is recorded in the history, nothing else changes —
	// a provider's retry must not re-block a video a human is reviewing.
	OwnerMatchUnderAppeal OwnerMatchOutcome = "under_appeal"
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
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	out, err := applyOwnerMatchTx(ctx, tx, in)
	if err != nil {
		return nil, err
	}
	return out, tx.Commit(ctx)
}

func applyOwnerMatchTx(ctx context.Context, tx pgx.Tx, in OwnerMatchInput) (*OwnerMatchResult, error) {
	// The post first (the restriction command's lock order: post, then row).
	var (
		authorID  uuid.UUID
		deleted   bool
		scheduled bool
	)
	err := tx.QueryRow(ctx, `SELECT author_id, deleted_at IS NOT NULL, publish_at IS NOT NULL FROM posts WHERE id = $1 FOR UPDATE`, in.PostID).
		Scan(&authorID, &deleted, &scheduled)
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
		return openOwnerHoldTx(ctx, tx, in, authorID, scheduled)
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
	if r.AppealedAt != nil {
		// Pending because the author appealed a block: a reviewer decides it.
		if err := insertTitleReviewEventTx(ctx, tx, r.ID, in.PostID, "owner_match", nil, r.State, r.State, ownerMatchNote(in)); err != nil {
			return nil, err
		}
		return &OwnerMatchResult{Outcome: OwnerMatchUnderAppeal, ReviewID: &id}, nil
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
// measured hold (an owner match comes after the transcode), then blocked at
// once (block) or queued with reason mute_requested (mute).
//
// Whether a reviewer's clear announces the post (announce_on_clear) is
// whether its PostCreated is still OWED: a live post was announced at its
// publication (a post with no review was never withheld by this rule), so a
// clear must not announce it twice; a SCHEDULED post (publish_at set, under
// the post lock) has not been announced at all, and the schedule worker's
// flip withholds it while this hold is active (scheduledFlipHeldTx), so the
// clear is what announces it — exactly once, through the same
// releaseTitleReviewTx path every clear takes (or the flip itself, if the
// clear comes first).
func openOwnerHoldTx(ctx context.Context, tx pgx.Tx, in OwnerMatchInput, authorID uuid.UUID, scheduled bool) (*OwnerMatchResult, error) {
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
		AnnounceOnClear: scheduled,
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
	// The hold (or block) lands on a post that may already be in feeds:
	// every viewer's cached copy goes (visibility_events.go). A scheduled
	// post owes nothing; the helper skips it.
	if err := emitVisibilityNarrowedTx(ctx, tx, in.PostID, VisibilityReasonCopyright); err != nil {
		return nil, err
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

// ErrOwnerTrackNotFound: no copyright_owner_tracks row for that post and
// entry (404 TRACK_NOT_FOUND).
var ErrOwnerTrackNotFound = errors.New("copyright owner track not found")

// OwnerTrackRemoval is what removing one track did.
type OwnerTrackRemoval struct {
	PostID  uuid.UUID `json:"post_id"`
	TitleID uuid.UUID `json:"title_id"`
	// StillTracked: another owner still tracks the post, so its earnings
	// stay excluded.
	StillTracked bool `json:"still_tracked"`
}

// RemoveCopyrightOwnerTrack deletes one tracked owner match (an admin's
// decision, migration 076) in one transaction under the post's row lock:
// the row, then a restatement of the post's eligibility — the event's
// earnings_excluded is recomputed from the tracks that remain, so the
// monetization projection counts the post again from now unless another
// owner still tracks it — and the admin audit row.
func (s *Store) RemoveCopyrightOwnerTrack(ctx context.Context, postID, titleID, actor uuid.UUID, note string) (*OwnerTrackRemoval, error) {
	if postID == uuid.Nil || titleID == uuid.Nil {
		return nil, fmt.Errorf("%w: a track names a post and an entry", ErrTitleReviewInvalid)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	out, err := removeOwnerTrackTx(ctx, tx, postID, titleID, actor, note)
	if err != nil {
		return nil, err
	}
	return out, tx.Commit(ctx)
}

func removeOwnerTrackTx(ctx context.Context, tx pgx.Tx, postID, titleID, actor uuid.UUID, note string) (*OwnerTrackRemoval, error) {
	if actor == uuid.Nil {
		return nil, ErrAdminAuditActor // never an anonymous removal
	}
	// The post first (the lock order every owner-match write takes).
	var locked bool
	err := tx.QueryRow(ctx, `SELECT TRUE FROM posts WHERE id = $1 FOR UPDATE`, postID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrOwnerTrackNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("owner track: lock post: %w", err)
	}
	var owner, reference *string
	err = tx.QueryRow(ctx, `
		DELETE FROM copyright_owner_tracks WHERE post_id = $1 AND title_id = $2
		RETURNING rights_owner, reference`, postID, titleID).Scan(&owner, &reference)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrOwnerTrackNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("owner track: remove: %w", err)
	}
	// Restate eligibility: earnings_excluded is read from the remaining rows.
	if err := BumpSearchRevAndEmitTx(ctx, tx, postID); err != nil {
		return nil, err
	}
	var still bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM copyright_owner_tracks WHERE post_id = $1)`, postID).Scan(&still); err != nil {
		return nil, err
	}
	prev := "title_id=" + titleID.String()
	if owner != nil {
		prev += fmt.Sprintf(" rights_owner=%q", *owner)
	}
	if reference != nil {
		prev += fmt.Sprintf(" reference=%q", *reference)
	}
	if err := insertAdminAudit(ctx, tx, actor, "copyright_track.remove", "copyright_track", postID, prev, "removed", note); err != nil {
		return nil, err
	}
	return &OwnerTrackRemoval{PostID: postID, TitleID: titleID, StillTracked: still}, nil
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
