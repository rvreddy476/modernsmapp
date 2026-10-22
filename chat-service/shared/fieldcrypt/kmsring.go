package fieldcrypt

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// The production ring: master keys minted by a KeyWrapper (AWS KMS
// GenerateDataKey under a customer-managed key), persisted WRAPPED in a
// durable store, unwrapped once at boot and kept in memory. The request path
// never calls KMS.
//
// This is commerce's mechanism (commerce-service/internal/pii/kms.go): KMS
// cannot regenerate a data key from a version number, so every wrapped blob
// is stored against its version, and a value sealed under v1 still opens
// after v2 exists. The encryption context (purpose, scope, environment) is
// stored with the row and passed back verbatim; KMS refuses a mismatch, so a
// key wrapped for staging does not unwrap in production.

// Encryption-context keys, the same names commerce uses so a KMS key policy
// can pin them the same way.
const (
	KeyContextPurpose     = "purpose"
	KeyContextScope       = "scope"
	KeyContextEnvironment = "environment"
)

var (
	// ErrActiveKeyExists is what a WrappedKeyStore returns when
	// CreateFirstWrappedKey loses the race to another process. The loser has
	// encrypted nothing with its key and adopts the winner's.
	ErrActiveKeyExists = errors.New("fieldcrypt: a wrapped key already exists")
	// ErrKeyContextMismatch means a stored wrapped key belongs to another
	// purpose, scope or environment. The ring refuses to start.
	ErrKeyContextMismatch = errors.New("fieldcrypt: stored key context does not match this ring")
)

// WrappedKey is one persisted master key, wrapped by the KMS.
type WrappedKey struct {
	Version  uint32
	Wrapped  []byte
	KMSKeyID string
	Context  map[string]string
}

// WrappedKeyStore persists wrapped keys. Losing it loses the data.
type WrappedKeyStore interface {
	// ListWrappedKeys returns every version, retired or not.
	ListWrappedKeys(ctx context.Context) ([]WrappedKey, error)
	// CreateFirstWrappedKey installs version 1 when the store is empty and
	// returns ErrActiveKeyExists (wrapped) when any version exists.
	CreateFirstWrappedKey(ctx context.Context, k WrappedKey) error
	// RotateWrappedKey installs the next version as the active one and
	// returns it.
	RotateWrappedKey(ctx context.Context, k WrappedKey) (uint32, error)
}

// KMSRingConfig configures OpenKMSRing.
type KMSRingConfig struct {
	Wrapper  KeyWrapper
	Store    WrappedKeyStore
	KMSKeyID string
	// Purpose and RingScope label the keys (e.g. "chat-field", "chat.field");
	// Environment binds them to one deployment.
	Purpose     string
	RingScope   string
	Environment string
	// RefreshInterval bounds how often an unknown version triggers a reload
	// (another replica rotated). Default 30s.
	RefreshInterval time.Duration
	// Now is for tests.
	Now func() time.Time
}

// KMSRing implements KeyRing over KMS-wrapped master keys.
type KMSRing struct {
	*VersionedRing
	cfg KMSRingConfig

	refreshMu   sync.Mutex
	lastRefresh time.Time
}

var _ KeyRing = (*KMSRing)(nil)

// OpenKMSRing loads and unwraps every stored version, minting version 1 on a
// fresh store. It fails closed: any unwrap failure, a context mismatch or an
// empty store that cannot be minted into refuses to return a ring.
func OpenKMSRing(ctx context.Context, cfg KMSRingConfig) (*KMSRing, error) {
	switch {
	case cfg.Wrapper == nil:
		return nil, errors.New("fieldcrypt: a KMS key wrapper is required")
	case cfg.Store == nil:
		return nil, errors.New("fieldcrypt: a wrapped-key store is required; without it rows sealed under an earlier version become unreadable")
	case cfg.KMSKeyID == "":
		return nil, errors.New("fieldcrypt: a KMS key id is required")
	case cfg.Purpose == "" || cfg.RingScope == "" || cfg.Environment == "":
		return nil, errors.New("fieldcrypt: purpose, ring scope and environment are required for the encryption context")
	}
	if cfg.RefreshInterval <= 0 {
		cfg.RefreshInterval = 30 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	r := &KMSRing{VersionedRing: &VersionedRing{}, cfg: cfg}
	n, err := r.load(ctx)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		if err := r.mintFirst(ctx); err != nil {
			return nil, err
		}
	}
	if len(r.Versions()) == 0 {
		return nil, fmt.Errorf("%w: the key store is empty after minting", ErrScopeNotConfigured)
	}
	return r, nil
}

func (r *KMSRing) keyContext() map[string]string {
	return map[string]string{
		KeyContextPurpose:     r.cfg.Purpose,
		KeyContextScope:       r.cfg.RingScope,
		KeyContextEnvironment: r.cfg.Environment,
	}
}

func (r *KMSRing) checkContext(stored map[string]string) error {
	want := r.keyContext()
	if len(stored) != len(want) {
		return ErrKeyContextMismatch
	}
	for k, v := range want {
		if stored[k] != v {
			return ErrKeyContextMismatch
		}
	}
	return nil
}

// load unwraps every stored version not yet in memory and returns how many
// versions the store holds.
func (r *KMSRing) load(ctx context.Context) (int, error) {
	keys, err := r.cfg.Store.ListWrappedKeys(ctx)
	if err != nil {
		return 0, fmt.Errorf("fieldcrypt: listing wrapped keys: %w", err)
	}
	for _, k := range keys {
		if k.Version == 0 {
			return 0, fmt.Errorf("%w: a stored key has version 0", ErrInvalidKeyVersion)
		}
		if r.has(k.Version) {
			continue
		}
		if err := r.checkContext(k.Context); err != nil {
			return 0, fmt.Errorf("fieldcrypt: key version %d: %w", k.Version, err)
		}
		plain, err := r.cfg.Wrapper.Unwrap(ctx, k.Wrapped, k.Context)
		if err != nil {
			return 0, fmt.Errorf("fieldcrypt: unwrapping key version %d: %w", k.Version, err)
		}
		err = r.add(k.Version, plain)
		zero(plain)
		if err != nil {
			return 0, err
		}
	}
	return len(keys), nil
}

func (r *KMSRing) mintFirst(ctx context.Context) error {
	keyCtx := r.keyContext()
	plain, wrapped, err := r.cfg.Wrapper.GenerateDataKey(ctx, keyCtx)
	if err != nil {
		return fmt.Errorf("fieldcrypt: generating the first data key: %w", err)
	}
	defer zero(plain)
	if err := validateKey(plain); err != nil {
		return fmt.Errorf("fieldcrypt: the KMS returned an unusable data key: %w", err)
	}
	err = r.cfg.Store.CreateFirstWrappedKey(ctx, WrappedKey{
		Version: 1, Wrapped: wrapped, KMSKeyID: r.cfg.KMSKeyID, Context: keyCtx,
	})
	switch {
	case err == nil:
		// Persisted before use: a key that was not stored never seals.
		return r.add(1, plain)
	case errors.Is(err, ErrActiveKeyExists):
		// Another replica won; adopt its key. Ours sealed nothing.
		_, lerr := r.load(ctx)
		return lerr
	default:
		return fmt.Errorf("fieldcrypt: persisting the first data key: %w", err)
	}
}

// Rotate mints a new master key, persists it as the next active version and
// starts sealing under it. Old versions keep opening. Other replicas pick the
// new version up when they meet it (DataKey) or on restart.
func (r *KMSRing) Rotate(ctx context.Context) (uint32, error) {
	keyCtx := r.keyContext()
	plain, wrapped, err := r.cfg.Wrapper.GenerateDataKey(ctx, keyCtx)
	if err != nil {
		return 0, fmt.Errorf("fieldcrypt: generating a data key: %w", err)
	}
	defer zero(plain)
	if err := validateKey(plain); err != nil {
		return 0, fmt.Errorf("fieldcrypt: the KMS returned an unusable data key: %w", err)
	}
	v, err := r.cfg.Store.RotateWrappedKey(ctx, WrappedKey{Wrapped: wrapped, KMSKeyID: r.cfg.KMSKeyID, Context: keyCtx})
	if err != nil {
		return 0, fmt.Errorf("fieldcrypt: persisting the rotated data key: %w", err)
	}
	if err := r.add(v, plain); err != nil {
		return 0, err
	}
	return v, nil
}

// DataKey implements KeyRing. A version this process has not seen (another
// replica rotated) triggers a reload, at most once per RefreshInterval.
func (r *KMSRing) DataKey(ctx context.Context, scope Scope, version uint32) ([]byte, error) {
	k, err := r.VersionedRing.DataKey(ctx, scope, version)
	if !errors.Is(err, ErrUnknownKeyVersion) || version == 0 {
		return k, err
	}
	r.refreshMu.Lock()
	if r.cfg.Now().Sub(r.lastRefresh) >= r.cfg.RefreshInterval && !r.has(version) {
		r.lastRefresh = r.cfg.Now()
		_, lerr := r.load(ctx)
		if lerr != nil {
			r.refreshMu.Unlock()
			return nil, fmt.Errorf("%w: reload failed: %v", ErrUnknownKeyVersion, lerr)
		}
	}
	r.refreshMu.Unlock()
	return r.VersionedRing.DataKey(ctx, scope, version)
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
