//go:build integration

package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DM consent model against a real Postgres (a *_test database only): the
// one-transaction open / reopen / promote, rollback when the rate-limit gate
// refuses, the 30-day expiry sweep's selection, the requests list query and
// the accepted-chat state graph-service asks for when deciding calls.

type gateCounter struct {
	calls int
	err   error
}

func (g *gateCounter) fn(context.Context) error {
	g.calls++
	return g.err
}

func requestRow(t *testing.T, pool *pgxpool.Pool, convID uuid.UUID) (status string, sender, receiver uuid.UUID, preview string, createdAt time.Time) {
	t.Helper()
	if err := pool.QueryRow(context.Background(), `
		SELECT status, sender_id, receiver_id, preview, created_at FROM chat.message_requests WHERE conversation_id = $1
	`, convID).Scan(&status, &sender, &receiver, &preview, &createdAt); err != nil {
		t.Fatalf("request row: %v", err)
	}
	return
}

func convFlags(t *testing.T, pool *pgxpool.Pool, convID uuid.UUID) (isRequest, declined bool) {
	t.Helper()
	if err := pool.QueryRow(context.Background(), `
		SELECT is_request, request_declined_at IS NOT NULL FROM chat.conversations WHERE id = $1
	`, convID).Scan(&isRequest, &declined); err != nil {
		t.Fatal(err)
	}
	return
}

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// decline is what DeclineRequest writes.
func decline(t *testing.T, store *ConversationStore, convID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if err := store.DeclineMessageRequest(ctx, convID); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateMessageRequestStatus(ctx, convID, "ignored"); err != nil {
		t.Fatal(err)
	}
}

func TestOpenDirectRequestLifecycle(t *testing.T) {
	store, pool := disappearingStore(t)
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	gate := &gateCounter{}

	// 1. A brand-new request: flag and envelope in one transaction.
	res, err := store.OpenDirectConversation(ctx, a, b, OpenAsRequest, gate.fn)
	if err != nil || res.Outcome != RequestCreated || gate.calls != 1 || res.RequestCreatedAt.IsZero() {
		t.Fatalf("new request: %+v err=%v gate=%d", res, err, gate.calls)
	}
	convID := res.ConversationID
	if isReq, _ := convFlags(t, pool, convID); !isReq {
		t.Fatal("a new request conversation must carry is_request")
	}
	if st, s, r, _, _ := requestRow(t, pool, convID); st != "pending" || s != a || r != b {
		t.Fatalf("envelope %s %s->%s", st, s, r)
	}

	// 2. Retried by the sender, or opened by the recipient: unchanged, and
	// the limiter is not charged.
	for _, actor := range []uuid.UUID{a, b} {
		other := b
		if actor == b {
			other = a
		}
		res, err := store.OpenDirectConversation(ctx, actor, other, OpenAsRequest, gate.fn)
		if err != nil || res.Outcome != DirectExisting || res.ConversationID != convID || gate.calls != 1 {
			t.Fatalf("retry by %v: %+v err=%v gate=%d", actor == a, res, err, gate.calls)
		}
	}

	// 3. Deleted: the next request (the service enforced the cooldown) is a
	// FRESH request — pending, empty preview, new created_at, back in
	// Requests, and the recipient's old thread hidden.
	exec(t, pool, `UPDATE chat.message_requests SET preview = 'first try' WHERE conversation_id = $1`, convID)
	_, _, _, _, firstAt := requestRow(t, pool, convID)
	decline(t, store, convID)
	time.Sleep(5 * time.Millisecond)
	res, err = store.OpenDirectConversation(ctx, a, b, OpenAsRequest, gate.fn)
	if err != nil || res.Outcome != RequestReopened || res.ConversationID != convID || gate.calls != 2 {
		t.Fatalf("after delete: %+v err=%v gate=%d", res, err, gate.calls)
	}
	st, s, r, preview, at := requestRow(t, pool, convID)
	if st != "pending" || s != a || r != b || preview != "" || !at.After(firstAt) {
		t.Fatalf("reopened envelope: %s %s->%s %q at %v (first %v)", st, s, r, preview, at, firstAt)
	}
	if isReq, declined := convFlags(t, pool, convID); !isReq || declined {
		t.Fatalf("reopened conversation is_request=%v declined=%v", isReq, declined)
	}
	var cleared bool
	if err := pool.QueryRow(ctx, `SELECT cleared_before IS NOT NULL FROM chat.conversation_members WHERE conversation_id = $1 AND user_id = $2`,
		convID, b).Scan(&cleared); err != nil || !cleared {
		t.Fatalf("recipient history must be cut at the reopen (cleared=%v err=%v)", cleared, err)
	}

	// 4. Expired (swept) and expired-but-not-yet-swept both reopen.
	exec(t, pool, `UPDATE chat.message_requests SET status = 'expired', responded_at = NOW() WHERE conversation_id = $1`, convID)
	if res, err := store.OpenDirectConversation(ctx, a, b, OpenAsRequest, gate.fn); err != nil || res.Outcome != RequestReopened {
		t.Fatalf("after expiry: %+v %v", res, err)
	}
	exec(t, pool, `UPDATE chat.message_requests SET expires_at = NOW() - INTERVAL '1 second' WHERE conversation_id = $1`, convID)
	if res, err := store.OpenDirectConversation(ctx, a, b, OpenAsRequest, gate.fn); err != nil || res.Outcome != RequestReopened {
		t.Fatalf("past expiry, unswept: %+v %v", res, err)
	}

	// 5. The decliner may request the other way round: the envelope turns.
	decline(t, store, convID)
	if res, err := store.OpenDirectConversation(ctx, b, a, OpenAsRequest, gate.fn); err != nil || res.Outcome != RequestReopened {
		t.Fatalf("reverse request: %+v %v", res, err)
	}
	if _, s, r, _, _ := requestRow(t, pool, convID); s != b || r != a {
		t.Fatalf("reverse envelope %s->%s", s, r)
	}
}

func TestOpenDirectGateRefusalRollsBack(t *testing.T) {
	store, pool := disappearingStore(t)
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	refuse := &gateCounter{err: errors.New("limiter down")}

	if _, err := store.OpenDirectConversation(ctx, a, b, OpenAsRequest, refuse.fn); !errors.Is(err, refuse.err) {
		t.Fatalf("gate refusal: %v", err)
	}
	lo, hi := normalizePair(a, b)
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM chat.direct_conversation_keys WHERE user_a = $1 AND user_b = $2`, lo, hi).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a refused request left %d conversations behind (err %v)", n, err)
	}

	// A refused REOPEN leaves the deleted request deleted.
	res, err := store.OpenDirectConversation(ctx, a, b, OpenAsRequest, nil)
	if err != nil {
		t.Fatal(err)
	}
	decline(t, store, res.ConversationID)
	if _, err := store.OpenDirectConversation(ctx, a, b, OpenAsRequest, refuse.fn); !errors.Is(err, refuse.err) {
		t.Fatalf("reopen refusal: %v", err)
	}
	if st, _, _, _, _ := requestRow(t, pool, res.ConversationID); st != "ignored" {
		t.Fatalf("refused reopen changed the status to %s", st)
	}
}

func TestOpenDirectAllowedMakesANormalChat(t *testing.T) {
	store, pool := disappearingStore(t)
	ctx := context.Background()

	// A new pair allowed by graph: a normal chat, no envelope.
	a, b := uuid.New(), uuid.New()
	res, err := store.OpenDirectConversation(ctx, a, b, OpenDirect, nil)
	if err != nil || res.Outcome != DirectCreated {
		t.Fatalf("direct: %+v %v", res, err)
	}
	if isReq, _ := convFlags(t, pool, res.ConversationID); isReq {
		t.Fatal("an allowed chat must not be a request")
	}

	// A pending request the actor SENT, and now the recipient follows them:
	// promoted, envelope accepted.
	c, d := uuid.New(), uuid.New()
	req, _ := store.OpenDirectConversation(ctx, c, d, OpenAsRequest, nil)
	// The recipient opening it while allowed leaves it a request (replying
	// is what accepts).
	if res, _ := store.OpenDirectConversation(ctx, d, c, OpenDirect, nil); res.Outcome != DirectExisting {
		t.Fatalf("recipient open: %+v", res)
	}
	res, err = store.OpenDirectConversation(ctx, c, d, OpenDirect, nil)
	if err != nil || res.Outcome != DirectPromoted || res.ConversationID != req.ConversationID {
		t.Fatalf("promote: %+v %v", res, err)
	}
	if isReq, _ := convFlags(t, pool, req.ConversationID); isReq {
		t.Fatal("promoted conversation still a request")
	}
	if st, _, _, _, _ := requestRow(t, pool, req.ConversationID); st != "accepted" {
		t.Fatalf("promoted envelope %s", st)
	}

	// A deleted request, then the recipient follows the sender: promoted.
	e, g := uuid.New(), uuid.New()
	req, _ = store.OpenDirectConversation(ctx, e, g, OpenAsRequest, nil)
	decline(t, store, req.ConversationID)
	if res, err := store.OpenDirectConversation(ctx, e, g, OpenDirect, nil); err != nil || res.Outcome != DirectPromoted {
		t.Fatalf("promote deleted: %+v %v", res, err)
	}
	if isReq, declined := convFlags(t, pool, req.ConversationID); isReq || declined {
		t.Fatalf("promoted deleted request: is_request=%v declined=%v", isReq, declined)
	}

	// Severed by a block (since lifted): revived as a request, or as a chat.
	h, k := uuid.New(), uuid.New()
	req, _ = store.OpenDirectConversation(ctx, h, k, OpenAsRequest, nil)
	exec(t, pool, `UPDATE chat.conversation_members SET left_at = NOW() WHERE conversation_id = $1`, req.ConversationID)
	exec(t, pool, `UPDATE chat.message_requests SET status = 'blocked', responded_at = NOW() WHERE conversation_id = $1`, req.ConversationID)
	gate := &gateCounter{}
	res, err = store.OpenDirectConversation(ctx, h, k, OpenAsRequest, gate.fn)
	if err != nil || res.Outcome != RequestReopened || gate.calls != 1 {
		t.Fatalf("revive as request: %+v %v gate=%d", res, err, gate.calls)
	}
	if st, _, _, _, _ := requestRow(t, pool, req.ConversationID); st != "pending" {
		t.Fatalf("revived envelope %s", st)
	}
}

func TestExpireDueMessageRequestsSelection(t *testing.T) {
	store, pool := disappearingStore(t)
	ctx := context.Background()
	exec(t, pool, `UPDATE chat.message_requests SET status = 'accepted' WHERE status = 'pending'`) // isolate from other tests
	mk := func(status string, expiresIn time.Duration) uuid.UUID {
		res, err := store.OpenDirectConversation(ctx, uuid.New(), uuid.New(), OpenAsRequest, nil)
		if err != nil {
			t.Fatal(err)
		}
		exec(t, pool, `UPDATE chat.message_requests SET status = $2, expires_at = NOW() + make_interval(secs => $3) WHERE conversation_id = $1`,
			res.ConversationID, status, expiresIn.Seconds())
		return res.ConversationID
	}
	due := map[uuid.UUID]bool{}
	for i := 0; i < 5; i++ {
		due[mk("pending", -time.Duration(i+1)*time.Hour)] = true
	}
	future := mk("pending", time.Hour)
	deletedPast := mk("ignored", -time.Hour)
	acceptedPast := mk("accepted", -time.Hour)

	first, err := store.ExpireDueMessageRequests(ctx, 2)
	if err != nil || len(first) != 2 {
		t.Fatalf("first sweep took %d (err %v), want the limit 2", len(first), err)
	}
	// Two concurrent sweeps share the rest without double-expiring.
	var mu sync.Mutex
	var rest []ExpiredRequest
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := store.ExpireDueMessageRequests(ctx, 10)
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			rest = append(rest, got...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	all := append(first, rest...)
	seen := map[uuid.UUID]bool{}
	for _, e := range all {
		if !due[e.ConversationID] || seen[e.ConversationID] {
			t.Fatalf("swept a row that is not due, or twice: %v", e.ConversationID)
		}
		seen[e.ConversationID] = true
		if st, _, _, _, _ := requestRow(t, pool, e.ConversationID); st != "expired" {
			t.Fatalf("swept row status %s", st)
		}
	}
	if len(seen) != len(due) {
		t.Fatalf("swept %d of %d due rows", len(seen), len(due))
	}
	if again, _ := store.ExpireDueMessageRequests(ctx, 10); len(again) != 0 {
		t.Fatalf("replayed sweep expired %d more", len(again))
	}
	for id, want := range map[uuid.UUID]string{future: "pending", deletedPast: "ignored", acceptedPast: "accepted"} {
		if st, _, _, _, _ := requestRow(t, pool, id); st != want {
			t.Fatalf("not-due row became %s, want %s", st, want)
		}
	}
}

func TestListRequestEnvelopesPageVisibility(t *testing.T) {
	store, pool := disappearingStore(t)
	ctx := context.Background()
	viewer := uuid.New()
	open := func(sender, receiver uuid.UUID) uuid.UUID {
		res, err := store.OpenDirectConversation(ctx, sender, receiver, OpenAsRequest, nil)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
		return res.ConversationID
	}
	inLive := open(uuid.New(), viewer)
	inDeleted := open(uuid.New(), viewer)
	decline(t, store, inDeleted)
	inExpired := open(uuid.New(), viewer)
	exec(t, pool, `UPDATE chat.message_requests SET status = 'expired' WHERE conversation_id = $1`, inExpired)
	inPastDue := open(uuid.New(), viewer)
	exec(t, pool, `UPDATE chat.message_requests SET expires_at = NOW() - INTERVAL '1 second' WHERE conversation_id = $1`, inPastDue)
	inLeft := open(uuid.New(), viewer)
	exec(t, pool, `UPDATE chat.conversation_members SET left_at = NOW() WHERE conversation_id = $1 AND user_id = $2`, inLeft, viewer)
	outLive := open(viewer, uuid.New())
	outDeleted := open(viewer, uuid.New())
	decline(t, store, outDeleted)
	outExpired := open(viewer, uuid.New())
	exec(t, pool, `UPDATE chat.message_requests SET status = 'expired' WHERE conversation_id = $1`, outExpired)
	inLive2 := open(uuid.New(), viewer)

	ids := func(direction string, after *RequestPageCursor, limit int) []uuid.UUID {
		envs, err := store.ListRequestEnvelopesPage(ctx, viewer, direction, after, limit)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]uuid.UUID, len(envs))
		for i, e := range envs {
			out[i] = e.Conversation.ID
		}
		return out
	}
	eq := func(name string, got, want []uuid.UUID) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: got %v, want %v", name, got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("%s: got %v, want %v", name, got, want)
			}
		}
	}
	eq("incoming", ids("incoming", nil, 50), []uuid.UUID{inLive2, inLive})
	// The sender still sees a deleted request exactly as before: never told.
	eq("outgoing", ids("outgoing", nil, 50), []uuid.UUID{outDeleted, outLive})
	eq("all", ids("all", nil, 50), []uuid.UUID{inLive2, outDeleted, outLive, inLive})

	// Cursor paging, newest request first; a reopened request sorts as new.
	envs, _ := store.ListRequestEnvelopesPage(ctx, viewer, "all", nil, 2)
	last := envs[len(envs)-1]
	eq("page 2", ids("all", &RequestPageCursor{At: last.RequestAt, ID: last.Conversation.ID}, 2), []uuid.UUID{outLive, inLive})
	_, sender, _, _, _ := requestRow(t, pool, inDeleted)
	if _, err := store.OpenDirectConversation(ctx, sender, viewer, OpenAsRequest, nil); err != nil {
		t.Fatal(err)
	}
	eq("reopened first", ids("incoming", nil, 50), []uuid.UUID{inDeleted, inLive2, inLive})
}

func TestDirectChatAcceptedStates(t *testing.T) {
	store, pool := disappearingStore(t)
	ctx := context.Background()
	check := func(name string, a, b uuid.UUID, want bool) {
		t.Helper()
		for _, pair := range [][2]uuid.UUID{{a, b}, {b, a}} { // unordered
			got, err := store.DirectChatAccepted(ctx, pair[0], pair[1])
			if err != nil || got != want {
				t.Fatalf("%s (%v first): accepted=%v err=%v, want %v", name, pair[0] == a, got, err, want)
			}
		}
	}
	pair := func() (uuid.UUID, uuid.UUID) { return uuid.New(), uuid.New() }

	a, b := pair()
	check("no conversation", a, b, false)

	a, b = pair()
	direct, _ := store.OpenDirectConversation(ctx, a, b, OpenDirect, nil)
	check("direct chat", a, b, true)

	a, b = pair()
	req, _ := store.OpenDirectConversation(ctx, a, b, OpenAsRequest, nil)
	check("pending request", a, b, false)
	if err := store.AcceptMessageRequest(ctx, req.ConversationID); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateMessageRequestStatus(ctx, req.ConversationID, "accepted"); err != nil {
		t.Fatal(err)
	}
	check("accepted request", a, b, true)

	a, b = pair()
	req, _ = store.OpenDirectConversation(ctx, a, b, OpenAsRequest, nil)
	decline(t, store, req.ConversationID)
	check("deleted request", a, b, false)

	a, b = pair()
	req, _ = store.OpenDirectConversation(ctx, a, b, OpenAsRequest, nil)
	exec(t, pool, `UPDATE chat.message_requests SET status = 'expired' WHERE conversation_id = $1`, req.ConversationID)
	check("expired request", a, b, false)

	// Legacy shapes: a request flag without an envelope, and a normal chat
	// whose envelope is still pending (a sever revived by the old path).
	a, b = pair()
	direct, _ = store.OpenDirectConversation(ctx, a, b, OpenDirect, nil)
	exec(t, pool, `UPDATE chat.conversations SET is_request = TRUE WHERE id = $1`, direct.ConversationID)
	check("request flag, no envelope", a, b, false)
	a, b = pair()
	direct, _ = store.OpenDirectConversation(ctx, a, b, OpenDirect, nil)
	exec(t, pool, `INSERT INTO chat.message_requests (conversation_id, sender_id, receiver_id) VALUES ($1, $2, $3)`, direct.ConversationID, a, b)
	check("normal chat, pending envelope", a, b, false)

	// Blocked (both severed) and one side gone.
	a, b = pair()
	direct, _ = store.OpenDirectConversation(ctx, a, b, OpenDirect, nil)
	if _, _, err := store.SeverDirectConversation(ctx, a, b); err != nil {
		t.Fatal(err)
	}
	check("blocked", a, b, false)
	a, b = pair()
	direct, _ = store.OpenDirectConversation(ctx, a, b, OpenDirect, nil)
	exec(t, pool, `UPDATE chat.conversation_members SET left_at = NOW() WHERE conversation_id = $1 AND user_id = $2`, direct.ConversationID, b)
	check("one side left", a, b, false)

	// A dating match's chat has its own call rules.
	a, b = pair()
	if _, _, err := store.CreateDatingMatchConversation(ctx, a, b, uuid.New()); err != nil {
		t.Fatal(err)
	}
	check("dating match chat", a, b, false)

	if got, _ := store.DirectChatAccepted(ctx, a, a); got {
		t.Fatal("a user is never in an accepted chat with themself")
	}
}

// The request sweep holds its batch limit under the plan that rescans the
// claim (see rescanPlanStore); the default plan once expired 5 for 2.
func TestExpireDueMessageRequestsNeverExceedsTheLimit(t *testing.T) {
	store, pool := disappearingStore(t)
	ctx := context.Background()
	exec(t, pool, `UPDATE chat.message_requests SET status = 'accepted' WHERE status = 'pending'`)
	for i := 0; i < 30; i++ {
		res, err := store.OpenDirectConversation(ctx, uuid.New(), uuid.New(), OpenAsRequest, nil)
		if err != nil {
			t.Fatal(err)
		}
		exec(t, pool, `UPDATE chat.message_requests SET expires_at = NOW() - make_interval(mins => $2) WHERE conversation_id = $1`, res.ConversationID, i+1)
	}
	pinned := rescanPlanStore(t)
	total := 0
	for {
		got, err := pinned.ExpireDueMessageRequests(ctx, 4)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) > 4 {
			t.Fatalf("one sweep expired %d, limit 4", len(got))
		}
		if len(got) == 0 {
			break
		}
		total += len(got)
	}
	if total != 30 {
		t.Fatalf("swept %d of 30", total)
	}
}
