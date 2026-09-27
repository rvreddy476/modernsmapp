package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// MTube contracts (2026-09-27) over httptest with the service faked: the
// download redirect and the creator caption list, asserted against the
// fixtures in testdata/contracts/mtube.

func mtubeFixture(t *testing.T, name string) any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "contracts", "mtube", name))
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("fixture %s is not JSON: %v", name, err)
	}
	return v
}

func assertBodyMatchesFixture(t *testing.T, body []byte, fixture string) {
	t.Helper()
	var got any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("response is not JSON: %v\n%s", err, body)
	}
	want := mtubeFixture(t, fixture)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("response differs from %s\n got: %s\nwant fixture", fixture, body)
	}
}

// ── download ────────────────────────────────────────────────────────────

type fakeDownloadService struct {
	owner   uuid.UUID
	allowed map[uuid.UUID]bool // viewers post-service says yes to
	missing bool
	outage  bool
	calls   []uuid.UUID
}

func (f *fakeDownloadService) DownloadURL(_ context.Context, viewerID, mediaID uuid.UUID) (string, error) {
	f.calls = append(f.calls, viewerID)
	switch {
	case f.missing:
		return "", service.ErrAssetNotFound
	case f.outage:
		return "", delivery.ErrDeliveryUnresolved
	case viewerID != uuid.Nil && viewerID == f.owner, f.allowed[viewerID]:
		return "https://cdn.example/uploads/" + mediaID.String() + "/720p?sig=1&response-content-disposition=attachment", nil
	}
	return "", service.ErrDownloadNotAllowed
}

func downloadRouter(fake *fakeDownloadService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	(&Handler{downloads: fake}).RegisterRoutes(r, passthrough, passthrough)
	return r
}

func mtubeGet(r *gin.Engine, path, viewerID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if viewerID != "" {
		req.Header.Set("X-User-Id", viewerID)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestDownloadRedirectsTheOwnerAndAllowedViewers(t *testing.T) {
	owner, viewer, mediaID := uuid.New(), uuid.New(), uuid.New()
	fake := &fakeDownloadService{owner: owner, allowed: map[uuid.UUID]bool{viewer: true}}
	r := downloadRouter(fake)
	for _, who := range []uuid.UUID{owner, viewer} {
		rec := mtubeGet(r, "/v1/media/"+mediaID.String()+"/download", who.String())
		if rec.Code != http.StatusTemporaryRedirect {
			t.Fatalf("%s: status=%d body=%s", who, rec.Code, rec.Body.String())
		}
		loc := rec.Header().Get("Location")
		if !strings.Contains(loc, "/720p?") || !strings.Contains(loc, "response-content-disposition=attachment") {
			t.Fatalf("Location %q is not the signed attachment URL", loc)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "private, max-age=60" {
			t.Fatalf("Cache-Control=%q: a download redirect must stay private and short", cc)
		}
		if vary := rec.Header().Get("Vary"); !strings.Contains(vary, "X-User-Id") {
			t.Fatalf("Vary=%q must name the viewer header", vary)
		}
	}
	if len(fake.calls) != 2 || fake.calls[0] != owner || fake.calls[1] != viewer {
		t.Fatalf("service saw viewers %v", fake.calls)
	}
}

func TestDownloadRefusesStrangersAndAnonymousWith403(t *testing.T) {
	owner, stranger, mediaID := uuid.New(), uuid.New(), uuid.New()
	r := downloadRouter(&fakeDownloadService{owner: owner})
	for _, who := range []string{stranger.String(), ""} {
		rec := mtubeGet(r, "/v1/media/"+mediaID.String()+"/download", who)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("viewer %q: status=%d body=%s", who, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Location") != "" {
			t.Fatal("a refused download must not leak a Location")
		}
		assertBodyMatchesFixture(t, rec.Body.Bytes(), "download_not_allowed.json")
	}
}

func TestDownloadErrorMapping(t *testing.T) {
	mediaID := uuid.New()
	if rec := mtubeGet(downloadRouter(&fakeDownloadService{missing: true}), "/v1/media/"+mediaID.String()+"/download", uuid.New().String()); rec.Code != http.StatusNotFound {
		t.Fatalf("missing asset: status=%d", rec.Code)
	}
	if rec := mtubeGet(downloadRouter(&fakeDownloadService{outage: true}), "/v1/media/"+mediaID.String()+"/download", uuid.New().String()); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("authority outage: status=%d (must be retryable, never a redirect)", rec.Code)
	}
	if rec := mtubeGet(downloadRouter(&fakeDownloadService{}), "/v1/media/not-a-uuid/download", uuid.New().String()); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: status=%d", rec.Code)
	}
}

// ── captions list ───────────────────────────────────────────────────────

var (
	fixtureMediaA = uuid.MustParse("5d1d1e4e-0c1a-4d3e-9a7b-3b1e6c2f8a01")
	fixtureMediaB = uuid.MustParse("0b6a2c7d-8e9f-4a1b-8c2d-4e5f6a7b8c02")
	fixtureSubID  = uuid.MustParse("7c3f9a2b-1d4e-4f5a-9b6c-7d8e9f0a1b03")
)

func fixtureTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

type fakeCaptionsMine struct {
	owner      uuid.UUID
	lastStatus string
	lastCursor string
	lastLimit  int
	patched    []string
}

func (f *fakeCaptionsMine) ListMyCaptions(_ context.Context, callerID uuid.UUID, status, cursor string, limit int) (*service.CaptionListPage, error) {
	f.lastStatus, f.lastCursor, f.lastLimit = status, cursor, limit
	if status == "bogus" {
		return nil, service.ErrInvalidCaptionFilter
	}
	if callerID != f.owner {
		return &service.CaptionListPage{Items: []service.CaptionMediaItem{}}, nil
	}
	return &service.CaptionListPage{
		Items: []service.CaptionMediaItem{
			{
				MediaID: fixtureMediaA, ModifiedAt: fixtureTime("2026-09-27T10:30:00Z"),
				Languages: []postgres.SubtitleLanguageRow{
					{Language: "en", Source: "manual", Published: true, UpdatedAt: fixtureTime("2026-09-27T09:15:00Z")},
					{Language: "hi", Source: "auto_generated", Published: false, UpdatedAt: fixtureTime("2026-09-27T10:30:00Z")},
				},
			},
			{
				MediaID: fixtureMediaB, ModifiedAt: fixtureTime("2026-09-26T18:00:00Z"),
				Languages: []postgres.SubtitleLanguageRow{
					{Language: "en", Source: "auto_generated", Published: false, UpdatedAt: fixtureTime("2026-09-26T18:00:00Z")},
				},
			},
		},
		NextCursor: "MjAyNi0wOS0yNlQxODowMDowMFp8MGI2YTJjN2QtOGU5Zi00YTFiLThjMmQtNGU1ZjZhN2I4YzAy",
	}, nil
}

func (f *fakeCaptionsMine) SetCaptionPublished(_ context.Context, callerID, mediaID uuid.UUID, language string, published bool) (*postgres.MediaSubtitle, error) {
	f.patched = append(f.patched, callerID.String()+"/"+mediaID.String()+"/"+language)
	if callerID != f.owner {
		return nil, service.ErrNotMediaOwner
	}
	if language != "hi" {
		return nil, service.ErrCaptionTrackNotFound
	}
	return &postgres.MediaSubtitle{
		ID: fixtureSubID, MediaAssetID: mediaID, Language: "hi", Source: "auto_generated", Format: "vtt",
		ContentURL: "/v1/subtitles/" + mediaID.String() + "/track/hi.vtt",
		CreatedAt:  fixtureTime("2026-09-27T10:30:00Z"), Published: published, UpdatedAt: fixtureTime("2026-09-27T11:00:00Z"),
	}, nil
}

func captionsMineRouter(fake *fakeCaptionsMine) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// authMW here is the real contract's shape: no verified user, no route.
	authMW := func(c *gin.Context) {
		if c.GetHeader("X-User-Id") == "" {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Next()
	}
	(&Handler{captionsMine: fake, subtitles: &fakeClipsService{}}).RegisterClipsRoutes(r, authMW)
	return r
}

func TestCaptionsMineListsTheCallersTracksGroupedPerMedia(t *testing.T) {
	owner := uuid.New()
	fake := &fakeCaptionsMine{owner: owner}
	r := captionsMineRouter(fake)
	rec := mtubeGet(r, "/v1/subtitles/mine?status=all&limit=2", owner.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertBodyMatchesFixture(t, rec.Body.Bytes(), "subtitles_mine.json")
	if fake.lastStatus != "all" || fake.lastLimit != 2 {
		t.Fatalf("service received status=%q limit=%d", fake.lastStatus, fake.lastLimit)
	}

	// Another creator sees their own (empty) list, never the owner's.
	rec = mtubeGet(r, "/v1/subtitles/mine", uuid.New().String())
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Fatalf("stranger: status=%d body=%s", rec.Code, rec.Body.String())
	}
	// Anonymous is refused by authMW, and "mine" is never read as a media id.
	if rec = mtubeGet(r, "/v1/subtitles/mine", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec = mtubeGet(r, "/v1/subtitles/mine?status=bogus", owner.String()); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad status filter: %d", rec.Code)
	}
	if rec = mtubeGet(r, "/v1/subtitles/mine?limit=0", owner.String()); rec.Code != http.StatusBadRequest {
		t.Fatalf("limit=0: %d", rec.Code)
	}
}

func TestCaptionPublishIsOwnerOnly(t *testing.T) {
	owner, stranger := uuid.New(), uuid.New()
	fake := &fakeCaptionsMine{owner: owner}
	r := captionsMineRouter(fake)
	patch := func(viewer, language, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPatch, "/v1/subtitles/"+fixtureMediaA.String()+"/"+language, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if viewer != "" {
			req.Header.Set("X-User-Id", viewer)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	rec := patch(owner.String(), "hi", `{"published":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner: status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertBodyMatchesFixture(t, rec.Body.Bytes(), "subtitle_publish.json")

	if rec = patch(stranger.String(), "hi", `{"published":true}`); rec.Code != http.StatusForbidden {
		t.Fatalf("stranger: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec = patch("", "hi", `{"published":true}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: status=%d", rec.Code)
	}
	if rec = patch(owner.String(), "fr", `{"published":false}`); rec.Code != http.StatusNotFound {
		t.Fatalf("missing track: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec = patch(owner.String(), "hi", `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing published: status=%d", rec.Code)
	}
	// The existing PATCH /:mediaId (transcript correction) is untouched:
	// a two-segment PATCH still routes to the publish handler only.
	if want := []string{owner.String() + "/" + fixtureMediaA.String() + "/hi", stranger.String() + "/" + fixtureMediaA.String() + "/hi", owner.String() + "/" + fixtureMediaA.String() + "/fr"}; !reflect.DeepEqual(fake.patched, want) {
		t.Fatalf("service saw %v, want %v", fake.patched, want)
	}
}

func TestSubtitleReadsStillReachTheOldHandlers(t *testing.T) {
	// Registering /mine and /:mediaId/:language must not shadow the track
	// route or the per-media list.
	fake := &fakeClipsService{authorizeErr: delivery.ErrDeliveryDenied}
	r := captionsMineRouter(&fakeCaptionsMine{})
	_ = fake
	found := map[string]string{}
	for _, ri := range r.Routes() {
		found[ri.Method+" "+ri.Path] = ri.Handler
	}
	for key, handler := range map[string]string{
		"GET /v1/subtitles/mine":                     ".ListMyCaptions",
		"PATCH /v1/subtitles/:mediaId/:language":     ".SetCaptionPublished",
		"GET /v1/subtitles/:mediaId":                 ".GetSubtitles",
		"GET /v1/subtitles/:mediaId/track/:language": ".ServeSubtitleTrack",
		"PATCH /v1/subtitles/:mediaId":               ".CorrectCaption",
	} {
		if h, ok := found[key]; !ok || !strings.Contains(h, handler) {
			t.Errorf("%s served by %q, want %s", key, h, handler)
		}
	}
	if !errors.Is(fake.authorizeErr, delivery.ErrDeliveryDenied) {
		t.Fatal("unreachable")
	}
}
