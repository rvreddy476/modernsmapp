package service

// Migration 045 — the books-only seller path: the "What will you sell?"
// step, the PAN seal, the upgrade, the listing restriction and the
// proof-of-address document rules. The store is a fake (SellScopeStore); the
// SQL is proven in store/postgres/sellscope_integration_test.go.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/atpost/commerce-service/internal/pii"
	"github.com/atpost/commerce-service/internal/store/postgres"
	sharedkyc "github.com/atpost/shared/kyc"
	"github.com/google/uuid"
)

// fakeScopeStore records what reached it. A nil seller means "no row".
type fakeScopeStore struct {
	seller      *postgres.Seller
	saveErr     error
	saved       []postgres.SellScopeWrite
	upgraded    []string
	bookCats    map[uuid.UUID]bool
	zeroClasses map[uuid.UUID]bool
	lookups     int
}

func (f *fakeScopeStore) GetSellerByUserID(context.Context, uuid.UUID) (*postgres.Seller, error) {
	if f.seller == nil {
		return nil, postgres.ErrNoSellerRow
	}
	return f.seller, nil
}

func (f *fakeScopeStore) SaveOnboardingSellScope(_ context.Context, _ uuid.UUID, w postgres.SellScopeWrite) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saved = append(f.saved, w)
	return nil
}

func (f *fakeScopeStore) UpgradeSellScopeToAllCategories(_ context.Context, _ uuid.UUID, gstin string) error {
	f.upgraded = append(f.upgraded, gstin)
	return nil
}

func (f *fakeScopeStore) CategoryIsBookCategory(_ context.Context, id uuid.UUID) (bool, error) {
	f.lookups++
	return f.bookCats[id], nil
}

func (f *fakeScopeStore) BookCategoryIDs(context.Context) ([]uuid.UUID, error) {
	out := []uuid.UUID{}
	for id := range f.bookCats {
		out = append(out, id)
	}
	return out, nil
}

func (f *fakeScopeStore) TaxClassIsZeroRated(_ context.Context, id uuid.UUID) (bool, error) {
	f.lookups++
	return f.zeroClasses[id], nil
}

const testPAN = "ABCPE1234F" // fourth character P: an individual

// gstinFor builds a GSTIN with a correct check character around a PAN.
func gstinFor(t *testing.T, pan string) string {
	t.Helper()
	first := "27" + pan + "1Z"
	d, err := sharedkyc.GSTINCheckDigit(first)
	if err != nil {
		t.Fatal(err)
	}
	return first + string(d)
}

func fieldOf(err error) string {
	var f *SellScopeFieldError
	if errors.As(err, &f) {
		return f.Field
	}
	return ""
}

// ─── The step's rules ────────────────────────────────────────────────

func TestTheSellScopeMustBeOneOfTheTwo(t *testing.T) {
	for _, s := range []string{"", "books", "everything"} {
		_, err := validateSellScope(SellScopeInput{SellScope: s})
		if !errors.Is(err, ErrSellScopeInvalid) || fieldOf(err) != "sell_scope" {
			t.Errorf("scope %q: err = %v, want ErrSellScopeInvalid on sell_scope", s, err)
		}
	}
	// Case and spaces are forgiven; the vocabulary is not.
	if c, err := validateSellScope(SellScopeInput{SellScope: " Books_Only ", PAN: strptr(testPAN), BusinessName: strptr("Asha Books")}); err != nil || c.scope != "books_only" {
		t.Fatalf("normalised scope = %q, %v", c.scope, err)
	}
}

func TestAllCategoriesRequiresAValidGSTIN(t *testing.T) {
	_, err := validateSellScope(SellScopeInput{SellScope: "all_categories"})
	if !errors.Is(err, ErrGSTINRequired) || fieldOf(err) != "gstin" {
		t.Fatalf("no GSTIN: err = %v", err)
	}
	g := gstinFor(t, testPAN)
	bad := g[:14] + string(rune(g[14]+1))
	if g[14] == 'Z' {
		bad = g[:14] + "0"
	}
	_, err = validateSellScope(SellScopeInput{SellScope: "all_categories", GSTIN: &bad})
	if !errors.Is(err, ErrGSTINInvalid) {
		t.Fatalf("wrong check character: err = %v, want ErrGSTINInvalid", err)
	}
	if strings.Contains(err.Error(), bad) {
		t.Fatalf("the refusal echoes the GSTIN: %v", err)
	}
	lower := strings.ToLower(g)
	c, err := validateSellScope(SellScopeInput{SellScope: "all_categories", GSTIN: &lower})
	if err != nil || c.gstin != g {
		t.Fatalf("valid GSTIN = %q, %v; want the normalised %q", c.gstin, err, g)
	}
}

// The GSTIN carries the PAN on this path; a second, sealed copy would be read
// by nothing.
func TestAllCategoriesRefusesASeparatePAN(t *testing.T) {
	g := gstinFor(t, testPAN)
	_, err := validateSellScope(SellScopeInput{SellScope: "all_categories", GSTIN: &g, PAN: strptr(testPAN)})
	if !errors.Is(err, ErrSellScopeFieldNotTaken) || fieldOf(err) != "pan" {
		t.Fatalf("err = %v, want ErrSellScopeFieldNotTaken on pan", err)
	}
}

func TestBooksOnlyRequiresAValidPANAndNoGSTIN(t *testing.T) {
	name := strptr("Asha Books")
	_, err := validateSellScope(SellScopeInput{SellScope: "books_only", BusinessName: name})
	if !errors.Is(err, ErrPANRequired) || fieldOf(err) != "pan" {
		t.Fatalf("no PAN: err = %v", err)
	}
	for _, bad := range []string{"ABCDE123", "ABCDE1234", "12345ABCDE", "ABCXE1234F" /* X is no holder type */} {
		_, err := validateSellScope(SellScopeInput{SellScope: "books_only", PAN: strptr(bad), BusinessName: name})
		if !errors.Is(err, ErrPANInvalid) {
			t.Errorf("PAN %q: err = %v, want ErrPANInvalid", bad, err)
		}
		if err != nil && strings.Contains(err.Error(), bad) {
			t.Errorf("the refusal echoes the PAN: %v", err)
		}
	}
	g := gstinFor(t, testPAN)
	_, err = validateSellScope(SellScopeInput{SellScope: "books_only", PAN: strptr(testPAN), BusinessName: name, GSTIN: &g})
	if !errors.Is(err, ErrSellScopeFieldNotTaken) || fieldOf(err) != "gstin" {
		t.Fatalf("GSTIN on the books path: err = %v, want ErrSellScopeFieldNotTaken on gstin", err)
	}
	c, err := validateSellScope(SellScopeInput{SellScope: "books_only", PAN: strptr(" abcpe1234f "), BusinessName: name})
	if err != nil || c.pan.Normalized != testPAN || c.pan.HolderType != sharedkyc.PANHolderIndividual {
		t.Fatalf("valid PAN = %+v, %v", c.pan, err)
	}
}

func TestBooksOnlyRequiresTheBusinessName(t *testing.T) {
	for _, name := range []*string{nil, strptr("   "), strptr("A"), strptr(strings.Repeat("x", 201))} {
		_, err := validateSellScope(SellScopeInput{SellScope: "books_only", PAN: strptr(testPAN), BusinessName: name})
		if !errors.Is(err, ErrBusinessNameRequired) || fieldOf(err) != "business_name" {
			t.Errorf("name %v: err = %v, want ErrBusinessNameRequired", name, err)
		}
	}
}

// ─── Saving the step ─────────────────────────────────────────────────

func TestABooksOnlyPANIsSealedAndNeverStoredInTheClearAfterCutover(t *testing.T) {
	c := kycCipher(t)
	ctx := context.Background()
	for _, mode := range []pii.Mode{pii.ModeDual, pii.ModeCiphertext} {
		st := &fakeScopeStore{}
		svc := (&Service{}).WithPII(c).WithKYCCutover(mode).WithSellScopeStore(st)
		out, err := svc.SaveSellScope(ctx, uuid.New(), SellScopeInput{
			SellScope: "books_only", PAN: strptr("abcpe1234f"), BusinessName: strptr("  Asha Books "),
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(st.saved) != 1 {
			t.Fatalf("saved %d times", len(st.saved))
		}
		w := st.saved[0]
		if w.Scope != "books_only" || w.GSTIN != nil {
			t.Fatalf("books-only write = scope %q gstin %v; a books-only shop holds no GSTIN", w.Scope, w.GSTIN)
		}
		if w.LegalBusinessName == nil || *w.LegalBusinessName != "Asha Books" {
			t.Fatalf("business name = %v", w.LegalBusinessName)
		}
		opened, err := c.OpenIdentifier(ctx, pii.ScopeKYC, w.PAN.Enc)
		if err != nil || opened != testPAN {
			t.Fatalf("sealed PAN opens to %q (%v)", opened, err)
		}
		if !w.PAN.Supplied || w.PAN.KeyVersion <= 0 || w.PAN.Hash == "" || w.PAN.Masked != pii.MaskPAN(testPAN) {
			t.Fatalf("sealed write incomplete: %+v", w.PAN)
		}
		if w.PAN.WritePlaintext != (mode == pii.ModeDual) || w.PAN.Plain != testPAN {
			t.Fatalf("mode %s: WritePlaintext = %v (plain %q)", mode, w.PAN.WritePlaintext, w.PAN.Plain)
		}
		if out.PANMasked != pii.MaskPAN(testPAN) || out.PANHolderType != "INDIVIDUAL" || strings.Contains(out.PANMasked, "ABCPE") {
			t.Fatalf("answer = %+v; the PAN must come back masked with its holder type", out)
		}
	}
}

func TestABooksOnlyPANIsRefusedWithoutACipherBeforeTheStore(t *testing.T) {
	st := &fakeScopeStore{}
	_, err := (&Service{}).WithSellScopeStore(st).SaveSellScope(context.Background(), uuid.New(), SellScopeInput{
		SellScope: "books_only", PAN: strptr(testPAN), BusinessName: strptr("Asha Books"),
	})
	if err == nil || !strings.Contains(err.Error(), "cipher") {
		t.Fatalf("err = %v, want a refusal naming the missing cipher", err)
	}
	if len(st.saved) != 0 {
		t.Fatal("a PAN reached the store with no cipher to seal it")
	}
}

func TestAllCategoriesStoresTheNormalisedGSTINAndNoPAN(t *testing.T) {
	st := &fakeScopeStore{}
	g := gstinFor(t, testPAN)
	lower := strings.ToLower(g)
	out, err := (&Service{}).WithSellScopeStore(st).SaveSellScope(context.Background(), uuid.New(),
		SellScopeInput{SellScope: "all_categories", GSTIN: &lower})
	if err != nil {
		t.Fatal(err)
	}
	w := st.saved[0]
	if w.GSTIN == nil || *w.GSTIN != g || w.PAN.Supplied || out.GSTIN == nil || *out.GSTIN != g {
		t.Fatalf("write = %+v answer = %+v", w, out)
	}
}

func TestTheStepPassesTheStoreRefusalsThrough(t *testing.T) {
	g := gstinFor(t, testPAN)
	in := SellScopeInput{SellScope: "all_categories", GSTIN: &g}
	_, err := (&Service{}).WithSellScopeStore(&fakeScopeStore{saveErr: postgres.ErrSellScopeLocked}).
		SaveSellScope(context.Background(), uuid.New(), in)
	if !errors.Is(err, postgres.ErrSellScopeLocked) {
		t.Fatalf("locked: err = %v", err)
	}
	_, err = (&Service{}).WithSellScopeStore(&fakeScopeStore{saveErr: postgres.ErrNoSellerRow}).
		SaveSellScope(context.Background(), uuid.New(), in)
	if !errors.Is(err, ErrNoSellerProfile) {
		t.Fatalf("no row: err = %v, want ErrNoSellerProfile", err)
	}
}

func TestStartOnboardingRefusesAnUnknownScopeBeforeTheStore(t *testing.T) {
	// A nil store: reaching it would panic.
	_, err := (&Service{}).StartOnboarding(context.Background(), StartOnboardingInput{
		UserID: uuid.New(), StoreName: "Asha Books", SellScope: "books",
	})
	if !errors.Is(err, ErrSellScopeInvalid) {
		t.Fatalf("err = %v, want ErrSellScopeInvalid", err)
	}
}

// ─── The upgrade ─────────────────────────────────────────────────────

func booksSeller(t *testing.T, c *pii.Cipher, pan string) *postgres.Seller {
	t.Helper()
	sel := &postgres.Seller{ID: uuid.New(), UserID: uuid.New(), SellScope: postgres.SellScopeBooksOnly}
	if pan != "" {
		s, err := c.SealIdentifier(context.Background(), pii.ScopeKYC, "pan", pan)
		if err != nil {
			t.Fatal(err)
		}
		sel.PANEnc = s.Enc
	}
	return sel
}

func TestTheUpgradeNeedsAGSTINOfThePANOnFile(t *testing.T) {
	c := kycCipher(t)
	ctx := context.Background()

	// Someone else's GSTIN: valid, wrong PAN.
	st := &fakeScopeStore{seller: booksSeller(t, c, testPAN)}
	svc := (&Service{}).WithPII(c).WithSellScopeStore(st)
	_, err := svc.UpgradeSellScope(ctx, st.seller.UserID, gstinFor(t, "PQRCS6789Z"))
	if !errors.Is(err, ErrGSTINPANMismatch) || fieldOf(err) != "gstin" {
		t.Fatalf("other PAN's GSTIN: err = %v, want ErrGSTINPANMismatch", err)
	}
	if len(st.upgraded) != 0 {
		t.Fatal("a mismatched GSTIN upgraded the shop")
	}

	// No PAN on file cannot match anything.
	st2 := &fakeScopeStore{seller: booksSeller(t, c, "")}
	_, err = (&Service{}).WithPII(c).WithSellScopeStore(st2).UpgradeSellScope(ctx, uuid.New(), gstinFor(t, testPAN))
	if !errors.Is(err, ErrGSTINPANMismatch) || len(st2.upgraded) != 0 {
		t.Fatalf("no PAN on file: err = %v, upgraded %v", err, st2.upgraded)
	}

	// Invalid and blank.
	good := gstinFor(t, testPAN)
	flipped := good[:14] + "0"
	if good[14] == '0' {
		flipped = good[:14] + "1"
	}
	if _, err := svc.UpgradeSellScope(ctx, uuid.New(), flipped); !errors.Is(err, ErrGSTINInvalid) {
		t.Fatalf("bad GSTIN: err = %v", err)
	}
	if _, err := svc.UpgradeSellScope(ctx, uuid.New(), "  "); !errors.Is(err, ErrGSTINRequired) {
		t.Fatalf("blank GSTIN: err = %v", err)
	}

	// The GSTIN of the PAN on file upgrades, normalised.
	g := gstinFor(t, testPAN)
	view, err := svc.UpgradeSellScope(ctx, st.seller.UserID, strings.ToLower(g))
	if err != nil {
		t.Fatal(err)
	}
	if len(st.upgraded) != 1 || st.upgraded[0] != g || view.SellScope != "all_categories" || view.CanUpgrade || !view.HasGSTIN {
		t.Fatalf("upgraded %v view %+v", st.upgraded, view)
	}
}

func TestOnlyABooksOnlyShopCanUpgrade(t *testing.T) {
	st := &fakeScopeStore{seller: &postgres.Seller{ID: uuid.New(), SellScope: postgres.SellScopeAllCategories}}
	_, err := (&Service{}).WithSellScopeStore(st).UpgradeSellScope(context.Background(), uuid.New(), gstinFor(t, testPAN))
	if !errors.Is(err, postgres.ErrSellScopeNotBooksOnly) || len(st.upgraded) != 0 {
		t.Fatalf("err = %v upgraded %v", err, st.upgraded)
	}
}

// ─── Listings ────────────────────────────────────────────────────────

func TestABooksOnlyShopListsOnlyBooksOnGSTZero(t *testing.T) {
	ctx := context.Background()
	book, other := uuid.New(), uuid.New()
	zero, eighteen := uuid.New(), uuid.New()
	st := &fakeScopeStore{bookCats: map[uuid.UUID]bool{book: true}, zeroClasses: map[uuid.UUID]bool{zero: true}}
	svc := (&Service{}).WithSellScopeStore(st)

	refusal := func(err error) string {
		var b *SellerScopeBooksOnlyError
		if !errors.As(err, &b) || !errors.Is(err, ErrSellerScopeBooksOnly) {
			return ""
		}
		return b.Field
	}
	if f := refusal(svc.checkBooksOnlyListing(ctx, "books_only", &other, &zero, false)); f != "category_id" {
		t.Fatalf("non-book category: field %q, want category_id", f)
	}
	if f := refusal(svc.checkBooksOnlyListing(ctx, "books_only", &book, &eighteen, false)); f != "tax_class_id" {
		t.Fatalf("a book at 18%%: field %q, want tax_class_id", f)
	}
	if err := svc.checkBooksOnlyListing(ctx, "books_only", &book, &zero, true); err != nil {
		t.Fatalf("a book on GST 0%%: %v", err)
	}
	// A draft may be uncategorised; review may not.
	if err := svc.checkBooksOnlyListing(ctx, "books_only", nil, nil, false); err != nil {
		t.Fatalf("uncategorised draft: %v", err)
	}
	if f := refusal(svc.checkBooksOnlyListing(ctx, "books_only", nil, &zero, true)); f != "category_id" {
		t.Fatalf("uncategorised submit: field %q, want category_id", f)
	}
	// An all-categories shop is not looked at at all.
	before := st.lookups
	if err := svc.checkBooksOnlyListing(ctx, "all_categories", &other, &eighteen, true); err != nil || st.lookups != before {
		t.Fatalf("all-categories shop: err %v, %d lookups", err, st.lookups-before)
	}
}

// The create path refuses before anything else is read: the service has a
// nil store, so reaching requireCategory would panic.
func TestCreateProductRefusesABooksOnlyShopOutsideBooks(t *testing.T) {
	other, zero := uuid.New(), uuid.New()
	svc := (&Service{}).WithSellScopeStore(&fakeScopeStore{zeroClasses: map[uuid.UUID]bool{zero: true}})
	_, err := svc.CreateProduct(context.Background(), CreateProductInput{
		SellerID: uuid.New(), SellScope: "books_only", CategoryID: &other, TaxClassID: &zero,
		Title: "A phone", Variants: []CreateVariantInput{{SKU: "P1", MRPMinor: 1000, SellingPriceMinor: 900}},
	})
	if !errors.Is(err, ErrSellerScopeBooksOnly) {
		t.Fatalf("err = %v, want ErrSellerScopeBooksOnly", err)
	}
}

// ─── Proof-of-address documents ──────────────────────────────────────

func TestTheBackOfADocument(t *testing.T) {
	front, back := uuid.New(), uuid.New()
	ok := []postgres.SellerDocument{
		{DocumentType: "voter_id", MediaID: front, BackMediaID: &back},
		{DocumentType: "passport", MediaID: uuid.New(), BackMediaID: ptrUUID(uuid.New())},
		{DocumentType: "electricity_bill", MediaID: uuid.New()},
		{DocumentType: "voter_id", MediaID: uuid.New()}, // the back may follow later
	}
	if err := validateDocumentSides(ok); err != nil {
		t.Fatalf("valid documents refused: %v", err)
	}
	for _, d := range []postgres.SellerDocument{
		{DocumentType: "electricity_bill", MediaID: front, BackMediaID: &back},
		{DocumentType: "pan_card", MediaID: front, BackMediaID: &back},
		{DocumentType: "voter_id", MediaID: front, BackMediaID: &front},
	} {
		if err := validateDocumentSides([]postgres.SellerDocument{d}); !errors.Is(err, ErrDocumentBackNotAccepted) {
			t.Errorf("%s back %v: err = %v, want ErrDocumentBackNotAccepted", d.DocumentType, d.BackMediaID, err)
		}
	}
	nilBack := uuid.Nil
	if err := validateDocumentSides([]postgres.SellerDocument{{DocumentType: "pan_card", MediaID: front, BackMediaID: &nilBack}}); err != nil {
		t.Fatalf("a nil uuid is no back at all: %v", err)
	}
}

func TestAnImageOnlyProofTakesNoNumber(t *testing.T) {
	for _, typ := range []string{"electricity_bill", "telephone_bill", "bank_statement"} {
		err := validateDocumentSides([]postgres.SellerDocument{{DocumentType: typ, MediaID: uuid.New(), DocumentNumber: strptr("50100012345678")}})
		if !errors.Is(err, ErrDocumentNumberNotAccepted) {
			t.Errorf("%s with a number: err = %v", typ, err)
		}
		if err != nil && strings.Contains(err.Error(), "50100012345678") {
			t.Errorf("the refusal echoes the number: %v", err)
		}
		if err := validateDocumentSides([]postgres.SellerDocument{{DocumentType: typ, MediaID: uuid.New(), DocumentNumber: strptr("  ")}}); err != nil {
			t.Errorf("%s with a blank number: %v", typ, err)
		}
	}
	if err := validateDocumentSides([]postgres.SellerDocument{{DocumentType: "voter_id", MediaID: uuid.New(), DocumentNumber: strptr("ABC1234567")}}); err != nil {
		t.Fatalf("a voter ID keeps its EPIC number: %v", err)
	}
}

// SaveDocuments applies the rules before the store (nil here).
func TestSaveDocumentsRefusesABackOnASingleSidedDocumentBeforeTheStore(t *testing.T) {
	err := (&Service{}).SaveDocuments(context.Background(), uuid.New(), []postgres.SellerDocument{
		{DocumentType: "bank_statement", MediaID: uuid.New(), BackMediaID: ptrUUID(uuid.New())},
	})
	if !errors.Is(err, ErrDocumentBackNotAccepted) {
		t.Fatalf("err = %v", err)
	}
}

// The reviewer's image route opens the side asked for, from the row.
func TestTheAdminImageSide(t *testing.T) {
	front, back := uuid.New(), uuid.New()
	doc := &postgres.AdminSellerDocument{MediaID: &front, BackMediaID: &back}
	if got, err := documentImageMediaID(doc, false); err != nil || got != front {
		t.Fatalf("front = %v, %v", got, err)
	}
	if got, err := documentImageMediaID(doc, true); err != nil || got != back {
		t.Fatalf("back = %v, %v", got, err)
	}
	single := &postgres.AdminSellerDocument{MediaID: &front}
	if _, err := documentImageMediaID(single, true); !errors.Is(err, ErrDocumentImageUnavailable) {
		t.Fatalf("back of a single-sided document: err = %v", err)
	}
}

func ptrUUID(u uuid.UUID) *uuid.UUID { return &u }
