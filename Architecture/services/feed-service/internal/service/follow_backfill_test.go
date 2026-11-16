package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/atpost/feed-service/internal/store/scylla"
	"github.com/google/uuid"
)

// feed-08 and feed-60: the follow backfill writes through the worker pool,
// a failed row fails the event, and only posts the follower may be listed
// are copied.

// fixedAuthorTimeline is the followee's author timeline.
type fixedAuthorTimeline struct {
	rows []scylla.FeedItem
	err  error
}

func (f fixedAuthorTimeline) GetAuthorTimelineMultiBucket(_ context.Context, _ uuid.UUID, limit, _ int) ([]scylla.FeedItem, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.rows[:min(limit, len(f.rows))], nil
}

// slowTimelines records home writes by POST, takes `delay` per write and
// fails the write of `failPost`.
type slowTimelines struct {
	mu       sync.Mutex
	delay    time.Duration
	failPost uuid.UUID
	written  map[uuid.UUID]homeWrite
}

type homeWrite struct {
	owner, author uuid.UUID
	contentType   string
	createdAt     time.Time
	times         int
}

func (s *slowTimelines) AddToAuthorTimeline(context.Context, uuid.UUID, uuid.UUID, time.Time, string) error {
	return nil
}

func (s *slowTimelines) AddToHomeTimeline(_ context.Context, userID uuid.UUID, postID, authorID uuid.UUID, createdAt time.Time, ct string) error {
	time.Sleep(s.delay)
	if postID == s.failPost {
		return errors.New("scylla write timeout")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.written == nil {
		s.written = map[uuid.UUID]homeWrite{}
	}
	w := s.written[postID]
	s.written[postID] = homeWrite{owner: userID, author: authorID, contentType: ct, createdAt: createdAt, times: w.times + 1}
	return nil
}

func (s *slowTimelines) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.written)
}

// backfillPostService answers POST /v1/posts/batch from `visibility`: a
// post with no entry is left out (post-service leaves out private and
// deleted posts for a viewer who is not the author).
type backfillPostService struct {
	mu         sync.Mutex
	visibility map[uuid.UUID]string
	author     uuid.UUID
	status     int // non-zero: answer with this status
	viewers    []string
}

func (b *backfillPostService) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/posts/batch" {
			t.Errorf("backfill called %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		var req struct {
			IDs      []string `json:"ids"`
			ViewerID string   `json:"viewer_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		b.mu.Lock()
		b.viewers = append(b.viewers, req.ViewerID+"|"+r.Header.Get("X-User-Id"))
		b.mu.Unlock()
		if b.status != 0 {
			w.WriteHeader(b.status)
			return
		}
		data := map[string]json.RawMessage{}
		for _, raw := range req.IDs {
			id, _ := uuid.Parse(raw)
			vis, ok := b.visibility[id]
			if !ok {
				continue
			}
			data[raw] = json.RawMessage(`{"id":"` + raw + `","author_id":"` + b.author.String() + `","visibility":"` + vis + `",
				"content_type":"post","created_at":"2026-11-16T10:00:00Z","updated_at":"2026-11-16T10:00:00Z"}`)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func authorRows(author uuid.UUID, n int) []scylla.FeedItem {
	start := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	rows := make([]scylla.FeedItem, n)
	for i := range rows {
		rows[i] = scylla.FeedItem{PostID: uuid.New(), AuthorID: author, CreatedAt: start.Add(-time.Duration(i) * time.Minute), ContentType: "post"}
	}
	return rows
}

func backfillService(t *testing.T, rows []scylla.FeedItem, post *backfillPostService, tl timelineWriter) *Service {
	t.Helper()
	srv := post.server(t)
	return &Service{
		postServiceURL: srv.URL, postClient: srv.Client(),
		authorTimelines: fixedAuthorTimeline{rows: rows}, timelines: tl,
	}
}

func allPublic(author uuid.UUID, rows []scylla.FeedItem) *backfillPostService {
	p := &backfillPostService{author: author, visibility: map[uuid.UUID]string{}}
	for _, r := range rows {
		p.visibility[r.PostID] = "public"
	}
	return p
}

// feed-08's acceptance test: 50 rows at 10 ms each. One after another that
// is 500 ms of consumer time per follow; through the pool it is four rounds.
func TestBackfillHomeOnFollow_FiftySlowRowsFinishWellUnderHalfASecond(t *testing.T) {
	follower, followee := uuid.New(), uuid.New()
	rows := authorRows(followee, 50)
	tl := &slowTimelines{delay: 10 * time.Millisecond}
	s := backfillService(t, rows, allPublic(followee, rows), tl)

	start := time.Now()
	res, err := s.BackfillHomeOnFollow(context.Background(), follower, followee)
	took := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if took >= 250*time.Millisecond {
		t.Fatalf("50 writes of 10 ms took %v, want well under 500 ms (they ran one after another)", took)
	}
	if res.Found != 50 || res.Written != 50 || res.Skipped != 0 || tl.count() != 50 {
		t.Fatalf("result %+v with %d rows written, want all 50", res, tl.count())
	}
	for _, r := range rows {
		w := tl.written[r.PostID]
		if w.owner != follower || w.author != followee || w.times != 1 || !w.createdAt.Equal(r.CreatedAt) || w.contentType != "post" {
			t.Fatalf("row for %s = %+v; want one row in the follower's timeline, by the followee, at the post's own time", r.PostID, w)
		}
	}
}

// A failed row used to be logged and dropped, and the event committed. It
// now fails the backfill, so the event is retried; the other rows are still
// written (the retry rewrites them, which is an upsert).
func TestBackfillHomeOnFollow_OneFailingRowIsAnError(t *testing.T) {
	follower, followee := uuid.New(), uuid.New()
	rows := authorRows(followee, 50)
	tl := &slowTimelines{failPost: rows[23].PostID}
	s := backfillService(t, rows, allPublic(followee, rows), tl)

	res, err := s.BackfillHomeOnFollow(context.Background(), follower, followee)
	if err == nil {
		t.Fatal("a failed row was swallowed: the event is committed and that post never reaches the follower")
	}
	if res.Written != 0 {
		t.Fatalf("a failed backfill reported %d rows written", res.Written)
	}
	if tl.count() != 49 {
		t.Fatalf("%d rows written, want the other 49 (a failure must not abandon the rest)", tl.count())
	}
}

// feed-60: the author timeline holds every post of the author. Only the
// ones the follower may be listed are copied.
func TestBackfillHomeOnFollow_SkipsPostsTheFollowerMayNotBeListed(t *testing.T) {
	follower, followee := uuid.New(), uuid.New()
	rows := authorRows(followee, 9)
	post := &backfillPostService{author: followee, visibility: map[uuid.UUID]string{
		rows[0].PostID: "public",
		rows[1].PostID: "followers",
		rows[2].PostID: "friends", // post-service returned it, so the author follows back
		rows[3].PostID: "",        // a legacy row with no visibility recorded
		rows[4].PostID: "unlisted",
		rows[5].PostID: "private", // returned only when shared with the viewer: still never listed
		rows[6].PostID: "staged",
		rows[7].PostID: "something-new",
		// rows[8]: left out by post-service (private, or deleted).
	}}
	tl := &slowTimelines{}
	s := backfillService(t, rows, post, tl)

	res, err := s.BackfillHomeOnFollow(context.Background(), follower, followee)
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range rows {
		_, written := tl.written[r.PostID]
		if want := i <= 3; written != want {
			t.Errorf("row %d (visibility %q, returned %v): written = %v, want %v",
				i, post.visibility[r.PostID], i != 8, written, want)
		}
	}
	if res.Found != 9 || res.Written != 4 || res.Skipped != 5 {
		t.Fatalf("result %+v, want found 9, written 4, skipped 5", res)
	}
	// post-service was asked AS THE FOLLOWER: its read rules are per viewer.
	if len(post.viewers) != 1 || post.viewers[0] != follower.String()+"|"+follower.String() {
		t.Fatalf("visibility was asked as %v, want once as the follower %s", post.viewers, follower)
	}
}

// Fails closed: when post-service cannot say what the follower may see,
// nothing is copied and the error is returned (the event is retried).
func TestBackfillHomeOnFollow_FailsClosedWithoutPostService(t *testing.T) {
	follower, followee := uuid.New(), uuid.New()
	rows := authorRows(followee, 5)
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusInternalServerError, http.StatusUnauthorized} {
		post := allPublic(followee, rows)
		post.status = status
		tl := &slowTimelines{}
		s := backfillService(t, rows, post, tl)
		if _, err := s.BackfillHomeOnFollow(context.Background(), follower, followee); err == nil {
			t.Fatalf("post-service answered %d and the backfill reported success", status)
		}
		if tl.count() != 0 {
			t.Fatalf("post-service answered %d and %d unfiltered rows were copied", status, tl.count())
		}
	}
}

func TestBackfillHomeOnFollow_NothingToCopyAndReadFailure(t *testing.T) {
	follower, followee := uuid.New(), uuid.New()
	post := &backfillPostService{author: followee}
	s := backfillService(t, nil, post, &slowTimelines{})
	res, err := s.BackfillHomeOnFollow(context.Background(), follower, followee)
	if err != nil || res != (FollowBackfillResult{}) {
		t.Fatalf("empty author timeline: %+v, %v", res, err)
	}
	if len(post.viewers) != 0 {
		t.Fatal("post-service was asked about no posts")
	}

	s.authorTimelines = fixedAuthorTimeline{err: errors.New("scylla down")}
	if _, err := s.BackfillHomeOnFollow(context.Background(), follower, followee); err == nil {
		t.Fatal("an unreadable author timeline was reported as nothing to backfill")
	}
}
