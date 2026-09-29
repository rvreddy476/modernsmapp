package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Metadata reads over httptest: the REAL handler, the REAL
// service.RecordReads and a REAL delivery.Gate, with the store, the blob
// presigner and the content authority faked. No PostgreSQL.
//
// The property under test: GET /v1/media/:id, /status and
// GET /v1/audio/:audioId/url answer to exactly the audience the bytes do,
// plus the uploader — and a denial is byte-for-byte the missing answer.

// ── fakes ───────────────────────────────────────────────────────────────

type fakeRecordStore struct {
	media  map[uuid.UUID]*postgres.MediaAsset
	tracks map[uuid.UUID]*postgres.AudioTrack
	jobs   map[uuid.UUID][]postgres.TranscodingJob
	fail   error

	// The sound lists (audio_reads_handler_test.go): listed is the order the
	// store returns ready sounds in; listFail fails the list alone; used
	// counts IncrementAudioUsageCount per track.
	listed   []uuid.UUID
	listFail error
	used     map[uuid.UUID]int
}

func (f *fakeRecordStore) page(match func(*postgres.AudioTrack) bool, limit, offset int) ([]postgres.AudioTrack, error) {
	if f.listFail != nil {
		return nil, f.listFail
	}
	var out []postgres.AudioTrack
	for _, id := range f.listed {
		t, ok := f.tracks[id]
		if !ok || t.Status != "ready" || !match(t) {
			continue
		}
		out = append(out, *t)
	}
	if offset >= len(out) {
		return nil, nil
	}
	out = out[offset:]
	if limit < len(out) {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeRecordStore) GetTrendingAudioTracks(_ context.Context, limit, offset int) ([]postgres.AudioTrack, error) {
	return f.page(func(*postgres.AudioTrack) bool { return true }, limit, offset)
}

func (f *fakeRecordStore) SearchAudioTracks(_ context.Context, query string, limit, offset int) ([]postgres.AudioTrack, error) {
	q := strings.ToLower(query)
	return f.page(func(t *postgres.AudioTrack) bool {
		return strings.Contains(strings.ToLower(t.Title), q) || strings.Contains(strings.ToLower(t.Artist), q)
	}, limit, offset)
}

func (f *fakeRecordStore) IncrementAudioUsageCount(_ context.Context, id uuid.UUID) error {
	if f.fail != nil {
		return f.fail
	}
	if f.used == nil {
		f.used = map[uuid.UUID]int{}
	}
	f.used[id]++
	return nil
}

func (f *fakeRecordStore) GetMediaWithVariants(_ context.Context, id uuid.UUID) (*postgres.MediaAsset, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	m, ok := f.media[id]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	cp := *m
	return &cp, nil
}

func (f *fakeRecordStore) GetTranscodingJobs(_ context.Context, id uuid.UUID) ([]postgres.TranscodingJob, error) {
	return f.jobs[id], nil
}

func (f *fakeRecordStore) GetAudioTrack(_ context.Context, id uuid.UUID) (*postgres.AudioTrack, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	t, ok := f.tracks[id]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	cp := *t
	return &cp, nil
}

type fakeRecordBlobs struct{ signed []string }

func (b *fakeRecordBlobs) GeneratePresignedGetURL(_ context.Context, key string, ttl time.Duration) (*url.URL, error) {
	b.signed = append(b.signed, key)
	return url.Parse("https://signed.example/" + key + "?ttl=" + ttl.String())
}

// fakeRecordAuthz stands in for post-service's media-access answer. The gate
// hands it the viewer as a string; a signed-out viewer arrives as the nil
// UUID, which delivery.AnonymousViewer recognises.
type fakeRecordAuthz struct {
	allow func(viewerID, mediaID string) bool
	err   error
	calls int
}

func (a *fakeRecordAuthz) Authorize(_ context.Context, viewerID, mediaID string) error {
	a.calls++
	if a.err != nil {
		return a.err
	}
	if a.allow != nil && a.allow(viewerID, mediaID) {
		return nil
	}
	return delivery.ErrDeliveryDenied
}

// ── fixture ─────────────────────────────────────────────────────────────

type recordFixture struct {
	router *gin.Engine
	store  *fakeRecordStore
	blobs  *fakeRecordBlobs
	authz  *fakeRecordAuthz

	owner, viewer, stranger uuid.UUID

	// privateID: a video whose only post is private — the authority admits
	// `viewer` and nobody else. publicID: a public post's video — the
	// authority admits everyone, signed-out included. datingID/anonID: the
	// uploader-only scopes. cdnID: keys under public/, no authority at all.
	privateID, publicID, datingID, anonID, cdnID uuid.UUID
	// Audio extracted from privateID / publicID, and a track with no source.
	trackPrivate, trackPublic, trackOrphan uuid.UUID
}

const recordInternalKey = "svc-key-for-tests"

func newRecordFixture(t *testing.T) *recordFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	f := &recordFixture{
		owner: uuid.New(), viewer: uuid.New(), stranger: uuid.New(),
		privateID: uuid.New(), publicID: uuid.New(), datingID: uuid.New(), anonID: uuid.New(), cdnID: uuid.New(),
		trackPrivate: uuid.New(), trackPublic: uuid.New(), trackOrphan: uuid.New(),
	}
	video := func(id uuid.UUID, scope, keyPrefix string) *postgres.MediaAsset {
		w, h, ms := 1080, 1920, 12_000
		bh := "LEHV6nWB2yk8pyo0adR*.7kCMdnj"
		return &postgres.MediaAsset{
			ID: id, UploaderID: f.owner, FileType: "video", MimeType: "video/mp4",
			StorageBucket: "media", StorageKey: fmt.Sprintf("%s%s/%s/original.mp4", keyPrefix, f.owner, id),
			ProcessingStatus: "ready", ModerationStatus: "passed",
			Width: &w, Height: &h, DurationMs: &ms, Blurhash: &bh,
			HLSMasterKey: fmt.Sprintf("%s%s/%s/hls/master.m3u8", keyPrefix, f.owner, id),
			AccessScope:  scope,
			Variants: []postgres.MediaVariant{
				{MediaAssetID: id, Name: "720p", Mime: "video/mp4", ObjectKey: fmt.Sprintf("%s%s/%s/720p.mp4", keyPrefix, f.owner, id)},
			},
		}
	}
	f.store = &fakeRecordStore{
		media: map[uuid.UUID]*postgres.MediaAsset{
			f.privateID: video(f.privateID, "", "user/"),
			f.publicID:  video(f.publicID, "", "user/"),
			f.datingID:  video(f.datingID, postgres.AccessScopeDatingPhoto, "user/"),
			f.anonID:    video(f.anonID, postgres.AccessScopeAnonymous, "user/"),
			f.cdnID:     video(f.cdnID, "", delivery.PublicPrefix),
		},
		tracks: map[uuid.UUID]*postgres.AudioTrack{
			f.trackPrivate: {ID: f.trackPrivate, SourceMediaID: &f.privateID, AudioKey: fmt.Sprintf("audio/%s/%s/audio.m4a", f.owner, f.privateID), Status: "ready"},
			f.trackPublic:  {ID: f.trackPublic, SourceMediaID: &f.publicID, AudioKey: fmt.Sprintf("audio/%s/%s/audio.m4a", f.owner, f.publicID), Status: "ready"},
			f.trackOrphan:  {ID: f.trackOrphan, SourceMediaID: nil, AudioKey: "audio/orphan.m4a", Status: "ready"},
		},
		jobs: map[uuid.UUID][]postgres.TranscodingJob{},
	}
	for _, id := range []uuid.UUID{f.privateID, f.publicID} {
		out := fmt.Sprintf("user/%s/%s/720p.mp4", f.owner, id)
		f.store.jobs[id] = []postgres.TranscodingJob{{ID: uuid.New(), MediaAssetID: id, TargetQuality: "720p", Status: "completed", OutputURL: &out}}
	}
	f.blobs = &fakeRecordBlobs{}
	f.authz = &fakeRecordAuthz{allow: func(viewerID, mediaID string) bool {
		switch mediaID {
		case f.publicID.String(), f.datingID.String(), f.anonID.String():
			return true // the authority would say yes to anyone, anonymous included
		case f.privateID.String():
			return viewerID == f.viewer.String()
		}
		return false
	}}
	f.router = f.build(recordInternalKey)
	return f
}

// build wires the real handler over the fakes; internalKey "" leaves the
// service-caller path unconfigured.
func (f *recordFixture) build(internalKey string) *gin.Engine {
	r := gin.New()
	h := &Handler{records: service.NewRecordReads(f.store, f.blobs, delivery.NewGate(nil, f.authz))}
	if internalKey != "" {
		h.WithInternalKey(internalKey)
	}
	h.RegisterRoutes(r, passthrough, passthrough)
	h.RegisterAudioRoutes(r, passthrough)
	return r
}

func (f *recordFixture) get(path string, viewer uuid.UUID, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if viewer != uuid.Nil {
		req.Header.Set("X-User-Id", viewer.String())
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func (f *recordFixture) info(id uuid.UUID, viewer uuid.UUID, headers ...string) *httptest.ResponseRecorder {
	return f.get("/v1/media/"+id.String(), viewer, headers...)
}

func (f *recordFixture) status(id uuid.UUID, viewer uuid.UUID, headers ...string) *httptest.ResponseRecorder {
	return f.get("/v1/media/"+id.String()+"/status", viewer, headers...)
}

func (f *recordFixture) audioURL(id uuid.UUID, viewer uuid.UUID) *httptest.ResponseRecorder {
	return f.get("/v1/audio/"+id.String()+"/url", viewer)
}

func dataField(t *testing.T, rec *httptest.ResponseRecorder, field string) string {
	t.Helper()
	var env struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode data envelope: %v (%s)", err, rec.Body.String())
	}
	var v any
	_ = json.Unmarshal(env.Data[field], &v)
	return fmt.Sprint(v)
}

// wantDeniedLikeMissing asserts a resolved denial: 404 NOT_FOUND whose body
// is exactly what a caller gets for an id that does not exist, and which
// carries nothing of the record.
func wantDeniedLikeMissing(t *testing.T, what string, got, missing *httptest.ResponseRecorder) {
	t.Helper()
	if got.Code != http.StatusNotFound || errorCode(t, got) != "NOT_FOUND" {
		t.Fatalf("%s: got %d %s want 404 NOT_FOUND", what, got.Code, got.Body.String())
	}
	if missing.Code != http.StatusNotFound {
		t.Fatalf("%s: the missing-id control is %d, not 404", what, missing.Code)
	}
	if got.Body.String() != missing.Body.String() {
		t.Errorf("%s: denial body differs from the missing-asset body\n denied: %s\nmissing: %s", what, got.Body.String(), missing.Body.String())
	}
	for _, leak := range []string{"uploader_id", "storage_key", "storage_bucket", "hls_master_key", "blurhash", "transcoding_jobs", "output_url", "signed.example"} {
		if strings.Contains(got.Body.String(), leak) {
			t.Errorf("%s: denial body leaks %q: %s", what, leak, got.Body.String())
		}
	}
}

// ── GET /v1/media/:id ───────────────────────────────────────────────────

// The uploader short-circuit. The authority is UNRESOLVED here, so the only
// way to a 200 is not asking it: neutering the owner comparison in
// service.authorizeMediaRecord fails this test.
func TestMediaInfoOwnerSeesOwnRecordWithoutTheAuthority(t *testing.T) {
	f := newRecordFixture(t)
	f.authz.err = delivery.ErrDeliveryUnresolved
	rec := f.info(f.privateID, f.owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner: got %d %s want 200", rec.Code, rec.Body.String())
	}
	if got := dataField(t, rec, "id"); got != f.privateID.String() {
		t.Errorf("owner sees id %s want %s", got, f.privateID)
	}
	if got := dataField(t, rec, "processing_status"); got != "ready" {
		t.Errorf("owner sees processing_status %q want ready (the studio polls this)", got)
	}
	if f.authz.calls != 0 {
		t.Errorf("the authority was asked %d times for the uploader's own record", f.authz.calls)
	}
}

func TestMediaInfoPermittedViewerIs200(t *testing.T) {
	f := newRecordFixture(t)
	rec := f.info(f.privateID, f.viewer)
	if rec.Code != http.StatusOK {
		t.Fatalf("permitted viewer: got %d %s want 200", rec.Code, rec.Body.String())
	}
	if f.authz.calls != 1 {
		t.Errorf("authority asked %d times want 1", f.authz.calls)
	}
}

func TestMediaInfoAnonymousOnPublicIs200(t *testing.T) {
	f := newRecordFixture(t)
	rec := f.info(f.publicID, uuid.Nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("anonymous on public: got %d %s want 200", rec.Code, rec.Body.String())
	}
	if f.authz.calls != 1 {
		t.Errorf("a signed-out viewer of a public post is the authority's question; asked %d times want 1", f.authz.calls)
	}
}

// The gap found on dev: a signed-out caller got the private post's video
// record. Neutering the gate call in service.authorizeMediaRecord (return
// nil instead of AuthorizeAsset) fails this test and the two after it.
func TestMediaInfoAnonymousOnPrivateIs404LikeMissing(t *testing.T) {
	f := newRecordFixture(t)
	wantDeniedLikeMissing(t, "anonymous on private", f.info(f.privateID, uuid.Nil), f.info(uuid.New(), uuid.Nil))
}

func TestMediaInfoStrangerOnPrivateIs404LikeMissing(t *testing.T) {
	f := newRecordFixture(t)
	wantDeniedLikeMissing(t, "stranger on private", f.info(f.privateID, f.stranger), f.info(uuid.New(), f.stranger))
}

func TestMediaInfoUnresolvedAuthorityIs503(t *testing.T) {
	f := newRecordFixture(t)
	f.authz.err = delivery.ErrDeliveryUnresolved
	for _, viewer := range []uuid.UUID{f.viewer, uuid.Nil} {
		rec := f.info(f.publicID, viewer)
		if rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "DEPENDENCY_UNAVAILABLE" {
			t.Errorf("viewer %s with the authority down: got %d %s want 503 DEPENDENCY_UNAVAILABLE", viewer, rec.Code, rec.Body.String())
		}
	}
}

// A store fault is not "gone": clients cache a 404.
func TestMediaInfoStoreFaultIs503(t *testing.T) {
	f := newRecordFixture(t)
	f.store.fail = errors.New("connection refused")
	rec := f.info(f.publicID, f.viewer)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("store down: got %d %s want 503", rec.Code, rec.Body.String())
	}
}

// Dating photos and anonymous attachments keep their uploader-only rule
// even though the authority would admit everyone.
func TestMediaInfoScopedRecordsStayUploaderOnly(t *testing.T) {
	f := newRecordFixture(t)
	for name, id := range map[string]uuid.UUID{"dating": f.datingID, "anonymous": f.anonID} {
		if rec := f.info(id, f.owner); rec.Code != http.StatusOK {
			t.Errorf("%s owner: got %d want 200", name, rec.Code)
		}
		wantDeniedLikeMissing(t, name+" other viewer", f.info(id, f.viewer), f.info(uuid.New(), f.viewer))
		wantDeniedLikeMissing(t, name+" anonymous", f.info(id, uuid.Nil), f.info(uuid.New(), uuid.Nil))
		wantDeniedLikeMissing(t, name+" service caller", f.info(id, uuid.Nil, internalKeyHeader, recordInternalKey), f.info(uuid.New(), uuid.Nil, internalKeyHeader, recordInternalKey))
	}
	if f.authz.calls != 0 {
		t.Errorf("scoped records are settled locally; authority asked %d times", f.authz.calls)
	}
}

// public/ keys are the public class: no authority round trip, ever.
func TestMediaInfoPublicClassKeysNeedNoAuthority(t *testing.T) {
	f := newRecordFixture(t)
	f.authz.err = delivery.ErrDeliveryUnresolved
	rec := f.info(f.cdnID, uuid.Nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("public-class asset, anonymous: got %d %s want 200", rec.Code, rec.Body.String())
	}
	if f.authz.calls != 0 {
		t.Errorf("authority asked %d times for a public-class asset", f.authz.calls)
	}
}

// post-service (moderation check before publish) and commerce-service
// (ownership check) call with the internal key and no viewer. Neutering
// the hmac comparison in Handler.trustedServiceCaller fails the wrong-key
// case; dropping the empty-key guard fails the unconfigured case.
func TestMediaInfoServiceKeyReadsWithoutAViewer(t *testing.T) {
	f := newRecordFixture(t)
	f.authz.err = delivery.ErrDeliveryUnresolved
	rec := f.info(f.privateID, uuid.Nil, internalKeyHeader, recordInternalKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("keyed service caller: got %d %s want 200", rec.Code, rec.Body.String())
	}
	if got := dataField(t, rec, "moderation_status"); got != "passed" {
		t.Errorf("service caller reads moderation_status %q want passed (post-service's publish check)", got)
	}
	if got := dataField(t, rec, "uploader_id"); got != f.owner.String() {
		t.Errorf("service caller reads uploader_id %q want %s (commerce's ownership check)", got, f.owner)
	}
	if f.authz.calls != 0 {
		t.Errorf("a service caller is not a viewer; authority asked %d times", f.authz.calls)
	}

	f.authz.err = nil
	wantDeniedLikeMissing(t, "wrong key", f.info(f.privateID, uuid.Nil, internalKeyHeader, "not-the-key"), f.info(uuid.New(), uuid.Nil))
	wantDeniedLikeMissing(t, "empty key header", f.info(f.privateID, uuid.Nil, internalKeyHeader, ""), f.info(uuid.New(), uuid.Nil))

	// No key configured: the header is never a credential.
	f.router = f.build("")
	wantDeniedLikeMissing(t, "key presented but none configured", f.info(f.privateID, uuid.Nil, internalKeyHeader, recordInternalKey), f.info(uuid.New(), uuid.Nil))
}

// ── GET /v1/media/:id/status ────────────────────────────────────────────

func TestMediaStatusOwnerSeesProcessingStateAndJobs(t *testing.T) {
	f := newRecordFixture(t)
	f.authz.err = delivery.ErrDeliveryUnresolved
	f.store.media[f.privateID].ProcessingStatus = "processing"
	rec := f.status(f.privateID, f.owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner status: got %d %s want 200", rec.Code, rec.Body.String())
	}
	if got := dataField(t, rec, "processing_status"); got != "processing" {
		t.Errorf("owner sees processing_status %q want processing", got)
	}
	if !strings.Contains(rec.Body.String(), `"transcoding_jobs"`) || !strings.Contains(rec.Body.String(), `"target_quality":"720p"`) {
		t.Errorf("owner status lacks the transcoding jobs: %s", rec.Body.String())
	}
	if f.authz.calls != 0 {
		t.Errorf("authority asked %d times for the uploader's own status", f.authz.calls)
	}
}

func TestMediaStatusAudience(t *testing.T) {
	f := newRecordFixture(t)
	if rec := f.status(f.privateID, f.viewer); rec.Code != http.StatusOK {
		t.Errorf("permitted viewer: got %d %s want 200", rec.Code, rec.Body.String())
	}
	if rec := f.status(f.publicID, uuid.Nil); rec.Code != http.StatusOK {
		t.Errorf("anonymous on public: got %d %s want 200", rec.Code, rec.Body.String())
	}
	wantDeniedLikeMissing(t, "anonymous on private", f.status(f.privateID, uuid.Nil), f.status(uuid.New(), uuid.Nil))
	wantDeniedLikeMissing(t, "stranger on private", f.status(f.privateID, f.stranger), f.status(uuid.New(), f.stranger))
	wantDeniedLikeMissing(t, "anonymous-scope, other viewer", f.status(f.anonID, f.viewer), f.status(uuid.New(), f.viewer))

	if rec := f.status(f.privateID, uuid.Nil, internalKeyHeader, recordInternalKey); rec.Code != http.StatusOK {
		t.Errorf("keyed service caller: got %d %s want 200", rec.Code, rec.Body.String())
	}

	f.authz.err = delivery.ErrDeliveryUnresolved
	if rec := f.status(f.privateID, f.viewer); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("authority down: got %d %s want 503", rec.Code, rec.Body.String())
	}
}

// ── GET /v1/audio/:audioId/url ──────────────────────────────────────────

func TestAudioURLOwnerIs200WithoutTheAuthority(t *testing.T) {
	f := newRecordFixture(t)
	f.authz.err = delivery.ErrDeliveryUnresolved
	rec := f.audioURL(f.trackPrivate, f.owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner: got %d %s want 200", rec.Code, rec.Body.String())
	}
	if got := dataField(t, rec, "url"); !strings.HasPrefix(got, "https://signed.example/audio/") {
		t.Errorf("owner url %q is not the presigned audio", got)
	}
	if len(f.blobs.signed) != 1 || f.blobs.signed[0] != f.store.tracks[f.trackPrivate].AudioKey {
		t.Errorf("signed keys %v want exactly the track's audio key", f.blobs.signed)
	}
	if f.authz.calls != 0 {
		t.Errorf("authority asked %d times for the uploader's own audio", f.authz.calls)
	}
}

func TestAudioURLPermittedViewerIs200(t *testing.T) {
	f := newRecordFixture(t)
	rec := f.audioURL(f.trackPrivate, f.viewer)
	if rec.Code != http.StatusOK {
		t.Fatalf("permitted viewer: got %d %s want 200", rec.Code, rec.Body.String())
	}
	if f.authz.calls != 1 {
		t.Errorf("authority asked %d times want 1", f.authz.calls)
	}
}

func TestAudioURLAnonymousOnPublicIs200(t *testing.T) {
	f := newRecordFixture(t)
	if rec := f.audioURL(f.trackPublic, uuid.Nil); rec.Code != http.StatusOK {
		t.Fatalf("anonymous on public: got %d %s want 200", rec.Code, rec.Body.String())
	}
}

// The second gap: the route took no viewer and presigned for anyone.
// Removing the MediaForViewer call in RecordReads.AudioTrackURLForViewer
// fails this test and the next; nothing may be signed on a denial.
func TestAudioURLAnonymousOnPrivateIs404LikeMissing(t *testing.T) {
	f := newRecordFixture(t)
	wantDeniedLikeMissing(t, "anonymous on private", f.audioURL(f.trackPrivate, uuid.Nil), f.audioURL(uuid.New(), uuid.Nil))
	if len(f.blobs.signed) != 0 {
		t.Errorf("a URL was signed for a denied viewer: %v", f.blobs.signed)
	}
}

func TestAudioURLStrangerOnPrivateIs404LikeMissing(t *testing.T) {
	f := newRecordFixture(t)
	wantDeniedLikeMissing(t, "stranger on private", f.audioURL(f.trackPrivate, f.stranger), f.audioURL(uuid.New(), f.stranger))
	if len(f.blobs.signed) != 0 {
		t.Errorf("a URL was signed for a denied viewer: %v", f.blobs.signed)
	}
}

func TestAudioURLUnresolvedAuthorityIs503(t *testing.T) {
	f := newRecordFixture(t)
	f.authz.err = delivery.ErrDeliveryUnresolved
	rec := f.audioURL(f.trackPublic, f.viewer)
	if rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "DEPENDENCY_UNAVAILABLE" {
		t.Errorf("authority down: got %d %s want 503 DEPENDENCY_UNAVAILABLE", rec.Code, rec.Body.String())
	}
	if len(f.blobs.signed) != 0 {
		t.Errorf("a URL was signed while the decision was unresolved: %v", f.blobs.signed)
	}
}

// A track with no source asset has nobody to answer for it. Removing the
// nil-source guard fails this: the authority never sees the id, so the
// fake would deny anyway — hence the owner, who would otherwise pass.
func TestAudioURLTrackWithoutSourceIsRefused(t *testing.T) {
	f := newRecordFixture(t)
	f.authz.allow = func(string, string) bool { return true }
	wantDeniedLikeMissing(t, "orphan track, owner", f.audioURL(f.trackOrphan, f.owner), f.audioURL(uuid.New(), f.owner))
	wantDeniedLikeMissing(t, "orphan track, anonymous", f.audioURL(f.trackOrphan, uuid.Nil), f.audioURL(uuid.New(), uuid.Nil))

	// A source that no longer exists is the same answer.
	gone := uuid.New()
	f.store.tracks[f.trackOrphan].SourceMediaID = &gone
	wantDeniedLikeMissing(t, "track whose source is gone", f.audioURL(f.trackOrphan, f.viewer), f.audioURL(uuid.New(), f.viewer))
	if len(f.blobs.signed) != 0 {
		t.Errorf("a URL was signed with no source to answer for it: %v", f.blobs.signed)
	}
}

// ── wiring ──────────────────────────────────────────────────────────────

// The three routes must be served by these handlers, which are the ones
// that go through RecordReads; a re-registration elsewhere would bypass it.
func TestRecordReadRoutesAreWired(t *testing.T) {
	f := newRecordFixture(t)
	want := map[string]string{
		"GET /v1/media/:mediaId":        ".GetMedia",
		"GET /v1/media/:mediaId/status": ".GetMediaStatus",
		"GET /v1/audio/:audioId/url":    ".GetAudioTrackURL",
		"GET /v1/audio/:audioId":        ".GetAudioTrack",
		"GET /v1/audio/trending":        ".GetTrendingAudio",
		"GET /v1/audio/search":          ".SearchAudio",
		"POST /v1/audio/:audioId/use":   ".UseAudioTrack",
	}
	found := map[string]string{}
	for _, ri := range f.router.Routes() {
		found[ri.Method+" "+ri.Path] = ri.Handler
	}
	for key, handler := range want {
		h, ok := found[key]
		if !ok {
			t.Errorf("%s is not registered", key)
			continue
		}
		if !strings.HasSuffix(h, handler) && !strings.HasSuffix(h, handler+"-fm") {
			t.Errorf("%s served by %s, want %s", key, h, handler)
		}
	}
}
