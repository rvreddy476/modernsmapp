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
//
// ─── AND PER SELL SCOPE (migration 045) ─────────────────────────────────
//
//	all_categories        a GSTIN. Founder, 24 Oct 2026: every category but
//	                      books needs one. Nothing required it before this,
//	                      and shops approved earlier are not re-checked —
//	                      readiness gates the SUBMIT, nothing else.
//	books_only            a PAN (sealed, migration 035), the business name,
//	                      a business address with its PIN code and state,
//	                      and ONE proof of address: a voter ID or passport
//	                      (front AND back), or an electricity bill, telephone
//	                      bill or bank statement. The proof is itself a KYC
//	                      document, so it also meets `kyc_document`.
//
// An unrecognised scope is never complete. A value this code has no rule for
// must not read as "nothing further required".

import (
	"context"

	"github.com/google/uuid"
)

// The two sell scopes (sellers.sell_scope, migration 045).
const (
	SellScopeAllCategories = "all_categories"
	SellScopeBooksOnly     = "books_only"
)

// ValidSellScope reports whether s is one of the two scopes.
func ValidSellScope(s string) bool {
	return s == SellScopeAllCategories || s == SellScopeBooksOnly
}

// AddressProofDocumentTypes are the documents a books-only seller may prove
// their address with. The Flipkart "Only Books" list: voter ID, passport,
// electricity bill, telephone bill, bank statement.
var AddressProofDocumentTypes = []string{
	"voter_id", "passport", "electricity_bill", "telephone_bill", "bank_statement",
}

// TwoSidedDocumentTypes need their back uploaded as well (back_media_id).
// The address on a voter ID and on a passport is on the back.
var TwoSidedDocumentTypes = []string{"voter_id", "passport"}

// IsAddressProofType reports whether t proves an address.
func IsAddressProofType(t string) bool { return contains(AddressProofDocumentTypes, t) }

// IsTwoSidedType reports whether t has a back that must be uploaded.
func IsTwoSidedType(t string) bool { return contains(TwoSidedDocumentTypes, t) }

// SellerReadiness is what is still missing before a shop can be reviewed.
type SellerReadiness struct {
	SellScope        string `json:"sell_scope"`
	HasStoreName     bool   `json:"has_store_name"`
	HasEmail         bool   `json:"has_email"`
	HasPickupAddress bool   `json:"has_pickup_address"`
	HasPayoutAccount bool   `json:"has_payout_account"`
	HasKYCDocument   bool   `json:"has_kyc_document"`
	// all_categories only.
	HasGSTIN bool `json:"has_gstin"`
	// books_only only.
	HasPAN             bool `json:"has_pan"`
	HasBusinessName    bool `json:"has_business_name"`
	HasBusinessAddress bool `json:"has_business_address"`
	HasAddressProof    bool `json:"has_address_proof"`
}

// Complete reports whether every requirement is met.
func (r SellerReadiness) Complete() bool {
	return len(r.Missing()) == 0
}

// Missing names what is still needed, in the order a seller would supply it.
//
// Returned as a list rather than a first-failure so the app can show the whole
// remaining checklist. A reviewer's queue is not a security boundary, and
// telling a seller one missing item at a time turns a five-minute task into
// five round trips.
//
// The scope's own requirements come first: they are what the onboarding
// asks for first ("What will you sell?").
func (r SellerReadiness) Missing() []string {
	missing := []string{}
	switch r.SellScope {
	case SellScopeAllCategories:
		if !r.HasGSTIN {
			missing = append(missing, "gstin")
		}
	case SellScopeBooksOnly:
		if !r.HasPAN {
			missing = append(missing, "pan")
		}
		if !r.HasBusinessName {
			missing = append(missing, "business_name")
		}
		if !r.HasBusinessAddress {
			missing = append(missing, "business_address")
		}
		if !r.HasAddressProof {
			missing = append(missing, "address_proof")
		}
	default:
		missing = append(missing, "sell_scope")
	}
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
// once. Every fact is read whatever the scope; Missing decides which count.
func (s *Store) SellerReadinessFor(ctx context.Context, sellerID uuid.UUID) (*SellerReadiness, error) {
	var r SellerReadiness
	err := s.db.QueryRow(ctx, `
		SELECT
		  s.sell_scope,
		  COALESCE(NULLIF(btrim(s.store_name), ''), '') <> '',
		  COALESCE(NULLIF(btrim(s.email), ''), '') <> '',
		  EXISTS (
		      SELECT 1 FROM seller_addresses a
		       WHERE a.seller_id = s.id
		         -- 045: a business address is not where the courier collects.
		         AND a.address_type <> 'business'
		         AND COALESCE(NULLIF(btrim(a.postal_code), ''), '') <> ''
		         AND COALESCE(NULLIF(btrim(a.state), ''), '') <> ''
		  ),
		  EXISTS (
		      SELECT 1 FROM seller_payout_accounts p
		       WHERE p.seller_id = s.id
		         AND (
		             COALESCE(NULLIF(btrim(p.upi_id), ''), '') <> ''
		             OR (
		                 (p.account_number_enc IS NOT NULL -- sealed (035), or legacy plaintext
			              OR COALESCE(NULLIF(btrim(p.account_number), ''), '') <> '')
		                 AND COALESCE(NULLIF(btrim(p.ifsc_code), ''), '') <> ''
		             )
		         )
		  ),
		  EXISTS (SELECT 1 FROM seller_documents d WHERE d.seller_id = s.id),
		  COALESCE(NULLIF(btrim(s.gst_number), ''), '') <> '',
		  (s.pan_enc IS NOT NULL OR COALESCE(NULLIF(btrim(s.pan_number), ''), '') <> ''),
		  COALESCE(NULLIF(btrim(s.legal_business_name), ''), '') <> '',
		  EXISTS (
		      SELECT 1 FROM seller_addresses a
		       WHERE a.seller_id = s.id
		         AND a.address_type = 'business'
		         AND COALESCE(NULLIF(btrim(a.postal_code), ''), '') <> ''
		         AND COALESCE(NULLIF(btrim(a.state), ''), '') <> ''
		  ),
		  EXISTS (
		      SELECT 1 FROM seller_documents d
		       WHERE d.seller_id = s.id
		         AND d.document_type = ANY($2::text[])
		         AND (NOT (d.document_type = ANY($3::text[])) OR d.back_media_id IS NOT NULL)
		  )
		FROM sellers s
		WHERE s.id = $1`, sellerID, AddressProofDocumentTypes, TwoSidedDocumentTypes).Scan(
		&r.SellScope, &r.HasStoreName, &r.HasEmail, &r.HasPickupAddress,
		&r.HasPayoutAccount, &r.HasKYCDocument,
		&r.HasGSTIN, &r.HasPAN, &r.HasBusinessName, &r.HasBusinessAddress, &r.HasAddressProof)
	if err != nil {
		return nil, err
	}
	return &r, nil
}
