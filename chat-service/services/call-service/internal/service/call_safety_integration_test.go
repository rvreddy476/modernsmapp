//go:build integration

package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/atpost/chat-call-service/database"
	"github.com/atpost/chat-call-service/internal/domain"
	"github.com/atpost/chat-call-service/internal/relationship"
	"github.com/atpost/chat-call-service/internal/sfu"
	store "github.com/atpost/chat-call-service/internal/store/postgres"
	events "github.com/atpost/chat-shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Live-PostgreSQL proofs for the two launch-safety defects:
//
//	1. permission was checked ONLY at CreateCall — never on accept or join,
//	   and no block / unmatch event ever ended a call in progress.
//	2. the target-busy check (409) and the ring limiter ran BEFORE the
//	   permission check, so CreateCall answered "is that person on a call
//	   right now?" for anyone, including people who had blocked the caller.
//
// Run with a DISPOSABLE database whose name ends in _test, and a reachable
// Redis:
//
//	CALL_POSTGRES_DSN=postgres://.../call_it_test?sslmode=disable \
//	CALL_REDIS_ADDR=localhost:6379 \
//	  go test -tags integration -run CallSafetyPG ./internal/service/ -count=1

// fakeGraph stands in for graph-service's /v1/permissions/check. Blocks are
// symmetric there, so a denied pair is denied in both directions.
type fakeGraph struct {
	srv    *httptest.Server
	mu     sync.Mutex
	denied map[string]bool
}

func newFakeGraph(t *testing.T) *fakeGraph {
	t.Helper()
	g := &fakeGraph{denied: map[string]bool{}}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller := r.Header.Get("X-User-Id")
		target := r.URL.Query().Get("target_user_id")
		allowed := !g.isDenied(caller, target)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"decisions": map[string]any{
					"call": map[string]any{"allowed": allowed, "reason": "blocked"},
				},
			},
		})
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func pairKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

func (g *fakeGraph) deny(a, b uuid.UUID) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.denied[pairKey(a.String(), b.String())] = true
}

func (g *fakeGraph) isDenied(a, b string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.denied[pairKey(a, b)]
}

// safetyService builds the real service with a REAL policy pointed at the fake
// graph, so the gate under test is the production code path.
func safetyService(t *testing.T) (*Service, *store.CallStore, *fakeGraph) {
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
	t.Cleanup(func() { _ = rdb.Close() })
	graph := newFakeGraph(t)
	svc := New(callStore, sfu.NewStubProvider(), NewRateLimiter(rdb),
		NewCallPolicy(graph.srv.URL, "internal-key"), rdb, slog.Default(), 30)
	return svc, callStore, graph
}

// inviteIDFor returns the callee's pending invite id for a call.
func inviteIDFor(t *testing.T, svc *Service, callee, callID uuid.UUID) uuid.UUID {
	t.Helper()
	invites, err := svc.ListPendingInvites(context.Background(), callee)
	if err != nil {
		t.Fatal(err)
	}
	for _, inv := range invites {
		if inv.CallID == callID {
			return inv.InviteID
		}
	}
	t.Fatalf("no pending invite for callee %s on call %s", callee, callID)
	return uuid.Nil
}

func endedCallReasons(t *testing.T, st *store.CallStore, callID uuid.UUID) []string {
	t.Helper()
	rows, err := st.FetchUnpublished(context.Background(), 200)
	if err != nil {
		t.Fatal(err)
	}
	var reasons []string
	for _, row := range rows {
		if row.EventType != events.CallEnded {
			continue
		}
		var p struct {
			CallID      string `json:"call_id"`
			EndedReason string `json:"ended_reason"`
		}
		if err := json.Unmarshal(row.Payload, &p); err != nil {
			continue
		}
		if p.CallID == callID.String() {
			reasons = append(reasons, p.EndedReason)
		}
	}
	return reasons
}

// DEFECT 1 — the headline case. A call is created while the pair is permitted;
// the callee then blocks the caller. The live call must END, both sides must be
// told through the ordinary lifecycle, and a later accept or join must be
// refused.
func TestCallSafetyPGBlockDuringCallEndsItAndRefusesLater(t *testing.T) {
	svc, st, graph := safetyService(t)
	ctx := context.Background()
	caller, callee := uuid.New(), uuid.New()

	created, err := svc.CreateCall(ctx, caller, directCallReq(callee))
	if err != nil {
		t.Fatalf("permitted create failed: %v", err)
	}
	// Capture the invite BEFORE teardown: the teardown expires pending invites.
	inviteID := inviteIDFor(t, svc, callee, created.ID)

	// The block lands mid-call, exactly as graph-service would emit it.
	graph.deny(caller, callee)
	payload, _ := json.Marshal(map[string]string{
		"blocker_id": callee.String(),
		"blocked_id": caller.String(),
	})
	handler := relationship.NewHandler(svc, domain.EndedReasonPermissionRevoked, slog.Default())
	if err := handler.Handle(ctx, relationship.EventUserBlocked, payload); err != nil {
		t.Fatalf("teardown failed: %v", err)
	}

	// The call is over, and recorded as a safety termination rather than a
	// hang-up.
	session, err := st.GetCallSession(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if session.State != domain.CallStateEnded {
		t.Fatalf("call still %s after the pair was blocked — a block must not leave media flowing", session.State)
	}
	if session.EndedReason == nil || *session.EndedReason != domain.EndedReasonPermissionRevoked {
		t.Fatalf("ended_reason = %v, want %s", session.EndedReason, domain.EndedReasonPermissionRevoked)
	}

	// Both sides are told through the existing lifecycle (CallEnded fans out
	// to the lifecycle + notification topics).
	reasons := endedCallReasons(t, st, created.ID)
	if len(reasons) == 0 {
		t.Fatal("no CallEnded event emitted — neither side would learn the call ended")
	}
	if reasons[0] != domain.EndedReasonPermissionRevoked {
		t.Fatalf("CallEnded ended_reason = %q, want %q", reasons[0], domain.EndedReasonPermissionRevoked)
	}

	// And the pair cannot resurrect it.
	if err := svc.AcceptInvite(ctx, callee, created.ID, inviteID); !errors.Is(err, ErrCallNotAllowed) {
		t.Fatalf("accept after block = %v, want ErrCallNotAllowed", err)
	}
	if _, err := svc.JoinCall(ctx, callee, created.ID); !errors.Is(err, ErrCallNotAllowed) {
		t.Fatalf("join after block = %v, want ErrCallNotAllowed", err)
	}
}

// DEFECT 1 — an unmatch is the main way a dating user cuts contact, so
// dating.match.closed must tear a live call down exactly like a block.
func TestCallSafetyPGDatingUnmatchEndsLiveCall(t *testing.T) {
	svc, st, graph := safetyService(t)
	ctx := context.Background()
	userA, userB := uuid.New(), uuid.New()

	created, err := svc.CreateCall(ctx, userA, directCallReq(userB))
	if err != nil {
		t.Fatalf("permitted create failed: %v", err)
	}

	graph.deny(userA, userB)
	payload, _ := json.Marshal(map[string]string{
		"match_id":  uuid.NewString(),
		"closed_by": userB.String(),
		"user_a":    userA.String(),
		"user_b":    userB.String(),
	})
	handler := relationship.NewHandler(svc, domain.EndedReasonPermissionRevoked, slog.Default())
	if err := handler.Handle(ctx, relationship.EventDatingMatchClosed, payload); err != nil {
		t.Fatalf("teardown failed: %v", err)
	}

	session, err := st.GetCallSession(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if session.State != domain.CallStateEnded {
		t.Fatalf("call still %s after the match closed — unmatching must end the call", session.State)
	}

	// Redelivery is safe: Kafka is at-least-once.
	if err := handler.Handle(ctx, relationship.EventDatingMatchClosed, payload); err != nil {
		t.Fatalf("redelivered teardown must be a no-op, got %v", err)
	}
}

// DEFECT 1 — accept and join must refuse with the SAME error the create path
// uses once permission is gone, even with no teardown event in sight (the
// event may be delayed, lost, or the revocation may predate the consumer).
func TestCallSafetyPGAcceptAndJoinRecheckPermission(t *testing.T) {
	svc, _, graph := safetyService(t)
	ctx := context.Background()

	t.Run("accept", func(t *testing.T) {
		caller, callee := uuid.New(), uuid.New()
		created, err := svc.CreateCall(ctx, caller, directCallReq(callee))
		if err != nil {
			t.Fatalf("permitted create failed: %v", err)
		}
		inviteID := inviteIDFor(t, svc, callee, created.ID)

		graph.deny(caller, callee)

		if err := svc.AcceptInvite(ctx, callee, created.ID, inviteID); !errors.Is(err, ErrCallNotAllowed) {
			t.Fatalf("accept with permission revoked = %v, want ErrCallNotAllowed (the create path's error)", err)
		}
	})

	t.Run("join", func(t *testing.T) {
		caller, callee := uuid.New(), uuid.New()
		created, err := svc.CreateCall(ctx, caller, directCallReq(callee))
		if err != nil {
			t.Fatalf("permitted create failed: %v", err)
		}

		graph.deny(caller, callee)

		if _, err := svc.JoinCall(ctx, callee, created.ID); !errors.Is(err, ErrCallNotAllowed) {
			t.Fatalf("join with permission revoked = %v, want ErrCallNotAllowed", err)
		}
		// The caller's own join is refused too — the gate is on the pair.
		if _, err := svc.JoinCall(ctx, caller, created.ID); !errors.Is(err, ErrCallNotAllowed) {
			t.Fatalf("initiator join with permission revoked = %v, want ErrCallNotAllowed", err)
		}
	})
}

// A forbidden pair must get the SAME refusal from create, accept and join, so
// none of the three can be used to tell one situation from another.
func TestCallSafetyPGRefusalIsIdenticalAcrossEntryPoints(t *testing.T) {
	svc, _, graph := safetyService(t)
	ctx := context.Background()
	caller, callee := uuid.New(), uuid.New()

	created, err := svc.CreateCall(ctx, caller, directCallReq(callee))
	if err != nil {
		t.Fatalf("permitted create failed: %v", err)
	}
	inviteID := inviteIDFor(t, svc, callee, created.ID)
	graph.deny(caller, callee)

	if err := svc.AcceptInvite(ctx, callee, created.ID, inviteID); !errors.Is(err, ErrCallNotAllowed) {
		t.Fatalf("accept = %v, want ErrCallNotAllowed", err)
	}
	if _, err := svc.JoinCall(ctx, callee, created.ID); !errors.Is(err, ErrCallNotAllowed) {
		t.Fatalf("join = %v, want ErrCallNotAllowed", err)
	}
	if _, err := svc.CreateCall(ctx, caller, directCallReq(callee)); !errors.Is(err, ErrCallNotAllowed) {
		t.Fatalf("create = %v, want ErrCallNotAllowed", err)
	}
}

// DEFECT 2 — the presence oracle. A caller who may NOT call the target must get
// the generic 403 whether or not the target happens to be on a call; only a
// PERMITTED caller ever sees the 409 busy refusal.
func TestCallSafetyPGForbiddenCallerCannotProbePresence(t *testing.T) {
	svc, _, graph := safetyService(t)
	ctx := context.Background()
	target := uuid.New()
	occupier, forbidden, permitted := uuid.New(), uuid.New(), uuid.New()

	// Put the target on a call.
	if _, err := svc.CreateCall(ctx, occupier, directCallReq(target)); err != nil {
		t.Fatalf("setup call failed: %v", err)
	}

	// The forbidden caller learns only "not permitted" — never that the
	// target is busy.
	graph.deny(forbidden, target)
	_, err := svc.CreateCall(ctx, forbidden, directCallReq(target))
	if errors.Is(err, ErrTargetUnavailable) {
		t.Fatal("forbidden caller got the BUSY refusal — CreateCall is a presence oracle: " +
			"the permission check must run before the target-busy check")
	}
	if !errors.Is(err, ErrCallNotAllowed) {
		t.Fatalf("forbidden caller got %v, want ErrCallNotAllowed", err)
	}

	// A permitted caller still gets the honest busy answer, so the busy
	// protection itself is intact.
	if _, err := svc.CreateCall(ctx, permitted, directCallReq(target)); !errors.Is(err, ErrTargetUnavailable) {
		t.Fatalf("permitted caller got %v, want ErrTargetUnavailable", err)
	}

	// Same story when the target is NOT busy: the forbidden caller's answer
	// is unchanged, so 403-vs-409 reveals nothing either way.
	idle := uuid.New()
	graph.deny(forbidden, idle)
	if _, err := svc.CreateCall(ctx, forbidden, directCallReq(idle)); !errors.Is(err, ErrCallNotAllowed) {
		t.Fatalf("forbidden caller (idle target) got %v, want ErrCallNotAllowed", err)
	}
}

// DEFECT 2 — moving the permission check ahead of the limiter must not weaken
// the limiter for callers who ARE permitted.
func TestCallSafetyPGRingLimiterStillAppliesToPermittedCallers(t *testing.T) {
	svc, _, _ := safetyService(t)
	ctx := context.Background()
	caller, callee := uuid.New(), uuid.New()

	// Two unanswered rings are allowed; the third trips the anti-spam gate.
	for i := 1; i <= 2; i++ {
		created, err := svc.CreateCall(ctx, caller, directCallReq(callee))
		if err != nil {
			t.Fatalf("ring %d: permitted create failed: %v", i, err)
		}
		if err := svc.EndCall(ctx, caller, created.ID); err != nil {
			t.Fatalf("ring %d: end failed: %v", i, err)
		}
	}
	if _, err := svc.CreateCall(ctx, caller, directCallReq(callee)); !errors.Is(err, ErrRingAntiSpam) {
		t.Fatalf("third ring = %v, want ErrRingAntiSpam — the limiter must still protect the callee", err)
	}
}

// A forbidden caller must not be able to burn the limiter either: their
// requests are refused before the counter is touched, so a blocked caller
// cannot spend the victim's ring budget.
func TestCallSafetyPGForbiddenCallerDoesNotConsumeRingBudget(t *testing.T) {
	svc, _, graph := safetyService(t)
	ctx := context.Background()
	caller, callee := uuid.New(), uuid.New()

	graph.deny(caller, callee)
	for i := 0; i < 5; i++ {
		if _, err := svc.CreateCall(ctx, caller, directCallReq(callee)); !errors.Is(err, ErrCallNotAllowed) {
			t.Fatalf("attempt %d = %v, want ErrCallNotAllowed", i, err)
		}
	}

	// Now permit the pair: the ring counter must be untouched, so the first
	// permitted ring succeeds.
	graph.mu.Lock()
	delete(graph.denied, pairKey(caller.String(), callee.String()))
	graph.mu.Unlock()

	if _, err := svc.CreateCall(ctx, caller, directCallReq(callee)); err != nil {
		t.Fatalf("first permitted ring failed (%v) — refused calls consumed the limiter budget", err)
	}
}

// Group calls are explicitly OUT of scope for pair-level teardown: a block
// between two of twenty participants is not grounds to end everyone's call.
// Group creation is fenced off in P0 anyway, so this pins the store scope.
func TestCallSafetyPGTeardownIsDirectCallsOnly(t *testing.T) {
	svc, st, _ := safetyService(t)
	ctx := context.Background()
	userA, userB := uuid.New(), uuid.New()

	created, err := svc.CreateCall(ctx, userA, directCallReq(userB))
	if err != nil {
		t.Fatal(err)
	}
	live, err := st.ListLiveDirectCallsBetween(ctx, userA, userB)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 || live[0].ID != created.ID {
		t.Fatalf("expected the direct call to be found for teardown, got %d rows", len(live))
	}
	for _, cs := range live {
		if !cs.IsDirectCall() {
			t.Fatalf("teardown lookup returned a non-direct call %s (%s) — group calls are out of scope",
				cs.ID, cs.CallType)
		}
	}

	// A pair with no shared call yields nothing, and a self-pair is never a
	// call between two people.
	if rows, err := st.ListLiveDirectCallsBetween(ctx, userA, uuid.New()); err != nil || len(rows) != 0 {
		t.Fatalf("unrelated pair returned %d rows (err %v)", len(rows), err)
	}
	if rows, err := st.ListLiveDirectCallsBetween(ctx, userA, userA); err != nil || len(rows) != 0 {
		t.Fatalf("self pair returned %d rows (err %v)", len(rows), err)
	}

	ended, err := svc.EndDirectCallsBetween(ctx, userA, userB, domain.EndedReasonPermissionRevoked)
	if err != nil {
		t.Fatal(err)
	}
	if ended != 1 {
		t.Fatalf("ended %d calls, want 1", ended)
	}
	// Idempotent: a redelivery ends nothing further.
	again, err := svc.EndDirectCallsBetween(ctx, userA, userB, domain.EndedReasonPermissionRevoked)
	if err != nil {
		t.Fatal(err)
	}
	if again != 0 {
		t.Fatalf("redelivery ended %d more calls, want 0", again)
	}
}
