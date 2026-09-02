//go:build integration

package store

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Private accounts — follow-request lifecycle against live PostgreSQL.
//
//	POSTGRES_DSN=postgres://... go test -tags integration ./internal/store/ -run FollowRequest -v
//
// Uses the same fixture/schema helpers as the pair-atomic suite so the
// follow_requests table, the outbox and the block sweep are all the real ones.

func countOutbox(t *testing.T, pool *pgxpool.Pool, eventType string, a, b uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM graph_outbox_events
		WHERE event_type = $1
		  AND ((actor_id = $2 AND target_id = $3) OR (actor_id = $3 AND target_id = $2))`,
		eventType, a, b).Scan(&n); err != nil {
		t.Fatalf("count outbox %s: %v", eventType, err)
	}
	return n
}

func followRequestStatus(t *testing.T, s *Store, requester, target uuid.UUID) string {
	t.Helper()
	var status string
	err := s.db.QueryRow(context.Background(),
		`SELECT status FROM follow_requests WHERE requester_id = $1 AND target_id = $2`,
		requester, target).Scan(&status)
	if err != nil {
		return "absent"
	}
	return status
}

func TestFollowRequestLifecycle(t *testing.T) {
	pool := graphPool(t)
	s := New(pool)
	ctx := context.Background()
	requester, target := pairFixture(t, pool)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM follow_requests WHERE requester_id IN ($1,$2) OR target_id IN ($1,$2)`, requester, target)
	})

	// Request → pending, one graph.follow_requested outbox row.
	created, err := s.UpsertFollowRequestPending(ctx, requester, target)
	if err != nil || !created {
		t.Fatalf("first request: created=%v err=%v", created, err)
	}
	if got := followRequestStatus(t, s, requester, target); got != "pending" {
		t.Fatalf("status after request = %q, want pending", got)
	}
	// A second tap while pending is a no-op and does not announce again.
	created, err = s.UpsertFollowRequestPending(ctx, requester, target)
	if err != nil || created {
		t.Fatalf("duplicate request: created=%v err=%v, want false/nil", created, err)
	}
	if n := countOutbox(t, pool, events.GraphFollowRequested, requester, target); n != 1 {
		t.Fatalf("follow_requested outbox rows = %d, want exactly 1", n)
	}

	// Relationship snapshot sees it from both sides.
	full, err := s.GetRelationshipFull(ctx, requester, target)
	if err != nil {
		t.Fatal(err)
	}
	if !full.FollowRequestSent || full.FollowRequestReceived || full.Follows {
		t.Fatalf("requester view = %+v, want FollowRequestSent only", full)
	}
	full, _ = s.GetRelationshipFull(ctx, target, requester)
	if !full.FollowRequestReceived || full.FollowRequestSent {
		t.Fatalf("target view = %+v, want FollowRequestReceived only", full)
	}
	batch, err := s.GetRelationshipBatch(ctx, requester, []uuid.UUID{target})
	if err != nil {
		t.Fatal(err)
	}
	if batch[target].FollowRequestStatus != "pending_sent" {
		t.Fatalf("batch follow_request_status = %q, want pending_sent", batch[target].FollowRequestStatus)
	}

	// Decline → re-request allowed → cancel.
	if err := s.DeclineFollowRequest(ctx, requester, target); err != nil {
		t.Fatalf("decline: %v", err)
	}
	if err := s.DeclineFollowRequest(ctx, requester, target); !errors.Is(err, ErrNoPendingFollowRequest) {
		t.Fatalf("second decline err = %v, want ErrNoPendingFollowRequest", err)
	}
	created, err = s.UpsertFollowRequestPending(ctx, requester, target)
	if err != nil || !created {
		t.Fatalf("re-request after decline: created=%v err=%v", created, err)
	}
	if err := s.CancelFollowRequest(ctx, requester, target); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if got := followRequestStatus(t, s, requester, target); got != "cancelled" {
		t.Fatalf("status after cancel = %q", got)
	}

	// Accept: ONE transaction — status, edge, UserFollowed, follow_request_accepted.
	if _, err := s.UpsertFollowRequestPending(ctx, requester, target); err != nil {
		t.Fatal(err)
	}
	inserted, err := s.AcceptFollowRequestAtomic(ctx, requester, target)
	if err != nil || !inserted {
		t.Fatalf("accept: inserted=%v err=%v", inserted, err)
	}
	if got := followRequestStatus(t, s, requester, target); got != "accepted" {
		t.Fatalf("status after accept = %q", got)
	}
	follows, _ := s.CheckFollow(ctx, requester, target)
	if !follows {
		t.Fatal("accept did not create the follow edge")
	}
	if n := countOutbox(t, pool, events.UserFollowed, requester, target); n != 1 {
		t.Fatalf("UserFollowed outbox rows = %d, want 1", n)
	}
	if n := countOutbox(t, pool, events.GraphFollowRequestAccepted, requester, target); n != 1 {
		t.Fatalf("follow_request_accepted outbox rows = %d, want 1", n)
	}
	// Accepting again: nothing pending.
	if _, err := s.AcceptFollowRequestAtomic(ctx, requester, target); !errors.Is(err, ErrNoPendingFollowRequest) {
		t.Fatalf("second accept err = %v, want ErrNoPendingFollowRequest", err)
	}
}

func TestFollowRequestBlockedPairAndSweep(t *testing.T) {
	pool := graphPool(t)
	s := New(pool)
	ctx := context.Background()
	requester, target := pairFixture(t, pool)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM follow_requests WHERE requester_id IN ($1,$2) OR target_id IN ($1,$2)`, requester, target)
	})

	// A pending request is swept by a block in either direction...
	if _, err := s.UpsertFollowRequestPending(ctx, requester, target); err != nil {
		t.Fatal(err)
	}
	res, err := s.BlockAtomic(ctx, target, requester)
	if err != nil {
		t.Fatal(err)
	}
	if res.RemovedFollowRequest != 1 {
		t.Fatalf("block swept %d follow requests, want 1", res.RemovedFollowRequest)
	}
	if got := followRequestStatus(t, s, requester, target); got != "absent" {
		t.Fatalf("request survived the block: %q", got)
	}
	// ...and cannot be created, nor accepted, across the block.
	if _, err := s.UpsertFollowRequestPending(ctx, requester, target); !errors.Is(err, ErrBlockedPair) {
		t.Fatalf("request across a block err = %v, want ErrBlockedPair", err)
	}
	if _, err := s.AcceptFollowRequestAtomic(ctx, requester, target); !errors.Is(err, ErrBlockedPair) {
		t.Fatalf("accept across a block err = %v, want ErrBlockedPair", err)
	}
}

func TestFollowRequestIncomingListAndAutoAcceptQueue(t *testing.T) {
	pool := graphPool(t)
	s := New(pool)
	ctx := context.Background()
	target := uuid.New()
	requesters := make([]uuid.UUID, 0, 5)
	for i := 0; i < 5; i++ {
		requesters = append(requesters, uuid.New())
	}
	seed := []interface{ String() string }{target}
	for _, r := range requesters {
		seed = append(seed, r)
	}
	seedUsers(t, pool, seed...)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM follow_requests WHERE target_id = $1`, target)
		_, _ = pool.Exec(ctx, `DELETE FROM follows WHERE followee_id = $1`, target)
		_, _ = pool.Exec(ctx, `DELETE FROM graph_outbox_events WHERE target_id = $1 OR actor_id = $1`, target)
	})
	for _, r := range requesters {
		if _, err := s.UpsertFollowRequestPending(ctx, r, target); err != nil {
			t.Fatal(err)
		}
	}

	// Incoming list: newest first, paginated by cursor, no overlap.
	page1, next, err := s.ListIncomingFollowRequests(ctx, target, 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 3 || next == "" {
		t.Fatalf("page1 len=%d next=%q, want 3 with a cursor", len(page1), next)
	}
	page2, next2, err := s.ListIncomingFollowRequests(ctx, target, 3, next)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 2 || next2 != "" {
		t.Fatalf("page2 len=%d next=%q, want 2 and no cursor", len(page2), next2)
	}
	seen := map[uuid.UUID]bool{}
	for _, r := range append(page1, page2...) {
		if seen[r.RequesterID] {
			t.Fatalf("requester %s returned twice across pages", r.RequesterID)
		}
		seen[r.RequesterID] = true
	}
	for i := 1; i < len(page1); i++ {
		if page1[i].CreatedAt.After(page1[i-1].CreatedAt) {
			t.Fatal("incoming list is not newest-first")
		}
	}

	// Auto-accept work queue: chunked, drains as accepts land.
	ids, err := s.ListPendingFollowRequesterIDs(ctx, target, 2)
	if err != nil || len(ids) != 2 {
		t.Fatalf("queue chunk len=%d err=%v, want 2", len(ids), err)
	}
	for _, r := range requesters {
		if _, err := s.AcceptFollowRequestAtomic(ctx, r, target); err != nil {
			t.Fatalf("accept %s: %v", r, err)
		}
	}
	ids, _ = s.ListPendingFollowRequesterIDs(ctx, target, 100)
	if len(ids) != 0 {
		t.Fatalf("queue not drained: %d left", len(ids))
	}
	if n := countOutboxByType(t, s, events.UserFollowed, target); n != 5 {
		t.Fatalf("UserFollowed outbox rows toward target = %d, want 5", n)
	}
}

func countOutboxByType(t *testing.T, s *Store, eventType string, target uuid.UUID) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM graph_outbox_events WHERE event_type = $1 AND target_id = $2`,
		eventType, target).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
