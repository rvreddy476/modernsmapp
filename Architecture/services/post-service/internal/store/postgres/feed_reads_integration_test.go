//go:build integration

// Feed read paths and post.visibility_changed on the real schema
// (2026-10-30 audit). CI only, on a *_test database (post_it_test):
//
//	M7_POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/store/postgres/ -run FeedReadsIT -v

package postgres_test

import (
	"encoding/json"
	"testing"
	"time"

	store "github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

func (it *restrictionIT) exec(sql string, args ...any) {
	it.t.Helper()
	if _, err := it.pool.Exec(it.ctx, sql, args...); err != nil {
		it.t.Fatal(err)
	}
}

// visibilityEvents is every post.visibility_changed written for post, oldest
// first.
func (it *restrictionIT) visibilityEvents(post uuid.UUID) []store.PostVisibilityChangedEvent {
	it.t.Helper()
	rows, err := it.pool.Query(it.ctx, `SELECT payload FROM post_outbox_events WHERE aggregate_id=$1 AND event_type=$2 ORDER BY created_at, id`,
		post, events.EventPostVisibilityChanged)
	if err != nil {
		it.t.Fatal(err)
	}
	defer rows.Close()
	var out []store.PostVisibilityChangedEvent
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			it.t.Fatal(err)
		}
		var ev store.PostVisibilityChangedEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			it.t.Fatal(err)
		}
		out = append(out, ev)
	}
	return out
}

func TestFeedReadsIT_TrendingListsOnlyViewerEligibleRows(t *testing.T) {
	it := newRestrictionIT(t)
	open := it.seed("public", "approved", "post")
	flagged := it.seed("public", "flagged", "post")
	rejected := it.seed("public", "rejected", "post")
	pending := it.seed("public", "pending", "post")
	followers := it.seed("followers", "approved", "post")
	held := it.seed("public", "approved", "post")
	it.apply(it.cmd(held, uuid.New(), "place_hold", "absent", 1))
	scheduled := it.seed("public", "approved", "post")
	it.exec(`UPDATE posts SET publish_at = NOW() + interval '1 day' WHERE id=$1`, scheduled)
	deleted := it.seed("public", "approved", "post")
	it.exec(`UPDATE posts SET deleted_at = NOW() WHERE id=$1`, deleted)

	listed := map[uuid.UUID]bool{}
	cursor := ""
	for page := 0; page < 500; page++ {
		posts, next, err := it.s.GetTrendingPosts(it.ctx, []string{"post"}, 50, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range posts {
			listed[p.ID] = true
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if !listed[open] {
		t.Fatal("trending lost a public approved post")
	}
	for name, id := range map[string]uuid.UUID{"flagged": flagged, "rejected": rejected, "pending": pending,
		"followers-only": followers, "copyright-held": held, "scheduled": scheduled, "deleted": deleted} {
		if listed[id] {
			t.Errorf("trending listed a %s post", name)
		}
	}
}

func TestFeedReadsIT_BookmarksDropOthersIneligibleRowsAndKeepOwn(t *testing.T) {
	it := newRestrictionIT(t)
	saver := uuid.New()
	open := it.seed("public", "approved", "post")
	flagged := it.seed("public", "flagged", "post")
	held := it.seed("public", "approved", "post")
	it.apply(it.cmd(held, uuid.New(), "place_hold", "absent", 1))
	scheduled := it.seed("public", "approved", "post")
	it.exec(`UPDATE posts SET publish_at = NOW() + interval '1 day' WHERE id=$1`, scheduled)
	own := uuid.New()
	it.exec(`INSERT INTO posts (id,author_id,text,visibility,content_type,review_status,search_rev,created_at,updated_at,published_at)
		VALUES ($1,$2,'own pending','public','post','pending',1,NOW(),NOW(),NOW())`, own, saver)
	for _, id := range []uuid.UUID{open, flagged, held, scheduled, own} {
		it.exec(`INSERT INTO saved_items (user_id, target_type, target_id) VALUES ($1,'post',$2)`, saver, id)
	}
	posts, _, err := it.s.GetBookmarks(it.ctx, saver, nil, 50, "", store.SavedScope{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[uuid.UUID]bool{}
	for _, p := range posts {
		got[p.ID] = true
	}
	if !got[open] || !got[own] {
		t.Fatalf("saved lost a visible post or the saver's own: %v", got)
	}
	for name, id := range map[string]uuid.UUID{"flagged": flagged, "held": held, "scheduled": scheduled} {
		if got[id] {
			t.Errorf("saved listed someone else's %s post", name)
		}
	}
}

func TestFeedReadsIT_AccessStateCarriesTheMembersGate(t *testing.T) {
	it := newRestrictionIT(t)
	post := it.seed("public", "approved", "post")
	state, err := it.s.GetPostAccessState(it.ctx, post)
	if err != nil || state == nil || state.TierRequiredID != nil {
		t.Fatalf("open post state: %+v %v", state, err)
	}
	tier := uuid.New()
	it.exec(`UPDATE posts SET tier_required_id=$2 WHERE id=$1`, post, tier)
	state, err = it.s.GetPostAccessState(it.ctx, post)
	if err != nil || state == nil || state.TierRequiredID == nil || *state.TierRequiredID != tier {
		t.Fatalf("gated post state: %+v %v", state, err)
	}
}

func TestFeedReadsIT_BatchedPollsMatchTheSingleRead(t *testing.T) {
	it := newRestrictionIT(t)
	voter := uuid.New()
	a := it.seed("public", "approved", "poll")
	b := it.seed("public", "approved", "poll")
	plain := it.seed("public", "approved", "post")
	for _, p := range []uuid.UUID{a, b} {
		it.exec(`INSERT INTO polls (post_id, question, allows_multiple, ends_at) VALUES ($1,'which?',false,NOW() - interval '1 minute')`, p)
	}
	optA1, optA2, optB1 := uuid.New(), uuid.New(), uuid.New()
	it.exec(`INSERT INTO poll_options (id, post_id, label, sort_order) VALUES ($1,$4,'one',0),($2,$4,'two',1),($3,$5,'only',0)`,
		optA1, optA2, optB1, a, b)
	it.exec(`INSERT INTO poll_votes (post_id, option_id, user_id) VALUES ($1,$2,$4),($1,$3,$5),($1,$3,$6)`,
		a, optA1, optA2, voter, uuid.New(), uuid.New())

	batch, err := it.s.GetPollsByPostIDs(it.ctx, []uuid.UUID{a, b, plain})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := batch[plain]; ok || len(batch) != 2 {
		t.Fatalf("batch returned %d polls (a post without one included: %v)", len(batch), ok)
	}
	for _, id := range []uuid.UUID{a, b} {
		single, err := it.s.GetPoll(it.ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		got := batch[id]
		if got.Question != single.Question || got.TotalVotes != single.TotalVotes || got.HasEnded != single.HasEnded ||
			len(got.Options) != len(single.Options) {
			t.Fatalf("poll %s: batch %+v, single %+v", id, got, single)
		}
		for i := range got.Options {
			if got.Options[i].ID != single.Options[i].ID || got.Options[i].VoteCount != single.Options[i].VoteCount ||
				got.Options[i].Percentage != single.Options[i].Percentage {
				t.Fatalf("poll %s option %d: batch %+v, single %+v", id, i, got.Options[i], single.Options[i])
			}
		}
	}
	votes, err := it.s.GetUserPollVotesBatch(it.ctx, []uuid.UUID{a, b}, voter)
	if err != nil {
		t.Fatal(err)
	}
	if len(votes[a]) != 1 || votes[a][0] != optA1 || len(votes[b]) != 0 {
		t.Fatalf("viewer votes %v", votes)
	}
}

// ── post.visibility_changed ───────────────────────────────────────────────

func TestFeedReadsIT_TakedownEmitsAndAHiddenPostDoesNot(t *testing.T) {
	it := newRestrictionIT(t)
	visible := it.seed("public", "approved", "post")
	moderate := func(post uuid.UUID, action string) {
		t.Helper()
		if _, err := it.s.ModeratePost(it.ctx, store.ModeratePostInput{
			DecisionID: uuid.New(), PostID: post, ActorID: uuid.New(), Action: action, Reason: "policy", Source: "admin",
		}); err != nil {
			t.Fatal(err)
		}
	}
	moderate(visible, "reject")
	evs := it.visibilityEvents(visible)
	if len(evs) != 1 || evs[0].Reason != store.VisibilityReasonModeration || evs[0].PostID != visible.String() ||
		evs[0].AuthorID != it.author.String() || evs[0].OldVisibility != "public" || evs[0].NewVisibility != "public" {
		t.Fatalf("takedown events: %+v", evs)
	}
	moderate(visible, "approve") // a reinstatement widens
	if n := len(it.visibilityEvents(visible)); n != 1 {
		t.Fatalf("a reinstatement emitted a narrowing (%d events)", n)
	}

	flagged := it.seed("public", "flagged", "post")
	moderate(flagged, "reject")
	if n := len(it.visibilityEvents(flagged)); n != 0 {
		t.Fatalf("rejecting an already hidden post emitted %d events", n)
	}
}

func TestFeedReadsIT_HoldEmitsCopyrightAndReleaseDoesNot(t *testing.T) {
	it := newRestrictionIT(t)
	post := it.seed("public", "approved", "long_video")
	c := uuid.New()
	it.apply(it.cmd(post, c, "place_hold", "absent", 1))
	evs := it.visibilityEvents(post)
	if len(evs) != 1 || evs[0].Reason != store.VisibilityReasonCopyright || evs[0].ContentType != "long_video" {
		t.Fatalf("hold events: %+v", evs)
	}
	it.apply(it.cmd(post, c, "release_hold", "active", 2))
	if n := len(it.visibilityEvents(post)); n != 1 {
		t.Fatalf("a release emitted a narrowing (%d events)", n)
	}

	scheduled := it.seed("public", "approved", "post")
	it.exec(`UPDATE posts SET publish_at = NOW() + interval '1 day' WHERE id=$1`, scheduled)
	it.apply(it.cmd(scheduled, uuid.New(), "place_hold", "absent", 1))
	if n := len(it.visibilityEvents(scheduled)); n != 0 {
		t.Fatalf("a hold on a scheduled post emitted %d events", n)
	}
}

func TestFeedReadsIT_MembersGateEmitsOnSetAndChangeNotOnClear(t *testing.T) {
	it := newRestrictionIT(t)
	post := it.seed("public", "approved", "long_video")
	t1, t2 := uuid.New(), uuid.New()
	if err := it.s.SetPostMembershipGate(it.ctx, post, &t1); err != nil {
		t.Fatal(err)
	}
	if err := it.s.SetPostMembershipGate(it.ctx, post, &t2); err != nil {
		t.Fatal(err)
	}
	if err := it.s.SetPostMembershipGate(it.ctx, post, nil); err != nil {
		t.Fatal(err)
	}
	evs := it.visibilityEvents(post)
	if len(evs) != 2 {
		t.Fatalf("set, change, clear emitted %d events, want 2: %+v", len(evs), evs)
	}
	for _, ev := range evs {
		if ev.Reason != store.VisibilityReasonMembershipGate {
			t.Fatalf("gate event reason %q", ev.Reason)
		}
	}
}

func TestFeedReadsIT_OwnerEditsCarryTheirReason(t *testing.T) {
	it := newRestrictionIT(t)
	post := it.seed("public", "approved", "long_video")
	followers := "followers"
	if _, err := it.s.UpdatePostFields(it.ctx, post, it.author, store.PostEditPatch{Visibility: &followers}); err != nil {
		t.Fatal(err)
	}
	evs := it.visibilityEvents(post)
	if len(evs) != 1 || evs[0].Reason != store.VisibilityReasonAudience || evs[0].OldVisibility != "public" || evs[0].NewVisibility != "followers" {
		t.Fatalf("audience edit events: %+v", evs)
	}
	tier := uuid.New()
	if _, err := it.s.UpdatePostFields(it.ctx, post, it.author, store.PostEditPatch{TierRequiredID: &tier}); err != nil {
		t.Fatal(err)
	}
	evs = it.visibilityEvents(post)
	if len(evs) != 2 || evs[1].Reason != store.VisibilityReasonMembershipGate || evs[1].NewVisibility != "followers" {
		t.Fatalf("gate edit events: %+v", evs)
	}
	if !evs[1].ChangedAt.After(time.Time{}) {
		t.Fatal("changed_at missing")
	}
}
