package events

// Follow requests to private accounts (2026-10-24): graph.follow_requested and
// graph.follow_request_accepted from graph-service's outbox, relayed on
// social.events.v1 (the topic this consumer already reads for UserFollowed).
// planFollowRequestNotice is pure; delivery is the service's
// CreateFollowRequestNotification (service/follow_request_notify.go).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// followRequestNotifier is the service seam; *service.Service satisfies it.
type followRequestNotifier interface {
	CreateFollowRequestNotification(ctx context.Context, n service.FollowRequestNotification) error
}

// followRequestDedupID is the envelope's event id (graph sets it to the
// outbox row id, stable across redeliveries); an envelope without one falls
// back to a hash of the message body. Never the Kafka key: graph keys by the
// canonical PAIR, so a request re-sent after a decline would share it.
func followRequestDedupID(env events.EventEnvelope, value []byte) string {
	if id := strings.TrimSpace(env.EventID); id != "" {
		return id
	}
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:16])
}

// planFollowRequestNotice turns one follow-request event into the notice it
// delivers. ok is false for a payload that names no deliverable recipient: an
// id that does not parse, or a notice to oneself.
func planFollowRequestNotice(eventType, dedupID string, raw json.RawMessage, now time.Time) (service.FollowRequestNotification, bool, error) {
	switch eventType {
	case events.GraphFollowRequested:
		var p events.FollowRequestedPayload
		if err := unmarshalPayload(raw, &p); err != nil {
			return service.FollowRequestNotification{}, false, err
		}
		// To the private account; the requester is the actor.
		n := service.FollowRequestNotification{
			EventID: dedupID, Type: service.FollowRequestType,
			DeepLink: service.FollowRequestsDeepLink, CreatedAt: orNow(p.CreatedAt, now),
		}
		return n, fillFollowRequestIDs(&n, p.TargetID, p.RequesterID), nil
	case events.GraphFollowRequestAccepted:
		var p events.FollowRequestAcceptedPayload
		if err := unmarshalPayload(raw, &p); err != nil {
			return service.FollowRequestNotification{}, false, err
		}
		// To the requester; the account that accepted is the actor.
		n := service.FollowRequestNotification{
			EventID: dedupID, Type: service.FollowRequestAcceptedType, CreatedAt: orNow(p.AcceptedAt, now),
		}
		ok := fillFollowRequestIDs(&n, p.RequesterID, p.TargetID)
		n.DeepLink = service.FollowRequestAcceptedDeepLink(n.ActorID)
		return n, ok, nil
	}
	return service.FollowRequestNotification{}, false, nil
}

// fillFollowRequestIDs parses recipient and actor onto n; false when either is
// missing or they are the same account.
func fillFollowRequestIDs(n *service.FollowRequestNotification, recipientID, actorID string) bool {
	var err error
	if n.RecipientID, err = uuid.Parse(recipientID); err != nil || n.RecipientID == uuid.Nil {
		return false
	}
	if n.ActorID, err = uuid.Parse(actorID); err != nil || n.ActorID == uuid.Nil {
		return false
	}
	return n.RecipientID != n.ActorID
}

// handleFollowRequestEvent delivers one follow-request event's notice.
func (c *Consumer) handleFollowRequestEvent(ctx context.Context, env events.EventEnvelope, value []byte) error {
	n, ok, err := planFollowRequestNotice(env.EventType, followRequestDedupID(env, value), env.Payload, time.Now())
	if err != nil {
		return err
	}
	if !ok {
		slog.Debug("follow-request event names no deliverable recipient; skipped", "event", env.EventType)
		return nil
	}
	if c.followRequestNotify == nil {
		return nil
	}
	return c.followRequestNotify.CreateFollowRequestNotification(ctx, n)
}
