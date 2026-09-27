package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/dubbing"
	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Alternate audio tracks over httptest: the REAL handler and the REAL
// service.AudioTracks, with the store, the blob store, the read gate and
// ffprobe faked. No PostgreSQL, no ffmpeg.

// ── fakes ───────────────────────────────────────────────────────────────

type fakeAudioTrackStore struct {
	media    map[uuid.UUID]*postgres.MediaAsset
	tracks   []postgres.MediaAudioTrack
	replaced []postgres.MediaAudioTrack
	deleted  []uuid.UUID
	requeued []uuid.UUID
}

func (f *fakeAudioTrackStore) GetMediaWithVariants(_ context.Context, id uuid.UUID) (*postgres.MediaAsset, error) {
	m, ok := f.media[id]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	cp := *m
	return &cp, nil
}

func (f *fakeAudioTrackStore) InsertVariants(_ context.Context, vs []postgres.MediaVariant) error {
	for _, v := range vs {
		m := f.media[v.MediaAssetID]
		m.Variants = append(m.Variants, v)
	}
	return nil
}

func (f *fakeAudioTrackStore) ReplaceMediaAudioTrack(_ context.Context, t *postgres.MediaAudioTrack) ([]string, error) {
	var stale []string
	kept := f.tracks[:0]
	for _, old := range f.tracks {
		if old.MediaAssetID == t.MediaAssetID && old.Language == t.Language {
			if old.SourceKey != nil {
				stale = append(stale, *old.SourceKey)
			}
			continue
		}
		kept = append(kept, old)
	}
	f.tracks = kept
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	t.CreatedAt = time.Now()
	t.UpdatedAt = t.CreatedAt
	f.tracks = append(f.tracks, *t)
	f.replaced = append(f.replaced, *t)
	return stale, nil
}

func (f *fakeAudioTrackStore) ListMediaAudioTracks(_ context.Context, mediaID uuid.UUID) ([]postgres.MediaAudioTrack, error) {
	var out []postgres.MediaAudioTrack
	for _, t := range f.tracks {
		if t.MediaAssetID == mediaID {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *fakeAudioTrackStore) CountMediaAudioTracks(ctx context.Context, mediaID uuid.UUID) (int, error) {
	ts, _ := f.ListMediaAudioTracks(ctx, mediaID)
	return len(ts), nil
}

func (f *fakeAudioTrackStore) GetMediaAudioTrack(_ context.Context, mediaID, trackID uuid.UUID) (*postgres.MediaAudioTrack, error) {
	for _, t := range f.tracks {
		if t.MediaAssetID == mediaID && t.ID == trackID {
			cp := t
			return &cp, nil
		}
	}
	return nil, postgres.ErrAudioTrackNotFound
}

func (f *fakeAudioTrackStore) DeleteMediaAudioTrack(_ context.Context, mediaID, trackID uuid.UUID) ([]string, error) {
	for i, t := range f.tracks {
		if t.MediaAssetID == mediaID && t.ID == trackID {
			f.tracks = append(f.tracks[:i], f.tracks[i+1:]...)
			f.deleted = append(f.deleted, trackID)
			var keys []string
			if t.SourceKey != nil {
				keys = append(keys, *t.SourceKey)
			}
			return keys, nil
		}
	}
	return nil, postgres.ErrAudioTrackNotFound
}

func (f *fakeAudioTrackStore) ClaimMediaAudioTracks(context.Context, time.Duration, int) ([]postgres.MediaAudioTrack, error) {
	return nil, nil
}
func (f *fakeAudioTrackStore) SetMediaAudioTrackSourceKey(context.Context, uuid.UUID, string, *string) error {
	return nil
}
func (f *fakeAudioTrackStore) CompleteMediaAudioTrack(context.Context, uuid.UUID, []string, *string) error {
	return nil
}
func (f *fakeAudioTrackStore) FailMediaAudioTrack(context.Context, uuid.UUID, string, *string) error {
	return nil
}
func (f *fakeAudioTrackStore) ReleaseMediaAudioTrack(context.Context, uuid.UUID, string, *string) error {
	return nil
}
func (f *fakeAudioTrackStore) RequeueMediaAudioTrack(_ context.Context, id uuid.UUID) error {
	f.requeued = append(f.requeued, id)
	return nil
}

type fakeAudioBlobs struct {
	objects map[string][]byte
	deleted []string
}

func (b *fakeAudioBlobs) DownloadObject(_ context.Context, key string) ([]byte, error) {
	d, ok := b.objects[key]
	if !ok {
		return nil, fmt.Errorf("no object %s", key)
	}
	return d, nil
}
func (b *fakeAudioBlobs) UploadObject(_ context.Context, key string, data []byte, _ string) error {
	b.objects[key] = data
	return nil
}
func (b *fakeAudioBlobs) DeleteObject(_ context.Context, key string) error {
	b.deleted = append(b.deleted, key)
	delete(b.objects, key)
	return nil
}

// ── fixture ─────────────────────────────────────────────────────────────

type audioTrackFixture struct {
	router   *gin.Engine
	store    *fakeAudioTrackStore
	blobs    *fakeAudioBlobs
	svc      *service.AudioTracks
	owner    uuid.UUID
	other    uuid.UUID
	mediaID  uuid.UUID
	media    *postgres.MediaAsset
	denyRead error
	probeMs  int
}

func newAudioTrackFixture(t *testing.T) *audioTrackFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	f := &audioTrackFixture{
		owner: uuid.New(), other: uuid.New(), mediaID: uuid.New(), probeMs: 30_000,
	}
	durationMs := 30_000
	w, h := 720, 1280
	f.media = &postgres.MediaAsset{
		ID: f.mediaID, UploaderID: f.owner, FileType: "video", MimeType: "video/mp4",
		StorageKey:       fmt.Sprintf("user/%s/%s/original", f.owner, f.mediaID),
		ProcessingStatus: "ready", DurationMs: &durationMs, Width: &w, Height: &h,
		Variants: []postgres.MediaVariant{
			{MediaAssetID: f.mediaID, Name: "360p", Mime: "video/mp4", ObjectKey: "k360"},
			{MediaAssetID: f.mediaID, Name: "720p", Mime: "video/mp4", ObjectKey: "k720"},
			{MediaAssetID: f.mediaID, Name: "thumb_150", Mime: "image/jpeg", ObjectKey: "kthumb"},
		},
	}
	f.store = &fakeAudioTrackStore{media: map[uuid.UUID]*postgres.MediaAsset{f.mediaID: f.media}}
	f.blobs = &fakeAudioBlobs{objects: map[string][]byte{}}
	authorize := func(context.Context, uuid.UUID, uuid.UUID) error { return f.denyRead }
	f.svc = service.NewAudioTracks(f.store, f.blobs, authorize, nil).
		WithAudioProbe(func(context.Context, string) (*processing.AudioMeta, error) {
			return &processing.AudioMeta{DurationMs: f.probeMs, Codec: "aac"}, nil
		})
	f.router = gin.New()
	pass := func(c *gin.Context) { c.Next() }
	(&Handler{audioTracks: f.svc}).RegisterRoutes(f.router, pass, pass)
	return f
}

func (f *audioTrackFixture) do(req *http.Request, userID uuid.UUID) *httptest.ResponseRecorder {
	if userID != uuid.Nil {
		req.Header.Set("X-User-Id", userID.String())
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func (f *audioTrackFixture) upload(t *testing.T, userID uuid.UUID, language, label string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("language", language)
	if label != "" {
		_ = w.WriteField("label", label)
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="file"; filename="track.m4a"`)
	h.Set("Content-Type", "audio/mp4")
	part, _ := w.CreatePart(h)
	_, _ = part.Write([]byte("fake-audio"))
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/media/"+f.mediaID.String()+"/audio-tracks", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return f.do(req, userID)
}

func (f *audioTrackFixture) generate(userID uuid.UUID, language string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/media/"+f.mediaID.String()+"/audio-tracks/generate",
		strings.NewReader(`{"language":"`+language+`"}`))
	req.Header.Set("Content-Type", "application/json")
	return f.do(req, userID)
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v (%s)", err, rec.Body.String())
	}
	return env.Error.Code
}

// ── route inventory ─────────────────────────────────────────────────────

func TestAudioTrackRoutesAreRegistered(t *testing.T) {
	f := newAudioTrackFixture(t)
	want := map[string]bool{
		"GET /v1/media/:mediaId/audio-tracks":             false,
		"POST /v1/media/:mediaId/audio-tracks":            false,
		"POST /v1/media/:mediaId/audio-tracks/generate":   false,
		"DELETE /v1/media/:mediaId/audio-tracks/:trackId": false,
	}
	for _, r := range f.router.Routes() {
		key := r.Method + " " + r.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for route, seen := range want {
		if !seen {
			t.Errorf("route not registered: %s", route)
		}
	}
}

// ── authentication and ownership ────────────────────────────────────────

func TestAudioTrackAnonymousWriteIs401(t *testing.T) {
	f := newAudioTrackFixture(t)
	if rec := f.upload(t, uuid.Nil, "hi", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous upload: got %d want 401", rec.Code)
	}
	if rec := f.generate(uuid.Nil, "hi"); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous generate: got %d want 401", rec.Code)
	}
	req := httptest.NewRequest(http.MethodDelete, "/v1/media/"+f.mediaID.String()+"/audio-tracks/"+uuid.New().String(), nil)
	if rec := f.do(req, uuid.Nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous delete: got %d want 401", rec.Code)
	}
}

// The owner gate. Neutering the uploader comparison in
// service.AudioTracks.loadOwnedVideo makes every assertion here fail.
func TestAudioTrackNonOwnerIs403(t *testing.T) {
	f := newAudioTrackFixture(t)
	if rec := f.upload(t, f.other, "hi", ""); rec.Code != http.StatusForbidden || errorCode(t, rec) != "FORBIDDEN" {
		t.Errorf("non-owner upload: got %d %s want 403 FORBIDDEN", rec.Code, rec.Body.String())
	}
	if rec := f.generate(f.other, "hi"); rec.Code != http.StatusForbidden {
		t.Errorf("non-owner generate: got %d want 403", rec.Code)
	}
	if len(f.store.replaced) != 0 {
		t.Errorf("nothing may be written for a non-owner")
	}

	// Seed a track the owner made, then try to delete it as someone else.
	ok := f.upload(t, f.owner, "hi", "")
	if ok.Code != http.StatusCreated {
		t.Fatalf("owner upload: %d %s", ok.Code, ok.Body.String())
	}
	trackID := f.store.tracks[0].ID
	req := httptest.NewRequest(http.MethodDelete, "/v1/media/"+f.mediaID.String()+"/audio-tracks/"+trackID.String(), nil)
	if rec := f.do(req, f.other); rec.Code != http.StatusForbidden {
		t.Errorf("non-owner delete: got %d want 403", rec.Code)
	}
	if len(f.store.deleted) != 0 || len(f.store.tracks) != 1 {
		t.Errorf("non-owner delete must not remove the track")
	}
}

func TestAudioTrackUnknownMediaIs404ForOwnerRoutes(t *testing.T) {
	f := newAudioTrackFixture(t)
	f.mediaID = uuid.New() // not in the store
	if rec := f.upload(t, f.owner, "hi", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown media upload: got %d want 404", rec.Code)
	}
}

// ── state and validation ────────────────────────────────────────────────

func TestAudioTrackNotReadyVideoIs409(t *testing.T) {
	f := newAudioTrackFixture(t)
	f.media.ProcessingStatus = "processing"
	rec := f.upload(t, f.owner, "hi", "")
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "MEDIA_NOT_READY" {
		t.Errorf("not-ready upload: got %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.generate(f.owner, "hi"); rec.Code != http.StatusConflict {
		t.Errorf("not-ready generate: got %d want 409", rec.Code)
	}
}

func TestAudioTrackDurationMismatchIs422(t *testing.T) {
	f := newAudioTrackFixture(t)
	f.probeMs = 45_000 // video is 30 s; tolerance is max(3 s, 5 %) = 3 s
	rec := f.upload(t, f.owner, "hi", "")
	if rec.Code != http.StatusUnprocessableEntity || errorCode(t, rec) != "AUDIO_DURATION_MISMATCH" {
		t.Errorf("mismatch: got %d %s", rec.Code, rec.Body.String())
	}
	if len(f.blobs.objects) != 0 || len(f.store.tracks) != 0 {
		t.Errorf("a rejected upload must store nothing")
	}

	f.probeMs = 32_000 // inside the 3 s tolerance
	if rec := f.upload(t, f.owner, "hi", ""); rec.Code != http.StatusCreated {
		t.Errorf("within tolerance: got %d %s", rec.Code, rec.Body.String())
	}
}

func TestAudioTrackEleventhLanguageIs422(t *testing.T) {
	f := newAudioTrackFixture(t)
	langs := []string{"en", "hi", "ta", "te", "kn", "ml", "mr", "bn", "gu", "pa"}
	for _, l := range langs {
		if rec := f.upload(t, f.owner, l, ""); rec.Code != http.StatusCreated {
			t.Fatalf("upload %s: %d %s", l, rec.Code, rec.Body.String())
		}
	}
	rec := f.upload(t, f.owner, "ur", "")
	if rec.Code != http.StatusUnprocessableEntity || errorCode(t, rec) != "AUDIO_TRACK_LIMIT" {
		t.Errorf("11th language: got %d %s", rec.Code, rec.Body.String())
	}
	// Replacing an existing language is not an 11th track.
	if rec := f.upload(t, f.owner, "hi", ""); rec.Code != http.StatusCreated {
		t.Errorf("replace within the limit: got %d %s", rec.Code, rec.Body.String())
	}
	f.svc.WithDubber(dubbing.StubDubber{})
	if rec := f.generate(f.owner, "ur"); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("11th via generate: got %d want 422", rec.Code)
	}
}

func TestAudioTrackInvalidInputIs400(t *testing.T) {
	f := newAudioTrackFixture(t)
	if rec := f.upload(t, f.owner, "not a tag!", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("bad language: got %d", rec.Code)
	}
	if rec := f.upload(t, f.owner, "hi", strings.Repeat("x", 61)); rec.Code != http.StatusBadRequest {
		t.Errorf("long label: got %d", rec.Code)
	}
}

func TestGenerateWithoutBackendIs503(t *testing.T) {
	f := newAudioTrackFixture(t)
	rec := f.generate(f.owner, "hi")
	if rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "DUBBING_UNAVAILABLE" {
		t.Errorf("no dubber: got %d %s", rec.Code, rec.Body.String())
	}
	if len(f.store.tracks) != 0 {
		t.Errorf("no track may be queued without a backend")
	}
}

func TestGenerateWithBackendIs202Pending(t *testing.T) {
	f := newAudioTrackFixture(t)
	f.svc.WithDubber(dubbing.StubDubber{})
	req := httptest.NewRequest(http.MethodPost, "/v1/media/"+f.mediaID.String()+"/audio-tracks/generate",
		strings.NewReader(`{"language":"pt-BR","source_language":"en"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := f.do(req, f.owner)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("generate: got %d %s", rec.Code, rec.Body.String())
	}
	var env struct{ Data service.AudioTrackView }
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Data.Source != "generated" || env.Data.Status != "pending" || env.Data.Language != "pt-br" ||
		env.Data.Label != "Portuguese (Brazil)" || env.Data.PlaybackURL != "" {
		t.Errorf("generated track view: %+v", env.Data)
	}
	if got := f.store.tracks[0]; got.SourceLanguage == nil || *got.SourceLanguage != "en" || got.SourceKey != nil {
		t.Errorf("stored generated row: %+v", got)
	}
}

// ── upload persistence ──────────────────────────────────────────────────

func TestUploadStoresSourceUnderTheVideoPrefixAndReplacesSameLanguage(t *testing.T) {
	f := newAudioTrackFixture(t)
	rec := f.upload(t, f.owner, "HI", "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	var env struct{ Data service.AudioTrackView }
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Data.Language != "hi" || env.Data.Label != "Hindi" || env.Data.Source != "uploaded" ||
		env.Data.Status != "pending" || env.Data.PlaybackURL != "" || env.Data.ID == "" {
		t.Errorf("track view: %+v", env.Data)
	}
	wantKey := fmt.Sprintf("user/%s/%s/dub/hi/source", f.owner, f.mediaID)
	if _, ok := f.blobs.objects[wantKey]; !ok {
		t.Errorf("source not stored at %s; objects: %v", wantKey, f.blobs.objects)
	}
	first := f.store.tracks[0].ID

	// Same language again: one row, the new id, the object still present.
	if rec := f.upload(t, f.owner, "hi", "Hindi (studio)"); rec.Code != http.StatusCreated {
		t.Fatalf("replace: %d %s", rec.Code, rec.Body.String())
	}
	if len(f.store.tracks) != 1 || f.store.tracks[0].ID == first || f.store.tracks[0].Label != "Hindi (studio)" {
		t.Errorf("replacement rows: %+v", f.store.tracks)
	}
	if _, ok := f.blobs.objects[wantKey]; !ok {
		t.Errorf("replacing a language must not delete the new source object (same key)")
	}
}

func TestDeleteRemovesRowAndObjects(t *testing.T) {
	f := newAudioTrackFixture(t)
	if rec := f.upload(t, f.owner, "hi", ""); rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	trackID := f.store.tracks[0].ID
	req := httptest.NewRequest(http.MethodDelete, "/v1/media/"+f.mediaID.String()+"/audio-tracks/"+trackID.String(), nil)
	if rec := f.do(req, f.owner); rec.Code != http.StatusNoContent {
		t.Errorf("delete: got %d %s", rec.Code, rec.Body.String())
	}
	if len(f.store.tracks) != 0 || len(f.blobs.objects) != 0 {
		t.Errorf("delete must remove the row and the source: %v %v", f.store.tracks, f.blobs.objects)
	}
	if rec := f.do(httptest.NewRequest(http.MethodDelete, req.URL.String(), nil), f.owner); rec.Code != http.StatusNotFound {
		t.Errorf("deleting again: got %d want 404", rec.Code)
	}
}

// ── list ────────────────────────────────────────────────────────────────

func TestListIsOriginalFirstThenByCreatedAt(t *testing.T) {
	f := newAudioTrackFixture(t)
	base := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	src := "k"
	errText := "provider down"
	f.store.tracks = []postgres.MediaAudioTrack{
		{ID: uuid.New(), MediaAssetID: f.mediaID, Language: "ta", Label: "Tamil", Source: "uploaded",
			Status: "ready", SourceKey: &src, Rungs: []string{"360p", "720p"}, CreatedAt: base.Add(2 * time.Minute)},
		{ID: uuid.New(), MediaAssetID: f.mediaID, Language: "hi", Label: "Hindi", Source: "generated",
			Status: "failed", LastError: &errText, CreatedAt: base},
		{ID: uuid.New(), MediaAssetID: f.mediaID, Language: "te", Label: "Telugu", Source: "uploaded",
			Status: "processing", SourceKey: &src, CreatedAt: base.Add(time.Minute)},
	}
	// The store returns rows in created_at order (the SQL orders); the fake
	// must too, so sort as the query would.
	f.store.tracks[0], f.store.tracks[1], f.store.tracks[2] = f.store.tracks[1], f.store.tracks[2], f.store.tracks[0]
	// The ready track's muxed variants exist.
	f.media.Variants = append(f.media.Variants,
		postgres.MediaVariant{MediaAssetID: f.mediaID, Name: "dub_ta_360p", Mime: "video/mp4", ObjectKey: "d360"},
		postgres.MediaVariant{MediaAssetID: f.mediaID, Name: "dub_ta_720p", Mime: "video/mp4", ObjectKey: "d720"},
	)

	rec := f.do(httptest.NewRequest(http.MethodGet, "/v1/media/"+f.mediaID.String()+"/audio-tracks", nil), uuid.Nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data struct {
			Tracks []service.AudioTrackView `json:"tracks"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	got := env.Data.Tracks
	if len(got) != 4 {
		t.Fatalf("want original + 3 tracks, got %d: %+v", len(got), got)
	}
	if got[0].ID != "original" || got[0].Language != "und" || got[0].Label != "Original" ||
		got[0].Source != "original" || got[0].Status != "ready" {
		t.Errorf("first entry must be the original: %+v", got[0])
	}
	if got[1].Language != "hi" || got[2].Language != "te" || got[3].Language != "ta" {
		t.Errorf("tracks must follow created_at: %s %s %s", got[1].Language, got[2].Language, got[3].Language)
	}
	if got[1].Status != "failed" || got[1].Error != "provider down" || got[1].PlaybackURL != "" {
		t.Errorf("failed entry: %+v", got[1])
	}
	if got[2].Status != "processing" || got[2].PlaybackURL != "" || got[2].Rungs != nil {
		t.Errorf("processing entry must carry no playback: %+v", got[2])
	}
	wantURL := "/v1/media/" + f.mediaID.String() + "/serve/dub_ta_720p"
	if got[3].Status != "ready" || got[3].PlaybackURL != wantURL || strings.Join(got[3].Rungs, ",") != "720p,360p" {
		t.Errorf("ready entry: %+v (want %s)", got[3], wantURL)
	}
	if got[3].CreatedAt == nil {
		t.Errorf("tracks carry created_at")
	}
}

// A ready track whose dub variants were pruned (transcode re-run) is not
// reported as playable: it is re-queued and shown pending.
func TestListRequeuesReadyTrackWithoutVariants(t *testing.T) {
	f := newAudioTrackFixture(t)
	src := "k"
	id := uuid.New()
	f.store.tracks = []postgres.MediaAudioTrack{{ID: id, MediaAssetID: f.mediaID, Language: "ta", Label: "Tamil",
		Source: "uploaded", Status: "ready", SourceKey: &src, Rungs: []string{"720p"}, CreatedAt: time.Now()}}
	rec := f.do(httptest.NewRequest(http.MethodGet, "/v1/media/"+f.mediaID.String()+"/audio-tracks", nil), f.owner)
	var env struct {
		Data struct {
			Tracks []service.AudioTrackView `json:"tracks"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Data.Tracks[1].Status != "pending" || env.Data.Tracks[1].PlaybackURL != "" {
		t.Errorf("pruned track must read pending: %+v", env.Data.Tracks[1])
	}
	if len(f.store.requeued) != 1 || f.store.requeued[0] != id {
		t.Errorf("pruned track must be re-queued: %v", f.store.requeued)
	}
}

// The list is gated like /serve: a denied viewer gets the same 404 the
// serve route gives, an unresolved gate the same 503.
func TestListGoesThroughTheDeliveryGate(t *testing.T) {
	f := newAudioTrackFixture(t)
	url := "/v1/media/" + f.mediaID.String() + "/audio-tracks"

	f.denyRead = delivery.ErrDeliveryDenied
	if rec := f.do(httptest.NewRequest(http.MethodGet, url, nil), f.other); rec.Code != http.StatusNotFound {
		t.Errorf("denied: got %d want 404", rec.Code)
	}
	f.denyRead = delivery.ErrDeliveryUnresolved
	if rec := f.do(httptest.NewRequest(http.MethodGet, url, nil), f.other); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("unresolved: got %d want 503", rec.Code)
	}
	f.denyRead = nil
	if rec := f.do(httptest.NewRequest(http.MethodGet, url, nil), uuid.Nil); rec.Code != http.StatusOK {
		t.Errorf("admitted anonymous viewer: got %d want 200", rec.Code)
	}
}
