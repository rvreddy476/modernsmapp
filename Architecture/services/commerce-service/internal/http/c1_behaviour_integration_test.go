//go:build integration

package http

// Lane C1 behaviour proofs, on the contract environment (a scratch database
// named commerce_contract_<n>_test beside COMMERCE_TEST_DSN, the production
// route table, the production service and store). Each test builds its own
// environment, so none of them can disturb the golden fixtures.
//
//	COMMERCE_TEST_DSN=…/commerce_it_test go test -tags=integration -p 1 -count=1 ./internal/http/ -run TestC1 -v

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/commerce-service/internal/courier"
	"github.com/atpost/commerce-service/internal/money"
	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

func (e *contractEnv) scalar(dst any, sql string, args ...any) {
	e.t.Helper()
	if err := e.pool.QueryRow(context.Background(), sql, args...).Scan(dst); err != nil {
		e.t.Fatalf("sql: %v\n%s", err, sql)
	}
}

func (e *contractEnv) orderState(id uuid.UUID) (status, payStatus string) {
	e.t.Helper()
	if err := e.pool.QueryRow(context.Background(),
		`SELECT status, payment_status FROM orders WHERE id = $1`, id).Scan(&status, &payStatus); err != nil {
		e.t.Fatal(err)
	}
	return
}

// outboxPayload returns the payload of the newest outbox row of eventType for
// the order, or nil.
func (e *contractEnv) outboxPayload(eventType string, orderID uuid.UUID) map[string]any {
	e.t.Helper()
	var raw []byte
	err := e.pool.QueryRow(context.Background(),
		`SELECT payload->'payload' FROM outbox_events
		  WHERE event_type = $1 AND payload->'payload'->>'order_id' = $2
		  ORDER BY id DESC LIMIT 1`, eventType, orderID.String()).Scan(&raw)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		e.t.Fatalf("outbox payload: %v", err)
	}
	return m
}

func c1WantKeys(t *testing.T, event string, payload map[string]any, want map[string]string) {
	t.Helper()
	if payload == nil {
		t.Fatalf("%s: no outbox row", event)
	}
	for k, v := range want {
		got, _ := payload[k].(string)
		if got != v {
			t.Errorf("%s payload[%q] = %v, want %q (payload %v)", event, k, payload[k], v, payload)
		}
	}
}

func c1Decode(t *testing.T, w *httptest.ResponseRecorder, into any) {
	t.Helper()
	env := struct {
		Data any `json:"data"`
	}{Data: into}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v\n%s", err, w.Body.String())
	}
}

func c1ErrorCode(w *httptest.ResponseRecorder) string {
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	return env.Error.Code
}

// ─── 2b: retry after a failed payment ────────────────────────────────────

func TestC1RetryAfterPaymentFailureReReservesAndPays(t *testing.T) {
	e := newContractEnv(t)
	e.payments.session = "stub"
	path := "/v1/commerce/orders/" + ctOFailed.String()

	var before struct {
		CanRetry bool `json:"can_retry_payment"`
	}
	c1Decode(t, e.get(path, ctBuyer), &before)
	if !before.CanRetry {
		t.Fatal("a payment_failed order must report can_retry_payment for its payer")
	}

	// A stranger cannot retry someone else's order, and nothing moves.
	if w := e.post(path+"/payment/intent", ctStranger, nil); w.Code < 400 {
		t.Fatalf("stranger retry answered %d", w.Code)
	}
	if st, _ := e.orderState(ctOFailed); st != "payment_failed" {
		t.Fatalf("a stranger's retry moved the order to %s", st)
	}

	var reservedBefore int
	e.scalar(&reservedBefore, `SELECT reserved_qty FROM inventory_items WHERE variant_id = $1`, ctV1)

	w := e.post(path+"/payment/intent", ctBuyer, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("retry answered %d: %s", w.Code, w.Body.String())
	}
	var intent struct {
		ID          string `json:"payment_intent_id"`
		ProviderRef string `json:"provider_ref"`
	}
	c1Decode(t, w, &intent)

	if st, ps := e.orderState(ctOFailed); st != "payment_pending" || ps != "pending" {
		t.Fatalf("after retry: %s/%s, want payment_pending/pending", st, ps)
	}
	var held, reservedAfter, history int
	e.scalar(&held, `SELECT COUNT(*) FROM inventory_reservations
	                  WHERE order_id = $1 AND released_at IS NULL AND committed_at IS NULL`, ctOFailed)
	e.scalar(&reservedAfter, `SELECT reserved_qty FROM inventory_items WHERE variant_id = $1`, ctV1)
	e.scalar(&history, `SELECT COUNT(*) FROM order_status_history
	                     WHERE order_id = $1 AND from_status = 'payment_failed' AND to_status = 'payment_pending'
	                       AND actor_type = 'customer'`, ctOFailed)
	if held != 1 || reservedAfter != reservedBefore+1 {
		t.Fatalf("reservations held=%d reserved %d→%d, want 1 and +1", held, reservedBefore, reservedAfter)
	}
	if history != 1 {
		t.Fatalf("history rows payment_failed→payment_pending by customer = %d, want 1", history)
	}
	if n := len(e.payments.keys); n == 0 || e.payments.keys[n-1] != "order:"+ctOFailed.String()+":attempt:2" {
		t.Fatalf("idempotency keys %v: the retry must open a FRESH intent under the attempt-2 key", e.payments.keys)
	}

	// Asking again returns the bound intent and opens nothing.
	keys := len(e.payments.keys)
	if w := e.post(path+"/payment/intent", ctBuyer, nil); w.Code != http.StatusOK {
		t.Fatalf("second intent read answered %d", w.Code)
	}
	if len(e.payments.keys) != keys {
		t.Fatal("a second intent call on a payment_pending order opened another intent")
	}

	// Paid, through the stub settlement a dev stack uses.
	if w := e.post(path+"/payment/confirm", ctBuyer, map[string]any{
		"payment_intent_id": intent.ID, "razorpay_order_id": intent.ProviderRef,
		"razorpay_payment_id": "pay_stub_retry", "razorpay_signature": "stub-signature",
		"amount_minor": 134800, "gateway": "stub",
	}); w.Code != http.StatusNoContent {
		t.Fatalf("stub confirm answered %d: %s", w.Code, w.Body.String())
	}
	var pay struct {
		Status string `json:"status"`
	}
	c1Decode(t, e.get(path+"/payment", ctBuyer), &pay)
	if pay.Status != "paid" {
		t.Fatalf("three-state read = %q after settlement, want paid", pay.Status)
	}
	// 2g: the paid event names both parties.
	c1WantKeys(t, "commerce.order.paid", e.outboxPayload("commerce.order.paid", ctOFailed), map[string]string{
		"user_id": ctBuyer.String(), "seller_id": ctSeller.String(),
		"seller_user_id": ctSellerUser.String(), "order_number": "ORD-2026-010003",
	})
}

func TestC1RetryWithStockGoneRefusesAndReservesNothing(t *testing.T) {
	e := newContractEnv(t)
	// Every unit of the line's variant is held by someone else.
	e.exec(`UPDATE inventory_items SET reserved_qty = total_qty WHERE variant_id = $1`, ctV1)
	var reservedBefore int
	e.scalar(&reservedBefore, `SELECT reserved_qty FROM inventory_items WHERE variant_id = $1`, ctV1)

	w := e.post("/v1/commerce/orders/"+ctOFailed.String()+"/payment/intent", ctBuyer, nil)
	if w.Code != http.StatusConflict || c1ErrorCode(w) != "OUT_OF_STOCK" {
		t.Fatalf("retry with no stock answered %d %s, want 409 OUT_OF_STOCK", w.Code, w.Body.String())
	}
	if st, ps := e.orderState(ctOFailed); st != "payment_failed" || ps != "failed" {
		t.Fatalf("after a refused retry: %s/%s, want payment_failed/failed", st, ps)
	}
	var rows, reservedAfter int
	e.scalar(&rows, `SELECT COUNT(*) FROM inventory_reservations WHERE order_id = $1`, ctOFailed)
	e.scalar(&reservedAfter, `SELECT reserved_qty FROM inventory_items WHERE variant_id = $1`, ctV1)
	if rows != 0 || reservedAfter != reservedBefore {
		t.Fatalf("a refused retry left %d reservation rows and reserved %d→%d", rows, reservedBefore, reservedAfter)
	}
	if len(e.payments.keys) != 0 {
		t.Fatalf("a refused retry opened an intent: %v", e.payments.keys)
	}
}

// ─── 2d/2e: delivered is real, and the stub courier gets there ───────────

// c1RealCourier is the contract courier answering to a real courier's name.
type c1RealCourier struct{ ctCourier }

func (c1RealCourier) Name() string { return "shiprocket" }

var _ courier.Provider = c1RealCourier{}

func TestC1StubCourierDeliversAndReviewsBecomePossible(t *testing.T) {
	e := newContractEnv(t)
	ctx := context.Background()
	review := func() *httptest.ResponseRecorder {
		return e.post("/v1/commerce/products/"+ctP1.String()+"/reviews", ctBuyer, map[string]any{
			"seller_id": ctSeller.String(), "order_item_id": ctIConfirmed.String(),
			"rating": 5, "title": "Arrived", "body": "Delivered by the stub courier.",
		})
	}
	if w := review(); w.Code != http.StatusBadRequest || c1ErrorCode(w) != "REVIEW_ITEM_NOT_DELIVERED" {
		t.Fatalf("review before delivery answered %d %s, want 400 REVIEW_ITEM_NOT_DELIVERED", w.Code, w.Body.String())
	}

	orderPath := "/v1/commerce/seller/orders/" + ctOConfirmed.String()
	if w := e.post(orderPath+"/pack", ctSellerUser, nil); w.Code != http.StatusOK {
		t.Fatalf("pack answered %d: %s", w.Code, w.Body.String())
	}
	if w := e.post(orderPath+"/ship", ctSellerUser, map[string]any{}); w.Code != http.StatusCreated {
		t.Fatalf("ship answered %d: %s", w.Code, w.Body.String())
	}
	itemStatus := func() (status string, delivered bool) {
		var at *time.Time
		if err := e.pool.QueryRow(ctx, `SELECT status, delivered_at FROM order_items WHERE id = $1`, ctIConfirmed).
			Scan(&status, &at); err != nil {
			t.Fatal(err)
		}
		return status, at != nil
	}
	if st, _ := itemStatus(); st != "shipped" {
		t.Fatalf("line after ship = %q, want shipped (lines follow the order)", st)
	}

	// Not yet due: nothing moves.
	if n := e.svc.SweepStubDelivery(ctx, time.Hour); n != 0 {
		t.Fatalf("sweep advanced %d orders before they were due", n)
	}
	backdate := func(to string) {
		e.exec(`UPDATE order_status_history SET created_at = NOW() - interval '10 minutes'
		         WHERE order_id = $1 AND to_status = $2`, ctOConfirmed, to)
	}
	backdate("shipped")

	// Two overdue shipped orders the timer must NEVER move, even with the
	// stub courier configured: one a real courier booked (found on dev,
	// 30 Sep 2026: the first sweep moved a live Shiprocket order and 61 old
	// test orders), one with no shipment at all. The exact counts below
	// (one order per sweep) fail if either is picked.
	realBooked, unbooked := uuid.New(), uuid.New()
	for i, id := range []uuid.UUID{realBooked, unbooked} {
		e.exec(`INSERT INTO orders (id,customer_user_id,order_number,subtotal,final_amount,subtotal_minor,final_amount_minor,
		           payment_method,payment_status,status,created_at,updated_at)
		        VALUES ($1,$2,$3,1299.00,1299.00,129900,129900,'upi','paid','shipped',
		                NOW() - interval '1 day',NOW() - interval '1 day')`,
			id, ctBuyer, fmt.Sprintf("ORD-C1-SD-%d", i))
	}
	e.exec(`INSERT INTO shipments (order_id,seller_id,courier,tracking_number,status,shipped_at)
	        VALUES ($1,$2,'shiprocket','SR-REAL-1','booked',NOW() - interval '1 day')`, realBooked, ctSeller)
	defer func() {
		for _, id := range []uuid.UUID{realBooked, unbooked} {
			if st, _ := e.orderState(id); st != "shipped" {
				t.Errorf("the stub timer moved order %s to %s; only stub-booked orders may move", id, st)
			}
		}
	}()

	// A real courier's webhooks decide delivery: the timer refuses to run.
	realSvc := service.New(postgres.New(e.pool), nil, "").WithCourier(c1RealCourier{})
	if realSvc.StubAutoDeliveryAllowed() {
		t.Fatal("the delivery timer is allowed under a real courier")
	}
	if n := realSvc.SweepStubDelivery(ctx, time.Minute); n != 0 {
		t.Fatalf("the timer advanced %d orders under a real courier", n)
	}
	if st, _ := e.orderState(ctOConfirmed); st != "shipped" {
		t.Fatalf("order moved to %s under a real courier", st)
	}

	if n := e.svc.SweepStubDelivery(ctx, time.Minute); n != 1 {
		t.Fatalf("first sweep advanced %d, want 1", n)
	}
	if st, _ := e.orderState(ctOConfirmed); st != "out_for_delivery" {
		t.Fatalf("after first sweep: %s, want out_for_delivery", st)
	}
	if st, _ := itemStatus(); st != "out_for_delivery" {
		t.Fatalf("line after first sweep = %q", st)
	}
	backdate("out_for_delivery")
	if n := e.svc.SweepStubDelivery(ctx, time.Minute); n != 1 {
		t.Fatalf("second sweep advanced %d, want 1", n)
	}
	if st, _ := e.orderState(ctOConfirmed); st != "delivered" {
		t.Fatalf("after second sweep: %s, want delivered", st)
	}
	if st, delivered := itemStatus(); st != "delivered" || !delivered {
		t.Fatalf("line = %q delivered_at set=%v, want delivered with a timestamp", st, delivered)
	}
	var sysRows int
	e.scalar(&sysRows, `SELECT COUNT(*) FROM order_status_history
	                     WHERE order_id = $1 AND actor_type = 'system'
	                       AND to_status IN ('out_for_delivery','delivered')`, ctOConfirmed)
	if sysRows != 2 {
		t.Fatalf("system history rows for the two delivery steps = %d, want 2", sysRows)
	}
	c1WantKeys(t, "commerce.order.delivered", e.outboxPayload("commerce.order.delivered", ctOConfirmed), map[string]string{
		"user_id": ctBuyer.String(), "order_number": "ORD-2026-010001",
	})
	// A third sweep finds nothing: delivered is terminal for the timer.
	if n := e.svc.SweepStubDelivery(ctx, 0); n != 0 {
		t.Fatalf("sweep advanced %d past delivered", n)
	}

	if w := review(); w.Code != http.StatusCreated {
		t.Fatalf("review after delivery answered %d: %s", w.Code, w.Body.String())
	}
}

// ─── 2f: product auto-approve ────────────────────────────────────────────

func (e *contractEnv) createProduct(sku string) string {
	e.t.Helper()
	w := e.post("/v1/commerce/products", ctSellerUser, map[string]any{
		"title": "Auto approve " + sku, "description": "A complete listing for the auto-approve proof.",
		"category_id": ctCatElectronics.String(), "tax_class_id": ctTax18.String(), "hsn_code": "8507",
		"weight_grams": 220, "length_cm": 14, "width_cm": 7, "height_cm": 1.5,
		"primary_image_media_id": ctMediaNew.String(), "return_policy_type": "7_days", "return_policy_days": 7,
		"variants": []map[string]any{{"sku": sku, "option_1_name": "Colour", "option_1_value": "Black",
			"mrp_minor": 199900, "selling_price_minor": 149900, "stock_qty": 10}},
	})
	if w.Code != http.StatusCreated {
		e.t.Fatalf("create product answered %d: %s", w.Code, w.Body.String())
	}
	var p struct {
		ID string `json:"id"`
	}
	c1Decode(e.t, w, &p)
	return p.ID
}

func (e *contractEnv) approvalOf(productID string) (approval string, autoRows int) {
	e.t.Helper()
	e.scalar(&approval, `SELECT approval_status FROM products WHERE id = $1`, productID)
	e.scalar(&autoRows, `SELECT COUNT(*) FROM product_moderation_log
	                      WHERE product_id = $1 AND action = 'approve' AND reason = $2 AND actor_user_id = $3`,
		productID, postgres.AutoApproveReason, postgres.SystemActorID)
	return
}

func TestC1ProductAutoApprove(t *testing.T) {
	e := newContractEnv(t)
	submit := func(id string) {
		t.Helper()
		if w := e.post("/v1/commerce/products/"+id+"/submit", ctSellerUser, nil); w.Code != http.StatusNoContent {
			t.Fatalf("submit answered %d: %s", w.Code, w.Body.String())
		}
	}

	t.Run("flag on, approved shop: approved on submit, logged as the rule", func(t *testing.T) {
		id := e.createProduct("C1-AUTO-ON")
		submit(id)
		approval, rows := e.approvalOf(id)
		var status string
		e.scalar(&status, `SELECT status FROM products WHERE id = $1`, id)
		if approval != "approved" || status != "active" || rows != 1 {
			t.Fatalf("approval=%s status=%s auto-log rows=%d, want approved/active/1", approval, status, rows)
		}
	})

	// A shop still under review cannot submit at all: submit refuses with
	// 409 SELLER_NOT_APPROVED before auto-approve is consulted, so the flag
	// can never approve a listing for an unapproved shop. (The seller check
	// inside auto-approve is a second line behind this one.)
	t.Run("flag on, shop not approved: submit is refused, nothing approved", func(t *testing.T) {
		id := e.createProduct("C1-AUTO-UNREVIEWED")
		before, _ := e.approvalOf(id)
		e.exec(`UPDATE sellers SET status = 'under_review' WHERE id = $1`, ctSeller)
		defer e.exec(`UPDATE sellers SET status = 'approved' WHERE id = $1`, ctSeller)
		w := e.post("/v1/commerce/products/"+id+"/submit", ctSellerUser, nil)
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "SELLER_NOT_APPROVED") {
			t.Fatalf("submit answered %d %s, want 409 SELLER_NOT_APPROVED", w.Code, w.Body.String())
		}
		if approval, rows := e.approvalOf(id); approval != before || rows != 0 {
			t.Fatalf("approval=%s auto-log rows=%d, want %s/0: an unapproved shop's listing must not move", approval, rows, before)
		}
	})

	t.Run("flag off: queued as before", func(t *testing.T) {
		e.svc.WithProductAutoApprove(false)
		defer e.svc.WithProductAutoApprove(true)
		id := e.createProduct("C1-AUTO-OFF")
		submit(id)
		if approval, rows := e.approvalOf(id); approval != "submitted" || rows != 0 {
			t.Fatalf("approval=%s auto-log rows=%d, want submitted/0", approval, rows)
		}
	})
}

// ─── 2g: the payment-lifecycle events carry their parties ───────────────

func TestC1OrderEventsCarryTheirParties(t *testing.T) {
	e := newContractEnv(t)
	ctx := context.Background()
	store := postgres.New(e.pool)
	parties := map[string]string{
		"user_id": ctBuyer.String(), "seller_id": ctSeller.String(), "seller_user_id": ctSellerUser.String(),
	}
	with := func(orderNumber string) map[string]string {
		m := map[string]string{"order_number": orderNumber}
		for k, v := range parties {
			m[k] = v
		}
		return m
	}

	// payment_failed (was {order_id} only).
	if err := store.ApplyPaymentFailed(ctx, postgres.PaymentEvent{
		EventID: "c1-failed-" + uuid.NewString(), EventType: "payment.failed", OrderID: ctOPending,
	}); err != nil {
		t.Fatal(err)
	}
	c1WantKeys(t, "payment_failed", e.outboxPayload("commerce.order.payment_failed", ctOPending), with("ORD-2026-010005"))

	// cancelled (had user_id; gains the order number and the seller).
	if w := e.post("/v1/commerce/orders/"+ctOPending.String()+"/cancel", ctBuyer, map[string]any{"reason": "c1"}); w.Code >= 300 {
		t.Fatalf("cancel answered %d: %s", w.Code, w.Body.String())
	}
	c1WantKeys(t, "cancelled", e.outboxPayload("commerce.order.cancelled", ctOPending), with("ORD-2026-010005"))

	// refunded (was {order_id, intent_id}).
	if err := store.SettleRefund(ctx, ctORefund, "c1-intent"); err != nil {
		t.Fatal(err)
	}
	c1WantKeys(t, "refunded", e.outboxPayload("commerce.order.refunded", ctORefund), with("ORD-2026-010004"))

	// paid on the signed-event path: a fresh payment_pending order on the
	// same parties, settled by ApplyPaymentSucceeded.
	fresh := uuid.New()
	e.exec(`INSERT INTO orders (id,customer_user_id,order_number,subtotal,final_amount,subtotal_minor,final_amount_minor,
	           payment_method,payment_status,status,created_at,updated_at)
	        VALUES ($1,$2,'ORD-2026-019999',1299.00,1299.00,129900,129900,'upi','pending','payment_pending',NOW(),NOW())`,
		fresh, ctBuyer)
	e.exec(`INSERT INTO order_items (order_id,product_id,variant_id,seller_id,product_title,sku,quantity,
	           unit_mrp,unit_price,final_price,unit_mrp_minor,unit_price_minor,final_price_minor,status)
	        VALUES ($1,$2,$3,$4,'Momentum Wireless Earbuds','MOM-EB-BLK',1,1299.00,1299.00,1299.00,129900,129900,129900,'confirmed')`,
		fresh, ctP1, ctV1, ctSeller)
	// Settlement refuses to mark an order paid without held stock, so the
	// order holds a live checkout reservation like a real checkout leaves.
	e.exec(`INSERT INTO inventory_reservations (variant_id,order_id,user_id,quantity,type,expires_at,created_at)
	        VALUES ($1,$2,$3,1,'checkout',NOW() + INTERVAL '20 minutes',NOW())`, ctV1, fresh, ctBuyer)
	e.exec(`UPDATE inventory_items SET reserved_qty = reserved_qty + 1 WHERE variant_id = $1`, ctV1)
	if err := store.ApplyPaymentSucceeded(ctx, postgres.PaymentEvent{
		EventID: "c1-paid-" + uuid.NewString(), EventType: "payment.succeeded", OrderID: fresh,
		AmountMinor: money.Paise(129900), Currency: "INR", PayerID: ctBuyer,
	}); err != nil {
		t.Fatalf("apply paid: %v", err)
	}
	paid := e.outboxPayload("commerce.order.paid", fresh)
	c1WantKeys(t, "paid", paid, with("ORD-2026-019999"))
	if _, ok := paid["buyer_email"]; !ok {
		t.Errorf("paid payload has no buyer_email key: %v", paid)
	}
}

// ─── 2b: the retry's own guards, below the service's ────────────────────
//
// The service refuses a stranger and an order that is not payment_failed
// before it reaches the store, so the HTTP tests above never exercise the
// store's copies of those checks. They are the line that holds if a later
// caller reaches RetryPaymentReservation some other way, so they are pinned
// here directly: refused with the typed error, nothing reserved, nothing moved.
func TestC1RetryStoreGuardsHoldOnTheirOwn(t *testing.T) {
	e := newContractEnv(t)
	ctx := context.Background()
	store := postgres.New(e.pool)
	held := func(order uuid.UUID) int {
		var n int
		e.scalar(&n, `SELECT COUNT(*) FROM inventory_reservations
		               WHERE order_id = $1 AND released_at IS NULL AND committed_at IS NULL`, order)
		return n
	}

	t.Run("a stranger cannot retry another buyer's failed order", func(t *testing.T) {
		before := held(ctOFailed)
		if _, err := store.RetryPaymentReservation(ctx, ctOFailed, ctStranger); !errors.Is(err, postgres.ErrNotOrderOwnerP0) {
			t.Fatalf("stranger retry: err=%v, want ErrNotOrderOwnerP0", err)
		}
		if st, _ := e.orderState(ctOFailed); st != "payment_failed" || held(ctOFailed) != before {
			t.Fatalf("a refused retry moved the order to %s or reserved stock", st)
		}
	})

	t.Run("an order that has not failed cannot be retried", func(t *testing.T) {
		for _, order := range []uuid.UUID{ctOConfirmed, ctOPending} {
			stBefore, _ := e.orderState(order)
			before := held(order)
			if _, err := store.RetryPaymentReservation(ctx, order, ctBuyer); !errors.Is(err, postgres.ErrPaymentNotRetryable) {
				t.Fatalf("retry of a %s order: err=%v, want ErrPaymentNotRetryable", stBefore, err)
			}
			if st, _ := e.orderState(order); st != stBefore || held(order) != before {
				t.Fatalf("a refused retry of a %s order moved it to %s or reserved stock", stBefore, st)
			}
		}
	})
}
