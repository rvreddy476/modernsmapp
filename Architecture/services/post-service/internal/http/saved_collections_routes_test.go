package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Saved collections over the real routes (2026-10-24). The store is an
// in-memory one with the Postgres store's user scoping; the SQL is pinned by
// the store package's integration test.

type routeSavedStore struct {
	cols  map[uuid.UUID]*postgres.SavedCollectionRow
	items map[uuid.UUID]*postgres.SavedItem
}

func newRouteSavedStore() *routeSavedStore {
	return &routeSavedStore{cols: map[uuid.UUID]*postgres.SavedCollectionRow{}, items: map[uuid.UUID]*postgres.SavedItem{}}
}

func (m *routeSavedStore) own(u, id uuid.UUID) (*postgres.SavedCollectionRow, error) {
	c, ok := m.cols[id]
	if !ok || c.UserID != u {
		return nil, postgres.ErrCollectionNotFound
	}
	return c, nil
}

func (m *routeSavedStore) ListSavedCollections(_ context.Context, u uuid.UUID) (int64, []postgres.SavedCollectionRow, error) {
	var def int64
	for _, it := range m.items {
		if it.UserID == u && it.CollectionID == nil {
			def++
		}
	}
	out := []postgres.SavedCollectionRow{}
	for _, c := range m.cols {
		if c.UserID == u {
			out = append(out, *c)
		}
	}
	return def, out, nil
}
func (m *routeSavedStore) GetSavedCollection(_ context.Context, u, id uuid.UUID) (*postgres.SavedCollectionRow, error) {
	return m.own(u, id)
}
func (m *routeSavedStore) FindSavedCollectionByName(_ context.Context, u uuid.UUID, name string) (*postgres.SavedCollectionRow, error) {
	for _, c := range m.cols {
		if c.UserID == u && strings.EqualFold(c.Name, name) {
			return c, nil
		}
	}
	return nil, postgres.ErrCollectionNotFound
}
func (m *routeSavedStore) CreateSavedCollection(_ context.Context, u uuid.UUID, name string, max int) (*postgres.SavedCollectionRow, error) {
	n := 0
	for _, c := range m.cols {
		if c.UserID == u {
			n++
			if strings.EqualFold(c.Name, name) {
				return nil, postgres.ErrCollectionNameTaken
			}
		}
	}
	if n >= max {
		return nil, postgres.ErrCollectionLimit
	}
	c := &postgres.SavedCollectionRow{ID: uuid.New(), UserID: u, Name: name, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	m.cols[c.ID] = c
	return c, nil
}
func (m *routeSavedStore) RenameSavedCollection(_ context.Context, u, id uuid.UUID, name string) (*postgres.SavedCollectionRow, error) {
	c, err := m.own(u, id)
	if err != nil {
		return nil, err
	}
	c.Name = name
	return c, nil
}
func (m *routeSavedStore) DeleteSavedCollection(_ context.Context, u, id uuid.UUID) (int64, error) {
	if _, err := m.own(u, id); err != nil {
		return 0, err
	}
	var moved int64
	for _, it := range m.items {
		if it.CollectionID != nil && *it.CollectionID == id {
			it.CollectionID, it.CollectionName = nil, postgres.DefaultCollectionName
			moved++
		}
	}
	delete(m.cols, id)
	return moved, nil
}
func (m *routeSavedStore) move(u uuid.UUID, it *postgres.SavedItem, cid *uuid.UUID) (*postgres.SavedItem, error) {
	if cid == nil {
		it.CollectionID, it.CollectionName = nil, postgres.DefaultCollectionName
		return it, nil
	}
	c, err := m.own(u, *cid)
	if err != nil {
		return nil, err
	}
	id := c.ID
	it.CollectionID, it.CollectionName = &id, c.Name
	return it, nil
}
func (m *routeSavedStore) MoveSavedItem(_ context.Context, u, savedID uuid.UUID, cid *uuid.UUID) (*postgres.SavedItem, error) {
	if cid != nil {
		if _, err := m.own(u, *cid); err != nil {
			return nil, err
		}
	}
	it, ok := m.items[savedID]
	if !ok || it.UserID != u {
		return nil, postgres.ErrSavedItemNotFound
	}
	return m.move(u, it, cid)
}
func (m *routeSavedStore) MoveSavedTarget(ctx context.Context, u uuid.UUID, tt string, target uuid.UUID, cid *uuid.UUID) (*postgres.SavedItem, error) {
	for id, it := range m.items {
		if it.UserID == u && it.TargetType == tt && it.TargetID == target {
			return m.MoveSavedItem(ctx, u, id, cid)
		}
	}
	return nil, postgres.ErrSavedItemNotFound
}
func (m *routeSavedStore) SaveItem(_ context.Context, u uuid.UUID, tt string, target uuid.UUID, cid *uuid.UUID) (*postgres.SavedItem, error) {
	it := &postgres.SavedItem{ID: uuid.New(), UserID: u, TargetType: tt, TargetID: target}
	m.items[it.ID] = it
	return m.move(u, it, cid)
}
func (m *routeSavedStore) ListSavedItems(_ context.Context, u uuid.UUID, _ postgres.SavedScope, _ int, _ string) ([]postgres.SavedItem, string, error) {
	return nil, "", nil
}

func savedRouter(st *routeSavedStore) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(service.NewForHandlerTests(service.HandlerTestDeps{SavedCollections: st}), nil).RegisterRoutes(r)
	return r
}

func doJSON(r *gin.Engine, method, path string, user uuid.UUID, body string) (int, map[string]json.RawMessage) {
	var rdr *bytes.Buffer
	if body != "" {
		rdr = bytes.NewBufferString(body)
	} else {
		rdr = &bytes.Buffer{}
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	if user != uuid.Nil {
		req.Header.Set("X-User-Id", user.String())
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var env map[string]json.RawMessage
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	return w.Code, env
}

func errCode(env map[string]json.RawMessage) string {
	var e struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(env["error"], &e)
	return e.Code
}

func TestSavedCollectionRoutes(t *testing.T) {
	st := newRouteSavedStore()
	r := savedRouter(st)
	alice, bob := uuid.New(), uuid.New()

	// Signed-out is refused.
	if code, _ := doJSON(r, http.MethodGet, "/v1/saved/collections", uuid.Nil, ""); code != http.StatusUnauthorized {
		t.Fatalf("signed out: %d", code)
	}

	code, env := doJSON(r, http.MethodPost, "/v1/saved/collections", alice, `{"name":"  Trips "}`)
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, env["error"])
	}
	var created service.SavedCollection
	_ = json.Unmarshal(env["data"], &created)
	if created.ID == nil || created.Name != "Trips" || created.IsDefault {
		t.Fatalf("created %+v", created)
	}
	id := created.ID.String()

	cases := []struct {
		name, method, path, body string
		user                     uuid.UUID
		status                   int
		code                     string
	}{
		{"name taken, any case", http.MethodPost, "/v1/saved/collections", `{"name":"TRIPS"}`, alice, http.StatusConflict, "COLLECTION_NAME_TAKEN"},
		{"name too long", http.MethodPost, "/v1/saved/collections", `{"name":"` + strings.Repeat("x", 41) + `"}`, alice, http.StatusBadRequest, "INVALID_COLLECTION_NAME"},
		{"reserved name", http.MethodPost, "/v1/saved/collections", `{"name":"All Saved"}`, alice, http.StatusBadRequest, "INVALID_COLLECTION_NAME"},
		{"missing name", http.MethodPost, "/v1/saved/collections", `{}`, alice, http.StatusBadRequest, "INVALID_COLLECTION_NAME"},
		{"rename someone else's", http.MethodPatch, "/v1/saved/collections/" + id, `{"name":"Mine"}`, bob, http.StatusNotFound, "COLLECTION_NOT_FOUND"},
		{"delete someone else's", http.MethodDelete, "/v1/saved/collections/" + id, ``, bob, http.StatusNotFound, "COLLECTION_NOT_FOUND"},
		{"rename the default", http.MethodPatch, "/v1/saved/collections/default", `{"name":"x"}`, alice, http.StatusNotFound, "COLLECTION_NOT_FOUND"},
		{"move needs collection_id", http.MethodPut, "/v1/saved/" + uuid.NewString() + "/collection", `{}`, alice, http.StatusBadRequest, "INVALID_REQUEST"},
		{"move with a bad id", http.MethodPut, "/v1/saved/" + uuid.NewString() + "/collection", `{"collection_id":"nope"}`, alice, http.StatusBadRequest, "INVALID_COLLECTION_ID"},
		{"move an unknown item", http.MethodPut, "/v1/saved/" + uuid.NewString() + "/collection", `{"collection_id":null}`, alice, http.StatusNotFound, "NOT_SAVED"},
		{"move an unsaved post", http.MethodPut, "/v1/posts/" + uuid.NewString() + "/bookmark/collection", `{"collection_id":"` + id + `"}`, alice, http.StatusNotFound, "NOT_SAVED"},
		{"bookmarks of someone else's collection", http.MethodGet, "/v1/posts/bookmarks?collection_id=" + id, ``, bob, http.StatusNotFound, "COLLECTION_NOT_FOUND"},
		{"saved items of someone else's collection", http.MethodGet, "/v1/saved?collection_id=" + id, ``, bob, http.StatusNotFound, "COLLECTION_NOT_FOUND"},
		{"bookmarks with a junk collection_id", http.MethodGet, "/v1/posts/bookmarks?collection_id=x", ``, alice, http.StatusBadRequest, "INVALID_COLLECTION_ID"},
	}
	for _, tc := range cases {
		code, env := doJSON(r, tc.method, tc.path, tc.user, tc.body)
		if code != tc.status || errCode(env) != tc.code {
			t.Errorf("%s: %d %s, want %d %s", tc.name, code, errCode(env), tc.status, tc.code)
		}
	}

	// Bob's list never shows Alice's collection.
	_, env = doJSON(r, http.MethodGet, "/v1/saved/collections", bob, "")
	var bobs []service.SavedCollection
	_ = json.Unmarshal(env["data"], &bobs)
	if len(bobs) != 1 || !bobs[0].IsDefault || bobs[0].ID != nil {
		t.Fatalf("bob's list: %s", env["data"])
	}

	// Move: into the collection by saved id, then back by null, then by post.
	post := uuid.New()
	item, _ := st.SaveItem(context.Background(), alice, "post", post, nil)
	code, env = doJSON(r, http.MethodPut, "/v1/saved/"+item.ID.String()+"/collection", alice, `{"collection_id":"`+id+`"}`)
	var moved postgres.SavedItem
	_ = json.Unmarshal(env["data"], &moved)
	if code != http.StatusOK || moved.CollectionID == nil || moved.CollectionID.String() != id {
		t.Fatalf("move in: %d %s", code, env["data"])
	}
	if !strings.Contains(string(env["data"]), `"collection_id":"`+id+`"`) {
		t.Fatalf("the saved item must carry collection_id: %s", env["data"])
	}
	code, env = doJSON(r, http.MethodPut, "/v1/saved/"+item.ID.String()+"/collection", alice, `{"collection_id":"default"}`)
	if code != http.StatusOK || !strings.Contains(string(env["data"]), `"collection_id":null`) {
		t.Fatalf("move home: %d %s", code, env["data"])
	}
	code, _ = doJSON(r, http.MethodPut, "/v1/posts/"+post.String()+"/bookmark/collection", alice, `{"collection_id":"`+id+`"}`)
	if code != http.StatusOK || st.items[item.ID].CollectionID == nil {
		t.Fatalf("move by post: %d", code)
	}
	// Bob cannot move Alice's item.
	if code, env := doJSON(r, http.MethodPut, "/v1/saved/"+item.ID.String()+"/collection", bob, `{"collection_id":null}`); code != http.StatusNotFound || errCode(env) != "NOT_SAVED" {
		t.Fatalf("bob moving alice's item: %d %s", code, errCode(env))
	}

	// Rename, then delete: the item goes back to the default.
	code, env = doJSON(r, http.MethodPatch, "/v1/saved/collections/"+id, alice, `{"name":"Holidays"}`)
	if code != http.StatusOK || !strings.Contains(string(env["data"]), `"name":"Holidays"`) {
		t.Fatalf("rename: %d %s", code, env["data"])
	}
	code, env = doJSON(r, http.MethodDelete, "/v1/saved/collections/"+id, alice, "")
	if code != http.StatusOK || !strings.Contains(string(env["data"]), `"moved_to_default":1`) {
		t.Fatalf("delete: %d %s", code, env["data"])
	}
	if it := st.items[item.ID]; it.CollectionID != nil || it.CollectionName != "All Saved" {
		t.Fatalf("after delete the item is in %v / %q, want the default", it.CollectionID, it.CollectionName)
	}
}

func TestSavedCollectionLimitRoute(t *testing.T) {
	st := newRouteSavedStore()
	r := savedRouter(st)
	u := uuid.New()
	for i := 0; i < service.MaxSavedCollections; i++ {
		if code, env := doJSON(r, http.MethodPost, "/v1/saved/collections", u, `{"name":"c`+uuid.NewString()[:8]+`"}`); code != http.StatusCreated {
			t.Fatalf("collection %d: %d %s", i+1, code, errCode(env))
		}
	}
	if code, env := doJSON(r, http.MethodPost, "/v1/saved/collections", u, `{"name":"fifty one"}`); code != http.StatusConflict || errCode(env) != "COLLECTION_LIMIT_REACHED" {
		t.Fatalf("51st: %d %s", code, errCode(env))
	}
}
