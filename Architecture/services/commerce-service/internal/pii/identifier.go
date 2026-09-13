package pii

// Seller KYC identifiers: bank account numbers and PANs.
//
// Migration 035. These were plaintext columns — `seller_payout_accounts.
// account_number` held the FULL number, which is the thing a fraudster needs to
// redirect a payout, and `sellers.pan_number` / `organizations.pan` were
// returned in full on the seller and admin APIs. They go through the same
// envelope as addresses, under their own scope, with a separate cutover
// (COMMERCE_KYC_PII_CUTOVER) so the address cutover is never held hostage to
// this one.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
)

// ScopeKYC covers business tax and bank identifiers: a seller's payout
// account, a seller's PAN, an organization's PAN. One retention class — these
// are business records kept for tax and settlement disputes, not a customer
// address book — and deliberately NOT the profile scope, whose proposed
// 90-day shred would destroy the account a seller is still being paid into.
const ScopeKYC Scope = "kyc"

// ErrEmptyIdentifier refuses to seal nothing. An absent identifier is a NULL
// column, not an encrypted empty string that looks like data.
var ErrEmptyIdentifier = errors.New("pii: refusing to seal an empty identifier")

// SealedIdentifier is the encrypted form of one identifier.
type SealedIdentifier struct {
	Enc        []byte
	KeyVersion int
	// Hash is the salted lookup digest, so "is this account already on
	// another seller?" can be answered after the plaintext is gone. It is
	// never displayed and never logged.
	Hash string
}

// SealIdentifier encrypts value under scope.
//
// The lookup hash is domain-separated ("kyc:"+domain) so the same digits in
// two different kinds of identifier never collide, and `hashParts` lets a
// caller bind context that makes two values genuinely the same thing — an
// account number is only the same account at the same IFSC.
func (c *Cipher) SealIdentifier(ctx context.Context, scope Scope, domain, value string, hashParts ...string) (*SealedIdentifier, error) {
	if value == "" {
		return nil, ErrEmptyIdentifier
	}
	enc, version, err := c.Seal(ctx, scope, value)
	if err != nil {
		return nil, err
	}
	parts := make([]string, 0, len(hashParts)+2)
	parts = append(parts, "kyc:"+domain)
	parts = append(parts, hashParts...)
	parts = append(parts, value)
	return &SealedIdentifier{Enc: enc, KeyVersion: version, Hash: c.LookupHash(parts...)}, nil
}

// OpenIdentifier decrypts a value sealed by SealIdentifier. An empty blob is
// an error, not "": a caller that reached for ciphertext and found none has a
// missing value, and treating that as an empty string would hide it.
func (c *Cipher) OpenIdentifier(ctx context.Context, scope Scope, enc []byte) (string, error) {
	if len(enc) == 0 {
		return "", ErrBadCiphertext
	}
	return c.Open(ctx, scope, enc)
}

// MaskPAN is the display form of a PAN: everything but the last four hidden.
//
// "ABCDE1234F" -> "XXXXXX234F". The last four are the part a seller recognises
// as theirs and, on their own, identify nobody.
func MaskPAN(pan string) string {
	p := strings.ToUpper(strings.TrimSpace(pan))
	n := len(p)
	switch {
	case n == 0:
		return ""
	case n <= 4:
		return strings.Repeat("X", n)
	default:
		return strings.Repeat("X", n-4) + p[n-4:]
	}
}

// NormalizePAN is the canonical form sealed and hashed.
func NormalizePAN(pan string) string {
	return strings.ToUpper(strings.TrimSpace(pan))
}

// ─── Local (development) key material ───────────────────────────────

// devKYCDerivationLabel is the HMAC label that derives the local KYC key from
// the local profile key when no dedicated one is configured.
const devKYCDerivationLabel = "commerce-pii-dev:kyc"

// LocalKeyProvider builds the development key provider from the configured
// static keys.
//
// It exists so cmd/server and cmd/piibackfill cannot drift: the backfill must
// seal with exactly the keys the service later opens with, and two copies of
// this map were one forgotten scope away from sealing KYC under nothing.
//
// When `kyc` is empty the KYC key is DERIVED from the profile key, and the
// second return reports it. That keeps an existing developer stack booting
// without a new secret; it is acceptable only because this provider is never
// used outside a local environment, which main.go enforces structurally.
func LocalKeyProvider(profile, snapshot, kyc []byte) (*StaticKeyProvider, bool, error) {
	if len(profile) != DataKeySize || len(snapshot) != DataKeySize {
		return nil, false, fmt.Errorf(
			"COMMERCE_PII_DEV_KEY_PROFILE and COMMERCE_PII_DEV_KEY_SNAPSHOT must each be exactly " +
				"32 bytes (development only; prod and staging use KMS)")
	}
	derived := false
	if len(kyc) == 0 {
		mac := hmac.New(sha256.New, profile)
		mac.Write([]byte(devKYCDerivationLabel))
		kyc = mac.Sum(nil)
		derived = true
	} else if len(kyc) != DataKeySize {
		return nil, false, fmt.Errorf("COMMERCE_PII_DEV_KEY_KYC must be exactly 32 bytes when set")
	}
	return &StaticKeyProvider{Keys: map[Scope][]byte{
		ScopeProfile:       profile,
		ScopeOrderSnapshot: snapshot,
		ScopeKYC:           kyc,
	}}, derived, nil
}
