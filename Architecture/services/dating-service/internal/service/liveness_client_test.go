package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// Lane D5 — the dating → media-service call uses media's internal liveness
// route with the internal key and carries NO end-user identity headers
// (media-service refuses a user identity on that route).

func TestHTTPLivenessClient_InternalRouteNoUserHeaders(t *testing.T) {
	video, reference, user := uuid.New(), uuid.New(), uuid.New()
	var gotPath, gotMethod, gotKey string
	var gotHeaders http.Header
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod, gotKey = r.URL.Path, r.Method, r.Header.Get("X-Internal-Service-Key")
		gotHeaders = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"blinks_detected":2,"frames_analysed":32,"duration_ms":3100,"single_face":true,` +
			`"same_face_across_frames":true,"similarity":93.5,"match":true,"provider":"rekognition"}}`))
	}))
	defer srv.Close()

	res, err := NewHTTPLivenessClient(srv.URL+"/", "svc-key", nil).CheckLiveness(context.Background(),
		LivenessRequest{VideoMediaID: video, ReferenceMediaID: reference, RequesterUserID: user})
	if err != nil {
		t.Fatalf("liveness: %v", err)
	}
	if MediaLivenessPath != "/internal/v1/media/faces/liveness" {
		t.Fatalf("MediaLivenessPath = %q", MediaLivenessPath)
	}
	if gotMethod != http.MethodPost || gotPath != MediaLivenessPath {
		t.Fatalf("called %s %s", gotMethod, gotPath)
	}
	if gotKey != "svc-key" {
		t.Fatalf("internal key header = %q", gotKey)
	}
	for _, h := range []string{"X-User-Id", "X-Verified-User-Id", "X-Scopes", "X-Admin-Role", "Authorization"} {
		if v := gotHeaders.Get(h); v != "" {
			t.Fatalf("request carried user identity header %s=%q", h, v)
		}
	}
	want := map[string]any{"video_media_id": video.String(), "reference_media_id": reference.String(), "requester_user_id": user.String()}
	if len(gotBody) != len(want) {
		t.Fatalf("body = %v; want exactly %v", gotBody, want)
	}
	for k, v := range want {
		if gotBody[k] != v {
			t.Fatalf("body[%s] = %v; want %v", k, gotBody[k], v)
		}
	}
	if res.BlinksDetected != 2 || res.FramesAnalysed != 32 || !res.SingleFace || !res.SameFaceAcrossFrames ||
		res.Similarity != 93.5 || !res.Match || res.Provider != "rekognition" || res.DurationMs != 3100 {
		t.Fatalf("decoded %+v", res)
	}
}

func TestHTTPLivenessClient_ErrorMapping(t *testing.T) {
	req := LivenessRequest{VideoMediaID: uuid.New(), ReferenceMediaID: uuid.New(), RequesterUserID: uuid.New()}
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"not found", http.StatusNotFound, `{"error":{"code":"MEDIA_NOT_FOUND"}}`, ErrSelfieMediaNotFound},
		{"unreadable video", http.StatusUnprocessableEntity, `{"error":{"code":"VIDEO_UNSUPPORTED"}}`, ErrSelfieVideoUnsupported},
		{"refused", http.StatusForbidden, `{"error":{"code":"USER_CALLER_REFUSED"}}`, ErrFaceCompareUnavailable},
		{"provider down", http.StatusServiceUnavailable, `{}`, ErrFaceCompareUnavailable},
		{"garbage", http.StatusOK, `not json`, ErrFaceCompareUnavailable},
		{"no data", http.StatusOK, `{"data":null}`, ErrFaceCompareUnavailable},
		{"out of range", http.StatusOK, `{"data":{"similarity":140,"blinks_detected":2}}`, ErrFaceCompareUnavailable},
		{"negative blinks", http.StatusOK, `{"data":{"similarity":90,"blinks_detected":-1}}`, ErrFaceCompareUnavailable},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		_, err := NewHTTPLivenessClient(srv.URL, "k", nil).CheckLiveness(context.Background(), req)
		srv.Close()
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: err=%v, want %v", tc.name, err, tc.want)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	if _, err := NewHTTPLivenessClient(url, "k", nil).CheckLiveness(context.Background(), req); !errors.Is(err, ErrFaceCompareUnavailable) {
		t.Fatalf("unreachable: err=%v", err)
	}
}
