package service

// Follow requests to private accounts (2026-10-24).
//
// graph-service writes two events to its outbox, in the transaction that
// creates the request or approves it, and relays them on social.events.v1 as
// canonical envelopes whose event_id is the outbox row id:
//
//	graph.follow_requested        -> follow_request          to the private account (actor: requester)
//	graph.follow_request_accepted -> follow_request_accepted to the requester       (actor: the account)
//
// The consumer (events/follow_request_consumer.go) turns each into a
// FollowRequestNotification; this file renders and delivers it through the
// ordinary resolution path (suppression, the existing `follows` preference
// category, master toggle, quiet hours), under an identity built from the
// event id and the recipient so a redelivered event cannot notify twice.

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Follow-request notification types. Both are registered in Templates and map
// to the `follows` preference category (push_follows / inapp_follows).
const (
	FollowRequestType         = "follow_request"
	FollowRequestAcceptedType = "follow_request_accepted"
)

// FollowRequestNotificationTypes lists both types, for the registry tests.
var FollowRequestNotificationTypes = []string{FollowRequestType, FollowRequestAcceptedType}

// FollowRequestEntityType is the entity a follow-request notice points at:
// the other account.
const FollowRequestEntityType = "user"

// FollowRequestsDeepLink is where a new request lands: the private account's
// pending requests, where they accept or decline.
const FollowRequestsDeepLink = "/notifications?tab=requests"

// followRequestFallbackName names an actor the profile lookup could not name.
const followRequestFallbackName = "Someone"

// FollowRequestNotification is one recipient's notice about one request.
type FollowRequestNotification struct {
	// EventID is the envelope's event id (graph's outbox row id). Required:
	// without it a redelivery could notify twice.
	EventID     string
	RecipientID uuid.UUID
	ActorID     uuid.UUID
	Type        string // FollowRequestType | FollowRequestAcceptedType
	// ActorName fills {actor} on the push; empty means "look it up".
	ActorName string
	DeepLink  string
	CreatedAt time.Time
}

// Identity keys the idempotent inbox write: one notice per event, recipient
// and type, however often Kafka redelivers the event.
func (n FollowRequestNotification) Identity() string {
	return n.EventID + ":" + n.RecipientID.String() + ":" + n.Type
}

// FollowRequestAcceptedDeepLink is the profile of the account that accepted.
func FollowRequestAcceptedDeepLink(accepterID uuid.UUID) string {
	return "/u/" + accepterID.String()
}

// RenderFollowRequestNotification renders the push copy from the registered
// template, naming the actor ("Someone" when unknown).
func RenderFollowRequestNotification(n FollowRequestNotification) RenderOverride {
	t := GetTemplate(n.Type)
	name := strings.TrimSpace(n.ActorName)
	if name == "" {
		name = followRequestFallbackName
	}
	return RenderOverride{
		Title: RenderTitle(t.TitleTemplate, map[string]string{"actor": name}),
		Body:  t.BodyTemplate,
		// Per actor, recipient and type: a re-sent request replaces the
		// earlier one on the device instead of stacking.
		CollapseKey: n.Type + ":" + n.ActorID.String() + ":" + n.RecipientID.String(),
	}
}

// followRequestTransports is the dependency set one notice rides on;
// *Service implements it and runFollowRequestNotice owns the rules, so they
// are testable without live stores.
type followRequestTransports interface {
	recipientSuppressed(ctx context.Context, userID uuid.UUID, notifType string) bool
	resolveGeneralDelivery(ctx context.Context, userID uuid.UUID, notifType string) DeliveryDecision
	followRequestActorName(ctx context.Context, userID uuid.UUID) string
	deliverWithDecision(ctx context.Context, decision DeliveryDecision, userID, actorID uuid.UUID, notifType, entityType string, entityID uuid.UUID, deepLink string, createdAt time.Time, identity string, render RenderOverride) error
}

// followRequestActorName is the display name (username as a second choice)
// the push uses; "" when unknown.
func (s *Service) followRequestActorName(ctx context.Context, userID uuid.UUID) string {
	if userID == uuid.Nil {
		return ""
	}
	return s.senderDisplayName(ctx, userID)
}

// CreateFollowRequestNotification delivers one follow-request notice.
func (s *Service) CreateFollowRequestNotification(ctx context.Context, n FollowRequestNotification) error {
	return runFollowRequestNotice(ctx, s, n)
}

func runFollowRequestNotice(ctx context.Context, t followRequestTransports, n FollowRequestNotification) error {
	// A notice to nobody, from nobody, to oneself, of an unknown type or
	// without an event id (no identity: a redelivery could notify twice) is
	// dropped.
	if n.RecipientID == uuid.Nil || n.ActorID == uuid.Nil || n.RecipientID == n.ActorID || strings.TrimSpace(n.EventID) == "" {
		return nil
	}
	if n.Type != FollowRequestType && n.Type != FollowRequestAcceptedType {
		return nil
	}
	if t.recipientSuppressed(ctx, n.RecipientID, n.Type) {
		return nil
	}
	decision := t.resolveGeneralDelivery(ctx, n.RecipientID, n.Type)
	if !decision.CreateInbox && !decision.SendWebSocket && !decision.SendPush {
		return nil
	}
	if decision.SendPush && strings.TrimSpace(n.ActorName) == "" {
		// Only the push needs the name; the inbox row hydrates at read time.
		n.ActorName = t.followRequestActorName(ctx, n.ActorID)
	}
	return t.deliverWithDecision(ctx, decision, n.RecipientID, n.ActorID, n.Type,
		FollowRequestEntityType, n.ActorID, n.DeepLink, n.CreatedAt, n.Identity(), RenderFollowRequestNotification(n))
}
