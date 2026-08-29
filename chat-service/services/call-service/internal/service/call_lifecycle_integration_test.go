//go:build integration

package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/atpost/chat-call-service/database"
	"github.com/atpost/chat-call-service/internal/domain"
	"github.com/atpost/chat-call-service/internal/sfu"
	store "github.com/atpost/chat-call-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Live-PostgreSQL proofs for the calling correction pass — the exact
// interleavings CALL-LB-1/3/5 named, run against the REAL store, service and
// rate limiter (policy nil: graph gating has its own unit suite; these tests
// pin state machines, not permissions).
//
// Run with a DISPOSABLE database and a reachable Redis:
//
//	CALL_POSTGRES_DSN=postgres://.../call_it?sslmode=disable \
//	CALL_REDIS_ADDR=localhost:6379 \
//	  go test -tags integration -run CallLifecyclePG ./internal/service/ -count=1
func lifecycleService(t *testing.T) (*Service, *store.CallStore, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("CALL_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("CALL_POSTGRES_DSN is required")
	}
	redisAddr := os.Getenv("CALL_REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS calls CASCADE`); err != nil {
		t.Fatal(err)
	}
	if err := store.BootstrapSchema(ctx, pool, database.SetupSQL); err != nil {
		t.Fatal(err)
	}
	callStore := store.NewCallStore(pool)
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	svc := New(callStore, sfu.NewStubProvider(), NewRateLimiter(rdb), nil, rdb, slog.Default(), 30)
	return svc, callStore, pool
}

func directCallReq(target uuid.UUID) CreateCallRequest {
	return CreateCallRequest{
		CallType:      domain.CallTypeDirectAudio,
		SourceType:    "chat",
		TargetUserIDs: []uuid.UUID{target},
		AudioOnly:     true,
	}
}

// CALL-LB-1: the EXACT Android caller order — create, then the caller's own
// join (for ICE), then ring — must leave the call discoverable as RINGING by
// the callee; only the CALLEE's join activates it.
func TestCallLifecyclePGCallerJoinDoesNotActivate(t *testing.T) {
	svc, _, _ := lifecycleService(t)
	ctx := context.Background()
	caller, callee := uuid.New(), uuid.New()

	created, err := svc.CreateCall(ctx, caller, directCallReq(callee))
	if err != nil {
		t.Fatal(err)
	}
	// The caller's own join — the step that used to flip ringing→active.
	if _, err := svc.JoinCall(ctx, caller, created.ID); err != nil {
		t.Fatal(err)
	}

	call, err := svc.GetCall(ctx, caller, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if call.State != domain.CallStateRinging {
		t.Fatalf("caller's own join activated the call: state=%q (CALL-LB-1)", call.State)
	}

	// The callee's discovery surface must still show a RINGING invite —
	// the Android recipient rings only for ringing/initiated.
	invites, err := svc.ListPendingInvites(ctx, callee)
	if err != nil {
		t.Fatal(err)
	}
	if len(invites) != 1 || invites[0].CallState != domain.CallStateRinging {
		t.Fatalf("callee cannot discover the ringing call: %+v", invites)
	}

	// Accept + the CALLEE's join is the answer transition.
	if err := svc.AcceptInvite(ctx, callee, created.ID, invites[0].InviteID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.JoinCall(ctx, callee, created.ID); err != nil {
		t.Fatal(err)
	}
	call, err = svc.GetCall(ctx, caller, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if call.State != domain.CallStateActive {
		t.Fatalf("callee join did not activate: state=%q", call.State)
	}
}

// CALL-LB-3: either participant leaving a DIRECT call ends it for BOTH, and
// both are immediately eligible for a subsequent call.
func TestCallLifecyclePGLeaveEndsDirectCallForBothPeers(t *testing.T) {
	svc, callStore, _ := lifecycleService(t)
	ctx := context.Background()
	caller, callee := uuid.New(), uuid.New()

	created, err := svc.CreateCall(ctx, caller, directCallReq(callee))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.JoinCall(ctx, caller, created.ID); err != nil {
		t.Fatal(err)
	}
	invites, _ := svc.ListPendingInvites(ctx, callee)
	if err := svc.AcceptInvite(ctx, callee, created.ID, invites[0].InviteID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.JoinCall(ctx, callee, created.ID); err != nil {
		t.Fatal(err)
	}

	// The CALLEE hangs up — the non-initiator, the previously stranding case:
	// the caller stayed joined, so the zero-joined auto-end never fired.
	if err := svc.LeaveCall(ctx, callee, created.ID); err != nil {
		t.Fatal(err)
	}

	call, err := svc.GetCall(ctx, caller, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if call.State != domain.CallStateEnded {
		t.Fatalf("direct call not ended after one peer left: %q (CALL-LB-3)", call.State)
	}
	for _, u := range []uuid.UUID{caller, callee} {
		active, err := callStore.GetActiveCallForUser(ctx, u)
		if err != nil {
			t.Fatal(err)
		}
		if active != nil {
			t.Fatalf("user %s still stranded in call %s", u, active.ID)
		}
	}

	// Subsequent eligibility, both directions.
	if _, err := svc.CreateCall(ctx, caller, directCallReq(callee)); err != nil {
		t.Fatalf("caller not eligible after peer hangup: %v", err)
	}
}

// CALL-LB-3 (decline arm): declining after the caller's join must end the
// call — previously the caller's joined row kept it active forever.
func TestCallLifecyclePGDeclineAfterCallerJoinEndsTheCall(t *testing.T) {
	svc, callStore, _ := lifecycleService(t)
	ctx := context.Background()
	caller, callee := uuid.New(), uuid.New()

	created, err := svc.CreateCall(ctx, caller, directCallReq(callee))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.JoinCall(ctx, caller, created.ID); err != nil {
		t.Fatal(err)
	}
	invites, _ := svc.ListPendingInvites(ctx, callee)
	if err := svc.DeclineInvite(ctx, callee, created.ID, invites[0].InviteID); err != nil {
		t.Fatal(err)
	}

	call, err := svc.GetCall(ctx, caller, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if call.State != domain.CallStateEnded {
		t.Fatalf("decline left the call %q — caller stranded (CALL-LB-3)", call.State)
	}
	active, _ := callStore.GetActiveCallForUser(ctx, caller)
	if active != nil {
		t.Fatalf("caller stranded after decline: %s", active.ID)
	}
	if _, err := svc.CreateCall(ctx, caller, directCallReq(callee)); err != nil {
		t.Fatalf("caller not eligible after decline: %v", err)
	}
}

// CALL-LB-5: barriered concurrent starts by the same initiator must produce
// exactly one live call. The race is repeated in-test because a single
// overlap does not reliably hit the unlocked check-then-create window — the
// negative control (advisory lock bypassed) fails within a handful of
// rounds, while the locked path survives every one.
func TestCallLifecyclePGConcurrentStartsCreateExactlyOneCall(t *testing.T) {
	svc, _, pool := lifecycleService(t)
	ctx := context.Background()

	for round := 0; round < concurrentStartRounds; round++ {
		caller := uuid.New()
		targetA, targetB := uuid.New(), uuid.New()

		start := make(chan struct{})
		results := make([]error, 2)
		var wg sync.WaitGroup
		for i, target := range []uuid.UUID{targetA, targetB} {
			wg.Add(1)
			go func(slot int, tgt uuid.UUID) {
				defer wg.Done()
				<-start // the barrier: both requests in flight together
				_, err := svc.CreateCall(ctx, caller, directCallReq(tgt))
				results[slot] = err
			}(i, target)
		}
		close(start)
		wg.Wait()

		successes, alreadyInCall := 0, 0
		for _, err := range results {
			switch {
			case err == nil:
				successes++
			case errors.Is(err, ErrAlreadyInCall):
				alreadyInCall++
			default:
				t.Fatalf("round %d: unexpected error: %v", round, err)
			}
		}
		if successes != 1 || alreadyInCall != 1 {
			t.Fatalf("round %d: want one winner and one ErrAlreadyInCall, got %d/%d (CALL-LB-5)",
				round, successes, alreadyInCall)
		}

		var liveSessions int
		if err := pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM calls.call_sessions
			WHERE initiator_user_id = $1
			  AND state NOT IN ('ended','canceled','failed','expired')
		`, caller).Scan(&liveSessions); err != nil {
			t.Fatal(err)
		}
		if liveSessions != 1 {
			t.Fatalf("round %d: database holds %d live calls for one initiator (CALL-LB-5)",
				round, liveSessions)
		}
	}
}

const concurrentStartRounds = 12

// countLiveCallsForUser counts DISTINCT live sessions the user participates
// in — the user-wide one-live-call invariant CALL-LB-5 protects.
func countLiveCallsForUser(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(DISTINCT cs.id)
		FROM calls.call_sessions cs
		JOIN calls.call_participants cp ON cp.call_session_id = cs.id
		WHERE cp.user_id = $1
		  AND cs.state IN ('initiated', 'ringing', 'active')
	`, userID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func tableCount(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// CALL-LB-5 (user-wide invariant, cross-initiator): callers A and C ring the
// SAME recipient B concurrently. Exactly one call may exist: one success,
// one generic ErrTargetUnavailable, B in exactly one live call, and the side
// effects of exactly ONE creation — one session, one room, one invite, one
// CallCreated and one CallInvited outbox event. The loser must leave no row
// behind at all.
func TestCallLifecyclePGCrossInitiatorSameRecipientOneCall(t *testing.T) {
	svc, callStore, pool := lifecycleService(t)
	ctx := context.Background()

	for round := 0; round < concurrentStartRounds; round++ {
		a, b, c := uuid.New(), uuid.New(), uuid.New()

		roomsBefore := tableCount(t, pool, `SELECT COUNT(*) FROM calls.call_rooms`)
		createdBefore := tableCount(t, pool,
			`SELECT COUNT(*) FROM calls.outbox_events WHERE event_type = 'CallCreated'`)
		invitedBefore := tableCount(t, pool,
			`SELECT COUNT(*) FROM calls.outbox_events WHERE event_type = 'CallInvited'`)

		start := make(chan struct{})
		results := make([]error, 2)
		var wg sync.WaitGroup
		for i, initiator := range []uuid.UUID{a, c} {
			wg.Add(1)
			go func(slot int, caller uuid.UUID) {
				defer wg.Done()
				<-start
				_, err := svc.CreateCall(ctx, caller, directCallReq(b))
				results[slot] = err
			}(i, initiator)
		}
		close(start)
		wg.Wait()

		successes, busy := 0, 0
		for _, err := range results {
			switch {
			case err == nil:
				successes++
			case errors.Is(err, ErrTargetUnavailable):
				busy++
			default:
				t.Fatalf("round %d: unexpected error: %v", round, err)
			}
		}
		if successes != 1 || busy != 1 {
			t.Fatalf("round %d: want one winner and one generic-busy, got %d/%d (CALL-LB-5)",
				round, successes, busy)
		}

		// B belongs to exactly one live call, discoverable both ways.
		if n := countLiveCallsForUser(t, pool, b); n != 1 {
			t.Fatalf("round %d: recipient is in %d live calls (CALL-LB-5)", round, n)
		}
		if active, err := callStore.GetActiveCallForUser(ctx, b); err != nil || active == nil {
			t.Fatalf("round %d: recipient's active call lookup: %v / %v", round, active, err)
		}

		// Exactly ONE creation's side effects, and the loser left nothing.
		liveSessions := tableCount(t, pool, `
			SELECT COUNT(*) FROM calls.call_sessions
			WHERE initiator_user_id IN ($1, $2)
			  AND state NOT IN ('ended','canceled','failed','expired')`, a, c)
		if liveSessions != 1 {
			t.Fatalf("round %d: %d live sessions from the two initiators", round, liveSessions)
		}
		if n := tableCount(t, pool,
			`SELECT COUNT(*) FROM calls.call_invites WHERE invitee_user_id = $1`, b); n != 1 {
			t.Fatalf("round %d: %d invites for the recipient", round, n)
		}
		if delta := tableCount(t, pool, `SELECT COUNT(*) FROM calls.call_rooms`) - roomsBefore; delta != 1 {
			t.Fatalf("round %d: %d rooms created", round, delta)
		}
		createdDelta := tableCount(t, pool,
			`SELECT COUNT(*) FROM calls.outbox_events WHERE event_type = 'CallCreated'`) - createdBefore
		invitedDelta := tableCount(t, pool,
			`SELECT COUNT(*) FROM calls.outbox_events WHERE event_type = 'CallInvited'`) - invitedBefore
		if createdDelta != 1 || invitedDelta != 1 {
			t.Fatalf("round %d: outbox deltas CallCreated=%d CallInvited=%d",
				round, createdDelta, invitedDelta)
		}

		// Reset for the next round: end the winner's call.
		if err := svc.LeaveCall(ctx, b, mustActiveCallID(t, callStore, b)); err != nil {
			t.Fatal(err)
		}
	}
}

func mustActiveCallID(t *testing.T, callStore *store.CallStore, userID uuid.UUID) uuid.UUID {
	t.Helper()
	active, err := callStore.GetActiveCallForUser(context.Background(), userID)
	if err != nil || active == nil {
		t.Fatalf("no active call for %s: %v", userID, err)
	}
	return active.ID
}

// CALL-LB-5 (user-wide invariant, non-direct writers): every client-reachable
// participant-add path that does NOT hold the user-set busy lock must be
// refused server-side in P0 — a busy user must be unaddable through group
// create, post-create invites, and open joins, not merely through direct
// create.
func TestCallLifecyclePGGroupPathsRefusedInP0(t *testing.T) {
	svc, callStore, pool := lifecycleService(t)
	ctx := context.Background()
	a, b, c := uuid.New(), uuid.New(), uuid.New()

	// B is busy in a live direct call with A.
	created, err := svc.CreateCall(ctx, a, directCallReq(b))
	if err != nil {
		t.Fatal(err)
	}

	// 1. Group CREATE targeting the busy user is refused at the boundary
	// (before any lock, room, session, invite or outbox write).
	_, err = svc.CreateCall(ctx, c, CreateCallRequest{
		CallType:      domain.CallTypeGroupAudio,
		SourceType:    "group",
		TargetUserIDs: []uuid.UUID{b},
	})
	if !errors.Is(err, ErrGroupCallsDisabled) {
		t.Fatalf("group create not refused: %v (CALL-LB-5)", err)
	}

	// 2. Post-create INVITE into the live call is refused — P0 direct
	// calls carry their one invite from creation.
	_, err = svc.InviteParticipants(ctx, a, created.ID, InviteParticipantsRequest{
		UserIDs: []uuid.UUID{c},
	})
	if !errors.Is(err, ErrGroupCallsDisabled) {
		t.Fatalf("post-create invite not refused: %v (CALL-LB-5)", err)
	}

	// 3. OPEN JOIN is refused even when an open-mode session exists (seeded
	// through the store, since the service can no longer create one).
	now := time.Now()
	openCall, openRoom := uuid.New(), uuid.New()
	if err := callStore.CreateRoom(ctx, &domain.CallRoom{
		ID: openRoom, RoomKey: "call-" + openCall.String(), Provider: "stub",
		ProviderRoomName: "stub-room", RegionCode: "ap-south-1",
		Status: domain.RoomStatusAllocated, MaxParticipants: 25, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := callStore.CreateCallSession(ctx, &domain.CallSession{
		ID: openCall, CallType: domain.CallTypeGroupAudio, SourceType: "group",
		InitiatorUserID: c, RoomID: &openRoom, State: domain.CallStateActive,
		MaxParticipants: 25, JoinMode: domain.JoinModeOpen,
		StartedAt: &now, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.JoinCall(ctx, b, openCall); !errors.Is(err, ErrNotParticipant) {
		t.Fatalf("open join not refused: %v (CALL-LB-5)", err)
	}

	// B is still in exactly the one direct call.
	if n := countLiveCallsForUser(t, pool, b); n != 1 {
		t.Fatalf("busy user reached %d live calls through gated paths (CALL-LB-5)", n)
	}
}

// CALL-LB-5 (initiator/recipient overlap): A→B races C→A. The shared user is
// A — initiator of one request, target of the other. Exactly one call may
// come to exist and A must belong to exactly one live call. Which side wins
// is timing-dependent; the loser's error names which check refused it.
func TestCallLifecyclePGInitiatorRecipientOverlapOneCall(t *testing.T) {
	svc, callStore, pool := lifecycleService(t)
	ctx := context.Background()

	for round := 0; round < concurrentStartRounds; round++ {
		a, b, c := uuid.New(), uuid.New(), uuid.New()

		start := make(chan struct{})
		results := make([]error, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, err := svc.CreateCall(ctx, a, directCallReq(b))
			results[0] = err
		}()
		go func() {
			defer wg.Done()
			<-start
			_, err := svc.CreateCall(ctx, c, directCallReq(a))
			results[1] = err
		}()
		close(start)
		wg.Wait()

		successes := 0
		for _, err := range results {
			switch {
			case err == nil:
				successes++
			case errors.Is(err, ErrTargetUnavailable), errors.Is(err, ErrAlreadyInCall):
				// A→B losing sees ErrAlreadyInCall (A busy as initiator);
				// C→A losing sees ErrTargetUnavailable (A busy as target).
			default:
				t.Fatalf("round %d: unexpected error: %v", round, err)
			}
		}
		if successes != 1 {
			t.Fatalf("round %d: %d successes, want exactly 1 (CALL-LB-5)", round, successes)
		}
		if n := countLiveCallsForUser(t, pool, a); n != 1 {
			t.Fatalf("round %d: overlapped user is in %d live calls (CALL-LB-5)", round, n)
		}

		if err := svc.LeaveCall(ctx, a, mustActiveCallID(t, callStore, a)); err != nil {
			t.Fatal(err)
		}
	}
}
