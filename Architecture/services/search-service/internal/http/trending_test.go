package http

// GET /v1/discover/trending (reels-19 / V4-4, store/search/trending.go).
//
// The list is an aggregation over posts_v1 restricted to public, approved
// documents of the last 24 hours, never post-service's Redis counter, which
// counted private, pending, rejected and scheduled posts and was never
// decremented. A store failure answers the standard envelope with a fixed
// message, never the OpenSearch error text.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/atpost/search-service/internal/store/search"
	"github.com/gin-gonic/gin"
)

func trendingRequest(t *testing.T, h *Handler) (int, []byte) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/v1/discover/trending", h.GetTrending)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/discover/trending", nil))
	return w.Code, w.Body.Bytes()
}

func TestTrending_AggregatesOnlyPublicApprovedRecentPosts(t *testing.T) {
	store, os := newTubeFakeOS(t)
	os.respond(search.IndexPosts, `{"hits":{"total":{"value":0},"hits":[]},
		"aggregations":{"trending":{"buckets":[{"key":"diwali","doc_count":7},{"key":"cricket","doc_count":3}]}}}`)
	// No Redis client: the handler must not need one.
	h := &Handler{store: store}

	code, body := trendingRequest(t, h)
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	var resp struct {
		Data struct {
			Trending []struct {
				Hashtag string  `json:"hashtag"`
				Score   float64 `json:"score"`
			} `json:"trending"`
		} `json:"data"`
		Trending []struct {
			Hashtag string  `json:"hashtag"`
			Score   float64 `json:"score"`
		} `json:"trending"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v: %s", err, body)
	}
	items := resp.Trending
	if len(items) == 0 {
		items = resp.Data.Trending
	}
	if len(items) != 2 || items[0].Hashtag != "diwali" || items[0].Score != 7 || items[1].Hashtag != "cricket" {
		t.Fatalf("trending = %+v (%s)", items, body)
	}

	sent, _ := json.Marshal(os.body(search.IndexPosts))
	for _, must := range []string{
		`{"term":{"visibility":"public"}}`,
		`{"term":{"review_status":"approved"}}`,
		`"created_at":{"gte":`,
		`"is_hidden"`, // hidden authors excluded, as on every posts query
		// Viewer scope applied on author_id (blocks and private authors
		// when the route's middleware resolved a scope); Echo notes stay out.
		`{"term":{"content_type":"` + search.NoteContentType + `"}}`,
	} {
		if !strings.Contains(string(sent), must) {
			t.Fatalf("trending query lacks %s: %s", must, sent)
		}
	}
}

func TestTrending_EmptyIsAnArray(t *testing.T) {
	store, _ := newTubeFakeOS(t)
	h := &Handler{store: store}
	code, body := trendingRequest(t, h)
	if code != http.StatusOK || !strings.Contains(string(body), `"trending":[]`) {
		t.Fatalf("status %d body %s", code, body)
	}
}

const leakSecret = "index_not_found_exception internal-host-10.0.3.7 posts_v1"

// failingStore answers every search and alias call with a 500 carrying
// internal detail that must never reach a client.
func failingStore(t *testing.T) *search.Store {
	t.Helper()
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		_, _ = io.Copy(io.Discard, r.Body)
		if strings.HasSuffix(r.URL.Path, "/_search") || strings.Contains(r.URL.Path, "_alias") {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, `{"error":{"type":%q}}`, leakSecret)
			return
		}
		fmt.Fprint(w, `{"acknowledged":true}`)
	}))
	t.Cleanup(srv.Close)
	store, err := search.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func assertNoLeak(t *testing.T, code int, body []byte, wantCode int) {
	t.Helper()
	if code != wantCode {
		t.Fatalf("status %d: %s", code, body)
	}
	if strings.Contains(string(body), "internal-host") || strings.Contains(string(body), "index_not_found") {
		t.Fatalf("%d body leaks the store error: %s", code, body)
	}
}

func TestProductsAlias_FailuresDoNotLeakTheStoreError(t *testing.T) {
	h := &Handler{store: failingStore(t)}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/alias", h.ProductsAlias)
	r.POST("/alias", h.MoveProductsAlias)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/alias", nil))
	assertNoLeak(t, w.Code, w.Body.Bytes(), http.StatusInternalServerError)

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/alias",
		strings.NewReader(`{"index":"`+search.IndexProductsV2+`"}`)))
	assertNoLeak(t, w.Code, w.Body.Bytes(), http.StatusInternalServerError)
}

func TestTrending_FailureDoesNotLeakTheStoreError(t *testing.T) {
	code, body := trendingRequest(t, &Handler{store: failingStore(t)})
	assertNoLeak(t, code, body, http.StatusInternalServerError)
	if !strings.Contains(string(body), "INTERNAL_ERROR") {
		t.Fatalf("500 body is not the standard envelope: %s", body)
	}
}
