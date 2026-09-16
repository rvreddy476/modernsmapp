package events

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Lane D10 — dating.match.new_message names the MATCH whichever producer
// wrote it, and its deep link carries both the match and the conversation.

type capturedNotification struct {
	recipient  uuid.UUID
	sender     uuid.UUID
	notifType  string
	entityType string
	entityID   uuid.UUID
	deepLink   string
}

type notifyCapture struct{ got []capturedNotification }

func (f *notifyCapture) CreateNotification(_ context.Context, recipient, sender uuid.UUID, notifType, entityType string,
	entityID uuid.UUID, deepLink string, _ time.Time) error {
	f.got = append(f.got, capturedNotification{recipient, sender, notifType, entityType, entityID, deepLink})
	return nil
}

func (f *notifyCapture) CreateNotificationWithoutPush(ctx context.Context, recipient, sender uuid.UUID, notifType, entityType string,
	entityID uuid.UUID, deepLink string, createdAt time.Time) error {
	return f.CreateNotification(ctx, recipient, sender, notifType, entityType, entityID, deepLink, createdAt)
}

func TestDatingNewMessageNotificationNamesTheMatch(t *testing.T) {
	match, conversation, sender, recipient := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	payload := chatDatingMessageNewPayload{
		ConversationID: conversation.String(),
		MatchID:        match.String(),
		SenderID:       sender.String(),
		RecipientID:    recipient.String(),
		SentAt:         time.Now().UTC(),
	}
	wantLink := "/dating/matches/" + match.String() + "?conversation=" + conversation.String()

	// The chat-topic bridge writes it through the shared mapping.
	cap := &notifyCapture{}
	chat := &ChatConsumer{service: cap}
	if err := chat.handleChatDatingMessageNew(context.Background(), payload); err != nil {
		t.Fatalf("chat bridge: %v", err)
	}
	if len(cap.got) != 1 {
		t.Fatalf("%d notifications, want 1", len(cap.got))
	}
	n := cap.got[0]
	if n.notifType != "dating.match.new_message" || n.entityType != DatingNewMessageEntityType || n.entityID != match {
		t.Fatalf("entity = %s/%s, want %s/%s", n.entityType, n.entityID, DatingNewMessageEntityType, match)
	}
	if n.deepLink != wantLink {
		t.Fatalf("deep link = %q, want %q", n.deepLink, wantLink)
	}
	if n.recipient != recipient || n.sender != sender {
		t.Fatalf("people = %s/%s", n.recipient, n.sender)
	}

	// The dating-topic consumer maps the same payload the same way.
	gotRecipient, gotSender, gotMatch, gotLink, ok := datingNewMessageNotification(payload)
	if !ok || gotRecipient != recipient || gotSender != sender || gotMatch != match || gotLink != wantLink {
		t.Fatalf("dating consumer mapping = %s/%s/%s/%q (ok=%v)", gotRecipient, gotSender, gotMatch, gotLink, ok)
	}
	if DatingNewMessageEntityType != "dating_match" {
		t.Fatalf("entity type = %q", DatingNewMessageEntityType)
	}
}

func TestDatingNewMessageDeepLinkEdges(t *testing.T) {
	if got := datingMatchDeepLink("m-1", ""); got != "/dating/matches/m-1" {
		t.Fatalf("deep link without a conversation = %q", got)
	}
	if got := datingMatchDeepLink("", "c-1"); got != "/dating/matches" {
		t.Fatalf("deep link without a match = %q", got)
	}
	if _, _, _, _, ok := datingNewMessageNotification(chatDatingMessageNewPayload{RecipientID: "nope"}); ok {
		t.Fatalf("an unparseable recipient produced a notification")
	}
}
