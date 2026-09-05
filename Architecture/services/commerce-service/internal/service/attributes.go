package service

// Authoring product attributes, and serving the form a category asks for.
//
// ─── THE TWO HALVES ─────────────────────────────────────────────────────
//
// READ  — AttributeSchemaFor answers "what does this category ask for", with
//         inheritance resolved, options and units inlined, grouped and
//         ordered the way the form should render. It is the only thing any
//         client needs to draw a create screen, and it is cacheable: the
//         ETag is the published version plus the newest edit behind it.
//
// WRITE — the admin surface. Every write is validated, recorded as a
//         before/after revision, and marks the draft dirty. None of them
//         changes what the READ half serves until somebody publishes.
//
// ─── WHY THE SERVER REFUSES RATHER THAN GUESSES ─────────────────────────
//
// This follows the tax-class precedent exactly. A product with no tax class is
// unsellable and the server refuses to pick a rate, because 18% on a 5% item
// overcharges every buyer. The same reasoning applies here in three places:
//
//	an unknown category id     404, not an empty form. An empty form and a
//	                           mistyped id are the same screen to a client,
//	                           and it renders the mistake as "this category
//	                           has no fields" instead of "you sent a bad id".
//
//	a `measure` with no family  refused at create. A weight with no unit is a
//	                           number, and whatever the form guesses — grams,
//	                           because the seed uses grams — is wrong for the
//	                           first seller who thinks in kilograms.
//
//	a narrowing edit           refused unless the operator quotes back how
//	                           many live listings it breaks. The server knows
//	                           the number; ticking "required" without seeing
//	                           it is the mistake that is expensive to undo.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ─── Errors ─────────────────────────────────────────────────────────────

// ErrAttributeCodeImmutable is returned when a patch tries to change a
// definition's `code`.
//
// The code is the join key to `product_attributes.name`. Renaming it does not
// rename the values already stored, so the definition and every product that
// answered it silently stop referring to the same thing — and the only symptom
// is a filter that quietly returns fewer results. Create a new definition and
// retire the old one instead; that leaves the old values readable.
var ErrAttributeCodeImmutable = errors.New("commerce: an attribute definition's code cannot be changed after it is created")

// ErrAttributeCodeTaken is returned when a create collides on `code`.
var ErrAttributeCodeTaken = errors.New("commerce: an attribute definition with that code already exists")

// ErrEnumCodeDuplicate is returned when two options of one definition share a
// code.
var ErrEnumCodeDuplicate = errors.New("commerce: enum option codes must be unique within a definition")

// ErrCategoryCycle is returned when a re-parent would make a category its own
// ancestor.
var ErrCategoryCycle = errors.New("commerce: a category cannot be moved beneath itself")

// AttributeValidationError names the field that is wrong and why.
//
// A single "invalid definition" error is useless in a form with twenty
// controls: the operator has to guess which one. This carries the field.
type AttributeValidationError struct {
	Field  string
	Reason string
}

func (e *AttributeValidationError) Error() string {
	return fmt.Sprintf("commerce: attribute definition field %q is invalid: %s", e.Field, e.Reason)
}

func invalidAttr(field, reason string) error {
	return &AttributeValidationError{Field: field, Reason: reason}
}

// ImpactAckError is the refusal a narrowing edit gets when it has not quoted
// the damage back.
//
// `Required` is the number the caller must send as `ack_impact`. It is carried
// on the error rather than merely logged so the 409 body can state it — an
// operator who is told only "acknowledge the impact" has to go and fetch the
// number by hand, and the one who does not bother sends a guess.
type ImpactAckError struct {
	// What narrowed, in words, e.g. "pages becomes required".
	What     string
	Required int
	Provided *int
	Impacts  []*postgres.AttributeImpact
}

func (e *ImpactAckError) Error() string {
	return fmt.Sprintf("commerce: %s affects %d live listings; re-send with ack_impact=%d",
		e.What, e.Required, e.Required)
}

// ─── The served document ────────────────────────────────────────────────

// AttributeSchemaDoc is the form definition for one category.
type AttributeSchemaDoc struct {
	CategoryID    uuid.UUID           `json:"category_id"`
	CategoryPath  []string            `json:"category_path"`
	SchemaVersion int                 `json:"schema_version"`
	VariationAxes []string            `json:"variation_axes"`
	Groups        []AttributeGroupDoc `json:"groups"`

	// ETag is served as a header, not as a body field.
	ETag string `json:"-"`
}

// AttributeGroupDoc is one fieldset of the form.
type AttributeGroupDoc struct {
	Name       string              `json:"name"`
	SortOrder  int                 `json:"sort_order"`
	Attributes []AttributeFieldDoc `json:"attributes"`
}

// AttributeFieldDoc is one control.
type AttributeFieldDoc struct {
	Code        string  `json:"code"`
	Label       string  `json:"label"`
	DataType    string  `json:"data_type"`
	Required    bool    `json:"required"`
	Scope       string  `json:"scope"`
	HelpText    *string `json:"help_text,omitempty"`
	Placeholder *string `json:"placeholder,omitempty"`
	Regex       *string `json:"regex,omitempty"`

	MinNum    *float64 `json:"min_num,omitempty"`
	MaxNum    *float64 `json:"max_num,omitempty"`
	MinLen    *int     `json:"min_len,omitempty"`
	MaxLen    *int     `json:"max_len,omitempty"`
	MaxValues *int     `json:"max_values,omitempty"`

	IsFilterable  bool `json:"is_filterable"`
	IsSearchable  bool `json:"is_searchable"`
	IsVariantAxis bool `json:"is_variant_axis"`
	SortOrder     int  `json:"sort_order"`

	// Values is present for enum and multi_enum.
	Values []AttributeOptionDoc `json:"values,omitempty"`

	// UnitFamily/DefaultUnit/Units are present for measure.
	UnitFamily  *string                  `json:"unit_family,omitempty"`
	DefaultUnit *string                  `json:"default_unit,omitempty"`
	Units       []postgres.AttributeUnit `json:"units,omitempty"`

	// LookupEndpoint is ALWAYS present, and is null today.
	//
	// A future enum with ten thousand options — brands, or ISBN publishers —
	// cannot ship its option list inline, and will carry the URL a client
	// types into instead. Adding the field then would be a contract change
	// every client has to be told about; shipping it null now means the
	// change is one row's value, and a client written today can already ask
	// "is this a lookup or a list?" and get an answer.
	LookupEndpoint *string `json:"lookup_endpoint"`
}

// AttributeOptionDoc is one enum option as the form renders it.
type AttributeOptionDoc struct {
	Code          string     `json:"code"`
	Label         string     `json:"label"`
	SortOrder     int        `json:"sort_order"`
	SwatchHex     *string    `json:"swatch_hex,omitempty"`
	SwatchMediaID *uuid.UUID `json:"swatch_media_id,omitempty"`
}

// displayGroupOrder is the order the fieldsets are drawn in.
//
// It lives here, not in a table. The list is closed by a CHECK constraint on
// the column, so a group cannot be invented at runtime, and the order is a
// property of the FORM — identity first, then what the thing is, then what
// this seller is offering — rather than a property of any row. A table would
// let the two disagree.
var displayGroupOrder = map[string]int{
	"Product Identity":    10,
	"Description":         20,
	"Product Details":     30,
	"Offer":               40,
	"Safety & Compliance": 50,
	"Logistics":           60,
}

func groupSortOrder(name string) int {
	if n, ok := displayGroupOrder[name]; ok {
		return n
	}
	// Unreachable while the CHECK constraint holds. A group it does not know
	// sorts last rather than first, so an unexpected one cannot push
	// "Product Identity" below the fold.
	return 1000
}

// ─── Read ───────────────────────────────────────────────────────────────

// AttributeScopeAll is the default: every field, item and offer alike.
const (
	AttributeScopeAll   = "all"
	AttributeScopeItem  = "item"
	AttributeScopeOffer = "offer"
)

// ErrInvalidAttributeScope is returned for a ?scope= the endpoint does not
// know. Refused rather than defaulted: a client that sent `scope=items` and
// silently got everything ships a form asking for the seller's own offer
// fields on a shared item record.
var ErrInvalidAttributeScope = errors.New("commerce: scope must be one of item, offer, all")

// AttributeSchemaFor builds the form definition for a category.
func (s *Service) AttributeSchemaFor(ctx context.Context, categoryID uuid.UUID, scope string) (*AttributeSchemaDoc, error) {
	switch scope {
	case "", AttributeScopeAll:
		scope = AttributeScopeAll
	case AttributeScopeItem, AttributeScopeOffer:
	default:
		return nil, ErrInvalidAttributeScope
	}

	// Existence first, and on its own. Every other query here answers
	// "nothing" for an unknown id, and "nothing" is a legitimate answer for a
	// real category nobody has bound anything to yet.
	exists, err := s.store.CategoryExists(ctx, categoryID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, postgres.ErrCategoryNotFound
	}

	path, err := s.store.CategoryPath(ctx, categoryID)
	if err != nil {
		return nil, err
	}
	effective, err := s.store.EffectiveCategoryAttributes(ctx, categoryID)
	if err != nil {
		return nil, err
	}
	state, err := s.store.GetAttributeSchemaState(ctx)
	if err != nil {
		return nil, err
	}

	// Two batched side-loads, chosen by what the fields actually need.
	enumIDs := []uuid.UUID{}
	familySet := map[string]bool{}
	for _, ea := range effective {
		switch ea.Definition.DataType {
		case "enum", "multi_enum":
			enumIDs = append(enumIDs, ea.Definition.ID)
		case "measure":
			if ea.Definition.UnitFamily != nil {
				familySet[*ea.Definition.UnitFamily] = true
			}
		}
	}
	families := make([]string, 0, len(familySet))
	for f := range familySet {
		families = append(families, f)
	}
	sort.Strings(families)

	options, err := s.store.EnumValuesForDefinitions(ctx, enumIDs)
	if err != nil {
		return nil, err
	}
	units, err := s.store.UnitsForFamilies(ctx, families)
	if err != nil {
		return nil, err
	}

	return buildAttributeSchema(categoryID, path, scope, state, effective, options, units), nil
}

// buildAttributeSchema is the pure assembly step.
//
// Split out from AttributeSchemaFor deliberately: everything above it is I/O
// and everything below it is the contract, so the contract can be tested — the
// grouping, the ordering, the ETag, which fields carry `values` and which
// carry `units` — without a database.
func buildAttributeSchema(
	categoryID uuid.UUID,
	path []string,
	scope string,
	state *postgres.AttributeSchemaState,
	effective []*postgres.EffectiveAttribute,
	options map[uuid.UUID][]*postgres.AttributeEnumValue,
	units map[string][]postgres.AttributeUnit,
) *AttributeSchemaDoc {

	doc := &AttributeSchemaDoc{
		CategoryID:    categoryID,
		CategoryPath:  path,
		SchemaVersion: state.PublishedVersion,
		VariationAxes: []string{},
		Groups:        []AttributeGroupDoc{},
	}
	if doc.CategoryPath == nil {
		doc.CategoryPath = []string{}
	}

	// newest is the freshest edit behind this particular form — the second
	// half of the ETag. It is the max over the definitions IN THIS RESPONSE
	// and their options, not over the whole table: a definition edited in
	// another category's form does not change these bytes, and an ETag that
	// moved anyway would make every client refetch every form on every edit.
	var newest time.Time

	byGroup := map[string][]AttributeFieldDoc{}
	for _, ea := range effective {
		d := ea.Definition
		if scope != AttributeScopeAll && d.AppliesTo != scope {
			continue
		}
		if d.UpdatedAt.After(newest) {
			newest = d.UpdatedAt
		}

		f := AttributeFieldDoc{
			Code:          d.Code,
			Label:         d.Label,
			DataType:      d.DataType,
			Required:      ea.IsRequired,
			Scope:         d.AppliesTo,
			HelpText:      d.HelpText,
			Placeholder:   d.Placeholder,
			Regex:         d.Regex,
			MinNum:        d.MinNum,
			MaxNum:        d.MaxNum,
			MinLen:        d.MinLen,
			MaxLen:        d.MaxLen,
			MaxValues:     d.MaxValues,
			IsFilterable:  d.IsFilterable,
			IsSearchable:  d.IsSearchable,
			IsVariantAxis: ea.IsVariantAxis,
			SortOrder:     ea.SortOrder,
		}

		switch d.DataType {
		case "enum", "multi_enum":
			for _, o := range options[d.ID] {
				if o.UpdatedAt.After(newest) {
					newest = o.UpdatedAt
				}
				f.Values = append(f.Values, AttributeOptionDoc{
					Code:          o.Code,
					Label:         o.Label,
					SortOrder:     o.SortOrder,
					SwatchHex:     o.SwatchHex,
					SwatchMediaID: o.SwatchMediaID,
				})
			}
		case "measure":
			f.UnitFamily = d.UnitFamily
			f.DefaultUnit = d.DefaultUnit
			if d.UnitFamily != nil {
				f.Units = units[*d.UnitFamily]
			}
		}

		if ea.IsVariantAxis {
			doc.VariationAxes = append(doc.VariationAxes, d.Code)
		}
		byGroup[ea.DisplayGroup] = append(byGroup[ea.DisplayGroup], f)
	}

	names := make([]string, 0, len(byGroup))
	for name := range byGroup {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		oi, oj := groupSortOrder(names[i]), groupSortOrder(names[j])
		if oi != oj {
			return oi < oj
		}
		return names[i] < names[j]
	})
	for _, name := range names {
		doc.Groups = append(doc.Groups, AttributeGroupDoc{
			Name:       name,
			SortOrder:  groupSortOrder(name),
			Attributes: byGroup[name],
		})
	}

	doc.ETag = attributeSchemaETag(state.PublishedVersion, newest)
	return doc
}

// attributeSchemaETag is the published version plus the newest edit behind the
// response.
//
// Both halves are needed. The version alone would not change when a definition
// is edited and republished within the same second on a dev stack, and the
// timestamp alone would not change on a publish that only re-blesses what was
// already there — which is exactly the operation an operator performs to force
// every client to refetch.
//
// Weak (`W/`) because it is a semantic tag, not a byte hash: two responses with
// the same tag carry the same schema, but not necessarily the same key order or
// whitespace.
func attributeSchemaETag(version int, newest time.Time) string {
	return fmt.Sprintf("W/\"as-%d-%d\"", version, newest.UTC().UnixNano())
}

// ETagMatches reports whether an If-None-Match header covers `tag`.
//
// Handles the list form (`"a", "b"`), the wildcard, and the strong/weak
// spelling difference — a cache that stored `W/"x"` and sends back `"x"` is
// asking about the same entity, and answering 200 to it re-sends a body it
// already has.
func ETagMatches(ifNoneMatch, tag string) bool {
	if ifNoneMatch == "" || tag == "" {
		return false
	}
	normalise := func(s string) string {
		s = strings.TrimSpace(s)
		s = strings.TrimPrefix(s, "W/")
		return strings.Trim(s, `"`)
	}
	want := normalise(tag)
	for _, candidate := range strings.Split(ifNoneMatch, ",") {
		c := strings.TrimSpace(candidate)
		if c == "*" {
			return true
		}
		if normalise(c) == want {
			return true
		}
	}
	return false
}

// ─── Category tree ──────────────────────────────────────────────────────

// CategoryTree returns the taxonomy nested, optionally trimmed to `depth`
// levels. depth <= 0 means the whole thing.
func (s *Service) CategoryTree(ctx context.Context, includeInactive bool, depth int) ([]*postgres.CategoryTreeNode, error) {
	roots, err := s.store.CategoryTree(ctx, includeInactive)
	if err != nil {
		return nil, err
	}
	return postgres.PruneTreeDepth(roots, depth), nil
}

// ─── Validation ─────────────────────────────────────────────────────────

var attributeCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,48}$`)
var enumCodePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

var attributeDataTypes = map[string]bool{
	"text": true, "long_text": true, "integer": true, "decimal": true,
	"money_minor": true, "boolean": true, "enum": true, "multi_enum": true,
	"date": true, "measure": true, "media": true, "gtin": true,
}

var variantAxisDataTypes = map[string]bool{"enum": true, "text": true, "integer": true}

var attributeAppliesTo = map[string]bool{"item": true, "offer": true}

// ValidateAttributeDefinition is every rule the database also enforces, plus
// the two it cannot.
//
// The duplication with the CHECK constraints is on purpose. A constraint
// violation reaches the edge as an opaque 500 naming a constraint the operator
// has never heard of; this names the field. The constraints stay because a
// second writer — a psql session, a future service — is not going through
// here.
//
// The two rules that are ONLY here:
//
//	the regex must COMPILE. Postgres would accept `[unclosed` as a string
//	quite happily and every product validated against it would then fail on a
//	server-side error rather than a validation message.
//
//	a `measure` must name a default unit its family contains — checked against
//	the family the caller sent, before the composite foreign key turns it into
//	a 500.
func ValidateAttributeDefinition(d *postgres.AttributeDefinition) error {
	if !attributeCodePattern.MatchString(d.Code) {
		return invalidAttr("code", "must be lower snake case, start with a letter, and be 2 to 49 characters")
	}
	if strings.TrimSpace(d.Label) == "" {
		return invalidAttr("label", "is required")
	}
	if !attributeDataTypes[d.DataType] {
		return invalidAttr("data_type", "must be one of text, long_text, integer, decimal, money_minor, boolean, enum, multi_enum, date, measure, media, gtin")
	}
	if !attributeAppliesTo[d.AppliesTo] {
		return invalidAttr("applies_to", "must be item or offer")
	}
	if _, ok := displayGroupOrder[d.DisplayGroup]; !ok {
		return invalidAttr("display_group", "must be one of Product Identity, Description, Product Details, Offer, Safety & Compliance, Logistics")
	}
	if d.DataType == "measure" && (d.UnitFamily == nil || *d.UnitFamily == "") {
		return invalidAttr("unit_family", "a measure attribute must name a unit family; a number with no unit cannot be compared or converted")
	}
	if d.DataType != "measure" && d.UnitFamily != nil && *d.UnitFamily != "" {
		return invalidAttr("unit_family", "only a measure attribute may name a unit family")
	}
	if d.DefaultUnit != nil && *d.DefaultUnit != "" && (d.UnitFamily == nil || *d.UnitFamily == "") {
		return invalidAttr("default_unit", "a default unit needs the family it belongs to")
	}
	if d.IsVariantAxis && !variantAxisDataTypes[d.DataType] {
		return invalidAttr("is_variant_axis", "only enum, text and integer attributes can be a variant axis; anything else produces one variant per keystroke")
	}
	if d.MinNum != nil && d.MaxNum != nil && *d.MinNum > *d.MaxNum {
		return invalidAttr("min_num", "must not be greater than max_num")
	}
	if d.MinLen != nil && d.MaxLen != nil && *d.MinLen > *d.MaxLen {
		return invalidAttr("min_len", "must not be greater than max_len")
	}
	if d.MinLen != nil && *d.MinLen < 0 {
		return invalidAttr("min_len", "must not be negative")
	}
	if d.MaxValues != nil && *d.MaxValues < 1 {
		return invalidAttr("max_values", "must be at least 1")
	}
	if _, err := compileAttributeRegex(d.Regex); err != nil {
		return invalidAttr("regex", "does not compile: "+err.Error())
	}
	return nil
}

// compileAttributeRegex compiles a definition's `regex`, treating nil and ""
// alike as "no pattern".
//
// Shared by both halves of attribute validation, and it has to be: the
// DEFINITION side compiles the pattern to prove it is a pattern at all
// (Postgres stores `[unclosed` quite happily, and every product validated
// against it would then fail on a server error rather than a message), and the
// VALUE side compiles the same pattern to match a value with. Two call sites
// with two `regexp.Compile` calls is two chances for them to disagree about
// what an empty pattern means — and the disagreement that matters is the one
// where the value side treats "" as a pattern that matches nothing, silently
// rejecting every value of a field whose regex was cleared.
func compileAttributeRegex(pattern *string) (*regexp.Regexp, error) {
	if pattern == nil || *pattern == "" {
		return nil, nil
	}
	return regexp.Compile(*pattern)
}

// validateEnumCodesUnique checks the option codes of one definition.
func validateEnumCodesUnique(codes []string) error {
	seen := map[string]bool{}
	for _, c := range codes {
		if !enumCodePattern.MatchString(c) {
			return invalidAttr("values[].code", fmt.Sprintf("%q must be lower alphanumeric with _ or -, 1 to 64 characters", c))
		}
		if seen[c] {
			return fmt.Errorf("%w: %s", ErrEnumCodeDuplicate, c)
		}
		seen[c] = true
	}
	return nil
}

// ─── Admin: definitions ─────────────────────────────────────────────────

// ListAttributeDefinitions is the admin console's catalogue of fields.
func (s *Service) ListAttributeDefinitions(ctx context.Context, includeInactive bool) ([]*postgres.AttributeDefinition, error) {
	return s.store.ListAttributeDefinitions(ctx, includeInactive)
}

// AttributeDefinitionDetail is a definition together with its options — the
// two things the edit screen needs and would otherwise fetch separately.
type AttributeDefinitionDetail struct {
	*postgres.AttributeDefinition
	Values []*postgres.AttributeEnumValue `json:"values"`
}

// GetAttributeDefinition reads one definition and its options, active ones
// included.
func (s *Service) GetAttributeDefinition(ctx context.Context, id uuid.UUID) (*AttributeDefinitionDetail, error) {
	d, err := s.store.GetAttributeDefinition(ctx, id)
	if err != nil {
		return nil, err
	}
	values, err := s.store.ListEnumValues(ctx, id, true)
	if err != nil {
		return nil, err
	}
	return &AttributeDefinitionDetail{AttributeDefinition: d, Values: values}, nil
}

// CreateAttributeDefinition validates, inserts, records revision 1, and marks
// the draft dirty.
//
// The code collision is checked before the insert rather than left to the
// UNIQUE constraint, so the refusal can say which definition already holds it.
func (s *Service) CreateAttributeDefinition(ctx context.Context, d *postgres.AttributeDefinition, actor uuid.UUID) (*postgres.AttributeDefinition, error) {
	if d.DisplayGroup == "" {
		d.DisplayGroup = "Product Details"
	}
	if d.AppliesTo == "" {
		d.AppliesTo = "item"
	}
	d.IsActive = true
	if err := ValidateAttributeDefinition(d); err != nil {
		return nil, err
	}
	if existing, err := s.store.GetAttributeDefinitionByCode(ctx, d.Code); err == nil {
		return nil, fmt.Errorf("%w: %s (%s)", ErrAttributeCodeTaken, existing.Code, existing.Label)
	} else if !errors.Is(err, postgres.ErrAttributeDefinitionNotFound) {
		return nil, err
	}
	if actor != uuid.Nil {
		d.CreatedBy = &actor
	}
	if err := s.store.CreateAttributeDefinition(ctx, d); err != nil {
		return nil, err
	}
	if err := s.store.RecordAttributeRevision(ctx, d.ID, d.Version, nil, d, actor); err != nil {
		return nil, err
	}
	return d, s.store.MarkAttributeSchemaDirty(ctx)
}

// PatchAttributeDefinition applies the fields PRESENT in the request body.
//
// Presence, not zero-ness: a patch that sets `regex` to null is clearing it,
// and one that omits `regex` is leaving it alone. A struct of pointers cannot
// tell those apart, so the raw JSON object is carried this far and the keys
// are read from it.
//
// `ack` is the operator's acknowledgement of the impact. It is required only
// when the patch NARROWS — see narrowingOf — and must equal the current
// affected count. Equality rather than "at least": a stale number means the
// operator was looking at a different catalogue than the one they are changing.
func (s *Service) PatchAttributeDefinition(ctx context.Context, id uuid.UUID, raw map[string]json.RawMessage,
	ack *int, actor uuid.UUID) (*postgres.AttributeDefinition, error) {

	before, err := s.store.GetAttributeDefinition(ctx, id)
	if err != nil {
		return nil, err
	}
	if _, present := raw["code"]; present {
		var code string
		if err := json.Unmarshal(raw["code"], &code); err == nil && code != before.Code {
			return nil, ErrAttributeCodeImmutable
		}
	}

	after := *before
	if err := applyDefinitionPatch(&after, raw); err != nil {
		return nil, err
	}
	if err := ValidateAttributeDefinition(&after); err != nil {
		return nil, err
	}

	if what := narrowingOf(before, &after); what != "" {
		imp, err := s.store.AttributeImpact(ctx, id)
		if err != nil {
			return nil, err
		}
		if ack == nil || *ack != imp.Affected {
			return nil, &ImpactAckError{
				What:     fmt.Sprintf("%s: %s", before.Code, what),
				Required: imp.Affected,
				Provided: ack,
				Impacts:  []*postgres.AttributeImpact{imp},
			}
		}
	}

	if err := s.store.UpdateAttributeDefinition(ctx, &after); err != nil {
		return nil, err
	}
	if err := s.store.RecordAttributeRevision(ctx, id, after.Version, before, &after, actor); err != nil {
		return nil, err
	}
	return &after, s.store.MarkAttributeSchemaDirty(ctx)
}

// applyDefinitionPatch copies the present keys onto `d`.
//
// Every key is listed explicitly. A reflective copy would silently accept
// `version`, `created_at` or `id` from the request body, and the first caller
// to send one would rewrite the audit trail's idea of what happened.
func applyDefinitionPatch(d *postgres.AttributeDefinition, raw map[string]json.RawMessage) error {
	str := func(key string, dst *string) error {
		if v, ok := raw[key]; ok {
			return json.Unmarshal(v, dst)
		}
		return nil
	}
	strPtr := func(key string, dst **string) error {
		if v, ok := raw[key]; ok {
			return json.Unmarshal(v, dst)
		}
		return nil
	}
	intPtr := func(key string, dst **int) error {
		if v, ok := raw[key]; ok {
			return json.Unmarshal(v, dst)
		}
		return nil
	}
	floatPtr := func(key string, dst **float64) error {
		if v, ok := raw[key]; ok {
			return json.Unmarshal(v, dst)
		}
		return nil
	}
	boolean := func(key string, dst *bool) error {
		if v, ok := raw[key]; ok {
			return json.Unmarshal(v, dst)
		}
		return nil
	}
	for _, f := range []func() error{
		func() error { return str("label", &d.Label) },
		func() error { return str("data_type", &d.DataType) },
		func() error { return str("display_group", &d.DisplayGroup) },
		func() error { return str("applies_to", &d.AppliesTo) },
		func() error { return strPtr("help_text", &d.HelpText) },
		func() error { return strPtr("placeholder", &d.Placeholder) },
		func() error { return strPtr("unit_family", &d.UnitFamily) },
		func() error { return strPtr("default_unit", &d.DefaultUnit) },
		func() error { return strPtr("regex", &d.Regex) },
		func() error { return floatPtr("min_num", &d.MinNum) },
		func() error { return floatPtr("max_num", &d.MaxNum) },
		func() error { return intPtr("min_len", &d.MinLen) },
		func() error { return intPtr("max_len", &d.MaxLen) },
		func() error { return intPtr("max_values", &d.MaxValues) },
		func() error { return boolean("is_variant_axis", &d.IsVariantAxis) },
		func() error { return boolean("is_filterable", &d.IsFilterable) },
		func() error { return boolean("is_searchable", &d.IsSearchable) },
		func() error { return boolean("is_active", &d.IsActive) },
	} {
		if err := f(); err != nil {
			return invalidAttr("body", "could not be decoded: "+err.Error())
		}
	}
	return nil
}

// narrowingOf names the first way `after` is stricter than `before`, or "".
//
// Stricter means: a value that was legal is no longer. Widening a bound,
// relaxing a regex, adding help text — none of these can invalidate a stored
// value, and none of them needs an acknowledgement.
func narrowingOf(before, after *postgres.AttributeDefinition) string {
	if before.IsActive && !after.IsActive {
		return "the field is being retired"
	}
	if before.DataType != after.DataType {
		return fmt.Sprintf("the data type changes from %s to %s", before.DataType, after.DataType)
	}
	if tightenedLower(before.MinNum, after.MinNum) {
		return "the minimum value is being raised"
	}
	if tightenedUpper(before.MaxNum, after.MaxNum) {
		return "the maximum value is being lowered"
	}
	if tightenedLowerInt(before.MinLen, after.MinLen) {
		return "the minimum length is being raised"
	}
	if tightenedUpperInt(before.MaxLen, after.MaxLen) {
		return "the maximum length is being lowered"
	}
	if tightenedUpperInt(before.MaxValues, after.MaxValues) {
		return "the number of permitted values is being reduced"
	}
	if derefStr(after.Regex) != "" && derefStr(before.Regex) != derefStr(after.Regex) {
		return "the format pattern is being added or changed"
	}
	return ""
}

// tightenedLower: a minimum that appears, or moves up.
func tightenedLower(before, after *float64) bool {
	if after == nil {
		return false
	}
	return before == nil || *after > *before
}

// tightenedUpper: a maximum that appears, or moves down.
func tightenedUpper(before, after *float64) bool {
	if after == nil {
		return false
	}
	return before == nil || *after < *before
}

func tightenedLowerInt(before, after *int) bool {
	if after == nil {
		return false
	}
	return before == nil || *after > *before
}

func tightenedUpperInt(before, after *int) bool {
	if after == nil {
		return false
	}
	return before == nil || *after < *before
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ─── Admin: enum values ─────────────────────────────────────────────────

// ListEnumValues returns one definition's options, retired ones included.
func (s *Service) ListEnumValues(ctx context.Context, definitionID uuid.UUID) ([]*postgres.AttributeEnumValue, error) {
	if _, err := s.store.GetAttributeDefinition(ctx, definitionID); err != nil {
		return nil, err
	}
	return s.store.ListEnumValues(ctx, definitionID, true)
}

// CreateEnumValue adds an option to an enum or multi_enum definition.
func (s *Service) CreateEnumValue(ctx context.Context, definitionID uuid.UUID, v *postgres.AttributeEnumValue, actor uuid.UUID) (*postgres.AttributeEnumValue, error) {
	d, err := s.store.GetAttributeDefinition(ctx, definitionID)
	if err != nil {
		return nil, err
	}
	if d.DataType != "enum" && d.DataType != "multi_enum" {
		return nil, invalidAttr("data_type", "only an enum or multi_enum attribute has options; this one is "+d.DataType)
	}
	existing, err := s.store.ListEnumValues(ctx, definitionID, true)
	if err != nil {
		return nil, err
	}
	codes := []string{v.Code}
	for _, e := range existing {
		codes = append(codes, e.Code)
	}
	if err := validateEnumCodesUnique(codes); err != nil {
		return nil, err
	}
	if strings.TrimSpace(v.Label) == "" {
		return nil, invalidAttr("label", "is required")
	}
	v.DefinitionID = definitionID
	v.IsActive = true
	if v.SortOrder == 0 {
		v.SortOrder = (len(existing) + 1) * 10
	}
	if err := s.store.CreateEnumValue(ctx, v); err != nil {
		return nil, err
	}
	if err := s.store.RecordAttributeRevision(ctx, definitionID, d.Version,
		nil, map[string]any{"enum_value_added": v}, actor); err != nil {
		return nil, err
	}
	return v, s.store.MarkAttributeSchemaDirty(ctx)
}

// PatchEnumValue edits one option.
//
// There is no DELETE. Products already store this option's code, and removing
// the row makes those values unreadable — the product would render a blank
// where its binding used to be, with nothing to explain why. `is_active =
// false` retires it: it disappears from the form and stays resolvable for
// every product that chose it. Retiring it is a NARROWING edit and needs the
// same acknowledgement a tightened bound does.
func (s *Service) PatchEnumValue(ctx context.Context, definitionID, valueID uuid.UUID,
	raw map[string]json.RawMessage, ack *int, actor uuid.UUID) (*postgres.AttributeEnumValue, error) {

	d, err := s.store.GetAttributeDefinition(ctx, definitionID)
	if err != nil {
		return nil, err
	}
	before, err := s.store.GetEnumValue(ctx, definitionID, valueID)
	if err != nil {
		return nil, err
	}
	if v, ok := raw["code"]; ok {
		var code string
		if err := json.Unmarshal(v, &code); err == nil && code != before.Code {
			return nil, ErrAttributeCodeImmutable
		}
	}

	after := *before
	if v, ok := raw["label"]; ok {
		if err := json.Unmarshal(v, &after.Label); err != nil {
			return nil, invalidAttr("label", "could not be decoded")
		}
	}
	if v, ok := raw["swatch_hex"]; ok {
		if err := json.Unmarshal(v, &after.SwatchHex); err != nil {
			return nil, invalidAttr("swatch_hex", "could not be decoded")
		}
	}
	if v, ok := raw["swatch_media_id"]; ok {
		if err := json.Unmarshal(v, &after.SwatchMediaID); err != nil {
			return nil, invalidAttr("swatch_media_id", "could not be decoded")
		}
	}
	if v, ok := raw["sort_order"]; ok {
		if err := json.Unmarshal(v, &after.SortOrder); err != nil {
			return nil, invalidAttr("sort_order", "could not be decoded")
		}
	}
	if v, ok := raw["is_active"]; ok {
		if err := json.Unmarshal(v, &after.IsActive); err != nil {
			return nil, invalidAttr("is_active", "could not be decoded")
		}
	}
	if strings.TrimSpace(after.Label) == "" {
		return nil, invalidAttr("label", "is required")
	}

	if before.IsActive && !after.IsActive {
		imp, err := s.store.AttributeImpact(ctx, definitionID)
		if err != nil {
			return nil, err
		}
		if ack == nil || *ack != imp.Affected {
			return nil, &ImpactAckError{
				What:     fmt.Sprintf("%s: the option %q is being retired", d.Code, before.Code),
				Required: imp.Affected,
				Provided: ack,
				Impacts:  []*postgres.AttributeImpact{imp},
			}
		}
	}

	if err := s.store.UpdateEnumValue(ctx, &after); err != nil {
		return nil, err
	}
	if err := s.store.RecordAttributeRevision(ctx, definitionID, d.Version,
		map[string]any{"enum_value": before}, map[string]any{"enum_value": after}, actor); err != nil {
		return nil, err
	}
	return &after, s.store.MarkAttributeSchemaDirty(ctx)
}

// ReorderEnumValues rewrites the option ordering.
//
// The request must list EVERY option, not a subset. A partial list leaves the
// omitted options holding their old sort_order, interleaved with the new one
// in a way nobody asked for.
func (s *Service) ReorderEnumValues(ctx context.Context, definitionID uuid.UUID, ordered []uuid.UUID, actor uuid.UUID) error {
	d, err := s.store.GetAttributeDefinition(ctx, definitionID)
	if err != nil {
		return err
	}
	existing, err := s.store.ListEnumValues(ctx, definitionID, true)
	if err != nil {
		return err
	}
	if len(ordered) != len(existing) {
		return invalidAttr("order", fmt.Sprintf("must list all %d options; got %d", len(existing), len(ordered)))
	}
	seen := map[uuid.UUID]bool{}
	for _, id := range ordered {
		if seen[id] {
			return invalidAttr("order", "lists the same option twice")
		}
		seen[id] = true
	}
	if err := s.store.ReorderEnumValues(ctx, definitionID, ordered); err != nil {
		return err
	}
	if err := s.store.RecordAttributeRevision(ctx, definitionID, d.Version,
		nil, map[string]any{"enum_values_reordered": ordered}, actor); err != nil {
		return err
	}
	return s.store.MarkAttributeSchemaDirty(ctx)
}

// ─── Admin: category bindings ───────────────────────────────────────────

// GetCategoryAttributeBindings returns a category's OWN bindings.
func (s *Service) GetCategoryAttributeBindings(ctx context.Context, categoryID uuid.UUID) ([]*postgres.CategoryAttribute, error) {
	exists, err := s.store.CategoryExists(ctx, categoryID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, postgres.ErrCategoryNotFound
	}
	return s.store.GetCategoryAttributes(ctx, categoryID)
}

// PutCategoryAttributeBindings replaces a category's own bindings.
//
// Narrowing here is a field becoming REQUIRED that was not required before —
// either newly marked, or newly bound as required. The acknowledgement is the
// SUM of the affected counts across every field that narrows, because the
// operator is being asked about one save, not one field, and asking for a
// number per field would mean a query string that grows with the form.
func (s *Service) PutCategoryAttributeBindings(ctx context.Context, categoryID uuid.UUID,
	bindings []postgres.CategoryAttribute, ack *int, actor uuid.UUID) ([]*postgres.CategoryAttribute, error) {

	exists, err := s.store.CategoryExists(ctx, categoryID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, postgres.ErrCategoryNotFound
	}

	seen := map[uuid.UUID]bool{}
	for i := range bindings {
		b := &bindings[i]
		if seen[b.DefinitionID] {
			return nil, invalidAttr("definition_id", "the same definition is bound twice: "+b.DefinitionID.String())
		}
		seen[b.DefinitionID] = true
		def, err := s.store.GetAttributeDefinition(ctx, b.DefinitionID)
		if err != nil {
			return nil, err
		}
		if b.IsRequired && b.IsExcluded {
			return nil, invalidAttr("is_excluded", "a field cannot be both required and excluded: "+def.Code)
		}
		if b.IsVariantAxis != nil && *b.IsVariantAxis && !variantAxisDataTypes[def.DataType] {
			return nil, invalidAttr("is_variant_axis", def.Code+" is a "+def.DataType+" and cannot be a variant axis")
		}
		if b.DisplayGroup != nil {
			if _, ok := displayGroupOrder[*b.DisplayGroup]; !ok {
				return nil, invalidAttr("display_group", "unknown group "+*b.DisplayGroup)
			}
		}
		b.CategoryID = categoryID
	}

	current, err := s.store.GetCategoryAttributes(ctx, categoryID)
	if err != nil {
		return nil, err
	}
	wasRequired := map[uuid.UUID]bool{}
	for _, c := range current {
		wasRequired[c.DefinitionID] = c.IsRequired
	}

	newlyRequired := []uuid.UUID{}
	for _, b := range bindings {
		if b.IsRequired && !wasRequired[b.DefinitionID] {
			newlyRequired = append(newlyRequired, b.DefinitionID)
		}
	}
	if len(newlyRequired) > 0 {
		total := 0
		impacts := []*postgres.AttributeImpact{}
		names := []string{}
		for _, id := range newlyRequired {
			imp, err := s.store.AttributeImpact(ctx, id)
			if err != nil {
				return nil, err
			}
			def, err := s.store.GetAttributeDefinition(ctx, id)
			if err != nil {
				return nil, err
			}
			names = append(names, def.Code)
			impacts = append(impacts, imp)
			total += imp.Affected
		}
		if ack == nil || *ack != total {
			return nil, &ImpactAckError{
				What:     strings.Join(names, ", ") + " becomes required",
				Required: total,
				Provided: ack,
				Impacts:  impacts,
			}
		}
	}

	if err := s.store.PutCategoryAttributes(ctx, categoryID, bindings); err != nil {
		return nil, err
	}
	if err := s.store.MarkAttributeSchemaDirty(ctx); err != nil {
		return nil, err
	}
	return s.store.GetCategoryAttributes(ctx, categoryID)
}

// ─── Admin: categories ──────────────────────────────────────────────────

// CreateCategory adds a taxonomy node.
func (s *Service) CreateCategory(ctx context.Context, n *postgres.CategoryTreeNode) (*postgres.CategoryTreeNode, error) {
	if strings.TrimSpace(n.Name) == "" {
		return nil, invalidAttr("name", "is required")
	}
	if strings.TrimSpace(n.Slug) == "" {
		return nil, invalidAttr("slug", "is required; it is the value deep links and saved filters keep")
	}
	if n.ParentID != nil {
		exists, err := s.store.CategoryExists(ctx, *n.ParentID)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, postgres.ErrCategoryNotFound
		}
	}
	if err := s.store.CreateCategory(ctx, n); err != nil {
		return nil, err
	}
	return s.store.GetCategoryNode(ctx, n.ID)
}

// PatchCategory edits a taxonomy node. There is no delete — see
// Store.UpdateCategory.
func (s *Service) PatchCategory(ctx context.Context, id uuid.UUID, raw map[string]json.RawMessage) (*postgres.CategoryTreeNode, error) {
	before, err := s.store.GetCategoryNode(ctx, id)
	if err != nil {
		return nil, err
	}
	after := *before
	decode := func(key string, dst any) error {
		if v, ok := raw[key]; ok {
			if err := json.Unmarshal(v, dst); err != nil {
				return invalidAttr(key, "could not be decoded")
			}
		}
		return nil
	}
	for _, f := range []func() error{
		func() error { return decode("name", &after.Name) },
		func() error { return decode("slug", &after.Slug) },
		func() error { return decode("description", &after.Description) },
		func() error { return decode("display_order", &after.DisplayOrder) },
		func() error { return decode("is_active", &after.IsActive) },
		func() error { return decode("is_featured", &after.IsFeatured) },
		func() error { return decode("is_listable", &after.IsListable) },
		func() error { return decode("parent_id", &after.ParentID) },
	} {
		if err := f(); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(after.Name) == "" {
		return nil, invalidAttr("name", "is required")
	}
	if after.ParentID != nil {
		if *after.ParentID == id {
			return nil, ErrCategoryCycle
		}
		// The new parent must not already sit beneath this node, or the walk
		// the form endpoint does becomes a loop capped at 32 levels rather
		// than an error anyone sees.
		below, err := s.store.CategoryHasAncestor(ctx, *after.ParentID, id)
		if err != nil {
			return nil, err
		}
		if below {
			return nil, ErrCategoryCycle
		}
	}
	if err := s.store.UpdateCategory(ctx, &after); err != nil {
		return nil, err
	}
	return s.store.GetCategoryNode(ctx, id)
}

// ─── Admin: impact and publish ──────────────────────────────────────────

// ImpactOf answers "what would tightening this field cost", without changing
// anything. See Store.AttributeImpact.
func (s *Service) ImpactOf(ctx context.Context, definitionID uuid.UUID) (*postgres.AttributeImpact, error) {
	return s.store.AttributeImpact(ctx, definitionID)
}

// PublishAttributeSchema makes the draft live and bumps the version every
// client caches against.
func (s *Service) PublishAttributeSchema(ctx context.Context) (*postgres.AttributeSchemaState, error) {
	return s.store.PublishAttributeSchema(ctx)
}

// AttributeSchemaState is the current publish state, for the console's
// "unpublished changes" banner.
func (s *Service) AttributeSchemaState(ctx context.Context) (*postgres.AttributeSchemaState, error) {
	return s.store.GetAttributeSchemaState(ctx)
}
