package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// GET /v1/channels/:ref/feed (RSS publishing, 2026-09-29) through the REAL
// router with in-memory stores behind service.NewForHandlerTests: the 200
// shape and headers, 404 (no channel, hidden owner, private owner: one
// answer), 400 for the category and the limit, and X-User-Id ignored. The
// rules themselves are proved in service/channel_feed_test.go and, for the
// SQL, in store/postgres/channels_feed_integration_test.go.

type feedRouteChannels struct {
	*esChannels
	byHandle map[string]*postgres.Channel
}

func (f *feedRouteChannels) GetChannelByHandle(_ context.Context, handle string) (*postgres.Channel, error) {
	if ch, ok := f.byHandle[handle]; ok {
		cp := *ch
		return &cp, nil
	}
	return nil, nil
}

type feedRouteStore struct {
	mu    sync.Mutex
	rows  map[uuid.UUID][]postgres.ChannelFeedVideo
	facts postgres.ChannelFeedFacts
	asked []feedRouteAsk
}

type feedRouteAsk struct {
	owner    uuid.UUID
	category string
	limit    int
}

func (f *feedRouteStore) ListChannelFeedVideos(_ context.Context, owner uuid.UUID, category string, limit int) ([]postgres.ChannelFeedVideo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, feedRouteAsk{owner, category, limit})
	return append([]postgres.ChannelFeedVideo(nil), f.rows[owner]...), nil
}

func (f *feedRouteStore) ChannelFeedFacts(context.Context, uuid.UUID) (postgres.ChannelFeedFacts, error) {
	return f.facts, nil
}

type feedRouteMedia struct {
	records map[uuid.UUID]*service.FeedMediaRecord
}

func (f *feedRouteMedia) MediaRecord(_ context.Context, id uuid.UUID) (*service.FeedMediaRecord, error) {
	if rec, ok := f.records[id]; ok {
		return rec, nil
	}
	return nil, context.DeadlineExceeded
}

type feedRouteHidden struct{ hidden map[uuid.UUID]bool }

func (f *feedRouteHidden) AnyHidden(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := map[uuid.UUID]bool{}
	for _, id := range ids {
		if f.hidden[id] {
			out[id] = true
		}
	}
	return out, nil
}

type feedRouteRig struct {
	router  *gin.Engine
	store   *feedRouteStore
	hidden  *feedRouteHidden
	private map[uuid.UUID]bool
	mu      sync.Mutex
	viewers []string // every viewer_id the graph was asked about
}

// newFeedRouteRig serves the golden fixture's channel: raghu.builds, owned
// by fxAuthor, with the fixture's two episodes behind it, plus one unlisted
// video of the owner's that the fake store (wrongly) returns.
func newFeedRouteRig(t *testing.T) *feedRouteRig {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := &feedRouteRig{
		store:  &feedRouteStore{rows: map[uuid.UUID][]postgres.ChannelFeedVideo{}, facts: postgres.ChannelFeedFacts{Language: "en", DominantCategory: "science-tech"}},
		hidden: &feedRouteHidden{hidden: map[uuid.UUID]bool{}}, private: map[uuid.UUID]bool{},
	}
	owner := &postgres.Channel{ID: uuid.New(), UserID: fxAuthor, Name: "Raghu Builds", Handle: "raghu.builds", About: "Weekly builds",
		AvatarMediaID: &fxBanner, ContactEmail: "hello@example.com", UpdatedAt: fxTime}
	chans := &feedRouteChannels{
		esChannels: &esChannels{subs: map[[2]uuid.UUID]bool{}, byUser: map[uuid.UUID]*postgres.Channel{fxAuthor: owner}},
		byHandle:   map[string]*postgres.Channel{"raghu.builds": owner},
	}
	friday, thursday := fxTime.Add(-time.Hour), fxTime.Add(-25*time.Hour)
	unlistedMedia := uuid.New()
	r.store.rows[fxAuthor] = []postgres.ChannelFeedVideo{
		{Post: postgres.Post{ID: fxPost, AuthorID: fxAuthor, Visibility: "public", ContentType: "long_video", ReviewStatus: "approved",
			Title: "Friday build", Text: "0:00 Intro\n1:23 Setup\n12:05 The build", Category: "science-tech", Language: "en",
			Hashtags: []string{"build"}, CoverMediaID: &fxCover, CreatedAt: friday, UpdatedAt: friday, PublishedAt: &friday},
			MediaID: &fxMedia, DurationMs: 725000, ProcessingStatus: "ready", ModerationStatus: "passed"},
		{Post: postgres.Post{ID: uuid.New(), AuthorID: fxAuthor, Visibility: "unlisted", ContentType: "long_video", ReviewStatus: "approved",
			Title: "Link only", CreatedAt: friday, UpdatedAt: friday, PublishedAt: &friday},
			MediaID: &unlistedMedia, DurationMs: 1000, ProcessingStatus: "ready", ModerationStatus: "passed"},
		{Post: postgres.Post{ID: fxRelated, AuthorID: fxAuthor, Visibility: "public", ContentType: "long_video", ReviewStatus: "approved",
			Title: "Thursday talk", Category: "podcasts", CreatedAt: thursday, UpdatedAt: thursday, PublishedAt: &thursday},
			MediaID: &fxStream, DurationMs: 3600000, ProcessingStatus: "ready", ModerationStatus: "passed"},
	}
	size720 := int64(184320000)
	media := &feedRouteMedia{records: map[uuid.UUID]*service.FeedMediaRecord{
		fxMedia: {FileType: "video", MimeType: "video/quicktime", FileSizeBytes: 500000000, ProcessingStatus: "ready", ModerationStatus: "passed",
			Variants: []service.FeedMediaVariant{{Name: "720p", Mime: "video/mp4", SizeBytes: &size720, ObjectKey: "user/x/720p"}}},
		fxStream:      {FileType: "video", MimeType: "video/quicktime", FileSizeBytes: 912680550, ProcessingStatus: "ready", ModerationStatus: "passed"},
		unlistedMedia: {FileType: "video", MimeType: "video/mp4", FileSizeBytes: 10, ProcessingStatus: "ready", ModerationStatus: "passed"},
	}}
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			ViewerID  string   `json:"viewer_id"`
			TargetIDs []string `json:"target_ids"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		r.mu.Lock()
		r.viewers = append(r.viewers, body.ViewerID)
		r.mu.Unlock()
		out := map[string]bool{}
		for _, id := range body.TargetIDs {
			out[id] = !r.private[uuid.MustParse(id)]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": out})
	}))
	t.Cleanup(graph.Close)
	deps := service.HandlerTestDeps{
		Channels: chans, ChannelFeed: r.store, FeedMedia: media, HiddenAuthors: r.hidden,
		GraphServiceURL: graph.URL, Now: func() time.Time { return fxTime },
	}
	r.router = gin.New()
	New(service.NewForHandlerTests(deps), nil).RegisterRoutes(r.router)
	return r
}

func (r *feedRouteRig) get(path string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	r.router.ServeHTTP(w, req)
	return w
}

func TestChannelFeedRouteRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	(&Handler{}).RegisterRoutes(r)
	for _, info := range r.Routes() {
		if info.Method == http.MethodGet && info.Path == "/v1/channels/:ref/feed" {
			return
		}
	}
	t.Fatal("GET /v1/channels/:ref/feed is not registered")
}

// The 200 body IS the golden fixture the web builds against: same keys,
// same values, through the real handler and service.
func TestChannelFeedAnswersTheGoldenFixture(t *testing.T) {
	r := newFeedRouteRig(t)
	for _, ref := range []string{"raghu.builds", "@raghu.builds", "Raghu.Builds", fxAuthor.String()} {
		w := r.get("/v1/channels/" + ref + "/feed")
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", ref, w.Code, w.Body.String())
		}
		var env struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile(filepath.Join("testdata", "contracts", "mtube", "channel_feed.json"))
		if err != nil {
			t.Fatal(err)
		}
		var got, golden any
		if err := json.Unmarshal(env.Data, &got); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(want, &golden); err != nil {
			t.Fatal(err)
		}
		gb, _ := json.Marshal(got)
		wb, _ := json.Marshal(golden)
		if string(gb) != string(wb) {
			t.Fatalf("%s: the route does not answer the fixture.\n--- want\n%s\n--- got\n%s", ref, wb, gb)
		}
	}
}

// Every key the plan pins is on the wire, and nothing else: a field added
// to the structs without a contract change fails here.
func TestChannelFeedWireKeysArePinned(t *testing.T) {
	r := newFeedRouteRig(t)
	w := r.get("/v1/channels/raghu.builds/feed")
	var env struct {
		Data struct {
			Channel map[string]json.RawMessage `json:"channel"`
			Items   []map[string]json.RawMessage
		} `json:"data"`
	}
	var top struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &top); err != nil {
		t.Fatal(err)
	}
	keys := func(m map[string]json.RawMessage) []string {
		out := make([]string, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	same := func(name string, got, want []string) {
		t.Helper()
		sort.Strings(want)
		if len(got) != len(want) {
			t.Fatalf("%s keys = %v, want %v", name, got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("%s keys = %v, want %v", name, got, want)
			}
		}
	}
	same("feed", keys(top.Data), []string{"channel", "category", "updated_at", "items"})
	same("channel", keys(env.Data.Channel), []string{"user_id", "name", "handle", "about", "avatar_media_id", "avatar_url",
		"contact_email", "language", "dominant_category"})
	if len(env.Data.Items) != 2 {
		t.Fatalf("%d items", len(env.Data.Items))
	}
	same("item", keys(env.Data.Items[0]), []string{"id", "title", "text", "category", "language", "hashtags", "published_at",
		"media_id", "duration_ms", "cover_media_id", "enclosure"})
	var enc map[string]json.RawMessage
	if err := json.Unmarshal(env.Data.Items[0]["enclosure"], &enc); err != nil {
		t.Fatal(err)
	}
	same("enclosure", keys(enc), []string{"variant", "path", "mime", "size_bytes"})
}

func TestChannelFeedIsPubliclyCacheable(t *testing.T) {
	r := newFeedRouteRig(t)
	w := r.get("/v1/channels/raghu.builds/feed")
	if got := w.Header().Get("Cache-Control"); got != "public, max-age=300" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := w.Header().Values("Vary"); len(got) != 0 {
		t.Fatalf("Vary = %v: the feed must not vary by identity", got)
	}
	if got := w.Header().Get("Set-Cookie"); got != "" {
		t.Fatalf("a cacheable document set a cookie: %q", got)
	}
	// An error is not given the public policy.
	miss := r.get("/v1/channels/nobody.here/feed")
	if got := miss.Header().Get("Cache-Control"); got == "public, max-age=300" {
		t.Fatalf("a 404 carried the feed's cache policy")
	}
}

// One document for everyone: the owner, a signed-in stranger and nobody at
// all get the same bytes, the unlisted video is in none of them, and every
// question to the graph was asked as the nil viewer.
func TestChannelFeedIgnoresXUserID(t *testing.T) {
	r := newFeedRouteRig(t)
	anonymous := r.get("/v1/channels/raghu.builds/feed")
	if anonymous.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", anonymous.Code, anonymous.Body.String())
	}
	var want struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(anonymous.Body.Bytes(), &want); err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]string{"the owner": fxAuthor.String(), "a stranger": fxViewer.String(), "garbage": "not-a-uuid"} {
		w := r.get("/v1/channels/raghu.builds/feed", "X-User-Id", id)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", name, w.Code, w.Body.String())
		}
		var got struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if string(got.Data) != string(want.Data) {
			t.Fatalf("%s got a different document.\n--- anonymous\n%s\n--- %s\n%s", name, want.Data, name, got.Data)
		}
		if w.Header().Get("Cache-Control") != "public, max-age=300" {
			t.Fatalf("%s: Cache-Control = %q", name, w.Header().Get("Cache-Control"))
		}
	}
	var feed service.ChannelFeed
	if err := json.Unmarshal(want.Data, &feed); err != nil {
		t.Fatal(err)
	}
	for _, it := range feed.Items {
		if it.Title == "Link only" {
			t.Fatalf("the unlisted video is in the feed")
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.viewers) == 0 {
		t.Fatal("the account gate was never asked")
	}
	for _, v := range r.viewers {
		if v != uuid.Nil.String() {
			t.Fatalf("the account gate was asked as %q; the feed is always the nil stranger's", v)
		}
	}
}

func TestChannelFeedNotFoundIsOneAnswer(t *testing.T) {
	r := newFeedRouteRig(t)
	none := r.get("/v1/channels/nobody.here/feed")
	if none.Code != http.StatusNotFound || errorCode(t, none) != "NOT_FOUND" {
		t.Fatalf("no channel: %d %s", none.Code, none.Body.String())
	}
	want := errorBodyWithoutMeta(t, none)

	check := func(name string, w *httptest.ResponseRecorder) {
		t.Helper()
		if w.Code != http.StatusNotFound || errorCode(t, w) != "NOT_FOUND" {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body.String())
		}
		if got := errorBodyWithoutMeta(t, w); got != want {
			t.Fatalf("%s answers %s; no channel answers %s: they must be indistinguishable", name, got, want)
		}
		if w.Header().Get("Cache-Control") != none.Header().Get("Cache-Control") {
			t.Fatalf("%s: headers differ from no channel", name)
		}
	}
	for _, ref := range []string{"x", "has%20space", uuid.NewString()} {
		check("ref "+ref, r.get("/v1/channels/"+ref+"/feed"))
	}

	r.hidden.hidden[fxAuthor] = true
	check("hidden owner", r.get("/v1/channels/raghu.builds/feed"))
	check("hidden owner by id", r.get("/v1/channels/"+fxAuthor.String()+"/feed"))
	// The owner asking for their own hidden channel gets the same answer.
	check("hidden owner, asked by the owner", r.get("/v1/channels/raghu.builds/feed", "X-User-Id", fxAuthor.String()))
	if len(r.store.asked) != 0 {
		t.Fatalf("a refused feed read the videos: %+v", r.store.asked)
	}

	// A fresh rig: the account gate's answer is cached for 3 s per service.
	rig := newFeedRouteRig(t)
	rig.private[fxAuthor] = true
	check("private owner", rig.get("/v1/channels/raghu.builds/feed"))
	check("private owner, asked by the owner", rig.get("/v1/channels/raghu.builds/feed", "X-User-Id", fxAuthor.String()))
	if len(rig.store.asked) != 0 {
		t.Fatalf("a refused feed read the videos: %+v", rig.store.asked)
	}
}

// errorBodyWithoutMeta is the error member of the envelope, which is what a
// caller can compare: meta carries a per-request id.
func errorBodyWithoutMeta(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return string(env.Error)
}

func TestChannelFeedBadRequests(t *testing.T) {
	r := newFeedRouteRig(t)
	for _, tc := range []struct{ query, code string }{
		{"category=cooking-shows", "INVALID_CATEGORY"},
		{"category=film%20%26%20animation", "INVALID_CATEGORY"},
		{"category=..%2Fx", "INVALID_CATEGORY"},
		{"limit=-1", "INVALID_REQUEST"},
		{"limit=ten", "INVALID_REQUEST"},
		{"limit=1.5", "INVALID_REQUEST"},
	} {
		for _, ref := range []string{"raghu.builds", "nobody.here"} {
			w := r.get("/v1/channels/" + ref + "/feed?" + tc.query)
			if w.Code != http.StatusBadRequest || errorCode(t, w) != tc.code {
				t.Fatalf("%s ?%s: %d %s, want 400 %s", ref, tc.query, w.Code, w.Body.String(), tc.code)
			}
		}
	}
	if len(r.store.asked) != 0 {
		t.Fatalf("a refused request reached the store: %+v", r.store.asked)
	}
}

func TestChannelFeedPassesCategoryAndLimit(t *testing.T) {
	r := newFeedRouteRig(t)
	for _, tc := range []struct {
		query string
		want  feedRouteAsk
	}{
		{"", feedRouteAsk{fxAuthor, "", 50}},
		{"?category=podcasts", feedRouteAsk{fxAuthor, "podcasts", 50}},
		{"?category=Podcasts&limit=5", feedRouteAsk{fxAuthor, "podcasts", 5}},
		{"?limit=0", feedRouteAsk{fxAuthor, "", 50}},
		{"?limit=51", feedRouteAsk{fxAuthor, "", 50}},
		{"?limit=100000", feedRouteAsk{fxAuthor, "", 50}},
	} {
		r.store.asked = nil
		w := r.get("/v1/channels/raghu.builds/feed" + tc.query)
		if w.Code != http.StatusOK {
			t.Fatalf("%q: %d %s", tc.query, w.Code, w.Body.String())
		}
		if len(r.store.asked) != 1 || r.store.asked[0] != tc.want {
			t.Fatalf("%q: store asked %+v, want %+v", tc.query, r.store.asked, tc.want)
		}
		var env struct {
			Data struct {
				Category string `json:"category"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil || env.Data.Category != tc.want.category {
			t.Fatalf("%q: category echoed as %q (%v)", tc.query, env.Data.Category, err)
		}
	}
}

// The sibling routes under /:ref still dispatch to their own handlers.
func TestChannelFeedDoesNotShadowItsSiblings(t *testing.T) {
	r := newFeedRouteRig(t)
	if w := r.get("/v1/channels/raghu.builds"); w.Code != http.StatusOK {
		t.Fatalf("GET /v1/channels/:ref: %d %s", w.Code, w.Body.String())
	}
	if w := r.get("/v1/channels/raghu.builds/subscription"); w.Code != http.StatusUnauthorized {
		t.Fatalf("GET /v1/channels/:ref/subscription without a caller: %d", w.Code)
	}
}
