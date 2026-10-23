package consumers

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/google/uuid"
)

type fakeScamApplier struct {
	calls []string
	err   error
}

func (f *fakeScamApplier) ApplyScamLinkReport(_ context.Context, _, _ uuid.UUID, targetType string, _ uuid.UUID, _ time.Time) (postgres.ActionLinkReportOutcome, error) {
	f.calls = append(f.calls, targetType)
	return postgres.ActionLinkReportOutcome{Found: true}, f.err
}

func reportEnvelope(t *testing.T, entityType, entityID, reason string) *events.EventEnvelope {
	t.Helper()
	p, _ := json.Marshal(events.ReportFiledPayload{
		ReportID: uuid.NewString(), ReporterID: uuid.NewString(), EntityType: entityType, EntityID: entityID,
		Reason: reason, CreatedAt: time.Now(),
	})
	return &events.EventEnvelope{EventID: uuid.NewString(), EventType: events.ReportFiled, Payload: p}
}

// Only scam_link reports on posts and profiles reach the threshold; every
// other report, and every other event, is ignored; a malformed one is
// Permanent; a store failure retries.
func TestActionLinkReportConsumer(t *testing.T) {
	f := &fakeScamApplier{}
	c := &ActionLinkReportConsumer{svc: f}
	ctx := context.Background()
	for _, env := range []*events.EventEnvelope{
		reportEnvelope(t, "post", uuid.NewString(), "spam"),
		reportEnvelope(t, "comment", uuid.NewString(), "scam_link"),
		reportEnvelope(t, "hashtag", uuid.NewString(), "scam_link"),
		{EventType: events.PostCreated, Payload: []byte(`{}`)},
	} {
		if err := c.handle(ctx, env); err != nil {
			t.Fatalf("ignored event errored: %v", err)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("applied %v", f.calls)
	}
	for _, typ := range []string{"post", "user", "POST"} {
		if err := c.handle(ctx, reportEnvelope(t, typ, uuid.NewString(), "SCAM_LINK")); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.calls) != 3 || f.calls[2] != "post" {
		t.Fatalf("calls = %v", f.calls)
	}
	err := c.handle(ctx, reportEnvelope(t, "post", "not-a-uuid", "scam_link"))
	if err == nil || !sharedkafka.IsPermanent(err) {
		t.Fatalf("malformed: %v, want Permanent", err)
	}
	f.err = errors.New("db down")
	if err := c.handle(ctx, reportEnvelope(t, "post", uuid.NewString(), "scam_link")); err == nil || sharedkafka.IsPermanent(err) {
		t.Fatalf("store failure: %v, want a retryable error", err)
	}
}
