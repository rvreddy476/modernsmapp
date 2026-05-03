// Vouches store tests. Skipped unless TEST_PG_DSN is set.
package store

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func vouchTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping vouches store tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	return New(pool), func() { pool.Close() }
}

func TestCreateVouchRequest_HappyPath(t *testing.T) {
	s, cleanup := vouchTestStore(t)
	defer cleanup()
	voucher, vouchee := uuid.New(), uuid.New()
	ensureProfileForTest(t, s, voucher)
	ensureProfileForTest(t, s, vouchee)
	v, err := s.CreateVouchRequest(context.Background(), voucher, vouchee, "friend", nil, "good guy")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if v.Status != "pending" {
		t.Fatalf("expected pending, got %s", v.Status)
	}
	if v.Relationship == nil || *v.Relationship != "friend" {
		t.Fatalf("relationship not stored: %+v", v.Relationship)
	}
}

func TestCreateVouchRequest_RejectsSelf(t *testing.T) {
	s, cleanup := vouchTestStore(t)
	defer cleanup()
	uid := uuid.New()
	if _, err := s.CreateVouchRequest(context.Background(), uid, uid, "friend", nil, ""); err == nil {
		t.Fatalf("expected error for self-vouch")
	}
}

func TestCreateVouchRequest_RejectsBogusRelationship(t *testing.T) {
	s, cleanup := vouchTestStore(t)
	defer cleanup()
	a, b := uuid.New(), uuid.New()
	ensureProfileForTest(t, s, a)
	ensureProfileForTest(t, s, b)
	if _, err := s.CreateVouchRequest(context.Background(), a, b, "neighbor", nil, ""); err == nil {
		t.Fatalf("expected error for bogus relationship")
	}
}

func TestCreateVouchRequest_DuplicateUpserts(t *testing.T) {
	s, cleanup := vouchTestStore(t)
	defer cleanup()
	a, b := uuid.New(), uuid.New()
	ensureProfileForTest(t, s, a)
	ensureProfileForTest(t, s, b)
	first, err := s.CreateVouchRequest(context.Background(), a, b, "friend", nil, "first")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	// Decline the first, then re-request → should reset to pending.
	if err := s.DecideVouch(context.Background(), first.ID, b, "declined"); err != nil {
		t.Fatalf("decline: %v", err)
	}
	second, err := s.CreateVouchRequest(context.Background(), a, b, "friend", nil, "again")
	if err != nil {
		t.Fatalf("re-request: %v", err)
	}
	if second.Status != "pending" {
		t.Fatalf("expected re-request to be pending, got %s", second.Status)
	}
	if first.ID != second.ID {
		t.Fatalf("expected same id (UNIQUE upsert)")
	}
}

func TestDecideAndRevokeVouch_StateMachine(t *testing.T) {
	s, cleanup := vouchTestStore(t)
	defer cleanup()
	a, b := uuid.New(), uuid.New()
	ensureProfileForTest(t, s, a)
	ensureProfileForTest(t, s, b)
	v, err := s.CreateVouchRequest(context.Background(), a, b, "friend", nil, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Bogus decision rejected.
	if err := s.DecideVouch(context.Background(), v.ID, b, "maybe"); err == nil {
		t.Fatalf("expected validation error on bogus decision")
	}
	// Wrong vouchee → not found.
	other := uuid.New()
	if err := s.DecideVouch(context.Background(), v.ID, other, "accepted"); err == nil {
		t.Fatalf("expected not-found when wrong vouchee decides")
	}
	// Accept.
	if err := s.DecideVouch(context.Background(), v.ID, b, "accepted"); err != nil {
		t.Fatalf("accept: %v", err)
	}
	// Revoke (only voucher allowed).
	if err := s.RevokeVouch(context.Background(), v.ID, other); err == nil {
		t.Fatalf("expected not-found when non-voucher revokes")
	}
	if err := s.RevokeVouch(context.Background(), v.ID, a); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	got, err := s.GetVouch(context.Background(), v.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != "revoked" {
		t.Fatalf("expected revoked, got %s", got.Status)
	}
}

func TestCountVouchRequestsThisWeek(t *testing.T) {
	s, cleanup := vouchTestStore(t)
	defer cleanup()
	a := uuid.New()
	ensureProfileForTest(t, s, a)
	for i := 0; i < 3; i++ {
		recipient := uuid.New()
		ensureProfileForTest(t, s, recipient)
		if _, err := s.CreateVouchRequest(context.Background(), a, recipient, "friend", nil, ""); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	n, err := s.CountVouchRequestsThisWeek(context.Background(), a)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n < 3 {
		t.Fatalf("expected at least 3, got %d", n)
	}
}

func TestListVouchesFor_PublicAcceptedOnly(t *testing.T) {
	s, cleanup := vouchTestStore(t)
	defer cleanup()
	target := uuid.New()
	ensureProfileForTest(t, s, target)
	// Two pending, one accepted.
	for i := 0; i < 2; i++ {
		voucher := uuid.New()
		ensureProfileForTest(t, s, voucher)
		if _, err := s.CreateVouchRequest(context.Background(), voucher, target, "friend", nil, ""); err != nil {
			t.Fatalf("seed pending %d: %v", i, err)
		}
	}
	acceptedVoucher := uuid.New()
	ensureProfileForTest(t, s, acceptedVoucher)
	v, err := s.CreateVouchRequest(context.Background(), acceptedVoucher, target, "friend", nil, "")
	if err != nil {
		t.Fatalf("seed accepted: %v", err)
	}
	if err := s.DecideVouch(context.Background(), v.ID, target, "accepted"); err != nil {
		t.Fatalf("accept: %v", err)
	}

	out, err := s.ListVouchesFor(context.Background(), target, "accepted")
	if err != nil {
		t.Fatalf("list accepted: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 accepted, got %d", len(out))
	}

	all, err := s.ListVouchesFor(context.Background(), target, "")
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 non-revoked, got %d", len(all))
	}
}
