package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestRecordAndFindIdempotency_RoundTrip(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	resID := uuid.New()
	body := []byte(`{"resource_id":"` + resID.String() + `"}`)
	hash := "testhash123"

	if err := s.RecordIdempotency(ctx, "k1", uid, "subscribe", hash, &resID, body); err != nil {
		t.Fatalf("record: %v", err)
	}
	rec, err := s.FindIdempotency(ctx, "k1", uid, "subscribe", hash)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if rec.ResourceID == nil || *rec.ResourceID != resID {
		t.Fatalf("resource id round-trip failed")
	}
	var gotBody map[string]any
	if err := json.Unmarshal(rec.ResponseBody, &gotBody); err != nil || gotBody["resource_id"] != resID.String() {
		t.Fatalf("body round-trip failed: got %s", rec.ResponseBody)
	}
}

func TestRecordIdempotency_DuplicateIsNoop(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	hash := "testhash123"
	if err := s.RecordIdempotency(ctx, "k-dup", uid, "subscribe", hash, nil, nil); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := s.RecordIdempotency(ctx, "k-dup", uid, "subscribe", hash, nil, nil); err != nil {
		t.Fatalf("second should be no-op: %v", err)
	}
}

func TestFindIdempotency_KeyNotFound(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	_, err := s.FindIdempotency(context.Background(), "missing", uuid.New(), "subscribe", "hash")
	if !errors.Is(err, ErrIdempotencyKeyNotFound) {
		t.Fatalf("expected key-not-found; got %v", err)
	}
}

func TestFindIdempotency_OperationMismatch(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	hash := "testhash123"
	if err := s.RecordIdempotency(ctx, "k-op", uid, "subscribe", hash, nil, nil); err != nil {
		t.Fatalf("record: %v", err)
	}
	if _, err := s.FindIdempotency(ctx, "k-op", uid, "ride_create", hash); !errors.Is(err, ErrIdempotencyMismatch) {
		t.Fatalf("expected mismatch; got %v", err)
	}
}

func TestFindIdempotency_UserMismatch(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	a := uuid.New()
	b := uuid.New()
	hash := "testhash123"
	if err := s.RecordIdempotency(ctx, "k-user", a, "subscribe", hash, nil, nil); err != nil {
		t.Fatalf("record: %v", err)
	}
	if _, err := s.FindIdempotency(ctx, "k-user", b, "subscribe", hash); !errors.Is(err, ErrIdempotencyMismatch) {
		t.Fatalf("expected mismatch when different user replays; got %v", err)
	}
}

func TestFindIdempotency_PayloadMismatch(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	if err := s.RecordIdempotency(ctx, "k-payload", uid, "ride_create", "hash_a", nil, nil); err != nil {
		t.Fatalf("record: %v", err)
	}
	if _, err := s.FindIdempotency(ctx, "k-payload", uid, "ride_create", "hash_b"); !errors.Is(err, ErrIdempotencyMismatch) {
		t.Fatalf("expected mismatch when request payload differs; got %v", err)
	}
}

func TestPurgeExpiredIdempotency_Smoke(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	if _, err := s.PurgeExpiredIdempotency(context.Background()); err != nil {
		t.Fatalf("purge: %v", err)
	}
}
