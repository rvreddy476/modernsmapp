package consumers

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/atpost/shared/paymentevents"
	"github.com/google/uuid"
)

// Audit 2026-10-30 M2: the refund consumer's filter and error mapping.

type fakeSettler struct {
	got []postgres.RefundEvent
	res postgres.RefundSettlement
	err error
}

func (f *fakeSettler) SettleRefund(_ context.Context, ev postgres.RefundEvent) (postgres.RefundSettlement, error) {
	f.got = append(f.got, ev)
	return f.res, f.err
}

type countingObs struct{ applied, dup, mismatch int }

func (o *countingObs) AmountMismatch(uuid.UUID, int64, int64) { o.mismatch++ }
func (o *countingObs) DuplicateSuppressed(string)             { o.dup++ }
func (o *countingObs) Applied(string)                         { o.applied++ }

func refundEnv() *events.EventEnvelope {
	return &events.EventEnvelope{EventID: "evt-" + uuid.NewString(), EventType: events.EventPaymentRefunded}
}

func TestApplyRefund_IgnoresOtherDomains(t *testing.T) {
	// Food, dating, rider, doorstep and membership references are UUIDs too.
	for _, rt := range []string{"food_order", "booking", "subscription", ""} {
		f := &fakeSettler{}
		err := applyRefundWith(context.Background(), f, nil, refundEnv(), paymentevents.Refunded{
			ID: "i-1", ReferenceType: rt, ReferenceID: uuid.NewString(), AmountMinor: 100,
		})
		if err != nil || len(f.got) != 0 {
			t.Fatalf("reference_type %q: err=%v settled=%d, want ignored", rt, err, len(f.got))
		}
	}
}

func TestApplyRefund_PassesTheRefundThrough(t *testing.T) {
	orderID := uuid.New()
	f := &fakeSettler{res: postgres.RefundSettlement{RefundedTotal: 20000, PaymentStatus: "partially_refunded"}}
	obs := &countingObs{}
	env := refundEnv()
	err := applyRefundWith(context.Background(), f, obs, env, paymentevents.Refunded{
		IntentID: "i-1", ProviderRefundID: "rfnd_1", AmountMinor: 20000, Status: "partially_refunded",
		ReferenceType: "order", ReferenceID: orderID.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.got) != 1 {
		t.Fatalf("settled %d times", len(f.got))
	}
	g := f.got[0]
	if g.EventID != env.EventID || g.OrderID != orderID || g.IntentID != "i-1" || g.RefundID != "rfnd_1" ||
		g.AmountMinor != 20000 || g.ProviderStatus != "partially_refunded" {
		t.Fatalf("settled %+v", g)
	}
	if obs.applied != 1 {
		t.Fatalf("applied = %d", obs.applied)
	}
}

func TestApplyRefund_ErrorMapping(t *testing.T) {
	order := paymentevents.Refunded{ID: "i-1", ReferenceType: "order", ReferenceID: uuid.NewString(), AmountMinor: 100}

	// Redelivery (same event id) and the same refund under another id: handled, counted as duplicates.
	for _, f := range []*fakeSettler{
		{err: postgres.ErrDuplicatePaymentEvt},
		{res: postgres.RefundSettlement{Duplicate: true}},
	} {
		obs := &countingObs{}
		if err := applyRefundWith(context.Background(), f, obs, refundEnv(), order); err != nil {
			t.Fatalf("duplicate: %v", err)
		}
		if obs.dup != 1 || obs.applied != 0 {
			t.Fatalf("duplicate counted %+v", obs)
		}
	}

	// A mismatch or an unknown order is poison: DLQ, and the mismatch pages.
	for _, e := range []error{postgres.ErrAmountMismatch, postgres.ErrOrderNotFoundP0} {
		obs := &countingObs{}
		err := applyRefundWith(context.Background(), &fakeSettler{err: e}, obs, refundEnv(), order)
		if !sharedkafka.IsPermanent(err) {
			t.Fatalf("%v: err = %v, want permanent", e, err)
		}
		if errors.Is(e, postgres.ErrAmountMismatch) && obs.mismatch != 1 {
			t.Fatal("a refund mismatch did not raise the alarm")
		}
	}

	// Transient: retried, not parked.
	boom := errors.New("connection reset")
	err := applyRefundWith(context.Background(), &fakeSettler{err: boom}, nil, refundEnv(), order)
	if !errors.Is(err, boom) || sharedkafka.IsPermanent(err) {
		t.Fatalf("transient: err = %v", err)
	}

	// An order reference that is not a UUID is poison too.
	bad := order
	bad.ReferenceID = "not-a-uuid"
	if err := applyRefundWith(context.Background(), &fakeSettler{}, nil, refundEnv(), bad); !sharedkafka.IsPermanent(err) {
		t.Fatalf("bad reference: err = %v", err)
	}
}
