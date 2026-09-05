package postgres

// The read-model contract: the names and the numbers a client actually gets.
//
// Every defect proven here was the same shape — the server had the right
// value in the database and put a different one, or a differently-named one,
// on the wire. None of them is a logic error, and none of them was visible
// from inside the service; they were only visible on a screen.

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// A product's store name must go out under BOTH wire names.
//
// The body sent `retailer_name`; the Android ProductDto reads `seller_name`.
// The store name was therefore simply absent from product detail — not
// wrong, absent — and no test noticed because the field the server sent was
// populated correctly.
func TestProductSendsTheStoreNameUnderBothWireNames(t *testing.T) {
	name := "E2E Merged Store"
	p := Product{ID: uuid.New(), Title: "Widget", RetailerName: &name}

	var got map[string]any
	raw, err := json.Marshal(&p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got["seller_name"] != name {
		t.Errorf("seller_name = %v, want %q — this is the name the app reads, and "+
			"the product page showed no store at all without it", got["seller_name"], name)
	}
	if got["retailer_name"] != name {
		t.Errorf("retailer_name = %v, want %q — the alias the existing web caller reads "+
			"must keep working", got["retailer_name"], name)
	}
}

// A product with no store name emits neither key, rather than two nulls.
func TestProductWithoutAStoreNameOmitsBothNames(t *testing.T) {
	raw, err := json.Marshal(&Product{ID: uuid.New()})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"seller_name", "retailer_name"} {
		if _, present := got[k]; present {
			t.Errorf("%s is present on a product with no store name", k)
		}
	}
}

// A variant carries paise and stock on the wire.
//
// The struct only ever had rupee floats, so product detail rendered every
// variant at ₹0.00 and out of stock while the catalogue grid — which had
// already moved to `min_price_minor` — showed the right price.
func TestVariantCarriesPaiseAndAvailableStock(t *testing.T) {
	sell, mrp, avail := int64(88000), int64(100000), 74
	v := ProductVariant{
		ID: uuid.New(), SKU: "E2E-MG-1",
		MRP: 1000, SellingPrice: 880,
		SellingPriceMinor: &sell, MRPMinor: &mrp, AvailableQty: &avail,
	}
	raw, err := json.Marshal(&v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for field, want := range map[string]float64{
		"selling_price_minor": 88000,
		"mrp_minor":           100000,
		"available_qty":       74,
	} {
		if got[field] != want {
			t.Errorf("%s = %v, want %v — the app reads exactly this name", field, got[field], want)
		}
	}
	// The rupee pair stays, for the existing web caller.
	if got["selling_price"] != float64(880) {
		t.Errorf("selling_price = %v, want 880 (the legacy shape must keep working)", got["selling_price"])
	}
}

// The money accessors read the minor column, and fall back to the rupee
// mirror only for a row written before migration 007.
func TestOrderMoneyPrefersPaiseAndFallsBackToRupees(t *testing.T) {
	t.Run("a P0 order reads its minor columns", func(t *testing.T) {
		o := Order{
			FinalAmount: 0, FinalAmountMinor: 92900, // the shape every P0 order has
			Subtotal: 0, SubtotalMinor: 88000,
			TaxAmount: 0, TaxAmountMinor: 14172,
		}
		if got := o.TotalMinor(); got != 92900 {
			t.Errorf("TotalMinor = %d, want 92900 — reading final_amount instead is what "+
				"made ConfirmPayment expect ₹0 and the order screen show ₹0.00", got)
		}
		if got := o.SubtotalMinorValue(); got != 88000 {
			t.Errorf("SubtotalMinorValue = %d, want 88000", got)
		}
		if got := o.TaxMinorValue(); got != 14172 {
			t.Errorf("TaxMinorValue = %d, want 14172", got)
		}
	})

	t.Run("a pre-007 order still reads its rupee columns", func(t *testing.T) {
		o := Order{FinalAmount: 929, ShippingCharges: 49}
		if got := o.TotalMinor(); got != 92900 {
			t.Errorf("TotalMinor = %d, want 92900 from the rupee mirror", got)
		}
		if got := o.ShippingMinorValue(); got != 4900 {
			t.Errorf("ShippingMinorValue = %d, want 4900", got)
		}
	})
}

// A line's total comes from final_price_minor. Summing final_price is what
// reported seller_subtotal: 0 on a ₹929 order.
func TestOrderItemLineTotalPrefersPaise(t *testing.T) {
	if got := (&OrderItem{FinalPriceMinor: 88000, FinalPrice: 0}).LineTotalMinor(); got != 88000 {
		t.Errorf("LineTotalMinor = %d, want 88000", got)
	}
	if got := (&OrderItem{FinalPrice: 880}).LineTotalMinor(); got != 88000 {
		t.Errorf("LineTotalMinor = %d, want 88000 from the rupee mirror", got)
	}
}

// can_cancel must agree with the D6 cancellation matrix migration 010
// installs — the button and the transition cannot hold different opinions.
func TestCustomerCanCancelMatchesTheCancellationMatrix(t *testing.T) {
	for _, status := range []string{"payment_pending", "payment_failed", "confirmed", "packed"} {
		if !CustomerCanCancel(status) {
			t.Errorf("CustomerCanCancel(%q) = false; the matrix permits a customer cancel here, "+
				"so the button must be drawn", status)
		}
	}
	for _, status := range []string{"shipped", "out_for_delivery", "delivered", "cancelled", "refunded"} {
		if CustomerCanCancel(status) {
			t.Errorf("CustomerCanCancel(%q) = true; only an admin may cancel from here, so "+
				"offering the button gets the customer a refusal", status)
		}
	}
}

// The KYC vocabulary the service validates against must be the one the
// schema enforces. If these drift, an accepted value 500s in Postgres.
func TestSellerDocumentTypesMatchTheSchemaVocabulary(t *testing.T) {
	// Exactly the list in the seller_documents.document_type CHECK
	// constraint (migration 001).
	want := []string{
		"gst_certificate", "pan_card", "aadhaar", "passport",
		"business_registration", "address_proof", "cancelled_cheque", "other",
	}
	if len(SellerDocumentTypes) != len(want) {
		t.Fatalf("SellerDocumentTypes has %d entries, the CHECK constraint has %d",
			len(SellerDocumentTypes), len(want))
	}
	for i, w := range want {
		if SellerDocumentTypes[i] != w {
			t.Errorf("SellerDocumentTypes[%d] = %q, want %q", i, SellerDocumentTypes[i], w)
		}
		if !ValidDocumentType(w) {
			t.Errorf("ValidDocumentType(%q) = false", w)
		}
	}
	if ValidDocumentType("drivers_license") {
		t.Error("ValidDocumentType accepted a type the CHECK constraint refuses; " +
			"it would reach Postgres and come back as a 500")
	}
}
