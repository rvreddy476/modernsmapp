package consumers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/atpost/shared/paymentevents"
	"github.com/google/uuid"
)

// samplePlainSucceeded is a payment.succeeded that states captured_minor
// without offer figures. payments-service today states it only on an offer
// capture (a plain capture's amount_minor is the money), but the key is read
// wherever it appears.
const samplePlainSucceeded = `{"id":"7d0d3b8e-3f5a-4a31-9a66-0f3c5d2b9e11","reference_type":"order",
  "reference_id":"9c1d2e3f-4a5b-4c6d-8e7f-001122334455","amount_minor":134800,"currency":"INR",
  "status":"succeeded","captured_minor":134800}`

func TestSucceededCaptured(t *testing.T) {
	for _, tc := range []struct {
		payload string
		want    int64 // 0 = nil
	}{
		{samplePlainSucceeded, 134800},
		{sampleOfferSucceeded, 121320},
		{`{"amount_minor":100}`, 0},
		{`{"amount_minor":100,"captured_minor":0}`, 0},
		{`{"amount_minor":100,"captured_minor":"x"}`, 0},
		{`not json`, 0},
	} {
		got := SucceededCaptured(&events.EventEnvelope{Payload: []byte(tc.payload)})
		switch {
		case tc.want == 0 && got != nil:
			t.Errorf("%s: %v, want nil", tc.payload, *got)
		case tc.want != 0 && (got == nil || got.Int64() != tc.want):
			t.Errorf("%s: %v, want %d", tc.payload, got, tc.want)
		}
	}
	if SucceededCaptured(nil) != nil {
		t.Fatal("nil envelope")
	}
}

func TestRefundedReturned(t *testing.T) {
	if got := refundedReturned(&events.EventEnvelope{Payload: []byte(`{"amount_minor":121810,"amount_returned_minor":109629}`)}); got == nil || *got != 109629 {
		t.Fatalf("amount_returned_minor = %v", got)
	}
	if got := refundedReturned(&events.EventEnvelope{Payload: []byte(`{"amount_minor":100}`)}); got != nil {
		t.Fatalf("absent = %v", *got)
	}
}

type fakeOwedRecorder struct {
	got []postgres.OwedCaptureEvent
	err error
}

func (f *fakeOwedRecorder) ApplyOwedCapture(_ context.Context, ev postgres.OwedCaptureEvent) error {
	f.got = append(f.got, ev)
	return f.err
}

func owedEnv(payload string) *events.EventEnvelope {
	return &events.EventEnvelope{EventID: "evt-" + uuid.NewString(), EventType: postgres.EventPaymentOwedCapture,
		Payload: []byte(payload)}
}

func TestApplyOwedCapture_PassesPaymentsLedgerThrough(t *testing.T) {
	order := uuid.New()
	f := &fakeOwedRecorder{}
	obs := &countingObs{}
	env := owedEnv(`{"id":"i-dup","intent_id":"i-dup","refund_required_id":"42","reason":"duplicate_order_capture",
	  "amount_minor":109629,"currency":"INR","refunded_minor":109629,"resolved":true,"state":"refunded",
	  "detected_at":"2026-09-09T10:05:00Z","reference_type":"order","reference_id":"` + order.String() + `"}`)
	if err := applyOwedCaptureWith(context.Background(), f, obs, env); err != nil {
		t.Fatal(err)
	}
	if len(f.got) != 1 {
		t.Fatalf("recorded %d", len(f.got))
	}
	g := f.got[0]
	if g.EventID != env.EventID || g.OrderID != order || g.IntentID != "i-dup" || g.RefundRequiredID != 42 ||
		g.Reason != "duplicate_order_capture" || g.AmountMinor != 109629 || g.RefundedMinor != 109629 || !g.Resolved ||
		!g.DetectedAt.Equal(time.Date(2026, 9, 9, 10, 5, 0, 0, time.UTC)) {
		t.Fatalf("recorded %+v", g)
	}
	if obs.applied != 1 {
		t.Fatalf("applied = %d", obs.applied)
	}
}

func TestApplyOwedCapture_FiltersAndErrors(t *testing.T) {
	order := uuid.NewString()
	// Another domain's payment: ignored.
	f := &fakeOwedRecorder{}
	if err := applyOwedCaptureWith(context.Background(), f, nil,
		owedEnv(`{"refund_required_id":"1","reference_type":"food_order","reference_id":"`+order+`"}`)); err != nil || len(f.got) != 0 {
		t.Fatalf("food payment: err=%v recorded=%d", err, len(f.got))
	}
	// Poison: unreadable, no order, no row.
	for _, p := range []string{
		`not json`,
	} {
		if err := applyOwedCaptureWith(context.Background(), &fakeOwedRecorder{}, nil, owedEnv(p)); !errors.Is(err, paymentevents.ErrMalformed) {
			t.Fatalf("%s: %v, want malformed", p, err)
		}
	}
	for _, p := range []string{
		`{"refund_required_id":"1","reference_type":"order","reference_id":"nope"}`,
		`{"refund_required_id":"","reference_type":"order","reference_id":"` + order + `"}`,
		`{"refund_required_id":"-3","reference_type":"order","reference_id":"` + order + `"}`,
	} {
		if err := applyOwedCaptureWith(context.Background(), &fakeOwedRecorder{}, nil, owedEnv(p)); !sharedkafka.IsPermanent(err) {
			t.Fatalf("%s: %v, want permanent", p, err)
		}
	}
	ok := `{"refund_required_id":"1","reference_type":"order","reference_id":"` + order + `"}`
	for _, tc := range []struct {
		err       error
		permanent bool
		dup       bool
	}{
		{postgres.ErrDuplicatePaymentEvt, false, true},
		{postgres.ErrOrderNotFoundP0, true, false},
		{postgres.ErrAmountMismatch, true, false},
	} {
		obs := &countingObs{}
		err := applyOwedCaptureWith(context.Background(), &fakeOwedRecorder{err: tc.err}, obs, owedEnv(ok))
		if tc.dup && (err != nil || obs.dup != 1) {
			t.Fatalf("%v: err=%v dup=%d", tc.err, err, obs.dup)
		}
		if tc.permanent && !sharedkafka.IsPermanent(err) {
			t.Fatalf("%v: %v, want permanent", tc.err, err)
		}
	}
	// Transient: retried.
	if err := applyOwedCaptureWith(context.Background(), &fakeOwedRecorder{err: errors.New("conn reset")}, nil, owedEnv(ok)); err == nil || sharedkafka.IsPermanent(err) {
		t.Fatalf("transient: %v, want a retryable error", err)
	}
}

type fakeFailedRecorder struct {
	got []postgres.RefundFailedEvent
	err error
}

func (f *fakeFailedRecorder) ApplyRefundFailed(_ context.Context, ev postgres.RefundFailedEvent) error {
	f.got = append(f.got, ev)
	return f.err
}

func TestApplyRefundFailed_RoutesOrdersAndOwedPayments(t *testing.T) {
	env := &events.EventEnvelope{EventID: "evt-1", EventType: events.EventPaymentRefundFailed}
	for _, rt := range []string{"order", "refund_required"} {
		f := &fakeFailedRecorder{}
		if err := applyRefundFailedWith(context.Background(), f, nil, env, paymentevents.RefundFailed{
			ID: "i-1", ReferenceType: rt, ReferenceID: "ref", AmountMinor: 500, ReasonCode: "provider_refused",
		}); err != nil {
			t.Fatal(err)
		}
		if len(f.got) != 1 || f.got[0].IntentID != "i-1" || f.got[0].ReferenceType != rt || f.got[0].ReasonCode != "provider_refused" ||
			f.got[0].EventID != "evt-1" || f.got[0].AmountMinor != 500 {
			t.Fatalf("%s: recorded %+v", rt, f.got)
		}
	}
	f := &fakeFailedRecorder{}
	if err := applyRefundFailedWith(context.Background(), f, nil, env, paymentevents.RefundFailed{ReferenceType: "booking"}); err != nil || len(f.got) != 0 {
		t.Fatalf("another domain: err=%v recorded=%d", err, len(f.got))
	}
	obs := &countingObs{}
	if err := applyRefundFailedWith(context.Background(), &fakeFailedRecorder{err: postgres.ErrDuplicatePaymentEvt}, obs, env,
		paymentevents.RefundFailed{ReferenceType: "order"}); err != nil || obs.dup != 1 {
		t.Fatalf("duplicate: err=%v dup=%d", err, obs.dup)
	}
	if err := applyRefundFailedWith(context.Background(), &fakeFailedRecorder{err: errors.New("db down")}, nil, env,
		paymentevents.RefundFailed{ReferenceType: "order"}); err == nil {
		t.Fatal("a transient failure must be retried")
	}
}
