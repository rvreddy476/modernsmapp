package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// post.visibility_changed for every narrowing (2026-10-30).
//
// feed-service caches hydrated posts per viewer and reads this event as
// "this post is no longer what the caches hold: drop it for every viewer".
// It was emitted only for an owner's visibility edit (post_edit.go), so a
// moderator's takedown, a copyright hold or a new members-only gate left
// the old body in every viewer's cache until it expired.
//
// The event goes through the outbox in the transaction that narrows the
// post, like every other post event. Its payload is the shared
// events.PostVisibilityChangedPayload plus `reason`, which says WHY:
//
//	reason           when                                  old/new visibility
//	audience         the owner edited the visibility       the edit's before / after
//	moderation       a takedown (ModeratePost) or a        both the post's current one
//	                 safety restriction placed
//	copyright        a copyright or title-review hold      both the post's current one
//	                 placed on a live post (rights-owner
//	                 block included)
//	membership_gate  an edit or PUT /membership added      both the post's current one
//	                 or changed the members-only tier
//
// Only `audience` changes the audience; the others leave the visibility
// value alone and narrow who may read the post by other means, so a
// consumer must not re-fan-out on them. A deleted post (PostDeleted already
// covers it) and a scheduled one (never fanned out, nothing cached) owe no
// event.

// The reason values on post.visibility_changed.
const (
	VisibilityReasonAudience       = "audience"
	VisibilityReasonModeration     = "moderation"
	VisibilityReasonCopyright      = "copyright"
	VisibilityReasonMembershipGate = "membership_gate"
)

// PostVisibilityChangedEvent is the wire payload of post.visibility_changed:
// the shared payload's fields, flattened, plus reason.
type PostVisibilityChangedEvent struct {
	events.PostVisibilityChangedPayload
	Reason string `json:"reason,omitempty"`
}

// visibilityEventOwed: a live, published post. A deleted one has left the
// feeds through PostDeleted; a scheduled one was never fanned out.
func visibilityEventOwed(deleted bool, publishAt *time.Time) bool {
	return !deleted && publishAt == nil
}

// moderationNarrows: a base decision that takes a VISIBLE (approved) post
// to a status no viewer but the author may read. A decision on a post that
// was already hidden (pending, flagged, needs_changes, rejected) narrows
// nothing a cache can hold.
func moderationNarrows(previous, resulting string) bool {
	return previous == "approved" && resulting != "approved"
}

// restrictionNarrows: a restriction command that made a hold active.
func restrictionNarrows(changed bool, next string) bool {
	return changed && next == RestrictionStateActive
}

// restrictionReason names a hold's source on the event.
func restrictionReason(source string) string {
	switch source {
	case RestrictionSourceCopyright, RestrictionSourceTitleReview:
		return VisibilityReasonCopyright
	default:
		return VisibilityReasonModeration
	}
}

// gateNarrows: the members-only tier was added or changed (a cleared gate
// widens, and owes nothing).
func gateNarrows(before, after *uuid.UUID) bool {
	if after == nil {
		return false
	}
	return before == nil || *before != *after
}

// narrowedVisibilityEvent builds the payload for a narrowing that left the
// visibility value as it was (every reason but audience).
func narrowedVisibilityEvent(postID, authorID uuid.UUID, contentType, visibility string, createdAt, now time.Time, channelID, reason string) PostVisibilityChangedEvent {
	return PostVisibilityChangedEvent{
		PostVisibilityChangedPayload: events.PostVisibilityChangedPayload{
			PostID:        postID.String(),
			AuthorID:      authorID.String(),
			ContentType:   contentType,
			OldVisibility: visibility,
			NewVisibility: visibility,
			CreatedAt:     createdAt,
			ChangedAt:     now,
			ChannelID:     channelID,
		},
		Reason: reason,
	}
}

// emitVisibilityNarrowedTx writes post.visibility_changed for postID with
// reason inside tx, reading the row as this transaction sees it. Nothing is
// written for a deleted or scheduled post.
func emitVisibilityNarrowedTx(ctx context.Context, tx pgx.Tx, postID uuid.UUID, reason string) error {
	var (
		authorID    uuid.UUID
		contentType string
		visibility  string
		createdAt   time.Time
		deleted     bool
		publishAt   *time.Time
	)
	err := tx.QueryRow(ctx, `
		SELECT author_id, content_type, visibility, created_at, deleted_at IS NOT NULL, publish_at
		FROM posts WHERE id = $1`, postID).
		Scan(&authorID, &contentType, &visibility, &createdAt, &deleted, &publishAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrPostRowMissing
	}
	if err != nil {
		return fmt.Errorf("read post for visibility event: %w", err)
	}
	if !visibilityEventOwed(deleted, publishAt) {
		return nil
	}
	payload := narrowedVisibilityEvent(postID, authorID, contentType, visibility, createdAt, time.Now().UTC(),
		channelIDForAuthorTx(ctx, tx, authorID), reason)
	if err := InsertOutboxEventTx(ctx, tx, events.EventPostVisibilityChanged, "post", postID, payload); err != nil {
		return fmt.Errorf("emit visibility change (%s): %w", reason, err)
	}
	return nil
}
