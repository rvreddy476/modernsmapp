package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/monetization-service/internal/client/razorpayx"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/atpost/shared/kyc"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Bank account capture (plan Phase 4D)
// ---------------------------------------------------------------------------
//
// payout_methods used to hold an opaque client-supplied blob and a
// method_type CHECK without bank_account. A bank method now records the
// holder name, the IFSC and the last four digits in the clear (what a
// creator is shown), and the full account number encrypted under
// MONETIZATION_BANK_DETAILS_KEY in the existing details_encrypted column.
// The format rules are the ones commerce seller onboarding already
// applies, lifted into shared/kyc. No RBI IFSC directory exists anywhere;
// RazorpayX refuses a bad IFSC at fund-account creation, and its
// fund-account validation (a penny drop) is what sets verified_at.

const (
	// PayoutMethodTypeBankAccount is the method_type the rail pays.
	PayoutMethodTypeBankAccount = "bank_account"

	bankDetailsKeyBytes   = 32 // AES-256
	bankDetailsCipherTag  = "v1:"
	holderNameMaxLen      = 100
	holderNameMinLen      = 2
)

var (
	// ErrBankCaptureNotConfigured: no MONETIZATION_BANK_DETAILS_KEY, so an
	// account number cannot be stored. Fail closed rather than store it
	// in the clear under a column called details_encrypted.
	ErrBankCaptureNotConfigured = errors.New("BANK_CAPTURE_NOT_CONFIGURED")
	// ErrBankDetailsRejected: the provider refused the fund account
	// outright (a wrong IFSC, an account it cannot pay).
	ErrBankDetailsRejected = errors.New("BANK_DETAILS_REJECTED")
	// ErrInvalidHolderName: the name on the account is missing or absurd.
	ErrInvalidHolderName = errors.New("INVALID_HOLDER_NAME")
	// ErrInvalidIFSC and ErrInvalidBankAccount re-export shared/kyc's so
	// the HTTP layer maps them without importing it.
	ErrInvalidIFSC        = kyc.ErrInvalidIFSC
	ErrInvalidBankAccount = kyc.ErrInvalidBankAccount
)

// WithBankDetailsKey sets the 32-byte AES key account numbers are stored
// under. A key of the wrong length is refused.
func (s *Service) WithBankDetailsKey(key []byte) *Service {
	if len(key) == bankDetailsKeyBytes {
		s.bankKey = append([]byte(nil), key...)
	} else if len(key) != 0 {
		slog.Error("bank details key ignored: must be 32 bytes", "got_bytes", len(key))
	}
	return s
}

// BankCaptureEnabled reports whether a bank account can be stored.
func (s *Service) BankCaptureEnabled() bool { return len(s.bankKey) == bankDetailsKeyBytes }

// ParseBankDetailsKey accepts the key as 64 hex characters or as
// standard/URL base64 of 32 bytes.
func ParseBankDetailsKey(v string) ([]byte, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	if b, err := hex.DecodeString(v); err == nil && len(b) == bankDetailsKeyBytes {
		return b, nil
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(v); err == nil && len(b) == bankDetailsKeyBytes {
			return b, nil
		}
	}
	return nil, fmt.Errorf("bank details key must be 32 bytes as hex or base64")
}

// BankPayoutMethodInput is what a creator supplies.
type BankPayoutMethodInput struct {
	HolderName    string
	AccountNumber string
	IFSC          string
	IsDefault     bool
}

// AddBankPayoutMethod validates, stores and — when the rail is configured
// — registers the account with the provider at once. The returned method
// is verified only if the provider's validation reported the account
// active; otherwise it is recorded unverified and the withdrawal path
// refuses it until it is.
func (s *Service) AddBankPayoutMethod(ctx context.Context, userID uuid.UUID, in BankPayoutMethodInput) (*postgres.PayoutMethod, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("INVALID_ID: user is required")
	}
	holder := strings.Join(strings.Fields(in.HolderName), " ")
	if len(holder) < holderNameMinLen || len(holder) > holderNameMaxLen {
		return nil, ErrInvalidHolderName
	}
	ifsc, err := kyc.NormalizeIFSC(in.IFSC)
	if err != nil {
		return nil, err
	}
	account, err := kyc.NormalizeBankAccountNumber(in.AccountNumber)
	if err != nil {
		return nil, err
	}
	if !s.BankCaptureEnabled() {
		return nil, ErrBankCaptureNotConfigured
	}
	enc, err := encryptSecret(s.bankKey, account)
	if err != nil {
		return nil, fmt.Errorf("encrypt account number: %w", err)
	}

	m := &postgres.PayoutMethod{
		UserID:           userID,
		MethodType:       PayoutMethodTypeBankAccount,
		DetailsEncrypted: enc,
		IsDefault:        in.IsDefault,
		IsVerified:       false,
		IFSC:             ifsc,
		AccountLast4:     kyc.BankAccountLast4(account),
		HolderName:       holder,
	}
	if err := s.store.AddPayoutMethod(ctx, m); err != nil {
		return nil, fmt.Errorf("store payout method: %w", err)
	}

	if !s.PayoutRailEnabled() {
		slog.Info("bank payout method recorded unverified: payout rail not configured", "user_id", userID, "method_id", m.ID)
		return m, nil
	}

	contactID, err := s.ensureContact(ctx, userID, holder)
	if err != nil {
		return s.bankCaptureProviderFailure(ctx, m, "ensure contact", err)
	}
	fundAccountID, err := s.payoutClient.EnsureFundAccount(ctx, contactID, razorpayx.BankDetails{
		HolderName: holder, AccountNumber: account, IFSC: ifsc,
	})
	if err != nil {
		return s.bankCaptureProviderFailure(ctx, m, "ensure fund account", err)
	}
	if err := s.store.SetPayoutMethodFundAccount(ctx, m.ID, fundAccountID); err != nil {
		return nil, fmt.Errorf("record fund account: %w", err)
	}
	m.RzpFundAccountID = &fundAccountID

	v, err := s.payoutClient.ValidateFundAccount(ctx, fundAccountID)
	if err != nil {
		slog.Warn("bank payout method: fund-account validation did not complete; method stays unverified",
			"user_id", userID, "method_id", m.ID, "error", err)
		return m, nil
	}
	if strings.EqualFold(v.Status, "completed") && strings.EqualFold(v.AccountStatus, "active") {
		now := time.Now()
		if err := s.store.SetPayoutMethodVerified(ctx, m.ID, now); err != nil {
			return nil, fmt.Errorf("mark payout method verified: %w", err)
		}
		m.IsVerified = true
		m.VerifiedAt = &now
		slog.Info("bank payout method verified", "user_id", userID, "method_id", m.ID, "fund_account", fundAccountID, "registered_name", v.RegisteredName)
	} else {
		slog.Warn("bank payout method: validation not active; method stays unverified",
			"user_id", userID, "method_id", m.ID, "status", v.Status, "account_status", v.AccountStatus)
	}
	return m, nil
}

// bankCaptureProviderFailure decides what a provider error during capture
// means: a definitive refusal undoes the row and tells the creator; an
// ambiguous one keeps the row unverified for the submitter to finish.
func (s *Service) bankCaptureProviderFailure(ctx context.Context, m *postgres.PayoutMethod, step string, err error) (*postgres.PayoutMethod, error) {
	if razorpayx.IsAmbiguous(err) {
		slog.Warn("bank payout method: provider unreachable; method recorded unverified", "method_id", m.ID, "step", step, "error", err)
		return m, nil
	}
	slog.Warn("bank payout method refused by the provider", "method_id", m.ID, "step", step, "error", err)
	if delErr := s.store.DeletePayoutMethodByID(ctx, m.ID); delErr != nil {
		slog.Error("bank payout method: could not remove refused method", "method_id", m.ID, "error", delErr)
	}
	return nil, fmt.Errorf("%w: %s: %v", ErrBankDetailsRejected, step, providerErrorSummary(err))
}

// providerErrorSummary keeps the provider's description and drops the
// rest of the body from what a creator sees.
func providerErrorSummary(err error) string {
	var pe *razorpayx.Error
	if errors.As(err, &pe) && pe.Status > 0 {
		return fmt.Sprintf("provider answered %d", pe.Status)
	}
	return "provider refused"
}

// decryptBankAccountNumber reads back what AddBankPayoutMethod stored.
func (s *Service) decryptBankAccountNumber(encrypted string) (string, error) {
	if !s.BankCaptureEnabled() {
		return "", ErrBankCaptureNotConfigured
	}
	return decryptSecret(s.bankKey, encrypted)
}

// encryptSecret is AES-256-GCM with a random nonce: "v1:" + base64(nonce || ciphertext).
func encryptSecret(key []byte, plaintext string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	return bankDetailsCipherTag + base64.RawStdEncoding.EncodeToString(append(nonce, sealed...)), nil
}

func decryptSecret(key []byte, encoded string) (string, error) {
	if !strings.HasPrefix(encoded, bankDetailsCipherTag) {
		return "", errors.New("not a v1 ciphertext")
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(encoded, bankDetailsCipherTag))
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
