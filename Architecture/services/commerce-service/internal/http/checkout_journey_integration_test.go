//go:build integration

package http

// C3-LB-2 acceptance criteria 1 and 3, end to end over HTTP.
//
// A positive-value cart goes through the REAL registered routes â the same
// gin engine cmd/server builds, the same handlers, the same service, the same
// store, against a live PostgreSQL â and produces:
//
//	POST /v1/commerce/checkout/quote      a complete server breakdown
//	POST /v1/commerce/v2/orders/checkout  ONE order, holding ONE stock unit,
//	                                      charged at exactly the quoted total
//
// This is the journey B-LB-1 made impossible. Nothing here computes a price:
// the total submitted at checkout is read out of the quote response body, the
// way a client reads it off the wire.
//
// ## Where it stops, and why
//
// It stops BEFORE the payment intent. Opening one requires a running
// payments-service, an Ed25519 service token and a PSP; none is available in
// this environment, and a stub standing in for a PSP would prove nothing
// about money. The external provider step is UNVERIFIED and is reported as
// such â see the handover.
//
// ## The two stubs, and why they are legitimate here
//
// `stubCourier` replaces an outbound HTTP call to Shiprocket. `pii.New` over
// a fixed development key replaces KMS. Both are external network
// dependencies of the QUOTE, not of the pricing under test, and production
// refuses to run with either stubbed (D7/M-10 for the courier,
// buildPIICipher for the cipher). The money path â pricing, tax, the expected
// -total comparison, stock â is entirely real.
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/http/... -v

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/commerce-service/internal/courier"
	"github.com/atpost/commerce-service/internal/pii"
	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// âââ Stubs for the two external network calls the quote makes ââââââââââââââ

type stubCourier struct{ chargeMinor int64 }

func (stubCourier) Name() string { return "stub" }
func (stubCourier) CreateShipment(context.Context, courier.ShipmentRequest) (*courier.ShipmentResponse, error) {
	return nil, fmt.Errorf("not used")
}
func (stubCourier) CancelShipment(context.Context, string) error { return nil }
func (stubCourier) ParseWebhook(context.Context, []byte) ([]courier.TrackingUpdate, error) {
	return nil, nil
}
func (stubCourier) VerifyWebhook(map[string]string, []byte) error { return nil }
func (s stubCourier) CheckServiceability(
	context.Context, courier.ServiceabilityRequest,
) (*courier.ServiceabilityResult, error) {
	return &courier.ServiceabilityResult{
		Serviceable:         true,
		Courier:             "stub",
		ShippingChargeMinor: s.chargeMinor,
	}, nil
}

type devKeyProvider struct{}

func (devKeyProvider) DataKey(_ context.Context, scope pii.Scope, version int) ([]byte, int, error) {
	k := make([]byte, 32)
	if version == 0 {
		version = 1
	}
	copy(k, fmt.Sprintf("journey-test-%s-%d", scope, version))
	return k, version, nil
}

// journeyEngine wires the production route table over a live store.
func journeyEngine(t *testing.T, shippingMinor int64) *gin.Engine {
	t.Helper()
	cipher, err := pii.New(devKeyProvider{}, []byte("journey-test-salt"))
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	svc := service.New(postgres.New(edgePool), nil, "").
		WithCourier(stubCourier{chargeMinor: shippingMinor}).
		WithPII(cipher)

	r := gin.New()
	r.Use(FenceMiddleware())
	h := New(svc)
	h.RegisterRoutes(r)
	h.RegisterP0Routes(r)
	return r
}

// âââ A cart that is worth something ââââââââââââââââââââââââââââââââââ

type journeyFixture struct {
	userID    uuid.UUID
	addressID uuid.UUID
	variantID uuid.UUID
}

func seedJourney(t *testing.T, stock int, unitMinor int64) journeyFixture {
	t.Helper()
	ctx := context.Background()
	f := journeyFixture{userID: uuid.New(), addressID: uuid.New(), variantID: uuid.New()}
	sellerID, productID, cartID := uuid.New(), uuid.New(), uuid.New()

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := edgePool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed: %v\nSQL: %s", err, sql)
		}
	}
	exec(`INSERT INTO sellers (id,user_id,store_name,slug,email,state)
	      VALUES ($1,$2,'Journey Store',$3,'j@example.test','KA')`,
		sellerID, uuid.New(), "journey-"+sellerID.String()[:8])
	exec(`INSERT INTO seller_addresses (seller_id,address_type,contact_name,phone,
	         address_line_1,city,state,postal_code,is_default)
	      VALUES ($1,'pickup','Pickup','9000000000','1 Warehouse Rd','Bengaluru','KA','560001',TRUE)`,
		sellerID)

	var taxClassID uuid.UUID
	if err := edgePool.QueryRow(ctx,
		`SELECT id FROM tax_classes WHERE name = 'GST 18%'`).Scan(&taxClassID); err != nil {
		t.Fatalf("tax class: %v", err)
	}
	exec(`INSERT INTO products (id,seller_id,title,slug,status,approval_status,
	         return_policy_type,tax_class_id,weight_grams)
	      VALUES ($1,$2,'Journey Product',$3,'active','approved','7_days',$4,500)`,
		productID, sellerID, "jp-"+productID.String()[:8], taxClassID)
	exec(`INSERT INTO product_variants (id,product_id,sku,mrp,selling_price,
	         mrp_minor,selling_price_minor,weight_grams)
	      VALUES ($1,$2,$3,$4,$4,$5,$5,500)`,
		f.variantID, productID, "SKU-"+f.variantID.String()[:8],
		float64(unitMinor)/100.0, unitMinor)
	exec(`INSERT INTO inventory_items (variant_id,seller_id,total_qty,reserved_qty)
	      VALUES ($1,$2,$3,0)`, f.variantID, sellerID, stock)
	exec(`INSERT INTO customer_addresses (id,user_id,contact_name,phone,
	         address_line_1,city,state,postal_code)
	      VALUES ($1,$2,'Buyer','9111111111','5 Main St','Bengaluru','KA','560002')`,
		f.addressID, f.userID)
	exec(`INSERT INTO carts (id,user_id) VALUES ($1,$2)`, cartID, f.userID)
	exec(`INSERT INTO cart_items (id,cart_id,variant_id,product_id,quantity,
	         price_snapshot,price_snapshot_minor)
	      VALUES (gen_random_uuid(),$1,$2,$3,1,$4,$5)`,
		cartID, f.variantID, productID, float64(unitMinor)/100.0, unitMinor)
	return f
}

func (f journeyFixture) post(t *testing.T, r *gin.Engine, path, idem string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", f.userID.String())
	if idem != "" {
		req.Header.Set("Idempotency-Key", idem)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// decode pulls `data` out of the API envelope, the way a client does.
func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var env struct {
		Data  T `json:"data"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decoding %d body %s: %v", w.Code, w.Body.String(), err)
	}
	if env.Error != nil {
		t.Fatalf("server returned %s: %s", env.Error.Code, env.Error.Message)
	}
	return env.Data
}

type quoteBody struct {
	QuoteID       string `json:"quote_id"`
	SubtotalMinor int64  `json:"subtotal_minor"`
	DiscountMinor int64  `json:"discount_minor"`
	ShippingMinor int64  `json:"shipping_minor"`
	TaxMinor      int64  `json:"tax_minor"`
	TotalMinor    int64  `json:"total_minor"`
	Currency      string `json:"currency"`
	Serviceable   bool   `json:"serviceable"`
}

type checkoutBody struct {
	OrderID     string `json:"order_id"`
	OrderNumber string `json:"order_number"`
	TotalMinor  int64  `json:"total_minor"`
	TaxMinor    int64  `json:"tax_minor"`
}

// âââ The journey âââââââââââââââââââââââââââââââââââââââââââââââââââââ

func TestC3JourneyQuoteThenCheckoutOverHTTP(t *testing.T) {
	const unitMinor, shippingMinor = 100000, 4000
	f := seedJourney(t, 5, unitMinor)
	r := journeyEngine(t, shippingMinor)

	// 1. QUOTE â the server states the whole price.
	qw := f.post(t, r, "/v1/commerce/checkout/quote", "", map[string]any{
		"address_id":     f.addressID.String(),
		"payment_method": "upi",
	})
	if qw.Code != http.StatusOK {
		t.Fatalf("quote returned %d: %s", qw.Code, qw.Body.String())
	}
	q := decode[quoteBody](t, qw)

	if !q.Serviceable || q.QuoteID == "" {
		t.Fatalf("unusable quote: %+v", q)
	}
	if q.SubtotalMinor != unitMinor {
		t.Fatalf("subtotal_minor = %d, want %d â the quote must price the GOODS, "+
			"not just the delivery", q.SubtotalMinor, unitMinor)
	}
	if want := q.SubtotalMinor - q.DiscountMinor + q.ShippingMinor; q.TotalMinor != want {
		t.Fatalf("total_minor = %d, want subtotal-discount+shipping = %d", q.TotalMinor, want)
	}
	if q.TaxMinor <= 0 || q.TaxMinor >= q.TotalMinor {
		t.Fatalf("tax_minor = %d against total %d; GST is the portion INSIDE the total",
			q.TaxMinor, q.TotalMinor)
	}
	if q.Currency != "INR" {
		t.Fatalf("currency = %q", q.Currency)
	}

	// 2. CHECKOUT â at exactly the number the server just stated.
	cw := f.post(t, r, "/v1/commerce/v2/orders/checkout", "journey-"+uuid.NewString(), map[string]any{
		"address_id":           f.addressID.String(),
		"quote_id":             q.QuoteID,
		"payment_method":       "upi",
		"expected_total_minor": q.TotalMinor,
	})
	if cw.Code != http.StatusCreated {
		t.Fatalf("checkout returned %d: %s\n\nThis is B-LB-1 if the code is PRICE_CHANGED: "+
			"the server refused its own quoted total.", cw.Code, cw.Body.String())
	}
	c := decode[checkoutBody](t, cw)

	if c.TotalMinor != q.TotalMinor {
		t.Fatalf("charged %d, quoted %d", c.TotalMinor, q.TotalMinor)
	}
	if c.TaxMinor != q.TaxMinor {
		t.Fatalf("charged tax %d, quoted tax %d", c.TaxMinor, q.TaxMinor)
	}

	// 3. ONE order, ONE stock hold.
	var orders, reserved int
	if err := edgePool.QueryRow(context.Background(),
		`SELECT count(*) FROM orders WHERE customer_user_id=$1`, f.userID).Scan(&orders); err != nil {
		t.Fatal(err)
	}
	if orders != 1 {
		t.Fatalf("orders = %d, want exactly 1", orders)
	}
	if err := edgePool.QueryRow(context.Background(),
		`SELECT reserved_qty FROM inventory_items WHERE variant_id=$1`,
		f.variantID).Scan(&reserved); err != nil {
		t.Fatal(err)
	}
	if reserved != 1 {
		t.Fatalf("reserved_qty = %d, want exactly 1", reserved)
	}

	// The payment intent is the next step and is NOT taken here: it needs a
	// running payments-service and a PSP. See the file header.
	t.Logf("journey complete to the payments boundary: order %s at %d minor, 1 unit held",
		c.OrderNumber, c.TotalMinor)
}

// The price-change half of the same journey, over HTTP: a total the server
// did not state is refused, and nothing is written.
func TestC3JourneyRefusesATotalTheServerNeverStated(t *testing.T) {
	f := seedJourney(t, 5, 100000)
	r := journeyEngine(t, 4000)

	qw := f.post(t, r, "/v1/commerce/checkout/quote", "", map[string]any{
		"address_id":     f.addressID.String(),
		"payment_method": "upi",
	})
	q := decode[quoteBody](t, qw)

	// The old client's number: shipping alone.
	cw := f.post(t, r, "/v1/commerce/v2/orders/checkout", "journey-bad-"+uuid.NewString(), map[string]any{
		"address_id":           f.addressID.String(),
		"quote_id":             q.QuoteID,
		"payment_method":       "upi",
		"expected_total_minor": q.ShippingMinor,
	})
	if cw.Code == http.StatusCreated {
		t.Fatal("checkout accepted a total the buyer was never shown")
	}

	var orders, reserved int
	_ = edgePool.QueryRow(context.Background(),
		`SELECT count(*) FROM orders WHERE customer_user_id=$1`, f.userID).Scan(&orders)
	_ = edgePool.QueryRow(context.Background(),
		`SELECT reserved_qty FROM inventory_items WHERE variant_id=$1`, f.variantID).Scan(&reserved)
	if orders != 0 || reserved != 0 {
		t.Fatalf("a refused checkout left orders=%d reserved=%d", orders, reserved)
	}
}
