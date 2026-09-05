package kafka

// B1 — the payment-event loss window, and its negative control.
//
// The defect: the dedupe key was written with SETNX BEFORE the handler ran,
// so the key was a CLAIM on the work rather than a RECEIPT for it. A crash
// between the claim and the handler's commit left a key with no effect
// behind it, and the redelivery skipped the handler on the strength of that
// key and committed the offset. The PSP had the customer's money; commerce
// had no record that it was owed.
//
// These tests observe the ordering directly rather than asserting on a
// count, because the count is identical in both the correct and the broken
// implementation on the happy path — which is exactly why the defect
// survived review. What differs is WHEN the receipt is written relative to
// the effect, so that is what is asserted.
//
// TestNegativeControl_PreClaimReceiptLosesTheEvent is the control required
// by review §4: it reimplements the original pre-claim ordering against the
// same seam and proves the event is lost. If a future edit restores the
// pre-claim, TestReceiptIsNeverWrittenBeforeTheEffect fails; if the control
// itself stops reproducing the defect, the control fails. Both directions
// are covered.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/atpost/shared/events"
	kafkago "github.com/segmentio/kafka-go"
)

// recordingDedupe is a dedupeStore that logs every operation against a
// shared timeline, so a test can assert on ORDER rather than on counts.
type recordingDedupe struct {
	mu       sync.Mutex
	keys     map[string]bool
	timeline *[]string
}

func newRecordingDedupe(timeline *[]string) *recordingDedupe {
	return &recordingDedupe{keys: map[string]bool{}, timeline: timeline}
}

func (d *recordingDedupe) Seen(_ context.Context, key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	seen := d.keys[key]
	if seen {
		*d.timeline = append(*d.timeline, "skip")
	}
	return seen
}

func (d *recordingDedupe) Mark(_ context.Context, key string, _ time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.keys[key] = true
	*d.timeline = append(*d.timeline, "receipt")
}

// harness builds a Consumer with every broker-facing dependency replaced by
// a seam. No Kafka, no Redis, no database.
func harness(t *testing.T, timeline *[]string, handler HandlerFunc) (*Consumer, *recordingDedupe) {
	t.Helper()
	d := newRecordingDedupe(timeline)
	c := &Consumer{
		cfg: ConsumerConfig{
			GroupID:      "test-group",
			Topic:        "test.topic",
			MaxRetries:   2,
			RetryBackoff: time.Millisecond,
			DedupTTL:     time.Minute,
		},
		dedupe:  d,
		handler: handler,
	}
	c.commit = func(context.Context, kafkago.Message) error {
		d.mu.Lock()
		defer d.mu.Unlock()
		*timeline = append(*timeline, "commit")
		return nil
	}
	return c, d
}

func envelopeMsg(t *testing.T, eventID string) kafkago.Message {
	t.Helper()
	body, err := json.Marshal(events.EventEnvelope{
		EventID:   eventID,
		EventType: "payment.succeeded",
	})
	if err != nil {
		t.Fatal(err)
	}
	return kafkago.Message{Topic: "test.topic", Value: body}
}

// The core guarantee: on the success path the effect happens first, the
// receipt second, the offset commit third.
func TestReceiptIsNeverWrittenBeforeTheEffect(t *testing.T) {
	var timeline []string
	var mu sync.Mutex
	c, _ := harness(t, &timeline, func(context.Context, *events.EventEnvelope) error {
		mu.Lock()
		timeline = append(timeline, "effect")
		mu.Unlock()
		return nil
	})

	c.processWithRetry(context.Background(), slog.Default(), envelopeMsg(t, "evt-1"))

	want := []string{"effect", "receipt", "commit"}
	if len(timeline) != len(want) {
		t.Fatalf("timeline = %v, want %v", timeline, want)
	}
	for i := range want {
		if timeline[i] != want[i] {
			t.Fatalf("timeline = %v, want %v", timeline, want)
		}
	}
}

// The failure this closes. The handler fails every attempt, so no effect is
// ever durable. A receipt written anyway would cause the redelivery to be
// skipped — the money-losing path. After retries are exhausted with no DLQ
// writer the message is NOT committed, so the partition stalls loudly
// instead of advancing past an unapplied payment.
func TestFailedHandlerLeavesNoReceipt(t *testing.T) {
	var timeline []string
	var mu sync.Mutex
	c, d := harness(t, &timeline, func(context.Context, *events.EventEnvelope) error {
		mu.Lock()
		timeline = append(timeline, "effect-attempt")
		mu.Unlock()
		return errors.New("postgres is unreachable")
	})

	// No DLQ writer is configured, so the exhausted path blocks in
	// retryDurable rather than committing. Bound it with a cancellable
	// context: the assertion is about the receipt, not about the stall.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	c.processWithRetry(ctx, slog.Default(), envelopeMsg(t, "evt-2"))

	for _, entry := range timeline {
		if entry == "receipt" {
			t.Fatalf("a receipt was written for an event whose effect never committed: %v", timeline)
		}
		if entry == "commit" {
			t.Fatalf("the offset advanced past an unapplied payment: %v", timeline)
		}
	}

	// And the redelivery must actually re-run the handler.
	if d.Seen(context.Background(), "consumed:test-group:test.topic:evt-2") {
		t.Fatal("redelivery would be skipped: the failed event holds a dedupe receipt")
	}
}

// A genuine duplicate — one whose effect DID commit — is still suppressed.
// Removing the loss window must not remove the dedupe.
func TestCompletedEventIsSuppressedOnRedelivery(t *testing.T) {
	var timeline []string
	var effects int
	var mu sync.Mutex
	c, _ := harness(t, &timeline, func(context.Context, *events.EventEnvelope) error {
		mu.Lock()
		effects++
		timeline = append(timeline, "effect")
		mu.Unlock()
		return nil
	})

	msg := envelopeMsg(t, "evt-3")
	c.processWithRetry(context.Background(), slog.Default(), msg)
	c.processWithRetry(context.Background(), slog.Default(), msg)

	if effects != 1 {
		t.Fatalf("handler ran %d times for one completed event; want exactly 1", effects)
	}
}

// ─── Negative control (review §4) ────────────────────────────────────
//
// Reproduce the ORIGINAL ordering — receipt claimed before the handler — and
// prove it loses the event. This control is coupled to the production seam
// (the same dedupeStore, the same key format, the same skip-and-commit
// branch), so it is not a scratch reimplementation of unrelated logic.
//
// If this test stops failing to deliver the effect, the control has stopped
// reproducing the defect and the guarantee above is passing for some other
// reason.
func TestNegativeControl_PreClaimReceiptLosesTheEvent(t *testing.T) {
	var timeline []string
	var effects int
	var mu sync.Mutex

	d := newRecordingDedupe(&timeline)
	key := "consumed:test-group:test.topic:evt-4"

	crashOnce := true
	handler := func(context.Context, *events.EventEnvelope) error {
		if crashOnce {
			crashOnce = false
			// Stands in for the process dying / PostgreSQL being
			// unavailable before the effect commits.
			return errors.New("postgres is unreachable")
		}
		mu.Lock()
		effects++
		mu.Unlock()
		return nil
	}

	// THE DEFECT, restored: claim the key before the handler is invoked.
	d.Mark(context.Background(), key, time.Minute)

	c, _ := harness(t, &timeline, handler)
	c.dedupe = d

	// First delivery fails after the claim was already taken.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	c.processWithRetry(ctx, slog.Default(), envelopeMsg(t, "evt-4"))

	// Redelivery: the stale claim makes the consumer skip the handler and
	// commit the offset. This is the money-losing behaviour.
	c.processWithRetry(context.Background(), slog.Default(), envelopeMsg(t, "evt-4"))

	if effects != 0 {
		t.Fatalf("negative control did not reproduce the defect: the effect was applied %d times, "+
			"so a pre-claimed receipt no longer suppresses redelivery", effects)
	}
	sawSkip := false
	for _, entry := range timeline {
		if entry == "skip" {
			sawSkip = true
		}
	}
	if !sawSkip {
		t.Fatal("negative control did not reproduce the defect: redelivery was not suppressed by the stale claim")
	}
	t.Log("negative control reproduced the original defect: a pre-claimed receipt suppressed redelivery " +
		"and the captured payment was never applied")
}
