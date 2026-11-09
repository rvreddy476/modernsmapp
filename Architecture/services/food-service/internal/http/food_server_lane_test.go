package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/food-service/internal/service"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Food server lane (9 Nov 2026): the wire contract of the server changes the
// Android food lane (8740a7a17) needs. Goldens: order_place_409_total_changed,
// partner_orders_get_200_page, support_ticket_post_201, refund_post_201 and
// the refund refusals.

var (
	ctLaneTicket = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0030")
	ctLaneRefund = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0031")
	ctLaneOrder2 = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0032")
)

// fslStore records what each new path hands the store.
type fslStore struct {
	service.Store
	mu sync.Mutex

	placed     *postgres.PlaceOrderInput
	placeErr   error
	filter     *postgres.PartnerOrderFilter
	pageOrders []postgres.Order
	pageNext   *postgres.OrderCursor
	refund     *postgres.RefundRequestInput
	refundErr  error
	ticket     *postgres.CreateTicketInput
	replayed   bool
	offlineErr error
}

func (f *fslStore) PlaceOrder(_ context.Context, _ uuid.UUID, in postgres.PlaceOrderInput, _ string) (*postgres.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.placed = &in
	if f.placeErr != nil {
		return nil, f.placeErr
	}
	return &postgres.Order{ID: ctOrder, OrderNumber: "FG1000000000001", UserID: ctCustomer, RestaurantID: ctRestaurant,
		Status: "PAYMENT_PENDING", PaymentStatus: "PENDING", PaymentMethod: "ONLINE", PlacedAt: ctTime}, nil
}

func (f *fslStore) ListPartnerOrdersPage(_ context.Context, _, _ uuid.UUID, filter postgres.PartnerOrderFilter) ([]postgres.Order, *postgres.OrderCursor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.filter = &filter
	return f.pageOrders, f.pageNext, nil
}

func (f *fslStore) RequestRefund(_ context.Context, in postgres.RefundRequestInput) (*postgres.RefundRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refund = &in
	if f.refundErr != nil {
		return nil, f.refundErr
	}
	ticket := ctLaneTicket
	reason := in.Reason
	return &postgres.RefundRequest{ID: ctLaneRefund, TicketID: &ticket, OrderID: in.OrderID, CustomerID: in.CustomerID,
		Amount: in.Amount, Reason: &reason, Status: "requested", CreatedAt: "2026-09-13 06:30:00+00", Replayed: f.replayed}, nil
}

func (f *fslStore) CreateTicket(_ context.Context, in postgres.CreateTicketInput) (*postgres.Ticket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ticket = &in
	if f.refundErr != nil {
		return nil, f.refundErr
	}
	detail := in.Detail
	return &postgres.Ticket{ID: ctLaneTicket, CustomerID: in.CustomerID, OrderID: in.OrderID, Category: in.Category,
		Subject: in.Subject, Detail: &detail, Status: "open", CreatedAt: "2026-09-13 06:30:00+00", Replayed: f.replayed}, nil
}

func (f *fslStore) SetDeliveryAvailability(context.Context, uuid.UUID, bool) (*postgres.DeliveryPartner, error) {
	return nil, f.offlineErr
}

// fslPublisher counts realtime frames.
type fslPublisher struct {
	mu     sync.Mutex
	frames int
}

func (p *fslPublisher) Publish(context.Context, string, string, any) error {
	p.mu.Lock()
	p.frames++
	p.mu.Unlock()
	return nil
}

func fslService(f *fslStore, pub *fslPublisher) *service.Service {
	return service.New(f).WithRealtime(pub, fixedRealtimeSigner{}).WithRealtimeClock(func() time.Time { return ctRtNow })
}

// fslRouter is the real route table (refunds, tickets, availability).
func fslRouter(f *fslStore, pub *fslPublisher) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(fslService(f, pub)).WithServiceAuth(testAdminVerifier()).RegisterRoutes(r)
	return r
}

// fslNewHandlers wires the two handlers that replace handler.go's on their
// routes once handler.go can be edited (PlaceOrderChecked on POST /orders,
// ListPartnerOrdersPage on GET .../orders), behind the same middleware.
func fslNewHandlers(f *fslStore) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(fslService(f, &fslPublisher{})).WithServiceAuth(testAdminVerifier())
	v1 := r.Group("/v1/food", idempotencyFingerprint())
	v1.POST("/orders", h.requireAuthenticated(), h.PlaceOrderChecked)
	v1.GET("/partner/restaurants/:restaurantId/orders", h.requireAuthenticated(), h.ListPartnerOrdersPage)
	return r
}

func fslDo(r *gin.Engine, method, path, body string, user uuid.UUID, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", user.String())
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func fslError(t *testing.T, rec *httptest.ResponseRecorder) (code string, details map[string]any) {
	t.Helper()
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("not an error envelope: %s", rec.Body.String())
	}
	return env.Error.Code, env.Error.Details
}

// ─── Item 7: expected_total_paise ──────────────────────────────────────────

const lanePlaceBody = `{"address_id":"0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0099","payment_method":"upi"`

func TestPlaceOrderCheckedHandsTheExpectedTotalToTheStore(t *testing.T) {
	f := &fslStore{}
	if rec := fslDo(fslNewHandlers(f), http.MethodPost, "/v1/food/orders", lanePlaceBody+`,"expected_total_paise":45600}`, ctCustomer, "k1"); rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if f.placed == nil || f.placed.ExpectedTotalPaise == nil || *f.placed.ExpectedTotalPaise != 45600 {
		t.Fatalf("store got %+v, want expected_total_paise 45600", f.placed)
	}
	// An older app sends none: placement is unchanged.
	f.placed = nil
	if rec := fslDo(fslNewHandlers(f), http.MethodPost, "/v1/food/orders", lanePlaceBody+`}`, ctCustomer, "k2"); rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if f.placed == nil || f.placed.ExpectedTotalPaise != nil {
		t.Fatalf("store got %+v, want no expectation", f.placed)
	}
	// A negative figure is no total the app could have shown.
	f.placed = nil
	rec := fslDo(fslNewHandlers(f), http.MethodPost, "/v1/food/orders", lanePlaceBody+`,"expected_total_paise":-1}`, ctCustomer, "k3")
	if rec.Code != http.StatusBadRequest || f.placed != nil {
		t.Fatalf("negative expectation: status %d, store called=%v", rec.Code, f.placed != nil)
	}
}

func TestPlaceOrderTotalChangedIs409WithTheServerTotal(t *testing.T) {
	f := &fslStore{placeErr: postgres.CheckExpectedTotal(fslInt64(45600), 47200)}
	rec := fslDo(fslNewHandlers(f), http.MethodPost, "/v1/food/orders", lanePlaceBody+`,"expected_total_paise":45600}`, ctCustomer, "k1")
	code, details := fslError(t, rec)
	if rec.Code != http.StatusConflict || code != "FOOD_ORDER_TOTAL_CHANGED" {
		t.Fatalf("status %d code %s: %s", rec.Code, code, rec.Body.String())
	}
	if details["server_total_paise"] != float64(47200) || details["expected_total_paise"] != float64(45600) {
		t.Fatalf("details = %v", details)
	}
	assertContract(t, rec, http.StatusConflict, "order_place_409_total_changed")
}

func fslInt64(v int64) *int64 { return &v }

// ─── Item 5: partner orders filter and cursor ──────────────────────────────

func TestListPartnerOrdersPageReadsFilterAndCursor(t *testing.T) {
	path := "/v1/food/partner/restaurants/" + ctRestaurant.String() + "/orders"
	created := time.Date(2026, 9, 13, 6, 29, 0, 0, time.UTC)
	f := &fslStore{
		pageOrders: []postgres.Order{
			{ID: ctOrder, OrderNumber: "FG1000000000001", UserID: ctCustomer, RestaurantID: ctRestaurant, RestaurantName: "Test Kitchen",
				Status: "CONFIRMED", PaymentStatus: "CAPTURED", PaymentMethod: "ONLINE",
				Totals: postgres.PriceBreakdown{ItemSubtotal: 400, FinalAmount: 456}, PlacedAt: "2026-09-13 06:30:00+00"},
			{ID: ctLaneOrder2, OrderNumber: "FG1000000000002", UserID: ctCustomer, RestaurantID: ctRestaurant, RestaurantName: "Test Kitchen",
				Status: "PREPARING", PaymentStatus: "CAPTURED", PaymentMethod: "ONLINE",
				Totals: postgres.PriceBreakdown{ItemSubtotal: 200, FinalAmount: 236.5}, PlacedAt: "2026-09-13 06:29:00+00"},
		},
		pageNext: &postgres.OrderCursor{CreatedAt: created, ID: ctLaneOrder2},
	}
	cursor := postgres.EncodeOrderCursor(postgres.OrderCursor{CreatedAt: created.Add(time.Minute), ID: ctOrder})
	rec := fslDo(fslNewHandlers(f), http.MethodGet, path+"?status=confirmed,PREPARING&limit=2&cursor="+cursor, ``, ctOwner, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	got := f.filter
	if got == nil || len(got.Statuses) != 2 || got.Statuses[0] != "CONFIRMED" || got.Statuses[1] != "PREPARING" || got.Limit != 2 ||
		got.After == nil || got.After.ID != ctOrder || !got.After.CreatedAt.Equal(created.Add(time.Minute)) {
		t.Fatalf("filter = %+v", got)
	}
	var env struct {
		Meta struct {
			NextCursor string `json:"next_cursor"`
		} `json:"meta"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if back, err := postgres.DecodeOrderCursor(env.Meta.NextCursor); err != nil || back.ID != ctLaneOrder2 {
		t.Fatalf("meta.next_cursor %q: %+v %v", env.Meta.NextCursor, back, err)
	}
	assertContract(t, rec, http.StatusOK, "partner_orders_get_200_page")

	// The original call: no query, so no filter, the default page, and no
	// next_cursor on the last page.
	f.filter, f.pageNext = nil, nil
	rec = fslDo(fslNewHandlers(f), http.MethodGet, path, ``, ctOwner, "")
	if rec.Code != http.StatusOK || f.filter == nil || f.filter.Statuses != nil || f.filter.After != nil || f.filter.Limit != 0 {
		t.Fatalf("plain call: status %d filter %+v", rec.Code, f.filter)
	}
	if strings.Contains(rec.Body.String(), "next_cursor") {
		t.Fatalf("last page names a cursor: %s", rec.Body.String())
	}
}

func TestListPartnerOrdersPageRefusesWhatItCannotRead(t *testing.T) {
	path := "/v1/food/partner/restaurants/" + ctRestaurant.String() + "/orders"
	for query, want := range map[string]string{
		"?status=SHIPPED":   "INVALID_STATUS",
		"?limit=0":          "INVALID_LIMIT",
		"?limit=101":        "INVALID_LIMIT",
		"?limit=ten":        "INVALID_LIMIT",
		"?cursor=not-ours!": "INVALID_CURSOR",
	} {
		f := &fslStore{}
		rec := fslDo(fslNewHandlers(f), http.MethodGet, path+query, ``, ctOwner, "")
		if code, _ := fslError(t, rec); rec.Code != http.StatusBadRequest || code != want || f.filter != nil {
			t.Fatalf("%s: status %d code %s, store called=%v", query, rec.Code, code, f.filter != nil)
		}
	}
}

// ─── Items 6 and 9: refund and ticket Idempotency-Key ──────────────────────

const laneRefundBody = `{"order_id":"0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0015","ticket_id":"0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0030","amount":456,"reason":"Cold food"}`

func TestRefundRequestCarriesTheIdempotencyKey(t *testing.T) {
	f, pub := &fslStore{}, &fslPublisher{}
	rec := fslDo(fslRouter(f, pub), http.MethodPost, "/v1/food/refunds", laneRefundBody, ctCustomer, "refund-key-1")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if f.refund == nil || f.refund.IdempotencyKey != "refund-key-1" || f.refund.CustomerID != ctCustomer || f.refund.OrderID != ctOrder || f.refund.Amount != 456 {
		t.Fatalf("store got %+v", f.refund)
	}
	if pub.frames == 0 {
		t.Fatal("a new refund request announced nothing")
	}
	assertContract(t, rec, http.StatusCreated, "refund_post_201")

	// Without the header the call works as before.
	if rec := fslDo(fslRouter(f, pub), http.MethodPost, "/v1/food/refunds", laneRefundBody, ctCustomer, ""); rec.Code != http.StatusCreated || f.refund.IdempotencyKey != "" {
		t.Fatalf("keyless: status %d key %q", rec.Code, f.refund.IdempotencyKey)
	}
}

// A resend gets the first answer, byte for byte, and announces nothing again.
func TestRefundResendAnswersAsTheFirstAndAnnouncesNothing(t *testing.T) {
	first := fslDo(fslRouter(&fslStore{}, &fslPublisher{}), http.MethodPost, "/v1/food/refunds", laneRefundBody, ctCustomer, "k")
	f, pub := &fslStore{replayed: true}, &fslPublisher{}
	again := fslDo(fslRouter(f, pub), http.MethodPost, "/v1/food/refunds", laneRefundBody, ctCustomer, "k")
	if again.Code != http.StatusCreated || again.Body.String() != first.Body.String() {
		t.Fatalf("resend: %d %s\nfirst: %s", again.Code, again.Body.String(), first.Body.String())
	}
	if pub.frames != 0 {
		t.Fatalf("a resend announced the refund again (%d frames)", pub.frames)
	}
}

func TestRefundRefusalGoldens(t *testing.T) {
	for fixture, tc := range map[string]struct {
		err    error
		status int
	}{
		"refund_post_422_amount_invalid":  {postgres.ErrInvalidRefundAmount, http.StatusUnprocessableEntity},
		"refund_post_409_not_eligible":    {postgres.ErrRefundNotEligible, http.StatusConflict},
		"refund_post_403_order_forbidden": {postgres.ErrSupportOrderForbidden, http.StatusForbidden},
	} {
		t.Run(fixture, func(t *testing.T) {
			rec := fslDo(fslRouter(&fslStore{refundErr: tc.err}, &fslPublisher{}), http.MethodPost, "/v1/food/refunds", laneRefundBody, ctCustomer, "k")
			assertContract(t, rec, tc.status, fixture)
		})
	}
}

const laneTicketBody = `{"order_id":"0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0015","category":"refund","subject":"Refund for order FG1000000000001","detail":"Cold food"}`

func TestSupportTicketCarriesTheIdempotencyKey(t *testing.T) {
	f, pub := &fslStore{}, &fslPublisher{}
	rec := fslDo(fslRouter(f, pub), http.MethodPost, "/v1/food/support/tickets", laneTicketBody, ctCustomer, "ticket-key-1")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if f.ticket == nil || f.ticket.IdempotencyKey != "ticket-key-1" || f.ticket.CustomerID != ctCustomer {
		t.Fatalf("store got %+v", f.ticket)
	}
	if pub.frames == 0 {
		t.Fatal("a new ticket announced nothing")
	}
	assertContract(t, rec, http.StatusCreated, "support_ticket_post_201")

	f2, pub2 := &fslStore{replayed: true}, &fslPublisher{}
	again := fslDo(fslRouter(f2, pub2), http.MethodPost, "/v1/food/support/tickets", laneTicketBody, ctCustomer, "ticket-key-1")
	if again.Code != http.StatusCreated || again.Body.String() != rec.Body.String() || pub2.frames != 0 {
		t.Fatalf("resend: %d frames=%d %s", again.Code, pub2.frames, again.Body.String())
	}
	assertContract(t, fslDo(fslRouter(&fslStore{refundErr: postgres.ErrSupportOrderForbidden}, &fslPublisher{}),
		http.MethodPost, "/v1/food/support/tickets", laneTicketBody, ctCustomer, ""), http.StatusForbidden, "support_ticket_post_403_order_forbidden")
}

// ─── Item 4: offline mid-delivery ──────────────────────────────────────────

func TestGoingOfflineOnAJobIs409WorkBusy(t *testing.T) {
	f := &fslStore{offlineErr: postgres.ErrRiderOnActiveJob}
	rec := fslDo(fslRouter(f, &fslPublisher{}), http.MethodPost, "/v1/food/delivery/availability", `{"is_online":false}`, ctRider, "")
	if code, _ := fslError(t, rec); rec.Code != http.StatusConflict || code != "WORK_BUSY" {
		t.Fatalf("status %d code %s: %s", rec.Code, code, rec.Body.String())
	}
	assertContract(t, rec, http.StatusConflict, "delivery_availability_post_409_on_job")
}
