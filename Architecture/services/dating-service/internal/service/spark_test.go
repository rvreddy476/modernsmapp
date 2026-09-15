// Spark service tests. These use the real *store.Store with TEST_PG_DSN
// and a stub message-service client. When TEST_PG_DSN is unset the test
// is skipped (CI requirement).
package service

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// stubMessageClient lets us assert the saga's external call without a real
// message-service.
type stubMessageClient struct {
	called      bool
	failNext    bool
	convID      uuid.UUID
	receivedReq CreateConversationRequest
}

func (s *stubMessageClient) CreateConversation(ctx context.Context, req CreateConversationRequest) (*CreateConversationResponse, error) {
	s.called = true
	s.receivedReq = req
	if s.failNext {
		return nil, errors.New("simulated 500")
	}
	if s.convID == uuid.Nil {
		s.convID = uuid.New()
	}
	return &CreateConversationResponse{ConversationID: s.convID.String()}, nil
}

func newSvcForTest(t *testing.T) (*Service, *store.Store, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping spark service tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	ensureSchemaForTest(t, pool)
	st := store.New(pool)
	svc := New(st, nil)
	return svc, st, func() { pool.Close() }
}

func seedProfile(t *testing.T, st *store.Store, id uuid.UUID) {
	t.Helper()
	intent := "casual"
	_, err := st.UpsertProfile(context.Background(), id, store.UpsertProfileParams{Intent: &intent})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestSparkService_CreateSpark_HappyPath(t *testing.T) {
	svc, st, cleanup := newSvcForTest(t)
	defer cleanup()
	from, to := uuid.New(), uuid.New()
	seedActiveProfile(t, st, from)
	seedActiveProfile(t, st, to)

	stub := &stubMessageClient{}
	svc.SetMessageClient(stub)

	sp, matchID, err := svc.CreateSpark(context.Background(), from, to, "photo", "0", "love")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if sp == nil || sp.FromUserID != from {
		t.Fatalf("bad spark: %+v", sp)
	}
	if matchID != nil {
		t.Fatalf("expected no match (no reverse spark), got %v", matchID)
	}
	if stub.called {
		t.Fatalf("message-service should not be invoked without mutual spark")
	}
}

func TestSparkService_MutualTriggersMatch(t *testing.T) {
	svc, st, cleanup := newSvcForTest(t)
	defer cleanup()
	a, b := uuid.New(), uuid.New()
	seedActiveProfile(t, st, a)
	seedActiveProfile(t, st, b)

	stub := &stubMessageClient{}
	svc.SetMessageClient(stub)

	// b sparks a first.
	if _, _, err := svc.CreateSpark(context.Background(), b, a, "prompt", "p1", ""); err != nil {
		t.Fatalf("first spark: %v", err)
	}
	// Now a sparks b → mutual.
	_, matchID, err := svc.CreateSpark(context.Background(), a, b, "photo", "0", "")
	if err != nil {
		t.Fatalf("second spark: %v", err)
	}
	if matchID == nil {
		t.Fatalf("expected match id from mutual spark")
	}
	if !stub.called {
		t.Fatalf("expected message-service to be called for match formation")
	}
}

func TestSparkService_MatchSagaCompensatesOnFailure(t *testing.T) {
	svc, st, cleanup := newSvcForTest(t)
	defer cleanup()
	a, b := uuid.New(), uuid.New()
	seedActiveProfile(t, st, a)
	seedActiveProfile(t, st, b)

	stub := &stubMessageClient{failNext: true}
	svc.SetMessageClient(stub)

	if _, _, err := svc.CreateSpark(context.Background(), b, a, "prompt", "p1", ""); err != nil {
		t.Fatalf("first: %v", err)
	}
	// Mutual spark — message-service will fail.
	sp, matchID, err := svc.CreateSpark(context.Background(), a, b, "photo", "0", "")
	if err != nil {
		t.Fatalf("expected spark to persist despite saga failure: %v", err)
	}
	if sp == nil {
		t.Fatalf("spark should still exist")
	}
	if matchID != nil {
		t.Fatalf("expected nil match id when saga fails")
	}
	// P0-9 (match.go): the failed handshake leaves the match 'matched' with
	// no conversation for the SagaReconciler instead of deleting it.
	m, err := st.GetMatchByUsers(context.Background(), a, b)
	if err != nil {
		t.Fatalf("expected the pending match to be kept for the reconciler: %v", err)
	}
	if m.Status != "matched" || m.ConversationID != nil {
		t.Fatalf("pending match = status %s conversation %v, want matched with no conversation", m.Status, m.ConversationID)
	}
}

// TestCreateSpark_BlockedWhenRestricted covers the §P1-1 profile-status
// gate. A user whose profile_status is restricted / suspended / deleted
// must not be able to create a new spark. pending_review is exercised
// alongside because it shares the moderator-flagged code path.
//
// Table-driven so the four states share the seed harness.
func TestCreateSpark_BlockedWhenRestricted(t *testing.T) {
	cases := []struct {
		name    string
		event   store.ProfileEvent
		actor   store.ProfileActor
		wantErr error
	}{
		{"restricted", store.ProfileEventRestrict, store.ProfileActorAdmin, ErrProfileRestricted},
		{"suspended", store.ProfileEventSuspend, store.ProfileActorAdmin, ErrProfileSuspended},
		// A soft delete stamps deleted_at, so the deleted actor no longer
		// has a live profile at all.
		{"deleted", store.ProfileEventDelete, store.ProfileActorUser, store.ErrProfileNotFound},
		{"pending_review", store.ProfileEventReview, store.ProfileActorAdmin, ErrProfilePendingReview},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			svc, st, cleanup := newSvcForTest(t)
			defer cleanup()

			from, to := uuid.New(), uuid.New()
			seedActiveProfile(t, st, from)
			seedActiveProfile(t, st, to)

			// Move the actor through the status machine to the case state.
			driveProfile(t, st, from, tc.event, tc.actor)

			_, _, err := svc.CreateSpark(context.Background(), from, to, "photo", "0", "")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("CreateSpark after %s: got err=%v want %v", tc.event, err, tc.wantErr)
			}
		})
	}
}

func TestSparkService_RevokeSpark_OwnerOnly(t *testing.T) {
	svc, st, cleanup := newSvcForTest(t)
	defer cleanup()
	owner, intruder, recipient := uuid.New(), uuid.New(), uuid.New()
	seedActiveProfile(t, st, owner)
	seedActiveProfile(t, st, intruder)
	seedActiveProfile(t, st, recipient)

	sp, _, err := svc.CreateSpark(context.Background(), owner, recipient, "photo", "0", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.RevokeSpark(context.Background(), sp.ID, intruder); err == nil {
		t.Fatalf("expected error revoking another user's spark")
	}
	if err := svc.RevokeSpark(context.Background(), sp.ID, owner); err != nil {
		t.Fatalf("owner revoke: %v", err)
	}
}
