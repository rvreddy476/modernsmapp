package service

// The completeness gate: the one moment a listing is asked whether it is
// FINISHED, plus what a reviewer sees on the second attempt and what happens
// to live listings when a rule tightens under them.
//
// ─── VALIDATION AT WRITE, COMPLETENESS AT SUBMIT ────────────────────────
//
// productwrite.go argues the first half: every value a seller sends is
// checked when it is written, because a wrong value does not fail loudly
// later — `pages: "many"` is simply a book that never appears under a
// page-count filter, and nobody reports that.
//
// This file is the second half, and it is the same argument from the other
// end. A create that demanded every required field would make drafts
// impossible, and a seller who cannot save a half-filled form does not go
// away and come back with the missing data: they type "n/a", "TBD" and "0"
// into fourteen controls to get past the gate. The catalogue then fills with
// values that are individually valid and collectively worthless, which is
// strictly worse than an empty field, because an empty field is visibly
// empty and "TBD" is not.
//
// So the question is asked exactly once, here, at the moment the listing
// claims to be ready — and when the answer is no, EVERY missing field comes
// back at once. A seller with six gaps who is told about one of them makes
// six round trips through a form, and by the fourth they are typing "n/a".
//
// ─── AND WHAT "REQUIRED" MEANS ──────────────────────────────────────────
//
// Two sources, and only two:
//
//  1. The category's effective schema — whatever `is_required` says on the
//     nearest binding in the category chain. The founder authors that; this
//     file does not second-guess it.
//
//  2. The built-ins below. Each one is on the list because the listing
//     CANNOT BE SOLD without it — not because it would be nice to have, not
//     because a reviewer would want it, not because a complete listing looks
//     better. The justification for each is in builtinRequirements, and
//     anything that cannot be justified that way does not belong in a gate
//     whose refusal blocks a seller from trading.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ─── The refusal ────────────────────────────────────────────────────────

// ProductIncompleteError is the submit gate's refusal, carrying EVERY gap.
//
// It reuses AttributeFieldError, and therefore the exact envelope
// AttributeValuesInvalidError already puts on the wire —
// `{"fields":[{"code":…,"reason":…}]}` — because a client already has the
// code to render that: it drew the create form from the schema, it keys its
// controls by attribute code, and it already knows how to put a `reason`
// under one. A second, parallel shape for "this field is missing" versus
// "this field is wrong" would have made it write that renderer twice.
//
// `label` is populated here and left empty on the write path's refusals. A
// missing field has no value on screen for the seller to look at, so the
// message may be the only thing naming it, and "hsn_code is required" is a
// worse sentence than "HSN Code is required".
type ProductIncompleteError struct {
	Fields []AttributeFieldError
}

func (e *ProductIncompleteError) Error() string {
	parts := make([]string, 0, len(e.Fields))
	for _, f := range e.Fields {
		parts = append(parts, f.Code+": "+f.Reason)
	}
	return fmt.Sprintf("commerce: this listing is not ready to submit; %d field(s) still needed: %s",
		len(e.Fields), strings.Join(parts, "; "))
}

// ─── The built-ins ──────────────────────────────────────────────────────

// Built-in codes are namespaced with a `.`, which
// `attribute_definitions.code` forbids (`^[a-z][a-z0-9_]{1,48}$`). So a
// built-in can never collide with an attribute code in the 422's `fields`
// array, and a client can tell the two apart without being told which is
// which — the ones with a dot are facts about the listing, the ones without
// are answers to its category's form.
const (
	builtinPrice    = "listing.price"
	builtinStock    = "listing.stock"
	builtinImage    = "listing.image"
	builtinTaxClass = "listing.tax_class"
	builtinVariant  = "listing.variant"
)

// builtinRequirement is one non-schema thing a listing must have.
type builtinRequirement struct {
	Code  string
	Label string
	// Satisfied reports whether the listing has it.
	Satisfied func(*postgres.ListingReadiness) bool
	// Reason is what the seller is told when it does not.
	Reason func(*postgres.ListingReadiness) string
}

// builtinRequirements IS the list, and the comment on each entry is the
// justification the list exists to be held to.
//
// ─── THE TEST EACH ONE HAD TO PASS ──────────────────────────────────────
//
// "Name the line of code that refuses to sell this listing." Not "a reviewer
// would reject it", not "the page looks bad", not "we will want it later" —
// an actual refusal, in this service, on the path from a buyer tapping Buy to
// money moving. If there is no such line, the field is not a completeness
// requirement; it is a wish, and a wish that blocks a seller from trading is
// how a marketplace loses its supply side.
//
// ─── WHAT WAS CONSIDERED AND LEFT OUT ───────────────────────────────────
//
//	description        A listing with no description sells. The buyer may
//	                   regret it; the checkout does not care. Not a gate.
//	weight_grams       Feels required — it is a shipping input. But
//	                   Service.CheckServiceability defaults to 0.5 kg when it
//	                   is absent ("sensible default until catalog enforces a
//	                   weight"), so the quote succeeds and the parcel ships.
//	                   The COST is wrong, which is a real problem and a real
//	                   thing to warn a seller about — but it is not
//	                   unsellable, so it does not belong in a refusal.
//	hsn_code           Statutorily required on a GST invoice. But nothing in
//	                   this service refuses a sale without it: the rate comes
//	                   from tax_class_id. Making it a gate here would be this
//	                   file inventing a legal opinion; if it must be
//	                   mandatory it belongs in the category schema, which the
//	                   founder authors and this gate already enforces.
//	brand, country     Neither is read by any sale path.
//	   of origin
//	dimensions         Same as weight: the courier quote has a fallback.
//	category_id        A product with no category has no schema, so it has no
//	                   required attributes either — and it is entirely
//	                   sellable. Refusing it here would be a rule about
//	                   tidiness, not about trade. (A category IS required to
//	                   answer any attribute at all; resolveAttributeValues
//	                   says so at write time, where it belongs.)
var builtinRequirements = []builtinRequirement{
	{
		// A listing with no variants has nothing a buyer can put in a cart:
		// `cart_items` keys on `variant_id`, and every price, stock and SKU
		// fact in this service hangs off a variant. Checked first because it
		// makes the other two variant-derived refusals legible — "no price"
		// on a listing with no variants would send the seller looking for a
		// price field that is not on screen.
		Code:      builtinVariant,
		Label:     "Variants",
		Satisfied: func(r *postgres.ListingReadiness) bool { return r.VariantCount > 0 },
		Reason: func(*postgres.ListingReadiness) string {
			return "this listing has no active variant, so there is nothing a buyer can add to a cart"
		},
	},
	{
		// REFUSED BY: postgres.ErrPriceNotPositive, and lockAndPriceLines
		// inside the checkout transaction. A variant with no positive
		// selling_price_minor is a buyable row that fails at the till.
		//
		// Every variant, not merely one, because each variant is separately
		// purchasable — a shirt whose XL has no price is a listing that
		// works for three sizes and 400s on the fourth.
		Code:      builtinPrice,
		Label:     "Price",
		Satisfied: func(r *postgres.ListingReadiness) bool { return r.UnpricedVariants == 0 },
		Reason: func(r *postgres.ListingReadiness) string {
			return fmt.Sprintf("%d variant(s) have no selling price; a variant priced at zero "+
				"is refused at checkout, so it is a row a buyer can reach and cannot buy",
				r.UnpricedVariants)
		},
	},
	{
		// REFUSED BY: postgres.OutOfStockError, raised by the stock lock in
		// the checkout transaction. Nothing hides a zero-stock listing —
		// productSummaryLive is `status='active' AND approval_status='approved'`
		// and says nothing about inventory — so the storefront shows it, the
		// buyer taps Buy, and the checkout refuses.
		//
		// One unit ANYWHERE is enough. Requiring every variant to be in stock
		// would make a sold-out size a submission blocker, and restocking is
		// an inventory action, not a re-listing.
		Code:      builtinStock,
		Label:     "Stock",
		Satisfied: func(r *postgres.ListingReadiness) bool { return r.SellableUnits > 0 },
		Reason: func(*postgres.ListingReadiness) string {
			return "no variant has a unit available to sell; the storefront does not hide a " +
				"zero-stock listing, so a buyer reaches it and the checkout refuses"
		},
	},
	{
		// REFUSED BY: nothing — and that is exactly the problem. There is no
		// error path at all: hydration finds no media id, the client draws a
		// placeholder, and the listing sits in the grid as a grey box next to
		// competitors that have photographs. It is the one entry on this list
		// whose failure is silent, which is why it is on it.
		//
		// Either source counts. `primary_image_media_id` is what the original
		// single-image create wrote; `product_media` is what the gallery
		// editor writes and the single-image column never gets. Requiring the
		// column specifically would refuse a listing with eight photographs.
		Code:      builtinImage,
		Label:     "Product image",
		Satisfied: func(r *postgres.ListingReadiness) bool { return r.HasImage },
		Reason: func(*postgres.ListingReadiness) string {
			return "this listing has no image; it would render as a blank tile in every grid, " +
				"and nothing anywhere reports that as an error"
		},
	},
	{
		// REFUSED BY: postgres.ErrTaxClassMissing / ErrTaxClassInvalid, from
		// rateFromClass, which the edge reports as 409
		// PRODUCT_TAX_UNCONFIGURED. `products.tax_class_id` is nullable and
		// the create route does not require it, so a listing genuinely can
		// reach the queue without one.
		//
		// The check is "a rate can be DERIVED", not "the id is not null": a
		// dangling id and a class whose percentages are all NULL both reach
		// the same refusal, and a gate that only tested for NULL would pass a
		// listing that cannot be sold.
		Code:      builtinTaxClass,
		Label:     "GST tax class",
		Satisfied: func(r *postgres.ListingReadiness) bool { return r.TaxRateResolvable },
		Reason: func(*postgres.ListingReadiness) string {
			return "no GST tax class from which a rate can be derived; checkout refuses a line " +
				"whose tax it cannot compute, so this listing cannot be bought"
		},
	},
}

// ─── The gate ───────────────────────────────────────────────────────────

// submissionReadiness is what one pass of the gate worked out: the gaps, and
// the snapshot of what IS there.
//
// Both come out of the same pass because they are two views of the same read.
// Computing the snapshot separately would mean reading the listing twice and
// would leave a window in which the values the gate judged are not the values
// it recorded.
type submissionReadiness struct {
	Missing  []AttributeFieldError
	Snapshot []postgres.SubmissionValue
}

// checkListingComplete reads everything the gate judges and reports every gap
// at once.
//
// The order of the returned fields is the order a seller's form draws them:
// built-ins first (they are the listing's own facts and they sit at the top
// of every editor), then the category's attributes in their effective group
// and sort order. A client rendering the refusal top to bottom walks the same
// path the seller's eye does.
func (s *Service) checkListingComplete(
	ctx context.Context, product *postgres.Product,
) (*submissionReadiness, error) {

	out := &submissionReadiness{
		Missing:  []AttributeFieldError{},
		Snapshot: []postgres.SubmissionValue{},
	}

	// ── The built-ins ───────────────────────────────────────
	readiness, err := s.store.ProductListingReadiness(ctx, product.ID)
	if err != nil {
		return nil, err
	}
	for _, req := range builtinRequirements {
		ok := req.Satisfied(readiness)
		if !ok {
			out.Missing = append(out.Missing, AttributeFieldError{
				Code: req.Code, Label: req.Label, Reason: req.Reason(readiness),
			})
		}
		out.Snapshot = append(out.Snapshot, postgres.SubmissionValue{
			Code: req.Code, Label: req.Label, Kind: "builtin",
			Value: builtinSnapshotValue(req.Code, readiness),
		})
	}

	// ── The category's form ─────────────────────────────────
	//
	// No category, no required attributes. That is not a gap: an
	// uncategorised listing is sellable, and resolveAttributeValues already
	// refuses any ANSWER sent without a category at write time.
	if product.CategoryID == nil {
		return out, nil
	}

	effective, err := s.store.EffectiveCategoryAttributes(ctx, *product.CategoryID)
	if err != nil {
		return nil, err
	}
	values, err := s.ProductAttributeValues(ctx, product.ID)
	if err != nil {
		return nil, err
	}
	answered := make(map[string]any, len(values))
	for _, v := range values {
		if !blankAnswer(v.Value) {
			answered[v.Code] = v.Value
		}
	}

	for _, ea := range effective {
		code := ea.Definition.Code
		val, has := answered[code]
		if ea.IsRequired && !has {
			out.Missing = append(out.Missing, AttributeFieldError{
				Code:  code,
				Label: ea.Definition.Label,
				Reason: fmt.Sprintf("%s requires this field; it has no value",
					categoryPhrase(ea)),
			})
		}
		// Every effective field goes in the snapshot, answered or not. A
		// reviewer diff has to be able to show a field going from nothing to
		// something — that is the single most common change between a
		// rejection and the re-submission that answers it — and a snapshot
		// holding only the answered fields could not.
		out.Snapshot = append(out.Snapshot, postgres.SubmissionValue{
			Code: code, Label: ea.Definition.Label, Kind: "attribute", Value: val,
		})
	}
	return out, nil
}

// categoryPhrase says WHERE the requirement comes from.
//
// A seller looking at "Author is required" on a listing filed under
// "Books › Textbooks › Physics" needs to know the rule is Books', not
// Physics', or they will go looking for a setting on the wrong category. The
// depth is the only thing that can explain it — see
// postgres.EffectiveAttribute.
func categoryPhrase(ea *postgres.EffectiveAttribute) string {
	if ea.Depth == 0 {
		return "this listing's category"
	}
	return fmt.Sprintf("a parent category %d level(s) up", ea.Depth)
}

// blankAnswer treats an empty string and an empty list as no answer.
//
// The write path already refuses a blank string for a text field, so this is
// belt and braces for the pre-registry estate and for the legacy rows a
// bulk import wrote — a listing carrying `author: ""` has not answered the
// question, and passing it would let a seller satisfy the gate with the
// space bar.
func blankAnswer(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(t) == ""
	case []any:
		return len(t) == 0
	default:
		return false
	}
}

// builtinSnapshotValue records WHAT the listing had, not merely whether it
// passed.
//
// A boolean snapshot would make the diff useless on exactly the fields it
// matters most for: "Stock: true → true" tells a reviewer nothing, and
// "Stock: 0 → 40" tells them the seller restocked.
func builtinSnapshotValue(code string, r *postgres.ListingReadiness) any {
	switch code {
	case builtinVariant:
		return r.VariantCount
	case builtinPrice:
		// The cheapest variant's paise — a number a reviewer recognises. NOT
		// the count of unpriced variants, which is what the gate actually
		// judges: "Price: 0 → 0" would be read by every human who saw it as a
		// price of zero rather than as "nothing unpriced, either time".
		//
		// One figure rather than every variant's, because the diff is about
		// the LISTING; dumping a matrix of paise into it makes a wall a
		// reviewer skips.
		return r.LowestPriceMinor
	case builtinStock:
		return r.SellableUnits
	case builtinImage:
		return r.HasImage
	case builtinTaxClass:
		return r.TaxRateResolvable
	default:
		return nil
	}
}

// ─── Submit ─────────────────────────────────────────────────────────────

// SubmitProduct submits a product for admin review, refusing an incomplete
// one.
//
// Order: ownership, then completeness, then the state change. Ownership
// first for the reason UpdateProduct puts it first — a stranger must not
// learn from a refusal's detail which of someone else's listings are
// unfinished. Completeness before the state change because a listing that
// fails the gate has not been submitted at all, and moving it to `submitted`
// and back would show in the seller's dashboard as a listing that went to
// review and returned rejected in the same second.
func (s *Service) SubmitProduct(ctx context.Context, productID, userID uuid.UUID) error {
	sel, err := s.store.GetSellerByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, postgres.ErrNoSellerRow) {
			return ErrNoSellerProfile
		}
		return fmt.Errorf("seller not found")
	}
	product, err := s.store.GetProductByID(ctx, productID)
	if err != nil {
		return err
	}
	if product.SellerID != sel.ID {
		return ErrNotProductOwner
	}

	ready, err := s.checkListingComplete(ctx, product)
	if err != nil {
		return err
	}
	if len(ready.Missing) > 0 {
		return &ProductIncompleteError{Fields: ready.Missing}
	}

	// The version the gate judged against, recorded with the submission. A
	// reviewer looking at a re-submission six weeks later needs to know
	// whether the rules moved between the two attempts, and the product row's
	// own schema_version only tells them about the last time its VALUES were
	// written.
	version, err := s.publishedSchemaVersion(ctx)
	if err != nil {
		return err
	}

	actor := userID
	if err := s.store.SubmitProductForReview(
		ctx, productID, sel.ID, &actor, version, ready.Snapshot); err != nil {
		return err
	}
	s.publish(ctx, "commerce.product.submitted", map[string]any{
		"product_id": productID, "seller_id": sel.ID,
	})
	// A submit is a visibility transition too, even though the usual case —
	// a draft going to review — was never visible in the first place. It is
	// published anyway rather than guarded by "was it live before?", because
	// the guard would be a second copy of the visibility rule with a
	// before-and-after of its own to get wrong, and the consumer's read-back
	// makes an event about an already-invisible product a no-op delete
	// rather than damage. See searchdoc.go.
	s.publishProductVisibility(ctx, productID)
	return nil
}

// ProductReadiness reports what a listing still needs, WITHOUT submitting it.
//
// Exposed for the same reason SellerReadiness is: a seller should see the
// remaining checklist on the editor rather than pressing Submit and being
// told no. A gate whose answer is only reachable by failing is a gate that
// teaches people to press the button and see what happens.
func (s *Service) ProductReadiness(ctx context.Context, productID, userID uuid.UUID) ([]AttributeFieldError, error) {
	sel, err := s.store.GetSellerByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, postgres.ErrNoSellerRow) {
			return nil, ErrNoSellerProfile
		}
		return nil, err
	}
	product, err := s.store.GetProductByID(ctx, productID)
	if err != nil {
		return nil, err
	}
	if product.SellerID != sel.ID {
		return nil, ErrNotProductOwner
	}
	ready, err := s.checkListingComplete(ctx, product)
	if err != nil {
		return nil, err
	}
	return ready.Missing, nil
}

// ─── The reviewer diff ──────────────────────────────────────────────────

// SubmissionRef identifies one attempt without repeating its whole snapshot.
type SubmissionRef struct {
	Attempt       int        `json:"attempt"`
	SubmittedAt   time.Time  `json:"submitted_at"`
	SubmittedBy   *uuid.UUID `json:"submitted_by,omitempty"`
	SchemaVersion int        `json:"schema_version"`
}

// SubmissionFieldChange is one line of the diff.
//
// `Label` sits beside `Code` for the same reason it does everywhere else in
// this service: the code is what a client keys on, the label is what a human
// reads, and a reviewer reading `hsn_code: null → "6109"` at 11pm should not
// have to know what `hsn_code` is.
//
// `Change` is spelled out rather than left to be inferred from before/after
// being null, because "the seller cleared this field" and "the seller filled
// it in" are opposite actions and a reviewer skimming forty rows must not
// have to work out which is which from a null.
type SubmissionFieldChange struct {
	Code   string `json:"code"`
	Label  string `json:"label"`
	Kind   string `json:"kind"`
	Change string `json:"change"` // added | removed | changed
	Before any    `json:"before"`
	After  any    `json:"after"`
}

// SubmissionDiff is what changed between the two most recent submissions.
//
// ─── WHY A DIFF AND NOT THE LISTING ─────────────────────────────────────
//
// A reviewer who rejected a listing for one bad photograph and is handed the
// whole listing again has to re-read all of it to find out whether the
// photograph changed — and, more importantly, to find out whether anything
// ELSE changed while they were not looking. The second is the part that
// matters: "fix the photo, and quietly rewrite the description" is the
// obvious way to walk a listing past a reviewer, and it is invisible in a
// full re-read of a listing nobody memorised.
type SubmissionDiff struct {
	ProductID uuid.UUID      `json:"product_id"`
	Current   *SubmissionRef `json:"current,omitempty"`
	// Previous is nil on a first submission. `FirstSubmission` says so
	// explicitly rather than leaving the client to infer it from a null,
	// because "nothing changed" and "there is nothing to compare against"
	// are different answers and both produce an empty Changes list.
	Previous        *SubmissionRef          `json:"previous,omitempty"`
	FirstSubmission bool                    `json:"first_submission"`
	Changes         []SubmissionFieldChange `json:"changes"`
	// SchemaVersionChanged is true when the two attempts were judged by
	// different published schema versions. A field that appears in the newer
	// snapshot and not the older one is then explained by the rules moving
	// rather than by the seller doing anything.
	SchemaVersionChanged bool `json:"schema_version_changed"`
}

// ProductSubmissionHistory is the read model behind the admin queue's
// endpoint: the recent attempts, and the diff between the last two.
func (s *Service) ProductSubmissionHistory(
	ctx context.Context, productID uuid.UUID, limit int,
) ([]*postgres.ProductSubmission, *SubmissionDiff, error) {
	subs, err := s.store.ProductSubmissions(ctx, productID, limit)
	if err != nil {
		return nil, nil, err
	}
	return subs, diffSubmissions(productID, subs), nil
}

// diffSubmissions compares the two newest attempts.
//
// Pure, and takes the slice rather than the store, so the comparison itself
// is unit-testable without a database — it is the part with the logic in it.
func diffSubmissions(productID uuid.UUID, subs []*postgres.ProductSubmission) *SubmissionDiff {
	d := &SubmissionDiff{ProductID: productID, Changes: []SubmissionFieldChange{}}
	if len(subs) == 0 {
		d.FirstSubmission = true
		return d
	}
	cur := subs[0]
	d.Current = refOf(cur)
	if len(subs) == 1 {
		d.FirstSubmission = true
		return d
	}
	prev := subs[1]
	d.Previous = refOf(prev)
	d.SchemaVersionChanged = cur.SchemaVersion != prev.SchemaVersion

	// Keyed on (kind, code): a built-in and an attribute sharing a code must
	// never be compared against each other. They cannot share one today —
	// built-ins carry a `.` the attribute code CHECK forbids — and keying on
	// the pair means that stays true if either vocabulary ever changes.
	type key struct{ kind, code string }
	before := make(map[key]postgres.SubmissionValue, len(prev.Snapshot))
	for _, v := range prev.Snapshot {
		before[key{v.Kind, v.Code}] = v
	}

	seen := map[key]bool{}
	for _, after := range cur.Snapshot {
		k := key{after.Kind, after.Code}
		seen[k] = true
		old, had := before[k]
		switch {
		case !had:
			// The field is on the newer form and was not on the older one.
			// Only interesting when it now carries something: a field the
			// schema added between the two attempts, still unanswered, is
			// noise on a reviewer's screen.
			if blankAnswer(after.Value) {
				continue
			}
			d.Changes = append(d.Changes, change(after, "added", nil, after.Value))
		case blankAnswer(old.Value) && !blankAnswer(after.Value):
			d.Changes = append(d.Changes, change(after, "added", nil, after.Value))
		case !blankAnswer(old.Value) && blankAnswer(after.Value):
			d.Changes = append(d.Changes, change(after, "removed", old.Value, nil))
		case !sameAnswer(old.Value, after.Value):
			d.Changes = append(d.Changes, change(after, "changed", old.Value, after.Value))
		}
	}
	// A field that was answered before and is not on the form now. The
	// category moved, or the schema dropped the field — either way the
	// reviewer is looking at a listing that no longer states something it
	// used to, and that is a change.
	for _, old := range prev.Snapshot {
		k := key{old.Kind, old.Code}
		if seen[k] || blankAnswer(old.Value) {
			continue
		}
		d.Changes = append(d.Changes, change(old, "removed", old.Value, nil))
	}

	// Built-ins first, then attributes alphabetically. Stable, so two reads
	// of the same diff never disagree about the order — a reviewer comparing
	// their screen with a colleague's should be looking at the same list.
	sort.SliceStable(d.Changes, func(i, j int) bool {
		a, b := d.Changes[i], d.Changes[j]
		if (a.Kind == "builtin") != (b.Kind == "builtin") {
			return a.Kind == "builtin"
		}
		return a.Code < b.Code
	})
	return d
}

func change(v postgres.SubmissionValue, kind string, before, after any) SubmissionFieldChange {
	return SubmissionFieldChange{
		Code: v.Code, Label: v.Label, Kind: v.Kind,
		Change: kind, Before: before, After: after,
	}
}

func refOf(s *postgres.ProductSubmission) *SubmissionRef {
	return &SubmissionRef{
		Attempt: s.Attempt, SubmittedAt: s.CreatedAt,
		SubmittedBy: s.SubmittedBy, SchemaVersion: s.SchemaVersion,
	}
}

// sameAnswer compares two snapshot values that have been through JSON.
//
// Through JSON is the whole difficulty: a page count stored as an int comes
// back as a float64, and a multi_enum comes back as []any. Comparing with ==
// would panic on the slice and report 328 != 328.0 on the int, so every value
// is rendered to its display string and those are compared. That is also the
// right SEMANTICS for this particular comparison: the diff exists to show a
// human what changed on their screen, and two values that render identically
// did not change on their screen.
func sameAnswer(a, b any) bool {
	return renderSnapshotValue(a) == renderSnapshotValue(b)
}

func renderSnapshotValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []any:
		parts := make([]string, len(t))
		for i, e := range t {
			parts[i] = renderSnapshotValue(e)
		}
		// Sorted, because a multi_enum is a SET: a seller who unticked
		// "Hindi" and re-ticked it has not changed their answer, and a
		// reviewer told they did would go looking for a change that is not
		// there.
		sort.Strings(parts)
		return "[" + strings.Join(parts, ",") + "]"
	default:
		return fmt.Sprintf("%v", t)
	}
}

// ─── The gap sweeper ────────────────────────────────────────────────────

// SweepComplianceGaps finds live listings that no longer satisfy their
// category's current schema and records a gap for each.
//
// ─── DECISION 8, IN ONE SENTENCE ────────────────────────────────────────
//
// Making a field required later must never take live listings down.
//
// The alternative — delist on violation — is the design that looks rigorous
// and destroys the supply side: an operator ticks "required" on the Books
// category at 2pm and every book listed before the field existed stops
// selling at 2:01, for a rule its seller was never shown. There is no version
// of that a seller experiences as anything but the platform breaking their
// shop.
//
// So the listing keeps selling and is FLAGGED. The seller sees "action
// needed" with the fields on it, the founder sees the queue, and the gap
// closes on the seller's next edit — which is a moment they are already in
// the form, already looking at that field, and at which fixing it costs them
// nothing.
func (s *Service) SweepComplianceGaps(ctx context.Context) (*postgres.SweepResult, error) {
	return s.store.SweepComplianceGaps(ctx)
}

// SellerActionNeeded is the seller-facing signal: their live listings that a
// tightened rule has left in violation, and the fields to fix.
//
// Grouped by listing rather than served as a flat gap list, because the
// seller's unit of work is a listing: they open one editor and fix everything
// on it. A flat list would have them open the same listing four times.
func (s *Service) SellerActionNeeded(ctx context.Context, userID uuid.UUID, limit int) ([]*ProductActionNeeded, error) {
	sel, err := s.store.GetSellerByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, postgres.ErrNoSellerRow) {
			return nil, ErrNoSellerProfile
		}
		return nil, err
	}
	gaps, err := s.store.OpenGapsForSeller(ctx, sel.ID, limit)
	if err != nil {
		return nil, err
	}
	return groupGaps(gaps), nil
}

// ProductActionNeeded is one listing and everything wrong with it.
type ProductActionNeeded struct {
	ProductID    uuid.UUID `json:"product_id"`
	ProductTitle string    `json:"product_title"`
	// StillSelling is always true, and it is on the wire on purpose. The
	// first thing a seller shown "action needed" wants to know is whether
	// their listing has gone down, and a signal that does not answer that
	// costs a support ticket per seller. See decision 8: it never has.
	StillSelling bool                  `json:"still_selling"`
	Fields       []AttributeFieldError `json:"fields"`
}

func groupGaps(gaps []*postgres.ComplianceGap) []*ProductActionNeeded {
	out := []*ProductActionNeeded{}
	byProduct := map[uuid.UUID]int{}
	for _, g := range gaps {
		idx, ok := byProduct[g.ProductID]
		if !ok {
			idx = len(out)
			byProduct[g.ProductID] = idx
			out = append(out, &ProductActionNeeded{
				ProductID: g.ProductID, ProductTitle: g.ProductTitle,
				StillSelling: true, Fields: []AttributeFieldError{},
			})
		}
		out[idx].Fields = append(out[idx].Fields, AttributeFieldError{
			Code: g.Code, Label: g.Label, Reason: gapReason(g),
		})
	}
	return out
}

// gapReason turns a stored verdict into the sentence the seller reads.
//
// It leads with the reassurance, every time. A seller who reads "your listing
// is in violation" assumes it is down; telling them in the same clause that it
// is still selling is the difference between a fix on their next edit and a
// support ticket this afternoon.
func gapReason(g *postgres.ComplianceGap) string {
	switch g.Reason {
	case "out_of_range":
		return "this field's rules changed and the value on this listing no longer fits them; " +
			"the listing is still on sale — please correct it on your next edit"
	default:
		return "this field is now required for this listing's category; the listing is still " +
			"on sale — please fill it in on your next edit"
	}
}

// AdminComplianceGaps is the founder's queue.
func (s *Service) AdminComplianceGaps(
	ctx context.Context, definitionID *uuid.UUID, limit, offset int,
) ([]*postgres.ComplianceGap, int, error) {
	return s.store.ListOpenComplianceGaps(ctx, definitionID, limit, offset)
}
