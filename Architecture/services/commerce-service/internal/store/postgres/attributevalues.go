package postgres

// The VALUE side of category attributes: a product's typed answers.
//
// 025 stored what a category may ask. This stores what one product answered,
// in the column its data type calls for, joined back to the definition that
// gives it a label, a group and an order.
//
// ─── WHY THE DOC IS REBUILT INSIDE THE WRITE ────────────────────────────
//
// `products.attributes_doc` is a denormalised code→value projection of these
// rows, for search and filtering. Every projection can drift, and the only
// question is whether it drifts silently. Rebuilding it in a SEPARATE call
// after the write — the obvious shape — drifts whenever the second call is
// forgotten, retried, or interrupted between the two, and the symptom is a
// product that is invisible to a facet it should match. Nobody reports that;
// it just quietly stops being found.
//
// So there is no exported "rebuild the doc" that a caller could omit.
// PutProductAttributeValues rebuilds it from the rows it just wrote, in the
// same transaction, and either both land or neither does. A doc that can
// disagree with its rows is worse than no doc, because the search index
// believes it.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrAttributeDefinitionNotFound — declared in attributes.go — is what a write
// naming an unknown code gets back. Reused rather than given a value-side twin
// so a caller's errors.Is stays true whichever half raised it.

// ─── Models ─────────────────────────────────────────────────────────────

// ProductAttributeValue is one typed answer.
//
// Exactly one of the five Value* fields is set, which the
// `product_attributes_one_typed_value` CHECK also enforces. UnitCode is not
// one of the five: it qualifies ValueNum and is legal only alongside it.
type ProductAttributeValue struct {
	ID           uuid.UUID  `json:"id"`
	ProductID    uuid.UUID  `json:"product_id"`
	DefinitionID uuid.UUID  `json:"definition_id"`
	OfferID      *uuid.UUID `json:"offer_id,omitempty"`

	// Position is the ordinal within one (product, definition): 0 for a
	// single-valued field, 0..n-1 for the options of a multi_enum.
	Position int `json:"position"`

	// ValueText carries text, long_text, gtin, and the OPTION CODE of an enum
	// or multi_enum — never the option's label, which is presentation.
	ValueText    *string    `json:"value_text,omitempty"`
	ValueNum     *float64   `json:"value_num,omitempty"`
	ValueBool    *bool      `json:"value_bool,omitempty"`
	ValueDate    *time.Time `json:"value_date,omitempty"`
	ValueMediaID *uuid.UUID `json:"value_media_id,omitempty"`
	UnitCode     *string    `json:"unit_code,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ProductAttributeValueRow is a value with the definition that explains it,
// plus the sort order the product's own category binds it at.
//
// The join is done in SQL rather than by the caller because the alternative is
// one definition lookup per value — the N+1 the batch readers in this package
// exist to remove.
type ProductAttributeValueRow struct {
	Value      ProductAttributeValue
	Definition AttributeDefinition

	// DisplayGroup is the EFFECTIVE group: the category binding's override if
	// it has one, otherwise the definition's own. A category may draw a field
	// in a different fieldset without every other category having to agree.
	DisplayGroup string

	// SortOrder is the position within the group, from the nearest ancestor
	// binding in the product's category chain. A product whose category binds
	// nothing (or has no category at all) gets 0 for every field, and the
	// tiebreak on code keeps the order stable rather than arbitrary.
	SortOrder int
}

// attributeValueColumns keeps the SELECT list and the scanner in one place, so
// a column added to the table cannot reach one query and miss another.
const attributeValueColumns = `
	pa.id, pa.product_id, pa.definition_id, pa.offer_id, pa.position,
	pa.value_text, pa.value_num::double precision, pa.value_bool, pa.value_date,
	pa.value_media_id, pa.unit_code, pa.created_at, pa.updated_at`

func scanAttributeValue(row rowScanner, v *ProductAttributeValue) error {
	return row.Scan(
		&v.ID, &v.ProductID, &v.DefinitionID, &v.OfferID, &v.Position,
		&v.ValueText, &v.ValueNum, &v.ValueBool, &v.ValueDate,
		&v.ValueMediaID, &v.UnitCode, &v.CreatedAt, &v.UpdatedAt,
	)
}

// ─── Read ───────────────────────────────────────────────────────────────

// ProductAttributeValues returns a product's typed values joined to their
// definitions, ordered for display.
//
// The ordering is (effective group, binding sort order, code, position). The
// GROUP's own order is not applied here: it lives in the service layer's
// `displayGroupOrder`, which is the one place the form's fieldset order is
// written down, and duplicating it into an ORDER BY would let the two
// disagree. This returns rows grouped-adjacent and correctly ordered WITHIN
// each group; the service arranges the groups.
//
// The recursive CTE is the same nearest-ancestor walk
// EffectiveCategoryAttributes uses: a field bound on "Books" applies to a
// product filed under "Books › Textbooks", and the nearest binding wins. It is
// a LEFT JOIN because a value must still be readable when its definition has
// since been unbound from the category — the product answered the question,
// and the answer does not evaporate because the form stopped asking.
func (s *Store) ProductAttributeValues(ctx context.Context, productID uuid.UUID) ([]*ProductAttributeValueRow, error) {
	rows, err := s.db.Query(ctx, `
		WITH RECURSIVE chain AS (
		    SELECT c.id, c.parent_id, 0 AS depth
		      FROM product_categories c
		      JOIN products p ON p.category_id = c.id
		     WHERE p.id = $1
		    UNION ALL
		    SELECT pc.id, pc.parent_id, ch.depth + 1
		      FROM product_categories pc
		      JOIN chain ch ON pc.id = ch.parent_id
		     WHERE ch.depth < 32
		),
		nearest AS (
		    SELECT DISTINCT ON (ca.definition_id)
		           ca.definition_id, ca.display_group, ca.sort_order
		      FROM category_attributes ca
		      JOIN chain ch ON ch.id = ca.category_id
		     WHERE NOT ca.is_excluded
		     ORDER BY ca.definition_id, ch.depth
		)
		SELECT `+attributeValueColumns+`, `+attributeDefinitionColumns+`,
		       COALESCE(n.display_group, d.display_group) AS eff_display_group,
		       COALESCE(n.sort_order, 0)                  AS eff_sort_order
		  FROM product_attributes pa
		  JOIN attribute_definitions d ON d.id = pa.definition_id
		  LEFT JOIN nearest n ON n.definition_id = pa.definition_id
		 WHERE pa.product_id = $1
		   AND pa.definition_id IS NOT NULL
		 ORDER BY eff_display_group, eff_sort_order, d.code, pa.position`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*ProductAttributeValueRow{}
	for rows.Next() {
		r := &ProductAttributeValueRow{}
		v, d := &r.Value, &r.Definition
		if err := rows.Scan(
			&v.ID, &v.ProductID, &v.DefinitionID, &v.OfferID, &v.Position,
			&v.ValueText, &v.ValueNum, &v.ValueBool, &v.ValueDate,
			&v.ValueMediaID, &v.UnitCode, &v.CreatedAt, &v.UpdatedAt,

			&d.ID, &d.Code, &d.Label, &d.HelpText, &d.Placeholder, &d.DataType,
			&d.UnitFamily, &d.DefaultUnit,
			&d.MinNum, &d.MaxNum,
			&d.MinLen, &d.MaxLen, &d.Regex, &d.MaxValues,
			&d.DisplayGroup, &d.AppliesTo,
			&d.IsVariantAxis, &d.IsFilterable, &d.IsSearchable, &d.IsActive,
			&d.Version, &d.CreatedBy, &d.CreatedAt, &d.UpdatedAt,

			&r.DisplayGroup, &r.SortOrder,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ProductAttributesDoc reads back the denormalised projection. Present for
// tests and for the search indexer; no buyer-facing surface reads it.
func (s *Store) ProductAttributesDoc(ctx context.Context, productID uuid.UUID) ([]byte, error) {
	var doc []byte
	err := s.db.QueryRow(ctx,
		`SELECT attributes_doc FROM products WHERE id=$1`, productID).Scan(&doc)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrProductNotFound
	}
	return doc, err
}

// ─── Write ──────────────────────────────────────────────────────────────

// AttributeValueSet is every value for ONE definition on one product — one
// element for a single-valued field, n for a multi_enum.
//
// The unit of replacement is the DEFINITION, not the row and not the product.
// Per-row would make "the seller removed a language" impossible to express;
// per-product would make a partial update silently delete every field the
// caller did not happen to mention.
type AttributeValueSet struct {
	DefinitionID uuid.UUID
	Values       []ProductAttributeValue
}

// PutProductAttributeValues replaces the stored values for each named
// definition and rebuilds the product's attributes_doc, in one transaction.
//
// REPLACE, not merge: the caller sends the complete set for a definition and
// what is not in it is gone. A multi_enum where the seller unticked an option
// has no way to say so otherwise.
//
// Definitions the caller does not name are untouched — that is what makes this
// usable for a partial edit — and so are the legacy `definition_id IS NULL`
// rows, which this method never reads or writes.
//
// PATCH /v1/commerce/products/:productId is the route that calls it, via
// Service.SetProductAttributeValues; POST /products calls putAttributeValuesTx
// directly, inside the transaction that creates the product, so a listing
// cannot exist with only some of its answers.
func (s *Store) PutProductAttributeValues(ctx context.Context, productID uuid.UUID, sets []AttributeValueSet) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := putAttributeValuesTx(ctx, tx, productID, sets); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// putAttributeValuesTx is the body of the write, inside a transaction the
// caller owns.
//
// It exists so the product-create path can write the values in the SAME
// transaction as the product and its variants. Calling the exported method
// there would have opened a second transaction, and a create whose attribute
// write failed would have committed a product with no answers on it — the
// exact class of half-built listing the atomic create exists to remove.
func putAttributeValuesTx(ctx context.Context, tx pgx.Tx, productID uuid.UUID, sets []AttributeValueSet) error {
	for _, set := range sets {
		// Delete-then-insert WITHIN one definition and one transaction. The
		// unique index makes the concurrent case a serialisation failure
		// rather than a duplicate row, which is the whole reason it is
		// partial-unique on (product, definition, position).
		if _, err := tx.Exec(ctx,
			`DELETE FROM product_attributes
			  WHERE product_id=$1 AND definition_id=$2`,
			productID, set.DefinitionID); err != nil {
			return err
		}
		for i, v := range set.Values {
			// Position is assigned here rather than trusted from the caller:
			// it is an implementation detail of how a multi-valued answer is
			// stored, and a caller that sent two values at position 0 would
			// get a unique violation reported as a 500.
			if _, err := tx.Exec(ctx, `
				INSERT INTO product_attributes
				    (product_id, definition_id, offer_id, position,
				     name, value,
				     value_text, value_num, value_bool, value_date, value_media_id, unit_code,
				     sort_order, created_at, updated_at)
				SELECT $1, $2, $3, $4,
				       d.code,
				       -- name/value are still NOT NULL, and a pre-026 pod
				       -- reads them. Mirroring the typed row into them costs
				       -- nothing and keeps a typed value legible to the old
				       -- free-text spec block instead of blank.
				       COALESCE($5::text,
				                $6::numeric::text,
				                $7::boolean::text,
				                $8::date::text,
				                $9::uuid::text, ''),
				       $5, $6, $7, $8, $9, $10,
				       $4, NOW(), NOW()
				  FROM attribute_definitions d
				 WHERE d.id = $2`,
				productID, set.DefinitionID, v.OfferID, i,
				v.ValueText, v.ValueNum, v.ValueBool, v.ValueDate, v.ValueMediaID, v.UnitCode,
			); err != nil {
				return err
			}
		}
	}

	return rebuildAttributesDocTx(ctx, tx, productID)
}

// rebuildAttributesDocTx recomputes products.attributes_doc from the typed
// rows, inside the caller's transaction.
//
// Unexported on purpose. See the file header: an exported rebuild is an
// invitation to write the rows in one call and the doc in another, and the
// window between them is where the search index and the catalogue disagree.
//
// Shape of the doc, per definition code:
//
//	measure          {"value": 250, "unit": "g"}   — a number with no unit is
//	                                                 not filterable, and the
//	                                                 unit cannot live in a
//	                                                 sibling key without every
//	                                                 reader knowing to look
//	multi_enum       ["en", "hi"]                  — always an array, even at
//	                                                 length one, so a consumer
//	                                                 never has to type-switch
//	everything else  328 / "R. K. Narayan" / true
func rebuildAttributesDocTx(ctx context.Context, tx pgx.Tx, productID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		WITH vals AS (
		    SELECT d.code, d.data_type, pa.position,
		           -- trim_scale, because the column is NUMERIC(24,6) and
		           -- to_jsonb keeps the declared scale: a page count comes out
		           -- as 264.000000. It parses as the same number, but it is
		           -- what a human reads when debugging a facet, and a
		           -- consumer comparing the JSON TEXT of two docs would see
		           -- 264 and 264.000000 as different.
		           CASE
		             WHEN pa.value_num IS NOT NULL AND pa.unit_code IS NOT NULL
		                  THEN jsonb_build_object('value', trim_scale(pa.value_num), 'unit', pa.unit_code)
		             WHEN pa.value_num      IS NOT NULL THEN to_jsonb(trim_scale(pa.value_num))
		             WHEN pa.value_bool     IS NOT NULL THEN to_jsonb(pa.value_bool)
		             WHEN pa.value_date     IS NOT NULL THEN to_jsonb(pa.value_date)
		             WHEN pa.value_media_id IS NOT NULL THEN to_jsonb(pa.value_media_id)
		             ELSE to_jsonb(pa.value_text)
		           END AS v
		      FROM product_attributes pa
		      JOIN attribute_definitions d ON d.id = pa.definition_id
		     WHERE pa.product_id = $1
		       AND pa.definition_id IS NOT NULL
		),
		agg AS (
		    SELECT code,
		           CASE WHEN data_type = 'multi_enum'
		                THEN jsonb_agg(v ORDER BY position)
		                ELSE (array_agg(v ORDER BY position))[1]
		           END AS v
		      FROM vals
		     GROUP BY code, data_type
		)
		UPDATE products
		   SET attributes_doc = COALESCE((SELECT jsonb_object_agg(code, v) FROM agg), '{}'::jsonb),
		       updated_at     = NOW()
		 WHERE id = $1`, productID)
	return err
}

// ─── Definition lookup for the write path ───────────────────────────────

// AttributeDefinitionsByCodes resolves the codes a write names, in one query.
//
// Codes, not ids, are what a client sends: the code is the stable contract and
// the id is an implementation detail nothing outside this service keeps.
// Inactive definitions are included — a product that already answered a
// retired field must still be editable, and refusing the write would strand
// the listing.
func (s *Store) AttributeDefinitionsByCodes(ctx context.Context, codes []string) (map[string]*AttributeDefinition, error) {
	out := map[string]*AttributeDefinition{}
	if len(codes) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx,
		`SELECT `+attributeDefinitionColumns+`
		   FROM attribute_definitions d WHERE d.code = ANY($1)`, codes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		d := &AttributeDefinition{}
		if err := scanAttributeDefinition(rows, d); err != nil {
			return nil, err
		}
		out[d.Code] = d
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, c := range codes {
		if _, ok := out[c]; !ok {
			return nil, fmt.Errorf("%w: %s", ErrAttributeDefinitionNotFound, c)
		}
	}
	return out, nil
}

// EnumCodeSetsFor returns the ACTIVE option codes of each definition, keyed by
// definition id, for membership checks on the write path.
//
// Active only, and that asymmetry with AttributeDefinitionsByCodes is
// deliberate: a retired DEFINITION must stay editable so an existing listing
// is not stranded, but a deactivated OPTION is one an operator withdrew, and
// letting a new write select it would keep growing the set of products that
// have to be migrated off it.
func (s *Store) EnumCodeSetsFor(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]map[string]bool, error) {
	out := map[uuid.UUID]map[string]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx,
		`SELECT definition_id, code FROM attribute_enum_values
		  WHERE definition_id = ANY($1) AND is_active`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var code string
		if err := rows.Scan(&id, &code); err != nil {
			return nil, err
		}
		if out[id] == nil {
			out[id] = map[string]bool{}
		}
		out[id][code] = true
	}
	return out, rows.Err()
}
