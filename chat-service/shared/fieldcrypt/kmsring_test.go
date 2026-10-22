package fieldcrypt

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeKMS wraps by XOR with a fixed pad and checks the context like KMS does.
type fakeKMS struct {
	mu        sync.Mutex
	generated int
	unwrapped int
	failGen   error
}

func ctxString(m map[string]string) string {
	return m[KeyContextPurpose] + "|" + m[KeyContextScope] + "|" + m[KeyContextEnvironment]
}

func (f *fakeKMS) GenerateDataKey(_ context.Context, kc map[string]string) ([]byte, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failGen != nil {
		return nil, nil, f.failGen
	}
	f.generated++
	plain := make([]byte, DataKeySize)
	_, _ = rand.Read(plain)
	wrapped := append([]byte(ctxString(kc)+"#"), plain...)
	for i := range wrapped {
		wrapped[i] ^= 0x5c
	}
	return plain, wrapped, nil
}

func (f *fakeKMS) Unwrap(_ context.Context, wrapped []byte, kc map[string]string) ([]byte, error) {
	f.mu.Lock()
	f.unwrapped++
	f.mu.Unlock()
	raw := append([]byte(nil), wrapped...)
	for i := range raw {
		raw[i] ^= 0x5c
	}
	head := []byte(ctxString(kc) + "#")
	if !bytes.HasPrefix(raw, head) {
		return nil, errors.New("fake kms: encryption context mismatch")
	}
	return raw[len(head):], nil
}

type memKeyStore struct {
	mu    sync.Mutex
	keys  []WrappedKey
	lists int
	// beforeCreate simulates another replica minting first.
	beforeCreate func(s *memKeyStore)
}

func (m *memKeyStore) ListWrappedKeys(context.Context) ([]WrappedKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lists++
	return append([]WrappedKey(nil), m.keys...), nil
}

func (m *memKeyStore) CreateFirstWrappedKey(_ context.Context, k WrappedKey) error {
	if m.beforeCreate != nil {
		m.beforeCreate(m)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.keys) > 0 {
		return fmt.Errorf("insert: %w", ErrActiveKeyExists)
	}
	k.Version = 1
	m.keys = append(m.keys, k)
	return nil
}

func (m *memKeyStore) RotateWrappedKey(_ context.Context, k WrappedKey) (uint32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k.Version = uint32(len(m.keys) + 1)
	m.keys = append(m.keys, k)
	return k.Version, nil
}

func kmsCfg(w KeyWrapper, s WrappedKeyStore, env string) KMSRingConfig {
	return KMSRingConfig{Wrapper: w, Store: s, KMSKeyID: "alias/chat-field", Purpose: "chat-field", RingScope: "chat.field", Environment: env}
}

func TestKMSRingMintsPersistsAndReopens(t *testing.T) {
	ctx := context.Background()
	kms, store := &fakeKMS{}, &memKeyStore{}
	r1, err := OpenKMSRing(ctx, kmsCfg(kms, store, "production"))
	if err != nil {
		t.Fatal(err)
	}
	if kms.generated != 1 || len(store.keys) != 1 || store.keys[0].Version != 1 {
		t.Fatalf("first boot must mint and persist exactly one key: generated=%d stored=%d", kms.generated, len(store.keys))
	}
	if store.keys[0].Context[KeyContextEnvironment] != "production" {
		t.Fatal("the encryption context is not stored with the key")
	}
	c1, _ := New(r1)
	sealed, err := c1.SealString(ctx, "chat.message.text", Bind("c", "m"), "hello")
	if err != nil {
		t.Fatal(err)
	}

	// A restart (or another replica) unwraps the stored key; no new mint.
	r2, err := OpenKMSRing(ctx, kmsCfg(kms, store, "production"))
	if err != nil {
		t.Fatal(err)
	}
	if kms.generated != 1 {
		t.Fatal("a reopen minted a new key")
	}
	c2, _ := New(r2)
	if got, err := c2.OpenString(ctx, "chat.message.text", Bind("c", "m"), sealed); err != nil || got != "hello" {
		t.Fatalf("reopened ring: %q, %v", got, err)
	}
}

func TestKMSRingAdoptsTheWinnerOfTheFirstMint(t *testing.T) {
	ctx := context.Background()
	kms := &fakeKMS{}
	store := &memKeyStore{}
	var winner *KMSRing
	store.beforeCreate = func(s *memKeyStore) {
		store.beforeCreate = nil
		var err error
		winner, err = OpenKMSRing(ctx, kmsCfg(kms, s, "staging"))
		if err != nil {
			t.Fatal(err)
		}
	}
	loser, err := OpenKMSRing(ctx, kmsCfg(kms, store, "staging"))
	if err != nil {
		t.Fatal(err)
	}
	if len(store.keys) != 1 {
		t.Fatalf("two keys persisted for one version: %d", len(store.keys))
	}
	cw, _ := New(winner)
	cl, _ := New(loser)
	sealed, _ := cw.SealString(ctx, "chat.note.text", Bind("n"), "x")
	if got, err := cl.OpenString(ctx, "chat.note.text", Bind("n"), sealed); err != nil || got != "x" {
		t.Fatalf("the race loser cannot read the winner's values: %v", err)
	}
}

func TestKMSRingRefusesAnotherEnvironmentsKeys(t *testing.T) {
	ctx := context.Background()
	kms, store := &fakeKMS{}, &memKeyStore{}
	if _, err := OpenKMSRing(ctx, kmsCfg(kms, store, "staging")); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenKMSRing(ctx, kmsCfg(kms, store, "production")); !errors.Is(err, ErrKeyContextMismatch) {
		t.Fatalf("a production ring opened staging's keys: %v", err)
	}
}

func TestKMSRingFailsClosed(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("kms unavailable")
	if _, err := OpenKMSRing(ctx, kmsCfg(&fakeKMS{failGen: boom}, &memKeyStore{}, "production")); !errors.Is(err, boom) {
		t.Fatalf("mint failure: %v", err)
	}
	for name, cfg := range map[string]KMSRingConfig{
		"no_wrapper": kmsCfg(nil, &memKeyStore{}, "production"),
		"no_store":   kmsCfg(&fakeKMS{}, nil, "production"),
		"no_env":     kmsCfg(&fakeKMS{}, &memKeyStore{}, ""),
	} {
		if r, err := OpenKMSRing(ctx, cfg); err == nil || r != nil {
			t.Fatalf("%s: opened a ring", name)
		}
	}
}

// A replica that booted before a rotation meets a value sealed under the new
// version and reloads the ring, at most once per refresh interval.
func TestKMSRingReloadsOnUnknownVersion(t *testing.T) {
	ctx := context.Background()
	kms, store := &fakeKMS{}, &memKeyStore{}
	now := time.Unix(1000, 0)
	cfg := kmsCfg(kms, store, "production")
	cfg.Now = func() time.Time { return now }
	old, err := OpenKMSRing(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	rotator, _ := OpenKMSRing(ctx, cfg)
	v, err := rotator.Rotate(ctx)
	if err != nil || v != 2 {
		t.Fatalf("Rotate = %d, %v", v, err)
	}
	cr, _ := New(rotator)
	sealed, _ := cr.SealString(ctx, "chat.message.text", Bind("c", "m"), "after rotation")
	if kv, _ := SealedKeyVersion(sealed); kv != 2 {
		t.Fatalf("rotated ring sealed under v%d", kv)
	}
	co, _ := New(old)
	if got, err := co.OpenString(ctx, "chat.message.text", Bind("c", "m"), sealed); err != nil || got != "after rotation" {
		t.Fatalf("old replica: %q, %v", got, err)
	}
	lists := store.lists
	// A bogus version does not hammer the store: one reload per interval.
	for i := 0; i < 5; i++ {
		if _, err := old.DataKey(ctx, "chat.message.text", 9); !errors.Is(err, ErrUnknownKeyVersion) {
			t.Fatalf("unknown version: %v", err)
		}
	}
	if store.lists != lists {
		t.Fatalf("unknown versions reloaded the store %d times inside one refresh interval", store.lists-lists)
	}
	now = now.Add(time.Minute)
	_, _ = old.DataKey(ctx, "chat.message.text", 9)
	if store.lists != lists+1 {
		t.Fatal("an unknown version after the interval did not reload the store")
	}
}
