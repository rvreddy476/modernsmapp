package http

// Search history and popular searches (T-15) — handler contracts.
//
//  1. Recording: signed-in searches on /v1/search and /v1/search/posts are
//     recorded; anonymous callers, opt-outs, continuation pages, too-short
//     queries and failed searches are not; picker routes record only on
//     ?record=1; a failing or saturated history store never touches the
//     search response.
//  2. DELETE /v1/search/history/:id: the caller's own entry only; anything
//     else is a 404 that does not say whether the id exists.
//  3. GET /v1/search/popular: threshold passed to the store, limit, cache,
//     empty (never an error) without Postgres. Shape pinned as golden JSON
//     under testdata/contracts/search_history/.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/search-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type recordCall struct {
	user  uuid.UUID
	query string
}

type fakeHistory struct {
	mu         sync.Mutex
	records    []recordCall
	recordErr  error
	block      chan struct{} // when set, RecordSearchHistory waits on it
	owned      map[uuid.UUID]uuid.UUID
	deleteErr  error
	popular    []postgres.PopularQuery
	popularErr error
	popularN   int
	minUsers   int
	window     time.Duration
}

func (f *fakeHistory) RecordSearchHistory(ctx context.Context, userID uuid.UUID, query string) error {
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records = append(f.records, recordCall{userID, query})
	return f.recordErr
}

func (f *fakeHistory) DeleteSearchHistoryItem(ctx context.Context, id, userID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteErr != nil {
		return f.deleteErr
	}
	if owner, ok := f.owned[id]; ok && owner == userID {
		delete(f.owned, id)
		return nil
	}
	return postgres.ErrNotFound
}

func (f *fakeHistory) PopularQueries(ctx context.Context, window time.Duration, minUsers, limit int) ([]postgres.PopularQuery, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.popularN++
	f.minUsers, f.window = minUsers, window
	if f.popularErr != nil {
		return nil, f.popularErr
	}
	out := f.popular
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeHistory) recorded() []recordCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordCall(nil), f.records...)
}

func historyRouter(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/v1/search", h.UniversalSearch)
	r.GET("/v1/search/posts", h.SearchPosts)
	r.GET("/v1/search/users", h.SearchUsers)
	r.GET("/v1/search/hashtags", h.SearchHashtags)
	r.GET("/v1/search/channels", h.SearchTubeChannels)
	r.GET("/v1/search/popular", h.PopularSearches)
	r.DELETE("/v1/search/history/:id", h.DeleteSearchHistoryItem)
	return r
}

func doHistory(t *testing.T, h *Handler, method, target string, user uuid.UUID) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if user != uuid.Nil {
		req.Header.Set("X-User-Id", user.String())
	}
	w := httptest.NewRecorder()
	historyRouter(h).ServeHTTP(w, req)
	h.WaitHistoryWrites()
	return w.Code, w.Body.Bytes()
}

func newHistoryHandler(t *testing.T) (*Handler, *fakeHistory) {
	t.Helper()
	store, _ := newTubeFakeOS(t)
	f := &fakeHistory{owned: map[uuid.UUID]uuid.UUID{}}
	return &Handler{store: store, history: f}, f
}

// ─── 1. Recording ───────────────────────────────────────────────────────

func TestHistory_SignedInSearchIsRecorded(t *testing.T) {
	user := uuid.New()
	for _, target := range []string{
		"/v1/search/posts?q=Red+Saree",
		"/v1/search/posts?q=Red+Saree&type=videos",
		"/v1/search/posts?q=Red+Saree&type=flicks",
		"/v1/search?q=Red+Saree",
		"/v1/search?q=Red+Saree&types=posts,users",
	} {
		t.Run(target, func(t *testing.T) {
			h, f := newHistoryHandler(t)
			code, body := doHistory(t, h, http.MethodGet, target, user)
			if code != http.StatusOK {
				t.Fatalf("status %d: %s", code, body)
			}
			got := f.recorded()
			if len(got) != 1 || got[0].user != user || got[0].query != "Red Saree" {
				t.Fatalf("recorded %+v, want one call for %s with %q", got, user, "Red Saree")
			}
		})
	}
}

func TestHistory_AnonymousIsNeverRecorded(t *testing.T) {
	h, f := newHistoryHandler(t)
	for _, target := range []string{
		"/v1/search/posts?q=cats",
		"/v1/search?q=cats",
		"/v1/search/users?q=cats&record=1",
	} {
		if code, body := doHistory(t, h, http.MethodGet, target, uuid.Nil); code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", target, code, body)
		}
	}
	if got := f.recorded(); len(got) != 0 {
		t.Fatalf("anonymous searches recorded: %+v", got)
	}
}

func TestHistory_NotRecorded(t *testing.T) {
	user := uuid.New()
	cases := map[string]string{
		"opt-out":                     "/v1/search/posts?q=cats&record=0",
		"query too short":             "/v1/search/posts?q=a",
		"continuation page (cursor.)": "/v1/search?q=cats&types=posts&cursor.posts=abc",
		"tube continuation (cursor)":  "/v1/search/channels?q=cats&cursor=20&record=1",
		"users route without opt-in":  "/v1/search/users?q=jo",
		"hashtags without opt-in":     "/v1/search/hashtags?q=dog",
		"channels without opt-in":     "/v1/search/channels?q=cooking",
		"refused search (bad type)":   "/v1/search/posts?q=cats&type=bogus",
		"refused search (empty q)":    "/v1/search/posts?q=%20%20",
	}
	for name, target := range cases {
		t.Run(name, func(t *testing.T) {
			h, f := newHistoryHandler(t)
			doHistory(t, h, http.MethodGet, target, user)
			if got := f.recorded(); len(got) != 0 {
				t.Fatalf("%s recorded %+v", target, got)
			}
		})
	}
}

func TestHistory_PickerRoutesRecordOnOptIn(t *testing.T) {
	user := uuid.New()
	for _, target := range []string{
		"/v1/search/users?q=ravi&record=1",
		"/v1/search/hashtags?q=diwali&record=1",
		"/v1/search/channels?q=cooking&record=true",
	} {
		t.Run(target, func(t *testing.T) {
			h, f := newHistoryHandler(t)
			if code, body := doHistory(t, h, http.MethodGet, target, user); code != http.StatusOK {
				t.Fatalf("status %d: %s", code, body)
			}
			if got := f.recorded(); len(got) != 1 {
				t.Fatalf("recorded %+v, want exactly one", got)
			}
		})
	}
}

func TestHistory_StoreFailureDoesNotFailSearch(t *testing.T) {
	h, f := newHistoryHandler(t)
	f.recordErr = errors.New("postgres down")
	code, body := doHistory(t, h, http.MethodGet, "/v1/search/posts?q=cats", uuid.New())
	if code != http.StatusOK {
		t.Fatalf("search failed with the history store down: %d %s", code, body)
	}
}

func TestHistory_SaturatedWritesAreDroppedNotQueued(t *testing.T) {
	h, f := newHistoryHandler(t)
	f.block = make(chan struct{})
	// Hold every slot.
	slots := h.recorder.slots()
	for i := 0; i < cap(slots); i++ {
		slots <- struct{}{}
	}
	done := make(chan int, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/v1/search/posts?q=cats", nil)
		req.Header.Set("X-User-Id", uuid.NewString())
		w := httptest.NewRecorder()
		historyRouter(h).ServeHTTP(w, req)
		done <- w.Code
	}()
	select {
	case code := <-done:
		if code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("search blocked behind a saturated history writer")
	}
	for i := 0; i < cap(slots); i++ {
		<-slots
	}
	close(f.block)
	h.WaitHistoryWrites()
	if got := f.recorded(); len(got) != 0 {
		t.Fatalf("a write was queued instead of dropped: %+v", got)
	}
}

func TestHistory_WriteDoesNotHoldTheResponse(t *testing.T) {
	h, f := newHistoryHandler(t)
	f.block = make(chan struct{})
	req := httptest.NewRequest(http.MethodGet, "/v1/search/posts?q=cats", nil)
	req.Header.Set("X-User-Id", uuid.NewString())
	w := httptest.NewRecorder()
	historyRouter(h).ServeHTTP(w, req) // returns while the write is still blocked
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	close(f.block)
	h.WaitHistoryWrites()
	if got := f.recorded(); len(got) != 1 {
		t.Fatalf("recorded %+v, want one", got)
	}
}

// ─── 2. DELETE /v1/search/history/:id ───────────────────────────────────

func TestHistory_DeleteOwnEntry(t *testing.T) {
	h, f := newHistoryHandler(t)
	owner, other := uuid.New(), uuid.New()
	mine := uuid.New()
	f.owned[mine] = owner

	if code, body := doHistory(t, h, http.MethodDelete, "/v1/search/history/"+mine.String(), other); code != http.StatusNotFound {
		t.Fatalf("someone else's entry: status %d (%s), want 404", code, body)
	}
	if _, still := f.owned[mine]; !still {
		t.Fatal("another user deleted the entry")
	}
	code404Body := func(target string, user uuid.UUID) string {
		_, b := doHistory(t, h, http.MethodDelete, target, user)
		return string(b)
	}
	// Not theirs and does not exist read exactly the same.
	if a, b := code404Body("/v1/search/history/"+mine.String(), other), code404Body("/v1/search/history/"+uuid.NewString(), other); stripRequestID(a) != stripRequestID(b) {
		t.Fatalf("404 bodies differ, leaking existence:\n%s\n%s", a, b)
	}

	code, body := doHistory(t, h, http.MethodDelete, "/v1/search/history/"+mine.String(), owner)
	if code != http.StatusOK || !strings.Contains(string(body), `"deleted"`) {
		t.Fatalf("owner delete: %d %s", code, body)
	}
	if code, _ := doHistory(t, h, http.MethodDelete, "/v1/search/history/"+mine.String(), owner); code != http.StatusNotFound {
		t.Fatalf("second delete: status %d, want 404", code)
	}
}

func TestHistory_DeleteRefusals(t *testing.T) {
	h, _ := newHistoryHandler(t)
	if code, _ := doHistory(t, h, http.MethodDelete, "/v1/search/history/"+uuid.NewString(), uuid.Nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous delete: %d, want 401", code)
	}
	if code, _ := doHistory(t, h, http.MethodDelete, "/v1/search/history/not-a-uuid", uuid.New()); code != http.StatusBadRequest {
		t.Fatalf("malformed id: %d, want 400", code)
	}
	noStore := &Handler{}
	if code, _ := doHistory(t, noStore, http.MethodDelete, "/v1/search/history/"+uuid.NewString(), uuid.New()); code != http.StatusServiceUnavailable {
		t.Fatalf("no store: %d, want 503", code)
	}
}

// stripRequestID drops any request id the error envelope carries.
func stripRequestID(body string) string {
	var m map[string]any
	if json.Unmarshal([]byte(body), &m) != nil {
		return body
	}
	delete(m, "request_id")
	if e, ok := m["error"].(map[string]any); ok {
		delete(e, "request_id")
	}
	out, _ := json.Marshal(m)
	return string(out)
}

// ─── 3. GET /v1/search/popular ──────────────────────────────────────────

func TestPopular_ThresholdWindowLimitAndCache(t *testing.T) {
	h, f := newHistoryHandler(t)
	f.popular = []postgres.PopularQuery{{Query: "diwali", Users: 40}, {Query: "cricket", Users: 12}, {Query: "recipes", Users: 6}}

	code, body := doHistory(t, h, http.MethodGet, "/v1/search/popular?limit=2", uuid.Nil)
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	if f.minUsers != popularDefaultMinUsers || f.window != 7*24*time.Hour {
		t.Fatalf("store asked with minUsers=%d window=%s, want %d over 7 days", f.minUsers, f.window, popularDefaultMinUsers)
	}
	var env struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data.Items) != 2 || env.Data.Items[0]["query"] != "diwali" {
		t.Fatalf("items %+v", env.Data.Items)
	}
	if _, leaked := env.Data.Items[0]["users"]; leaked {
		t.Fatal("distinct-user counts must not be serialised")
	}

	doHistory(t, h, http.MethodGet, "/v1/search/popular", uuid.New())
	if f.popularN != 1 {
		t.Fatalf("store read %d times, want 1 (cached)", f.popularN)
	}
}

func TestPopular_MinUsersConfigNeverBelowFloor(t *testing.T) {
	h, f := newHistoryHandler(t)
	h.WithPopularMinUsers(1)
	doHistory(t, h, http.MethodGet, "/v1/search/popular", uuid.Nil)
	if f.minUsers != popularDefaultMinUsers {
		t.Fatalf("minUsers=%d after WithPopularMinUsers(1); the floor must hold", f.minUsers)
	}
	h2, f2 := newHistoryHandler(t)
	h2.WithPopularMinUsers(postgres.PopularMinUsersFloor)
	doHistory(t, h2, http.MethodGet, "/v1/search/popular", uuid.Nil)
	if f2.minUsers != postgres.PopularMinUsersFloor {
		t.Fatalf("minUsers=%d, want %d", f2.minUsers, postgres.PopularMinUsersFloor)
	}
}

func TestPopular_EmptyNeverError(t *testing.T) {
	code, body := doHistory(t, &Handler{}, http.MethodGet, "/v1/search/popular", uuid.Nil)
	if code != http.StatusOK || !strings.Contains(string(body), `"items":[]`) {
		t.Fatalf("no store: %d %s", code, body)
	}
	h, f := newHistoryHandler(t)
	f.popularErr = errors.New("boom")
	code, body = doHistory(t, h, http.MethodGet, "/v1/search/popular", uuid.Nil)
	if code != http.StatusOK || !strings.Contains(string(body), `"items":[]`) {
		t.Fatalf("store error: %d %s", code, body)
	}
}

// Golden: the popular-searches and delete shapes clients build against.
func TestHistoryContracts_Golden(t *testing.T) {
	h, f := newHistoryHandler(t)
	f.popular = []postgres.PopularQuery{{Query: "diwali", Users: 40}, {Query: "cricket highlights", Users: 12}}
	owner, entry := uuid.New(), uuid.New()
	f.owned[entry] = owner

	_, popular := doHistory(t, h, http.MethodGet, "/v1/search/popular", uuid.Nil)
	_, deleted := doHistory(t, h, http.MethodDelete, "/v1/search/history/"+entry.String(), owner)
	_, missing := doHistory(t, h, http.MethodDelete, "/v1/search/history/"+uuid.NewString(), owner)

	for name, body := range map[string][]byte{
		"popular.json":            popular,
		"delete_history.json":     deleted,
		"delete_history_404.json": []byte(stripRequestID(string(missing))),
	} {
		checkHistoryGolden(t, filepath.Join("testdata", "contracts", "search_history", name), body)
	}
}

func checkHistoryGolden(t *testing.T, path string, body []byte) {
	t.Helper()
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("%s: not JSON: %v", path, err)
	}
	pretty, _ := json.MarshalIndent(v, "", "  ")
	pretty = append(pretty, '\n')
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, pretty, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v (run with -args -update to create it)", path, err)
	}
	if strings.ReplaceAll(string(want), "\r\n", "\n") != string(pretty) {
		t.Fatalf("%s drifted:\nwant %s\ngot  %s", path, want, pretty)
	}
}
