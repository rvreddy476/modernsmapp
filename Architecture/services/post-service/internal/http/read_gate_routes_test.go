package http

import (
	"context"
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

// The post detail's read gate on the single-post routes that had none
// (2026-09-29, the Creator Hub audit's leak list). Each route runs the gate
// before anything else touches the post and answers its refusal exactly as
// GET /v1/posts/:postId does. The service here has no stores behind it, so
// a route that got past a refusal would reach a nil store and come back as
// a recovered 500, never as the 404 / 403 asserted.

type gatedRoute struct {
	method, path, body string
	signedIn           bool
}

func gatedRoutes(post, other uuid.UUID) []gatedRoute {
	p, x := post.String(), other.String()
	return []gatedRoute{
		{"GET", "/v1/posts/" + p + "/chapters", "", false},
		{"GET", "/v1/posts/" + p + "/product-tags", "", false},
		{"POST", "/v1/posts/" + p + "/product-tags/" + x + "/impression", "", false},
		{"POST", "/v1/posts/" + p + "/product-tags/" + x + "/click", "", false},
		{"GET", "/v1/posts/" + p + "/poll", "", false},
		{"GET", "/v1/posts/" + p + "/poll/results", "", false},
		{"POST", "/v1/posts/" + p + "/vote", `{"option_id":"` + x + `"}`, true},
		{"POST", "/v1/posts/" + p + "/poll/vote", `{"option_id":"` + x + `"}`, true},
		{"POST", "/v1/posts/" + p + "/reactions", `{"reaction":"like"}`, true},
		{"GET", "/v1/posts/" + p + "/reactions/me", "", true},
		{"POST", "/v1/posts/" + p + "/react", `{"reaction_type":"like"}`, true},
		{"GET", "/v1/posts/" + p + "/reactions/counts", "", false},
		{"GET", "/v1/posts/" + p + "/reposters", "", false},
		{"GET", "/v1/posts/" + p + "/remix-token", "", false},
		{"POST", "/v1/posts/" + p + "/tune", "", true},
		{"GET", "/v1/posts/" + p + "/tune/me", "", true},
		{"POST", "/v1/videos/" + p + "/progress", `{"position_ms":1000,"duration_ms":0}`, true},
		{"GET", "/v1/videos/" + p + "/progress", "", true},
		{"POST", "/v1/saved", `{"target_type":"post","target_id":"` + p + `"}`, true},
		{"POST", "/v1/reels/" + p + "/react", `{"reaction":"like"}`, true},
		{"GET", "/v1/reels/" + p + "/react/me", "", true},
		{"POST", "/v1/reels/" + p + "/comments", `{"text":"hi"}`, true},
		{"GET", "/v1/reels/" + p + "/comments", "", false},
		{"POST", "/v1/reels/" + p + "/share", `{}`, true},
		{"POST", "/v1/reels/" + p + "/save", "", true},
		{"GET", "/v1/reels/" + p + "/saved", "", true},
		{"GET", "/v1/reels/" + p + "/counts", "", false},
		{"GET", "/v1/posts/" + p + "/end-screens", "", false},
		{"GET", "/v1/posts/" + p + "/cards", "", false},
	}
}

type gateRig struct {
	router *gin.Engine
	calls  []esGateCall
}

func newGateRig(t *testing.T, gate func(uuid.UUID, *uuid.UUID) error, extra func(*service.HandlerTestDeps)) *gateRig {
	t.Helper()
	gin.SetMode(gin.TestMode)
	g := &gateRig{}
	deps := service.HandlerTestDeps{
		Now: func() time.Time { return time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC) },
		ReadGate: func(_ context.Context, id uuid.UUID, viewer *uuid.UUID) error {
			g.calls = append(g.calls, esGateCall{post: id, viewer: viewer})
			return gate(id, viewer)
		},
	}
	if extra != nil {
		extra(&deps)
	}
	g.router = gin.New()
	g.router.Use(gin.CustomRecovery(func(c *gin.Context, _ any) {
		c.AbortWithStatus(http.StatusInternalServerError)
	}))
	h := New(service.NewForHandlerTests(deps), nil)
	h.RegisterRoutes(g.router)
	h.RegisterRepostRoutes(g.router)
	h.RegisterReelEngagementRoutes(g.router)
	return g
}

func (g *gateRig) do(method, path string, caller *uuid.UUID, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if caller != nil {
		req.Header.Set("X-User-Id", caller.String())
	}
	w := httptest.NewRecorder()
	g.router.ServeHTTP(w, req)
	return w
}

// Every route answers the detail's refusal, for the post named in the path,
// as the caller.
func TestSinglePostRoutesRunTheDetailGate(t *testing.T) {
	post, other, viewer := uuid.New(), uuid.New(), uuid.New()
	for _, tc := range []struct {
		refusal error
		status  int
		code    string
	}{
		{service.ErrPostNotVisible, 404, "NOT_FOUND"},
		{service.ErrAgeRestricted, 403, "AGE_RESTRICTED"},
		{service.ErrAgeUnverified, 403, "AGE_UNVERIFIED"},
	} {
		for _, rt := range gatedRoutes(post, other) {
			t.Run(tc.code+" "+rt.method+" "+rt.path, func(t *testing.T) {
				refusal := tc.refusal
				g := newGateRig(t, func(uuid.UUID, *uuid.UUID) error { return refusal }, nil)
				w := g.do(rt.method, rt.path, &viewer, rt.body)
				if w.Code != tc.status || errorCode(t, w) != tc.code {
					t.Fatalf("got %d %s want %d %s", w.Code, w.Body.String(), tc.status, tc.code)
				}
				if len(g.calls) == 0 || g.calls[0].post != post || g.calls[0].viewer == nil || *g.calls[0].viewer != viewer {
					t.Fatalf("gate calls %+v; want the path's post as the caller", g.calls)
				}
			})
		}
	}
	// Anonymous callers on the open routes get the sign-in refusal.
	for _, rt := range gatedRoutes(post, other) {
		if rt.signedIn {
			continue
		}
		g := newGateRig(t, func(_ uuid.UUID, v *uuid.UUID) error {
			if v == nil {
				return service.ErrAgeSignIn
			}
			return nil
		}, nil)
		if w := g.do(rt.method, rt.path, nil, rt.body); w.Code != 401 || errorCode(t, w) != "AGE_RESTRICTED_SIGN_IN" {
			t.Fatalf("anonymous %s %s: %d %s", rt.method, rt.path, w.Code, w.Body.String())
		}
	}
}

// DELETEs of the caller's own state stay open: they reveal nothing, and a
// viewer who lost access must still be able to clear what they left.
func TestOwnStateDeletesAreNotGated(t *testing.T) {
	post, viewer := uuid.New(), uuid.New()
	g := newGateRig(t, func(uuid.UUID, *uuid.UUID) error { return service.ErrPostNotVisible }, nil)
	for _, path := range []string{"/v1/posts/" + post.String() + "/tune", "/v1/videos/" + post.String() + "/progress", "/v1/reels/" + post.String() + "/save"} {
		g.do(http.MethodDelete, path, &viewer, "")
	}
	if len(g.calls) != 0 {
		t.Fatalf("own-state deletes ran the gate: %+v", g.calls)
	}
}

// The batch counts drop the reels the caller may not open, and are bounded.
func TestReelBatchCountsDropsUnreadableReels(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	g := newGateRig(t, func(uuid.UUID, *uuid.UUID) error { return service.ErrPostNotVisible }, nil)
	w := g.do(http.MethodPost, "/v1/reels/batch/counts", nil, `{"reel_ids":["`+a.String()+`","`+b.String()+`"]}`)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), a.String()) || strings.Contains(w.Body.String(), b.String()) {
		t.Fatalf("batch counts for unreadable reels: %d %s", w.Code, w.Body.String())
	}
	if len(g.calls) != 2 {
		t.Fatalf("gate calls %d want 2", len(g.calls))
	}
	many := make([]string, 101)
	for i := range many {
		many[i] = `"` + uuid.NewString() + `"`
	}
	expectCode(t, g.do(http.MethodPost, "/v1/reels/batch/counts", nil, `{"reel_ids":[`+strings.Join(many, ",")+`]}`), 400, "INVALID_REQUEST")
}

// ── episode lists ───────────────────────────────────────────────────────────

type gateSeriesStore struct {
	series   *postgres.VideoSeries
	episodes []postgres.VideoSeriesEpisode
}

func (f *gateSeriesStore) GetVideoSeries(_ context.Context, id uuid.UUID) (*postgres.VideoSeries, error) {
	if f.series.ID == id {
		cp := *f.series
		return &cp, nil
	}
	return nil, nil
}
func (f *gateSeriesStore) ListVideoSeriesByCreator(context.Context, uuid.UUID, int, int) ([]postgres.VideoSeries, error) {
	return nil, nil
}
func (f *gateSeriesStore) GetVideoSeriesEpisodes(context.Context, uuid.UUID, bool) ([]postgres.VideoSeriesEpisode, error) {
	return append([]postgres.VideoSeriesEpisode(nil), f.episodes...), nil
}
func (f *gateSeriesStore) AddEpisodeToVideoSeries(context.Context, uuid.UUID, uuid.UUID, int, *string) (*postgres.VideoSeriesEpisode, error) {
	return nil, nil
}
func (f *gateSeriesStore) FindVideoSeriesEpisodeByPost(context.Context, uuid.UUID, uuid.UUID) (*postgres.VideoSeriesEpisode, error) {
	return nil, nil
}
func (f *gateSeriesStore) FindSeriesMembershipsByPost(_ context.Context, post uuid.UUID) ([]postgres.VideoSeriesEpisode, error) {
	for _, ep := range f.episodes {
		if ep.PostID == post {
			return []postgres.VideoSeriesEpisode{ep}, nil
		}
	}
	return nil, nil
}
func (f *gateSeriesStore) UpdateVideoSeries(context.Context, uuid.UUID, postgres.VideoSeriesPatch) (*postgres.VideoSeries, error) {
	return nil, nil
}
func (f *gateSeriesStore) DeleteVideoSeries(context.Context, uuid.UUID) error { return nil }
func (f *gateSeriesStore) DeleteVideoSeriesEpisodeByNum(context.Context, uuid.UUID, int) (bool, error) {
	return false, nil
}
func (f *gateSeriesStore) DeleteVideoSeriesEpisodeByPost(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return false, nil
}

type gateFlickStore struct {
	series *postgres.FlickSeries
	items  []postgres.FlickSeriesItem
}

func (f *gateFlickStore) GetFlickSeries(_ context.Context, id uuid.UUID) (*postgres.FlickSeries, error) {
	if f.series.ID == id {
		cp := *f.series
		return &cp, nil
	}
	return nil, nil
}
func (f *gateFlickStore) GetSeriesEpisodes(context.Context, uuid.UUID) ([]postgres.FlickSeriesItem, error) {
	return append([]postgres.FlickSeriesItem(nil), f.items...), nil
}

// Every list of episodes drops the ones the caller may not open; the
// series owner sees all of them.
func TestEpisodeListsDropUnreadableEpisodes(t *testing.T) {
	owner, viewer := uuid.New(), uuid.New()
	e1, e2, e3 := uuid.New(), uuid.New(), uuid.New()
	hidden := map[uuid.UUID]bool{e2: true}
	vs := &postgres.VideoSeries{ID: uuid.New(), CreatorID: owner, Title: "S", IsPublic: true}
	vstore := &gateSeriesStore{series: vs, episodes: []postgres.VideoSeriesEpisode{
		{SeriesID: vs.ID, PostID: e1, EpisodeNum: 1}, {SeriesID: vs.ID, PostID: e2, EpisodeNum: 2}, {SeriesID: vs.ID, PostID: e3, EpisodeNum: 3}}}
	fs := &postgres.FlickSeries{ID: uuid.New(), CreatorID: owner, Title: "F"}
	fstore := &gateFlickStore{series: fs, items: []postgres.FlickSeriesItem{
		{SeriesID: fs.ID, PostID: e1, EpisodeNum: 1}, {SeriesID: fs.ID, PostID: e2, EpisodeNum: 2}, {SeriesID: fs.ID, PostID: e3, EpisodeNum: 3}}}
	g := newGateRig(t, func(id uuid.UUID, v *uuid.UUID) error {
		if hidden[id] && (v == nil || *v != owner) {
			return service.ErrPostNotVisible
		}
		return nil
	}, func(d *service.HandlerTestDeps) {
		d.VideoSeries = vstore
		d.FlickSeries = fstore
	})
	for _, path := range []string{"/v1/video-series/" + vs.ID.String() + "/episodes", "/v1/series/" + fs.ID.String() + "/episodes", "/v1/posts/" + e1.String() + "/series"} {
		for _, caller := range []*uuid.UUID{&viewer, nil} {
			w := g.do(http.MethodGet, path, caller, "")
			if w.Code != http.StatusOK || strings.Contains(w.Body.String(), e2.String()) || !strings.Contains(w.Body.String(), e3.String()) {
				t.Fatalf("%s for %v: %d %s", path, caller, w.Code, w.Body.String())
			}
		}
		w := g.do(http.MethodGet, path, &owner, "")
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), e2.String()) {
			t.Fatalf("%s for the owner: %d %s", path, w.Code, w.Body.String())
		}
	}
	// The watch page's next from episode 1 skips the hidden episode 2.
	w := g.do(http.MethodGet, "/v1/posts/"+e1.String()+"/series", &viewer, "")
	if !strings.Contains(w.Body.String(), `"next":{"post_id":"`+e3.String()) {
		t.Fatalf("next skipped nothing: %s", w.Body.String())
	}
	// A hidden episode's own series view is a 404, as if in no series.
	expectCode(t, g.do(http.MethodGet, "/v1/posts/"+e2.String()+"/series", &viewer, ""), 404, "NOT_FOUND")
	expectCode(t, g.do(http.MethodGet, "/v1/series/"+uuid.NewString()+"/episodes", nil, ""), 404, "NOT_FOUND")
}
