package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/feed-service/internal/store/postgres"
	"github.com/atpost/feed-service/internal/store/scylla"
	"github.com/google/uuid"
)

// Echo notes in feed-service (notes.go): fan-out (top-level vs reply,
// large-account skip), Following order and cursor, For You sources and
// filters, the unfollow purge's claim check, and Home exclusion.

// ── fakes ───────────────────────────────────────────────────────────────────

type fakeNotesTimelines struct {
	mu       sync.Mutex
	rows     map[uuid.UUID]map[uuid.UUID]scylla.FeedItem // owner → post → row
	liked    map[uuid.UUID][]uuid.UUID
	reposts  map[uuid.UUID][]scylla.FeedItem
	deleted  map[uuid.UUID][]uuid.UUID // owner → authors purged
	writeErr error
}

func newFakeNotesTimelines() *fakeNotesTimelines {
	return &fakeNotesTimelines{
		rows:    map[uuid.UUID]map[uuid.UUID]scylla.FeedItem{},
		liked:   map[uuid.UUID][]uuid.UUID{},
		reposts: map[uuid.UUID][]scylla.FeedItem{},
		deleted: map[uuid.UUID][]uuid.UUID{},
	}
}

func (f *fakeNotesTimelines) AddToNotesTimeline(_ context.Context, owner, post, author uuid.UUID, createdAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return f.writeErr
	}
	if f.rows[owner] == nil {
		f.rows[owner] = map[uuid.UUID]scylla.FeedItem{}
	}
	f.rows[owner][post] = scylla.FeedItem{
		PostID: post, AuthorID: author, CreatedAt: createdAt, ContentType: NoteContentType,
		CursorToken: scylla.TimelineTS(post, createdAt).String(),
	}
	return nil
}

func (f *fakeNotesTimelines) count(owner uuid.UUID) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows[owner])
}

func (f *fakeNotesTimelines) GetNotesTimelineBefore(_ context.Context, owner uuid.UUID, before string, limit int) ([]scylla.FeedItem, error) {
	f.mu.Lock()
	var all []scylla.FeedItem
	for _, r := range f.rows[owner] {
		all = append(all, r)
	}
	f.mu.Unlock()
	sort.Slice(all, func(i, j int) bool { return timelineKeyAfter(all[i].CursorToken, all[j].CursorToken) })
	var out []scylla.FeedItem
	for _, r := range all {
		if before != "" && !timelineKeyAfter(before, r.CursorToken) {
			continue
		}
		out = append(out, r)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (f *fakeNotesTimelines) DeleteNotesTimelineEntriesByAuthorForUser(_ context.Context, user, author uuid.UUID, _ int) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted[user] = append(f.deleted[user], author)
	n := 0
	for pid, r := range f.rows[user] {
		if r.AuthorID == author {
			delete(f.rows[user], pid)
			n++
		}
	}
	return n, nil
}

func (f *fakeNotesTimelines) UserInteractedPosts(_ context.Context, user uuid.UUID, _ int) ([]uuid.UUID, error) {
	return f.liked[user], nil
}

func (f *fakeNotesTimelines) GetHomeTimelineByContentTypes(_ context.Context, user uuid.UUID, _ []string, _ int) ([]scylla.FeedItem, error) {
	return f.reposts[user], nil
}

type fakeNotesMeta struct {
	mu     sync.Mutex
	notes  map[uuid.UUID]postgres.NoteRow
	pull   map[uuid.UUID]int
	celebs map[uuid.UUID]bool
}

func newFakeNotesMeta() *fakeNotesMeta {
	return &fakeNotesMeta{notes: map[uuid.UUID]postgres.NoteRow{}, pull: map[uuid.UUID]int{}, celebs: map[uuid.UUID]bool{}}
}

func (m *fakeNotesMeta) RecordNote(_ context.Context, n postgres.NoteRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.notes[n.PostID]; !ok {
		m.notes[n.PostID] = n
	}
	return nil
}

func (m *fakeNotesMeta) NoteMeta(_ context.Context, id uuid.UUID) (postgres.NoteRow, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.notes[id]
	return n, ok, nil
}

func (m *fakeNotesMeta) SetNoteDeleted(_ context.Context, id uuid.UUID, d bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n, ok := m.notes[id]; ok {
		n.Deleted = d
		m.notes[id] = n
	}
	return nil
}

func (m *fakeNotesMeta) SetNoteVisibility(_ context.Context, id uuid.UUID, v string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n, ok := m.notes[id]; ok {
		n.Visibility = v
		m.notes[id] = n
	}
	return nil
}

func (m *fakeNotesMeta) live(keep func(postgres.NoteRow) bool) []postgres.NoteRow {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []postgres.NoteRow
	for _, n := range m.notes {
		if n.TopLevel && !n.Deleted && n.Visibility == "public" && keep(n) {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (m *fakeNotesMeta) LiveNotesByAuthors(_ context.Context, authors []uuid.UUID, before time.Time, limit int) ([]postgres.NoteRow, error) {
	set := map[uuid.UUID]bool{}
	for _, a := range authors {
		set[a] = true
	}
	out := m.live(func(n postgres.NoteRow) bool { return set[n.AuthorID] && n.CreatedAt.Before(before) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *fakeNotesMeta) RecentLiveNotes(_ context.Context, since time.Time, limit int) ([]postgres.NoteRow, error) {
	out := m.live(func(n postgres.NoteRow) bool { return !n.CreatedAt.Before(since) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *fakeNotesMeta) LiveNotesAmong(_ context.Context, ids []uuid.UUID) ([]postgres.NoteRow, error) {
	set := map[uuid.UUID]bool{}
	for _, id := range ids {
		set[id] = true
	}
	return m.live(func(n postgres.NoteRow) bool { return set[n.PostID] }), nil
}

func (m *fakeNotesMeta) MarkNotesPullAuthor(_ context.Context, a uuid.UUID, n int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pull[a] = n
	return nil
}

func (m *fakeNotesMeta) IsNotesPullAuthor(_ context.Context, a uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.pull[a]
	return ok, nil
}

func (m *fakeNotesMeta) NotesPullAuthorsAmong(_ context.Context, ids []uuid.UUID) ([]uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []uuid.UUID
	for _, id := range ids {
		if _, ok := m.pull[id]; ok {
			out = append(out, id)
		}
	}
	return out, nil
}

func (m *fakeNotesMeta) CelebsAmong(_ context.Context, ids []uuid.UUID) ([]uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []uuid.UUID
	for _, id := range ids {
		if m.celebs[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

// notesGraph is a graph-service stub: followers / following lists (offset
// paged), the follower count, the viewer's blocked-and-muted set and the
// relationship batch.
type notesGraph struct {
	followers     map[uuid.UUID][]uuid.UUID
	following     map[uuid.UUID][]uuid.UUID
	counts        map[uuid.UUID]int // absent = count unavailable
	blocked       []uuid.UUID
	blockedStatus int
	follows       map[uuid.UUID]bool
	relStatus     int
}

func (g *notesGraph) server(t *testing.T) *httptest.Server {
	t.Helper()
	page := func(w http.ResponseWriter, r *http.Request, ids []uuid.UUID) {
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit == 0 {
			limit = 100
		}
		out := []string{}
		for i := offset; i < len(ids) && i < offset+limit; i++ {
			out = append(out, ids[i].String())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": out})
	}
	idFrom := func(path string) uuid.UUID {
		id, _ := uuid.Parse(path[strings.LastIndex(path, "/")+1:])
		return id
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/graph/followers/"):
			page(w, r, g.followers[idFrom(r.URL.Path)])
		case strings.HasPrefix(r.URL.Path, "/v1/graph/following/"):
			page(w, r, g.following[idFrom(r.URL.Path)])
		case strings.HasPrefix(r.URL.Path, "/v1/graph/counts/"):
			n, ok := g.counts[idFrom(r.URL.Path)]
			if !ok {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"follower_count": n}})
		case r.URL.Path == "/v1/internal/graph/blocked-and-muted":
			if g.blockedStatus != 0 {
				w.WriteHeader(g.blockedStatus)
				return
			}
			ids := []string{}
			for _, id := range g.blocked {
				ids = append(ids, id.String())
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"user_ids": ids})
		case r.URL.Path == "/v1/graph/relationships/batch":
			if g.relStatus != 0 {
				w.WriteHeader(g.relStatus)
				return
			}
			raw, _ := io.ReadAll(r.Body)
			var req struct {
				TargetIDs []string `json:"target_ids"`
			}
			_ = json.Unmarshal(raw, &req)
			out := map[string]map[string]bool{}
			for _, id := range req.TargetIDs {
				u, _ := uuid.Parse(id)
				out[id] = map[string]bool{"follows": g.follows[u], "is_connection": false}
			}
			_ = json.NewEncoder(w).Encode(out)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func notesService(t *testing.T, g *notesGraph, celeb bool) (*Service, *fakeNotesTimelines, *fakeNotesMeta, *recordingTimelines) {
	t.Helper()
	srv := g.server(t)
	tl, meta, home := newFakeNotesTimelines(), newFakeNotesMeta(), newRecordingTimelines()
	s := &Service{
		graphURL:       srv.URL,
		graphClient:    &http.Client{Timeout: 2 * time.Second},
		timelines:      home,
		celebs:         fixedCelebs(celeb),
		notesTimelines: tl,
		notesMeta:      meta,
		echo:           echoConfig{maxFollowers: defaultEchoMaxFollowers},
	}
	return s, tl, meta, home
}

func newIDs(n int) []uuid.UUID {
	out := make([]uuid.UUID, n)
	for i := range out {
		out[i] = uuid.New()
	}
	return out
}

// ── fan-out ─────────────────────────────────────────────────────────────────

func TestFanoutNote_TopLevelReachesAuthorAndFollowersOnly(t *testing.T) {
	author := uuid.New()
	followers := newIDs(3)
	s, tl, meta, home := notesService(t, &notesGraph{followers: map[uuid.UUID][]uuid.UUID{author: followers}}, false)
	post := uuid.New()

	if err := s.FanoutNote(context.Background(), NoteCreated{PostID: post, AuthorID: author, CreatedAt: time.Now(), Visibility: "public"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range append([]uuid.UUID{author}, followers...) {
		if got := tl.count(id); got != 1 {
			t.Errorf("%s has %d notes rows, want 1", id, got)
		}
	}
	if len(home.home) != 0 {
		t.Fatalf("a note wrote %d home-timeline rows; notes never enter Home", len(home.home))
	}
	if n, ok := meta.notes[post]; !ok || !n.TopLevel {
		t.Fatalf("note not recorded as top-level: %+v ok=%v", n, ok)
	}
}

func TestFanoutNote_ReplyGoesToNoTimeline(t *testing.T) {
	author := uuid.New()
	followers := newIDs(2)
	s, tl, meta, _ := notesService(t, &notesGraph{followers: map[uuid.UUID][]uuid.UUID{author: followers}}, false)
	post := uuid.New()

	if err := s.FanoutNote(context.Background(), NoteCreated{PostID: post, AuthorID: author, CreatedAt: time.Now(), ReplyToID: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	for _, id := range append([]uuid.UUID{author}, followers...) {
		if got := tl.count(id); got != 0 {
			t.Fatalf("a reply reached %s's notes timeline (%d rows); replies never go to timelines", id, got)
		}
	}
	if n, ok := meta.notes[post]; !ok || n.TopLevel {
		t.Fatalf("reply must be recorded as not top-level, got %+v ok=%v", n, ok)
	}
}

func TestFanoutNote_LargeAccountByFollowerCountIsPulledNotPushed(t *testing.T) {
	author := uuid.New()
	followers := newIDs(3)
	g := &notesGraph{followers: map[uuid.UUID][]uuid.UUID{author: followers}, counts: map[uuid.UUID]int{author: 101}}
	s, tl, meta, _ := notesService(t, g, false)
	s.echo.maxFollowers = 100

	if err := s.FanoutNote(context.Background(), NoteCreated{PostID: uuid.New(), AuthorID: author, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for _, f := range followers {
		if tl.count(f) != 0 {
			t.Fatal("an author over ECHO_FANOUT_MAX_FOLLOWERS was pushed to followers")
		}
	}
	if tl.count(author) != 1 {
		t.Fatal("the author's own row must still be written")
	}
	if _, ok := meta.pull[author]; !ok {
		t.Fatal("the author was not flagged for pull, so followers would never see the note")
	}
}

func TestFanoutNote_AtThresholdIsStillPushed(t *testing.T) {
	author := uuid.New()
	followers := newIDs(2)
	g := &notesGraph{followers: map[uuid.UUID][]uuid.UUID{author: followers}, counts: map[uuid.UUID]int{author: 100}}
	s, tl, meta, _ := notesService(t, g, false)
	s.echo.maxFollowers = 100

	if err := s.FanoutNote(context.Background(), NoteCreated{PostID: uuid.New(), AuthorID: author, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if tl.count(followers[0]) != 1 || len(meta.pull) != 0 {
		t.Fatal("exactly ECHO_FANOUT_MAX_FOLLOWERS followers is not 'more than' it: push, do not pull")
	}
}

// The count route is down: the follower leg's running count is the backstop.
func TestFanoutNote_LargeAccountByRunningCountStopsPushing(t *testing.T) {
	author := uuid.New()
	followers := newIDs(250)
	s, tl, meta, _ := notesService(t, &notesGraph{followers: map[uuid.UUID][]uuid.UUID{author: followers}}, false)
	s.echo.maxFollowers = 150

	if err := s.FanoutNote(context.Background(), NoteCreated{PostID: uuid.New(), AuthorID: author, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if tl.count(followers[199]) != 0 || tl.count(followers[249]) != 0 {
		t.Fatal("followers past the threshold were pushed")
	}
	if _, ok := meta.pull[author]; !ok {
		t.Fatal("author not flagged for pull after the running count passed the threshold")
	}
}

func TestFanoutNote_CelebIsPulledNotPushed(t *testing.T) {
	author := uuid.New()
	followers := newIDs(2)
	s, tl, _, _ := notesService(t, &notesGraph{followers: map[uuid.UUID][]uuid.UUID{author: followers}}, true)

	if err := s.FanoutNote(context.Background(), NoteCreated{PostID: uuid.New(), AuthorID: author, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if tl.count(followers[0]) != 0 {
		t.Fatal("a celeb's note was pushed to followers")
	}
}

func TestFanoutNote_FailedWriteIsReturned(t *testing.T) {
	author := uuid.New()
	s, tl, _, _ := notesService(t, &notesGraph{followers: map[uuid.UUID][]uuid.UUID{author: newIDs(2)}}, false)
	tl.writeErr = errors.New("scylla down")
	if err := s.FanoutNote(context.Background(), NoteCreated{PostID: uuid.New(), AuthorID: author, CreatedAt: time.Now()}); err == nil {
		t.Fatal("a failed notes write was swallowed; the event would be committed and the note lost")
	}
}

// A note that may not be listed (private, unlisted) stays the author's own.
func TestFanoutNote_UnlistableNoteStaysTheAuthors(t *testing.T) {
	author := uuid.New()
	followers := newIDs(2)
	s, tl, _, _ := notesService(t, &notesGraph{followers: map[uuid.UUID][]uuid.UUID{author: followers}}, false)
	if err := s.FanoutNote(context.Background(), NoteCreated{PostID: uuid.New(), AuthorID: author, CreatedAt: time.Now(), Visibility: "private"}); err != nil {
		t.Fatal(err)
	}
	if tl.count(author) != 1 || tl.count(followers[0]) != 0 {
		t.Fatal("a private note reached a follower's timeline")
	}
}

// Pulled notes page with the same exact cursor as pushed ones.
func TestNotesFollowing_PulledNotesPageWithoutRepeats(t *testing.T) {
	viewer, big := uuid.New(), uuid.New()
	s, _, meta, _ := notesService(t, &notesGraph{following: map[uuid.UUID][]uuid.UUID{viewer: {big}}}, true)
	meta.celebs[big] = true
	now := time.Now()
	for i := 0; i < 15; i++ {
		id := uuid.New()
		meta.notes[id] = postgres.NoteRow{PostID: id, AuthorID: big, CreatedAt: now.Add(-time.Duration(i) * time.Minute), TopLevel: true, Visibility: "public"}
	}
	seen := map[uuid.UUID]bool{}
	cursor := ""
	for page := 0; page < 5; page++ {
		items, next, err := s.GetNotesFollowingPage(context.Background(), viewer, 10, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			if seen[it.PostID] {
				t.Fatalf("pulled note %s served twice", it.PostID)
			}
			seen[it.PostID] = true
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if len(seen) != 15 {
		t.Fatalf("served %d pulled notes, want 15", len(seen))
	}
}

// Restore and visibility re-fan-out reach FanoutPost without reply_to_id.
func TestFanoutPost_NoteRoutesByRecordedKind(t *testing.T) {
	author := uuid.New()
	followers := newIDs(2)
	s, tl, meta, home := notesService(t, &notesGraph{followers: map[uuid.UUID][]uuid.UUID{author: followers}}, false)
	top, reply, unknown := uuid.New(), uuid.New(), uuid.New()
	now := time.Now()
	meta.notes[top] = postgres.NoteRow{PostID: top, AuthorID: author, CreatedAt: now, TopLevel: true, Visibility: "public"}
	meta.notes[reply] = postgres.NoteRow{PostID: reply, AuthorID: author, CreatedAt: now, TopLevel: false, Visibility: "public"}

	for _, id := range []uuid.UUID{top, reply, unknown} {
		if err := s.FanoutPost(context.Background(), id, author, now, NoteContentType, "public", uuid.Nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(home.home) != 0 {
		t.Fatal("a note re-fan-out wrote Home rows")
	}
	got, _ := tl.GetNotesTimelineBefore(context.Background(), followers[0], "", 10)
	if len(got) != 1 || got[0].PostID != top {
		t.Fatalf("follower holds %v; want only the top-level note (a reply or unrecorded note must not be fanned out)", got)
	}
}

// ── Home exclusion ──────────────────────────────────────────────────────────

func TestDropNotes_RemovesNotesOnly(t *testing.T) {
	in := []FeedItem{shortFormItem("post"), shortFormItem(NoteContentType), shortFormItem("repost"), shortFormItem(NoteContentType)}
	got := dropNotes(in)
	if len(got) != 2 || got[0].PostID != in[0].PostID || got[1].PostID != in[2].PostID {
		t.Fatalf("dropNotes kept %v", got)
	}
}

// GET /v1/feed/home is GetHomeFeed (and the pill is deltaHome): both must
// drop notes, at the seam where timeline, celeb-pull and cold-start
// candidates are already merged — before the main_feed policy filter, so a
// degraded policy lookup cannot let a note through. Asserted at the source
// level for the reason shortform_mainfeed_test.go gives (GetHomeFeed reads a
// concrete Scylla store).
func TestHomeSurfacesDropNotesBeforeServing(t *testing.T) {
	for _, tc := range []struct{ file, fn string }{{"feed.go", "GetHomeFeed"}, {"delta.go", "deltaHome"}} {
		fn := funcDeclNamed(t, tc.file, tc.fn)
		drop := firstCallPos(fn, "dropNotes")
		excl := firstCallPos(fn, "filterMainFeedExcluded")
		if !drop.IsValid() {
			t.Fatalf("%s never calls dropNotes: an Echo note could appear on Home", tc.fn)
		}
		if drop > excl {
			t.Fatalf("%s drops notes after filterMainFeedExcluded; it must run at the merged seam before it", tc.fn)
		}
	}
	if !firstCallPos(funcDeclNamed(t, "discovery.go", "discoveryFor"), "dropNotes").IsValid() {
		t.Fatal("Home's out-of-network discovery fill does not drop notes")
	}
}

// ── Following ───────────────────────────────────────────────────────────────

func seedNotes(tl *fakeNotesTimelines, owner, author uuid.UUID, n int, newest time.Time) []uuid.UUID {
	out := make([]uuid.UUID, n)
	for i := 0; i < n; i++ {
		out[i] = uuid.New()
		_ = tl.AddToNotesTimeline(context.Background(), owner, out[i], author, newest.Add(-time.Duration(i)*time.Minute))
	}
	return out
}

func TestNotesFollowing_NewestFirstWithAnExactCursor(t *testing.T) {
	viewer, author := uuid.New(), uuid.New()
	s, tl, _, _ := notesService(t, &notesGraph{}, false)
	want := seedNotes(tl, viewer, author, 25, time.Now()) // want[0] is newest

	var got []uuid.UUID
	cursor := ""
	for page := 0; page < 5; page++ {
		items, next, err := s.GetNotesFollowingPage(context.Background(), viewer, 10, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			got = append(got, it.PostID)
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if len(got) != len(want) {
		t.Fatalf("paged %d notes, want %d (a cursor repeated or skipped rows)", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("position %d: got %s want %s — not newest first", i, got[i], want[i])
		}
	}
}

func TestNotesFollowing_BlockedAndMutedAuthorsAreDropped(t *testing.T) {
	viewer, ok, blocked, muted := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	// graph-service's blocked-and-muted is the union of both.
	s, tl, _, _ := notesService(t, &notesGraph{blocked: []uuid.UUID{blocked, muted}}, false)
	now := time.Now()
	seedNotes(tl, viewer, ok, 2, now)
	seedNotes(tl, viewer, blocked, 2, now.Add(-time.Hour))
	seedNotes(tl, viewer, muted, 2, now.Add(-2*time.Hour))

	items, _, err := s.GetNotesFollowingPage(context.Background(), viewer, 20, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	for _, it := range items {
		if it.AuthorID != ok {
			t.Fatalf("a blocked or muted author's note was served: %s", it.AuthorID)
		}
	}
}

func TestNotesFollowing_FailsClosedWhenBlocksAreUnknown(t *testing.T) {
	viewer := uuid.New()
	s, tl, _, _ := notesService(t, &notesGraph{blockedStatus: http.StatusBadGateway}, false)
	seedNotes(tl, viewer, uuid.New(), 2, time.Now())
	if _, _, err := s.GetNotesFollowingPage(context.Background(), viewer, 20, ""); err == nil {
		t.Fatal("Following was served with the block set unresolved")
	}
}

func TestNotesFollowing_PullsLargeAuthorsInOrder(t *testing.T) {
	viewer, small, big := uuid.New(), uuid.New(), uuid.New()
	g := &notesGraph{following: map[uuid.UUID][]uuid.UUID{viewer: {small, big}}}
	s, tl, meta, _ := notesService(t, g, false)
	now := time.Now()
	pushed := seedNotes(tl, viewer, small, 2, now) // now, now-1m
	pulled := uuid.New()
	meta.pull[big] = 500000
	meta.notes[pulled] = postgres.NoteRow{PostID: pulled, AuthorID: big, CreatedAt: now.Add(-30 * time.Second), TopLevel: true, Visibility: "public"}

	items, _, err := s.GetNotesFollowingPage(context.Background(), viewer, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []uuid.UUID{pushed[0], pulled, pushed[1]}
	if len(items) != len(want) {
		t.Fatalf("got %d items, want %d (pulled author missing?)", len(items), len(want))
	}
	for i := range want {
		if items[i].PostID != want[i] {
			t.Fatalf("position %d: got %s want %s", i, items[i].PostID, want[i])
		}
	}
}

// ── For You ─────────────────────────────────────────────────────────────────

func TestNotesForYou_ThreeSourcesFilteredAndPaged(t *testing.T) {
	viewer, friend, stranger, blocked := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	g := &notesGraph{following: map[uuid.UUID][]uuid.UUID{viewer: {friend}}, blocked: []uuid.UUID{blocked}}
	s, tl, meta, _ := notesService(t, g, false)
	now := time.Now()
	rec := func(author uuid.UUID, age time.Duration) uuid.UUID {
		id := uuid.New()
		meta.notes[id] = postgres.NoteRow{PostID: id, AuthorID: author, CreatedAt: now.Add(-age), TopLevel: true, Visibility: "public"}
		return id
	}
	following := seedNotes(tl, viewer, friend, 1, now)[0]
	meta.notes[following] = postgres.NoteRow{PostID: following, AuthorID: friend, CreatedAt: now, TopLevel: true, Visibility: "public"}
	liked := rec(stranger, 72*time.Hour) // old: only the network source can bring it
	tl.liked[friend] = []uuid.UUID{liked}
	reposted := rec(stranger, 60*time.Hour)
	tl.reposts[viewer] = []scylla.FeedItem{{PostID: reposted, AuthorID: friend, ContentType: "repost"}}
	rising := rec(stranger, time.Hour)
	own := rec(viewer, 2*time.Hour)
	fromBlocked := rec(blocked, 3*time.Hour)

	var got []uuid.UUID
	cursor := ForYouCursor{}
	for page := 0; page < 5; page++ {
		items, next, err := s.GetNotesForYouPage(context.Background(), viewer, 2, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			got = append(got, it.PostID)
		}
		if next == nil {
			break
		}
		cursor = *next
	}
	set := map[uuid.UUID]int{}
	for _, id := range got {
		set[id]++
	}
	for name, id := range map[string]uuid.UUID{"following": following, "liked by a follow": liked, "reposted by a follow": reposted, "rising": rising} {
		if set[id] != 1 {
			t.Errorf("%s note served %d times, want once", name, set[id])
		}
	}
	if set[own] != 0 {
		t.Error("For You served the viewer's own note")
	}
	if set[fromBlocked] != 0 {
		t.Error("For You served a blocked author's note")
	}
}

func TestNotesForYou_FailsClosedWhenBlocksAreUnknown(t *testing.T) {
	s, _, _, _ := notesService(t, &notesGraph{blockedStatus: http.StatusInternalServerError}, false)
	if _, _, err := s.GetNotesForYouPage(context.Background(), uuid.New(), 10, ForYouCursor{}); err == nil {
		t.Fatal("For You was served with the block set unresolved")
	}
}

// ── unfollow and follow ─────────────────────────────────────────────────────

func TestPurgeNotesAfterUnfollow(t *testing.T) {
	follower, followee := uuid.New(), uuid.New()

	t.Run("no longer follows: purged", func(t *testing.T) {
		s, tl, _, _ := notesService(t, &notesGraph{}, false)
		seedNotes(tl, follower, followee, 3, time.Now())
		n, err := s.PurgeNotesAfterUnfollow(context.Background(), follower, followee)
		if err != nil || n != 3 || tl.count(follower) != 0 {
			t.Fatalf("purged %d (err %v), %d left; want all 3 gone", n, err, tl.count(follower))
		}
	})
	t.Run("follows again: kept", func(t *testing.T) {
		s, tl, _, _ := notesService(t, &notesGraph{follows: map[uuid.UUID]bool{followee: true}}, false)
		seedNotes(tl, follower, followee, 3, time.Now())
		if _, err := s.PurgeNotesAfterUnfollow(context.Background(), follower, followee); err != nil {
			t.Fatal(err)
		}
		if tl.count(follower) != 3 {
			t.Fatal("a live follow's notes were purged")
		}
	})
	t.Run("relationship unknown: nothing purged, error", func(t *testing.T) {
		s, tl, _, _ := notesService(t, &notesGraph{relStatus: http.StatusBadGateway}, false)
		seedNotes(tl, follower, followee, 3, time.Now())
		if _, err := s.PurgeNotesAfterUnfollow(context.Background(), follower, followee); err == nil {
			t.Fatal("an unestablished relationship must be an error (retried)")
		}
		if tl.count(follower) != 3 {
			t.Fatal("rows purged although the relationship was not established")
		}
	})
}

func TestBackfillNotesOnFollow(t *testing.T) {
	follower, followee := uuid.New(), uuid.New()
	s, tl, meta, _ := notesService(t, &notesGraph{}, false)
	now := time.Now()
	live, reply, gone := uuid.New(), uuid.New(), uuid.New()
	meta.notes[live] = postgres.NoteRow{PostID: live, AuthorID: followee, CreatedAt: now, TopLevel: true, Visibility: "public"}
	meta.notes[reply] = postgres.NoteRow{PostID: reply, AuthorID: followee, CreatedAt: now, TopLevel: false, Visibility: "public"}
	meta.notes[gone] = postgres.NoteRow{PostID: gone, AuthorID: followee, CreatedAt: now, TopLevel: true, Visibility: "public", Deleted: true}

	if _, err := s.BackfillNotesOnFollow(context.Background(), follower, followee); err != nil {
		t.Fatal(err)
	}
	got, _ := tl.GetNotesTimelineBefore(context.Background(), follower, "", 10)
	if len(got) != 1 || got[0].PostID != live {
		t.Fatalf("backfill wrote %v, want only the live top-level note", got)
	}
}

// ── switches ────────────────────────────────────────────────────────────────

func TestParseEchoConfigDefaults(t *testing.T) {
	cases := []struct {
		enabled, max string
		want         echoConfig
	}{
		{"", "", echoConfig{false, 100000}},
		{"false", "abc", echoConfig{false, 100000}},
		{"true", "5000", echoConfig{true, 5000}},
		{"1", "0", echoConfig{true, 100000}},
		{"yes", "-3", echoConfig{false, 100000}},
	}
	for _, c := range cases {
		if got := parseEchoConfig(c.enabled, c.max); got != c.want {
			t.Errorf("parseEchoConfig(%q, %q) = %+v, want %+v", c.enabled, c.max, got, c.want)
		}
	}
}

// ── hydration pass-through ──────────────────────────────────────────────────

func TestHydratedPost_PassesNoteFieldsThrough(t *testing.T) {
	in := `{"id":"` + uuid.NewString() + `","author_id":"` + uuid.NewString() + `","content_type":"note","text":"hi",
	  "created_at":"2026-11-12T00:00:00Z","edited_at":null,"reply_to_id":null,"conversation_id":"c1","quote_of_id":null,
	  "reply_policy":"everyone","reply_count":0,"like_count":3,"viewer_has_liked":false,"viewer_has_reposted":true,
	  "viewer_can_reply":true,"quoted_note":null,
	  "author":{"id":"` + uuid.NewString() + `","display_name":"A","is_verified":true,"verification_level":"person","badge_flags":1}}`
	var hp HydratedPost
	if err := json.Unmarshal([]byte(in), &hp); err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(hp)
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	for _, k := range []string{"edited_at", "reply_to_id", "conversation_id", "quote_of_id", "reply_policy", "reply_count",
		"like_count", "viewer_has_liked", "viewer_has_reposted", "viewer_can_reply", "quoted_note"} {
		if _, ok := got[k]; !ok {
			t.Errorf("Note field %q was dropped between post-service and the feed", k)
		}
	}
	if got["like_count"] != float64(3) || got["viewer_has_reposted"] != true || got["edited_at"] != nil {
		t.Errorf("values changed in transit: %s", out)
	}
	author, _ := got["author"].(map[string]any)
	if author["is_verified"] != true || author["verification_level"] != "person" {
		t.Errorf("author verification marks dropped: %v", author)
	}

	var plain HydratedPost
	_ = json.Unmarshal([]byte(`{"id":"`+uuid.NewString()+`","content_type":"post"}`), &plain)
	raw, _ := json.Marshal(plain)
	for _, k := range []string{"reply_to_id", "quoted_note", "viewer_can_reply", "is_verified"} {
		if strings.Contains(string(raw), fmt.Sprintf("%q", k)) {
			t.Errorf("a non-note post gained key %q", k)
		}
	}
}
