// Audit H2 store integration tests: every chat closure (block, unmatch,
// suspension, deletion, a chat-blocking risk level) closes the matches and
// writes its dating.match.closed / dating.user.blocked rows to
// dating.outbox_events in the same transaction; repeating the state change
// enqueues nothing new; GetMatchChatState reads the authoritative state.
// Skipped unless TEST_PG_DSN is set; refuses a database not named *_test.
package store

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// outboxRowsFor returns the outbox events whose payload names id (a match
// id or a user id), oldest first.
type outboxRowForTest struct {
	EventType    string
	PartitionKey string
	Payload      map[string]any
}

func outboxRowsFor(t *testing.T, s *Store, id uuid.UUID) []outboxRowForTest {
	t.Helper()
	rows, err := s.db.Query(context.Background(), `
        SELECT event_type, partition_key, payload
        FROM `+OutboxTable+`
        WHERE payload->'payload' @> jsonb_build_object('match_id', $1::text)
           OR payload->'payload' @> jsonb_build_object('blocker_id', $1::text)
           OR payload->'payload' @> jsonb_build_object('blocked_id', $1::text)
        ORDER BY id`, id.String())
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	defer rows.Close()
	var out []outboxRowForTest
	for rows.Next() {
		var r outboxRowForTest
		var raw []byte
		if err := rows.Scan(&r.EventType, &r.PartitionKey, &raw); err != nil {
			t.Fatalf("scan outbox: %v", err)
		}
		var env struct {
			EventType string         `json:"event_type"`
			Payload   map[string]any `json:"payload"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("decode outbox envelope: %v", err)
		}
		if env.EventType != r.EventType {
			t.Fatalf("row event_type %q but envelope says %q", r.EventType, env.EventType)
		}
		r.Payload = env.Payload
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	return out
}

func closedEventsFor(t *testing.T, s *Store, matchID uuid.UUID) []outboxRowForTest {
	t.Helper()
	var out []outboxRowForTest
	for _, r := range outboxRowsFor(t, s, matchID) {
		if r.EventType == "dating.match.closed" {
			out = append(out, r)
		}
	}
	return out
}

func h2Pair(t *testing.T, s *Store) (uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	a, b := uuid.New(), uuid.New()
	ensureProfileForTest(t, s, a)
	ensureProfileForTest(t, s, b)
	id, _, err := s.CreateOrGetOpenMatch(context.Background(), a, b, nil)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	return a, b, id
}

func assertMatchClosedWith(t *testing.T, s *Store, matchID, closedBy uuid.UUID, reason string) {
	t.Helper()
	m, err := s.GetMatch(context.Background(), matchID)
	if err != nil {
		t.Fatalf("get match: %v", err)
	}
	if m.Status != "closed" || m.ClosedBy == nil || *m.ClosedBy != closedBy {
		t.Fatalf("match = status %s closed_by %v, want closed by %s", m.Status, m.ClosedBy, closedBy)
	}
	evs := closedEventsFor(t, s, matchID)
	if len(evs) != 1 {
		t.Fatalf("%d dating.match.closed rows for %s, want exactly 1", len(evs), matchID)
	}
	if evs[0].Payload["reason"] != reason || evs[0].Payload["closed_by"] != closedBy.String() {
		t.Fatalf("match.closed payload = %v, want reason %q closed_by %s", evs[0].Payload, reason, closedBy)
	}
	if evs[0].PartitionKey != MatchClosedPartitionKey(matchID) {
		t.Fatalf("partition key = %q", evs[0].PartitionKey)
	}
}

func TestH2Store_BlockEnqueuesUserBlockedAndMatchClosed(t *testing.T) {
	s, cleanup := statusITStore(t)
	defer cleanup()
	a, b, id := h2Pair(t, s)

	if _, err := s.BlockUserAndSever(context.Background(), a, b); err != nil {
		t.Fatalf("block: %v", err)
	}
	assertMatchClosedWith(t, s, id, a, CloseReasonBlock)
	var blocked int
	for _, r := range outboxRowsFor(t, s, a) {
		if r.EventType == "dating.user.blocked" && r.Payload["blocker_id"] == a.String() && r.Payload["blocked_id"] == b.String() {
			blocked++
			if r.PartitionKey != PairPartitionKey(a, b) {
				t.Fatalf("user.blocked partition key = %q", r.PartitionKey)
			}
		}
	}
	if blocked != 1 {
		t.Fatalf("%d dating.user.blocked rows for the pair, want 1", blocked)
	}
	// A repeated block closes nothing more; it re-announces the block
	// (chat applies it idempotently) and never a second match.closed.
	if _, err := s.BlockUserAndSever(context.Background(), a, b); err != nil {
		t.Fatalf("repeat block: %v", err)
	}
	if n := len(closedEventsFor(t, s, id)); n != 1 {
		t.Fatalf("repeat block enqueued %d match.closed rows in all, want 1", n)
	}
}

func TestH2Store_UnmatchEnqueuesMatchClosed(t *testing.T) {
	s, cleanup := statusITStore(t)
	defer cleanup()
	_, b, id := h2Pair(t, s)
	if err := s.CloseMatch(context.Background(), id, b); err != nil {
		t.Fatalf("unmatch: %v", err)
	}
	assertMatchClosedWith(t, s, id, b, CloseReasonUnmatch)
	if err := s.CloseMatch(context.Background(), id, b); err == nil {
		t.Fatal("closing a closed match again must fail (not found)")
	}
	if n := len(closedEventsFor(t, s, id)); n != 1 {
		t.Fatalf("%d match.closed rows after a repeated unmatch, want 1", n)
	}
}

func TestH2Store_SuspensionClosesEveryOpenMatchOnce(t *testing.T) {
	s, cleanup := statusITStore(t)
	defer cleanup()
	ctx := context.Background()
	scammer, _, m1 := h2Pair(t, s)
	v2 := uuid.New()
	ensureProfileForTest(t, s, v2)
	m2, _, err := s.CreateOrGetOpenMatch(ctx, scammer, v2, nil)
	if err != nil {
		t.Fatalf("second match: %v", err)
	}
	// A match between two other people stays open.
	_, _, other := h2Pair(t, s)

	if _, err := s.TransitionProfileStatus(ctx, scammer, ProfileEventSuspend, ProfileActorAdmin); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	assertMatchClosedWith(t, s, m1, scammer, CloseReasonSuspended)
	assertMatchClosedWith(t, s, m2, scammer, CloseReasonSuspended)
	if m, _ := s.GetMatch(ctx, other); m == nil || m.Status != "matched" {
		t.Fatalf("an unrelated match was closed: %+v", m)
	}
	if n := len(closedEventsFor(t, s, other)); n != 0 {
		t.Fatalf("an unrelated match got %d match.closed rows", n)
	}
	// Suspending again (an idempotent edge) enqueues nothing new.
	if _, err := s.TransitionProfileStatus(ctx, scammer, ProfileEventSuspend, ProfileActorAdmin); err != nil {
		t.Fatalf("suspend again: %v", err)
	}
	assertMatchClosedWith(t, s, m1, scammer, CloseReasonSuspended)
}

func TestH2Store_DeletionClosesMatchesAndRedeliveryIsANoop(t *testing.T) {
	s, cleanup := statusITStore(t)
	defer cleanup()
	ctx := context.Background()
	user, _, id := h2Pair(t, s)

	if _, err := s.TransitionProfileStatus(ctx, user, ProfileEventDelete, ProfileActorUser); err != nil {
		t.Fatalf("self delete: %v", err)
	}
	assertMatchClosedWith(t, s, id, user, CloseReasonDeleted)
	// The platform DPDP deletion arriving afterwards (or redelivered).
	if err := s.SoftDeleteProfile(ctx, user); err != nil {
		t.Fatalf("dpdp delete: %v", err)
	}
	if err := s.SoftDeleteProfile(ctx, user); err != nil {
		t.Fatalf("dpdp delete redelivered: %v", err)
	}
	assertMatchClosedWith(t, s, id, user, CloseReasonDeleted)
}

func TestH2Store_ChatBlockingRiskLevelClosesMatches(t *testing.T) {
	s, cleanup := statusITStore(t)
	defer cleanup()
	ctx := context.Background()

	// A level below chat_hold leaves the chat alone.
	calm, _, calmMatch := h2Pair(t, s)
	if err := s.UpsertAccountRisk(ctx, &AccountRisk{UserID: calm, RiskScore: 70, RiskLevel: RiskLevelHideFromDiscovery}); err != nil {
		t.Fatalf("risk: %v", err)
	}
	if m, _ := s.GetMatch(ctx, calmMatch); m == nil || m.Status != "matched" {
		t.Fatalf("hide_from_discovery closed a match: %+v", m)
	}

	held, _, id := h2Pair(t, s)
	if err := s.UpsertAccountRisk(ctx, &AccountRisk{UserID: held, RiskScore: 80, RiskLevel: RiskLevelChatHold}); err != nil {
		t.Fatalf("risk: %v", err)
	}
	assertMatchClosedWith(t, s, id, held, CloseReasonRiskHold)
	// The sweeper's next recompute at the same level enqueues nothing new.
	if err := s.UpsertAccountRisk(ctx, &AccountRisk{UserID: held, RiskScore: 81, RiskLevel: RiskLevelChatHold}); err != nil {
		t.Fatalf("risk recompute: %v", err)
	}
	assertMatchClosedWith(t, s, id, held, CloseReasonRiskHold)
}

func TestH2Store_MatchChatStateReadsAuthoritativeState(t *testing.T) {
	s, cleanup := statusITStore(t)
	defer cleanup()
	ctx := context.Background()
	state := func(id uuid.UUID) *MatchChatState {
		t.Helper()
		st, err := s.GetMatchChatState(ctx, id)
		if err != nil {
			t.Fatalf("chat state: %v", err)
		}
		return st
	}

	a, _, id := h2Pair(t, s)
	if st := state(id); !st.Open || st.Reason != "" {
		t.Fatalf("open match = %+v, want open", st)
	}
	// The authoritative state wins even where the closure never ran: a
	// suspension written without the close (a legacy row) still reads closed.
	if _, err := s.db.Exec(ctx, `UPDATE dating_profiles SET profile_status = 'suspended' WHERE user_id = $1`, a); err != nil {
		t.Fatalf("legacy suspend: %v", err)
	}
	if st := state(id); st.Open || st.Reason != ChatClosedReasonSuspended {
		t.Fatalf("legacy-suspended member = %+v, want closed (suspended)", st)
	}

	c, _, id2 := h2Pair(t, s)
	if _, err := s.db.Exec(ctx, `
        INSERT INTO dating_account_risk (user_id, risk_score, risk_level, signals)
        VALUES ($1, 90, 'admin_review', '{}'::jsonb)`, c); err != nil {
		t.Fatalf("legacy risk row: %v", err)
	}
	if st := state(id2); st.Open || st.Reason != ChatClosedReasonRiskHold {
		t.Fatalf("member in admin review = %+v, want closed (risk_hold)", st)
	}

	x, y, id3 := h2Pair(t, s)
	if _, err := s.db.Exec(ctx, `INSERT INTO dating_blocks (user_id, blocked_id) VALUES ($1, $2)`, y, x); err != nil {
		t.Fatalf("legacy block: %v", err)
	}
	if st := state(id3); st.Open || st.Reason != ChatClosedReasonBlocked {
		t.Fatalf("blocked pair = %+v, want closed (blocked)", st)
	}

	_, q, id4 := h2Pair(t, s)
	if err := s.CloseMatch(ctx, id4, q); err != nil {
		t.Fatalf("unmatch: %v", err)
	}
	if st := state(id4); st.Open || st.Reason != ChatClosedReasonMatch {
		t.Fatalf("unmatched = %+v, want closed (match_closed)", st)
	}

	if _, err := s.GetMatchChatState(ctx, uuid.New()); err != ErrMatchNotFound {
		t.Fatalf("unknown match: err = %v, want ErrMatchNotFound", err)
	}
}

// A restricted member (a moderator's restriction, or the system's under-18
// identity birth date) closes every open match like a suspension: no adult
// keeps chatting with someone flagged under 18.
func TestH2Store_RestrictionClosesEveryOpenMatchOnce(t *testing.T) {
	s, cleanup := statusITStore(t)
	defer cleanup()
	ctx := context.Background()

	minor, _, m1 := h2Pair(t, s)
	adult := uuid.New()
	ensureProfileForTest(t, s, adult)
	m2, _, err := s.CreateOrGetOpenMatch(ctx, minor, adult, nil)
	if err != nil {
		t.Fatalf("second match: %v", err)
	}
	if _, err := s.TransitionProfileStatus(ctx, minor, ProfileEventRestrict, ProfileActorSystem); err != nil {
		t.Fatalf("system restrict (under 18): %v", err)
	}
	assertMatchClosedWith(t, s, m1, minor, CloseReasonRestricted)
	assertMatchClosedWith(t, s, m2, minor, CloseReasonRestricted)
	// The system restriction is idempotent on a restricted row, and so is
	// the closure: nothing new is enqueued.
	if _, err := s.TransitionProfileStatus(ctx, minor, ProfileEventRestrict, ProfileActorSystem); err != nil {
		t.Fatalf("restrict again: %v", err)
	}
	assertMatchClosedWith(t, s, m1, minor, CloseReasonRestricted)

	// A moderator's restriction closes too.
	restricted, _, m3 := h2Pair(t, s)
	if _, err := s.TransitionProfileStatus(ctx, restricted, ProfileEventRestrict, ProfileActorAdmin); err != nil {
		t.Fatalf("admin restrict: %v", err)
	}
	assertMatchClosedWith(t, s, m3, restricted, CloseReasonRestricted)

	// pending_review is unchanged by this rule: the match stays open.
	reviewed, _, m4 := h2Pair(t, s)
	if _, err := s.TransitionProfileStatus(ctx, reviewed, ProfileEventReview, ProfileActorAdmin); err != nil {
		t.Fatalf("review: %v", err)
	}
	if m, _ := s.GetMatch(ctx, m4); m == nil || m.Status != "matched" {
		t.Fatalf("pending_review closed a match: %+v", m)
	}
}

// The chat-state answer reads a restricted member as not open, even on a
// legacy row whose restriction never closed the match.
func TestH2Store_MatchChatStateRefusesARestrictedMember(t *testing.T) {
	s, cleanup := statusITStore(t)
	defer cleanup()
	ctx := context.Background()
	_, b, id := h2Pair(t, s)
	if _, err := s.db.Exec(ctx, `UPDATE dating_profiles SET profile_status = 'restricted' WHERE user_id = $1`, b); err != nil {
		t.Fatalf("legacy restrict: %v", err)
	}
	st, err := s.GetMatchChatState(ctx, id)
	if err != nil {
		t.Fatalf("chat state: %v", err)
	}
	if st.Open || st.Reason != ChatClosedReasonRestricted {
		t.Fatalf("restricted member = %+v, want closed (restricted)", st)
	}
}

// pending_review is a reversible hold: chat-state answers not-open
// (pending_review) while it lasts, the match is NOT closed and nothing is
// enqueued, and chat-state answers open again once a moderator reinstates.
func TestH2Store_PendingReviewHoldsChatWithoutClosing(t *testing.T) {
	s, cleanup := statusITStore(t)
	defer cleanup()
	ctx := context.Background()
	reported, _, id := h2Pair(t, s)

	if _, err := s.TransitionProfileStatus(ctx, reported, ProfileEventReview, ProfileActorSystem); err != nil {
		t.Fatalf("underage-report hold: %v", err)
	}
	st, err := s.GetMatchChatState(ctx, id)
	if err != nil || st.Open || st.Reason != ChatClosedReasonPendingReview {
		t.Fatalf("chat state while held = %+v, %v; want not-open (pending_review)", st, err)
	}
	if m, _ := s.GetMatch(ctx, id); m == nil || m.Status != "matched" {
		t.Fatalf("the hold closed the match: %+v", m)
	}
	if n := len(closedEventsFor(t, s, id)); n != 0 {
		t.Fatalf("the hold enqueued %d match.closed rows; it must close nothing", n)
	}

	if _, err := s.TransitionProfileStatus(ctx, reported, ProfileEventReinstate, ProfileActorAdmin); err != nil {
		t.Fatalf("reinstate: %v", err)
	}
	st, err = s.GetMatchChatState(ctx, id)
	if err != nil || !st.Open {
		t.Fatalf("chat state after the moderator cleared the user = %+v, %v; want open", st, err)
	}
}
