//go:build integration

package service

// A1 / R4-LB-1 — the ORDINARY successful CreateOrder response is a money fact.
//
// Review 4: "the claim 'one policy on every money path' is false." It was. The
// ambiguous-recovery path, the blank-reference repair, the reconciler and the
// webhook all verified their tuple. The one path that runs on every single
// checkout — a PSP answering HTTP 200 — kept only `ProviderOrderID` and threw
// `order.Amount` away, even though both adapters decode and return it.
//
// A PSP that answers 200 naming a different order, a different amount, or no
// currency is not a transport failure. Nothing retries it. The buyer is handed
// that identifier and pays against it.
//
// Every case here drives the REAL RazorpayProvider over httptest against a
// live PostgreSQL, through the real InitiatePayment path a checkout takes.
//
//	PAYMENTS_TEST_DSN=... go test -tags=integration ./internal/service/... -v

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/atpost/payments-service/internal/gateway"
	"github.com/google/uuid"
)

// openOK makes the stub's POST /orders SUCCEED with the given order body,
// which is the state A1 exists to police.
func openOK(s *createStub, order map[string]any) {
	body, _ := json.Marshal(order)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createCode = http.StatusOK
	s.createBody = string(body)
}

// The positive control. An exact response is attached, once.
//
// Without this the refusals below could pass against a path that refuses
// everything, and no checkout would ever open a payment.
func TestA1ExactSuccessfulOrderIsAttached(t *testing.T) {
	stub := newCreateStub(t)
	// Unique per run: gated 999's uniqueness invariant means a fixed id would
	// be refused the second time this suite is pointed at the same database.
	orderID := "order_ok_" + uuid.NewString()[:8]
	openOK(stub, map[string]any{"id": orderID, "amount": 118000, "currency": "INR"})

	key, err := initiate(t, svcWith(t, stub.provider()), 118000, "INR")
	if err != nil {
		t.Fatalf("initiate: %v", err)
	}
	if got := refByKey(t, key); got != orderID {
		t.Fatalf("provider ref = %q, want %q", got, orderID)
	}
	if create, lookup := stub.calls(); create != 1 || lookup != 0 {
		t.Fatalf("calls: create=%d lookup=%d, want 1/0 — a successful create must not "+
			"trigger the recovery path", create, lookup)
	}
}

// THE defect. HTTP 200, a perfectly well-formed order — for a different amount.
func TestA1SuccessfulOrderWithTheWrongAmountIsRefused(t *testing.T) {
	stub := newCreateStub(t)
	openOK(stub, map[string]any{"id": "order_wamt_" + uuid.NewString()[:8], "amount": 999900, "currency": "INR"})

	key, err := initiate(t, svcWith(t, stub.provider()), 118000, "INR")
	if err == nil {
		t.Fatal("a successful CreateOrder for a DIFFERENT amount was accepted; the buyer " +
			"would be handed this identifier and pay 9,999.00 against an 1,180.00 order")
	}
	if !errors.Is(err, gateway.ErrProviderMoneyUnverified) {
		t.Fatalf("error = %v, want ErrProviderMoneyUnverified", err)
	}
	if got := refByKey(t, key); got != "" {
		t.Fatalf("provider ref = %q, want empty", got)
	}
}

func TestA1SuccessfulOrderInAnotherCurrencyIsRefused(t *testing.T) {
	stub := newCreateStub(t)
	// Same NUMBER, different denomination.
	openOK(stub, map[string]any{"id": "order_wccy_" + uuid.NewString()[:8], "amount": 118000, "currency": "USD"})

	key, err := initiate(t, svcWith(t, stub.provider()), 118000, "INR")
	if err == nil {
		t.Fatal("a USD order was attached to an INR intent")
	}
	if got := refByKey(t, key); got != "" {
		t.Fatalf("provider ref = %q, want empty", got)
	}
}

func TestA1SuccessfulOrderWithNoCurrencyIsRefused(t *testing.T) {
	stub := newCreateStub(t)
	openOK(stub, map[string]any{"id": "order_noccy_" + uuid.NewString()[:8], "amount": 118000})

	key, err := initiate(t, svcWith(t, stub.provider()), 118000, "INR")
	if err == nil {
		t.Fatal("an order with no currency was attached; a field the provider never sent " +
			"is not a fact about money")
	}
	if got := refByKey(t, key); got != "" {
		t.Fatalf("provider ref = %q, want empty", got)
	}
}

func TestA1SuccessfulOrderWithZeroAmountIsRefused(t *testing.T) {
	stub := newCreateStub(t)
	openOK(stub, map[string]any{"id": "order_zero_" + uuid.NewString()[:8], "amount": 0, "currency": "INR"})

	key, err := initiate(t, svcWith(t, stub.provider()), 118000, "INR")
	if err == nil {
		t.Fatal("a zero-amount order was attached")
	}
	if got := refByKey(t, key); got != "" {
		t.Fatalf("provider ref = %q, want empty", got)
	}
}

func TestA1SuccessfulOrderWithABlankIDIsRefused(t *testing.T) {
	stub := newCreateStub(t)
	openOK(stub, map[string]any{"id": "", "amount": 118000, "currency": "INR"})

	key, err := initiate(t, svcWith(t, stub.provider()), 118000, "INR")
	if err == nil {
		t.Fatal("an order with a blank id was accepted")
	}
	if got := refByKey(t, key); got != "" {
		t.Fatalf("provider ref = %q, want empty", got)
	}
}

// A refused open must leave NOTHING behind that looks like progress: the
// intent stays pending with no reference, and no terminal state or outbox
// effect exists. That is what makes it the reconciler's to repair.
func TestA1RefusedOpenLeavesNoTerminalOrOutboxEffect(t *testing.T) {
	stub := newCreateStub(t)
	openOK(stub, map[string]any{"id": "order_noeff_" + uuid.NewString()[:8], "amount": 42, "currency": "INR"})

	key, err := initiate(t, svcWith(t, stub.provider()), 118000, "INR")
	if err == nil {
		t.Fatal("expected a refusal")
	}

	var status, ref string
	var refID uuid.UUID
	if qErr := recPool.QueryRow(t.Context(), `
		SELECT status, COALESCE(provider_order_id, COALESCE(provider_ref,'')), reference_id
		  FROM payments.payment_intents WHERE idempotency_key = $1`, key).
		Scan(&status, &ref, &refID); qErr != nil {
		t.Fatalf("reading the intent: %v", qErr)
	}
	if status != "pending" || ref != "" {
		t.Fatalf("intent is status=%q ref=%q, want pending with no reference", status, ref)
	}

	var outbox, audit int
	_ = recPool.QueryRow(t.Context(),
		`SELECT count(*) FROM payments.outbox_events WHERE partition_key=$1`,
		refID.String()).Scan(&outbox)
	_ = recPool.QueryRow(t.Context(),
		`SELECT count(*) FROM payments.payment_audit_log WHERE event='provider_webhook'
		   AND intent_id IN (SELECT id FROM payments.payment_intents WHERE idempotency_key=$1)`,
		key).Scan(&audit)
	if outbox != 0 || audit != 0 {
		t.Fatalf("a refused open wrote outbox=%d audit=%d rows", outbox, audit)
	}
}

// A retry of the same business request converges on the same intent and the
// same reference — the deterministic idempotency key doing its job. A1 must
// not break that by refusing the second attach of a reference the intent
// already holds.
func TestA1RetryConvergesOnTheSameIntentAndReference(t *testing.T) {
	stub := newCreateStub(t)
	orderID := "order_conv_" + uuid.NewString()[:8]
	openOK(stub, map[string]any{"id": orderID, "amount": 118000, "currency": "INR"})

	svc := svcWith(t, stub.provider())
	key := "a1-converge-" + uuid.NewString()
	payer, payee, ref := uuid.New(), uuid.New(), uuid.New()

	var ids []uuid.UUID
	for i := 0; i < 3; i++ {
		intent, err := svc.InitiatePayment(t.Context(), InitiateInput{
			PayerID: payer, PayeeID: payee,
			ReferenceType: "order", ReferenceID: ref,
			AmountMinor: 118000, Currency: "INR", Method: "upi",
			IdempotencyKey: key, OwnerDomain: "commerce",
		})
		if err != nil {
			t.Fatalf("attempt %d: %v", i+1, err)
		}
		ids = append(ids, intent.ID)
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] != ids[0] {
			t.Fatalf("retry produced a different intent: %s vs %s", ids[i], ids[0])
		}
	}
	if got := refByKey(t, key); got != orderID {
		t.Fatalf("provider ref = %q, want %q", got, orderID)
	}
}
