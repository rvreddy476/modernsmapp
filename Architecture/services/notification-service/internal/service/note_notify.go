package service

// Echo note notifications (2026-11-12, docs/designs/echo-contract.md
// "notification-service").
//
// post-service writes note.replied and note.quoted to its outbox and relays
// them on social.events.v1:
//
//	note.replied -> note_reply  to the author of reply_to_id  ("{actor} replied to your note")
//	note.quoted  -> note_quote  to the author of quote_of_id  ("{actor} quoted your note")
//
// The consumer (events/note_consumer.go) parses the event, drops a notice to
// oneself and checks blocks in both directions; this file renders and
// delivers it through the ordinary resolution path (suppression, the
// `replies` preference category, master toggle, quiet hours) under an
// identity built from the event id and the recipient, so a redelivered event
// cannot notify twice.

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Echo note notification types. Both are registered in Templates and map to
// the `replies` preference category (push_replies / inapp_replies), the one
// comment replies use.
const (
	NoteReplyType = "note_reply"
	NoteQuoteType = "note_quote"
)

// NoteNotificationTypes lists both types, for the registry tests.
var NoteNotificationTypes = []string{NoteReplyType, NoteQuoteType}

// NoteEntityType is the entity a note notice points at: the new note (the
// reply or the quote), which is a post row. Deleting that note therefore
// matches the row like any other post entity.
const NoteEntityType = "post"

// NoteWebDeepLink is the web route of a note; it is what the inbox row and
// the realtime frame carry.
func NoteWebDeepLink(noteID uuid.UUID) string { return "/echo/note/" + noteID.String() }

// NoteAppDeepLink is the app route the device push opens.
func NoteAppDeepLink(noteID uuid.UUID) string { return "momentum://echo/note/" + noteID.String() }

// noteFallbackName names an actor the profile lookup could not name.
const noteFallbackName = "Someone"

// NoteNotification is one recipient's notice about one reply or quote.
type NoteNotification struct {
	// EventID is the envelope's event id. Required: without it a
	// redelivery could notify twice.
	EventID     string
	Type        string // NoteReplyType | NoteQuoteType
	RecipientID uuid.UUID
	ActorID     uuid.UUID
	// NoteID is the new note: the reply or the quote. A tap opens it.
	NoteID uuid.UUID
	// ParentID is the recipient's note that was replied to or quoted; it
	// keys the device collapse so a burst of replies replaces itself.
	ParentID uuid.UUID
	// ActorName fills {actor} on the push; empty means "look it up".
	ActorName string
	CreatedAt time.Time
}

// Identity keys the idempotent inbox write: one notice per event, recipient
// and type.
func (n NoteNotification) Identity() string {
	return n.EventID + ":" + n.RecipientID.String() + ":" + n.Type
}

// RenderNoteNotification renders the push copy from the registered template,
// naming the actor ("Someone" when unknown), and points the push at the app
// route.
func RenderNoteNotification(n NoteNotification) RenderOverride {
	t := GetTemplate(n.Type)
	name := strings.TrimSpace(n.ActorName)
	if name == "" {
		name = noteFallbackName
	}
	return RenderOverride{
		Title: RenderTitle(t.TitleTemplate, map[string]string{"actor": name}),
		Body:  t.BodyTemplate,
		// Per parent note, recipient and type: ten replies to one note
		// replace each other on the device instead of stacking ten.
		CollapseKey:  n.Type + ":" + n.ParentID.String() + ":" + n.RecipientID.String(),
		PushDeepLink: NoteAppDeepLink(n.NoteID),
	}
}

// noteTransports is the dependency set one notice rides on; *Service
// implements it (the same seam follow requests use) and runNoteNotice owns
// the rules, so they are testable without live stores.
type noteTransports interface {
	recipientSuppressed(ctx context.Context, userID uuid.UUID, notifType string) bool
	resolveGeneralDelivery(ctx context.Context, userID uuid.UUID, notifType string) DeliveryDecision
	followRequestActorName(ctx context.Context, userID uuid.UUID) string
	deliverWithDecision(ctx context.Context, decision DeliveryDecision, userID, actorID uuid.UUID, notifType, entityType string, entityID uuid.UUID, deepLink string, createdAt time.Time, identity string, render RenderOverride) error
}

// CreateNoteNotification delivers one Echo note notice.
func (s *Service) CreateNoteNotification(ctx context.Context, n NoteNotification) error {
	return runNoteNotice(ctx, s, n)
}

func runNoteNotice(ctx context.Context, t noteTransports, n NoteNotification) error {
	// An unknown type, or a notice without an event id (no identity: a
	// redelivery could notify twice), is dropped. Self and block rules are
	// the consumer's (events/note_consumer.go).
	if n.Type != NoteReplyType && n.Type != NoteQuoteType {
		return nil
	}
	if n.RecipientID == uuid.Nil || n.NoteID == uuid.Nil || strings.TrimSpace(n.EventID) == "" {
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
		NoteEntityType, n.NoteID, NoteWebDeepLink(n.NoteID), n.CreatedAt, n.Identity(), RenderNoteNotification(n))
}
