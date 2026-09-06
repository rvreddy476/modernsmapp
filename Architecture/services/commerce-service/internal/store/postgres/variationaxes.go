package postgres

// A product's variation AXES, and a variant's options against them — write
// side, plus the diagnostics that read the residue.
//
// ─── WHAT MIGRATION 028 MOVED ───────────────────────────────────────────
//
// `product_variants` has three hard-coded option slots, six free-text
// columns, and nothing relating one variant's slots to another's. So one
// shirt could be keyed on "Size", "size" and "Colour" at once, in different
// slots, and afterwards nothing could answer either of the two questions a
// variant matrix exists to answer — what does this product vary on, and are
// these two variants the same combination.
//
// 028 gave both questions a home:
//
//	product_variation_axes    the product's axes, in order, capped at two
//	product_variant_options   one row per (variant, axis), holding a CODE,
//	                          with a COMPOSITE foreign key back to the axes
//	                          so a variant cannot carry an option on an axis
//	                          the product does not declare
//	variation_key             the canonical combination, UNIQUE per OFFER
//
// This file is the code half. What it deliberately does NOT do is read any
// of it into a response: every endpoint returns exactly what it returned
// before 028, and the legacy `option_N_*` columns remain what the phone, the
// importer and the analytics readers see. They are kept correct by the
// database trigger 028 installs, not by anything here — see below.
//
// ─── WHY THE LEGACY COLUMNS ARE NOT WRITTEN HERE ────────────────────────
//
// The obvious implementation is to compute `option_1_name`, `option_1_value`
// and the rest in Go at the point the options are written. That is the
// version that goes stale: the moment a second write path appears — the bulk
// importer, an operator's UPDATE, a repair script — the derivation has to be
// remembered again, and the copy that forgets is silently wrong.
//
// So the derivation lives in a trigger on `product_variant_options`. Any
// writer that touches the option rows gets the legacy columns and the
// variation key updated, whether or not it knows they exist. This file
// writes the option rows and reads the result back; it never composes a
// legacy value.

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrUndeclaredVariationAxis is the composite foreign key, named.
//
// It should be unreachable from the service, which checks the axis set
// before it writes anything. It is mapped anyway: the constraint is the
// authority and the validation is a courtesy, and an unmapped constraint
// violation reaches the seller as "one of the supplied values is not
// permitted", which names nothing.
var ErrUndeclaredVariationAxis = errors.New(
	"commerce: a variant carries an option on an attribute this product does not vary on")

// ErrDuplicateVariantCombination is UNIQUE(offer_id, variation_key).
//
// Per OFFER, which is the whole point: two shops listing the same shirt must
// both be able to offer "Blue / M", and neither may offer it twice.
var ErrDuplicateVariantCombination = errors.New(
	"commerce: this listing already has a variant for that combination of options")

// asVariationConflict names the two constraints this write path can trip.
//
// Matched on the constraint name, not on the SQLSTATE: several unique
// indexes and several foreign keys sit on the same transaction — the SKU,
// the offer, the product's own category key — and reporting any of them as a
// duplicate combination would send the seller to change the field that was
// fine. Same reasoning as asDuplicateSKU, and the two are used together.
func asVariationConflict(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.ConstraintName {
	case "product_variants_offer_variation_key":
		return ErrDuplicateVariantCombination
	case "product_variant_options_axis_fk":
		return ErrUndeclaredVariationAxis
	}
	return err
}

// ─── The write shapes ───────────────────────────────────────────────────

// VariationAxis is one attribute a product varies on, at one position.
//
// Position is 1 or 2 and the database says so. See migration 028 for why two
// and not three: the matrix is a cross product a seller prices by hand, and
// nobody prices sixty combinations — they price six and leave the rest at
// whatever the form defaulted to.
type VariationAxis struct {
	DefinitionID uuid.UUID `json:"definition_id"`
	Position     int       `json:"position"`
}

// VariantOption is one variant's value on one axis, as a CODE.
//
// A code, never a label and never what the seller typed. Free text is
// refused at the service layer for the reason 028's header gives: on a
// shared catalogue "Blue", "blue" and "Navy Blue" become three permanent
// colours that no filter will ever reunite.
type VariantOption struct {
	DefinitionID uuid.UUID `json:"definition_id"`
	ValueCode    string    `json:"value_code"`
}

// VariationUpdate is the COMPLETE variation picture for one product.
//
// Complete, not partial, and that is a deliberate narrowing. Axes are a fact
// about the product and options are a fact about each variant, so a partial
// update — "add an axis" — leaves every existing variant carrying no option
// on it, which is a product whose variants no longer describe distinct
// things. There is no honest way to fill that in on the seller's behalf: the
// service cannot know whether the shirts it already has are the blue ones or
// the red ones.
//
// So a caller that changes the axes sends every variant's options with them,
// and the store replaces the lot inside one transaction.
type VariationUpdate struct {
	Axes           []VariationAxis
	VariantOptions map[uuid.UUID][]VariantOption
}

// ─── The statements ─────────────────────────────────────────────────────

// replaceVariationTx rewrites a product's axes and its variants' options.
//
// DELETE-then-INSERT of the whole axis set rather than a diff. The composite
// foreign key cascades, so deleting an axis takes every option on it with it
// and the trigger clears the legacy columns of every variant that had one —
// which is exactly right, and is why the caller must supply the replacement
// options in the same call.
//
// A diff would be cheaper and would be wrong in one specific way: it would
// let a product's axes change while some variant's options were left over
// from the previous shape, and the composite key would happily accept them
// because the axis they name still exists. The state that produces is a
// matrix with a hole in it, which nothing downstream can render and nothing
// upstream can explain.
func replaceVariationTx(ctx context.Context, tx pgx.Tx, productID uuid.UUID, u VariationUpdate) error {
	if _, err := tx.Exec(ctx,
		`DELETE FROM product_variation_axes WHERE product_id = $1`, productID); err != nil {
		return err
	}
	for _, a := range u.Axes {
		if _, err := tx.Exec(ctx, `
			INSERT INTO product_variation_axes (product_id, definition_id, position)
			VALUES ($1, $2, $3)`, productID, a.DefinitionID, a.Position); err != nil {
			return asVariationConflict(err)
		}
	}
	for variantID, opts := range u.VariantOptions {
		if err := insertVariantOptionsTx(ctx, tx, variantID, productID, opts); err != nil {
			return err
		}
	}
	return nil
}

// insertVariantOptionsTx writes one variant's options.
//
// No DELETE first: this is called either on a freshly-inserted variant, which
// has none, or from replaceVariationTx after the axis DELETE has cascaded
// them away. A DELETE here would be dead code that looked load-bearing.
func insertVariantOptionsTx(ctx context.Context, tx pgx.Tx, variantID, productID uuid.UUID, opts []VariantOption) error {
	for _, o := range opts {
		if _, err := tx.Exec(ctx, `
			INSERT INTO product_variant_options (variant_id, product_id, definition_id, value_code)
			VALUES ($1, $2, $3, $4)`, variantID, productID, o.DefinitionID, o.ValueCode); err != nil {
			return asVariationConflict(err)
		}
	}
	return nil
}

// ─── Reads: diagnostic and test-facing ONLY ─────────────────────────────
//
// Nothing below is called by an endpoint, and nothing may be until the step
// that moves the readers over. Same posture as GetOfferForProduct: the shape
// exists so the write path's tests can assert on the rows they claim to have
// written without reaching past the store into raw SQL.

// ProductVariationAxisRow is an axis with the definition's code beside it,
// which is what makes a failure message readable.
type ProductVariationAxisRow struct {
	DefinitionID   uuid.UUID `json:"definition_id"`
	DefinitionCode string    `json:"definition_code"`
	Label          string    `json:"label"`
	Position       int       `json:"position"`
}

// ProductVariationAxes returns a product's axes in position order.
func (s *Store) ProductVariationAxes(ctx context.Context, productID uuid.UUID) ([]ProductVariationAxisRow, error) {
	rows, err := s.db.Query(ctx, `
		SELECT a.definition_id, d.code, d.label, a.position
		  FROM product_variation_axes a
		  JOIN attribute_definitions d ON d.id = a.definition_id
		 WHERE a.product_id = $1
		 ORDER BY a.position`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProductVariationAxisRow{}
	for rows.Next() {
		var r ProductVariationAxisRow
		if err := rows.Scan(&r.DefinitionID, &r.DefinitionCode, &r.Label, &r.Position); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// VariantOptionCodes returns one variant's options keyed by definition CODE.
//
// Keyed by code rather than by id because every caller of this is comparing
// against something a human wrote — a test's expectation, an operator's
// question — and a map of uuids to codes is unreadable in a failure message.
func (s *Store) VariantOptionCodes(ctx context.Context, variantID uuid.UUID) (map[string]string, error) {
	rows, err := s.db.Query(ctx, `
		SELECT d.code, o.value_code
		  FROM product_variant_options o
		  JOIN attribute_definitions d ON d.id = o.definition_id
		 WHERE o.variant_id = $1`, variantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var code, value string
		if err := rows.Scan(&code, &value); err != nil {
			return nil, err
		}
		out[code] = value
	}
	return out, rows.Err()
}

// VariantIdentity is the minimum needed to talk about a variant in a refusal
// and to key its options by.
type VariantIdentity struct {
	ID     uuid.UUID `json:"id"`
	SKU    string    `json:"sku"`
	Status string    `json:"status"`
}

// ProductVariantIdentities lists EVERY variant of a product, whatever its
// status.
//
// Every one, unlike GetVariantsByProduct, which returns only the active ones
// because it serves a buyer. The matrix is a fact about the product and the
// composite foreign key applies to every variant row there is, so a caller
// replacing the axes has to account for the archived ones too — otherwise
// dropping an axis silently cascades the options off a retired variant and
// the trigger blanks the legacy columns an order-history join still reads.
func (s *Store) ProductVariantIdentities(ctx context.Context, productID uuid.UUID) ([]VariantIdentity, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, sku, status FROM product_variants WHERE product_id = $1 ORDER BY created_at, id`,
		productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VariantIdentity{}
	for rows.Next() {
		var v VariantIdentity
		if err := rows.Scan(&v.ID, &v.SKU, &v.Status); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// VariantLegacyOptions is what the trigger left in the columns the phone,
// the importer and the analytics readers still read, plus the derived key.
//
// It exists so a test can assert that the derived half stayed in step with
// the authoritative half without writing the same six-column SELECT in four
// files.
type VariantLegacyOptions struct {
	Option1Name   *string
	Option1Value  *string
	Option2Name   *string
	Option2Value  *string
	Option3Name   *string
	Option3Value  *string
	VariationKey  *string
	OfferID       *uuid.UUID
	SKU           string
	VariantStatus string
}

// VariantLegacyView reads one variant's derived columns back.
func (s *Store) VariantLegacyView(ctx context.Context, variantID uuid.UUID) (*VariantLegacyOptions, error) {
	v := &VariantLegacyOptions{}
	err := s.db.QueryRow(ctx, `
		SELECT option_1_name, option_1_value, option_2_name, option_2_value,
		       option_3_name, option_3_value, variation_key, offer_id, sku, status
		  FROM product_variants WHERE id = $1`, variantID).Scan(
		&v.Option1Name, &v.Option1Value, &v.Option2Name, &v.Option2Value,
		&v.Option3Name, &v.Option3Value, &v.VariationKey, &v.OfferID, &v.SKU, &v.VariantStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return v, nil
}

// ─── The migration's residue ────────────────────────────────────────────

// VariationBackfillReport is what one run of the 028 backfill did.
//
// Three counts, and the third is the one that matters. A backfill that
// migrated everything is not evidence of a good migration — it is evidence
// that it guessed. See migration 028 on why a wrong axis is worse than an
// unmigrated one on a shared catalogue.
type VariationBackfillReport struct {
	ProductsMigrated   int `json:"products_migrated"`
	VariantsMigrated   int `json:"variants_migrated"`
	ExceptionsRecorded int `json:"exceptions_recorded"`
}

// BackfillVariationAxes re-runs migration 028's resolution pass.
//
// The pass is a SQL function rather than a straight-line block inside the
// migration precisely so it can be called again: an operator who resolves a
// batch of exceptions by creating the missing definition runs this and picks
// those products up, without a second migration file and without touching
// the products that already migrated. It skips any product that already has
// axes, so re-running it is safe and idempotent.
func (s *Store) BackfillVariationAxes(ctx context.Context) (*VariationBackfillReport, error) {
	r := &VariationBackfillReport{}
	if err := s.db.QueryRow(ctx,
		`SELECT products_migrated, variants_migrated, exceptions_recorded
		   FROM commerce_backfill_variation_axes()`,
	).Scan(&r.ProductsMigrated, &r.VariantsMigrated, &r.ExceptionsRecorded); err != nil {
		return nil, fmt.Errorf("backfill variation axes: %w", err)
	}
	return r, nil
}

// VariantMigrationException is one variant option 028 refused to guess at.
type VariantMigrationException struct {
	ProductID      uuid.UUID `json:"product_id"`
	VariantID      uuid.UUID `json:"variant_id"`
	OptionPosition int       `json:"option_position"`
	OptionName     *string   `json:"option_name,omitempty"`
	OptionValue    *string   `json:"option_value,omitempty"`
	Reason         string    `json:"reason"`
}

// ListVariantMigrationExceptions reads the worklist.
func (s *Store) ListVariantMigrationExceptions(ctx context.Context, limit int) ([]VariantMigrationException, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		SELECT product_id, variant_id, option_position, option_name, option_value, reason
		  FROM variant_migration_exceptions
		 ORDER BY created_at DESC, variant_id, option_position
		 LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VariantMigrationException{}
	for rows.Next() {
		var e VariantMigrationException
		if err := rows.Scan(&e.ProductID, &e.VariantID, &e.OptionPosition,
			&e.OptionName, &e.OptionValue, &e.Reason); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountVariantMigrationExceptions splits the worklist by reason.
//
// By reason rather than a bare total, because the two halves need different
// people: "no attribute definition matches …" is an operator creating a
// definition, and "variants disagree about which slot an axis occupies" is a
// seller's listing that has to be corrected by hand.
func (s *Store) CountVariantMigrationExceptions(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.Query(ctx,
		`SELECT reason, count(*) FROM variant_migration_exceptions GROUP BY reason ORDER BY count(*) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var reason string
		var n int
		if err := rows.Scan(&reason, &n); err != nil {
			return nil, err
		}
		out[reason] = n
	}
	return out, rows.Err()
}
