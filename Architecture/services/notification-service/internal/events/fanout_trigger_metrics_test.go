package events

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/service"
	sharedevents "github.com/atpost/shared/events"
	"github.com/google/uuid"
	dto "github.com/prometheus/client_model/go"
)

// lv-21: each trigger event is counted once, under what the consumer did
// with it.

func triggerCount(t *testing.T, trigger, outcome string) float64 {
	t.Helper()
	var m dto.Metric
	if err := fanoutTriggerEvents.WithLabelValues(trigger, outcome).Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetCounter().GetValue()
}

type failingFanout struct{ fakeFanout }

func (f *failingFanout) Enqueue(context.Context, service.EnqueueParams) error {
	return errors.New("db down")
}

func TestFanoutTriggerMetrics_PostCreated(t *testing.T) {
	channel := uuid.New()
	ok := sharedevents.PostCreatedPayload{
		PostID: uuid.NewString(), AuthorID: uuid.NewString(), ContentType: "long_video",
		Visibility: "public", ChannelID: channel.String(), CreatedAt: time.Now(),
	}
	cases := []struct {
		name    string
		c       *Consumer
		mutate  func(*sharedevents.PostCreatedPayload)
		outcome string
		wantErr bool
	}{
		{"enqueued", &Consumer{fanout: &fakeFanout{channel: channel}}, func(*sharedevents.PostCreatedPayload) {}, triggerEnqueued, false},
		{"not an upload", &Consumer{fanout: &fakeFanout{}}, func(e *sharedevents.PostCreatedPayload) { e.ContentType = "photo" }, triggerIgnoredNotUpload, false},
		{"private", &Consumer{fanout: &fakeFanout{}}, func(e *sharedevents.PostCreatedPayload) { e.Visibility = "private" }, triggerIgnoredAudience, false},
		{"opted out", &Consumer{fanout: &fakeFanout{}}, func(e *sharedevents.PostCreatedPayload) { e.NotifySubscribers = boolp(false) }, triggerIgnoredOptedOut, false},
		{"malformed", &Consumer{fanout: &fakeFanout{}}, func(e *sharedevents.PostCreatedPayload) { e.PostID = "nope" }, triggerDroppedMalformed, false},
		{"no channel", &Consumer{fanout: &fakeFanout{}}, func(e *sharedevents.PostCreatedPayload) { e.ChannelID = "" }, triggerIgnoredNoChannel, false},
		{"no pipeline", &Consumer{}, func(*sharedevents.PostCreatedPayload) {}, triggerDroppedNoPipeline, false},
		{"enqueue error", &Consumer{fanout: &failingFanout{fakeFanout{channel: channel}}}, func(*sharedevents.PostCreatedPayload) {}, triggerEnqueueError, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := ok
			tc.mutate(&e)
			before := triggerCount(t, fanoutTriggerPostCreated, tc.outcome)
			err := tc.c.enqueueSubscriberFanout(context.Background(), e)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got := triggerCount(t, fanoutTriggerPostCreated, tc.outcome) - before; got != 1 {
				t.Fatalf("fanout_trigger_events_total{post_created,%s} moved by %v, want 1", tc.outcome, got)
			}
		})
	}
}

func TestFanoutTriggerMetrics_PostVisible(t *testing.T) {
	channel := uuid.New()
	ok := sharedevents.PostSearchEligibilityChangedPayload{
		PostID: uuid.NewString(), AuthorID: uuid.NewString(), ContentType: "long_video",
		Visibility: "public", ReviewStatus: "approved", CreatedAt: time.Now(),
	}
	cases := []struct {
		name    string
		mutate  func(*sharedevents.PostSearchEligibilityChangedPayload)
		outcome string
	}{
		{"enqueued", func(*sharedevents.PostSearchEligibilityChangedPayload) {}, triggerEnqueued},
		{"still pending", func(e *sharedevents.PostSearchEligibilityChangedPayload) { e.ReviewStatus = "pending" }, triggerIgnoredState},
		{"deleted", func(e *sharedevents.PostSearchEligibilityChangedPayload) { e.Deleted = true }, triggerIgnoredState},
		{"unlisted", func(e *sharedevents.PostSearchEligibilityChangedPayload) { e.Visibility = "unlisted" }, triggerIgnoredAudience},
		{"too old", func(e *sharedevents.PostSearchEligibilityChangedPayload) { e.CreatedAt = time.Now().Add(-30 * 24 * time.Hour) }, triggerIgnoredTooOld},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := ok
			tc.mutate(&e)
			before := triggerCount(t, fanoutTriggerPostVisible, tc.outcome)
			if err := (&Consumer{fanout: &fakeFanout{channel: channel}}).enqueueOnVisible(context.Background(), e); err != nil {
				t.Fatal(err)
			}
			if got := triggerCount(t, fanoutTriggerPostVisible, tc.outcome) - before; got != 1 {
				t.Fatalf("fanout_trigger_events_total{post_visible,%s} moved by %v, want 1", tc.outcome, got)
			}
		})
	}
}
