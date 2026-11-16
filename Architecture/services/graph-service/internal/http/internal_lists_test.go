package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/atpost/graph-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// The internal keyset id lists over the REAL RegisterRoutes wiring, with the
// store faked by an in-memory list that pages the way the SQL does (newest
// first, id as the tie-break, a page is strictly after its cursor). The SQL
// has its own live test (store/id_pages_integration_test.go, CI).

type keysetRow struct {
	id uuid.UUID
	at time.Time
}

type fakeIDPages struct {
	mu    sync.Mutex
	rows  map[string][]keysetRow // "<kind>:<owner>"
	err   error
	calls []fakeIDPageCall
}

type fakeIDPageCall struct {
	kind  string
	limit int
	after *store.ListCursor
}

func newFakeIDPages() *fakeIDPages { return &fakeIDPages{rows: map[string][]keysetRow{}} }

func (f *fakeIDPages) add(kind string, owner, id uuid.UUID, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := kind + ":" + owner.String()
	f.rows[key] = append(f.rows[key], keysetRow{id, at})
}

func (f *fakeIDPages) remove(kind string, owner, id uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := kind + ":" + owner.String()
	kept := f.rows[key][:0]
	for _, r := range f.rows[key] {
		if r.id != id {
			kept = append(kept, r)
		}
	}
	f.rows[key] = kept
}

func keysetBefore(a, b keysetRow) bool { // a sorts before b: newer, then higher id
	if !a.at.Equal(b.at) {
		return a.at.After(b.at)
	}
	return bytes.Compare(a.id[:], b.id[:]) > 0
}

func (f *fakeIDPages) ListIDPage(_ context.Context, kind string, owner uuid.UUID, limit int, after *store.ListCursor) ([]uuid.UUID, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeIDPageCall{kind, limit, after})
	if f.err != nil {
		return nil, "", f.err
	}
	rows := append([]keysetRow(nil), f.rows[kind+":"+owner.String()]...)
	sort.Slice(rows, func(i, j int) bool { return keysetBefore(rows[i], rows[j]) })
	var ids []uuid.UUID
	next := ""
	for _, r := range rows {
		if after != nil && !keysetBefore(keysetRow{after.ID, after.CreatedAt}, r) {
			continue // at or before the cursor
		}
		if len(ids) == limit {
			last := ids[len(ids)-1]
			for _, l := range rows {
				if l.id == last {
					next = store.ListCursor{CreatedAt: l.at, ID: l.id}.String()
				}
			}
			break
		}
		ids = append(ids, r.id)
	}
	return ids, next, nil
}

func (f *fakeIDPages) lastCall(t *testing.T) fakeIDPageCall {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		t.Fatal("the list was never read")
	}
	return f.calls[len(f.calls)-1]
}

func idPagesRouter(fake *fakeIDPages) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(nil).WithCanonicalWriteSource(true, nil)
	h.idPages = fake
	h.RegisterRoutes(r)
	return r
}

func internalListPath(kind string, owner uuid.UUID, limit int, cursor string) string {
	q := url.Values{}
	if limit != 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	p := "/v1/internal/graph/" + kind + "/" + owner.String()
	if len(q) > 0 {
		p += "?" + q.Encode()
	}
	return p
}

type idPageEnvelope struct {
	Data *struct {
		IDs        *[]uuid.UUID `json:"ids"`
		NextCursor *string      `json:"next_cursor"`
	} `json:"data"`
	Error *struct {
		Code string `json:"code"`
	} `json:"error"`
}

func getIDPage(t *testing.T, r *gin.Engine, path string) (int, idPageEnvelope) {
	t.Helper()
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, path, nil))
	var env idPageEnvelope
	if err := json.Unmarshal(resp.Body.Bytes(), &env); err != nil {
		t.Fatalf("%s: body is not JSON: %v: %s", path, err, resp.Body.String())
	}
	return resp.Code, env
}

// walkInternalList follows next_cursor to the end over HTTP.
func walkInternalList(t *testing.T, r *gin.Engine, kind string, owner uuid.UUID, limit int, between func(page int)) []uuid.UUID {
	t.Helper()
	var all []uuid.UUID
	cursor := ""
	for page := 0; page < 1000; page++ {
		code, env := getIDPage(t, r, internalListPath(kind, owner, limit, cursor))
		if code != http.StatusOK || env.Data == nil || env.Data.IDs == nil || env.Data.NextCursor == nil {
			t.Fatalf("page %d: status %d, envelope %+v", page, code, env)
		}
		all = append(all, *env.Data.IDs...)
		if *env.Data.NextCursor == "" {
			return all
		}
		if len(*env.Data.IDs) != limit {
			t.Fatalf("page %d holds %d ids but names a next page", page, len(*env.Data.IDs))
		}
		if *env.Data.NextCursor == cursor {
			t.Fatalf("page %d returned the cursor it was asked with", page)
		}
		cursor = *env.Data.NextCursor
		if between != nil {
			between(page)
		}
	}
	t.Fatal("the walk never ended")
	return nil
}

var listBase = time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC)

// seedList adds n rows, oldest first, one second apart, and returns them
// newest first (the order the route must serve).
func seedList(f *fakeIDPages, kind string, owner uuid.UUID, n int) []uuid.UUID {
	out := make([]uuid.UUID, n)
	for i := 0; i < n; i++ {
		id := uuid.New()
		f.add(kind, owner, id, listBase.Add(time.Duration(i)*time.Second))
		out[n-1-i] = id
	}
	return out
}

func TestInternalIDList_WalksNewestFirstToAnEmptyCursor(t *testing.T) {
	for _, kind := range []string{store.ListFollowers, store.ListFollowing, store.ListConnections} {
		t.Run(kind, func(t *testing.T) {
			fake := newFakeIDPages()
			owner := uuid.New()
			want := seedList(fake, kind, owner, 250)
			// Another owner's rows and another kind's rows must not leak in.
			seedList(fake, kind, uuid.New(), 5)
			r := idPagesRouter(fake)

			got := walkInternalList(t, r, kind, owner, 100, nil)
			if len(got) != len(want) {
				t.Fatalf("walk returned %d ids, want %d", len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("position %d out of order", i)
				}
			}
			if call := fake.lastCall(t); call.kind != kind {
				t.Fatalf("route %s read the %s list", kind, call.kind)
			}
		})
	}
}

func TestInternalIDList_LastPageAndEmptyList(t *testing.T) {
	fake := newFakeIDPages()
	owner := uuid.New()
	seedList(fake, store.ListFollowers, owner, 200)
	r := idPagesRouter(fake)

	// Exactly two full pages: the second names no third.
	_, first := getIDPage(t, r, internalListPath(store.ListFollowers, owner, 100, ""))
	if *first.Data.NextCursor == "" {
		t.Fatal("the first of two full pages named no next page")
	}
	_, second := getIDPage(t, r, internalListPath(store.ListFollowers, owner, 100, *first.Data.NextCursor))
	if len(*second.Data.IDs) != 100 || *second.Data.NextCursor != "" {
		t.Fatalf("last page: %d ids, next %q; want 100 and no cursor", len(*second.Data.IDs), *second.Data.NextCursor)
	}

	// Nobody: 200, an empty array (never null), no cursor.
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, internalListPath(store.ListFollowers, uuid.New(), 0, ""), nil))
	if resp.Code != http.StatusOK || !bytes.Contains(resp.Body.Bytes(), []byte(`"ids":[]`)) || !bytes.Contains(resp.Body.Bytes(), []byte(`"next_cursor":""`)) {
		t.Fatalf("empty list: status %d, body %s", resp.Code, resp.Body.String())
	}
}

func TestInternalIDList_BadCursorIs400AndNeverReadsTheList(t *testing.T) {
	owner := uuid.New()
	for name, cursor := range map[string]string{
		"an offset":     "300",
		"garbage":       "not-a-cursor",
		"half a cursor": "1794821400123456:",
		"bad id":        "1794821400123456:zzzz",
	} {
		t.Run(name, func(t *testing.T) {
			fake := newFakeIDPages()
			seedList(fake, store.ListFollowers, owner, 5)
			r := idPagesRouter(fake)
			code, env := getIDPage(t, r, internalListPath(store.ListFollowers, owner, 100, cursor))
			if code != http.StatusBadRequest || env.Error == nil || env.Error.Code != "INVALID_CURSOR" {
				t.Fatalf("status %d, error %+v; want 400 INVALID_CURSOR", code, env.Error)
			}
			if env.Data != nil {
				t.Fatal("a refused cursor still returned a page (the head of the list, served as if it were the tail)")
			}
			if len(fake.calls) != 0 {
				t.Fatal("a refused cursor still read the list")
			}
		})
	}
}

func TestInternalIDList_Refusals(t *testing.T) {
	fake := newFakeIDPages()
	owner := uuid.New()
	seedList(fake, store.ListFollowers, owner, 3)
	r := idPagesRouter(fake)

	if code, env := getIDPage(t, r, "/v1/internal/graph/followers/not-a-uuid"); code != http.StatusBadRequest || env.Error == nil || env.Error.Code != "INVALID_ID" {
		t.Fatalf("bad id: status %d, error %+v", code, env.Error)
	}

	// Edge traffic (the gateway's stamp) is refused: no audience rule here.
	req := httptest.NewRequest(http.MethodGet, internalListPath(store.ListFollowers, owner, 0, ""), nil)
	req.Header.Set(GraphWriteSourceHeader, "api-gateway")
	req.Header.Set("X-User-Id", uuid.NewString())
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusForbidden || !bytes.Contains(resp.Body.Bytes(), []byte("INTERNAL_ONLY")) {
		t.Fatalf("edge request: status %d, body %s; want 403 INTERNAL_ONLY", resp.Code, resp.Body.String())
	}
	if len(fake.calls) != 0 {
		t.Fatal("an edge request read the list")
	}

	// A store failure is a 500, never an empty page a fan-out would take for
	// "no followers".
	fake.err = errors.New("postgres down")
	if code, env := getIDPage(t, r, internalListPath(store.ListFollowers, owner, 0, "")); code != http.StatusInternalServerError || env.Data != nil {
		t.Fatalf("store failure: status %d, data %+v", code, env.Data)
	}
}

func TestInternalIDList_LimitIsClamped(t *testing.T) {
	fake := newFakeIDPages()
	owner := uuid.New()
	r := idPagesRouter(fake)
	for raw, want := range map[string]int{"": 100, "0": 100, "-3": 100, "abc": 100, "37": 37, "500": 500, "100000": 500} {
		path := "/v1/internal/graph/following/" + owner.String()
		if raw != "" {
			path += "?limit=" + raw
		}
		if code, _ := getIDPage(t, r, path); code != http.StatusOK {
			t.Fatalf("limit=%q: status %d", raw, code)
		}
		if got := fake.lastCall(t).limit; got != want {
			t.Errorf("limit=%q reached the store as %d, want %d", raw, got, want)
		}
	}
}

// A follower who arrives while a walk is under way (the head of the list)
// and one who leaves must neither repeat nor skip anyone who was there
// throughout. Offset paging gets both wrong.
func TestInternalIDList_ChangesMidWalkNeitherRepeatNorSkip(t *testing.T) {
	fake := newFakeIDPages()
	owner := uuid.New()
	original := seedList(fake, store.ListFollowers, owner, 250) // newest first
	r := idPagesRouter(fake)
	newcomers := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}

	got := walkInternalList(t, r, store.ListFollowers, owner, 100, func(page int) {
		if page != 0 {
			return
		}
		for i, id := range newcomers {
			fake.add(store.ListFollowers, owner, id, listBase.Add(time.Hour+time.Duration(i)*time.Second))
		}
		fake.remove(store.ListFollowers, owner, original[10]) // already returned
	})
	seen := map[uuid.UUID]int{}
	for _, id := range got {
		seen[id]++
	}
	for i, id := range original {
		if seen[id] != 1 {
			t.Fatalf("follower %d was returned %d time(s), want exactly once", i, seen[id])
		}
	}
	for _, id := range newcomers {
		if seen[id] != 0 {
			t.Fatal("a follower who arrived at the head mid-walk appeared in a later page")
		}
	}
}

// Rows sharing one created_at (a bulk import) straddling a page boundary:
// the id tie-break keeps the walk exact.
func TestInternalIDList_TiedTimestampsAcrossAPageBoundary(t *testing.T) {
	fake := newFakeIDPages()
	owner := uuid.New()
	want := map[uuid.UUID]bool{}
	for i := 0; i < 25; i++ {
		id := uuid.New()
		want[id] = true
		fake.add(store.ListFollowers, owner, id, listBase) // all at one instant
	}
	got := walkInternalList(t, idPagesRouter(fake), store.ListFollowers, owner, 10, nil)
	if len(got) != 25 {
		t.Fatalf("walk returned %d ids, want 25", len(got))
	}
	for _, id := range got {
		if !want[id] {
			t.Fatal("unknown or repeated id")
		}
		delete(want, id)
	}
}

// The public offset lists are untouched: same paths, same bare-array shape,
// and the 10,000 offset cap still answers OFFSET_TOO_LARGE.
func TestPublicOffsetListsUnchanged(t *testing.T) {
	rule := newFakeConnections()
	owner := uuid.New()
	rule.lists[owner] = []uuid.UUID{uuid.New(), uuid.New()}
	r := connectionsRouter(rule)
	wantList(t, "offset page", listGet(r, "/v1/graph/followers/"+owner.String()+"?limit=100&offset=0", false, ""), rule.lists[owner])
	resp := listGet(r, "/v1/graph/followers/"+owner.String()+"?limit=100&offset=10100", false, "")
	if resp.Code != http.StatusBadRequest || !bytes.Contains(resp.Body.Bytes(), []byte("OFFSET_TOO_LARGE")) {
		t.Fatalf("offset 10100: status %d, body %s", resp.Code, resp.Body.String())
	}
}

// Golden JSON for the internal id lists, under testdata/contracts/
// internal_lists at the module root. Each file is the whole response body
// the route writes for a pinned list; feed-service keeps byte-for-byte
// copies (internal/service/testdata/contracts/graph_internal_lists) and
// decodes them with its real client.
//
//	UPDATE_CONTRACTS=1 go test ./internal/http -run TestInternalIDListContracts
func TestInternalIDListContracts(t *testing.T) {
	owner := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	fake := newFakeIDPages()
	at := time.Date(2026, 11, 16, 9, 30, 0, 123456000, time.UTC)
	for i, id := range []string{
		"11111111-1111-4111-8111-111111111111",
		"22222222-2222-4222-8222-222222222222",
		"33333333-3333-4333-8333-333333333333",
	} {
		fake.add(store.ListFollowers, owner, uuid.MustParse(id), at.Add(-time.Duration(i)*time.Minute))
	}
	r := idPagesRouter(fake)
	body := func(path string, wantStatus int) []byte {
		resp := httptest.NewRecorder()
		r.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, path, nil))
		if resp.Code != wantStatus {
			t.Fatalf("%s: status %d, want %d: %s", path, resp.Code, wantStatus, resp.Body.String())
		}
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, bytes.TrimSpace(resp.Body.Bytes()), "", "  "); err != nil {
			t.Fatal(err)
		}
		pretty.WriteByte('\n')
		return pretty.Bytes()
	}
	firstCursor := store.ListCursor{CreatedAt: at.Add(-time.Minute), ID: uuid.MustParse("22222222-2222-4222-8222-222222222222")}.String()
	contracts := map[string][]byte{
		"id_page.json":              body(internalListPath(store.ListFollowers, owner, 2, ""), http.StatusOK),
		"id_page_last.json":         body(internalListPath(store.ListFollowers, owner, 2, firstCursor), http.StatusOK),
		"error_invalid_cursor.json": body(internalListPath(store.ListFollowers, owner, 2, "300"), http.StatusBadRequest),
	}

	dir := filepath.Join("..", "..", "testdata", "contracts", "internal_lists")
	update := os.Getenv("UPDATE_CONTRACTS") == "1"
	if update {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, got := range contracts {
		path := filepath.Join(dir, name)
		if update {
			if err := os.WriteFile(path, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read fixture: %v (UPDATE_CONTRACTS=1 to generate)", err)
		}
		if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
			t.Fatalf("fixture %s is stale.\n--- want\n%s\n--- got\n%s", name, want, got)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if _, ok := contracts[e.Name()]; !ok {
			t.Fatalf("testdata/contracts/internal_lists/%s is asserted by no test", e.Name())
		}
	}
}
