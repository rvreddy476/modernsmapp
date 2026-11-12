package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/feed-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ECHO_ENABLED (echo-contract.md "Switches"): off means both note routes
// answer 503 ECHO_NOT_ENABLED; on, they are signed-in routes.

func notesRouter(t *testing.T, enabled string) *gin.Engine {
	t.Helper()
	t.Setenv("ECHO_ENABLED", enabled)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(service.New(nil, nil, nil)).RegisterRoutes(r)
	return r
}

func notesGet(r *gin.Engine, path, user string) (int, string) {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if user != "" {
		req.Header.Set("X-User-Id", user)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var body struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	code := ""
	if body.Error != nil {
		code = body.Error.Code
	}
	return w.Code, code
}

func TestNotesRoutes_SwitchedOffAnswer503(t *testing.T) {
	for _, enabled := range []string{"", "false", "no"} {
		r := notesRouter(t, enabled)
		for _, path := range []string{"/v1/feed/notes/following", "/v1/feed/notes/for-you"} {
			status, code := notesGet(r, path, uuid.NewString())
			if status != http.StatusServiceUnavailable || code != "ECHO_NOT_ENABLED" {
				t.Errorf("ECHO_ENABLED=%q %s: got %d %q, want 503 ECHO_NOT_ENABLED", enabled, path, status, code)
			}
		}
	}
}

func TestNotesRoutes_SwitchedOnRequireSignIn(t *testing.T) {
	r := notesRouter(t, "true")
	for _, path := range []string{"/v1/feed/notes/following", "/v1/feed/notes/for-you"} {
		status, code := notesGet(r, path, "")
		if status != http.StatusUnauthorized || code != "UNAUTHORIZED" {
			t.Errorf("%s signed out: got %d %q, want 401 UNAUTHORIZED", path, status, code)
		}
	}
}

func TestNotesRoutes_BadCursorIs400(t *testing.T) {
	r := notesRouter(t, "true")
	for _, path := range []string{"/v1/feed/notes/following?cursor=bogus!", "/v1/feed/notes/for-you?cursor=bogus!"} {
		if status, code := notesGet(r, path, uuid.NewString()); status != http.StatusBadRequest || code != "INVALID_CURSOR" {
			t.Errorf("%s: got %d %q, want 400 INVALID_CURSOR", path, status, code)
		}
	}
}

func TestForYouCursorRoundTripAndRejects(t *testing.T) {
	in := service.ForYouCursor{Snapshot: "0a1b2c3d4e5f6a7b", Offset: 40}
	got, err := decodeForYouCursor(encodeForYouCursor(in))
	if err != nil || got != in {
		t.Fatalf("round trip: got %+v, %v", got, err)
	}
	for _, raw := range []string{"%%%", "Zm9v", encodeForYouCursor(service.ForYouCursor{Snapshot: "X!", Offset: 1})} {
		if _, err := decodeForYouCursor(raw); err == nil {
			t.Errorf("cursor %q accepted", raw)
		}
	}
}

func TestOnlyNotesKeepsNotes(t *testing.T) {
	got := onlyNotes([]service.HydratedPost{{ContentType: "note"}, {ContentType: "post"}, {ContentType: "note"}})
	if len(got) != 2 {
		t.Fatalf("kept %d, want 2", len(got))
	}
}
