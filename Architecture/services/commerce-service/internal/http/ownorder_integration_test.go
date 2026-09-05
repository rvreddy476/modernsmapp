//go:build integration

package http

// A seller acting on their own order, and a buyer reading it — over the real
// registered routes, against a live PostgreSQL.
//
// ─── THE DEFECT THESE PROVE ─────────────────────────────────────────────
//
// Service.OrderActor compared `order_items.seller_id` — a `sellers.id` —
// against the caller's X-User-Id, which is a USER id. The two are drawn from
// different tables and never collide, so `role.IsSeller` could not be true
// for anyone. Every write it gates (book a shipment, issue an invoice) and
// every read (get shipment, list shipments, get invoice) answered 403 to the
// seller who owned the line, saying "actor is not a seller on this order".
//
// The E2E journey hit exactly that on a real order and read it as an
// authorisation bug in the seller's account.
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/http/... -v

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atpost/commerce-service/internal/courier"
	"github.com/atpost/commerce-service/internal/pii"
	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// bookingCourier is the COURIER_PROVIDER=stub adapter's behaviour: it
// confirms a booking without contacting a carrier. journeyEngine's
// stubCourier deliberately refuses to book (it exists to price a quote), and
// what is under test here is the authorisation gate in front of the booking,
// not the carrier integration behind it.
type bookingCourier struct{ stubCourier }

func (bookingCourier) CreateShipment(_ context.Context, req courier.ShipmentRequest) (*courier.ShipmentResponse, error) {
	id := uuid.New().String()[:12]
	return &courier.ShipmentResponse{
		CourierOrderID: "co_" + id,
		AWBNumber:      "AWB" + id,
		LabelURL:       "https://courier.test/label/" + id,
		TrackingURL:    "https://courier.test/track/" + id,
		EstimatedETA:   time.Now().Add(72 * time.Hour),
	}, nil
}

// fulfilmentEngine is journeyEngine with a courier that actually books.
func fulfilmentEngine(t *testing.T) *gin.Engine {
	t.Helper()
	cipher, err := pii.New(devKeyProvider{}, []byte("own-order-test-salt"))
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	svc := service.New(postgres.New(edgePool), nil, "").
		WithCourier(bookingCourier{stubCourier{chargeMinor: 4900}}).
		WithPII(cipher)
	r := gin.New()
	r.Use(FenceMiddleware())
	h := New(svc)
	h.RegisterRoutes(r)
	h.RegisterP0Routes(r)
	return r
}

// ownOrderFixture is one paid, single-seller order — the state a seller
// books a shipment from.
type ownOrderFixture struct {
	sellerUserID uuid.UUID // what the seller sends as X-User-Id
	sellerID     uuid.UUID // what order_items.seller_id holds
	buyerUserID  uuid.UUID
	orderID      uuid.UUID
	productID    uuid.UUID
	variantID    uuid.UUID
}

func seedOwnOrder(t *testing.T, unitMinor, shippingMinor, taxMinor int64) ownOrderFixture {
	t.Helper()
	ctx := context.Background()
	f := ownOrderFixture{
		sellerUserID: uuid.New(), sellerID: uuid.New(),
		buyerUserID: uuid.New(), orderID: uuid.New(),
		productID: uuid.New(), variantID: uuid.New(),
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := edgePool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed: %v\nSQL: %s", err, sql)
		}
	}

	exec(`INSERT INTO sellers (id,user_id,store_name,slug,email,state)
	      VALUES ($1,$2,'Own Order Store',$3,'own@example.test','KA')`,
		f.sellerID, f.sellerUserID, "own-"+f.sellerID.String()[:8])
	exec(`INSERT INTO seller_addresses (seller_id,address_type,contact_name,phone,
	         address_line_1,city,state,postal_code,is_default)
	      VALUES ($1,'pickup','Pickup','9000000000','1 Warehouse Rd','Bengaluru','KA','560001',TRUE)`,
		f.sellerID)

	var taxClassID uuid.UUID
	if err := edgePool.QueryRow(ctx,
		`SELECT id FROM tax_classes WHERE name = 'GST 18%'`).Scan(&taxClassID); err != nil {
		t.Fatalf("tax class: %v", err)
	}
	exec(`INSERT INTO products (id,seller_id,title,slug,status,approval_status,
	         return_policy_type,tax_class_id,weight_grams)
	      VALUES ($1,$2,'Own Order Product',$3,'active','approved','7_days',$4,500)`,
		f.productID, f.sellerID, "oop-"+f.productID.String()[:8], taxClassID)
	// The rupee columns deliberately carry a DIFFERENT number from the paise
	// ones, so a read that falls back to the deprecated column is visibly
	// wrong rather than accidentally right.
	exec(`INSERT INTO product_variants (id,product_id,sku,mrp,selling_price,
	         mrp_minor,selling_price_minor,weight_grams)
	      VALUES ($1,$2,$3,0,0,$4,$5,500)`,
		f.variantID, f.productID, "OWN-"+f.variantID.String()[:8],
		unitMinor+12000, unitMinor)
	exec(`INSERT INTO inventory_items (variant_id,seller_id,total_qty,reserved_qty)
	      VALUES ($1,$2,40,6)`, f.variantID, f.sellerID)

	total := unitMinor + shippingMinor + taxMinor
	exec(`INSERT INTO orders (id,customer_user_id,order_number,status,payment_status,
	         payment_method,currency_code,
	         subtotal,shipping_charges,tax_amount,final_amount,
	         subtotal_minor,shipping_charges_minor,tax_amount_minor,final_amount_minor)
	      VALUES ($1,$2,$3,'confirmed','paid','upi','INR',
	              0,0,0,0,$4,$5,$6,$7)`,
		f.orderID, f.buyerUserID, "ORD-OWN-"+f.orderID.String()[:8],
		unitMinor, shippingMinor, taxMinor, total)
	exec(`INSERT INTO order_items (id,order_id,product_id,variant_id,seller_id,
	         product_title,sku,quantity,unit_mrp,unit_price,tax_amount,final_price,status,
	         unit_mrp_minor,unit_price_minor,tax_amount_minor,final_price_minor)
	      VALUES (gen_random_uuid(),$1,$2,$3,$4,'Own Order Product',$5,1,0,0,0,0,'confirmed',
	              $6,$7,$8,$9)`,
		f.orderID, f.productID, f.variantID, f.sellerID, "OWN-"+f.variantID.String()[:8],
		unitMinor+12000, unitMinor, taxMinor, unitMinor)
	return f
}

// ─── Defect 3: a seller can act on their own order ──────────────────────

func TestTheOwningSellerMayBookAShipmentAndAStrangerMayNot(t *testing.T) {
	r := fulfilmentEngine(t)
	f := seedOwnOrder(t, 88000, 4900, 14172)

	t.Run("the owning seller books it", func(t *testing.T) {
		w := call(t, r, http.MethodPost,
			"/v1/commerce/orders/"+f.orderID.String()+"/shipment", f.sellerUserID, nil)
		if w.Code == http.StatusForbidden {
			t.Fatalf("the seller who owns every line on this order was refused with 403: %s\n"+
				"OrderActor is comparing order_items.seller_id (a sellers.id) against the "+
				"caller's user id, so IsSeller can never be true", w.Body.String())
		}
		if w.Code != http.StatusCreated {
			t.Fatalf("status %d, want 201\n%s", w.Code, w.Body.String())
		}
	})

	t.Run("a stranger may not", func(t *testing.T) {
		w := call(t, r, http.MethodPost,
			"/v1/commerce/orders/"+f.orderID.String()+"/shipment", uuid.New(), nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("a caller with no seller profile and no items on this order got %d, "+
				"want 403 — the gate must still be a gate\n%s", w.Code, w.Body.String())
		}
	})

	t.Run("a DIFFERENT seller may not", func(t *testing.T) {
		// The sharper control: a real seller account, with a real
		// sellers.id, that simply has no line on this order. Passing this
		// rules out "any seller can act on any order".
		other := seedOwnOrder(t, 5000, 0, 900)
		w := call(t, r, http.MethodPost,
			"/v1/commerce/orders/"+f.orderID.String()+"/shipment", other.sellerUserID, nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("a seller with no items on this order got %d, want 403\n%s",
				w.Code, w.Body.String())
		}
	})

	t.Run("the seller can read what they booked", func(t *testing.T) {
		w := call(t, r, http.MethodGet,
			"/v1/commerce/orders/"+f.orderID.String()+"/shipments", f.sellerUserID, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d, want 200 — the read side answered 403 for the same reason "+
				"the write side did\n%s", w.Code, w.Body.String())
		}
	})

	t.Run("the buyer can still read it", func(t *testing.T) {
		w := call(t, r, http.MethodGet,
			"/v1/commerce/orders/"+f.orderID.String()+"/shipments", f.buyerUserID, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("the customer got %d on their own order's shipments, want 200\n%s",
				w.Code, w.Body.String())
		}
	})
}

// ─── Defect 2: the order detail shape ───────────────────────────────────

func TestOrderDetailCarriesMoneyLinesAddressAndTheCancelFlag(t *testing.T) {
	r := journeyEngine(t, 4900)
	f := seedOwnOrder(t, 88000, 4900, 14172)

	w := call(t, r, http.MethodGet, "/v1/commerce/orders/"+f.orderID.String(), f.buyerUserID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d\n%s", w.Code, w.Body.String())
	}
	var env struct {
		Data struct {
			SubtotalMinor int64 `json:"subtotal_minor"`
			ShippingMinor int64 `json:"shipping_minor"`
			TaxMinor      int64 `json:"tax_minor"`
			TotalMinor    int64 `json:"total_minor"`
			Currency      string
			CanCancel     bool `json:"can_cancel"`
			Items         []struct {
				SKU            string `json:"sku"`
				Quantity       int    `json:"quantity"`
				LineTotalMinor int64  `json:"line_total_minor"`
			} `json:"items"`
			// If the raw base64 snapshot were still being emitted it would
			// land here as a string and fail to decode into an object.
			DeliveryAddress *struct {
				City string `json:"city"`
			} `json:"delivery_address"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("order detail did not decode: %v\n%s", err, w.Body.String())
	}
	d := env.Data

	if d.TotalMinor != 107072 {
		t.Errorf("total_minor = %d, want 107072 — the detail endpoint was returning the raw "+
			"row, whose rupee columns migration 007 stopped maintaining, so the same order "+
			"read ₹1070.72 in the list and ₹0.00 here", d.TotalMinor)
	}
	if d.SubtotalMinor != 88000 || d.ShippingMinor != 4900 || d.TaxMinor != 14172 {
		t.Errorf("breakdown = subtotal %d / shipping %d / tax %d, want 88000 / 4900 / 14172",
			d.SubtotalMinor, d.ShippingMinor, d.TaxMinor)
	}
	if d.Currency != "INR" {
		t.Errorf("currency = %q, want INR", d.Currency)
	}
	if len(d.Items) != 1 {
		t.Fatalf("items = %d, want 1 — the order screen showed no lines at all", len(d.Items))
	}
	if d.Items[0].LineTotalMinor != 88000 {
		t.Errorf("line_total_minor = %d, want 88000", d.Items[0].LineTotalMinor)
	}
	if !d.CanCancel {
		t.Error("can_cancel = false on a confirmed order; the D6 matrix permits a customer " +
			"cancel from `confirmed`, so the button must be offered")
	}
}

// A stranger gets "not found", not "forbidden" — order ids must not be
// probeable by watching the status code change.
func TestOrderDetailDoesNotConfirmSomeoneElsesOrderExists(t *testing.T) {
	r := journeyEngine(t, 4900)
	f := seedOwnOrder(t, 88000, 4900, 14172)

	w := call(t, r, http.MethodGet, "/v1/commerce/orders/"+f.orderID.String(), uuid.New(), nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d for a stranger's order, want 404\n%s", w.Code, w.Body.String())
	}
	// And an id that does not exist answers the same way.
	w = call(t, r, http.MethodGet, "/v1/commerce/orders/"+uuid.New().String(), f.buyerUserID, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d for a non-existent order, want 404\n%s", w.Code, w.Body.String())
	}
}

// ─── Defect 1: variant money + stock on product detail ──────────────────

func TestProductDetailVariantsCarryPaiseStockAndTheStoreName(t *testing.T) {
	r := journeyEngine(t, 4900)
	f := seedOwnOrder(t, 88000, 4900, 14172)

	w := call(t, r, http.MethodGet, "/v1/commerce/products/"+f.productID.String(), uuid.Nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d\n%s", w.Code, w.Body.String())
	}
	var env struct {
		Data struct {
			Product struct {
				SellerName   string `json:"seller_name"`
				RetailerName string `json:"retailer_name"`
			} `json:"product"`
			Variants []struct {
				SKU               string `json:"sku"`
				SellingPriceMinor *int64 `json:"selling_price_minor"`
				MRPMinor          *int64 `json:"mrp_minor"`
				AvailableQty      *int   `json:"available_qty"`
			} `json:"variants"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("product detail did not decode: %v\n%s", err, w.Body.String())
	}

	if env.Data.Product.SellerName != "Own Order Store" {
		t.Errorf("seller_name = %q, want %q — the body sent only retailer_name, which the "+
			"app does not read, so the store name was absent from product detail",
			env.Data.Product.SellerName, "Own Order Store")
	}
	if env.Data.Product.RetailerName != "Own Order Store" {
		t.Errorf("retailer_name = %q; the alias must keep working for the web caller",
			env.Data.Product.RetailerName)
	}

	if len(env.Data.Variants) != 1 {
		t.Fatalf("variants = %d, want 1", len(env.Data.Variants))
	}
	v := env.Data.Variants[0]
	if v.SellingPriceMinor == nil || *v.SellingPriceMinor != 88000 {
		t.Errorf("selling_price_minor = %v, want 88000 — without it every variant on the "+
			"product page rendered as ₹0.00", v.SellingPriceMinor)
	}
	if v.MRPMinor == nil || *v.MRPMinor != 100000 {
		t.Errorf("mrp_minor = %v, want 100000", v.MRPMinor)
	}
	// 40 total, 6 reserved.
	if v.AvailableQty == nil || *v.AvailableQty != 34 {
		t.Errorf("available_qty = %v, want 34 — without it every variant rendered as out "+
			"of stock and add-to-cart was disabled", v.AvailableQty)
	}
}

// ─── Defect 6: the seller's own subtotal ────────────────────────────────

func TestSellerOrderDetailReportsWhatTheSellerIsOwed(t *testing.T) {
	r := journeyEngine(t, 4900)
	f := seedOwnOrder(t, 88000, 4900, 14172)

	w := call(t, r, http.MethodGet,
		"/v1/commerce/seller/orders/"+f.orderID.String(), f.sellerUserID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d\n%s", w.Code, w.Body.String())
	}
	var env struct {
		Data struct {
			SellerSubtotalMinor int64   `json:"seller_subtotal_minor"`
			SellerSubtotal      float64 `json:"seller_subtotal"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v\n%s", err, w.Body.String())
	}
	if env.Data.SellerSubtotalMinor != 88000 {
		t.Errorf("seller_subtotal_minor = %d, want 88000", env.Data.SellerSubtotalMinor)
	}
	if env.Data.SellerSubtotal != 880 {
		t.Errorf("seller_subtotal = %v, want 880 — it reported 0 because it summed "+
			"order_items.final_price, the column migration 007 stopped writing",
			env.Data.SellerSubtotal)
	}
}

// ─── POST /products/:id/variants accepts paise ──────────────────────────

// The seller's add-a-variant route was the last money-entry point that took
// rupees ONLY, while POST /products and PATCH /variants/:id both accept
// `*_minor` and prefer it. So an app that speaks paise everywhere else had
// to convert on the way out of this one call and hope the server's rounding
// agreed with its own — and for a price like ₹1299.99 it does not reliably:
// the float arrives as 1299.9899999999998 and the stored paise depend on
// which way math.Round falls.
func TestAddingAVariantAcceptsPaiseAndStillAcceptsRupees(t *testing.T) {
	r := fulfilmentEngine(t)
	f := seedOwnOrder(t, 88000, 4900, 14172)

	t.Run("paise are stored exactly", func(t *testing.T) {
		w := call(t, r, http.MethodPost, "/v1/commerce/products/"+f.productID.String()+"/variants",
			f.sellerUserID, map[string]any{
				"sku":                 "PAISE-" + uuid.New().String()[:8],
				"option_1_name":       "Size",
				"option_1_value":      "XL",
				"mrp_minor":           149900,
				"selling_price_minor": 129999, // ₹1299.99 — the float-hostile case
				"cost_price_minor":    90000,
			})
		if w.Code != http.StatusCreated {
			t.Fatalf("status %d, want 201\n%s", w.Code, w.Body.String())
		}
		v := decodeVariant(t, w)
		if v.SellingPriceMinor == nil || *v.SellingPriceMinor != 129999 {
			t.Errorf("selling_price_minor = %v, want 129999 exactly", v.SellingPriceMinor)
		}
		if v.MRPMinor == nil || *v.MRPMinor != 149900 {
			t.Errorf("mrp_minor = %v, want 149900", v.MRPMinor)
		}
		// And a stock row exists, at zero — POST /products creates one for
		// the variants it makes and this route did not, so a variant added
		// after launch had no inventory row for the seller to adjust.
		if v.AvailableQty == nil {
			t.Error("available_qty is absent: the new variant has no inventory row, so it " +
				"reports no stock figure at all rather than `0 in stock`")
		} else if *v.AvailableQty != 0 {
			t.Errorf("available_qty = %d on a brand-new variant, want 0", *v.AvailableQty)
		}
	})

	t.Run("the legacy rupee shape still works", func(t *testing.T) {
		w := call(t, r, http.MethodPost, "/v1/commerce/products/"+f.productID.String()+"/variants",
			f.sellerUserID, map[string]any{
				"sku":            "RUPEE-" + uuid.New().String()[:8],
				"mrp":            1599,
				"selling_price":  1399,
				"option_1_name":  "Size",
				"option_1_value": "XXL",
			})
		if w.Code != http.StatusCreated {
			t.Fatalf("status %d, want 201 — a client written before the minor migration must "+
				"keep working\n%s", w.Code, w.Body.String())
		}
		v := decodeVariant(t, w)
		if v.SellingPriceMinor == nil || *v.SellingPriceMinor != 139900 {
			t.Errorf("selling_price_minor = %v, want 139900 converted from rupees", v.SellingPriceMinor)
		}
	})

	t.Run("a variant with no price at all is refused", func(t *testing.T) {
		w := call(t, r, http.MethodPost, "/v1/commerce/products/"+f.productID.String()+"/variants",
			f.sellerUserID, map[string]any{"sku": "FREE-" + uuid.New().String()[:8]})
		if w.Code == http.StatusCreated {
			t.Fatalf("a priceless variant was created; the buyer could take it for nothing\n%s",
				w.Body.String())
		}
		if w.Code != http.StatusBadRequest {
			t.Errorf("status %d, want 400\n%s", w.Code, w.Body.String())
		}
	})
}

// decodeVariant reads the created variant out of the API envelope.
func decodeVariant(t *testing.T, w *httptest.ResponseRecorder) struct {
	SKU               string `json:"sku"`
	SellingPriceMinor *int64 `json:"selling_price_minor"`
	MRPMinor          *int64 `json:"mrp_minor"`
	AvailableQty      *int   `json:"available_qty"`
} {
	t.Helper()
	var env struct {
		Data struct {
			SKU               string `json:"sku"`
			SellingPriceMinor *int64 `json:"selling_price_minor"`
			MRPMinor          *int64 `json:"mrp_minor"`
			AvailableQty      *int   `json:"available_qty"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode variant: %v\n%s", err, w.Body.String())
	}
	return env.Data
}
