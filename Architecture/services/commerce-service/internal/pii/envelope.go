// Package pii encrypts customer and seller address fields at rest.
//
// LB-24 / v1 §5.14. Names, phone numbers and street addresses were plaintext
// columns. Aurora's storage encryption protects a stolen volume; it does
// nothing about a query, a log line, an analytics export, or a compromised
// read replica — and those are the realistic ways address data leaks.
//
// Envelope encryption, because the alternative shapes are worse:
//
//   - pgcrypto with a key in the DSN or a GUC puts the key where the data
//     is, so anything that can read the table can decrypt it.
//   - Calling KMS per row would put a network round trip in the checkout
//     path, which is the one place we have just spent a migration removing
//     network calls from.
//
// So KMS holds the key-encrypting key, the process holds a decrypted data
// key in memory for a bounded lifetime, and each value is sealed with
// AES-256-GCM under a random nonce. The wire format records a key version,
// so rotation can decrypt old rows while new writes use the new key.
//
// Review §5-D8: profile addresses and order snapshots are separate retention
// classes with separate key scopes, so one can be shredded without
// destroying the other. Shredding stays disabled until legal rules.
package pii

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// Scope separates key material by retention class.
type Scope string

const (
	// ScopeProfile covers a customer's saved address book. Proposed
	// retention: 90 days decryptable after last use (D8, awaiting ruling).
	ScopeProfile Scope = "profile"
	// ScopeOrderSnapshot covers the immutable address stored on an order.
	// GST and consumer-dispute obligations may require years, so this scope
	// is never shredded on a product default.
	ScopeOrderSnapshot Scope = "order_snapshot"
)

// KeyProvider yields a data key for a scope and version. In production this
// is KMS-backed; tests use a static provider.
type KeyProvider interface {
	// DataKey returns the 32-byte key for (scope, version). Version 0 means
	// "the current version", and the provider reports which one that is.
	DataKey(ctx context.Context, scope Scope, version int) (key []byte, actualVersion int, err error)
}

var (
	ErrNoKeyProvider = errors.New("pii: no key provider configured")
	ErrBadCiphertext = errors.New("pii: ciphertext is malformed")
	ErrWrongScope    = errors.New("pii: ciphertext belongs to a different scope")
)

// Cipher seals and opens PII values.
type Cipher struct {
	provider KeyProvider
	// lookupSalt keys the deterministic lookup hash. It is NOT the
	// encryption key: a hash that lets us answer "is this the same address
	// the customer already saved?" without decrypting, and which is
	// useless without the salt.
	lookupSalt []byte

	mu    sync.RWMutex
	cache map[cacheKey]cachedKey
	ttl   time.Duration
	now   func() time.Time
}

type cacheKey struct {
	scope   Scope
	version int
}

type cachedKey struct {
	key       []byte
	version   int
	expiresAt time.Time
}

// New builds a Cipher. `lookupSalt` must be stable for the life of the data:
// changing it invalidates every lookup hash (which is recoverable — the
// hashes are a convenience) but never the ciphertext.
func New(provider KeyProvider, lookupSalt []byte) (*Cipher, error) {
	if provider == nil {
		return nil, ErrNoKeyProvider
	}
	if len(lookupSalt) < 16 {
		return nil, fmt.Errorf("pii: lookup salt must be at least 16 bytes")
	}
	return &Cipher{
		provider:   provider,
		lookupSalt: lookupSalt,
		cache:      map[cacheKey]cachedKey{},
		ttl:        10 * time.Minute,
		now:        time.Now,
	}, nil
}

// Seal encrypts a value. An empty input seals to nil so an absent optional
// field stays absent rather than becoming an encrypted empty string.
//
// Format: version(4 BE) | nonce(12) | ciphertext+tag. The scope is bound
// into the GCM additional data, so a ciphertext moved between the profile
// and order-snapshot columns fails to open rather than silently decrypting.
func (c *Cipher) Seal(ctx context.Context, scope Scope, plaintext string) ([]byte, int, error) {
	if plaintext == "" {
		return nil, 0, nil
	}
	key, version, err := c.dataKey(ctx, scope, 0)
	if err != nil {
		return nil, 0, err
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, 0, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, 0, fmt.Errorf("pii: nonce: %w", err)
	}
	out := make([]byte, 4+len(nonce))
	binary.BigEndian.PutUint32(out[:4], uint32(version))
	copy(out[4:], nonce)
	out = aead.Seal(out, nonce, []byte(plaintext), []byte(scope))
	return out, version, nil
}

// Open decrypts a value sealed by Seal.
func (c *Cipher) Open(ctx context.Context, scope Scope, blob []byte) (string, error) {
	if len(blob) == 0 {
		return "", nil
	}
	if len(blob) < 4+12 {
		return "", ErrBadCiphertext
	}
	version := int(binary.BigEndian.Uint32(blob[:4]))
	key, _, err := c.dataKey(ctx, scope, version)
	if err != nil {
		return "", err
	}
	aead, err := newAEAD(key)
	if err != nil {
		return "", err
	}
	ns := aead.NonceSize()
	nonce := blob[4 : 4+ns]
	pt, err := aead.Open(nil, nonce, blob[4+ns:], []byte(scope))
	if err != nil {
		// Could be tampering, a wrong key version, or a ciphertext from
		// another scope. All three are refusals, and none of them should
		// reveal which.
		return "", ErrBadCiphertext
	}
	return string(pt), nil
}

// LookupHash produces a stable, salted digest for exact-match lookup without
// decryption. It is never displayed and never logged.
func (c *Cipher) LookupHash(parts ...string) string {
	mac := hmac.New(sha256.New, c.lookupSalt)
	for _, p := range parts {
		mac.Write([]byte(p))
		mac.Write([]byte{0}) // separator, so ("ab","c") != ("a","bc")
	}
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (c *Cipher) dataKey(ctx context.Context, scope Scope, version int) ([]byte, int, error) {
	ck := cacheKey{scope: scope, version: version}
	c.mu.RLock()
	if e, ok := c.cache[ck]; ok && c.now().Before(e.expiresAt) {
		c.mu.RUnlock()
		return e.key, e.version, nil
	}
	c.mu.RUnlock()

	key, actual, err := c.provider.DataKey(ctx, scope, version)
	if err != nil {
		return nil, 0, err
	}
	if len(key) != 32 {
		return nil, 0, fmt.Errorf("pii: data key for %s v%d must be 32 bytes, got %d", scope, version, len(key))
	}
	c.mu.Lock()
	c.cache[ck] = cachedKey{key: key, version: actual, expiresAt: c.now().Add(c.ttl)}
	// Cache the resolved version too, so a later Open of a row written with
	// this version is a cache hit rather than another KMS call.
	c.cache[cacheKey{scope: scope, version: actual}] = cachedKey{
		key: key, version: actual, expiresAt: c.now().Add(c.ttl),
	}
	c.mu.Unlock()
	return key, actual, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("pii: cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

// ─── Static provider (dev, tests, and the smoke environment) ─────────

// StaticKeyProvider serves keys from memory.
//
// It exists so tests and local development do not need KMS. Production must
// use the KMS provider; the service refuses to start with this one when
// ENV=prod, because a data key baked into a process is not a key management
// story.
type StaticKeyProvider struct {
	Keys map[Scope][]byte
}

func (p *StaticKeyProvider) DataKey(_ context.Context, scope Scope, version int) ([]byte, int, error) {
	k, ok := p.Keys[scope]
	if !ok {
		return nil, 0, fmt.Errorf("pii: no static key for scope %q", scope)
	}
	return k, 1, nil
}

// ─── Address helpers ─────────────────────────────────────────────────

// Address is the decrypted shape the domain works with.
//
// PostalCode, City and State are deliberately NOT encrypted: delivery
// serviceability and the interstate/intrastate GST determination both need
// them in a WHERE clause, and none of the three identifies a person alone.
type Address struct {
	ContactName  string `json:"contact_name"`
	Phone        string `json:"phone"`
	AddressLine1 string `json:"address_line_1"`
	AddressLine2 string `json:"address_line_2,omitempty"`
	Landmark     string `json:"landmark,omitempty"`
	City         string `json:"city"`
	State        string `json:"state"`
	PostalCode   string `json:"postal_code"`
	Country      string `json:"country"`
}

// Sealed is the encrypted column set for one address.
type Sealed struct {
	ContactName  []byte
	Phone        []byte
	AddressLine1 []byte
	AddressLine2 []byte
	Landmark     []byte
	KeyVersion   int
	LookupHash   string
}

// SealAddress encrypts the identifying fields of an address.
func (c *Cipher) SealAddress(ctx context.Context, scope Scope, a Address) (*Sealed, error) {
	out := &Sealed{}
	var err error
	var v int
	if out.ContactName, v, err = c.Seal(ctx, scope, a.ContactName); err != nil {
		return nil, err
	}
	out.KeyVersion = v
	if out.Phone, _, err = c.Seal(ctx, scope, a.Phone); err != nil {
		return nil, err
	}
	if out.AddressLine1, _, err = c.Seal(ctx, scope, a.AddressLine1); err != nil {
		return nil, err
	}
	if out.AddressLine2, _, err = c.Seal(ctx, scope, a.AddressLine2); err != nil {
		return nil, err
	}
	if out.Landmark, _, err = c.Seal(ctx, scope, a.Landmark); err != nil {
		return nil, err
	}
	out.LookupHash = c.LookupHash(a.ContactName, a.Phone, a.AddressLine1, a.PostalCode)
	return out, nil
}

// OpenAddress decrypts an address, merging in the plaintext geo columns.
func (c *Cipher) OpenAddress(ctx context.Context, scope Scope, s Sealed, city, state, postal, country string) (*Address, error) {
	a := &Address{City: city, State: state, PostalCode: postal, Country: country}
	var err error
	if a.ContactName, err = c.Open(ctx, scope, s.ContactName); err != nil {
		return nil, err
	}
	if a.Phone, err = c.Open(ctx, scope, s.Phone); err != nil {
		return nil, err
	}
	if a.AddressLine1, err = c.Open(ctx, scope, s.AddressLine1); err != nil {
		return nil, err
	}
	if a.AddressLine2, err = c.Open(ctx, scope, s.AddressLine2); err != nil {
		return nil, err
	}
	if a.Landmark, err = c.Open(ctx, scope, s.Landmark); err != nil {
		return nil, err
	}
	return a, nil
}

// Redact returns a form safe for a log line or a metric label.
//
// Nothing in this package should ever be logged, but the function exists so
// that a call site which genuinely needs to say WHICH address it is talking
// about has something safe to reach for instead of interpolating the struct.
func Redact(a Address) string {
	last4 := ""
	if n := len(a.Phone); n >= 4 {
		last4 = a.Phone[n-4:]
	}
	return fmt.Sprintf("addr(pin=%s,state=%s,phone=…%s)", a.PostalCode, a.State, last4)
}
