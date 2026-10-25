//go:build integration

// Owner-registered blocks and owner matches on the real schema (migration
// 075), against a *_test database (M7_POSTGRES_DSN=…/post_it_test):
//
//	M7_POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/store/postgres/ -run OwnerBlockIT -v
//
// An auto_block entry's hold is blocked at publication with no reviewer
// (decided_by NULL, reason owner_registered — the 075 decision-shape CHECK
// admits it and nothing else); the duration rule blocks a waiting one; an
// owner match blocks, holds (mute) or tracks; a track carries
// earnings_excluded on every later eligibility event.

package postgres_test

import (
	"context"
	"encoding/json"
	"testing"

	store "github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (it *restrictionIT) ownerTitle(name string) uuid.UUID {
	it.t.Helper()
	owner := "Sun Pictures"
	tt, err := it.s.CreateCopyrightTitle(it.ctx, uuid.New(), store.CopyrightTitleWrite{Title: name, Active: true, AutoBlock: true, RightsOwner: &owner}, uuid.New())
	if err != nil {
		it.t.Fatal(err)
	}
	if !tt.AutoBlock || tt.RightsOwner == nil || *tt.RightsOwner != owner {
		it.t.Fatalf("entry = %+v", tt)
	}
	return tt.ID
}

func (it *restrictionIT) ownerHeldPost(titleID uuid.UUID, awaiting bool) (uuid.UUID, uuid.UUID) {
	it.t.Helper()
	id := uuid.New()
	owner := "Sun Pictures"
	hold := &store.TitleReviewHold{ReviewID: uuid.New(), TitleID: titleID, MatchedTitle: "Jailer 2", MatchedTerm: "Jailer 2",
		MatchedField: "title", AnnounceOnClear: true, AutoBlock: true, RightsOwner: &owner}
	if awaiting {
		hold.AwaitingDuration = true
	} else {
		d := 3600
		hold.DurationSeconds = &d
	}
	p := &store.Post{ID: id, AuthorID: it.author, Text: "full movie", Visibility: "public", ContentType: "long_video",
		Title: "Jailer 2 full movie", ReviewStatus: "approved", TitleReviewWrite: hold}
	if err := it.s.CreatePostWithEventIdempotent(it.ctx, p, "", nil, nil); err != nil {
		it.t.Fatal(err)
	}
	return id, hold.ReviewID
}

func (it *restrictionIT) reviewEvents(postID uuid.UUID) []events.PostCopyrightReviewPayload {
	it.t.Helper()
	// Write order: post_outbox_events.id is a random UUID (gen_random_uuid),
	// so ORDER BY id shuffled the events; created_at is each write's
	// transaction time, ctid breaks ties inside one transaction.
	rows, err := it.pool.Query(it.ctx, `SELECT payload FROM post_outbox_events WHERE aggregate_id=$1 AND event_type=$2 ORDER BY created_at, ctid`,
		postID, events.EventPostCopyrightReview)
	if err != nil {
		it.t.Fatal(err)
	}
	defer rows.Close()
	var out []events.PostCopyrightReviewPayload
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			it.t.Fatal(err)
		}
		var p events.PostCopyrightReviewPayload
		_ = json.Unmarshal(raw, &p)
		out = append(out, p)
	}
	return out
}

func TestOwnerBlockIT_AutoBlockAtPublication(t *testing.T) {
	it := newRestrictionIT(t)
	post, review := it.ownerHeldPost(it.ownerTitle("Jailer 2"), false)
	r, err := it.s.GetTitleReview(it.ctx, review)
	if err != nil {
		t.Fatal(err)
	}
	if r.State != store.TitleReviewBlocked || r.DecidedBy != nil || r.DecidedAt == nil || r.Reason == nil || *r.Reason != "owner_registered" {
		t.Fatalf("review = %+v, want a system block", r)
	}
	evs := it.reviewEvents(post)
	if len(evs) != 1 || evs[0].State != "blocked" || evs[0].DecidedBy != "" || evs[0].RightsOwner != "Sun Pictures" || evs[0].Reason != "owner_registered" {
		t.Fatalf("events = %+v, want one system block", evs)
	}
	if n, eff, _ := it.state(post); n != 1 || eff != "restricted" {
		t.Fatalf("blocked post: count=%d effective=%s", n, eff)
	}
	// Never in the reviewer queue; listed as blocked.
	pending, _, _ := it.s.ListTitleReviews(it.ctx, "pending", "", 200)
	for _, q := range pending {
		if q.PostID == post {
			t.Fatal("an automatic block is waiting for a reviewer")
		}
	}
	// A human block is still impossible without a reviewer: the CHECK.
	if _, err := it.pool.Exec(it.ctx, `UPDATE copyright_title_reviews SET reason = NULL WHERE id = $1`, review); err == nil {
		t.Fatal("a blocked review without a reviewer or owner_registered was accepted")
	}
	// An entry cannot block automatically without naming the owner.
	if _, err := it.pool.Exec(it.ctx, `INSERT INTO copyright_review_titles (id, title, auto_block, created_by, updated_by) VALUES ($1,'X Film',TRUE,$2,$2)`,
		uuid.New(), uuid.New()); err == nil {
		t.Fatal("an owner-less auto_block entry was accepted")
	}
}

func TestOwnerBlockIT_DurationRuleBlocks(t *testing.T) {
	it := newRestrictionIT(t)
	titleID := it.ownerTitle("Jailer 2")
	post, review := it.ownerHeldPost(titleID, true)
	if got := len(it.reviewEvents(post)); got != 0 {
		t.Fatal("an unmeasured hold told the author")
	}
	out, err := it.s.ResolveTitleReviewDuration(it.ctx, post, 3600, true, true, "", nil)
	if err != nil || out != store.TitleReviewDurationBlocked {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if r, _ := it.s.GetTitleReview(it.ctx, review); r.State != "blocked" || r.AwaitingDuration {
		t.Fatalf("review = %+v", r)
	}
	// With auto-block switched off the same shape queues instead.
	post2, _ := it.ownerHeldPost(titleID, true)
	if out, err := it.s.ResolveTitleReviewDuration(it.ctx, post2, 3600, true, false, "", nil); err != nil || out != store.TitleReviewDurationQueued {
		t.Fatalf("kill switch: out=%q err=%v", out, err)
	}
}

func TestOwnerBlockIT_OwnerMatches(t *testing.T) {
	it := newRestrictionIT(t)
	titleID := it.ownerTitle("Jailer 2")
	owner := "Sun Pictures"
	in := func(post uuid.UUID, p store.OwnerMatchPolicy) store.OwnerMatchInput {
		return store.OwnerMatchInput{PostID: post, TitleID: titleID, MatchedTitle: "Jailer 2", RightsOwner: &owner, Reference: "fp-1", Policy: p}
	}

	blocked := it.seed("public", "approved", "long_video")
	if out, err := it.s.ApplyOwnerMatch(it.ctx, in(blocked, store.OwnerPolicyBlock)); err != nil || out.Outcome != store.OwnerMatchBlocked {
		t.Fatalf("block: %+v %v", out, err)
	}
	if n, eff, _ := it.state(blocked); n != 1 || eff != "restricted" {
		t.Fatalf("blocked: count=%d effective=%s", n, eff)
	}
	if out, _ := it.s.ApplyOwnerMatch(it.ctx, in(blocked, store.OwnerPolicyBlock)); out.Outcome != store.OwnerMatchAlreadyBlocked {
		t.Fatalf("repeat block: %+v", out)
	}

	muted := it.seed("public", "approved", "long_video")
	if out, err := it.s.ApplyOwnerMatch(it.ctx, in(muted, store.OwnerPolicyMute)); err != nil || out.Outcome != store.OwnerMatchHeld {
		t.Fatalf("mute: %+v %v", out, err)
	}
	if evs := it.reviewEvents(muted); len(evs) != 1 || evs[0].State != "pending" || evs[0].Reason != "mute_requested" {
		t.Fatalf("mute events = %+v", evs)
	}

	tracked := it.seed("public", "approved", "long_video")
	if out, err := it.s.ApplyOwnerMatch(it.ctx, in(tracked, store.OwnerPolicyTrack)); err != nil || out.Outcome != store.OwnerMatchTracked {
		t.Fatalf("track: %+v %v", out, err)
	}
	if n, eff, _ := it.state(tracked); n != 0 || eff != "approved" {
		t.Fatalf("a track hid the post: count=%d effective=%s", n, eff)
	}
	// Every later eligibility event keeps the exclusion.
	if _, err := it.s.WithSearchEligibilityTx(it.ctx, tracked, func(_ context.Context, _ pgx.Tx) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := it.pool.QueryRow(it.ctx, `SELECT payload FROM post_outbox_events WHERE aggregate_id=$1 AND event_type=$2 ORDER BY created_at DESC, ctid DESC LIMIT 1`,
		tracked, events.PostSearchEligibilityChanged).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var el events.PostSearchEligibilityChangedPayload
	_ = json.Unmarshal(raw, &el)
	if !el.EarningsExcluded || !events.SearchEligible(el.Visibility, el.ReviewStatus, el.Deleted) {
		t.Fatalf("later eligibility = %+v, want visible and earnings-excluded", el)
	}
	if got := len(it.reviewEvents(tracked)); got != 0 {
		t.Fatal("a track notified the author")
	}
}
