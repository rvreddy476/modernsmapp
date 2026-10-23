//go:build integration

// Copyright title review on the real schema (migration 073), against a
// *_test database (M7_POSTGRES_DSN=…/post_it_test; openRestrictionDB
// refuses any other name):
//
//	M7_POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/store/postgres/ -run TitleReviewIT -v
//
// The hold is published with the post and hides it from every store-level
// viewer read; the clear releases only its own row and writes the withheld
// PostCreated exactly once; a block keeps it hidden; the duration rule
// settles a hold that waited for its length; a held post cannot be gated;
// trust-safety's restriction command cannot touch the source.

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

func (it *restrictionIT) title(name string) uuid.UUID {
	it.t.Helper()
	tt, err := it.s.CreateCopyrightTitle(it.ctx, uuid.New(), store.CopyrightTitleWrite{Title: name, Active: true}, uuid.New())
	if err != nil {
		it.t.Fatal(err)
	}
	return tt.ID
}

// heldPost creates a long video through CreatePostWithEventIdempotent with a
// hold and no PostCreated, as the service does for a held publication.
func (it *restrictionIT) heldPost(titleID uuid.UUID, awaiting bool) (uuid.UUID, uuid.UUID) {
	it.t.Helper()
	id := uuid.New()
	hold := &store.TitleReviewHold{ReviewID: uuid.New(), TitleID: titleID, MatchedTitle: "Jailer 2", MatchedTerm: "Jailer 2",
		MatchedField: "title", AnnounceOnClear: true}
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

func (it *restrictionIT) outboxTypes(postID uuid.UUID) []string {
	it.t.Helper()
	rows, err := it.pool.Query(it.ctx, `SELECT event_type FROM post_outbox_events WHERE aggregate_id=$1 ORDER BY id`, postID)
	if err != nil {
		it.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			it.t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func count(types []string, want string) int {
	n := 0
	for _, t := range types {
		if t == want {
			n++
		}
	}
	return n
}

func TestTitleReviewIT_HoldHidesFromViewersButNotTheAuthor(t *testing.T) {
	it := newRestrictionIT(t)
	post, _ := it.heldPost(it.title("Jailer 2"), false)

	if n, eff, _ := it.state(post); n != 1 || eff != "restricted" {
		t.Fatalf("held post: count=%d effective=%s", n, eff)
	}
	types := it.outboxTypes(post)
	if count(types, events.PostCreated) != 0 {
		t.Fatalf("a held post wrote PostCreated: %v", types)
	}
	if count(types, events.PostSearchEligibilityChanged) != 1 || count(types, events.EventPostCopyrightReview) != 1 {
		t.Fatalf("outbox = %v, want one eligibility change and one review event", types)
	}
	var payload []byte
	if err := it.pool.QueryRow(it.ctx, `SELECT payload FROM post_outbox_events WHERE aggregate_id=$1 AND event_type=$2`, post, events.PostSearchEligibilityChanged).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var el events.PostSearchEligibilityChangedPayload
	_ = json.Unmarshal(payload, &el)
	if el.ReviewStatus != "restricted" || !el.Restricted || events.SearchEligible(el.Visibility, el.ReviewStatus, el.Deleted) {
		t.Fatalf("eligibility event %+v makes the held post searchable", el)
	}
	recent, _, _ := it.s.GetRecentPosts(it.ctx, nil, nil, "", 50, "")
	for _, p := range recent {
		if p.ID == post {
			t.Fatal("GetRecentPosts returned the held post")
		}
	}
	viewer, _, _ := it.s.GetPostsByAuthor(it.ctx, it.author, "", 50, "", false)
	if len(viewer) != 0 {
		t.Fatal("the viewer's by-author read returned the held post")
	}
	owner, _, _ := it.s.GetPostsByAuthor(it.ctx, it.author, "", 50, "", true)
	if len(owner) != 1 || owner[0].EffectiveReviewStatus() != "restricted" {
		t.Fatalf("the owner's read: %+v", owner)
	}
	// Only post-service's decision path may lift the source: trust-safety's
	// signed command refuses it.
	cmd := it.cmd(post, uuid.New(), store.RestrictionActionReleaseHold, "active", 2)
	cmd.Source = store.RestrictionSourceTitleReview
	if _, err := it.s.ApplyPostRestriction(it.ctx, cmd); !errors.Is(err, store.ErrRestrictionInvalidInput) {
		t.Fatalf("trust-safety command on title_review: %v", err)
	}
	// The issuer CHECK: no row of this source can claim trust-safety.
	if _, err := it.pool.Exec(it.ctx, `INSERT INTO post_restrictions (restriction_id, post_id, source, case_id, issuer, state, case_revision, last_decision_id, policy_version, reason_code)
		VALUES ($1,$2,'title_review',$3,'trust-safety-service','active',1,$4,'x','x')`, uuid.New(), post, uuid.New(), uuid.New()); err == nil {
		t.Fatal("a title_review row was accepted from trust-safety")
	}
}

func TestTitleReviewIT_ClearAnnouncesOnceBlockStaysHidden(t *testing.T) {
	it := newRestrictionIT(t)
	titleID := it.title("Jailer 2")
	post, review := it.heldPost(titleID, false)
	before := it.snapshot(post)
	clearAt := time.Now().UTC()
	announce := func(rev int64) (string, interface{}, error) {
		return events.PostCreated, events.PostCreatedPayload{PostID: post.String(), AuthorID: it.author.String(), SearchRev: rev, EligibleFrom: &clearAt}, nil
	}
	actor := uuid.New()
	out, err := it.s.DecideTitleReview(it.ctx, store.DecideTitleReviewInput{ReviewID: review, Decision: "clear", ActorID: actor, Note: "licensed"}, announce)
	if err != nil || !out.Announced || out.Review.State != "cleared" || out.Review.DecidedBy == nil || *out.Review.DecidedBy != actor {
		t.Fatalf("clear: %+v %v", out, err)
	}
	if n, eff, _ := it.state(post); n != 0 || eff != "approved" {
		t.Fatalf("cleared: count=%d effective=%s", n, eff)
	}
	if !sameSnapshot(before, it.snapshot(post)) {
		t.Fatal("the clear moved a base column")
	}
	if _, err := it.s.DecideTitleReview(it.ctx, store.DecideTitleReviewInput{ReviewID: review, Decision: "clear", ActorID: actor}, announce); !errors.Is(err, store.ErrTitleReviewDecided) {
		t.Fatalf("second decision: %v", err)
	}
	if got := count(it.outboxTypes(post), events.PostCreated); got != 1 {
		t.Fatalf("PostCreated written %d times, want once", got)
	}
	var audits int
	_ = it.pool.QueryRow(it.ctx, `SELECT COUNT(*) FROM post_admin_audit WHERE target_id=$1 AND action='copyright_review.decide'`, review).Scan(&audits)
	if audits != 1 {
		t.Fatalf("audit rows = %d", audits)
	}

	blocked, blockReview := it.heldPost(titleID, false)
	if _, err := it.s.DecideTitleReview(it.ctx, store.DecideTitleReviewInput{ReviewID: blockReview, Decision: "block", ActorID: actor, Note: "pirated"}, announce); err != nil {
		t.Fatal(err)
	}
	if n, eff, _ := it.state(blocked); n != 1 || eff != "restricted" {
		t.Fatalf("blocked: count=%d effective=%s", n, eff)
	}
	types := it.outboxTypes(blocked)
	if count(types, events.PostCreated) != 0 || count(types, events.EventPostCopyrightReview) != 2 {
		t.Fatalf("blocked outbox = %v", types)
	}
	if st := it.rowState(blocked, blockReview, store.RestrictionSourceTitleReview); st != "active" {
		t.Fatalf("blocked restriction state %s", st)
	}
}

func TestTitleReviewIT_DurationRule(t *testing.T) {
	it := newRestrictionIT(t)
	titleID := it.title("Jailer 2")
	announced := 0
	announce := func(rev int64) (string, interface{}, error) {
		announced++
		return events.PostCreated, events.PostCreatedPayload{SearchRev: rev}, nil
	}
	short, _ := it.heldPost(titleID, true)
	if got := count(it.outboxTypes(short), events.EventPostCopyrightReview); got != 0 {
		t.Fatal("an unmeasured hold told the author")
	}
	queue, _, _ := it.s.ListTitleReviews(it.ctx, "pending", "", 50)
	for _, q := range queue {
		if q.PostID == short {
			t.Fatal("an unmeasured hold is in the reviewer queue")
		}
	}
	out, err := it.s.ResolveTitleReviewDuration(it.ctx, short, 600, false, "", announce)
	if err != nil || out != store.TitleReviewDurationCleared || announced != 1 {
		t.Fatalf("short: %q announced=%d %v", out, announced, err)
	}
	if n, eff, _ := it.state(short); n != 0 || eff != "approved" {
		t.Fatalf("short after: count=%d effective=%s", n, eff)
	}
	long, _ := it.heldPost(titleID, true)
	if out, err := it.s.ResolveTitleReviewDuration(it.ctx, long, 3600, true, "", announce); err != nil || out != store.TitleReviewDurationQueued {
		t.Fatalf("long: %q %v", out, err)
	}
	if got := count(it.outboxTypes(long), events.EventPostCopyrightReview); got != 1 {
		t.Fatalf("queued hold review events = %d, want 1", got)
	}
	if out, _ := it.s.ResolveTitleReviewDuration(it.ctx, long, 3600, true, "", announce); out != store.TitleReviewDurationNone {
		t.Fatalf("a measured review was settled again: %q", out)
	}
}

func TestTitleReviewIT_HeldPostCannotBeMembersOnly(t *testing.T) {
	it := newRestrictionIT(t)
	post, _ := it.heldPost(it.title("Jailer 2"), false)
	tier := uuid.New()
	if err := it.s.SetPostMembershipGate(it.ctx, post, &tier); !errors.Is(err, store.ErrPostCopyrightHeld) {
		t.Fatalf("gate on a held post: %v", err)
	}
	if _, err := it.s.UpdatePostFields(it.ctx, post, it.author, store.PostEditPatch{TierRequiredID: &tier}); !errors.Is(err, store.ErrPostCopyrightHeld) {
		t.Fatalf("edit adding a tier: %v", err)
	}
	if err := it.s.SetPostMembershipGate(it.ctx, post, nil); err != nil {
		t.Fatalf("clearing a gate: %v", err)
	}
}

func TestTitleReviewIT_ScheduledFlipHolds(t *testing.T) {
	it := newRestrictionIT(t)
	titleID := it.title("Jailer 2")
	id := uuid.New()
	at := time.Now().Add(-time.Minute)
	p := &store.Post{ID: id, AuthorID: it.author, Text: "x", Visibility: "public", ContentType: "long_video", Title: "Jailer 2", ReviewStatus: "approved", PublishAt: &at}
	if err := it.s.CreatePostWithEventIdempotent(it.ctx, p, "", nil, nil); err != nil {
		t.Fatal(err)
	}
	d := 3600
	hold := &store.TitleReviewHold{ReviewID: uuid.New(), TitleID: titleID, MatchedTitle: "Jailer 2", MatchedTerm: "Jailer 2", MatchedField: "title", DurationSeconds: &d, AnnounceOnClear: true}
	called := false
	res, err := it.s.PublishScheduledPostWithHold(it.ctx, id, nil, time.Now().UTC(), true, hold, func(rev int64) (string, interface{}, error) {
		called = true
		return events.PostCreated, events.PostCreatedPayload{}, nil
	})
	if err != nil || res == nil || !res.Held || called {
		t.Fatalf("flip: %+v called=%v %v", res, called, err)
	}
	if n, eff, _ := it.state(id); n != 1 || eff != "restricted" {
		t.Fatalf("flipped: count=%d effective=%s", n, eff)
	}
	if got := count(it.outboxTypes(id), events.PostCreated); got != 0 {
		t.Fatal("a held flip wrote PostCreated")
	}
}

func TestTitleReviewIT_CatalogueAuditedAndAuthorRequest(t *testing.T) {
	it := newRestrictionIT(t)
	actor := uuid.New()
	lang := "ta"
	tt, err := it.s.CreateCopyrightTitle(it.ctx, uuid.New(), store.CopyrightTitleWrite{Title: "Vikram", Aliases: []string{"Vikram 2022"}, Language: &lang, Active: true}, actor)
	if err != nil {
		t.Fatal(err)
	}
	upd, err := it.s.UpdateCopyrightTitle(it.ctx, tt.ID, store.CopyrightTitleWrite{Title: "Vikram", Active: false}, actor, "duplicate")
	if err != nil || upd.Active {
		t.Fatalf("disable: %+v %v", upd, err)
	}
	active, _ := it.s.ListActiveCopyrightTitles(it.ctx)
	for _, a := range active {
		if a.ID == tt.ID {
			t.Fatal("a disabled title is still active")
		}
	}
	var audits int
	_ = it.pool.QueryRow(it.ctx, `SELECT COUNT(*) FROM post_admin_audit WHERE target_id=$1`, tt.ID).Scan(&audits)
	if audits != 2 {
		t.Fatalf("catalogue audit rows = %d, want 2", audits)
	}
	post, _ := it.heldPost(it.title("Jailer 2"), false)
	if _, err := it.s.RequestTitleReview(it.ctx, post, uuid.New(), "mine"); !errors.Is(err, store.ErrTitleReviewNotFound) {
		t.Fatalf("a stranger's request: %v", err)
	}
	r, err := it.s.RequestTitleReview(it.ctx, post, it.author, "I own the rights")
	if err != nil || r.AuthorNote == nil || r.AuthorRequestedAt == nil {
		t.Fatalf("author request: %+v %v", r, err)
	}
}
