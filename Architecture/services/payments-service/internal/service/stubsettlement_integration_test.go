//go:build integration

package service

// The stub gateway's settlement path, and the fact that it is a stub-only
// path.
//
// ─── THE DEFECT ─────────────────────────────────────────────────────────
//
// In stub mode `provider` is nil, so POST /v1/payments/webhook answers 503
// and the reconciler is not started. Those are the only two authorities A1
// permits to create terminal state, so a stub intent had no way to leave
// `pending`. That is not merely untidy: the order behind it could never be
// paid, and a refund of it was refused outright —
//
//	payments: cannot refund an intent in status pending
//
// — which is what the launch journey hit. The whole post-payment half of the
// product was untestable on the only environment where it is tested.
//
// The fix lets VerifyIntent settle, and ONLY when boot selected the stub
// gateway. What is proven here is that it settles through the real
// machinery, that it is idempotent, and — the assertion that matters — that
// it does nothing at all when the flag is off, which is every deployment
// with real credentials.
//
//	PAYMENTS_TEST_DSN=... go test -tags=integration ./internal/service/... -v

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/atpost/payments-service/internal/gateway"
	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/google/uuid"
)

// stubSvc is the service as main.go builds it in ModeStub: the stub gateway,
// NO provider (so no webhook and no reconciler), and settlement permitted.
func stubSvc(t *testing.T, settlement bool) *Service {
	t.Helper()
	return New(postgres.New(recPool), &gateway.StubGateway{}).WithStubSettlement(settlement)
}

// seedStubIntent is a fresh pending intent with a stub provider reference —
// the row commerce's checkout leaves behind on a dev stack.
func seedStubIntent(t *testing.T, amountMinor int64, providerRef string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := recPool.Exec(context.Background(), `
		INSERT INTO payments.payment_intents
		    (id, payer_id, payee_id, reference_type, reference_id, amount, amount_minor,
		     currency, method, status, provider, provider_ref, provider_order_id,
		     owner_domain, idempotency_key, created_at)
		-- provider='razorpay' DELIBERATELY, on a stub deployment.
		--
		-- That is what the live checkout writes: the column carries the
		-- schema default and nothing on the stub path overrides it. A
		-- fixture that wrote 'stub' here would have let the first version of
		-- this fix pass — it addressed the settlement to the literal
		-- provider "stub", matched no row, and failed with
		-- "no intent for provider order" the moment it met real data.
		VALUES ($1,$2,$3,'order',$4,$5,$6,'INR','upi','pending','razorpay',$7,$7,
		        'commerce',$8,NOW())`,
		id, uuid.New(), uuid.New(), uuid.New(),
		float64(amountMinor)/100.0, amountMinor, providerRef, "idem-"+id.String())
	if err != nil {
		t.Fatalf("seed stub intent: %v", err)
	}
	return id
}

func TestStubSettlementMakesAnIntentTerminalAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	ref := "order_stub_" + uuid.New().String()[:12]
	pay := "pay_stub_" + uuid.New().String()[:12]
	id := seedStubIntent(t, 91900, ref)

	svc := stubSvc(t, true)

	res, err := svc.VerifyIntent(ctx, id, ref, pay, "any-signature-the-stub-accepts", 91900)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !res.Verified {
		t.Fatal("the stub gateway accepts every signature; Verified must be true")
	}
	if got := statusOf(t, id); got != "succeeded" {
		t.Fatalf("intent status = %q, want succeeded. Without this the order behind it "+
			"cannot be paid and a refund is refused with "+
			`"cannot refund an intent in status pending"`, got)
	}

	// The settlement went through the same atomic transaction a provider
	// event takes, so it left an inbox row and an outbox event behind — not
	// a bare status write.
	if n := countBy(t, `SELECT COUNT(*) FROM payments.provider_events WHERE provider='razorpay' AND event_id=$1`, "stub_callback_"+pay); n != 1 {
		t.Errorf("inbox rows for the settlement = %d, want 1 — settlement must go through "+
			"ApplyWebhook so dev exercises the production path, not a parallel one", n)
	}

	// A retried callback is deduped, not applied twice.
	if _, err := svc.VerifyIntent(ctx, id, ref, pay, "sig", 91900); err != nil {
		t.Fatalf("second verify: %v", err)
	}
	if n := countBy(t, `SELECT COUNT(*) FROM payments.provider_events WHERE provider='razorpay' AND event_id=$1`, "stub_callback_"+pay); n != 1 {
		t.Errorf("a retried callback wrote a second inbox row (%d); the event id must be "+
			"deterministic so a retry is a duplicate", n)
	}
	if got := statusOf(t, id); got != "succeeded" {
		t.Fatalf("status after the retry = %q, want succeeded", got)
	}
}

// The control that matters. With the flag off — every deployment holding
// real Razorpay credentials — VerifyIntent is advisory exactly as A1/R-3
// requires, and a genuine-looking callback changes nothing.
func TestWithoutTheStubFlagVerifyIntentStillSettlesNothing(t *testing.T) {
	ctx := context.Background()
	ref := "order_stub_" + uuid.New().String()[:12]
	pay := "pay_stub_" + uuid.New().String()[:12]
	id := seedStubIntent(t, 50000, ref)

	svc := stubSvc(t, false)

	res, err := svc.VerifyIntent(ctx, id, ref, pay, "sig", 50000)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !res.Advisory {
		t.Error("the verdict must announce itself as advisory")
	}
	if got := statusOf(t, id); got != "pending" {
		t.Fatalf("intent status = %q after an advisory verify, want pending. A client "+
			"callback must never create terminal state when a provider exists to send "+
			"a signature-verified webhook (A1/R-3)", got)
	}
	if n := countBy(t, `SELECT COUNT(*) FROM payments.provider_events WHERE provider='razorpay' AND event_id=$1`,
		"stub_callback_"+pay); n != 0 {
		t.Errorf("an advisory verify wrote %d inbox rows; it must write none", n)
	}
}

// A refund runs to completion on the stub, rather than stopping at
// `submitted` for ever.
//
// The refund worker places the refund and records the provider refund id,
// but the LEDGER is credited by a `payment.refunded` webhook — which the
// stub cannot send. So a dev refund was durable and half-finished: the
// intent's refunded balance stayed 0 and the order behind it never left
// `refund_pending`. The stub's refund is synchronous and final, so there is
// nothing to wait for, and it is applied through the same atomic refund
// transaction a provider event takes.
func TestStubSettlementCompletesARefundInsteadOfLeavingItSubmitted(t *testing.T) {
	ctx := context.Background()
	ref := "order_stub_" + uuid.New().String()[:12]
	pay := "pay_stub_" + uuid.New().String()[:12]
	id := seedStubIntent(t, 91900, ref)
	svc := stubSvc(t, true)

	// Pay it first: a refund needs a succeeded intent.
	if _, err := svc.VerifyIntent(ctx, id, ref, pay, "sig", 91900); err != nil {
		t.Fatalf("settle: %v", err)
	}

	if _, err := svc.RequestRefund(ctx, RefundRequest{
		IntentID:               id,
		AmountMinor:            91900,
		Reason:                 "integration proof",
		ProviderIdempotencyKey: "refund-" + id.String(),
		CallerDomain:           "commerce",
	}); err != nil {
		t.Fatalf("request refund: %v", err)
	}

	// One worker pass, synchronously — the same call the ticker makes.
	svc.drainRefundCommands(ctx)

	var refunded int64
	var status string
	if err := recPool.QueryRow(ctx,
		`SELECT COALESCE(refunded_amount_minor,0), status
		   FROM payments.payment_intents WHERE id=$1`, id).Scan(&refunded, &status); err != nil {
		t.Fatalf("read intent: %v", err)
	}
	if refunded != 91900 {
		t.Errorf("refunded_amount_minor = %d, want 91900. The refund was placed at the "+
			"provider but the ledger was never credited, because crediting it is the "+
			"webhook's job and the stub has no webhook", refunded)
	}
	if status != "refunded" {
		t.Errorf("intent status = %q, want refunded", status)
	}

	var cmdStatus string
	if err := recPool.QueryRow(ctx,
		`SELECT status FROM payments.refund_commands WHERE intent_id=$1`, id).Scan(&cmdStatus); err != nil {
		t.Fatalf("read refund command: %v", err)
	}
	if cmdStatus == "submitted" {
		t.Error("the refund command is still `submitted`; on a stub there is nothing left " +
			"to wait for, so it must reach a terminal state")
	}

	// The event has to say WHAT was refunded.
	//
	// The payload carried the intent id and nothing else, and commerce's
	// consumer keys its refund handler on reference_type/reference_id: it
	// parses reference_id as an order id and returns nil the moment the
	// parse fails, because a refund belonging to another domain is a
	// legitimate no-op. So every refund event was silently dropped, the
	// order stayed `refund_pending` with the money already credited at the
	// provider, and commerce re-sent the same refund every forty seconds.
	var payload []byte
	if err := recPool.QueryRow(ctx,
		`SELECT payload FROM payments.outbox_events
		  WHERE event_type='payment.refunded' AND partition_key=$1
		  ORDER BY created_at DESC LIMIT 1`, id.String()).Scan(&payload); err != nil {
		t.Fatalf("read refund outbox event: %v", err)
	}
	var env struct {
		Payload struct {
			ReferenceType string `json:"reference_type"`
			ReferenceID   string `json:"reference_id"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		t.Fatalf("decode refund event: %v\n%s", err, payload)
	}
	if env.Payload.ReferenceType != "order" || env.Payload.ReferenceID == "" {
		t.Errorf("payment.refunded payload carries reference_type=%q reference_id=%q; "+
			"without both, the calling domain cannot attribute the refund to anything "+
			"and drops the event",
			env.Payload.ReferenceType, env.Payload.ReferenceID)
	}
}

// And the guards in front of settlement still apply: a caller naming another
// intent's provider order, or a different amount, is refused before anything
// is settled.
func TestStubSettlementStillHonoursTheProviderRefAndAmountGuards(t *testing.T) {
	ctx := context.Background()
	ref := "order_stub_" + uuid.New().String()[:12]
	id := seedStubIntent(t, 91900, ref)
	svc := stubSvc(t, true)

	if _, err := svc.VerifyIntent(ctx, id, "order_stub_somebody_else", "pay_x", "sig", 91900); err == nil {
		t.Error("a callback naming a different provider order was accepted")
	}
	if _, err := svc.VerifyIntent(ctx, id, ref, "pay_x", "sig", 1); err == nil {
		t.Error("a callback naming a different amount was accepted")
	}
	if got := statusOf(t, id); got != "pending" {
		t.Fatalf("intent status = %q after two refused callbacks, want pending", got)
	}
}
