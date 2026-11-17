package events

// Post author notices (reels-29, REG-6): post.author_notice from
// post-service's outbox (store/postgres/author_notices.go there), on the
// topic this consumer already reads for PostCreated. planAuthorNotice is
// pure; delivery is the service's CreateAuthorNotice (author_notice.go).
// The event is handled on the read loop and its offset committed only after
// delivery succeeded (commitsAfterProcessing, consumer.go): an error here is
// retried, and notice_id makes the redelivery a no-op.

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/google/uuid"
)

// EventPostAuthorNotice is post-service's event type (EventPostAuthorNotice
// in post-service's author_notices.go).
const EventPostAuthorNotice = "post.author_notice"

// postAuthorNoticePayload is the wire payload of post.author_notice
// (post-service AuthorNoticePayload).
type postAuthorNoticePayload struct {
	NoticeID    string    `json:"notice_id"`
	Kind        string    `json:"kind"`
	PostID      string    `json:"post_id"`
	AuthorID    string    `json:"author_id"`
	ContentType string    `json:"content_type"`
	Title       string    `json:"title"`
	Reason      string    `json:"reason"`
	DecisionID  string    `json:"decision_id"`
	Source      string    `json:"source"`
	SourceRefID string    `json:"source_ref_id"`
	Appealable  bool      `json:"appealable"`
	OccurredAt  time.Time `json:"occurred_at"`
}

// authorNoticeTypes maps post-service's kinds onto notification types.
var authorNoticeTypes = map[string]string{
	"moderation_takedown":    service.AuthorNoticeTypeTakedown,
	"moderation_restored":    service.AuthorNoticeTypeRestored,
	"scheduled_video_failed": service.AuthorNoticeTypeScheduledFailed,
}

// authorNoticeNotifier is the service seam; *service.Service satisfies it.
type authorNoticeNotifier interface {
	CreateAuthorNotice(ctx context.Context, n service.AuthorNotice) error
}

// planAuthorNotice turns one event into the author's notice. ok is false
// for an unknown kind or a payload that names no notice, post or author.
func planAuthorNotice(raw json.RawMessage, now time.Time) (service.AuthorNotice, bool, error) {
	var p postAuthorNoticePayload
	if err := unmarshalPayload(raw, &p); err != nil {
		return service.AuthorNotice{}, false, err
	}
	typ, known := authorNoticeTypes[p.Kind]
	if !known {
		return service.AuthorNotice{}, false, nil
	}
	n := service.AuthorNotice{
		Type: typ, ContentType: p.ContentType, Title: p.Title, Reason: p.Reason,
		Appealable: p.Appealable, CreatedAt: orNow(p.OccurredAt, now),
	}
	var err error
	if n.RecipientID, err = uuid.Parse(p.AuthorID); err != nil || n.RecipientID == uuid.Nil {
		return n, false, nil
	}
	if n.NoticeID, err = uuid.Parse(p.NoticeID); err != nil || n.NoticeID == uuid.Nil {
		return n, false, nil
	}
	if n.PostID, err = uuid.Parse(p.PostID); err != nil || n.PostID == uuid.Nil {
		return n, false, nil
	}
	n.DeepLink = service.AuthorNoticeDeepLink(n.PostID)
	return n, true, nil
}

// handleAuthorNoticeEvent delivers one author notice.
func (c *Consumer) handleAuthorNoticeEvent(ctx context.Context, raw json.RawMessage) error {
	n, ok, err := planAuthorNotice(raw, time.Now())
	if err != nil {
		// A payload that does not decode never will: skipped, so the
		// commit-after-success loop is not held by it forever.
		slog.Error("post author notice payload undecodable; skipped", "error", err)
		return nil
	}
	if !ok {
		slog.Debug("post author notice names no deliverable notice; skipped")
		return nil
	}
	if c.authorNotify == nil {
		return nil
	}
	return c.authorNotify.CreateAuthorNotice(ctx, n)
}
