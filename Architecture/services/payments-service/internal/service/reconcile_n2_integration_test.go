//go:build integration

package service

// MRC-1 / MRC-2 — payment recovery, proven through the REAL provider adapter.
//
// What the previous pass got wrong, and why this file is shaped the way it
// is. Its reconciler proof used a `fakeGateway` returning a hand-built
// `gateway.GatewayPayment{Currency: "INR"}`. The production reconciler used
// the LEGACY adapter, whose FetchPayment response struct has no `currency`
// field at all — so the fake supplied precisely the field production drops,
// the test went green, and production could mark an INR intent succeeded on a
// same-numeric settlement in another currency.
//
// So there is no hand-built provider payload here. Every reconciliation test
// drives the real `RazorpayProvider` decoder against recorded Razorpay JSON
// served by an `httptest.Server`, against a live PostgreSQL. If the adapter
// stops decoding a field, these turn red.
//
//	PAYMENTS_TEST_DSN=... go test -tags=integration ./internal/service/... -v

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/payments-service/internal/gateway"
	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var recPool *pgxpool.Pool

func TestMain(m *testing.M) {
	dsn := os.Getenv("PAYMENTS_TEST_DSN")
	if dsn == "" {
		fmt.Println("PAYMENTS_TEST_DSN not set; skipping reconciler integration proofs")
		os.Exit(0)
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		fmt.Printf("connect: %v\n", err)
		os.Exit(1)
	}
	recPool = pool
	code := m.Run()
	pool.Close()
	os.Exit(code)
}

// ─── A recorded Razorpay, served over HTTP ───────────────────────────

// razorpayStub serves the two endpoints the recovery paths use:
//
//	GET /payments/{id}        → the payment fetch the reconciler reads
//	GET /orders?receipt={key} → the idempotency-key lookup MRC-2 repairs by
//
// Bodies are raw JSON strings in Razorpay's documented shape, so the
// assertions exercise the adapter's own decoder rather than a struct a test
// filled in.
type razorpayStub struct {
	srv *httptest.Server

	mu           sync.Mutex
	paymentBody  string
	ordersBody   string
	paymentCalls int
	orderCalls   int
	paymentCode  int
	ordersCode   int
}

func newRazorpayStub(t *testing.T) *razorpayStub {
	t.Helper()
	s := &razorpayStub{paymentCode: http.StatusOK, ordersCode: http.StatusOK}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/payments/"):
			s.paymentCalls++
			w.WriteHeader(s.paymentCode)
			_, _ = w.Write([]byte(s.paymentBody))
		case r.URL.Path == "/orders":
			s.orderCalls++
			w.WriteHeader(s.ordersCode)
			_, _ = w.Write([]byte(s.ordersBody))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *razorpayStub) setPayment(body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paymentBody = body
}

func (s *razorpayStub) setOrders(body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ordersBody = body
}

func (s *razorpayStub) counts() (payments, orders int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.paymentCalls, s.orderCalls
}

// provider returns the REAL Razorpay adapter pointed at the stub.
func (s *razorpayStub) provider() *gateway.RazorpayProvider {
	return gateway.NewRazorpayProvider("rzp_test", "secret", "whsec").
		WithEndpoint(s.srv.URL, s.srv.Client())
}

// capturedPayment is a recorded Razorpay payment-fetch body.
func capturedPayment(id, orderID string, amount int64, currency string) string {
	m := map[string]any{
		"id": id, "order_id": orderID, "amount": amount,
		"status": "captured", "method": "upi",
	}
	if currency != "" {
		m["currency"] = currency
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// orderList is a recorded Razorpay `GET /orders?receipt=` body.
func orderList(items ...map[string]any) string {
	b, _ := json.Marshal(map[string]any{"count": len(items), "items": items})
	return string(b)
}

func rzpOrder(id string, amount int64, currency, status string) map[string]any {
	return map[string]any{"id": id, "amount": amount, "currency": currency, "status": status}
}

// ─── Fixtures ────────────────────────────────────────────────────────

type staleIntent struct {
	id            uuid.UUID
	providerOrder string
	referenceID   uuid.UUID
	idemKey       string
}

// seedStale creates a pending intent old enough for the reconciler.
// providerOrder == "" leaves the reference blank, which is the MRC-2 state.
func seedStale(t *testing.T, amountMinor int64, currency, providerOrder string) staleIntent {
	t.Helper()
	si := staleIntent{
		id:            uuid.New(),
		providerOrder: providerOrder,
		referenceID:   uuid.New(),
	}
	si.idemKey = "idem-" + si.id.String()

	// Isolation. reconcileOnce processes every stale intent it finds, up to
	// StalePending's LIMIT 50, so intents left pending by earlier tests both
	// crowd this one out of the window and get driven through whichever
	// provider stub the current test installed. Age the others out instead
	// of deleting them: it leaves their rows and effects intact for the
	// assertions that own them, and exercises the real StalePending query
	// rather than working around it.
	if _, err := recPool.Exec(context.Background(),
		`UPDATE payments.payment_intents
		    SET created_at = NOW()
		  WHERE status IN ('pending','processing')`); err != nil {
		t.Fatalf("ageing out other stale intents: %v", err)
	}

	_, err := recPool.Exec(context.Background(), `
		INSERT INTO payments.payment_intents
		    (id, payer_id, payee_id, reference_type, reference_id, amount, amount_minor,
		     currency, method, status, provider, provider_ref, provider_order_id,
		     owner_domain, idempotency_key, created_at)
		VALUES ($1,$2,$3,'order',$4,$5,$6,$7,'upi','pending','razorpay',
		        NULLIF($8,''), NULLIF($8,''), 'commerce', $9, NOW() - INTERVAL '2 hours')`,
		si.id, uuid.New(), uuid.New(), si.referenceID,
		float64(amountMinor)/100.0, amountMinor, currency, providerOrder, si.idemKey)
	if err != nil {
		t.Fatalf("seed stale intent: %v", err)
	}
	return si
}

func statusOf(t *testing.T, id uuid.UUID) string {
	t.Helper()
	var s string
	if err := recPool.QueryRow(context.Background(),
		`SELECT status FROM payments.payment_intents WHERE id=$1`, id).Scan(&s); err != nil {
		t.Fatalf("read status: %v", err)
	}
	return s
}

func providerRefOf(t *testing.T, id uuid.UUID) string {
	t.Helper()
	var s string
	if err := recPool.QueryRow(context.Background(),
		`SELECT COALESCE(provider_order_id, COALESCE(provider_ref,''))
		   FROM payments.payment_intents WHERE id=$1`, id).Scan(&s); err != nil {
		t.Fatalf("read provider ref: %v", err)
	}
	return s
}

func countBy(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := recPool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// requireNoTerminalEffect asserts MRC-1.4: nothing at all was written.
func requireNoTerminalEffect(t *testing.T, si staleIntent, why string) {
	t.Helper()
	if s := statusOf(t, si.id); s != "pending" {
		t.Fatalf("%s: intent moved to %q, want pending", why, s)
	}
	if n := countBy(t,
		`SELECT count(*) FROM payments.outbox_events WHERE partition_key=$1`,
		si.referenceID.String()); n != 0 {
		t.Fatalf("%s: %d outbox row(s) written", why, n)
	}
	if n := countBy(t,
		`SELECT count(*) FROM payments.provider_events WHERE provider_order_id=$1`,
		si.providerOrder); si.providerOrder != "" && n != 0 {
		t.Fatalf("%s: %d provider-inbox row(s) written", why, n)
	}
	if n := countBy(t,
		`SELECT count(*) FROM payments.payment_audit_log WHERE intent_id=$1 AND event='provider_webhook'`,
		si.id); n != 0 {
		t.Fatalf("%s: %d terminal audit row(s) written", why, n)
	}
}

func svcWith(t *testing.T, p gateway.Provider) *Service {
	t.Helper()
	return New(postgres.New(recPool), nil).WithProvider(p)
}

// ─── MRC-1: the full provider money tuple ────────────────────────────

// The positive contract, end to end: recorded Razorpay bytes → real adapter →
// reconciler → live PostgreSQL. This is the proof that would have caught the
// dropped currency, because nothing here fills the field in for the adapter.
func TestReconcileAppliesACapturedPaymentWhoseWebhookWasLost(t *testing.T) {
	ctx := context.Background()
	const amt = 118000
	si := seedStale(t, amt, "INR", "order_lost_"+uuid.NewString()[:8])

	stub := newRazorpayStub(t)
	stub.setPayment(capturedPayment("pay_"+uuid.NewString()[:12], si.providerOrder, amt, "INR"))
	svcWith(t, stub.provider()).reconcileOnce(ctx, time.Minute)

	if p, _ := stub.counts(); p == 0 {
		t.Fatal("the reconciler never fetched provider state; the proof is not exercising it")
	}
	if s := statusOf(t, si.id); s != "succeeded" {
		t.Fatalf("intent status = %q, want succeeded", s)
	}
	if n := countBy(t,
		`SELECT count(*) FROM payments.outbox_events
		  WHERE event_type='payment.succeeded' AND partition_key=$1`,
		si.referenceID.String()); n != 1 {
		t.Fatalf("outbox rows = %d, want exactly 1", n)
	}
}

// MRC-1.5: a second pass over an already-reconciled intent changes nothing.
func TestReconcileIsIdempotentAcrossRestarts(t *testing.T) {
	ctx := context.Background()
	const amt = 90000
	si := seedStale(t, amt, "INR", "order_idem_"+uuid.NewString()[:8])

	stub := newRazorpayStub(t)
	stub.setPayment(capturedPayment("pay_"+uuid.NewString()[:12], si.providerOrder, amt, "INR"))
	svc := svcWith(t, stub.provider())

	svc.reconcileOnce(ctx, time.Minute)
	svc.reconcileOnce(ctx, time.Minute) // restart / next tick

	if n := countBy(t,
		`SELECT count(*) FROM payments.outbox_events
		  WHERE event_type='payment.succeeded' AND partition_key=$1`,
		si.referenceID.String()); n != 1 {
		t.Fatalf("outbox rows = %d after two passes, want exactly 1", n)
	}
}

// THE MRC-1 regression: same amount, wrong currency. Before this pass the
// production adapter dropped currency, the store treated blank as "do not
// compare", and this settled.
func TestReconcileRefusesAMatchingAmountInTheWrongCurrency(t *testing.T) {
	ctx := context.Background()
	const amt = 118000
	si := seedStale(t, amt, "INR", "order_ccy_"+uuid.NewString()[:8])

	stub := newRazorpayStub(t)
	stub.setPayment(capturedPayment("pay_"+uuid.NewString()[:12], si.providerOrder, amt, "USD"))
	svcWith(t, stub.provider()).reconcileOnce(ctx, time.Minute)

	requireNoTerminalEffect(t, si, "same amount in USD against an INR intent")
}

// A capture whose payload carries NO currency at all is refused, not defaulted.
func TestReconcileRefusesACaptureWithNoCurrency(t *testing.T) {
	ctx := context.Background()
	const amt = 118000
	si := seedStale(t, amt, "INR", "order_noccy_"+uuid.NewString()[:8])

	stub := newRazorpayStub(t)
	stub.setPayment(capturedPayment("pay_"+uuid.NewString()[:12], si.providerOrder, amt, "")) // field absent
	svcWith(t, stub.provider()).reconcileOnce(ctx, time.Minute)

	requireNoTerminalEffect(t, si, "captured payment with no currency")
}

func TestReconcileRefusesAWrongAmount(t *testing.T) {
	ctx := context.Background()
	si := seedStale(t, 118000, "INR", "order_amt_"+uuid.NewString()[:8])

	stub := newRazorpayStub(t)
	stub.setPayment(capturedPayment("pay_"+uuid.NewString()[:12], si.providerOrder, 1, "INR"))
	svcWith(t, stub.provider()).reconcileOnce(ctx, time.Minute)

	requireNoTerminalEffect(t, si, "1 paise against a 118000 paise intent")
}

func TestReconcileRefusesAZeroAmount(t *testing.T) {
	ctx := context.Background()
	si := seedStale(t, 118000, "INR", "order_zero_"+uuid.NewString()[:8])

	stub := newRazorpayStub(t)
	stub.setPayment(capturedPayment("pay_"+uuid.NewString()[:12], si.providerOrder, 0, "INR"))
	svcWith(t, stub.provider()).reconcileOnce(ctx, time.Minute)

	requireNoTerminalEffect(t, si, "zero-amount capture")
}

// A capture with no payment id cannot be recorded against an inbox key.
func TestReconcileRefusesACaptureWithNoPaymentID(t *testing.T) {
	ctx := context.Background()
	const amt = 118000
	si := seedStale(t, amt, "INR", "order_noid_"+uuid.NewString()[:8])

	stub := newRazorpayStub(t)
	stub.setPayment(capturedPayment("", si.providerOrder, amt, "INR"))
	svcWith(t, stub.provider()).reconcileOnce(ctx, time.Minute)

	requireNoTerminalEffect(t, si, "capture with no payment id")
}

// ─── MRC-2: blank provider reference is repairable ───────────────────

// THE MRC-2 regression: the loop's first statement used to be
// `if intent.ProviderRef == "" { continue }`, so this intent was skipped
// forever while the handover claimed the reconciler owned it.
func TestReconcileRepairsABlankProviderReference(t *testing.T) {
	ctx := context.Background()
	const amt = 118000
	si := seedStale(t, amt, "INR", "") // blank reference

	recovered := "order_recovered_" + uuid.NewString()[:8]
	stub := newRazorpayStub(t)
	stub.setOrders(orderList(rzpOrder(recovered, amt, "INR", "created")))
	stub.setPayment(capturedPayment("pay_"+uuid.NewString()[:12], recovered, amt, "INR"))

	svcWith(t, stub.provider()).reconcileOnce(ctx, time.Minute)

	if _, o := stub.counts(); o == 0 {
		t.Fatal("the reconciler never looked the order up by idempotency key")
	}
	if got := providerRefOf(t, si.id); got != recovered {
		t.Fatalf("provider reference = %q, want %q — the blank reference was not repaired", got, recovered)
	}
	if s := statusOf(t, si.id); s != "succeeded" {
		t.Fatalf("intent status = %q, want succeeded after repair + reconcile", s)
	}
}

// MRC-2.3: nothing at the provider under this key. Not an error, not a
// success — try again next tick, with no reference attached.
func TestReconcileLeavesABlankReferenceWhenTheProviderHasNothing(t *testing.T) {
	ctx := context.Background()
	si := seedStale(t, 118000, "INR", "")

	stub := newRazorpayStub(t)
	stub.setOrders(orderList()) // zero items
	svcWith(t, stub.provider()).reconcileOnce(ctx, time.Minute)

	if got := providerRefOf(t, si.id); got != "" {
		t.Fatalf("provider reference = %q, want empty — nothing existed to adopt", got)
	}
	requireNoTerminalEffect(t, si, "provider holds nothing under the key")
}

// MRC-2.4: two orders under one deterministic key is an ambiguity.
func TestReconcileRefusesMultipleOrdersForOneIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	const amt = 118000
	si := seedStale(t, amt, "INR", "")

	stub := newRazorpayStub(t)
	stub.setOrders(orderList(
		rzpOrder("order_dup_a", amt, "INR", "created"),
		rzpOrder("order_dup_b", amt, "INR", "created"),
	))
	svcWith(t, stub.provider()).reconcileOnce(ctx, time.Minute)

	if got := providerRefOf(t, si.id); got != "" {
		t.Fatalf("provider reference = %q; an ambiguous lookup must attach nothing", got)
	}
	requireNoTerminalEffect(t, si, "two orders under one key")
}

// MRC-2.2: a recovered order whose tuple disagrees is never attached —
// attaching it would point our intent at someone else's money.
func TestReconcileRefusesARecoveredOrderWithTheWrongAmount(t *testing.T) {
	ctx := context.Background()
	si := seedStale(t, 118000, "INR", "")

	stub := newRazorpayStub(t)
	stub.setOrders(orderList(rzpOrder("order_wrongamt", 999, "INR", "created")))
	svcWith(t, stub.provider()).reconcileOnce(ctx, time.Minute)

	if got := providerRefOf(t, si.id); got != "" {
		t.Fatalf("provider reference = %q; a wrong-amount recovery must not attach", got)
	}
	requireNoTerminalEffect(t, si, "recovered order with the wrong amount")
}

func TestReconcileRefusesARecoveredOrderWithTheWrongCurrency(t *testing.T) {
	ctx := context.Background()
	const amt = 118000
	si := seedStale(t, amt, "INR", "")

	stub := newRazorpayStub(t)
	stub.setOrders(orderList(rzpOrder("order_wrongccy", amt, "USD", "created")))
	svcWith(t, stub.provider()).reconcileOnce(ctx, time.Minute)

	if got := providerRefOf(t, si.id); got != "" {
		t.Fatalf("provider reference = %q; a wrong-currency recovery must not attach", got)
	}
	requireNoTerminalEffect(t, si, "recovered order with the wrong currency")
}

// MRC-2.5: restart-safety. A run that dies after the provider lookup but
// before the local attach must converge on the SAME order next time, and
// create no duplicate effect.
func TestReconcileRepairIsRestartSafe(t *testing.T) {
	ctx := context.Background()
	const amt = 77000
	si := seedStale(t, amt, "INR", "")

	recovered := "order_restart_" + uuid.NewString()[:8]
	stub := newRazorpayStub(t)
	stub.setOrders(orderList(rzpOrder(recovered, amt, "INR", "created")))
	stub.setPayment(capturedPayment("pay_"+uuid.NewString()[:12], recovered, amt, "INR"))
	svc := svcWith(t, stub.provider())

	// First run: repairs and reconciles.
	svc.reconcileOnce(ctx, time.Minute)
	// "Restart": the same work is attempted again from scratch.
	svc.reconcileOnce(ctx, time.Minute)

	if got := providerRefOf(t, si.id); got != recovered {
		t.Fatalf("provider reference = %q, want %q", got, recovered)
	}
	if n := countBy(t,
		`SELECT count(*) FROM payments.outbox_events
		  WHERE event_type='payment.succeeded' AND partition_key=$1`,
		si.referenceID.String()); n != 1 {
		t.Fatalf("outbox rows = %d across a restart, want exactly 1", n)
	}
}

// MRC-2.3: a concurrent repair that already attached a DIFFERENT reference is
// a conflict, and must not be overwritten.
func TestReconcileDoesNotOverwriteAConcurrentlyAttachedReference(t *testing.T) {
	ctx := context.Background()
	const amt = 64000
	si := seedStale(t, amt, "INR", "")

	// Another worker won the race and attached its own reference.
	other := "order_other_" + uuid.NewString()[:8]
	if err := postgres.New(recPool).SetProviderOrder(ctx, si.id, other); err != nil {
		t.Fatalf("simulating a concurrent attach: %v", err)
	}

	stub := newRazorpayStub(t)
	stub.setOrders(orderList(rzpOrder("order_mine", amt, "INR", "created")))
	stub.setPayment(capturedPayment("pay_"+uuid.NewString()[:12], other, amt, "INR"))
	svcWith(t, stub.provider()).reconcileOnce(ctx, time.Minute)

	if got := providerRefOf(t, si.id); got != other {
		t.Fatalf("provider reference = %q, want %q — a concurrent attach must not be overwritten", got, other)
	}
}

// MRC-2.6: a provider that cannot look up by key cannot repair. The intent is
// left pending and the inability is reported, not papered over.
type noLookupProvider struct{ gateway.Provider }

func (noLookupProvider) Name() string { return "nolookup" }
func (noLookupProvider) FetchByIdempotencyKey(context.Context, string) (gateway.ProviderPaymentState, error) {
	return gateway.ProviderPaymentState{}, gateway.ErrLookupNotSupported
}

func TestReconcileCannotRepairWhenTheProviderHasNoLookup(t *testing.T) {
	ctx := context.Background()
	si := seedStale(t, 118000, "INR", "")

	svcWith(t, noLookupProvider{}).reconcileOnce(ctx, time.Minute)

	if got := providerRefOf(t, si.id); got != "" {
		t.Fatalf("provider reference = %q; a provider with no lookup cannot repair one", got)
	}
	requireNoTerminalEffect(t, si, "provider without lookup-by-key")
}
