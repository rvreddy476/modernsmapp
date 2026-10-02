package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Pulse dating clips — the internal routes over httptest with a fake asset
// store, object store and signer. No database, no AWS.

type fakeClipStore struct {
	mu       sync.Mutex
	assets   map[uuid.UUID]*postgres.MediaAsset
	variants map[uuid.UUID][]postgres.MediaVariant
	marks    int // successful scope changes
	attempts int // every MarkDatingClip call
	purged   []uuid.UUID
}

func (f *fakeClipStore) GetMedia(_ context.Context, id uuid.UUID) (*postgres.MediaAsset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.assets[id]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	cp := *a
	return &cp, nil
}

func (f *fakeClipStore) GetVariants(_ context.Context, id uuid.UUID) ([]postgres.MediaVariant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]postgres.MediaVariant(nil), f.variants[id]...), nil
}

// MarkDatingClip mirrors the store's UPDATE ... WHERE guard.
func (f *fakeClipStore) MarkDatingClip(_ context.Context, id, uploader uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
	a, ok := f.assets[id]
	if !ok || a.UploaderID != uploader {
		return pgx.ErrNoRows
	}
	if a.AccessScope != "" && a.AccessScope != postgres.AccessScopeDatingClip {
		return postgres.ErrScopeConflict
	}
	a.AccessScope = postgres.AccessScopeDatingClip
	f.marks++
	return nil
}

func (f *fakeClipStore) DeleteAssetForReferrer(_ context.Context, id uuid.UUID, kind string, _ uuid.UUID) (*postgres.AssetPurgeRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.assets[id]
	if !ok {
		return nil, postgres.ErrMediaNotFound
	}
	if kind != service.ReferrerDatingClip {
		return nil, postgres.ErrMediaStillReferenced
	}
	keys := []string{a.StorageKey}
	for _, v := range f.variants[id] {
		keys = append(keys, v.ObjectKey)
	}
	delete(f.assets, id)
	delete(f.variants, id)
	f.purged = append(f.purged, id)
	return &postgres.AssetPurgeRecord{MediaID: id, UploaderID: a.UploaderID, Prefix: postgres.AssetPrefix(a.UploaderID, id), ObjectKeys: keys}, nil
}

func (f *fakeClipStore) ClearBlobReclaim(context.Context, string) error                 { return nil }
func (f *fakeClipStore) RecordBlobReclaimFailure(context.Context, string, string) error { return nil }

type clipFixture struct {
	r      *gin.Engine
	store  *fakeClipStore
	blobs  *fakeDatingBlobs
	signer *fakeDatingSigner
	owner  uuid.UUID
	other  uuid.UUID
	// ids by role
	voice, video, processing, exact, tooLong, image, amr, failed, anon, foreign uuid.UUID
}

const clipTTL = 90 * time.Second

func newClipFixture(t *testing.T) *clipFixture {
	t.Helper()
	f := &clipFixture{
		store:  &fakeClipStore{assets: map[uuid.UUID]*postgres.MediaAsset{}, variants: map[uuid.UUID][]postgres.MediaVariant{}},
		blobs:  &fakeDatingBlobs{objects: map[string][]byte{}},
		signer: &fakeDatingSigner{},
		owner:  uuid.New(), other: uuid.New(),
		voice: uuid.New(), video: uuid.New(), processing: uuid.New(), exact: uuid.New(), tooLong: uuid.New(),
		image: uuid.New(), amr: uuid.New(), failed: uuid.New(), anon: uuid.New(), foreign: uuid.New(),
	}
	add := func(id, uploader uuid.UUID, fileType, mime, status, moderation string, durationMs int) *postgres.MediaAsset {
		key := "user/" + uploader.String() + "/" + id.String() + "/original"
		a := &postgres.MediaAsset{ID: id, UploaderID: uploader, FileType: fileType, MimeType: mime,
			ProcessingStatus: status, ModerationStatus: moderation, StorageKey: key}
		if durationMs > 0 {
			d := durationMs
			a.DurationMs = &d
		}
		f.store.assets[id] = a
		f.blobs.objects[key] = []byte("bytes")
		return a
	}
	add(f.voice, f.owner, "audio", "audio/mp4", "ready", "approved", 12_000)
	add(f.video, f.owner, "video", "video/quicktime", "ready", "passed", 25_000)
	prefix := "user/" + f.owner.String() + "/" + f.video.String() + "/"
	for _, name := range []string{"thumb_150", "360p", "480p", "720p", "1080p"} {
		f.store.variants[f.video] = append(f.store.variants[f.video],
			postgres.MediaVariant{MediaAssetID: f.video, Name: name, ObjectKey: prefix + name})
	}
	add(f.processing, f.owner, "video", "video/mp4", "processing", "pending", 0)
	add(f.exact, f.owner, "audio", "audio/mpeg", "ready", "approved", service.DefaultDatingClipMaxMs)
	add(f.tooLong, f.owner, "audio", "audio/mpeg", "ready", "approved", service.DefaultDatingClipMaxMs+1)
	// The image and the failed video carry a duration so that only the
	// kind and processing guards refuse them, not the missing-duration one.
	add(f.image, f.owner, "image", "image/gif", "ready", "passed", 4_000)
	add(f.amr, f.owner, "audio", "audio/amr", "ready", "approved", 9_000)
	add(f.failed, f.owner, "video", "video/mp4", "failed", "manual_review", 9_000)
	add(f.anon, f.owner, "video", "video/mp4", "ready", "passed", 9_000).AccessScope = postgres.AccessScopeAnonymous
	add(f.foreign, f.other, "audio", "audio/mp4", "ready", "approved", 9_000)

	gin.SetMode(gin.TestMode)
	f.r = gin.New()
	svc := service.NewDatingClipService(f.store, f.blobs, f.signer, clipTTL, service.DefaultDatingClipMaxMs, nil)
	New(nil).WithInternalKey(datingKey).WithDatingClips(svc).RegisterDatingClipRoutes(f.r)
	return f
}

func clipCall(t *testing.T, r *gin.Engine, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", datingKey)
	for k, v := range headers {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

const clipBase = "/internal/v1/media/dating-clips/"

func (f *clipFixture) status(t *testing.T, media, requester uuid.UUID) *httptest.ResponseRecorder {
	return clipCall(t, f.r, http.MethodGet, clipBase+media.String()+"/owner-status?requester_user_id="+requester.String(), nil, nil)
}

func (f *clipFixture) prepare(t *testing.T, media, requester uuid.UUID) *httptest.ResponseRecorder {
	return clipCall(t, f.r, http.MethodPost, clipBase+media.String()+"/prepare",
		map[string]any{"requester_user_id": requester.String()}, nil)
}

func (f *clipFixture) deliver(t *testing.T, media, owner uuid.UUID) *httptest.ResponseRecorder {
	return clipCall(t, f.r, http.MethodPost, clipBase+media.String()+"/delivery-url",
		map[string]any{"owner_user_id": owner.String()}, nil)
}

func clipData(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil || env.Data == nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return env.Data
}

func clipKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func wantClipError(t *testing.T, what string, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status || errCode(t, w) != code {
		t.Fatalf("%s: got %d %s, want %d %s", what, w.Code, w.Body.String(), status, code)
	}
}

// MEDIA_DATING_CLIPS_ENABLED off leaves the service unwired: every route is
// an unknown path. Removing the nil check in RegisterDatingClipRoutes
// panics here (nil service).
func TestDatingClipRoutes_FlagOffAnswers404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	off := gin.New()
	New(nil).WithInternalKey(datingKey).RegisterDatingClipRoutes(off)
	noKey := gin.New()
	svc := service.NewDatingClipService(&fakeClipStore{}, &fakeDatingBlobs{}, &fakeDatingSigner{}, clipTTL, 0, nil)
	New(nil).WithDatingClips(svc).RegisterDatingClipRoutes(noKey)

	id, user := uuid.New().String(), uuid.New().String()
	for name, r := range map[string]*gin.Engine{"flag off": off, "no internal key": noKey} {
		for _, c := range []struct{ method, path string }{
			{http.MethodGet, clipBase + id + "/owner-status?requester_user_id=" + user},
			{http.MethodPost, clipBase + id + "/prepare"},
			{http.MethodPost, clipBase + id + "/delivery-url"},
			{http.MethodDelete, clipBase + id + "?requester_user_id=" + user},
		} {
			if w := clipCall(t, r, c.method, c.path, map[string]any{"requester_user_id": user, "owner_user_id": user}, nil); w.Code != http.StatusNotFound {
				t.Fatalf("%s: %s %s = %d, want 404", name, c.method, c.path, w.Code)
			}
		}
	}
}

func TestDatingClipRoutes_ServiceCallersOnly(t *testing.T) {
	f := newClipFixture(t)
	path := clipBase + f.voice.String() + "/owner-status?requester_user_id=" + f.owner.String()
	if w := clipCall(t, f.r, http.MethodGet, path, nil, map[string]string{"X-Internal-Service-Key": ""}); w.Code != http.StatusUnauthorized {
		t.Fatalf("no key: %d", w.Code)
	}
	if w := clipCall(t, f.r, http.MethodGet, path, nil, map[string]string{"X-Internal-Service-Key": "wrong"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key: %d", w.Code)
	}
	for _, h := range gatewayIdentityHeaders {
		w := clipCall(t, f.r, http.MethodGet, path, nil, map[string]string{h: f.owner.String()})
		if w.Code != http.StatusForbidden || errCode(t, w) != CodeUserCallerRefused {
			t.Fatalf("key + %s: %d %s", h, w.Code, w.Body.String())
		}
	}
}

func TestDatingClipOwnerStatus_ContractAndNormalisedModeration(t *testing.T) {
	f := newClipFixture(t)
	d := clipData(t, f.status(t, f.voice, f.owner))
	if got := strings.Join(clipKeys(d), ","); got != "duration_ms,kind,moderation,owner_user_id,processing,usable" {
		t.Fatalf("owner-status fields = %s", got)
	}
	if d["owner_user_id"] != f.owner.String() || d["kind"] != "audio" || d["processing"] != "ready" ||
		d["moderation"] != "passed" || d["duration_ms"] != float64(12_000) || d["usable"] != true {
		t.Fatalf("voice status = %v", d)
	}

	// Each moderation spelling this service writes, on the asset kind that
	// writes it, and whether the clip is usable with it.
	cases := []struct {
		name           string
		id             uuid.UUID
		moderation     string
		wantModeration string
		wantProcessing string
		wantUsable     bool
	}{
		{"voice approved", f.voice, "approved", "passed", "ready", true},
		{"voice pending (captioning)", f.voice, "pending", "pending", "ready", false},
		{"voice failed (no verdict)", f.voice, "failed", "review", "ready", false},
		{"voice rejected", f.voice, "rejected", "rejected", "ready", false},
		{"voice unset", f.voice, "", "pending", "ready", false},
		{"video passed", f.video, "passed", "passed", "ready", true},
		{"video manual review", f.video, "manual_review", "review", "ready", false},
		{"video rejected", f.video, "rejected", "rejected", "ready", false},
		{"video still transcoding", f.processing, "pending", "pending", "processing", false},
		{"video transcode failed", f.failed, "manual_review", "review", "failed", false},
		{"an image", f.image, "passed", "passed", "ready", false},
		{"AMR audio", f.amr, "approved", "passed", "ready", false},
		{"an anonymous attachment", f.anon, "passed", "passed", "ready", false},
	}
	for _, tc := range cases {
		f.store.assets[tc.id].ModerationStatus = tc.moderation
		d := clipData(t, f.status(t, tc.id, f.owner))
		if d["moderation"] != tc.wantModeration || d["processing"] != tc.wantProcessing || d["usable"] != tc.wantUsable {
			t.Errorf("%s: got moderation=%v processing=%v usable=%v, want %s %s %v",
				tc.name, d["moderation"], d["processing"], d["usable"], tc.wantModeration, tc.wantProcessing, tc.wantUsable)
		}
	}
}

func TestDatingClipOwnerStatus_RefusesForeignAndMissing(t *testing.T) {
	f := newClipFixture(t)
	missing := f.status(t, uuid.New(), f.owner)
	wantClipError(t, "missing", missing, http.StatusNotFound, CodeClipNotFound)
	foreign := f.status(t, f.foreign, f.owner)
	wantClipError(t, "foreign", foreign, http.StatusNotFound, CodeClipNotFound)
	if strings.Contains(foreign.Body.String(), "moderation") || strings.Contains(foreign.Body.String(), f.other.String()) {
		t.Fatalf("refusal leaks the clip: %s", foreign.Body.String())
	}
	if w := clipCall(t, f.r, http.MethodGet, clipBase+f.voice.String()+"/owner-status?requester_user_id=nope", nil, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("bad requester: %d", w.Code)
	}
}

// 30 000 ms is a clip; 30 001 ms is not, and prepare leaves it unscoped.
func TestDatingClip_ThirtySecondLimit(t *testing.T) {
	f := newClipFixture(t)
	if d := clipData(t, f.status(t, f.exact, f.owner)); d["usable"] != true {
		t.Fatalf("exactly the limit: %v", d)
	}
	if d := clipData(t, f.status(t, f.tooLong, f.owner)); d["usable"] != false || d["duration_ms"] != float64(30_001) {
		t.Fatalf("one ms over: %v", d)
	}

	w := f.prepare(t, f.tooLong, f.owner)
	wantClipError(t, "prepare too long", w, http.StatusUnprocessableEntity, CodeClipTooLong)
	var env struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil || env.Error.Details["max_ms"] != float64(30_000) {
		t.Fatalf("too-long details = %s", w.Body.String())
	}
	if f.store.assets[f.tooLong].AccessScope != "" || f.store.marks != 0 {
		t.Fatal("a too-long clip was scoped")
	}
	// Even scoped by hand, a too-long clip is never delivered.
	f.store.assets[f.tooLong].AccessScope = postgres.AccessScopeDatingClip
	wantClipError(t, "deliver too long", f.deliver(t, f.tooLong, f.owner), http.StatusNotFound, CodeClipNotFound)

	if w := f.prepare(t, f.exact, f.owner); w.Code != http.StatusOK {
		t.Fatalf("prepare at the limit: %d %s", w.Code, w.Body.String())
	}
}

func TestDatingClipPrepare_RefusalsAndIdempotence(t *testing.T) {
	f := newClipFixture(t)
	wantClipError(t, "foreign", f.prepare(t, f.foreign, f.owner), http.StatusNotFound, CodeClipNotFound)
	wantClipError(t, "missing", f.prepare(t, uuid.New(), f.owner), http.StatusNotFound, CodeClipNotFound)
	wantClipError(t, "processing", f.prepare(t, f.processing, f.owner), http.StatusConflict, CodeClipNotReady)
	for name, id := range map[string]uuid.UUID{"image": f.image, "amr": f.amr, "failed": f.failed, "anonymous": f.anon} {
		wantClipError(t, name, f.prepare(t, id, f.owner), http.StatusUnprocessableEntity, CodeClipUnsupported)
	}
	// Each is refused by the service before the store is asked (the store's
	// own WHERE guard is the backstop for the anonymous one).
	if f.store.attempts != 0 || f.store.assets[f.anon].AccessScope != postgres.AccessScopeAnonymous {
		t.Fatalf("a refused prepare reached the store %d time(s)", f.store.attempts)
	}

	// Moderation need not have finished.
	f.store.assets[f.voice].ModerationStatus = "pending"
	w := f.prepare(t, f.voice, f.owner)
	if w.Code != http.StatusOK {
		t.Fatalf("prepare pending voice: %d %s", w.Code, w.Body.String())
	}
	d := clipData(t, w)
	if got := strings.Join(clipKeys(d), ","); got != "duration_ms,kind" || d["kind"] != "audio" || d["duration_ms"] != float64(12_000) {
		t.Fatalf("prepare response = %v", d)
	}
	if f.store.assets[f.voice].AccessScope != postgres.AccessScopeDatingClip || f.store.marks != 1 {
		t.Fatalf("scope after prepare = %q (marks %d)", f.store.assets[f.voice].AccessScope, f.store.marks)
	}
	if w := f.prepare(t, f.voice, f.owner); w.Code != http.StatusOK || f.store.marks != 1 {
		t.Fatalf("second prepare: %d, marks %d", w.Code, f.store.marks)
	}
}

func TestDatingClipDeliveryURL_RefusesUnlessPreparedPassedAndOwned(t *testing.T) {
	f := newClipFixture(t)
	// Not prepared (no dating_clip scope).
	wantClipError(t, "unprepared", f.deliver(t, f.voice, f.owner), http.StatusNotFound, CodeClipNotFound)

	for _, id := range []uuid.UUID{f.voice, f.video} {
		if w := f.prepare(t, id, f.owner); w.Code != http.StatusOK {
			t.Fatalf("prepare: %d %s", w.Code, w.Body.String())
		}
	}
	wantClipError(t, "wrong owner", f.deliver(t, f.voice, f.other), http.StatusNotFound, CodeClipNotFound)
	for _, mod := range []string{"pending", "failed", "rejected", ""} {
		f.store.assets[f.voice].ModerationStatus = mod
		wantClipError(t, "voice moderation "+mod, f.deliver(t, f.voice, f.owner), http.StatusNotFound, CodeClipNotFound)
	}
	for _, mod := range []string{"manual_review", "rejected"} {
		f.store.assets[f.video].ModerationStatus = mod
		wantClipError(t, "video moderation "+mod, f.deliver(t, f.video, f.owner), http.StatusNotFound, CodeClipNotFound)
	}
	// Another uploader-only scope is not a clip.
	f.store.assets[f.voice].ModerationStatus = "approved"
	f.store.assets[f.voice].AccessScope = postgres.AccessScopeDatingPhoto
	wantClipError(t, "dating photo scope", f.deliver(t, f.voice, f.owner), http.StatusNotFound, CodeClipNotFound)
	// Not ready.
	f.store.assets[f.video].ModerationStatus = "passed"
	f.store.assets[f.video].ProcessingStatus = "processing"
	wantClipError(t, "video reprocessing", f.deliver(t, f.video, f.owner), http.StatusNotFound, CodeClipNotFound)
	if len(f.signer.keys) != 0 {
		t.Fatalf("signed %v on a refusal", f.signer.keys)
	}
}

func TestDatingClipDeliveryURL_SignsPlayableRenditions(t *testing.T) {
	f := newClipFixture(t)
	for _, id := range []uuid.UUID{f.voice, f.video} {
		if w := f.prepare(t, id, f.owner); w.Code != http.StatusOK {
			t.Fatalf("prepare: %d %s", w.Code, w.Body.String())
		}
	}
	prefix := "user/" + f.owner.String() + "/" + f.video.String() + "/"

	before := time.Now().UTC()
	w := f.deliver(t, f.video, f.owner)
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("video: %d %s", w.Code, w.Body.String())
	}
	d := clipData(t, w)
	if d["kind"] != "video" || !strings.Contains(d["url"].(string), prefix+"720p") || !strings.Contains(d["poster_url"].(string), prefix+"thumb_150") {
		t.Fatalf("video delivery = %v; want the 720p rendition and the thumbnail", d)
	}
	exp, err := time.Parse(time.RFC3339, d["expires_at"].(string))
	if err != nil || exp.Before(before.Add(clipTTL-time.Second)) || exp.After(time.Now().UTC().Add(clipTTL+time.Second)) {
		t.Fatalf("expires_at = %v (%v); want now + %s", d["expires_at"], err, clipTTL)
	}

	// No 720p: the next rung down.
	f.store.variants[f.video] = f.store.variants[f.video][:3] // thumb_150, 360p, 480p
	if d := clipData(t, f.deliver(t, f.video, f.owner)); !strings.Contains(d["url"].(string), prefix+"480p") {
		t.Fatalf("without 720p = %v; want 480p", d)
	}

	d = clipData(t, f.deliver(t, f.voice, f.owner))
	if got := strings.Join(clipKeys(d), ","); got != "expires_at,kind,url" || d["kind"] != "audio" ||
		!strings.Contains(d["url"].(string), f.store.assets[f.voice].StorageKey) {
		t.Fatalf("audio delivery = %v; want the original and no poster", d)
	}
}

func TestDatingClipDelete_OwnerChecked(t *testing.T) {
	f := newClipFixture(t)
	del := func(id, requester uuid.UUID) *httptest.ResponseRecorder {
		return clipCall(t, f.r, http.MethodDelete, clipBase+id.String()+"?requester_user_id="+requester.String(), nil, nil)
	}
	wantClipError(t, "foreign", del(f.foreign, f.owner), http.StatusNotFound, CodeClipNotFound)
	if _, ok := f.store.assets[f.foreign]; !ok {
		t.Fatal("a foreign clip was deleted")
	}
	w := del(f.video, f.owner)
	if w.Code != http.StatusOK || clipData(t, w)["status"] != "deleted" {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if len(f.store.purged) != 1 || f.store.purged[0] != f.video {
		t.Fatalf("purged = %v", f.store.purged)
	}
	wantClipError(t, "again", del(f.video, f.owner), http.StatusNotFound, CodeClipNotFound)
}
