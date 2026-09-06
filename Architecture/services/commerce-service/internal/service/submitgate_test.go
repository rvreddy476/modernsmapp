package service

// The parts of the submit gate that need no database: the reviewer diff, and
// the two rules the built-in list has to keep obeying.

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ─── The built-in list ──────────────────────────────────────────────────

// attributeCodeShape is the CHECK on attribute_definitions.code (migration
// 025). Copied here for one purpose: to prove a built-in code can never be
// one.
var attributeCodeShape = regexp.MustCompile(`^[a-z][a-z0-9_]{1,48}$`)

// A built-in and an attribute share one `fields` array in the 422, keyed by
// code. If a built-in code were a legal attribute code, an operator could
// define an attribute called `price`, and a client would have two entries
// with the same key and no way to tell which control each belongs under.
//
// The `.` in `listing.price` is what makes that impossible, and this is the
// test that stops somebody "tidying" it away.
func TestSubmitGateBuiltinCodesCannotCollideWithAnAttributeCode(t *testing.T) {
	for _, req := range builtinRequirements {
		if attributeCodeShape.MatchString(req.Code) {
			t.Errorf("built-in %q is a legal attribute code, so an operator could define an "+
				"attribute with the same code and the 422's fields array would carry two "+
				"entries under one key", req.Code)
		}
		if !strings.HasPrefix(req.Code, "listing.") {
			t.Errorf("built-in %q is not namespaced; a client cannot tell a listing fact from "+
				"a category answer", req.Code)
		}
		if req.Label == "" {
			t.Errorf("built-in %q has no label; a missing field has nothing on screen, so the "+
				"message is the only thing naming it", req.Code)
		}
	}
}

// completeListing is a listing with nothing wrong with it. Each case below
// breaks exactly ONE thing about it.
func completeListing() *postgres.ListingReadiness {
	return &postgres.ListingReadiness{
		VariantCount: 2, UnpricedVariants: 0, SellableUnits: 7,
		HasImage: true, TaxRateResolvable: true,
	}
}

// None of the built-ins may fire on a listing that has everything. A
// requirement that cannot be satisfied is a listing nobody can ever submit.
func TestSubmitGateNoBuiltinRefusesACompleteListing(t *testing.T) {
	r := completeListing()
	for _, req := range builtinRequirements {
		if !req.Satisfied(r) {
			t.Errorf("built-in %q refuses a listing that has everything: %s",
				req.Code, req.Reason(r))
		}
	}
}

// And each one must actually fire when its own fact is missing — a
// requirement that cannot be triggered is dead code pretending to be a rule.
//
// One break per case, so the test also proves the checks are INDEPENDENT: a
// listing with no image is not reported as having no price.
func TestSubmitGateEachBuiltinFiresOnItsOwnGapAndNoOther(t *testing.T) {
	cases := []struct {
		name   string
		code   string
		break_ func(*postgres.ListingReadiness)
		// alsoFires are the built-ins this break legitimately trips as well,
		// because the facts are not independent: a listing with no variants
		// has no stock either, and saying so is honest rather than noisy.
		alsoFires []string
	}{
		{"no variants", builtinVariant, func(r *postgres.ListingReadiness) {
			r.VariantCount, r.SellableUnits = 0, 0
		}, []string{builtinStock}},
		{"a variant with no price", builtinPrice, func(r *postgres.ListingReadiness) {
			r.UnpricedVariants = 1
		}, nil},
		{"nothing in stock", builtinStock, func(r *postgres.ListingReadiness) {
			r.SellableUnits = 0
		}, nil},
		{"no photograph", builtinImage, func(r *postgres.ListingReadiness) {
			r.HasImage = false
		}, nil},
		{"no usable GST class", builtinTaxClass, func(r *postgres.ListingReadiness) {
			r.TaxRateResolvable = false
		}, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := completeListing()
			tc.break_(r)

			expected := map[string]bool{tc.code: true}
			for _, c := range tc.alsoFires {
				expected[c] = true
			}
			for _, req := range builtinRequirements {
				fired := !req.Satisfied(r)
				if fired != expected[req.Code] {
					t.Errorf("built-in %q fired=%v, want %v", req.Code, fired, expected[req.Code])
				}
				if fired && strings.TrimSpace(req.Reason(r)) == "" {
					t.Errorf("built-in %q refuses with an empty sentence", req.Code)
				}
			}
		})
	}
}

// One unit anywhere is enough. A sold-out size on a shirt whose other sizes
// are in stock is a normal listing, and a gate that refused it would turn
// restocking into a re-listing.
func TestSubmitGateStockNeedsOneUnitAnywhereNotOnePerVariant(t *testing.T) {
	r := &postgres.ListingReadiness{VariantCount: 4, SellableUnits: 1, HasImage: true, TaxRateResolvable: true}
	for _, req := range builtinRequirements {
		if req.Code != builtinStock {
			continue
		}
		if !req.Satisfied(r) {
			t.Fatalf("a listing with one sellable unit across four variants was refused: %s",
				req.Reason(r))
		}
	}
}

// ─── The diff ───────────────────────────────────────────────────────────

func snap(vals ...postgres.SubmissionValue) []postgres.SubmissionValue { return vals }

func attr(code, label string, v any) postgres.SubmissionValue {
	return postgres.SubmissionValue{Code: code, Label: label, Kind: "attribute", Value: v}
}

func builtin(code, label string, v any) postgres.SubmissionValue {
	return postgres.SubmissionValue{Code: code, Label: label, Kind: "builtin", Value: v}
}

func submission(attempt, version int, s []postgres.SubmissionValue) *postgres.ProductSubmission {
	return &postgres.ProductSubmission{
		Attempt: attempt, SchemaVersion: version, Snapshot: s,
		CreatedAt: time.Now().Add(-time.Duration(attempt) * time.Hour),
	}
}

func changeFor(t *testing.T, d *SubmissionDiff, code string) SubmissionFieldChange {
	t.Helper()
	for _, c := range d.Changes {
		if c.Code == code {
			return c
		}
	}
	t.Fatalf("no change reported for %q; got %+v", code, d.Changes)
	return SubmissionFieldChange{}
}

// A first submission has nothing to compare against, and that is a different
// answer from "nothing changed" — both produce an empty Changes list, so the
// flag is the only thing that separates them.
func TestSubmissionDiffFirstSubmissionSaysSoRatherThanShowingNoChanges(t *testing.T) {
	d := diffSubmissions(uuid.New(), []*postgres.ProductSubmission{
		submission(1, 3, snap(attr("pages", "Pages", nil))),
	})
	if !d.FirstSubmission {
		t.Fatal("a single submission must be reported as the first, not as an unchanged one")
	}
	if len(d.Changes) != 0 {
		t.Fatalf("a first submission cannot have changes: %+v", d.Changes)
	}
	if d.Previous != nil {
		t.Fatal("a first submission has no previous attempt")
	}
}

// The shape a rejected-then-resubmitted listing produces: one field filled
// in, one corrected, one cleared, and the rest untouched. A reviewer must see
// exactly those three and nothing else.
func TestSubmissionDiffReportsAddedChangedAndRemovedWithLabels(t *testing.T) {
	prev := submission(1, 4, snap(
		builtin(builtinStock, "Stock", 0),
		attr("author", "Author", "R K Narayan"),
		attr("pages", "Pages", float64(328)),
		attr("isbn", "ISBN", nil),
		attr("edition", "Edition", "1st"),
	))
	cur := submission(2, 4, snap(
		builtin(builtinStock, "Stock", 40),
		attr("author", "Author", "R K Narayan"),
		attr("pages", "Pages", float64(412)),
		attr("isbn", "ISBN", "9780143039655"),
		attr("edition", "Edition", nil),
	))

	d := diffSubmissions(uuid.New(), []*postgres.ProductSubmission{cur, prev})
	if d.FirstSubmission {
		t.Fatal("two submissions is not a first submission")
	}
	if len(d.Changes) != 4 {
		t.Fatalf("want stock, pages, isbn and edition and nothing else, got %+v", d.Changes)
	}

	if c := changeFor(t, d, "isbn"); c.Change != "added" || c.Before != nil || c.After != "9780143039655" {
		t.Errorf("a field filled in must read as added: %+v", c)
	}
	if c := changeFor(t, d, "pages"); c.Change != "changed" {
		t.Errorf("a corrected value must read as changed: %+v", c)
	}
	if c := changeFor(t, d, "edition"); c.Change != "removed" || c.After != nil {
		t.Errorf("a cleared field must read as removed, not as a change to null: %+v", c)
	}

	// Labels, because a reviewer reading `isbn: null → "978…"` at 11pm should
	// not have to know what `isbn` is.
	for _, c := range d.Changes {
		if c.Label == "" {
			t.Errorf("change on %q carries no label", c.Code)
		}
	}

	// Built-ins first: they are the listing's own facts and sit at the top of
	// every editor, so a reviewer reading top to bottom walks the same path
	// the seller's eye does.
	if d.Changes[0].Kind != "builtin" {
		t.Errorf("built-ins must sort first, got %+v", d.Changes)
	}
	// And "author" did not change, so it must not be in the list at all —
	// that is the entire point of a diff over a re-read.
	for _, c := range d.Changes {
		if c.Code == "author" {
			t.Error("an unchanged field appeared in the diff")
		}
	}
}

// A multi_enum is a SET. Unticking "Hindi" and re-ticking it is not a change,
// and a reviewer told it was would go looking for one that is not there.
func TestSubmissionDiffTreatsAMultiValuedAnswerAsASet(t *testing.T) {
	prev := submission(1, 2, snap(attr("languages", "Languages", []any{"en", "hi"})))
	cur := submission(2, 2, snap(attr("languages", "Languages", []any{"hi", "en"})))
	d := diffSubmissions(uuid.New(), []*postgres.ProductSubmission{cur, prev})
	if len(d.Changes) != 0 {
		t.Fatalf("reordering a multi-valued answer is not a change: %+v", d.Changes)
	}

	real := submission(2, 2, snap(attr("languages", "Languages", []any{"hi", "en", "ta"})))
	d2 := diffSubmissions(uuid.New(), []*postgres.ProductSubmission{real, prev})
	if len(d2.Changes) != 1 {
		t.Fatalf("adding a language IS a change: %+v", d2.Changes)
	}
}

// A number that went out as an int comes back from JSONB as a float64.
// Comparing with == would report 328 != 328.0 and fill a reviewer's diff with
// changes nobody made.
func TestSubmissionDiffSurvivesTheJSONNumberRoundTrip(t *testing.T) {
	prev := submission(1, 1, snap(attr("pages", "Pages", 328)))
	cur := submission(2, 1, snap(attr("pages", "Pages", float64(328))))
	d := diffSubmissions(uuid.New(), []*postgres.ProductSubmission{cur, prev})
	if len(d.Changes) != 0 {
		t.Fatalf("328 and 328.0 are the same page count: %+v", d.Changes)
	}
}

// A field the schema ADDED between two attempts, still unanswered, is not
// something the seller did. It must not appear as a change — but the version
// bump must be flagged, so a reviewer can explain a field that did appear.
func TestSubmissionDiffFlagsASchemaVersionBumpAndHidesTheNoiseItCauses(t *testing.T) {
	prev := submission(1, 4, snap(attr("author", "Author", "R K Narayan")))
	cur := submission(2, 5, snap(
		attr("author", "Author", "R K Narayan"),
		attr("binding", "Binding", nil),
	))
	d := diffSubmissions(uuid.New(), []*postgres.ProductSubmission{cur, prev})
	if !d.SchemaVersionChanged {
		t.Error("the two attempts were judged by different schema versions and the diff did not say so")
	}
	if len(d.Changes) != 0 {
		t.Fatalf("an unanswered new field is not a change the seller made: %+v", d.Changes)
	}
}

func TestBlankAnswerTreatsWhitespaceAndEmptyListsAsNoAnswer(t *testing.T) {
	for _, v := range []any{nil, "", "   ", []any{}} {
		if !blankAnswer(v) {
			t.Errorf("%#v should count as no answer; otherwise the space bar satisfies the gate", v)
		}
	}
	for _, v := range []any{"x", 0, false, []any{"en"}} {
		if blankAnswer(v) {
			t.Errorf("%#v is an answer", v)
		}
	}
}

// The refusal's own sentence has to name the count and the codes: it is what
// lands in a log when a client does not render the fields array.
func TestProductIncompleteErrorNamesEveryField(t *testing.T) {
	err := &ProductIncompleteError{Fields: []AttributeFieldError{
		{Code: builtinPrice, Label: "Price", Reason: "no price"},
		{Code: "pages", Label: "Pages", Reason: "required"},
	}}
	msg := err.Error()
	for _, want := range []string{"2 field(s)", builtinPrice, "pages"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal message %q does not mention %q", msg, want)
		}
	}
}
