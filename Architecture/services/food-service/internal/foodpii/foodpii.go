// Package foodpii wires shared/pii for food-service: the two sealing scopes
// (restaurant PAN, payout account number), their lookup hashes, and the
// FOOD_PII_KEYS / FOOD_PII_LOOKUP_SALT configuration.
//
// It fails closed. A nil *Crypto is valid and answers ErrNotConfigured from
// every seal, so a route can never fall back to writing plaintext. Production
// (ENV other than local/dev/development, blank included) refuses to start
// without keys; local/dev without keys boots with the PII routes answering 503.
package foodpii

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/atpost/shared/kyc"
	"github.com/atpost/shared/pii"
)

const (
	ScopeRestaurantPAN pii.Scope = "food.restaurant_pan"
	ScopePayoutAccount pii.Scope = "food.payout_account"

	LookupDomainPAN         = "pan"
	LookupDomainBankAccount = "bank_account"

	EnvKeys       = "FOOD_PII_KEYS"
	EnvLookupSalt = "FOOD_PII_LOOKUP_SALT"
)

// ErrNotConfigured is what every PII route answers (503 PII_NOT_CONFIGURED)
// when the keys are absent.
var ErrNotConfigured = errors.New("PII_NOT_CONFIGURED")

// VersionedKey is one parsed FOOD_PII_KEYS entry.
type VersionedKey struct {
	Version uint32
	Key     []byte
}

// ParseKeys reads "v1:<key>[,v2:<key>...]". Each key is 32 bytes as base64 or
// hex (pii.DecodeKey). The highest version seals; every version opens. Errors
// name the entry position, never its content.
func ParseKeys(raw string) ([]VersionedKey, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("%s is empty", EnvKeys)
	}
	seen := map[uint32]bool{}
	var out []VersionedKey
	for i, entry := range strings.Split(raw, ",") {
		pos := i + 1
		entry = strings.TrimSpace(entry)
		if entry == "" {
			return nil, fmt.Errorf("%s entry %d is empty", EnvKeys, pos)
		}
		ver, key, ok := strings.Cut(entry, ":")
		if !ok || len(ver) < 2 || ver[0] != 'v' {
			return nil, fmt.Errorf("%s entry %d must look like v<version>:<base64 key>", EnvKeys, pos)
		}
		n, err := strconv.ParseUint(ver[1:], 10, 32)
		if err != nil || n == 0 {
			return nil, fmt.Errorf("%s entry %d: version must be a whole number from 1", EnvKeys, pos)
		}
		if seen[uint32(n)] {
			return nil, fmt.Errorf("%s entry %d: version %d appears twice", EnvKeys, pos, n)
		}
		seen[uint32(n)] = true
		decoded, err := pii.DecodeKey(key)
		if err != nil {
			return nil, fmt.Errorf("%s entry %d: key must be 32 bytes, base64 or hex", EnvKeys, pos)
		}
		out = append(out, VersionedKey{Version: uint32(n), Key: decoded})
	}
	return out, nil
}

// IsProduction is true for every ENV except local, dev and development — a
// blank ENV included, matching the payments client.
func IsProduction(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "local", "dev", "development":
		return false
	}
	return true
}

// Sealed is what a column set receives: the sealed blob, the key version it
// used, and the exact-match lookup hash. It never holds plaintext.
type Sealed struct {
	Blob       []byte
	KeyVersion uint32
	Lookup     string
}

type Crypto struct {
	pan           *pii.Sealer
	account       *pii.Sealer
	panLookup     *pii.LookupHasher
	accountLookup *pii.LookupHasher
}

// New builds the sealers and hashers. Every version is registered for both
// scopes; the envelope binds the scope as additional data, so a blob sealed
// for one scope does not open under the other.
func New(ctx context.Context, keys []VersionedKey, salt []byte) (*Crypto, error) {
	var static []pii.StaticKey
	for _, k := range keys {
		static = append(static,
			pii.StaticKey{Scope: ScopeRestaurantPAN, Version: k.Version, Key: k.Key},
			pii.StaticKey{Scope: ScopePayoutAccount, Version: k.Version, Key: k.Key},
		)
	}
	ring, err := pii.NewStaticKeyRing(static...)
	if err != nil {
		return nil, fmt.Errorf("%s rejected: %w", EnvKeys, err)
	}
	panSealer, err := pii.NewSealer(ctx, ring, ScopeRestaurantPAN)
	if err != nil {
		return nil, fmt.Errorf("pan sealer: %w", err)
	}
	accountSealer, err := pii.NewSealer(ctx, ring, ScopePayoutAccount)
	if err != nil {
		return nil, fmt.Errorf("payout account sealer: %w", err)
	}
	panLookup, err := pii.NewLookupHasher(salt, LookupDomainPAN, pii.CompactUpper)
	if err != nil {
		return nil, fmt.Errorf("%s rejected: %w", EnvLookupSalt, err)
	}
	accountLookup, err := pii.NewLookupHasher(salt, LookupDomainBankAccount, kyc.NormalizeBankAccountNumber)
	if err != nil {
		return nil, fmt.Errorf("%s rejected: %w", EnvLookupSalt, err)
	}
	return &Crypto{pan: panSealer, account: accountSealer, panLookup: panLookup, accountLookup: accountLookup}, nil
}

// FromEnv returns (nil, nil) only in local/dev with BOTH variables unset.
// Production without them, either one set alone, or a malformed value is an
// error, so a typo cannot silently disable sealing.
func FromEnv(ctx context.Context, getenv func(string) string) (*Crypto, error) {
	keysRaw := strings.TrimSpace(getenv(EnvKeys))
	saltRaw := strings.TrimSpace(getenv(EnvLookupSalt))
	if keysRaw == "" && saltRaw == "" {
		if IsProduction(getenv("ENV")) {
			return nil, fmt.Errorf("%s and %s are required unless ENV is local, dev or development", EnvKeys, EnvLookupSalt)
		}
		return nil, nil
	}
	if keysRaw == "" || saltRaw == "" {
		return nil, fmt.Errorf("set both %s and %s, or (local/dev only) neither", EnvKeys, EnvLookupSalt)
	}
	keys, err := ParseKeys(keysRaw)
	if err != nil {
		return nil, err
	}
	return New(ctx, keys, []byte(saltRaw))
}

// Configured reports whether sealing is available.
func (c *Crypto) Configured() bool { return c != nil }

// SealPAN seals a PAN under food.restaurant_pan and hashes it in domain "pan".
func (c *Crypto) SealPAN(ctx context.Context, pan string) (Sealed, error) {
	if c == nil {
		return Sealed{}, ErrNotConfigured
	}
	return seal(ctx, c.pan, c.panLookup, pan)
}

// SealAccountNumber seals a bank account number under food.payout_account and
// hashes it in domain "bank_account".
func (c *Crypto) SealAccountNumber(ctx context.Context, account string) (Sealed, error) {
	if c == nil {
		return Sealed{}, ErrNotConfigured
	}
	return seal(ctx, c.account, c.accountLookup, account)
}

// OpenPAN is for ops tooling and tests; no route returns an opened PAN.
func (c *Crypto) OpenPAN(ctx context.Context, blob []byte) (string, error) {
	if c == nil {
		return "", ErrNotConfigured
	}
	return c.pan.Open(ctx, blob)
}

// OpenAccountNumber is for a future payout adapter and tests; no route returns it.
func (c *Crypto) OpenAccountNumber(ctx context.Context, blob []byte) (string, error) {
	if c == nil {
		return "", ErrNotConfigured
	}
	return c.account.Open(ctx, blob)
}

func seal(ctx context.Context, s *pii.Sealer, h *pii.LookupHasher, value string) (Sealed, error) {
	lookup, err := h.Hash(value)
	if err != nil {
		return Sealed{}, fmt.Errorf("foodpii: lookup hash: %w", err)
	}
	blob, version, err := s.Seal(ctx, value)
	if err != nil {
		return Sealed{}, fmt.Errorf("foodpii: seal: %w", err)
	}
	return Sealed{Blob: blob, KeyVersion: version, Lookup: lookup}, nil
}
