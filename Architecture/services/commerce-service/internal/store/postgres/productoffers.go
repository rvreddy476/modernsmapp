package postgres

// The seller's OFFER, split out of the catalogue row — write side only.
//
// ─── WHAT THIS FILE IS FOR ──────────────────────────────────────────────
//
// Migration 027 created `product_offers` and backfilled it 1:1 from
// `products`. This file is the code half of that: every path that creates a
// product creates its offer, and every path that changes a product's
// lifecycle columns changes the offer's copy in the SAME transaction.
//
// ─── THE SHADOW HAS BEEN PROMOTED ───────────────────────────────────────
//
// This file used to say that nothing read `product_offers`. That stopped
// being true in step 14: the buyer-facing reads — browse, home, the product
// page, the category counts, the seller's catalogue, cart hydration, the
// search documents — now resolve a listing's lifecycle AND its seller from
// the offer row. See `productOfferJoin` in storefront.go for the join and the
// argument for its shape.
//
// CheckProductOfferConsistency below was the instrument that decided the
// shadow was trustworthy enough to promote. It reported the estate clean —
// every product matched, nothing missing, nothing diverging — and that report
// is what the flip was gated on.
//
// ─── WHY THE DUAL-WRITE IS STILL HERE ───────────────────────────────────
//
// The legacy columns on `products` are still written by every path that
// writes them, and this file is why. That is not leftover scaffolding and it
// must not be tidied away:
//
//	ROLLBACK. Rolling the service image back one deploy puts readers on
//	`products` again. If the writes had stopped when the reads moved, every
//	lifecycle change made in between would be invisible to the rolled-back
//	image — an approval that un-approves itself, a withdrawn listing back on
//	sale.
//
//	THE PHONE. It is frozen on the shipped contract, and the contract is
//	served from these columns' values. They are the same values; that is the
//	whole point, and it stays true only while both copies are written.
//
//	THE CHECKER. It compares the two copies. Stop writing one and the
//	instrument that would tell you the other is wrong stops working, in the
//	same change.
//
// So the order is: write both (027), prove they agree (the checker), move the
// readers (this step), and only then — a deploy later, deliberately, with the
// checker still clean — narrow the columns. Not before.
//
// ─── WHY THE SYNC COPIES RATHER THAN MIRRORS ────────────────────────────
//
// There are five statements in this package that change a product's
// lifecycle — approve, request-changes, reject, submit, and the patch's
// revalidation bounce — and each writes a different combination of literals.
// The obvious dual-write is to repeat each combination against the offer.
//
// That is the version that drifts. A sixth transition gets added, or an
// existing one grows a column, and the second copy of the literals is updated
// in four places out of five. The copy is silently wrong from then on, and
// nothing fails until the readers move.
//
// So syncOfferLifecycleTx does not mirror the literals. It reads the product
// row that was just written and copies it — one statement, no vocabulary of
// its own, correct for any transition including ones not yet written. The
// only thing a new transition has to remember is to CALL it, and a caller
// that forgets shows up in the checker as a divergence rather than as a
// buyer seeing a withdrawn listing.

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ProductOffer is one shop's willingness to sell one catalogue item.
//
// The lifecycle columns are the ones that were on `products` and are
// conceptually per-seller: two shops listing the same book are approved
// separately, publish separately, and are taken down separately.
type ProductOffer struct {
	ID               uuid.UUID  `json:"id"`
	ProductID        uuid.UUID  `json:"product_id"`
	SellerID         uuid.UUID  `json:"seller_id"`
	Status           string     `json:"status"`
	Visibility       string     `json:"visibility"`
	ApprovalStatus   string     `json:"approval_status"`
	RejectionReason  *string    `json:"rejection_reason,omitempty"`
	PublishedAt      *time.Time `json:"published_at,omitempty"`
	Condition        string     `json:"condition"`
	HandlingTimeDays *int       `json:"handling_time_days,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// insertOfferForProductTx is the ONLY INSERT into `product_offers` in this
// package, and it is called from exactly one place: insertProductTx.
//
// Putting it there rather than at each of the two create call sites is what
// makes "every path that creates a product creates its offer" a property of
// the code instead of a promise. `CreateProduct` and `CreateProductAtomic`
// both go through insertProductTx; a third create added tomorrow will too,
// because there is no other statement that can put a row in `products`.
//
// The values are read back off the product struct rather than defaulted, so
// a create that asks for `status='active'` gets an offer that says so — the
// two copies agree from the first instant, not from the first sync.
func insertOfferForProductTx(ctx context.Context, tx pgx.Tx, p *Product) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO product_offers
		    (id, product_id, seller_id, status, visibility, approval_status,
		     rejection_reason, published_at, condition, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
		ON CONFLICT (product_id, seller_id) DO NOTHING`,
		p.ID, p.SellerID, p.Status, p.Visibility, p.ApprovalStatus,
		p.RejectionReason, p.PublishedAt, p.Condition, p.CreatedAt,
	)
	return err
}

// LinkVariantToOfferTx points a freshly-inserted variant at its seller's
// offer for the product.
//
// Resolved by (product_id, seller_id) rather than carried down from the
// create, because the variant insert does not know the seller — it knows the
// product, and the product knows the seller. One subquery, and it cannot
// name an offer belonging to a different shop.
//
// Silent when there is no offer: a variant inserted against a product created
// by a pod on the previous image has none to point at, and refusing the
// insert would take the catalogue down during a rollout to add a column
// nothing reads yet. The checker counts those instead.
func linkVariantToOfferTx(ctx context.Context, tx pgx.Tx, variantID, productID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE product_variants v
		   SET offer_id = o.id
		  FROM product_offers o
		  JOIN products p ON p.id = o.product_id AND o.seller_id = p.seller_id
		 WHERE v.id = $1 AND o.product_id = $2 AND v.offer_id IS NULL`,
		variantID, productID)
	return err
}

// syncOfferLifecycleTx copies a product's lifecycle columns onto its
// seller's offer. See the file header for why it copies rather than mirrors.
//
// Call it inside the SAME transaction as the products UPDATE. Outside one,
// a crash between the two writes leaves the copies disagreeing, which is
// precisely the state the checker exists to find and precisely the state a
// dual-write is supposed to make impossible.
//
// A product with no offer row is a no-op, not an error, for the same reason
// linkVariantToOfferTx is: mid-rollout there can be products the backfill
// never saw, and an approval that 500s because a shadow table is behind is a
// worse failure than a shadow table that is behind.
func syncOfferLifecycleTx(ctx context.Context, tx pgx.Tx, productID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE product_offers o
		   SET status           = p.status,
		       visibility       = p.visibility,
		       approval_status  = p.approval_status,
		       rejection_reason = p.rejection_reason,
		       published_at     = p.published_at,
		       condition        = p.condition,
		       updated_at       = NOW()
		  FROM products p
		 WHERE p.id = $1
		   AND o.product_id = p.id
		   AND o.seller_id  = p.seller_id`,
		productID)
	return err
}

// SyncOfferLifecycle is the non-transactional form, for a caller that has
// already committed its products UPDATE and cannot be retrofitted to a
// transaction. Nothing uses it today; it exists so that a future caller
// reaching for a shortcut finds one that at least writes the right thing.
func (s *Store) SyncOfferLifecycle(ctx context.Context, productID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := syncOfferLifecycleTx(ctx, tx, productID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ─── The instrument ─────────────────────────────────────────────────────

// OfferDivergence is one product whose offer does not say what the product
// says, with both sides quoted.
//
// Both sides, not just the fields' names: an operator looking at this is
// deciding whether the shadow is safe to promote, and "approval_status
// differs" does not tell them whether the offer is stale by one transition
// or holding a value from a different vocabulary entirely.
type OfferDivergence struct {
	ProductID uuid.UUID
	OfferID   uuid.UUID
	Field     string
	Product   string
	Offer     string
}

// OfferConsistencyReport is the answer to "may the readers move yet".
//
// Counts are over the WHOLE table — every product, every offer, no sampling —
// because the question is whether the estate is clean, and a sampled answer
// to that question is not an answer. The Divergent/Missing slices are capped
// so that a report over a broken estate is still readable; the counts beside
// them are not capped and are what a gate should test.
type OfferConsistencyReport struct {
	Products int
	Offers   int

	// MissingOfferCount is products with no offer for their own seller_id.
	MissingOfferCount int
	MissingOffer      []uuid.UUID

	// DivergentCount is products whose offer disagrees on at least one of
	// seller, status, visibility, approval_status or published_at. One
	// product with three disagreeing fields counts once here and appears
	// three times in Divergent.
	DivergentCount int
	Divergent      []OfferDivergence

	// ExtraOfferCount is offers whose product has no row — impossible while
	// the foreign key stands, reported anyway so that removing the key
	// cannot remove the check with it.
	ExtraOfferCount int

	// VariantsWithoutOffer is variants whose offer_id is still NULL. Not a
	// divergence — the column is nullable by design until a later step — but
	// it is the number that step has to reach zero.
	VariantsWithoutOffer int
}

// OK reports whether the two copies agree everywhere.
func (r *OfferConsistencyReport) OK() bool {
	return r.MissingOfferCount == 0 && r.DivergentCount == 0 && r.ExtraOfferCount == 0
}

const offerConsistencySampleLimit = 25

// CheckProductOfferConsistency walks every product and asserts that its
// offer agrees on seller, status, visibility, approval_status and
// published_at.
//
// This is the gate for the step that moves the readers onto `product_offers`.
// It is worth more than the dual-write itself: a dual-write is a claim, and
// this is the only thing that can check it against an estate that has been
// through a rolling deploy, a rollback, an operator's manual UPDATE and
// whatever the previous image was doing at the time.
//
// `rejection_reason` and `condition` are synced too but are NOT asserted
// here: `rejection_reason` is free text an operator may legitimately have
// edited on one side during the shadow period, and `condition` becomes a
// genuinely per-offer fact the moment a second seller lists the same item,
// so a disagreement there stops being drift and becomes the point. The five
// fields asserted are the ones whose disagreement would change what a buyer
// is shown.
func (s *Store) CheckProductOfferConsistency(ctx context.Context) (*OfferConsistencyReport, error) {
	rep := &OfferConsistencyReport{}

	if err := s.db.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM products),
		       (SELECT count(*) FROM product_offers),
		       (SELECT count(*) FROM product_variants WHERE offer_id IS NULL),
		       (SELECT count(*) FROM product_offers o
		         WHERE NOT EXISTS (SELECT 1 FROM products p WHERE p.id = o.product_id)),
		       (SELECT count(*) FROM products p
		         WHERE NOT EXISTS (SELECT 1 FROM product_offers o
		                            WHERE o.product_id = p.id AND o.seller_id = p.seller_id))`,
	).Scan(&rep.Products, &rep.Offers, &rep.VariantsWithoutOffer,
		&rep.ExtraOfferCount, &rep.MissingOfferCount); err != nil {
		return nil, err
	}

	if rep.MissingOfferCount > 0 {
		rows, err := s.db.Query(ctx, `
			SELECT p.id FROM products p
			 WHERE NOT EXISTS (SELECT 1 FROM product_offers o
			                    WHERE o.product_id = p.id AND o.seller_id = p.seller_id)
			 ORDER BY p.created_at DESC
			 LIMIT $1`, offerConsistencySampleLimit)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			rep.MissingOffer = append(rep.MissingOffer, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	// The join is on product_id ALONE, not on (product_id, seller_id): an
	// offer that has drifted onto the wrong seller is the single most
	// damaging divergence there is — it is one shop's listing attributed to
	// another — and joining on seller_id would hide it as a missing offer
	// rather than name it.
	if err := s.db.QueryRow(ctx, `
		SELECT count(DISTINCT p.id)
		  FROM products p
		  JOIN product_offers o ON o.product_id = p.id
		 WHERE o.seller_id       IS DISTINCT FROM p.seller_id
		    OR o.status          IS DISTINCT FROM p.status
		    OR o.visibility      IS DISTINCT FROM p.visibility
		    OR o.approval_status IS DISTINCT FROM p.approval_status
		    OR o.published_at    IS DISTINCT FROM p.published_at`,
	).Scan(&rep.DivergentCount); err != nil {
		return nil, err
	}

	if rep.DivergentCount == 0 {
		return rep, nil
	}

	rows, err := s.db.Query(ctx, `
		SELECT p.id, o.id, f.field, f.p_val, f.o_val
		  FROM products p
		  JOIN product_offers o ON o.product_id = p.id
		 CROSS JOIN LATERAL (VALUES
		        ('seller_id',       p.seller_id::text,       o.seller_id::text),
		        ('status',          p.status,                o.status),
		        ('visibility',      p.visibility,            o.visibility),
		        ('approval_status', p.approval_status,       o.approval_status),
		        ('published_at',    p.published_at::text,    o.published_at::text)
		 ) AS f(field, p_val, o_val)
		 WHERE f.p_val IS DISTINCT FROM f.o_val
		 ORDER BY p.updated_at DESC
		 LIMIT $1`, offerConsistencySampleLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d OfferDivergence
		var pv, ov *string
		if err := rows.Scan(&d.ProductID, &d.OfferID, &d.Field, &pv, &ov); err != nil {
			return nil, err
		}
		d.Product, d.Offer = derefOrNULL(pv), derefOrNULL(ov)
		rep.Divergent = append(rep.Divergent, d)
	}
	return rep, rows.Err()
}

func derefOrNULL(s *string) string {
	if s == nil {
		return "NULL"
	}
	return *s
}

// GetOfferForProduct returns one seller's offer on a product.
//
// Test-facing and diagnostic. The reader flip did NOT route any endpoint
// through here: the buyer-facing reads take the offer as a JOIN inside the
// query that was already being run, because a second round trip per product
// to fetch a row the first query could have joined is how a grid becomes
// N+1. This exists so the dual-write's tests can assert on the row they claim
// to have written without reaching past the store into raw SQL.
func (s *Store) GetOfferForProduct(ctx context.Context, productID, sellerID uuid.UUID) (*ProductOffer, error) {
	o := &ProductOffer{}
	err := s.db.QueryRow(ctx, `
		SELECT id, product_id, seller_id, status, visibility, approval_status,
		       rejection_reason, published_at, condition, handling_time_days,
		       created_at, updated_at
		  FROM product_offers WHERE product_id = $1 AND seller_id = $2`,
		productID, sellerID,
	).Scan(&o.ID, &o.ProductID, &o.SellerID, &o.Status, &o.Visibility, &o.ApprovalStatus,
		&o.RejectionReason, &o.PublishedAt, &o.Condition, &o.HandlingTimeDays,
		&o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return o, nil
}
