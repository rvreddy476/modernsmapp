package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Dating mechanic M9: delivery stamps the sender's first message in a dating
// conversation, which the call rule reads. The fixture's fake store records
// the stamp.

func (s *datingSendStore) SetDatingConversationRules(context.Context, uuid.UUID, bool, bool) error {
	return nil
}

func (s *datingSendStore) MarkMemberSent(_ context.Context, conversationID, userID uuid.UUID) error {
	if s.sent == nil {
		s.sent = map[uuid.UUID][]uuid.UUID{}
	}
	s.sent[conversationID] = append(s.sent[conversationID], userID)
	return nil
}

func (s *datingSendStore) SetDatingReceiptsUntil(context.Context, uuid.UUID, uuid.UUID, *time.Time) (bool, error) {
	return true, nil
}

func (s *datingSendStore) DatingReceiptsAllowed(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return true, nil
}

func TestDatingDeliveryStampsTheSender(t *testing.T) {
	f := newDatingFixture(t, "", true)
	f.send(t, f.userA, "m9-a")
	f.send(t, f.userB, "m9-b")
	got := f.store.sent[f.convID]
	if len(got) != 2 || got[0] != f.userA || got[1] != f.userB {
		t.Fatalf("stamped senders = %v, want [%s %s]", got, f.userA, f.userB)
	}
}

func TestPlainDeliveryStampsNothing(t *testing.T) {
	f := newDatingFixture(t, "", false)
	f.send(t, f.userA, "m9-plain")
	if len(f.store.sent) != 0 {
		t.Fatalf("a non-dating message was stamped: %v", f.store.sent)
	}
}
