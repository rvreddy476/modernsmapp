package http

import (
	"bytes"
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
)

// Lane D5 — POST /internal/v1/media/faces/liveness over httptest with the
// deterministic mock analyzer and fake storage. No database, AWS or ffmpeg.

type livenessFixture struct {
	r                                    *gin.Engine
	owner, other                         uuid.UUID
	video, oneBlink, longVideo           uuid.UUID
	foreignVideo, processingVideo, photo uuid.UUID
}

func newLivenessFixture(t *testing.T) *livenessFixture {
	t.Helper()
	f := &livenessFixture{owner: uuid.New(), other: uuid.New(), video: uuid.New(), oneBlink: uuid.New(),
		longVideo: uuid.New(), foreignVideo: uuid.New(), processingVideo: uuid.New(), photo: uuid.New()}
	media := &fakeFaceMedia{assets: map[uuid.UUID]*postgres.MediaAsset{}, variants: map[uuid.UUID][]postgres.MediaVariant{}}
	blobs := fakeFaceBlobs{}
	add := func(id, owner uuid.UUID, fileType, status string, durationMs *int, content string) {
		key := "media/" + id.String() + "/original"
		media.assets[id] = &postgres.MediaAsset{ID: id, UploaderID: owner, FileType: fileType, ProcessingStatus: status,
			ModerationStatus: "passed", StorageKey: key, DurationMs: durationMs, FileSizeBytes: int64(len(content))}
		blobs[key] = []byte(content)
	}
	three, six := 3000, 6000
	add(f.video, f.owner, "video", "ready", &three, secretBytes+" "+processing.MockLivenessMarker+"blinks=2:faces=1:subject=owner\n")
	add(f.oneBlink, f.owner, "video", "ready", &three, secretBytes+" "+processing.MockLivenessMarker+"blinks=1:faces=1:subject=owner\n")
	add(f.longVideo, f.owner, "video", "ready", &six, secretBytes+" "+processing.MockLivenessMarker+"blinks=2:faces=1:subject=owner\n")
	add(f.foreignVideo, f.other, "video", "ready", &three, secretBytes+" "+processing.MockLivenessMarker+"blinks=2:faces=1:subject=owner\n")
	add(f.processingVideo, f.owner, "video", "processing", nil, secretBytes+" "+processing.MockLivenessMarker+"blinks=2:faces=1:subject=owner\n")
	add(f.photo, f.owner, "image", "ready", nil, secretBytes+" "+processing.MockFaceMarker+"faces=1:subject=owner\n")

	gin.SetMode(gin.TestMode)
	f.r = gin.New()
	fc := service.NewFaceCompareService(media, blobs, processing.NewMockFaceComparer(), 90).
		WithLiveness(processing.NewMockLivenessAnalyzer(), processing.DefaultLivenessConfig())
	New(nil).WithInternalKey(faceKey).WithFaceCompare(fc).RegisterFaceCompareRoutes(f.r)
	return f
}

func (f *livenessFixture) post(t *testing.T, key string, headers map[string]string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, LivenessPath, bytes.NewReader(raw))
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

func (f *livenessFixture) body(video, reference, requester uuid.UUID) map[string]any {
	return map[string]any{"video_media_id": video.String(), "reference_media_id": reference.String(), "requester_user_id": requester.String()}
}

func livenessOutcome(t *testing.T, w *httptest.ResponseRecorder) service.LivenessOutcome {
	t.Helper()
	var env struct {
		Data service.LivenessOutcome `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return env.Data
}

func TestLivenessRoute_AuthLikeCompare(t *testing.T) {
	f := newLivenessFixture(t)
	if w := f.post(t, "", nil, f.body(f.video, f.photo, f.owner)); w.Code != http.StatusUnauthorized {
		t.Fatalf("missing key: %d", w.Code)
	}
	for _, h := range gatewayIdentityHeaders {
		if w := f.post(t, faceKey, map[string]string{h: f.owner.String()}, f.body(f.video, f.photo, f.owner)); w.Code != http.StatusForbidden || errCode(t, w) != CodeUserCallerRefused {
			t.Fatalf("key + %s: %d %s", h, w.Code, w.Body.String())
		}
	}
}

func TestLivenessRoute_TwoBlinksMatch(t *testing.T) {
	f := newLivenessFixture(t)
	w := f.post(t, faceKey, nil, f.body(f.video, f.photo, f.owner))
	o := livenessOutcome(t, w)
	if w.Code != http.StatusOK || !o.Match || o.BlinksDetected != 2 || !o.SingleFace || !o.SameFaceAcrossFrames ||
		o.Similarity != 99 || o.Reason != "" || o.Provider != "mock" {
		t.Fatalf("two blinks: %d %+v", w.Code, o)
	}
}

func TestLivenessRoute_ReasonsPassThrough(t *testing.T) {
	f := newLivenessFixture(t)
	w := f.post(t, faceKey, nil, f.body(f.oneBlink, f.photo, f.owner))
	if o := livenessOutcome(t, w); w.Code != http.StatusOK || o.Match || o.Reason != processing.LivenessReasonNotEnoughBlinks || o.BlinksDetected != 1 {
		t.Fatalf("one blink: %d %+v", w.Code, o)
	}
	w = f.post(t, faceKey, nil, f.body(f.longVideo, f.photo, f.owner))
	if o := livenessOutcome(t, w); w.Code != http.StatusOK || o.Match || o.Reason != processing.LivenessReasonVideoTooLong || o.DurationMs != 6000 {
		t.Fatalf("long video: %d %+v", w.Code, o)
	}
}

func TestLivenessRoute_Ownership404AndShape(t *testing.T) {
	f := newLivenessFixture(t)
	for name, b := range map[string]map[string]any{
		"foreign video":        f.body(f.foreignVideo, f.photo, f.owner),
		"video still encoding": f.body(f.processingVideo, f.photo, f.owner),
		"reference is a video": f.body(f.video, f.oneBlink, f.owner),
		"video is an image":    f.body(f.photo, f.video, f.owner),
		"other requester":      f.body(f.video, f.photo, f.other),
		"missing video":        f.body(uuid.New(), f.photo, f.owner),
	} {
		if w := f.post(t, faceKey, nil, b); w.Code != http.StatusNotFound || errCode(t, w) != CodeMediaNotFound {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	burst := map[string]any{"frame_media_ids": []string{uuid.NewString(), uuid.NewString()}, "reference_media_id": f.photo.String(), "requester_user_id": f.owner.String()}
	if w := f.post(t, faceKey, nil, burst); w.Code != http.StatusBadRequest || errCode(t, w) != CodeFrameBurstUnsupported {
		t.Fatalf("frame burst: %d %s", w.Code, w.Body.String())
	}
}

func TestLivenessRoute_NoBytesInResponseOrLogs(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)
	f := newLivenessFixture(t)
	var bodies strings.Builder
	for _, b := range []map[string]any{f.body(f.video, f.photo, f.owner), f.body(f.oneBlink, f.photo, f.owner), f.body(f.foreignVideo, f.photo, f.owner)} {
		bodies.WriteString(f.post(t, faceKey, nil, b).Body.String())
	}
	for name, text := range map[string]string{"response": bodies.String(), "logs": logs.String()} {
		lower := strings.ToLower(text)
		for _, forbidden := range []string{strings.ToLower(secretBytes), "atpost-liveness-test", "atpost-face-test", "embedding", "/original", "frame_"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("%s contains %q:\n%s", name, forbidden, text)
			}
		}
	}
	if !strings.Contains(logs.String(), "face liveness") {
		t.Fatalf("no liveness audit line:\n%s", logs.String())
	}
}
