package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/dating-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Lane D5 — request-shape refusals on POST /v1/dating/verification/selfie.
// These return before the service is reached, so no database is needed.

func selfieRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(service.New(nil, nil)).RegisterRoutes(r)
	return r
}

func postSelfie(r *gin.Engine, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/dating/verification/selfie", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", uuid.NewString())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func selfieErrCode(w *httptest.ResponseRecorder) string {
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	return env.Error.Code
}

// The removed client embedding is refused with 410 whatever else the body
// holds — including a valid video_media_id and challenge_id.
func TestSubmitSelfie_EmbeddingFieldIs410(t *testing.T) {
	r := selfieRouter()
	for _, body := range []string{
		`{"embedding":[0.1,0.2,0.3]}`,
		`{"embedding":[1]}`,
		`{"embedding":[]}`,
		`{"embedding":null,"video_media_id":"` + uuid.NewString() + `","challenge_id":"` + uuid.NewString() + `"}`,
	} {
		w := postSelfie(r, body)
		if w.Code != http.StatusGone || selfieErrCode(w) != CodeSelfieEmbeddingRemoved {
			t.Fatalf("body %s: %d %s; want 410 SELFIE_EMBEDDING_REMOVED", body, w.Code, w.Body.String())
		}
	}
}

func TestSubmitSelfie_ChallengeRequiredAndShape(t *testing.T) {
	r := selfieRouter()
	video := uuid.NewString()
	cases := []struct {
		body       string
		wantStatus int
		wantCode   string
	}{
		{`{"video_media_id":"` + video + `"}`, http.StatusBadRequest, CodeSelfieChallengeRequired},
		{`{"video_media_id":"` + video + `","challenge_id":"  "}`, http.StatusBadRequest, CodeSelfieChallengeRequired},
		{`{"video_media_id":"` + video + `","challenge_id":"not-a-uuid"}`, http.StatusBadRequest, CodeSelfieChallengeInvalid},
		{`{"challenge_id":"` + uuid.NewString() + `"}`, http.StatusBadRequest, "INVALID_REQUEST"},
		// A still image id under the old field is not a blink video.
		{`{"media_id":"` + video + `","challenge_id":"` + uuid.NewString() + `"}`, http.StatusBadRequest, "INVALID_REQUEST"},
		{`{"frame_media_ids":["` + video + `"],"challenge_id":"` + uuid.NewString() + `"}`, http.StatusBadRequest, CodeSelfieFrameBurstUnsupported},
		{`[1,2]`, http.StatusBadRequest, "INVALID_BODY"},
	}
	for _, tc := range cases {
		w := postSelfie(r, tc.body)
		if w.Code != tc.wantStatus || selfieErrCode(w) != tc.wantCode {
			t.Fatalf("body %s: %d %s; want %d %s", tc.body, w.Code, w.Body.String(), tc.wantStatus, tc.wantCode)
		}
	}
}
