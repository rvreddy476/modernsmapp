package http

// MTube search contracts (2026-09-27, handler_tube.go).
//
// Three claims:
//
//  1. Parameter validation happens BEFORE OpenSearch is asked: an unknown
//     sort/duration/date/feature, or any of them on a non-video type, is a
//     400 and no query is sent.
//  2. The response shapes are pinned as golden JSON under
//     testdata/contracts/mtube/ — the files a client can be built against.
//     Regenerate with `go test ./internal/http/ -run Golden -args -update`
//     and read the diff before committing it.
//  3. The collections query only ever asks for public playlists (the
//     query-time half of the visibility rule).

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/atpost/search-service/internal/store/search"
	"github.com/gin-gonic/gin"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden contract files under testdata/contracts/mtube")

// tubeFakeOS is an OpenSearch stand-in routed by index: each index answers
// with its own canned hits and remembers the last query body it was sent.
type tubeFakeOS struct {
	mu        sync.Mutex
	responses map[string]string
	bodies    map[string]map[string]any
	searches  int
}

func newTubeFakeOS(t *testing.T) (*search.Store, *tubeFakeOS) {
	t.Helper()
	f := &tubeFakeOS{responses: map[string]string{}, bodies: map[string]map[string]any{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/_search") {
			index := strings.Trim(strings.TrimSuffix(r.URL.Path, "/_search"), "/")
			body, _ := io.ReadAll(r.Body)
			f.mu.Lock()
			parsed := map[string]any{}
			_ = json.Unmarshal(body, &parsed)
			f.bodies[index] = parsed
			f.searches++
			resp, ok := f.responses[index]
			f.mu.Unlock()
			if !ok {
				resp = `{"hits":{"total":{"value":0},"hits":[]}}`
			}
			fmt.Fprint(w, resp)
			return
		}
		fmt.Fprint(w, `{"acknowledged":true}`)
	}))
	t.Cleanup(srv.Close)
	store, err := search.New(srv.URL)
	if err != nil {
		t.Fatalf("search.New: %v", err)
	}
	return store, f
}

func (f *tubeFakeOS) respond(index, hitsJSON string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses[index] = hitsJSON
}

func (f *tubeFakeOS) body(index string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[index]
}

func (f *tubeFakeOS) searchCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.searches
}

func tubeRequest(t *testing.T, h *Handler, target string) (int, []byte) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/v1/search/posts", h.SearchPosts)
	r.GET("/v1/search/channels", h.SearchTubeChannels)
	r.GET("/v1/search/collections", h.SearchTubeCollections)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
	return w.Code, w.Body.Bytes()
}

func hits(sources ...string) string {
	return `{"hits":{"total":{"value":` + fmt.Sprint(len(sources)) + `},"hits":[` +
		strings.Join(func() []string {
			out := make([]string, 0, len(sources))
			for _, s := range sources {
				out = append(out, `{"_source":`+s+`}`)
			}
			return out
		}(), ",") + `]}}`
}

// ─── Claim 1: validation before the store ───────────────────────────────

func TestTubeSearch_InvalidParametersAreRefusedBeforeOpenSearch(t *testing.T) {
	store, os := newTubeFakeOS(t)
	h := &Handler{store: store}
	before := os.searchCount()
	cases := []struct{ name, target, wantIn string }{
		{"unknown sort", "/v1/search/posts?q=cats&type=videos&sort=trending", "'sort'"},
		{"unknown duration", "/v1/search/posts?q=cats&type=videos&duration=tiny", "'duration'"},
		{"unknown date", "/v1/search/posts?q=cats&type=videos&date=yesterday", "'date'"},
		{"unknown feature", "/v1/search/posts?q=cats&type=videos&features=8k", "'features'"},
		{"one bad feature among good", "/v1/search/posts?q=cats&type=videos&features=cc,8k", "'features'"},
		{"video filters on the posts type", "/v1/search/posts?q=cats&type=posts&duration=short", "type=videos"},
		{"video filters on the flicks type", "/v1/search/posts?q=cats&type=flicks&sort=views", "type=videos"},
		{"video filters with no type", "/v1/search/posts?q=cats&features=hd", "type=videos"},
		{"channels: empty q", "/v1/search/channels?q=%20", "empty"},
		{"collections: empty q", "/v1/search/collections?limit=5", "empty"},
		{"collections: q too long", "/v1/search/collections?q=" + strings.Repeat("x", 501), "too long"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := tubeRequest(t, h, tc.target)
			if code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", code, body)
			}
			if !strings.Contains(string(body), tc.wantIn) {
				t.Fatalf("body %s does not name the problem (%q)", body, tc.wantIn)
			}
		})
	}
	if os.searchCount() != before {
		t.Fatalf("%d quer(ies) reached OpenSearch for requests that were refused", os.searchCount()-before)
	}
}

func TestTubeSearch_VideoFiltersReachTheQuery(t *testing.T) {
	store, os := newTubeFakeOS(t)
	h := &Handler{store: store}
	code, body := tubeRequest(t, h, "/v1/search/posts?q=cats&type=videos&sort=views&duration=medium&date=week&features=cc,hd")
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, body)
	}
	sent, _ := json.Marshal(os.body(search.IndexPosts))
	for _, want := range []string{`"view_count"`, `"duration_ms":{"gte":240000,"lte":1200000}`, `"created_at":{"gte":"`, `"has_subtitles":true`, `"height":{"gte":720}`, `"content_type":["long_video","video"]`} {
		if !strings.Contains(string(sent), want) {
			t.Fatalf("query sent to OpenSearch lacks %s: %s", want, sent)
		}
	}
	// Without the video parameters the query is exactly the classic one:
	// no sort block, no extra filters.
	if code, _ := tubeRequest(t, h, "/v1/search/posts?q=cats&type=videos"); code != http.StatusOK {
		t.Fatalf("plain videos search status = %d", code)
	}
	plain, _ := json.Marshal(os.body(search.IndexPosts))
	if strings.Contains(string(plain), `"sort"`) || strings.Contains(string(plain), `duration_ms`) {
		t.Fatalf("plain videos search changed shape: %s", plain)
	}
}

// ─── Claim 3: collections are public only ───────────────────────────────

func TestTubeSearch_CollectionsQueryIsPublicOnly(t *testing.T) {
	store, os := newTubeFakeOS(t)
	h := &Handler{store: store}
	if code, body := tubeRequest(t, h, "/v1/search/collections?q=mixes"); code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, body)
	}
	sent, _ := json.Marshal(os.body(search.IndexTubeCollections))
	if !strings.Contains(string(sent), `{"term":{"visibility":"public"}}`) {
		t.Fatalf("collections query does not filter on visibility=public: %s\n"+
			"A playlist made private after it was indexed would stay discoverable.", sent)
	}
	// And a non-public document that somehow came back is still dropped
	// at the row layer.
	os.respond(search.IndexTubeCollections, hits(
		`{"playlist_id":"pub","owner_id":"o","title":"Public","visibility":"public","item_count":2}`,
		`{"playlist_id":"priv","owner_id":"o","title":"Private","visibility":"private","item_count":9}`,
	))
	_, body := tubeRequest(t, h, "/v1/search/collections?q=mixes")
	if strings.Contains(string(body), "priv") || strings.Contains(string(body), "Private") {
		t.Fatalf("a private playlist reached the response: %s", body)
	}
}

// ─── Claim 2: golden contracts ──────────────────────────────────────────

const goldenDir = "testdata/contracts/mtube"

func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join(goldenDir, name)
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, got, "", "  "); err != nil {
		t.Fatalf("response is not JSON: %v\n%s", err, got)
	}
	pretty.WriteByte('\n')
	if *updateGolden {
		if err := os.MkdirAll(goldenDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, pretty.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden %s missing (%v); run with -args -update to create it", path, err)
	}
	var gotAny, wantAny any
	if err := json.Unmarshal(got, &gotAny); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &wantAny); err != nil {
		t.Fatalf("golden %s is not JSON: %v", path, err)
	}
	if !reflect.DeepEqual(gotAny, wantAny) {
		t.Fatalf("response differs from %s.\n--- got ---\n%s\n--- want ---\n%s", path, pretty.String(), want)
	}
}

func TestTubeSearch_GoldenVideos(t *testing.T) {
	store, os := newTubeFakeOS(t)
	os.respond(search.IndexPosts, hits(`{
		"post_id":"6b1f9a2e-0c6d-4a5e-9f3a-1b2c3d4e5f60","author_id":"a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d",
		"author_is_private":false,"is_hidden":false,
		"text":"Weeknight biryani, start to finish #biryani","hashtags":["biryani"],
		"visibility":"public","review_status":"approved","search_rev":3,
		"like_count":12,"comment_count":4,"engagement_score":20,
		"created_at":"2026-09-20T10:00:00Z","published_at":"2026-09-20T10:00:00Z",
		"content_type":"long_video","post_type":"long_video","title":"Weeknight biryani",
		"duration_ms":734000,"media_id":"9f8e7d6c-5b4a-4c3d-8e2f-1a0b9c8d7e6f","media_kind":"video",
		"height":1080,"has_subtitles":true,"view_count":1234
	}`, `{
		"post_id":"7c2a0b3f-1d7e-4b6f-8a4b-2c3d4e5f6a71","author_id":"a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d",
		"author_is_private":false,"is_hidden":false,
		"text":"Old upload, nothing measured yet","visibility":"public","review_status":"approved","search_rev":1,
		"like_count":0,"comment_count":0,"engagement_score":0,
		"created_at":"2026-09-01T08:30:00Z","content_type":"long_video","post_type":"long_video","title":"Untitled walk"
	}`))
	os.respond(search.IndexUsers, hits(`{"user_id":"a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d","username":"asha","display_name":"Asha K","avatar_media_id":""}`))
	h := &Handler{store: store}
	code, body := tubeRequest(t, h, "/v1/search/posts?q=biryani&type=videos&sort=views&duration=medium&features=cc,hd")
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, body)
	}
	checkGolden(t, "search_videos.json", body)
}

func TestTubeSearch_GoldenChannels(t *testing.T) {
	store, os := newTubeFakeOS(t)
	os.respond(search.IndexTubeChannels, hits(`{
		"channel_id":"3d4e5f6a-7b8c-4d9e-8f0a-1b2c3d4e5f6a","owner_id":"a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d",
		"name":"Cooking with Asha","handle":"asha","about":"Weeknight food, no fuss",
		"avatar_media_id":"5e6f7a8b-9c0d-4e1f-8a2b-3c4d5e6f7a8b","follower_count":4200,
		"created_at":"2026-09-05T12:00:00Z","updated_at":"2026-09-20T12:00:00Z"
	}`, `{
		"channel_id":"4e5f6a7b-8c9d-4e0f-9a1b-2c3d4e5f6a7b","owner_id":"b2c3d4e5-f6a7-4b8c-9d0e-1f2a3b4c5d6e",
		"name":"Asha's Garage","handle":"ashagarage","follower_count":0,
		"created_at":"2026-09-25T12:00:00Z","updated_at":"2026-09-25T12:00:00Z"
	}`))
	h := &Handler{store: store}
	code, body := tubeRequest(t, h, "/v1/search/channels?q=asha&limit=2")
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, body)
	}
	checkGolden(t, "search_channels.json", body)
}

func TestTubeSearch_GoldenCollections(t *testing.T) {
	store, os := newTubeFakeOS(t)
	os.respond(search.IndexTubeCollections, hits(`{
		"playlist_id":"8d9e0f1a-2b3c-4d5e-9f6a-7b8c9d0e1f2a","owner_id":"a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d",
		"title":"Biryani, every way","description":"Dum, pot, pressure cooker","visibility":"public","item_count":7,
		"cover_media_id":"6f7a8b9c-0d1e-4f2a-8b3c-4d5e6f7a8b9c",
		"created_at":"2026-09-10T12:00:00Z","updated_at":"2026-09-22T12:00:00Z"
	}`))
	h := &Handler{store: store}
	code, body := tubeRequest(t, h, "/v1/search/collections?q=biryani")
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, body)
	}
	checkGolden(t, "search_collections.json", body)
}
