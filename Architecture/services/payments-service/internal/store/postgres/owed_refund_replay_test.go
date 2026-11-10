package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// replayTx is a pgx.Tx that answers the owed-refund ledger lookup and
// records every write. Any other call panics on the nil embedded Tx.
type replayTx struct {
	pgx.Tx
	ledger map[string]bool // provider + "/" + provider_refund_id already counted
	writes []string
}

type boolRow struct {
	v   bool
	err error
}

func (r boolRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*(dest[0].(*bool)) = r.v
	return nil
}

func (t *replayTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	if !strings.Contains(sql, "payments.refund_required_refunds") {
		return boolRow{err: errors.New("unexpected query: " + sql)}
	}
	return boolRow{v: t.ledger[args[0].(string)+"/"+args[1].(string)]}
}

func (t *replayTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	t.writes = append(t.writes, sql)
	if strings.Contains(sql, "INSERT INTO payments.refund_required_refunds") {
		key := args[0].(string) + "/" + args[1].(string)
		if t.ledger[key] {
			return pgconn.NewCommandTag("INSERT 0 0"), nil
		}
		t.ledger[key] = true
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	}
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func owedRefundEvent(eventID string, amount int64) WebhookEffect {
	return WebhookEffect{Provider: "razorpay", EventID: eventID, AmountMinor: amount, Currency: "INR"}
}

// The CI failure (TestRefundRequiredWorkerRefundsADuplicateCaptureAndTheWebhookResolvesIt):
// one provider refund of a 118000 duplicate capture arrives as refund.processed
// and again as refund.created under another event id. The second must be a
// no-op, not "refunds of 236000 on an owed payment of 118000".
func TestOwedCaptureRefundRedeliveredUnderAnotherEventIsCountedOnce(t *testing.T) {
	ctx := context.Background()
	const amt, refundID = int64(118000), "rfnd_rr_1"
	tx := &replayTx{ledger: map[string]bool{}}
	owed := &OwedCapture{ID: 7, IntentID: uuid.New(), Provider: "razorpay",
		ProviderPaymentID: "pay_B", AmountMinor: amt, Currency: "INR", Reason: reasonDuplicateOrderCapture}

	recorded, err := applyOwedCaptureRefundOnceTx(ctx, tx, owed, "INR", owedRefundEvent("evt_rfnd", amt), refundID)
	if err != nil || !recorded {
		t.Fatalf("first delivery: recorded=%v err=%v, want recorded", recorded, err)
	}
	if owed.RefundedMinor != amt || !owed.Resolved {
		t.Fatalf("after the first delivery: refunded=%d resolved=%v, want %d resolved", owed.RefundedMinor, owed.Resolved, amt)
	}
	writesAfterFirst := len(tx.writes)

	// The row as the next transaction re-reads it: refunded in full.
	reread := *owed
	recorded, err = applyOwedCaptureRefundOnceTx(ctx, tx, &reread, "INR", owedRefundEvent("evt_rfnd2", amt), refundID)
	if err != nil {
		t.Fatalf("the same refund redelivered: %v", err)
	}
	if recorded {
		t.Fatal("the same refund redelivered was recorded again")
	}
	if len(tx.writes) != writesAfterFirst {
		t.Fatalf("a redelivered refund wrote %d statement(s): %v", len(tx.writes)-writesAfterFirst, tx.writes[writesAfterFirst:])
	}
	if reread.RefundedMinor != amt {
		t.Fatalf("a redelivered refund was counted twice: refunded_minor = %d", reread.RefundedMinor)
	}
}

// Why the ledger must be consulted first: the arithmetic alone refuses the
// replay of a fully refunded owed payment as an amount mismatch.
func TestOwedCaptureArithmeticAloneRefusesAReplayOfAFullRefund(t *testing.T) {
	tx := &replayTx{ledger: map[string]bool{"razorpay/rfnd_rr_1": true}}
	owed := &OwedCapture{ID: 7, IntentID: uuid.New(), Provider: "razorpay",
		ProviderPaymentID: "pay_B", AmountMinor: 118000, RefundedMinor: 118000, Currency: "INR", Resolved: true}
	_, err := applyOwedCaptureRefundTx(context.Background(), tx, owed, "INR", owedRefundEvent("evt_rfnd2", 118000), "rfnd_rr_1")
	if !errors.Is(err, ErrWebhookAmountMismatch) {
		t.Fatalf("err = %v, want the amount mismatch the ledger check now avoids", err)
	}
}

// The ledger check never waves through a NEW refund that over-refunds: a
// different refund id past the owed amount is still refused.
func TestOwedCaptureNewRefundPastTheOwedAmountIsStillRefused(t *testing.T) {
	ctx := context.Background()
	tx := &replayTx{ledger: map[string]bool{"razorpay/rfnd_rr_1": true}}
	owed := &OwedCapture{ID: 7, IntentID: uuid.New(), Provider: "razorpay",
		ProviderPaymentID: "pay_B", AmountMinor: 118000, RefundedMinor: 118000, Currency: "INR", Resolved: true}
	recorded, err := applyOwedCaptureRefundOnceTx(ctx, tx, owed, "INR", owedRefundEvent("evt_rfnd3", 1000), "rfnd_rr_2")
	if !errors.Is(err, ErrWebhookAmountMismatch) || recorded {
		t.Fatalf("recorded=%v err=%v, want a refused over-refund", recorded, err)
	}
	if len(tx.writes) != 0 {
		t.Fatalf("an over-refund wrote: %v", tx.writes)
	}
}

// A partial refund followed by a second, distinct refund still adds up and
// resolves the row; only a repeated refund id is a no-op.
func TestOwedCaptureTwoDistinctPartialRefundsResolveTheRow(t *testing.T) {
	ctx := context.Background()
	tx := &replayTx{ledger: map[string]bool{}}
	owed := &OwedCapture{ID: 9, IntentID: uuid.New(), Provider: "razorpay",
		ProviderPaymentID: "pay_B", AmountMinor: 100000, Currency: "INR"}
	for i, step := range []struct {
		event, refund string
		amount        int64
		wantRecorded  bool
	}{
		{"evt_a", "rfnd_a", 40000, true},
		{"evt_a2", "rfnd_a", 40000, false}, // replay of rfnd_a
		{"evt_b", "rfnd_b", 60000, true},
	} {
		recorded, err := applyOwedCaptureRefundOnceTx(ctx, tx, owed, "INR", owedRefundEvent(step.event, step.amount), step.refund)
		if err != nil || recorded != step.wantRecorded {
			t.Fatalf("step %d: recorded=%v err=%v, want recorded=%v", i, recorded, err, step.wantRecorded)
		}
	}
	if owed.RefundedMinor != 100000 || !owed.Resolved {
		t.Fatalf("refunded=%d resolved=%v, want 100000 resolved", owed.RefundedMinor, owed.Resolved)
	}
}
