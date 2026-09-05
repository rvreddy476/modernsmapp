package service

// Validating a product's ANSWERS against the definitions that asked the
// questions, and serving them back on the product detail page.
//
// ─── WHY THE SERVER VALIDATES AT ALL ────────────────────────────────────
//
// The form the client renders already carries the bounds — AttributeSchemaFor
// serves min_num, max_len, the regex and the enum options — so a well-behaved
// client will not send a bad value. That is exactly the argument that leaves a
// catalogue full of them. The schema is versioned and cached: a client holding
// yesterday's copy is offering yesterday's bounds, an operator can tighten a
// bound between the form opening and the seller pressing save, and neither the
// bulk importer nor a partner API is rendering a form at all. Client-side
// checking is a courtesy to the seller; this is the one that decides what gets
// stored.
//
// And a value that gets in wrong does not fail loudly later. `pages: "many"`
// is simply a book that never appears under any page-count filter, which
// nobody reports as a bug.
//
// ─── ONE VALIDATION, TWO CALLERS ────────────────────────────────────────
//
// The rules live beside ValidateAttributeDefinition in attributes.go, and the
// pieces both halves need — the error type that names the field, the regex
// compile — are shared rather than reimplemented. See compileAttributeRegex
// for why that particular one had to be.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ErrTooManyAttributeValues is returned when a multi-valued field is sent more
// values than its definition's max_values allows.
var ErrTooManyAttributeValues = errors.New("commerce: too many values for this attribute")

// ─── The wire shape ─────────────────────────────────────────────────────

// AttributeValueInput is one field's answer as a caller sends it.
//
// `Value` is `any` because the type it should be is a property of the
// DEFINITION, not of the request: `pages` is a number, `author` a string,
// `in_print` a bool, `language` an array. Decoding it into a typed field per
// data type would need twelve request shapes, and a client would have to know
// which one to use before it could read the schema that tells it.
//
// A `nil` Value is not "no answer" — it is "delete this field's answer", which
// is the only way a seller clears a value they set by mistake. Omitting the
// code entirely is what means "leave it alone".
type AttributeValueInput struct {
	Code     string  `json:"code"`
	Value    any     `json:"value"`
	UnitCode *string `json:"unit_code,omitempty"`
}

// ProductAttributeDoc is one attribute as the product detail page renders it.
//
// Codes go on the wire alongside labels: the code is what a client filters,
// links and stores against, and the label is what it prints. A response
// carrying only the label would make renaming a field — the no-deploy change
// the whole registry exists to allow — break every client that had keyed
// anything on it.
type ProductAttributeDoc struct {
	Code         string  `json:"code"`
	Label        string  `json:"label"`
	DataType     string  `json:"data_type"`
	Value        any     `json:"value"`
	UnitCode     *string `json:"unit_code,omitempty"`
	DisplayGroup string  `json:"display_group"`
}

// ─── Validation ─────────────────────────────────────────────────────────

// attributeValueContext is everything ValidateAttributeValue needs besides the
// definition itself: the option codes an enum accepts, and the units its
// family contains.
//
// Passed in rather than fetched inside, so validating twenty fields is two
// queries rather than forty.
type attributeValueContext struct {
	enumCodes map[string]bool
	units     map[string]bool
}

// ValidateAttributeValue checks one field's answer against its definition and
// returns the typed rows to store.
//
// Returns one row for a single-valued field and n for a multi_enum, with
// Position left at zero — the store assigns it, because it is a fact about
// storage rather than about the answer.
func validateAttributeValue(
	d *postgres.AttributeDefinition,
	in AttributeValueInput,
	vc attributeValueContext,
) ([]postgres.ProductAttributeValue, error) {

	// A multi-valued field takes a list; every other field takes a scalar.
	// Accepting a bare scalar for a multi_enum too would be a kindness that
	// costs the caller the ability to send exactly one value and mean it.
	if d.DataType == "multi_enum" {
		list, ok := in.Value.([]any)
		if !ok {
			return nil, invalidAttr(d.Code, "must be a list of option codes")
		}
		if d.MaxValues != nil && len(list) > *d.MaxValues {
			return nil, fmt.Errorf("%w: %s accepts at most %d, got %d",
				ErrTooManyAttributeValues, d.Code, *d.MaxValues, len(list))
		}
		seen := map[string]bool{}
		out := make([]postgres.ProductAttributeValue, 0, len(list))
		for _, item := range list {
			code, ok := item.(string)
			if !ok {
				return nil, invalidAttr(d.Code, "every value must be an option code")
			}
			if !vc.enumCodes[code] {
				return nil, invalidAttr(d.Code, fmt.Sprintf("%q is not one of this field's options", code))
			}
			// The same option twice is not a second answer; it is a bug in the
			// caller that would otherwise consume a max_values slot and show
			// the buyer "English, English".
			if seen[code] {
				return nil, invalidAttr(d.Code, fmt.Sprintf("%q is selected twice", code))
			}
			seen[code] = true
			c := code
			out = append(out, postgres.ProductAttributeValue{ValueText: &c})
		}
		return out, nil
	}

	// A unit is only ever legal on a measure, and only from its own family.
	// The CHECK constraint stops a unit landing beside a non-number; this
	// stops 'kg' landing on a LENGTH, which the constraint cannot see.
	if in.UnitCode != nil && *in.UnitCode != "" {
		if d.DataType != "measure" {
			return nil, invalidAttr(d.Code, "only a measure attribute takes a unit")
		}
		if !vc.units[*in.UnitCode] {
			fam := ""
			if d.UnitFamily != nil {
				fam = *d.UnitFamily
			}
			return nil, invalidAttr(d.Code,
				fmt.Sprintf("unit %q does not belong to the %q family this field measures", *in.UnitCode, fam))
		}
	}

	row := postgres.ProductAttributeValue{}

	switch d.DataType {

	case "text", "long_text", "gtin":
		s, ok := in.Value.(string)
		if !ok {
			return nil, invalidAttr(d.Code, "must be text")
		}
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, invalidAttr(d.Code, "must not be blank")
		}
		// Runes, not bytes. max_len is a limit a seller was shown in a form,
		// and counting bytes makes a 60-character Devanagari title fail a
		// 100-character limit for reasons nothing on screen explains.
		if n := len([]rune(s)); (d.MinLen != nil && n < *d.MinLen) || (d.MaxLen != nil && n > *d.MaxLen) {
			return nil, invalidAttr(d.Code, lengthReason(d.MinLen, d.MaxLen, n))
		}
		re, err := compileAttributeRegex(d.Regex)
		if err != nil {
			// The definition stores a pattern that does not compile. That is
			// the definition's fault, not this value's, and reporting it as
			// "your ISBN is invalid" sends the seller to fix something that
			// is not wrong.
			return nil, invalidAttr(d.Code, "this field's validation pattern is misconfigured; contact support")
		}
		if re != nil && !re.MatchString(s) {
			return nil, invalidAttr(d.Code, "does not match the required format")
		}
		row.ValueText = &s

	case "enum":
		s, ok := in.Value.(string)
		if !ok {
			return nil, invalidAttr(d.Code, "must be an option code")
		}
		if !vc.enumCodes[s] {
			return nil, invalidAttr(d.Code, fmt.Sprintf("%q is not one of this field's options", s))
		}
		row.ValueText = &s

	case "integer", "money_minor":
		n, err := toFloat(in.Value)
		if err != nil {
			return nil, invalidAttr(d.Code, "must be a whole number")
		}
		if n != float64(int64(n)) {
			return nil, invalidAttr(d.Code, "must be a whole number")
		}
		if err := checkNumBounds(d, n); err != nil {
			return nil, err
		}
		row.ValueNum = &n

	case "decimal", "measure":
		n, err := toFloat(in.Value)
		if err != nil {
			return nil, invalidAttr(d.Code, "must be a number")
		}
		if err := checkNumBounds(d, n); err != nil {
			return nil, err
		}
		row.ValueNum = &n
		// A measure with no unit falls back to the definition's default. It is
		// NOT left null: `250` with no unit is not a weight, and the doc's
		// {value, unit} shape would be half-built.
		if in.UnitCode != nil && *in.UnitCode != "" {
			row.UnitCode = in.UnitCode
		} else if d.DataType == "measure" {
			if d.DefaultUnit == nil || *d.DefaultUnit == "" {
				return nil, invalidAttr(d.Code, "needs a unit; this field has no default to fall back on")
			}
			row.UnitCode = d.DefaultUnit
		}

	case "boolean":
		b, ok := in.Value.(bool)
		if !ok {
			return nil, invalidAttr(d.Code, "must be true or false")
		}
		row.ValueBool = &b

	case "date":
		s, ok := in.Value.(string)
		if !ok {
			return nil, invalidAttr(d.Code, "must be a date as YYYY-MM-DD")
		}
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			return nil, invalidAttr(d.Code, "must be a date as YYYY-MM-DD")
		}
		row.ValueDate = &t

	case "media":
		s, ok := in.Value.(string)
		if !ok {
			return nil, invalidAttr(d.Code, "must be a media id")
		}
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, invalidAttr(d.Code, "must be a media id")
		}
		row.ValueMediaID = &id

	default:
		// Unreachable while the CHECK constraint on data_type holds. Refused
		// rather than stored as text: a data type this build does not know is
		// one a NEWER build introduced, and guessing its storage would put
		// values in a column the newer build does not read.
		return nil, invalidAttr(d.Code, fmt.Sprintf("unsupported data type %q", d.DataType))
	}

	return []postgres.ProductAttributeValue{row}, nil
}

// checkNumBounds applies the definition's min_num / max_num to a value.
func checkNumBounds(d *postgres.AttributeDefinition, n float64) error {
	if d.MinNum != nil && n < *d.MinNum {
		return invalidAttr(d.Code, fmt.Sprintf("must be at least %s", trimFloat(*d.MinNum)))
	}
	if d.MaxNum != nil && n > *d.MaxNum {
		return invalidAttr(d.Code, fmt.Sprintf("must be at most %s", trimFloat(*d.MaxNum)))
	}
	return nil
}

func lengthReason(min, max *int, got int) string {
	switch {
	case min != nil && got < *min:
		return fmt.Sprintf("must be at least %d characters, got %d", *min, got)
	default:
		return fmt.Sprintf("must be at most %d characters, got %d", *max, got)
	}
}

// toFloat accepts the shapes a JSON number can arrive as.
//
// encoding/json gives float64; a caller decoding with UseNumber gives
// json.Number; and a bulk import from a CSV gives a string. A string is
// accepted because refusing it would fail every CSV import over a
// representation detail, but it is PARSED rather than trusted, so "many" is
// still rejected.
func toFloat(v any) (float64, error) {
	switch n := v.(type) {
	case float64:
		return n, nil
	case float32:
		return float64(n), nil
	case int:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case string:
		return strconv.ParseFloat(strings.TrimSpace(n), 64)
	default:
		if s, ok := v.(interface{ String() string }); ok { // json.Number
			return strconv.ParseFloat(s.String(), 64)
		}
		return 0, fmt.Errorf("not a number")
	}
}

func trimFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// ─── Write ──────────────────────────────────────────────────────────────

// SetProductAttributeValues validates a set of answers and stores them.
//
// Every value is validated BEFORE anything is written. A per-field write that
// validated as it went would leave the product half-updated when the
// nineteenth field is wrong, and the seller would see some of their edit
// applied and some not, with no way to tell which.
//
// No HTTP route calls this yet — the write endpoint is the next step. It is
// here now because the read side and the doc projection are meaningless
// without the writer that maintains them, and because a storage design is
// worth proving before a handler depends on it.
func (s *Service) SetProductAttributeValues(
	ctx context.Context, productID uuid.UUID, inputs []AttributeValueInput,
) ([]ProductAttributeDoc, error) {

	if _, err := s.store.GetProductByID(ctx, productID); err != nil {
		return nil, err
	}

	codes := make([]string, 0, len(inputs))
	for _, in := range inputs {
		codes = append(codes, in.Code)
	}
	defs, err := s.store.AttributeDefinitionsByCodes(ctx, codes)
	if err != nil {
		return nil, err
	}

	ids := make([]uuid.UUID, 0, len(defs))
	families := []string{}
	for _, d := range defs {
		ids = append(ids, d.ID)
		if d.UnitFamily != nil && *d.UnitFamily != "" {
			families = append(families, *d.UnitFamily)
		}
	}
	enums, err := s.store.EnumCodeSetsFor(ctx, ids)
	if err != nil {
		return nil, err
	}
	unitsByFamily, err := s.store.UnitsForFamilies(ctx, families)
	if err != nil {
		return nil, err
	}

	sets := make([]postgres.AttributeValueSet, 0, len(inputs))
	for _, in := range inputs {
		d := defs[in.Code]

		// nil means "clear this field": an empty set, which the store's
		// replace semantics turn into a delete.
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
			return nil, err
		}
		sets = append(sets, postgres.AttributeValueSet{DefinitionID: d.ID, Values: rows})
	}

	if err := s.store.PutProductAttributeValues(ctx, productID, sets); err != nil {
		return nil, err
	}
	return s.ProductAttributeValues(ctx, productID)
}

// ─── Read ───────────────────────────────────────────────────────────────

// ProductAttributeValues is the product detail page's attribute block.
//
// It reads the TYPED ROWS, never products.attributes_doc. The doc is a
// projection for search: it has no labels, no data types, no display groups
// and no order, so a detail page built from it could not draw a fieldset — and
// it is a denormalisation that can, in principle, lag. The rows are the source
// of truth and this is a buyer-facing surface.
func (s *Service) ProductAttributeValues(ctx context.Context, productID uuid.UUID) ([]ProductAttributeDoc, error) {
	rows, err := s.store.ProductAttributeValues(ctx, productID)
	if err != nil {
		return nil, err
	}

	// The store ordered within each group; the group order itself is
	// displayGroupOrder, which lives in this package precisely so it is
	// written down once. Sorting here rather than in the ORDER BY is what
	// stops the two copies drifting.
	sort.SliceStable(rows, func(i, j int) bool {
		gi, gj := groupSortOrder(rows[i].DisplayGroup), groupSortOrder(rows[j].DisplayGroup)
		if gi != gj {
			return gi < gj
		}
		if rows[i].SortOrder != rows[j].SortOrder {
			return rows[i].SortOrder < rows[j].SortOrder
		}
		if rows[i].Definition.Code != rows[j].Definition.Code {
			return rows[i].Definition.Code < rows[j].Definition.Code
		}
		return rows[i].Value.Position < rows[j].Value.Position
	})

	out := []ProductAttributeDoc{}
	// A multi_enum arrives as several adjacent rows and leaves as one entry
	// with a list value, keyed on code so the grouping cannot be confused by
	// two definitions sharing a group.
	byCode := map[string]int{}
	for _, r := range rows {
		v := renderAttributeValue(r)
		if idx, ok := byCode[r.Definition.Code]; ok {
			if list, isList := out[idx].Value.([]any); isList {
				out[idx].Value = append(list, v)
			}
			continue
		}
		doc := ProductAttributeDoc{
			Code:         r.Definition.Code,
			Label:        r.Definition.Label,
			DataType:     r.Definition.DataType,
			Value:        v,
			UnitCode:     r.Value.UnitCode,
			DisplayGroup: r.DisplayGroup,
		}
		if r.Definition.DataType == "multi_enum" {
			doc.Value = []any{v}
		}
		byCode[r.Definition.Code] = len(out)
		out = append(out, doc)
	}
	return out, nil
}

// renderAttributeValue unwraps the one populated column into a JSON scalar.
//
// The unit is NOT folded into the value here the way attributes_doc folds it.
// The doc has one field per code and has to carry the unit inside it; this
// shape has a dedicated unit_code, and a client that renders "250 g" wants the
// number and the unit apart so it can format them.
func renderAttributeValue(r *postgres.ProductAttributeValueRow) any {
	switch {
	case r.Value.ValueText != nil:
		return *r.Value.ValueText
	case r.Value.ValueNum != nil:
		return *r.Value.ValueNum
	case r.Value.ValueBool != nil:
		return *r.Value.ValueBool
	case r.Value.ValueDate != nil:
		return r.Value.ValueDate.Format("2006-01-02")
	case r.Value.ValueMediaID != nil:
		return r.Value.ValueMediaID.String()
	default:
		// The CHECK constraint makes this unreachable. nil rather than "" so
		// that if it ever happens it is visibly absent instead of looking like
		// a seller who typed an empty string.
		return nil
	}
}
