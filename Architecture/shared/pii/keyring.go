package pii

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
)

// KeyRing resolves data keys by exact (scope, version). Version 0 is never
// valid, which matches the pii_key_ring CHECK (version > 0).
//
// Implementations must never return key material or plaintext inside an error.
//
// # Mapping onto existing implementations
//
// Commerce KMS ring (commerce-service/internal/pii/kms.go): a KMS-backed
// KeyRing is a KeyWrapper plus a durable wrapped-key store such as the
// pii_key_ring table.
//   - KMSClient.GenerateDataKey(ctx, keyID, encCtx) becomes
//     KeyWrapper.GenerateDataKey(ctx, encCtx); the adapter closes over the CMK id.
//   - KMSClient.Decrypt becomes KeyWrapper.Unwrap.
//   - ActiveVersion(scope) comes from ring.Active. On ErrNoActiveKey the ring
//     mints with GenerateDataKey, persists with Create, then uses the key, and
//     adopts the winner when it loses the race.
//   - DataKey(scope, v) comes from ring.ByVersion plus Unwrap with the STORED
//     encryption context, after re-checking its scope and environment. The ring
//     caches keys and bounds the plaintext key lifetime.
//   - Commerce's "version 0 means current" overload splits into ActiveVersion
//     and DataKey. Rotation stays a method on the concrete KMS ring; it is not
//     part of this read interface.
//   - The int version from the database converts to uint32; a value <= 0 or
//     above MaxUint32 is refused.
//
// Commerce dev StaticKeyProvider: NewStaticKeyRing({"profile",1,k1},
// {"order_snapshot",1,k2}). Its always-version-1 behaviour matches, so blobs are
// wire-identical.
//
// Monetization bank-details key (payout_methods.go): NewStaticKeyRing(
// {"monetization.bank_account",1,DecodeKey(MONETIZATION_BANK_DETAILS_KEY)}), and
// ErrBankCaptureNotConfigured becomes a NewSealer failure at startup. It is NOT
// wire-compatible (no header, no additional data, "v1:"+base64 text): a
// migration keeps the legacy decrypt as a read-only path for "v1:" rows, writes
// new rows under a distinct tag, re-seals in a backfill, then retires "v1:".
type KeyRing interface {
	// ActiveVersion returns the version new values are sealed under. It returns
	// an error wrapping ErrScopeNotConfigured when the scope has no key.
	ActiveVersion(ctx context.Context, scope Scope) (uint32, error)
	// DataKey returns the 32-byte key for exactly (scope, version). It returns
	// an error wrapping ErrUnknownKeyVersion when that version is absent, and
	// never falls back to the active version.
	DataKey(ctx context.Context, scope Scope, version uint32) ([]byte, error)
}

// KeyWrapper generates and unwraps data keys under a key-encrypting key held by
// a KMS. It is defined here and deliberately not implemented: a service that
// already imports the AWS SDK adapts KMS to it, so this module stays free of
// that dependency. keyContext is the KMS encryption context (purpose, scope,
// environment); it must be stored with the wrapped key and passed back verbatim.
type KeyWrapper interface {
	GenerateDataKey(ctx context.Context, keyContext map[string]string) (plaintext, wrapped []byte, err error)
	Unwrap(ctx context.Context, wrapped []byte, keyContext map[string]string) (plaintext []byte, err error)
}

// StaticKey is one entry for NewStaticKeyRing.
type StaticKey struct {
	Scope   Scope
	Version uint32
	Key     []byte
}

// StaticKeyRing holds keys in memory. It is for development and tests only;
// services must refuse it in production. The zero value is usable and holds no
// keys, so it fails closed. Keys are copied in and copied out.
type StaticKeyRing struct {
	mu     sync.RWMutex
	keys   map[Scope]map[uint32][]byte
	active map[Scope]uint32
}

var _ KeyRing = (*StaticKeyRing)(nil)

// NewStaticKeyRing builds a ring from keys given in any order. The active
// version of each scope is its highest version. It refuses an invalid scope,
// version 0, a key that is not 32 bytes, an all-zero key, and a duplicate
// (scope, version).
func NewStaticKeyRing(keys ...StaticKey) (*StaticKeyRing, error) {
	r := &StaticKeyRing{}
	for _, k := range keys {
		if err := r.add(k.Scope, k.Version, k.Key, false); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// Rotate adds a key and makes it active. The version must exceed the scope's
// current active version; the same refusals as NewStaticKeyRing apply.
func (r *StaticKeyRing) Rotate(scope Scope, version uint32, key []byte) error {
	if r == nil {
		return &KeyError{Scope: scope, Version: version, Err: ErrNoKeyRing}
	}
	return r.add(scope, version, key, true)
}

// ActiveVersion implements KeyRing.
func (r *StaticKeyRing) ActiveVersion(_ context.Context, scope Scope) (uint32, error) {
	if r == nil {
		return 0, ErrScopeNotConfigured
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.active[scope]
	if !ok || v == 0 {
		return 0, ErrScopeNotConfigured
	}
	return v, nil
}

// DataKey implements KeyRing. It resolves exactly and returns a copy.
func (r *StaticKeyRing) DataKey(_ context.Context, scope Scope, version uint32) ([]byte, error) {
	if r == nil || version == 0 {
		return nil, ErrUnknownKeyVersion
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	k, ok := r.keys[scope][version]
	if !ok {
		return nil, ErrUnknownKeyVersion
	}
	return append([]byte(nil), k...), nil
}

func (r *StaticKeyRing) add(scope Scope, version uint32, key []byte, mustBeNewer bool) error {
	if err := scope.Validate(); err != nil {
		return &KeyError{Scope: scope, Version: version, Err: err}
	}
	if version == 0 {
		return &KeyError{Scope: scope, Err: fmt.Errorf("%w: version 0 is reserved", ErrInvalidKeyVersion)}
	}
	if err := validateKey(key); err != nil {
		return &KeyError{Scope: scope, Version: version, Err: err}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.keys[scope][version]; dup {
		return &KeyError{Scope: scope, Version: version, Err: fmt.Errorf("%w: version is already in the ring", ErrInvalidKeyVersion)}
	}
	if mustBeNewer && version <= r.active[scope] {
		return &KeyError{Scope: scope, Version: version, Err: fmt.Errorf("%w: rotation must exceed the active version", ErrInvalidKeyVersion)}
	}
	if r.keys == nil {
		r.keys = map[Scope]map[uint32][]byte{}
		r.active = map[Scope]uint32{}
	}
	if r.keys[scope] == nil {
		r.keys[scope] = map[uint32][]byte{}
	}
	r.keys[scope][version] = append([]byte(nil), key...)
	if version > r.active[scope] {
		r.active[scope] = version
	}
	return nil
}

func validateKey(key []byte) error {
	if len(key) != DataKeySize {
		return ErrBadDataKey
	}
	var acc byte
	for _, b := range key {
		acc |= b
	}
	if acc == 0 {
		return fmt.Errorf("%w: key is all zeros", ErrBadDataKey)
	}
	return nil
}

// DecodeKey parses a 32-byte data key from 64 hex characters or from standard or
// URL-safe base64 (padded or not). Surrounding whitespace is ignored. An all-zero
// key is refused. Errors never echo the input.
func DecodeKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	bad := fmt.Errorf("%w: expected 64 hex characters or base64 of exactly 32 bytes", ErrBadDataKey)
	var key []byte
	if len(s) == 2*DataKeySize {
		if b, err := hex.DecodeString(s); err == nil {
			key = b
		}
	}
	if key == nil {
		for _, enc := range []*base64.Encoding{
			base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
		} {
			if b, err := enc.DecodeString(s); err == nil && len(b) == DataKeySize {
				key = b
				break
			}
		}
	}
	if key == nil {
		return nil, bad
	}
	if err := validateKey(key); err != nil {
		return nil, err
	}
	return key, nil
}
