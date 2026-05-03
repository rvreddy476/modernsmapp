// Vouch service tests.
//
// Eligibility checks are wired through GraphServiceClient and
// CommunityServiceClient interfaces — tests inject fakes so we don't need
// the live services.
package service

import (
	"context"
	"os"
	"testing"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fakeGraph returns a configured boolean for IsMutualFollow.
type fakeGraph struct {
	mutual bool
	err    error
}

func (f *fakeGraph) IsMutualFollow(_ context.Context, _, _ uuid.UUID) (bool, error) {
	return f.mutual, f.err
}

// fakeCommunity returns a configured shared-membership boolean.
type fakeCommunity struct {
	shared bool
	err    error
}

func (f *fakeCommunity) UsersShareCommunity(_ context.Context, _, _ uuid.UUID, _ uuid.UUID) (bool, error) {
	return f.shared, f.err
}

func newVouchSvc(t *testing.T) (*Service, *store.Store, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping vouch service tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	st := store.New(pool)
	return New(st, nil), st, func() { pool.Close() }
}

func TestRequestVouch_FriendRequiresMutualFollow(t *testing.T) {
	svc, st, cleanup := newVouchSvc(t)
	defer cleanup()
	a, b := uuid.New(), uuid.New()
	seedProfile(t, st, a)
	seedProfile(t, st, b)

	// Not mutual → forbidden.
	svc.SetGraphServiceClient(&fakeGraph{mutual: false})
	if _, err := svc.RequestVouch(context.Background(), a, b, "friend", nil, ""); err == nil {
		t.Fatalf("expected forbidden when not mutual")
	}

	// Mutual → success.
	svc.SetGraphServiceClient(&fakeGraph{mutual: true})
	v, err := svc.RequestVouch(context.Background(), a, b, "friend", nil, "good guy")
	if err != nil {
		t.Fatalf("happy: %v", err)
	}
	if v.Status != "pending" {
		t.Fatalf("expected pending, got %s", v.Status)
	}
}

func TestRequestVouch_CommunityMemberRequiresShared(t *testing.T) {
	svc, st, cleanup := newVouchSvc(t)
	defer cleanup()
	a, b := uuid.New(), uuid.New()
	seedProfile(t, st, a)
	seedProfile(t, st, b)
	commID := uuid.New()

	svc.SetCommunityServiceClient(&fakeCommunity{shared: false})
	if _, err := svc.RequestVouch(context.Background(), a, b, "community_member", &commID, ""); err == nil {
		t.Fatalf("expected forbidden when not shared community")
	}

	svc.SetCommunityServiceClient(&fakeCommunity{shared: true})
	if _, err := svc.RequestVouch(context.Background(), a, b, "community_member", &commID, ""); err != nil {
		t.Fatalf("shared community happy path: %v", err)
	}
}

func TestRequestVouch_CommunityMemberRequiresCommunityID(t *testing.T) {
	svc, st, cleanup := newVouchSvc(t)
	defer cleanup()
	a, b := uuid.New(), uuid.New()
	seedProfile(t, st, a)
	seedProfile(t, st, b)
	if _, err := svc.RequestVouch(context.Background(), a, b, "community_member", nil, ""); err == nil {
		t.Fatalf("expected error when community_id is missing for community_member")
	}
}

func TestRequestVouch_ColleagueAndFamilyNoGraphCheck(t *testing.T) {
	svc, st, cleanup := newVouchSvc(t)
	defer cleanup()
	a, b := uuid.New(), uuid.New()
	seedProfile(t, st, a)
	seedProfile(t, st, b)
	// Without any graph client, colleague/family should still go through.
	if _, err := svc.RequestVouch(context.Background(), a, b, "colleague", nil, "boss"); err != nil {
		t.Fatalf("colleague: %v", err)
	}
	c := uuid.New()
	seedProfile(t, st, c)
	if _, err := svc.RequestVouch(context.Background(), a, c, "family", nil, "cousin"); err != nil {
		t.Fatalf("family: %v", err)
	}
}

func TestRequestVouch_WeeklyRateLimit(t *testing.T) {
	svc, st, cleanup := newVouchSvc(t)
	defer cleanup()
	voucher := uuid.New()
	seedProfile(t, st, voucher)
	// 5 successful requests, 6th is denied.
	for i := 0; i < MaxVouchRequestsPerWeek; i++ {
		recipient := uuid.New()
		seedProfile(t, st, recipient)
		if _, err := svc.RequestVouch(context.Background(), voucher, recipient, "colleague", nil, ""); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	overflow := uuid.New()
	seedProfile(t, st, overflow)
	if _, err := svc.RequestVouch(context.Background(), voucher, overflow, "colleague", nil, ""); err == nil {
		t.Fatalf("expected rate-limit error on 6th request")
	}
}

func TestAcceptDeclineVouch_OnlyVouchee(t *testing.T) {
	svc, st, cleanup := newVouchSvc(t)
	defer cleanup()
	a, b := uuid.New(), uuid.New()
	seedProfile(t, st, a)
	seedProfile(t, st, b)
	v, err := svc.RequestVouch(context.Background(), a, b, "colleague", nil, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	intruder := uuid.New()
	if err := svc.AcceptVouch(context.Background(), v.ID, intruder); err == nil {
		t.Fatalf("expected forbidden when non-vouchee accepts")
	}
	if err := svc.AcceptVouch(context.Background(), v.ID, b); err != nil {
		t.Fatalf("accept: %v", err)
	}
}

func TestRevokeVouch_OnlyVoucher(t *testing.T) {
	svc, st, cleanup := newVouchSvc(t)
	defer cleanup()
	a, b := uuid.New(), uuid.New()
	seedProfile(t, st, a)
	seedProfile(t, st, b)
	v, err := svc.RequestVouch(context.Background(), a, b, "family", nil, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.RevokeVouch(context.Background(), v.ID, b); err == nil {
		t.Fatalf("expected forbidden when non-voucher revokes")
	}
	if err := svc.RevokeVouch(context.Background(), v.ID, a); err != nil {
		t.Fatalf("revoke: %v", err)
	}
}
