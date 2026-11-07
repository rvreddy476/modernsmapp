package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// A pending request's one first message is claimed in the same transaction
// as the delivery intent (review 7 Nov 2026: the old rule read preview = ''
// and wrote it after the message was stored, so parallel sends with
// different keys each landed a message). The SQL's row-lock semantics are
// proven against PostgreSQL in request_claim_integration_test.go; these pin
// the Go side: the claim runs first, inside the transaction, keyed by the
// send, and a claim that matched nothing reserves nothing.

func firstRequestIntent(conv uuid.UUID, key string) MessageDeliveryIntent {
	receiver := uuid.New()
	now := time.Now().UTC()
	return MessageDeliveryIntent{
		IdempotencyKey: key, RequestHash: "h", ConversationID: conv, SenderID: uuid.New(),
		MessageID: uuid.New(), Bucket: now.Format("200601"), MessageTS: now, MessageType: "text",
		MessageText: "hello", MemberIDs: []uuid.UUID{receiver}, FirstRequest: true, RequestReceiverID: &receiver,
	}
}

func TestReserveRefusesARequestWhoseFirstMessageIsTaken(t *testing.T) {
	conv := uuid.New()
	s, f := newFakeStore(t, nil)
	f.execTag = func(sql string, _ []any) string {
		if sql == claimRequestFirstMessageSQL {
			return "UPDATE 0" // another key holds the claim
		}
		return ""
	}
	_, err := s.ReserveMessageDeliveryIntent(context.Background(), firstRequestIntent(conv, "loser"))
	if !errors.Is(err, ErrRequestFirstMessageTaken) {
		t.Fatalf("err = %v, want ErrRequestFirstMessageTaken", err)
	}
	for _, c := range f.calls {
		if strings.Contains(c.sql, "INSERT INTO chat.message_delivery_intents") {
			t.Fatal("a send that lost the claim still reserved a delivery intent")
		}
	}
}

func TestReserveClaimsTheRequestBeforeTheIntent(t *testing.T) {
	conv := uuid.New()
	s, f := newFakeStore(t, nil)
	// The read-back is not faked, so Reserve ends with "disappeared"; the
	// writes before it are what this checks.
	_, _ = s.ReserveMessageDeliveryIntent(context.Background(), firstRequestIntent(conv, "winner"))
	claimAt, insertAt := -1, -1
	for i, c := range f.calls {
		switch {
		case c.sql == claimRequestFirstMessageSQL:
			claimAt = i
			if len(c.args) != 2 || c.args[0] != conv || c.args[1] != "winner" {
				t.Fatalf("claim args = %v, want the conversation and the send's key", c.args)
			}
		case strings.Contains(c.sql, "INSERT INTO chat.message_delivery_intents"):
			insertAt = i
		}
	}
	if claimAt < 0 || insertAt < 0 || claimAt > insertAt {
		t.Fatalf("claim at %d, intent insert at %d: the claim must run first", claimAt, insertAt)
	}

	// An ordinary message never touches the request row.
	s2, f2 := newFakeStore(t, nil)
	plain := firstRequestIntent(conv, "plain")
	plain.FirstRequest, plain.RequestReceiverID = false, nil
	_, _ = s2.ReserveMessageDeliveryIntent(context.Background(), plain)
	for _, c := range f2.calls {
		if c.sql == claimRequestFirstMessageSQL {
			t.Fatal("an ordinary message claimed the request's first message")
		}
	}
}

// Both paths that (re)open a request start it without a claim: a reopened
// request gets its own one message.
func TestReopeningARequestClearsTheFirstMessageClaim(t *testing.T) {
	ctx := context.Background()
	conv, a, b := uuid.New(), uuid.New(), uuid.New()
	s, f := newFakeStore(t, nil)
	if err := s.CreateMessageRequest(ctx, conv, a, b); err != nil {
		t.Fatal(err)
	}
	_, _ = upsertPendingRequest(ctx, &fakeTx{db: f}, conv, a, b) // the read-back is not faked
	if len(f.calls) != 2 {
		t.Fatalf("recorded %d statements, want the two request upserts", len(f.calls))
	}
	for _, c := range f.calls {
		if !strings.Contains(c.sql, "first_message_key = NULL") {
			t.Fatalf("request upsert keeps the old claim:\n%s", c.sql)
		}
	}
}
