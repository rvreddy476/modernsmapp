//go:build integration

// Upheld copyright blocks and the long-video pause against
// trust_safety_it_test (migration 019; openM7TrustDB refuses any database
// whose name does not end in _test). A redelivered block is one block; a
// title-review block, a held copyright case and a copyright strike naming
// that case count as two blocks, not three; three within 90 days pause long
// video in the standing answer while the standing stays ok.
package service

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

func TestCopyrightBlocksIT_CountAndPause(t *testing.T) {
	pool, svc, extras := newStrikeRig(t)
	ctx := context.Background()
	user := uuid.New()
	now := time.Now().UTC()

	review := uuid.New()
	block := postgres.CopyrightBlockInput{UserID: user, PostID: uuid.New(), ReviewID: review, MatchedTitle: "Jailer 2", BlockedAt: now.Add(-20 * 24 * time.Hour)}
	if created, err := extras.RecordCopyrightBlock(ctx, block); err != nil || !created {
		t.Fatalf("first record: created=%v err=%v", created, err)
	}
	if created, err := extras.RecordCopyrightBlock(ctx, block); err != nil || created {
		t.Fatalf("replay: created=%v err=%v", created, err)
	}

	// A held copyright case and a copyright strike naming it: one block.
	caseID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO trust.copyright_cases (id, subject_post_id, subject_author_id, state, policy_version, created_at)
		VALUES ($1,$2,$3,'hold_active','copyright-v1',$4)`, caseID, uuid.New(), user, now.Add(-10*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.IssueStrike(ctx, IssueStrikeInput{UserID: user, Reason: "copyright upheld", Severity: "warning", CaseID: &caseID,
		StrikeGroup: "copyright", IdempotencyKey: "copyright_case:" + caseID.String() + ":strike"}, adminMeta(uuid.New(), "copyright upheld")); err != nil {
		t.Fatal(err)
	}
	// A released case does not count.
	if _, err := pool.Exec(ctx, `INSERT INTO trust.copyright_cases (id, subject_post_id, subject_author_id, state, policy_version)
		VALUES ($1,$2,$3,'hold_released','copyright-v1')`, uuid.New(), uuid.New(), user); err != nil {
		t.Fatal(err)
	}
	times, err := extras.CopyrightBlockTimes(ctx, user, now.Add(-postgres.CopyrightBlockLookback))
	if err != nil || len(times) != 2 {
		t.Fatalf("blocks = %v err=%v, want 2", times, err)
	}
	st, err := svc.Standing(ctx, user)
	if err != nil || st.LongVideoPausedUntil != nil {
		t.Fatalf("two blocks: paused=%v err=%v", st.LongVideoPausedUntil, err)
	}

	third := postgres.CopyrightBlockInput{UserID: user, PostID: uuid.New(), ReviewID: uuid.New(), BlockedAt: now.Add(-time.Hour)}
	if _, err := extras.RecordCopyrightBlock(ctx, third); err != nil {
		t.Fatal(err)
	}
	st, err = svc.Standing(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if st.Standing != StandingOK || st.LongVideoPausedUntil == nil || st.LongVideoPausedUntil.Sub(third.BlockedAt.Add(LongVideoPauseDuration)).Abs() > time.Second {
		t.Fatalf("three blocks: standing=%s paused=%v", st.Standing, st.LongVideoPausedUntil)
	}

	// A voided block no longer counts.
	if _, err := pool.Exec(ctx, `UPDATE trust.copyright_blocks SET voided_at = now(), void_reason = 'overturned' WHERE source_ref = $1`, third.ReviewID); err != nil {
		t.Fatal(err)
	}
	if st, _ := svc.Standing(ctx, user); st.LongVideoPausedUntil != nil {
		t.Fatalf("a voided block still pauses: %v", st.LongVideoPausedUntil)
	}
}

// An overturned block (post-service migration 076: the author appealed and
// a reviewer cleared it) stops counting and lifts the pause; the void is
// idempotent per review; a redelivered old block or old clear changes
// nothing; a clear of a review never blocked changes nothing; a block of
// the same review after the clear counts once again.
func TestCopyrightBlocksIT_OverturnLiftsPause(t *testing.T) {
	pool, svc, extras := newStrikeRig(t)
	ctx := context.Background()
	user, reviewer := uuid.New(), uuid.New()
	now := time.Now().UTC()

	var blocks []postgres.CopyrightBlockInput
	for _, ago := range []time.Duration{30 * 24 * time.Hour, 20 * 24 * time.Hour, 2 * time.Hour} {
		b := postgres.CopyrightBlockInput{UserID: user, PostID: uuid.New(), ReviewID: uuid.New(), MatchedTitle: "Jailer 2", BlockedAt: now.Add(-ago)}
		if created, err := extras.RecordCopyrightBlock(ctx, b); err != nil || !created {
			t.Fatalf("record: created=%v err=%v", created, err)
		}
		blocks = append(blocks, b)
	}
	paused := func() *time.Time {
		t.Helper()
		st, err := svc.Standing(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		return st.LongVideoPausedUntil
	}
	if paused() == nil {
		t.Fatal("three blocks did not pause long video")
	}

	third := blocks[2]
	clear := postgres.CopyrightBlockVoidInput{UserID: user, ReviewID: third.ReviewID, ClearedAt: third.BlockedAt.Add(time.Hour),
		VoidedBy: &reviewer, Reason: postgres.CopyrightBlockVoidAppealCleared}
	if voided, err := extras.VoidCopyrightBlock(ctx, clear); err != nil || !voided {
		t.Fatalf("void: voided=%v err=%v", voided, err)
	}
	if p := paused(); p != nil {
		t.Fatalf("the pause outlived the overturned block: %v", p)
	}
	var reason string
	var by *uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT void_reason, voided_by FROM trust.copyright_blocks WHERE source_ref = $1 AND voided_at IS NOT NULL`,
		third.ReviewID).Scan(&reason, &by); err != nil || reason != postgres.CopyrightBlockVoidAppealCleared || by == nil || *by != reviewer {
		t.Fatalf("void row: reason=%q by=%v err=%v", reason, by, err)
	}

	// Replays: the clear again, the overturned block again.
	if voided, err := extras.VoidCopyrightBlock(ctx, clear); err != nil || voided {
		t.Fatalf("replayed void: voided=%v err=%v", voided, err)
	}
	if created, err := extras.RecordCopyrightBlock(ctx, third); err != nil || created {
		t.Fatalf("replayed block after its void: created=%v err=%v", created, err)
	}
	if p := paused(); p != nil {
		t.Fatalf("a replay brought the pause back: %v", p)
	}

	// A clear of a review that was never blocked, and one naming another
	// user, change nothing.
	if voided, err := extras.VoidCopyrightBlock(ctx, postgres.CopyrightBlockVoidInput{UserID: user, ReviewID: uuid.New(),
		ClearedAt: now, Reason: postgres.CopyrightBlockVoidReviewCleared}); err != nil || voided {
		t.Fatalf("clear without a block: voided=%v err=%v", voided, err)
	}
	other := clear
	other.UserID, other.ReviewID, other.ClearedAt = uuid.New(), blocks[1].ReviewID, now
	if voided, err := extras.VoidCopyrightBlock(ctx, other); err != nil || voided {
		t.Fatalf("clear naming another user: voided=%v err=%v", voided, err)
	}

	// Blocked again after the clear: one new block, counted once.
	reblock := third
	reblock.BlockedAt = clear.ClearedAt.Add(30 * time.Minute)
	if created, err := extras.RecordCopyrightBlock(ctx, reblock); err != nil || !created {
		t.Fatalf("re-block: created=%v err=%v", created, err)
	}
	if created, err := extras.RecordCopyrightBlock(ctx, reblock); err != nil || created {
		t.Fatalf("replayed re-block: created=%v err=%v", created, err)
	}
	// The old clear redelivered after the re-block does not void it.
	if voided, err := extras.VoidCopyrightBlock(ctx, clear); err != nil || voided {
		t.Fatalf("old clear after the re-block: voided=%v err=%v", voided, err)
	}
	if p := paused(); p == nil || p.Sub(reblock.BlockedAt.Add(LongVideoPauseDuration)).Abs() > time.Second {
		t.Fatalf("re-block: paused=%v, want from the re-block", p)
	}
	var rows, active int
	if err := pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE voided_at IS NULL) FROM trust.copyright_blocks WHERE source_ref = $1`,
		third.ReviewID).Scan(&rows, &active); err != nil || rows != 2 || active != 1 {
		t.Fatalf("review rows=%d active=%d err=%v, want 2 and 1", rows, active, err)
	}
	times, err := extras.CopyrightBlockTimes(ctx, user, now.Add(-postgres.CopyrightBlockLookback))
	if err != nil || len(times) != 3 {
		t.Fatalf("blocks = %v err=%v, want 3", times, err)
	}
}
