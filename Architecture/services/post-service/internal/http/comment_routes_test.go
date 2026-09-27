package http

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// Replies for everyone and emoji reactions on comments (2026-09-27).
//
// The web is built against these three routes by name; the inventory is
// read from the REAL router (the same technique as the video-series tests)
// so a handler nobody wired up cannot pass.

func TestCommentRepliesAndReactionRoutesAreRegistered(t *testing.T) {
	routes := registeredRoutesWithPrefix(t, "/v1/comments")
	for _, want := range []string{
		"GET /v1/comments/:commentId/replies",
		"PUT /v1/comments/:commentId/reaction",
		"DELETE /v1/comments/:commentId/reaction",
		// The legacy toggle stays and maps onto the same table.
		"POST /v1/comments/:commentId/like",
		"POST /v1/comments/:commentId/reply",
	} {
		if !routes[want] {
			t.Errorf("%s is not registered. Registered comment routes: %v", want, keysOf(routes))
		}
	}
}

// Both reaction writes are viewer-scoped: no X-User-Id is a 401 at the
// handler, before anything reaches the (nil) service.
func TestCommentReactionRequiresIdentity(t *testing.T) {
	path := "/v1/comments/" + uuid.NewString() + "/reaction"
	for _, tc := range []struct{ method, body string }{
		{http.MethodPut, `{"emoji":"🔥"}`},
		{http.MethodDelete, ""},
	} {
		w := performVideoSeriesRequestWithBody(t, tc.method, path, nil, tc.body)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s with no X-User-Id: status=%d want 401 (body %s)", tc.method, path, w.Code, w.Body.String())
		}
	}
}

// An empty (or whitespace, or over-long) emoji is INVALID_EMOJI, refused
// before the service is reached.
func TestSetCommentReactionRejectsInvalidEmoji(t *testing.T) {
	headers := map[string]string{"X-User-Id": uuid.NewString()}
	path := "/v1/comments/" + uuid.NewString() + "/reaction"
	for name, body := range map[string]string{
		"empty":      `{"emoji":""}`,
		"missing":    `{}`,
		"whitespace": `{"emoji":"   "}`,
		"too long":   `{"emoji":"01234567890123456"}`,
	} {
		t.Run(name, func(t *testing.T) {
			w := performVideoSeriesRequestWithBody(t, http.MethodPut, path, headers, body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("PUT %s body %s: status=%d want 400 (body %s)", path, body, w.Code, w.Body.String())
			}
			if got := w.Body.String(); !containsCode(got, "INVALID_EMOJI") {
				t.Fatalf("PUT %s body %s: code is not INVALID_EMOJI: %s", path, body, got)
			}
		})
	}
}

func containsCode(body, code string) bool {
	return len(body) > 0 && (indexOf(body, `"code":"`+code+`"`) >= 0 || indexOf(body, `"`+code+`"`) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
