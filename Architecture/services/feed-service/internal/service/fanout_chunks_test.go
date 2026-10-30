package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Audit H3: the fan-out runs chunk by chunk (one graph page and its writes),
// records its progress per event, resumes at the failed chunk on retry, and
// recognises a celeb before pushing anything.

// pagedGraph serves /followers/ and /connections/ with real offset paging
// and counts how often each (kind, offset) was asked for.
type pagedGraph struct {
	mu          sync.Mutex
	followers   []uuid.UUID
	connections []uuid.UUID
	hits        map[string]int
	countStatus int // status for /counts/; 0 = 404
	count       int
}

func (g *pagedGraph) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if strings.Contains(r.URL.Path, "/counts/") {
		if g.countStatus == 0 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(g.countStatus)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"follower_count": g.count}})
		return
	}
	var list []uuid.UUID
	kind := ""
	switch {
	case strings.Contains(r.URL.Path, "/followers/"):
		list, kind = g.followers, "followers"
	case strings.Contains(r.URL.Path, "/connections/"):
		list, kind = g.connections, "connections"
	default:
		w.WriteHeader(http.StatusNotFound)
		return
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	g.hits[kind+":"+strconv.Itoa(offset)]++
	end := offset + limit
	if end > len(list) {
		end = len(list)
	}
	page := []string{}
	for _, id := range list[min(offset, len(list)):end] {
		page = append(page, id.String())
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": page})
}

func (g *pagedGraph) hitsAt(key string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.hits[key]
}

func ids(n int) []uuid.UUID {
	out := make([]uuid.UUID, n)
	for i := range out {
		out[i] = uuid.New()
	}
	return out
}

// flakyTimelines fails every home write to `failFor` while failing is set.
type flakyTimelines struct {
	*recordingTimelines
	mu      sync.Mutex
	failFor uuid.UUID
	failing bool
}

func (f *flakyTimelines) AddToHomeTimeline(ctx context.Context, userID uuid.UUID, postID, authorID uuid.UUID, createdAt time.Time, ct string) error {
	f.mu.Lock()
	fail := f.failing && userID == f.failFor
	f.mu.Unlock()
	if fail {
		return errors.New("scylla write timeout")
	}
	return f.recordingTimelines.AddToHomeTimeline(ctx, userID, postID, authorID, createdAt, ct)
}

func chunkedService(t *testing.T, g *pagedGraph) (*Service, *miniredis.Miniredis) {
	t.Helper()
	g.hits = map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(srv.Close)
	mr := miniredis.RunT(t)
	return &Service{
		graphURL:    srv.URL,
		graphClient: &http.Client{Timeout: 2 * time.Second},
		rdb:         redis.NewClient(&redis.Options{Addr: mr.Addr()}),
		celebs:      fixedCelebs(false),
	}, mr
}

func TestFanoutPost_RetryResumesAtTheFailedChunk(t *testing.T) {
	g := &pagedGraph{followers: ids(250)}
	s, mr := chunkedService(t, g)
	tl := &flakyTimelines{recordingTimelines: newRecordingTimelines(), failFor: g.followers[150], failing: true}
	s.timelines = tl
	author, post := uuid.New(), uuid.New()
	ctx := WithFanoutKey(context.Background(), "event-1")

	if err := s.FanoutPost(ctx, post, author, time.Now(), "post", "public", uuid.Nil); err == nil {
		t.Fatal("a failed chunk was swallowed; the event must be retried")
	}
	if got := mr.HGet(FanoutProgressKey(post, "event-1"), "followers"); got != "100" {
		t.Fatalf("progress after a failure in the second chunk = %q, want 100 (the failed chunk's offset)", got)
	}
	// The third chunk is not reached on the failing run: chunks are units.
	if n := g.hitsAt("followers:200"); n != 0 {
		t.Fatalf("the chunk after the failed one was fetched %d time(s) before the retry", n)
	}

	tl.mu.Lock()
	tl.failing = false
	tl.mu.Unlock()
	if err := s.FanoutPost(ctx, post, author, time.Now(), "post", "public", uuid.Nil); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if n := g.hitsAt("followers:0"); n != 1 {
		t.Fatalf("the first (successful) chunk was fetched %d times; a retry must resume, not start over", n)
	}
	if n := g.hitsAt("followers:100"); n != 2 {
		t.Fatalf("the failed chunk was fetched %d times, want 2 (the failure and the retry)", n)
	}
	for i, f := range g.followers {
		if tl.rows(f) == 0 {
			t.Fatalf("follower %d never got the row", i)
		}
	}
	if mr.Exists(FanoutProgressKey(post, "event-1")) {
		t.Fatal("progress kept after a complete fan-out")
	}
}

func TestFanoutPost_DifferentEventStartsFresh(t *testing.T) {
	g := &pagedGraph{followers: ids(150)}
	s, mr := chunkedService(t, g)
	tl := newRecordingTimelines()
	s.timelines = tl
	post := uuid.New()
	// A finished leg recorded by ANOTHER event (the creation) must not make
	// a visibility change's re-fan-out skip anyone.
	mr.HSet(FanoutProgressKey(post, "creation"), "followers:done", "1")
	if err := s.FanoutPost(WithFanoutKey(context.Background(), "visibility-change"), post, uuid.New(), time.Now(), "post", "public", uuid.Nil); err != nil {
		t.Fatal(err)
	}
	for _, f := range g.followers {
		if tl.rows(f) != 1 {
			t.Fatal("a new event reused another event's progress and skipped followers")
		}
	}
}

func TestFanoutPost_FollowerCountFlagsCelebBeforeAnyPush(t *testing.T) {
	g := &pagedGraph{followers: ids(5), countStatus: http.StatusOK, count: 50000}
	s, _ := chunkedService(t, g)
	tl := newRecordingTimelines()
	s.timelines = tl
	marker := &recordingCelebs{}
	s.celebs = marker
	s.celeb = celebConfig{pull: true, threshold: 10000}
	author := uuid.New()

	if err := s.FanoutPost(context.Background(), uuid.New(), author, time.Now(), "post", "public", uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if !marker.flagged(author) {
		t.Fatal("an author with 50k followers was not flagged celeb")
	}
	if n := g.hitsAt("followers:0"); n != 0 {
		t.Fatalf("follower pages were read %d time(s) for a known celeb", n)
	}
	for _, f := range g.followers {
		if tl.rows(f) != 0 {
			t.Fatal("a celeb's post was pushed to a follower")
		}
	}
}

func TestFanoutPost_CountBelowThresholdStillPushes(t *testing.T) {
	g := &pagedGraph{followers: ids(3), countStatus: http.StatusOK, count: 3}
	s, _ := chunkedService(t, g)
	tl := newRecordingTimelines()
	s.timelines = tl
	marker := &recordingCelebs{}
	s.celebs = marker
	s.celeb = celebConfig{pull: true, threshold: 10000}
	author := uuid.New()
	if err := s.FanoutPost(context.Background(), uuid.New(), author, time.Now(), "post", "public", uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if marker.flagged(author) {
		t.Fatal("an author with 3 followers was flagged celeb")
	}
	for _, f := range g.followers {
		if tl.rows(f) != 1 {
			t.Fatal("a follower of a small author got no row")
		}
	}
}

func TestRecipientDedupe_IsBounded(t *testing.T) {
	d := newRecipientDedupe()
	for i := 0; i < fanoutDedupeCap+500; i++ {
		d.first(uuid.New())
	}
	if len(d.seen) > fanoutDedupeCap {
		t.Fatalf("dedupe set holds %d ids, cap is %d", len(d.seen), fanoutDedupeCap)
	}
	id := uuid.New()
	if !d.first(id) {
		t.Fatal("an unseen id past the cap must still be written")
	}
}

func TestParseCelebConfig_Defaults(t *testing.T) {
	cases := []struct {
		pull, threshold string
		want            celebConfig
	}{
		{"", "", celebConfig{pull: true, threshold: defaultCelebThreshold}},
		{"false", "", celebConfig{pull: false, threshold: defaultCelebThreshold}},
		{"true", "5000", celebConfig{pull: true, threshold: 5000}},
		{"", "0", celebConfig{pull: true, threshold: 0}},
		{"", "50000", celebConfig{pull: true, threshold: maxCelebThreshold}},
		{"", "lots", celebConfig{pull: true, threshold: defaultCelebThreshold}},
	}
	for _, tc := range cases {
		if got := parseCelebConfig(tc.pull, tc.threshold); got != tc.want {
			t.Errorf("parseCelebConfig(%q, %q) = %+v, want %+v", tc.pull, tc.threshold, got, tc.want)
		}
	}
	if defaultCelebThreshold != 10000 {
		t.Fatalf("default celeb threshold changed to %d; update the founder-facing note in celebpull.go", defaultCelebThreshold)
	}
}

// countingTransport counts the requests that went through it.
type countingTransport struct {
	mu sync.Mutex
	n  int
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
	return http.DefaultTransport.RoundTrip(r)
}

func (c *countingTransport) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// Audit H2: work done for a Kafka event goes through the fan-out clients
// (their own breakers), and user requests never do, so a fan-out failure
// burst cannot open the breaker the request path's fail-closed filters use.
func TestBackgroundTrafficUsesTheFanoutClients(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []string{}, "user_ids": []string{}})
	}))
	defer srv.Close()
	request, background := &countingTransport{}, &countingTransport{}
	s := &Service{
		graphURL:    srv.URL,
		graphClient: &http.Client{Transport: request},
		fanout:      fanoutClients{graph: &http.Client{Transport: background}},
	}
	bg := WithBackgroundTraffic(context.Background())
	if _, err := s.fetchFollowers(bg, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if background.count() != 1 || request.count() != 0 {
		t.Fatalf("fan-out page went through request=%d background=%d, want 0/1", request.count(), background.count())
	}
	if _, err := s.getBlockedAndMuted(context.Background(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	if request.count() != 1 || background.count() != 1 {
		t.Fatalf("user request went through request=%d background=%d, want 1/1", request.count(), background.count())
	}
	// The production constructor builds distinct fan-out clients.
	f := newFanoutClients()
	if f.graph == nil || f.post == nil || f.user == nil {
		t.Fatal("newFanoutClients left a fan-out client nil")
	}
	real := New(nil, nil, nil)
	for name, pair := range map[string][2]*http.Client{
		"graph": {real.graphClient, real.fanout.graph},
		"post":  {real.postClient, real.fanout.post},
		"user":  {real.userClient, real.fanout.user},
	} {
		if pair[1] == nil || pair[0] == pair[1] || pair[0].Transport == pair[1].Transport {
			t.Fatalf("%s: the fan-out shares the request path's client/breaker", name)
		}
	}
}
