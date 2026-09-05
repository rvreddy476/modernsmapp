package postgres

// Whether a shop is actually ready to be reviewed.
//
// ─── WHY THIS EXISTS ────────────────────────────────────────────────────
//
// `SubmitSellerApplication` flipped any `draft` seller to `submitted` and
// checked nothing else:
//
//	UPDATE sellers SET status='submitted' WHERE user_id=$1 AND status='draft'
//
// So a shop with no PAN, no bank account and no pickup address could be
// submitted, and it landed in a human reviewer's queue as an empty
// application. Three things go wrong at once, and all three are quiet:
//
//   - the reviewer has nothing to approve against, and the queue fills with
//     applications that cannot be actioned;
//   - the seller is told "submitted" and waits, when nothing was sent;
//   - if a reviewer approves anyway, the shop goes live without a payout
//     account — it can take money it has no way to be paid.
//
// The last one is the reason this is a launch-safety check and not a UI
// nicety. A seller with no bank details who starts selling is an obligation
// with no settlement path.
//
// ─── WHAT IS REQUIRED, AND WHY EACH ─────────────────────────────────────
//
// Deliberately the minimum a reviewer needs to say yes, not everything the
// schema can hold:
//
//	store name + email    who this is and how to reach them
//	pickup address        the origin of every shipment, and the seller half
//	                      of the GST place-of-supply comparison
//	payout account        where the money goes; without it an approved shop
//	                      can sell and cannot be paid
//	one KYC document      what the reviewer actually checks identity against
//
// Fulfilment settings are NOT required. They have working defaults, and a
// reviewer does not need them to decide whether this is a real business.

import (
	"context"

	"github.com/google/uuid"
)

// SellerReadiness is what is still missing before a shop can be reviewed.
type SellerReadiness struct {
	HasStoreName     bool `json:"has_store_name"`
	HasEmail         bool `json:"has_email"`
	HasPickupAddress bool `json:"has_pickup_address"`
	HasPayoutAccount bool `json:"has_payout_account"`
	HasKYCDocument   bool `json:"has_kyc_document"`
}

// Complete reports whether every requirement is met.
func (r SellerReadiness) Complete() bool {
	return r.HasStoreName && r.HasEmail && r.HasPickupAddress &&
		r.HasPayoutAccount && r.HasKYCDocument
}

// Missing names what is still needed, in the order a seller would supply it.
//
// Returned as a list rather than a first-failure so the app can show the whole
// remaining checklist. A reviewer's queue is not a security boundary, and
// telling a seller one missing item at a time turns a five-minute task into
// five round trips.
func (r SellerReadiness) Missing() []string {
	missing := []string{}
	if !r.HasStoreName {
		missing = append(missing, "store_name")
	}
	if !r.HasEmail {
		missing = append(missing, "email")
	}
	if !r.HasPickupAddress {
		missing = append(missing, "pickup_address")
	}
	if !r.HasPayoutAccount {
		missing = append(missing, "payout_account")
	}
	if !r.HasKYCDocument {
		missing = append(missing, "kyc_document")
	}
	return missing
}

// SellerReadinessFor reports what a shop still needs before review.
//
// One query. A per-requirement round trip would let the answer change between
// checks, and the seller would be shown a checklist that was never true all at
// once.
func (s *Store) SellerReadinessFor(ctx context.Context, sellerID uuid.UUID) (*SellerReadiness, error) {
	var r SellerReadiness
	err := s.db.QueryRow(ctx, `
		SELECT
		  COALESCE(NULLIF(btrim(s.store_name), ''), '') <> '',
		  COALESCE(NULLIF(btrim(s.email), ''), '') <> '',
		  EXISTS (
		      SELECT 1 FROM seller_addresses a
		       WHERE a.seller_id = s.id
		         AND COALESCE(NULLIF(btrim(a.postal_code), ''), '') <> ''
		         AND COALESCE(NULLIF(btrim(a.state), ''), '') <> ''
		  ),
		  EXISTS (
		      SELECT 1 FROM seller_payout_accounts p
		       WHERE p.seller_id = s.id
		         AND (
		             COALESCE(NULLIF(btrim(p.upi_id), ''), '') <> ''
		             OR (
		                 COALESCE(NULLIF(btrim(p.account_number), ''), '') <> ''
		                 AND COALESCE(NULLIF(btrim(p.ifsc_code), ''), '') <> ''
		             )
		         )
		  ),
		  EXISTS (SELECT 1 FROM seller_documents d WHERE d.seller_id = s.id)
		FROM sellers s
		WHERE s.id = $1`, sellerID).Scan(
		&r.HasStoreName, &r.HasEmail, &r.HasPickupAddress,
		&r.HasPayoutAccount, &r.HasKYCDocument)
	if err != nil {
		return nil, err
	}
	return &r, nil
}
