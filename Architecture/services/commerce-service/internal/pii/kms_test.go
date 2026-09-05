package pii

// The production key provider, driven by a fake KMS and an in-memory ring.
//
// Review 2 §4.5 named the defect these exist to prevent: `DataKey(scope,
// version)` must return the SAME bytes for a given version, while KMS
// `GenerateDataKey` returns different bytes every call. A provider that did
// not persist the wrapped blob would make every row written under an earlier
// version permanently undecryptable — silently, and only discovered when
// someone opened an old address.
//
// So the tests below are mostly about identity across time: seal now, rotate,
// and still open. Plus the refusals that keep a wrong key from being used in
// place of a missing one.

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
)

// ─── Fakes ───────────────────────────────────────────────────────────

// fakeKMS models the property that matters: a wrapped blob is the ONLY way
// back to its plaintext, and the encryption context is enforced.
type fakeKMS struct {
	mu sync.Mutex
	// vault maps a wrapped blob to what it wraps, exactly as KMS does
	// server-side. Nothing here can regenerate a key from a version number,
	// which is the whole point.
	vault map[string]kmsEntry
	seq   int

	generateErr error
	decryptErr  error
	// shortKey makes GenerateDataKey return the wrong size, to prove the
	// provider checks rather than trusting.
	shortKey bool
}

type kmsEntry struct {
	plaintext []byte
	encCtx    map[string]string
	keyID     string
}

func newFakeKMS() *fakeKMS { return &fakeKMS{vault: map[string]kmsEntry{}} }

func (f *fakeKMS) GenerateDataKey(_ context.Context, keyID string, encCtx map[string]string) ([]byte, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.generateErr != nil {
		return nil, nil, f.generateErr
	}
	size := DataKeySize
	if f.shortKey {
		size = 16
	}
	plaintext := make([]byte, size)
	if _, err := io.ReadFull(rand.Reader, plaintext); err != nil {
		return nil, nil, err
	}
	f.seq++
	wrapped := []byte(fmt.Sprintf("wrapped-%d", f.seq))
	f.vault[string(wrapped)] = kmsEntry{
		plaintext: append([]byte(nil), plaintext...),
		encCtx:    copyCtx(encCtx),
		keyID:     keyID,
	}
	return plaintext, wrapped, nil
}

func (f *fakeKMS) Decrypt(_ context.Context, wrapped []byte, encCtx map[string]string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.decryptErr != nil {
		return nil, f.decryptErr
	}
	entry, ok := f.vault[string(wrapped)]
	if !ok {
		return nil, errors.New("kms: unknown ciphertext blob")
	}
	// KMS enforces the encryption context on Decrypt. A mismatch is an
	// error, not a warning — that is what makes the context a real boundary
	// rather than a label.
	if !sameCtx(entry.encCtx, encCtx) {
		return nil, errors.New("kms: encryption context mismatch")
	}
	return append([]byte(nil), entry.plaintext...), nil
}

func copyCtx(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func sameCtx(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// memRing is an in-memory KeyRing with the same uniqueness rule as the
// database: at most one live version per scope.
type memRing struct {
	mu   sync.Mutex
	rows map[string][]ringRow
	// createErr forces the persist step to fail, to prove a key that was
	// not stored is never handed out.
	createErr error
}

type ringRow struct {
	version int
	wrapped []byte
	keyID   string
	encCtx  map[string]string
	retired bool
}

func newMemRing() *memRing { return &memRing{rows: map[string][]ringRow{}} }

func (r *memRing) Active(_ context.Context, scope string) (int, []byte, map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, row := range r.rows[scope] {
		if !row.retired {
			return row.version, row.wrapped, row.encCtx, nil
		}
	}
	return 0, nil, nil, ErrNoActiveKey
}

func (r *memRing) ByVersion(_ context.Context, scope string, version int) ([]byte, map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, row := range r.rows[scope] {
		if row.version == version {
			return row.wrapped, row.encCtx, nil
		}
	}
	return nil, nil, fmt.Errorf("no key %s v%d", scope, version)
}

// Create models the database's partial unique index on
// (scope) WHERE retired_at IS NULL: at most one live version, so a second
// creator is REFUSED rather than silently rotating on top. Without this the
// fake was more permissive than the real store, and the concurrency test
// passed against a ring that could not exist.
func (r *memRing) Create(_ context.Context, scope string, wrapped []byte, keyID string, encCtx map[string]string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.createErr != nil {
		return 0, r.createErr
	}
	for _, row := range r.rows[scope] {
		if !row.retired {
			return row.version, ErrActiveKeyExists
		}
	}
	return r.insertLocked(scope, wrapped, keyID, encCtx), nil
}

func (r *memRing) Rotate(_ context.Context, scope string, wrapped []byte, keyID string, encCtx map[string]string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.createErr != nil {
		return 0, r.createErr
	}
	return r.insertLocked(scope, wrapped, keyID, encCtx), nil
}

func (r *memRing) insertLocked(scope string, wrapped []byte, keyID string, encCtx map[string]string) int {
	next := len(r.rows[scope]) + 1
	for i := range r.rows[scope] {
		r.rows[scope][i].retired = true
	}
	r.rows[scope] = append(r.rows[scope], ringRow{
		version: next, wrapped: wrapped, keyID: keyID, encCtx: copyCtx(encCtx),
	})
	return next
}

func newProvider(t *testing.T) (*KMSKeyProvider, *fakeKMS, *memRing) {
	t.Helper()
	kms, ring := newFakeKMS(), newMemRing()
	p, err := NewKMSKeyProvider(kms, ring, "arn:aws:kms:ap-south-1:1:key/abc", "prod")
	if err != nil {
		t.Fatalf("building provider: %v", err)
	}
	return p, kms, ring
}

// ─── The identity property ───────────────────────────────────────────

// THE test. Seal a value, rotate the key, and still open it — because the
// ring kept the wrapped blob for the version the ciphertext names.
func TestValueSealedBeforeRotationStillOpensAfter(t *testing.T) {
	ctx := context.Background()
	p, _, _ := newProvider(t)
	cipher, err := New(p, []byte("a-lookup-salt-at-least-16"))
	if err != nil {
		t.Fatal(err)
	}

	const address = "5 Main St, Bengaluru"
	blob, v1, err := cipher.Seal(ctx, ScopeProfile, address)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if v1 != 1 {
		t.Fatalf("first version = %d, want 1", v1)
	}

	v2, err := p.Rotate(ctx, ScopeProfile)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if v2 != 2 {
		t.Fatalf("rotated to version %d, want 2", v2)
	}

	// A fresh cipher, so nothing is served from the in-process cache — this
	// has to come back through the ring and KMS.
	fresh, err := New(p, []byte("a-lookup-salt-at-least-16"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := fresh.Open(ctx, ScopeProfile, blob)
	if err != nil {
		t.Fatalf("opening a pre-rotation value: %v — this is the defect the key ring exists "+
			"to prevent: the row is now undecryptable", err)
	}
	if got != address {
		t.Fatalf("opened %q, want %q", got, address)
	}
}

// After rotation, NEW writes use the new version. Old rows keep theirs.
func TestRotationChangesOnlyNewWrites(t *testing.T) {
	ctx := context.Background()
	p, _, _ := newProvider(t)
	cipher, _ := New(p, []byte("a-lookup-salt-at-least-16"))

	_, before, err := cipher.Seal(ctx, ScopeProfile, "old")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Rotate(ctx, ScopeProfile); err != nil {
		t.Fatal(err)
	}
	fresh, _ := New(p, []byte("a-lookup-salt-at-least-16"))
	_, after, err := fresh.Seal(ctx, ScopeProfile, "new")
	if err != nil {
		t.Fatal(err)
	}
	if after <= before {
		t.Fatalf("post-rotation write used version %d, want greater than %d", after, before)
	}
}

// Two calls for the same version return the SAME bytes. If this ever fails,
// the provider is regenerating rather than unwrapping.
func TestSameVersionAlwaysYieldsTheSameKey(t *testing.T) {
	ctx := context.Background()
	p, _, _ := newProvider(t)

	first, v, err := p.DataKey(ctx, ScopeProfile, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := p.DataKey(ctx, ScopeProfile, v)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("the same version produced different key bytes; every row written under it " +
			"would be undecryptable")
	}
}

// ─── Scope and environment separation ────────────────────────────────

// D8: profile and order-snapshot are separate retention classes, so they must
// be separate keys — one can be shredded without destroying the other.
func TestScopesGetDistinctKeys(t *testing.T) {
	ctx := context.Background()
	p, _, _ := newProvider(t)

	profile, _, err := p.DataKey(ctx, ScopeProfile, 0)
	if err != nil {
		t.Fatal(err)
	}
	order, _, err := p.DataKey(ctx, ScopeOrderSnapshot, 0)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(profile, order) {
		t.Fatal("both scopes share a key; shredding one would destroy the other")
	}
}

// A ciphertext moved between scope columns must not open. The Cipher binds
// the scope into the AEAD additional data, so this is enforced by the crypto
// rather than by a check that could be forgotten.
func TestCiphertextDoesNotOpenUnderAnotherScope(t *testing.T) {
	ctx := context.Background()
	p, _, _ := newProvider(t)
	cipher, _ := New(p, []byte("a-lookup-salt-at-least-16"))

	// Seed BOTH scopes first, so the other scope genuinely has a version 1
	// key. Without this the open fails merely because the key is missing,
	// which proves nothing about the AEAD binding — the interesting case is
	// a ciphertext moved between two columns that BOTH have keys.
	if _, _, err := cipher.Seal(ctx, ScopeOrderSnapshot, "seed"); err != nil {
		t.Fatal(err)
	}

	blob, version, err := cipher.Seal(ctx, ScopeProfile, "5 Main St")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.DataKey(ctx, ScopeOrderSnapshot, version); err != nil {
		t.Fatalf("the other scope must hold version %d for this test to mean anything: %v",
			version, err)
	}

	// Now the key exists and it is the AEAD's additional data — the scope —
	// that refuses.
	if _, err := cipher.Open(ctx, ScopeOrderSnapshot, blob); !errors.Is(err, ErrBadCiphertext) {
		t.Fatalf("got %v, want ErrBadCiphertext for a cross-scope open", err)
	}
}

// A ring row whose stored context names another scope is refused before KMS
// is even asked. KMS would refuse it too; this is the defence-in-depth layer
// that catches a tampered or mis-written ring.
func TestRingRowWithMismatchedScopeIsRefused(t *testing.T) {
	ctx := context.Background()
	p, kms, ring := newProvider(t)

	// Persist a key whose stored context claims a different scope.
	_, wrapped, err := kms.GenerateDataKey(ctx, "k", map[string]string{
		"purpose": "commerce-pii", "scope": "order_snapshot", "environment": "prod",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ring.Create(ctx, string(ScopeProfile), wrapped, "k", map[string]string{
		"purpose": "commerce-pii", "scope": "order_snapshot", "environment": "prod",
	}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := p.DataKey(ctx, ScopeProfile, 0); !errors.Is(err, ErrWrongScope) {
		t.Fatalf("got %v, want ErrWrongScope", err)
	}
}

// A key created in another environment must not unwrap here, even with CMK
// access — staging data and production data are different blast radii.
func TestKeyFromAnotherEnvironmentIsRefused(t *testing.T) {
	ctx := context.Background()
	kms, ring := newFakeKMS(), newMemRing()

	staging, err := NewKMSKeyProvider(kms, ring, "k", "staging")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := staging.DataKey(ctx, ScopeProfile, 0); err != nil {
		t.Fatal(err)
	}

	production, err := NewKMSKeyProvider(kms, ring, "k", "prod")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := production.DataKey(ctx, ScopeProfile, 0); !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("got %v, want ErrKeyUnavailable for a cross-environment key", err)
	}
}

// ─── Tamper and failure posture ──────────────────────────────────────

func TestTamperedCiphertextIsRefused(t *testing.T) {
	ctx := context.Background()
	p, _, _ := newProvider(t)
	cipher, _ := New(p, []byte("a-lookup-salt-at-least-16"))

	blob, _, err := cipher.Seal(ctx, ScopeProfile, "5 Main St")
	if err != nil {
		t.Fatal(err)
	}
	blob[len(blob)-1] ^= 0xFF // flip a bit in the tag

	if _, err := cipher.Open(ctx, ScopeProfile, blob); !errors.Is(err, ErrBadCiphertext) {
		t.Fatalf("got %v, want ErrBadCiphertext for tampered input", err)
	}
}

// A ciphertext naming a version the ring does not hold must fail, NOT fall
// back to the current key. Falling back would hand back bytes that cannot
// open the value, and the AEAD failure would look like corruption.
func TestUnknownVersionFailsRatherThanFallingBack(t *testing.T) {
	ctx := context.Background()
	p, _, _ := newProvider(t)
	if _, _, err := p.DataKey(ctx, ScopeProfile, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.DataKey(ctx, ScopeProfile, 99); !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("got %v, want ErrKeyUnavailable for a version the ring does not hold", err)
	}
}

// A KMS outage is fatal for the operation. There is no degraded mode: a
// fallback key would encrypt real addresses under something that disappears.
func TestKMSFailureIsFatalNotDegraded(t *testing.T) {
	ctx := context.Background()
	p, kms, _ := newProvider(t)
	kms.generateErr = errors.New("kms unavailable")

	if _, _, err := p.DataKey(ctx, ScopeProfile, 0); !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("got %v, want ErrKeyUnavailable", err)
	}
}

func TestDecryptFailureIsFatal(t *testing.T) {
	ctx := context.Background()
	p, kms, _ := newProvider(t)
	if _, _, err := p.DataKey(ctx, ScopeProfile, 0); err != nil {
		t.Fatal(err)
	}
	kms.decryptErr = errors.New("access denied")

	fresh, _, _ := newProvider(t)
	_ = fresh
	if _, _, err := p.DataKey(ctx, ScopeProfile, 1); !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("got %v, want ErrKeyUnavailable when the CMK grant is denied", err)
	}
}

// A key that could not be PERSISTED is never handed out. It has encrypted
// nothing at that point, so discarding it costs nothing; using it would
// produce rows nobody can read.
func TestKeyThatCannotBePersistedIsNeverUsed(t *testing.T) {
	ctx := context.Background()
	kms, ring := newFakeKMS(), newMemRing()
	ring.createErr = errors.New("disk full")
	p, err := NewKMSKeyProvider(kms, ring, "k", "prod")
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := p.DataKey(ctx, ScopeProfile, 0); !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("got %v, want ErrKeyUnavailable when the ring write fails", err)
	}
}

// A wrong-sized key from KMS is refused rather than used with AES-256.
func TestWrongSizedKeyIsRefused(t *testing.T) {
	ctx := context.Background()
	p, kms, _ := newProvider(t)
	kms.shortKey = true

	if _, _, err := p.DataKey(ctx, ScopeProfile, 0); !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("got %v, want ErrKeyUnavailable for a 16-byte key", err)
	}
}

// ─── Construction refuses an unsafe provider ─────────────────────────

func TestProviderRefusesIncompleteConfiguration(t *testing.T) {
	kms, ring := newFakeKMS(), newMemRing()
	cases := map[string]struct {
		kms   KMSClient
		ring  KeyRing
		keyID string
		env   string
	}{
		"no kms client": {nil, ring, "k", "prod"},
		"no key ring":   {kms, nil, "k", "prod"},
		"no cmk id":     {kms, ring, "", "prod"},
		"no environment": {kms, ring, "k", ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewKMSKeyProvider(tc.kms, tc.ring, tc.keyID, tc.env); err == nil {
				t.Fatal("an incompletely configured provider must not be constructible")
			}
		})
	}
}

// A concurrent first-use must converge on ONE key. Two pods each holding a
// different key would each write rows the other cannot read.
func TestConcurrentFirstUseConvergesOnOneKey(t *testing.T) {
	ctx := context.Background()
	p, _, _ := newProvider(t)

	const n = 8
	keys := make([][]byte, n)
	versions := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			k, v, err := p.DataKey(ctx, ScopeProfile, 0)
			if err != nil {
				t.Errorf("goroutine %d: %v", i, err)
				return
			}
			keys[i], versions[i] = k, v
		}(i)
	}
	wg.Wait()

	for i := 1; i < n; i++ {
		if versions[i] != versions[0] {
			t.Fatalf("callers saw versions %d and %d; concurrent first use must converge",
				versions[0], versions[i])
		}
		if !bytes.Equal(keys[i], keys[0]) {
			t.Fatal("callers got different key bytes for the same version")
		}
	}
}
