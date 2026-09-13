package postgres

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/atpost/food-service/internal/onboarding"
	"github.com/atpost/food-service/internal/riderkyc"
	"github.com/atpost/shared/kyc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Delivery-partner verification (Wave 1 B4): DigiLocker OAuth state, KYC
// checks, sealed document numbers, the admin view and the approval gate.
//
// A document number reaches this file only sealed, lookup-hashed and masked;
// an Aadhaar document never reaches it at all (only a check with an opaque
// reference and a hashed document-type label).

var (
	ErrDigiLockerStateNotFound = errors.New("the DigiLocker state is not recognised")
	ErrDigiLockerStateUsed     = errors.New("the DigiLocker state has already been used")
	ErrDigiLockerStateExpired  = errors.New("the DigiLocker state has expired")
	ErrDigiLockerStateNotYours = errors.New("the DigiLocker state belongs to another delivery partner")
	ErrDocumentNumberInUse     = errors.New("this document number is already registered to another delivery partner")

	errDeliveryDocumentNotSealed = errors.New("delivery document record must arrive sealed, lookup-hashed where required, and masked")
	errKYCCheckInvalid           = errors.New("kyc check record must carry a kind, an opaque reference and a hashed document type")

	twelveDigits = regexp.MustCompile(`[0-9]{12}`)
	sha256Hex    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

const (
	KYCProviderDigiLocker = "DIGILOCKER"

	KYCKindAadhaar        = "AADHAAR"
	KYCKindDrivingLicence = "DRIVING_LICENCE"
	KYCKindVehicleRC      = "VEHICLE_RC"

	DocumentStatusPending  = "PENDING"
	DocumentStatusApproved = "APPROVED"
)

// ─── DigiLocker OAuth state ─────────────────────────────────────────────────

// DigiLockerAuthState is a consumed state: whose it was and the sealed verifier.
type DigiLockerAuthState struct {
	PartnerID      uuid.UUID
	VerifierSealed []byte
}

// CreateDigiLockerAuthState stores a state hash and sealed verifier for the
// caller's own delivery-partner profile. No profile is pgx.ErrNoRows.
func (s *Store) CreateDigiLockerAuthState(ctx context.Context, userID uuid.UUID, stateHash string, verifierSealed []byte, keyVersion uint32, expiresAt time.Time) (uuid.UUID, error) {
	if !sha256Hex.MatchString(stateHash) || len(verifierSealed) == 0 || keyVersion == 0 {
		return uuid.Nil, errors.New("digilocker state must arrive hashed, with a sealed verifier")
	}
	var partnerID uuid.UUID
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.digilocker_auth_states (state_hash, partner_id, code_verifier_sealed, key_version, expires_at)
		SELECT $2, p.id, $3, $4, $5
		FROM food.delivery_partners p
		WHERE p.user_id = $1
		RETURNING partner_id
	`, userID, stateHash, verifierSealed, int64(keyVersion), expiresAt).Scan(&partnerID); err != nil {
		return uuid.Nil, err
	}
	// Housekeeping: a partner's states a day past expiry are of no further use.
	if _, err := s.db.Exec(ctx, `
		DELETE FROM food.digilocker_auth_states
		WHERE partner_id = $1 AND expires_at < NOW() - INTERVAL '1 day'
	`, partnerID); err != nil {
		return uuid.Nil, err
	}
	return partnerID, nil
}

// ConsumeDigiLockerAuthState marks the state used exactly once. The single
// conditional UPDATE is the guard: the state must exist, belong to a partner
// profile owned by userID, be unconsumed, and be unexpired. Only when it
// matches nothing does a read work out which refusal to give; a stranger's
// attempt does not consume the owner's state.
func (s *Store) ConsumeDigiLockerAuthState(ctx context.Context, userID uuid.UUID, stateHash string) (*DigiLockerAuthState, error) {
	var out DigiLockerAuthState
	err := s.db.QueryRow(ctx, `
		UPDATE food.digilocker_auth_states st
		SET consumed_at = NOW()
		FROM food.delivery_partners p
		WHERE st.state_hash = $1
		  AND p.id = st.partner_id
		  AND p.user_id = $2
		  AND st.consumed_at IS NULL
		  AND st.expires_at > NOW()
		RETURNING st.partner_id, st.code_verifier_sealed
	`, stateHash, userID).Scan(&out.PartnerID, &out.VerifierSealed)
	if err == nil {
		return &out, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	var owner, used, expired bool
	derr := s.db.QueryRow(ctx, `
		SELECT p.user_id = $2, st.consumed_at IS NOT NULL, st.expires_at <= NOW()
		FROM food.digilocker_auth_states st
		JOIN food.delivery_partners p ON p.id = st.partner_id
		WHERE st.state_hash = $1
	`, stateHash, userID).Scan(&owner, &used, &expired)
	switch {
	case errors.Is(derr, pgx.ErrNoRows):
		return nil, ErrDigiLockerStateNotFound
	case derr != nil:
		return nil, derr
	case !owner:
		return nil, ErrDigiLockerStateNotYours
	case used:
		return nil, ErrDigiLockerStateUsed
	case expired:
		return nil, ErrDigiLockerStateExpired
	}
	// Consumed by a concurrent callback between the UPDATE and the read.
	return nil, ErrDigiLockerStateUsed
}

// ─── Documents ──────────────────────────────────────────────────────────────

// DeliveryDocumentRecord is the only shape a delivery-partner document is
// written in. There is deliberately no plaintext number field.
type DeliveryDocumentRecord struct {
	DocumentType     string
	NumberSealed     []byte
	NumberKeyVersion uint32
	NumberLookup     string
	NumberMasked     string
	MediaID          *uuid.UUID
	FileURL          string
	// Status is PENDING for an upload, APPROVED for a DigiLocker-issued document.
	Status    string
	ExpiresAt *time.Time
}

func (r DeliveryDocumentRecord) validate() error {
	if r.DocumentType == "" || (r.Status != DocumentStatusPending && r.Status != DocumentStatusApproved) {
		return errDeliveryDocumentNotSealed
	}
	hasNumber := len(r.NumberSealed) > 0
	if hasNumber != (r.NumberKeyVersion > 0) || hasNumber != (r.NumberMasked != "") || (r.NumberLookup != "" && !hasNumber) {
		return errDeliveryDocumentNotSealed
	}
	// A mask is "****" plus at most four characters; anything longer is a
	// number that was never masked.
	if hasNumber && (!strings.HasPrefix(r.NumberMasked, "****") || len(r.NumberMasked) > 8) {
		return errDeliveryDocumentNotSealed
	}
	switch r.DocumentType {
	case riderkyc.DocumentTypeDrivingLicence, riderkyc.DocumentTypeVehicleRC:
		if !hasNumber || r.NumberLookup == "" {
			return errDeliveryDocumentNotSealed
		}
	case riderkyc.DocumentTypeSelfie:
		if hasNumber || r.MediaID == nil {
			return errDeliveryDocumentNotSealed
		}
	case riderkyc.DocumentTypeAadhaar:
		return errDeliveryDocumentNotSealed
	}
	return nil
}

// DeliveryDocument is the only shape a delivery-partner document leaves the
// store in: masked number, never sealed bytes or lookup hash.
type DeliveryDocument struct {
	ID                uuid.UUID  `json:"id"`
	DeliveryPartnerID uuid.UUID  `json:"delivery_partner_id"`
	DocumentType      string     `json:"document_type"`
	NumberMasked      *string    `json:"number_masked"`
	MediaID           *uuid.UUID `json:"media_id"`
	Status            string     `json:"status"`
	RejectionReason   *string    `json:"rejection_reason"`
	ExpiresAt         *string    `json:"expires_at"`
	VerifiedAt        *string    `json:"verified_at"`
	CreatedAt         string     `json:"created_at"`
}

const deliveryDocumentColumns = `id, delivery_partner_id, document_type, number_masked, media_id, status::text,
	rejection_reason, expires_at, verified_at, created_at`

func scanDeliveryDocument(row pgx.Row) (*DeliveryDocument, error) {
	var d DeliveryDocument
	var expires, verified *time.Time
	var created time.Time
	if err := row.Scan(&d.ID, &d.DeliveryPartnerID, &d.DocumentType, &d.NumberMasked, &d.MediaID, &d.Status,
		&d.RejectionReason, &expires, &verified, &created); err != nil {
		return nil, err
	}
	d.ExpiresAt, d.VerifiedAt, d.CreatedAt = optionalRFC3339(expires), optionalRFC3339(verified), rfc3339(created)
	return &d, nil
}

// upsertDeliveryDocumentTx is the only writer of a delivery-partner document.
//
// A DL or RC carries a lookup hash, and (document_type, number_lookup) is
// unique, so one licence or registration cannot back two partners. The same
// partner re-submitting their own number updates their row; another partner's
// conflicting row is left untouched and the write is refused
// (ErrDocumentNumberInUse). A DigiLocker document (APPROVED) approves the
// row; an upload never downgrades an approved row.
func upsertDeliveryDocumentTx(ctx context.Context, tx pgx.Tx, partnerID uuid.UUID, r DeliveryDocumentRecord) (*DeliveryDocument, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	var sealed any
	if len(r.NumberSealed) > 0 {
		sealed = r.NumberSealed
	}
	var keyVersion any
	if r.NumberKeyVersion > 0 {
		keyVersion = int64(r.NumberKeyVersion)
	}
	args := []any{partnerID, r.DocumentType, sealed, keyVersion, emptyToNil(r.NumberLookup), emptyToNil(r.NumberMasked),
		r.MediaID, emptyToNil(r.FileURL), r.Status, r.ExpiresAt}
	const insert = `
		INSERT INTO food.delivery_partner_documents AS d (
			delivery_partner_id, document_type, number_sealed, number_key_version, number_lookup, number_masked,
			media_id, file_url, status, expires_at, verified_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::food.document_status, $10,
			CASE WHEN $9 = 'APPROVED' THEN NOW() END)`
	if r.NumberLookup == "" {
		return scanDeliveryDocument(tx.QueryRow(ctx, insert+` RETURNING `+deliveryDocumentColumns, args...))
	}
	doc, err := scanDeliveryDocument(tx.QueryRow(ctx, insert+`
		ON CONFLICT (document_type, number_lookup) WHERE number_lookup IS NOT NULL DO UPDATE SET
			number_sealed = EXCLUDED.number_sealed,
			number_key_version = EXCLUDED.number_key_version,
			number_masked = EXCLUDED.number_masked,
			media_id = COALESCE(EXCLUDED.media_id, d.media_id),
			file_url = COALESCE(EXCLUDED.file_url, d.file_url),
			expires_at = COALESCE(EXCLUDED.expires_at, d.expires_at),
			status = CASE WHEN EXCLUDED.status = 'APPROVED' OR d.status = 'APPROVED'
				THEN 'APPROVED'::food.document_status ELSE 'PENDING'::food.document_status END,
			rejection_reason = NULL,
			verified_at = CASE WHEN EXCLUDED.status = 'APPROVED' THEN NOW() ELSE d.verified_at END
		WHERE d.delivery_partner_id = EXCLUDED.delivery_partner_id
		RETURNING `+deliveryDocumentColumns, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrDocumentNumberInUse
	}
	return doc, err
}

// AddDeliveryPartnerDocument records an upload against the caller's own
// profile. A new SELFIE retires the partner's previous active selfie, so a
// partner has at most one.
func (s *Store) AddDeliveryPartnerDocument(ctx context.Context, userID uuid.UUID, r DeliveryDocumentRecord) (*DeliveryDocument, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var partnerID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM food.delivery_partners WHERE user_id = $1 FOR UPDATE`, userID).Scan(&partnerID); err != nil {
		return nil, err
	}
	if r.DocumentType == riderkyc.DocumentTypeSelfie {
		if _, err := tx.Exec(ctx, `
			UPDATE food.delivery_partner_documents SET status = 'EXPIRED'
			WHERE delivery_partner_id = $1 AND document_type = 'SELFIE' AND status IN ('PENDING', 'APPROVED')
		`, partnerID); err != nil {
			return nil, err
		}
	}
	doc, err := upsertDeliveryDocumentTx(ctx, tx, partnerID, r)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return doc, nil
}

// ─── DigiLocker verification ────────────────────────────────────────────────

// KYCCheckRecord is one provider assertion. Aadhaar is recorded ONLY this way.
type KYCCheckRecord struct {
	Kind                 string
	AssertionRef         string
	DocTypeHash          string
	NameOnDocumentMasked string
	ValidUntil           *time.Time
}

func (r KYCCheckRecord) validate() error {
	switch r.Kind {
	case KYCKindAadhaar, KYCKindDrivingLicence, KYCKindVehicleRC:
	default:
		return errKYCCheckInvalid
	}
	ref := strings.TrimSpace(r.AssertionRef)
	if ref == "" || len(ref) > 200 || twelveDigits.MatchString(ref) || kyc.LooksLikeAadhaar(ref) || !sha256Hex.MatchString(r.DocTypeHash) {
		return errKYCCheckInvalid
	}
	if strings.ContainsAny(r.NameOnDocumentMasked, "0123456789") {
		return errKYCCheckInvalid
	}
	return nil
}

// DigiLockerVerification is everything one callback persists, atomically.
type DigiLockerVerification struct {
	Checks    []KYCCheckRecord
	Documents []DeliveryDocumentRecord
}

// RecordDigiLockerVerification writes the checks and the issued DL and RC in
// one transaction: a duplicate licence refuses the whole callback, so no
// check is left behind for a document that was not stored.
func (s *Store) RecordDigiLockerVerification(ctx context.Context, partnerID uuid.UUID, v DigiLockerVerification) error {
	for _, c := range v.Checks {
		if err := c.validate(); err != nil {
			return err
		}
	}
	for _, d := range v.Documents {
		if err := d.validate(); err != nil {
			return err
		}
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var locked uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM food.delivery_partners WHERE id = $1 FOR UPDATE`, partnerID).Scan(&locked); err != nil {
		return err
	}
	for _, c := range v.Checks {
		if _, err := tx.Exec(ctx, `
			INSERT INTO food.delivery_partner_kyc_checks
				(partner_id, provider, kind, assertion_ref, doc_type_hash, name_on_document_masked, valid_until, verified_at)
			VALUES ($1, 'DIGILOCKER', $2, $3, $4, $5, $6::date, NOW())
		`, partnerID, c.Kind, strings.TrimSpace(c.AssertionRef), c.DocTypeHash, emptyToNil(c.NameOnDocumentMasked), dateOnly(c.ValidUntil)); err != nil {
			return err
		}
	}
	for _, d := range v.Documents {
		if _, err := upsertDeliveryDocumentTx(ctx, tx, partnerID, d); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func dateOnly(t *time.Time) *string {
	if t == nil {
		return nil
	}
	v := t.Format(onboarding.DateLayout)
	return &v
}

// ─── Readiness, the KYC view and the approval gate ─────────────────────────

// deliveryPartnerFacts reads what riderkyc.MissingSteps needs. A DL or RC
// counts only with a DigiLocker check still in validity (IST calendar) AND an
// APPROVED, unexpired document of that type, so an admin's rejection of the
// document takes it back out.
func deliveryPartnerFacts(ctx context.Context, q rowQuerier, partnerID uuid.UUID) (string, riderkyc.Facts, error) {
	var status string
	var f riderkyc.Facts
	err := q.QueryRow(ctx, `
		SELECT p.status::text, COALESCE(p.vehicle_type, ''),
			EXISTS (SELECT 1 FROM food.delivery_partner_kyc_checks k
				WHERE k.partner_id = p.id AND k.provider = 'DIGILOCKER' AND k.kind = 'AADHAAR'),
			EXISTS (SELECT 1 FROM food.delivery_partner_kyc_checks k
				WHERE k.partner_id = p.id AND k.provider = 'DIGILOCKER' AND k.kind = 'DRIVING_LICENCE'
				  AND k.valid_until >= (NOW() AT TIME ZONE 'Asia/Kolkata')::date)
			AND EXISTS (SELECT 1 FROM food.delivery_partner_documents d
				WHERE d.delivery_partner_id = p.id AND d.document_type = 'DRIVING_LICENCE'
				  AND d.status = 'APPROVED' AND d.expires_at > NOW()),
			EXISTS (SELECT 1 FROM food.delivery_partner_kyc_checks k
				WHERE k.partner_id = p.id AND k.provider = 'DIGILOCKER' AND k.kind = 'VEHICLE_RC'
				  AND k.valid_until >= (NOW() AT TIME ZONE 'Asia/Kolkata')::date)
			AND EXISTS (SELECT 1 FROM food.delivery_partner_documents d
				WHERE d.delivery_partner_id = p.id AND d.document_type = 'VEHICLE_RC'
				  AND d.status = 'APPROVED' AND d.expires_at > NOW()),
			EXISTS (SELECT 1 FROM food.delivery_partner_documents d
				WHERE d.delivery_partner_id = p.id AND d.document_type = 'SELFIE' AND d.status = 'APPROVED'),
			EXISTS (SELECT 1 FROM food.payout_accounts a
				WHERE a.owner_type = 'DELIVERY_PARTNER' AND a.owner_id = p.id)
		FROM food.delivery_partners p
		WHERE p.id = $1
	`, partnerID).Scan(&status, &f.VehicleType, &f.HasAadhaarCheck, &f.HasValidDrivingLicence, &f.HasValidVehicleRC,
		&f.HasApprovedSelfie, &f.HasPayoutAccount)
	return status, f, err
}

// requireDeliveryPartnerReadyTx locks the partner and refuses with
// *riderkyc.NotReadyError while any step is missing.
func requireDeliveryPartnerReadyTx(ctx context.Context, tx pgx.Tx, partnerID uuid.UUID) error {
	var locked uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM food.delivery_partners WHERE id = $1 FOR UPDATE`, partnerID).Scan(&locked); err != nil {
		return err
	}
	_, facts, err := deliveryPartnerFacts(ctx, tx, partnerID)
	if err != nil {
		return err
	}
	if missing := riderkyc.MissingSteps(facts); len(missing) > 0 {
		return &riderkyc.NotReadyError{Missing: missing}
	}
	return nil
}

// KYCCheck is a check as the rider app and admin see it: kind, validity and
// when; never the assertion reference or the document-type hash.
type KYCCheck struct {
	Kind                 string  `json:"kind"`
	Provider             string  `json:"provider"`
	NameOnDocumentMasked *string `json:"name_on_document_masked"`
	ValidUntil           *string `json:"valid_until"`
	Valid                bool    `json:"valid"`
	VerifiedAt           string  `json:"verified_at"`
}

// DeliveryPartnerKYC is the verification view shared by the rider's own
// status route and the admin route. Numbers are masked.
type DeliveryPartnerKYC struct {
	PartnerID                uuid.UUID          `json:"partner_id"`
	Status                   string             `json:"status"`
	VehicleType              string             `json:"vehicle_type"`
	DrivingDocumentsRequired bool               `json:"driving_documents_required"`
	Missing                  []string           `json:"missing"`
	Checks                   []KYCCheck         `json:"checks"`
	Documents                []DeliveryDocument `json:"documents"`
	HasPayoutAccount         bool               `json:"has_payout_account"`
}

// DeliveryPartnerKYCForUser is the caller's own view.
func (s *Store) DeliveryPartnerKYCForUser(ctx context.Context, userID uuid.UUID) (*DeliveryPartnerKYC, error) {
	var partnerID uuid.UUID
	if err := s.db.QueryRow(ctx, `SELECT id FROM food.delivery_partners WHERE user_id = $1`, userID).Scan(&partnerID); err != nil {
		return nil, err
	}
	return s.AdminDeliveryPartnerKYC(ctx, partnerID)
}

// AdminDeliveryPartnerKYC is the view for one partner by id.
func (s *Store) AdminDeliveryPartnerKYC(ctx context.Context, partnerID uuid.UUID) (*DeliveryPartnerKYC, error) {
	status, facts, err := deliveryPartnerFacts(ctx, s.db, partnerID)
	if err != nil {
		return nil, err
	}
	out := &DeliveryPartnerKYC{
		PartnerID: partnerID, Status: status, VehicleType: facts.VehicleType,
		DrivingDocumentsRequired: riderkyc.DrivingDocumentsRequired(facts.VehicleType),
		Missing:                  riderkyc.MissingSteps(facts), Checks: []KYCCheck{}, Documents: []DeliveryDocument{},
		HasPayoutAccount: facts.HasPayoutAccount,
	}
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT ON (kind) kind, provider, name_on_document_masked, valid_until::text,
			(kind = 'AADHAAR' OR valid_until >= (NOW() AT TIME ZONE 'Asia/Kolkata')::date),
			verified_at
		FROM food.delivery_partner_kyc_checks
		WHERE partner_id = $1
		ORDER BY kind, verified_at DESC
	`, partnerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c KYCCheck
		var valid *bool
		var verified time.Time
		if err := rows.Scan(&c.Kind, &c.Provider, &c.NameOnDocumentMasked, &c.ValidUntil, &valid, &verified); err != nil {
			return nil, err
		}
		c.Valid, c.VerifiedAt = valid != nil && *valid, rfc3339(verified)
		out.Checks = append(out.Checks, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	docs, err := s.db.Query(ctx, `
		SELECT `+deliveryDocumentColumns+`
		FROM food.delivery_partner_documents
		WHERE delivery_partner_id = $1 AND status <> 'EXPIRED'
		ORDER BY created_at DESC, id
		LIMIT 50
	`, partnerID)
	if err != nil {
		return nil, err
	}
	defer docs.Close()
	for docs.Next() {
		d, err := scanDeliveryDocument(docs)
		if err != nil {
			return nil, err
		}
		out.Documents = append(out.Documents, *d)
	}
	return out, docs.Err()
}

// AdminDecideDeliveryPartnerDocument approves or rejects one document of one
// partner. An expired or superseded document cannot be approved.
func (s *Store) AdminDecideDeliveryPartnerDocument(ctx context.Context, adminID, partnerID, documentID uuid.UUID, decision, reason string) (*DeliveryDocument, error) {
	d, err := onboarding.ValidateDocumentDecision(decision, reason)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var unusable bool
	if err := tx.QueryRow(ctx, `
		SELECT (expires_at IS NOT NULL AND expires_at <= NOW()) OR status = 'EXPIRED'
		FROM food.delivery_partner_documents
		WHERE id = $1 AND delivery_partner_id = $2
		FOR UPDATE
	`, documentID, partnerID).Scan(&unusable); err != nil {
		return nil, err
	}
	if d == onboarding.DecisionApproved && unusable {
		return nil, ErrDocumentExpired
	}
	var rejection any
	if d == onboarding.DecisionRejected {
		rejection = strings.TrimSpace(reason)
	}
	doc, err := scanDeliveryDocument(tx.QueryRow(ctx, `
		UPDATE food.delivery_partner_documents
		SET status = $2::food.document_status, verified_by = $3, verified_at = NOW(), rejection_reason = $4
		WHERE id = $1
		RETURNING `+deliveryDocumentColumns, documentID, d, adminID, rejection))
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO food.admin_audit_logs (actor_user_id, action, entity_type, entity_id, new_value)
		VALUES ($1, 'delivery_partner.document_decided', 'delivery_partner_document', $2,
			jsonb_build_object('delivery_partner_id', $3::text, 'document_type', $4::text, 'status', $5::text, 'reason', $6::text))
	`, adminID, documentID, partnerID.String(), doc.DocumentType, d, strings.TrimSpace(reason)); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return doc, nil
}

// ─── Backfill of pre-B4 plaintext numbers ───────────────────────────────────

// SealedDocumentNumber is what the backfill's sealer returns for one number.
type SealedDocumentNumber struct {
	Blob       []byte
	KeyVersion uint32
	Lookup     string
	Masked     string
}

// DocumentNumberSealer seals one legacy number; the service supplies it.
type DocumentNumberSealer func(ctx context.Context, documentType, number string) (SealedDocumentNumber, error)

// DocumentBackfillResult counts what one pass did. Nothing here carries a value.
type DocumentBackfillResult struct {
	Sealed int `json:"sealed"`
	// AadhaarRefused rows hold an Aadhaar-shaped number. They are neither
	// sealed nor cleared: deleting the value is an ops decision.
	AadhaarRefused int `json:"aadhaar_refused"`
	// Conflicts rows share a DL or RC with another row; they keep their
	// plaintext until ops resolves the duplicate.
	Conflicts int `json:"conflicts"`
	// Failed rows could not be sealed (for example keys unavailable).
	Failed int `json:"failed"`
}

// BackfillDeliveryDocumentNumbers seals every delivery-partner document that
// still holds a plaintext number, then clears the plaintext IN THE SAME
// UPDATE, guarded on the plaintext being unchanged. A row it could not seal
// keeps its plaintext. Idempotent: a second pass finds nothing to seal.
func (s *Store) BackfillDeliveryDocumentNumbers(ctx context.Context, seal DocumentNumberSealer, batch int) (DocumentBackfillResult, error) {
	var res DocumentBackfillResult
	if seal == nil {
		return res, errors.New("backfill needs a sealer")
	}
	if batch <= 0 || batch > 500 {
		batch = 100
	}
	after := uuid.Nil
	for {
		type legacy struct {
			id             uuid.UUID
			docType, value string
		}
		rows, err := s.db.Query(ctx, `
			SELECT id, document_type, document_number
			FROM food.delivery_partner_documents
			WHERE document_number IS NOT NULL AND id > $1
			ORDER BY id
			LIMIT $2
		`, after, batch)
		if err != nil {
			return res, err
		}
		var page []legacy
		for rows.Next() {
			var l legacy
			if err := rows.Scan(&l.id, &l.docType, &l.value); err != nil {
				rows.Close()
				return res, err
			}
			page = append(page, l)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return res, err
		}
		if len(page) == 0 {
			return res, nil
		}
		for _, l := range page {
			after = l.id
			if kyc.LooksLikeAadhaar(l.value) {
				res.AadhaarRefused++
				continue
			}
			sealed, err := seal(ctx, l.docType, l.value)
			if err != nil || len(sealed.Blob) == 0 || sealed.KeyVersion == 0 || !strings.HasPrefix(sealed.Masked, "****") || len(sealed.Masked) > 8 {
				res.Failed++
				continue
			}
			tag, err := s.db.Exec(ctx, `
				UPDATE food.delivery_partner_documents
				SET number_sealed = $2, number_key_version = $3, number_lookup = $4, number_masked = $5,
					document_number = NULL
				WHERE id = $1 AND document_number = $6
			`, l.id, sealed.Blob, int64(sealed.KeyVersion), emptyToNil(sealed.Lookup), sealed.Masked, l.value)
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				res.Conflicts++
				continue
			}
			if err != nil {
				return res, fmt.Errorf("backfill delivery document: %w", err)
			}
			if tag.RowsAffected() == 1 {
				res.Sealed++
			}
		}
	}
}
