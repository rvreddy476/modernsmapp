package postgres

// The GST rate table a seller must choose from.
//
// ─── WHY THIS EXISTS ────────────────────────────────────────────────────
//
// `products.tax_class_id` is nullable and `POST /v1/commerce/products` took it
// as an optional field, so a seller could create a product without one. That
// product is not merely untaxed — it is **unsellable**. Checkout resolves the
// rate under a row lock and refuses when there is none:
//
//	rateFromClass: if taxClassID == nil { return 0, ErrTaxClassMissing }
//	→ 409 PRODUCT_TAX_UNCONFIGURED
//
// So the listing goes live, appears in search, sits in a buyer's cart, and
// fails at the last step with an error the seller never sees. The refusal is
// correct — a product whose GST cannot be computed must not be sold, and
// silently treating a missing class as 0% would under-collect tax on every
// sale — but nothing ever told the seller to set one, and nothing gave them
// the list to choose from.
//
// There was no endpoint exposing these rows at all. A create form had no way
// to offer the choice, which is why the field stayed optional and why every
// product created through the API was a product that could not be bought.
//
// ─── WHY NOT DEFAULT IT ─────────────────────────────────────────────────
//
// Picking a default rate is worse than refusing. 18% on a 5% item overcharges
// every buyer and files the wrong tax; 0% on an 18% item leaves the seller
// owing GST they never collected. The rate is a fact about the goods that only
// the seller knows, so the server asks and refuses to guess.

import (
	"context"

	"github.com/google/uuid"
)

// TaxClassOption is one row of the rate table, as a seller sees it.
type TaxClassOption struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	// RatePercent is the single number a seller recognises: the total GST on
	// the sale. CGST+SGST within a state and IGST across one come to the same
	// figure, and showing three columns invites a seller to think they are
	// choosing between them when the place of supply decides that.
	RatePercent float64 `json:"rate_percent"`
}

// ListTaxClasses returns the rate table, cheapest first.
//
// Public: these are statutory rates, not seller data. Ordering by rate rather
// than by name puts "GST 0%" before "GST 5%" instead of after "GST 28%", which
// is what an alphabetical sort on the name does.
func (s *Store) ListTaxClasses(ctx context.Context) ([]TaxClassOption, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, name,
		       -- IGST is the whole-sale rate. CGST+SGST is the same total
		       -- split in two for an intra-state sale, so it is the fallback
		       -- when a row states the halves and not the total.
		       CASE WHEN igst_percentage > 0 THEN igst_percentage
		            ELSE cgst_percentage + sgst_percentage END
		  FROM tax_classes
		 ORDER BY 3, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []TaxClassOption{}
	for rows.Next() {
		var tc TaxClassOption
		if err := rows.Scan(&tc.ID, &tc.Name, &tc.RatePercent); err != nil {
			return nil, err
		}
		out = append(out, tc)
	}
	return out, rows.Err()
}

// TaxClassExists reports whether an id names a real rate row.
//
// Checked before a product is created rather than left to the foreign key,
// because an FK violation reaches the edge as an opaque 500 and the seller
// needs to be told which field is wrong.
func (s *Store) TaxClassExists(ctx context.Context, id uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM tax_classes WHERE id = $1)`, id).Scan(&exists)
	return exists, err
}
