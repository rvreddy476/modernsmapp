package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestRecordAndFindIdempotency_RoundTrip(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	// Unique per run: key is the table primary key, and other packages
	// (internal/service) write fixed keys into the same database.
	key := "it-" + uuid.NewString()
	txID := uuid.New()
	body := []byte(`{"transaction_id":"` + txID.String() + `"}`)

	if err := s.RecordIdempotency(ctx, key, uid, "top_up", &txID, body); err != nil {
		t.Fatalf("record: %v", err)
	}
	rec, err := s.FindIdempotency(ctx, key, uid, "top_up")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if rec.TransactionID == nil || *rec.TransactionID != txID {
		t.Fatalf("tx id round-trip failed")
	}
	// response_body is JSONB, which re-serialises the document (spacing,
	// key order); a replay returns the same JSON value, not the same bytes.
	if !sameJSON(t, rec.ResponseBody, body) {
		t.Fatalf("body round-trip failed")
	}
}

func TestRecordIdempotency_DuplicateIsNoop(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	// Unique per run: key is the table primary key, and other packages
	// (internal/service) write fixed keys into the same database.
	key := "it-" + uuid.NewString()
	if err := s.RecordIdempotency(ctx, key, uid, "top_up", nil, nil); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := s.RecordIdempotency(ctx, key, uid, "top_up", nil, nil); err != nil {
		t.Fatalf("second should be no-op: %v", err)
	}
}

func TestFindIdempotency_KeyNotFound(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	_, err := s.FindIdempotency(context.Background(), "missing-"+uuid.NewString(), uuid.New(), "top_up")
	if !errors.Is(err, ErrIdempotencyKeyNotFound) {
		t.Fatalf("expected key-not-found; got %v", err)
	}
}

func TestFindIdempotency_OperationMismatch(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	// Unique per run: key is the table primary key, and other packages
	// (internal/service) write fixed keys into the same database.
	key := "it-" + uuid.NewString()
	if err := s.RecordIdempotency(ctx, key, uid, "top_up", nil, nil); err != nil {
		t.Fatalf("record: %v", err)
	}
	if _, err := s.FindIdempotency(ctx, key, uid, "send"); !errors.Is(err, ErrIdempotencyMismatch) {
		t.Fatalf("expected mismatch; got %v", err)
	}
}

func TestFindIdempotency_UserMismatch(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	a := uuid.New()
	// Unique per run: key is the table primary key, and other packages
	// (internal/service) write fixed keys into the same database.
	key := "it-" + uuid.NewString()
	b := uuid.New()
	if err := s.RecordIdempotency(ctx, key, a, "top_up", nil, nil); err != nil {
		t.Fatalf("record: %v", err)
	}
	if _, err := s.FindIdempotency(ctx, key, b, "top_up"); !errors.Is(err, ErrIdempotencyMismatch) {
		t.Fatalf("expected mismatch when different user replays; got %v", err)
	}
}

func TestPurgeExpiredIdempotency(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	// Just verify it runs without error against the live schema.
	if _, err := s.PurgeExpiredIdempotency(context.Background()); err != nil {
		t.Fatalf("purge: %v", err)
	}
}

// sameJSON reports whether two JSON documents hold the same value.
func sameJSON(t *testing.T, got, want []byte) bool {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("stored body is not JSON: %v", err)
	}
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatalf("test body is not JSON: %v", err)
	}
	return reflect.DeepEqual(g, w)
}
