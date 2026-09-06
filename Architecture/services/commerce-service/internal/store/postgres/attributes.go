package postgres

// The definition side of product attributes: what a category is allowed to
// ask for, and what a client must render to ask it.
//
// ─── WHY A CATEGORY DOES NOT OWN ITS FIELDS ─────────────────────────────
//
// `category_attributes` binds a definition to a category, and a category
// INHERITS every binding on its ancestors. "Books" is given `author` once and
// Textbooks, Fiction and Reference all ask for it. Without inheritance the
// taxonomy's depth would be paid for in duplicated bindings — twelve leaves
// under Fashion each carrying the same eight rows, and eight edits to make
// one of them required.
//
// The rule the CTE below implements is NEAREST ANCESTOR WINS, per definition.
// Not "deepest binding for the category", not "union of everything": for each
// definition, exactly one binding is in force — the one on the closest node
// walking up from the category to the root. That is what makes an override
// mean something. Shoes › Running can bind `size` again with
// `is_variant_axis = TRUE` and its parent's non-axis binding is not merged
// with it, it is replaced. And a binding with `is_excluded` set is still the
// row that wins; winning is what lets it drop the field.
//
// The same rule is why exclusion is a column rather than a DELETE. Deleting
// the inherited row is not possible — it belongs to the ancestor, and removing
// it there removes the field from every sibling too.
//
// ─── WHY THE ORDER OF THE WALK IS BOUNDED ───────────────────────────────
//
// `product_categories.parent_id` is a self-reference with no constraint
// preventing a cycle: an operator who sets A's parent to B and B's parent to A
// has built one, and a recursive CTE over it does not terminate. The walk is
// capped at 32 levels. A taxonomy deeper than that is a data defect, and
// answering with the first 32 is better than a query that never returns.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrCategoryNotFound is returned when a category id names no row.
//
// Today's code checks category existence nowhere: `POST /products` takes an
// optional `category_id` and lets the foreign key decide, and every browse
// filter treats an unknown id as "no matches". A form endpoint cannot do that
// — an empty form and a wrong id look identical to the client, and the client
// renders an empty create screen instead of saying which field is wrong.
var ErrCategoryNotFound = errors.New("commerce: category not found")

// ErrAttributeDefinitionNotFound is returned when a definition id or code
// names no row.
var ErrAttributeDefinitionNotFound = errors.New("commerce: attribute definition not found")

// ErrAttributeEnumValueNotFound is returned when an enum value id names no row
// on the definition it was asked for.
var ErrAttributeEnumValueNotFound = errors.New("commerce: attribute enum value not found")

// ─── Models ─────────────────────────────────────────────────────────────

// AttributeDefinition is one field a category may ask for.
//
// The numeric bounds are float64 rather than the NUMERIC(20,6) the column
// holds. They are read to render a form control's min/max and to check a typed
// value; neither needs more precision than a float carries, and every client
// that consumes them decodes JSON numbers anyway. Money is the case where that
// reasoning fails, and money attributes carry `money_minor` as their data type
// precisely so the VALUE stays an integer count of paise.
type AttributeDefinition struct {
	ID            uuid.UUID  `json:"id"`
	Code          string     `json:"code"`
	Label         string     `json:"label"`
	HelpText      *string    `json:"help_text,omitempty"`
	Placeholder   *string    `json:"placeholder,omitempty"`
	DataType      string     `json:"data_type"`
	UnitFamily    *string    `json:"unit_family,omitempty"`
	DefaultUnit   *string    `json:"default_unit,omitempty"`
	MinNum        *float64   `json:"min_num,omitempty"`
	MaxNum        *float64   `json:"max_num,omitempty"`
	MinLen        *int       `json:"min_len,omitempty"`
	MaxLen        *int       `json:"max_len,omitempty"`
	Regex         *string    `json:"regex,omitempty"`
	MaxValues     *int       `json:"max_values,omitempty"`
	DisplayGroup  string     `json:"display_group"`
	AppliesTo     string     `json:"applies_to"`
	IsVariantAxis bool       `json:"is_variant_axis"`
	IsFilterable  bool       `json:"is_filterable"`
	IsSearchable  bool       `json:"is_searchable"`
	IsActive      bool       `json:"is_active"`
	Version       int        `json:"version"`
	CreatedBy     *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// AttributeEnumValue is one option of an enum or multi_enum definition.
type AttributeEnumValue struct {
	ID            uuid.UUID  `json:"id"`
	DefinitionID  uuid.UUID  `json:"definition_id"`
	Code          string     `json:"code"`
	Label         string     `json:"label"`
	SwatchHex     *string    `json:"swatch_hex,omitempty"`
	SwatchMediaID *uuid.UUID `json:"swatch_media_id,omitempty"`
	SortOrder     int        `json:"sort_order"`
	IsActive      bool       `json:"is_active"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// AttributeUnit is one unit within a family, with the factor that normalises
// a value expressed in it to the family's base unit.
type AttributeUnit struct {
	Family       string  `json:"family"`
	Code         string  `json:"code"`
	Label        string  `json:"label"`
	FactorToBase float64 `json:"factor_to_base"`
	SortOrder    int     `json:"sort_order"`
}

// CategoryAttribute is one binding — a category's own row, before inheritance
// is resolved.
type CategoryAttribute struct {
	CategoryID    uuid.UUID `json:"category_id"`
	DefinitionID  uuid.UUID `json:"definition_id"`
	IsRequired    bool      `json:"is_required"`
	IsExcluded    bool      `json:"is_excluded"`
	IsVariantAxis *bool     `json:"is_variant_axis,omitempty"`
	DisplayGroup  *string   `json:"display_group,omitempty"`
	SortOrder     int       `json:"sort_order"`
	CreatedAt     time.Time `json:"created_at"`
}

// EffectiveAttribute is a definition as it applies to ONE category, after the
// nearest-ancestor walk has picked the binding and applied its overrides.
//
// `Depth` is kept because it is the only thing that explains the answer: a
// field the seller did not expect to be required was made required somewhere,
// and 0 means "here", 2 means "two levels up".
type EffectiveAttribute struct {
	Definition    AttributeDefinition
	BoundAt       uuid.UUID
	Depth         int
	IsRequired    bool
	IsVariantAxis bool
	DisplayGroup  string
	SortOrder     int
}

// AttributeSchemaState is the single row that decides what is live.
type AttributeSchemaState struct {
	PublishedVersion int        `json:"published_version"`
	DraftDirty       bool       `json:"draft_dirty"`
	PublishedAt      *time.Time `json:"published_at,omitempty"`
}

// AttributeImpact is what a narrowing edit would do to the catalogue as it
// stands. See Store.AttributeImpact.
type AttributeImpact struct {
	DefinitionID uuid.UUID `json:"definition_id"`
	LiveProducts int       `json:"live_products"`
	Missing      int       `json:"missing"`
	OutOfRange   int       `json:"out_of_range"`
	// Affected is the number a caller must echo back as `ack_impact`. It is
	// missing + out_of_range: the products a narrowing edit would put into
	// violation, which is the only figure the operator is being asked to
	// accept. LiveProducts is context, not consent.
	Affected int `json:"affected"`
}

// ─── Definitions ────────────────────────────────────────────────────────

// attributeDefinitionColumns is the one place the column list lives, so a
// column added to the table cannot reach one query and miss another. The
// numeric casts are deliberate: pgx has no unambiguous scan target for
// NUMERIC into *float64, and `::double precision` states the conversion here
// rather than leaving it to a codec.
const attributeDefinitionColumns = `
	d.id, d.code, d.label, d.help_text, d.placeholder, d.data_type,
	d.unit_family, d.default_unit,
	d.min_num::double precision, d.max_num::double precision,
	d.min_len, d.max_len, d.regex, d.max_values,
	d.display_group, d.applies_to,
	d.is_variant_axis, d.is_filterable, d.is_searchable, d.is_active,
	d.version, d.created_by, d.created_at, d.updated_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAttributeDefinition(row rowScanner, d *AttributeDefinition) error {
	return row.Scan(
		&d.ID, &d.Code, &d.Label, &d.HelpText, &d.Placeholder, &d.DataType,
		&d.UnitFamily, &d.DefaultUnit,
		&d.MinNum, &d.MaxNum,
		&d.MinLen, &d.MaxLen, &d.Regex, &d.MaxValues,
		&d.DisplayGroup, &d.AppliesTo,
		&d.IsVariantAxis, &d.IsFilterable, &d.IsSearchable, &d.IsActive,
		&d.Version, &d.CreatedBy, &d.CreatedAt, &d.UpdatedAt,
	)
}

// ListAttributeDefinitions returns the definition catalogue.
//
// includeInactive is the admin console's view. A deactivated definition is
// still readable — products carry values against it and the console has to
// show what it is they carry.
func (s *Store) ListAttributeDefinitions(ctx context.Context, includeInactive bool) ([]*AttributeDefinition, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+attributeDefinitionColumns+`
		  FROM attribute_definitions d
		 WHERE ($1 OR d.is_active)
		 ORDER BY d.display_group, d.code`, includeInactive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AttributeDefinition{}
	for rows.Next() {
		var d AttributeDefinition
		if err := scanAttributeDefinition(rows, &d); err != nil {
			return nil, err
		}
		out = append(out, &d)
	}
	return out, rows.Err()
}

// GetAttributeDefinition reads one definition by id.
func (s *Store) GetAttributeDefinition(ctx context.Context, id uuid.UUID) (*AttributeDefinition, error) {
	var d AttributeDefinition
	err := scanAttributeDefinition(s.db.QueryRow(ctx, `
		SELECT `+attributeDefinitionColumns+`
		  FROM attribute_definitions d WHERE d.id = $1`, id), &d)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAttributeDefinitionNotFound
	}
	return &d, err
}

// GetAttributeDefinitionByCode reads one definition by its code.
//
// Used to answer "is this code taken?" with the row rather than a boolean, so
// a create collision can name the definition already holding the code instead
// of leaving the operator to search for it.
func (s *Store) GetAttributeDefinitionByCode(ctx context.Context, code string) (*AttributeDefinition, error) {
	var d AttributeDefinition
	err := scanAttributeDefinition(s.db.QueryRow(ctx, `
		SELECT `+attributeDefinitionColumns+`
		  FROM attribute_definitions d WHERE d.code = $1`, code), &d)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAttributeDefinitionNotFound
	}
	return &d, err
}

// CreateAttributeDefinition inserts a definition and returns it as stored.
//
// `version` starts at 1 and `id` is generated here rather than by the column
// default, so the caller has the id to write the revision row against without
// a second round trip.
func (s *Store) CreateAttributeDefinition(ctx context.Context, d *AttributeDefinition) error {
	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	now := time.Now().UTC()
	d.CreatedAt, d.UpdatedAt, d.Version = now, now, 1
	_, err := s.db.Exec(ctx, `
		INSERT INTO attribute_definitions
		  (id, code, label, help_text, placeholder, data_type, unit_family, default_unit,
		   min_num, max_num, min_len, max_len, regex, max_values,
		   display_group, applies_to, is_variant_axis, is_filterable, is_searchable,
		   is_active, version, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24)`,
		d.ID, d.Code, d.Label, d.HelpText, d.Placeholder, d.DataType, d.UnitFamily, d.DefaultUnit,
		d.MinNum, d.MaxNum, d.MinLen, d.MaxLen, d.Regex, d.MaxValues,
		d.DisplayGroup, d.AppliesTo, d.IsVariantAxis, d.IsFilterable, d.IsSearchable,
		d.IsActive, d.Version, d.CreatedBy, d.CreatedAt, d.UpdatedAt)
	return err
}

// UpdateAttributeDefinition writes every mutable column and bumps `version`.
//
// `code` is NOT in the SET list. It is the join key to
// `product_attributes.name`; changing it orphans every value already stored
// against the old spelling, and the rename would look like a successful edit
// right up until the filter that used it returned nothing. The service refuses
// the change before it reaches here; this is the second lock on the same door.
func (s *Store) UpdateAttributeDefinition(ctx context.Context, d *AttributeDefinition) error {
	d.UpdatedAt = time.Now().UTC()
	tag, err := s.db.Exec(ctx, `
		UPDATE attribute_definitions SET
		    label = $2, help_text = $3, placeholder = $4, data_type = $5,
		    unit_family = $6, default_unit = $7,
		    min_num = $8, max_num = $9, min_len = $10, max_len = $11,
		    regex = $12, max_values = $13,
		    display_group = $14, applies_to = $15,
		    is_variant_axis = $16, is_filterable = $17, is_searchable = $18,
		    is_active = $19,
		    version = version + 1,
		    updated_at = $20
		 WHERE id = $1`,
		d.ID, d.Label, d.HelpText, d.Placeholder, d.DataType,
		d.UnitFamily, d.DefaultUnit,
		d.MinNum, d.MaxNum, d.MinLen, d.MaxLen,
		d.Regex, d.MaxValues,
		d.DisplayGroup, d.AppliesTo,
		d.IsVariantAxis, d.IsFilterable, d.IsSearchable,
		d.IsActive, d.UpdatedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrAttributeDefinitionNotFound
	}
	d.Version++
	return nil
}

// ─── Enum values ────────────────────────────────────────────────────────

const attributeEnumValueColumns = `
	e.id, e.definition_id, e.code, e.label, e.swatch_hex, e.swatch_media_id,
	e.sort_order, e.is_active, e.created_at, e.updated_at`

func scanEnumValue(row rowScanner, v *AttributeEnumValue) error {
	return row.Scan(&v.ID, &v.DefinitionID, &v.Code, &v.Label, &v.SwatchHex,
		&v.SwatchMediaID, &v.SortOrder, &v.IsActive, &v.CreatedAt, &v.UpdatedAt)
}

// ListEnumValues returns one definition's options.
func (s *Store) ListEnumValues(ctx context.Context, definitionID uuid.UUID, includeInactive bool) ([]*AttributeEnumValue, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+attributeEnumValueColumns+`
		  FROM attribute_enum_values e
		 WHERE e.definition_id = $1 AND ($2 OR e.is_active)
		 ORDER BY e.sort_order, e.code`, definitionID, includeInactive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AttributeEnumValue{}
	for rows.Next() {
		var v AttributeEnumValue
		if err := scanEnumValue(rows, &v); err != nil {
			return nil, err
		}
		out = append(out, &v)
	}
	return out, rows.Err()
}

// EnumValuesForDefinitions loads the ACTIVE options for many definitions in
// one round trip, keyed by definition id.
//
// One query rather than one per enum field: a category form with four enums
// and two multi_enums is six extra round trips on a request that renders a
// screen, and the schema endpoint is on the critical path of every create.
func (s *Store) EnumValuesForDefinitions(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]*AttributeEnumValue, error) {
	out := map[uuid.UUID][]*AttributeEnumValue{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT `+attributeEnumValueColumns+`
		  FROM attribute_enum_values e
		 WHERE e.definition_id = ANY($1) AND e.is_active
		 ORDER BY e.definition_id, e.sort_order, e.code`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v AttributeEnumValue
		if err := scanEnumValue(rows, &v); err != nil {
			return nil, err
		}
		out[v.DefinitionID] = append(out[v.DefinitionID], &v)
	}
	return out, rows.Err()
}

// CreateEnumValue adds one option.
func (s *Store) CreateEnumValue(ctx context.Context, v *AttributeEnumValue) error {
	if v.ID == uuid.Nil {
		v.ID = uuid.New()
	}
	now := time.Now().UTC()
	v.CreatedAt, v.UpdatedAt = now, now
	_, err := s.db.Exec(ctx, `
		INSERT INTO attribute_enum_values
		  (id, definition_id, code, label, swatch_hex, swatch_media_id, sort_order, is_active, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		v.ID, v.DefinitionID, v.Code, v.Label, v.SwatchHex, v.SwatchMediaID,
		v.SortOrder, v.IsActive, v.CreatedAt, v.UpdatedAt)
	return err
}

// GetEnumValue reads one option, scoped to its definition.
//
// Scoped deliberately: an id alone would let a caller patch an option
// belonging to a different definition through the wrong URL, and the response
// would look like it had worked.
func (s *Store) GetEnumValue(ctx context.Context, definitionID, valueID uuid.UUID) (*AttributeEnumValue, error) {
	var v AttributeEnumValue
	err := scanEnumValue(s.db.QueryRow(ctx, `
		SELECT `+attributeEnumValueColumns+`
		  FROM attribute_enum_values e
		 WHERE e.id = $1 AND e.definition_id = $2`, valueID, definitionID), &v)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAttributeEnumValueNotFound
	}
	return &v, err
}

// UpdateEnumValue writes label, swatch, ordering and active state.
//
// `code` is immutable for the same reason a definition's is: it is the value
// stored on every product that chose this option.
func (s *Store) UpdateEnumValue(ctx context.Context, v *AttributeEnumValue) error {
	v.UpdatedAt = time.Now().UTC()
	tag, err := s.db.Exec(ctx, `
		UPDATE attribute_enum_values SET
		    label = $3, swatch_hex = $4, swatch_media_id = $5,
		    sort_order = $6, is_active = $7, updated_at = $8
		 WHERE id = $1 AND definition_id = $2`,
		v.ID, v.DefinitionID, v.Label, v.SwatchHex, v.SwatchMediaID,
		v.SortOrder, v.IsActive, v.UpdatedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrAttributeEnumValueNotFound
	}
	return nil
}

// ReorderEnumValues applies a new ordering in one transaction.
//
// All or nothing: a half-applied reorder leaves two options sharing a
// sort_order and the list renders in an order that changes between requests.
func (s *Store) ReorderEnumValues(ctx context.Context, definitionID uuid.UUID, orderedIDs []uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for i, id := range orderedIDs {
		tag, err := tx.Exec(ctx, `
			UPDATE attribute_enum_values SET sort_order = $3, updated_at = NOW()
			 WHERE id = $1 AND definition_id = $2`, id, definitionID, (i+1)*10)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: %s", ErrAttributeEnumValueNotFound, id)
		}
	}
	return tx.Commit(ctx)
}

// ─── Units ──────────────────────────────────────────────────────────────

// UnitsForFamilies loads every unit of the named families, keyed by family.
func (s *Store) UnitsForFamilies(ctx context.Context, families []string) (map[string][]AttributeUnit, error) {
	out := map[string][]AttributeUnit{}
	if len(families) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT u.family, u.code, u.label, u.factor_to_base::double precision, u.sort_order
		  FROM attribute_units u
		 WHERE u.family = ANY($1)
		 ORDER BY u.family, u.sort_order, u.code`, families)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var u AttributeUnit
		if err := rows.Scan(&u.Family, &u.Code, &u.Label, &u.FactorToBase, &u.SortOrder); err != nil {
			return nil, err
		}
		out[u.Family] = append(out[u.Family], u)
	}
	return out, rows.Err()
}

// ─── Categories ─────────────────────────────────────────────────────────

// CategoryExists reports whether an id names a real category row.
//
// Checked ahead of every category-scoped read, because the alternative — an
// empty result — is indistinguishable from a real category nobody has bound
// anything to yet.
func (s *Store) CategoryExists(ctx context.Context, id uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM product_categories WHERE id = $1)`, id).Scan(&exists)
	return exists, err
}

// CategoryPath returns the names from the root down to the category.
//
// Root-first because that is how it is read aloud and rendered as a
// breadcrumb: "Books › Textbooks". The recursive walk produces it leaf-first,
// so the final SELECT reverses it by ordering on descending depth.
func (s *Store) CategoryPath(ctx context.Context, id uuid.UUID) ([]string, error) {
	rows, err := s.db.Query(ctx, `
		WITH RECURSIVE chain AS (
		    SELECT c.id, c.parent_id, c.name, 0 AS depth
		      FROM product_categories c WHERE c.id = $1
		    UNION ALL
		    SELECT p.id, p.parent_id, p.name, ch.depth + 1
		      FROM product_categories p
		      JOIN chain ch ON p.id = ch.parent_id
		     WHERE ch.depth < 32
		)
		SELECT name FROM chain ORDER BY depth DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	path := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		path = append(path, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(path) == 0 {
		return nil, ErrCategoryNotFound
	}
	return path, nil
}

// CategoryTreeNode is a category as the tree response renders it.
//
// It does NOT reuse CategoryCard. The flat `GET /categories` response is read
// by the storefront and by the phone, and adding `is_listable` or `children`
// to the struct behind it would change every one of those bytes for a
// behaviour neither client asked for. A second struct costs one query and
// leaves the flat contract untouched.
type CategoryTreeNode struct {
	ID           uuid.UUID           `json:"id"`
	ParentID     *uuid.UUID          `json:"parent_id,omitempty"`
	Name         string              `json:"name"`
	Slug         string              `json:"slug"`
	Description  *string             `json:"description,omitempty"`
	DisplayOrder int                 `json:"display_order"`
	IsActive     bool                `json:"is_active"`
	IsFeatured   bool                `json:"is_featured"`
	IsListable   bool                `json:"is_listable"`
	ImageMediaID *uuid.UUID          `json:"image_media_id,omitempty"`
	ProductCount int                 `json:"product_count"`
	Depth        int                 `json:"depth"`
	Children     []*CategoryTreeNode `json:"children"`
}

// CategoryTree returns the taxonomy nested, roots first.
//
// The nesting is done in Go rather than in SQL. The whole taxonomy is a few
// hundred rows at most, a recursive CTE would still have to be re-assembled
// into a tree on this side, and doing it here means the depth limit and the
// orphan rule are written where they can be read.
//
// An ORPHAN — a row whose parent_id points at a category that is inactive, or
// at nothing at all — is promoted to a root rather than dropped. Dropping it
// is how a category disappears from the picker without anybody editing it.
func (s *Store) CategoryTree(ctx context.Context, includeInactive bool) ([]*CategoryTreeNode, error) {
	rows, err := s.db.Query(ctx, `
		SELECT c.id, c.parent_id, c.name, c.slug, c.description, c.display_order,
		       c.is_active, c.is_featured, c.is_listable,
		       COALESCE(c.icon_media_id, c.banner_media_id) AS image_media_id,
		       COALESCE(n.cnt, 0)                           AS product_count
		  FROM product_categories c
		  LEFT JOIN LATERAL (
		      SELECT COUNT(*)::int AS cnt FROM products p
		       WHERE p.category_id = c.id AND `+productSummaryLive+`
		  ) n ON TRUE
		 WHERE ($1 OR c.is_active)
		 ORDER BY c.display_order, c.name`, includeInactive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byID := map[uuid.UUID]*CategoryTreeNode{}
	order := []*CategoryTreeNode{}
	for rows.Next() {
		n := &CategoryTreeNode{Children: []*CategoryTreeNode{}}
		if err := rows.Scan(&n.ID, &n.ParentID, &n.Name, &n.Slug, &n.Description,
			&n.DisplayOrder, &n.IsActive, &n.IsFeatured, &n.IsListable,
			&n.ImageMediaID, &n.ProductCount); err != nil {
			return nil, err
		}
		byID[n.ID] = n
		order = append(order, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	roots := []*CategoryTreeNode{}
	for _, n := range order {
		if n.ParentID != nil {
			if parent, ok := byID[*n.ParentID]; ok && parent != n {
				parent.Children = append(parent.Children, n)
				continue
			}
		}
		roots = append(roots, n)
	}
	assignTreeDepth(roots, 0, 0)
	return roots, nil
}

// assignTreeDepth stamps each node's depth, capped at the same 32 levels the
// SQL walk uses. A cycle an operator built by pointing two categories at each
// other is unreachable from a root — nothing in `roots` leads into it — so it
// simply does not appear, which is the safe half of the failure.
func assignTreeDepth(nodes []*CategoryTreeNode, depth, guard int) {
	if guard > 32 {
		return
	}
	for _, n := range nodes {
		n.Depth = depth
		assignTreeDepth(n.Children, depth+1, guard+1)
	}
}

// PruneTreeDepth trims a tree to `maxDepth` levels. maxDepth <= 0 means no
// limit. A node at the cut line keeps an empty children array rather than a
// null one, so a client cannot mistake "trimmed" for "decode failure".
func PruneTreeDepth(nodes []*CategoryTreeNode, maxDepth int) []*CategoryTreeNode {
	if maxDepth <= 0 {
		return nodes
	}
	for _, n := range nodes {
		if n.Depth+1 >= maxDepth {
			n.Children = []*CategoryTreeNode{}
			continue
		}
		n.Children = PruneTreeDepth(n.Children, maxDepth)
	}
	return nodes
}

// CreateCategory inserts a category node.
func (s *Store) CreateCategory(ctx context.Context, c *CategoryTreeNode) error {
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO product_categories
		  (id, parent_id, name, slug, description, display_order, is_active, is_featured, is_listable)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		c.ID, c.ParentID, c.Name, c.Slug, c.Description, c.DisplayOrder,
		c.IsActive, c.IsFeatured, c.IsListable)
	return err
}

// UpdateCategory writes the mutable columns of one node.
//
// There is no DeleteCategory, and there will not be. Products point at
// categories, and a delete either cascades into the catalogue or is refused by
// the foreign key at a moment nobody expects. `is_active = FALSE` removes it
// from every surface and leaves the products readable.
func (s *Store) UpdateCategory(ctx context.Context, c *CategoryTreeNode) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE product_categories SET
		    parent_id = $2, name = $3, slug = $4, description = $5,
		    display_order = $6, is_active = $7, is_featured = $8, is_listable = $9,
		    updated_at = NOW()
		 WHERE id = $1`,
		c.ID, c.ParentID, c.Name, c.Slug, c.Description, c.DisplayOrder,
		c.IsActive, c.IsFeatured, c.IsListable)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrCategoryNotFound
	}
	return nil
}

// GetCategoryNode reads one category as a tree node (without children).
func (s *Store) GetCategoryNode(ctx context.Context, id uuid.UUID) (*CategoryTreeNode, error) {
	n := &CategoryTreeNode{Children: []*CategoryTreeNode{}}
	err := s.db.QueryRow(ctx, `
		SELECT c.id, c.parent_id, c.name, c.slug, c.description, c.display_order,
		       c.is_active, c.is_featured, c.is_listable,
		       COALESCE(c.icon_media_id, c.banner_media_id)
		  FROM product_categories c WHERE c.id = $1`, id).Scan(
		&n.ID, &n.ParentID, &n.Name, &n.Slug, &n.Description, &n.DisplayOrder,
		&n.IsActive, &n.IsFeatured, &n.IsListable, &n.ImageMediaID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCategoryNotFound
	}
	return n, err
}

// CategoryHasAncestor reports whether `ancestor` is on the path from
// `category` to the root — including when they are the same node.
//
// Asked before a re-parent. Making a category its own descendant's child
// builds the cycle the walk is capped against, and the cap turns a data defect
// into a silently truncated form rather than an error anybody sees.
func (s *Store) CategoryHasAncestor(ctx context.Context, category, ancestor uuid.UUID) (bool, error) {
	var found bool
	err := s.db.QueryRow(ctx, `
		WITH RECURSIVE chain AS (
		    SELECT c.id, c.parent_id, 0 AS depth
		      FROM product_categories c WHERE c.id = $1
		    UNION ALL
		    SELECT p.id, p.parent_id, ch.depth + 1
		      FROM product_categories p
		      JOIN chain ch ON p.id = ch.parent_id
		     WHERE ch.depth < 32
		)
		SELECT EXISTS (SELECT 1 FROM chain WHERE id = $2)`, category, ancestor).Scan(&found)
	return found, err
}

// ─── Bindings ───────────────────────────────────────────────────────────

// GetCategoryAttributes returns a category's OWN bindings — no inheritance.
// This is what the admin console edits.
func (s *Store) GetCategoryAttributes(ctx context.Context, categoryID uuid.UUID) ([]*CategoryAttribute, error) {
	rows, err := s.db.Query(ctx, `
		SELECT ca.category_id, ca.definition_id, ca.is_required, ca.is_excluded,
		       ca.is_variant_axis, ca.display_group, ca.sort_order, ca.created_at
		  FROM category_attributes ca
		 WHERE ca.category_id = $1
		 ORDER BY ca.sort_order, ca.definition_id`, categoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*CategoryAttribute{}
	for rows.Next() {
		var b CategoryAttribute
		if err := rows.Scan(&b.CategoryID, &b.DefinitionID, &b.IsRequired, &b.IsExcluded,
			&b.IsVariantAxis, &b.DisplayGroup, &b.SortOrder, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &b)
	}
	return out, rows.Err()
}

// PutCategoryAttributes replaces a category's own bindings in one transaction.
//
// A replace rather than a merge: the console sends the list it is showing, and
// a binding the operator removed from that list has to disappear. Doing it in
// one transaction is what stops a failed insert halfway through leaving the
// category with the deletions applied and none of the additions.
func (s *Store) PutCategoryAttributes(ctx context.Context, categoryID uuid.UUID, bindings []CategoryAttribute) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM category_attributes WHERE category_id = $1`, categoryID); err != nil {
		return err
	}
	for _, b := range bindings {
		if _, err := tx.Exec(ctx, `
			INSERT INTO category_attributes
			  (category_id, definition_id, is_required, is_excluded, is_variant_axis, display_group, sort_order)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			categoryID, b.DefinitionID, b.IsRequired, b.IsExcluded,
			b.IsVariantAxis, b.DisplayGroup, b.SortOrder); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// EffectiveCategoryAttributes resolves the form for one category.
//
// `chain` walks parent_id from the category to the root, recording how far up
// each ancestor is. `nearest` then picks, per definition_id, the binding on
// the SMALLEST depth — the closest ancestor that has an opinion. DISTINCT ON
// with that ORDER BY is what "nearest wins" is, in one pass.
//
// What is dropped, and why:
//
//	is_excluded    the nearest binding says this category does not ask. It
//	               still WON — that is what lets a leaf escape an inherited
//	               field without the parent dropping it for its siblings.
//	NOT is_active  a retired definition. Products keep their stored values
//	               and the admin console still lists it; a form must not go
//	               on asking for it.
//
// Ordering is group, then the binding's sort_order, then code. Code last so
// two fields sharing a sort_order — which is the default, 0 — still come back
// in the same order on every request instead of whatever the plan produced.
func (s *Store) EffectiveCategoryAttributes(ctx context.Context, categoryID uuid.UUID) ([]*EffectiveAttribute, error) {
	rows, err := s.db.Query(ctx, `
		WITH RECURSIVE chain AS (
		    SELECT c.id, c.parent_id, 0 AS depth
		      FROM product_categories c WHERE c.id = $1
		    UNION ALL
		    SELECT p.id, p.parent_id, ch.depth + 1
		      FROM product_categories p
		      JOIN chain ch ON p.id = ch.parent_id
		     WHERE ch.depth < 32
		),
		nearest AS (
		    SELECT DISTINCT ON (ca.definition_id)
		           ca.definition_id, ca.category_id, ca.is_required, ca.is_excluded,
		           ca.is_variant_axis, ca.display_group, ca.sort_order, ch.depth
		      FROM category_attributes ca
		      JOIN chain ch ON ch.id = ca.category_id
		     ORDER BY ca.definition_id, ch.depth
		)
		SELECT `+attributeDefinitionColumns+`,
		       n.category_id, n.depth, n.is_required,
		       COALESCE(n.is_variant_axis, d.is_variant_axis) AS eff_variant_axis,
		       COALESCE(n.display_group, d.display_group)     AS eff_display_group,
		       n.sort_order
		  FROM nearest n
		  JOIN attribute_definitions d ON d.id = n.definition_id
		 WHERE NOT n.is_excluded
		   AND d.is_active
		 ORDER BY eff_display_group, n.sort_order, d.code`, categoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*EffectiveAttribute{}
	for rows.Next() {
		ea := &EffectiveAttribute{}
		d := &ea.Definition
		if err := rows.Scan(
			&d.ID, &d.Code, &d.Label, &d.HelpText, &d.Placeholder, &d.DataType,
			&d.UnitFamily, &d.DefaultUnit,
			&d.MinNum, &d.MaxNum,
			&d.MinLen, &d.MaxLen, &d.Regex, &d.MaxValues,
			&d.DisplayGroup, &d.AppliesTo,
			&d.IsVariantAxis, &d.IsFilterable, &d.IsSearchable, &d.IsActive,
			&d.Version, &d.CreatedBy, &d.CreatedAt, &d.UpdatedAt,
			&ea.BoundAt, &ea.Depth, &ea.IsRequired,
			&ea.IsVariantAxis, &ea.DisplayGroup, &ea.SortOrder,
		); err != nil {
			return nil, err
		}
		out = append(out, ea)
	}
	return out, rows.Err()
}

// ─── Schema state ───────────────────────────────────────────────────────

// GetAttributeSchemaState reads the single publish row.
//
// A plain SELECT, and the missing-row case answers the migration's own
// defaults rather than writing anything. This runs on the PUBLIC schema
// endpoint, which is a GET a client may call on every form open; a read that
// takes a row lock to fix bookkeeping is a read that serialises the whole
// endpoint behind one row. The migration seeds it, an admin publish creates it
// if it is somehow absent, and until then version 1 / not-dirty is exactly
// what an unpublished schema means.
func (s *Store) GetAttributeSchemaState(ctx context.Context) (*AttributeSchemaState, error) {
	var st AttributeSchemaState
	err := s.db.QueryRow(ctx, `
		SELECT published_version, draft_dirty, published_at
		  FROM attribute_schema_state WHERE singleton`).Scan(
		&st.PublishedVersion, &st.DraftDirty, &st.PublishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return &AttributeSchemaState{PublishedVersion: 1}, nil
	}
	return &st, err
}

// MarkAttributeSchemaDirty records that the draft has diverged from what is
// published. Called on every definition, enum-value and binding write.
func (s *Store) MarkAttributeSchemaDirty(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO attribute_schema_state (singleton, draft_dirty) VALUES (TRUE, TRUE)
		ON CONFLICT (singleton) DO UPDATE SET draft_dirty = TRUE`)
	return err
}

// PublishAttributeSchema bumps the published version and clears the dirty
// flag, returning the new state.
//
// The bump happens whether or not anything is dirty. A publish is also how an
// operator forces every cached form to be refetched — the version is the ETag
// input — and refusing a no-op publish would take that away.
func (s *Store) PublishAttributeSchema(ctx context.Context) (*AttributeSchemaState, error) {
	var st AttributeSchemaState
	err := s.db.QueryRow(ctx, `
		INSERT INTO attribute_schema_state (singleton, published_version, draft_dirty, published_at)
		VALUES (TRUE, 2, FALSE, NOW())
		ON CONFLICT (singleton) DO UPDATE SET
		    published_version = attribute_schema_state.published_version + 1,
		    draft_dirty = FALSE,
		    published_at = NOW()
		RETURNING published_version, draft_dirty, published_at`).Scan(
		&st.PublishedVersion, &st.DraftDirty, &st.PublishedAt)
	return &st, err
}

// ─── Revisions ──────────────────────────────────────────────────────────

// RecordAttributeRevision appends one before/after row to the audit trail.
//
// `before` is nil on a create. Both sides are stored as JSONB of the whole
// definition rather than a field-level diff: the diff can be computed from the
// pair later, and a diff cannot be turned back into the row.
func (s *Store) RecordAttributeRevision(ctx context.Context, definitionID uuid.UUID, version int,
	before, after any, actor uuid.UUID) error {
	var beforeJSON, afterJSON []byte
	var err error
	if before != nil {
		if beforeJSON, err = json.Marshal(before); err != nil {
			return err
		}
	}
	if after != nil {
		if afterJSON, err = json.Marshal(after); err != nil {
			return err
		}
	}
	var actorArg any
	if actor != uuid.Nil {
		actorArg = actor
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO attribute_definition_revisions
		  (definition_id, version, before, after, actor_user_id)
		VALUES ($1,$2,$3,$4,$5)`,
		definitionID, version, beforeJSON, afterJSON, actorArg)
	return err
}

// ─── Impact ─────────────────────────────────────────────────────────────

// attributeViolationCTE is the ONE definition of "this live listing is in
// violation of this attribute definition".
//
// It is a fragment rather than a query because two callers need the same
// verdict and must not be allowed to disagree about it:
//
//	AttributeImpact         counts it, BEFORE an operator tightens a rule,
//	                        so the admin patch can refuse a narrowing edit
//	                        that has not quoted the damage back.
//	SweepAttributeViolations lists it, AFTER the rule was tightened, so the
//	                        listings it left behind get a per-product gap
//	                        row that a seller can act on.
//
// Written twice, those two drift, and the drift is invisible: the operator is
// warned about 412 listings and the sweeper flags 380 of them, or 470. The
// fragment ends with a `judged` CTE exposing `is_missing` and
// `is_out_of_range` as booleans, and each caller does nothing but aggregate
// or select from it.
//
// Placeholders, in both callers:
//
//	$1 definition id   $2 definition code
//	$3 min_num   $4 max_num   $5 min_len   $6 max_len   $7 regex
//	$8 whether the definition is an enum
//
// The category set is computed by propagating the binding DOWNWARD: each
// binding seeds the walk, and a child that has its own binding for the same
// definition stops the inheritance at that point — because its own row is a
// separate seed. That is the same nearest-ancestor-wins rule the form uses,
// read from the other end.
//
// `is_required` is carried through the walk alongside `is_excluded` because
// the sweeper needs it and the count does not: "how many listings would this
// break" is asked about a field that is NOT yet required, so filtering the
// count by required-ness would always answer zero.
//
// The value is read from `product_attributes.value` — the free-text mirror —
// rather than from the typed columns, and that is correct rather than lazy:
// putAttributeValuesTx writes the mirror for every typed row (see
// attributevalues.go), so it covers the typed estate AND the pre-026 rows
// that have no typed column filled in at all. A typed-only read would report
// every legacy listing as compliant because it could not see its values.
const attributeViolationCTE = `
	WITH RECURSIVE eff AS (
	    SELECT ca.category_id, ca.is_excluded, ca.is_required, 0 AS depth
	      FROM category_attributes ca
	     WHERE ca.definition_id = $1
	    UNION ALL
	    SELECT pc.id, e.is_excluded, e.is_required, e.depth + 1
	      FROM product_categories pc
	      JOIN eff e ON pc.parent_id = e.category_id
	     WHERE e.depth < 32
	       AND NOT EXISTS (
	           SELECT 1 FROM category_attributes ca2
	            WHERE ca2.category_id = pc.id AND ca2.definition_id = $1)
	),
	cats AS (
	    SELECT category_id, bool_or(is_required) AS is_required
	      FROM eff WHERE NOT is_excluded GROUP BY category_id
	),
	live AS (
	    SELECT p.id, p.seller_id, cats.is_required,
	           (SELECT pa.value FROM product_attributes pa
	             WHERE pa.product_id = p.id AND pa.name = $2
	             ORDER BY pa.sort_order LIMIT 1) AS val
	      FROM products p
	      JOIN cats ON cats.category_id = p.category_id
	     WHERE ` + productSummaryLive + `
	),
	judged AS (
	    SELECT id, seller_id, is_required, val,
	           (val IS NULL OR btrim(val) = '') AS is_missing,
	           (val IS NOT NULL AND btrim(val) <> '' AND (
	                ($3::numeric IS NOT NULL AND (num IS NULL OR num < $3::numeric))
	             OR ($4::numeric IS NOT NULL AND (num IS NULL OR num > $4::numeric))
	             OR ($5::int     IS NOT NULL AND length(val) < $5::int)
	             OR ($6::int     IS NOT NULL AND length(val) > $6::int)
	             OR ($7::text    IS NOT NULL AND val !~ $7::text)
	             OR ($8::bool AND NOT EXISTS (
	                    SELECT 1 FROM attribute_enum_values e
	                     WHERE e.definition_id = $1 AND e.is_active AND e.code = val))
	           )) AS is_out_of_range
	      FROM (
	          SELECT id, seller_id, is_required, val,
	                 CASE WHEN val ~ '^-?[0-9]+(\.[0-9]+)?$' THEN val::numeric END AS num
	            FROM live
	      ) t
	)`

// attributeViolationArgs is the argument list attributeViolationCTE expects,
// in order. Shared for the same reason the SQL is: two callers passing eight
// positional parameters in two places is one refactor away from passing
// min_len where max_len goes and reporting a different set of violations to
// the operator than to the seller.
func attributeViolationArgs(d *AttributeDefinition) []any {
	return []any{
		d.ID, d.Code,
		d.MinNum, d.MaxNum, d.MinLen, d.MaxLen, d.Regex,
		d.DataType == "enum",
	}
}

// AttributeImpact counts what a narrowing edit to this definition would do.
//
// ─── WHY THIS EXISTS ────────────────────────────────────────────────────
//
// "Make `pages` required" is one checkbox and it is not reversible in effect:
// every live listing without a page count is instantly non-compliant, and the
// operator who ticked it finds out from the sellers. The three numbers are the
// answer to "how many, and how badly" BEFORE the checkbox is ticked, which is
// why the admin patch refuses a narrowing edit that has not quoted them back.
//
//	live_products  listings in the categories this definition is effective
//	               for. The denominator.
//	missing        of those, the ones with no value stored for this code —
//	               what making it required would break.
//	out_of_range   of those, the ones whose stored value the CURRENT rules
//	               already reject: outside the numeric bounds, outside the
//	               length bounds, failing the regex, or naming an enum option
//	               that is no longer active. Non-zero here means the catalogue
//	               is already inconsistent with its own definition.
//
// The verdict itself is attributeViolationCTE, shared with the sweeper.
func (s *Store) AttributeImpact(ctx context.Context, definitionID uuid.UUID) (*AttributeImpact, error) {
	d, err := s.GetAttributeDefinition(ctx, definitionID)
	if err != nil {
		return nil, err
	}

	imp := &AttributeImpact{DefinitionID: definitionID}
	err = s.db.QueryRow(ctx, attributeViolationCTE+`
		SELECT COUNT(*)::int,
		       COUNT(*) FILTER (WHERE is_missing)::int,
		       COUNT(*) FILTER (WHERE is_out_of_range)::int
		  FROM judged`,
		attributeViolationArgs(d)...,
	).Scan(&imp.LiveProducts, &imp.Missing, &imp.OutOfRange)
	if err != nil {
		return nil, err
	}
	imp.Affected = imp.Missing + imp.OutOfRange
	return imp, nil
}
