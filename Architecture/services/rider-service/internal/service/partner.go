package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/atpost/rider-service/internal/digilocker"
	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// allowedPartnerTypes lists the values rider_partner_type accepts.
var allowedPartnerTypes = map[string]bool{
	"individual_driver": true,
	"owner_driver":      true,
	"fleet_owner":       true,
	"fleet_driver":      true,
}

// CreatePartnerRequest is the input shape for CreatePartnerProfile.
type CreatePartnerRequest struct {
	PartnerType string
	FullName    string
	Phone       string
	Email       *string
	CityID      *uuid.UUID
}

// CreatePartnerProfile creates a `draft`-status partner row for the AtPost
// user. Validates inputs, emits EventRiderPartnerCreated.
func (s *Service) CreatePartnerProfile(ctx context.Context, userID uuid.UUID, req CreatePartnerRequest) (*store.Partner, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user_id required")
	}
	if !allowedPartnerTypes[req.PartnerType] {
		return nil, fmt.Errorf("invalid: partner_type must be one of individual_driver, owner_driver, fleet_owner, fleet_driver")
	}
	if strings.TrimSpace(req.FullName) == "" {
		return nil, fmt.Errorf("invalid: full_name required")
	}
	if strings.TrimSpace(req.Phone) == "" {
		return nil, fmt.Errorf("invalid: phone required")
	}
	if existing, err := s.store.GetPartnerByUserID(ctx, userID); err == nil {
		// Partner already exists; return the existing row idempotently.
		return existing, nil
	} else if !errors.Is(err, store.ErrPartnerNotFound) {
		return nil, err
	}
	if req.CityID != nil {
		if _, err := s.store.GetCity(ctx, *req.CityID); err != nil {
			return nil, fmt.Errorf("invalid: city_id not found")
		}
	}
	p, err := s.store.CreatePartner(ctx, store.CreatePartnerInput{
		UserID:      userID,
		PartnerType: req.PartnerType,
		FullName:    strings.TrimSpace(req.FullName),
		Phone:       strings.TrimSpace(req.Phone),
		Email:       req.Email,
		CityID:      req.CityID,
	})
	if err != nil {
		return nil, fmt.Errorf("create partner: %w", err)
	}
	cityID := ""
	if p.CityID != nil {
		cityID = p.CityID.String()
	}
	if perr := s.producer.PublishPartnerCreated(ctx, p.ID, p.UserID, p.PartnerType, cityID); perr != nil {
		slog.Warn("rider: publish partner.created failed", "partner_id", p.ID, "error", perr)
	}
	return p, nil
}

// GetMyPartner returns the partner row for the user.
func (s *Service) GetMyPartner(ctx context.Context, userID uuid.UUID) (*store.Partner, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user_id required")
	}
	p, err := s.store.GetPartnerByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, store.ErrPartnerNotFound) {
			return nil, fmt.Errorf("not_found: no partner profile")
		}
		return nil, err
	}
	return p, nil
}

// UpdatePartnerProfileRequest is the input for partial updates.
type UpdatePartnerProfileRequest struct {
	FullName        *string
	Email           *string
	ProfilePhotoURL *string
	CityID          *uuid.UUID
}

// UpdatePartnerProfile applies the patch to the user's partner row.
func (s *Service) UpdatePartnerProfile(ctx context.Context, userID uuid.UUID, req UpdatePartnerProfileRequest) (*store.Partner, error) {
	p, err := s.GetMyPartner(ctx, userID)
	if err != nil {
		return nil, err
	}
	if req.CityID != nil {
		if _, err := s.store.GetCity(ctx, *req.CityID); err != nil {
			return nil, fmt.Errorf("invalid: city_id not found")
		}
	}
	return s.store.UpdatePartnerProfile(ctx, p.ID, store.UpdatePartnerProfileInput{
		FullName:        req.FullName,
		Email:           req.Email,
		ProfilePhotoURL: req.ProfilePhotoURL,
		CityID:          req.CityID,
	})
}

// SubmitKYCDocumentRequest is the input for SubmitKYCDocument.
type SubmitKYCDocumentRequest struct {
	DocumentType   string
	DocumentNumber *string
	FileURL        string
	ExpiresAt      *time.Time
}

// allowedDocumentTypes covers the rider_document_type enum values.
var allowedDocumentTypes = map[string]bool{
	"aadhaar":               true,
	"pan":                   true,
	"driving_license":       true,
	"profile_photo":         true,
	"police_verification":   true,
	"vehicle_rc":            true,
	"vehicle_insurance":     true,
	"pollution_certificate": true,
	"permit":                true,
	"fitness_certificate":   true,
	"bank_proof":            true,
	"other":                 true,
}

// SubmitKYCDocument creates a partner document row in `pending` status. For
// document_type='aadhaar' it cross-checks with the previously stored
// DigiLocker assertion (DPDP: we still don't store the raw number — the
// `document_number` field is nil'd out for aadhaar uploads so a malicious
// client can't smuggle one through).
//
// The partner status flips draft -> pending_verification once any document
// is uploaded; admin verification (S3) flips to approved.
func (s *Service) SubmitKYCDocument(ctx context.Context, userID, partnerID uuid.UUID, req SubmitKYCDocumentRequest) (*store.PartnerDocument, error) {
	if !allowedDocumentTypes[req.DocumentType] {
		return nil, fmt.Errorf("invalid: document_type must be a known rider_document_type")
	}
	if strings.TrimSpace(req.FileURL) == "" {
		return nil, fmt.Errorf("invalid: file_url required")
	}
	p, err := s.store.GetPartner(ctx, partnerID)
	if err != nil {
		if errors.Is(err, store.ErrPartnerNotFound) {
			return nil, fmt.Errorf("not_found: partner")
		}
		return nil, err
	}
	if p.UserID != userID {
		return nil, fmt.Errorf("forbidden: partner does not belong to user")
	}
	docNumber := req.DocumentNumber
	if req.DocumentType == "aadhaar" {
		// DPDP: never persist a raw Aadhaar number. The DigiLocker flow is
		// the canonical capture path; the document row only references the
		// uploaded file (e.g. a masked ID image) without the number.
		docNumber = nil
		// Cross-check: an Aadhaar document submission should ideally come
		// after the DigiLocker callback recorded an assertion. Log a soft
		// warning if no assertion exists yet — admin reviews in S3.
		if _, aerr := s.store.GetAadhaarVerification(ctx, partnerID); aerr != nil {
			slog.Warn("rider: aadhaar doc submitted without prior digilocker assertion",
				"partner_id", partnerID)
		}
	}
	doc, err := s.store.CreatePartnerDocument(ctx, store.CreatePartnerDocumentInput{
		PartnerID:      partnerID,
		DocumentType:   req.DocumentType,
		DocumentNumber: docNumber,
		FileURL:        req.FileURL,
		ExpiresAt:      req.ExpiresAt,
	})
	if err != nil {
		return nil, fmt.Errorf("create partner document: %w", err)
	}
	// Move partner from `draft` to `pending_verification` on first doc.
	if p.Status == "draft" {
		if uerr := s.store.UpdatePartnerStatus(ctx, partnerID, "pending_verification"); uerr != nil {
			return nil, fmt.Errorf("update partner status: %w", uerr)
		}
		if uerr := s.store.UpdatePartnerKYCStatus(ctx, partnerID, "pending"); uerr != nil {
			return nil, fmt.Errorf("update kyc status: %w", uerr)
		}
	}
	if perr := s.producer.PublishPartnerKYCSubmitted(ctx, partnerID, doc.ID, doc.DocumentType); perr != nil {
		slog.Warn("rider: publish kyc.submitted failed", "partner_id", partnerID, "error", perr)
	}
	return doc, nil
}

// --- Aadhaar / DigiLocker -------------------------------------------------

// AadhaarFlowStart is returned by StartAadhaarFlow.
type AadhaarFlowStart struct {
	DigiLockerAuthorizeURL string `json:"digilocker_authorize_url"`
	State                  string `json:"state"`
}

// AadhaarFlowResult is returned by CompleteAadhaarFlow.
type AadhaarFlowResult struct {
	Verified bool      `json:"verified"`
	IssuedAt time.Time `json:"issued_at"`
}

const digilockerStateTTL = 10 * time.Minute

func riderStateKey(state string) string { return "rider:digilocker:state:" + state }

func generateState() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// StartAadhaarFlow allocates a one-shot PKCE-style state nonce for the
// partner, persists it in Redis under the partner id, and returns the
// DigiLocker authorize URL the mobile app should open.
//
// DPDP Act compliant — see mopedu/MOPEDU_SPEC.md §19.
func (s *Service) StartAadhaarFlow(ctx context.Context, userID, partnerID uuid.UUID) (*AadhaarFlowStart, error) {
	p, err := s.store.GetPartner(ctx, partnerID)
	if err != nil {
		return nil, err
	}
	if p.UserID != userID {
		return nil, fmt.Errorf("forbidden: partner does not belong to user")
	}
	clientID := os.Getenv("DIGILOCKER_CLIENT_ID")
	redirectURI := os.Getenv("DIGILOCKER_REDIRECT_URI")
	authorizeBase := os.Getenv("DIGILOCKER_AUTHORIZE_URL")
	if authorizeBase == "" {
		authorizeBase = "https://api.digitallocker.gov.in/public/oauth2/1/authorize"
	}
	state, err := generateState()
	if err != nil {
		return nil, err
	}
	if s.rdb != nil {
		if err := s.rdb.Set(ctx, riderStateKey(state), partnerID.String(), digilockerStateTTL).Err(); err != nil {
			return nil, fmt.Errorf("persist state: %w", err)
		}
	}
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("scope", "aadhaar")
	authorizeURL := authorizeBase + "?" + q.Encode()
	return &AadhaarFlowStart{DigiLockerAuthorizeURL: authorizeURL, State: state}, nil
}

// CompleteAadhaarFlow validates the PKCE state via Redis, exchanges the code
// with the partner, and persists the assertion. DPDP-compliant: no Aadhaar
// number ever crosses this boundary — only the opaque assertion reference +
// hashed document-type label.
func (s *Service) CompleteAadhaarFlow(ctx context.Context, userID, partnerID uuid.UUID, code, state string) (*AadhaarFlowResult, error) {
	if userID == uuid.Nil || partnerID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user_id and partner_id required")
	}
	if code == "" || state == "" {
		return nil, fmt.Errorf("invalid: code and state required")
	}
	p, err := s.store.GetPartner(ctx, partnerID)
	if err != nil {
		return nil, err
	}
	if p.UserID != userID {
		return nil, fmt.Errorf("forbidden: partner does not belong to user")
	}
	if s.rdb != nil {
		stored, err := s.rdb.Get(ctx, riderStateKey(state)).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				return nil, fmt.Errorf("forbidden: state expired or invalid")
			}
			return nil, fmt.Errorf("lookup state: %w", err)
		}
		if stored != partnerID.String() {
			return nil, fmt.Errorf("forbidden: state does not match partner")
		}
		if err := s.rdb.Del(ctx, riderStateKey(state)).Err(); err != nil {
			slog.Warn("rider: clear state key failed", "error", err)
		}
	}
	if s.digilockerClient == nil {
		return nil, fmt.Errorf("digilocker client not configured")
	}
	assertion, err := s.digilockerClient.ExchangeCode(ctx, code, state)
	if err != nil {
		return nil, fmt.Errorf("digilocker exchange: %w", err)
	}
	if assertion == nil || assertion.Reference == "" {
		return nil, fmt.Errorf("digilocker: empty assertion")
	}
	docHash := digilocker.HashDocumentType(assertion.DocumentType)
	if err := s.store.RecordAadhaarVerification(ctx, partnerID, assertion.Reference, docHash, assertion.IssuedAt.Unix()); err != nil {
		return nil, fmt.Errorf("persist verification: %w", err)
	}
	return &AadhaarFlowResult{Verified: true, IssuedAt: assertion.IssuedAt}, nil
}
