//go:build integration

package http

// The seller fulfilment routes, over the production route table and a live
// database.
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/http/... -run SellerFulfilment -v
//
// Fixture: seedOwnOrder (ownorder_integration_test.go), one paid, confirmed,
// single-seller order with stock 40 total / 6 reserved.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/atpost/commerce-service/internal/courier"
	"github.com/atpost/commerce-service/internal/pii"
	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// manualCourier books like the stub and, like the stub, lets the seller
// record their own courier and tracking number.
type manualCourier struct{ bookingCourier }

func (manualCourier) AcceptsManualBooking() bool { return true }

// carrierCourier books like a real carrier: its AWB is the one webhooks will
// name, so a seller-typed number is ignored.
type carrierCourier struct{ bookingCourier }

func (carrierCourier) Name() string { return "carrier" }

func sellerFulfilmentEngine(t *testing.T, p courier.Provider) *gin.Engine {
	t.Helper()
	cipher, err := pii.New(devKeyProvider{}, []byte("seller-fulfil-salt"))
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	svc := service.New(postgres.New(edgePool), nil, "").WithCourier(p).WithPII(cipher)
	r := gin.New()
	r.Use(FenceMiddleware())
	h := New(svc)
	h.RegisterRoutes(r)
	h.RegisterP0Routes(r)
	return r
}

type fulfilResp struct {
	Data struct {
		OrderID string `json:"order_id"`
		Status  string `json:"status"`
		Applied bool   `json:"applied"`
	} `json:"data"`
	Error *struct {
		Code string `json:"code"`
	} `json:"error"`
}

func decodeFulfil(t *testing.T, body []byte) fulfilResp {
	t.Helper()
	var out fulfilResp
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	return out
}

func errCode(t *testing.T, body []byte) string {
	t.Helper()
	return decodeFulfil(t, body).Error.Code
}

func dbOrder(t *testing.T, orderID uuid.UUID) (status, payStatus string) {
	t.Helper()
	if err := edgePool.QueryRow(context.Background(),
		`SELECT status, payment_status FROM orders WHERE id=$1`, orderID).Scan(&status, &payStatus); err != nil {
		t.Fatalf("read order: %v", err)
	}
	return
}

func setStatus(t *testing.T, orderID uuid.UUID, status string) {
	t.Helper()
	if _, err := edgePool.Exec(context.Background(),
		`UPDATE orders SET status=$2 WHERE id=$1`, orderID, status); err != nil {
		t.Fatalf("set status: %v", err)
	}
}

func sellerPath(f ownOrderFixture, tail string) string {
	return "/v1/commerce/seller/orders/" + f.orderID.String() + tail
}

// ─── pack ────────────────────────────────────────────────────────────────

func TestSellerFulfilment_PackFromConfirmedThenRepeatIsIdempotent(t *testing.T) {
	r := sellerFulfilmentEngine(t, manualCourier{})
	f := seedOwnOrder(t, 88000, 4900, 14172)

	w := call(t, r, http.MethodPost, sellerPath(f, "/pack"), f.sellerUserID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("pack = %d\n%s", w.Code, w.Body.String())
	}
	got := decodeFulfil(t, w.Body.Bytes())
	if got.Data.Status != "packed" || !got.Data.Applied || got.Data.OrderID != f.orderID.String() {
		t.Fatalf("pack body = %+v; want status packed, applied true", got.Data)
	}
	if s, _ := dbOrder(t, f.orderID); s != "packed" {
		t.Fatalf("order status = %s, want packed", s)
	}

	again := call(t, r, http.MethodPost, sellerPath(f, "/pack"), f.sellerUserID, nil)
	if again.Code != http.StatusOK {
		t.Fatalf("repeat pack = %d, want 200 same-state\n%s", again.Code, again.Body.String())
	}
	if g := decodeFulfil(t, again.Body.Bytes()); g.Data.Applied || g.Data.Status != "packed" {
		t.Fatalf("repeat pack body = %+v; want applied false, status packed", g.Data)
	}
}

func TestSellerFulfilment_PackFromShippedIsRefused(t *testing.T) {
	r := sellerFulfilmentEngine(t, manualCourier{})
	f := seedOwnOrder(t, 88000, 4900, 14172)
	setStatus(t, f.orderID, "shipped")

	w := call(t, r, http.MethodPost, sellerPath(f, "/pack"), f.sellerUserID, nil)
	if w.Code != http.StatusConflict || errCode(t, w.Body.Bytes()) != "TRANSITION_NOT_PERMITTED" {
		t.Fatalf("pack from shipped = %d %s, want 409 TRANSITION_NOT_PERMITTED", w.Code, w.Body.String())
	}
}

// ─── ownership ───────────────────────────────────────────────────────────

func TestSellerFulfilment_RoutesAreOwnerChecked(t *testing.T) {
	r := sellerFulfilmentEngine(t, manualCourier{})
	f := seedOwnOrder(t, 88000, 4900, 14172)
	other := seedOwnOrder(t, 5000, 0, 900) // a real seller, no line on f
	body := map[string]any{"reason": "no stock", "courier": "X", "tracking_number": "1"}

	for _, tc := range []struct{ method, tail string }{
		{http.MethodPost, "/pack"},
		{http.MethodPost, "/cancel"},
		{http.MethodPost, "/ship"},
		{http.MethodGet, "/history"},
	} {
		w := call(t, r, tc.method, sellerPath(f, tc.tail), other.sellerUserID, body)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s by a DIFFERENT seller = %d, want 403\n%s", tc.method, tc.tail, w.Code, w.Body.String())
		}
		w = call(t, r, tc.method, sellerPath(f, tc.tail), uuid.New(), body)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s by a caller with no seller profile = %d, want 403\n%s", tc.method, tc.tail, w.Code, w.Body.String())
		}
		w = call(t, r, tc.method, sellerPath(f, tc.tail), f.buyerUserID, body)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s by the BUYER = %d, want 403: the buyer is not a seller\n%s", tc.method, tc.tail, w.Code, w.Body.String())
		}
	}

	missing := "/v1/commerce/seller/orders/" + uuid.NewString() + "/pack"
	if w := call(t, r, http.MethodPost, missing, f.sellerUserID, nil); w.Code != http.StatusNotFound {
		t.Errorf("pack on a non-existent order = %d, want 404\n%s", w.Code, w.Body.String())
	}
	// Nothing above moved the order.
	if s, _ := dbOrder(t, f.orderID); s != "confirmed" {
		t.Fatalf("order status = %s after refused calls, want confirmed", s)
	}
}

// ─── cancel ──────────────────────────────────────────────────────────────

func TestSellerFulfilment_CancelFromPackedReleasesStockAndOwesARefund(t *testing.T) {
	r := sellerFulfilmentEngine(t, manualCourier{})
	f := seedOwnOrder(t, 88000, 4900, 14172)
	ctx := context.Background()

	if w := call(t, r, http.MethodPost, sellerPath(f, "/pack"), f.sellerUserID, nil); w.Code != http.StatusOK {
		t.Fatalf("pack = %d\n%s", w.Code, w.Body.String())
	}

	// No reason: refused by name, nothing moves.
	w := call(t, r, http.MethodPost, sellerPath(f, "/cancel"), f.sellerUserID, map[string]any{"reason": "  "})
	if w.Code != http.StatusBadRequest || errCode(t, w.Body.Bytes()) != "REASON_REQUIRED" {
		t.Fatalf("cancel without a reason = %d %s, want 400 REASON_REQUIRED", w.Code, w.Body.String())
	}

	w = call(t, r, http.MethodPost, sellerPath(f, "/cancel"), f.sellerUserID, map[string]any{"reason": "damaged in the warehouse"})
	if w.Code != http.StatusOK {
		t.Fatalf("cancel = %d\n%s", w.Code, w.Body.String())
	}
	if g := decodeFulfil(t, w.Body.Bytes()); g.Data.Status != "cancelled" || !g.Data.Applied {
		t.Fatalf("cancel body = %+v", g.Data)
	}

	status, payStatus := dbOrder(t, f.orderID)
	if status != "cancelled" || payStatus != "refund_pending" {
		t.Fatalf("order = %s/%s, want cancelled/refund_pending: the seller cancel must take the customer cancel's refund path", status, payStatus)
	}
	var cancelledBy, reason string
	if err := edgePool.QueryRow(ctx, `SELECT cancelled_by, cancellation_reason FROM orders WHERE id=$1`, f.orderID).
		Scan(&cancelledBy, &reason); err != nil {
		t.Fatal(err)
	}
	if cancelledBy != "seller" || reason != "damaged in the warehouse" {
		t.Errorf("cancelled_by/reason = %q/%q", cancelledBy, reason)
	}
	// Committed stock came back: 40 -> 41.
	var total int
	if err := edgePool.QueryRow(ctx, `SELECT total_qty FROM inventory_items WHERE variant_id=$1`, f.variantID).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 41 {
		t.Errorf("total_qty = %d, want 41: the paid line was not restocked", total)
	}
	// The refund is a durable command, the same one a buyer's cancel writes.
	var refunds int
	if err := edgePool.QueryRow(ctx, `SELECT count(*) FROM order_refund_commands WHERE order_id=$1 AND amount_minor=107072`, f.orderID).Scan(&refunds); err != nil {
		t.Fatal(err)
	}
	if refunds != 1 {
		t.Errorf("refund commands for the order = %d, want 1 for 107072 paise", refunds)
	}

	// Repeat: 200, applied false, no second refund command.
	again := call(t, r, http.MethodPost, sellerPath(f, "/cancel"), f.sellerUserID, map[string]any{"reason": "again"})
	if again.Code != http.StatusOK {
		t.Fatalf("repeat cancel = %d\n%s", again.Code, again.Body.String())
	}
	if g := decodeFulfil(t, again.Body.Bytes()); g.Data.Applied || g.Data.Status != "cancelled" {
		t.Fatalf("repeat cancel body = %+v; want applied false", g.Data)
	}
	if err := edgePool.QueryRow(ctx, `SELECT count(*) FROM order_refund_commands WHERE order_id=$1`, f.orderID).Scan(&refunds); err != nil {
		t.Fatal(err)
	}
	if refunds != 1 {
		t.Errorf("refund commands after a repeat = %d, want still 1", refunds)
	}

	// The timeline shows every step with the reason on the cancel.
	h := call(t, r, http.MethodGet, sellerPath(f, "/history"), f.sellerUserID, nil)
	if h.Code != http.StatusOK {
		t.Fatalf("history = %d\n%s", h.Code, h.Body.String())
	}
	var hist struct {
		Data struct {
			OrderID string `json:"order_id"`
			History []struct {
				From  *string `json:"from_status"`
				To    string  `json:"to_status"`
				Actor string  `json:"actor_type"`
				Notes *string `json:"notes"`
			} `json:"history"`
		} `json:"data"`
	}
	if err := json.Unmarshal(h.Body.Bytes(), &hist); err != nil {
		t.Fatalf("history decode: %v\n%s", err, h.Body.String())
	}
	var sawPack, sawCancel bool
	for _, row := range hist.Data.History {
		if row.From != nil && *row.From == "confirmed" && row.To == "packed" && row.Actor == "seller" {
			sawPack = true
		}
		if row.From != nil && *row.From == "packed" && row.To == "cancelled" && row.Actor == "seller" &&
			row.Notes != nil && *row.Notes == "damaged in the warehouse" {
			sawCancel = true
		}
	}
	if !sawPack || !sawCancel {
		t.Errorf("history lacks the seller's pack and cancel rows: %s", h.Body.String())
	}
}

func TestSellerFulfilment_CancelFromShippedIsRefused(t *testing.T) {
	r := sellerFulfilmentEngine(t, manualCourier{})
	f := seedOwnOrder(t, 88000, 4900, 14172)
	setStatus(t, f.orderID, "shipped")

	w := call(t, r, http.MethodPost, sellerPath(f, "/cancel"), f.sellerUserID, map[string]any{"reason": "too late"})
	if w.Code != http.StatusConflict || errCode(t, w.Body.Bytes()) != "CANCEL_NOT_PERMITTED" {
		t.Fatalf("cancel from shipped = %d %s, want 409 CANCEL_NOT_PERMITTED", w.Code, w.Body.String())
	}
	if s, ps := dbOrder(t, f.orderID); s != "shipped" || ps != "paid" {
		t.Fatalf("order = %s/%s after a refused cancel, want shipped/paid", s, ps)
	}
}

// ─── ship ────────────────────────────────────────────────────────────────

type shipmentsResp struct {
	Data struct {
		Shipments []struct {
			Shipment map[string]any `json:"shipment"`
			Events   []struct {
				Status string  `json:"status"`
				Remark *string `json:"remark"`
			} `json:"events"`
		} `json:"shipments"`
	} `json:"data"`
}

func listShipments(t *testing.T, r *gin.Engine, f ownOrderFixture) shipmentsResp {
	t.Helper()
	w := call(t, r, http.MethodGet, "/v1/commerce/orders/"+f.orderID.String()+"/shipments", f.sellerUserID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list shipments = %d\n%s", w.Code, w.Body.String())
	}
	var out shipmentsResp
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode shipments: %v\n%s", err, w.Body.String())
	}
	if len(out.Data.Shipments) != 1 {
		t.Fatalf("shipments = %d, want 1\n%s", len(out.Data.Shipments), w.Body.String())
	}
	return out
}

func TestSellerFulfilment_ShipBodyIsStoredUnderTheStubAndTheOrderIsShipped(t *testing.T) {
	r := sellerFulfilmentEngine(t, manualCourier{})
	f := seedOwnOrder(t, 88000, 4900, 14172)
	// Unique per run: shipments are UNIQUE on (courier, tracking_number)
	// and the fixtures do not tear down.
	awb := "DL" + strings.ToUpper(uuid.NewString()[:8])

	w := call(t, r, http.MethodPost, sellerPath(f, "/ship"), f.sellerUserID,
		map[string]any{"courier": "Delhivery", "tracking_number": awb})
	if w.Code != http.StatusCreated {
		t.Fatalf("ship = %d\n%s", w.Code, w.Body.String())
	}

	// The order moved, as the seller, through packed.
	if s, _ := dbOrder(t, f.orderID); s != "shipped" {
		t.Fatalf("order status = %s, want shipped: the flip used to run as actor system and drop the error", s)
	}
	var steps []string
	rows, err := edgePool.Query(context.Background(),
		`SELECT COALESCE(from_status,'')||'>'||to_status||'@'||actor_type FROM order_status_history WHERE order_id=$1 ORDER BY created_at, id`, f.orderID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		steps = append(steps, s)
	}
	rows.Close()
	joined := strings.Join(steps, " ")
	if !strings.Contains(joined, "confirmed>packed@seller") || !strings.Contains(joined, "packed>shipped@seller") {
		t.Errorf("history = %v; want confirmed>packed@seller then packed>shipped@seller", steps)
	}

	// The seller's values are on the shipment, in snake_case, and on its
	// first event.
	got := listShipments(t, r, f)
	sh := got.Data.Shipments[0].Shipment
	if sh["courier"] != "Delhivery" || sh["tracking_number"] != awb {
		t.Errorf("shipment courier/tracking = %v/%v, want Delhivery/%s", sh["courier"], sh["tracking_number"], awb)
	}
	if v, present := sh["label_url"]; !present || v != nil {
		t.Errorf("label_url = %v (present=%v); the invented label must be dropped with the invented AWB", v, present)
	}
	if _, pascal := sh["TrackingNumber"]; pascal {
		t.Error("shipment still carries PascalCase keys")
	}
	evs := got.Data.Shipments[0].Events
	if len(evs) == 0 || evs[0].Status != "booked" || evs[0].Remark == nil || !strings.Contains(*evs[0].Remark, awb) {
		t.Errorf("first event = %+v; want a booked event naming %s", evs, awb)
	}

	// A repeat returns the same shipment rather than booking again.
	again := call(t, r, http.MethodPost, sellerPath(f, "/ship"), f.sellerUserID,
		map[string]any{"courier": "Other", "tracking_number": "X"})
	if again.Code != http.StatusCreated {
		t.Fatalf("repeat ship = %d\n%s", again.Code, again.Body.String())
	}
	if sh := listShipments(t, r, f).Data.Shipments[0].Shipment; sh["tracking_number"] != awb {
		t.Errorf("repeat ship changed the tracking number to %v", sh["tracking_number"])
	}
}

func TestSellerFulfilment_ShipBodyIsIgnoredUnderACarrier(t *testing.T) {
	r := sellerFulfilmentEngine(t, carrierCourier{})
	f := seedOwnOrder(t, 88000, 4900, 14172)

	w := call(t, r, http.MethodPost, "/v1/commerce/orders/"+f.orderID.String()+"/shipment", f.sellerUserID,
		map[string]any{"courier": "Delhivery", "tracking_number": "DL123456"})
	if w.Code != http.StatusCreated {
		t.Fatalf("ship = %d\n%s", w.Code, w.Body.String())
	}
	sh := listShipments(t, r, f).Data.Shipments[0].Shipment
	awb, _ := sh["tracking_number"].(string)
	if sh["courier"] != "carrier" || !strings.HasPrefix(awb, "AWB") {
		t.Errorf("shipment courier/tracking = %v/%v; the carrier's booking must win", sh["courier"], sh["tracking_number"])
	}
	if sh["label_url"] == nil {
		t.Error("the carrier's label_url was dropped")
	}
	if s, _ := dbOrder(t, f.orderID); s != "shipped" {
		t.Errorf("order status = %s, want shipped", s)
	}
}

func TestSellerFulfilment_ShipFromCancelledIsRefused(t *testing.T) {
	r := sellerFulfilmentEngine(t, manualCourier{})
	f := seedOwnOrder(t, 88000, 4900, 14172)
	setStatus(t, f.orderID, "cancelled")

	w := call(t, r, http.MethodPost, sellerPath(f, "/ship"), f.sellerUserID, nil)
	if w.Code == http.StatusCreated {
		t.Fatalf("a cancelled order was shipped\n%s", w.Body.String())
	}
}

// ─── list ────────────────────────────────────────────────────────────────

func TestSellerFulfilment_OrderListCarriesCountAndMoney(t *testing.T) {
	r := sellerFulfilmentEngine(t, manualCourier{})
	f := seedOwnOrder(t, 88000, 4900, 14172)

	w := call(t, r, http.MethodGet, "/v1/commerce/seller/orders", f.sellerUserID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d\n%s", w.Code, w.Body.String())
	}
	var env struct {
		Data []struct {
			ID                  string `json:"id"`
			Status              string `json:"status"`
			ItemCount           int    `json:"item_count"`
			SellerSubtotalMinor int64  `json:"seller_subtotal_minor"`
			TotalMinor          int64  `json:"total_minor"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v\n%s", err, w.Body.String())
	}
	if len(env.Data) != 1 {
		t.Fatalf("rows = %d, want 1", len(env.Data))
	}
	row := env.Data[0]
	if row.ID != f.orderID.String() || row.ItemCount != 1 || row.SellerSubtotalMinor != 88000 || row.TotalMinor != 107072 {
		t.Errorf("row = %+v; want item_count 1, seller_subtotal_minor 88000, total_minor 107072", row)
	}
}

// A seller recording an AWB that another shipment already carries with the
// same courier is told so by name. The unique violation used to be logged
// and answered as "no shipments could be booked".
func TestSellerFulfilment_ReusedTrackingNumberIsRefusedByName(t *testing.T) {
	r := sellerFulfilmentEngine(t, manualCourier{})
	first := seedOwnOrder(t, 88000, 4900, 14172)
	second := seedOwnOrder(t, 5000, 0, 900)
	awb := "DL" + strings.ToUpper(uuid.NewString()[:8])

	if w := call(t, r, http.MethodPost, sellerPath(first, "/ship"), first.sellerUserID,
		map[string]any{"courier": "Delhivery", "tracking_number": awb}); w.Code != http.StatusCreated {
		t.Fatalf("first ship = %d\n%s", w.Code, w.Body.String())
	}
	w := call(t, r, http.MethodPost, sellerPath(second, "/ship"), second.sellerUserID,
		map[string]any{"courier": "Delhivery", "tracking_number": awb})
	if w.Code != http.StatusConflict || errCode(t, w.Body.Bytes()) != "TRACKING_NUMBER_IN_USE" {
		t.Fatalf("reused AWB = %d %s, want 409 TRACKING_NUMBER_IN_USE", w.Code, w.Body.String())
	}
	// Nothing moved on the second order.
	if s, _ := dbOrder(t, second.orderID); s != "confirmed" {
		t.Errorf("second order status = %s after a refused ship, want confirmed", s)
	}
}
