package events

// Copyright title review (2026-10-24): post.copyright_review.changed from
// post-service's outbox, on the topic this consumer already reads for
// PostCreated. planCopyrightNotice is pure; delivery is the service's
// CreateCopyrightReviewNotification (copyright_notify.go).

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// copyrightNotifier is the service seam; *service.Service satisfies it.
type copyrightNotifier interface {
	CreateCopyrightReviewNotification(ctx context.Context, n service.CopyrightReviewNotification) error
}

// planCopyrightNotice turns one review event into the author's notice. ok is
// false for a state with no notice or a payload that names no author,
// review or post.
func planCopyrightNotice(raw json.RawMessage, now time.Time) (service.CopyrightReviewNotification, bool, error) {
	var p events.PostCopyrightReviewPayload
	if err := unmarshalPayload(raw, &p); err != nil {
		return service.CopyrightReviewNotification{}, false, err
	}
	n := service.CopyrightReviewNotification{MatchedTitle: p.MatchedTitle, PostTitle: p.PostTitle,
		RightsOwner: p.RightsOwner, Reason: p.Reason, CreatedAt: orNow(p.OccurredAt, now)}
	switch p.State {
	case events.CopyrightReviewPending:
		n.Type, n.DeepLink = service.CopyrightTypeHold, service.CopyrightHubDeepLink
	case events.CopyrightReviewCleared:
		n.Type = service.CopyrightTypeCleared
		n.DeepLink = service.UploadDeepLink(p.ContentType, p.PostID)
		if n.DeepLink == "" {
			n.DeepLink = service.CopyrightHubDeepLink
		}
	case events.CopyrightReviewBlocked:
		n.Type, n.DeepLink = service.CopyrightTypeBlocked, service.CopyrightHubDeepLink
	default:
		return n, false, nil
	}
	var err error
	if n.RecipientID, err = uuid.Parse(p.AuthorID); err != nil || n.RecipientID == uuid.Nil {
		return n, false, nil
	}
	if n.ReviewID, err = uuid.Parse(p.ReviewID); err != nil || n.ReviewID == uuid.Nil {
		return n, false, nil
	}
	if n.PostID, err = uuid.Parse(p.PostID); err != nil || n.PostID == uuid.Nil {
		return n, false, nil
	}
	return n, true, nil
}

// handleCopyrightReviewEvent delivers one review event's notice.
func (c *Consumer) handleCopyrightReviewEvent(ctx context.Context, raw json.RawMessage) error {
	n, ok, err := planCopyrightNotice(raw, time.Now())
	if err != nil {
		return err
	}
	if !ok {
		slog.Debug("copyright review event names no deliverable notice; skipped")
		return nil
	}
	if c.copyrightNotify == nil {
		return nil
	}
	return c.copyrightNotify.CreateCopyrightReviewNotification(ctx, n)
}
