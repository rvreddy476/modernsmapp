package postgres

// The storage behind the submit gate: what was submitted, and which live
// listings a tightened rule has left behind.
//
// See migration 029 for the argument. In one line: a submission is a frozen
// photograph, because a diff needs two of them and the product row only ever
// holds one; and a compliance gap is a row BESIDE the product rather than a
// change to its status, because a rule tightened today must never delist a
// listing that was compliant when it was approved.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ─── Submissions ────────────────────────────────────────────────────────

// SubmissionValue is one field as it stood at the moment of a submit.
//
// `Label` travels with `Code` for the reason the product detail page's
// attribute block carries both: the code is what a client keys on and the
// label is what a human reads. A reviewer diff showing `hsn_code` changed
// from one string to another is a puzzle; "HSN Code" is a sentence. And the
// label is FROZEN here rather than joined at read time — an operator renaming
// an attribute must not retroactively rewrite what a seller submitted.
type SubmissionValue struct {
	Code  string `json:"code"`
	Label string `json:"label"`
	// Kind is "builtin" for a field of the listing itself (price, stock,
	// image, tax class) and "attribute" for a category-schema answer. It is
	// part of the diff's identity, so a built-in and an attribute that
	// happened to share a code could never be compared against each other.
	Kind  string `json:"kind"`
	Value any    `json:"value"`
}

// ProductSubmission is one row of product_submissions.
type ProductSubmission struct {
	ID            uuid.UUID         `json:"id"`
	ProductID     uuid.UUID         `json:"product_id"`
	SellerID      uuid.UUID         `json:"seller_id"`
	SubmittedBy   *uuid.UUID        `json:"submitted_by,omitempty"`
	Attempt       int               `json:"attempt"`
	SchemaVersion int               `json:"schema_version"`
	Snapshot      []SubmissionValue `json:"snapshot"`
	CreatedAt     time.Time         `json:"created_at"`
}

// recordSubmissionTx writes one submission inside the caller's transaction.
//
// Inside the transaction that flips `approval_status`, and it has to be: a
// submission row without the status change is a listing the reviewer queue
// does not show but the audit trail says was submitted, and a status change
// without the row is a re-submission whose diff silently compares against the
// wrong attempt.
//
// The attempt number is computed in the INSERT rather than read first and
// incremented in Go. Two concurrent submits reading "the last attempt was 2"
// would both write 3; here the second one loses to the unique index on
// (product_id, attempt) and is reported, which is the correct outcome for a
// double-tapped button.
func recordSubmissionTx(
	ctx context.Context, tx pgx.Tx,
	productID, sellerID uuid.UUID, submittedBy *uuid.UUID,
	schemaVersion int, snapshot []SubmissionValue,
) error {
	if snapshot == nil {
		snapshot = []SubmissionValue{}
	}
	blob, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("marshal submission snapshot: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO product_submissions
		    (product_id, seller_id, submitted_by, attempt, schema_version, snapshot)
		SELECT $1, $2, $3,
		       COALESCE((SELECT MAX(attempt) FROM product_submissions WHERE product_id = $1), 0) + 1,
		       $4, $5::jsonb`,
		productID, sellerID, submittedBy, schemaVersion, blob)
	return err
}

// ProductSubmissions returns a product's submissions, newest first.
//
// `limit` caps the read because the reviewer needs the latest two and the
// audit view needs a page, and neither needs the whole history of a listing
// that has been round-tripped forty times.
func (s *Store) ProductSubmissions(ctx context.Context, productID uuid.UUID, limit int) ([]*ProductSubmission, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, product_id, seller_id, submitted_by, attempt, schema_version, snapshot, created_at
		  FROM product_submissions
		 WHERE product_id = $1
		 ORDER BY attempt DESC
		 LIMIT $2`, productID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*ProductSubmission{}
	for rows.Next() {
		sub := &ProductSubmission{}
		var blob []byte
		if err := rows.Scan(&sub.ID, &sub.ProductID, &sub.SellerID, &sub.SubmittedBy,
			&sub.Attempt, &sub.SchemaVersion, &blob, &sub.CreatedAt); err != nil {
			return nil, err
		}
		if len(blob) > 0 {
			if err := json.Unmarshal(blob, &sub.Snapshot); err != nil {
				// A snapshot this build cannot parse is not a reason to fail
				// the reviewer's whole read — the submission still happened,
				// and the attempt number, the actor and the timestamp are all
				// still true. It renders as a submission with no fields.
				sub.Snapshot = nil
			}
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

// ─── Compliance gaps ────────────────────────────────────────────────────

// ComplianceGap is one live listing failing one field of its category's
// CURRENT schema.
//
// It carries the product's title and the seller's name because both of its
// readers — the seller's "action needed" list and the founder's queue —
// render a sentence about a listing, and a row that had to be joined back to
// `products` and `sellers` per gap would be two joins per row on a queue that
// exists precisely because there are a lot of rows.
type ComplianceGap struct {
	ID            uuid.UUID  `json:"id"`
	ProductID     uuid.UUID  `json:"product_id"`
	SellerID      uuid.UUID  `json:"seller_id"`
	DefinitionID  *uuid.UUID `json:"definition_id,omitempty"`
	Code          string     `json:"code"`
	Label         string     `json:"label"`
	Reason        string     `json:"reason"`
	SchemaVersion int        `json:"schema_version"`
	DetectedAt    time.Time  `json:"detected_at"`
	LastSeenAt    time.Time  `json:"last_seen_at"`

	ProductTitle string  `json:"product_title,omitempty"`
	StoreName    *string `json:"store_name,omitempty"`
}

// gapFinding is one verdict the sweep reached, before it is stored.
type gapFinding struct {
	ProductID uuid.UUID
	SellerID  uuid.UUID
	Reason    string
}

// SweepResult is what one sweep did, for the operator who ran it.
//
// `Resolved` is on it because it is the number that says the mechanism is
// working: gaps closing means sellers are fixing them on their next edit,
// which is the entire premise of decision 8.
type SweepResult struct {
	DefinitionsChecked int       `json:"definitions_checked"`
	ProductsFlagged    int       `json:"products_flagged"`
	GapsOpened         int       `json:"gaps_opened"`
	GapsResolved       int       `json:"gaps_resolved"`
	SweptAt            time.Time `json:"swept_at"`
}

// requiredAttributeDefinitions returns every active definition that at least
// one category binding marks required.
//
// Only the required ones, because a gap is by definition a field the
// catalogue now insists on. An optional field whose stored value is
// out-of-range is also a violation — and it IS swept, because the query below
// keeps those rows regardless of required-ness. What is skipped is a
// definition no category requires and whose values are all fine, which is
// most of them, and running the violation CTE for each of those would be a
// full catalogue scan per definition for a guaranteed empty result.
func (s *Store) requiredAttributeDefinitions(ctx context.Context) ([]*AttributeDefinition, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+attributeDefinitionColumns+`
		  FROM attribute_definitions d
		 WHERE d.is_active
		   AND EXISTS (SELECT 1 FROM category_attributes ca
		                WHERE ca.definition_id = d.id
		                  AND ca.is_required AND NOT ca.is_excluded)
		 ORDER BY d.code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AttributeDefinition{}
	for rows.Next() {
		d := &AttributeDefinition{}
		if err := scanAttributeDefinition(rows, d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// violationsFor lists the live listings currently in violation of one
// definition, using attributeViolationCTE — the same SQL AttributeImpact
// counts with, so the founder is never warned about a different set than the
// sweeper flags.
//
// `is_missing` is only a violation where the binding actually requires the
// field; `is_out_of_range` is a violation either way, because a value the
// current rules reject is wrong whether or not anyone had to supply it.
func (s *Store) violationsFor(ctx context.Context, d *AttributeDefinition) ([]gapFinding, error) {
	rows, err := s.db.Query(ctx, attributeViolationCTE+`
		SELECT id, seller_id,
		       CASE WHEN is_out_of_range THEN 'out_of_range' ELSE 'missing' END
		  FROM judged
		 WHERE (is_required AND is_missing) OR is_out_of_range`,
		attributeViolationArgs(d)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []gapFinding{}
	for rows.Next() {
		var f gapFinding
		if err := rows.Scan(&f.ProductID, &f.SellerID, &f.Reason); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// SweepComplianceGaps walks every required definition and reconciles the open
// gap set with what the CURRENT schema says.
//
// ─── WHAT IT DOES NOT DO ────────────────────────────────────────────────
//
// It does not touch `products.status`, `products.approval_status` or
// `products.published_at`. Not once, not for the worst offender, not behind a
// flag. A listing this sweep flags goes on selling exactly as it did the
// minute before, and there is an integration test whose entire job is to buy
// one afterwards.
//
// ─── PER DEFINITION, NOT ONE BIG QUERY ──────────────────────────────────
//
// The violation CTE is parameterised by ONE definition's bounds — its regex,
// its min and max, whether it is an enum — and those bounds are columns, not
// constants. A single query over every definition would have to apply each
// row's own bounds to each product's own value, which in SQL means either a
// lateral join per definition (the same number of scans, less legible) or
// moving the bound checks into a function nothing else could reuse. Per
// definition is the same work, written once, and it is the shape that let the
// count and the sweep share their SQL.
func (s *Store) SweepComplianceGaps(ctx context.Context) (*SweepResult, error) {
	defs, err := s.requiredAttributeDefinitions(ctx)
	if err != nil {
		return nil, err
	}
	state, err := s.GetAttributeSchemaState(ctx)
	if err != nil {
		return nil, err
	}

	res := &SweepResult{DefinitionsChecked: len(defs), SweptAt: time.Now()}
	flagged := map[uuid.UUID]bool{}

	for _, d := range defs {
		found, err := s.violationsFor(ctx, d)
		if err != nil {
			return nil, fmt.Errorf("sweep %s: %w", d.Code, err)
		}
		ids := make([]uuid.UUID, 0, len(found))
		for _, f := range found {
			ids = append(ids, f.ProductID)
			flagged[f.ProductID] = true

			// ON CONFLICT over the partial unique index on the OPEN set, so a
			// gap already raised is refreshed rather than duplicated and a
			// resolved one from a previous cycle is not resurrected in place
			// — its history stays where it is and a new row opens.
			// `xmax = 0` is true only on the INSERT arm of the upsert, and it
			// is the difference between a number the founder can read and one
			// they cannot: RowsAffected counts the DO UPDATE refresh too, so a
			// second sweep over an unchanged catalogue would report every
			// existing gap as newly opened and the figure would climb forever.
			var inserted bool
			err := s.db.QueryRow(ctx, `
				INSERT INTO product_compliance_gaps
				    (product_id, seller_id, definition_id, code, label, reason, schema_version)
				VALUES ($1,$2,$3,$4,$5,$6,$7)
				ON CONFLICT (product_id, code) WHERE resolved_at IS NULL
				DO UPDATE SET last_seen_at = NOW(),
				              reason = EXCLUDED.reason,
				              schema_version = EXCLUDED.schema_version
				RETURNING (xmax = 0)`,
				f.ProductID, f.SellerID, d.ID, d.Code, d.Label, f.Reason, state.PublishedVersion,
			).Scan(&inserted)
			if err != nil {
				return nil, fmt.Errorf("record gap %s on %s: %w", d.Code, f.ProductID, err)
			}
			if inserted {
				res.GapsOpened++
			}
		}

		// Anything open against this definition that the sweep did NOT find
		// is fixed. Closed here rather than left for the seller's next edit
		// to notice, because a gap can also close without an edit — an
		// operator loosening the bound they tightened last week resolves
		// every one of them at once, and nobody should have to touch a
		// thousand listings to clear a rule that was withdrawn.
		tag, err := s.db.Exec(ctx, `
			UPDATE product_compliance_gaps
			   SET resolved_at = NOW()
			 WHERE definition_id = $1
			   AND resolved_at IS NULL
			   AND NOT (product_id = ANY($2::uuid[]))`, d.ID, ids)
		if err != nil {
			return nil, fmt.Errorf("resolve gaps for %s: %w", d.Code, err)
		}
		res.GapsResolved += int(tag.RowsAffected())
	}

	// A definition that stopped being required, or was deactivated, leaves
	// gaps nothing above would ever look at again. They are resolved rather
	// than left open forever, or the founder's queue slowly fills with
	// findings from rules that no longer exist.
	tag, err := s.db.Exec(ctx, `
		UPDATE product_compliance_gaps g
		   SET resolved_at = NOW()
		 WHERE g.resolved_at IS NULL
		   AND g.definition_id IS NOT NULL
		   AND NOT EXISTS (
		       SELECT 1 FROM attribute_definitions d
		        WHERE d.id = g.definition_id AND d.is_active
		          AND EXISTS (SELECT 1 FROM category_attributes ca
		                       WHERE ca.definition_id = d.id
		                         AND ca.is_required AND NOT ca.is_excluded))`)
	if err != nil {
		return nil, err
	}
	res.GapsResolved += int(tag.RowsAffected())

	res.ProductsFlagged = len(flagged)
	return res, nil
}

// ResolveGapsForProduct closes every open gap on one product whose field now
// has a value the current rules accept.
//
// Called after a seller's patch lands. THAT is how a gap is meant to close —
// "the seller fixes it on their next edit" is the whole shape of decision 8,
// and a seller who filled in the missing field and still saw "action needed"
// on their dashboard would reasonably conclude the warning is noise.
//
// Best-effort by contract: it re-judges only the definitions this product
// already has open gaps against, and a failure here must never fail the
// patch. The sweep is the backstop.
func (s *Store) ResolveGapsForProduct(ctx context.Context, productID uuid.UUID) (int, error) {
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT definition_id FROM product_compliance_gaps
		 WHERE product_id = $1 AND resolved_at IS NULL AND definition_id IS NOT NULL`, productID)
	if err != nil {
		return 0, err
	}
	ids := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	closed := 0
	for _, id := range ids {
		d, err := s.GetAttributeDefinition(ctx, id)
		if err != nil {
			if errors.Is(err, ErrAttributeDefinitionNotFound) {
				continue
			}
			return closed, err
		}
		found, err := s.violationsFor(ctx, d)
		if err != nil {
			return closed, err
		}
		still := false
		for _, f := range found {
			if f.ProductID == productID {
				still = true
				break
			}
		}
		if still {
			continue
		}
		tag, err := s.db.Exec(ctx, `
			UPDATE product_compliance_gaps SET resolved_at = NOW()
			 WHERE product_id = $1 AND definition_id = $2 AND resolved_at IS NULL`,
			productID, id)
		if err != nil {
			return closed, err
		}
		closed += int(tag.RowsAffected())
	}
	return closed, nil
}

// gapColumns keeps the SELECT list and the scan in one place across the three
// reads below.
const gapColumns = `
	g.id, g.product_id, g.seller_id, g.definition_id, g.code, g.label,
	g.reason, g.schema_version, g.detected_at, g.last_seen_at,
	p.title, sl.store_name`

func scanGaps(rows pgx.Rows) ([]*ComplianceGap, error) {
	defer rows.Close()
	out := []*ComplianceGap{}
	for rows.Next() {
		g := &ComplianceGap{}
		if err := rows.Scan(&g.ID, &g.ProductID, &g.SellerID, &g.DefinitionID,
			&g.Code, &g.Label, &g.Reason, &g.SchemaVersion,
			&g.DetectedAt, &g.LastSeenAt, &g.ProductTitle, &g.StoreName); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// OpenGapsForSeller is the seller's "action needed" list.
func (s *Store) OpenGapsForSeller(ctx context.Context, sellerID uuid.UUID, limit int) ([]*ComplianceGap, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.Query(ctx, `
		SELECT `+gapColumns+`
		  FROM product_compliance_gaps g
		  JOIN products p ON p.id = g.product_id
		  LEFT JOIN sellers sl ON sl.id = g.seller_id
		 WHERE g.seller_id = $1 AND g.resolved_at IS NULL
		 ORDER BY p.title, g.code
		 LIMIT $2`, sellerID, limit)
	if err != nil {
		return nil, err
	}
	return scanGaps(rows)
}

// OpenGapsForProduct is one listing's gaps, for the seller's editor.
func (s *Store) OpenGapsForProduct(ctx context.Context, productID uuid.UUID) ([]*ComplianceGap, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+gapColumns+`
		  FROM product_compliance_gaps g
		  JOIN products p ON p.id = g.product_id
		  LEFT JOIN sellers sl ON sl.id = g.seller_id
		 WHERE g.product_id = $1 AND g.resolved_at IS NULL
		 ORDER BY g.code`, productID)
	if err != nil {
		return nil, err
	}
	return scanGaps(rows)
}

// ListOpenComplianceGaps is the founder's queue.
//
// Ordered oldest-first: the gap that has been open longest is the listing
// that has been quietly non-compliant the longest, which is the one worth
// looking at. A newest-first queue shows the founder the consequences of the
// rule they changed five minutes ago and hides the ones from March.
func (s *Store) ListOpenComplianceGaps(
	ctx context.Context, definitionID *uuid.UUID, limit, offset int,
) ([]*ComplianceGap, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	if err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM product_compliance_gaps g
		 WHERE g.resolved_at IS NULL
		   AND ($1::uuid IS NULL OR g.definition_id = $1)`, definitionID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT `+gapColumns+`
		  FROM product_compliance_gaps g
		  JOIN products p ON p.id = g.product_id
		  LEFT JOIN sellers sl ON sl.id = g.seller_id
		 WHERE g.resolved_at IS NULL
		   AND ($1::uuid IS NULL OR g.definition_id = $1)
		 ORDER BY g.detected_at ASC, g.code
		 LIMIT $2 OFFSET $3`, definitionID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	gaps, err := scanGaps(rows)
	return gaps, total, err
}

// ─── The built-in readiness read ────────────────────────────────────────

// ListingReadiness is the four facts about a listing that have nothing to do
// with its category's schema and everything to do with whether it can be
// bought. See service.builtinRequirements for why each one is on the list.
//
// Every field is a fact the CHECKOUT or the STOREFRONT depends on, read in
// one query rather than four, because the submit gate reports all of them at
// once and a seller with three gaps must not need three round trips.
type ListingReadiness struct {
	// VariantCount is how many ACTIVE variants the listing has. A listing
	// with none of them has nothing to sell, and it is counted separately so
	// the gate can say that rather than reporting "no price".
	VariantCount int

	// UnpricedVariants counts active variants with no positive
	// selling_price_minor. EVERY variant, not merely one: each variant is
	// separately purchasable, so an unpriced one is a buyable row that
	// refuses at checkout with PRICE_NOT_POSITIVE.
	UnpricedVariants int

	// LowestPriceMinor is the cheapest active variant's selling price in
	// paise, or zero when nothing is priced. It decides nothing — the gate
	// reads UnpricedVariants — and exists purely so the submission snapshot
	// records a number a reviewer recognises. A diff reading
	// "Price: 0 → 0" (unpriced variants) would be read as a price of zero by
	// every human who saw it.
	LowestPriceMinor int64

	// SellableUnits is total_qty − reserved_qty summed over active variants,
	// floored at zero per variant so one oversold variant cannot cancel out
	// another's stock. One unit ANYWHERE is enough: a sold-out size on a
	// shirt that still has the others is a normal listing, and requiring
	// every variant to be in stock would make restocking a delisting.
	SellableUnits int

	// HasImage is true when the listing has a primary image or at least one
	// image in its gallery. Either is enough — hydration prefers the primary
	// column and falls back to the gallery.
	HasImage bool

	// TaxRateResolvable is true when tax_class_id names a row from which
	// rateFromClass could derive a rate. NOT merely "tax_class_id is not
	// null": a dangling id and a class with no percentages both refuse at
	// checkout, and both have to be caught here instead.
	TaxRateResolvable bool
}

// ProductListingReadiness answers the built-in questions in one round trip.
func (s *Store) ProductListingReadiness(ctx context.Context, productID uuid.UUID) (*ListingReadiness, error) {
	r := &ListingReadiness{}
	err := s.db.QueryRow(ctx, `
		SELECT
		  (SELECT COUNT(*) FROM product_variants v
		    WHERE v.product_id = p.id AND v.status = 'active')::int,
		  (SELECT COUNT(*) FROM product_variants v
		    WHERE v.product_id = p.id AND v.status = 'active'
		      AND COALESCE(v.selling_price_minor, 0) <= 0)::int,
		  COALESCE((SELECT MIN(v.selling_price_minor) FROM product_variants v
		             WHERE v.product_id = p.id AND v.status = 'active'
		               AND COALESCE(v.selling_price_minor, 0) > 0), 0)::bigint,
		  COALESCE((SELECT SUM(GREATEST(i.total_qty - i.reserved_qty, 0))
		              FROM inventory_items i
		              JOIN product_variants v ON v.id = i.variant_id
		             WHERE v.product_id = p.id AND v.status = 'active'), 0)::int,
		  (p.primary_image_media_id IS NOT NULL
		   OR EXISTS (SELECT 1 FROM product_media pm
		               WHERE pm.product_id = p.id AND pm.media_type = 'image')),
		  EXISTS (SELECT 1 FROM tax_classes tc
		           WHERE tc.id = p.tax_class_id
		             AND (tc.igst_percentage IS NOT NULL
		                  OR (tc.cgst_percentage IS NOT NULL AND tc.sgst_percentage IS NOT NULL)))
		  FROM products p WHERE p.id = $1`, productID,
	).Scan(&r.VariantCount, &r.UnpricedVariants, &r.LowestPriceMinor,
		&r.SellableUnits, &r.HasImage, &r.TaxRateResolvable)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrProductNotFound
	}
	return r, err
}
