package service

// Seller KYC identifiers (migration 035): bank account numbers and PANs are
// sealed here, under pii.ScopeKYC, before the store sees them; and a raw
// Aadhaar number is refused before it reaches Postgres at all.
//
// The cutover is SEPARATE from the address one (COMMERCE_KYC_PII_CUTOVER), with
// the same two modes: dual writes plaintext beside the ciphertext and may read
// a legacy plaintext-only row; ciphertext writes and reads ciphertext only, and
// a row without it is an error.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/atpost/commerce-service/internal/kyc"
	"github.com/atpost/commerce-service/internal/pii"
	"github.com/atpost/commerce-service/internal/store/postgres"
	sharedkyc "github.com/atpost/shared/kyc"
)

// ErrAadhaarNumberNotAccepted refuses a raw Aadhaar number in a KYC document.
// Mapped to 400 at the edge with a fixed message: the refused value is never
// echoed, because an error body or log line is where it would leak next.
var ErrAadhaarNumberNotAccepted = errors.New(
	"an Aadhaar number cannot be stored; upload the Aadhaar document and leave its number blank")

// WithKYCCutover selects the seller-KYC encryption cutover mode. Unset means
// ModeDual, the only mode safe against an unfinished backfill.
func (s *Service) WithKYCCutover(m pii.Mode) *Service {
	s.kycCutover = m
	return s
}

// validateDocumentNumbers refuses a document set that would store an Aadhaar
// number.
//
// The `aadhaar` type stores its uploaded reference ONLY: any number at all is
// refused, masked or not, because accepting "masked" forms means parsing them
// and one missed format is a stored Aadhaar. Every other type is refused when
// its number is shaped like an Aadhaar — except a cancelled cheque, whose
// number is a bank account and would otherwise be refused one time in ten.
func validateDocumentNumbers(docs []postgres.SellerDocument) error {
	for _, d := range docs {
		if d.DocumentNumber == nil {
			continue
		}
		n := kyc.NormalizeDocumentNumber(*d.DocumentNumber)
		if n == "" {
			continue
		}
		if d.DocumentType == "aadhaar" {
			return fmt.Errorf("%w (document_type aadhaar)", ErrAadhaarNumberNotAccepted)
		}
		if d.DocumentType != "cancelled_cheque" && kyc.LooksLikeAadhaar(n) {
			return fmt.Errorf("%w (document_type %s)", ErrAadhaarNumberNotAccepted, d.DocumentType)
		}
	}
	return nil
}

// sealPayoutForWrite seals a payout account number.
//
// It runs BEFORE the store is touched, so a service with no cipher refuses
// rather than storing a bank account in plaintext.
func (s *Service) sealPayoutForWrite(ctx context.Context, in postgres.OnboardingPayoutInput) (postgres.SealedPayoutWrite, error) {
	if s.pii == nil {
		return postgres.SealedPayoutWrite{}, fmt.Errorf(
			"commerce: the PII cipher is not configured; refusing to store a payout account in plaintext")
	}
	number := strings.TrimSpace(in.AccountNumber)
	ifsc := ""
	if in.IFSCCode != nil {
		ifsc = strings.ToUpper(strings.TrimSpace(*in.IFSCCode))
	}
	// The hash binds the IFSC: the same digits at a different bank are a
	// different account, and duplicate detection must not say otherwise.
	sealed, err := s.pii.SealIdentifier(ctx, pii.ScopeKYC, "bank_account", number, ifsc)
	if err != nil {
		return postgres.SealedPayoutWrite{}, fmt.Errorf("commerce: sealing the payout account: %w", err)
	}
	return postgres.SealedPayoutWrite{
		AccountNumberEnc: sealed.Enc,
		KeyVersion:       sealed.KeyVersion,
		Hash:             sealed.Hash,
		Last4:            sharedkyc.BankAccountLast4(number),
		WritePlaintext:   s.kycCutover.WritesPlaintext(),
	}, nil
}

// openPayoutAccountNumber returns the full account number of a stored row.
//
// Ciphertext wins whenever it exists. A row without it is a legacy row in dual
// mode and a defect after cutover — silently serving the plaintext then would
// hide exactly the failure the cutover exists to surface.
func (s *Service) openPayoutAccountNumber(ctx context.Context, row *postgres.PayoutAccountRow) (string, error) {
	if len(row.AccountNumberEnc) > 0 {
		if s.pii == nil {
			return "", fmt.Errorf("commerce: the PII cipher is not configured; cannot open a payout account")
		}
		n, err := s.pii.OpenIdentifier(ctx, pii.ScopeKYC, row.AccountNumberEnc)
		if err != nil {
			return "", fmt.Errorf("commerce: opening the payout account: %w", err)
		}
		return n, nil
	}
	if !s.kycCutover.AllowsPlaintextRead() {
		return "", fmt.Errorf(
			"commerce: payout account has no ciphertext and the KYC PII cutover is complete")
	}
	if strings.TrimSpace(row.AccountNumber) == "" {
		return "", fmt.Errorf("commerce: payout account has neither ciphertext nor plaintext")
	}
	return row.AccountNumber, nil
}

// sellerPAN returns a seller's full PAN, or "" when the seller has none.
func (s *Service) sellerPAN(ctx context.Context, sel *postgres.Seller) (string, error) {
	if len(sel.PANEnc) > 0 {
		if s.pii == nil {
			return "", fmt.Errorf("commerce: the PII cipher is not configured; cannot open a PAN")
		}
		p, err := s.pii.OpenIdentifier(ctx, pii.ScopeKYC, sel.PANEnc)
		if err != nil {
			return "", fmt.Errorf("commerce: opening the seller PAN: %w", err)
		}
		return p, nil
	}
	if sel.PANNumber == nil || strings.TrimSpace(*sel.PANNumber) == "" {
		return "", nil
	}
	if !s.kycCutover.AllowsPlaintextRead() {
		return "", fmt.Errorf("commerce: seller PAN has no ciphertext and the KYC PII cutover is complete")
	}
	return pii.NormalizePAN(*sel.PANNumber), nil
}

// sealOrganizationPAN seals a PAN supplied on an organization write. A nil
// pointer means "not supplied" and leaves the stored PAN alone.
func (s *Service) sealOrganizationPAN(ctx context.Context, pan *string) (postgres.SealedIdentifierWrite, error) {
	if pan == nil {
		return postgres.SealedIdentifierWrite{}, nil
	}
	n := pii.NormalizePAN(*pan)
	if n == "" {
		return postgres.SealedIdentifierWrite{Supplied: true}, nil
	}
	if s.pii == nil {
		return postgres.SealedIdentifierWrite{}, fmt.Errorf(
			"commerce: the PII cipher is not configured; refusing to store a PAN in plaintext")
	}
	sealed, err := s.pii.SealIdentifier(ctx, pii.ScopeKYC, "pan", n)
	if err != nil {
		return postgres.SealedIdentifierWrite{}, fmt.Errorf("commerce: sealing the PAN: %w", err)
	}
	return postgres.SealedIdentifierWrite{
		Supplied:       true,
		Plain:          n,
		Enc:            sealed.Enc,
		KeyVersion:     sealed.KeyVersion,
		Hash:           sealed.Hash,
		Masked:         pii.MaskPAN(n),
		WritePlaintext: s.kycCutover.WritesPlaintext(),
	}, nil
}
