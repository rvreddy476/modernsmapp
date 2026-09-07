package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The related-videos guard rails.
//
// suggestion-service/internal/service/block_safety.go records what happens
// when a recommendation surface filters somewhere other than at egress:
// every branch that skips the filter — a cache hit, a fallback path — ships
// the thing the filter existed to prevent. The rules below are therefore
// enforced on the one path everything funnels through, and tested there.

func TestRelatedFamily_NeverCrossesTheReelLongVideoSplit(t *testing.T) {
	cases := map[string]string{
		"long_video": "long_video",
		"video":      "long_video", // legacy spelling of the same thing
		"flick":      "flick",
		"reel":       "flick", // legacy spelling
		"post":       "",      // not a video at all
		"poll":       "",
		"voice":      "",
		"":           "",
	}
	for in, want := range cases {
		if got := relatedFamily(in); got != want {
			t.Errorf("relatedFamily(%q) = %q, want %q", in, got, want)
		}
	}
}

// relatedFixture stands post-service up as a stub and points a Service at
// it. Every candidate source in related.go goes through post-service, so
// one stub covers the whole collection path.
type relatedFixture struct {
	svc      *Service
	viewer   uuid.UUID
	seed     HydratedPost
	byAuthor []map[string]any
	recent   []map[string]any
	// calls records which post-service paths were hit, so a test can
	// assert that a needless call was NOT made.
	calls []string
}

func newRelatedFixture(t *testing.T, seedContentType, seedCategory string) *relatedFixture {
	t.Helper()
	t.Setenv("INTERNAL_SERVICE_KEY", "test-internal")

	f := &relatedFixture{viewer: uuid.New()}
	f.seed = HydratedPost{
		ID:          uuid.New(),
		AuthorID:    uuid.New(),
		ContentType: seedContentType,
		Category:    seedCategory,
	}

	// One stub stands in for every upstream the related path touches:
	// post-service for the candidates and the hydration batch,
	// graph-service for blocks and the private-account gate,
	// trust-safety for the viewer's keyword filters, identity-profile for
	// the author cards. All of them fail CLOSED in feed-service, so a
	// missing stub is an error rather than a quietly unfiltered page —
	// which is the correct behaviour, and the reason they all have to be
	// here.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == "/v1/internal/graph/blocked-and-muted":
			_ = json.NewEncoder(w).Encode(map[string]any{"user_ids": []string{}})
			return
		case r.URL.Path == "/v1/internal/graph/can":
			var body struct {
				TargetIDs []string `json:"target_ids"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			allowed := map[string]bool{}
			for _, id := range body.TargetIDs {
				allowed[id] = true
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": allowed})
			return
		case r.URL.Path == "/v1/internal/keyword-filters":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"keywords": []string{}}})
			return
		case r.URL.Path == "/v1/profiles/batch":
			_ = json.NewEncoder(w).Encode(map[string]any{})
			return
		}
		switch {
		case r.URL.Path == "/v1/posts/batch":
			// The seed lookup, and hydration. Both go through the same
			// viewer-scoped batch endpoint.
			var body struct {
				IDs []string `json:"ids"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			out := map[string]HydratedPost{}
			for _, id := range body.IDs {
				if id == f.seed.ID.String() {
					out[id] = f.seed
					continue
				}
				if p, ok := f.byID(id); ok {
					out[id] = p
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": out})
		case strings.HasPrefix(r.URL.Path, "/v1/posts/by-author/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"data": f.byAuthor})
		case r.URL.Path == "/v1/posts/recent":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": f.recent})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	f.svc = New(nil, nil, nil)
	f.svc.postServiceURL = srv.URL
	f.svc.graphURL = srv.URL
	f.svc.trustSafetyURL = srv.URL
	f.svc.profileServiceURL = srv.URL
	f.svc.mediaServiceURL = srv.URL
	return f
}

// byID finds a stub post among the collection sources, so hydration
// answers for whatever collection returned.
func (f *relatedFixture) byID(id string) (HydratedPost, bool) {
	for _, group := range [][]map[string]any{f.byAuthor, f.recent} {
		for _, p := range group {
			if p["id"] == id {
				aid, _ := uuid.Parse(p["author_id"].(string))
				pid, _ := uuid.Parse(id)
				return HydratedPost{
					ID:          pid,
					AuthorID:    aid,
					ContentType: p["content_type"].(string),
				}, true
			}
		}
	}
	return HydratedPost{}, false
}

func stubPost(author uuid.UUID, contentType string) map[string]any {
	return map[string]any{
		"id":           uuid.New().String(),
		"author_id":    author.String(),
		"created_at":   time.Now().UTC().Format(time.RFC3339Nano),
		"content_type": contentType,
	}
}

func TestRelated_RefusesANonVideoSeed(t *testing.T) {
	f := newRelatedFixture(t, "post", "")
	_, _, err := f.svc.GetRelatedVideos(context.Background(), f.viewer, f.seed.ID, 10, 0)
	if err != ErrRelatedUnsupported {
		t.Fatalf("a text post's related list returned %v, want ErrRelatedUnsupported", err)
	}
}

func TestRelated_NeverReturnsTheSeedItself(t *testing.T) {
	f := newRelatedFixture(t, "long_video", "")
	other := stubPost(f.seed.AuthorID, "long_video")
	// post-service legitimately includes the seed in the author's own
	// catalogue — it IS one of their videos. The endpoint has to drop it.
	seedRow := map[string]any{
		"id": f.seed.ID.String(), "author_id": f.seed.AuthorID.String(),
		"created_at": time.Now().UTC().Format(time.RFC3339Nano), "content_type": "long_video",
	}
	f.byAuthor = []map[string]any{seedRow, other}

	page, _, err := f.svc.GetRelatedVideos(context.Background(), f.viewer, f.seed.ID, 10, 0)
	if err != nil {
		t.Fatalf("GetRelatedVideos: %v", err)
	}
	for _, p := range page {
		if p.ID == f.seed.ID {
			t.Fatal("the video being watched was offered as the thing to play next")
		}
	}
	if len(page) != 1 {
		t.Fatalf("page had %d items, want 1 (the seed's sibling)", len(page))
	}
}

func TestRelated_NeverReturnsTheViewersOwnContent(t *testing.T) {
	f := newRelatedFixture(t, "long_video", "")
	// The viewer has uploaded long videos of their own, and post-service
	// returns them from the recent-public read because a viewer can
	// always see their own posts.
	f.recent = []map[string]any{
		stubPost(f.viewer, "long_video"),
		stubPost(uuid.New(), "long_video"),
		stubPost(f.viewer, "long_video"),
	}

	page, _, err := f.svc.GetRelatedVideos(context.Background(), f.viewer, f.seed.ID, 10, 0)
	if err != nil {
		t.Fatalf("GetRelatedVideos: %v", err)
	}
	if len(page) != 1 {
		t.Fatalf("page had %d items, want 1 — the viewer's own two videos should be gone", len(page))
	}
	for _, p := range page {
		if p.AuthorID == f.viewer {
			t.Fatal("the viewer was recommended their own video")
		}
	}
}

func TestRelated_NeverCrossesTheContentFamily(t *testing.T) {
	f := newRelatedFixture(t, "long_video", "")
	stranger := uuid.New()
	// A source that answers with the wrong family — a stale cache, a
	// post-service filter change, a legacy row. The pool re-check is what
	// stops a long video appearing in a reel list and vice versa.
	f.recent = []map[string]any{
		stubPost(stranger, "flick"),
		stubPost(stranger, "post"),
		stubPost(stranger, "long_video"),
	}

	page, _, err := f.svc.GetRelatedVideos(context.Background(), f.viewer, f.seed.ID, 10, 0)
	if err != nil {
		t.Fatalf("GetRelatedVideos: %v", err)
	}
	if len(page) != 1 {
		t.Fatalf("page had %d items, want only the one long video", len(page))
	}
	if page[0].ContentType != "long_video" {
		t.Fatalf("a %q leaked into a long-video related list", page[0].ContentType)
	}
}

func TestRelated_ReelSeedReturnsReels(t *testing.T) {
	f := newRelatedFixture(t, "flick", "")
	stranger := uuid.New()
	f.recent = []map[string]any{
		stubPost(stranger, "long_video"),
		stubPost(stranger, "flick"),
	}

	page, _, err := f.svc.GetRelatedVideos(context.Background(), f.viewer, f.seed.ID, 10, 0)
	if err != nil {
		t.Fatalf("GetRelatedVideos: %v", err)
	}
	if len(page) != 1 || page[0].ContentType != "flick" {
		t.Fatalf("a reel's related list returned %v, want exactly one reel", page)
	}
}

func TestRelated_SkipsTheAuthorCatalogueWhenTheViewerIsTheCreator(t *testing.T) {
	f := newRelatedFixture(t, "long_video", "")
	f.seed.AuthorID = f.viewer // the viewer is watching their own upload
	f.recent = []map[string]any{stubPost(uuid.New(), "long_video")}

	if _, _, err := f.svc.GetRelatedVideos(context.Background(), f.viewer, f.seed.ID, 10, 0); err != nil {
		t.Fatalf("GetRelatedVideos: %v", err)
	}
	for _, c := range f.calls {
		if strings.Contains(c, "/v1/posts/by-author/") {
			t.Fatal("fetched the creator's own catalogue for a viewer who IS the creator; every row would have been dropped")
		}
	}
}

func TestRelated_UnknownSeedIsNotFound(t *testing.T) {
	f := newRelatedFixture(t, "long_video", "")
	// A post id the stub does not know: post-service's batch omits it,
	// which is the same answer it gives for a post the viewer may not
	// see. One response for both, so the endpoint cannot be used to probe
	// for hidden posts.
	_, _, err := f.svc.GetRelatedVideos(context.Background(), f.viewer, uuid.New(), 10, 0)
	if err != ErrFeedbackPostNotFound {
		t.Fatalf("unknown seed returned %v, want ErrFeedbackPostNotFound", err)
	}
}

func TestRelated_PagingConsumesTheOrderingExactly(t *testing.T) {
	f := newRelatedFixture(t, "long_video", "")
	author := f.seed.AuthorID
	for i := 0; i < 7; i++ {
		f.byAuthor = append(f.byAuthor, stubPost(author, "long_video"))
	}

	seen := map[uuid.UUID]int{}
	offset, pages := 0, 0
	for {
		page, next, err := f.svc.GetRelatedVideos(context.Background(), f.viewer, f.seed.ID, 3, offset)
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		for _, p := range page {
			seen[p.ID]++
		}
		pages++
		if next == 0 {
			break
		}
		if pages > 10 {
			t.Fatal("paging did not terminate")
		}
		offset = next
	}
	if len(seen) != 7 {
		t.Fatalf("paging returned %d distinct videos, want all 7", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("video %s appeared on %d pages; paging must not repeat", id, n)
		}
	}
}

func TestOrderByCandidates_KeepsRankingOrderAndToleratesDrops(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	order := []FeedItem{{PostID: a}, {PostID: b}, {PostID: c}}
	// Hydration returned them shuffled and dropped one (a keyword filter,
	// a private author, a "not interested" answer).
	hydrated := []HydratedPost{{ID: c}, {ID: a}}

	got := orderByCandidates(hydrated, order)
	if len(got) != 2 || got[0].ID != a || got[1].ID != c {
		t.Fatalf("orderByCandidates = %v, want [a c] in ranked order", got)
	}
}

func TestExcludeByID(t *testing.T) {
	keep, drop := uuid.New(), uuid.New()
	got := excludeByID([]FeedItem{{PostID: drop}, {PostID: keep}, {PostID: drop}}, drop)
	if len(got) != 1 || got[0].PostID != keep {
		t.Fatalf("excludeByID = %v, want only %v", got, keep)
	}
}

func TestDecodeHashtags(t *testing.T) {
	if got := decodeHashtags(nil); got != nil {
		t.Errorf("nil hashtags = %v, want nil", got)
	}
	if got := decodeHashtags(json.RawMessage(`["guitar","music"]`)); len(got) != 2 || got[0] != "guitar" {
		t.Errorf("decodeHashtags = %v", got)
	}
	// A shape we did not expect costs a ranking nudge, never a request.
	if got := decodeHashtags(json.RawMessage(`{"not":"a list"}`)); got != nil {
		t.Errorf("unparseable hashtags = %v, want nil", got)
	}
}

func TestIntersectIDs(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	got := intersectIDs([]uuid.UUID{a, b, c}, []uuid.UUID{b, c, uuid.New()})
	if len(got) != 2 {
		t.Fatalf("intersectIDs returned %d, want 2", len(got))
	}
	if len(intersectIDs(nil, []uuid.UUID{a})) != 0 {
		t.Error("an empty side must yield an empty intersection")
	}
	// Duplicates on either side must not be emitted twice: the result
	// becomes a Redis set, but a duplicated id would inflate the count
	// the warmer logs and reports.
	if got := intersectIDs([]uuid.UUID{a, a}, []uuid.UUID{a, a}); len(got) != 1 {
		t.Errorf("duplicate ids produced %d entries, want 1", len(got))
	}
}
