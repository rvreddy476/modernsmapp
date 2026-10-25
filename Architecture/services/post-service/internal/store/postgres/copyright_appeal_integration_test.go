//go:build integration

// Migration 076 on the real schema, against a *_test database
// (M7_POSTGRES_DSN=…/post_it_test):
//
//	M7_POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/store/postgres/ -run AppealIT -v
//
// The author's one appeal of a block keeps the video hidden until a
// reviewer clears it (then it is announced once) or blocks it again; an
// owner match on a scheduled post owes its announcement to the clear and
// the flip withholds it meanwhile; an admin's track removal restates
// eligibility without earnings_excluded and is audited.

package postgres_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	store "github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

func announceOnce(calls *int) store.TitleReviewAnnounce {
	return func(rev int64) (string, interface{}, error) {
		*calls++
		return events.PostCreated, events.PostCreatedPayload{SearchRev: rev}, nil
	}
}

func countType(types []string, want string) int {
	n := 0
	for _, t := range types {
		if t == want {
			n++
		}
	}
	return n
}

func TestAppealIT_SystemBlockAppealedThenCleared(t *testing.T) {
	it := newRestrictionIT(t)
	post, review := it.ownerHeldPost(it.ownerTitle("Jailer 2"), false)

	r, err := it.s.RequestTitleReview(it.ctx, post, it.author, "I licensed it")
	if err != nil || r.State != store.TitleReviewPending || r.AppealedAt == nil || r.DecidedAt != nil || r.DecidedBy != nil {
		t.Fatalf("appeal: review=%+v err=%v", r, err)
	}
	// Still hidden: the hold was not touched.
	if n, eff, _ := it.state(post); n != 1 || eff != "restricted" {
		t.Fatalf("appealed post: count=%d effective=%s, want still restricted", n, eff)
	}
	if countType(it.outboxTypes(post), events.PostCreated) != 0 {
		t.Fatal("the appeal announced the post")
	}
	evs := it.reviewEvents(post)
	if last := evs[len(evs)-1]; last.State != "pending" || !last.Appeal {
		t.Fatalf("appeal event = %+v", last)
	}
	// In the reviewer queue.
	queued := false
	pending, _, _ := it.s.ListTitleReviews(it.ctx, "pending", "", 200)
	for _, q := range pending {
		queued = queued || q.ID == review
	}
	if !queued {
		t.Fatal("the appeal is not in the reviewer queue")
	}
	// A second appeal is refused while it waits.
	if _, err := it.s.RequestTitleReview(it.ctx, post, it.author, "again"); !errors.Is(err, store.ErrTitleReviewAppealed) {
		t.Fatalf("second appeal: err=%v", err)
	}
	// Someone else cannot appeal it.
	if _, err := it.s.RequestTitleReview(it.ctx, post, uuid.New(), "mine"); !errors.Is(err, store.ErrTitleReviewNotFound) {
		t.Fatalf("stranger: err=%v", err)
	}

	calls := 0
	out, err := it.s.DecideTitleReview(it.ctx, store.DecideTitleReviewInput{ReviewID: review, Decision: "clear", ActorID: uuid.New()}, announceOnce(&calls))
	if err != nil || !out.Announced || calls != 1 {
		t.Fatalf("clear: out=%+v calls=%d err=%v", out, calls, err)
	}
	if n, _, _ := it.state(post); n != 0 {
		t.Fatalf("cleared post still held: count=%d", n)
	}
	if got := countType(it.outboxTypes(post), events.PostCreated); got != 1 {
		t.Fatalf("PostCreated written %d times, want once", got)
	}
	if _, err := it.s.RequestTitleReview(it.ctx, post, it.author, "again"); !errors.Is(err, store.ErrTitleReviewDecided) {
		t.Fatalf("after the clear: err=%v", err)
	}
	var history []string
	rows, _ := it.pool.Query(it.ctx, `SELECT action FROM copyright_title_review_events WHERE review_id=$1 ORDER BY created_at`, review)
	for rows.Next() {
		var a string
		_ = rows.Scan(&a)
		history = append(history, a)
	}
	rows.Close()
	if len(history) < 3 || history[len(history)-2] != "appeal" || history[len(history)-1] != "clear" {
		t.Fatalf("history = %v", history)
	}
}

func TestAppealIT_ReviewerBlockAppealedAndConfirmed(t *testing.T) {
	it := newRestrictionIT(t)
	post, review := it.heldPost(it.title("Leo"), false)
	if _, err := it.s.DecideTitleReview(it.ctx, store.DecideTitleReviewInput{ReviewID: review, Decision: "block", ActorID: uuid.New()}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := it.s.RequestTitleReview(it.ctx, post, it.author, "fair use"); err != nil {
		t.Fatal(err)
	}
	if _, err := it.s.DecideTitleReview(it.ctx, store.DecideTitleReviewInput{ReviewID: review, Decision: "block", ActorID: uuid.New()}, nil); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if n, eff, _ := it.state(post); n != 1 || eff != "restricted" {
		t.Fatalf("confirmed block: count=%d effective=%s", n, eff)
	}
	evs := it.reviewEvents(post)
	if last := evs[len(evs)-1]; last.State != "blocked" || !last.Appeal {
		t.Fatalf("the answer to the appeal = %+v, want blocked with appeal", last)
	}
	if _, err := it.s.RequestTitleReview(it.ctx, post, it.author, "again"); !errors.Is(err, store.ErrTitleReviewAppealed) {
		t.Fatalf("appeal after the answer: err=%v", err)
	}
	// The CHECK: an appeal always carries the request's time.
	if _, err := it.pool.Exec(it.ctx, `UPDATE copyright_title_reviews SET author_requested_at = NULL WHERE id = $1`, review); err == nil {
		t.Fatal("an appeal without author_requested_at was accepted")
	}
}

// An owner match on a scheduled post: the flip withholds its PostCreated,
// the reviewer's clear announces it once.
func TestAppealIT_ScheduledOwnerHoldIsAnnouncedOnClear(t *testing.T) {
	it := newRestrictionIT(t)
	titleID := it.ownerTitle("Jailer 2")
	post := it.seed("public", "approved", "long_video")
	if _, err := it.pool.Exec(it.ctx, `UPDATE posts SET publish_at = NOW() + interval '1 hour' WHERE id = $1`, post); err != nil {
		t.Fatal(err)
	}
	owner := "Sun Pictures"
	out, err := it.s.ApplyOwnerMatch(it.ctx, store.OwnerMatchInput{PostID: post, TitleID: titleID, MatchedTitle: "Jailer 2",
		RightsOwner: &owner, Policy: store.OwnerPolicyMute})
	if err != nil || out.Outcome != store.OwnerMatchHeld {
		t.Fatalf("mute: %+v %v", out, err)
	}
	r, err := it.s.GetTitleReview(it.ctx, *out.ReviewID)
	if err != nil || !r.AnnounceOnClear {
		t.Fatalf("review = %+v err=%v, want the announcement owed", r, err)
	}
	flips := 0
	pub, err := it.s.PublishScheduledPost(it.ctx, post, nil, time.Now().UTC(), false, func(rev int64) (string, interface{}, error) {
		flips++
		return events.PostCreated, events.PostCreatedPayload{SearchRev: rev}, nil
	})
	if err != nil || pub == nil || !pub.Held || flips != 0 {
		t.Fatalf("flip: pub=%+v flips=%d err=%v, want held and nothing announced", pub, flips, err)
	}
	calls := 0
	dec, err := it.s.DecideTitleReview(it.ctx, store.DecideTitleReviewInput{ReviewID: r.ID, Decision: "clear", ActorID: uuid.New()}, announceOnce(&calls))
	if err != nil || !dec.Announced || calls != 1 {
		t.Fatalf("clear: %+v calls=%d err=%v", dec, calls, err)
	}
	if got := countType(it.outboxTypes(post), events.PostCreated); got != 1 {
		t.Fatalf("PostCreated written %d times, want once", got)
	}
}

func TestAppealIT_TrackRemoval(t *testing.T) {
	it := newRestrictionIT(t)
	titleID := it.ownerTitle("Jailer 2")
	post := it.seed("public", "approved", "long_video")
	if out, err := it.s.ApplyOwnerMatch(it.ctx, store.OwnerMatchInput{PostID: post, TitleID: titleID, MatchedTitle: "Jailer 2",
		Reference: "fp-7", Policy: store.OwnerPolicyTrack}); err != nil || out.Outcome != store.OwnerMatchTracked {
		t.Fatalf("track: %+v %v", out, err)
	}
	actor := uuid.New()
	rm, err := it.s.RemoveCopyrightOwnerTrack(it.ctx, post, titleID, actor, "licence shown")
	if err != nil || rm.StillTracked {
		t.Fatalf("remove: %+v %v", rm, err)
	}
	var raw []byte
	if err := it.pool.QueryRow(it.ctx, `SELECT payload FROM post_outbox_events WHERE aggregate_id=$1 AND event_type=$2 ORDER BY created_at DESC, ctid DESC LIMIT 1`,
		post, events.PostSearchEligibilityChanged).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var el events.PostSearchEligibilityChangedPayload
	_ = json.Unmarshal(raw, &el)
	if el.EarningsExcluded {
		t.Fatalf("eligibility after removal = %+v, want earnings no longer excluded", el)
	}
	var audits int
	if err := it.pool.QueryRow(it.ctx, `SELECT COUNT(*) FROM post_admin_audit WHERE action='copyright_track.remove' AND target_id=$1 AND actor_user_id=$2`,
		post, actor).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audit rows = %d err=%v", audits, err)
	}
	if _, err := it.s.RemoveCopyrightOwnerTrack(it.ctx, post, titleID, actor, ""); !errors.Is(err, store.ErrOwnerTrackNotFound) {
		t.Fatalf("second removal: err=%v", err)
	}
}
