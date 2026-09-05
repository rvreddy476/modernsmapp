package service

// Value validation: every way an answer can be wrong, and the ways it can be
// right that a stricter reading would break.
//
// These are the checks that decide what gets stored. The form the client
// renders carries the same bounds, but a client holding a cached schema, a
// bulk importer and a partner API are all sending values no form ever saw.

import (
	"strings"
	"testing"

	"github.com/atpost/commerce-service/internal/store/postgres"
)

func def(dataType string, tweak func(*postgres.AttributeDefinition)) *postgres.AttributeDefinition {
	d := &postgres.AttributeDefinition{
		Code: "field", Label: "Field", DataType: dataType,
		DisplayGroup: "Product Details", AppliesTo: "item", IsActive: true,
	}
	if tweak != nil {
		tweak(d)
	}
	return d
}

func f64(v float64) *float64 { return &v }
func ip(v int) *int          { return &v }
func sp(v string) *string    { return &v }

var noContext = attributeValueContext{enumCodes: map[string]bool{}, units: map[string]bool{}}

func enumCtx(codes ...string) attributeValueContext {
	c := attributeValueContext{enumCodes: map[string]bool{}, units: map[string]bool{}}
	for _, code := range codes {
		c.enumCodes[code] = true
	}
	return c
}

func unitCtx(units ...string) attributeValueContext {
	c := attributeValueContext{enumCodes: map[string]bool{}, units: map[string]bool{}}
	for _, u := range units {
		c.units[u] = true
	}
	return c
}

// ─── Refusals ───────────────────────────────────────────────────────────

func TestValidateAttributeValueRejectsEachWayAValueCanBeWrong(t *testing.T) {
	cases := []struct {
		name string
		def  *postgres.AttributeDefinition
		in   AttributeValueInput
		vc   attributeValueContext
		want string // substring of the reason
	}{
		// ── type ──
		{"text given a number", def("text", nil),
			AttributeValueInput{Value: 42}, noContext, "must be text"},
		{"boolean given a string", def("boolean", nil),
			AttributeValueInput{Value: "yes"}, noContext, "true or false"},
		{"integer given words", def("integer", nil),
			AttributeValueInput{Value: "many"}, noContext, "whole number"},
		{"integer given a fraction", def("integer", nil),
			AttributeValueInput{Value: 3.5}, noContext, "whole number"},
		{"date given a timestamp", def("date", nil),
			AttributeValueInput{Value: "2019-04-02T00:00:00Z"}, noContext, "YYYY-MM-DD"},
		{"date given nonsense", def("date", nil),
			AttributeValueInput{Value: "02/04/2019"}, noContext, "YYYY-MM-DD"},
		{"media given a non-uuid", def("media", nil),
			AttributeValueInput{Value: "not-a-uuid"}, noContext, "media id"},

		// ── range ──
		{"below min", def("integer", func(d *postgres.AttributeDefinition) { d.MinNum = f64(1) }),
			AttributeValueInput{Value: 0}, noContext, "at least 1"},
		{"above max", def("integer", func(d *postgres.AttributeDefinition) { d.MaxNum = f64(10000) }),
			AttributeValueInput{Value: 10001}, noContext, "at most 10000"},

		// ── length ──
		{"too short", def("text", func(d *postgres.AttributeDefinition) { d.MinLen = ip(3) }),
			AttributeValueInput{Value: "ab"}, noContext, "at least 3 characters"},
		{"too long", def("text", func(d *postgres.AttributeDefinition) { d.MaxLen = ip(5) }),
			AttributeValueInput{Value: "abcdef"}, noContext, "at most 5 characters"},
		{"blank", def("text", nil),
			AttributeValueInput{Value: "   "}, noContext, "must not be blank"},

		// ── regex ──
		{"fails the pattern", def("gtin", func(d *postgres.AttributeDefinition) { d.Regex = sp(`^[0-9]{10,14}$`) }),
			AttributeValueInput{Value: "978-81-265-5864"}, noContext, "required format"},

		// ── enum membership ──
		{"not an option", def("enum", nil),
			AttributeValueInput{Value: "spiral"}, enumCtx("paperback", "hardback"), "not one of this field"},
		{"multi_enum with a bad option", def("multi_enum", nil),
			AttributeValueInput{Value: []any{"en", "klingon"}}, enumCtx("en", "hi"), "not one of this field"},
		{"multi_enum given a scalar", def("multi_enum", nil),
			AttributeValueInput{Value: "en"}, enumCtx("en"), "must be a list"},
		{"multi_enum with a duplicate", def("multi_enum", nil),
			AttributeValueInput{Value: []any{"en", "en"}}, enumCtx("en"), "selected twice"},

		// ── max_values ──
		{"too many values", def("multi_enum", func(d *postgres.AttributeDefinition) { d.MaxValues = ip(2) }),
			AttributeValueInput{Value: []any{"en", "hi", "ta"}}, enumCtx("en", "hi", "ta"), "at most 2"},

		// ── units ──
		{"unit from the wrong family",
			def("measure", func(d *postgres.AttributeDefinition) { d.UnitFamily = sp("mass"); d.DefaultUnit = sp("g") }),
			AttributeValueInput{Value: 250, UnitCode: sp("cm")}, unitCtx("g", "kg"), "does not belong to the \"mass\""},
		{"unit on a field that is not a measure", def("text", nil),
			AttributeValueInput{Value: "red", UnitCode: sp("kg")}, noContext, "only a measure"},
		{"measure with no unit and no default",
			def("measure", func(d *postgres.AttributeDefinition) { d.UnitFamily = sp("mass") }),
			AttributeValueInput{Value: 250}, unitCtx("g"), "no default to fall back on"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateAttributeValue(tc.def, tc.in, tc.vc)
			if err == nil {
				t.Fatalf("must be refused, but it was accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("wrong reason:\n want to contain %q\n  got %q", tc.want, err.Error())
			}
		})
	}
}

// A refusal has to name the field. A form with twenty controls and one
// "invalid value" makes the seller guess which.
func TestValidateAttributeValueNamesTheField(t *testing.T) {
	d := def("integer", func(d *postgres.AttributeDefinition) { d.Code = "pages"; d.MaxNum = f64(10) })
	_, err := validateAttributeValue(d, AttributeValueInput{Value: 99}, noContext)
	var ve *AttributeValidationError
	if !asAttrErr(err, &ve) {
		t.Fatalf("want an AttributeValidationError, got %T: %v", err, err)
	}
	if ve.Field != "pages" {
		t.Fatalf("want the field named 'pages', got %q", ve.Field)
	}
}

func asAttrErr(err error, target **AttributeValidationError) bool {
	if e, ok := err.(*AttributeValidationError); ok {
		*target = e
		return true
	}
	return false
}

// ─── Acceptances ────────────────────────────────────────────────────────

// The cases a stricter reading would break, each of which is a real caller.
func TestValidateAttributeValueAcceptsTheLegitimateShapes(t *testing.T) {
	t.Run("a measure falls back to its default unit", func(t *testing.T) {
		d := def("measure", func(d *postgres.AttributeDefinition) {
			d.UnitFamily = sp("mass")
			d.DefaultUnit = sp("g")
		})
		rows, err := validateAttributeValue(d, AttributeValueInput{Value: 250}, unitCtx("g", "kg"))
		if err != nil {
			t.Fatalf("must be accepted: %v", err)
		}
		if rows[0].UnitCode == nil || *rows[0].UnitCode != "g" {
			t.Fatalf("want the default unit filled in, got %#v", rows[0].UnitCode)
		}
	})

	t.Run("a number arriving as a string from a CSV import", func(t *testing.T) {
		d := def("integer", nil)
		rows, err := validateAttributeValue(d, AttributeValueInput{Value: "328"}, noContext)
		if err != nil {
			t.Fatalf("a bulk import sends strings; refusing them fails every CSV: %v", err)
		}
		if *rows[0].ValueNum != 328 {
			t.Fatalf("want 328, got %v", *rows[0].ValueNum)
		}
	})

	t.Run("max_len counts runes, not bytes", func(t *testing.T) {
		// A byte-counting limit fails a Devanagari name well inside the
		// character limit the seller was shown, for reasons nothing on screen
		// explains. The limit is set to the string's exact rune length, which
		// is far below its byte length — so a byte count cannot pass this.
		name := "मुंशी प्रेम"
		runes, bytes := len([]rune(name)), len(name)
		if runes >= bytes {
			t.Fatalf("fixture is not multi-byte: %d runes, %d bytes", runes, bytes)
		}
		d := def("text", func(d *postgres.AttributeDefinition) { d.MaxLen = ip(runes) })
		if _, err := validateAttributeValue(d, AttributeValueInput{Value: name}, noContext); err != nil {
			t.Fatalf("%d runes (%d bytes) against a %d-character limit must be accepted: %v",
				runes, bytes, runes, err)
		}
		// One rune over is refused, so the limit is real and not just absent.
		d = def("text", func(d *postgres.AttributeDefinition) { d.MaxLen = ip(runes - 1) })
		if _, err := validateAttributeValue(d, AttributeValueInput{Value: name}, noContext); err == nil {
			t.Fatalf("a %d-character limit must refuse %d characters", runes-1, runes)
		}
	})

	t.Run("a multi_enum of exactly one", func(t *testing.T) {
		d := def("multi_enum", func(d *postgres.AttributeDefinition) { d.MaxValues = ip(5) })
		rows, err := validateAttributeValue(d, AttributeValueInput{Value: []any{"en"}}, enumCtx("en"))
		if err != nil {
			t.Fatalf("must be accepted: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("want one row, got %d", len(rows))
		}
	})

	t.Run("an empty multi_enum clears the field", func(t *testing.T) {
		d := def("multi_enum", nil)
		rows, err := validateAttributeValue(d, AttributeValueInput{Value: []any{}}, enumCtx("en"))
		if err != nil {
			t.Fatalf("unticking every option must be expressible: %v", err)
		}
		if len(rows) != 0 {
			t.Fatalf("want no rows, got %d", len(rows))
		}
	})

	t.Run("text is trimmed before it is measured and stored", func(t *testing.T) {
		d := def("text", func(d *postgres.AttributeDefinition) { d.MaxLen = ip(5) })
		rows, err := validateAttributeValue(d, AttributeValueInput{Value: "  abc  "}, noContext)
		if err != nil {
			t.Fatalf("must be accepted: %v", err)
		}
		if *rows[0].ValueText != "abc" {
			t.Fatalf("want %q, got %q", "abc", *rows[0].ValueText)
		}
	})

	t.Run("every scalar type lands in exactly one column", func(t *testing.T) {
		// The Go side of the CHECK constraint: whatever the data type, exactly
		// one value_* is set, or the INSERT is refused by the database.
		for _, tc := range []struct {
			dataType string
			in       AttributeValueInput
			vc       attributeValueContext
		}{
			{"text", AttributeValueInput{Value: "x"}, noContext},
			{"long_text", AttributeValueInput{Value: "x"}, noContext},
			{"gtin", AttributeValueInput{Value: "9788126558643"}, noContext},
			{"integer", AttributeValueInput{Value: 1}, noContext},
			{"decimal", AttributeValueInput{Value: 1.5}, noContext},
			{"money_minor", AttributeValueInput{Value: 12900}, noContext},
			{"boolean", AttributeValueInput{Value: true}, noContext},
			{"date", AttributeValueInput{Value: "2019-04-02"}, noContext},
			{"media", AttributeValueInput{Value: "0f8fad5b-d9cb-469f-a165-70867728950e"}, noContext},
			{"enum", AttributeValueInput{Value: "paperback"}, enumCtx("paperback")},
		} {
			d := def(tc.dataType, nil)
			rows, err := validateAttributeValue(d, tc.in, tc.vc)
			if err != nil {
				t.Fatalf("%s: %v", tc.dataType, err)
			}
			set := 0
			for _, isSet := range []bool{
				rows[0].ValueText != nil, rows[0].ValueNum != nil, rows[0].ValueBool != nil,
				rows[0].ValueDate != nil, rows[0].ValueMediaID != nil,
			} {
				if isSet {
					set++
				}
			}
			if set != 1 {
				t.Fatalf("%s: %d value columns set, the CHECK constraint allows exactly 1", tc.dataType, set)
			}
			if rows[0].UnitCode != nil {
				t.Fatalf("%s: a non-measure must not carry a unit", tc.dataType)
			}
		}
	})
}

// A definition whose stored regex does not compile is the DEFINITION's fault.
// Reporting it as "your ISBN is invalid" sends the seller to fix something
// that is not wrong.
func TestValidateAttributeValueBlamesTheDefinitionForABrokenRegex(t *testing.T) {
	d := def("text", func(d *postgres.AttributeDefinition) { d.Regex = sp("[unclosed") })
	_, err := validateAttributeValue(d, AttributeValueInput{Value: "anything"}, noContext)
	if err == nil {
		t.Fatalf("a broken pattern must not silently accept every value")
	}
	if !strings.Contains(err.Error(), "misconfigured") {
		t.Fatalf("want the definition blamed, got %q", err.Error())
	}
}

// An empty regex is "no pattern", not "a pattern that matches nothing". The
// difference is a field whose regex was cleared silently rejecting every value.
func TestCompileAttributeRegexTreatsEmptyAsNoPattern(t *testing.T) {
	for _, p := range []*string{nil, sp("")} {
		re, err := compileAttributeRegex(p)
		if err != nil {
			t.Fatalf("empty pattern must not error: %v", err)
		}
		if re != nil {
			t.Fatalf("empty pattern must compile to no pattern")
		}
	}
	d := def("text", func(d *postgres.AttributeDefinition) { d.Regex = sp("") })
	if _, err := validateAttributeValue(d, AttributeValueInput{Value: "anything"}, noContext); err != nil {
		t.Fatalf("a cleared regex must accept any value: %v", err)
	}
}

// The group order the detail page renders in is displayGroupOrder, and it is
// the form's order — identity first, then what the thing is, then logistics.
func TestDisplayGroupOrderPutsIdentityFirstAndTheUnknownLast(t *testing.T) {
	if groupSortOrder("Product Identity") >= groupSortOrder("Product Details") {
		t.Fatalf("identity must sort before details")
	}
	if groupSortOrder("Product Details") >= groupSortOrder("Logistics") {
		t.Fatalf("details must sort before logistics")
	}
	// An unknown group sorts LAST rather than first, so it cannot push
	// "Product Identity" below the fold.
	if groupSortOrder("Invented") <= groupSortOrder("Logistics") {
		t.Fatalf("an unknown group must sort last, got %d", groupSortOrder("Invented"))
	}
}
