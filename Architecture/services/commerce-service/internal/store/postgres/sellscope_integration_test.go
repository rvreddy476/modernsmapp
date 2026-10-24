//go:build integration

package postgres

// Migration 045 — the books-only seller path, against a live PostgreSQL.
//
//	COMMERCE_TEST_DSN=.../commerce_it_test go test -tags=integration ./internal/store/postgres/ -run SellScope -v

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// newScopeSeller makes a draft shop and returns (seller id, user id).
func newScopeSeller(t *testing.T, status string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	id, user := uuid.New(), uuid.New()
	mustExec(t, `INSERT INTO sellers (id,user_id,store_name,slug,email,status)
	             VALUES ($1,$2,'Scope Store',$3,'scope@example.test',$4)`,
		id, user, "scope-"+id.String()[:8], status)
	return id, user
}

func sealedPAN(tag string, plaintext bool) SealedIdentifierWrite {
	return SealedIdentifierWrite{Supplied: true, Plain: "ABCPE1234F", Enc: []byte("enc:" + tag),
		KeyVersion: 1, Hash: "hash:" + tag, Masked: "XXXXXX234F", WritePlaintext: plaintext}
}

func TestSellScopeDefaultsToAllCategoriesForEveryExistingShop(t *testing.T) {
	sellerID := newSeller(t)
	var scope string
	if err := testPool.QueryRow(context.Background(),
		`SELECT sell_scope FROM sellers WHERE id = $1`, sellerID).Scan(&scope); err != nil {
		t.Fatal(err)
	}
	if scope != SellScopeAllCategories {
		t.Fatalf("a shop created without a scope is %q; every existing shop must keep selling everything", scope)
	}
}

func TestSellScopeBooksOnlyStoresTheSealedPANAndClearsTheGSTIN(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	_, user := newScopeSeller(t, "draft")
	g := "29AAACM1234A1Z4"
	if err := store.SaveOnboardingSellScope(ctx, user, SellScopeWrite{Scope: SellScopeAllCategories, GSTIN: &g}); err != nil {
		t.Fatal(err)
	}
	name := "Asha Books"
	if err := store.SaveOnboardingSellScope(ctx, user, SellScopeWrite{
		Scope: SellScopeBooksOnly, LegalBusinessName: &name, PAN: sealedPAN("books", false),
	}); err != nil {
		t.Fatal(err)
	}
	sel, err := store.GetSellerByUserID(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if sel.SellScope != SellScopeBooksOnly || sel.GSTNumber != nil {
		t.Fatalf("scope %q gstin %v; switching to books-only must clear the GSTIN", sel.SellScope, sel.GSTNumber)
	}
	if string(sel.PANEnc) != "enc:books" || sel.PANNumber != nil || sel.PANMasked == nil || *sel.PANMasked != "XXXXXX234F" {
		t.Fatalf("PAN stored as enc=%q plain=%v masked=%v; ciphertext mode keeps no plaintext", sel.PANEnc, sel.PANNumber, sel.PANMasked)
	}
	if sel.LegalBusinessName == nil || *sel.LegalBusinessName != "Asha Books" {
		t.Fatalf("business name = %v", sel.LegalBusinessName)
	}
}

func TestSellScopeIsLockedOnceTheApplicationLeavesDraft(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	g := "29AAACM1234A1Z4"
	for _, status := range []string{"submitted", "under_review", "approved", "rejected", "suspended"} {
		_, user := newScopeSeller(t, status)
		err := store.SaveOnboardingSellScope(ctx, user, SellScopeWrite{Scope: SellScopeAllCategories, GSTIN: &g})
		if !errors.Is(err, ErrSellScopeLocked) {
			t.Errorf("status %s: err = %v, want ErrSellScopeLocked", status, err)
		}
	}
	_, user := newScopeSeller(t, "changes_required")
	if err := store.SaveOnboardingSellScope(ctx, user, SellScopeWrite{Scope: SellScopeAllCategories, GSTIN: &g}); err != nil {
		t.Fatalf("changes_required may change scope: %v", err)
	}
	if err := store.SaveOnboardingSellScope(ctx, uuid.New(), SellScopeWrite{Scope: SellScopeAllCategories, GSTIN: &g}); !errors.Is(err, ErrNoSellerRow) {
		t.Fatalf("no row: err = %v", err)
	}
}

func TestSellScopeUpgradeOnlyFromBooksOnly(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	sellerID, _ := newScopeSeller(t, "approved")
	mustExec(t, `UPDATE sellers SET sell_scope = 'books_only' WHERE id = $1`, sellerID)
	if err := store.UpgradeSellScopeToAllCategories(ctx, sellerID, "27ABCPE1234F1ZB"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpgradeSellScopeToAllCategories(ctx, sellerID, "27ABCPE1234F1ZB"); !errors.Is(err, ErrSellScopeNotBooksOnly) {
		t.Fatalf("second upgrade: err = %v, want ErrSellScopeNotBooksOnly", err)
	}
}

func TestSellScopeBookCategoriesAreTextbooksAndBeneathButNotStationery(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	textbooks := uuid.MustParse("00000000-0000-4000-8000-00000000c101")
	books := uuid.MustParse("00000000-0000-4000-8000-00000000c008")
	electronics := uuid.MustParse("00000000-0000-4000-8000-00000000c001")
	child := uuid.New()
	mustExec(t, `INSERT INTO product_categories (id,parent_id,name,slug,is_active,is_listable)
	             VALUES ($1,$2,'Engineering',$3,TRUE,TRUE)`, child, textbooks, "books-eng-"+child.String()[:8])
	sibling := uuid.New()
	mustExec(t, `INSERT INTO product_categories (id,parent_id,name,slug,is_active,is_listable)
	             VALUES ($1,$2,'Notebooks',$3,TRUE,TRUE)`, sibling, books, "stationery-"+sibling.String()[:8])

	for id, want := range map[uuid.UUID]bool{textbooks: true, child: true, books: false, sibling: false, electronics: false, uuid.New(): false} {
		got, err := store.CategoryIsBookCategory(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("CategoryIsBookCategory(%s) = %v, want %v", id, got, want)
		}
	}
	ids, err := store.BookCategoryIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[uuid.UUID]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	if !seen[textbooks] || !seen[child] || seen[books] || seen[sibling] {
		t.Fatalf("book categories = %v", ids)
	}
}

func TestSellScopeOnlyGSTZeroIsZeroRated(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	rows, err := testPool.Query(ctx, `SELECT id, name FROM tax_classes`)
	if err != nil {
		t.Fatal(err)
	}
	classes := map[string]uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		var name string
		_ = rows.Scan(&id, &name)
		classes[name] = id
	}
	rows.Close()
	for name, want := range map[string]bool{"GST 0%": true, "GST 5%": false, "GST 18%": false} {
		id, ok := classes[name]
		if !ok {
			t.Fatalf("tax class %s missing", name)
		}
		got, err := store.TaxClassIsZeroRated(ctx, id)
		if err != nil || got != want {
			t.Errorf("TaxClassIsZeroRated(%s) = %v, %v; want %v", name, got, err, want)
		}
	}
	if got, _ := store.TaxClassIsZeroRated(ctx, uuid.New()); got {
		t.Fatal("an unknown tax class is zero-rated")
	}
}

// Readiness per scope, from the rows.
func TestSellScopeReadinessFromTheRows(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	sellerID, user := newScopeSeller(t, "draft")
	name := "Asha Books"
	if err := store.SaveOnboardingSellScope(ctx, user, SellScopeWrite{
		Scope: SellScopeBooksOnly, LegalBusinessName: &name, PAN: sealedPAN("ready", false),
	}); err != nil {
		t.Fatal(err)
	}
	r, err := store.SellerReadinessFor(ctx, sellerID)
	if err != nil {
		t.Fatal(err)
	}
	if r.SellScope != SellScopeBooksOnly || !r.HasPAN || !r.HasBusinessName || r.HasBusinessAddress || r.HasAddressProof || r.HasGSTIN {
		t.Fatalf("readiness = %+v", r)
	}

	// A business address is not a pickup address.
	mustExec(t, `INSERT INTO seller_addresses (seller_id,address_type,contact_name,phone,address_line_1,city,state,postal_code)
	             VALUES ($1,'business','Asha','9000000000','2 Book Lane','Pune','MH','411001')`, sellerID)
	// A voter ID without its back is not yet a proof of address.
	mustExec(t, `INSERT INTO seller_documents (seller_id,document_type,media_id) VALUES ($1,'voter_id',gen_random_uuid())`, sellerID)
	r, _ = store.SellerReadinessFor(ctx, sellerID)
	if !r.HasBusinessAddress || r.HasPickupAddress {
		t.Fatalf("business address: has_business_address=%v has_pickup_address=%v", r.HasBusinessAddress, r.HasPickupAddress)
	}
	if r.HasAddressProof {
		t.Fatal("a voter ID with no back counts as proof of address")
	}
	mustExec(t, `UPDATE seller_documents SET back_media_id = gen_random_uuid() WHERE seller_id = $1`, sellerID)
	r, _ = store.SellerReadinessFor(ctx, sellerID)
	if !r.HasAddressProof || !r.HasKYCDocument {
		t.Fatalf("voter ID front and back: proof=%v kyc=%v", r.HasAddressProof, r.HasKYCDocument)
	}

	// A bill needs no back.
	other, user2 := newScopeSeller(t, "draft")
	_ = user2
	mustExec(t, `INSERT INTO seller_documents (seller_id,document_type,media_id) VALUES ($1,'electricity_bill',gen_random_uuid())`, other)
	r, _ = store.SellerReadinessFor(ctx, other)
	if !r.HasAddressProof {
		t.Fatal("an electricity bill is not counted as proof of address")
	}
	// A PAN card is not proof of address.
	third, _ := newScopeSeller(t, "draft")
	mustExec(t, `INSERT INTO seller_documents (seller_id,document_type,media_id) VALUES ($1,'pan_card',gen_random_uuid())`, third)
	if r, _ = store.SellerReadinessFor(ctx, third); r.HasAddressProof {
		t.Fatal("a PAN card counted as proof of address")
	}
}

// The document vocabulary the schema accepts is the widened one, and a back
// side round-trips.
func TestSellScopeNewDocumentTypesAndTheBackSide(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	sellerID, _ := newScopeSeller(t, "draft")
	back := uuid.New()
	docs := []SellerDocument{
		{DocumentType: "voter_id", MediaID: uuid.New(), BackMediaID: &back},
		{DocumentType: "electricity_bill", MediaID: uuid.New()},
		{DocumentType: "telephone_bill", MediaID: uuid.New()},
		{DocumentType: "bank_statement", MediaID: uuid.New()},
	}
	if err := store.SaveOnboardingCompliance(ctx, sellerID, docs); err != nil {
		t.Fatalf("the widened CHECK refused a proof-of-address kind: %v", err)
	}
	list, err := store.ListSellerDocumentsForAdmin(ctx, sellerID)
	if err != nil {
		t.Fatal(err)
	}
	backs := 0
	for _, d := range list {
		if d.HasBackImage() {
			backs++
			if *d.BackMediaID != back {
				t.Fatalf("back = %v, want %v", d.BackMediaID, back)
			}
		}
	}
	if len(list) != 4 || backs != 1 {
		t.Fatalf("%d documents, %d backs", len(list), backs)
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO seller_documents (seller_id,document_type,media_id) VALUES ($1,'drivers_license',gen_random_uuid())`, sellerID); err == nil {
		t.Fatal("the CHECK accepted a type nobody added")
	}
}
