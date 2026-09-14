//go:build integration

package service

// The refund worker refunds a PAYMENT, and knows which provider refusals are
// final.
//
// ─── THE DEFECT ─────────────────────────────────────────────────────────
//
// attemptRefund handed the intent's provider reference — the Razorpay ORDER
// id — to POST /v1/payments/{id}/refund. Razorpay answers an order id on a
// payments route with 400, on every attempt, so no Razorpay refund could ever
// succeed. And because every provider error was treated as transient, each of
// those commands retried for ever: on the dev stack four commands sat
// `pending` at 14–18 attempts with "POST /payments/order_…/refund returned
// 400" in last_error.
//
// Same rig as reconcile_n2: the REAL Razorpay adapter against an httptest
// server, a live PostgreSQL (payments_it_test). The server answers any
// /v1/payments/order_… request the way Razorpay does AND records it as a
// violation, which fails the test that made it.
//
//	PAYMENTS_TEST_DSN=... go test -tags=integration ./internal/service/... -v

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/atpost/payments-service/internal/gateway"
	"github.com/google/uuid"
)

// ─── A scripted Razorpay ─────────────────────────────────────────────

type rwReply struct {
	code int
	body string
}

type rwRazorpay struct {
	srv *httptest.Server

	mu sync.Mutex
	// refunds scripts POST /v1/payments/{payment_id}/refund per payment id.
	// Replies are consumed in order and the last one repeats.
	refunds       map[string][]rwReply
	orderPayments map[string]rwReply // GET /v1/orders/{order_id}/payments
	refundLists   map[string]rwReply // GET /v1/payments/{payment_id}/refunds
	requests      []string
	idemKeys      []string
	violations    []string
}

func newRWRazorpay(t *testing.T) *rwRazorpay {
	t.Helper()
	s := &rwRazorpay{
		refunds:       map[string][]rwReply{},
		orderPayments: map[string]rwReply{},
		refundLists:   map[string]rwReply{},
	}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		path := r.URL.Path
		s.requests = append(s.requests, r.Method+" "+path)
		w.Header().Set("Content-Type", "application/json")
		reply := func(rep rwReply) {
			w.WriteHeader(rep.code)
			_, _ = w.Write([]byte(rep.body))
		}

		if rest, ok := strings.CutPrefix(path, "/v1/payments/"); ok && strings.HasPrefix(rest, "order_") {
			// What Razorpay does with an order id on a payments route.
			s.violations = append(s.violations, r.Method+" "+path)
			reply(rwReply{http.StatusBadRequest, rwError("The id provided does not exist", "input_validation_failed")})
			return
		}

		switch {
		case r.Method == http.MethodPost && strings.HasPrefix(path, "/v1/payments/") && strings.HasSuffix(path, "/refund"):
			id := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/payments/"), "/refund")
			s.idemKeys = append(s.idemKeys, r.Header.Get("X-Refund-Idempotency"))
			if script := s.refunds[id]; len(script) > 0 {
				if len(script) > 1 {
					s.refunds[id] = script[1:]
				}
				reply(script[0])
				return
			}
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/orders/") && strings.HasSuffix(path, "/payments"):
			if rep, ok := s.orderPayments[strings.TrimSuffix(strings.TrimPrefix(path, "/v1/orders/"), "/payments")]; ok {
				reply(rep)
				return
			}
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/payments/") && strings.HasSuffix(path, "/refunds"):
			if rep, ok := s.refundLists[strings.TrimSuffix(strings.TrimPrefix(path, "/v1/payments/"), "/refunds")]; ok {
				reply(rep)
				return
			}
		}
		s.violations = append(s.violations, "unscripted "+r.Method+" "+path)
		reply(rwReply{http.StatusNotFound, `{"error":{"code":"BAD_REQUEST_ERROR","description":"The requested URL was not found on the server."}}`})
	}))
	t.Cleanup(s.srv.Close)
	t.Cleanup(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if len(s.violations) > 0 {
			t.Errorf("requests Razorpay must never receive: %v", s.violations)
		}
	})
	return s
}

func (s *rwRazorpay) provider() *gateway.RazorpayProvider {
	return gateway.NewRazorpayProvider("rzp_test", "secret", "whsec").
		WithEndpoint(s.srv.URL+"/v1", s.srv.Client())
}

func (s *rwRazorpay) scriptRefund(paymentID string, replies ...rwReply) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refunds[paymentID] = replies
}

func (s *rwRazorpay) scriptOrderPayments(orderID string, rep rwReply) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.orderPayments[orderID] = rep
}

func (s *rwRazorpay) scriptRefundList(paymentID string, rep rwReply) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refundLists[paymentID] = rep
}

// count reports how many requests matched "METHOD path" exactly.
func (s *rwRazorpay) count(methodPath string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.requests {
		if r == methodPath {
			n++
		}
	}
	return n
}

func (s *rwRazorpay) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

func (s *rwRazorpay) keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.idemKeys...)
}

// ─── Recorded Razorpay bodies ────────────────────────────────────────

func rwOK(body string) rwReply { return rwReply{http.StatusOK, body} }

func rwCollection(items ...string) string {
	return fmt.Sprintf(`{"entity":"collection","count":%d,"items":[%s]}`, len(items), strings.Join(items, ","))
}

func rwRefundEntity(id, paymentID string, amount int64, status string) string {
	b, _ := json.Marshal(map[string]any{
		"id": id, "entity": "refund", "amount": amount, "currency": "INR",
		"payment_id": paymentID, "status": status, "speed_processed": "normal",
	})
	return string(b)
}

// rwRawBodyMarker sits in the error body's metadata. It must never reach
// last_error: only the status, code, reason and a bounded description may.
const rwRawBodyMarker = "rw-raw-body-marker"

func rwError(description, reason string) string {
	b, _ := json.Marshal(map[string]any{"error": map[string]any{
		"code": "BAD_REQUEST_ERROR", "description": description, "source": "business",
		"step": "payment_initiation", "reason": reason,
		"metadata": map[string]any{"note": rwRawBodyMarker},
	}})
	return string(b)
}

// ─── Fixtures ────────────────────────────────────────────────────────

type rwPaid struct {
	id      uuid.UUID
	order   string
	payment string
	amount  int64
}

// rwIDs returns a fresh Razorpay-shaped order id and payment id.
func rwIDs() (order, payment string) {
	s := strings.ReplaceAll(uuid.New().String(), "-", "")[:14]
	return "order_" + s, "pay_" + s
}

// rwSeedPaid is a captured intent, as the webhook leaves one: succeeded, with
// the provider ORDER id in provider_ref/provider_order_id and, when
// storedPayment is not blank, the captured PAYMENT id in provider_payment_id.
func rwSeedPaid(t *testing.T, amountMinor int64, order, storedPayment string) rwPaid {
	t.Helper()
	pi := rwPaid{id: uuid.New(), order: order, payment: storedPayment, amount: amountMinor}
	_, err := recPool.Exec(context.Background(), `
		INSERT INTO payments.payment_intents
		    (id, payer_id, payee_id, reference_type, reference_id, amount, amount_minor,
		     currency, method, status, provider, provider_ref, provider_order_id,
		     provider_payment_id, owner_domain, idempotency_key, created_at)
		VALUES ($1,$2,$3,'order',$4,$5,$6,'INR','upi','succeeded','razorpay',$7,$7,
		        NULLIF($8,''),'commerce',$9,NOW())`,
		pi.id, uuid.New(), uuid.New(), uuid.New(),
		float64(amountMinor)/100.0, amountMinor, order, storedPayment, "idem-"+pi.id.String())
	if err != nil {
		t.Fatalf("seed paid intent: %v", err)
	}
	return pi
}

func rwKey(pi rwPaid) string { return "rw-refund-" + pi.id.String() }

// rwRequestRefund asks for a full refund the way commerce does.
func rwRequestRefund(t *testing.T, svc *Service, pi rwPaid) uuid.UUID {
	t.Helper()
	cmd, err := svc.RequestRefund(context.Background(), RefundRequest{
		IntentID:               pi.id,
		AmountMinor:            pi.amount,
		Reason:                 "refund worker proof",
		ProviderIdempotencyKey: rwKey(pi),
		CallerDomain:           "commerce",
	})
	if err != nil {
		t.Fatalf("request refund: %v", err)
	}
	// Leave nothing claimable behind for a later test's drain.
	t.Cleanup(func() {
		_, _ = recPool.Exec(context.Background(),
			`UPDATE payments.refund_commands SET next_attempt_at = NOW() + INTERVAL '30 days'
			  WHERE id = $1 AND status IN ('pending','submitted')`, cmd.ID)
	})
	return cmd.ID
}

// rwTick is one worker tick with exactly this command due.
//
// Isolation, the same way seedStaleAged does it: drainRefundCommands claims
// every due command in the database, so commands other tests (or earlier runs)
// left claimable would be driven through this test's scripted Razorpay. They
// are pushed out of the window rather than deleted. This command is made due
// whatever its status, so "the next tick makes no call" is proven by the
// status the worker left, not by its backoff.
func rwTick(t *testing.T, svc *Service, cmdID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if _, err := recPool.Exec(ctx,
		`UPDATE payments.refund_commands SET next_attempt_at = NOW() + INTERVAL '30 days'
		  WHERE status IN ('pending','submitted') AND id <> $1`, cmdID); err != nil {
		t.Fatalf("isolating the refund queue: %v", err)
	}
	if _, err := recPool.Exec(ctx,
		`UPDATE payments.refund_commands SET next_attempt_at = NOW() - INTERVAL '1 second' WHERE id = $1`,
		cmdID); err != nil {
		t.Fatalf("making the command due: %v", err)
	}
	svc.drainRefundCommands(ctx)
}

type rwCommand struct {
	status, providerRefundID, lastError string
	attempts                            int
}

func rwReadCommand(t *testing.T, id uuid.UUID) rwCommand {
	t.Helper()
	var c rwCommand
	if err := recPool.QueryRow(context.Background(),
		`SELECT status, COALESCE(provider_refund_id,''), COALESCE(last_error,''), attempts
		   FROM payments.refund_commands WHERE id = $1`, id).
		Scan(&c.status, &c.providerRefundID, &c.lastError, &c.attempts); err != nil {
		t.Fatalf("read refund command: %v", err)
	}
	return c
}

func rwRefundedEvents(t *testing.T, pi rwPaid) int {
	t.Helper()
	return countBy(t,
		`SELECT count(*) FROM payments.outbox_events WHERE event_type='payment.refunded' AND partition_key=$1`,
		pi.id.String())
}

func rwRefundedMinor(t *testing.T, pi rwPaid) int64 {
	t.Helper()
	var n int64
	if err := recPool.QueryRow(context.Background(),
		`SELECT COALESCE(refunded_amount_minor,0) FROM payments.payment_intents WHERE id=$1`, pi.id).Scan(&n); err != nil {
		t.Fatalf("read refunded amount: %v", err)
	}
	return n
}

func rwStoredPayment(t *testing.T, pi rwPaid) string {
	t.Helper()
	var s string
	if err := recPool.QueryRow(context.Background(),
		`SELECT COALESCE(provider_payment_id,'') FROM payments.payment_intents WHERE id=$1`, pi.id).Scan(&s); err != nil {
		t.Fatalf("read provider payment id: %v", err)
	}
	return s
}

// rwRefundWebhook delivers Razorpay's refund.processed for one refund, as the
// webhook handler would after verifying it.
func rwRefundWebhook(t *testing.T, svc *Service, pi rwPaid, paymentID, eventID, refundID string) {
	t.Helper()
	err := svc.ApplyWebhook(context.Background(), WebhookInput{
		Provider:          "razorpay",
		EventID:           eventID,
		EventType:         "refund.processed",
		ProviderPaymentID: paymentID,
		ProviderRefundID:  refundID,
		AmountMinor:       pi.amount,
		Currency:          "INR",
	})
	if err != nil && err != ErrWebhookDuplicate {
		t.Fatalf("refund webhook %s: %v", eventID, err)
	}
}

// ─── The payment id ──────────────────────────────────────────────────

// The defect: the refund goes to the captured PAYMENT the webhook stored, with
// the command's idempotency key, and never to the order.
func TestRefundWorkerRefundsTheStoredPaymentNotTheOrder(t *testing.T) {
	rzp := newRWRazorpay(t)
	order, pay := rwIDs()
	pi := rwSeedPaid(t, 60712, order, pay)
	svc := svcWith(t, rzp.provider())
	rzp.scriptRefund(pay, rwOK(rwRefundEntity("rfnd_"+pay[4:], pay, 60712, "processed")))

	cmd := rwRequestRefund(t, svc, pi)
	rwTick(t, svc, cmd)

	if n := rzp.count("POST /v1/payments/" + pay + "/refund"); n != 1 {
		t.Fatalf("refunds placed against the stored payment %s = %d, want 1; requests were %v",
			pay, n, rzp.all())
	}
	if n := rzp.count("GET /v1/orders/" + order + "/payments"); n != 0 {
		t.Errorf("the payment id was stored, yet the order's payments were listed %d time(s)", n)
	}
	if keys := rzp.keys(); len(keys) != 1 || keys[0] != rwKey(pi) {
		t.Errorf("X-Refund-Idempotency = %v, want exactly [%s]; the key must be the command's own", keys, rwKey(pi))
	}
	if c := rwReadCommand(t, cmd); c.status != "submitted" || c.providerRefundID != "rfnd_"+pay[4:] {
		t.Fatalf("command = %+v, want submitted with the provider refund id", c)
	}
}

// No payment id stored (a legacy intent, or one settled before the column was
// written): the worker lists the order's payments, refunds the captured one
// whose money matches the intent, persists it, and does not look it up again.
func TestRefundWorkerLooksUpThePaymentWhenNoneIsStored(t *testing.T) {
	rzp := newRWRazorpay(t)
	order, pay := rwIDs()
	failed := "pay_failed" + pay[4:12]
	pi := rwSeedPaid(t, 60712, order, "")
	svc := svcWith(t, rzp.provider())
	rzp.scriptOrderPayments(order, rwOK(rwCollection(
		rzpPayment(pay, order, 60712, "INR", "captured", 1700000100),
		rzpPayment(failed, order, 60712, "INR", "failed", 1700000000),
	)))
	rzp.scriptRefund(pay, rwOK(rwRefundEntity("rfnd_"+pay[4:], pay, 60712, "processed")))

	cmd := rwRequestRefund(t, svc, pi)
	rwTick(t, svc, cmd)

	if n := rzp.count("GET /v1/orders/" + order + "/payments"); n != 1 {
		t.Fatalf("order payments listed %d time(s), want 1; requests were %v", n, rzp.all())
	}
	if n := rzp.count("POST /v1/payments/" + pay + "/refund"); n != 1 {
		t.Fatalf("refunds placed against the captured payment = %d, want 1; requests were %v", n, rzp.all())
	}
	if n := rzp.count("POST /v1/payments/" + failed + "/refund"); n != 0 {
		t.Errorf("a refund was placed against the FAILED attempt %s", failed)
	}
	if got := rwStoredPayment(t, pi); got != pay {
		t.Fatalf("provider_payment_id = %q after the lookup, want %q", got, pay)
	}
	if c := rwReadCommand(t, cmd); c.status != "submitted" {
		t.Fatalf("command = %+v, want submitted", c)
	}

	// The submitted command is claimed again (the webhook has not come); that
	// attempt uses the persisted id rather than listing the order again, and
	// sends the same key, so Razorpay returns the same refund.
	rwTick(t, svc, cmd)
	if n := rzp.count("GET /v1/orders/" + order + "/payments"); n != 1 {
		t.Errorf("the second attempt listed the order's payments again (%d lookups)", n)
	}
	if n := rzp.count("POST /v1/payments/" + pay + "/refund"); n != 2 {
		t.Errorf("refund attempts = %d after two ticks, want 2", n)
	}
	for _, k := range rzp.keys() {
		if k != rwKey(pi) {
			t.Errorf("an attempt carried idempotency key %q, want %q", k, rwKey(pi))
		}
	}
}

// A captured payment whose money does not match the intent is not ours to
// refund. With no other candidate the command is parked, and nothing is placed.
func TestRefundWorkerParksWhenNoCapturedPaymentMatchesTheIntent(t *testing.T) {
	rzp := newRWRazorpay(t)
	order, pay := rwIDs()
	pi := rwSeedPaid(t, 60712, order, "")
	svc := svcWith(t, rzp.provider())
	rzp.scriptOrderPayments(order, rwOK(rwCollection(
		rzpPayment(pay, order, 60712, "USD", "captured", 1700000100),
	)))

	cmd := rwRequestRefund(t, svc, pi)
	rwTick(t, svc, cmd)

	if n := rzp.count("GET /v1/orders/" + order + "/payments"); n != 1 {
		t.Fatalf("order payments listed %d time(s), want 1 — without the lookup this proves nothing; requests were %v",
			n, rzp.all())
	}
	if n := rzp.count("POST /v1/payments/" + pay + "/refund"); n != 0 {
		t.Fatalf("a refund was placed against a payment in another currency")
	}
	if c := rwReadCommand(t, cmd); c.status != "needs_attention" {
		t.Fatalf("command = %+v, want needs_attention", c)
	}
	if got := rwStoredPayment(t, pi); got != "" {
		t.Errorf("provider_payment_id = %q; a payment that does not verify must not be attached", got)
	}
}

// ─── Terminal refusals ───────────────────────────────────────────────

// Razorpay does not know the payment (the dev case: a capture simulated by a
// signed test-mode webhook). One call, parked, and never called again.
func TestRefundWorkerParksAPaymentRazorpayDoesNotKnow(t *testing.T) {
	rzp := newRWRazorpay(t)
	order, pay := rwIDs()
	pi := rwSeedPaid(t, 60712, order, pay)
	svc := svcWith(t, rzp.provider())
	rzp.scriptRefund(pay, rwReply{http.StatusBadRequest, rwError("The id provided does not exist", "input_validation_failed")})

	cmd := rwRequestRefund(t, svc, pi)
	rwTick(t, svc, cmd)

	if n := len(rzp.all()); n != 1 {
		t.Fatalf("provider calls = %d, want exactly 1; requests were %v", n, rzp.all())
	}
	c := rwReadCommand(t, cmd)
	if c.status != "needs_attention" {
		t.Fatalf("command = %+v, want needs_attention; a refusal that can never succeed must not be retried", c)
	}
	if !strings.Contains(c.lastError, "does not exist") || !strings.Contains(c.lastError, "400") {
		t.Errorf("last_error = %q; it must carry the status and the provider's description", c.lastError)
	}
	if strings.Contains(c.lastError, rwRawBodyMarker) {
		t.Errorf("last_error = %q; it carries the raw provider body", c.lastError)
	}

	rwTick(t, svc, cmd)
	if n := len(rzp.all()); n != 1 {
		t.Fatalf("the next tick called the provider again (%d calls); requests were %v", n, rzp.all())
	}
	if n := rwRefundedEvents(t, pi); n != 0 {
		t.Errorf("%d payment.refunded event(s) for a refund that never happened", n)
	}
	if n := rwRefundedMinor(t, pi); n != 0 {
		t.Errorf("refunded_amount_minor = %d for a refund that never happened", n)
	}
}

// The order itself is unknown to Razorpay and no payment id is stored: the
// lookup's 400 is just as final.
func TestRefundWorkerParksAnOrderRazorpayDoesNotKnow(t *testing.T) {
	rzp := newRWRazorpay(t)
	order, _ := rwIDs()
	pi := rwSeedPaid(t, 60712, order, "")
	svc := svcWith(t, rzp.provider())
	rzp.scriptOrderPayments(order, rwReply{http.StatusBadRequest, rwError("The id provided does not exist", "input_validation_failed")})

	cmd := rwRequestRefund(t, svc, pi)
	rwTick(t, svc, cmd)
	rwTick(t, svc, cmd)

	if n := len(rzp.all()); n != 1 {
		t.Fatalf("provider calls = %d across two ticks, want exactly 1; requests were %v", n, rzp.all())
	}
	if c := rwReadCommand(t, cmd); c.status != "needs_attention" {
		t.Fatalf("command = %+v, want needs_attention", c)
	}
}

// A stored "payment id" that is really an order id is refused before any
// request is made.
func TestRefundWorkerRefusesAStoredOrderIDAsAPaymentID(t *testing.T) {
	rzp := newRWRazorpay(t)
	order, _ := rwIDs()
	pi := rwSeedPaid(t, 60712, order, order)
	svc := svcWith(t, rzp.provider())

	cmd := rwRequestRefund(t, svc, pi)
	rwTick(t, svc, cmd)

	if reqs := rzp.all(); len(reqs) != 0 {
		t.Fatalf("an order id reached the provider: %v", reqs)
	}
	c := rwReadCommand(t, cmd)
	if c.status != "needs_attention" {
		t.Fatalf("command = %+v, want needs_attention", c)
	}
	if !strings.Contains(c.lastError, "order") {
		t.Errorf("last_error = %q; it must say an order id was refused", c.lastError)
	}
}

// ─── Transient failures ──────────────────────────────────────────────

// A 500 is retried, and the retry succeeds. payment.refunded goes out once, when
// the refund webhook settles it — however often that webhook is delivered.
func TestRefundWorkerRetriesA5xxThenSucceedsOnce(t *testing.T) {
	rzp := newRWRazorpay(t)
	order, pay := rwIDs()
	refund := "rfnd_" + pay[4:]
	pi := rwSeedPaid(t, 60712, order, pay)
	svc := svcWith(t, rzp.provider())
	rzp.scriptRefund(pay,
		rwReply{http.StatusInternalServerError, `{"error":{"code":"SERVER_ERROR","description":"We are facing some trouble completing your request at the moment. Please try again shortly."}}`},
		rwOK(rwRefundEntity(refund, pay, 60712, "processed")))

	cmd := rwRequestRefund(t, svc, pi)
	rwTick(t, svc, cmd)
	c := rwReadCommand(t, cmd)
	if c.status != "pending" || !strings.Contains(c.lastError, "500") {
		t.Fatalf("after a 500 the command = %+v, want pending with the error recorded", c)
	}

	rwTick(t, svc, cmd)
	if n := rzp.count("POST /v1/payments/" + pay + "/refund"); n != 2 {
		t.Fatalf("refund attempts = %d, want 2 (the 500, then the retry)", n)
	}
	if c := rwReadCommand(t, cmd); c.status != "submitted" || c.providerRefundID != refund {
		t.Fatalf("after the retry the command = %+v, want submitted with %s", c, refund)
	}
	for _, k := range rzp.keys() {
		if k != rwKey(pi) {
			t.Errorf("a retry carried idempotency key %q, want %q", k, rwKey(pi))
		}
	}

	rwRefundWebhook(t, svc, pi, pay, "evt_rw_"+refund, refund)
	rwRefundWebhook(t, svc, pi, pay, "evt_rw_again_"+refund, refund)

	if n := rwRefundedEvents(t, pi); n != 1 {
		t.Fatalf("payment.refunded events = %d, want exactly 1", n)
	}
	if n := rwRefundedMinor(t, pi); n != 60712 {
		t.Errorf("refunded_amount_minor = %d, want 60712", n)
	}
	if c := rwReadCommand(t, cmd); c.status != "succeeded" {
		t.Fatalf("command = %+v, want succeeded", c)
	}
	rwTick(t, svc, cmd)
	if n := rzp.count("POST /v1/payments/" + pay + "/refund"); n != 2 {
		t.Errorf("a settled command was attempted again (%d attempts)", n)
	}
}

// ─── Already refunded ────────────────────────────────────────────────

// Razorpay says the payment is already fully refunded. That is the outcome the
// command wanted: it settles through the refund ledger once, with one event.
func TestRefundWorkerSettlesAnAlreadyFullyRefundedPaymentOnce(t *testing.T) {
	rzp := newRWRazorpay(t)
	order, pay := rwIDs()
	refund := "rfnd_prior" + pay[4:12]
	pi := rwSeedPaid(t, 60712, order, pay)
	svc := svcWith(t, rzp.provider())
	rzp.scriptRefund(pay, rwReply{http.StatusBadRequest, rwError("The payment has been fully refunded already", "")})
	rzp.scriptRefundList(pay, rwOK(rwCollection(rwRefundEntity(refund, pay, 60712, "processed"))))

	cmd := rwRequestRefund(t, svc, pi)
	rwTick(t, svc, cmd)

	c := rwReadCommand(t, cmd)
	if c.status != "succeeded" || c.providerRefundID != refund {
		t.Fatalf("command = %+v, want succeeded with %s; requests were %v", c, refund, rzp.all())
	}
	if n := rwRefundedEvents(t, pi); n != 1 {
		t.Fatalf("payment.refunded events = %d, want exactly 1", n)
	}
	if n := rwRefundedMinor(t, pi); n != 60712 {
		t.Errorf("refunded_amount_minor = %d, want 60712", n)
	}
	if s := statusOf(t, pi.id); s != "refunded" {
		t.Errorf("intent status = %q, want refunded", s)
	}

	rwTick(t, svc, cmd)
	rwRefundWebhook(t, svc, pi, pay, "evt_rw_late_"+refund, refund)
	if n := rzp.count("POST /v1/payments/" + pay + "/refund"); n != 1 {
		t.Errorf("refund attempts = %d, want 1; a settled command must not be attempted again", n)
	}
	if n := rwRefundedEvents(t, pi); n != 1 {
		t.Errorf("payment.refunded events = %d after a second tick and a late webhook, want 1", n)
	}
	if n := rwRefundedMinor(t, pi); n != 60712 {
		t.Errorf("refunded_amount_minor = %d after a late webhook, want 60712 — credited twice", n)
	}
}

// The refund reached the ledger BEFORE the command learned its id (the refund
// response was lost, then the webhook came). The provider now says "already
// fully refunded": the command settles, and nothing is credited or published a
// second time.
func TestRefundWorkerSettlesARefundTheLedgerAlreadyCreditedWithoutASecondEvent(t *testing.T) {
	rzp := newRWRazorpay(t)
	order, pay := rwIDs()
	refund := "rfnd_early" + pay[4:12]
	pi := rwSeedPaid(t, 60712, order, pay)
	svc := svcWith(t, rzp.provider())
	rzp.scriptRefund(pay, rwReply{http.StatusBadRequest, rwError("The payment has been fully refunded already", "")})
	rzp.scriptRefundList(pay, rwOK(rwCollection(rwRefundEntity(refund, pay, 60712, "processed"))))

	cmd := rwRequestRefund(t, svc, pi)
	rwRefundWebhook(t, svc, pi, pay, "evt_rw_early_"+refund, refund)
	if n := rwRefundedEvents(t, pi); n != 1 {
		t.Fatalf("setup: payment.refunded events = %d after the webhook, want 1", n)
	}

	rwTick(t, svc, cmd)

	if c := rwReadCommand(t, cmd); c.status != "succeeded" || c.providerRefundID != refund {
		t.Fatalf("command = %+v, want succeeded with %s; requests were %v", c, refund, rzp.all())
	}
	if n := rwRefundedEvents(t, pi); n != 1 {
		t.Errorf("payment.refunded events = %d, want still 1", n)
	}
	if n := rwRefundedMinor(t, pi); n != 60712 {
		t.Errorf("refunded_amount_minor = %d, want 60712 — credited twice", n)
	}
}

// ─── Stub intents ────────────────────────────────────────────────────

// An intent paid through the stub gateway, refunded on a deployment with a
// real provider (a dev stack moved from stub to Razorpay test keys). No
// provider has ever heard of it: nothing is sent, and it is parked with a
// reason that says so.
func TestRefundWorkerNeverSendsAStubIntentToTheProvider(t *testing.T) {
	rzp := newRWRazorpay(t)
	suffix := strings.ReplaceAll(uuid.New().String(), "-", "")[:12]
	pi := rwSeedPaid(t, 60712, gateway.StubOrderPrefix+suffix, "pay_stub_"+suffix)
	svc := svcWith(t, rzp.provider())

	cmd := rwRequestRefund(t, svc, pi)
	rwTick(t, svc, cmd)
	rwTick(t, svc, cmd)

	if reqs := rzp.all(); len(reqs) != 0 {
		t.Fatalf("a stub intent's refund reached the provider: %v", reqs)
	}
	c := rwReadCommand(t, cmd)
	if c.status != "needs_attention" {
		t.Fatalf("command = %+v, want needs_attention", c)
	}
	if !strings.Contains(c.lastError, "stub") {
		t.Errorf("last_error = %q; it must say the intent was paid through the stub gateway", c.lastError)
	}
	if n := rwRefundedEvents(t, pi); n != 0 {
		t.Errorf("%d payment.refunded event(s) for a stub intent on a real provider", n)
	}
}
