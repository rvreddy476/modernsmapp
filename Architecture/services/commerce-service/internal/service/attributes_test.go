package service

// What a definition is allowed to be, and what the form endpoint promises.
//
// Both halves are tested without a database on purpose. The validation rules
// and the response assembly are the contract — the grouping, the ordering,
// which fields carry `values` and which carry `units`, what the ETag is made
// of — and none of it needs a row to be wrong.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

func strp(s string) *string   { return &s }
func intp(n int) *int         { return &n }
func f64p(f float64) *float64 { return &f }

// ─── Validation ─────────────────────────────────────────────────────────

func TestValidateAttributeDefinition(t *testing.T) {
	base := func(mutate func(*postgres.AttributeDefinition)) *postgres.AttributeDefinition {
		d := &postgres.AttributeDefinition{
			Code: "page_count", Label: "Pages", DataType: "integer",
			DisplayGroup: "Product Details", AppliesTo: "item",
		}
		if mutate != nil {
			mutate(d)
		}
		return d
	}

	cases := []struct {
		name      string
		def       *postgres.AttributeDefinition
		wantField string // "" means it must be accepted
	}{
		{"a plain integer field is fine", base(nil), ""},

		// ─── code shape ───────────────────────────────────────────
		{"code must not start with a digit",
			base(func(d *postgres.AttributeDefinition) { d.Code = "1pages" }), "code"},
		{"code must not contain a hyphen — it is a SQL identifier's shape, not a slug's",
			base(func(d *postgres.AttributeDefinition) { d.Code = "page-count" }), "code"},
		{"code must not be upper case: `Author` and `author` would be two fields storing one fact",
			base(func(d *postgres.AttributeDefinition) { d.Code = "Author" }), "code"},
		{"code must be at least two characters",
			base(func(d *postgres.AttributeDefinition) { d.Code = "a" }), "code"},
		{"code must be at most 49 characters",
			base(func(d *postgres.AttributeDefinition) {
				d.Code = "a123456789012345678901234567890123456789012345678901234567890"
			}), "code"},
		{"a label is required — a form control with no label is a box with no question",
			base(func(d *postgres.AttributeDefinition) { d.Label = "  " }), "label"},

		// ─── type vocabulary ──────────────────────────────────────
		{"an unknown data type is refused rather than stored and rendered as text",
			base(func(d *postgres.AttributeDefinition) { d.DataType = "colour" }), "data_type"},
		{"applies_to must be item or offer",
			base(func(d *postgres.AttributeDefinition) { d.AppliesTo = "seller" }), "applies_to"},
		{"an unknown display group would render as a lonely extra fieldset",
			base(func(d *postgres.AttributeDefinition) { d.DisplayGroup = "Misc" }), "display_group"},

		// ─── measure needs a family ───────────────────────────────
		{"a measure with no unit family is a number with no meaning",
			base(func(d *postgres.AttributeDefinition) { d.DataType = "measure" }), "unit_family"},
		{"a measure with a family is fine",
			base(func(d *postgres.AttributeDefinition) {
				d.DataType = "measure"
				d.UnitFamily = strp("mass")
				d.DefaultUnit = strp("g")
			}), ""},
		{"a non-measure may not carry a unit family",
			base(func(d *postgres.AttributeDefinition) { d.UnitFamily = strp("mass") }), "unit_family"},
		{"a default unit with no family has nothing to be a unit of",
			base(func(d *postgres.AttributeDefinition) { d.DefaultUnit = strp("g") }), "default_unit"},

		// ─── variant axis ─────────────────────────────────────────
		{"integer may be a variant axis",
			base(func(d *postgres.AttributeDefinition) { d.IsVariantAxis = true }), ""},
		{"enum may be a variant axis",
			base(func(d *postgres.AttributeDefinition) {
				d.DataType = "enum"
				d.IsVariantAxis = true
			}), ""},
		{"a date axis would produce one variant per day",
			base(func(d *postgres.AttributeDefinition) {
				d.DataType = "date"
				d.IsVariantAxis = true
			}), "is_variant_axis"},
		{"a long_text axis would produce one variant per keystroke",
			base(func(d *postgres.AttributeDefinition) {
				d.DataType = "long_text"
				d.IsVariantAxis = true
			}), "is_variant_axis"},

		// ─── bounds ───────────────────────────────────────────────
		{"min above max admits no value at all",
			base(func(d *postgres.AttributeDefinition) {
				d.MinNum, d.MaxNum = f64p(10), f64p(1)
			}), "min_num"},
		{"min equal to max is a single legal value, which is legitimate",
			base(func(d *postgres.AttributeDefinition) {
				d.MinNum, d.MaxNum = f64p(10), f64p(10)
			}), ""},
		{"min_len above max_len admits no string at all",
			base(func(d *postgres.AttributeDefinition) {
				d.MinLen, d.MaxLen = intp(20), intp(4)
			}), "min_len"},
		{"a negative minimum length is not a length",
			base(func(d *postgres.AttributeDefinition) { d.MinLen = intp(-1) }), "min_len"},
		{"max_values below one forbids every value of a multi-valued field",
			base(func(d *postgres.AttributeDefinition) {
				d.DataType = "multi_enum"
				d.MaxValues = intp(0)
			}), "max_values"},

		// ─── regex ────────────────────────────────────────────────
		{"a regex that does not compile would fail every product on a server error",
			base(func(d *postgres.AttributeDefinition) {
				d.DataType = "text"
				d.Regex = strp("^[0-9")
			}), "regex"},
		{"a regex that compiles is fine",
			base(func(d *postgres.AttributeDefinition) {
				d.DataType = "text"
				d.Regex = strp("^[0-9]{10,14}$")
			}), ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAttributeDefinition(tc.def)
			if tc.wantField == "" {
				if err != nil {
					t.Fatalf("expected the definition to be accepted, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected a refusal naming %q, got none", tc.wantField)
			}
			ve, ok := err.(*AttributeValidationError)
			if !ok {
				t.Fatalf("expected an AttributeValidationError, got %T: %v", err, err)
			}
			if ve.Field != tc.wantField {
				t.Fatalf("refusal named field %q, want %q (reason: %s)", ve.Field, tc.wantField, ve.Reason)
			}
		})
	}
}

func TestEnumCodesMustBeUniqueWithinADefinition(t *testing.T) {
	if err := validateEnumCodesUnique([]string{"hardcover", "paperback", "spiral"}); err != nil {
		t.Fatalf("distinct codes should be accepted: %v", err)
	}
	if err := validateEnumCodesUnique([]string{"hardcover", "hardcover"}); err == nil {
		t.Fatal("a duplicated option code must be refused: two options with one code make the " +
			"stored value ambiguous")
	}
	if err := validateEnumCodesUnique([]string{"Hard Cover"}); err == nil {
		t.Fatal("an option code with a space and capitals must be refused; it is a stored value, not a label")
	}
}

// ─── Narrowing ──────────────────────────────────────────────────────────

// A narrowing edit is one that makes a value that was legal illegal. Only
// those need the operator to acknowledge the damage; widening one cannot
// invalidate anything already stored.
func TestNarrowingOf(t *testing.T) {
	def := func(mutate func(*postgres.AttributeDefinition)) *postgres.AttributeDefinition {
		d := &postgres.AttributeDefinition{
			Code: "pages", Label: "Pages", DataType: "integer",
			DisplayGroup: "Product Details", AppliesTo: "item", IsActive: true,
			MinNum: f64p(1), MaxNum: f64p(10000),
		}
		if mutate != nil {
			mutate(d)
		}
		return d
	}

	cases := []struct {
		name      string
		after     *postgres.AttributeDefinition
		narrowing bool
	}{
		{"no change", def(nil), false},
		{"a widened maximum cannot invalidate a stored value",
			def(func(d *postgres.AttributeDefinition) { d.MaxNum = f64p(20000) }), false},
		{"a lowered maximum can", def(func(d *postgres.AttributeDefinition) { d.MaxNum = f64p(500) }), true},
		{"a raised minimum can", def(func(d *postgres.AttributeDefinition) { d.MinNum = f64p(50) }), true},
		{"a lowered minimum cannot", def(func(d *postgres.AttributeDefinition) { d.MinNum = f64p(0) }), false},
		{"a bound that appears where there was none is a narrowing",
			def(func(d *postgres.AttributeDefinition) { d.MinLen = intp(3) }), true},
		{"a bound that is removed is not",
			def(func(d *postgres.AttributeDefinition) { d.MaxNum = nil }), false},
		{"a format pattern that appears is a narrowing",
			def(func(d *postgres.AttributeDefinition) { d.Regex = strp("^[0-9]+$") }), true},
		{"retiring the field is a narrowing",
			def(func(d *postgres.AttributeDefinition) { d.IsActive = false }), true},
		{"changing the data type is a narrowing whichever way it goes",
			def(func(d *postgres.AttributeDefinition) { d.DataType = "text" }), true},
		{"help text is not",
			def(func(d *postgres.AttributeDefinition) { d.HelpText = strp("Printed pages") }), false},
	}

	before := def(nil)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			what := narrowingOf(before, tc.after)
			if tc.narrowing && what == "" {
				t.Fatal("expected this edit to require an impact acknowledgement")
			}
			if !tc.narrowing && what != "" {
				t.Fatalf("expected no acknowledgement to be required, got %q", what)
			}
		})
	}
}

// ─── The served document ────────────────────────────────────────────────

// booksFixture is the shape migration 025 seeds: gtin and publication_date in
// Product Identity, author/binding/pages/language in Product Details,
// item_weight in Logistics, with `binding` as the variant axis.
func booksFixture() (
	[]*postgres.EffectiveAttribute,
	map[uuid.UUID][]*postgres.AttributeEnumValue,
	map[string][]postgres.AttributeUnit,
) {
	at := func(s string) time.Time {
		ts, _ := time.Parse(time.RFC3339, s)
		return ts
	}
	gtinID, bindingID, weightID := uuid.New(), uuid.New(), uuid.New()

	effective := []*postgres.EffectiveAttribute{
		{
			Definition: postgres.AttributeDefinition{
				ID: gtinID, Code: "gtin", Label: "ISBN / EAN", DataType: "gtin",
				DisplayGroup: "Product Identity", AppliesTo: "item",
				HelpText: strp("The barcode on the back cover."),
				Regex:    strp("^[0-9]{10,14}$"),
				IsActive: true, UpdatedAt: at("2026-09-01T10:00:00Z"),
			},
			IsRequired: true, DisplayGroup: "Product Identity", SortOrder: 10, Depth: 1,
		},
		{
			Definition: postgres.AttributeDefinition{
				ID: bindingID, Code: "binding", Label: "Binding", DataType: "enum",
				DisplayGroup: "Product Details", AppliesTo: "item",
				IsFilterable: true, IsVariantAxis: true, IsActive: true,
				UpdatedAt: at("2026-09-02T10:00:00Z"),
			},
			IsVariantAxis: true, DisplayGroup: "Product Details", SortOrder: 20, Depth: 1,
		},
		{
			Definition: postgres.AttributeDefinition{
				ID: weightID, Code: "item_weight", Label: "Item weight", DataType: "measure",
				DisplayGroup: "Logistics", AppliesTo: "item",
				UnitFamily: strp("mass"), DefaultUnit: strp("g"),
				IsActive: true, UpdatedAt: at("2026-09-01T10:00:00Z"),
			},
			DisplayGroup: "Logistics", SortOrder: 10, Depth: 1,
		},
		{
			Definition: postgres.AttributeDefinition{
				ID: uuid.New(), Code: "condition", Label: "Condition", DataType: "text",
				DisplayGroup: "Offer", AppliesTo: "offer",
				IsActive: true, UpdatedAt: at("2026-09-01T10:00:00Z"),
			},
			DisplayGroup: "Offer", SortOrder: 10, Depth: 0,
		},
	}

	options := map[uuid.UUID][]*postgres.AttributeEnumValue{
		bindingID: {
			{Code: "hardcover", Label: "Hardcover", SortOrder: 10, UpdatedAt: at("2026-09-03T10:00:00Z")},
			{Code: "paperback", Label: "Paperback", SortOrder: 20, UpdatedAt: at("2026-09-02T10:00:00Z")},
		},
	}
	units := map[string][]postgres.AttributeUnit{
		"mass": {
			{Family: "mass", Code: "g", Label: "grams", FactorToBase: 1, SortOrder: 10},
			{Family: "mass", Code: "kg", Label: "kilograms", FactorToBase: 1000, SortOrder: 20},
		},
	}
	return effective, options, units
}

func TestAttributeSchemaShape(t *testing.T) {
	effective, options, units := booksFixture()
	state := &postgres.AttributeSchemaState{PublishedVersion: 3}
	catID := uuid.New()

	doc := buildAttributeSchema(catID, []string{"Books", "Textbooks"}, AttributeScopeAll,
		state, effective, options, units)

	if doc.SchemaVersion != 3 {
		t.Fatalf("schema_version = %d, want the PUBLISHED version 3 — an unpublished edit must not "+
			"move it, or every client refetches on every keystroke in the admin console", doc.SchemaVersion)
	}
	if got := doc.CategoryPath; len(got) != 2 || got[0] != "Books" || got[1] != "Textbooks" {
		t.Fatalf("category_path = %v, want root-first [Books Textbooks] — it is rendered as a breadcrumb", got)
	}
	if len(doc.VariationAxes) != 1 || doc.VariationAxes[0] != "binding" {
		t.Fatalf("variation_axes = %v, want [binding]", doc.VariationAxes)
	}

	// Groups come back in FORM order, not alphabetical: identity first, then
	// what the thing is, then the seller's offer, then logistics.
	wantGroups := []struct {
		name      string
		sortOrder int
	}{
		{"Product Identity", 10},
		{"Product Details", 30},
		{"Offer", 40},
		{"Logistics", 60},
	}
	if len(doc.Groups) != len(wantGroups) {
		t.Fatalf("got %d groups, want %d: %+v", len(doc.Groups), len(wantGroups), doc.Groups)
	}
	for i, want := range wantGroups {
		if doc.Groups[i].Name != want.name {
			t.Fatalf("group %d is %q, want %q — groups must be in form order, not alphabetical",
				i, doc.Groups[i].Name, want.name)
		}
		if doc.Groups[i].SortOrder != want.sortOrder {
			t.Fatalf("group %q sort_order = %d, want %d", want.name, doc.Groups[i].SortOrder, want.sortOrder)
		}
	}

	fields := map[string]AttributeFieldDoc{}
	for _, g := range doc.Groups {
		for _, f := range g.Attributes {
			fields[f.Code] = f
		}
	}

	gtin := fields["gtin"]
	if !gtin.Required {
		t.Fatal("gtin is bound required on Books and must come back required")
	}
	if gtin.Regex == nil || *gtin.Regex != "^[0-9]{10,14}$" {
		t.Fatalf("gtin.regex = %v, want the stored pattern — a client that cannot see it cannot "+
			"validate before submitting", gtin.Regex)
	}
	if gtin.LookupEndpoint != nil {
		t.Fatal("lookup_endpoint must be null today, and PRESENT — a future searchable enum must not " +
			"be a contract change")
	}

	// The field must SERIALISE with lookup_endpoint present-and-null. A
	// pointer with omitempty would drop the key entirely and a client written
	// against it would never learn the field exists.
	raw, err := json.Marshal(gtin)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v, ok := decoded["lookup_endpoint"]; !ok || string(v) != "null" {
		t.Fatalf("lookup_endpoint must serialise as an explicit null; got present=%v value=%s", ok, v)
	}

	binding := fields["binding"]
	if len(binding.Values) != 2 || binding.Values[0].Code != "hardcover" {
		t.Fatalf("an enum field must carry its options inline, in order; got %+v", binding.Values)
	}
	if binding.Units != nil {
		t.Fatal("an enum field must not carry units")
	}

	weight := fields["item_weight"]
	if weight.UnitFamily == nil || *weight.UnitFamily != "mass" {
		t.Fatalf("a measure field must name its unit family; got %v", weight.UnitFamily)
	}
	if weight.DefaultUnit == nil || *weight.DefaultUnit != "g" {
		t.Fatalf("a measure field must name its default unit; got %v", weight.DefaultUnit)
	}
	if len(weight.Units) != 2 || weight.Units[1].FactorToBase != 1000 {
		t.Fatalf("a measure field must carry the family's units and their conversion factors; got %+v",
			weight.Units)
	}
	if weight.Values != nil {
		t.Fatal("a measure field must not carry enum options")
	}
}

func TestAttributeSchemaScopeFilter(t *testing.T) {
	effective, options, units := booksFixture()
	state := &postgres.AttributeSchemaState{PublishedVersion: 1}

	item := buildAttributeSchema(uuid.New(), []string{"Books"}, AttributeScopeItem, state, effective, options, units)
	for _, g := range item.Groups {
		for _, f := range g.Attributes {
			if f.Scope != "item" {
				t.Fatalf("?scope=item returned %q, whose scope is %q — an offer field on a shared "+
					"item record is a fact about one seller stored against every seller's copy",
					f.Code, f.Scope)
			}
		}
	}

	offer := buildAttributeSchema(uuid.New(), []string{"Books"}, AttributeScopeOffer, state, effective, options, units)
	if len(offer.Groups) != 1 || offer.Groups[0].Name != "Offer" ||
		len(offer.Groups[0].Attributes) != 1 || offer.Groups[0].Attributes[0].Code != "condition" {
		t.Fatalf("?scope=offer should return only `condition`; got %+v", offer.Groups)
	}

	// An empty result is still an object with empty arrays, never nulls: a
	// client decoding `groups` into an array type must not fail on a category
	// that asks for nothing.
	empty := buildAttributeSchema(uuid.New(), nil, AttributeScopeAll, state, nil, nil, nil)
	if empty.Groups == nil || empty.VariationAxes == nil || empty.CategoryPath == nil {
		t.Fatalf("empty schema must carry empty arrays, not nulls: %+v", empty)
	}
}

// ─── ETag ───────────────────────────────────────────────────────────────

func TestAttributeSchemaETagChangesWithBothInputs(t *testing.T) {
	effective, options, units := booksFixture()
	cat := uuid.New()

	v3 := buildAttributeSchema(cat, []string{"Books"}, AttributeScopeAll,
		&postgres.AttributeSchemaState{PublishedVersion: 3}, effective, options, units)
	again := buildAttributeSchema(cat, []string{"Books"}, AttributeScopeAll,
		&postgres.AttributeSchemaState{PublishedVersion: 3}, effective, options, units)
	if v3.ETag != again.ETag {
		t.Fatalf("the same schema must produce the same ETag, got %q and %q", v3.ETag, again.ETag)
	}

	// A publish that changes nothing else still invalidates every cache. That
	// is the operation an operator performs precisely to force a refetch.
	v4 := buildAttributeSchema(cat, []string{"Books"}, AttributeScopeAll,
		&postgres.AttributeSchemaState{PublishedVersion: 4}, effective, options, units)
	if v4.ETag == v3.ETag {
		t.Fatal("a publish must change the ETag even when no definition changed")
	}

	// An edit to an option — not to the definition — must move it too. The
	// newest timestamp is taken over the options as well for exactly this.
	options2 := map[uuid.UUID][]*postgres.AttributeEnumValue{}
	for id, vals := range options {
		copied := make([]*postgres.AttributeEnumValue, len(vals))
		for i, v := range vals {
			cp := *v
			cp.UpdatedAt = cp.UpdatedAt.Add(time.Hour)
			copied[i] = &cp
		}
		options2[id] = copied
	}
	edited := buildAttributeSchema(cat, []string{"Books"}, AttributeScopeAll,
		&postgres.AttributeSchemaState{PublishedVersion: 3}, effective, options2, units)
	if edited.ETag == v3.ETag {
		t.Fatal("relabelling an enum option must change the ETag; it changes the rendered form")
	}
}

func TestETagMatches(t *testing.T) {
	tag := `W/"as-3-1756718400000000000"`
	cases := []struct {
		ifNoneMatch string
		want        bool
		why         string
	}{
		{"", false, "no header means the client has nothing cached"},
		{tag, true, "the exact tag it was given"},
		{`"as-3-1756718400000000000"`, true,
			"a cache that dropped the weak marker is still asking about the same entity"},
		{`W/"as-2-1756718400000000000", ` + tag, true,
			"the list form: a match anywhere in it is a match"},
		{"*", true, "the wildcard means any current representation"},
		{`W/"as-2-1756718400000000000"`, false, "an older version must get the body"},
	}
	for _, tc := range cases {
		if got := ETagMatches(tc.ifNoneMatch, tag); got != tc.want {
			t.Errorf("ETagMatches(%q) = %v, want %v — %s", tc.ifNoneMatch, got, tc.want, tc.why)
		}
	}
	if ETagMatches(tag, "") {
		t.Error("a response with no ETag can never be a 304")
	}
}
