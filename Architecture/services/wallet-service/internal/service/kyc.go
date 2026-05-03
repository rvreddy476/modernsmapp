package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"

	"github.com/atpost/wallet-service/internal/store"
	"github.com/google/uuid"
)

// panRE validates an Indian PAN: 5 letters, 4 digits, 1 letter.
var panRE = regexp.MustCompile(`^[A-Z]{5}[0-9]{4}[A-Z]$`)

// AadhaarStartResult is what StartAadhaar returns to the handler.
type AadhaarStartResult struct {
	AuthorizeURL string `json:"authorize_url"`
	State        string `json:"state"`
}

// StartAadhaar generates a CSRF-safe state nonce and a DigiLocker authorize
// URL for the client to open. The full OAuth-style code-exchange callback
// runs in CompleteAadhaar.
//
// DPDP: the raw Aadhaar number is NEVER passed through this service. Only an
// opaque digilocker_ref returned by the partner survives in our DB. Same
// pattern as dating-service §15.8.
func (s *Service) StartAadhaar(ctx context.Context, userID uuid.UUID, digilockerBaseURL, redirectURI string) (*AadhaarStartResult, error) {
	if digilockerBaseURL == "" {
		return nil, fmt.Errorf("invalid: digilocker not configured")
	}
	state, err := randomHex(16)
	if err != nil {
		return nil, fmt.Errorf("generate state: %w", err)
	}
	if err := s.store.MarkAadhaarPending(ctx, userID); err != nil {
		return nil, fmt.Errorf("mark aadhaar pending: %w", err)
	}
	q := url.Values{}
	q.Set("client_id", "atpost-wallet")
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", "aadhaar.profile")
	q.Set("state", state)
	return &AadhaarStartResult{
		AuthorizeURL: strings.TrimRight(digilockerBaseURL, "/") + "/oauth/authorize?" + q.Encode(),
		State:        state,
	}, nil
}

// AadhaarVerifier is the contract a DigiLocker partner client must satisfy.
// Same shape as dating-service.digilocker.Client; we keep the dependency
// inverted so wallet-service can be unit-tested without importing the
// dating-service package.
type AadhaarVerifier interface {
	ExchangeCode(ctx context.Context, code, state string) (digilockerAssertion AadhaarAssertion, err error)
}

// AadhaarAssertion mirrors dating-service.digilocker.Assertion. NEVER carries
// the Aadhaar number — the partner client deliberately drops that field.
type AadhaarAssertion struct {
	Reference    string
	DocumentType string
}

// CompleteAadhaar runs the partner code exchange, stores the digilocker_ref,
// and upgrades the user's KYC tier to 'full'. Idempotent: re-submitting the
// same successful code is a no-op (same ref, same tier).
func (s *Service) CompleteAadhaar(ctx context.Context, userID uuid.UUID, code, state string, verifier AadhaarVerifier) (*store.KYCRecord, error) {
	if verifier == nil {
		return nil, fmt.Errorf("invalid: digilocker not configured")
	}
	if code == "" || state == "" {
		return nil, fmt.Errorf("invalid: code and state required")
	}
	assertion, err := verifier.ExchangeCode(ctx, code, state)
	if err != nil {
		// Record the rejection with no PII in the reason string.
		_ = s.store.SetRejection(ctx, userID, "digilocker_exchange_failed")
		return nil, fmt.Errorf("digilocker exchange: %w", err)
	}
	if assertion.Reference == "" {
		_ = s.store.SetRejection(ctx, userID, "digilocker_empty_reference")
		return nil, fmt.Errorf("digilocker: empty reference")
	}
	if err := s.store.UpsertAadhaarVerified(ctx, userID, assertion.Reference); err != nil {
		return nil, fmt.Errorf("upsert aadhaar: %w", err)
	}
	if err := s.store.SetKYCTier(ctx, userID, store.KYCFull); err != nil {
		return nil, fmt.Errorf("set tier: %w", err)
	}
	if err := s.producer.PublishKYCCompleted(ctx, userID, string(store.KYCFull)); err != nil {
		slog.Warn("wallet: publish kyc completed failed", "user", userID, "error", err)
	}
	return s.store.GetKYC(ctx, userID)
}

// SubmitPAN masks the PAN to its last 4 chars and stores it as 'pending'
// (a real partner-bank verification job flips it to 'verified' / 'failed').
// DPDP: the full PAN is never stored.
func (s *Service) SubmitPAN(ctx context.Context, userID uuid.UUID, panNumber string) (*store.KYCRecord, error) {
	pan := strings.ToUpper(strings.TrimSpace(panNumber))
	if !panRE.MatchString(pan) {
		return nil, fmt.Errorf("invalid: pan must match AAAAA9999A")
	}
	masked := "XXXXXX" + pan[len(pan)-4:] // last 4 chars only
	if err := s.store.SetPANStatus(ctx, userID, masked, "pending"); err != nil {
		return nil, fmt.Errorf("set pan: %w", err)
	}
	return s.store.GetKYC(ctx, userID)
}

// GetKYC returns the user's KYC record. Returns a synthetic minimal-tier row
// if no record exists yet — every user has at least minimal KYC.
func (s *Service) GetKYC(ctx context.Context, userID uuid.UUID) (*store.KYCRecord, error) {
	rec, err := s.store.GetKYC(ctx, userID)
	if err != nil {
		// store.ErrKYCNotFound is silenced into a default minimal row.
		return &store.KYCRecord{UserID: userID, Tier: store.KYCMinimal}, nil
	}
	return rec, nil
}

// randomHex generates a URL-safe random hex string of n bytes (2n chars).
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
