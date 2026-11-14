package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeNotesUpstream is post-service's /internal/notes/batch plus the two
// lookups the hydration tail makes (graph-service view_posts, trust-safety
// keyword filters). Notes it was not given are absent from its answer.
type fakeNotesUpstream struct {
	t        *testing.T
	viewer   uuid.UUID
	notes    map[uuid.UUID]map[string]any // the Note each id answers
	reverse  bool                         // answer in reverse request order
	keywords []string
	status   int

	mu      sync.Mutex
	calls   [][]string // ids asked for, per call
	viewers []string   // X-User-Id per call
	keys    []string   // X-Internal-Service-Key per call
}

func (f *fakeNotesUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/internal/notes/batch":
		if r.Method != http.MethodGet {
			f.t.Errorf("notes batch method = %s", r.Method)
		}
		ids := strings.Split(r.URL.Query().Get("ids"), ",")
		f.mu.Lock()
		f.calls = append(f.calls, ids)
		f.viewers = append(f.viewers, r.Header.Get("X-User-Id"))
		f.keys = append(f.keys, r.Header.Get("X-Internal-Service-Key"))
		f.mu.Unlock()
		if f.status != 0 {
			w.WriteHeader(f.status)
			return
		}
		if len(ids) > notesBatchMaxContract {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		items := []any{}
		for _, raw := range ids {
			id, err := uuid.Parse(raw)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			n, ok := f.notes[id]
			if !ok {
				continue
			}
			// The viewer-specific fields are true only for the viewer.
			copyN := map[string]any{}
			for k, v := range n {
				copyN[k] = v
			}
			mine := r.Header.Get("X-User-Id") == f.viewer.String()
			copyN["viewer_has_bookmarked"] = mine
			copyN["viewer_has_liked"] = mine
			items = append(items, copyN)
		}
		if f.reverse {
			for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
				items[i], items[j] = items[j], items[i]
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"items": items}})
	case "/v1/internal/graph/can":
		var body struct {
			TargetIDs []string `json:"target_ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		out := map[string]bool{}
		for _, id := range body.TargetIDs {
			out[id] = true
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": out})
	case "/v1/internal/keyword-filters":
		kws := f.keywords
		if kws == nil {
			kws = []string{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"keywords": kws}})
	default:
		f.t.Errorf("unexpected upstream call %s", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

// notesBatchMaxContract is post-service's documented limit, independent of
// the constant under test.
const notesBatchMaxContract = 50

func newNotesHydrationService(t *testing.T, f *fakeNotesUpstream) *Service {
	t.Helper()
	t.Setenv("INTERNAL_SERVICE_KEY", "test-internal")
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return &Service{
		postServiceURL: srv.URL, graphURL: srv.URL, trustSafetyURL: srv.URL,
		postClient: srv.Client(), graphClient: srv.Client(), trustClient: srv.Client(),
	}
}

func testNote(id, author uuid.UUID, text string) map[string]any {
	return map[string]any{
		"id": id.String(), "author_id": author.String(), "text": text,
		"visibility": "public", "content_type": "note", "status": "published",
		"scheduled_at": nil, "created_at": "2026-11-12T09:35:00Z",
		"view_count": 3, "bookmark_count": 1, "quote_count": 0,
	}
}

func noteIDsOf(t *testing.T, raws []json.RawMessage) []uuid.UUID {
	t.Helper()
	out := make([]uuid.UUID, 0, len(raws))
	for _, r := range raws {
		var n struct {
			ID uuid.UUID `json:"id"`
		}
		if err := json.Unmarshal(r, &n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n.ID)
	}
	return out
}

func noteItems(ids []uuid.UUID, author uuid.UUID) []FeedItem {
	items := make([]FeedItem, 0, len(ids))
	for _, id := range ids {
		items = append(items, FeedItem{PostID: id, AuthorID: author, ContentType: NoteContentType, CreatedAt: time.Now()})
	}
	return items
}

func seedBatchNotes(n int, author uuid.UUID) ([]uuid.UUID, map[uuid.UUID]map[string]any) {
	ids := make([]uuid.UUID, n)
	notes := make(map[uuid.UUID]map[string]any, n)
	for i := range ids {
		ids[i] = uuid.New()
		notes[ids[i]] = testNote(ids[i], author, "note")
	}
	return ids, notes
}

func TestHydrateNotes_ViewerIDForwardedOnEveryCall(t *testing.T) {
	viewer, author := uuid.New(), uuid.New()
	ids, notes := seedBatchNotes(60, author)
	f := &fakeNotesUpstream{t: t, viewer: viewer, notes: notes}
	svc := newNotesHydrationService(t, f)

	got, err := svc.HydrateNotes(context.Background(), noteItems(ids, author), viewer)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.viewers) != 2 {
		t.Fatalf("calls = %d, want 2", len(f.viewers))
	}
	for i, v := range f.viewers {
		if v != viewer.String() {
			t.Errorf("call %d X-User-Id = %q, want the viewer %s", i, v, viewer)
		}
		if f.keys[i] != "test-internal" {
			t.Errorf("call %d internal key missing", i)
		}
	}
	for _, raw := range got {
		var n struct {
			Bookmarked bool `json:"viewer_has_bookmarked"`
			Liked      bool `json:"viewer_has_liked"`
		}
		_ = json.Unmarshal(raw, &n)
		if !n.Bookmarked || !n.Liked {
			t.Fatalf("viewer-specific fields are not the viewer's: %s", raw)
		}
	}
}

func TestHydrateNotes_KeepsCandidateOrder(t *testing.T) {
	viewer, author := uuid.New(), uuid.New()
	ids, notes := seedBatchNotes(7, author)
	f := &fakeNotesUpstream{t: t, viewer: viewer, notes: notes, reverse: true}
	svc := newNotesHydrationService(t, f)

	got, err := svc.HydrateNotes(context.Background(), noteItems(ids, author), viewer)
	if err != nil {
		t.Fatal(err)
	}
	gotIDs := noteIDsOf(t, got)
	if len(gotIDs) != len(ids) {
		t.Fatalf("got %d notes, want %d", len(gotIDs), len(ids))
	}
	for i := range ids {
		if gotIDs[i] != ids[i] {
			t.Fatalf("position %d = %s, want %s (candidate order)", i, gotIDs[i], ids[i])
		}
	}
}

func TestHydrateNotes_ChunksAtFiftyInOrder(t *testing.T) {
	viewer, author := uuid.New(), uuid.New()
	ids, notes := seedBatchNotes(120, author)
	f := &fakeNotesUpstream{t: t, viewer: viewer, notes: notes}
	svc := newNotesHydrationService(t, f)

	got, err := svc.HydrateNotes(context.Background(), noteItems(ids, author), viewer)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 3 || len(f.calls[0]) != 50 || len(f.calls[1]) != 50 || len(f.calls[2]) != 20 {
		sizes := []int{}
		for _, c := range f.calls {
			sizes = append(sizes, len(c))
		}
		t.Fatalf("call sizes = %v, want [50 50 20]", sizes)
	}
	var asked []string
	for _, c := range f.calls {
		asked = append(asked, c...)
	}
	for i, id := range ids {
		if asked[i] != id.String() {
			t.Fatalf("id %d asked out of order", i)
		}
	}
	gotIDs := noteIDsOf(t, got)
	if len(gotIDs) != 120 || gotIDs[0] != ids[0] || gotIDs[50] != ids[50] || gotIDs[119] != ids[119] {
		t.Fatalf("got %d notes, order broken across chunks", len(gotIDs))
	}
}

func TestHydrateNotes_MissingNoteIsDropped(t *testing.T) {
	viewer, author := uuid.New(), uuid.New()
	ids, notes := seedBatchNotes(3, author)
	delete(notes, ids[1]) // hidden or deleted: post-service does not answer it
	f := &fakeNotesUpstream{t: t, viewer: viewer, notes: notes}
	svc := newNotesHydrationService(t, f)

	got, err := svc.HydrateNotes(context.Background(), noteItems(ids, author), viewer)
	if err != nil {
		t.Fatal(err)
	}
	gotIDs := noteIDsOf(t, got)
	if len(gotIDs) != 2 || gotIDs[0] != ids[0] || gotIDs[1] != ids[2] {
		t.Fatalf("got %v, want [%s %s]", gotIDs, ids[0], ids[2])
	}
	for _, raw := range got {
		if len(raw) == 0 || string(raw) == "null" {
			t.Fatalf("a missing note left an empty row")
		}
	}
}

func TestHydrateNotes_ScheduledIsNeverShown(t *testing.T) {
	viewer := uuid.New()
	ids, notes := seedBatchNotes(3, viewer) // the viewer's own notes: still never on a feed
	notes[ids[0]]["status"] = "scheduled"
	notes[ids[0]]["scheduled_at"] = "2026-12-01T09:00:00Z"
	notes[ids[1]]["scheduled_at"] = "2026-12-01T09:00:00Z"
	f := &fakeNotesUpstream{t: t, viewer: viewer, notes: notes}
	svc := newNotesHydrationService(t, f)

	got, err := svc.HydrateNotes(context.Background(), noteItems(ids, viewer), viewer)
	if err != nil {
		t.Fatal(err)
	}
	if gotIDs := noteIDsOf(t, got); len(gotIDs) != 1 || gotIDs[0] != ids[2] {
		t.Fatalf("got %v, want only the published note %s", gotIDs, ids[2])
	}
	for _, n := range []noteRow{
		{status: "scheduled"},
		{status: "failed"},
		{status: "published", scheduledAt: json.RawMessage(`"2026-12-01T09:00:00Z"`)},
		{post: HydratedPost{IsScheduled: true}},
	} {
		if !n.scheduled() {
			t.Errorf("%+v read as published", n)
		}
	}
	if (noteRow{status: "published", scheduledAt: json.RawMessage(`null`)}).scheduled() {
		t.Error("a published note read as scheduled")
	}
}

func TestHydrateNotes_KeepsNotesOnlyAndKeywordFilter(t *testing.T) {
	viewer, author := uuid.New(), uuid.New()
	ids, notes := seedBatchNotes(3, author)
	notes[ids[0]]["content_type"] = "post"
	notes[ids[1]]["text"] = "spoilers ahead"
	f := &fakeNotesUpstream{t: t, viewer: viewer, notes: notes, keywords: []string{"spoilers"}}
	svc := newNotesHydrationService(t, f)

	got, err := svc.HydrateNotes(context.Background(), noteItems(ids, author), viewer)
	if err != nil {
		t.Fatal(err)
	}
	if gotIDs := noteIDsOf(t, got); len(gotIDs) != 1 || gotIDs[0] != ids[2] {
		t.Fatalf("got %v, want only %s", gotIDs, ids[2])
	}
}

func TestHydrateNotes_UpstreamFailureIsAnError(t *testing.T) {
	viewer, author := uuid.New(), uuid.New()
	ids, notes := seedBatchNotes(2, author)
	f := &fakeNotesUpstream{t: t, viewer: viewer, notes: notes, status: http.StatusInternalServerError}
	svc := newNotesHydrationService(t, f)
	if got, err := svc.HydrateNotes(context.Background(), noteItems(ids, author), viewer); err == nil {
		t.Fatalf("got %d notes and no error from a failed batch", len(got))
	}
}

// The Note on a feed is post-service's Note JSON, byte for byte up to
// whitespace: the golden fixture (copied from post-service's
// testdata/contracts/notes) comes out unchanged.
func TestHydrateNotes_PassesPostServiceNoteFixtureThrough(t *testing.T) {
	note := readNoteV2Fixture(t)
	var fields map[string]any
	if err := json.Unmarshal(note, &fields); err != nil {
		t.Fatal(err)
	}
	id := uuid.MustParse(fields["id"].(string))
	author := uuid.MustParse(fields["author_id"].(string))
	viewer := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal/notes/batch":
			_, _ = w.Write([]byte(`{"data":{"items":[` + string(note) + `]}}`))
		case "/v1/internal/graph/can":
			_, _ = w.Write([]byte(`{"data":{"` + author.String() + `":true}}`))
		default:
			_, _ = w.Write([]byte(`{"data":{"keywords":[]}}`))
		}
	}))
	defer srv.Close()
	svc := &Service{postServiceURL: srv.URL, graphURL: srv.URL, trustSafetyURL: srv.URL,
		postClient: srv.Client(), graphClient: srv.Client(), trustClient: srv.Client()}

	got, err := svc.HydrateNotes(context.Background(), noteItems([]uuid.UUID{id}, author), viewer)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d notes, want 1", len(got))
	}
	assertSameJSON(t, note, got[0])
}

// readNoteV2Fixture returns the fixture's data object (the Note).
func readNoteV2Fixture(t *testing.T) json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "contracts", "notes", "note_v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || len(env.Data) == 0 {
		t.Fatalf("fixture: %v", err)
	}
	return env.Data
}

func assertSameJSON(t *testing.T, want, got []byte) {
	t.Helper()
	var w, g any
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatal(err)
	}
	wb, _ := json.Marshal(w)
	gb, _ := json.Marshal(g)
	if string(wb) != string(gb) {
		t.Fatalf("Note changed in the feed.\n--- want\n%s\n--- got\n%s", wb, gb)
	}
}
