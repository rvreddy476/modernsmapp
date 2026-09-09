package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Card / end-screen `type` validation.
//
// Both columns carry a CHECK constraint and nothing else used to enforce
// them, so "sponsor" or a typo travelled all the way to Postgres and came
// back as a 500 with a constraint name in it. These cases must be rejected at
// the handler, before the service is touched — which is why the Handler below
// has a nil svc: a request that reaches the service in these tests panics
// instead of quietly passing.
//
// The two lists genuinely differ. `poll` is a card and not an end screen;
// `channel_subscribe` is an end screen and not a card. The cross cases below
// pin that difference so a later "cleanup" cannot unify them silently.

func newAuthoringEnumRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handler{} // svc nil — every case here is rejected at the handler.
	r.POST("/v1/posts/:postId/cards", h.SaveVideoCards)
	r.POST("/v1/posts/:postId/end-screens", h.SaveEndScreens)
	return r
}

func postAuthoringJSON(t *testing.T, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("encode body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("X-User-Id", uuid.NewString())
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	newAuthoringEnumRouter().ServeHTTP(w, req)
	return w
}

func authoringErrorBody(t *testing.T, w *httptest.ResponseRecorder) (code, message string) {
	t.Helper()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return body.Error.Code, body.Error.Message
}

func TestSaveVideoCardsRejectsUnknownType(t *testing.T) {
	postID := uuid.NewString()
	for _, bad := range []string{"sponsor", "", "VIDEO", "channel_subscribe"} {
		t.Run("type="+bad, func(t *testing.T) {
			w := postAuthoringJSON(t, "/v1/posts/"+postID+"/cards", map[string]any{
				"cards": []map[string]any{
					{"type": "video", "title": "ok", "appear_at_ms": 0},
					{"type": bad, "title": "bad", "appear_at_ms": 10},
				},
			})
			if w.Code != http.StatusBadRequest {
				t.Fatalf("card type %q: status=%d want 400 (body %s)", bad, w.Code, w.Body.String())
			}
			code, msg := authoringErrorBody(t, w)
			if code != "INVALID_TYPE" && code != "INVALID_REQUEST" {
				t.Fatalf("card type %q: code=%q want INVALID_TYPE", bad, code)
			}
			// The message has to be usable by the composer: it must name
			// the offending value or the allowed set.
			if !strings.Contains(msg, "video") {
				t.Fatalf("card type %q: message %q does not list the allowed values", bad, msg)
			}
		})
	}
}

func TestSaveEndScreensRejectsUnknownType(t *testing.T) {
	postID := uuid.NewString()
	for _, bad := range []string{"sponsor", "", "poll"} {
		t.Run("type="+bad, func(t *testing.T) {
			w := postAuthoringJSON(t, "/v1/posts/"+postID+"/end-screens", map[string]any{
				"screens": []map[string]any{
					{"type": bad, "position": map[string]any{"x": 1}, "start_ms": 0, "end_ms": 1},
				},
			})
			if w.Code != http.StatusBadRequest {
				t.Fatalf("end-screen type %q: status=%d want 400 (body %s)", bad, w.Code, w.Body.String())
			}
			code, msg := authoringErrorBody(t, w)
			if code != "INVALID_TYPE" && code != "INVALID_REQUEST" {
				t.Fatalf("end-screen type %q: code=%q want INVALID_TYPE", bad, code)
			}
			if !strings.Contains(msg, "channel_subscribe") {
				t.Fatalf("end-screen type %q: message %q does not list the allowed values", bad, msg)
			}
		})
	}
}

// The accepted lists themselves, so the two enums cannot drift into one.
func TestVideoAuthoringEnumsAreDistinct(t *testing.T) {
	if validVideoCardTypes["channel_subscribe"] {
		t.Error("channel_subscribe is an end-screen type, not a card type")
	}
	if validEndScreenTypes["poll"] {
		t.Error("poll is a card type, not an end-screen type")
	}
	for _, want := range []string{"video", "playlist", "poll", "external_link"} {
		if !validVideoCardTypes[want] {
			t.Errorf("card type %q should be accepted", want)
		}
	}
	for _, want := range []string{"video", "playlist", "channel_subscribe", "external_link"} {
		if !validEndScreenTypes[want] {
			t.Errorf("end-screen type %q should be accepted", want)
		}
	}
}
