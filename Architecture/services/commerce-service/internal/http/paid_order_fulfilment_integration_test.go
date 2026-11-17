//go:build integration

package http

// com-1, over the real routes and a live database: the fulfilment job that
// runs about a second after payment used to book the courier and move the
// order to `shipped` as "system", before any seller had packed it. Now a paid
// order stays `confirmed` until its seller ships it.
//
//	COMMERCE_TEST_DSN=postgres://.../commerce_it_test go test -tags=integration ./internal/http/... -run PaidOrderFulfilment -v
//
// CI only, on commerce_it_test. Fixture: seedOwnOrder
// (ownorder_integration_test.go), one paid, confirmed, single-seller order of
// 88000 + 4900 shipping + 14172 tax = 107072 paise.

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/atpost/commerce-service/internal/courier"
	"github.com/atpost/commerce-service/internal/pii"
	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// countedBookingCourier books like a carrier and counts every booking.
type countedBookingCourier struct {
	bookingCourier
	n *int32
}

func (c countedBookingCourier) CreateShipment(ctx context.Context, req courier.ShipmentRequest) (*courier.ShipmentResponse, error) {
	atomic.AddInt32(c.n, 1)
	return c.bookingCourier.CreateShipment(ctx, req)
}

func paidOrderFulfilmentEngine(t *testing.T) (*gin.Engine, *service.Service, *int32) {
	t.Helper()
	cipher, err := pii.New(devKeyProvider{}, []byte("paid-order-fulfil-salt"))
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	var bookings int32
	svc := service.New(postgres.New(edgePool), nil, "").
		WithCourier(countedBookingCourier{n: &bookings}).
		WithPII(cipher)
	r := gin.New()
	r.Use(FenceMiddleware())
	h := New(svc)
	h.RegisterRoutes(r)
	h.RegisterP0Routes(r)
	return r, svc, &bookings
}

func shipmentCount(t *testing.T, orderID uuid.UUID) int {
	t.Helper()
	var n int
	if err := edgePool.QueryRow(context.Background(),
		`SELECT count(*) FROM shipments WHERE order_id=$1`, orderID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func statusSteps(t *testing.T, orderID uuid.UUID) string {
	t.Helper()
	rows, err := edgePool.Query(context.Background(),
		`SELECT COALESCE(from_status,'')||'>'||to_status||'@'||actor_type FROM order_status_history WHERE order_id=$1 ORDER BY created_at, id`, orderID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var steps []string
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		steps = append(steps, s)
	}
	return strings.Join(steps, " ")
}

// runPaymentJob runs what the worker runs after payment, twice: a retried
// job must change nothing either.
func runPaymentJob(t *testing.T, svc *service.Service, orderID uuid.UUID) {
	t.Helper()
	for i := 0; i < 2; i++ {
		if err := svc.FulfillPaidOrderJob(context.Background(), orderID); err != nil {
			t.Fatalf("FulfillPaidOrderJob run %d: %v", i+1, err)
		}
	}
}

func TestPaidOrderFulfilment_JobLeavesThePaidOrderConfirmedWithNoShipment(t *testing.T) {
	_, svc, bookings := paidOrderFulfilmentEngine(t)
	f := seedOwnOrder(t, 88000, 4900, 14172)

	runPaymentJob(t, svc, f.orderID)

	if s, ps := dbOrder(t, f.orderID); s != "confirmed" || ps != "paid" {
		t.Fatalf("order = %s/%s after the payment job, want confirmed/paid", s, ps)
	}
	if n := shipmentCount(t, f.orderID); n != 0 {
		t.Fatalf("shipments = %d after the payment job, want 0 until the seller ships", n)
	}
	if n := atomic.LoadInt32(bookings); n != 0 {
		t.Fatalf("courier bookings = %d after the payment job, want 0", n)
	}
	if steps := statusSteps(t, f.orderID); strings.Contains(steps, ">shipped") {
		t.Fatalf("history = %q; the payment job moved the order to shipped", steps)
	}
}

func TestPaidOrderFulfilment_BuyerCancelsAPaidUnshippedOrderAndIsRefunded(t *testing.T) {
	r, svc, _ := paidOrderFulfilmentEngine(t)
	f := seedOwnOrder(t, 88000, 4900, 14172)
	runPaymentJob(t, svc, f.orderID)

	w := call(t, r, http.MethodPost, "/v1/commerce/orders/"+f.orderID.String()+"/cancel", f.buyerUserID,
		map[string]any{"reason": "ordered by mistake"})
	if w.Code >= 300 {
		t.Fatalf("buyer cancel of a paid, unshipped order = %d, want 2xx\n%s", w.Code, w.Body.String())
	}
	if s, ps := dbOrder(t, f.orderID); s != "cancelled" || ps != "refund_pending" {
		t.Fatalf("order = %s/%s after the buyer's cancel, want cancelled/refund_pending", s, ps)
	}
	var refunds int
	if err := edgePool.QueryRow(context.Background(),
		`SELECT count(*) FROM order_refund_commands WHERE order_id=$1 AND amount_minor=107072`, f.orderID).Scan(&refunds); err != nil {
		t.Fatal(err)
	}
	if refunds != 1 {
		t.Fatalf("refund commands = %d, want 1 for 107072 paise", refunds)
	}
}

func TestPaidOrderFulfilment_SellerPackThenShipBooksExactlyOnce(t *testing.T) {
	r, svc, bookings := paidOrderFulfilmentEngine(t)
	f := seedOwnOrder(t, 88000, 4900, 14172)
	runPaymentJob(t, svc, f.orderID)

	if w := call(t, r, http.MethodPost, sellerPath(f, "/pack"), f.sellerUserID, nil); w.Code != http.StatusOK {
		t.Fatalf("pack = %d\n%s", w.Code, w.Body.String())
	}
	if n := atomic.LoadInt32(bookings); n != 0 {
		t.Fatalf("courier bookings = %d after pack, want 0: packing is not shipping", n)
	}
	for i := 1; i <= 2; i++ {
		if w := call(t, r, http.MethodPost, sellerPath(f, "/ship"), f.sellerUserID, nil); w.Code != http.StatusCreated {
			t.Fatalf("ship %d = %d\n%s", i, w.Code, w.Body.String())
		}
	}
	// And the job again, after shipping: still nothing.
	runPaymentJob(t, svc, f.orderID)

	if n := atomic.LoadInt32(bookings); n != 1 {
		t.Fatalf("courier bookings = %d after ship, retry and a re-run job, want exactly 1", n)
	}
	if n := shipmentCount(t, f.orderID); n != 1 {
		t.Fatalf("shipments = %d, want 1", n)
	}
	if s, _ := dbOrder(t, f.orderID); s != "shipped" {
		t.Fatalf("order status = %s after the seller shipped, want shipped", s)
	}
	steps := statusSteps(t, f.orderID)
	if !strings.Contains(steps, "confirmed>packed@seller") || !strings.Contains(steps, "packed>shipped@seller") {
		t.Errorf("history = %q; want confirmed>packed@seller then packed>shipped@seller", steps)
	}
	if strings.Contains(steps, "shipped@system") {
		t.Errorf("history = %q; the order was shipped as system", steps)
	}
}
