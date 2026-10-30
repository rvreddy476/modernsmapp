// Audit H2 service integration tests: the chat closures go through
// dating.outbox_events (committed with the state change), not the
// best-effort producer. Skipped unless TEST_PG_DSN is set; refuses a database
// not named *_test.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// outboxEventsMentioning returns the outbox envelopes whose payload names
// any of ids as match_id, blocker_id or blocked_id, oldest first.
func outboxEventsMentioning(t *testing.T, st *store.Store, ids ...uuid.UUID) []chatEnvelope {
	t.Helper()
	ctx := context.Background()
	tx, err := st.BeginTx(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	strs := make([]string, 0, len(ids))
	for _, id := range ids {
		strs = append(strs, id.String())
	}
	rows, err := tx.Query(ctx, `
        SELECT payload FROM `+store.OutboxTable+`
        WHERE payload->'payload'->>'match_id' = ANY($1)
           OR payload->'payload'->>'blocker_id' = ANY($1)
           OR payload->'payload'->>'blocked_id' = ANY($1)
        ORDER BY id`, strs)
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	defer rows.Close()
	var out []chatEnvelope
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatalf("scan outbox: %v", err)
		}
		var env chatEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("decode outbox envelope: %v", err)
		}
		out = append(out, env)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	return out
}

func outboxMatchClosed(t *testing.T, st *store.Store, matchID uuid.UUID) int {
	t.Helper()
	n := 0
	for _, e := range outboxEventsMentioning(t, st, matchID) {
		if e.EventType == "dating.match.closed" {
			n++
		}
	}
	return n
}

func directEventsOfTypes(t *testing.T, rec *recordingWriter, types ...string) int {
	t.Helper()
	n := 0
	for _, e := range rec.events(t) {
		for _, typ := range types {
			if e.EventType == typ {
				n++
			}
		}
	}
	return n
}

// Unmatch: the close and its event commit together; nothing goes out on
// the request path.
func TestH2_UnmatchEnqueuesMatchClosedInTheOutbox(t *testing.T) {
	svc, st, rec := newD3Svc(t)
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	seedActiveProfile(t, st, a)
	seedActiveProfile(t, st, b)
	mid := d3Match(t, svc, a, b)
	before := directEventsOfTypes(t, rec, "dating.match.closed")

	if err := svc.CloseMatch(ctx, mid, a); err != nil {
		t.Fatalf("unmatch: %v", err)
	}
	if n := outboxMatchClosed(t, st, mid); n != 1 {
		t.Fatalf("%d dating.match.closed outbox rows for the unmatched match, want 1", n)
	}
	if after := directEventsOfTypes(t, rec, "dating.match.closed"); after != before {
		t.Fatalf("unmatch still published match.closed directly (%d -> %d); the outbox is the only path", before, after)
	}
}

// Moderator suspension from a report closes every open match of the
// suspended user (audit H2 scenario a).
func TestH2_AdminSuspensionClosesTheScammersMatches(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	scammer, v1, v2 := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{scammer, v1, v2} {
		seedActiveProfile(t, st, id)
	}
	m1 := d3Match(t, svc, scammer, v1)
	m2 := d3Match(t, svc, scammer, v2)
	rep, err := svc.Report(ctx, v1, ReportRequest{TargetID: scammer, Reason: "scam", Details: "asked for money"})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if _, err := svc.ActOnReport(ctx, uuid.New(), rep.ID, scammer, "suspend"); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	for _, mid := range []uuid.UUID{m1, m2} {
		m, err := st.GetMatch(ctx, mid)
		if err != nil || m.Status != "closed" {
			t.Fatalf("match %s after suspension = %+v, %v; want closed", mid, m, err)
		}
		if n := outboxMatchClosed(t, st, mid); n != 1 {
			t.Fatalf("%d dating.match.closed outbox rows for %s, want 1", n, mid)
		}
		cs, err := svc.MatchChatState(ctx, mid)
		if err != nil || cs.Open {
			t.Fatalf("chat state after suspension = %+v, %v; want closed", cs, err)
		}
	}
}

// Self-delete closes the user's matches.
func TestH2_SelfDeleteClosesMatches(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	seedActiveProfile(t, st, a)
	seedActiveProfile(t, st, b)
	mid := d3Match(t, svc, a, b)
	if err := svc.DeleteProfile(ctx, a, "user_request"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n := outboxMatchClosed(t, st, mid); n != 1 {
		t.Fatalf("%d dating.match.closed outbox rows after self delete, want 1", n)
	}
}

// Identity reporting an under-18 birth date restricts the profile, which
// closes the minor's matches and makes dating answer chat-state closed.
func TestH2_UnderageRestrictionClosesMatches(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	minor, adult := uuid.New(), uuid.New()
	seedActiveProfile(t, st, minor)
	seedActiveProfile(t, st, adult)
	mid := d3Match(t, svc, minor, adult)

	dob := time.Now().AddDate(-16, 0, 0)
	if err := svc.restrictUnderageProfile(ctx, minor, dob, store.BasicsSourceIdentity); err != nil && !errors.Is(err, ErrUnderage) {
		t.Fatalf("restrict underage: %v", err)
	}
	p, err := st.GetProfile(ctx, minor)
	if err != nil || p.ProfileStatus != store.ProfileStatusRestricted {
		t.Fatalf("profile after the under-18 birth date = %+v, %v; want restricted", p, err)
	}
	m, err := st.GetMatch(ctx, mid)
	if err != nil || m.Status != "closed" {
		t.Fatalf("match after the restriction = %+v, %v; want closed", m, err)
	}
	if n := outboxMatchClosed(t, st, mid); n != 1 {
		t.Fatalf("%d dating.match.closed outbox rows, want 1", n)
	}
	cs, err := svc.MatchChatState(ctx, mid)
	if err != nil || cs.Open {
		t.Fatalf("chat state = %+v, %v; want closed", cs, err)
	}
}

// An underage report holds the target in pending_review: their other
// matches stay open (nothing closed or enqueued) but chat-state reports
// them not-open (pending_review) until a moderator clears the target.
func TestH2_UnderageReportHoldsChatReversibly(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	target, adult, reporter := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{target, adult, reporter} {
		seedActiveProfile(t, st, id)
	}
	mid := d3Match(t, svc, target, adult)

	rep, err := svc.Report(ctx, reporter, ReportRequest{TargetID: target, Reason: store.ReportReasonUnderage, Details: "says they are 15"})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	p, err := st.GetProfile(ctx, target)
	if err != nil || p.ProfileStatus != store.ProfileStatusPendingReview {
		t.Fatalf("target after an underage report = %+v, %v; want pending_review", p, err)
	}
	cs, err := svc.MatchChatState(ctx, mid)
	if err != nil || cs.Open || cs.Reason != store.ChatClosedReasonPendingReview {
		t.Fatalf("chat state while held = %+v, %v; want not-open (pending_review)", cs, err)
	}
	if m, err := st.GetMatch(ctx, mid); err != nil || m.Status == "closed" {
		t.Fatalf("the hold closed the match: %+v, %v", m, err)
	}
	if n := outboxMatchClosed(t, st, mid); n != 0 {
		t.Fatalf("the hold enqueued %d match.closed rows", n)
	}

	if _, err := svc.ActOnReport(ctx, uuid.New(), rep.ID, target, "reinstate"); err != nil {
		t.Fatalf("moderator clears the target: %v", err)
	}
	cs, err = svc.MatchChatState(ctx, mid)
	if err != nil || !cs.Open {
		t.Fatalf("chat state after the review cleared = %+v, %v; want open", cs, err)
	}
}
