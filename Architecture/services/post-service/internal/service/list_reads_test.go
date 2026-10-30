package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/post-service/internal/store/scylla"
	"github.com/google/uuid"
)

// 2026-10-30 audit B2 (trending), B3 (saved), B6 (Scylla resilience),
// B7 / B8 / B9 (batched counts, relationships, polls).

func listPost(author uuid.UUID, visibility, review string) postgres.Post {
	return postgres.Post{ID: uuid.New(), AuthorID: author, Visibility: visibility, ReviewStatus: review, ContentType: "post"}
}

func idsOf(details []PostDetail) map[uuid.UUID]bool {
	out := map[uuid.UUID]bool{}
	for _, d := range details {
		out[d.Post.ID] = true
	}
	return out
}

// listFixture: a page with one post per case the viewer gate decides.
type listFixture struct {
	viewer                                        uuid.UUID
	open, private, blocking, hidden               postgres.Post
	followersNo, followersYes, flagged, held, own postgres.Post
	page                                          []postgres.Post
	store                                         *fakeListReads
	svc                                           *Service
}

func newListFixture(t *testing.T) *listFixture {
	t.Helper()
	g, srv := newAccountGraph(t)
	f := &listFixture{viewer: uuid.New(), store: newFakeListReads()}
	f.open = listPost(uuid.New(), "public", "approved")
	f.private = listPost(uuid.New(), "public", "approved")  // private account, viewer not approved
	f.blocking = listPost(uuid.New(), "public", "approved") // a block either way
	f.hidden = listPost(uuid.New(), "public", "approved")   // deactivated author
	f.followersNo = listPost(uuid.New(), "followers", "approved")
	f.followersYes = listPost(uuid.New(), "followers", "approved")
	f.flagged = listPost(uuid.New(), "public", "flagged")
	f.held = listPost(uuid.New(), "public", "approved")
	f.held.ActiveRestrictionCount = 1
	f.own = listPost(f.viewer, "followers", "pending")
	f.page = []postgres.Post{f.open, f.private, f.blocking, f.hidden, f.followersNo, f.followersYes, f.flagged, f.held, f.own}

	g.denyView(&f.viewer, f.private.AuthorID)
	g.denyView(nil, f.private.AuthorID)
	g.denyView(&f.viewer, f.blocking.AuthorID)
	g.relate(f.viewer, f.blocking.AuthorID, map[string]bool{"follows": true, "blocked": true})
	g.relate(f.viewer, f.followersYes.AuthorID, map[string]bool{"follows": true})

	f.svc = &Service{
		graphServiceURL: srv.URL, httpClient: srv.Client(), listReadSeam: f.store,
		counterReadSeam: &fakeCounters{}, engagementRowSeam: &fakeEngagementRows{}, pollSeam: &fakePolls{},
	}
	f.svc.hiddenAuthors = fakeHidden{f.hidden.AuthorID: true}
	return f
}

func TestPostsVisibleToViewer_AppliesTheDirectReadsRules(t *testing.T) {
	f := newListFixture(t)
	got := f.svc.postsVisibleToViewer(context.Background(), &f.viewer, f.page)
	ids := map[uuid.UUID]bool{}
	for _, p := range got {
		ids[p.ID] = true
	}
	want := map[string]struct {
		p    postgres.Post
		seen bool
	}{
		"public post":                  {f.open, true},
		"private account":              {f.private, false},
		"blocked author":               {f.blocking, false},
		"deactivated author":           {f.hidden, false},
		"followers post, not follower": {f.followersNo, false},
		"followers post, follower":     {f.followersYes, true},
		"flagged post":                 {f.flagged, false},
		"copyright-held post":          {f.held, false},
		"viewer's own pending post":    {f.own, true},
	}
	for name, w := range want {
		if ids[w.p.ID] != w.seen {
			t.Errorf("%s: listed=%v, want %v", name, ids[w.p.ID], w.seen)
		}
	}
	if len(got) > 1 && got[0].ID != f.open.ID {
		t.Error("the page order changed")
	}

	anon := f.svc.postsVisibleToViewer(context.Background(), nil, f.page)
	for _, p := range anon {
		if p.ID == f.private.ID || p.ID == f.followersYes.ID || p.ID == f.own.ID || p.ID == f.hidden.ID {
			t.Errorf("an anonymous viewer was listed %s's post", p.AuthorID)
		}
	}
}

func TestGetTrendingPosts_GatesEveryRow(t *testing.T) {
	f := newListFixture(t)
	f.store.trending = f.page
	details, next, err := f.svc.GetTrendingPosts(context.Background(), nil, 20, "", &f.viewer)
	if err != nil || next != "next" {
		t.Fatalf("trending: %v %q", err, next)
	}
	ids := idsOf(details)
	for _, hidden := range []postgres.Post{f.private, f.blocking, f.hidden, f.followersNo, f.flagged, f.held} {
		if ids[hidden.ID] {
			t.Errorf("trending listed a post the viewer may not see (author %s, %s/%s)", hidden.AuthorID, hidden.Visibility, hidden.ReviewStatus)
		}
	}
	if !ids[f.open.ID] {
		t.Error("trending lost a public post")
	}
	for _, d := range details {
		if d.Counts == nil {
			t.Fatal("a trending row has no counts")
		}
	}

	anon, _, err := f.svc.GetTrendingPosts(context.Background(), nil, 20, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if idsOf(anon)[f.private.ID] {
		t.Error("an anonymous viewer saw a private account's post on trending")
	}
}

func TestGetBookmarks_GatesEveryRow(t *testing.T) {
	f := newListFixture(t)
	f.store.bookmarks = f.page
	details, _, err := f.svc.GetBookmarks(context.Background(), f.viewer, nil, 20, "", postgres.SavedScope{})
	if err != nil {
		t.Fatal(err)
	}
	ids := idsOf(details)
	for _, hidden := range []postgres.Post{f.private, f.blocking, f.hidden, f.followersNo, f.flagged, f.held} {
		if ids[hidden.ID] {
			t.Errorf("saved listed a post the saver may no longer see (author %s, %s/%s)", hidden.AuthorID, hidden.Visibility, hidden.ReviewStatus)
		}
	}
	for _, kept := range []postgres.Post{f.open, f.followersYes, f.own} {
		if !ids[kept.ID] {
			t.Errorf("saved lost a post the saver may see (%s/%s)", kept.Visibility, kept.ReviewStatus)
		}
	}
	for _, d := range details {
		if !d.IsBookmarked {
			t.Fatal("a saved row is not marked bookmarked")
		}
	}
}

// ── Scylla resilience ─────────────────────────────────────────────────────

type fakeCounters struct {
	err      error
	counts   map[uuid.UUID]*scylla.Counts
	reaction map[uuid.UUID]string
	batches  int
}

func (f *fakeCounters) GetCounts(_ context.Context, id uuid.UUID) (*scylla.Counts, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.counts[id], nil
}
func (f *fakeCounters) GetReaction(_ context.Context, id, _ uuid.UUID) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.reaction[id], nil
}
func (f *fakeCounters) BatchGetCounts(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]*scylla.Counts, error) {
	f.batches++
	if f.err != nil {
		return nil, f.err
	}
	out := map[uuid.UUID]*scylla.Counts{}
	for _, id := range ids {
		if c := f.counts[id]; c != nil {
			cp := *c
			out[id] = &cp
		}
	}
	return out, nil
}
func (f *fakeCounters) BatchGetReactions(_ context.Context, ids []uuid.UUID, _ uuid.UUID) (map[uuid.UUID]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := map[uuid.UUID]string{}
	for _, id := range ids {
		if r := f.reaction[id]; r != "" {
			out[id] = r
		}
	}
	return out, nil
}

type fakeEngagementRows struct {
	err   error
	rows  map[uuid.UUID]postgres.EngagementCountRow
	calls int
}

func (f *fakeEngagementRows) GetEngagementCountRows(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]postgres.EngagementCountRow, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.rows, nil
}

func TestPageCounts_BatchedAndDegradesOnScyllaError(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	counters := &fakeCounters{counts: map[uuid.UUID]*scylla.Counts{a: {Likes: 7}}}
	rows := &fakeEngagementRows{rows: map[uuid.UUID]postgres.EngagementCountRow{a: {Comments: 3, Shares: 2, Saves: 1}}}
	s := &Service{counterReadSeam: counters, engagementRowSeam: rows}

	got := s.pageCounts(context.Background(), []uuid.UUID{a, b})
	if counters.batches != 1 || rows.calls != 1 {
		t.Fatalf("a page took %d Scylla and %d PostgreSQL reads, want 1 each", counters.batches, rows.calls)
	}
	if c := got[a]; c == nil || c.Likes != 7 || c.Comments != 3 || c.Shares != 2 || c.Saves != 1 {
		t.Fatalf("counts for a = %+v", got[a])
	}
	if c := got[b]; c == nil || *c != (scylla.Counts{}) {
		t.Fatalf("a post with no counters must read zeros, got %+v", got[b])
	}

	counters.err = errors.New("scylla: no hosts available")
	got = s.pageCounts(context.Background(), []uuid.UUID{a, b})
	if c := got[a]; c == nil || c.Likes != 0 || c.Comments != 3 {
		t.Fatalf("a Scylla error must zero the likes and keep PostgreSQL's figures, got %+v", got[a])
	}
	if got[b] == nil {
		t.Fatal("a Scylla error left a row without counts")
	}

	counters.err = nil
	rows.err = errors.New("pg: timeout")
	got = s.pageCounts(context.Background(), []uuid.UUID{a})
	if c := got[a]; c == nil || c.Likes != 7 || c.Comments != 0 {
		t.Fatalf("a PostgreSQL count error must zero its figures and keep the likes, got %+v", got[a])
	}
}

func TestViewerReactionAndLikesDegradeOnScyllaError(t *testing.T) {
	post, viewer := uuid.New(), uuid.New()
	counters := &fakeCounters{reaction: map[uuid.UUID]string{post: "heart"}, counts: map[uuid.UUID]*scylla.Counts{post: {Likes: 4}}}
	s := &Service{counterReadSeam: counters}
	if r := s.pageReactions(context.Background(), []uuid.UUID{post}, viewer); r[post] != "heart" {
		t.Fatalf("reactions = %v", r)
	}
	counters.err = errors.New("scylla: timeout")
	if r := s.pageReactions(context.Background(), []uuid.UUID{post}, viewer); r == nil || len(r) != 0 {
		t.Fatalf("a Scylla error must show no reaction, got %v", r)
	}
	c, err := s.likeCountsForPost(context.Background(), post)
	if err != nil || c == nil || c.Likes != 0 {
		t.Fatalf("a Scylla error must read zero likes, not fail the open: %+v %v", c, err)
	}
}

// ── polls ─────────────────────────────────────────────────────────────────

type fakePolls struct {
	polls    map[uuid.UUID]*postgres.PollData
	votes    map[uuid.UUID][]uuid.UUID
	err      error
	pollHits int
	voteHits int
}

func (f *fakePolls) GetPollsByPostIDs(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]*postgres.PollData, error) {
	f.pollHits++
	if f.err != nil {
		return nil, f.err
	}
	out := map[uuid.UUID]*postgres.PollData{}
	for _, id := range ids {
		if p := f.polls[id]; p != nil {
			cp := *p
			out[id] = &cp
		}
	}
	return out, nil
}
func (f *fakePolls) GetUserPollVotesBatch(_ context.Context, ids []uuid.UUID, _ uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	f.voteHits++
	return f.votes, nil
}

func TestAttachPolls_OneReadForThePage(t *testing.T) {
	viewer, opt := uuid.New(), uuid.New()
	p1 := &postgres.Post{ID: uuid.New(), ContentType: "poll"}
	p2 := &postgres.Post{ID: uuid.New(), ContentType: "poll"}
	text := &postgres.Post{ID: uuid.New(), ContentType: "post"}
	polls := &fakePolls{
		polls: map[uuid.UUID]*postgres.PollData{p1.ID: {Question: "a?"}, p2.ID: {Question: "b?"}},
		votes: map[uuid.UUID][]uuid.UUID{p2.ID: {opt}},
	}
	s := &Service{pollSeam: polls}
	s.attachPolls(context.Background(), &viewer, []*postgres.Post{p1, text, p2})
	if polls.pollHits != 1 || polls.voteHits != 1 {
		t.Fatalf("a page of polls took %d poll and %d vote reads, want 1 each", polls.pollHits, polls.voteHits)
	}
	if p1.Poll == nil || p1.Poll.Question != "a?" || p2.Poll == nil || len(p2.Poll.ViewerVotes) != 1 || text.Poll != nil {
		t.Fatalf("polls attached wrongly: %+v %+v %+v", p1.Poll, p2.Poll, text.Poll)
	}
	polls.err = errors.New("down")
	p3 := &postgres.Post{ID: uuid.New(), ContentType: "poll"}
	s.attachPolls(context.Background(), &viewer, []*postgres.Post{p3})
	if p3.Poll != nil {
		t.Fatal("a failed poll read attached a poll")
	}
}

// ── relationship batch (B8) ───────────────────────────────────────────────

func TestFollowEdgesOfRestrictedPosts_OneBatchCallPerHundredAuthorsAndBlocksDeny(t *testing.T) {
	var mu sync.Mutex
	var calls []int
	viewer, blocker, follower := uuid.New(), uuid.New(), uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/graph/relationships/batch" {
			t.Errorf("unexpected graph call %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var req struct {
			ViewerID  string   `json:"viewer_id"`
			TargetIDs []string `json:"target_ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		calls = append(calls, len(req.TargetIDs))
		mu.Unlock()
		out := map[string]any{}
		for _, id := range req.TargetIDs {
			switch id {
			case blocker.String():
				out[id] = map[string]bool{"follows": true, "followed_by": true, "blocked_by": true}
			default:
				out[id] = map[string]bool{"follows": true}
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	s := &Service{graphServiceURL: srv.URL, httpClient: srv.Client()}

	posts := []postgres.Post{
		{AuthorID: blocker, Visibility: "followers"},
		{AuthorID: follower, Visibility: "friends"},
	}
	for i := 0; i < 148; i++ {
		posts = append(posts, postgres.Post{AuthorID: uuid.New(), Visibility: "followers"})
	}
	got := s.followEdgesOfRestrictedPosts(context.Background(), posts, &viewer)
	if len(calls) != 2 || calls[0] != 100 || calls[1] != 50 {
		t.Fatalf("150 authors took calls of %v, want [100 50]", calls)
	}
	if _, ok := got[blocker]; ok {
		t.Error("an author with a block produced edges; their restricted posts must be dropped")
	}
	if e := got[follower]; !e.follows || e.followedBy {
		t.Errorf("follower edges = %+v", e)
	}
	if !batchVisibilityAllowsEdges("followers", got[posts[5].AuthorID]) {
		t.Error("a followed author's followers post was refused")
	}
}
