package store

import (
	"context"
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

	if err := s.RecordIdempotency(ctx, "k1", uid, "subscribe", &resID, body); err != nil {
		t.Fatalf("record: %v", err)
	}
	rec, err := s.FindIdempotency(ctx, "k1", uid, "subscribe")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if rec.ResourceID == nil || *rec.ResourceID != resID {
		t.Fatalf("resource id round-trip failed")
	}
	if string(rec.ResponseBody) != string(body) {
		t.Fatalf("body round-trip failed")
	}
}

func TestRecordIdempotency_DuplicateIsNoop(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	if err := s.RecordIdempotency(ctx, "k-dup", uid, "subscribe", nil, nil); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := s.RecordIdempotency(ctx, "k-dup", uid, "subscribe", nil, nil); err != nil {
		t.Fatalf("second should be no-op: %v", err)
	}
}

func TestFindIdempotency_KeyNotFound(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	_, err := s.FindIdempotency(context.Background(), "missing", uuid.New(), "subscribe")
	if !errors.Is(err, ErrIdempotencyKeyNotFound) {
		t.Fatalf("expected key-not-found; got %v", err)
	}
}

func TestFindIdempotency_OperationMismatch(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	if err := s.RecordIdempotency(ctx, "k-op", uid, "subscribe", nil, nil); err != nil {
		t.Fatalf("record: %v", err)
	}
	if _, err := s.FindIdempotency(ctx, "k-op", uid, "ride_create"); !errors.Is(err, ErrIdempotencyMismatch) {
		t.Fatalf("expected mismatch; got %v", err)
	}
}

func TestFindIdempotency_UserMismatch(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	a := uuid.New()
	b := uuid.New()
	if err := s.RecordIdempotency(ctx, "k-user", a, "subscribe", nil, nil); err != nil {
		t.Fatalf("record: %v", err)
	}
	if _, err := s.FindIdempotency(ctx, "k-user", b, "subscribe"); !errors.Is(err, ErrIdempotencyMismatch) {
		t.Fatalf("expected mismatch when different user replays; got %v", err)
	}
}

func TestPurgeExpiredIdempotency_Smoke(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	if _, err := s.PurgeExpiredIdempotency(context.Background()); err != nil {
		t.Fatalf("purge: %v", err)
	}
}
