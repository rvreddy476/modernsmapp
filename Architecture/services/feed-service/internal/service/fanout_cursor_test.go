package service

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Fan-out pages the graph by cursor (16 Nov 2026). graph-service's offset
// lists refuse anything past offset 10,000, so an author with more than
// ~10,100 followers could not be fanned out at all; the event was
// dead-lettered. These tests run against a stub that answers in the pinned
// contract and REFUSES offset paging the way production does.

// strictGraph is pagedGraph with production's refusals: any request that
// pages by offset past 10,000, or reaches the old public list paths, fails
// the test.
func strictGraph(t *testing.T, g *pagedGraph) (*Service, *recordingTimelines) {
	t.Helper()
	s, _ := chunkedService(t, g)
	guard := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("offset") {
			t.Errorf("fan-out paged by offset: %s", r.URL)
		}
		if strings.HasPrefix(r.URL.Path, "/v1/graph/") && !strings.Contains(r.URL.Path, "/counts/") {
			t.Errorf("fan-out read a public list route: %s", r.URL)
		}
		g.serve(w, r)
	}))
	t.Cleanup(guard.Close)
	s.graphURL = guard.URL
	tl := newRecordingTimelines()
	s.timelines = tl
	return s, tl
}

// The lead's acceptance test: 250 followers in pages of 100 reach all 250
// exactly once.
func TestFanoutPost_CursorWalkReachesEveryFollowerExactlyOnce(t *testing.T) {
	g := &pagedGraph{followers: ids(250)}
	s, tl := strictGraph(t, g)
	if err := s.FanoutPost(WithFanoutKey(context.Background(), "event-1"), uuid.New(), uuid.New(), time.Now(), "post", "public", uuid.Nil); err != nil {
		t.Fatal(err)
	}
	for i, f := range g.followers {
		if n := tl.rows(f); n != 1 {
			t.Fatalf("follower %d got %d rows, want exactly 1", i, n)
		}
	}
	for _, page := range []string{"followers:0", "followers:100", "followers:200"} {
		if n := g.hitsAt(page); n != 1 {
			t.Fatalf("page %s was read %d time(s), want 1", page, n)
		}
	}
	if n := len(g.hits); n != 4 { // three follower pages and the (empty) connections page
		t.Fatalf("graph was asked for %d distinct pages, want 4: %v", n, g.hits)
	}
}

// ... and resumes mid-way after a failure: the chunk that failed is read
// again from the saved cursor, the one before it is not, and nobody is
// missed.
func TestFanoutPost_CursorWalkResumesMidWayAfterAFailure(t *testing.T) {
	g := &pagedGraph{followers: ids(250)}
	s, mr := chunkedService(t, g)
	tl := &flakyTimelines{recordingTimelines: newRecordingTimelines(), failFor: g.followers[150], failing: true}
	s.timelines = tl
	author, post := uuid.New(), uuid.New()
	ctx := WithFanoutKey(context.Background(), "event-1")

	if err := s.FanoutPost(ctx, post, author, time.Now(), "post", "public", uuid.Nil); err == nil {
		t.Fatal("a failed chunk was swallowed")
	}
	saved := mr.HGet(FanoutProgressKey(post, "event-1"), "followers")
	cursor, seen, ok := decodeLegPosition(saved)
	if !ok || seen != 100 || cursor != stubGraphCursor(g.followers[99]) {
		t.Fatalf("saved position %q decodes to (%q, %d, %v); want the cursor after follower 99 and 100 read", saved, cursor, seen, ok)
	}

	tl.mu.Lock()
	tl.failing = false
	tl.mu.Unlock()
	if err := s.FanoutPost(ctx, post, author, time.Now(), "post", "public", uuid.Nil); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if n := g.hitsAt("followers:0"); n != 1 {
		t.Fatalf("the finished first chunk was read %d times; the retry must resume, not start over", n)
	}
	for i, f := range g.followers {
		n := tl.rows(f)
		switch {
		case n == 0:
			t.Fatalf("follower %d never got the row", i)
		case (i < 100 || i >= 200) && n != 1:
			t.Fatalf("follower %d (outside the failed chunk) got %d rows, want exactly 1", i, n)
		case n > 2:
			t.Fatalf("follower %d got %d rows; the failed chunk is written at most twice", i, n)
		}
	}
}

// The finding itself: more than 10,100 followers. With pull off there is no
// celeb switch, so every follower must get the row by push.
func TestFanoutPost_ReachesFollowersPastTenThousand(t *testing.T) {
	g := &pagedGraph{followers: ids(10250)}
	s, tl := strictGraph(t, g)
	s.celeb = celebConfig{pull: false}
	if err := s.FanoutPost(context.Background(), uuid.New(), uuid.New(), time.Now(), "post", "public", uuid.Nil); err != nil {
		t.Fatalf("fan-out to 10,250 followers failed (dead-lettered before cursor paging): %v", err)
	}
	for i, f := range g.followers {
		if n := tl.rows(f); n != 1 {
			t.Fatalf("follower %d of 10,250 got %d rows, want 1", i, n)
		}
	}
}

// Progress saved before cursor paging is a bare offset ("100"). It cannot
// be turned into a cursor, so the leg starts over: every follower gets the
// row (writes upsert), and the offset is never sent to graph-service, which
// would refuse it and get the event dead-lettered.
func TestFanoutPost_OffsetFormProgressRestartsTheLeg(t *testing.T) {
	for _, old := range []string{"100", "10100", "k1:abc:x", "k1:100:", "garbage"} {
		t.Run(old, func(t *testing.T) {
			g := &pagedGraph{followers: ids(250)}
			s, mr := chunkedService(t, g)
			tl := newRecordingTimelines()
			s.timelines = tl
			post := uuid.New()
			mr.HSet(FanoutProgressKey(post, "event-1"), "followers", old)

			if err := s.FanoutPost(WithFanoutKey(context.Background(), "event-1"), post, uuid.New(), time.Now(), "post", "public", uuid.Nil); err != nil {
				t.Fatalf("an old-form position failed the event: %v", err)
			}
			for i, f := range g.followers {
				if tl.rows(f) != 1 {
					t.Fatalf("follower %d got %d rows after a restart, want 1", i, tl.rows(f))
				}
			}
			if n := g.hitsAt("followers:0"); n != 1 {
				t.Fatalf("the leg did not restart from the first page (read %d times)", n)
			}
		})
	}
}

// A saved cursor graph-service refuses (400 INVALID_CURSOR) restarts the
// leg once instead of dead-lettering the event over a position record. A
// refusal of the FIRST page is still a refusal.
func TestFanoutPost_RefusedSavedCursorRestartsTheLegOnce(t *testing.T) {
	g := &pagedGraph{followers: ids(250)}
	s, mr := chunkedService(t, g)
	tl := newRecordingTimelines()
	s.timelines = tl
	post := uuid.New()
	// A well-formed position whose cursor names nobody on the list: the stub
	// (like graph-service for a cursor it did not issue) answers 400.
	mr.HSet(FanoutProgressKey(post, "event-1"), "followers", encodeLegPosition(100, stubGraphCursor(uuid.New())))

	if err := s.FanoutPost(WithFanoutKey(context.Background(), "event-1"), post, uuid.New(), time.Now(), "post", "public", uuid.Nil); err != nil {
		t.Fatalf("a refused saved cursor failed the event: %v", err)
	}
	for i, f := range g.followers {
		if tl.rows(f) != 1 {
			t.Fatalf("follower %d got %d rows, want 1", i, tl.rows(f))
		}
	}

	// No saved position: a 400 is a refusal and must not loop.
	var calls atomic.Int32
	refuse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer refuse.Close()
	s.graphURL = refuse.URL
	err := s.FanoutPost(WithFanoutKey(context.Background(), "event-2"), uuid.New(), uuid.New(), time.Now(), "post", "public", uuid.Nil)
	if !UpstreamRejected(err) {
		t.Fatalf("a refused first page = %v, want a refusal", err)
	}
	if n := calls.Load(); n != 2 { // one followers page, one connections page
		t.Fatalf("graph was called %d times for a refused first page, want 2 (no restart loop)", n)
	}
}

// An unfollow while the walk is under way removes a row the walk has
// already passed. Under offset paging the next page starts one row late
// and a follower is skipped; by cursor nobody who stayed is missed.
func TestFanoutPost_UnfollowMidWalkSkipsNobody(t *testing.T) {
	g := &pagedGraph{followers: ids(250)}
	original := append([]uuid.UUID(nil), g.followers...)
	s, _ := chunkedService(t, g)
	leaver := original[5]
	var once sync.Once
	tl := &hookTimelines{recordingTimelines: newRecordingTimelines()}
	tl.onWrite = func(userID uuid.UUID) {
		if userID != original[150] { // a write of the second chunk
			return
		}
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			kept := make([]uuid.UUID, 0, len(g.followers))
			for _, id := range g.followers {
				if id != leaver {
					kept = append(kept, id)
				}
			}
			g.followers = kept
		})
	}
	s.timelines = tl
	if err := s.FanoutPost(context.Background(), uuid.New(), uuid.New(), time.Now(), "post", "public", uuid.Nil); err != nil {
		t.Fatal(err)
	}
	for i, f := range original {
		if n := tl.rows(f); n != 1 {
			t.Fatalf("follower %d got %d rows, want exactly 1 (an unfollow mid-walk shifted the pages)", i, n)
		}
	}
}

// hookTimelines calls onWrite before recording a home row.
type hookTimelines struct {
	*recordingTimelines
	onWrite func(userID uuid.UUID)
}

func (h *hookTimelines) AddToHomeTimeline(ctx context.Context, userID uuid.UUID, postID, authorID uuid.UUID, createdAt time.Time, ct string) error {
	h.onWrite(userID)
	return h.recordingTimelines.AddToHomeTimeline(ctx, userID, postID, authorID, createdAt, ct)
}

// feed-35's acceptance: a viewer following 10,200 accounts. The whole list
// comes back, in pages of graphListPageSize, never by offset.
func TestFetchFollowing_PastTenThousandByCursor(t *testing.T) {
	following := ids(10200)
	var pages atomic.Int32
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if graphListKind(r.URL.Path) != "following" || r.URL.Query().Has("offset") {
			t.Errorf("unexpected request %s", r.URL)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if got := r.URL.Query().Get("limit"); got != "500" {
			t.Errorf("limit = %s, want 500", got)
		}
		pages.Add(1)
		writeGraphIDPage(w, r, following)
	}))
	defer graph.Close()
	s := &Service{graphURL: graph.URL, graphClient: graph.Client()}
	got, err := s.fetchFollowing(context.Background(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(following) {
		t.Fatalf("got %d ids, want %d", len(got), len(following))
	}
	for i := range following {
		if got[i] != following[i] {
			t.Fatalf("position %d differs", i)
		}
	}
	if n := pages.Load(); n != 21 {
		t.Fatalf("list was read in %d pages, want 21", n)
	}
}

func TestDecodeLegPosition(t *testing.T) {
	cursor := "1794821400000000:7d9f1c1e-55a0-4a53-9a55-0d5a3e1f0c11"
	if c, n, ok := decodeLegPosition(encodeLegPosition(300, cursor)); !ok || c != cursor || n != 300 {
		t.Fatalf("round trip = (%q, %d, %v)", c, n, ok)
	}
	if c, n, ok := decodeLegPosition(""); !ok || c != "" || n != 0 {
		t.Fatalf("no saved position = (%q, %d, %v), want the start", c, n, ok)
	}
	for _, raw := range []string{"0", "100", "10100", cursor, "k1:", "k1:5", "k1:5:", "k1:x:" + cursor, "k1:-1:" + cursor, "k2:5:" + cursor} {
		if c, n, ok := decodeLegPosition(raw); ok || c != "" || n != 0 {
			t.Errorf("decodeLegPosition(%q) = (%q, %d, %v), want a restart", raw, c, n, ok)
		}
	}
}

// Both sides of the contract: the three golden files are graph-service's
// (testdata/contracts/internal_lists at its module root), copied byte for
// byte, and the real client reads them.
func TestGraphInternalListContract(t *testing.T) {
	dir := filepath.Join("testdata", "contracts", "graph_internal_lists")
	read := func(name string) []byte {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	serve := func(status int, body []byte) *Service {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write(body)
		}))
		t.Cleanup(srv.Close)
		return &Service{graphURL: srv.URL, graphClient: srv.Client()}
	}

	t.Run("id_page.json", func(t *testing.T) {
		got, next, err := serve(200, read("id_page.json")).graphIDPage(context.Background(), "followers", uuid.New(), "", 2)
		if err != nil {
			t.Fatal(err)
		}
		want := []uuid.UUID{uuid.MustParse("11111111-1111-4111-8111-111111111111"), uuid.MustParse("22222222-2222-4222-8222-222222222222")}
		if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("ids = %v, want %v", got, want)
		}
		if next != "1794821340123456:22222222-2222-4222-8222-222222222222" {
			t.Fatalf("next = %q", next)
		}
	})
	t.Run("id_page_last.json", func(t *testing.T) {
		got, next, err := serve(200, read("id_page_last.json")).graphIDPage(context.Background(), "followers", uuid.New(), "1794821340123456:22222222-2222-4222-8222-222222222222", 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0] != uuid.MustParse("33333333-3333-4333-8333-333333333333") || next != "" {
			t.Fatalf("last page = %v, next %q", got, next)
		}
	})
	t.Run("error_invalid_cursor.json", func(t *testing.T) {
		_, _, err := serve(400, read("error_invalid_cursor.json")).graphIDPage(context.Background(), "followers", uuid.New(), "300", 2)
		var status *UpstreamStatusError
		if !errors.As(err, &status) || status.Status != 400 || !strings.Contains(status.Body, "INVALID_CURSOR") || !UpstreamRejected(err) {
			t.Fatalf("a refused cursor = %v, want a 400 refusal naming INVALID_CURSOR", err)
		}
	})
	t.Run("the cursor is sent back verbatim, escaped", func(t *testing.T) {
		var gotCursor, gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotCursor, gotPath = r.URL.Query().Get("cursor"), r.URL.Path
			_, _ = w.Write(read("id_page_last.json"))
		}))
		defer srv.Close()
		s := &Service{graphURL: srv.URL, graphClient: srv.Client()}
		owner := uuid.New()
		cursor := "1794821340123456:22222222-2222-4222-8222-222222222222"
		if _, _, err := s.graphIDPage(context.Background(), "connections", owner, cursor, 100); err != nil {
			t.Fatal(err)
		}
		if gotCursor != cursor || gotPath != "/v1/internal/graph/connections/"+owner.String() {
			t.Fatalf("request = %s ?cursor=%q", gotPath, gotCursor)
		}
	})

	// The copies must not drift from graph-service's originals. Skipped only
	// where the sibling module is not checked out.
	origin := filepath.Join("..", "..", "..", "graph-service", "testdata", "contracts", "internal_lists")
	entries, err := os.ReadDir(origin)
	if err != nil {
		t.Skipf("graph-service fixtures not readable from here: %v", err)
	}
	lf := func(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }
	for _, e := range entries {
		theirs, err := os.ReadFile(filepath.Join(origin, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(lf(theirs), lf(read(e.Name()))) {
			t.Errorf("%s differs from graph-service's fixture; copy it again", e.Name())
		}
	}
}

// A 200 that is not the contract is an error, never "nobody follows them".
func TestGraphIDPage_MalformedBodiesAreErrors(t *testing.T) {
	for name, body := range map[string]string{
		"the old bare array":         `{"data":["11111111-1111-4111-8111-111111111111"]}`,
		"the public cursor shape":    `{"data":{"items":[],"next_cursor":""}}`,
		"no next_cursor":             `{"data":{"ids":[]}}`,
		"null ids":                   `{"data":{"ids":null,"next_cursor":""}}`,
		"empty object":               `{}`,
		"not json":                   `<html>bad gateway</html>`,
		"an id that is not a uuid":   `{"data":{"ids":["nope"],"next_cursor":""}}`,
		"a cursor that goes nowhere": `{"data":{"ids":[],"next_cursor":"same"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()
			s := &Service{graphURL: srv.URL, graphClient: srv.Client()}
			ids, _, err := s.graphIDPage(context.Background(), "followers", uuid.New(), "same", 100)
			if err == nil {
				t.Fatalf("decoded as a page of %d ids", len(ids))
			}
		})
	}
}
