// Sellers: creating and reading seller rows, seller status, and the
// seller pickup/warehouse address upsert. Moved out of store.go unchanged.

package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ─── Seller ──────────────────────────────────────────────────

func (s *Store) CreateSeller(ctx context.Context, sel *Seller) error {
	sel.ID = uuid.New()
	sel.CreatedAt = time.Now()
	sel.UpdatedAt = time.Now()
	_, err := s.db.Exec(ctx, `
		INSERT INTO sellers (id, user_id, seller_type, store_name, brand_name, slug, description,
		  email, phone, gst_number, state, city, postal_code, verification_status, store_status, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
		sel.ID, sel.UserID, sel.SellerType, sel.StoreName, sel.BrandName, sel.Slug, sel.Description,
		sel.Email, sel.Phone, sel.GSTNumber, sel.State, sel.City, sel.PostalCode,
		sel.VerificationStatus, sel.StoreStatus, sel.CreatedAt, sel.UpdatedAt,
	)
	return err
}

func (s *Store) GetSellerByUserID(ctx context.Context, userID uuid.UUID) (*Seller, error) {
	var sel Seller
	// `status` and `onboarding_step` are selected, and that is not cosmetic.
	//
	// They were absent from this list, so `GET /v1/commerce/sellers/me`
	// always answered `"status": ""` and `"onboarding_step": 0` no matter what
	// the row actually held. A client could not tell a draft seller from an
	// approved one — which is precisely the question any seller UI has to
	// answer before it decides what to show, and the question that decides
	// whether `POST /products/:id/submit` will be refused.
	//
	// `status` is the onboarding/approval state machine (draft → submitted →
	// approved). `verification_status` is the separate KYC column, and the two
	// are not interchangeable: migration 014 added `format_ok`, which means
	// only that a regex liked the document's shape.
	err := s.db.QueryRow(ctx, `SELECT id,user_id,seller_type,store_name,brand_name,slug,description,
		logo_media_id,banner_media_id,email,phone,gst_number,pan_number,pan_enc,pan_masked,state,city,postal_code,
		verification_status,store_status,quality_score,performance_tier,avg_rating,review_count,
		follower_count,total_products,total_orders,created_at,updated_at,
		status,onboarding_step,sell_scope,legal_business_name
		FROM sellers WHERE user_id=$1`, userID).Scan(
		&sel.ID, &sel.UserID, &sel.SellerType, &sel.StoreName, &sel.BrandName, &sel.Slug, &sel.Description,
		&sel.LogoMediaID, &sel.BannerMediaID, &sel.Email, &sel.Phone, &sel.GSTNumber, &sel.PANNumber,
		&sel.PANEnc, &sel.PANMasked,
		&sel.State, &sel.City, &sel.PostalCode, &sel.VerificationStatus, &sel.StoreStatus,
		&sel.QualityScore, &sel.PerformanceTier, &sel.AvgRating, &sel.ReviewCount,
		&sel.FollowerCount, &sel.TotalProducts, &sel.TotalOrders, &sel.CreatedAt, &sel.UpdatedAt,
		&sel.Status, &sel.OnboardingStep, &sel.SellScope, &sel.LegalBusinessName,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		// The caller has no seller account.
		//
		// This used to `return &sel, err` — a non-nil, zero-valued Seller
		// alongside pgx.ErrNoRows. Every caller guarding with
		// `if seller == nil { return ErrNoSellerProfile }` was therefore
		// holding a check that could not fire, and the raw pgx error reached
		// the edge as a 500 instead of a 403.
		//
		// The nil is what makes those guards live. The typed sentinel is what
		// lets a handler tell "this user is not a seller" apart from "the
		// database is down" — the existing handlers map *any* error here to
		// 403 NO_SELLER, which reports an outage as an authorisation failure.
		return nil, ErrNoSellerRow
	}
	if err != nil {
		return nil, err
	}
	return &sel, nil
}

// ErrNoSellerRow means the user has no seller account.
//
// It is an error rather than a (nil, nil) return because the onboarding path
// reads "no row" as "start a new draft", and that branch predates this
// package's nil-and-sentinel convention.
var ErrNoSellerRow = errors.New("commerce: this user has no seller account")

func (s *Store) GetSellerByID(ctx context.Context, id uuid.UUID) (*Seller, error) {
	var sel Seller
	err := s.db.QueryRow(ctx, `SELECT id,user_id,seller_type,store_name,brand_name,slug,description,
		logo_media_id,banner_media_id,email,phone,gst_number,pan_number,pan_enc,pan_masked,state,city,postal_code,
		verification_status,store_status,quality_score,performance_tier,avg_rating,review_count,
		follower_count,total_products,total_orders,created_at,updated_at,
		sell_scope,legal_business_name
		FROM sellers WHERE id=$1`, id).Scan(
		&sel.ID, &sel.UserID, &sel.SellerType, &sel.StoreName, &sel.BrandName, &sel.Slug, &sel.Description,
		&sel.LogoMediaID, &sel.BannerMediaID, &sel.Email, &sel.Phone, &sel.GSTNumber, &sel.PANNumber,
		&sel.PANEnc, &sel.PANMasked,
		&sel.State, &sel.City, &sel.PostalCode, &sel.VerificationStatus, &sel.StoreStatus,
		&sel.QualityScore, &sel.PerformanceTier, &sel.AvgRating, &sel.ReviewCount,
		&sel.FollowerCount, &sel.TotalProducts, &sel.TotalOrders, &sel.CreatedAt, &sel.UpdatedAt,
		&sel.SellScope, &sel.LegalBusinessName,
	)
	return &sel, err
}

func (s *Store) UpdateSellerStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := s.db.Exec(ctx, `UPDATE sellers SET store_status=$2, updated_at=NOW() WHERE id=$1`, id, status)
	return err
}

// ─── Seller pickup addresses ─────────────────────────────────────────

// SellerAddress is a seller's pickup, warehouse or business address.
//
// The courier needs the pickup PIN as the origin of every shipment, and
// `PrepareQuote` reads it for the delivery rate. Until now nothing in
// production ever wrote one: `POST /sellers/onboard` writes only the flat
// `state`/`city`/`postal_code` columns on `sellers`, and the onboarding wizard
// leaves `pickup_address_id` NULL. `SellerPickupPin`'s "fall back to the
// seller's own postcode" branch was therefore the ONLY live branch — and that
// column is optional, so a seller who skipped it quoted deliveries from an
// empty origin.
type SellerAddress struct {
	ID           uuid.UUID
	SellerID     uuid.UUID
	AddressType  string
	ContactName  string
	Phone        string
	AddressLine1 string
	AddressLine2 *string
	City         string
	State        string
	PostalCode   string
	Country      string
	IsDefault    bool
}

// UpsertSellerAddress writes a seller address with its identifying fields
// sealed, replacing any existing address of the same type.
//
// B4 applies here exactly as it does to customer addresses: a seller's contact
// name, phone and street are identifying PII, they live in the same estate the
// backfill covers, and the gated scrub will clear their plaintext. A write
// that stored them unsealed would be a row the scrub destroys.
//
// One address per (seller, type). A seller has one pickup point at a time, and
// letting two exist would make `SellerPickupPin`'s ORDER BY the thing that
// silently decides where couriers collect from.
func (s *Store) UpsertSellerAddress(ctx context.Context, a SellerAddress, sealed SealedAddressWrite) error {
	if sealed.KeyVersion <= 0 || len(sealed.AddressLine1) == 0 {
		return fmt.Errorf("commerce: refusing to store a seller address without sealed identifying fields")
	}
	if strings.TrimSpace(a.PostalCode) == "" {
		// The whole reason this row exists. A pickup address with no PIN
		// cannot originate a shipment, and the courier call would quote from
		// nowhere.
		return fmt.Errorf("commerce: a seller address requires a postal code")
	}
	if strings.TrimSpace(a.State) == "" {
		// GST place of supply. An empty seller state silently bills CGST+SGST
		// on an interstate sale (ErrPlaceOfSupplyUnknown).
		return fmt.Errorf("commerce: a seller address requires a state")
	}

	name, phone, line1, line2, _ := sealed.identifying(
		a.ContactName, a.Phone, a.AddressLine1, a.AddressLine2, nil)

	_, err := s.db.Exec(ctx, `
		INSERT INTO seller_addresses
		    (seller_id, address_type, contact_name, phone, address_line_1, address_line_2,
		     city, state, country, postal_code, is_default,
		     contact_name_enc, phone_enc, address_line_1_enc, address_line_2_enc, pii_key_version)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		ON CONFLICT (seller_id, address_type) DO UPDATE SET
		    contact_name       = EXCLUDED.contact_name,
		    phone              = EXCLUDED.phone,
		    address_line_1     = EXCLUDED.address_line_1,
		    address_line_2     = EXCLUDED.address_line_2,
		    city               = EXCLUDED.city,
		    state              = EXCLUDED.state,
		    country            = EXCLUDED.country,
		    postal_code        = EXCLUDED.postal_code,
		    is_default         = EXCLUDED.is_default,
		    contact_name_enc   = EXCLUDED.contact_name_enc,
		    phone_enc          = EXCLUDED.phone_enc,
		    address_line_1_enc = EXCLUDED.address_line_1_enc,
		    address_line_2_enc = EXCLUDED.address_line_2_enc,
		    pii_key_version    = EXCLUDED.pii_key_version`,
		a.SellerID, a.AddressType, name, phone, line1, line2,
		a.City, a.State, orDefaultCountry(a.Country), a.PostalCode, a.IsDefault,
		sealed.ContactName, sealed.Phone, sealed.AddressLine1, sealed.AddressLine2,
		sealed.KeyVersion,
	)
	return err
}

func orDefaultCountry(c string) string {
	if strings.TrimSpace(c) == "" {
		return "IN"
	}
	return c
}
