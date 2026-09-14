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
	"errors"
	"fmt"
	"log/slog"
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

// razorpayStub serves the endpoints the recovery paths use, under the same
// `/v1` prefix production's base URL carries:
//
//	GET /v1/orders/{order_id}/payments → the order's payments, which the reconciler reads
//	GET /v1/orders?receipt={key}       → the idempotency-key lookup MRC-2 repairs by
//
// `provider_ref` holds a Razorpay ORDER id. The reconciler used to send it to
// GET /v1/payments/{id}, which Razorpay answers with 400 because an order id is
// not a payment id, so every tick logged an error and a payment whose webhook
// was lost was never reconciled. The stub answers that call the same way and
// records it as a violation, which fails the test that made it.
//
// Bodies are raw JSON strings in Razorpay's documented shape, so the
// assertions exercise the adapter's own decoder rather than a struct a test
// filled in.
type razorpayStub struct {
	srv *httptest.Server

	mu sync.Mutex
	// orderPayments maps an order id to the raw payment entities under it.
	orderPayments     map[string][]string
	ordersBody        string
	orderPaymentCalls int
	orderCalls        int
	orderPaymentsCode int
	ordersCode        int
	paths             []string
	violations        []string
}

func newRazorpayStub(t *testing.T) *razorpayStub {
	t.Helper()
	s := &razorpayStub{
		orderPayments:     map[string][]string{},
		orderPaymentsCode: http.StatusOK,
		ordersCode:        http.StatusOK,
	}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		path := r.URL.Path
		s.paths = append(s.paths, path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(path, "/v1/orders/") && strings.HasSuffix(path, "/payments"):
			s.orderPaymentCalls++
			orderID := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/orders/"), "/payments")
			items := s.orderPayments[orderID]
			w.WriteHeader(s.orderPaymentsCode)
			_, _ = fmt.Fprintf(w, `{"entity":"collection","count":%d,"items":[%s]}`,
				len(items), strings.Join(items, ","))
		case strings.HasPrefix(path, "/v1/payments/"):
			if strings.HasPrefix(strings.TrimPrefix(path, "/v1/payments/"), "order_") {
				s.violations = append(s.violations, path)
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"BAD_REQUEST_ERROR","description":"The id provided does not exist"}}`))
		case path == "/v1/orders":
			s.orderCalls++
			w.WriteHeader(s.ordersCode)
			_, _ = w.Write([]byte(s.ordersBody))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.srv.Close)
	t.Cleanup(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if len(s.violations) > 0 {
			t.Errorf("an ORDER id was fetched as a PAYMENT id: %v", s.violations)
		}
	})
	return s
}

// setPayment records a payment as existing at Razorpay under the order its
// body names, so GET /v1/orders/{order_id}/payments lists it.
func (s *razorpayStub) setPayment(body string) {
	var p struct {
		OrderID string `json:"order_id"`
	}
	_ = json.Unmarshal([]byte(body), &p)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.orderPayments[p.OrderID] = append(s.orderPayments[p.OrderID], body)
}

func (s *razorpayStub) setOrders(body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ordersBody = body
}

func (s *razorpayStub) setOrderPaymentsCode(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.orderPaymentsCode = code
}

// counts reports how often the order-payments collection and the
// receipt lookup were read.
func (s *razorpayStub) counts() (orderPayments, orders int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.orderPaymentCalls, s.orderCalls
}

func (s *razorpayStub) requestedPaths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.paths...)
}

// requireReadOrderPayments fails unless the reconciler read this order's
// payments collection. Without it a refusal test passes trivially when the
// lookup never reaches the provider at all.
func (s *razorpayStub) requireReadOrderPayments(t *testing.T, orderID string) {
	t.Helper()
	want := "/v1/orders/" + orderID + "/payments"
	for _, p := range s.requestedPaths() {
		if p == want {
			return
		}
	}
	t.Fatalf("the reconciler never read %s; requests were %v", want, s.requestedPaths())
}

// provider returns the REAL Razorpay adapter pointed at the stub.
func (s *razorpayStub) provider() *gateway.RazorpayProvider {
	return gateway.NewRazorpayProvider("rzp_test", "secret", "whsec").
		WithEndpoint(s.srv.URL+"/v1", s.srv.Client())
}

// rzpPayment is a recorded Razorpay payment entity. createdAt 0 omits it.
func rzpPayment(id, orderID string, amount int64, currency, status string, createdAt int64) string {
	m := map[string]any{
		"id": id, "entity": "payment", "order_id": orderID, "amount": amount,
		"status": status, "method": "upi",
	}
	if currency != "" {
		m["currency"] = currency
	}
	if createdAt > 0 {
		m["created_at"] = createdAt
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// capturedPayment is a recorded captured Razorpay payment entity.
func capturedPayment(id, orderID string, amount int64, currency string) string {
	return rzpPayment(id, orderID, amount, currency, "captured", 0)
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

	stub.requireReadOrderPayments(t, si.providerOrder)
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

	stub.requireReadOrderPayments(t, si.providerOrder)
	requireNoTerminalEffect(t, si, "captured payment with no currency")
}

func TestReconcileRefusesAWrongAmount(t *testing.T) {
	ctx := context.Background()
	si := seedStale(t, 118000, "INR", "order_amt_"+uuid.NewString()[:8])

	stub := newRazorpayStub(t)
	stub.setPayment(capturedPayment("pay_"+uuid.NewString()[:12], si.providerOrder, 1, "INR"))
	svcWith(t, stub.provider()).reconcileOnce(ctx, time.Minute)

	stub.requireReadOrderPayments(t, si.providerOrder)
	requireNoTerminalEffect(t, si, "1 paise against a 118000 paise intent")
}

func TestReconcileRefusesAZeroAmount(t *testing.T) {
	ctx := context.Background()
	si := seedStale(t, 118000, "INR", "order_zero_"+uuid.NewString()[:8])

	stub := newRazorpayStub(t)
	stub.setPayment(capturedPayment("pay_"+uuid.NewString()[:12], si.providerOrder, 0, "INR"))
	svcWith(t, stub.provider()).reconcileOnce(ctx, time.Minute)

	stub.requireReadOrderPayments(t, si.providerOrder)
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

	stub.requireReadOrderPayments(t, si.providerOrder)
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

// ─── The order-vs-payment lookup ─────────────────────────────────────
//
// provider_ref holds the Razorpay ORDER id. These pin the reconciler to the
// order's payments collection, the outcome to the webhook's own status
// mapping, and the effect to the webhook's exactly-once transaction.

// logSink captures slog records so a test can assert what was (not) logged.
type logSink struct {
	mu   sync.Mutex
	recs []slog.Record
}

func captureLogs(t *testing.T) *logSink {
	t.Helper()
	sink := &logSink{}
	prev := slog.Default()
	slog.SetDefault(slog.New(sink))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return sink
}

func (l *logSink) Enabled(context.Context, slog.Level) bool { return true }
func (l *logSink) WithAttrs([]slog.Attr) slog.Handler       { return l }
func (l *logSink) WithGroup(string) slog.Handler            { return l }
func (l *logSink) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.recs = append(l.recs, r.Clone())
	return nil
}

// atOrAbove returns the messages logged at `level` or louder.
func (l *logSink) atOrAbove(level slog.Level) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, r := range l.recs {
		if r.Level >= level {
			out = append(out, r.Level.String()+" "+r.Message)
		}
	}
	return out
}

// mentioning returns the messages carrying an attribute equal to value.
func (l *logSink) mentioning(value string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, r := range l.recs {
		r.Attrs(func(a slog.Attr) bool {
			if a.Value.String() == value {
				out = append(out, r.Message)
				return false
			}
			return true
		})
	}
	return out
}

func requireOutboxEvents(t *testing.T, si staleIntent, eventType string, want int, when string) {
	t.Helper()
	if n := countBy(t,
		`SELECT count(*) FROM payments.outbox_events WHERE event_type=$1 AND partition_key=$2`,
		eventType, si.referenceID.String()); n != want {
		t.Fatalf("%s: %d %s outbox row(s), want %d", when, n, eventType, want)
	}
}

// The defect end to end: a captured payment under the order, its webhook lost.
// The reconciler must read /v1/orders/{order_id}/payments, settle the intent,
// and publish payment.succeeded exactly once, across a second tick AND the
// real webhook turning up late under its own event id.
func TestReconcileReadsTheOrdersPaymentsAndSettlesExactlyOnce(t *testing.T) {
	ctx := context.Background()
	const amt = 118000
	si := seedStale(t, amt, "INR", "order_lostwh_"+uuid.NewString()[:8])
	payID := "pay_" + uuid.NewString()[:12]

	stub := newRazorpayStub(t)
	stub.setPayment(capturedPayment(payID, si.providerOrder, amt, "INR"))
	svc := svcWith(t, stub.provider())

	svc.reconcileOnce(ctx, time.Minute)
	stub.requireReadOrderPayments(t, si.providerOrder)
	if s := statusOf(t, si.id); s != "succeeded" {
		t.Fatalf("intent status = %q, want succeeded", s)
	}
	requireOutboxEvents(t, si, "payment.succeeded", 1, "after the first tick")

	requestsAfterFirst := len(stub.requestedPaths())
	svc.reconcileOnce(ctx, time.Minute)
	requireOutboxEvents(t, si, "payment.succeeded", 1, "after a second tick")
	if n := len(stub.requestedPaths()); n != requestsAfterFirst {
		t.Fatalf("a second tick asked the provider about a settled intent (%d -> %d requests)",
			requestsAfterFirst, n)
	}

	// The real webhook, delivered late under Razorpay's own event id.
	err := svc.ApplyWebhook(ctx, WebhookInput{
		Provider:          "razorpay",
		EventID:           "evt_late_" + uuid.NewString()[:12],
		EventType:         "payment.captured",
		ProviderOrderID:   si.providerOrder,
		ProviderPaymentID: payID,
		AmountMinor:       amt,
		Currency:          "INR",
	})
	if err != nil && !errors.Is(err, ErrWebhookDuplicate) {
		t.Fatalf("late webhook: %v", err)
	}
	requireOutboxEvents(t, si, "payment.succeeded", 1, "after the late webhook")
	if n := countBy(t,
		`SELECT count(*) FROM payments.outbox_events WHERE partition_key=$1`,
		si.referenceID.String()); n != 1 {
		t.Fatalf("outbox rows of any type = %d, want exactly 1", n)
	}
}

// An order nobody has paid against yet: pending, silent, no event.
func TestReconcileLeavesAnOrderWithNoPaymentsPendingWithoutComplaint(t *testing.T) {
	ctx := context.Background()
	si := seedStale(t, 118000, "INR", "order_nopay_"+uuid.NewString()[:8])

	stub := newRazorpayStub(t) // the order has an empty payments collection
	logs := captureLogs(t)
	svc := svcWith(t, stub.provider())
	svc.reconcileOnce(ctx, time.Minute)
	svc.reconcileOnce(ctx, time.Minute)

	stub.requireReadOrderPayments(t, si.providerOrder)
	requireNoTerminalEffect(t, si, "an order with no payments")
	if loud := logs.atOrAbove(slog.LevelWarn); len(loud) > 0 {
		t.Fatalf("no payments yet is not a failure, but the reconciler logged: %v", loud)
	}
}

// A stub gateway's order reference was never a Razorpay object. It must not
// reach the provider, must not change, and must not be logged every tick.
func TestReconcileNeverSendsAStubReferenceToTheProvider(t *testing.T) {
	ctx := context.Background()
	const amt = 118000
	si := seedStale(t, amt, "INR", fmt.Sprintf("order_stub_%d", time.Now().UnixNano()))

	stub := newRazorpayStub(t)
	// Even a "captured" payment under that id must not be consulted.
	stub.setPayment(capturedPayment("pay_"+uuid.NewString()[:12], si.providerOrder, amt, "INR"))
	logs := captureLogs(t)
	svc := svcWith(t, stub.provider())
	svc.reconcileOnce(ctx, time.Minute)
	svc.reconcileOnce(ctx, time.Minute)

	if paths := stub.requestedPaths(); len(paths) != 0 {
		t.Fatalf("a stub order reference reached the provider: %v", paths)
	}
	requireNoTerminalEffect(t, si, "stub order reference")
	if n := len(logs.mentioning(si.id.String())); n > 1 {
		t.Fatalf("the stub skip was logged %d times across two ticks, want at most once", n)
	}
}

// Mirrors the webhook: `payment.authorized` maps to no status change, so an
// authorized-but-uncaptured payment leaves the intent pending.
func TestReconcileLeavesAnAuthorizedPaymentPending(t *testing.T) {
	ctx := context.Background()
	const amt = 118000
	si := seedStale(t, amt, "INR", "order_auth_"+uuid.NewString()[:8])

	stub := newRazorpayStub(t)
	stub.setPayment(rzpPayment("pay_"+uuid.NewString()[:12], si.providerOrder, amt, "INR", "authorized", 0))
	svcWith(t, stub.provider()).reconcileOnce(ctx, time.Minute)

	stub.requireReadOrderPayments(t, si.providerOrder)
	requireNoTerminalEffect(t, si, "authorized, not captured")
}

// Mirrors the webhook's `payment.failed` → failed, but only when EVERY attempt
// on the order failed.
func TestReconcileFailsAnOrderWhosePaymentsAllFailed(t *testing.T) {
	ctx := context.Background()
	const amt = 118000
	si := seedStale(t, amt, "INR", "order_allfail_"+uuid.NewString()[:8])

	stub := newRazorpayStub(t)
	stub.setPayment(rzpPayment("pay_"+uuid.NewString()[:12], si.providerOrder, amt, "INR", "failed", 1700000100))
	stub.setPayment(rzpPayment("pay_"+uuid.NewString()[:12], si.providerOrder, amt, "INR", "failed", 1700000200))
	svc := svcWith(t, stub.provider())
	svc.reconcileOnce(ctx, time.Minute)
	svc.reconcileOnce(ctx, time.Minute)

	stub.requireReadOrderPayments(t, si.providerOrder)
	if s := statusOf(t, si.id); s != "failed" {
		t.Fatalf("intent status = %q, want failed", s)
	}
	requireOutboxEvents(t, si, "payment.failed", 1, "all attempts failed")
	requireOutboxEvents(t, si, "payment.succeeded", 0, "all attempts failed")
}

// A customer's first attempt failed and the retry on the same order was
// captured. The capture wins; nothing is published as failed.
func TestReconcileSettlesACaptureThatFollowedAFailedAttempt(t *testing.T) {
	ctx := context.Background()
	const amt = 118000
	si := seedStale(t, amt, "INR", "order_retry_"+uuid.NewString()[:8])

	stub := newRazorpayStub(t)
	stub.setPayment(rzpPayment("pay_"+uuid.NewString()[:12], si.providerOrder, amt, "INR", "failed", 1700000100))
	stub.setPayment(rzpPayment("pay_"+uuid.NewString()[:12], si.providerOrder, amt, "INR", "captured", 1700000200))
	svcWith(t, stub.provider()).reconcileOnce(ctx, time.Minute)

	stub.requireReadOrderPayments(t, si.providerOrder)
	if s := statusOf(t, si.id); s != "succeeded" {
		t.Fatalf("intent status = %q, want succeeded", s)
	}
	requireOutboxEvents(t, si, "payment.succeeded", 1, "capture after a failed attempt")
	requireOutboxEvents(t, si, "payment.failed", 0, "capture after a failed attempt")
}

// A provider error is retried with backoff, not re-requested and re-logged on
// every tick.
func TestReconcileBacksOffAProviderErrorInsteadOfLoggingEveryTick(t *testing.T) {
	ctx := context.Background()
	si := seedStale(t, 118000, "INR", "order_rzp5xx_"+uuid.NewString()[:8])

	stub := newRazorpayStub(t)
	stub.setOrderPaymentsCode(http.StatusBadGateway)
	logs := captureLogs(t)
	svc := svcWith(t, stub.provider())
	for i := 0; i < 3; i++ {
		svc.reconcileOnce(ctx, time.Minute)
	}

	if c, _ := stub.counts(); c != 1 {
		t.Fatalf("the order's payments were requested %d times in three immediate ticks, want 1 (backoff)", c)
	}
	if loud := logs.atOrAbove(slog.LevelWarn); len(loud) != 1 {
		t.Fatalf("logged %d warnings for one failing lookup across three ticks, want 1: %v", len(loud), loud)
	}
	requireNoTerminalEffect(t, si, "provider error")
}
