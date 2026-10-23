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
