package events

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/trust-safety-service/internal/service"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

// A reported message request lands ONCE in the report queue, however often
// the event arrives — chat's relay re-publishes with a fresh event_id, so
// the store's source key (the conversation) is what dedupes.

// dedupingFiler models the uq_reports_source_event index.
type dedupingFiler struct {
	rows  map[string]service.FileReportInput
	calls int
	err   error
}

func (f *dedupingFiler) FileReportWith(_ context.Context, in service.FileReportInput) (*postgres.Report, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if f.rows == nil {
		f.rows = map[string]service.FileReportInput{}
	}
	if _, dup := f.rows[in.SourceEventID]; dup {
		return nil, postgres.ErrDuplicateSourceEvent
	}
	f.rows[in.SourceEventID] = in
	return &postgres.Report{ID: uuid.New()}, nil
}

func reportedEvent(eventID string, conv, sender, receiver uuid.UUID) []byte {
	return []byte(`{"event_id":"` + eventID + `","event_type":"MessageRequestReported","payload":{"conversation_id":"` +
		conv.String() + `","sender_id":"` + sender.String() + `","receiver_id":"` + receiver.String() +
		`","preview":"hello stranger","occurred_at":"2026-10-18T09:00:00Z"}}`)
}

func TestChatReportConsumerIsIdempotent(t *testing.T) {
	f := &dedupingFiler{}
	c := &ChatReportConsumer{filer: f}
	conv, sender, receiver := uuid.New(), uuid.New(), uuid.New()
	ev := uuid.NewString()

	for i := 0; i < 2; i++ { // redelivery of the same event
		if err := c.processMessage(context.Background(), reportedEvent(ev, conv, sender, receiver)); err != nil {
			t.Fatalf("delivery %d: %v", i, err)
		}
	}
	// Re-publish by chat's relay: same report, new event id.
	if err := c.processMessage(context.Background(), reportedEvent(uuid.NewString(), conv, sender, receiver)); err != nil {
		t.Fatalf("republish: %v", err)
	}
	if len(f.rows) != 1 || f.calls != 3 {
		t.Fatalf("rows %d calls %d, want one row from three deliveries", len(f.rows), f.calls)
	}
	in := f.rows[service.MessageRequestReportSourceKey(conv)]
	if in.ReporterID != receiver || in.EntityRef != sender.String() || in.Evidence == nil || in.Evidence.Text != "hello stranger" {
		t.Fatalf("filed %+v", in)
	}

	// A different request is its own report.
	if err := c.processMessage(context.Background(), reportedEvent(uuid.NewString(), uuid.New(), sender, receiver)); err != nil {
		t.Fatal(err)
	}
	if len(f.rows) != 2 {
		t.Fatalf("rows %d, want 2", len(f.rows))
	}
}

func TestChatReportConsumerIgnoresOtherAndMalformedEvents(t *testing.T) {
	f := &dedupingFiler{}
	c := &ChatReportConsumer{filer: f}
	// Blocked / Ignored / Accepted share the reported payload shape exactly;
	// only the event type tells them apart.
	full := `{"conversation_id":"` + uuid.NewString() + `","sender_id":"` + uuid.NewString() + `","receiver_id":"` + uuid.NewString() + `"}`
	for _, raw := range []string{
		`{"event_type":"MessageRequestBlocked","payload":` + full + `}`,
		`{"event_type":"MessageRequestIgnored","payload":` + full + `}`,
		`{"event_type":"MessageRequestAccepted","payload":` + full + `}`,
		`{"event_type":"MessageCreated","payload":{}}`,
		`{"event_type":"MessageRequestReported","payload":{"conversation_id":"nope"}}`,
		`{"event_type":"MessageRequestReported","payload":"string"}`,
		`not json`,
	} {
		if err := c.processMessage(context.Background(), []byte(raw)); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
	}
	if f.calls != 0 {
		t.Fatalf("filer called %d times for non-report events", f.calls)
	}
}

func TestChatReportConsumerRetriesOnlyTransientFailures(t *testing.T) {
	conv, sender, receiver := uuid.New(), uuid.New(), uuid.New()
	for _, tc := range []struct {
		err       error
		wantRetry bool
	}{
		{errors.New("connection refused"), true},
		{postgres.ErrActiveReportExists, false},
		{postgres.ErrDuplicateSourceEvent, false},
		{service.ErrInvalidReport, false},
	} {
		c := &ChatReportConsumer{filer: &dedupingFiler{err: tc.err}}
		err := c.processMessage(context.Background(), reportedEvent(uuid.NewString(), conv, sender, receiver))
		if (err != nil) != tc.wantRetry {
			t.Errorf("%v: returned %v, want retry=%v", tc.err, err, tc.wantRetry)
		}
	}
}
