package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// End screens and cards that viewers see (2026-09-29), driven through the
// REAL router with in-memory stores behind service.NewForHandlerTests:
// 401 / 403 / 404 and every 422 code in the contract's order, the viewer
// read's drops, owner-only stats, and the one-per-viewer-per-day counters.
// The real gates (private, deleted, blocked, 18+) are exercised against
// Postgres in service/end_screens_integration_test.go.

// ── in-memory stores ────────────────────────────────────────────────────────

type esBump struct {
	post, element uuid.UUID
	day           string
	click, card   bool
}

type esFakeStore struct {
	subjects   map[uuid.UUID]*postgres.EndScreenSubject
	facts      map[uuid.UUID]*postgres.PostTargetFacts
	screens    map[uuid.UUID][]postgres.EndScreen
	cards      map[uuid.UUID][]postgres.VideoCard
	videos     map[uuid.UUID][]uuid.UUID
	esStats    map[uuid.UUID]postgres.ElementStats
	cardStats  map[uuid.UUID]postgres.ElementStats
	statsSince []time.Time
	bumps      []esBump
	saves      int
}

func newESFakeStore() *esFakeStore {
	return &esFakeStore{
		subjects: map[uuid.UUID]*postgres.EndScreenSubject{}, facts: map[uuid.UUID]*postgres.PostTargetFacts{},
		screens: map[uuid.UUID][]postgres.EndScreen{}, cards: map[uuid.UUID][]postgres.VideoCard{},
		videos: map[uuid.UUID][]uuid.UUID{}, esStats: map[uuid.UUID]postgres.ElementStats{}, cardStats: map[uuid.UUID]postgres.ElementStats{},
	}
}

func (f *esFakeStore) GetEndScreenSubject(_ context.Context, id uuid.UUID) (*postgres.EndScreenSubject, error) {
	if s, ok := f.subjects[id]; ok {
		cp := *s
		return &cp, nil
	}
	return nil, nil
}
func (f *esFakeStore) GetPostTargetFacts(_ context.Context, id uuid.UUID) (*postgres.PostTargetFacts, error) {
	if s, ok := f.facts[id]; ok {
		cp := *s
		return &cp, nil
	}
	return nil, nil
}
func (f *esFakeStore) GetEndScreens(_ context.Context, id uuid.UUID) ([]postgres.EndScreen, error) {
	return append([]postgres.EndScreen(nil), f.screens[id]...), nil
}
func (f *esFakeStore) SaveEndScreens(_ context.Context, id uuid.UUID, rows []postgres.EndScreen) error {
	f.saves++
	for i := range rows {
		if rows[i].ID == uuid.Nil {
			rows[i].ID = uuid.New()
		}
	}
	f.screens[id] = append([]postgres.EndScreen(nil), rows...)
	return nil
}
func (f *esFakeStore) GetVideoCards(_ context.Context, id uuid.UUID) ([]postgres.VideoCard, error) {
	return append([]postgres.VideoCard(nil), f.cards[id]...), nil
}
func (f *esFakeStore) SaveVideoCards(_ context.Context, id uuid.UUID, rows []postgres.VideoCard) error {
	f.saves++
	for i := range rows {
		if rows[i].ID == uuid.Nil {
			rows[i].ID = uuid.New()
		}
	}
	f.cards[id] = append([]postgres.VideoCard(nil), rows...)
	return nil
}
func (f *esFakeStore) ChannelPublicVideoIDs(_ context.Context, owner, exclude uuid.UUID, limit int) ([]uuid.UUID, error) {
	var out []uuid.UUID
	for _, id := range f.videos[owner] {
		if id != exclude && len(out) < limit {
			out = append(out, id)
		}
	}
	return out, nil
}
func (f *esFakeStore) EndScreenStatsSince(_ context.Context, _ uuid.UUID, since time.Time) (map[uuid.UUID]postgres.ElementStats, error) {
	f.statsSince = append(f.statsSince, since)
	return f.esStats, nil
}
func (f *esFakeStore) CardStatsSince(_ context.Context, _ uuid.UUID, since time.Time) (map[uuid.UUID]postgres.ElementStats, error) {
	f.statsSince = append(f.statsSince, since)
	return f.cardStats, nil
}
func (f *esFakeStore) EndScreenOnPost(_ context.Context, post, el uuid.UUID) (bool, error) {
	for _, row := range f.screens[post] {
		if row.ID == el {
			return true, nil
		}
	}
	return false, nil
}
func (f *esFakeStore) CardOnPost(_ context.Context, post, el uuid.UUID) (bool, error) {
	for _, row := range f.cards[post] {
		if row.ID == el {
			return true, nil
		}
	}
	return false, nil
}
func (f *esFakeStore) BumpEndScreenStat(_ context.Context, post, el uuid.UUID, day time.Time, click bool) error {
	f.bumps = append(f.bumps, esBump{post: post, element: el, day: day.Format("2006-01-02"), click: click})
	return nil
}
func (f *esFakeStore) BumpCardStat(_ context.Context, post, el uuid.UUID, day time.Time, click bool) error {
	f.bumps = append(f.bumps, esBump{post: post, element: el, day: day.Format("2006-01-02"), click: click, card: true})
	return nil
}

type esAuthoring struct {
	authors   map[uuid.UUID]uuid.UUID
	playlists map[uuid.UUID]*postgres.Playlist
	items     map[uuid.UUID][]postgres.PlaylistItem
}

func (f *esAuthoring) GetPostAuthorID(_ context.Context, id uuid.UUID) (uuid.UUID, error) {
	if a, ok := f.authors[id]; ok {
		return a, nil
	}
	return uuid.Nil, pgx.ErrNoRows
}
func (f *esAuthoring) GetPlaylist(_ context.Context, id uuid.UUID) (*postgres.Playlist, error) {
	if p, ok := f.playlists[id]; ok {
		cp := *p
		return &cp, nil
	}
	return nil, pgx.ErrNoRows
}
func (f *esAuthoring) ListPlaylistsByCreator(context.Context, uuid.UUID, bool, int, int) ([]postgres.Playlist, error) {
	return nil, nil
}
func (f *esAuthoring) GetPlaylistItems(_ context.Context, id uuid.UUID) ([]postgres.PlaylistItem, error) {
	return f.items[id], nil
}
func (f *esAuthoring) GetVideoMetadata(context.Context, uuid.UUID) (*postgres.VideoMetadata, error) {
	return nil, nil
}

type esChannels struct {
	byUser map[uuid.UUID]*postgres.Channel
	subs   map[[2]uuid.UUID]bool
}

func (f *esChannels) CreateChannel(context.Context, *postgres.Channel) error { return nil }
func (f *esChannels) UpdateChannel(context.Context, uuid.UUID, postgres.ChannelPatch) (*postgres.Channel, error) {
	return nil, nil
}
func (f *esChannels) GetChannelByUserID(_ context.Context, id uuid.UUID) (*postgres.Channel, error) {
	if ch, ok := f.byUser[id]; ok {
		cp := *ch
		return &cp, nil
	}
	return nil, nil
}
func (f *esChannels) GetChannelByHandle(context.Context, string) (*postgres.Channel, error) {
	return nil, nil
}
func (f *esChannels) ChannelHandleExists(context.Context, string) (bool, error) { return false, nil }
func (f *esChannels) GetChannelsByUserIDs(context.Context, []uuid.UUID) (map[uuid.UUID]*postgres.Channel, error) {
	return nil, nil
}
func (f *esChannels) CountChannelVideos(context.Context, uuid.UUID) (int, error) { return 0, nil }
func (f *esChannels) CountChannelVideosBatch(context.Context, []uuid.UUID) (map[uuid.UUID]int, error) {
	return nil, nil
}
func (f *esChannels) SearchChannels(context.Context, string, int) ([]postgres.ChannelSearchHit, error) {
	return nil, nil
}
func (f *esChannels) CountChannelContent(context.Context, uuid.UUID) (postgres.ChannelContentCounts, error) {
	return postgres.ChannelContentCounts{}, nil
}
func (f *esChannels) Subscribe(context.Context, uuid.UUID, uuid.UUID, string) (bool, error) {
	return false, nil
}
func (f *esChannels) Unsubscribe(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return false, nil
}
func (f *esChannels) SetNotifyOn(context.Context, uuid.UUID, uuid.UUID, string) error { return nil }
func (f *esChannels) GetSubscription(_ context.Context, channelID, userID uuid.UUID) (*postgres.ChannelSubscription, error) {
	if f.subs[[2]uuid.UUID{channelID, userID}] {
		return &postgres.ChannelSubscription{ChannelID: channelID, UserID: userID, NotifyOn: "all"}, nil
	}
	return nil, nil
}
func (f *esChannels) ListSubscriberIDsAfter(context.Context, uuid.UUID, uuid.UUID, int) ([]uuid.UUID, error) {
	return nil, nil
}
func (f *esChannels) ListSubscribedOwnersAfter(context.Context, uuid.UUID, uuid.UUID, int) ([]uuid.UUID, error) {
	return nil, nil
}
func (f *esChannels) ListSubscriptionsForUser(context.Context, uuid.UUID, *postgres.SubscriptionCursor, int) ([]postgres.SubscriptionRow, error) {
	return nil, nil
}

type memDedupe struct{ seen map[string]bool }

func (d *memDedupe) FirstToday(_ context.Context, key string) (bool, error) {
	if d.seen[key] {
		return false, nil
	}
	d.seen[key] = true
	return true, nil
}

// ── rig ─────────────────────────────────────────────────────────────────────

type esGateCall struct {
	post   uuid.UUID
	viewer *uuid.UUID
}

type esRig struct {
	owner, viewer, other uuid.UUID
	post                 uuid.UUID
	ownVideo, ownVideo2  uuid.UUID
	ownPrivate, foreign  uuid.UUID
	ownPoll              uuid.UUID
	playlist, privList   uuid.UUID
	systemList           uuid.UUID
	store                *esFakeStore
	auth                 *esAuthoring
	chans                *esChannels
	dedupe               *memDedupe
	viewable             map[uuid.UUID]*service.RelatedPostCard
	gateErr              map[uuid.UUID]error
	gateCalls            []esGateCall
	blocked              map[uuid.UUID]bool
	views                map[uuid.UUID]int64
	now                  time.Time
	router               *gin.Engine
}

// newESRig: owner's long video `post` (60 s), a public and a private video
// of the owner's, a foreign video, a poll post, a public / private / system
// collection, a channel for the owner and for `other`.
func newESRig(t *testing.T) *esRig {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := &esRig{owner: uuid.New(), viewer: uuid.New(), other: uuid.New(), post: uuid.New(),
		ownVideo: uuid.New(), ownVideo2: uuid.New(), ownPrivate: uuid.New(), foreign: uuid.New(), ownPoll: uuid.New(),
		playlist: uuid.New(), privList: uuid.New(), systemList: uuid.New(),
		store: newESFakeStore(), dedupe: &memDedupe{seen: map[string]bool{}},
		viewable: map[uuid.UUID]*service.RelatedPostCard{}, gateErr: map[uuid.UUID]error{}, blocked: map[uuid.UUID]bool{},
		views: map[uuid.UUID]int64{}, now: time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)}
	r.store.subjects[r.post] = &postgres.EndScreenSubject{PostID: r.post, AuthorID: r.owner, ContentType: "long_video", DurationMs: 60000}
	r.store.facts[r.ownVideo] = &postgres.PostTargetFacts{ID: r.ownVideo, AuthorID: r.owner, Visibility: "public"}
	r.store.facts[r.ownVideo2] = &postgres.PostTargetFacts{ID: r.ownVideo2, AuthorID: r.owner, Visibility: "unlisted"}
	r.store.facts[r.ownPrivate] = &postgres.PostTargetFacts{ID: r.ownPrivate, AuthorID: r.owner, Visibility: "private"}
	r.store.facts[r.foreign] = &postgres.PostTargetFacts{ID: r.foreign, AuthorID: r.other, Visibility: "public"}
	r.store.facts[r.ownPoll] = &postgres.PostTargetFacts{ID: r.ownPoll, AuthorID: r.owner, Visibility: "public", HasPoll: true}
	r.store.facts[r.post] = &postgres.PostTargetFacts{ID: r.post, AuthorID: r.owner, Visibility: "public"}
	r.viewable[r.ownVideo] = &service.RelatedPostCard{ID: r.ownVideo, Title: "Thursday build", ThumbnailURL: "/t/1", DurationSeconds: 640, ChannelName: "Raghu Builds"}
	r.viewable[r.ownVideo2] = &service.RelatedPostCard{ID: r.ownVideo2, Title: "Wednesday build", ThumbnailURL: "/t/2", DurationSeconds: 300, ChannelName: "Raghu Builds"}
	r.auth = &esAuthoring{authors: map[uuid.UUID]uuid.UUID{r.post: r.owner}, items: map[uuid.UUID][]postgres.PlaylistItem{},
		playlists: map[uuid.UUID]*postgres.Playlist{
			r.playlist:   {ID: r.playlist, CreatorID: r.owner, Title: "Builds", Visibility: "public", ItemCount: 7, Kind: "user"},
			r.privList:   {ID: r.privList, CreatorID: r.owner, Title: "Drafts", Visibility: "private", Kind: "user"},
			r.systemList: {ID: r.systemList, CreatorID: r.owner, Title: "Loved", Visibility: "public", Kind: "liked"},
		}}
	r.auth.items[r.playlist] = []postgres.PlaylistItem{{PlaylistID: r.playlist, PostID: r.ownPrivate}, {PlaylistID: r.playlist, PostID: r.ownVideo2}}
	r.chans = &esChannels{subs: map[[2]uuid.UUID]bool{}, byUser: map[uuid.UUID]*postgres.Channel{
		r.owner: {ID: uuid.New(), UserID: r.owner, Name: "Raghu Builds", Handle: "raghu.builds", SubscriberCount: 1200},
		r.other: {ID: uuid.New(), UserID: r.other, Name: "Other Channel", Handle: "other", SubscriberCount: 40},
	}}
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			TargetIDs []string `json:"target_ids"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		out := map[string]bool{}
		for _, id := range body.TargetIDs {
			out[id] = !r.blocked[uuid.MustParse(id)]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": out})
	}))
	t.Cleanup(graph.Close)
	analytics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			IDs []string `json:"content_ids"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		out := map[string]any{}
		for _, id := range body.IDs {
			out[id] = map[string]int64{"views_display": r.views[uuid.MustParse(id)]}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": out})
	}))
	t.Cleanup(analytics.Close)
	deps := service.HandlerTestDeps{
		Authoring: r.auth, EndScreens: r.store, Channels: r.chans, StatDedupe: r.dedupe,
		GraphServiceURL: graph.URL, AnalyticsServiceURL: analytics.URL,
		Now: func() time.Time { return r.now },
		ReadGate: func(_ context.Context, id uuid.UUID, viewer *uuid.UUID) error {
			r.gateCalls = append(r.gateCalls, esGateCall{post: id, viewer: viewer})
			return r.gateErr[id]
		},
		PostCard: func(_ context.Context, id uuid.UUID, _ *uuid.UUID) *service.RelatedPostCard {
			if c, ok := r.viewable[id]; ok {
				cp := *c
				return &cp
			}
			return nil
		},
	}
	r.router = gin.New()
	h := New(service.NewForHandlerTests(deps), nil)
	h.RegisterRoutes(r.router)
	return r
}

func (r *esRig) do(t *testing.T, method, path string, caller *uuid.UUID, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if caller != nil {
		req.Header.Set("X-User-Id", caller.String())
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	r.router.ServeHTTP(w, req)
	return w
}

func (r *esRig) esPath() string   { return "/v1/posts/" + r.post.String() + "/end-screens" }
func (r *esRig) cardPath() string { return "/v1/posts/" + r.post.String() + "/cards" }

// el is one end-screen element; extra keys override the defaults.
func el(kind string, extra map[string]any) map[string]any {
	m := map[string]any{"type": kind, "position": map[string]any{"x": 0.05, "y": 0.1, "w": 0.3}, "start_ms": 45000, "end_ms": 60000}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func pos(x, y, w float64) map[string]any { return map[string]any{"x": x, "y": y, "w": w} }

func screensBody(t *testing.T, els ...map[string]any) string {
	t.Helper()
	if els == nil {
		els = []map[string]any{}
	}
	b, err := json.Marshal(map[string]any{"screens": els})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func cardsBody(t *testing.T, cards ...map[string]any) string {
	t.Helper()
	if cards == nil {
		cards = []map[string]any{}
	}
	b, err := json.Marshal(map[string]any{"cards": cards})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func decodeList(t *testing.T, w *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var env struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return env.Data
}

// ── routes ──────────────────────────────────────────────────────────────────

func TestEndScreenRoutesRegistered(t *testing.T) {
	routes := registeredRoutes(t)
	for _, want := range []string{
		"POST /v1/posts/:postId/end-screens", "GET /v1/posts/:postId/end-screens",
		"POST /v1/posts/:postId/end-screens/:elementId/impression", "POST /v1/posts/:postId/end-screens/:elementId/click",
		"POST /v1/posts/:postId/cards", "GET /v1/posts/:postId/cards",
		"POST /v1/posts/:postId/cards/:cardId/impression", "POST /v1/posts/:postId/cards/:cardId/click",
	} {
		if !routes[want] {
			t.Errorf("%s is not registered", want)
		}
	}
}

// ── save: 401 / 404 / 403 ───────────────────────────────────────────────────

func TestSaveEndScreensIdentityAndOwnership(t *testing.T) {
	r := newESRig(t)
	body := screensBody(t, el("external_link", map[string]any{"target_url": "https://example.com"}))
	expectCode(t, r.do(t, http.MethodPost, r.esPath(), nil, body), 401, "UNAUTHORIZED")
	expectCode(t, r.do(t, http.MethodPost, r.cardPath(), nil, cardsBody(t)), 401, "UNAUTHORIZED")
	expectCode(t, r.do(t, http.MethodPost, "/v1/posts/"+uuid.NewString()+"/end-screens", &r.owner, body), 404, "NOT_FOUND")
	expectCode(t, r.do(t, http.MethodPost, "/v1/posts/"+uuid.NewString()+"/cards", &r.owner, cardsBody(t)), 404, "NOT_FOUND")
	expectCode(t, r.do(t, http.MethodPost, r.esPath(), &r.viewer, body), 403, "FORBIDDEN")
	expectCode(t, r.do(t, http.MethodPost, r.cardPath(), &r.viewer, cardsBody(t)), 403, "FORBIDDEN")
	if r.store.saves != 0 {
		t.Fatalf("refused saves reached the store: %d", r.store.saves)
	}
}

// ── save: every 422, one rule at a time ─────────────────────────────────────

func TestSaveEndScreensRules(t *testing.T) {
	five := []map[string]any{}
	for i := 0; i < 5; i++ {
		five = append(five, el("external_link", map[string]any{"target_url": "https://a.b", "position": pos(0.05+float64(i)*0.001, 0.1, 0.12)}))
	}
	for _, tc := range []struct {
		name  string
		setup func(r *esRig)
		els   func(r *esRig) []map[string]any
		code  string
	}{
		{"five elements", nil, func(*esRig) []map[string]any { return five }, "END_SCREEN_TOO_MANY"},
		{"two subscribe buttons", nil, func(*esRig) []map[string]any {
			return []map[string]any{el("channel_subscribe", map[string]any{"position": pos(0.05, 0.1, 0.2)}),
				el("channel_subscribe", map[string]any{"position": pos(0.7, 0.1, 0.2)})}
		}, "END_SCREEN_SUBSCRIBE"},
		{"a reel", func(r *esRig) { r.store.subjects[r.post].ContentType = "flick" }, linkOnly, "END_SCREEN_NOT_ELIGIBLE"},
		{"under 25 s", func(r *esRig) { r.store.subjects[r.post].DurationMs = 24999 }, linkOnly, "END_SCREEN_NOT_ELIGIBLE"},
		{"duration unknown", func(r *esRig) { r.store.subjects[r.post].DurationMs = 0 }, linkOnly, "END_SCREEN_NOT_ELIGIBLE"},
		{"made for kids", func(r *esRig) { r.store.subjects[r.post].MadeForKids = true }, linkOnly, "END_SCREEN_KIDS"},
		{"starts before the last 20 s", nil, withLink(map[string]any{"start_ms": 39999}), "END_SCREEN_TIMING"},
		{"starts inside the last 5 s", nil, withLink(map[string]any{"start_ms": 55001, "end_ms": 60000}), "END_SCREEN_TIMING"},
		{"ends before it starts", nil, withLink(map[string]any{"start_ms": 50000, "end_ms": 50000}), "END_SCREEN_TIMING"},
		{"ends past the video", nil, withLink(map[string]any{"end_ms": 60501}), "END_SCREEN_TIMING"},
		{"x below 0", nil, withLink(map[string]any{"position": pos(-0.01, 0.1, 0.3)}), "END_SCREEN_POSITION"},
		{"y above 1", nil, withLink(map[string]any{"position": pos(0.1, 1.01, 0.3)}), "END_SCREEN_POSITION"},
		{"too narrow", nil, withLink(map[string]any{"position": pos(0.1, 0.1, 0.119)}), "END_SCREEN_POSITION"},
		{"too wide", nil, withLink(map[string]any{"position": pos(0.1, 0.1, 0.51)}), "END_SCREEN_POSITION"},
		{"past the right edge", nil, withLink(map[string]any{"position": pos(0.75, 0.1, 0.3)}), "END_SCREEN_POSITION"},
		{"tile past the bottom", nil, withLink(map[string]any{"position": pos(0.1, 0.71, 0.3)}), "END_SCREEN_POSITION"},
		// A circle of w 0.3 is 0.533 of the frame's height: y 0.5 fits a
		// tile (0.8) and not a circle (1.033).
		{"circle past the bottom", nil, func(*esRig) []map[string]any {
			return []map[string]any{el("channel_subscribe", map[string]any{"position": pos(0.1, 0.5, 0.3)})}
		}, "END_SCREEN_POSITION"},
		{"overlap over 10 %", nil, func(*esRig) []map[string]any {
			return []map[string]any{link(pos(0.1, 0.1, 0.3)), link(pos(0.3, 0.3, 0.3))}
		}, "END_SCREEN_OVERLAP"},
		{"video with no target", nil, func(*esRig) []map[string]any { return []map[string]any{el("video", nil)} }, "END_SCREEN_TARGET"},
		{"someone else's video", nil, func(r *esRig) []map[string]any {
			return []map[string]any{el("video", map[string]any{"target_id": r.foreign.String()})}
		}, "END_SCREEN_TARGET"},
		{"a private video", nil, func(r *esRig) []map[string]any {
			return []map[string]any{el("video", map[string]any{"target_id": r.ownPrivate.String()})}
		}, "END_SCREEN_TARGET"},
		{"a missing video", nil, func(r *esRig) []map[string]any {
			return []map[string]any{el("video", map[string]any{"target_id": uuid.NewString()})}
		}, "END_SCREEN_TARGET"},
		{"the video itself", nil, func(r *esRig) []map[string]any {
			return []map[string]any{el("video", map[string]any{"target_id": r.post.String()})}
		}, "END_SCREEN_TARGET"},
		{"latest with a target", nil, func(r *esRig) []map[string]any {
			return []map[string]any{el("video", map[string]any{"video_mode": "latest", "target_id": r.ownVideo.String()})}
		}, "END_SCREEN_TARGET"},
		{"popular with a target", nil, func(r *esRig) []map[string]any {
			return []map[string]any{el("video", map[string]any{"video_mode": "popular", "target_id": r.ownVideo.String()})}
		}, "END_SCREEN_TARGET"},
		{"a private collection", nil, func(r *esRig) []map[string]any {
			return []map[string]any{el("playlist", map[string]any{"target_id": r.privList.String()})}
		}, "END_SCREEN_TARGET"},
		{"a system collection", nil, func(r *esRig) []map[string]any {
			return []map[string]any{el("playlist", map[string]any{"target_id": r.systemList.String()})}
		}, "END_SCREEN_TARGET"},
		{"someone else's collection", func(r *esRig) { r.auth.playlists[r.playlist].CreatorID = r.other }, func(r *esRig) []map[string]any {
			return []map[string]any{el("playlist", map[string]any{"target_id": r.playlist.String()})}
		}, "END_SCREEN_TARGET"},
		{"own channel as a channel", nil, func(r *esRig) []map[string]any {
			return []map[string]any{el("channel", map[string]any{"target_id": r.owner.String(), "position": pos(0.1, 0.1, 0.2)})}
		}, "END_SCREEN_TARGET"},
		{"unknown channel", nil, func(r *esRig) []map[string]any {
			return []map[string]any{el("channel", map[string]any{"target_id": uuid.NewString(), "position": pos(0.1, 0.1, 0.2)})}
		}, "END_SCREEN_TARGET"},
		{"http link", nil, withLink(map[string]any{"target_url": "http://example.com"}), "END_SCREEN_TARGET"},
		{"link with no host", nil, withLink(map[string]any{"target_url": "https:///path"}), "END_SCREEN_TARGET"},
		{"link over 2048", nil, withLink(map[string]any{"target_url": "https://a.b/" + strings.Repeat("x", 2040)}), "END_SCREEN_TARGET"},
		{"link missing", nil, withLink(map[string]any{"target_url": nil}), "END_SCREEN_TARGET"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newESRig(t)
			if tc.setup != nil {
				tc.setup(r)
			}
			expectCode(t, r.do(t, http.MethodPost, r.esPath(), &r.owner, screensBody(t, tc.els(r)...)), 422, tc.code)
			if r.store.saves != 0 {
				t.Fatal("a refused save reached the store")
			}
		})
	}
}

func link(p map[string]any) map[string]any {
	return el("external_link", map[string]any{"target_url": "https://example.com", "position": p})
}

func linkOnly(*esRig) []map[string]any { return []map[string]any{link(pos(0.05, 0.1, 0.3))} }

func withLink(extra map[string]any) func(*esRig) []map[string]any {
	return func(*esRig) []map[string]any {
		m := el("external_link", map[string]any{"target_url": "https://example.com"})
		for k, v := range extra {
			m[k] = v
		}
		return []map[string]any{m}
	}
}

// The contract's order: each request breaks two rules and must be refused
// with the earlier one.
func TestSaveEndScreensRuleOrder(t *testing.T) {
	bad := func(extra map[string]any) map[string]any {
		m := el("external_link", map[string]any{"target_url": "http://nope"})
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	five := []map[string]any{}
	for i := 0; i < 5; i++ {
		five = append(five, el("channel_subscribe", nil))
	}
	for _, tc := range []struct {
		name  string
		setup func(r *esRig)
		els   []map[string]any
		code  string
	}{
		{"too many before subscribe", nil, five, "END_SCREEN_TOO_MANY"},
		{"subscribe before eligibility", func(r *esRig) { r.store.subjects[r.post].DurationMs = 0 },
			[]map[string]any{el("channel_subscribe", nil), el("channel_subscribe", nil)}, "END_SCREEN_SUBSCRIBE"},
		{"eligibility before kids", func(r *esRig) {
			r.store.subjects[r.post].DurationMs = 0
			r.store.subjects[r.post].MadeForKids = true
		}, []map[string]any{bad(nil)}, "END_SCREEN_NOT_ELIGIBLE"},
		{"kids before timing", func(r *esRig) { r.store.subjects[r.post].MadeForKids = true },
			[]map[string]any{bad(map[string]any{"start_ms": 0})}, "END_SCREEN_KIDS"},
		{"timing before position", nil, []map[string]any{bad(map[string]any{"start_ms": 0, "position": pos(2, 2, 2)})}, "END_SCREEN_TIMING"},
		{"position before overlap", nil, []map[string]any{bad(map[string]any{"position": pos(0.1, 0.1, 0.3)}),
			bad(map[string]any{"position": pos(0.1, 0.1, 0.9)})}, "END_SCREEN_POSITION"},
		{"overlap before target", nil, []map[string]any{bad(map[string]any{"position": pos(0.1, 0.1, 0.3)}),
			bad(map[string]any{"position": pos(0.1, 0.1, 0.3)})}, "END_SCREEN_OVERLAP"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newESRig(t)
			if tc.setup != nil {
				tc.setup(r)
			}
			expectCode(t, r.do(t, http.MethodPost, r.esPath(), &r.owner, screensBody(t, tc.els...)), 422, tc.code)
		})
	}
}

// Boundaries that must pass: the window's edges, the tolerance, a 10 %
// overlap exactly, the smallest and widest box, a circle that just fits.
func TestSaveEndScreensAcceptsTheEdges(t *testing.T) {
	r := newESRig(t)
	els := []map[string]any{
		el("video", map[string]any{"target_id": r.ownVideo.String(), "start_ms": 40000, "end_ms": 60500, "position": pos(0, 0, 0.5)}),
		el("video", map[string]any{"video_mode": "latest", "start_ms": 55000, "end_ms": 55001, "position": pos(0.5, 0, 0.5)}),
		el("channel_subscribe", map[string]any{"position": pos(0, 0.5, 0.28125)}), // 0.28125*16/9 = 0.5
		el("playlist", map[string]any{"target_id": r.playlist.String(), "position": pos(0.88, 0.88, 0.12)}),
	}
	w := r.do(t, http.MethodPost, r.esPath(), &r.owner, screensBody(t, els...))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"saved":4`) {
		t.Fatalf("edges: %d %s", w.Code, w.Body.String())
	}
	// Two tiles overlapping by exactly 10 % of the smaller one pass.
	r2 := newESRig(t)
	// a: (0,0,0.3) area 0.09; b starts at x 0.27: overlap 0.03 x 0.3 = 0.009.
	w = r2.do(t, http.MethodPost, r2.esPath(), &r2.owner, screensBody(t, link(pos(0, 0, 0.3)), link(pos(0.27, 0, 0.3))))
	if w.Code != http.StatusOK {
		t.Fatalf("10 %% overlap: %d %s", w.Code, w.Body.String())
	}
}

// The saved rows: kind-specific fields normalised, positions in the new
// shape (an old {slot:n} converted), an echoed id kept.
func TestSaveEndScreensWritesNormalisedRows(t *testing.T) {
	r := newESRig(t)
	keep := uuid.New()
	r.store.screens[r.post] = []postgres.EndScreen{{ID: keep, PostID: r.post, Type: "external_link"}}
	els := []map[string]any{
		el("external_link", map[string]any{"id": keep.String(), "target_url": " https://example.com/x ", "title": " Docs ", "target_id": r.ownVideo.String(), "position": map[string]any{"slot": 1}}),
		el("channel_subscribe", map[string]any{"target_id": r.ownVideo.String(), "position": map[string]any{"slot": 2}}),
		link(map[string]any{"slot": 0}),
		el("video", map[string]any{"video_mode": "popular", "target_url": "https://x.y", "position": map[string]any{"x": 0.05}}),
	}
	w := r.do(t, http.MethodPost, r.esPath(), &r.owner, screensBody(t, els...))
	if w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	rows := r.store.screens[r.post]
	if len(rows) != 4 || rows[0].ID != keep {
		t.Fatalf("rows %+v (the echoed id must be kept)", rows)
	}
	if rows[0].TargetID != nil || rows[0].TargetURL == nil || *rows[0].TargetURL != "https://example.com/x" || rows[0].Title == nil || *rows[0].Title != "Docs" {
		t.Fatalf("link row: %+v", rows[0])
	}
	if rows[1].TargetID != nil || rows[1].VideoMode != "specific" {
		t.Fatalf("subscribe row: %+v", rows[1])
	}
	if rows[3].TargetURL != nil || rows[3].VideoMode != "popular" || rows[3].TargetID != nil {
		t.Fatalf("popular row: %+v", rows[3])
	}
	// Slot 1 for a tile: x = 1 - 0.05 - 0.30; slot 2 for a circle: y = 1 - 0.10 - 0.20*16/9;
	// slot 0 top-left; the position missing y / w falls back to its index (3):
	// bottom-right.
	for i, want := range []service.EndScreenPosition{
		{X: 0.65, Y: 0.10, W: 0.30}, {X: 0.05, Y: 1 - 0.10 - 0.20*16/9, W: 0.20}, {X: 0.05, Y: 0.10, W: 0.30}, {X: 0.65, Y: 0.60, W: 0.30},
	} {
		var got service.EndScreenPosition
		if err := json.Unmarshal(rows[i].Position, &got); err != nil {
			t.Fatal(err)
		}
		if !closeTo(got.X, want.X) || !closeTo(got.Y, want.Y) || !closeTo(got.W, want.W) {
			t.Fatalf("row %d position %+v want %+v", i, got, want)
		}
	}
	// An empty list clears, on any post — made-for-kids included.
	r.store.subjects[r.post].MadeForKids = true
	if w := r.do(t, http.MethodPost, r.esPath(), &r.owner, screensBody(t)); w.Code != http.StatusOK || len(r.store.screens[r.post]) != 0 {
		t.Fatalf("clear: %d %s", w.Code, w.Body.String())
	}
}

func closeTo(a, b float64) bool { d := a - b; return d < 1e-9 && d > -1e-9 }

// ── cards: every 422 ────────────────────────────────────────────────────────

func TestSaveVideoCardsRules(t *testing.T) {
	card := func(kind string, extra map[string]any) map[string]any {
		m := map[string]any{"type": kind, "title": "Watch next", "appear_at_ms": 1000}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	six := []map[string]any{}
	for i := 0; i < 6; i++ {
		six = append(six, card("external_link", map[string]any{"target_url": "https://a.b"}))
	}
	for _, tc := range []struct {
		name  string
		setup func(r *esRig)
		cards func(r *esRig) []map[string]any
		code  string
	}{
		{"six cards", nil, func(*esRig) []map[string]any { return six }, "CARD_TOO_MANY"},
		{"made for kids", func(r *esRig) { r.store.subjects[r.post].MadeForKids = true }, func(*esRig) []map[string]any {
			return []map[string]any{card("external_link", map[string]any{"target_url": "http://late", "appear_at_ms": -1})}
		}, "CARD_KIDS"},
		{"negative time", nil, func(*esRig) []map[string]any {
			return []map[string]any{card("external_link", map[string]any{"target_url": "http://x", "appear_at_ms": -1})}
		}, "CARD_TIMING"},
		{"after the end", nil, func(*esRig) []map[string]any {
			return []map[string]any{card("external_link", map[string]any{"target_url": "https://a.b", "appear_at_ms": 60001})}
		}, "CARD_TIMING"},
		// appear_at_ms 0 would be inside any known video: only the
		// unknown length refuses it.
		{"length unknown", func(r *esRig) { r.store.subjects[r.post].DurationMs = 0 }, func(*esRig) []map[string]any {
			return []map[string]any{card("external_link", map[string]any{"target_url": "https://a.b", "appear_at_ms": 0})}
		}, "CARD_TIMING"},
		{"someone else's video", nil, func(r *esRig) []map[string]any {
			return []map[string]any{card("video", map[string]any{"target_id": r.foreign.String()})}
		}, "CARD_TARGET"},
		{"a private video", nil, func(r *esRig) []map[string]any {
			return []map[string]any{card("video", map[string]any{"target_id": r.ownPrivate.String()})}
		}, "CARD_TARGET"},
		{"a poll card on a post with no poll", nil, func(r *esRig) []map[string]any {
			return []map[string]any{card("poll", map[string]any{"target_id": r.ownVideo.String()})}
		}, "CARD_TARGET"},
		{"a private collection", nil, func(r *esRig) []map[string]any {
			return []map[string]any{card("playlist", map[string]any{"target_id": r.privList.String()})}
		}, "CARD_TARGET"},
		{"http link", nil, func(*esRig) []map[string]any {
			return []map[string]any{card("external_link", map[string]any{"target_url": "http://a.b"})}
		}, "CARD_TARGET"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newESRig(t)
			if tc.setup != nil {
				tc.setup(r)
			}
			expectCode(t, r.do(t, http.MethodPost, r.cardPath(), &r.owner, cardsBody(t, tc.cards(r)...)), 422, tc.code)
			if r.store.saves != 0 {
				t.Fatal("a refused card save reached the store")
			}
		})
	}
	r := newESRig(t)
	w := r.do(t, http.MethodPost, r.cardPath(), &r.owner, cardsBody(t,
		card("video", map[string]any{"target_id": r.ownVideo.String(), "appear_at_ms": 60000}),
		card("poll", map[string]any{"target_id": r.ownPoll.String(), "appear_at_ms": 0}),
		card("playlist", map[string]any{"target_id": r.playlist.String()}),
		card("external_link", map[string]any{"target_url": "https://example.com", "target_id": r.ownVideo.String()})))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"saved":4`) {
		t.Fatalf("valid cards: %d %s", w.Code, w.Body.String())
	}
	if got := r.store.cards[r.post][3]; got.TargetID != nil || got.TargetURL == nil {
		t.Fatalf("link card row: %+v", got)
	}
}

// ── viewer read ─────────────────────────────────────────────────────────────

// The detail's gate answers first, with the detail's codes, on both reads.
func TestEndScreenReadsAreGatedLikeTheDetail(t *testing.T) {
	for _, tc := range []struct {
		refusal error
		status  int
		code    string
	}{
		{service.ErrPostNotVisible, 404, "NOT_FOUND"},
		{service.ErrAgeSignIn, 401, "AGE_RESTRICTED_SIGN_IN"},
		{service.ErrAgeRestricted, 403, "AGE_RESTRICTED"},
		{service.ErrAgeUnverified, 403, "AGE_UNVERIFIED"},
	} {
		r := newESRig(t)
		r.gateErr[r.post] = tc.refusal
		expectCode(t, r.do(t, http.MethodGet, r.esPath(), &r.viewer, ""), tc.status, tc.code)
		expectCode(t, r.do(t, http.MethodGet, r.cardPath(), nil, ""), tc.status, tc.code)
	}
	r := newESRig(t)
	expectCode(t, r.do(t, http.MethodGet, "/v1/posts/nope/end-screens", nil, ""), 400, "INVALID_ID")
	expectCode(t, r.do(t, http.MethodGet, "/v1/posts/"+uuid.NewString()+"/end-screens", nil, ""), 404, "NOT_FOUND")
}

// seedScreens stores one element of every kind plus the ones whose target
// the viewer may not open.
func (r *esRig) seedScreens() (kept map[string]uuid.UUID, dropped []uuid.UUID) {
	id := func() uuid.UUID { return uuid.New() }
	up := func(p service.EndScreenPosition) json.RawMessage { b, _ := json.Marshal(p); return b }
	u := "https://www.Example.com/docs"
	title := "Docs"
	bad := "http://example.com"
	kept = map[string]uuid.UUID{"video": id(), "latest": id(), "playlist": id(), "subscribe": id(), "channel": id(), "link": id()}
	hiddenVideo, blockedChannel, privList, badLink, sysList, blockedList := id(), id(), id(), id(), id(), id()
	dropped = []uuid.UUID{hiddenVideo, blockedChannel, privList, badLink, sysList, blockedList}
	blocked := uuid.New()
	r.blocked[blocked] = true
	r.chans.byUser[blocked] = &postgres.Channel{ID: uuid.New(), UserID: blocked, Name: "Blocked", Handle: "blocked"}
	blockedPlaylist := id()
	r.auth.playlists[blockedPlaylist] = &postgres.Playlist{ID: blockedPlaylist, CreatorID: blocked, Title: "Blocked list", Visibility: "public", Kind: "user"}
	r.store.videos[r.owner] = []uuid.UUID{r.post, r.ownPrivate, r.ownVideo2, r.ownVideo}
	p := service.EndScreenPosition{X: 0.05, Y: 0.1, W: 0.3}
	r.store.screens[r.post] = []postgres.EndScreen{
		{ID: kept["video"], Type: "video", VideoMode: "specific", TargetID: &r.ownVideo, Position: up(p), StartMs: 45000, EndMs: 60000},
		{ID: hiddenVideo, Type: "video", VideoMode: "specific", TargetID: &r.ownPrivate, Position: up(p), StartMs: 45000, EndMs: 60000},
		{ID: kept["latest"], Type: "video", VideoMode: "latest", Position: json.RawMessage(`{"slot":1}`), StartMs: 45000, EndMs: 60000},
		{ID: kept["playlist"], Type: "playlist", TargetID: &r.playlist, Position: up(p), StartMs: 45000, EndMs: 60000},
		{ID: privList, Type: "playlist", TargetID: &r.privList, Position: up(p), StartMs: 45000, EndMs: 60000},
		{ID: kept["subscribe"], Type: "channel_subscribe", Position: up(p), StartMs: 45000, EndMs: 60000},
		{ID: kept["channel"], Type: "channel", TargetID: &r.other, Position: up(p), StartMs: 45000, EndMs: 60000},
		{ID: blockedChannel, Type: "channel", TargetID: &blocked, Position: up(p), StartMs: 45000, EndMs: 60000},
		{ID: kept["link"], Type: "external_link", TargetURL: &u, Title: &title, Position: up(p), StartMs: 45000, EndMs: 60000},
		{ID: badLink, Type: "external_link", TargetURL: &bad, Position: up(p), StartMs: 45000, EndMs: 60000},
		{ID: sysList, Type: "playlist", TargetID: &r.systemList, Position: up(p), StartMs: 45000, EndMs: 60000},
		{ID: blockedList, Type: "playlist", TargetID: &blockedPlaylist, Position: up(p), StartMs: 45000, EndMs: 60000},
	}
	r.chans.subs[[2]uuid.UUID{r.chans.byUser[r.owner].ID, r.viewer}] = true
	r.store.esStats[kept["video"]] = postgres.ElementStats{Impressions: 200, Clicks: 9}
	return kept, dropped
}

// Every element whose target the viewer may not open is dropped — never
// sent half-empty — and nothing owner-only is on the viewer's copy.
func TestEndScreenViewerReadResolvesAndDrops(t *testing.T) {
	r := newESRig(t)
	kept, dropped := r.seedScreens()
	for _, caller := range []*uuid.UUID{&r.viewer, nil} {
		w := r.do(t, http.MethodGet, r.esPath(), caller, "")
		if w.Code != http.StatusOK {
			t.Fatalf("read: %d %s", w.Code, w.Body.String())
		}
		body := w.Body.String()
		for _, id := range dropped {
			if strings.Contains(body, id.String()) {
				t.Fatalf("a dropped element was sent: %s in %s", id, body)
			}
		}
		for _, leak := range []string{`"stats"`, `"target_id"`, `"target_url"`, `"video_mode"`, r.ownPrivate.String(), "Blocked", "Drafts", "http://example.com"} {
			if strings.Contains(body, leak) {
				t.Fatalf("viewer copy carries %s: %s", leak, body)
			}
		}
		list := decodeList(t, w)
		if len(list) != len(kept) {
			t.Fatalf("got %d elements want %d: %s", len(list), len(kept), body)
		}
		for _, item := range list {
			set := 0
			for _, k := range []string{"video", "playlist", "channel", "link"} {
				if v, ok := item[k]; !ok {
					t.Fatalf("key %s missing (it must be null, not absent): %v", k, item)
				} else if v != nil {
					set++
				}
			}
			if set != 1 {
				t.Fatalf("element %v has %d blocks set", item["id"], set)
			}
		}
		byID := map[string]map[string]any{}
		for _, item := range list {
			byID[item["id"].(string)] = item
		}
		if v := byID[kept["latest"].String()]["video"].(map[string]any); v["id"] != r.ownVideo2.String() {
			t.Fatalf("latest resolved to %v; want the newest public video the viewer may open (%s)", v["id"], r.ownVideo2)
		}
		if p := byID[kept["latest"].String()]["position"].(map[string]any); !closeTo(p["x"].(float64), 0.65) || !closeTo(p["y"].(float64), 0.1) {
			t.Fatalf("legacy slot 1 drawn at %v", p)
		}
		if pl := byID[kept["playlist"].String()]["playlist"].(map[string]any); pl["thumbnail_url"] != "/t/2" || pl["item_count"].(float64) != 7 {
			t.Fatalf("playlist block %v (the thumbnail must come from an item the viewer may open)", pl)
		}
		ch := byID[kept["subscribe"].String()]["channel"].(map[string]any)
		if ch["user_id"] != r.owner.String() || ch["is_subscribed"] != (caller != nil) {
			t.Fatalf("subscribe block %v for caller %v", ch, caller)
		}
		if l := byID[kept["link"].String()]["link"].(map[string]any); l["domain"] != "example.com" || l["title"] != "Docs" {
			t.Fatalf("link block %v", l)
		}
	}
}

// popular = the most viewed of the channel's public videos the viewer may
// open; a tie keeps the newer.
func TestEndScreenPopularPicksMostViewed(t *testing.T) {
	r := newESRig(t)
	r.store.videos[r.owner] = []uuid.UUID{r.ownVideo2, r.ownPrivate, r.ownVideo}
	r.views[r.ownPrivate] = 9000 // most viewed, but not the viewer's to open
	r.views[r.ownVideo] = 50
	r.views[r.ownVideo2] = 10
	p, _ := json.Marshal(service.EndScreenPosition{X: 0.05, Y: 0.1, W: 0.3})
	r.store.screens[r.post] = []postgres.EndScreen{{ID: uuid.New(), Type: "video", VideoMode: "popular", Position: p, StartMs: 45000, EndMs: 60000}}
	list := decodeList(t, r.do(t, http.MethodGet, r.esPath(), &r.viewer, ""))
	if len(list) != 1 || list[0]["video"].(map[string]any)["id"] != r.ownVideo.String() || list[0]["video"].(map[string]any)["view_count"].(float64) != 50 {
		t.Fatalf("popular: %v", list)
	}
	r2 := newESRig(t)
	r2.store.videos[r2.owner] = []uuid.UUID{r2.ownVideo2, r2.ownVideo}
	r2.views = map[uuid.UUID]int64{r2.ownVideo2: 50, r2.ownVideo: 50}
	r2.store.screens[r2.post] = []postgres.EndScreen{{ID: uuid.New(), Type: "video", VideoMode: "popular", Position: p, StartMs: 45000, EndMs: 60000}}
	list = decodeList(t, r2.do(t, http.MethodGet, r2.esPath(), &r2.viewer, ""))
	if len(list) != 1 || list[0]["video"].(map[string]any)["id"] != r2.ownVideo2.String() {
		t.Fatalf("popular tie: %v", list)
	}
}

// The owner's copy: every element (a dead target resolves to null so the
// editor can show it), the raw fields, and 28 days of stats.
func TestEndScreenOwnerReadCarriesRawFieldsAndStats(t *testing.T) {
	r := newESRig(t)
	kept, dropped := r.seedScreens()
	w := r.do(t, http.MethodGet, r.esPath(), &r.owner, "")
	if w.Code != http.StatusOK {
		t.Fatalf("owner read: %d %s", w.Code, w.Body.String())
	}
	list := decodeList(t, w)
	if len(list) != len(kept)+len(dropped) {
		t.Fatalf("owner got %d elements want %d", len(list), len(kept)+len(dropped))
	}
	for _, item := range list {
		for _, k := range []string{"video_mode", "target_id", "target_url", "title", "stats"} {
			if _, ok := item[k]; !ok {
				t.Fatalf("owner element lacks %s: %v", k, item)
			}
		}
		if item["id"] == kept["video"].String() {
			st := item["stats"].(map[string]any)
			if st["impressions"].(float64) != 200 || st["clicks"].(float64) != 9 || st["click_rate"].(float64) != 0.045 {
				t.Fatalf("stats %v", st)
			}
		}
		if item["id"] == dropped[0].String() && item["video"] != nil {
			t.Fatalf("a target the owner's viewers cannot open still resolved: %v", item)
		}
	}
	if len(r.store.statsSince) != 1 || !r.store.statsSince[0].Equal(r.now.AddDate(0, 0, -27)) {
		t.Fatalf("stats window %v; want the 28 days ending today", r.store.statsSince)
	}
}

// Made for kids: [] for everyone but the owner.
func TestEndScreensMadeForKidsReadEmpty(t *testing.T) {
	r := newESRig(t)
	r.seedScreens()
	r.store.cards[r.post] = []postgres.VideoCard{{ID: uuid.New(), Type: "external_link", Title: "Docs", TargetURL: strp("https://a.b")}}
	r.store.subjects[r.post].MadeForKids = true
	for _, path := range []string{r.esPath(), r.cardPath()} {
		if list := decodeList(t, r.do(t, http.MethodGet, path, &r.viewer, "")); len(list) != 0 {
			t.Fatalf("%s for a viewer on a kids post: %v", path, list)
		}
		if list := decodeList(t, r.do(t, http.MethodGet, path, &r.owner, "")); len(list) == 0 {
			t.Fatalf("%s: the owner's editor must still see what is there", path)
		}
	}
}

// Cards resolve the same way; the watch page draws no poll card, so poll
// cards are the owner's only.
func TestVideoCardReads(t *testing.T) {
	r := newESRig(t)
	u, bad := "https://example.com", "http://x"
	visible, hidden, poll, link, badLink := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	r.store.cards[r.post] = []postgres.VideoCard{
		{ID: visible, Type: "video", TargetID: &r.ownVideo, Title: "Next", AppearAtMs: 1000},
		{ID: hidden, Type: "video", TargetID: &r.ownPrivate, Title: "Secret", AppearAtMs: 2000},
		{ID: poll, Type: "poll", TargetID: &r.ownPoll, Title: "Vote", AppearAtMs: 3000},
		{ID: link, Type: "external_link", TargetURL: &u, Title: "Docs", AppearAtMs: 4000},
		{ID: badLink, Type: "external_link", TargetURL: &bad, Title: "Bad", AppearAtMs: 5000},
	}
	r.store.cardStats[visible] = postgres.ElementStats{Impressions: 3, Clicks: 1}
	w := r.do(t, http.MethodGet, r.cardPath(), &r.viewer, "")
	list := decodeList(t, w)
	if len(list) != 2 || list[0]["id"] != visible.String() || list[1]["id"] != link.String() {
		t.Fatalf("viewer cards: %s", w.Body.String())
	}
	for _, leak := range []string{"Secret", "Vote", `"stats"`, `"target_id"`} {
		if strings.Contains(w.Body.String(), leak) {
			t.Fatalf("viewer cards carry %s: %s", leak, w.Body.String())
		}
	}
	if l := list[1]["link"].(map[string]any); l["title"] != "Docs" || l["url"] != u {
		t.Fatalf("link card %v", l)
	}
	owner := decodeList(t, r.do(t, http.MethodGet, r.cardPath(), &r.owner, ""))
	if len(owner) != 5 {
		t.Fatalf("owner cards: %v", owner)
	}
	st := owner[0]["stats"].(map[string]any)
	if st["click_rate"].(float64) != 0.3333 || owner[0]["target_id"] != r.ownVideo.String() {
		t.Fatalf("owner card %v", owner[0])
	}
}

// ── impressions and clicks ──────────────────────────────────────────────────

func TestEndScreenStatsDedupeAndOwner(t *testing.T) {
	r := newESRig(t)
	kept, _ := r.seedScreens()
	el := kept["video"]
	imp := r.esPath() + "/" + el.String() + "/impression"
	click := r.esPath() + "/" + el.String() + "/click"
	expect204 := func(w *httptest.ResponseRecorder) {
		t.Helper()
		if w.Code != http.StatusNoContent {
			t.Fatalf("status %d %s", w.Code, w.Body.String())
		}
	}
	expect204(r.do(t, http.MethodPost, imp, &r.viewer, ""))
	expect204(r.do(t, http.MethodPost, imp, &r.viewer, ""))   // same viewer, same day: not counted
	expect204(r.do(t, http.MethodPost, click, &r.viewer, "")) // a click is its own
	expect204(r.do(t, http.MethodPost, click, &r.viewer, ""))
	other := uuid.New()
	expect204(r.do(t, http.MethodPost, imp, &other, ""))
	// Anonymous viewers are keyed by the client IP the gateway forwards.
	expect204(r.do(t, http.MethodPost, imp, nil, "", "X-Real-IP", "203.0.113.7"))
	expect204(r.do(t, http.MethodPost, imp, nil, "", "X-Real-IP", "203.0.113.7"))
	expect204(r.do(t, http.MethodPost, imp, nil, "", "X-Real-IP", "203.0.113.8"))
	// The owner's own views are not counted.
	expect204(r.do(t, http.MethodPost, imp, &r.owner, ""))
	expect204(r.do(t, http.MethodPost, click, &r.owner, ""))
	var imps, clicks int
	for _, b := range r.store.bumps {
		if b.element != el || b.post != r.post || b.day != "2026-09-29" {
			t.Fatalf("bump %+v", b)
		}
		if b.click {
			clicks++
		} else {
			imps++
		}
	}
	if imps != 4 || clicks != 1 {
		t.Fatalf("impressions=%d clicks=%d want 4 and 1 (%+v)", imps, clicks, r.store.bumps)
	}
	// The next day the same viewer counts again.
	r.now = r.now.Add(24 * time.Hour)
	expect204(r.do(t, http.MethodPost, imp, &r.viewer, ""))
	if last := r.store.bumps[len(r.store.bumps)-1]; last.day != "2026-09-30" || len(r.store.bumps) != 6 {
		t.Fatalf("next day: %+v", r.store.bumps)
	}
}

func TestEndScreenStatsRefusals(t *testing.T) {
	r := newESRig(t)
	kept, _ := r.seedScreens()
	cardID := uuid.New()
	r.store.cards[r.post] = []postgres.VideoCard{{ID: cardID, Type: "external_link", Title: "x", TargetURL: strp("https://a.b")}}
	foreignPost := uuid.New()
	r.store.subjects[foreignPost] = &postgres.EndScreenSubject{PostID: foreignPost, AuthorID: r.other, ContentType: "long_video", DurationMs: 60000}
	// Unknown element, and an element of another post: 404.
	expectCode(t, r.do(t, http.MethodPost, r.esPath()+"/"+uuid.NewString()+"/impression", &r.viewer, ""), 404, "NOT_FOUND")
	expectCode(t, r.do(t, http.MethodPost, "/v1/posts/"+foreignPost.String()+"/end-screens/"+kept["video"].String()+"/click", &r.viewer, ""), 404, "NOT_FOUND")
	expectCode(t, r.do(t, http.MethodPost, r.cardPath()+"/"+kept["video"].String()+"/impression", &r.viewer, ""), 404, "NOT_FOUND")
	expectCode(t, r.do(t, http.MethodPost, r.esPath()+"/nope/impression", &r.viewer, ""), 400, "INVALID_ID")
	// The detail's gate.
	r.gateErr[r.post] = service.ErrPostNotVisible
	expectCode(t, r.do(t, http.MethodPost, r.esPath()+"/"+kept["video"].String()+"/impression", &r.viewer, ""), 404, "NOT_FOUND")
	expectCode(t, r.do(t, http.MethodPost, r.cardPath()+"/"+cardID.String()+"/click", &r.viewer, ""), 404, "NOT_FOUND")
	r.gateErr[r.post] = service.ErrAgeSignIn
	expectCode(t, r.do(t, http.MethodPost, r.esPath()+"/"+kept["video"].String()+"/click", nil, ""), 401, "AGE_RESTRICTED_SIGN_IN")
	delete(r.gateErr, r.post)
	// A kids post shows no elements, so it counts none.
	r.store.subjects[r.post].MadeForKids = true
	expectCode(t, r.do(t, http.MethodPost, r.cardPath()+"/"+cardID.String()+"/impression", &r.viewer, ""), 404, "NOT_FOUND")
	if len(r.store.bumps) != 0 {
		t.Fatalf("refused events were counted: %+v", r.store.bumps)
	}
	// Cards count through the same rule.
	r.store.subjects[r.post].MadeForKids = false
	for i := 0; i < 2; i++ {
		if w := r.do(t, http.MethodPost, r.cardPath()+"/"+cardID.String()+"/click", &r.viewer, ""); w.Code != http.StatusNoContent {
			t.Fatalf("card click: %d %s", w.Code, w.Body.String())
		}
	}
	if len(r.store.bumps) != 1 || !r.store.bumps[0].card || !r.store.bumps[0].click {
		t.Fatalf("card bumps: %+v", r.store.bumps)
	}
}

// ── error mapping ───────────────────────────────────────────────────────────

func TestAuthoringRuleErrorIs422WithItsCode(t *testing.T) {
	for _, code := range []string{
		service.CodeEndScreenTooMany, service.CodeEndScreenSubscribe, service.CodeEndScreenNotEligible, service.CodeEndScreenKids,
		service.CodeEndScreenTiming, service.CodeEndScreenPosition, service.CodeEndScreenOverlap, service.CodeEndScreenTarget,
		service.CodeCardTooMany, service.CodeCardKids, service.CodeCardTiming, service.CodeCardTarget,
	} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/x", nil)
		if !writeAuthoringRuleError(c, fmt.Errorf("wrapped: %w", &service.AuthoringRuleError{Code: code, Message: "m"})) ||
			rec.Code != http.StatusUnprocessableEntity || errorCode(t, rec) != code {
			t.Fatalf("%s: %d %s", code, rec.Code, rec.Body.String())
		}
	}
}
