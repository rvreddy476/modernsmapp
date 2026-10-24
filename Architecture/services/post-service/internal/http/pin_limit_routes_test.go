package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/post-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// PUT /v1/posts/:postId/pin past the limit answers 409 PIN_LIMIT {max: 3},
// not a 500; re-pinning a pinned post and unpinning never hit the limit.

type fakePins struct {
	pinned map[uuid.UUID]bool // the author's pinned posts
	sets   int
}

func (f *fakePins) CountPinnedByAuthor(_ context.Context, _ uuid.UUID, except uuid.UUID) (int, error) {
	n := 0
	for id, on := range f.pinned {
		if on && id != except {
			n++
		}
	}
	return n, nil
}

func (f *fakePins) SetPinned(_ context.Context, postID, _ uuid.UUID, pinned bool) error {
	f.sets++
	f.pinned[postID] = pinned
	return nil
}

func pinRouter(pins *fakePins) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(service.NewForHandlerTests(service.HandlerTestDeps{Pins: pins}), nil)
	r.PUT("/v1/posts/:postId/pin", h.TogglePin)
	return r
}

func putPin(t *testing.T, r *gin.Engine, author, post uuid.UUID, pinned bool) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	body := `{"pinned":false}`
	if pinned {
		body = `{"pinned":true}`
	}
	req := httptest.NewRequest(http.MethodPut, "/v1/posts/"+post.String()+"/pin", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", author.String())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

func TestPinLimitIsAConflictNotAnInternalError(t *testing.T) {
	author := uuid.New()
	first, second, third, fourth := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	pins := &fakePins{pinned: map[uuid.UUID]bool{first: true, second: true, third: true}}
	r := pinRouter(pins)

	w, body := putPin(t, r, author, fourth, true)
	if w.Code != http.StatusConflict {
		t.Fatalf("4th pin: status %d body %s, want 409", w.Code, w.Body.String())
	}
	e, _ := body["error"].(map[string]any)
	details, _ := e["details"].(map[string]any)
	if e["code"] != "PIN_LIMIT" || details["max"] != float64(3) {
		t.Fatalf("4th pin error = %v", e)
	}
	if pins.sets != 0 || pins.pinned[fourth] {
		t.Fatal("the 4th post was pinned anyway")
	}

	// Re-pinning one of the three is not a 4th pin.
	if w, _ := putPin(t, r, author, second, true); w.Code != http.StatusOK {
		t.Fatalf("re-pin: status %d body %s", w.Code, w.Body.String())
	}
	// Unpinning is never limited, and frees a slot.
	if w, _ := putPin(t, r, author, first, false); w.Code != http.StatusOK {
		t.Fatalf("unpin: status %d", w.Code)
	}
	if w, _ := putPin(t, r, author, fourth, true); w.Code != http.StatusOK || !pins.pinned[fourth] {
		t.Fatalf("pin after unpin: status %d", w.Code)
	}
}
