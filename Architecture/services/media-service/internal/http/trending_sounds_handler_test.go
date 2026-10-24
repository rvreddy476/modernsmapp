package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// GET /v1/audio/trending, the sound page's `trending` and the internal
// extras route (trending sounds, 2026-10-24) through the real handlers.
// The rules behind them are service/trending_sounds_test.go.

type fakeTrending struct {
	page      *service.TrendingPage
	err       error
	badges    map[uuid.UUID]*service.TrendingBadge
	badgeErr  error
	extras    *service.SoundExtras
	extrasErr error

	viewer         uuid.UUID
	filter, cursor string
	limit          int
}

func (f *fakeTrending) List(_ context.Context, viewer uuid.UUID, filter, cursor string, limit int) (*service.TrendingPage, error) {
	f.viewer, f.filter, f.cursor, f.limit = viewer, filter, cursor, limit
	return f.page, f.err
}

func (f *fakeTrending) Badge(_ context.Context, id uuid.UUID) (*service.TrendingBadge, error) {
	return f.badges[id], f.badgeErr
}

func (f *fakeTrending) Extras(context.Context, uuid.UUID) (*service.SoundExtras, error) {
	return f.extras, f.extrasErr
}

// requireUser stands in for authMW: no X-User-Id, no route.
func requireUser(c *gin.Context) {
	if c.GetHeader("X-User-Id") == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"code": "UNAUTHORIZED"}})
		return
	}
	c.Next()
}

func trendingRouter(f *recordFixture, trending trendingSoundsService) *gin.Engine {
	r := gin.New()
	h := &Handler{
		records:  service.NewRecordReads(f.store, f.blobs, delivery.NewGate(nil, f.authz)),
		trending: trending,
	}
	h.WithInternalKey(recordInternalKey)
	h.RegisterRoutes(r, requireUser, passthrough)
	h.RegisterAudioRoutes(r, requireUser)
	return r
}

func serve(r *gin.Engine, path string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestTrendingRouteShape(t *testing.T) {
	f := newRecordFixture(t)
	at := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	media := uuid.New()
	fake := &fakeTrending{page: &service.TrendingPage{
		Items: []service.TrendingItem{{Rank: 1, Uses7d: 42, UsesPrev7d: 10, Rising: true,
			Sound: postgres.AudioTrack{ID: uuid.New(), Title: "Monsoon", SourceMediaID: &media, AudioKey: "audio/u/m/audio.m4a", Status: "ready"}}},
		NextCursor: "abc", UpdatedAt: &at,
	}}
	r := trendingRouter(f, fake)
	rec := serve(r, "/v1/audio/trending?filter=rising&cursor=abc&limit=7", "X-User-Id", f.viewer.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	if fake.viewer != f.viewer || fake.filter != "rising" || fake.cursor != "abc" || fake.limit != 7 {
		t.Fatalf("service asked %+v", fake)
	}
	var body struct {
		Data []map[string]json.RawMessage `json:"data"`
		Meta map[string]json.RawMessage   `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 1 {
		t.Fatalf("data %s", rec.Body)
	}
	row := body.Data[0]
	for _, k := range []string{"rank", "sound", "uses_7d", "uses_prev_7d", "rising"} {
		if _, ok := row[k]; !ok {
			t.Errorf("row has no %q: %s", k, rec.Body)
		}
	}
	if string(row["rank"]) != "1" || string(row["uses_7d"]) != "42" || string(row["uses_prev_7d"]) != "10" || string(row["rising"]) != "true" {
		t.Errorf("row %s", rec.Body)
	}
	var sound map[string]json.RawMessage
	_ = json.Unmarshal(row["sound"], &sound)
	if string(sound["title"]) != `"Monsoon"` || string(sound["cover_url"]) != fmt.Sprintf(`"/v1/media/%s/serve/thumb_150"`, media) {
		t.Errorf("sound is not the Sound shape: %s", row["sound"])
	}
	if strings.Contains(rec.Body.String(), "audio.m4a") {
		t.Errorf("a storage key is on the wire: %s", rec.Body)
	}
	if string(body.Meta["next_cursor"]) != `"abc"` || string(body.Meta["updated_at"]) != `"2026-10-24T12:00:00Z"` {
		t.Errorf("meta %s", rec.Body)
	}

	// The last page, before the first run: [] and two nulls.
	fake.page = &service.TrendingPage{}
	rec = serve(r, "/v1/audio/trending", "X-User-Id", f.viewer.String())
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"data":[]`) ||
		!strings.Contains(rec.Body.String(), `"next_cursor":null`) || !strings.Contains(rec.Body.String(), `"updated_at":null`) {
		t.Fatalf("empty page: %d %s", rec.Code, rec.Body)
	}
	if fake.limit != 0 || fake.filter != "" {
		t.Fatalf("defaults passed as limit %d filter %q", fake.limit, fake.filter)
	}
}

func TestTrendingRouteRefusals(t *testing.T) {
	f := newRecordFixture(t)
	for _, tc := range []struct {
		name   string
		svc    trendingSoundsService
		path   string
		signed bool
		status int
		code   string
	}{
		{"signed out", &fakeTrending{page: &service.TrendingPage{}}, "/v1/audio/trending", false, 401, "UNAUTHORIZED"},
		{"no viewer behind the token", &fakeTrending{err: delivery.ErrDeliveryDenied}, "/v1/audio/trending", true, 401, "UNAUTHORIZED"},
		{"bad limit", &fakeTrending{page: &service.TrendingPage{}}, "/v1/audio/trending?limit=ten", true, 400, "BAD_REQUEST"},
		{"bad filter", &fakeTrending{err: service.ErrTrendingFilter}, "/v1/audio/trending?filter=hot", true, 400, "INVALID_FILTER"},
		{"bad cursor", &fakeTrending{err: service.ErrTrendingCursor}, "/v1/audio/trending?cursor=x", true, 400, "INVALID_CURSOR"},
		{"unresolved", &fakeTrending{err: fmt.Errorf("%w: graph", delivery.ErrDeliveryUnresolved)}, "/v1/audio/trending", true, 503, "DEPENDENCY_UNAVAILABLE"},
		{"fault", &fakeTrending{err: errors.New(`relation "audio_trending_sounds" does not exist`)}, "/v1/audio/trending", true, 500, "INTERNAL_ERROR"},
		{"not wired", nil, "/v1/audio/trending", true, 503, "DEPENDENCY_UNAVAILABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := trendingRouter(f, tc.svc)
			var rec *httptest.ResponseRecorder
			if tc.signed {
				rec = serve(r, tc.path, "X-User-Id", f.viewer.String())
			} else {
				rec = serve(r, tc.path)
			}
			if rec.Code != tc.status || errorCode(t, rec) != tc.code {
				t.Fatalf("got %d %s, want %d %s", rec.Code, rec.Body, tc.status, tc.code)
			}
			if strings.Contains(rec.Body.String(), "audio_trending_sounds") {
				t.Fatalf("a fault's text is on the wire: %s", rec.Body)
			}
		})
	}
}

// The sound page read carries `trending`, after every field of the Sound
// shape, and null when the sound has none or the badge cannot be read.
func TestSoundReadCarriesTrending(t *testing.T) {
	f := newRecordFixture(t)
	fake := &fakeTrending{badges: map[uuid.UUID]*service.TrendingBadge{f.trackPublic: {Rank: 3, Rising: false}}}
	r := trendingRouter(f, fake)

	rec := serve(r, "/v1/audio/"+f.trackPublic.String(), "X-User-Id", f.viewer.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	var env struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if string(env.Data["trending"]) != `{"rank":3,"rising":false}` || string(env.Data["id"]) != `"`+f.trackPublic.String()+`"` {
		t.Fatalf("sound page %s", rec.Body)
	}
	if _, ok := env.Data["cover_url"]; !ok {
		t.Fatalf("no cover_url: %s", rec.Body)
	}

	// Not trending: null, never absent.
	rec = serve(r, "/v1/audio/"+f.trackPrivate.String(), "X-User-Id", f.viewer.String())
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"trending":null`) {
		t.Fatalf("an untrending sound: %d %s", rec.Code, rec.Body)
	}
	// A badge fault costs the badge, not the page.
	fake.badgeErr = errors.New("db down")
	rec = serve(r, "/v1/audio/"+f.trackPublic.String(), "X-User-Id", f.viewer.String())
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"trending":null`) {
		t.Fatalf("badge fault: %d %s", rec.Code, rec.Body)
	}
	// Unwired: null.
	rec = serve(trendingRouter(f, nil), "/v1/audio/"+f.trackPublic.String(), "X-User-Id", f.viewer.String())
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"trending":null`) {
		t.Fatalf("unwired: %d %s", rec.Code, rec.Body)
	}
	// A denied sound is still the missing answer, badge or not.
	fake.badgeErr = nil
	wantSoundDenied(t, "stranger on private", serve(r, "/v1/audio/"+f.trackPrivate.String(), "X-User-Id", f.stranger.String()),
		serve(r, "/v1/audio/"+uuid.New().String(), "X-User-Id", f.stranger.String()))
}

func TestSoundWithTrendingKeepsTheSoundShape(t *testing.T) {
	media := uuid.New()
	track := &postgres.AudioTrack{ID: uuid.New(), SourceMediaID: &media, Title: "T", Status: "ready"}
	plain, _ := json.Marshal(track)
	with, err := json.Marshal(soundWithTrending{track: track, trending: &service.TrendingBadge{Rank: 1, Rising: true}})
	if err != nil {
		t.Fatal(err)
	}
	want := string(plain[:len(plain)-1]) + `,"trending":{"rank":1,"rising":true}}`
	if string(with) != want {
		t.Fatalf("got  %s\nwant %s", with, want)
	}
}

func TestSoundExtrasInternalRoute(t *testing.T) {
	f := newRecordFixture(t)
	cover := "/v1/media/x/serve/thumb_150"
	fake := &fakeTrending{extras: &service.SoundExtras{CoverURL: &cover, Trending: &service.TrendingBadge{Rank: 2, Rising: true}}}
	r := trendingRouter(f, fake)
	path := "/v1/media/internal/sounds/" + f.trackPublic.String() + "/extras"

	rec := serve(r, path, internalKeyHeader, recordInternalKey)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"trending":{"rank":2,"rising":true}`) ||
		!strings.Contains(rec.Body.String(), `"cover_url":"/v1/media/x/serve/thumb_150"`) {
		t.Fatalf("extras: %d %s", rec.Code, rec.Body)
	}
	if rec := serve(r, path); rec.Code == http.StatusOK {
		t.Fatalf("no internal key: %d", rec.Code)
	}
	if rec := serve(r, path, internalKeyHeader, recordInternalKey, "X-User-Id", f.viewer.String()); rec.Code != http.StatusForbidden {
		t.Fatalf("a user's request through the key: %d", rec.Code)
	}
	if rec := serve(r, "/v1/media/internal/sounds/nope/extras", internalKeyHeader, recordInternalKey); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d", rec.Code)
	}
	fake.extrasErr = service.ErrSoundNotFound
	if rec := serve(r, path, internalKeyHeader, recordInternalKey); rec.Code != http.StatusNotFound {
		t.Fatalf("missing: %d", rec.Code)
	}
	fake.extrasErr = errors.New("db down")
	if rec := serve(r, path, internalKeyHeader, recordInternalKey); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("fault: %d", rec.Code)
	}
	if rec := serve(trendingRouter(f, nil), path, internalKeyHeader, recordInternalKey); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unwired: %d", rec.Code)
	}
}
