package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/atpost/food-service/internal/onboarding"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Restaurant onboarding writes (Wave 1 B1). Every route here takes values
// that already passed internal/onboarding, and the PAN arrives sealed: this
// file has no way to receive a plaintext PAN or account number.

var (
	ErrRestaurantNotDraft = errors.New("restaurant can only be submitted for review from DRAFT")
	ErrRestaurantNotLive  = errors.New("restaurant must be ACTIVE before it can accept orders")
	ErrFSSAIRequired      = errors.New("restaurant requires an approved, unexpired FSSAI document")
	ErrDocumentExpired    = errors.New("an expired document cannot be approved")
)

// DocumentTypeFSSAI is the one document type the licence gates accept.
const DocumentTypeFSSAI = "FSSAI"

type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func optionalRFC3339(t *time.Time) *string {
	if t == nil {
		return nil
	}
	v := rfc3339(*t)
	return &v
}

// lockOwnedRestaurantTx is THE owner check for the onboarding and payout
// routes: it locks the row only when the caller owns it, and returns
// pgx.ErrNoRows (404) otherwise, so a stranger cannot tell the restaurant
// exists.
func lockOwnedRestaurantTx(ctx context.Context, tx pgx.Tx, ownerID, restaurantID uuid.UUID) (string, error) {
	var status string
	err := tx.QueryRow(ctx, `
		SELECT status::text FROM food.restaurants
		WHERE id = $1 AND owner_user_id = $2
		FOR UPDATE
	`, restaurantID, ownerID).Scan(&status)
	return status, err
}

// hasApprovedFSSAI is the licence gate shared by approval, the status setter
// and accepting orders: an APPROVED document of type exactly FSSAI whose
// expiry is set and still ahead.
func hasApprovedFSSAI(ctx context.Context, q rowQuerier, restaurantID uuid.UUID) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM food.restaurant_documents
			WHERE restaurant_id = $1
			  AND document_type = 'FSSAI'
			  AND status = 'APPROVED'
			  AND expires_at IS NOT NULL
			  AND expires_at > NOW()
		)
	`, restaurantID).Scan(&ok)
	return ok, err
}

// insertRestaurantDocument is the only writer of food.restaurant_documents.
// A document number that looks like an Aadhaar number is refused before the
// INSERT is built.
func insertRestaurantDocument(ctx context.Context, q rowQuerier, restaurantID uuid.UUID, docType, number string, mediaID *uuid.UUID, fileURL string, expiresAt *time.Time) (uuid.UUID, error) {
	if number != "" {
		if err := onboarding.RefuseAadhaarIn("document_number", number); err != nil {
			return uuid.Nil, err
		}
	}
	var id uuid.UUID
	err := q.QueryRow(ctx, `
		INSERT INTO food.restaurant_documents (restaurant_id, document_type, document_number, media_id, file_url, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`, restaurantID, docType, emptyToNil(number), mediaID, emptyToNil(fileURL), expiresAt).Scan(&id)
	return id, err
}

type RestaurantDocument struct {
	ID              uuid.UUID  `json:"id"`
	RestaurantID    uuid.UUID  `json:"restaurant_id"`
	DocumentType    string     `json:"document_type"`
	DocumentNumber  *string    `json:"document_number"`
	MediaID         *uuid.UUID `json:"media_id"`
	Status          string     `json:"status"`
	RejectionReason *string    `json:"rejection_reason"`
	ExpiresAt       *string    `json:"expires_at"`
	VerifiedBy      *uuid.UUID `json:"verified_by"`
	VerifiedAt      *string    `json:"verified_at"`
	CreatedAt       string     `json:"created_at"`
}

func getRestaurantDocument(ctx context.Context, q rowQuerier, restaurantID, documentID uuid.UUID) (*RestaurantDocument, error) {
	var d RestaurantDocument
	var expires, verified *time.Time
	var created time.Time
	if err := q.QueryRow(ctx, `
		SELECT id, restaurant_id, document_type, document_number, media_id, status::text,
			rejection_reason, expires_at, verified_by, verified_at, created_at
		FROM food.restaurant_documents
		WHERE id = $1 AND restaurant_id = $2
	`, documentID, restaurantID).Scan(&d.ID, &d.RestaurantID, &d.DocumentType, &d.DocumentNumber, &d.MediaID,
		&d.Status, &d.RejectionReason, &expires, &d.VerifiedBy, &verified, &created); err != nil {
		return nil, err
	}
	d.ExpiresAt, d.VerifiedAt, d.CreatedAt = optionalRFC3339(expires), optionalRFC3339(verified), rfc3339(created)
	return &d, nil
}

// ─── Compliance ─────────────────────────────────────────────────────────────

// ComplianceRecord is the sealed form of a validated compliance submission.
// There is deliberately no plaintext PAN field.
type ComplianceRecord struct {
	TaxCategory                 string
	LegalName                   string
	GSTIN                       *string
	GSTINStateCode              *string
	PANSealed                   []byte
	PANKeyVersion               uint32
	PANLookup                   string
	PANMasked                   string
	PANHolderType               string
	SpecifiedPremisesDeclaredAt *time.Time
}

type RestaurantCompliance struct {
	RestaurantID                uuid.UUID `json:"restaurant_id"`
	TaxCategory                 string    `json:"tax_category"`
	LegalName                   string    `json:"legal_name"`
	GSTIN                       *string   `json:"gstin"`
	GSTINStateCode              *string   `json:"gstin_state_code"`
	PANMasked                   string    `json:"pan_masked"`
	PANHolderType               string    `json:"pan_holder_type"`
	SpecifiedPremisesDeclaredAt *string   `json:"specified_premises_declared_at"`
	ComplianceSubmittedAt       string    `json:"compliance_submitted_at"`
}

func (s *Store) SetRestaurantCompliance(ctx context.Context, ownerID, restaurantID uuid.UUID, rec ComplianceRecord) (*RestaurantCompliance, error) {
	if len(rec.PANSealed) == 0 || rec.PANKeyVersion == 0 || rec.PANLookup == "" || rec.PANMasked == "" {
		return nil, errors.New("compliance record must carry a sealed PAN")
	}
	var declared *string
	if rec.SpecifiedPremisesDeclaredAt != nil {
		v := rec.SpecifiedPremisesDeclaredAt.Format(onboarding.DateLayout)
		declared = &v
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := lockOwnedRestaurantTx(ctx, tx, ownerID, restaurantID); err != nil {
		return nil, err
	}
	var out RestaurantCompliance
	var submitted time.Time
	if err := tx.QueryRow(ctx, `
		UPDATE food.restaurants
		SET tax_category = $2, legal_name = $3, gstin = $4, gstin_state_code = $5,
			pan_sealed = $6, pan_key_version = $7, pan_lookup = $8, pan_masked = $9, pan_holder_type = $10,
			specified_premises_declared_at = $11::date, compliance_submitted_at = NOW()
		WHERE id = $1
		RETURNING id, tax_category, legal_name, gstin, gstin_state_code, pan_masked, pan_holder_type,
			specified_premises_declared_at::text, compliance_submitted_at
	`, restaurantID, rec.TaxCategory, rec.LegalName, rec.GSTIN, rec.GSTINStateCode,
		rec.PANSealed, int64(rec.PANKeyVersion), rec.PANLookup, rec.PANMasked, rec.PANHolderType, declared,
	).Scan(&out.RestaurantID, &out.TaxCategory, &out.LegalName, &out.GSTIN, &out.GSTINStateCode,
		&out.PANMasked, &out.PANHolderType, &out.SpecifiedPremisesDeclaredAt, &submitted); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	out.ComplianceSubmittedAt = rfc3339(submitted)
	return &out, nil
}

// ─── Location ───────────────────────────────────────────────────────────────

type RestaurantLocation struct {
	RestaurantID     uuid.UUID `json:"restaurant_id"`
	Latitude         float64   `json:"latitude"`
	Longitude        float64   `json:"longitude"`
	AddressLine1     string    `json:"address_line1"`
	AddressLine2     string    `json:"address_line2"`
	City             string    `json:"city"`
	State            string    `json:"state"`
	PostalCode       string    `json:"postal_code"`
	GooglePlaceID    string    `json:"google_place_id"`
	DeliveryRadiusKM float64   `json:"delivery_radius_km"`
	ServiceAreaID    uuid.UUID `json:"service_area_id"`
}

// SetRestaurantLocation writes the pin and leaves exactly one active
// restaurant_service_areas row, centred on the restaurant — the row Wave 0
// serviceability reads. Earlier areas are deactivated, not deleted.
func (s *Store) SetRestaurantLocation(ctx context.Context, ownerID, restaurantID uuid.UUID, loc onboarding.ValidatedLocation) (*RestaurantLocation, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := lockOwnedRestaurantTx(ctx, tx, ownerID, restaurantID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.restaurants
		SET latitude = $2, longitude = $3, address_line1 = $4, address_line2 = $5, city = $6,
			state = $7, postal_code = $8, google_place_id = $9
		WHERE id = $1
	`, restaurantID, loc.Latitude, loc.Longitude, loc.AddressLine1, emptyToNil(loc.AddressLine2), loc.City,
		emptyToNil(loc.State), emptyToNil(loc.PostalCode), emptyToNil(loc.GooglePlaceID)); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.restaurant_service_areas SET is_active = FALSE
		WHERE restaurant_id = $1 AND is_active = TRUE
	`, restaurantID); err != nil {
		return nil, err
	}
	out := RestaurantLocation{
		RestaurantID: restaurantID, Latitude: loc.Latitude, Longitude: loc.Longitude,
		AddressLine1: loc.AddressLine1, AddressLine2: loc.AddressLine2, City: loc.City, State: loc.State,
		PostalCode: loc.PostalCode, GooglePlaceID: loc.GooglePlaceID, DeliveryRadiusKM: loc.DeliveryRadiusKM,
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO food.restaurant_service_areas
			(restaurant_id, area_name, city, postal_code, radius_km, center_latitude, center_longitude, is_active)
		VALUES ($1, 'Delivery radius', $2, $3, $4, $5, $6, TRUE)
		RETURNING id
	`, restaurantID, loc.City, emptyToNil(loc.PostalCode), loc.DeliveryRadiusKM, loc.Latitude, loc.Longitude).Scan(&out.ServiceAreaID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &out, nil
}

// ─── Accepting orders ───────────────────────────────────────────────────────

type RestaurantAccepting struct {
	RestaurantID      uuid.UUID `json:"restaurant_id"`
	Status            string    `json:"status"`
	IsAcceptingOrders bool      `json:"is_accepting_orders"`
}

// SetRestaurantAccepting pauses freely; resuming needs an ACTIVE restaurant
// with a valid licence, so the expiry worker's pause cannot be undone by the
// partner app.
func (s *Store) SetRestaurantAccepting(ctx context.Context, ownerID, restaurantID uuid.UUID, accepting bool) (*RestaurantAccepting, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	status, err := lockOwnedRestaurantTx(ctx, tx, ownerID, restaurantID)
	if err != nil {
		return nil, err
	}
	if accepting {
		if status != "ACTIVE" {
			return nil, ErrRestaurantNotLive
		}
		ok, err := hasApprovedFSSAI(ctx, tx, restaurantID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrFSSAIRequired
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE food.restaurants SET is_accepting_orders = $2 WHERE id = $1`, restaurantID, accepting); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &RestaurantAccepting{RestaurantID: restaurantID, Status: status, IsAcceptingOrders: accepting}, nil
}

// ─── FSSAI ──────────────────────────────────────────────────────────────────

type RestaurantFSSAI struct {
	RestaurantID  uuid.UUID          `json:"restaurant_id"`
	LicenceNumber string             `json:"fssai_licence_number"`
	ExpiresAt     string             `json:"fssai_expires_at"`
	Document      RestaurantDocument `json:"document"`
}

// SubmitRestaurantFSSAI records a licence as a PENDING document of type FSSAI
// (expires_at = the expiry date at 00:00 IST) and mirrors the number and date
// on the restaurant.
func (s *Store) SubmitRestaurantFSSAI(ctx context.Context, ownerID, restaurantID uuid.UUID, v onboarding.ValidatedFSSAI) (*RestaurantFSSAI, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := lockOwnedRestaurantTx(ctx, tx, ownerID, restaurantID); err != nil {
		return nil, err
	}
	expires := v.ExpiresOn
	mediaID := v.MediaID
	docID, err := insertRestaurantDocument(ctx, tx, restaurantID, DocumentTypeFSSAI, v.LicenceNumber, &mediaID, "", &expires)
	if err != nil {
		return nil, err
	}
	expiresOn := v.ExpiresOn.Format(onboarding.DateLayout)
	if _, err := tx.Exec(ctx, `
		UPDATE food.restaurants SET fssai_licence_number = $2, fssai_expires_at = $3::date WHERE id = $1
	`, restaurantID, v.LicenceNumber, expiresOn); err != nil {
		return nil, err
	}
	doc, err := getRestaurantDocument(ctx, tx, restaurantID, docID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &RestaurantFSSAI{RestaurantID: restaurantID, LicenceNumber: v.LicenceNumber, ExpiresAt: expiresOn, Document: *doc}, nil
}

// ─── Submit for review ──────────────────────────────────────────────────────

type RestaurantSubmission struct {
	RestaurantID uuid.UUID `json:"restaurant_id"`
	Status       string    `json:"status"`
	Missing      []string  `json:"missing"`
}

func readinessFactsTx(ctx context.Context, tx pgx.Tx, restaurantID uuid.UUID) (onboarding.ReadinessFacts, error) {
	var f onboarding.ReadinessFacts
	err := tx.QueryRow(ctx, `
		SELECT
			(r.latitude IS NOT NULL AND r.longitude IS NOT NULL AND EXISTS (
				SELECT 1 FROM food.restaurant_service_areas a WHERE a.restaurant_id = r.id AND a.is_active)),
			EXISTS (SELECT 1 FROM food.restaurant_operating_hours h WHERE h.restaurant_id = r.id),
			(r.compliance_submitted_at IS NOT NULL AND r.pan_sealed IS NOT NULL),
			EXISTS (SELECT 1 FROM food.restaurant_documents d
				WHERE d.restaurant_id = r.id AND d.document_type = 'FSSAI'
				  AND d.status IN ('PENDING', 'APPROVED') AND d.expires_at > NOW()),
			EXISTS (SELECT 1 FROM food.payout_accounts p WHERE p.owner_type = 'RESTAURANT' AND p.owner_id = r.id),
			EXISTS (SELECT 1 FROM food.menu_items i WHERE i.restaurant_id = r.id AND i.is_available AND i.is_active)
		FROM food.restaurants r
		WHERE r.id = $1
	`, restaurantID).Scan(&f.HasLocation, &f.HasOperatingHours, &f.HasCompliance, &f.HasFSSAIDocument, &f.HasPayoutAccount, &f.HasAvailableMenuItem)
	return f, err
}

// SubmitRestaurantForReview moves DRAFT to PENDING_REVIEW only when every
// onboarding step exists; otherwise it returns *onboarding.NotReadyError.
func (s *Store) SubmitRestaurantForReview(ctx context.Context, ownerID, restaurantID uuid.UUID) (*RestaurantSubmission, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	status, err := lockOwnedRestaurantTx(ctx, tx, ownerID, restaurantID)
	if err != nil {
		return nil, err
	}
	if status != "DRAFT" {
		return nil, ErrRestaurantNotDraft
	}
	facts, err := readinessFactsTx(ctx, tx, restaurantID)
	if err != nil {
		return nil, err
	}
	if missing := onboarding.MissingSteps(facts); len(missing) > 0 {
		return nil, &onboarding.NotReadyError{Missing: missing}
	}
	if _, err := tx.Exec(ctx, `UPDATE food.restaurants SET status = 'PENDING_REVIEW' WHERE id = $1`, restaurantID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.restaurant_partners p SET status = 'PENDING_REVIEW'
		FROM food.restaurants r
		WHERE r.id = $1 AND p.id = r.partner_id AND p.status = 'DRAFT'
	`, restaurantID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &RestaurantSubmission{RestaurantID: restaurantID, Status: "PENDING_REVIEW", Missing: []string{}}, nil
}

// ─── Admin document decision ────────────────────────────────────────────────

// AdminDecideRestaurantDocument approves or rejects one document of one
// restaurant. An expired document cannot be approved, and an FSSAI document
// without an expiry is treated as expired.
func (s *Store) AdminDecideRestaurantDocument(ctx context.Context, adminID, restaurantID, documentID uuid.UUID, decision, reason string) (*RestaurantDocument, error) {
	d, err := onboarding.ValidateDocumentDecision(decision, reason)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var expired bool
	if err := tx.QueryRow(ctx, `
		SELECT (expires_at IS NOT NULL AND expires_at <= NOW()) OR (document_type = 'FSSAI' AND expires_at IS NULL)
		FROM food.restaurant_documents
		WHERE id = $1 AND restaurant_id = $2
		FOR UPDATE
	`, documentID, restaurantID).Scan(&expired); err != nil {
		return nil, err
	}
	if d == onboarding.DecisionApproved && expired {
		return nil, ErrDocumentExpired
	}
	var rejection any
	if d == onboarding.DecisionRejected {
		rejection = reason
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.restaurant_documents
		SET status = $2::food.document_status, verified_by = $3, verified_at = NOW(), rejection_reason = $4
		WHERE id = $1
	`, documentID, d, adminID, rejection); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO food.admin_audit_logs (actor_user_id, action, entity_type, entity_id, new_value)
		VALUES ($1, 'restaurant.document_decided', 'restaurant_document', $2,
			jsonb_build_object('restaurant_id', $3::text, 'status', $4::text, 'reason', $5::text))
	`, adminID, documentID, restaurantID.String(), d, reason); err != nil {
		return nil, err
	}
	doc, err := getRestaurantDocument(ctx, tx, restaurantID, documentID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return doc, nil
}

// ─── FSSAI expiry ───────────────────────────────────────────────────────────

// PauseRestaurantsWithExpiredFSSAI stops orders at restaurants whose approved
// FSSAI licence has lapsed with no approved, unexpired licence to replace it,
// and returns exactly the restaurants it flipped (so each is announced once).
// A restaurant that never had an FSSAI document is left alone.
func (s *Store) PauseRestaurantsWithExpiredFSSAI(ctx context.Context, limit int) ([]uuid.UUID, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
		WITH due AS (
			SELECT r.id
			FROM food.restaurants r
			WHERE r.is_accepting_orders = TRUE
			  AND EXISTS (
				SELECT 1 FROM food.restaurant_documents d
				WHERE d.restaurant_id = r.id AND d.document_type = 'FSSAI' AND d.status = 'APPROVED'
				  AND d.expires_at IS NOT NULL AND d.expires_at <= NOW())
			  AND NOT EXISTS (
				SELECT 1 FROM food.restaurant_documents d
				WHERE d.restaurant_id = r.id AND d.document_type = 'FSSAI' AND d.status = 'APPROVED'
				  AND d.expires_at IS NOT NULL AND d.expires_at > NOW())
			ORDER BY r.id
			LIMIT $1
			FOR UPDATE OF r SKIP LOCKED
		)
		UPDATE food.restaurants r
		SET is_accepting_orders = FALSE
		FROM due
		WHERE r.id = due.id
		RETURNING r.id
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
