package postgres

// The read-model contract: the names and the numbers a client actually gets.
//
// Every defect proven here was the same shape — the server had the right
// value in the database and put a different one, or a differently-named one,
// on the wire. None of them is a logic error, and none of them was visible
// from inside the service; they were only visible on a screen.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
	// constraint, read out of the migration that last replaced it (045), so
	// the two cannot drift by someone editing only one of them.
	want := documentTypeCheckFromMigration(t, "../../../database/migrations/045_books_only_sellers.sql")
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

// documentTypeCheckFromMigration returns the quoted values of the
// `CHECK (document_type IN (...))` in a migration, in order.
func documentTypeCheckFromMigration(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)CHECK \(document_type IN \((.*?)\)\)`).FindSubmatch(raw)
	if m == nil {
		t.Fatalf("%s has no CHECK (document_type IN (...))", path)
	}
	var out []string
	for _, q := range regexp.MustCompile(`'([a-z_]+)'`).FindAllSubmatch(m[1], -1) {
		out = append(out, string(q[1]))
	}
	if len(out) == 0 || strings.Contains(string(m[1]), "--") {
		t.Fatalf("could not read the document_type vocabulary out of %s", path)
	}
	return out
}

// approvalCheckLists returns the quoted values of every
// `ADD CONSTRAINT products_approval_status_check ... CHECK (approval_status
// IN (...))` in a SQL file, in file order.
func approvalCheckLists(t *testing.T, path string) [][]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out [][]string
	for _, m := range regexp.MustCompile(`(?s)ADD CONSTRAINT products_approval_status_check[^\n]*\n\s*CHECK \(approval_status IN \((.*?)\)\)`).FindAllSubmatch(raw, -1) {
		var vals []string
		for _, q := range regexp.MustCompile(`'([a-z_]+)'`).FindAllSubmatch(m[1], -1) {
			vals = append(vals, string(q[1]))
		}
		out = append(out, vals)
	}
	return out
}

// The approval vocabulary is owned by the migrations: 046 installs the full
// list on a database that lacks it (with 'live' only while the old image may
// still write it), and gated 1001 contracts it. Both 046 branches must agree
// with 1001, and the list must carry every state the service writes.
func TestApprovalVocabularyIsOwnedByTheMigrations(t *testing.T) {
	gated := approvalCheckLists(t, "../../../database/gated/1001_approval_status_tighten.sql")
	if len(gated) != 1 {
		t.Fatalf("gated 1001 restates the CHECK %d times, want 1", len(gated))
	}
	contracted := strings.Join(gated[0], ",")
	fresh := approvalCheckLists(t, "../../../database/migrations/046_approval_status_fresh_database.sql")
	if len(fresh) != 2 {
		t.Fatalf("046 has %d CHECK lists, want 2 (with and without 'live')", len(fresh))
	}
	var withoutLive []string
	for _, v := range fresh[0] {
		if v != "live" {
			withoutLive = append(withoutLive, v)
		}
	}
	if len(withoutLive) != len(fresh[0])-1 {
		t.Fatalf("046's first list %v must permit 'live' exactly once (the old image still writes it)", fresh[0])
	}
	if got := strings.Join(withoutLive, ","); got != contracted {
		t.Fatalf("046's list minus 'live' is %s, gated 1001 contracts to %s", got, contracted)
	}
	if got := strings.Join(fresh[1], ","); got != contracted {
		t.Fatalf("046's no-'live' list is %s, gated 1001's is %s", got, contracted)
	}
	allowed := map[string]bool{}
	for _, v := range gated[0] {
		allowed[v] = true
	}
	for _, st := range []string{"draft", "pending", "rejected", "changes_requested", "flagged", "approved", "submitted", "under_review", "hidden", "archived"} {
		if !allowed[st] {
			t.Errorf("approval_status %q is written by the service but refused once 1001 has run", st)
		}
	}
}

// sqlWithoutComments drops `--` comments so prose about a DROP is not a DROP.
func sqlWithoutComments(raw []byte) string {
	return regexp.MustCompile(`--[^\n]*`).ReplaceAllString(string(raw), "")
}

// setup.sql runs on EVERY boot, before the migrations, against a database
// that may already be migrated past it — by the gated contract files too,
// which no boot re-applies. Anything in it that drops, restates or replaces
// an object a migration owns therefore undoes that migration on every
// restart. That is how gated 1001 was reverted: setup.sql dropped and
// re-added products_approval_status_check with 'live' in it.
//
// So setup.sql may create what is absent and add columns that are absent,
// and nothing else: no DROP of any kind, no change to a column's type,
// default or nullability, no ADD CONSTRAINT that is not guarded by a
// pg_constraint existence check, and no CREATE OR REPLACE of a function a
// migration or gated file also defines.
func TestSetupSQLNeverRestatesAConstraint(t *testing.T) {
	raw, err := os.ReadFile("../../../database/setup.sql")
	if err != nil {
		t.Fatal(err)
	}
	setup := sqlWithoutComments(raw)
	for _, bad := range []struct{ re, why string }{
		{`(?i)\bDROP\s+(CONSTRAINT|TRIGGER|INDEX|FUNCTION|TABLE|COLUMN|VIEW|NOT\s+NULL|DEFAULT)\b`, "a DROP undoes whatever a migration made of the object"},
		{`(?i)\bALTER\s+COLUMN\s+\w+\s+(TYPE|SET\s+DATA|SET\s+DEFAULT|SET\s+NOT\s+NULL)\b`, "a column change re-applied at boot overrides a later migration of that column"},
	} {
		if m := regexp.MustCompile(bad.re).FindString(setup); m != "" {
			t.Errorf("setup.sql contains %q: %s. Put the change in a migration.", m, bad.why)
		}
	}
	for _, m := range regexp.MustCompile(`(?i)\bADD\s+CONSTRAINT\s+(\w+)`).FindAllStringSubmatch(setup, -1) {
		if !strings.Contains(setup, "conname = '"+m[1]+"'") {
			t.Errorf("setup.sql adds constraint %s without an IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = '%s') guard; "+
				"re-adding it at boot restates it over any migration of it", m[1], m[1])
		}
	}
	later := map[string]string{}
	for _, dir := range []string{"../../../database/migrations", "../../../database/gated"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no SQL in %s: %v", dir, err)
		}
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range regexp.MustCompile(`(?i)CREATE\s+OR\s+REPLACE\s+FUNCTION\s+(\w+)`).FindAllStringSubmatch(sqlWithoutComments(b), -1) {
				later[strings.ToLower(m[1])] = filepath.Base(f)
			}
		}
	}
	for _, m := range regexp.MustCompile(`(?i)CREATE\s+OR\s+REPLACE\s+FUNCTION\s+(\w+)`).FindAllStringSubmatch(setup, -1) {
		if f, ok := later[strings.ToLower(m[1])]; ok {
			t.Errorf("setup.sql replaces function %s, which %s also defines; every boot would put setup.sql's body back", m[1], f)
		}
	}
}
