package events

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

type fakePremiumMailer struct {
	mu      sync.Mutex
	got     []service.PremiumInvoiceEvent
	failFor int
}

func (f *fakePremiumMailer) QueuePremiumInvoiceEmail(_ context.Context, ev service.PremiumInvoiceEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failFor > 0 {
		f.failFor--
		return errors.New("queue down")
	}
	f.got = append(f.got, ev)
	return nil
}

func runPremiumInvoiceConsumer(t *testing.T, msgs []kafka.Message, mail *fakePremiumMailer) *fakeMembershipReader {
	t.Helper()
	r := &fakeMembershipReader{msgs: msgs, drained: make(chan struct{}, 1)}
	c := &PremiumInvoiceConsumer{reader: r, mail: mail}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Start(ctx); close(done) }()
	select {
	case <-r.drained:
	case <-time.After(10 * time.Second):
		t.Fatal("consumer did not drain the partition")
	}
	cancel()
	<-done
	return r
}

func piPayload(user, inv uuid.UUID, kind string) map[string]any {
	return map[string]any{"user_id": user.String(), "invoice_id": inv.String(), "pass_id": uuid.NewString(), "kind": kind}
}

// Both document events are queued with the event's own buyer; garbage, a
// payload without a buyer, a kind that contradicts its type and other topic
// events are committed and skipped.
func TestPremiumInvoiceConsumerQueuesBothKinds(t *testing.T) {
	user, inv, cn := uuid.New(), uuid.New(), uuid.New()
	contradictory := piPayload(user, inv, service.PremiumKindCreditNote)
	noUser := piPayload(uuid.Nil, inv, service.PremiumKindInvoice)
	bad := msEnvelope(t, service.EventPremiumInvoiceIssued, "evt-bad", nil)
	bad.Payload = json.RawMessage(`{"user_id": 7}`)
	msgs := []kafka.Message{
		{Offset: 0, Value: []byte("not json")},
		msMessage(t, bad, 1),
		msMessage(t, msEnvelope(t, service.EventPremiumInvoiceIssued, "evt-x", contradictory), 2),
		msMessage(t, msEnvelope(t, service.EventPremiumInvoiceIssued, "evt-y", noUser), 3),
		msMessage(t, msEnvelope(t, EventMembershipStarted, "evt-m", msPayload(nil)), 4),
		msMessage(t, msEnvelope(t, service.EventPremiumInvoiceIssued, "evt-inv", piPayload(user, inv, service.PremiumKindInvoice)), 5),
		msMessage(t, msEnvelope(t, service.EventPremiumCreditNoteIssued, "evt-cn", piPayload(user, cn, service.PremiumKindCreditNote)), 6),
	}
	mail := &fakePremiumMailer{}
	r := runPremiumInvoiceConsumer(t, msgs, mail)
	if len(r.committed) != 7 {
		t.Fatalf("committed %v", r.committed)
	}
	if len(mail.got) != 2 {
		t.Fatalf("queued %+v", mail.got)
	}
	if g := mail.got[0]; g.EventType != service.EventPremiumInvoiceIssued || g.UserID != user || g.InvoiceID != inv || g.Kind != service.PremiumKindInvoice {
		t.Fatalf("invoice event: %+v", g)
	}
	if g := mail.got[1]; g.EventType != service.EventPremiumCreditNoteIssued || g.UserID != user || g.InvoiceID != cn || g.Kind != service.PremiumKindCreditNote {
		t.Fatalf("credit note event: %+v", g)
	}
}

// A queue write that fails is retried before the offset commits.
func TestPremiumInvoiceConsumerRetriesQueueErrors(t *testing.T) {
	mail := &fakePremiumMailer{failFor: 1}
	r := runPremiumInvoiceConsumer(t, []kafka.Message{
		msMessage(t, msEnvelope(t, service.EventPremiumInvoiceIssued, "evt-inv", piPayload(uuid.New(), uuid.New(), service.PremiumKindInvoice)), 9),
	}, mail)
	if len(mail.got) != 1 || len(r.committed) != 1 || r.committed[0] != 9 {
		t.Fatalf("queued %d, committed %v", len(mail.got), r.committed)
	}
}
