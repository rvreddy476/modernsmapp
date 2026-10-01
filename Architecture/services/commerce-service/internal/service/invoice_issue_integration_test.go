//go:build integration

package service

// The invoice a buyer receives for a P0 order carries the paise that order
// STORED — per line and in total — and never a recomputation.
//
// The defect this pins (1 Oct 2026): IssueInvoice built its lines from the
// legacy NUMERIC rupee columns of order_items, which the P0 checkout writes
// as 0.00. Every invoice issued since the P0 checkout read ₹0 per line and a
// ₹0 grand total on an order that charged real money. On dev, ORD-2026-011170
// charged ₹2,447.00 and was invoiced at ₹0.00.
//
// The order placed here is the awkward one on purpose: two lines on
// different GST slabs (18% and 5%), a seller coupon scoped to ONE of them
// (so the other line is CouponExcluded and must show no discount), and a
// delivery charge that the checkout spreads across both lines. It runs once
// intra-state (CGST+SGST) and once inter-state (IGST).
//
//	source scratchpad/c1dsn.sh   # commerce_it_test, never commerce_db
//	go test -p 1 -count=1 -tags integration ./internal/service/ -run InvoiceCarries

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// capturedBlob keeps what IssueInvoice uploads so the test can read the
// document the buyer would download.
type capturedBlob struct {
	mu   sync.Mutex
	objs map[string][]byte
}

func (b *capturedBlob) Upload(_ context.Context, key string, data []byte, _ string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.objs == nil {
		b.objs = map[string][]byte{}
	}
	b.objs[key] = append([]byte(nil), data...)
	return nil
}

func (b *capturedBlob) PresignedGetURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}

func invExec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := svcTestPool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("seed: %v\nSQL: %s", err, sql)
	}
}

// invProduct seeds one live product with one variant (50 in stock) at
// unitMinor on the given GST slab, with an HSN code.
func invProduct(t *testing.T, sellerID uuid.UUID, title string, unitMinor int64, taxPct, hsn string) (productID, variantID uuid.UUID, sku string) {
	t.Helper()
	ctx := context.Background()
	productID, variantID = uuid.New(), uuid.New()
	sku = "INV-" + variantID.String()[:8]
	var taxID uuid.UUID
	if err := svcTestPool.QueryRow(ctx, `SELECT id FROM tax_classes WHERE name = $1`, "GST "+taxPct+"%").Scan(&taxID); err != nil {
		t.Fatalf("tax class %s: %v", taxPct, err)
	}
	invExec(t, `INSERT INTO products (id,seller_id,title,slug,status,approval_status,return_policy_type,tax_class_id,hsn_code,weight_grams)
	            VALUES ($1,$2,$3,$4,'active','approved','7_days',$5,$6,400)`,
		productID, sellerID, title, "inv-"+productID.String()[:8], taxID, hsn)
	invExec(t, `INSERT INTO product_variants (id,product_id,sku,mrp,selling_price,mrp_minor,selling_price_minor,weight_grams)
	            VALUES ($1,$2,$3,$4,$4,$5,$5,400)`,
		variantID, productID, sku, float64(unitMinor)/100.0, unitMinor)
	invExec(t, `INSERT INTO inventory_items (variant_id,seller_id,total_qty,reserved_qty) VALUES ($1,$2,50,0)`,
		variantID, sellerID)
	invExec(t, `
		INSERT INTO product_offers (product_id, seller_id, status, visibility,
		                            approval_status, rejection_reason, published_at, condition)
		SELECT p.id, p.seller_id, p.status, p.visibility,
		       p.approval_status, p.rejection_reason, p.published_at, p.condition
		  FROM products p WHERE p.id = $1
		 ON CONFLICT (product_id, seller_id) DO NOTHING`, productID)
	return productID, variantID, sku
}

type invDest struct {
	city, state, pin string
}

// placeP0Order seeds a KA seller, two products on different slabs, a 10%
// seller coupon scoped to the 5% product only, and a buyer at dest; quotes
// and checks out through the P0 path; and marks the order paid. It returns
// the order id.
func placeP0Order(t *testing.T, st *postgres.Store, dest invDest) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	sellerID, userID, addressID, cartID := uuid.New(), uuid.New(), uuid.New(), uuid.New()

	invExec(t, `INSERT INTO sellers (id,user_id,store_name,slug,email,state,status)
	            VALUES ($1,$2,'Invoice Store',$3,'inv-seller@example.test','KA','approved')`,
		sellerID, uuid.New(), "inv-store-"+sellerID.String()[:8])
	invExec(t, `INSERT INTO seller_addresses (seller_id,address_type,contact_name,phone,
	               address_line_1,city,state,postal_code,is_default)
	            VALUES ($1,'pickup','Pickup','9000000000','1 Warehouse Rd','Bengaluru','KA','560001',TRUE)`,
		sellerID)

	p18, v18, _ := invProduct(t, sellerID, "Cotton Shirt", 118000, "18", "6205") // ₹1,180 at 18%
	p5, v5, _ := invProduct(t, sellerID, "Paper Notebook", 52500, "5", "4820")   // ₹525 at 5%

	invExec(t, `INSERT INTO customer_addresses (id,user_id,contact_name,phone,address_line_1,city,state,postal_code)
	            VALUES ($1,$2,'Buyer','9111111111','5 Main St',$3,$4,$5)`,
		addressID, userID, dest.city, dest.state, dest.pin)
	invExec(t, `INSERT INTO carts (id,user_id) VALUES ($1,$2)`, cartID, userID)
	invExec(t, `INSERT INTO cart_items (id,cart_id,variant_id,product_id,quantity,price_snapshot,price_snapshot_minor)
	            VALUES (gen_random_uuid(),$1,$2,$3,2,1180.00,118000)`, cartID, v18, p18)
	invExec(t, `INSERT INTO cart_items (id,cart_id,variant_id,product_id,quantity,price_snapshot,price_snapshot_minor)
	            VALUES (gen_random_uuid(),$1,$2,$3,3,525.00,52500)`, cartID, v5, p5)

	// A 10% seller coupon on the notebook only: the shirt line is
	// CouponExcluded and its GST must not be lowered by it.
	code := "INV" + strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")[:10])
	invExec(t, `INSERT INTO coupons (seller_id, code, discount_type, discount_value, discount_basis_points,
	                 min_order_amount_minor, max_uses_per_user, applicable_to, applicable_ids,
	                 starts_at, is_public, is_active)
	            VALUES ($1,$2,'percentage',10,1000,0,5,'product',$3,NOW() - INTERVAL '1 hour',TRUE,TRUE)`,
		sellerID, code, []uuid.UUID{p5})

	const shippingMinor = 4900
	meta, err := st.CartMetaForQuote(ctx, userID)
	if err != nil {
		t.Fatalf("cart meta: %v", err)
	}
	pricing, err := st.PriceCartForQuote(ctx, postgres.QuotePricingInput{
		UserID: userID, CartID: meta.CartID, ShippingMinor: shippingMinor,
		CouponCode: code, SellerState: "KA", DestinationState: dest.state,
	})
	if err != nil {
		t.Fatalf("price the cart: %v", err)
	}
	hash := postgres.HashAddress("5 Main St", "", dest.city, dest.state, dest.pin)
	q, err := st.SaveQuote(ctx, postgres.ShippingQuote{
		UserID: userID, CartID: meta.CartID, CartVersion: meta.Version, AddressID: addressID,
		AddressHash: hash, SellerID: meta.SellerID, ItemsHash: meta.ItemsHash, TotalWeightG: meta.WeightG,
		DestinationPin: dest.pin, ShippingMinor: shippingMinor, CourierCode: "test",
		CouponCode: code, PaymentMethod: "upi",
		SubtotalMinor: pricing.SubtotalMinor, DiscountMinor: pricing.DiscountMinor,
		TaxMinor: pricing.TaxMinor, TotalMinor: pricing.TotalMinor,
	}, map[string]string{"courier": "test"})
	if err != nil {
		t.Fatalf("save quote: %v", err)
	}
	row, err := st.GetAddressRow(ctx, addressID)
	if err != nil {
		t.Fatalf("read the address: %v", err)
	}
	idem := "inv-" + uuid.NewString()
	res, err := st.Checkout(ctx, postgres.CheckoutParams{
		UserID: userID, AddressID: addressID, QuoteID: q.ID,
		IdempotencyKey: idem, RequestFingerprint: "fp-" + idem,
		CouponCode: code, PaymentMethod: "upi",
		ExpectedTotalMinor: money.Paise(pricing.TotalMinor),
		AddressSnapshot: []byte(fmt.Sprintf(`{"contact_name":"Buyer","phone":"9111111111",
			"address_line_1":"5 Main St","city":%q,"state":%q,"postal_code":%q,"country":"IN"}`,
			dest.city, dest.state, dest.pin)),
		DestinationState: dest.state, DestinationPin: dest.pin,
		AddressHash: hash, AddressFingerprint: row.ContentFingerprint(),
		ActorType: "customer",
	})
	if err != nil {
		t.Fatalf("P0 checkout: %v", err)
	}
	invExec(t, `UPDATE orders SET payment_status = 'paid' WHERE id = $1`, res.OrderID)
	return res.OrderID
}

// storedOrder is what the order row holds, read straight from the tables.
type storedOrder struct {
	subtotal, discount, coupon, shipping, tax, final int64
	taxable, cgst, sgst, igst                        int64
	interstate                                       bool
	lines                                            []storedLine
}

type storedLine struct {
	sku, hsn                                         string
	qty                                              int
	unit, disc, ship, taxable, cgst, sgst, igst, net int64
	rate                                             int
}

func readStoredOrder(t *testing.T, orderID uuid.UUID) storedOrder {
	t.Helper()
	ctx := context.Background()
	var o storedOrder
	if err := svcTestPool.QueryRow(ctx, `
		SELECT subtotal_minor, discount_amount_minor, coupon_discount_minor, shipping_charges_minor,
		       tax_amount_minor, final_amount_minor, taxable_minor, cgst_minor, sgst_minor, igst_minor, is_interstate
		  FROM orders WHERE id = $1`, orderID).Scan(&o.subtotal, &o.discount, &o.coupon, &o.shipping,
		&o.tax, &o.final, &o.taxable, &o.cgst, &o.sgst, &o.igst, &o.interstate); err != nil {
		t.Fatalf("read order: %v", err)
	}
	rows, err := svcTestPool.Query(ctx, `
		SELECT sku, COALESCE(hsn_code,''), quantity, unit_price_minor, allocated_discount_minor,
		       allocated_shipping_minor, taxable_minor, cgst_minor, sgst_minor, igst_minor,
		       final_price_minor, tax_rate_bp
		  FROM order_items WHERE order_id = $1 ORDER BY sku`, orderID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var l storedLine
		if err := rows.Scan(&l.sku, &l.hsn, &l.qty, &l.unit, &l.disc, &l.ship, &l.taxable,
			&l.cgst, &l.sgst, &l.igst, &l.net, &l.rate); err != nil {
			t.Fatal(err)
		}
		o.lines = append(o.lines, l)
	}
	return o
}

// rupeeCell formats paise as the invoice prints them, computed in integers
// so the expectation cannot share a float rounding error with the code.
func rupeeCell(p int64) string {
	sign := ""
	if p < 0 {
		sign, p = "-", -p
	}
	return fmt.Sprintf("%s₹%d.%02d", sign, p/100, p%100)
}

func pctCell(bp int) string { return fmt.Sprintf("%d.%02d%%", bp/100, bp%100) }

var (
	trRe  = regexp.MustCompile(`(?s)<tr[^>]*>(.*?)</tr>`)
	tdRe  = regexp.MustCompile(`(?s)<td[^>]*>(.*?)</td>`)
	tagRe = regexp.MustCompile(`<[^>]+>`)
)

// cells returns each <td>'s text, tags stripped, whitespace trimmed.
func cells(rowHTML string) []string {
	var out []string
	for _, m := range tdRe.FindAllStringSubmatch(rowHTML, -1) {
		out = append(out, strings.TrimSpace(tagRe.ReplaceAllString(m[1], "")))
	}
	return out
}

// An order whose stored money does not reconcile is refused: no invoice
// row, nothing uploaded, and no invoice number consumed.
func TestInvoiceCarriesNothingForAnUnreconciledOrder(t *testing.T) {
	ctx := context.Background()
	st := postgres.New(svcTestPool)
	orderID := placeP0Order(t, st, invDest{"Bengaluru", "KA", "560002"})
	invExec(t, `UPDATE orders SET taxable_minor = taxable_minor + 1 WHERE id = $1`, orderID)

	seq := func() int64 {
		var n int64
		_ = svcTestPool.QueryRow(ctx, `SELECT COALESCE(MAX(last_sequence),0) FROM invoice_sequences`).Scan(&n)
		return n
	}
	before := seq()
	blob := &capturedBlob{}
	_, err := (&Service{store: st}).WithBlob(blob).IssueInvoice(ctx, orderID)
	if !errors.Is(err, errInvoiceMoneyUnreconciled) {
		t.Fatalf("issue = %v, want errInvoiceMoneyUnreconciled", err)
	}
	if _, err := st.GetInvoiceByOrder(ctx, orderID); err == nil {
		t.Fatal("an invoice row was written for a refused order")
	}
	if len(blob.objs) != 0 {
		t.Fatalf("documents were uploaded for a refused order: %v", blob.objs)
	}
	if after := seq(); after != before {
		t.Fatalf("invoice sequence moved %d → %d on a refused order", before, after)
	}
}

// An order with no stored split (the RFQ conversion writes rupee columns
// through CreateOrder) still gets the legacy invoice, unchanged.
func TestInvoiceCarriesTheLegacyPathForAnOrderWithoutAStoredSplit(t *testing.T) {
	ctx := context.Background()
	st := postgres.New(svcTestPool)
	sellerID := seedSeller(t, "inv-legacy")
	productID, variantID, sku := invProduct(t, sellerID, "Legacy Item", 90000, "18", "6205")
	method := "upi"
	order := &postgres.Order{
		CustomerUserID: uuid.New(), Subtotal: 900, FinalAmount: 900, CurrencyCode: "INR",
		PaymentMethod: &method, PaymentStatus: "paid", Status: "confirmed",
	}
	items := []*postgres.OrderItem{{
		ProductID: productID, VariantID: variantID, SellerID: sellerID, ProductTitle: "Legacy Item",
		SKU: sku, Quantity: 1, UnitMRP: 900, UnitPrice: 900, FinalPrice: 900, Status: "confirmed",
	}}
	if err := st.CreateOrder(ctx, order, items); err != nil {
		t.Fatalf("legacy order: %v", err)
	}
	blob := &capturedBlob{}
	rec, err := (&Service{store: st}).WithBlob(blob).IssueInvoice(ctx, order.ID)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	// 18% added on top of ₹900, as the legacy path always computed.
	if rec.GrandTotal != 1062 {
		t.Fatalf("legacy grand total %v, want 1062", rec.GrandTotal)
	}
	for k, v := range blob.objs {
		if strings.HasSuffix(k, ".html") && !strings.Contains(string(v), "Subtotal (Taxable)") {
			t.Fatal("the legacy order was rendered with the tax-inclusive layout")
		}
	}
}

func TestInvoiceCarriesTheOrdersStoredPaise(t *testing.T) {
	for _, tc := range []struct {
		name       string
		dest       invDest
		interstate bool
	}{
		{"intra-state CGST+SGST", invDest{"Bengaluru", "KA", "560002"}, false},
		{"inter-state IGST", invDest{"Mumbai", "MH", "400001"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st := postgres.New(svcTestPool)
			orderID := placeP0Order(t, st, tc.dest)
			want := readStoredOrder(t, orderID)

			// The fixture has to be the hard case, or the test proves less
			// than it claims.
			if want.interstate != tc.interstate {
				t.Fatalf("order is_interstate = %v, want %v", want.interstate, tc.interstate)
			}
			if len(want.lines) != 2 || want.lines[0].rate == want.lines[1].rate {
				t.Fatalf("want two lines on different slabs, got %+v", want.lines)
			}
			discounted := 0
			for _, l := range want.lines {
				if l.disc > 0 {
					discounted++
				}
				if l.ship <= 0 {
					t.Fatalf("line %s carries no share of delivery: %+v", l.sku, l)
				}
			}
			if discounted != 1 || want.coupon <= 0 {
				t.Fatalf("want the seller coupon on exactly one line, got %d discounted lines (coupon %d)", discounted, want.coupon)
			}
			if want.final <= 0 {
				t.Fatalf("the order charged %d", want.final)
			}

			blob := &capturedBlob{}
			svc := (&Service{store: st}).WithBlob(blob)
			rec, err := svc.IssueInvoice(ctx, orderID)
			if err != nil {
				t.Fatalf("issue invoice: %v", err)
			}

			// ── The persisted record ──────────────────────────────────
			toMinor := func(r float64) int64 { return int64(math.Round(r * 100)) }
			if toMinor(rec.GrandTotal) == 0 {
				t.Fatalf("invoice %s was issued with a ₹0 grand total on an order that charged %s",
					rec.InvoiceNumber, rupeeCell(want.final))
			}
			if got := toMinor(rec.GrandTotal); got != want.final {
				t.Fatalf("invoice grand total %d, order final_amount_minor %d", got, want.final)
			}
			if toMinor(rec.CGSTTotal) != want.cgst || toMinor(rec.SGSTTotal) != want.sgst || toMinor(rec.IGSTTotal) != want.igst {
				t.Fatalf("invoice CGST/SGST/IGST %v/%v/%v, order stored %d/%d/%d",
					rec.CGSTTotal, rec.SGSTTotal, rec.IGSTTotal, want.cgst, want.sgst, want.igst)
			}
			if rec.IsInterstate != want.interstate {
				t.Fatalf("invoice is_interstate %v, order %v", rec.IsInterstate, want.interstate)
			}
			stored, err := st.GetInvoiceByOrder(ctx, orderID)
			if err != nil {
				t.Fatalf("read the invoice back: %v", err)
			}
			if toMinor(stored.GrandTotal) != want.final {
				t.Fatalf("stored invoice grand_total %v, order %d", stored.GrandTotal, want.final)
			}

			// ── The document the buyer downloads ─────────────────────
			var doc string
			for k, v := range blob.objs {
				if strings.HasSuffix(k, ".html") {
					doc = string(v)
				}
			}
			if doc == "" {
				t.Fatalf("no HTML invoice was uploaded (keys %v)", blob.objs)
			}

			var lineNetSum int64
			for _, l := range want.lines {
				var row []string
				for _, m := range trRe.FindAllStringSubmatch(doc, -1) {
					if strings.Contains(m[1], l.sku) {
						row = cells(m[1])
					}
				}
				if row == nil {
					t.Fatalf("line %s is not on the invoice", l.sku)
				}
				// #, item, HSN, qty, rate, discount, delivery, taxable, GST…, total
				wantCells := []string{l.hsn, fmt.Sprint(l.qty), rupeeCell(l.unit), rupeeCell(l.disc),
					rupeeCell(l.ship), rupeeCell(l.taxable)}
				if want.interstate {
					wantCells = append(wantCells, pctCell(l.rate)+rupeeCell(l.igst))
				} else {
					wantCells = append(wantCells, pctCell(l.rate/2)+rupeeCell(l.cgst),
						pctCell(l.rate-l.rate/2)+rupeeCell(l.sgst))
				}
				wantCells = append(wantCells, rupeeCell(l.net))
				if len(row) != 2+len(wantCells) {
					t.Fatalf("line %s renders %d cells %q, want %d", l.sku, len(row), row, 2+len(wantCells))
				}
				for i, w := range wantCells {
					if row[2+i] != w {
						t.Errorf("line %s cell %d = %q, want %q (row %q)", l.sku, 2+i, row[2+i], w, row)
					}
				}
				lineNetSum += l.net
			}
			if lineNetSum != want.final {
				t.Fatalf("stored lines sum to %d, order final %d", lineNetSum, want.final)
			}

			totals := map[string]string{}
			if i := strings.Index(doc, `class="totals"`); i >= 0 {
				end := strings.Index(doc[i:], "</table>")
				for _, m := range trRe.FindAllStringSubmatch(doc[i:i+end], -1) {
					c := cells(m[1])
					if len(c) == 2 {
						totals[c[0]] = c[1]
					}
				}
			}
			wantTotals := map[string]string{
				"Items (incl. GST)":    rupeeCell(want.subtotal),
				"Delivery (incl. GST)": rupeeCell(want.shipping),
				"Grand Total":          rupeeCell(want.final),
				"Taxable value":        rupeeCell(want.taxable),
			}
			var couponLabel string
			for k := range totals {
				if strings.HasPrefix(k, "Discount") {
					couponLabel = k
				}
			}
			if couponLabel == "" {
				t.Errorf("the coupon discount is not in the totals: %q", totals)
			} else if got := totals[couponLabel]; got != "−"+rupeeCell(want.coupon+want.discount) {
				t.Errorf("totals %q = %q, want −%s", couponLabel, got, rupeeCell(want.coupon+want.discount))
			}
			if want.interstate {
				wantTotals["IGST"] = rupeeCell(want.igst)
			} else {
				wantTotals["CGST"] = rupeeCell(want.cgst)
				wantTotals["SGST"] = rupeeCell(want.sgst)
			}
			for label, w := range wantTotals {
				if got := totals[label]; got != w {
					t.Errorf("totals %q = %q, want %q (all totals %q)", label, got, w, totals)
				}
			}
			if want.cgst+want.sgst+want.igst != want.tax {
				t.Fatalf("stored split %d+%d+%d != stored tax %d", want.cgst, want.sgst, want.igst, want.tax)
			}
		})
	}
}
