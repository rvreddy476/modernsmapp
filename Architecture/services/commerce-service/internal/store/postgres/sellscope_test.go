package postgres

// Migration 045: readiness per sell scope, and the document rules the
// readiness query and the service share. The SQL itself is proven in
// sellscope_integration_test.go.

import (
	"reflect"
	"testing"
)

// Everything a shop needs whatever it sells.
func baseReady(scope string) SellerReadiness {
	return SellerReadiness{
		SellScope: scope, HasStoreName: true, HasEmail: true, HasPickupAddress: true,
		HasPayoutAccount: true, HasKYCDocument: true,
	}
}

func TestAllCategoriesNeedsAGSTIN(t *testing.T) {
	r := baseReady(SellScopeAllCategories)
	if r.Complete() {
		t.Fatal("an all-categories shop with no GSTIN is complete; founder 24 Oct: every category but books needs one")
	}
	if got := r.Missing(); !reflect.DeepEqual(got, []string{"gstin"}) {
		t.Fatalf("missing = %v, want [gstin]", got)
	}
	r.HasGSTIN = true
	if !r.Complete() {
		t.Fatalf("all-categories shop with a GSTIN is incomplete: %v", r.Missing())
	}
}

// The books path asks for no GSTIN, and for four things instead.
func TestBooksOnlyNeedsPANBusinessNameAddressAndProofButNoGSTIN(t *testing.T) {
	r := baseReady(SellScopeBooksOnly)
	if got, want := r.Missing(), []string{"pan", "business_name", "business_address", "address_proof"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("missing = %v, want %v", got, want)
	}
	r.HasPAN, r.HasBusinessName, r.HasBusinessAddress, r.HasAddressProof = true, true, true, true
	if !r.Complete() {
		t.Fatalf("a books-only shop with PAN, name, address and proof is incomplete: %v — "+
			"a GSTIN must NOT be required on this path", r.Missing())
	}
	// Each of the four on its own blocks the submit.
	for _, drop := range []func(*SellerReadiness){
		func(r *SellerReadiness) { r.HasPAN = false },
		func(r *SellerReadiness) { r.HasBusinessName = false },
		func(r *SellerReadiness) { r.HasBusinessAddress = false },
		func(r *SellerReadiness) { r.HasAddressProof = false },
	} {
		c := r
		drop(&c)
		if c.Complete() {
			t.Fatalf("books-only readiness complete with a requirement missing: %+v", c)
		}
	}
}

// The base requirements still hold on the books path.
func TestBooksOnlyStillNeedsTheBaseRequirements(t *testing.T) {
	r := SellerReadiness{SellScope: SellScopeBooksOnly, HasPAN: true, HasBusinessName: true,
		HasBusinessAddress: true, HasAddressProof: true}
	want := []string{"store_name", "email", "pickup_address", "payout_account", "kyc_document"}
	if got := r.Missing(); !reflect.DeepEqual(got, want) {
		t.Fatalf("missing = %v, want %v", got, want)
	}
}

// A scope with no rule is not "nothing further required".
func TestAnUnknownScopeIsNeverComplete(t *testing.T) {
	for _, scope := range []string{"", "everything", "BOOKS_ONLY"} {
		r := baseReady(scope)
		r.HasGSTIN, r.HasPAN, r.HasBusinessName, r.HasBusinessAddress, r.HasAddressProof = true, true, true, true, true
		if r.Complete() {
			t.Fatalf("scope %q with every fact true is complete", scope)
		}
		if got := r.Missing(); len(got) == 0 || got[0] != "sell_scope" {
			t.Fatalf("scope %q: missing = %v, want sell_scope first", scope, got)
		}
	}
}

// The Flipkart "Only Books" list, and which of them need a back.
func TestTheProofOfAddressVocabulary(t *testing.T) {
	for _, typ := range []string{"voter_id", "passport", "electricity_bill", "telephone_bill", "bank_statement"} {
		if !IsAddressProofType(typ) {
			t.Errorf("%s is not accepted as proof of address", typ)
		}
		if !ValidDocumentType(typ) {
			t.Errorf("%s is a proof of address the schema refuses", typ)
		}
	}
	for _, typ := range []string{"pan_card", "aadhaar", "gst_certificate", "cancelled_cheque", "other", "address_proof"} {
		if IsAddressProofType(typ) {
			t.Errorf("%s counts as proof of address; it is not on the list", typ)
		}
	}
	for _, typ := range []string{"voter_id", "passport"} {
		if !IsTwoSidedType(typ) {
			t.Errorf("%s does not need its back; the address is on the back", typ)
		}
	}
	for _, typ := range []string{"electricity_bill", "telephone_bill", "bank_statement", "pan_card"} {
		if IsTwoSidedType(typ) {
			t.Errorf("%s asks for a back it does not have", typ)
		}
	}
}

func TestValidSellScope(t *testing.T) {
	for _, s := range []string{SellScopeAllCategories, SellScopeBooksOnly} {
		if !ValidSellScope(s) {
			t.Errorf("%s refused", s)
		}
	}
	for _, s := range []string{"", "books", "all"} {
		if ValidSellScope(s) {
			t.Errorf("%q accepted", s)
		}
	}
	if sellScopeOrDefault("") != SellScopeAllCategories || sellScopeOrDefault(SellScopeBooksOnly) != SellScopeBooksOnly {
		t.Fatal("sellScopeOrDefault")
	}
}
