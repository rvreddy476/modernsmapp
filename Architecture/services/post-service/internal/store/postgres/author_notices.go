package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// post.author_notice: something happened to a creator's own post that the
// creator must be told about, with the reason (lane post-service-2,
// 2026-11-18). Written through the outbox in the transaction that made the
// change, so a notice is never sent for a change that rolled back and never
// lost for one that committed. notification-service turns it into the
// creator's in-app (and push) notification; until its consumer ships the
// event is simply unread.
//
//	kind                    when
//	moderation_takedown     a moderation decision took the post to
//	                        'rejected' (an upheld report, an admin decision)
//	moderation_restored     a decision took a 'rejected' post back to
//	                        'approved' (an appeal overturned, or a moderator
//	                        reversed it)
//	scheduled_video_failed  a scheduled video's media failed for good: the
//	                        post left the schedule and was rejected (REG-6)
//
// notice_id is the consumer's dedupe key: the decision id for a moderation
// notice, the post id for a failed scheduled video (it happens once).
const EventPostAuthorNotice = "post.author_notice"

// The kinds on post.author_notice.
const (
	AuthorNoticeModerationTakedown   = "moderation_takedown"
	AuthorNoticeModerationRestored   = "moderation_restored"
	AuthorNoticeScheduledVideoFailed = "scheduled_video_failed"
)

// AuthorNoticePayload is the wire payload of post.author_notice.
type AuthorNoticePayload struct {
	NoticeID    string `json:"notice_id"`
	Kind        string `json:"kind"`
	PostID      string `json:"post_id"`
	AuthorID    string `json:"author_id"`
	ContentType string `json:"content_type"`
	// Title is the post's title (long video) or "" — copy may fall back to
	// "your post".
	Title string `json:"title,omitempty"`
	// Reason is the moderator's reason, shown to the author verbatim on a
	// takedown; a fixed system reason on scheduled_video_failed.
	Reason string `json:"reason"`
	// DecisionID / Source / SourceRefID identify the moderation decision
	// ("admin", "appeal" or "report"; SourceRefID is the report id on a
	// report). Empty on scheduled_video_failed.
	DecisionID  string `json:"decision_id,omitempty"`
	Source      string `json:"source,omitempty"`
	SourceRefID string `json:"source_ref_id,omitempty"`
	// Appealable is true on a takedown: the author may appeal it through
	// trust-safety (POST /v1/appeals binds the post's latest decision).
	Appealable bool      `json:"appealable"`
	OccurredAt time.Time `json:"occurred_at"`
}

// ScheduledVideoFailedReason is the reason carried on scheduled_video_failed.
const ScheduledVideoFailedReason = "The video could not be processed, so the scheduled post was not published. Upload it again to publish it."

// moderationNoticeKind names the author notice a base decision owes: a
// takedown when it newly rejects, a restore when it lifts a rejection, and
// nothing otherwise (needs_changes has its own resubmit flow; a decision
// that changed nothing owes nothing).
func moderationNoticeKind(previous, resulting string) string {
	switch {
	case resulting == "rejected" && previous != "rejected":
		return AuthorNoticeModerationTakedown
	case previous == "rejected" && resulting == "approved":
		return AuthorNoticeModerationRestored
	}
	return ""
}

// moderationSourceAllowed is the decision-source rule: an admin decision and
// an appeal outcome may take any action; an upheld report only takes the post
// down (a report cannot approve or ask for changes; lifting a report's
// takedown is an appeal or an admin decision).
func moderationSourceAllowed(source, action string) bool {
	switch source {
	case "admin", "appeal":
		return true
	case "report":
		return action == "reject"
	}
	return false
}

// readNoticePostTx reads what a notice names about the post, in tx.
func readNoticePostTx(ctx context.Context, tx pgx.Tx, postID uuid.UUID) (authorID uuid.UUID, contentType, title string, err error) {
	var t *string
	err = tx.QueryRow(ctx, `SELECT author_id, content_type, title FROM posts WHERE id = $1`, postID).
		Scan(&authorID, &contentType, &t)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", "", ErrPostRowMissing
	}
	if err != nil {
		return uuid.Nil, "", "", fmt.Errorf("read post for author notice: %w", err)
	}
	if t != nil {
		title = *t
	}
	return authorID, contentType, title, nil
}

// emitModerationNoticeTx writes the author notice for a changed decision.
func emitModerationNoticeTx(ctx context.Context, tx pgx.Tx, d *ModerationDecision, kind string) error {
	authorID, contentType, title, err := readNoticePostTx(ctx, tx, d.PostID)
	if err != nil {
		return err
	}
	payload := AuthorNoticePayload{
		NoticeID: d.DecisionID.String(), Kind: kind,
		PostID: d.PostID.String(), AuthorID: authorID.String(),
		ContentType: contentType, Title: title, Reason: d.Reason,
		DecisionID: d.DecisionID.String(), Source: d.Source,
		Appealable: kind == AuthorNoticeModerationTakedown,
		OccurredAt: time.Now().UTC(),
	}
	if d.SourceRefID != nil {
		payload.SourceRefID = d.SourceRefID.String()
	}
	return InsertOutboxEventTx(ctx, tx, EventPostAuthorNotice, "post", d.PostID, payload)
}
