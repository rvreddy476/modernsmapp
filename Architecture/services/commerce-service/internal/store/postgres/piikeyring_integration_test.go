//go:build integration

package postgres

// The key ring against a REAL PostgreSQL.
//
// The in-memory ring in internal/pii models the uniqueness rule; this proves
// the database actually enforces it. That distinction matters here more than
// usual: the rule is what stops two pods each installing their own "version
// 1" and then writing rows the other cannot read, and a model that is more
// permissive than the schema would hide exactly that.

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func scopeName(t *testing.T) string {
	t.Helper()
	// A distinct scope per test: the ring is keyed by scope and these run
	// against a shared database.
	return "test_" + uuid.NewString()[:8]
}

func TestKeyRingStoresAndReadsBack(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	scope := scopeName(t)

	encCtx := map[string]string{"purpose": "commerce-pii", "scope": scope, "environment": "test"}
	version, err := store.CreatePIIKeyVersion(ctx, KeyRecord{
		Scope:             scope,
		WrappedKey:        []byte("wrapped-blob-1"),
		KMSKeyID:          "arn:aws:kms:ap-south-1:1:key/abc",
		EncryptionContext: encCtx,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if version != 1 {
		t.Fatalf("first version = %d, want 1", version)
	}

	rec, err := store.ActivePIIKey(ctx, scope)
	if err != nil {
		t.Fatalf("read active: %v", err)
	}
	if string(rec.WrappedKey) != "wrapped-blob-1" {
		t.Fatalf("wrapped key = %q, want wrapped-blob-1", rec.WrappedKey)
	}
	// The encryption context must survive the round trip exactly: KMS
	// requires the SAME map at Decrypt, so a lossy store makes every key
	// unusable.
	if rec.EncryptionContext["scope"] != scope ||
		rec.EncryptionContext["environment"] != "test" ||
		rec.EncryptionContext["purpose"] != "commerce-pii" {
		t.Fatalf("encryption context did not round-trip: %v", rec.EncryptionContext)
	}
}

func TestKeyRingReportsNoActiveKeyForAFreshScope(t *testing.T) {
	ctx := context.Background()
	if _, err := New(testPool).ActivePIIKey(ctx, scopeName(t)); !errors.Is(err, ErrNoActiveKey) {
		t.Fatalf("got %v, want ErrNoActiveKey — a first run is not a fault", err)
	}
}

// THE rule. A second Create must be refused, not silently rotate: the caller
// is holding a key KMS just generated, and letting it take over would leave
// whoever wrote first with unreadable rows.
func TestKeyRingRefusesASecondActiveKey(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	scope := scopeName(t)

	first, err := store.CreatePIIKeyVersion(ctx, KeyRecord{
		Scope: scope, WrappedKey: []byte("first"), KMSKeyID: "k",
		EncryptionContext: map[string]string{"scope": scope},
	})
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	existing, err := store.CreatePIIKeyVersion(ctx, KeyRecord{
		Scope: scope, WrappedKey: []byte("second"), KMSKeyID: "k",
		EncryptionContext: map[string]string{"scope": scope},
	})
	if !errors.Is(err, ErrActiveKeyExists) {
		t.Fatalf("got %v, want ErrActiveKeyExists", err)
	}
	if existing != first {
		t.Fatalf("refusal reported version %d, want the existing %d", existing, first)
	}

	// And the original is untouched.
	rec, err := store.ActivePIIKey(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if string(rec.WrappedKey) != "first" {
		t.Fatalf("active wrapped key = %q; the loser overwrote the winner", rec.WrappedKey)
	}
}

// Rotation installs a new version AND leaves the old one readable — that is
// the entire reason the ring exists.
func TestRotationRetiresButKeepsTheOldKeyReadable(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	scope := scopeName(t)

	v1, err := store.CreatePIIKeyVersion(ctx, KeyRecord{
		Scope: scope, WrappedKey: []byte("old"), KMSKeyID: "k",
		EncryptionContext: map[string]string{"scope": scope},
	})
	if err != nil {
		t.Fatal(err)
	}
	v2, err := store.RotatePIIKey(ctx, KeyRecord{
		Scope: scope, WrappedKey: []byte("new"), KMSKeyID: "k",
		EncryptionContext: map[string]string{"scope": scope},
	})
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if v2 != v1+1 {
		t.Fatalf("rotated to %d, want %d", v2, v1+1)
	}

	active, err := store.ActivePIIKey(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if active.Version != v2 || string(active.WrappedKey) != "new" {
		t.Fatalf("active = v%d %q, want v%d new", active.Version, active.WrappedKey, v2)
	}

	// The retired key MUST still be retrievable. Losing it here is losing
	// every row written while it was current.
	old, err := store.PIIKeyByVersion(ctx, scope, v1)
	if err != nil {
		t.Fatalf("reading the retired key: %v — every value sealed under it is now lost", err)
	}
	if string(old.WrappedKey) != "old" {
		t.Fatalf("retired wrapped key = %q, want old", old.WrappedKey)
	}
}

func TestUnknownVersionIsReported(t *testing.T) {
	ctx := context.Background()
	if _, err := New(testPool).PIIKeyByVersion(ctx, scopeName(t), 7); !errors.Is(err, ErrKeyVersionNotFound) {
		t.Fatalf("got %v, want ErrKeyVersionNotFound", err)
	}
}

// Concurrent first-use against the real database: exactly one key is
// installed and every caller converges on it.
func TestConcurrentCreateInstallsExactlyOneKey(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	scope := scopeName(t)

	const n = 8
	var wg sync.WaitGroup
	created := make([]int, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, err := store.CreatePIIKeyVersion(ctx, KeyRecord{
				Scope:             scope,
				WrappedKey:        []byte("blob"),
				KMSKeyID:          "k",
				EncryptionContext: map[string]string{"scope": scope},
			})
			created[i], errs[i] = v, err
		}(i)
	}
	wg.Wait()

	winners := 0
	for i := range errs {
		switch {
		case errs[i] == nil:
			winners++
		case errors.Is(errs[i], ErrActiveKeyExists):
			// Correct: discarded its key and will re-read the winner.
		default:
			t.Fatalf("goroutine %d: unexpected error %v", i, errs[i])
		}
	}
	if winners != 1 {
		t.Fatalf("%d creators succeeded, want exactly 1 — each would have written rows the "+
			"others cannot read", winners)
	}

	var rows int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM pii_key_ring WHERE scope = $1`, scope).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("key ring holds %d rows for %s, want 1", rows, scope)
	}
}

// The schema itself must refuse a second live row, independently of the
// application's check. This is the negative control for the partial unique
// index: bypass CreatePIIKeyVersion and insert directly.
func TestPartialUniqueIndexRefusesTwoLiveRows(t *testing.T) {
	ctx := context.Background()
	scope := scopeName(t)

	for _, v := range []int{1, 2} {
		_, err := testPool.Exec(ctx, `
			INSERT INTO pii_key_ring (scope, version, wrapped_key, kms_key_id, encryption_context)
			VALUES ($1,$2,$3,'k','{}'::jsonb)`, scope, v, []byte("blob"))
		if v == 1 && err != nil {
			t.Fatalf("first direct insert: %v", err)
		}
		if v == 2 && err == nil {
			t.Fatal("the database accepted a second LIVE key for one scope; the partial unique " +
				"index is not enforcing the rule the application relies on")
		}
	}
}
