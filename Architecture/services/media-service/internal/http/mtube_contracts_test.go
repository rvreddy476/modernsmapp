package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// fakeDownloadService is the service's decision: the uploader, or a trusted
// service. postAllows is the set of viewers whose post allows download —
// kept in the fake to prove it changes nothing.
type fakeDownloadService struct {
	owner        uuid.UUID
	postAllows   map[uuid.UUID]bool
	missing      bool
	outage       bool
	calls        []uuid.UUID
	serviceCalls int
}

const fakeDownloadLocation = "https://cdn.example/uploads/%s/720p?sig=1&response-content-disposition=attachment"

func (f *fakeDownloadService) DownloadURL(_ context.Context, viewerID, mediaID uuid.UUID) (string, error) {
	f.calls = append(f.calls, viewerID)
	switch {
	case f.missing:
		return "", service.ErrAssetNotFound
	case f.outage:
		return "", delivery.ErrDeliveryUnresolved
	case viewerID != uuid.Nil && viewerID == f.owner:
		return fmt.Sprintf(fakeDownloadLocation, mediaID), nil
	}
	return "", service.ErrDownloadNotAllowed
}

func (f *fakeDownloadService) DownloadURLForService(_ context.Context, mediaID uuid.UUID) (string, error) {
	f.serviceCalls++
	if f.missing {
		return "", service.ErrAssetNotFound
	}
	return fmt.Sprintf(fakeDownloadLocation, mediaID), nil
}

const downloadTestKey = "internal-key-for-tests"

func downloadRouter(fake *fakeDownloadService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	(&Handler{downloads: fake, internalKey: downloadTestKey}).RegisterRoutes(r, passthrough, passthrough)
	return r
}

func mtubeGet(r *gin.Engine, path, viewerID string) *httptest.ResponseRecorder {
	return mtubeGetWith(r, path, viewerID, "")
}

func mtubeGetWith(r *gin.Engine, path, viewerID, internalKey string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if viewerID != "" {
		req.Header.Set("X-User-Id", viewerID)
	}
	if internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", internalKey)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestDownloadRedirectsTheOwner(t *testing.T) {
	owner, mediaID := uuid.New(), uuid.New()
	fake := &fakeDownloadService{owner: owner}
	r := downloadRouter(fake)
	rec := mtubeGet(r, "/v1/media/"+mediaID.String()+"/download", owner.String())
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("owner: status=%d body=%s", rec.Code, rec.Body.String())
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
	if len(fake.calls) != 1 || fake.calls[0] != owner || fake.serviceCalls != 0 {
		t.Fatalf("service saw viewers %v, service calls %d", fake.calls, fake.serviceCalls)
	}
}

// Owner only (2026-10-02): a stranger, an anonymous caller and a viewer
// whose post allows download all get the 404 of an asset that does not
// exist — the same bytes, so the route says nothing about which ids are
// real — and never a Location.
func TestDownloadRefusesEveryoneButTheOwnerWith404(t *testing.T) {
	owner, stranger, allowed, mediaID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	missing := mtubeGet(downloadRouter(&fakeDownloadService{missing: true}), "/v1/media/"+mediaID.String()+"/download", stranger.String())
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing asset: status=%d", missing.Code)
	}
	r := downloadRouter(&fakeDownloadService{owner: owner, postAllows: map[uuid.UUID]bool{allowed: true}})
	for name, who := range map[string]string{"stranger": stranger.String(), "anonymous": "", "post allows download": allowed.String()} {
		rec := mtubeGet(r, "/v1/media/"+mediaID.String()+"/download", who)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status=%d body=%s, want 404", name, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Location") != "" {
			t.Fatalf("%s: a refused download must not leak a Location", name)
		}
		if rec.Body.String() != missing.Body.String() {
			t.Fatalf("%s: the refusal differs from a missing asset\n got: %s\nwant: %s", name, rec.Body.String(), missing.Body.String())
		}
		assertBodyMatchesFixture(t, rec.Body.Bytes(), "download_not_found.json")
	}
}

// The administrator path is the internal key with NO viewer identity. A
// request that names a viewer is judged as that viewer whatever key it
// carries, and a wrong or absent key is an ordinary caller.
func TestDownloadAdministratorPathNeedsTheKeyAndNoViewer(t *testing.T) {
	owner, stranger, mediaID := uuid.New(), uuid.New(), uuid.New()
	path := "/v1/media/" + mediaID.String() + "/download"
	cases := []struct {
		name, viewer, key string
		status            int
		serviceCalls      int
	}{
		{"internal key, no viewer", "", downloadTestKey, http.StatusTemporaryRedirect, 1},
		{"internal key, a stranger's identity", stranger.String(), downloadTestKey, http.StatusNotFound, 0},
		{"internal key, the owner's identity", owner.String(), downloadTestKey, http.StatusTemporaryRedirect, 0},
		{"wrong key, no viewer", "", "not-the-key", http.StatusNotFound, 0},
		{"no key, no viewer", "", "", http.StatusNotFound, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeDownloadService{owner: owner}
			rec := mtubeGetWith(downloadRouter(fake), path, tc.viewer, tc.key)
			if rec.Code != tc.status || fake.serviceCalls != tc.serviceCalls {
				t.Fatalf("status=%d serviceCalls=%d, want %d and %d", rec.Code, fake.serviceCalls, tc.status, tc.serviceCalls)
			}
		})
	}
	// A deployment with no internal key configured has no administrator path,
	// whatever the request presents.
	fake := &fakeDownloadService{owner: owner}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	(&Handler{downloads: fake}).RegisterRoutes(r, passthrough, passthrough)
	for _, key := range []string{"", "anything"} {
		if rec := mtubeGetWith(r, path, "", key); rec.Code != http.StatusNotFound || fake.serviceCalls != 0 {
			t.Fatalf("no key configured, presented %q: status=%d serviceCalls=%d", key, rec.Code, fake.serviceCalls)
		}
	}
}

func TestDownloadErrorMapping(t *testing.T) {
	mediaID := uuid.New()
	if rec := mtubeGet(downloadRouter(&fakeDownloadService{missing: true}), "/v1/media/"+mediaID.String()+"/download", uuid.New().String()); rec.Code != http.StatusNotFound {
		t.Fatalf("missing asset: status=%d", rec.Code)
	}
	if rec := mtubeGet(downloadRouter(&fakeDownloadService{outage: true}), "/v1/media/"+mediaID.String()+"/download", uuid.New().String()); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("outage: status=%d (must be retryable, never a redirect)", rec.Code)
	}
	if rec := mtubeGet(downloadRouter(&fakeDownloadService{}), "/v1/media/not-a-uuid/download", uuid.New().String()); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: status=%d", rec.Code)
	}
	// Every refusal the service or the gate can produce is the same 404.
	for _, err := range []error{service.ErrDownloadNotAllowed, delivery.ErrDeliveryDenied, service.ErrAssetNotFound, service.ErrDownloadNotVideo} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		writeDownloadError(c, err)
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "DOWNLOAD_NOT_ALLOWED") {
			t.Fatalf("%v: status=%d body=%s", err, rec.Code, rec.Body.String())
		}
	}
}

// Playback is not a download. The redirect every /serve route answers with
// carries no Content-Disposition of its own, and the stream path (the
// anonymous-scope assets this service serves itself) answers a Range with
// 206 + Content-Range and no disposition either.
func TestServeAnswersAreNeverAnAttachment(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/media/x/serve/720p", nil)
	writeDeliveryRedirect(c, "https://cdn.example/uploads/u/m/720p?Expires=1&Signature=s&Key-Pair-Id=k")
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status=%d", rec.Code)
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != "" {
		t.Fatalf("a playback redirect carries Content-Disposition %q", cd)
	}

	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/media/x/serve/720p", nil)
	writeStream(c, &service.StreamResult{Data: []byte("abcd"), ContentType: "video/mp4", Partial: true, Start: 4, End: 7, Size: 100})
	if rec.Code != http.StatusPartialContent || rec.Header().Get("Content-Range") != "bytes 4-7/100" ||
		rec.Header().Get("Accept-Ranges") != "bytes" || rec.Header().Get("Content-Disposition") != "" {
		t.Fatalf("stream: status=%d headers=%v", rec.Code, rec.Header())
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
