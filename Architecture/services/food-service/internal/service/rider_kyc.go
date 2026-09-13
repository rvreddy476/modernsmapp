package service

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/atpost/food-service/internal/digilocker"
	"github.com/atpost/food-service/internal/foodpii"
	"github.com/atpost/food-service/internal/onboarding"
	"github.com/atpost/food-service/internal/riderkyc"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/kyc"
	"github.com/atpost/shared/pii"
	"github.com/google/uuid"
)

// Delivery-partner verification (Wave 1 B4).
//
// DigiLocker with real PKCE: start stores a hashed state and a sealed
// verifier; the callback consumes the state once, exchanges the code with the
// verifier, and persists an Aadhaar reference plus sealed DL and RC numbers.
// Nothing on this path stores, logs or returns an Aadhaar number, a state, a
// verifier, a code or a plaintext DL/RC.

var (
	// ErrDigiLockerNotConfigured maps to 503 FOOD_DIGILOCKER_NOT_CONFIGURED.
	ErrDigiLockerNotConfigured = errors.New("DigiLocker verification is not configured")
	// ErrDigiLockerProvider maps to 502 FOOD_DIGILOCKER_PROVIDER_FAILED. The
	// state is already consumed; the partner starts again.
	ErrDigiLockerProvider = errors.New("DigiLocker could not complete the verification; start again")
)

const (
	digiLockerStateTTL = 10 * time.Minute
	maxOAuthParam      = 512

	CodeDigiLockerCodeRequired = "FOOD_DIGILOCKER_CODE_REQUIRED"
)

// WithDigiLocker attaches the DigiLocker settings; a nil client keeps the
// routes at 503.
func (s *Service) WithDigiLocker(settings digilocker.Settings) *Service {
	s.digilocker = settings
	return s
}

// DigiLockerStart is the start response.
type DigiLockerStart struct {
	AuthorizeURL string `json:"authorize_url"`
	State        string `json:"state"`
	ExpiresAt    string `json:"expires_at"`
}

// StartDigiLocker begins verification for the caller's own profile.
func (s *Service) StartDigiLocker(ctx context.Context, userID uuid.UUID) (*DigiLockerStart, error) {
	dl := s.digilocker
	if dl.Client == nil || dl.PublicBaseURL == "" {
		return nil, ErrDigiLockerNotConfigured
	}
	if !s.pii.Configured() {
		return nil, ErrPIINotConfigured
	}
	state, err := digilocker.NewState()
	if err != nil {
		return nil, err
	}
	verifier, err := digilocker.NewCodeVerifier()
	if err != nil {
		return nil, err
	}
	sealed, version, err := s.pii.SealCodeVerifier(ctx, verifier)
	if err != nil {
		return nil, err
	}
	expires := s.onboardingClock().Add(digiLockerStateTTL).UTC()
	if _, err := s.store.CreateDigiLockerAuthState(ctx, userID, digilocker.HashState(state), sealed, version, expires); err != nil {
		return nil, err
	}
	authorize, err := authorizeURL(dl, state, digilocker.CodeChallengeS256(verifier))
	if err != nil {
		return nil, err
	}
	return &DigiLockerStart{AuthorizeURL: authorize, State: state, ExpiresAt: expires.Format(time.RFC3339)}, nil
}

// authorizeURL points the Custom Tab at DigiLocker, or in local/dev mock mode
// at the dev-only authorize route.
func authorizeURL(dl digilocker.Settings, state, challenge string) (string, error) {
	if dl.Mock() {
		return dl.PublicBaseURL + digilocker.DevAuthorizePath + "?" + url.Values{"state": {state}}.Encode(), nil
	}
	u, err := url.Parse(dl.AuthorizeURL)
	if err != nil || u.Host == "" {
		return "", ErrDigiLockerNotConfigured
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", dl.ClientID)
	q.Set("redirect_uri", dl.RedirectURI())
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// CompleteDigiLocker is the callback: the caller must own the partner the
// state was issued to. It returns the caller's verification view.
func (s *Service) CompleteDigiLocker(ctx context.Context, userID uuid.UUID, code, state string) (*postgres.DeliveryPartnerKYC, error) {
	dl := s.digilocker
	if dl.Client == nil {
		return nil, ErrDigiLockerNotConfigured
	}
	if !s.pii.Configured() {
		return nil, ErrPIINotConfigured
	}
	code = strings.TrimSpace(code)
	if code == "" || len(code) > maxOAuthParam {
		return nil, &onboarding.FieldError{Code: CodeDigiLockerCodeRequired, Field: "code", Message: "code is required"}
	}
	if !digilocker.ValidState(state) {
		return nil, postgres.ErrDigiLockerStateNotFound
	}
	st, err := s.store.ConsumeDigiLockerAuthState(ctx, userID, digilocker.HashState(state))
	if err != nil {
		return nil, err
	}
	verifier, err := s.pii.OpenCodeVerifier(ctx, st.VerifierSealed)
	if err != nil {
		return nil, err
	}
	session, err := dl.Client.ExchangeCode(ctx, code, verifier)
	if err != nil {
		slog.WarnContext(ctx, "food-service: digilocker code exchange failed", "partner_id", st.PartnerID, "provider_error", errors.Is(err, digilocker.ErrProvider))
		return nil, ErrDigiLockerProvider
	}
	docs, err := dl.Client.IssuedDocuments(ctx, session)
	if err != nil {
		slog.WarnContext(ctx, "food-service: digilocker issued documents failed", "partner_id", st.PartnerID, "provider_error", errors.Is(err, digilocker.ErrProvider))
		return nil, ErrDigiLockerProvider
	}
	record, err := s.digiLockerRecord(ctx, st.PartnerID, docs)
	if err != nil {
		return nil, err
	}
	if err := s.store.RecordDigiLockerVerification(ctx, st.PartnerID, record); err != nil {
		return nil, err
	}
	return s.store.DeliveryPartnerKYCForUser(ctx, userID)
}

// digiLockerRecord turns issued documents into what may be stored. Aadhaar
// becomes a check with its reference and a hashed type label, never a
// number. Any Aadhaar-shaped value anywhere refuses the whole callback. A DL
// or RC that fails shared/kyc is skipped (the checklist keeps asking for it).
func (s *Service) digiLockerRecord(ctx context.Context, partnerID uuid.UUID, docs []digilocker.IssuedDocument) (postgres.DigiLockerVerification, error) {
	var v postgres.DigiLockerVerification
	for _, d := range docs {
		ref := strings.TrimSpace(d.Reference)
		if kyc.LooksLikeAadhaar(ref) || kyc.LooksLikeAadhaar(d.NameOnDocument) || kyc.RefuseAadhaar(d.Number) != nil {
			slog.ErrorContext(ctx, "food-service: digilocker returned an Aadhaar-shaped value; nothing stored", "partner_id", partnerID, "kind", string(d.Kind))
			return postgres.DigiLockerVerification{}, ErrDigiLockerProvider
		}
		if ref == "" {
			slog.WarnContext(ctx, "food-service: digilocker document without a reference skipped", "partner_id", partnerID, "kind", string(d.Kind))
			continue
		}
		docTypeHash := digilocker.HashDocumentType(d.DocType)
		name := riderkyc.MaskName(d.NameOnDocument)
		switch d.Kind {
		case digilocker.KindAadhaar:
			if d.Number != "" {
				slog.ErrorContext(ctx, "food-service: digilocker returned a number on an Aadhaar document; nothing stored", "partner_id", partnerID)
				return postgres.DigiLockerVerification{}, ErrDigiLockerProvider
			}
			v.Checks = append(v.Checks, postgres.KYCCheckRecord{Kind: postgres.KYCKindAadhaar, AssertionRef: ref, DocTypeHash: docTypeHash, NameOnDocumentMasked: name})
		case digilocker.KindDrivingLicence, digilocker.KindVehicleRC:
			docType, kind, normalized, sealed, err := s.sealIssuedNumber(ctx, d)
			if err != nil {
				if errors.Is(err, foodpii.ErrNotConfigured) {
					return postgres.DigiLockerVerification{}, ErrPIINotConfigured
				}
				slog.WarnContext(ctx, "food-service: digilocker document skipped", "partner_id", partnerID, "kind", string(d.Kind), "reason", err.Error())
				continue
			}
			if compact, _ := pii.CompactUpper(ref); strings.Contains(compact, normalized) {
				slog.ErrorContext(ctx, "food-service: digilocker reference embeds the document number; nothing stored", "partner_id", partnerID, "kind", string(d.Kind))
				return postgres.DigiLockerVerification{}, ErrDigiLockerProvider
			}
			v.Checks = append(v.Checks, postgres.KYCCheckRecord{Kind: kind, AssertionRef: ref, DocTypeHash: docTypeHash, NameOnDocumentMasked: name, ValidUntil: d.ValidUntil})
			v.Documents = append(v.Documents, postgres.DeliveryDocumentRecord{
				DocumentType: docType, NumberSealed: sealed.Blob, NumberKeyVersion: sealed.KeyVersion, NumberLookup: sealed.Lookup,
				NumberMasked: pii.MaskTail(normalized, 4), Status: postgres.DocumentStatusApproved, ExpiresAt: expiryInstant(d.ValidUntil),
			})
		}
	}
	return v, nil
}

var errIssuedNumberMalformed = errors.New("issued number fails shared/kyc")

func (s *Service) sealIssuedNumber(ctx context.Context, d digilocker.IssuedDocument) (docType, kind, normalized string, sealed foodpii.Sealed, err error) {
	if d.Kind == digilocker.KindDrivingLicence {
		dl, verr := kyc.ValidateDrivingLicence(d.Number)
		if verr != nil {
			return "", "", "", foodpii.Sealed{}, errIssuedNumberMalformed
		}
		sealed, err = s.pii.SealDrivingLicence(ctx, dl.Normalized)
		return riderkyc.DocumentTypeDrivingLicence, postgres.KYCKindDrivingLicence, dl.Normalized, sealed, err
	}
	rc, verr := kyc.ValidateVehicleRegistration(d.Number)
	if verr != nil {
		return "", "", "", foodpii.Sealed{}, errIssuedNumberMalformed
	}
	sealed, err = s.pii.SealVehicleRegistration(ctx, rc.Normalized)
	return riderkyc.DocumentTypeVehicleRC, postgres.KYCKindVehicleRC, rc.Normalized, sealed, err
}

// expiryInstant: a document valid until a calendar date (IST) expires at the
// start of the next day.
func expiryInstant(validUntil *time.Time) *time.Time {
	if validUntil == nil {
		return nil
	}
	y, m, d := validUntil.Date()
	t := time.Date(y, m, d+1, 0, 0, 0, 0, onboarding.IST)
	return &t
}

// DigiLockerDevReturnURL is the mock provider's authorize step: it sends the
// browser to the public return route with a fresh mock code. Refused unless
// the mock is selected outside production.
func (s *Service) DigiLockerDevReturnURL(state string) (string, error) {
	dl := s.digilocker
	if !dl.Mock() || dl.Production {
		return "", ErrDigiLockerNotConfigured
	}
	if !digilocker.ValidState(state) {
		return "", postgres.ErrDigiLockerStateNotFound
	}
	nonce, err := digilocker.NewState()
	if err != nil {
		return "", err
	}
	q := url.Values{"code": {"mock-" + nonce[:24]}, "state": {state}}
	return dl.PublicBaseURL + digilocker.ReturnPath + "?" + q.Encode(), nil
}

// DigiLockerAppLinkURL is where the public return route sends the browser:
// the Rider App Link with the provider's code, state and any error passed
// through unchanged.
func (s *Service) DigiLockerAppLinkURL(query url.Values) (string, error) {
	u, err := url.Parse(s.digilocker.AppLinkURL)
	if s.digilocker.AppLinkURL == "" || err != nil || u.Host == "" {
		return "", ErrDigiLockerNotConfigured
	}
	q := u.Query()
	for _, k := range []string{"code", "state", "error", "error_description"} {
		if v := query.Get(k); v != "" && len(v) <= maxOAuthParam {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// ─── Documents, readiness and admin ────────────────────────────────────────

// AddDeliveryDocument validates, seals and records an upload. A document
// with a number needs the PII keys; a selfie does not.
func (s *Service) AddDeliveryDocument(ctx context.Context, userID uuid.UUID, in riderkyc.DocumentInput) (*postgres.DeliveryDocument, error) {
	v, err := riderkyc.ValidateDocument(in)
	if err != nil {
		return nil, err
	}
	rec := postgres.DeliveryDocumentRecord{DocumentType: v.DocumentType, MediaID: v.MediaID, FileURL: v.FileURL, Status: postgres.DocumentStatusPending}
	if v.Number != "" {
		if !s.pii.Configured() {
			return nil, ErrPIINotConfigured
		}
		sealed, err := s.sealDocumentNumber(ctx, v.DocumentType, v.Number)
		if err != nil {
			return nil, err
		}
		rec.NumberSealed, rec.NumberKeyVersion, rec.NumberLookup, rec.NumberMasked = sealed.Blob, sealed.KeyVersion, sealed.Lookup, sealed.Masked
	}
	return s.store.AddDeliveryPartnerDocument(ctx, userID, rec)
}

// sealDocumentNumber picks the lookup domain by document type: DL and RC are
// hashed for uniqueness; any other number is sealed without a lookup.
func (s *Service) sealDocumentNumber(ctx context.Context, docType, number string) (postgres.SealedDocumentNumber, error) {
	var sealed foodpii.Sealed
	var err error
	switch docType {
	case riderkyc.DocumentTypeDrivingLicence:
		sealed, err = s.pii.SealDrivingLicence(ctx, number)
	case riderkyc.DocumentTypeVehicleRC:
		sealed, err = s.pii.SealVehicleRegistration(ctx, number)
	default:
		sealed, err = s.pii.SealPartnerDocumentNumber(ctx, number)
	}
	if err != nil {
		return postgres.SealedDocumentNumber{}, err
	}
	compact, cerr := pii.CompactUpper(number)
	if cerr != nil {
		compact = number
	}
	return postgres.SealedDocumentNumber{Blob: sealed.Blob, KeyVersion: sealed.KeyVersion, Lookup: sealed.Lookup, Masked: pii.MaskTail(compact, 4)}, nil
}

// BackfillDeliveryDocumentNumbers seals the plaintext numbers written before
// B4 and clears them. Without PII keys it does nothing.
func (s *Service) BackfillDeliveryDocumentNumbers(ctx context.Context) (postgres.DocumentBackfillResult, error) {
	if !s.pii.Configured() {
		return postgres.DocumentBackfillResult{}, ErrPIINotConfigured
	}
	return s.store.BackfillDeliveryDocumentNumbers(ctx, s.sealDocumentNumber, 200)
}

func (s *Service) DeliveryPartnerKYCForUser(ctx context.Context, userID uuid.UUID) (*postgres.DeliveryPartnerKYC, error) {
	return s.store.DeliveryPartnerKYCForUser(ctx, userID)
}

func (s *Service) AdminDeliveryPartnerKYC(ctx context.Context, partnerID uuid.UUID) (*postgres.DeliveryPartnerKYC, error) {
	return s.store.AdminDeliveryPartnerKYC(ctx, partnerID)
}

func (s *Service) AdminDecideDeliveryPartnerDocument(ctx context.Context, adminID, partnerID, documentID uuid.UUID, decision, reason string) (*postgres.DeliveryDocument, error) {
	d, err := onboarding.ValidateDocumentDecision(decision, reason)
	if err != nil {
		return nil, err
	}
	return s.store.AdminDecideDeliveryPartnerDocument(ctx, adminID, partnerID, documentID, d, strings.TrimSpace(reason))
}
