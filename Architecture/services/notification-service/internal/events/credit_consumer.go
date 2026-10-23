package events

// Video credits (2026-10-23): post.credit.requested and post.credit.accepted
// from post-service's outbox, on the topic this consumer already reads for
// PostCreated. planCreditNotice is pure; delivery is the service's
// CreateCreditNotification (credit_notify.go).

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// creditNotifier is the service seam; *service.Service satisfies it.
type creditNotifier interface {
	CreateCreditNotification(ctx context.Context, n service.CreditNotification) error
}

// planCreditNotice turns one credit event into the notice it delivers. ok is
// false for a payload that names no deliverable recipient: an id that does
// not parse, or a notice to oneself (a self-credit never asks).
func planCreditNotice(eventType string, raw json.RawMessage, now time.Time) (service.CreditNotification, bool, error) {
	switch eventType {
	case events.EventPostCreditRequested:
		var p events.PostCreditRequestedPayload
		if err := unmarshalPayload(raw, &p); err != nil {
			return service.CreditNotification{}, false, err
		}
		n := service.CreditNotification{
			Type: service.CreditTypeRequest, Name: p.Name, Role: p.Role, PostTitle: p.PostTitle,
			UploaderName: p.UploaderName, DeepLink: service.CreditRequestsDeepLink, CreatedAt: orNow(p.CreatedAt, now),
		}
		return n, fillCreditIDs(&n, p.CreditID, p.PostID, p.TaggedUserID, p.UploaderID), nil
	case events.EventPostCreditAccepted:
		var p events.PostCreditAcceptedPayload
		if err := unmarshalPayload(raw, &p); err != nil {
			return service.CreditNotification{}, false, err
		}
		n := service.CreditNotification{
			Type: service.CreditTypeAccepted, Name: p.Name, Role: p.Role, PostTitle: p.PostTitle,
			DeepLink: service.UploadDeepLink("long_video", p.PostID), CreatedAt: orNow(p.AcceptedAt, now),
		}
		return n, fillCreditIDs(&n, p.CreditID, p.PostID, p.UploaderID, p.TaggedUserID), nil
	}
	return service.CreditNotification{}, false, nil
}

// fillCreditIDs parses the four ids onto n; false when any is missing or the
// recipient is the actor.
func fillCreditIDs(n *service.CreditNotification, creditID, postID, recipientID, actorID string) bool {
	var err error
	if n.CreditID, err = uuid.Parse(creditID); err != nil || n.CreditID == uuid.Nil {
		return false
	}
	if n.PostID, err = uuid.Parse(postID); err != nil || n.PostID == uuid.Nil {
		return false
	}
	if n.RecipientID, err = uuid.Parse(recipientID); err != nil || n.RecipientID == uuid.Nil {
		return false
	}
	if n.ActorID, err = uuid.Parse(actorID); err != nil || n.ActorID == uuid.Nil {
		return false
	}
	return n.RecipientID != n.ActorID
}

func orNow(t, now time.Time) time.Time {
	if t.IsZero() {
		return now
	}
	return t
}

// handleCreditEvent delivers one credit event's notice.
func (c *Consumer) handleCreditEvent(ctx context.Context, eventType string, raw json.RawMessage) error {
	n, ok, err := planCreditNotice(eventType, raw, time.Now())
	if err != nil {
		return err
	}
	if !ok {
		slog.Debug("credit event names no deliverable recipient; skipped", "event", eventType)
		return nil
	}
	if c.creditNotify == nil {
		return nil
	}
	return c.creditNotify.CreateCreditNotification(ctx, n)
}
