package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestRecordAndFindIdempotency_RoundTrip(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	txID := uuid.New()
	body := []byte(`{"transaction_id":"` + txID.String() + `"}`)

	if err := s.RecordIdempotency(ctx, "k1", uid, "top_up", &txID, body); err != nil {
		t.Fatalf("record: %v", err)
	}
	rec, err := s.FindIdempotency(ctx, "k1", uid, "top_up")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if rec.TransactionID == nil || *rec.TransactionID != txID {
		t.Fatalf("tx id round-trip failed")
	}
	if string(rec.ResponseBody) != string(body) {
		t.Fatalf("body round-trip failed")
	}
}

func TestRecordIdempotency_DuplicateIsNoop(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	if err := s.RecordIdempotency(ctx, "k-dup", uid, "top_up", nil, nil); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := s.RecordIdempotency(ctx, "k-dup", uid, "top_up", nil, nil); err != nil {
		t.Fatalf("second should be no-op: %v", err)
	}
}

func TestFindIdempotency_KeyNotFound(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	_, err := s.FindIdempotency(context.Background(), "missing", uuid.New(), "top_up")
	if !errors.Is(err, ErrIdempotencyKeyNotFound) {
		t.Fatalf("expected key-not-found; got %v", err)
	}
}

func TestFindIdempotency_OperationMismatch(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	if err := s.RecordIdempotency(ctx, "k-op", uid, "top_up", nil, nil); err != nil {
		t.Fatalf("record: %v", err)
	}
	if _, err := s.FindIdempotency(ctx, "k-op", uid, "send"); !errors.Is(err, ErrIdempotencyMismatch) {
		t.Fatalf("expected mismatch; got %v", err)
	}
}

func TestFindIdempotency_UserMismatch(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	a := uuid.New()
	b := uuid.New()
	if err := s.RecordIdempotency(ctx, "k-user", a, "top_up", nil, nil); err != nil {
		t.Fatalf("record: %v", err)
	}
	if _, err := s.FindIdempotency(ctx, "k-user", b, "top_up"); !errors.Is(err, ErrIdempotencyMismatch) {
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
