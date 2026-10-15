package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/post-service/internal/store/scylla"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Profile sort (T-21) and hashtag total (T-34), 2026-10-14.
//
// Golden JSON under testdata/contracts/profile_hashtag/, same rule as the
// MTube fixtures: each file is marshalled from the structs and functions the
// handlers use (whole envelopes here, because both changes live in `meta`),
// so a shape change that does not update the fixture fails.
//
//	UPDATE_CONTRACTS=1 go test ./internal/http -run TestProfileHashtagContracts
//
// The cursor is opaque to clients; the fixture pins that it is URL-safe and
// travels in meta.next_cursor exactly as before.

func newSortRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.CustomRecovery(func(c *gin.Context, _ any) { c.AbortWithStatus(http.StatusInternalServerError) }))
	// No graph URL (no privacy policy) and no store: a request that got past
	// validation would reach the nil store and come back a recovered 500.
	h := New(service.NewForHandlerTests(service.HandlerTestDeps{}), nil)
	h.RegisterRoutes(r)
	return r
}

func getJSON(r *gin.Engine, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestByAuthorRejectsUnknownSort(t *testing.T) {
	r := newSortRouter(t)
	author := uuid.New()
	for _, sort := range []string{"newest", "top", "likes", "recent"} {
		w := getJSON(r, "/v1/posts/by-author/"+author.String()+"?sort="+sort)
		if w.Code != http.StatusBadRequest || errorCode(t, w) != "INVALID_SORT" {
			t.Fatalf("sort=%s: got %d %s, want 400 INVALID_SORT", sort, w.Code, w.Body.String())
		}
	}
}

func TestByAuthorRejectsACursorFromAnotherSort(t *testing.T) {
	r := newSortRouter(t)
	author := uuid.New()
	latest := postgres.EncodeAuthorCursor(postgres.AuthorSortLatest, false, fxTime, 0, fxPost)
	for _, path := range []string{
		"/v1/posts/by-author/" + author.String() + "?sort=popular&cursor=" + latest,
		"/v1/posts/by-author/" + author.String() + "?sort=oldest&cursor=" + latest,
		"/v1/posts/by-author/" + author.String() + "?cursor=not-a-cursor",
		// A pre-T-21 timestamp cursor is still a latest cursor, never a popular one.
		"/v1/posts/by-author/" + author.String() + "?sort=popular&cursor=" + fxTime.Format(time.RFC3339Nano),
	} {
		w := getJSON(r, path)
		if w.Code != http.StatusBadRequest || errorCode(t, w) != "INVALID_CURSOR" {
			t.Fatalf("%s: got %d %s, want 400 INVALID_CURSOR", path, w.Code, w.Body.String())
		}
	}
}

func TestHashtagPostsMeta(t *testing.T) {
	if m := newHashtagPostsMeta("", nil, false); m != nil {
		t.Fatalf("last page without a count must keep the old envelope (no meta), got %+v", m)
	}
	if m := newHashtagPostsMeta("c2", nil, true); m == nil || m.NextCursor != "c2" || m.Total != nil || m.TotalCapped {
		t.Fatalf("later page: %+v", m)
	}
	zero := 0
	b, _ := json.Marshal(newHashtagPostsMeta("", &zero, false))
	if string(b) != `{"total":0}` {
		t.Fatalf("an empty tag must still say total 0, got %s", b)
	}
	n := postgres.HashtagTotalCap
	if m := newHashtagPostsMeta("c", &n, true); m.Total == nil || *m.Total != n || !m.TotalCapped {
		t.Fatalf("capped: %+v", m)
	}
}

func profileHashtagContracts(t *testing.T) map[string]any {
	post := fixturePost()
	post.ContentType, post.PostType, post.AppOrigin = "flick", "reel", "reels"
	detail := service.PostDetail{Post: post, Counts: &scylla.Counts{Likes: 128, Comments: 9}}
	page := []service.PostDetail{detail}

	r := newSortRouter(t)
	badSort := getJSON(r, "/v1/posts/by-author/"+fxAuthor.String()+"?sort=newest")
	badCursor := getJSON(r, "/v1/posts/by-author/"+fxAuthor.String()+"?sort=popular&cursor="+
		postgres.EncodeAuthorCursor(postgres.AuthorSortLatest, false, fxTime, 0, fxPost))

	total, capped := 1342, postgres.HashtagTotalCap
	return map[string]any{
		"by_author_sorts.json": map[string]any{
			"param": "sort", "values": postgres.AuthorPostSorts, "default": postgres.AuthorSortLatest,
			"pinned_first_only_for": postgres.AuthorSortLatest,
		},
		"by_author_popular_page.json": api.Response{Data: page, Meta: &api.Meta{
			NextCursor: postgres.EncodeAuthorCursor(postgres.AuthorSortPopular, false, post.CreatedAt, 128, post.ID),
		}},
		"by_author_oldest_page.json": api.Response{Data: page, Meta: &api.Meta{
			NextCursor: postgres.EncodeAuthorCursor(postgres.AuthorSortOldest, false, post.CreatedAt, 0, post.ID),
		}},
		"by_author_invalid_sort.json":   rawJSON(t, badSort),
		"by_author_invalid_cursor.json": rawJSON(t, badCursor),
		"hashtag_posts_first_page.json": hashtagPostsResponse{Data: page, Meta: newHashtagPostsMeta("c2", &total, false)},
		"hashtag_posts_capped_meta.json": hashtagPostsResponse{Data: []service.PostDetail{},
			Meta: newHashtagPostsMeta("", &capped, true)},
	}
}

func rawJSON(t *testing.T, w *httptest.ResponseRecorder) json.RawMessage {
	t.Helper()
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want a 400 for the error fixture, got %d %s", w.Code, w.Body.String())
	}
	return json.RawMessage(bytes.TrimSpace(w.Body.Bytes()))
}

func TestProfileHashtagContracts(t *testing.T) {
	dir := filepath.Join("testdata", "contracts", "profile_hashtag")
	update := os.Getenv("UPDATE_CONTRACTS") == "1"
	if update {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, value := range profileHashtagContracts(t) {
		t.Run(name, func(t *testing.T) {
			got, err := json.MarshalIndent(value, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, '\n')
			path := filepath.Join(dir, name)
			if update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read fixture: %v (UPDATE_CONTRACTS=1 to generate)", err)
			}
			if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
				t.Fatalf("fixture %s is stale.\n--- want\n%s\n--- got\n%s", name, want, got)
			}
		})
	}
}
