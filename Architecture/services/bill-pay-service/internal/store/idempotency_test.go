package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestRecordAndFindIdempotency_RoundTrip(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	pid := uuid.New()
	body := []byte(`{"payment_id":"` + pid.String() + `"}`)

	if err := s.RecordIdempotency(ctx, "k1", uid, &pid, body); err != nil {
		t.Fatalf("record: %v", err)
	}
	rec, err := s.FindIdempotency(ctx, "k1", uid)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if rec.PaymentID == nil || *rec.PaymentID != pid {
		t.Fatalf("payment id round-trip failed")
	}
	if string(rec.ResponseBody) != string(body) {
		t.Fatalf("body round-trip failed")
	}
}

func TestRecordIdempotency_DuplicateIsNoop(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	if err := s.RecordIdempotency(ctx, "k-dup", uid, nil, nil); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := s.RecordIdempotency(ctx, "k-dup", uid, nil, nil); err != nil {
		t.Fatalf("second should be no-op: %v", err)
	}
}

func TestFindIdempotency_KeyNotFound(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	_, err := s.FindIdempotency(context.Background(), "missing-billpay", uuid.New())
	if !errors.Is(err, ErrIdempotencyKeyNotFound) {
		t.Fatalf("expected key-not-found; got %v", err)
	}
}

func TestFindIdempotency_UserMismatch(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	ctx := context.Background()
	a := uuid.New()
	b := uuid.New()
	if err := s.RecordIdempotency(ctx, "k-user", a, nil, nil); err != nil {
		t.Fatalf("record: %v", err)
	}
	if _, err := s.FindIdempotency(ctx, "k-user", b); !errors.Is(err, ErrIdempotencyMismatch) {
		t.Fatalf("expected mismatch when different user replays; got %v", err)
	}
}

func TestPurgeExpiredIdempotency(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	if _, err := s.PurgeExpiredIdempotency(context.Background()); err != nil {
		t.Fatalf("purge: %v", err)
	}
}
