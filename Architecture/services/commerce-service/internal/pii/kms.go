package pii

// The production KeyProvider: envelope keys wrapped by AWS KMS, kept in a
// durable ring.
//
// ## The problem this solves
//
// [KeyProvider.DataKey] is asked for "the same 32 bytes as version N", and
// the ciphertext envelope records only that integer. KMS cannot answer that
// from a number: `GenerateDataKey` returns a plaintext key AND an opaque
// ciphertext blob, and the only way back to that plaintext is to hand the
// SAME blob to `Decrypt`. Calling GenerateDataKey again yields different
// bytes.
//
// A provider that ignored this would make every row written under an earlier
// version permanently undecryptable — silently, and only discovered when
// someone opened an old address. So the blob is persisted per (scope,
// version) in a key ring, and this type is the thing that reads it.
//
// ## Encryption context
//
// Every call binds a context of scope + environment + purpose. KMS requires
// the SAME context at Decrypt, which means a blob wrapped for
// `order_snapshot` in staging cannot be unwrapped as `profile` in
// production even by a caller holding both. It is authenticated data, not
// metadata, and it is stored with the row rather than rebuilt from config
// that may have drifted since.
//
// ## Failure posture
//
// Every error path refuses. There is no fallback to a process-local key, in
// any environment: a fallback would encrypt real customer addresses under a
// key that vanishes with the pod, which is data loss wearing the costume of
// availability.

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
)

// KMSClient is the narrow slice of KMS this needs.
//
// Two methods, defined here rather than imported, so the provider can be
// driven by a fake in tests and so `internal/pii` does not depend on the AWS
// SDK. The concrete adapter lives at the edge and implements exactly this.
type KMSClient interface {
	// GenerateDataKey returns a fresh 256-bit key and its wrapped form.
	GenerateDataKey(ctx context.Context, keyID string, encryptionContext map[string]string) (plaintext, wrapped []byte, err error)

	// Decrypt unwraps a blob previously returned by GenerateDataKey. The
	// encryption context MUST match the one used to create it.
	Decrypt(ctx context.Context, wrapped []byte, encryptionContext map[string]string) (plaintext []byte, err error)
}

// KeyRing is the durable store of wrapped keys.
type KeyRing interface {
	// Active returns the live version for a scope, or an error wrapping
	// [ErrNoActiveKey] when the scope has none yet.
	Active(ctx context.Context, scope string) (version int, wrapped []byte, encryptionContext map[string]string, err error)

	// ByVersion returns a specific version, retired or not — a retired key
	// still has to open everything sealed while it was current.
	ByVersion(ctx context.Context, scope string, version int) (wrapped []byte, encryptionContext map[string]string, err error)

	// Create installs the scope's FIRST live version.
	//
	// It must REFUSE when a live version already exists, returning the
	// existing version and an error wrapping [ErrActiveKeyExists]. Callers
	// reach here holding a key KMS just generated, so a Create that
	// silently rotated would give every concurrent first-user its own
	// version — and each would then write rows the others cannot read.
	Create(ctx context.Context, scope string, wrapped []byte, kmsKeyID string, encryptionContext map[string]string) (version int, err error)

	// Rotate installs a NEW version, retiring the current one. Unlike
	// Create it expects an active key to exist.
	Rotate(ctx context.Context, scope string, wrapped []byte, kmsKeyID string, encryptionContext map[string]string) (version int, err error)
}

var (
	// ErrNoActiveKey is what a KeyRing wraps when a scope has no live key.
	// The provider treats it as "create the first one", not as a fault.
	ErrNoActiveKey = errors.New("pii: no active key for scope")

	// ErrKeyUnavailable means the key could not be produced. Callers must
	// treat it as fatal for the operation; there is no degraded mode.
	ErrKeyUnavailable = errors.New("pii: key unavailable")

	// ErrActiveKeyExists is what a KeyRing wraps when Create loses a race.
	// The caller discards its freshly generated key — it has encrypted
	// nothing — and re-reads the winner.
	ErrActiveKeyExists = errors.New("pii: an active key already exists for this scope")
)

// DataKeySize is 32 bytes — AES-256.
const DataKeySize = 32

// KMSKeyProvider implements [KeyProvider] against KMS plus a key ring.
type KMSKeyProvider struct {
	kms KMSClient
	// ring persists the wrapped keys. Losing it loses the data.
	ring KeyRing
	// keyID is the customer-managed CMK that wraps data keys.
	keyID string
	// environment binds keys to dev/staging/prod, so a blob from one cannot
	// be unwrapped in another even with CMK access.
	environment string
}

// NewKMSKeyProvider builds the production provider.
func NewKMSKeyProvider(kms KMSClient, ring KeyRing, keyID, environment string) (*KMSKeyProvider, error) {
	if kms == nil {
		return nil, fmt.Errorf("pii: a KMS client is required")
	}
	if ring == nil {
		return nil, fmt.Errorf("pii: a key ring is required; without durable wrapped keys, " +
			"rows written under an earlier version become undecryptable")
	}
	if keyID == "" {
		return nil, fmt.Errorf("pii: a CMK id is required")
	}
	if environment == "" {
		return nil, fmt.Errorf("pii: an environment is required for the encryption context")
	}
	return &KMSKeyProvider{kms: kms, ring: ring, keyID: keyID, environment: environment}, nil
}

// encryptionContext is the authenticated binding for a scope.
//
// KMS verifies this on Decrypt, so it is a real constraint rather than a
// label: a blob wrapped for one scope or environment will not unwrap as
// another, even by a caller with full CMK access.
func (p *KMSKeyProvider) encryptionContext(scope Scope) map[string]string {
	return map[string]string{
		"purpose":     "commerce-pii",
		"scope":       string(scope),
		"environment": p.environment,
	}
}

// DataKey implements [KeyProvider].
//
// version 0 means "current": return the live key, creating the scope's first
// one if the ring is empty. Any other version is a historical lookup and
// must resolve exactly — falling back to the current key would hand the
// caller bytes that cannot open the value it asked about.
func (p *KMSKeyProvider) DataKey(ctx context.Context, scope Scope, version int) ([]byte, int, error) {
	if version < 0 {
		return nil, 0, fmt.Errorf("%w: negative key version %d", ErrKeyUnavailable, version)
	}
	if version > 0 {
		return p.historical(ctx, scope, version)
	}
	return p.current(ctx, scope)
}

func (p *KMSKeyProvider) historical(ctx context.Context, scope Scope, version int) ([]byte, int, error) {
	wrapped, storedCtx, err := p.ring.ByVersion(ctx, string(scope), version)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %s v%d: %v", ErrKeyUnavailable, scope, version, err)
	}
	key, err := p.unwrap(ctx, wrapped, storedCtx, scope)
	if err != nil {
		return nil, 0, err
	}
	return key, version, nil
}

func (p *KMSKeyProvider) current(ctx context.Context, scope Scope) ([]byte, int, error) {
	version, wrapped, storedCtx, err := p.ring.Active(ctx, string(scope))
	switch {
	case err == nil:
		key, unwrapErr := p.unwrap(ctx, wrapped, storedCtx, scope)
		if unwrapErr != nil {
			return nil, 0, unwrapErr
		}
		return key, version, nil

	case errors.Is(err, ErrNoActiveKey):
		// First use of this scope. Mint one.
		return p.mint(ctx, scope, false)

	default:
		return nil, 0, fmt.Errorf("%w: reading the active key for %s: %v", ErrKeyUnavailable, scope, err)
	}
}

// mint creates the scope's first (or next) key and persists it before
// returning.
//
// Persist-then-use is the whole point: a key returned to a caller that had
// not been stored would encrypt rows nobody can ever decrypt. If the ring
// write fails, the key is discarded and the operation fails — it has
// encrypted nothing at that point, so nothing is lost.
func (p *KMSKeyProvider) mint(ctx context.Context, scope Scope, rotating bool) ([]byte, int, error) {
	encCtx := p.encryptionContext(scope)
	plaintext, wrapped, err := p.kms.GenerateDataKey(ctx, p.keyID, encCtx)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: generating a data key for %s: %v", ErrKeyUnavailable, scope, err)
	}
	if len(plaintext) != DataKeySize {
		zero(plaintext)
		return nil, 0, fmt.Errorf("%w: KMS returned a %d-byte key, want %d",
			ErrKeyUnavailable, len(plaintext), DataKeySize)
	}

	version, err := p.persist(ctx, scope, wrapped, encCtx, rotating)
	if err != nil {
		// Lost a race, or the write failed. Either way this key has
		// encrypted nothing, so discarding it costs nothing — and using it
		// would be the expensive mistake.
		zero(plaintext)

		// A concurrent creator won. That is a resolved condition, not a
		// failure: re-read and use the winner's key rather than failing a
		// customer's request over a race that has already settled.
		if errors.Is(err, ErrActiveKeyExists) {
			existingVersion, existingWrapped, storedCtx, reErr := p.ring.Active(ctx, string(scope))
			if reErr != nil {
				return nil, 0, fmt.Errorf("%w: re-reading after a lost race for %s: %v",
					ErrKeyUnavailable, scope, reErr)
			}
			key, unwrapErr := p.unwrap(ctx, existingWrapped, storedCtx, scope)
			if unwrapErr != nil {
				return nil, 0, unwrapErr
			}
			return key, existingVersion, nil
		}
		return nil, 0, fmt.Errorf("%w: persisting a data key for %s: %v", ErrKeyUnavailable, scope, err)
	}
	return plaintext, version, nil
}

func (p *KMSKeyProvider) persist(
	ctx context.Context,
	scope Scope,
	wrapped []byte,
	encCtx map[string]string,
	rotating bool,
) (int, error) {
	if rotating {
		return p.ring.Rotate(ctx, string(scope), wrapped, p.keyID, encCtx)
	}
	return p.ring.Create(ctx, string(scope), wrapped, p.keyID, encCtx)
}

// unwrap decrypts a stored blob, re-asserting the context it was made under.
func (p *KMSKeyProvider) unwrap(ctx context.Context, wrapped []byte, storedCtx map[string]string, scope Scope) ([]byte, error) {
	if len(wrapped) == 0 {
		return nil, fmt.Errorf("%w: empty wrapped key for %s", ErrKeyUnavailable, scope)
	}
	// Defence in depth. KMS enforces the context itself, but a ring row whose
	// stored context names a different scope means the ring has been tampered
	// with or mis-written, and using it would be trusting the wrong record.
	if storedCtx["scope"] != string(scope) {
		return nil, fmt.Errorf("%w: key ring row for %s carries scope %q",
			ErrWrongScope, scope, storedCtx["scope"])
	}
	if storedCtx["environment"] != p.environment {
		return nil, fmt.Errorf("%w: key ring row for %s was created in environment %q, this is %q",
			ErrKeyUnavailable, scope, storedCtx["environment"], p.environment)
	}

	plaintext, err := p.kms.Decrypt(ctx, wrapped, storedCtx)
	if err != nil {
		return nil, fmt.Errorf("%w: unwrapping the key for %s: %v", ErrKeyUnavailable, scope, err)
	}
	if len(plaintext) != DataKeySize {
		zero(plaintext)
		return nil, fmt.Errorf("%w: unwrapped a %d-byte key, want %d",
			ErrKeyUnavailable, len(plaintext), DataKeySize)
	}
	return plaintext, nil
}

// Rotate installs a new key version for a scope.
//
// Existing rows keep their version and keep opening under the retired key —
// rotation changes what NEW writes use, and nothing else. Re-encrypting old
// rows is a separate backfill and deliberately not done here, because doing
// it implicitly during a rotation would rewrite order snapshots that GST
// rules require to stay as they were.
func (p *KMSKeyProvider) Rotate(ctx context.Context, scope Scope) (int, error) {
	key, version, err := p.mint(ctx, scope, true)
	if err != nil {
		return 0, err
	}
	zero(key)
	return version, nil
}

// zero best-effort clears key material.
//
// Go gives no guarantee the compiler keeps this, and the GC may already have
// copied the slice; it is a reduction in exposure window, not a promise. Said
// plainly here so nobody reads it as one.
func zero(b []byte) {
	if len(b) == 0 {
		return
	}
	for i := range b {
		b[i] = 0
	}
	// Prevent the write being optimised away entirely.
	_ = subtle.ConstantTimeByteEq(b[0], 0)
}
