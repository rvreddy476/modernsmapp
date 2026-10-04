// Package propii seals the professional's personal data with shared/pii
// (the food-service foodpii pattern): payout account numbers, PAN, police
// and trade certificate numbers, and the DigiLocker PKCE code_verifier. Keys
// are DOORSTEP_PII_KEYS ("v1:<key>[,v2:...]", parsed by internal/config).
//
// It fails closed: a nil *Crypto answers ErrNotConfigured from every seal,
// so a route can never fall back to writing plaintext. Production refuses to
// start without keys (config); development without keys boots with these
// routes answering 503.
package propii

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/doorstep-service/internal/config"
	"github.com/atpost/shared/pii"
)

// Scopes. Each binds its ciphertext: a blob sealed for one does not open
// under another.
const (
	ScopePayoutAccount      pii.Scope = "doorstep.payout_account"
	ScopePAN                pii.Scope = "doorstep.pan"
	ScopeProDocument        pii.Scope = "doorstep.pro_document"
	ScopeDigiLockerVerifier pii.Scope = "doorstep.digilocker_verifier"
	// ScopeCustomerAddress seals a customer address's street lines (A3).
	ScopeCustomerAddress pii.Scope = "doorstep.customer_address"
)

// ErrNotConfigured: no keys (development only).
var ErrNotConfigured = errors.New("doorstep PII sealing is not configured")

// Sealed is a sealed blob and the key version it used. Never plaintext.
type Sealed struct {
	Blob       []byte
	KeyVersion uint32
}

// Crypto holds the sealers.
type Crypto struct {
	account, pan, document, verifier, address *pii.Sealer
}

// New builds the sealers from parsed keys. No keys → (nil, nil).
func New(ctx context.Context, keys []config.PIIKey) (*Crypto, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	var static []pii.StaticKey
	scopes := []pii.Scope{ScopePayoutAccount, ScopePAN, ScopeProDocument, ScopeDigiLockerVerifier, ScopeCustomerAddress}
	for _, k := range keys {
		for _, s := range scopes {
			static = append(static, pii.StaticKey{Scope: s, Version: k.Version, Key: k.Key})
		}
	}
	ring, err := pii.NewStaticKeyRing(static...)
	if err != nil {
		return nil, fmt.Errorf("DOORSTEP_PII_KEYS rejected: %w", err)
	}
	c := &Crypto{}
	for _, x := range []struct {
		dst   **pii.Sealer
		scope pii.Scope
	}{{&c.account, ScopePayoutAccount}, {&c.pan, ScopePAN}, {&c.document, ScopeProDocument}, {&c.verifier, ScopeDigiLockerVerifier}, {&c.address, ScopeCustomerAddress}} {
		s, err := pii.NewSealer(ctx, ring, x.scope)
		if err != nil {
			return nil, fmt.Errorf("sealer %s: %w", x.scope, err)
		}
		*x.dst = s
	}
	return c, nil
}

// Configured reports whether sealing is available.
func (c *Crypto) Configured() bool { return c != nil }

func seal(ctx context.Context, s *pii.Sealer, v string) (Sealed, error) {
	blob, version, err := s.Seal(ctx, v)
	if err != nil {
		return Sealed{}, err
	}
	return Sealed{Blob: blob, KeyVersion: version}, nil
}

// SealAccountNumber seals a bank account number.
func (c *Crypto) SealAccountNumber(ctx context.Context, v string) (Sealed, error) {
	if c == nil {
		return Sealed{}, ErrNotConfigured
	}
	return seal(ctx, c.account, v)
}

// SealPAN seals a PAN.
func (c *Crypto) SealPAN(ctx context.Context, v string) (Sealed, error) {
	if c == nil {
		return Sealed{}, ErrNotConfigured
	}
	return seal(ctx, c.pan, v)
}

// SealDocumentNumber seals a certificate number.
func (c *Crypto) SealDocumentNumber(ctx context.Context, v string) (Sealed, error) {
	if c == nil {
		return Sealed{}, ErrNotConfigured
	}
	return seal(ctx, c.document, v)
}

// SealVerifier seals a PKCE code_verifier.
func (c *Crypto) SealVerifier(ctx context.Context, v string) (Sealed, error) {
	if c == nil {
		return Sealed{}, ErrNotConfigured
	}
	return seal(ctx, c.verifier, v)
}

// OpenVerifier opens a sealed PKCE code_verifier.
func (c *Crypto) OpenVerifier(ctx context.Context, blob []byte) (string, error) {
	if c == nil {
		return "", ErrNotConfigured
	}
	return c.verifier.Open(ctx, blob)
}

// OpenAccountNumber opens a sealed account number (settlement export only;
// never returned by an API).
func (c *Crypto) OpenAccountNumber(ctx context.Context, blob []byte) (string, error) {
	if c == nil {
		return "", ErrNotConfigured
	}
	return c.account.Open(ctx, blob)
}

// SealAddress seals a customer address's street lines (a JSON blob).
func (c *Crypto) SealAddress(ctx context.Context, v string) (Sealed, error) {
	if c == nil {
		return Sealed{}, ErrNotConfigured
	}
	return seal(ctx, c.address, v)
}

// OpenAddress opens sealed street lines for the address's owner (and, from
// acceptance to completion, the professional; admin views).
func (c *Crypto) OpenAddress(ctx context.Context, blob []byte) (string, error) {
	if c == nil {
		return "", ErrNotConfigured
	}
	return c.address.Open(ctx, blob)
}
