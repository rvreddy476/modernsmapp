package service

// The product write path: creating a listing atomically, patching one through
// an allowlist, and checking the seller's answers against the form their
// category actually asks.
//
// ─── WHY VALIDATION AND COMPLETENESS ARE DIFFERENT QUESTIONS ────────────
//
// Every value a seller sends is checked HERE, at write time: the type, the
// bounds, the regex, the enum membership, the unit's family. A wrong value is
// refused, on create and on patch alike, because a wrong value that lands is
// not a loud failure later — `pages: "many"` is simply a book that never
// appears under a page-count filter, and nobody reports that.
//
// What is NOT checked here is whether the form is FINISHED. A create route
// that demanded every required field would make drafts impossible, and a
// seller who cannot save a half-filled form does not go away and come back
// with the missing data — they type "n/a", "TBD" and "0" into fourteen
// controls to get past the gate, and the catalogue is then full of values
// that are individually valid and collectively worthless. Completeness is the
// submit-for-review gate's question, asked once, at the moment the listing
// claims to be ready.
//
// So: a draft may be incomplete. A draft may not be wrong.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/atpost/commerce-service/internal/media"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ErrUnknownProductCategory is returned when `category_id` names no row.
//
// Nothing in this service checked it before. The foreign key on
// products.category_id is the only thing that ever refused one, and it
// surfaces as an unnamed constraint violation the edge reports as "one of the
// supplied values is not permitted" — which does not tell the seller WHICH
// value, and does not exist at all on a database where the key was never
// installed.
//
// It matters more now than it did: an unknown category has no attribute
// schema, so `GET /categories/:id/attribute-schema` 404s and the create form
// draws no fields. Without this check the seller sees an empty form, fills in
// nothing, and gets a listing that is missing everything its real category
// asks for — with no error anywhere in the sequence.
var ErrUnknownProductCategory = errors.New("commerce: no such product category")

// ErrProductNotEditable is returned when a patch names a product whose review
// state does not permit editing. See postgres.ProductEditability.
var ErrProductNotEditable = errors.New("commerce: this product cannot be edited in its current state")

// ─── Per-field refusals ─────────────────────────────────────────────────

// AttributeFieldError is one control's complaint, keyed by the attribute CODE.
//
// The code, not the label and not a positional index: the code is what the
// schema endpoint served, what the client built its control from, and what it
// can look the control up by to put this message underneath it. A label is
// presentation and can be renamed without a deploy — which is the entire
// point of the registry — so keying an error on it would break the form the
// first time an operator fixed a typo.
type AttributeFieldError struct {
	Code string `json:"code"`
	// Label is the human name of the field, and is `omitempty` because the
	// write path leaves it blank: a value the seller typed is on their screen
	// under a control they can see, so the code is enough to find it.
	//
	// The submit gate DOES set it (see ProductIncompleteError). A field that
	// is MISSING has nothing on screen — the message may be the only thing
	// naming it — and "hsn_code is required" is a worse sentence than
	// "HSN Code is required".
	Label  string `json:"label,omitempty"`
	Reason string `json:"reason"`
}

// AttributeValuesInvalidError carries EVERY field that failed, not the first.
//
// One flat message is not enough for a form. A seller who sends twenty
// answers and is told "invalid attribute value" has to bisect their own
// submission; told "the third one is wrong", they fix it, resubmit, and are
// told the seventh is wrong. A form with twenty controls needs twenty
// verdicts in one response, so the round trip count does not scale with the
// number of mistakes.
type AttributeValuesInvalidError struct {
	Fields []AttributeFieldError
}

func (e *AttributeValuesInvalidError) Error() string {
	parts := make([]string, 0, len(e.Fields))
	for _, f := range e.Fields {
		parts = append(parts, f.Code+": "+f.Reason)
	}
	return "commerce: " + fmt.Sprintf("%d attribute value(s) were refused: ", len(e.Fields)) +
		strings.Join(parts, "; ")
}

// ─── Validating a set of answers against a category's form ──────────────

// resolveAttributeValues checks every answer against the definitions the
// PRODUCT'S CATEGORY binds, and returns the rows to store.
//
// Against the category, not merely against the definition registry: a code
// that exists but is not on this category's form is a field the seller was
// never shown and a value no reader of this product will look for. Accepting
// it would store an answer to a question this listing was not asked, which
// then appears in `attributes_doc` and in nothing else.
//
// Every failure is collected. The first bad field does not end the pass.
func (s *Service) resolveAttributeValues(
	ctx context.Context, categoryID *uuid.UUID, inputs []AttributeValueInput,
) ([]postgres.AttributeValueSet, error) {

	if len(inputs) == 0 {
		return nil, nil
	}

	fieldErrs := []AttributeFieldError{}
	fail := func(code, reason string) {
		fieldErrs = append(fieldErrs, AttributeFieldError{Code: code, Reason: reason})
	}

	// No category, no form. Every answer is unanswerable rather than wrong,
	// and it is reported per field so the client can still show the seller
	// exactly which values it is holding that it cannot save.
	if categoryID == nil {
		for _, in := range inputs {
			fail(in.Code, "this product has no category, so nothing defines this field; "+
				"choose a category first")
		}
		return nil, &AttributeValuesInvalidError{Fields: fieldErrs}
	}

	effective, err := s.store.EffectiveCategoryAttributes(ctx, *categoryID)
	if err != nil {
		return nil, err
	}
	byCode := make(map[string]*postgres.EffectiveAttribute, len(effective))
	for _, ea := range effective {
		byCode[ea.Definition.Code] = ea
	}

	// Which definitions are in play, so the enum options and the unit
	// vocabularies are two batched reads rather than two per field.
	defIDs := []uuid.UUID{}
	familySet := map[string]bool{}
	seen := map[string]bool{}
	usable := make([]AttributeValueInput, 0, len(inputs))

	for _, in := range inputs {
		code := strings.TrimSpace(in.Code)
		if code == "" {
			fail("", "an attribute value must name the field it answers")
			continue
		}
		// The same code twice is two answers to one question, and whichever
		// the loop happened to write last would win silently.
		if seen[code] {
			fail(code, "sent twice in one request")
			continue
		}
		seen[code] = true

		ea, ok := byCode[code]
		if !ok {
			fail(code, "is not a field this category asks for; "+
				"GET /v1/commerce/categories/"+categoryID.String()+"/attribute-schema lists them")
			continue
		}
		in.Code = code
		usable = append(usable, in)
		defIDs = append(defIDs, ea.Definition.ID)
		if ea.Definition.UnitFamily != nil && *ea.Definition.UnitFamily != "" {
			familySet[*ea.Definition.UnitFamily] = true
		}
	}

	families := make([]string, 0, len(familySet))
	for f := range familySet {
		families = append(families, f)
	}
	sort.Strings(families)

	enums, err := s.store.EnumCodeSetsFor(ctx, defIDs)
	if err != nil {
		return nil, err
	}
	unitsByFamily, err := s.store.UnitsForFamilies(ctx, families)
	if err != nil {
		return nil, err
	}

	sets := make([]postgres.AttributeValueSet, 0, len(usable))
	for _, in := range usable {
		d := &byCode[in.Code].Definition

		// nil is "clear this field", not "no answer": an empty set, which
		// the store's replace semantics turn into a delete. It is the only
		// way a seller removes a value they set by mistake.
		if in.Value == nil {
			sets = append(sets, postgres.AttributeValueSet{DefinitionID: d.ID})
			continue
		}

		vc := attributeValueContext{enumCodes: enums[d.ID], units: map[string]bool{}}
		if d.UnitFamily != nil {
			for _, u := range unitsByFamily[*d.UnitFamily] {
				vc.units[u.Code] = true
			}
		}
		rows, err := validateAttributeValue(d, in, vc)
		if err != nil {
			fail(in.Code, attributeReason(err))
			continue
		}
		sets = append(sets, postgres.AttributeValueSet{DefinitionID: d.ID, Values: rows})
	}

	if len(fieldErrs) > 0 {
		return nil, &AttributeValuesInvalidError{Fields: fieldErrs}
	}
	return sets, nil
}

// attributeReason unwraps whichever refusal validateAttributeValue produced
// into the sentence that belongs under the control.
//
// It already names the field, so the field name is stripped rather than
// repeated: the code is the KEY of this error, and "pages: pages must be a
// whole number" is what a message that carries its own key reads like.
func attributeReason(err error) string {
	var invalid *AttributeValidationError
	if errors.As(err, &invalid) {
		return invalid.Reason
	}
	if errors.Is(err, ErrTooManyAttributeValues) {
		// The sentinel's own text is stripped, not split off at the first
		// colon: the wrapped half is "<code> accepts at most N, got M", and a
		// split would leave the sentence starting with the sentinel's words
		// on top of the code this error is already keyed by.
		msg := strings.TrimPrefix(err.Error(), ErrTooManyAttributeValues.Error()+": ")
		// The code is the KEY of this error; repeating it inside the message
		// reads as "pages: pages accepts at most 2".
		if i := strings.Index(msg, " accepts at most "); i >= 0 {
			return "accepts at most" + msg[i+len(" accepts at most"):]
		}
		return msg
	}
	return err.Error()
}

// ─── Category existence ─────────────────────────────────────────────────

// requireCategory refuses a category_id that names no row.
//
// A nil id is permitted and is not an error: `products.category_id` is
// nullable, an uncategorised draft is a real state, and refusing it would
// mean a seller could not save anything until they had picked a category —
// which is the draft-hostile behaviour the header of this file argues
// against. What is refused is a NON-NULL id that names nothing.
func (s *Service) requireCategory(ctx context.Context, id *uuid.UUID) error {
	if id == nil {
		return nil
	}
	exists, err := s.store.CategoryExists(ctx, *id)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: %s", ErrUnknownProductCategory, id)
	}
	return nil
}

// publishedSchemaVersion is the version stamped onto a product at write time.
//
// Read once per write and recorded, rather than looked up when the values are
// read back. A reader then knows which vintage of the form produced them: if
// an operator narrows `pages` to a maximum of 2000 tomorrow, every product
// stamped with today's version is one whose page count was checked against
// the old bound and may now be outside the new one. A field that always
// reported the current version could not distinguish those from listings
// saved after the change.
func (s *Service) publishedSchemaVersion(ctx context.Context) (int, error) {
	state, err := s.store.GetAttributeSchemaState(ctx)
	if err != nil {
		return 0, err
	}
	return state.PublishedVersion, nil
}

// ─── The patch ──────────────────────────────────────────────────────────

// UpdateProductInput is a patch as it arrives at the service.
//
// `Fields` is the typed allowlist; `Attributes` is the seller's answers, in
// exactly the shape the create route takes. `AckRevalidation` is the
// acknowledgement an approved product's edit needs — see UpdateProduct.
type UpdateProductInput struct {
	ActorUserID uuid.UUID
	ProductID   uuid.UUID
	Fields      postgres.ProductPatch
	Attributes  []AttributeValueInput
	// AttributesPresent distinguishes `"attributes": []` (clear nothing,
	// send nothing) from an absent key. Both mean "leave the values alone"
	// today; the flag is here so a future "replace the whole set" cannot be
	// added without someone deciding which one it is.
	AttributesPresent bool
	AckRevalidation   bool

	// Variation is the product's matrix, COMPLETE, or nil for a patch that
	// does not mention it. See VariationPatchInput.
	Variation *VariationPatchInput
}

// VariationPatchInput replaces a product's whole variation matrix.
//
// Whole, not partial, and the reason is in postgres.VariationUpdate: axes are
// a fact about the product and options are a fact about each variant, so
// "add an axis" leaves every existing variant carrying no value on it, and
// there is no honest way to fill that in — this service cannot know whether
// the shirts already listed are the blue ones or the red ones.
//
// So a caller changing the matrix sends every variant with it. An empty
// `Axes` with every variant carrying no options is how a product stops
// varying: the axes go, their options cascade with them, and the trigger
// clears the legacy columns it had been deriving.
type VariationPatchInput struct {
	Axes     []VariationAxisInput  `json:"variation_axes"`
	Variants []VariantOptionsPatch `json:"variants"`
}

// VariantOptionsPatch is one existing variant's options.
type VariantOptionsPatch struct {
	VariantID uuid.UUID            `json:"variant_id"`
	Options   []VariantOptionInput `json:"options"`
}

// UpdateProductResult reports what the patch did, including the part the
// caller did not ask for.
type UpdateProductResult struct {
	Product *postgres.Product
	// Revalidated is true when this edit sent an approved product back to
	// review. It is on the response because a seller whose live listing has
	// just gone dark must be told so in the same breath as being told the
	// edit worked.
	Revalidated bool
}

// RevalidationRequiredError is the refusal an approved product's substantive
// edit gets when it has not acknowledged the cost.
//
// ─── THE RULE, AND WHY THIS ONE ─────────────────────────────────────────
//
// An approved product is one a human read and permitted. Editing its title,
// its description, its images or its category changes the thing that was
// permitted, and there are exactly three options:
//
//  1. Apply the edit and keep the approval. This is a moderation bypass:
//     get a bland listing approved, then rewrite it into whatever you
//     actually wanted to sell. It is the one option that cannot be chosen.
//
//  2. Apply the edit and silently drop back to review. Truthful, but the
//     seller pressed "save" on a spelling fix and their live listing
//     disappeared from the catalogue with no warning. They will not find
//     out from this response; they will find out from their sales.
//
//  3. Refuse, state the cost, and require the caller to say yes.
//
// This is (3). The service names which fields triggered it, and re-sending
// with `revalidate: true` applies the edit AND returns the product to
// `approval_status='submitted'`, `status='draft'`, `published_at=NULL` —
// reported in the response, not merely done.
//
// Not every field costs an approval. `meta_title`, `meta_description`,
// `search_keywords`, the physical dimensions and `tax_class_id` are outside
// the review: no reviewer reads a meta description, and a seller correcting a
// GST class or a parcel weight must not have to take their listing down to do
// it. Those apply in place, on an approved product, with no state change.
//
// The precedent for the shape is this service's own ImpactAckError: a
// narrowing schema edit is refused until the operator quotes the damage back.
type RevalidationRequiredError struct {
	// Fields are the substantive fields this patch would change.
	Fields []string
}

func (e *RevalidationRequiredError) Error() string {
	return fmt.Sprintf(
		"commerce: this product is approved and %s %s reviewed content; "+
			"re-send with \"revalidate\": true to apply the edit and return the listing to review",
		strings.Join(e.Fields, ", "), plural(len(e.Fields), "changes", "change"))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// reviewNeutralPatch reports whether a patch touches only fields no reviewer
// looks at. See RevalidationRequiredError for the list and the argument.
func substantiveFields(p postgres.ProductPatch, attrs int, variation bool) []string {
	out := []string{}
	add := func(name string, changed bool) {
		if changed {
			out = append(out, name)
		}
	}
	add("category_id", p.CategoryID != nil)
	add("brand_id", p.BrandID != nil)
	add("title", p.Title != nil)
	add("short_title", p.ShortTitle != nil)
	add("description", p.Description != nil)
	add("short_description", p.ShortDescription != nil)
	add("brand_name", p.BrandName != nil)
	add("manufacturer_name", p.ManufacturerName != nil)
	add("product_type", p.ProductType != nil)
	add("condition", p.Condition != nil)
	add("primary_image_media_id", p.PrimaryImageMediaID != nil)
	add("video_media_id", p.VideoMediaID != nil)
	add("country_of_origin", p.CountryOfOrigin != nil)
	add("warranty_info", p.WarrantyInfo != nil)
	add("return_policy_type", p.ReturnPolicyType != nil)
	add("return_policy_days", p.ReturnPolicyDays != nil)
	add("hsn_code", p.HSNCode != nil)
	add("attributes", attrs > 0)
	// A reviewer read a listing that varied on two axes with six
	// combinations. Changing which axes it varies on, or which combination
	// each variant is, changes what was permitted just as squarely as
	// rewriting the description does — and it is the edit that most rewards
	// doing quietly, because the price and the stock hang off it.
	add("variation_axes", variation)
	return out
}

// UpdateProduct patches a product the caller owns.
//
// Three gates, in this order, and the order is the point: ownership before
// state (a stranger must not learn which of someone else's listings are under
// review), state before validation (there is no reason to check the values of
// an edit that is refused anyway), and validation before anything is written.
func (s *Service) UpdateProduct(ctx context.Context, in UpdateProductInput) (*UpdateProductResult, error) {
	product, err := s.store.GetProductByID(ctx, in.ProductID)
	if err != nil {
		return nil, err
	}

	// ── Ownership ───────────────────────────────────────────
	seller, err := s.GetSellerProfile(ctx, in.ActorUserID)
	if err != nil {
		if errors.Is(err, postgres.ErrNoSellerRow) {
			return nil, ErrNoSellerProfile
		}
		return nil, err
	}
	if seller == nil || seller.ID != product.SellerID {
		return nil, ErrNotProductOwner
	}

	// ── State ───────────────────────────────────────────────
	editable, needsRevalidation := postgres.ProductEditability(product.ApprovalStatus)
	if !editable {
		return nil, fmt.Errorf("%w: approval_status=%s", ErrProductNotEditable, product.ApprovalStatus)
	}

	// The category this edit's attribute values are checked against: the new
	// one if the patch moves the product, otherwise the one it already has.
	// Checking against the OLD category while moving to a new one would
	// validate the answers against a form that is about to stop applying.
	targetCategory := product.CategoryID
	if in.Fields.CategoryID != nil {
		if err := s.requireCategory(ctx, in.Fields.CategoryID); err != nil {
			return nil, err
		}
		targetCategory = in.Fields.CategoryID
	}

	if err := s.verifyMedia(ctx, in.ActorUserID, media.KindImage, in.Fields.PrimaryImageMediaID); err != nil {
		return nil, err
	}
	if err := s.verifyMedia(ctx, in.ActorUserID, media.KindVideo, in.Fields.VideoMediaID); err != nil {
		return nil, err
	}

	// ── Values ──────────────────────────────────────────────
	sets, err := s.resolveAttributeValues(ctx, targetCategory, in.Attributes)
	if err != nil {
		return nil, err
	}

	// ── The matrix ──────────────────────────────────────────
	variation, err := s.resolveVariationPatch(ctx, in.ProductID, targetCategory, in.Variation)
	if err != nil {
		return nil, err
	}

	patch := in.Fields
	if needsRevalidation {
		if changed := substantiveFields(patch, len(sets), variation != nil); len(changed) > 0 {
			if !in.AckRevalidation {
				return nil, &RevalidationRequiredError{Fields: changed}
			}
			patch.Revalidate = true
		}
	}

	// The version stamp goes on whenever this write touched the answers.
	// A patch of the title alone leaves it where it was, because the values
	// it describes were not re-checked.
	if len(sets) > 0 {
		v, err := s.publishedSchemaVersion(ctx)
		if err != nil {
			return nil, err
		}
		patch.SchemaVersion = &v
	}

	if !patch.TouchesAnyColumn() && len(sets) == 0 && variation == nil {
		// Nothing to do is not an error — a client re-sending an unchanged
		// form should get the product back, not a refusal.
		return &UpdateProductResult{Product: product}, nil
	}

	if err := s.store.PatchProduct(ctx, in.ProductID, patch, sets, variation); err != nil {
		return nil, err
	}

	// "The seller fixes the gap on their next edit" is decision 8's whole
	// mechanism, and this is the line that makes it true. A seller who filled
	// in the newly-required field and still saw "action needed" on their
	// dashboard until the next nightly sweep would reasonably conclude the
	// warning is noise — and the next thing they conclude is that the one
	// after it is noise too.
	//
	// Best effort, and deliberately so: the edit HAS landed. Failing the
	// patch because a bookkeeping table could not be updated would take a
	// successful save away from the seller over a row nobody has read yet,
	// and SweepComplianceGaps is the backstop that closes it anyway.
	if _, err := s.store.ResolveGapsForProduct(ctx, in.ProductID); err != nil {
		slog.WarnContext(ctx, "commerce: could not re-check compliance gaps after a patch",
			"product_id", in.ProductID, "error", err)
	}

	updated, err := s.store.GetProductByID(ctx, in.ProductID)
	if err != nil {
		return nil, err
	}
	s.hydrateProductImages(ctx, []*postgres.Product{updated})
	s.publish(ctx, "commerce.product.updated", map[string]any{
		"product_id": updated.ID, "seller_id": updated.SellerID,
		"revalidated": patch.Revalidate,
	})
	// Two different transitions arrive here and publishProductVisibility
	// tells them apart by reading the row rather than by asking the patch:
	//
	//	revalidation bounce   an approved listing edited substantively goes
	//	                      status='draft', approval_status='submitted' —
	//	                      it must LEAVE the index, or search keeps
	//	                      offering a listing the catalogue has taken off
	//	                      sale pending re-review.
	//	an ordinary edit      the listing is still live, and the document
	//	                      search holds is now stale. Re-publishing makes
	//	                      the consumer read it back, so a retitled or
	//	                      recategorised listing is findable under what it
	//	                      says now rather than what it said when it was
	//	                      approved.
	//
	// Which is why this is not `if patch.Revalidate`.
	s.publishProductVisibility(ctx, updated.ID)
	return &UpdateProductResult{Product: updated, Revalidated: patch.Revalidate}, nil
}
