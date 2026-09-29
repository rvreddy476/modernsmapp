//go:build integration

// Case-specific restrictions on the real schema (M7_POSTGRES_DSN, a *_test
// database: post_it_test). Copyright Match plan section 17: T1-1 (two
// copyright holds plus a safety row, each release changes only its row and
// no other column moves), T1-2 / T1-3 (scheduled and deleted subjects),
// T1-5 (idempotent replay, conflict, stale revision, state mismatch),
// T1-6 (the four base writers change only the base under a hold), T1-7
// (count drift repaired), T1-8 (every store-level viewer surface excludes
// a restricted post), plus the reconciliation read and the append-only
// audit.
//
//	M7_POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/store/postgres/ -run Restriction -v

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/post-service/database"
	store "github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func openRestrictionDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("M7_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("M7_POSTGRES_DSN is required")
	}
	name := dsn[strings.LastIndex(dsn, "/")+1:]
	if i := strings.Index(name, "?"); i >= 0 {
		name = name[:i]
	}
	if !strings.HasSuffix(name, "_test") {
		t.Fatalf("refusing to run against a database whose name does not end in _test (use post_it_test)")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS media_assets (
			id UUID PRIMARY KEY, uploader_id UUID NOT NULL, file_type TEXT NOT NULL, processing_status TEXT NOT NULL,
			moderation_status TEXT NOT NULL DEFAULT 'pending', duration_seconds INTEGER, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS duration_ms INT`); err != nil {
		t.Fatal(err)
	}
	if err := store.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatalf("bootstrap real schema: %v", err)
	}
	return pool
}

type restrictionIT struct {
	t      *testing.T
	pool   *pgxpool.Pool
	s      *store.Store
	ctx    context.Context
	author uuid.UUID
}

func newRestrictionIT(t *testing.T) *restrictionIT {
	pool := openRestrictionDB(t)
	return &restrictionIT{t: t, pool: pool, s: store.New(pool), ctx: context.Background(), author: uuid.New()}
}

func (it *restrictionIT) seed(visibility, review, contentType string) uuid.UUID {
	it.t.Helper()
	id := uuid.New()
	if _, err := it.pool.Exec(it.ctx, `
		INSERT INTO posts (id,author_id,text,visibility,content_type,review_status,search_rev,created_at,updated_at,published_at,distribution,age_restricted)
		VALUES ($1,$2,'restriction proof',$3,$4,$5,1,NOW() - interval '1 hour',NOW() - interval '1 hour',NOW() - interval '1 hour','{"main_feed":true}',false)
	`, id, it.author, visibility, contentType, review); err != nil {
		it.t.Fatal(err)
	}
	return id
}

func (it *restrictionIT) cmd(postID uuid.UUID, caseID uuid.UUID, action, expected string, rev int64) store.RestrictionCommand {
	reason := "removal_upheld"
	if action == store.RestrictionActionReleaseHold {
		reason = "claim_withdrawn"
	}
	return store.RestrictionCommand{
		Action: action, Source: store.RestrictionSourceCopyright, CaseID: caseID, CaseRevision: rev,
		PostID: postID, SubjectAuthorID: it.author, ExpectedState: expected, DecisionID: uuid.New(),
		PolicyVersion: "copyright-v1", ReasonCode: reason, ActorID: uuid.New(), ClaimsDigest: []byte(uuid.NewString()),
	}
}

func (it *restrictionIT) apply(in store.RestrictionCommand) *store.RestrictionOutcome {
	it.t.Helper()
	out, err := it.s.ApplyPostRestriction(it.ctx, in)
	if err != nil {
		it.t.Fatalf("%s case %s expected %s rev %d: %v", in.Action, in.CaseID, in.ExpectedState, in.CaseRevision, err)
	}
	return out
}

// snapshot is every column a restriction command must never write.
type postSnapshot struct {
	Visibility, Review          string
	Deleted, Publish, Published *time.Time
	Created, Updated            time.Time
	Distribution                []byte
	DistributionRev             int64
	AgeRestricted               bool
}

func (it *restrictionIT) snapshot(id uuid.UUID) postSnapshot {
	it.t.Helper()
	var s postSnapshot
	if err := it.pool.QueryRow(it.ctx, `SELECT visibility, review_status, deleted_at, publish_at, published_at, created_at, updated_at,
		distribution, distribution_rev, age_restricted FROM posts WHERE id=$1`, id).Scan(
		&s.Visibility, &s.Review, &s.Deleted, &s.Publish, &s.Published, &s.Created, &s.Updated, &s.Distribution, &s.DistributionRev, &s.AgeRestricted); err != nil {
		it.t.Fatal(err)
	}
	return s
}

func (it *restrictionIT) state(id uuid.UUID) (count int, effective string, rev int64) {
	it.t.Helper()
	if err := it.pool.QueryRow(it.ctx, `SELECT active_restriction_count, effective_review_status, search_rev FROM posts WHERE id=$1`, id).Scan(&count, &effective, &rev); err != nil {
		it.t.Fatal(err)
	}
	return
}

func (it *restrictionIT) rowState(postID, caseID uuid.UUID, source string) string {
	it.t.Helper()
	var state string
	if err := it.pool.QueryRow(it.ctx, `SELECT state FROM post_restrictions WHERE post_id=$1 AND case_id=$2 AND source=$3`, postID, caseID, source).Scan(&state); err != nil {
		it.t.Fatal(err)
	}
	return state
}

func sameSnapshot(a, b postSnapshot) bool {
	tp := func(x, y *time.Time) bool { return (x == nil) == (y == nil) && (x == nil || x.Equal(*y)) }
	return a.Visibility == b.Visibility && a.Review == b.Review && tp(a.Deleted, b.Deleted) && tp(a.Publish, b.Publish) &&
		tp(a.Published, b.Published) && a.Created.Equal(b.Created) && a.Updated.Equal(b.Updated) &&
		string(a.Distribution) == string(b.Distribution) && a.DistributionRev == b.DistributionRev && a.AgeRestricted == b.AgeRestricted
}

// insertSafetyRow plants the reserved safety source by SQL (the store
// refuses it in v1) with the count kept honest, as a future safety lane
// would.
func (it *restrictionIT) insertSafetyRow(postID uuid.UUID) uuid.UUID {
	it.t.Helper()
	caseID := uuid.New()
	tx, err := it.pool.Begin(it.ctx)
	if err != nil {
		it.t.Fatal(err)
	}
	defer tx.Rollback(it.ctx)
	if _, err := tx.Exec(it.ctx, `INSERT INTO post_restrictions (restriction_id, post_id, source, case_id, issuer, state, case_revision, last_decision_id, policy_version, reason_code)
		VALUES ($1,$2,'safety',$3,'trust-safety-service','active',1,$4,'safety-v0','test')`, uuid.New(), postID, caseID, uuid.New()); err != nil {
		it.t.Fatal(err)
	}
	if _, err := tx.Exec(it.ctx, `UPDATE posts SET active_restriction_count = (SELECT COUNT(*) FROM post_restrictions WHERE post_id=$1 AND state='active') WHERE id=$1`, postID); err != nil {
		it.t.Fatal(err)
	}
	if err := tx.Commit(it.ctx); err != nil {
		it.t.Fatal(err)
	}
	return caseID
}

func TestRestrictionIT_TwoCopyrightHoldsAndASafetyRowAreIndependent(t *testing.T) {
	it := newRestrictionIT(t)
	post := it.seed("public", "approved", "long_video")
	before := it.snapshot(post)
	c1, c2 := uuid.New(), uuid.New()

	it.apply(it.cmd(post, c1, "place_hold", "absent", 1))
	if n, eff, _ := it.state(post); n != 1 || eff != "restricted" {
		t.Fatalf("after C1: count=%d effective=%s", n, eff)
	}
	it.apply(it.cmd(post, c2, "place_hold", "absent", 1))
	safety := it.insertSafetyRow(post)
	if n, eff, _ := it.state(post); n != 3 || eff != "restricted" {
		t.Fatalf("after C2+safety: count=%d effective=%s", n, eff)
	}
	if !sameSnapshot(before, it.snapshot(post)) {
		t.Fatalf("placing holds moved a column:\nbefore=%+v\n after=%+v", before, it.snapshot(post))
	}

	// Release C1: only C1's row changes.
	it.apply(it.cmd(post, c1, "release_hold", "active", 2))
	if it.rowState(post, c1, "copyright") != "released" || it.rowState(post, c2, "copyright") != "active" || it.rowState(post, safety, "safety") != "active" {
		t.Fatalf("release C1 touched another row: c1=%s c2=%s safety=%s", it.rowState(post, c1, "copyright"), it.rowState(post, c2, "copyright"), it.rowState(post, safety, "safety"))
	}
	if n, eff, _ := it.state(post); n != 2 || eff != "restricted" {
		t.Fatalf("after release C1: count=%d effective=%s", n, eff)
	}
	// Release C2: the safety row still holds the post.
	it.apply(it.cmd(post, c2, "release_hold", "active", 2))
	if n, eff, _ := it.state(post); n != 1 || eff != "restricted" {
		t.Fatalf("after release C2: count=%d effective=%s (safety row must still hold)", n, eff)
	}
	if !sameSnapshot(before, it.snapshot(post)) {
		t.Fatalf("releasing moved a column:\nbefore=%+v\n after=%+v", before, it.snapshot(post))
	}
	// A base rejection under the remaining hold: effective stays
	// restricted; when the safety row goes, the base shows through.
	if _, err := it.s.ModeratePost(it.ctx, store.ModeratePostInput{DecisionID: uuid.New(), PostID: post, ActorID: uuid.New(), Action: "reject", Reason: "policy", Source: "admin"}); err != nil {
		t.Fatal(err)
	}
	if n, eff, _ := it.state(post); n != 1 || eff != "restricted" {
		t.Fatalf("rejected under hold: count=%d effective=%s", n, eff)
	}
	if _, err := it.pool.Exec(it.ctx, `UPDATE post_restrictions SET state='released', released_at=now(), updated_at=now() WHERE post_id=$1 AND source='safety'`, post); err == nil {
		t.Fatal("releasing the safety row without the recount committed: the deferred count check did not fire")
	}
	tx, _ := it.pool.Begin(it.ctx)
	_, _ = tx.Exec(it.ctx, `UPDATE post_restrictions SET state='released', released_at=now(), updated_at=now() WHERE post_id=$1 AND source='safety'`, post)
	_, _ = tx.Exec(it.ctx, `UPDATE posts SET active_restriction_count = 0 WHERE id=$1`, post)
	if err := tx.Commit(it.ctx); err != nil {
		t.Fatal(err)
	}
	if n, eff, _ := it.state(post); n != 0 || eff != "rejected" {
		t.Fatalf("all released: count=%d effective=%s, want 0/rejected", n, eff)
	}
	// Re-approve the base: approved again, no restriction in the way.
	if _, err := it.s.ModeratePost(it.ctx, store.ModeratePostInput{DecisionID: uuid.New(), PostID: post, ActorID: uuid.New(), Action: "approve", Reason: "reversed", Source: "admin"}); err != nil {
		t.Fatal(err)
	}
	if _, eff, _ := it.state(post); eff != "approved" {
		t.Fatalf("effective=%s, want approved", eff)
	}
	// A place after a release on the same case, with a higher revision.
	it.apply(it.cmd(post, c1, "place_hold", "released", 3))
	if n, eff, _ := it.state(post); n != 1 || eff != "restricted" {
		t.Fatalf("re-place C1: count=%d effective=%s", n, eff)
	}
}

func TestRestrictionIT_ScheduledAndDeletedSubjects(t *testing.T) {
	it := newRestrictionIT(t)
	// T1-2: a scheduled post. The hold is accepted, publish_at is untouched,
	// and no PostCreated is emitted by the command.
	scheduled := it.seed("public", "approved", "post")
	if _, err := it.pool.Exec(it.ctx, `UPDATE posts SET publish_at = NOW() + interval '1 day', published_at = NULL WHERE id=$1`, scheduled); err != nil {
		t.Fatal(err)
	}
	before := it.snapshot(scheduled)
	c := uuid.New()
	it.apply(it.cmd(scheduled, c, "place_hold", "absent", 1))
	it.apply(it.cmd(scheduled, c, "release_hold", "active", 2))
	if !sameSnapshot(before, it.snapshot(scheduled)) {
		t.Fatalf("scheduled post moved:\nbefore=%+v\n after=%+v", before, it.snapshot(scheduled))
	}
	var created int
	if err := it.pool.QueryRow(it.ctx, `SELECT count(*) FROM post_outbox_events WHERE aggregate_id=$1 AND event_type=$2`, scheduled, events.PostCreated).Scan(&created); err != nil {
		t.Fatal(err)
	}
	if created != 0 {
		t.Fatalf("a restriction command emitted %d PostCreated", created)
	}
	var elig int
	if err := it.pool.QueryRow(it.ctx, `SELECT count(*) FROM post_outbox_events WHERE aggregate_id=$1 AND event_type=$2`, scheduled, events.PostSearchEligibilityChanged).Scan(&elig); err != nil {
		t.Fatal(err)
	}
	if elig != 2 {
		t.Fatalf("eligibility events=%d, want 2 (place, release)", elig)
	}

	// T1-3: a deleted post. Accepted; stays deleted. An author restore
	// under the hold gives an undeleted but ineligible post.
	deleted := it.seed("public", "approved", "post")
	if _, err := it.pool.Exec(it.ctx, `UPDATE posts SET deleted_at = NOW() WHERE id=$1`, deleted); err != nil {
		t.Fatal(err)
	}
	before = it.snapshot(deleted)
	c = uuid.New()
	it.apply(it.cmd(deleted, c, "place_hold", "absent", 1))
	if !sameSnapshot(before, it.snapshot(deleted)) {
		t.Fatalf("hold on a deleted post moved a column")
	}
	if _, err := it.s.RestorePost(it.ctx, deleted, it.author, 30*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	after := it.snapshot(deleted)
	n, eff, _ := it.state(deleted)
	if after.Deleted != nil || n != 1 || eff != "restricted" || it.rowState(deleted, c, "copyright") != "active" {
		t.Fatalf("restore under hold: deleted=%v count=%d effective=%s row=%s", after.Deleted, n, eff, it.rowState(deleted, c, "copyright"))
	}
	// And a release on a (re-)deleted post never undeletes.
	if _, err := it.pool.Exec(it.ctx, `UPDATE posts SET deleted_at = NOW() WHERE id=$1`, deleted); err != nil {
		t.Fatal(err)
	}
	before = it.snapshot(deleted)
	it.apply(it.cmd(deleted, c, "release_hold", "active", 2))
	if !sameSnapshot(before, it.snapshot(deleted)) || it.snapshot(deleted).Deleted == nil {
		t.Fatalf("release undeleted or moved a column")
	}
}

func TestRestrictionIT_IdempotencyRevisionAndState(t *testing.T) {
	it := newRestrictionIT(t)
	post := it.seed("public", "approved", "post")
	c := uuid.New()
	in := it.cmd(post, c, "place_hold", "absent", 5)

	const workers = 10
	var wg sync.WaitGroup
	outs := make(chan *store.RestrictionOutcome, workers)
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := it.s.ApplyPostRestriction(it.ctx, in)
			if err != nil {
				errs <- err
				return
			}
			outs <- out
		}()
	}
	wg.Wait()
	close(outs)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent send failed: %v", err)
	}
	replayed := 0
	for out := range outs {
		if out.Replayed {
			replayed++
		}
		if out.State != "active" || out.ActiveRestrictionCount != 1 || out.EffectiveReviewStatus != "restricted" || out.CaseRevision != 5 {
			t.Fatalf("outcome %+v", out)
		}
	}
	if replayed != workers-1 {
		t.Fatalf("replayed=%d, want %d", replayed, workers-1)
	}
	var eventRows, eligEvents int
	_ = it.pool.QueryRow(it.ctx, `SELECT count(*) FROM post_restriction_events WHERE decision_id=$1`, in.DecisionID).Scan(&eventRows)
	_ = it.pool.QueryRow(it.ctx, `SELECT count(*) FROM post_outbox_events WHERE aggregate_id=$1 AND event_type=$2`, post, events.PostSearchEligibilityChanged).Scan(&eligEvents)
	if eventRows != 1 || eligEvents != 1 {
		t.Fatalf("events=%d eligibility=%d, want 1/1", eventRows, eligEvents)
	}
	if _, _, rev := it.state(post); rev != 2 {
		t.Fatalf("search_rev=%d, want 2 (one bump)", rev)
	}

	// The same id with a different digest (a changed reason) conflicts.
	conflict := in
	conflict.ReasonCode, conflict.ClaimsDigest = "reinstated_on_review", []byte("other")
	if _, err := it.s.ApplyPostRestriction(it.ctx, conflict); !errors.Is(err, store.ErrRestrictionDecisionConflict) {
		t.Fatalf("conflict: %v", err)
	}
	// A stale revision on a new decision.
	stale := it.cmd(post, c, "release_hold", "active", 5)
	if _, err := it.s.ApplyPostRestriction(it.ctx, stale); !errors.Is(err, store.ErrRestrictionStaleRevision) {
		t.Fatalf("stale: %v", err)
	}
	// The wrong expected state.
	wrong := it.cmd(post, c, "release_hold", "released", 6)
	if _, err := it.s.ApplyPostRestriction(it.ctx, wrong); !errors.Is(err, store.ErrRestrictionStateMismatch) {
		t.Fatalf("state mismatch: %v", err)
	}
	// Nothing to release on an unknown case.
	none := it.cmd(post, uuid.New(), "release_hold", "absent", 1)
	if _, err := it.s.ApplyPostRestriction(it.ctx, none); !errors.Is(err, store.ErrRestrictionStateMismatch) {
		t.Fatalf("release absent: %v", err)
	}
	// Another author's id.
	other := it.cmd(post, uuid.New(), "place_hold", "absent", 1)
	other.SubjectAuthorID = uuid.New()
	if _, err := it.s.ApplyPostRestriction(it.ctx, other); !errors.Is(err, store.ErrRestrictionSubjectMismatch) {
		t.Fatalf("author mismatch: %v", err)
	}
	// An unknown post.
	missing := it.cmd(uuid.New(), uuid.New(), "place_hold", "absent", 1)
	if _, err := it.s.ApplyPostRestriction(it.ctx, missing); !errors.Is(err, store.ErrRestrictionSubjectNotFound) {
		t.Fatalf("missing post: %v", err)
	}
	// The reserved source.
	safety := it.cmd(post, uuid.New(), "place_hold", "absent", 1)
	safety.Source = store.RestrictionSourceSafety
	if _, err := it.s.ApplyPostRestriction(it.ctx, safety); !errors.Is(err, store.ErrRestrictionSourceNotEnabled) {
		t.Fatalf("safety: %v", err)
	}
	// None of the refusals wrote anything.
	_ = it.pool.QueryRow(it.ctx, `SELECT count(*) FROM post_restriction_events WHERE post_id=$1`, post).Scan(&eventRows)
	if eventRows != 1 {
		t.Fatalf("refusals wrote events: %d", eventRows)
	}
	// A re-affirming place (expected active, higher revision) changes
	// nothing and emits nothing, but is recorded.
	again := it.cmd(post, c, "place_hold", "active", 7)
	out := it.apply(again)
	if out.Changed || out.CaseRevision != 7 {
		t.Fatalf("re-affirm: %+v", out)
	}
	_ = it.pool.QueryRow(it.ctx, `SELECT count(*) FROM post_outbox_events WHERE aggregate_id=$1 AND event_type=$2`, post, events.PostSearchEligibilityChanged).Scan(&eligEvents)
	if eligEvents != 1 {
		t.Fatalf("re-affirm emitted: %d", eligEvents)
	}
}

func TestRestrictionIT_EligibilityEventCarriesTheEffectiveStatus(t *testing.T) {
	it := newRestrictionIT(t)
	post := it.seed("public", "approved", "post")
	c := uuid.New()
	it.apply(it.cmd(post, c, "place_hold", "absent", 1))
	var raw []byte
	if err := it.pool.QueryRow(it.ctx, `SELECT payload FROM post_outbox_events WHERE aggregate_id=$1 AND event_type=$2 ORDER BY created_at DESC LIMIT 1`, post, events.PostSearchEligibilityChanged).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var p events.PostSearchEligibilityChangedPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if p.ReviewStatus != "restricted" || p.BaseReviewStatus != "approved" || !p.Restricted || p.Text != "" || p.SearchRev != 2 {
		t.Fatalf("hold payload: %+v", p)
	}
	if events.SearchEligible(p.Visibility, p.ReviewStatus, p.Deleted) {
		t.Fatal("the allowlist admitted 'restricted'")
	}
	it.apply(it.cmd(post, c, "release_hold", "active", 2))
	if err := it.pool.QueryRow(it.ctx, `SELECT payload FROM post_outbox_events WHERE aggregate_id=$1 AND event_type=$2 ORDER BY created_at DESC LIMIT 1`, post, events.PostSearchEligibilityChanged).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var released events.PostSearchEligibilityChangedPayload
	_ = json.Unmarshal(raw, &released)
	p = released
	if p.ReviewStatus != "approved" || p.Restricted || p.Text == "" || p.SearchRev != 3 {
		t.Fatalf("release payload: %+v", p)
	}
	// The reconciler's scan reads the same thing.
	rows, err := it.s.ScanEligibility(it.ctx, uuid.Nil, 1000)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rows {
		if r.PostID == post {
			found = true
			if r.Review != "approved" || r.BaseReview != "approved" || r.Restricted || !r.Eligible() {
				t.Fatalf("scan row %+v", r)
			}
		}
	}
	if !found && len(rows) == 1000 {
		t.Skip("post beyond the first scan page in a crowded test database")
	}
	if !found {
		t.Fatal("scan did not return the post")
	}
}

// T1-6 / P-2: the four base writers change only the base under a hold.
func TestRestrictionIT_BaseWritersNeverClearAHold(t *testing.T) {
	it := newRestrictionIT(t)
	hold := func(post uuid.UUID) uuid.UUID {
		c := uuid.New()
		it.apply(it.cmd(post, c, "place_hold", "absent", 1))
		return c
	}
	assertHeld := func(name string, post, c uuid.UUID, wantBase string) {
		t.Helper()
		n, eff, _ := it.state(post)
		snap := it.snapshot(post)
		if n != 1 || eff != "restricted" || it.rowState(post, c, "copyright") != "active" || snap.Review != wantBase {
			t.Fatalf("%s: count=%d effective=%s row=%s base=%s (want 1/restricted/active/%s)", name, n, eff, it.rowState(post, c, "copyright"), snap.Review, wantBase)
		}
	}

	// 1. The moderation authority (legacy moderator route and admin-token
	// route both end here): approve from flagged.
	p1 := it.seed("public", "flagged", "post")
	c1 := hold(p1)
	if _, err := it.s.ModeratePost(it.ctx, store.ModeratePostInput{DecisionID: uuid.New(), PostID: p1, ActorID: uuid.New(), Action: "approve", Reason: "fine", Source: "admin"}); err != nil {
		t.Fatal(err)
	}
	assertHeld("ModeratePost approve", p1, c1, "approved")
	// …and the appeal source with a revision check, the trust-safety
	// overturn path.
	p1b := it.seed("public", "rejected", "post")
	c1b := hold(p1b)
	_, _, rev := it.state(p1b)
	if _, err := it.s.ModeratePost(it.ctx, store.ModeratePostInput{DecisionID: uuid.New(), PostID: p1b, ActorID: uuid.New(), Action: "approve", Reason: "overturned", Source: "appeal", ExpectedRevision: rev}); err != nil {
		t.Fatal(err)
	}
	assertHeld("ModeratePost appeal overturn", p1b, c1b, "approved")

	// 2. AdminSetReviewStatus / SetReviewStatusInternal (reviewer ML).
	p2 := it.seed("public", "flagged", "post")
	c2 := hold(p2)
	changed, err := it.s.SetReviewStatusFromFlagged(it.ctx, p2, "approved", store.ReviewAuditActor{Service: "reviewer-service"})
	if err != nil || !changed {
		t.Fatalf("SetReviewStatusFromFlagged: changed=%v err=%v", changed, err)
	}
	assertHeld("SetReviewStatusFromFlagged approve", p2, c2, "approved")

	// 3. Resubmit from needs_changes, then the auto-resolve.
	p3 := it.seed("public", "needs_changes", "post")
	c3 := hold(p3)
	if changed, err := it.s.ResubmitFromNeedsChanges(it.ctx, p3); err != nil || !changed {
		t.Fatalf("resubmit: %v %v", changed, err)
	}
	assertHeld("ResubmitFromNeedsChanges", p3, c3, "flagged")
	if _, err := it.s.SetReviewStatusFromFlagged(it.ctx, p3, "approved", store.ReviewAuditActor{Service: "reviewer-service"}); err != nil {
		t.Fatal(err)
	}
	assertHeld("resubmit then approve", p3, c3, "approved")

	// 3b. The pending gate (media consumer).
	p3b := it.seed("public", "pending", "post")
	c3b := hold(p3b)
	if _, err := it.s.FlipReviewStatusFromPending(it.ctx, p3b, "approved"); err != nil {
		t.Fatal(err)
	}
	assertHeld("FlipReviewStatusFromPending", p3b, c3b, "approved")

	// 4. RestorePost.
	p4 := it.seed("public", "approved", "post")
	c4 := hold(p4)
	if _, err := it.pool.Exec(it.ctx, `UPDATE posts SET deleted_at = NOW() WHERE id=$1`, p4); err != nil {
		t.Fatal(err)
	}
	if _, err := it.s.RestorePost(it.ctx, p4, it.author, 30*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	assertHeld("RestorePost", p4, c4, "approved")
}

// T1-8, store level: every viewer surface excludes the restricted post
// while the owner-facing reads still return it with its count.
func TestRestrictionIT_ViewerSurfacesExcludeARestrictedPost(t *testing.T) {
	it := newRestrictionIT(t)
	contains := func(posts []store.Post, id uuid.UUID) bool {
		for _, p := range posts {
			if p.ID == id {
				return true
			}
		}
		return false
	}
	containsPtr := func(posts []*store.Post, id uuid.UUID) bool {
		for _, p := range posts {
			if p.ID == id {
				return true
			}
		}
		return false
	}
	video := it.seed("public", "approved", "long_video")
	reel := it.seed("public", "approved", "reel")
	live := it.seed("public", "approved", "long_video")
	if _, err := it.pool.Exec(it.ctx, `UPDATE posts SET source='live' WHERE id=$1`, live); err != nil {
		t.Fatal(err)
	}
	root := it.seed("public", "approved", "post")
	if _, err := it.pool.Exec(it.ctx, `UPDATE posts SET thread_root_id=$1, thread_seq=0 WHERE id=$1`, root); err != nil {
		t.Fatal(err)
	}
	beforeCounts, _ := it.s.CountChannelContent(it.ctx, it.author)
	beforeIDs, _ := it.s.ChannelPublicVideoIDs(it.ctx, it.author, uuid.Nil, 50)

	for _, id := range []uuid.UUID{video, reel, live, root} {
		it.apply(it.cmd(id, uuid.New(), "place_hold", "absent", 1))
	}

	recent, _, err := it.s.GetRecentPosts(it.ctx, nil, nil, "", 50, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{video, reel, live, root} {
		if contains(recent, id) {
			t.Fatalf("GetRecentPosts returned restricted %s", id)
		}
	}
	byAuthor, _, _ := it.s.GetPostsByAuthor(it.ctx, it.author, "", 50, "", false)
	if len(byAuthor) != 0 {
		t.Fatalf("GetPostsByAuthor (viewer) returned %d restricted posts", len(byAuthor))
	}
	owner, _, _ := it.s.GetPostsByAuthor(it.ctx, it.author, "", 50, "", true)
	if len(owner) != 4 {
		t.Fatalf("GetPostsByAuthor (owner) returned %d, want 4", len(owner))
	}
	for _, p := range owner {
		if p.ActiveRestrictionCount != 1 || p.EffectiveReviewStatus() != "restricted" || p.ReviewStatus != "approved" {
			t.Fatalf("owner row %s: count=%d effective=%s base=%s", p.ID, p.ActiveRestrictionCount, p.EffectiveReviewStatus(), p.ReviewStatus)
		}
	}
	reels, _, _ := it.s.GetReelCandidates(it.ctx, 50, "")
	if containsPtr(reels, reel) {
		t.Fatal("GetReelCandidates returned the restricted reel")
	}
	thread, _ := it.s.GetThreadPosts(it.ctx, root)
	if contains(thread, root) {
		t.Fatal("GetThreadPosts returned the restricted root")
	}
	recordings, _, _ := it.s.ListLiveRecordings(it.ctx, 50, "")
	if contains(recordings, live) {
		t.Fatal("ListLiveRecordings returned the restricted recording")
	}
	counts, _ := it.s.CountChannelContent(it.ctx, it.author)
	if counts.Videos != beforeCounts.Videos-2 || counts.Shorts != beforeCounts.Shorts-1 || counts.Live != beforeCounts.Live-1 {
		t.Fatalf("CountChannelContent before=%+v after=%+v", beforeCounts, counts)
	}
	ids, _ := it.s.ChannelPublicVideoIDs(it.ctx, it.author, uuid.Nil, 50)
	if len(ids) != len(beforeIDs)-2 {
		t.Fatalf("ChannelPublicVideoIDs before=%d after=%d", len(beforeIDs), len(ids))
	}
	videoCount, _ := it.s.CountChannelVideos(it.ctx, it.author)
	if videoCount != 0 {
		t.Fatalf("CountChannelVideos=%d, want 0", videoCount)
	}
	// The direct read and the batch read return the row (the service gate
	// decides), with the count that makes the gate say no.
	p, err := it.s.GetPost(it.ctx, video)
	if err != nil || p == nil || p.ActiveRestrictionCount != 1 || p.EffectiveReviewStatus() != "restricted" {
		t.Fatalf("GetPost: %+v %v", p, err)
	}
	batch, _ := it.s.GetPostsByIDs(it.ctx, []uuid.UUID{video, reel})
	for _, b := range batch {
		if b.EffectiveReviewStatus() != "restricted" {
			t.Fatalf("batch row %s effective=%s", b.ID, b.EffectiveReviewStatus())
		}
	}
	state, _ := it.s.GetPostAccessState(it.ctx, video)
	if state == nil || state.ActiveRestrictionCount != 1 || state.EffectiveReviewStatus() != "restricted" {
		t.Fatalf("access state: %+v", state)
	}
	subject, err := it.s.GetModerationSubject(it.ctx, video)
	if err != nil || subject.BaseReviewStatus != "approved" || subject.EffectiveReviewStatus != "restricted" || len(subject.ActiveRestrictions) != 1 || subject.ReviewStatus != "approved" {
		t.Fatalf("moderation subject: %+v %v", subject, err)
	}
}

func TestRestrictionIT_ReconciliationReadAndCountRepair(t *testing.T) {
	it := newRestrictionIT(t)
	posts := []uuid.UUID{it.seed("public", "approved", "post"), it.seed("public", "approved", "post"), it.seed("public", "approved", "post")}
	cases := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	start := time.Now().Add(-time.Second)
	for i, p := range posts {
		it.apply(it.cmd(p, cases[i], "place_hold", "absent", 1))
	}
	it.apply(it.cmd(posts[0], cases[0], "release_hold", "active", 2))

	byCase, _, err := it.s.ListPostRestrictions(it.ctx, store.RestrictionListFilter{Source: "copyright", CaseIDs: cases})
	if err != nil || len(byCase) != 3 {
		t.Fatalf("by case: %d %v", len(byCase), err)
	}
	for _, r := range byCase {
		want := "active"
		if r.CaseID == cases[0] {
			want = "released"
		}
		if r.State != want || r.Scope != "global" || r.PolicyVersion != "copyright-v1" {
			t.Fatalf("row %+v", r)
		}
		if want == "released" && (r.ReleasedAt == nil || r.CaseRevision != 2) {
			t.Fatalf("released row %+v", r)
		}
	}
	byPost, _, _ := it.s.ListPostRestrictions(it.ctx, store.RestrictionListFilter{Source: "copyright", PostID: &posts[1]})
	if len(byPost) != 1 || byPost[0].CaseID != cases[1] {
		t.Fatalf("by post: %+v", byPost)
	}
	// Incremental sweep: updated_after + cursor paging, one row at a time.
	var seen []uuid.UUID
	cursor := ""
	for {
		page, next, err := it.s.ListPostRestrictions(it.ctx, store.RestrictionListFilter{Source: "copyright", UpdatedAfter: &start, Cursor: cursor, Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range page {
			seen = append(seen, r.CaseID)
		}
		if next == "" {
			break
		}
		cursor = next
	}
	for _, c := range cases {
		found := false
		for _, s := range seen {
			if s == c {
				found = true
			}
		}
		if !found {
			t.Fatalf("sweep missed case %s (saw %d rows)", c, len(seen))
		}
	}
	if _, _, err := it.s.ListPostRestrictions(it.ctx, store.RestrictionListFilter{Source: "dmca"}); !errors.Is(err, store.ErrRestrictionInvalidInput) {
		t.Fatalf("bad source: %v", err)
	}
	active, _ := it.s.ActiveRestrictionsByPost(it.ctx, posts)
	if len(active[posts[0]]) != 0 || len(active[posts[1]]) != 1 || len(active[posts[2]]) != 1 {
		t.Fatalf("active by post: %v", active)
	}

	// The deferred check refuses a drifting counter outright…
	if _, err := it.pool.Exec(it.ctx, `UPDATE posts SET active_restriction_count = 5 WHERE id=$1`, posts[1]); err == nil {
		t.Fatal("a hand edit of active_restriction_count committed")
	}
	// …so drift is planted with the trigger off, and the nightly repair
	// finds and fixes it, emitting the eligibility change.
	if _, err := it.pool.Exec(it.ctx, `ALTER TABLE posts DISABLE TRIGGER trg_posts_assert_restriction_count`); err != nil {
		t.Fatal(err)
	}
	_, err = it.pool.Exec(it.ctx, `UPDATE posts SET active_restriction_count = 0 WHERE id=$1`, posts[1])
	if _, err2 := it.pool.Exec(it.ctx, `ALTER TABLE posts ENABLE TRIGGER trg_posts_assert_restriction_count`); err2 != nil {
		t.Fatal(err2)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, eff, _ := it.state(posts[1]); eff != "approved" {
		t.Fatalf("drift not planted: effective=%s", eff)
	}
	_, _, revBefore := it.state(posts[1])
	repaired, err := it.s.RepairRestrictionCounts(it.ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, id := range repaired {
		if id == posts[1] {
			found = true
		}
	}
	n, eff, revAfter := it.state(posts[1])
	if !found || n != 1 || eff != "restricted" || revAfter != revBefore+1 {
		t.Fatalf("repair: found=%v count=%d effective=%s rev %d→%d", found, n, eff, revBefore, revAfter)
	}
	if again, _ := it.s.RepairRestrictionCounts(it.ctx); len(again) != 0 {
		t.Fatalf("second repair still found drift: %v", again)
	}

	// The audit is append-only.
	if _, err := it.pool.Exec(it.ctx, `UPDATE post_restriction_events SET reason_code='edited' WHERE post_id=$1`, posts[0]); err == nil {
		t.Fatal("post_restriction_events accepted an UPDATE")
	}
	if _, err := it.pool.Exec(it.ctx, `DELETE FROM post_restriction_events WHERE post_id=$1`, posts[0]); err == nil {
		t.Fatal("post_restriction_events accepted a DELETE")
	}
	var n2 int
	_ = it.pool.QueryRow(it.ctx, `SELECT count(*) FROM post_restriction_events WHERE post_id=$1`, posts[0]).Scan(&n2)
	if n2 != 2 {
		t.Fatalf("events for post 0 = %d, want 2", n2)
	}
}
