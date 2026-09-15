package http

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Lane D5 — POST /internal/v1/media/faces/compare, over httptest with a fake
// asset store, blob reader and provider. No database, no AWS.

const faceKey = "face-internal-key"

type fakeFaceMedia struct {
	assets   map[uuid.UUID]*postgres.MediaAsset
	variants map[uuid.UUID][]postgres.MediaVariant
}

func (f *fakeFaceMedia) GetMedia(_ context.Context, id uuid.UUID) (*postgres.MediaAsset, error) {
	if a, ok := f.assets[id]; ok {
		cp := *a
		return &cp, nil
	}
	return nil, pgx.ErrNoRows
}

func (f *fakeFaceMedia) GetVariants(_ context.Context, id uuid.UUID) ([]postgres.MediaVariant, error) {
	return f.variants[id], nil
}

type fakeFaceBlobs map[string][]byte

func (f fakeFaceBlobs) DownloadObject(_ context.Context, key string) ([]byte, error) {
	if b, ok := f[key]; ok {
		return b, nil
	}
	return nil, pgx.ErrNoRows
}

// Marker bytes the test later proves never leave the service.
const secretBytes = "SECRET-IMAGE-BYTES"

type faceFixture struct {
	r                  *gin.Engine
	owner, other       uuid.UUID
	selfie, photo      uuid.UUID
	foreign, notReady  uuid.UUID
	unmoderated        uuid.UUID
	noFace, groupPhoto uuid.UUID
	media              *fakeFaceMedia
}

func newFaceFixture(t *testing.T, comparer processing.FaceComparer) *faceFixture {
	t.Helper()
	f := &faceFixture{
		owner: uuid.New(), other: uuid.New(),
		selfie: uuid.New(), photo: uuid.New(), foreign: uuid.New(), notReady: uuid.New(),
		unmoderated: uuid.New(), noFace: uuid.New(), groupPhoto: uuid.New(),
	}
	f.media = &fakeFaceMedia{assets: map[uuid.UUID]*postgres.MediaAsset{}, variants: map[uuid.UUID][]postgres.MediaVariant{}}
	blobs := fakeFaceBlobs{}
	add := func(id, owner uuid.UUID, status, moderation, marker string) {
		key := "media/" + id.String() + "/original"
		f.media.assets[id] = &postgres.MediaAsset{ID: id, UploaderID: owner, FileType: "image",
			ProcessingStatus: status, ModerationStatus: moderation, StorageKey: key}
		blobs[key] = []byte(secretBytes + " " + processing.MockFaceMarker + marker + "\n")
	}
	add(f.selfie, f.owner, "ready", "passed", "faces=1:subject=owner")
	add(f.photo, f.owner, "ready", "passed", "faces=1:subject=owner")
	add(f.foreign, f.other, "ready", "passed", "faces=1:subject=owner")
	add(f.notReady, f.owner, "processing", "passed", "faces=1:subject=owner")
	add(f.unmoderated, f.owner, "ready", "manual_review", "faces=1:subject=owner")
	add(f.noFace, f.owner, "ready", "passed", "faces=0:subject=owner")
	add(f.groupPhoto, f.owner, "ready", "passed", "faces=2:subject=owner")
	// The photo compares through its 1080 rendition.
	rendition := "media/" + f.photo.String() + "/medium_1080"
	f.media.variants[f.photo] = []postgres.MediaVariant{{Name: service.FaceCompareVariant, ObjectKey: rendition}}
	blobs[rendition] = []byte(secretBytes + " " + processing.MockFaceMarker + "faces=1:subject=owner\n")

	gin.SetMode(gin.TestMode)
	f.r = gin.New()
	fc := service.NewFaceCompareService(f.media, blobs, comparer, 90)
	New(nil).WithInternalKey(faceKey).WithFaceCompare(fc).RegisterFaceCompareRoutes(f.r)
	return f
}

func (f *faceFixture) post(t *testing.T, key string, headers map[string]string, source, target, requester uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"source_media_id": source.String(), "target_media_id": target.String(), "requester_user_id": requester.String(),
	})
	req := httptest.NewRequest(http.MethodPost, FaceComparePath, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, req)
	return w
}

func errCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	return env.Error.Code
}

func outcome(t *testing.T, w *httptest.ResponseRecorder) service.FaceCompareOutcome {
	t.Helper()
	var env struct {
		Data service.FaceCompareOutcome `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return env.Data
}

func TestFaceCompareRoute_RequiresInternalKey(t *testing.T) {
	f := newFaceFixture(t, processing.NewMockFaceComparer())
	if w := f.post(t, "", nil, f.selfie, f.photo, f.owner); w.Code != http.StatusUnauthorized {
		t.Fatalf("missing key: %d %s", w.Code, w.Body.String())
	}
	if w := f.post(t, "wrong", nil, f.selfie, f.photo, f.owner); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key: %d", w.Code)
	}
	if w := f.post(t, faceKey, nil, f.selfie, f.photo, f.owner); w.Code != http.StatusOK {
		t.Fatalf("service caller: %d %s", w.Code, w.Body.String())
	}
}

// The gateway injects the key on every proxied request, so a key alongside
// any user identity header is a proxied user and is refused.
func TestFaceCompareRoute_RefusesKeyWithUserHeaders(t *testing.T) {
	f := newFaceFixture(t, processing.NewMockFaceComparer())
	for _, h := range gatewayIdentityHeaders {
		w := f.post(t, faceKey, map[string]string{h: f.owner.String()}, f.selfie, f.photo, f.owner)
		if w.Code != http.StatusForbidden || errCode(t, w) != CodeUserCallerRefused {
			t.Fatalf("key + %s: %d %s; want 403 USER_CALLER_REFUSED", h, w.Code, w.Body.String())
		}
	}
}

func TestFaceCompareRoute_NotRegisteredWithoutKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	fc := service.NewFaceCompareService(&fakeFaceMedia{}, fakeFaceBlobs{}, processing.NewMockFaceComparer(), 90)
	New(nil).WithFaceCompare(fc).RegisterFaceCompareRoutes(r)
	req := httptest.NewRequest(http.MethodPost, FaceComparePath, strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("route without a key: %d; want 404 (not registered)", w.Code)
	}
}

// Foreign, not-ready, not-moderated and missing media all answer the same 404.
func TestFaceCompareRoute_OwnershipAndReadiness404(t *testing.T) {
	f := newFaceFixture(t, processing.NewMockFaceComparer())
	cases := map[string][2]uuid.UUID{
		"foreign source": {f.foreign, f.photo},
		"foreign target": {f.selfie, f.foreign},
		"not ready":      {f.notReady, f.photo},
		"not moderated":  {f.selfie, f.unmoderated},
		"missing":        {uuid.New(), f.photo},
	}
	for name, ids := range cases {
		w := f.post(t, faceKey, nil, ids[0], ids[1], f.owner)
		if w.Code != http.StatusNotFound || errCode(t, w) != CodeMediaNotFound {
			t.Fatalf("%s: %d %s; want 404 MEDIA_NOT_FOUND", name, w.Code, w.Body.String())
		}
	}
	// The other user cannot compare the owner's media either.
	if w := f.post(t, faceKey, nil, f.selfie, f.photo, f.other); w.Code != http.StatusNotFound {
		t.Fatalf("other requester: %d", w.Code)
	}
	if w := f.post(t, faceKey, nil, f.selfie, f.selfie, f.owner); w.Code != http.StatusBadRequest || errCode(t, w) != CodeSameMedia {
		t.Fatalf("same media: %d %s", w.Code, w.Body.String())
	}
}

func TestFaceCompareRoute_FaceCountReasons(t *testing.T) {
	f := newFaceFixture(t, processing.NewMockFaceComparer())
	w := f.post(t, faceKey, nil, f.noFace, f.photo, f.owner)
	if o := outcome(t, w); w.Code != http.StatusOK || o.Match || o.Reason != processing.FaceReasonNoFace || o.FaceCountSource != 0 {
		t.Fatalf("no face: %d %+v", w.Code, o)
	}
	w = f.post(t, faceKey, nil, f.selfie, f.groupPhoto, f.owner)
	if o := outcome(t, w); w.Code != http.StatusOK || o.Match || o.Reason != processing.FaceReasonMultipleFaces || o.FaceCountTarget != 2 {
		t.Fatalf("multiple faces: %d %+v", w.Code, o)
	}
	w = f.post(t, faceKey, nil, f.selfie, f.photo, f.owner)
	if o := outcome(t, w); w.Code != http.StatusOK || !o.Match || o.Similarity != 99 || o.Provider != "mock" ||
		o.FaceCountSource != 1 || o.FaceCountTarget != 1 {
		t.Fatalf("match: %d %+v", w.Code, o)
	}
}

type downComparer struct{}

func (downComparer) Name() string { return "rekognition" }
func (downComparer) CompareFaces(context.Context, []byte, []byte) (processing.FaceCompareResult, error) {
	return processing.FaceCompareResult{}, processing.ErrFaceCompareUnavailable
}

func TestFaceCompareRoute_ProviderDownIs503(t *testing.T) {
	f := newFaceFixture(t, downComparer{})
	w := f.post(t, faceKey, nil, f.selfie, f.photo, f.owner)
	if w.Code != http.StatusServiceUnavailable || errCode(t, w) != "FACE_COMPARE_UNAVAILABLE" {
		t.Fatalf("provider down: %d %s", w.Code, w.Body.String())
	}
}

// No image bytes, embeddings or storage keys reach the response or the logs.
func TestFaceCompareRoute_NoBytesOrEmbeddingsInResponseOrLogs(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	f := newFaceFixture(t, processing.NewMockFaceComparer())
	var bodies strings.Builder
	for _, ids := range [][2]uuid.UUID{{f.selfie, f.photo}, {f.noFace, f.photo}, {f.foreign, f.photo}} {
		bodies.WriteString(f.post(t, faceKey, nil, ids[0], ids[1], f.owner).Body.String())
	}
	fd := newFaceFixture(t, downComparer{})
	bodies.WriteString(fd.post(t, faceKey, nil, fd.selfie, fd.photo, fd.owner).Body.String())

	for name, text := range map[string]string{"response": bodies.String(), "logs": logs.String()} {
		lower := strings.ToLower(text)
		for _, forbidden := range []string{strings.ToLower(secretBytes), "embedding", "atpost-face-test", "storage_key", "/original", "medium_1080", "bytes"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("%s contains %q:\n%s", name, forbidden, text)
			}
		}
	}
	if !strings.Contains(logs.String(), "face compare") {
		t.Fatalf("expected an audit log line for the comparison, got:\n%s", logs.String())
	}
}
