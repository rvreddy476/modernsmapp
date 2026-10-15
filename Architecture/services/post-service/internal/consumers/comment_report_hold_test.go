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

type appliedReport struct{ report, comment, reporter uuid.UUID }

type fakeCommentReportApplier struct {
	calls []appliedReport
	out   postgres.CommentReportOutcome
	err   error
}

func (f *fakeCommentReportApplier) ApplyTrustSafetyCommentReport(_ context.Context, reportID, commentID, reporterID uuid.UUID) (postgres.CommentReportOutcome, error) {
	f.calls = append(f.calls, appliedReport{reportID, commentID, reporterID})
	return f.out, f.err
}

// reportFiledEnvelope builds exactly what trust-safety publishes: the
// shared payload plus its entity_key extension.
func reportFiledEnvelope(t *testing.T, eventType, entityType, entityID, reportID, reporterID string) *events.EventEnvelope {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"report_id":   reportID,
		"reporter_id": reporterID,
		"entity_type": entityType,
		"entity_id":   entityID,
		"entity_key":  "",
		"reason":      "harassment",
		"created_at":  time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return &events.EventEnvelope{EventID: uuid.NewString(), EventType: eventType, Payload: payload}
}

func TestCommentReportHoldAppliesCommentReportsOnly(t *testing.T) {
	report, comment, reporter := uuid.New(), uuid.New(), uuid.New()
	f := &fakeCommentReportApplier{out: postgres.CommentReportOutcome{Found: true, Counted: true}}
	c := &CommentReportHoldConsumer{svc: f}
	ctx := context.Background()

	if err := c.handle(ctx, reportFiledEnvelope(t, events.ReportFiled, "comment", comment.String(), report.String(), reporter.String())); err != nil {
		t.Fatalf("comment report: %v", err)
	}
	if len(f.calls) != 1 || f.calls[0] != (appliedReport{report, comment, reporter}) {
		t.Fatalf("applied %+v, want the report, comment and reporter from the event", f.calls)
	}

	// Not ours: other entity types, other events.
	for _, env := range []*events.EventEnvelope{
		reportFiledEnvelope(t, events.ReportFiled, "post", uuid.NewString(), uuid.NewString(), uuid.NewString()),
		reportFiledEnvelope(t, events.ReportFiled, "hashtag", uuid.NewString(), uuid.NewString(), uuid.NewString()),
		reportFiledEnvelope(t, events.ReportFiled, "sound", uuid.NewString(), uuid.NewString(), uuid.NewString()),
		reportFiledEnvelope(t, events.ReportResolved, "comment", uuid.NewString(), uuid.NewString(), uuid.NewString()),
		reportFiledEnvelope(t, events.UserUnfollowed, "comment", uuid.NewString(), uuid.NewString(), uuid.NewString()),
	} {
		if err := c.handle(ctx, env); err != nil {
			t.Fatalf("%s/%s: %v", env.EventType, env.Payload, err)
		}
	}
	if len(f.calls) != 1 {
		t.Fatalf("applied %d reports, want only the comment one", len(f.calls))
	}
}

func TestCommentReportHoldPoisonIsPermanentAndFailuresRetry(t *testing.T) {
	ctx := context.Background()
	f := &fakeCommentReportApplier{}
	c := &CommentReportHoldConsumer{svc: f}

	bad := []*events.EventEnvelope{
		{EventID: "x", EventType: events.ReportFiled, Payload: json.RawMessage(`{not json`)},
		reportFiledEnvelope(t, events.ReportFiled, "comment", "not-a-uuid", uuid.NewString(), uuid.NewString()),
		reportFiledEnvelope(t, events.ReportFiled, "comment", uuid.NewString(), "", uuid.NewString()),
		reportFiledEnvelope(t, events.ReportFiled, "comment", uuid.NewString(), uuid.NewString(), uuid.Nil.String()),
	}
	for _, env := range bad {
		if err := c.handle(ctx, env); !sharedkafka.IsPermanent(err) {
			t.Fatalf("%s: err = %v, want a Permanent error (DLQ, not a stalled partition)", env.Payload, err)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("poison reached the store: %+v", f.calls)
	}

	// A store failure is retried (not Permanent): the offset must not pass
	// a report that was not counted.
	f.err = errors.New("db down")
	err := c.handle(ctx, reportFiledEnvelope(t, events.ReportFiled, "comment", uuid.NewString(), uuid.NewString(), uuid.NewString()))
	if err == nil || sharedkafka.IsPermanent(err) {
		t.Fatalf("store failure: err = %v, want a retryable error", err)
	}
}
