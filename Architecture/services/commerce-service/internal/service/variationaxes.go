package service

// What a product varies on, and what each variant is — validated before
// anything is written.
//
// ─── THE ONE RULE THAT MATTERS: NO FREE TEXT ────────────────────────────
//
// A variation axis value must be a CODE the schema already knows. Not the
// text the seller typed, not a normalised form of it, not a best guess.
//
// The reason is the shared catalogue. `product_variants` used to key on six
// free-text columns, so "Blue", "blue", " Blue" and "Navy Blue" were four
// different colours of the same shirt, permanently, and no filter, no facet
// and no buy-box would ever reunite them. That was survivable while a
// product belonged to one seller — the mess was one shop's. It is not
// survivable once two shops list the same item, because then the axis is a
// fact about the ITEM: whichever spelling the first seller happened to use
// becomes the catalogue's spelling for everybody, and the second seller's
// perfectly correct "Navy" simply does not match.
//
// There is no cleanup for this. An enum option can be renamed by an operator
// in one place; a thousand free-text spellings cannot be reconciled by
// anyone, because nothing records which of them were meant to be the same.
// So the refusal is at the edge, before the first one is stored, and the
// message says what to send instead — the codes, listed, from the schema the
// client already fetched to draw the form.
//
// ─── WHAT ELSE IS CHECKED HERE, AND WHY NOT IN THE DATABASE ─────────────
//
// The database already refuses the two things that must never be storable:
// an option on an axis the product does not declare (a composite foreign
// key), and two variants of one offer on the same combination (a partial
// unique index). Those are constraints because they are invariants — no
// validation pass, however careful, is allowed to be the only thing standing
// between the catalogue and them.
//
// What is checked HERE is everything whose refusal has to be a sentence
// rather than a constraint name: which attributes may be an axis at all, that
// every variant answers every axis exactly once, that the combination count
// is one a human can price. A seller told "product_variant_options_axis_fk"
// learns nothing.

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// maxVariationCombinations caps the matrix.
//
// Twenty, which is two axes of four and five, or one axis of twenty. Above
// that a seller is not filling in a form, they are doing data entry — and the
// observable outcome of asking somebody to price sixty rows by hand is that
// they price six and leave the rest at whatever the form defaulted to, which
// is how a large size ends up on sale at the small size's price.
//
// The cap is on COMBINATIONS, not on axes. The axis cap is two and lives in
// the database (see migration 028); this is the second, softer limit that
// stops two axes of ten.
const maxVariationCombinations = 20

// valueCodeShape mirrors the CHECK on product_variant_options.value_code.
//
// Duplicated deliberately, and it is not the usual duplication smell: the
// database's copy is the invariant and this one exists only so the refusal
// is a sentence a seller can act on instead of a constraint violation. If
// they ever disagree the database wins, and the disagreement shows up as a
// 500 on a value this pass thought was fine — loud, and in a test.
var valueCodeShape = regexp.MustCompile(`^[^|=]{1,128}$`)

var integerValue = regexp.MustCompile(`^-?[0-9]+$`)

// ─── Inputs ─────────────────────────────────────────────────────────────

// VariationAxisInput is one axis as the client sends it.
//
// `Code` is the attribute definition's code — the same code the schema
// endpoint served and the same one `attributes` uses. Position is optional:
// omitted, the axes are numbered by their order in the array, because that
// is the order the client drew them in and asking a form to send both an
// array order and an explicit position is asking for the two to disagree.
type VariationAxisInput struct {
	Code     string `json:"code"`
	Position int    `json:"position,omitempty"`
}

// VariantOptionInput is one variant's value on one axis.
//
// `Value` is a CODE. The field is not called `code` because the client is
// echoing back what the seller picked from a list, and calling it `value`
// keeps it parallel with the attribute inputs; what it must CONTAIN is the
// enum option's code. See the file header.
type VariantOptionInput struct {
	Code  string `json:"code"`
	Value string `json:"value"`
}

// ─── The refusal ────────────────────────────────────────────────────────

// VariationProblem is one complaint about the matrix.
//
// `Variant` names which variant, by SKU where there is one and by position
// where there is not — a create's variants have no ids yet, so an id-keyed
// error would be unusable on the request that most needs it.
type VariationProblem struct {
	Variant string `json:"variant,omitempty"`
	Code    string `json:"code,omitempty"`
	Reason  string `json:"reason"`
}

// VariationInvalidError carries EVERY problem, for the reason
// AttributeValuesInvalidError does: a seller with a six-cell matrix and three
// mistakes should not need three round trips to find out about all of them.
type VariationInvalidError struct {
	Problems []VariationProblem
}

func (e *VariationInvalidError) Error() string {
	parts := make([]string, 0, len(e.Problems))
	for _, p := range e.Problems {
		switch {
		case p.Variant != "" && p.Code != "":
			parts = append(parts, p.Variant+"/"+p.Code+": "+p.Reason)
		case p.Variant != "":
			parts = append(parts, p.Variant+": "+p.Reason)
		case p.Code != "":
			parts = append(parts, p.Code+": "+p.Reason)
		default:
			parts = append(parts, p.Reason)
		}
	}
	return fmt.Sprintf("commerce: %d problem(s) with the variation matrix: %s",
		len(e.Problems), strings.Join(parts, "; "))
}

// ─── Resolution ─────────────────────────────────────────────────────────

// variationVariant is one variant as this pass sees it: a name to blame and
// the options it claims.
type variationVariant struct {
	// Name is the SKU on a create and on a patch alike. It is what the
	// seller typed and what they will look for in the error.
	Name string
	// ID is set on a patch, where the variants already exist, and is the
	// zero value on a create.
	ID      uuid.UUID
	Options []VariantOptionInput
}

// resolvedVariation is what the store needs, keyed the way the store wants
// it.
type resolvedVariation struct {
	Axes []postgres.VariationAxis
	// PerVariant is parallel to the input slice, so a create can zip it back
	// onto its NewVariant values without a second lookup.
	PerVariant [][]postgres.VariantOption
}

// resolveVariation validates a product's axes and its variants' options
// against the category's published schema.
//
// A nil result with a nil error means "this product does not vary", which is
// the answer for the overwhelming majority of listings and is NOT an error:
// a single-variant product with no axes is a perfectly good listing and
// always has been.
func (s *Service) resolveVariation(
	ctx context.Context, categoryID *uuid.UUID,
	axes []VariationAxisInput, variants []variationVariant,
) (*resolvedVariation, error) {

	problems := []VariationProblem{}
	fail := func(variant, code, reason string) {
		problems = append(problems, VariationProblem{Variant: variant, Code: code, Reason: reason})
	}

	anyOptions := false
	for _, v := range variants {
		if len(v.Options) > 0 {
			anyOptions = true
			break
		}
	}

	// ── No axes declared ────────────────────────────────────
	if len(axes) == 0 {
		if !anyOptions {
			return nil, nil
		}
		// Options with no axes is the state the composite foreign key
		// exists to make unstorable. Refused here so the seller is told
		// what is missing rather than shown a constraint name.
		for _, v := range variants {
			if len(v.Options) > 0 {
				fail(v.Name, "", "this variant carries options, but the product declares no "+
					"variation axes; send \"variation_axes\" alongside them")
			}
		}
		return nil, &VariationInvalidError{Problems: problems}
	}

	// A product with no category has no schema, so nothing defines what an
	// axis could be. Reported as one problem rather than one per axis: the
	// fix is a single field on the form.
	if categoryID == nil {
		return nil, &VariationInvalidError{Problems: []VariationProblem{{
			Reason: "this product has no category, so nothing defines which attributes it may " +
				"vary on; choose a category first",
		}}}
	}

	if len(axes) > 2 {
		fail("", "", fmt.Sprintf(
			"a product may vary on at most two attributes; %d were sent. A third axis multiplies "+
				"the matrix past what anyone prices by hand — sell the third dimension as a "+
				"separate product", len(axes)))
		return nil, &VariationInvalidError{Problems: problems}
	}

	// ── The axes themselves ─────────────────────────────────
	effective, err := s.store.EffectiveCategoryAttributes(ctx, *categoryID)
	if err != nil {
		return nil, err
	}
	byCode := make(map[string]*postgres.EffectiveAttribute, len(effective))
	for _, ea := range effective {
		byCode[ea.Definition.Code] = ea
	}

	resolved := make([]postgres.VariationAxis, 0, len(axes))
	axisByCode := map[string]*postgres.EffectiveAttribute{}
	axisOrder := []string{}
	seenCode := map[string]bool{}
	seenPos := map[int]bool{}
	defIDs := []uuid.UUID{}

	for i, in := range axes {
		code := strings.TrimSpace(in.Code)
		if code == "" {
			fail("", "", fmt.Sprintf("variation axis %d names no attribute", i+1))
			continue
		}
		if seenCode[code] {
			fail("", code, "sent twice; a product cannot vary on the same attribute in two slots")
			continue
		}
		seenCode[code] = true

		ea, ok := byCode[code]
		if !ok {
			fail("", code, "is not an attribute this category asks for, so it cannot be a "+
				"variation axis; GET /v1/commerce/categories/"+categoryID.String()+
				"/attribute-schema lists them")
			continue
		}
		// The EFFECTIVE flag, not the definition's own: 025 lets a category
		// promote a field to an axis (size matters for shoes) without every
		// other category that binds it having to agree.
		if !ea.IsVariantAxis {
			fail("", code, "is not marked as a variation axis for this category; a variant may "+
				"only be keyed on an attribute whose values are discrete and comparable")
			continue
		}
		switch ea.Definition.DataType {
		case "enum", "text", "integer":
		default:
			fail("", code, "is a "+ea.Definition.DataType+", which cannot key a variant: "+
				"an axis needs discrete, comparable values")
			continue
		}

		pos := in.Position
		if pos == 0 {
			pos = i + 1
		}
		if pos < 1 || pos > 2 {
			fail("", code, fmt.Sprintf("position %d is out of range; a product has two axis "+
				"slots, 1 and 2", pos))
			continue
		}
		if seenPos[pos] {
			fail("", code, fmt.Sprintf("two axes claim position %d", pos))
			continue
		}
		seenPos[pos] = true

		resolved = append(resolved, postgres.VariationAxis{DefinitionID: ea.Definition.ID, Position: pos})
		axisByCode[code] = ea
		axisOrder = append(axisOrder, code)
		defIDs = append(defIDs, ea.Definition.ID)
	}

	if len(problems) > 0 {
		return nil, &VariationInvalidError{Problems: problems}
	}

	// ── The combination count, before the values ────────────
	//
	// Checked on the number of VARIANTS rather than on the product of the
	// distinct values, because the variants are what a seller actually has
	// to price, photograph and keep in stock. A 3×3 axis pair with four
	// variants filled in is four rows of work, not nine.
	if len(variants) > maxVariationCombinations {
		fail("", "", fmt.Sprintf(
			"%d variants were sent; a product is capped at %d combinations. Beyond that the grid "+
				"stops being something a seller can price and photograph one cell at a time",
			len(variants), maxVariationCombinations))
		return nil, &VariationInvalidError{Problems: problems}
	}

	// ── The values ──────────────────────────────────────────
	enumValues, err := s.store.EnumValuesForDefinitions(ctx, defIDs)
	if err != nil {
		return nil, err
	}

	codeByDef := make(map[uuid.UUID]string, len(axisByCode))
	for code, ea := range axisByCode {
		codeByDef[ea.Definition.ID] = code
	}

	perVariant := make([][]postgres.VariantOption, len(variants))
	combos := map[string]string{} // canonical key -> the variant that claimed it first

	for vi, v := range variants {
		name := v.Name
		if name == "" {
			name = fmt.Sprintf("variant %d", vi+1)
		}

		given := map[string]string{}
		dup := false
		for _, o := range v.Options {
			code := strings.TrimSpace(o.Code)
			if code == "" {
				fail(name, "", "an option must name the axis it answers")
				dup = true
				continue
			}
			if _, seen := given[code]; seen {
				fail(name, code, "answered twice on one variant")
				dup = true
				continue
			}
			if _, isAxis := axisByCode[code]; !isAxis {
				fail(name, code, "is not one of this product's variation axes ("+
					strings.Join(axisOrder, ", ")+")")
				dup = true
				continue
			}
			given[code] = strings.TrimSpace(o.Value)
		}
		if dup {
			continue
		}

		opts := make([]postgres.VariantOption, 0, len(axisOrder))
		bad := false
		for _, code := range axisOrder {
			raw, ok := given[code]
			if !ok {
				fail(name, code, "is a variation axis of this product and this variant does not "+
					"answer it; every variant must carry exactly one value on every axis")
				bad = true
				continue
			}
			ea := axisByCode[code]
			valueCode, reason := axisValueCode(&ea.Definition, raw, enumValues[ea.Definition.ID])
			if reason != "" {
				fail(name, code, reason)
				bad = true
				continue
			}
			opts = append(opts, postgres.VariantOption{
				DefinitionID: ea.Definition.ID, ValueCode: valueCode,
			})
		}
		if bad {
			continue
		}

		// The same canonical key the database derives, computed here so the
		// duplicate is reported as "PROBE-2 has the same combination as
		// PROBE-1" rather than as a unique-index violation naming neither.
		key := canonicalVariationKey(resolved, codeByDef, opts)
		if first, clash := combos[key]; clash {
			fail(name, "", "has the same combination of options as "+first+
				"; one listing cannot offer the same combination twice")
			continue
		}
		combos[key] = name
		perVariant[vi] = opts
	}

	if len(problems) > 0 {
		sort.SliceStable(problems, func(i, j int) bool {
			if problems[i].Variant != problems[j].Variant {
				return problems[i].Variant < problems[j].Variant
			}
			return problems[i].Code < problems[j].Code
		})
		return nil, &VariationInvalidError{Problems: problems}
	}

	sort.Slice(resolved, func(i, j int) bool { return resolved[i].Position < resolved[j].Position })
	return &resolvedVariation{Axes: resolved, PerVariant: perVariant}, nil
}

// resolveVariationPatch validates a PATCH's matrix against the product's
// existing variants.
//
// nil in, nil out: a patch that does not mention the matrix leaves it alone,
// which is what every request written before migration 028 does.
//
// ─── WHY EVERY VARIANT MUST BE NAMED ────────────────────────────────────
//
// The store replaces the whole picture, because a partial one has no honest
// meaning (see postgres.VariationUpdate). So a variant the patch does not
// name is a variant whose options would be cascaded away by the axis
// replacement and never rewritten — leaving a listing with a declared matrix
// and a variant outside it, which is exactly the state the composite foreign
// key exists to prevent and which nothing downstream could render.
//
// EVERY variant, not just the active ones: an archived variant is still a row
// the foreign key applies to, and it is still joined by order history, which
// reads the legacy option columns the trigger would blank.
func (s *Service) resolveVariationPatch(
	ctx context.Context, productID uuid.UUID, categoryID *uuid.UUID, in *VariationPatchInput,
) (*postgres.VariationUpdate, error) {

	if in == nil {
		return nil, nil
	}

	existing, err := s.store.ProductVariantIdentities(ctx, productID)
	if err != nil {
		return nil, err
	}

	sent := make(map[uuid.UUID][]VariantOptionInput, len(in.Variants))
	problems := []VariationProblem{}
	known := make(map[uuid.UUID]postgres.VariantIdentity, len(existing))
	for _, v := range existing {
		known[v.ID] = v
	}
	for _, v := range in.Variants {
		if _, ok := known[v.VariantID]; !ok {
			problems = append(problems, VariationProblem{
				Variant: v.VariantID.String(),
				Reason:  "is not a variant of this product",
			})
			continue
		}
		if _, dup := sent[v.VariantID]; dup {
			problems = append(problems, VariationProblem{
				Variant: known[v.VariantID].SKU,
				Reason:  "appears twice in \"variants\"",
			})
			continue
		}
		sent[v.VariantID] = v.Options
	}
	for _, v := range existing {
		if _, ok := sent[v.ID]; !ok {
			problems = append(problems, VariationProblem{
				Variant: v.SKU,
				Reason: "is a variant of this product (status " + v.Status + ") and this request " +
					"does not say what its options are; a matrix change replaces the whole " +
					"picture, so every variant must be listed",
			})
		}
	}
	if len(problems) > 0 {
		return nil, &VariationInvalidError{Problems: problems}
	}

	vv := make([]variationVariant, 0, len(existing))
	for _, v := range existing {
		vv = append(vv, variationVariant{Name: v.SKU, ID: v.ID, Options: sent[v.ID]})
	}

	res, err := s.resolveVariation(ctx, categoryID, in.Axes, vv)
	if err != nil {
		return nil, err
	}

	// A nil result is "no axes and no options" — the caller is CLEARING the
	// matrix. That is a real request, and it is not the same as "the patch
	// did not mention the matrix", so it returns a non-nil update with an
	// empty axis set rather than nil.
	u := &postgres.VariationUpdate{
		VariantOptions: make(map[uuid.UUID][]postgres.VariantOption, len(existing)),
	}
	if res != nil {
		u.Axes = res.Axes
		for i, v := range existing {
			u.VariantOptions[v.ID] = res.PerVariant[i]
		}
	}
	return u, nil
}

// axisValueCode turns what the seller sent into the code that is stored, or
// says why it cannot.
//
// ─── THE ENUM CASE IS THE POINT ─────────────────────────────────────────
//
// Exact match on the option's CODE. Not case-insensitive, not matched
// against the label, not trimmed-and-hoped. A client that sends "Blue"
// instead of "blue" is a client that built its control from something other
// than the schema endpoint, and quietly accepting it would make the schema
// advisory — which is how the free-text mess this whole change exists to end
// got started. The message names the codes, so the fix is one edit away.
func axisValueCode(d *postgres.AttributeDefinition, raw string, options []*postgres.AttributeEnumValue) (string, string) {
	if raw == "" {
		return "", "carries no value"
	}

	switch d.DataType {
	case "enum":
		for _, o := range options {
			if o.Code == raw {
				return o.Code, ""
			}
		}
		codes := make([]string, 0, len(options))
		for _, o := range options {
			codes = append(codes, o.Code)
		}
		if len(codes) == 0 {
			return "", fmt.Sprintf(
				"%q has no options defined yet, so nothing can be selected on it; an operator "+
					"must add its values before it can key a variant", d.Code)
		}
		return "", fmt.Sprintf(
			"%q is not one of the option codes for %q. Free text is refused on a variation axis: "+
				"on a shared catalogue it mints \"Blue\", \"blue\" and \"Navy Blue\" as three "+
				"permanent colours that no filter can reunite. Send one of: %s",
			raw, d.Code, strings.Join(codes, ", "))

	case "integer":
		if !integerValue.MatchString(raw) {
			return "", fmt.Sprintf("%q is not a whole number, and %q is an integer axis", raw, d.Code)
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return "", fmt.Sprintf("%q is not a whole number this service can store", raw)
		}
		if d.MinNum != nil && float64(n) < *d.MinNum {
			return "", fmt.Sprintf("%d is below the minimum of %g for %q", n, *d.MinNum, d.Code)
		}
		if d.MaxNum != nil && float64(n) > *d.MaxNum {
			return "", fmt.Sprintf("%d is above the maximum of %g for %q", n, *d.MaxNum, d.Code)
		}
		// Re-rendered from the parsed number rather than passed through, so
		// "007" and "7" cannot become two combinations of the same size.
		return strconv.FormatInt(n, 10), ""

	case "text":
		if d.MinLen != nil && len(raw) < *d.MinLen {
			return "", fmt.Sprintf("is shorter than the %d characters %q requires", *d.MinLen, d.Code)
		}
		if d.MaxLen != nil && len(raw) > *d.MaxLen {
			return "", fmt.Sprintf("is longer than the %d characters %q allows", *d.MaxLen, d.Code)
		}
		if d.Regex != nil && *d.Regex != "" {
			re, err := regexp.Compile(*d.Regex)
			if err != nil {
				// A definition with an uncompilable pattern is an operator's
				// mistake, not the seller's. Refusing the write is still the
				// right answer — the alternative is storing a value nothing
				// checked against a rule somebody meant to apply.
				return "", fmt.Sprintf("%q has a validation pattern this service cannot read; "+
					"an operator must fix the attribute definition", d.Code)
			}
			if !re.MatchString(raw) {
				return "", fmt.Sprintf("%q does not match the format %q requires", raw, d.Code)
			}
		}
		if !valueCodeShape.MatchString(raw) {
			return "", fmt.Sprintf(
				"%q cannot be a variation value: it must be 1 to 128 characters and contain "+
					"neither \"|\" nor \"=\", which separate the parts of the stored combination key", raw)
		}
		return raw, ""
	}

	return "", fmt.Sprintf("%q is a %s, which cannot key a variant", d.Code, d.DataType)
}

// canonicalVariationKey builds the same string the database derives, for use
// in the duplicate message only.
//
// It is NOT sent to the store and it is not what gets written: the column is
// maintained by a trigger, so that every writer of the option rows — this
// service, the bulk importer, an operator's repair script — gets the same
// key without having to know the rule. This copy exists purely so a refusal
// can name the two variants that clashed.
func canonicalVariationKey(axes []postgres.VariationAxis, codeByDef map[uuid.UUID]string,
	opts []postgres.VariantOption) string {

	byDef := make(map[uuid.UUID]string, len(opts))
	for _, o := range opts {
		byDef[o.DefinitionID] = o.ValueCode
	}
	ordered := append([]postgres.VariationAxis(nil), axes...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Position < ordered[j].Position })

	parts := make([]string, 0, len(ordered))
	for _, a := range ordered {
		parts = append(parts, codeByDef[a.DefinitionID]+"="+byDef[a.DefinitionID])
	}
	return strings.Join(parts, "|")
}
