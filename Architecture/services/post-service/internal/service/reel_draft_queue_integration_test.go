//go:build integration

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The scheduled reel-draft queue against a real Postgres (HIGH 1 and LOW 11,
// 2026-10-30), so migration 079 is itself under test:
//
//	POSTGRES_DSN=postgres://…/post_it_test?sslmode=disable go test -tags integration ./internal/service/ -run ReelDraftQueueIT
//
// Refuses any database whose name does not end in _test (openStandingDB).

func queueITDraft(t *testing.T, pool *pgxpool.Pool, st *postgres.Store, author uuid.UUID, media *uuid.UUID, at time.Time) *postgres.ReelDraft {
	t.Helper()
	d := &postgres.ReelDraft{AuthorID: author, MediaID: media, Caption: "queue proof", Visibility: "public", ScheduleAt: &at,
		License: "standard", RemixSetting: "allow", CommentModeration: "none", CommentAccess: "everyone",
		LikesEnabled: true, CommentsEnabled: true, PublishToFeed: true, OriginalAudioVol: 1, OverlayAudioVol: 1}
	if err := st.CreateDraft(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM reel_drafts WHERE id = $1`, d.ID) })
	return d
}

type queueRow struct {
	status     string
	reason     *string
	scheduleAt *time.Time
	attempts   int
	next       *time.Time
}

func queueITRow(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) queueRow {
	t.Helper()
	var r queueRow
	if err := pool.QueryRow(context.Background(),
		`SELECT status, blocked_reason, schedule_at, publish_attempts, next_attempt_at FROM reel_drafts WHERE id = $1`, id).
		Scan(&r.status, &r.reason, &r.scheduleAt, &r.attempts, &r.next); err != nil {
		t.Fatal(err)
	}
	return r
}

func setClaimed(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `UPDATE reel_drafts SET status = 'publishing_pending' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
}

// 50 drafts that can never publish used to hold the 50-row claim window
// forever. One tick parks them, with the reason, and the next claim
// reaches everyone else's.
func TestReelDraftQueueIT_RefusedDraftsAreParkedAndFreeTheWindow(t *testing.T) {
	pool := openStandingDB(t)
	svc, _, st := standingITService(t, pool, standingOKBody)
	ctx := context.Background()
	attacker, other := uuid.New(), uuid.New()
	base := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

	bad := make([]uuid.UUID, 0, 50)
	for i := 0; i < 50; i++ {
		// No video: CreateDraft/UpdateDraft now refuse to schedule it, but a
		// row saved before that rule is exactly this.
		d := queueITDraft(t, pool, st, attacker, nil, base.Add(time.Duration(i)*time.Second))
		bad = append(bad, d.ID)
	}
	media := uuid.New()
	waiting := queueITDraft(t, pool, st, other, &media, base.Add(time.Hour))

	if _, err := svc.PublishScheduledDrafts(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range bad {
		r := queueITRow(t, pool, id)
		if r.status != "blocked" || r.reason == nil || *r.reason != "no_video" {
			t.Fatalf("a draft with no video after one tick: %+v", r)
		}
	}
	claimed, err := st.ClaimDueReelDrafts(ctx, time.Now().UTC(), time.Hour, 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range claimed {
		if c.ID == waiting.ID {
			found = true
		}
		for _, id := range bad {
			if c.ID == id {
				t.Fatal("a parked draft was claimed again")
			}
		}
	}
	if !found {
		t.Fatal("the next tick did not reach the draft behind the refused ones")
	}
}

// A transient failure counts, waits for next_attempt_at, and parks the
// draft as publish_failed at the limit. A draft that is not claimed any
// more is left alone.
func TestReelDraftQueueIT_TransientFailureBacksOff(t *testing.T) {
	pool := openStandingDB(t)
	_, _, st := standingITService(t, pool, standingOKBody)
	ctx := context.Background()
	media := uuid.New()
	d := queueITDraft(t, pool, st, uuid.New(), &media, time.Date(2000, 1, 3, 0, 0, 0, 0, time.UTC))
	now := time.Now().UTC()

	setClaimed(t, pool, d.ID)
	parked, err := st.DeferReelDraftPublish(ctx, d.ID, reelDraftRetryPlan(now))
	if err != nil || parked {
		t.Fatalf("first failure: parked=%v err=%v", parked, err)
	}
	r := queueITRow(t, pool, d.ID)
	if r.status != "draft" || r.attempts != 1 || r.next == nil || r.next.Sub(now) < 59*time.Second || r.next.Sub(now) > 61*time.Second {
		t.Fatalf("after one failure: %+v", r)
	}
	claimedIDs := func(at time.Time) map[uuid.UUID]bool {
		t.Helper()
		out := map[uuid.UUID]bool{}
		cs, err := st.ClaimDueReelDrafts(ctx, at, time.Hour, 500)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cs {
			out[c.ID] = true
			if c.ID != d.ID {
				// Hand back what this test did not mean to claim.
				_, _ = pool.Exec(ctx, `UPDATE reel_drafts SET status = 'draft' WHERE id = $1 AND status = 'publishing_pending'`, c.ID)
			}
		}
		return out
	}
	if claimedIDs(now)[d.ID] {
		t.Fatal("a draft waiting on its next attempt was claimed")
	}
	if !claimedIDs(now.Add(2 * time.Minute))[d.ID] {
		t.Fatal("a draft past its next attempt was not claimed")
	}

	// At the limit it is parked.
	if _, err := pool.Exec(ctx, `UPDATE reel_drafts SET publish_attempts = $2, status = 'publishing_pending' WHERE id = $1`,
		d.ID, maxReelDraftPublishAttempts-1); err != nil {
		t.Fatal(err)
	}
	parked, err = st.DeferReelDraftPublish(ctx, d.ID, reelDraftRetryPlan(now))
	if err != nil || !parked {
		t.Fatalf("at the limit: parked=%v err=%v", parked, err)
	}
	r = queueITRow(t, pool, d.ID)
	if r.status != "blocked" || r.reason == nil || *r.reason != BlockReasonPublishFailed || r.next != nil {
		t.Fatalf("parked at the limit: %+v", r)
	}

	// Not claimed (already parked): nothing changes.
	parked, err = st.DeferReelDraftPublish(ctx, d.ID, reelDraftRetryPlan(now))
	if err != nil || parked {
		t.Fatalf("an unclaimed draft: parked=%v err=%v", parked, err)
	}
	if again := queueITRow(t, pool, d.ID); again.attempts != r.attempts || again.status != "blocked" {
		t.Fatalf("an unclaimed draft was changed: %+v", again)
	}
}

// LOW 11: an edit revives a parked draft. Its passed schedule_at goes with
// it unless the edit sets a new one, so an edit never publishes it on the
// spot; scheduling again restarts the backoff.
func TestReelDraftQueueIT_EditRevivesABlockedDraft(t *testing.T) {
	pool := openStandingDB(t)
	svc, _, st := standingITService(t, pool, standingOKBody)
	ctx := context.Background()
	author := uuid.New()
	media := uuid.New()
	d := queueITDraft(t, pool, st, author, &media, time.Now().Add(-time.Hour))
	block := func() {
		t.Helper()
		setClaimed(t, pool, d.ID)
		if _, err := pool.Exec(ctx, `UPDATE reel_drafts SET publish_attempts = 7 WHERE id = $1`, d.ID); err != nil {
			t.Fatal(err)
		}
		if err := st.MarkReelDraftBlocked(ctx, d.ID, "invalid_tier"); err != nil {
			t.Fatal(err)
		}
		if r := queueITRow(t, pool, d.ID); r.status != "blocked" {
			t.Fatalf("setup: %+v", r)
		}
	}

	block()
	caption := "fixed"
	if _, err := svc.UpdateDraft(ctx, d.ID, author, &UpdateDraftInput{Caption: &caption}); err != nil {
		t.Fatal(err)
	}
	r := queueITRow(t, pool, d.ID)
	if r.status != "draft" || r.reason != nil || r.scheduleAt != nil || r.attempts != 7 {
		t.Fatalf("after an edit: %+v, want draft, no reason, no schedule, backoff kept", r)
	}

	block()
	at := time.Now().Add(time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339)
	if _, err := svc.UpdateDraft(ctx, d.ID, author, &UpdateDraftInput{ScheduleAt: &at}); err != nil {
		t.Fatal(err)
	}
	r = queueITRow(t, pool, d.ID)
	if r.status != "draft" || r.reason != nil || r.scheduleAt == nil || r.scheduleAt.UTC().Format(time.RFC3339) != at || r.attempts != 0 || r.next != nil {
		t.Fatalf("after rescheduling: %+v", r)
	}

	// Someone else's edit revives nothing.
	block()
	if got, err := svc.UpdateDraft(ctx, d.ID, uuid.New(), &UpdateDraftInput{Caption: &caption}); err == nil || got != nil {
		t.Fatalf("a stranger's edit answered %+v, %v; want no draft and an error", got, err)
	}
	if r := queueITRow(t, pool, d.ID); r.status != "blocked" {
		t.Fatalf("a stranger's edit revived the draft: %+v", r)
	}

	// A draft without a video cannot be scheduled.
	novideo := queueITDraft(t, pool, st, author, nil, time.Now().Add(-time.Hour))
	if _, err := svc.UpdateDraft(ctx, novideo.ID, author, &UpdateDraftInput{ScheduleAt: &at}); !errors.Is(err, ErrDraftNeedsVideo) {
		t.Fatalf("scheduling a draft without a video: %v", err)
	}
}

// Migration 079 lets a draft hold every visibility CreatePost takes.
func TestReelDraftQueueIT_FriendsVisibility(t *testing.T) {
	pool := openStandingDB(t)
	svc, _, st := standingITService(t, pool, standingOKBody)
	ctx := context.Background()
	author := uuid.New()
	media := uuid.New()
	d := queueITDraft(t, pool, st, author, &media, time.Now().Add(time.Hour))
	friends := "friends"
	if _, err := svc.UpdateDraft(ctx, d.ID, author, &UpdateDraftInput{Visibility: &friends}); err != nil {
		t.Fatalf("friends-only draft: %v", err)
	}
	bad := "world"
	if _, err := svc.UpdateDraft(ctx, d.ID, author, &UpdateDraftInput{Visibility: &bad}); !errors.Is(err, ErrInvalidVisibility) {
		t.Fatalf("visibility world: %v", err)
	}
}
