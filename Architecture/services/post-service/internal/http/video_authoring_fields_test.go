package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Card / end-screen / chapter FIELD validation (2026-09-10).
//
// The `type` enums were validated yesterday; the rest of the row was not.
// Every case below answered 200 {"saved":1} against the running stack:
//
//   - a card with no title at all — stored "" and the watch page drew an
//     empty box;
//   - target_id: "not-a-uuid" — uuid.Parse failed and the handler DROPPED it,
//     so the save reported success and the card pointed at nothing;
//   - appear_at_ms: -5000 — a timestamp that never arrives;
//   - an end screen whose window closes before it opens.
//
// media_chapters.source was the other half: a CHECK constraint with no
// handler validation, so "bogus" reached Postgres and came back as a 500
// naming media_chapters_source_check — exactly the shape the card enum fixed.
//
// As in video_authoring_enum_test.go the Handler has a nil svc, so anything
// that is not refused here panics instead of quietly passing.

func TestSaveVideoCardsRejectsBadFields(t *testing.T) {
	postID := uuid.NewString()
	cases := []struct {
		name string
		card map[string]any
		code string
	}{
		{"no title", map[string]any{"type": "video", "appear_at_ms": 1000}, "INVALID_TITLE"},
		{"blank title", map[string]any{"type": "video", "title": "   ", "appear_at_ms": 1000}, "INVALID_TITLE"},
		{"target_id is not a uuid", map[string]any{"type": "video", "title": "t", "target_id": "not-a-uuid"}, "INVALID_TARGET_ID"},
		{"negative appear_at_ms", map[string]any{"type": "video", "title": "t", "appear_at_ms": -5000}, "INVALID_TIMING"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := postAuthoringRequest(t, "/v1/posts/"+postID+"/cards",
				map[string]any{"cards": []map[string]any{tc.card}})
			if w.Code != http.StatusBadRequest {
				t.Fatalf("%s: status=%d want 400 (body %s) — %d means the request was "+
					"ACCEPTED and reached the service", tc.name, w.Code, w.Body.String(), statusReachedService)
			}
			code, msg := authoringErrorBody(t, w)
			if code != tc.code && code != "INVALID_REQUEST" {
				t.Fatalf("%s: code=%q want %q", tc.name, code, tc.code)
			}
			if msg == "" {
				t.Fatalf("%s: refusal carried no message; the composer has to be able to show the creator what is wrong", tc.name)
			}
		})
	}
}

// statusReachedService is what the helper below reports when a request got
// past validation and into the (nil) service. It is not a real HTTP status —
// it is the marker that says "this request was ACCEPTED", which is exactly
// what the positive cases need to assert.
const statusReachedService = 599

// postAuthoringRequest drives the same routes with a recovery middleware, so
// a request that passes validation and dereferences the nil service reports
// statusReachedService instead of taking the test process down.
func postAuthoringRequest(t *testing.T, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				c.AbortWithStatus(statusReachedService)
			}
		}()
		c.Next()
	})
	h := &Handler{} // svc nil on purpose; see above.
	r.POST("/v1/posts/:postId/cards", h.SaveVideoCards)
	r.POST("/v1/posts/:postId/end-screens", h.SaveEndScreens)
	r.POST("/v1/posts/:postId/chapters", h.SaveChapters)

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("encode body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("X-User-Id", uuid.NewString())
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// A well-formed card must still save — the validation must not lock the
// composer out. A target_id may legitimately be absent (an external_link card
// points at a URL); absent and "" both mean "no target".
func TestSaveVideoCardsAcceptsWellFormedCard(t *testing.T) {
	postID := uuid.NewString()
	for _, card := range []map[string]any{
		{"type": "video", "title": "Watch this next", "target_id": uuid.NewString(), "appear_at_ms": 1500},
		{"type": "external_link", "title": "Docs", "target_url": "https://example.com", "appear_at_ms": 0},
		{"type": "video", "title": "Empty target", "target_id": "", "appear_at_ms": 10},
	} {
		w := postAuthoringRequest(t, "/v1/posts/"+postID+"/cards",
			map[string]any{"cards": []map[string]any{card}})
		if w.Code != statusReachedService {
			t.Fatalf("well-formed card %v was refused with %d: %s", card, w.Code, w.Body.String())
		}
	}
}

func TestSaveEndScreensRejectsBadFields(t *testing.T) {
	postID := uuid.NewString()
	pos := map[string]any{"x": 1, "y": 1}
	cases := []struct {
		name   string
		screen map[string]any
		code   string
	}{
		{"target_id is not a uuid", map[string]any{"type": "video", "title": "t", "target_id": "not-a-uuid", "position": pos, "start_ms": 0, "end_ms": 100}, "INVALID_TARGET_ID"},
		{"negative start_ms", map[string]any{"type": "video", "title": "t", "position": pos, "start_ms": -100, "end_ms": 100}, "INVALID_TIMING"},
		{"inverted window", map[string]any{"type": "video", "title": "t", "position": pos, "start_ms": 9000, "end_ms": 1000}, "INVALID_TIMING"},
		{"empty window", map[string]any{"type": "video", "title": "t", "position": pos, "start_ms": 5000, "end_ms": 5000}, "INVALID_TIMING"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := postAuthoringRequest(t, "/v1/posts/"+postID+"/end-screens",
				map[string]any{"screens": []map[string]any{tc.screen}})
			if w.Code != http.StatusBadRequest {
				t.Fatalf("%s: status=%d want 400 (body %s) — %d means the request was "+
					"ACCEPTED and reached the service", tc.name, w.Code, w.Body.String(), statusReachedService)
			}
			if code, _ := authoringErrorBody(t, w); code != tc.code && code != "INVALID_REQUEST" {
				t.Fatalf("%s: code=%q want %q", tc.name, code, tc.code)
			}
		})
	}
}

// media_chapters.source: the same 500 class as the card and end-screen enums.
func TestSaveChaptersRejectsUnknownSource(t *testing.T) {
	postID := uuid.NewString()
	for _, bad := range []string{"bogus", "MANUAL", "auto"} {
		t.Run("source="+bad, func(t *testing.T) {
			w := postAuthoringRequest(t, "/v1/posts/"+postID+"/chapters", map[string]any{
				"chapters": []map[string]any{
					{"chapter_index": 0, "title": "Intro", "start_ms": 0, "source": bad},
				},
			})
			if w.Code != http.StatusBadRequest {
				t.Fatalf("chapter source %q: status=%d want 400 (body %s)", bad, w.Code, w.Body.String())
			}
			code, msg := authoringErrorBody(t, w)
			if code != "INVALID_SOURCE" && code != "INVALID_REQUEST" {
				t.Fatalf("chapter source %q: code=%q want INVALID_SOURCE", bad, code)
			}
			if msg == "" || !containsAll(msg, "manual", "ai_generated") {
				t.Fatalf("chapter source %q: message %q does not list the allowed values", bad, msg)
			}
		})
	}
}

// An omitted source is NOT an error: the store writes 'manual' for it, and
// the composer has always been allowed to leave it out.
func TestSaveChaptersAcceptsOmittedSource(t *testing.T) {
	for _, source := range []any{nil, "manual", "ai_generated"} {
		ch := map[string]any{"chapter_index": 0, "title": "Intro", "start_ms": 0}
		if source != nil {
			ch["source"] = source
		}
		w := postAuthoringRequest(t, "/v1/posts/"+uuid.NewString()+"/chapters",
			map[string]any{"chapters": []map[string]any{ch}})
		if w.Code != statusReachedService {
			t.Fatalf("chapter with source=%v was refused with %d: %s", source, w.Code, w.Body.String())
		}
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
