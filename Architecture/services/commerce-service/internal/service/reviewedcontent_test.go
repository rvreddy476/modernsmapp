package service

// The old-vs-new comparisons behind the revalidation rule, without a
// database: what counts as CHANGING an approved listing's reviewed content.
//
// Each test is written so that neutering the comparison it covers (back to
// "the request carries the field") fails it by name.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// strp and intp come from attributes_test.go.
func i64p(i int64) *int64 { return &i }

func liveProduct() *postgres.Product {
	brand := uuid.New()
	cover := uuid.New()
	return &postgres.Product{
		ID: uuid.New(), ApprovalStatus: "approved",
		Title: "Live listing", Description: strp("The description."),
		ShortTitle: nil, BrandID: &brand, PrimaryImageMediaID: &cover,
		ProductType: "physical", Condition: "new",
		ReturnPolicyType: "7_days", ReturnPolicyDays: 7,
		WarrantyInfo: strp("  1 year  "), HSNCode: nil,
	}
}

// ─── 5: a patch that re-sends what is stored is not substantive ─────────

// TestSubstantiveFieldsIgnoresAnUnchangedResend is THE regression for the
// "every web save forces re-review" defect: the editor sends the whole form,
// and a form whose values match the row must name no field.
func TestSubstantiveFieldsIgnoresAnUnchangedResend(t *testing.T) {
	cur := liveProduct()
	same := postgres.ProductPatch{
		Title:               strp("Live listing"),
		Description:         strp("The description."),
		ShortTitle:          strp(""),       // blank ⇔ NULL
		WarrantyInfo:        strp("1 year"), // whitespace is not a change
		HSNCode:             strp(""),
		BrandID:             cur.BrandID,
		PrimaryImageMediaID: cur.PrimaryImageMediaID,
		CategoryID:          nil, // absent
		ProductType:         strp("physical"),
		Condition:           strp("new"),
		ReturnPolicyType:    strp("7_days"),
		ReturnPolicyDays:    intp(7),
	}
	if got := substantiveFields(cur, same, false, false); len(got) != 0 {
		t.Fatalf("re-sending the stored values must change nothing, got %v", got)
	}
}

func TestSubstantiveFieldsNamesEveryFieldThatActuallyChanges(t *testing.T) {
	cur := liveProduct()
	other := uuid.New()
	patch := postgres.ProductPatch{
		Title:               strp("Rewritten"),
		Description:         strp(""), // clearing a value IS a change
		WarrantyInfo:        strp("2 years"),
		BrandID:             &uuid.Nil, // null clears the brand
		PrimaryImageMediaID: &other,
		ReturnPolicyDays:    intp(15),
		ReturnPolicyType:    strp("15_days"),
		// Review-neutral fields never appear, however much they change.
		MetaTitle:   strp("new meta"),
		WeightGrams: intp(900),
	}
	got := substantiveFields(cur, patch, true, true)
	want := []string{"brand_id", "title", "description", "primary_image_media_id",
		"warranty_info", "return_policy_type", "return_policy_days", "attributes", "variation_axes"}
	if len(got) != len(want) {
		t.Fatalf("changed = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("changed = %v, want %v", got, want)
		}
	}
}

// ─── attributes: stored rows against the rows a write would put there ───

func TestAttributeValuesDifferComparesValuesNotPresence(t *testing.T) {
	n := 264.0
	stored := []postgres.ProductAttributeValue{{ValueNum: &n}}

	same := 264.0
	if attributeValuesDiffer(stored, []postgres.ProductAttributeValue{{ValueNum: &same}}) {
		t.Fatal("the same page count must not read as a change")
	}
	// NUMERIC(24,6): what the column cannot hold is not a difference.
	almost := 264.0000004
	if attributeValuesDiffer(stored, []postgres.ProductAttributeValue{{ValueNum: &almost}}) {
		t.Fatal("a difference below the column's scale must not read as a change")
	}
	changed := 300.0
	if !attributeValuesDiffer(stored, []postgres.ProductAttributeValue{{ValueNum: &changed}}) {
		t.Fatal("a different page count must read as a change")
	}
	// Clearing (an empty set) against a stored value is a change; against
	// nothing stored it is not.
	if !attributeValuesDiffer(stored, nil) {
		t.Fatal("clearing a stored value must read as a change")
	}
	if attributeValuesDiffer(nil, nil) {
		t.Fatal("clearing nothing must not read as a change")
	}

	// A multi-value answer compares in position order.
	a, b := "en", "hi"
	two := []postgres.ProductAttributeValue{{ValueText: &a}, {ValueText: &b}}
	if attributeValuesDiffer(two, []postgres.ProductAttributeValue{{ValueText: &a}, {ValueText: &b}}) {
		t.Fatal("the same languages in the same order must not read as a change")
	}
	if !attributeValuesDiffer(two, []postgres.ProductAttributeValue{{ValueText: &b}, {ValueText: &a}}) {
		t.Fatal("the same languages in a different order IS a change (position is stored)")
	}

	// Dates compare as calendar dates: the column is DATE.
	d1 := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 10, 21, 15, 30, 0, 0, time.UTC)
	if attributeValuesDiffer([]postgres.ProductAttributeValue{{ValueDate: &d1}}, []postgres.ProductAttributeValue{{ValueDate: &d2}}) {
		t.Fatal("the same calendar date must not read as a change")
	}

	// A unit change on the same number is a change.
	g, kg := "g", "kg"
	if !attributeValuesDiffer([]postgres.ProductAttributeValue{{ValueNum: &n, UnitCode: &g}},
		[]postgres.ProductAttributeValue{{ValueNum: &n, UnitCode: &kg}}) {
		t.Fatal("264 g and 264 kg are different answers")
	}
}

// ─── the matrix: axes in order, options as a set ────────────────────────

func TestVariationDiffersComparesAxesAndOptions(t *testing.T) {
	size, colour := uuid.New(), uuid.New()
	v1, v2 := uuid.New(), uuid.New()
	axes := []postgres.ProductVariationAxisRow{{DefinitionID: size, Position: 0}, {DefinitionID: colour, Position: 1}}
	opts := map[uuid.UUID][]postgres.VariantOption{
		v1: {{DefinitionID: size, ValueCode: "l"}, {DefinitionID: colour, ValueCode: "blue"}},
		v2: {{DefinitionID: colour, ValueCode: "red"}, {DefinitionID: size, ValueCode: "m"}},
	}
	same := &postgres.VariationUpdate{
		Axes: []postgres.VariationAxis{{DefinitionID: size, Position: 0}, {DefinitionID: colour, Position: 1}},
		VariantOptions: map[uuid.UUID][]postgres.VariantOption{
			// Option order within a variant is not part of the value.
			v1: {{DefinitionID: colour, ValueCode: "blue"}, {DefinitionID: size, ValueCode: "l"}},
			v2: {{DefinitionID: size, ValueCode: "m"}, {DefinitionID: colour, ValueCode: "red"}},
		},
	}
	if variationDiffers(axes, opts, same) {
		t.Fatal("re-sending the stored matrix must not read as a change")
	}

	swapped := &postgres.VariationUpdate{
		Axes:           []postgres.VariationAxis{{DefinitionID: colour, Position: 0}, {DefinitionID: size, Position: 1}},
		VariantOptions: same.VariantOptions,
	}
	if !variationDiffers(axes, opts, swapped) {
		t.Fatal("swapping the axis order IS a change")
	}

	recoloured := &postgres.VariationUpdate{
		Axes: same.Axes,
		VariantOptions: map[uuid.UUID][]postgres.VariantOption{
			v1: {{DefinitionID: size, ValueCode: "l"}, {DefinitionID: colour, ValueCode: "green"}},
			v2: opts[v2],
		},
	}
	if !variationDiffers(axes, opts, recoloured) {
		t.Fatal("changing one variant's colour IS a change")
	}

	cleared := &postgres.VariationUpdate{VariantOptions: map[uuid.UUID][]postgres.VariantOption{v1: nil, v2: nil}}
	if !variationDiffers(axes, opts, cleared) {
		t.Fatal("clearing the matrix IS a change")
	}
	if variationDiffers(nil, map[uuid.UUID][]postgres.VariantOption{}, &postgres.VariationUpdate{
		VariantOptions: map[uuid.UUID][]postgres.VariantOption{v1: nil}}) {
		t.Fatal("clearing a matrix that was never declared is not a change")
	}
}

// ─── the gallery ────────────────────────────────────────────────────────

func TestGalleryChangesIsOrderSensitive(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	if galleryChanges([]uuid.UUID{a, b}, []uuid.UUID{a, b}) {
		t.Fatal("the same gallery must not read as a change")
	}
	if !galleryChanges([]uuid.UUID{a, b}, []uuid.UUID{b, a}) {
		t.Fatal("a reorder moves the cover and IS a change")
	}
	if !galleryChanges([]uuid.UUID{a, b}, []uuid.UUID{a, b, c}) {
		t.Fatal("an added image IS a change")
	}
	if !galleryChanges([]uuid.UUID{a, b}, []uuid.UUID{a}) {
		t.Fatal("a removed image IS a change")
	}
	if galleryChanges(nil, []uuid.UUID{}) {
		t.Fatal("empty and empty are the same gallery")
	}
}

// ─── a variant's reviewed fields ────────────────────────────────────────

func TestVariantSubstantiveChangesComparesAgainstTheRow(t *testing.T) {
	cur := &postgres.ProductVariant{
		SKU: "TEE-L-BLUE", MRPMinor: i64p(149900), SellingPriceMinor: i64p(129900),
		MRP: 1499, SellingPrice: 1299, Option1Name: strp("Size"), Option1Value: strp("L"),
	}

	// The web editor re-sends the SKU and both prices on every save.
	resend := map[string]any{"sku": "TEE-L-BLUE", "mrp_minor": float64(149900), "selling_price_minor": float64(129900)}
	if got := variantSubstantiveChanges(cur, resend); len(got) != 0 {
		t.Fatalf("re-sending the stored price and SKU must change nothing, got %v", got)
	}
	// The legacy rupee shape of the same money is the same money.
	rupees := map[string]any{"mrp": float64(1499), "selling_price": 1299.00}
	if got := variantSubstantiveChanges(cur, rupees); len(got) != 0 {
		t.Fatalf("the rupee form of the stored price must change nothing, got %v", got)
	}

	// The MSeller rule: price and stock go live at once. A new price is NOT
	// reviewed content, so it must not send the listing back to review.
	reprice := map[string]any{"selling_price_minor": float64(99900), "mrp": float64(1599)}
	if got := variantSubstantiveChanges(cur, reprice); len(got) != 0 {
		t.Fatalf("a new price goes live at once and must name no reviewed field, got %v", got)
	}
	resku := map[string]any{"sku": " TEE-L-NAVY "}
	if got := variantSubstantiveChanges(cur, resku); len(got) != 1 || got[0] != "sku" {
		t.Fatalf("a new SKU must be named as sku, got %v", got)
	}
	reoption := map[string]any{"option_1_value": "XL"}
	if got := variantSubstantiveChanges(cur, reoption); len(got) != 1 || got[0] != "options" {
		t.Fatalf("a new option value must be named as options, got %v", got)
	}
	// Not reviewed content: pausing a size, a parcel weight, a barcode.
	neutral := map[string]any{"status": "inactive", "weight_grams": float64(300), "barcode": "890", "cost_price_minor": float64(50000)}
	if got := variantSubstantiveChanges(cur, neutral); len(got) != 0 {
		t.Fatalf("status, weight, barcode and cost price are not reviewed, got %v", got)
	}
	reimage := map[string]any{"image_media_id": uuid.New().String()}
	if got := variantSubstantiveChanges(cur, reimage); len(got) != 1 || got[0] != "image" {
		t.Fatalf("a new variant image goes back through checks and must be named as image, got %v", got)
	}
}

// ─── a variant's price: live at once, but validated ─────────────────────

func TestPriceWithinMRP(t *testing.T) {
	if err := priceWithinMRP(149900, 149900); err != nil {
		t.Fatalf("selling AT the MRP is lawful: %v", err)
	}
	if err := priceWithinMRP(149900, 99900); err != nil {
		t.Fatalf("selling below the MRP is the normal case: %v", err)
	}
	if err := priceWithinMRP(149900, 149901); !errors.Is(err, ErrSellingAboveMRP) {
		t.Fatalf("one paisa above the MRP must be refused, got %v", err)
	}
	// Non-positive amounts are someone else's refusal (ErrPriceNotPositive).
	if err := priceWithinMRP(0, 99900); err != nil {
		t.Fatalf("a zero MRP is not this check's to refuse: %v", err)
	}
}

func TestVariantPriceAfterPatchComparesThePairItWouldLeave(t *testing.T) {
	cur := &postgres.ProductVariant{MRPMinor: i64p(149900), SellingPriceMinor: i64p(129900), MRP: 1499, SellingPrice: 1299}

	for _, tc := range []struct {
		name    string
		updates map[string]any
		refused bool
	}{
		{"selling price raised above the stored MRP", map[string]any{"selling_price_minor": float64(159900)}, true},
		{"MRP lowered below the stored selling price", map[string]any{"mrp_minor": float64(119900)}, true},
		{"both sent, inverted", map[string]any{"mrp_minor": float64(100000), "selling_price_minor": float64(100100)}, true},
		{"the legacy rupee shape is checked too", map[string]any{"selling_price": float64(1500)}, true},
		{"both raised together", map[string]any{"mrp_minor": float64(199900), "selling_price_minor": float64(159900)}, false},
		{"selling price lowered", map[string]any{"selling_price_minor": float64(99900)}, false},
		{"selling price set to the MRP", map[string]any{"selling_price_minor": float64(149900)}, false},
		{"no price in the body", map[string]any{"status": "inactive"}, false},
		{"an unreadable price is the store's to refuse", map[string]any{"selling_price_minor": "lots"}, false},
	} {
		err := variantPriceAfterPatch(cur, tc.updates)
		if tc.refused && !errors.Is(err, ErrSellingAboveMRP) {
			t.Errorf("%s: want ErrSellingAboveMRP, got %v", tc.name, err)
		}
		if !tc.refused && err != nil {
			t.Errorf("%s: want no refusal, got %v", tc.name, err)
		}
	}

	// A legacy row whose minor columns are zero is read through its rupees.
	legacy := &postgres.ProductVariant{MRPMinor: i64p(0), MRP: 1499, SellingPriceMinor: i64p(0), SellingPrice: 1299}
	if err := variantPriceAfterPatch(legacy, map[string]any{"selling_price_minor": float64(149901)}); !errors.Is(err, ErrSellingAboveMRP) {
		t.Fatalf("a legacy row's MRP comes from its rupee mirror, got %v", err)
	}
}

func TestCheckVariantPatch(t *testing.T) {
	variant := func() *postgres.ProductVariant {
		return &postgres.ProductVariant{SKU: "TEE-L", MRPMinor: i64p(149900), SellingPriceMinor: i64p(129900), MRP: 1499, SellingPrice: 1299}
	}
	draft := &postgres.Product{ApprovalStatus: "draft"}
	live := &postgres.Product{ApprovalStatus: "approved"}

	t.Run("a price change on a live listing names no reviewed field", func(t *testing.T) {
		changed, err := checkVariantPatch(live, variant(), map[string]any{"selling_price_minor": float64(119900)})
		if err != nil || len(changed) != 0 {
			t.Fatalf("want live-at-once, got changed=%v err=%v", changed, err)
		}
		if reval, err := gateApprovedEdit(live, changed, false); err != nil || reval {
			t.Fatalf("the gate must apply it in place with no ack: reval=%v err=%v", reval, err)
		}
	})
	t.Run("a price above the MRP is refused on a live listing and on a draft", func(t *testing.T) {
		for _, p := range []*postgres.Product{live, draft} {
			if _, err := checkVariantPatch(p, variant(), map[string]any{"selling_price_minor": float64(149901)}); !errors.Is(err, ErrSellingAboveMRP) {
				t.Fatalf("%s: want ErrSellingAboveMRP, got %v", p.ApprovalStatus, err)
			}
		}
	})
	t.Run("the SKU is written while a draft, trimmed", func(t *testing.T) {
		updates := map[string]any{"sku": "  TEE-L-FIXED "}
		changed, err := checkVariantPatch(draft, variant(), updates)
		if err != nil || updates["sku"] != "TEE-L-FIXED" || len(changed) != 1 || changed[0] != "sku" {
			t.Fatalf("want the trimmed SKU kept for the write, got updates=%v changed=%v err=%v", updates, changed, err)
		}
	})
	t.Run("the SKU is locked once approved, ack or not", func(t *testing.T) {
		if _, err := checkVariantPatch(live, variant(), map[string]any{"sku": "TEE-L-NEW"}); !errors.Is(err, ErrSKULocked) {
			t.Fatalf("want ErrSKULocked, got %v", err)
		}
	})
	t.Run("re-sending the stored SKU on a live listing is dropped, not locked", func(t *testing.T) {
		updates := map[string]any{"sku": "TEE-L", "selling_price_minor": float64(129900)}
		changed, err := checkVariantPatch(live, variant(), updates)
		if err != nil || len(changed) != 0 {
			t.Fatalf("want no change, got changed=%v err=%v", changed, err)
		}
		if _, still := updates["sku"]; still {
			t.Fatal("the unchanged SKU must be removed from the write")
		}
	})
	t.Run("a blank SKU is refused", func(t *testing.T) {
		if _, err := checkVariantPatch(draft, variant(), map[string]any{"sku": "  "}); !errors.Is(err, ErrVariantFieldRequired) {
			t.Fatalf("want ErrVariantFieldRequired, got %v", err)
		}
	})
	t.Run("paused is not a variant status; the schema's word is inactive", func(t *testing.T) {
		if _, err := checkVariantPatch(live, variant(), map[string]any{"status": "paused"}); !errors.Is(err, ErrVariantStatusInvalid) {
			t.Fatalf("want ErrVariantStatusInvalid, got %v", err)
		}
		if _, err := checkVariantPatch(live, variant(), map[string]any{"status": "inactive"}); err != nil {
			t.Fatalf("inactive must be accepted: %v", err)
		}
	})
	t.Run("an option change on a live listing is still reviewed", func(t *testing.T) {
		changed, err := checkVariantPatch(live, variant(), map[string]any{"option_1_value": "XL"})
		if err != nil || len(changed) != 1 || changed[0] != "options" {
			t.Fatalf("want options named, got changed=%v err=%v", changed, err)
		}
	})
}

// ─── the return window ──────────────────────────────────────────────────

func TestReturnPolicyTypesAreTheCheck(t *testing.T) {
	// setup.sql: CHECK (return_policy_type IN ('no_return','7_days','15_days','30_days','custom')).
	want := []string{"no_return", "7_days", "15_days", "30_days", "custom"}
	if len(ReturnPolicyTypes) != len(want) {
		t.Fatalf("ReturnPolicyTypes = %v, want the CHECK's %v", ReturnPolicyTypes, want)
	}
	for i, v := range want {
		if ReturnPolicyTypes[i] != v || !validReturnPolicyType(v) {
			t.Fatalf("ReturnPolicyTypes = %v, want the CHECK's %v", ReturnPolicyTypes, want)
		}
	}
	for _, bad := range []string{"14_days", "standard", "", "7 days", "7_DAYS"} {
		if validReturnPolicyType(bad) {
			t.Fatalf("%q is not in the CHECK and must be refused", bad)
		}
	}
}

// These refusals come before the first store read, so a zero Service (no
// database) is enough to prove each entry point makes them.
func TestCreateProductRefusesBadInputBeforeAnyRead(t *testing.T) {
	s := &Service{}
	base := func() CreateProductInput {
		return CreateProductInput{
			Title:    "Tee",
			Variants: []CreateVariantInput{{SKU: "TEE-L", MRPMinor: 149900, SellingPriceMinor: 129900}},
		}
	}

	in := base()
	in.ReturnPolicyType = "14_days"
	if _, err := s.CreateProduct(context.Background(), in); !errors.Is(err, ErrReturnPolicyInvalid) {
		t.Fatalf("a 14-day window must be refused by name, got %v", err)
	}

	in = base()
	in.Variants = append(in.Variants, CreateVariantInput{SKU: "TEE-XL", MRPMinor: 149900, SellingPriceMinor: 159900})
	if _, err := s.CreateProduct(context.Background(), in); !errors.Is(err, ErrSellingAboveMRP) {
		t.Fatalf("a variant priced above its MRP must be refused, got %v", err)
	}
}

func TestAddProductVariantRefusesAPriceAboveTheMRPBeforeAnyRead(t *testing.T) {
	s := &Service{}
	v := &postgres.ProductVariant{SKU: "TEE-XL", MRPMinorIn: i64p(99900), SellingPriceMinorIn: i64p(109900)}
	if _, err := s.AddProductVariant(context.Background(), uuid.New(), uuid.New(), v, 1); !errors.Is(err, ErrSellingAboveMRP) {
		t.Fatalf("want ErrSellingAboveMRP, got %v", err)
	}
}

func TestUpdateProductRefusesAReturnWindowOutsideTheCheck(t *testing.T) {
	s := &Service{}
	_, err := s.UpdateProduct(context.Background(), UpdateProductInput{Fields: postgres.ProductPatch{ReturnPolicyType: strp("14_days")}})
	if !errors.Is(err, ErrReturnPolicyInvalid) {
		t.Fatalf("want ErrReturnPolicyInvalid before any read, got %v", err)
	}
}

// ─── the gate ───────────────────────────────────────────────────────────

func TestGateApprovedEdit(t *testing.T) {
	draft := &postgres.Product{ApprovalStatus: "draft"}
	live := &postgres.Product{ApprovalStatus: "approved"}
	queued := &postgres.Product{ApprovalStatus: "submitted"}

	if reval, err := gateApprovedEdit(draft, []string{"price"}, false); err != nil || reval {
		t.Fatalf("a draft's edit applies in place: reval=%v err=%v", reval, err)
	}
	if reval, err := gateApprovedEdit(live, nil, false); err != nil || reval {
		t.Fatalf("an approved listing's non-change applies in place: reval=%v err=%v", reval, err)
	}
	_, err := gateApprovedEdit(live, []string{"gallery"}, false)
	var need *RevalidationRequiredError
	if !errors.As(err, &need) || len(need.Fields) != 1 || need.Fields[0] != "gallery" {
		t.Fatalf("an approved listing's change without the ack must be refused naming the field, got %v", err)
	}
	if reval, err := gateApprovedEdit(live, []string{"gallery"}, true); err != nil || !reval {
		t.Fatalf("with the ack the edit applies AND revalidates: reval=%v err=%v", reval, err)
	}
	if reval, err := gateApprovedEdit(queued, nil, false); err != nil || reval {
		t.Fatalf("a non-change on a listing in front of a reviewer applies in place: reval=%v err=%v", reval, err)
	}
	if _, err := gateApprovedEdit(queued, []string{"price"}, true); !errors.Is(err, ErrProductNotEditable) {
		t.Fatalf("reviewed content on a listing in front of a reviewer is not editable, got %v", err)
	}
}
