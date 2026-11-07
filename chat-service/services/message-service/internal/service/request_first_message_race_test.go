package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
)

// claimLostStore plays the store side of a request whose one first message
// another send claimed while this one was past the preview check (parallel
// sends with different idempotency keys).
type claimLostStore struct{ *datingSendStore }

func (s claimLostStore) ReserveMessageDeliveryIntent(ctx context.Context, intent postgres.MessageDeliveryIntent) (*postgres.MessageDeliveryIntent, error) {
	if intent.FirstRequest {
		return nil, postgres.ErrRequestFirstMessageTaken
	}
	return s.datingSendStore.ReserveMessageDeliveryIntent(ctx, intent)
}

func pendingRequestFixture(t *testing.T) *datingFixture {
	t.Helper()
	f := newDatingFixture(t, "", false)
	f.store.conversations[f.convID].IsRequest = true
	f.store.latestRequest = &postgres.MessageRequest{
		ConversationID: f.convID, SenderID: f.userA, ReceiverID: f.userB,
		Status: "pending", ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	return f
}

func TestRequestFirstMessageReservesWithTheClaim(t *testing.T) {
	f := pendingRequestFixture(t)
	f.send(t, f.userA, "first")
	intent := f.store.intents["first"]
	if intent == nil || !intent.FirstRequest {
		t.Fatalf("the request's first message reserved %+v, want a FirstRequest intent (the store claims on it)", intent)
	}
}

func TestRequestSendThatLostTheClaimIsRefusedLikeASecondMessage(t *testing.T) {
	f := pendingRequestFixture(t)
	f.svc.convStore = claimLostStore{f.store}
	_, err := f.svc.SendMessage(context.Background(), f.userA, f.convID, MessageTypeText, "hello", nil, nil, nil, "racer")
	if !errors.Is(err, ErrAwaitingRequestAcceptance) {
		t.Fatalf("err = %v, want ErrAwaitingRequestAcceptance (the answer a second request message gets)", err)
	}
	if len(f.store.intents) != 0 {
		t.Fatalf("a send that lost the claim reserved %d intents", len(f.store.intents))
	}
}
