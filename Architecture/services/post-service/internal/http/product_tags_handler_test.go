package http

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// HTTP-level validation tests for CreateProductTag — exercises the
// request-binding + per-field rules in product_tags_handler.go without
// touching the service or store layer. The service is nil because none
// of these inputs reach it; every case is rejected at the handler.

func init() {
	gin.SetMode(gin.TestMode)
}

func newCreateRouter() *gin.Engine {
	r := gin.New()
	h := &Handler{} // svc nil — validation cases never call into it.
	r.POST("/v1/posts/:postId/product-tags", h.CreateProductTag)
	return r
}

func postJSON(t *testing.T, r *gin.Engine, path string, headers map[string]string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var got map[string]any
	if b, _ := io.ReadAll(w.Body); len(b) > 0 {
		_ = json.Unmarshal(b, &got)
	}
	return w, got
}

func TestCreateProductTagRejectsNonUUIDPostID(t *testing.T) {
	w, _ := postJSON(t, newCreateRouter(),
		"/v1/posts/not-a-uuid/product-tags",
		map[string]string{"X-User-Id": uuid.NewString()},
		map[string]any{"affiliate_link_id": uuid.NewString()})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("non-UUID postId: status=%d want 400", w.Code)
	}
}

func TestCreateProductTagRejectsMissingCallerID(t *testing.T) {
	w, _ := postJSON(t, newCreateRouter(),
		"/v1/posts/"+uuid.NewString()+"/product-tags",
		nil,
		map[string]any{"affiliate_link_id": uuid.NewString()})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("missing X-User-Id: status=%d want 401", w.Code)
	}
}

func TestCreateProductTagRejectsInvalidPositionX(t *testing.T) {
	w, _ := postJSON(t, newCreateRouter(),
		"/v1/posts/"+uuid.NewString()+"/product-tags",
		map[string]string{"X-User-Id": uuid.NewString()},
		map[string]any{
			"affiliate_link_id": uuid.NewString(),
			"position_x":        150, // > 100
		})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("position_x=150: status=%d want 400", w.Code)
	}
}

func TestCreateProductTagRejectsInvalidPositionY(t *testing.T) {
	w, _ := postJSON(t, newCreateRouter(),
		"/v1/posts/"+uuid.NewString()+"/product-tags",
		map[string]string{"X-User-Id": uuid.NewString()},
		map[string]any{
			"affiliate_link_id": uuid.NewString(),
			"position_y":        -5, // < 0
		})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("position_y=-5: status=%d want 400", w.Code)
	}
}

func TestCreateProductTagRejectsBackwardsTimeWindow(t *testing.T) {
	w, _ := postJSON(t, newCreateRouter(),
		"/v1/posts/"+uuid.NewString()+"/product-tags",
		map[string]string{"X-User-Id": uuid.NewString()},
		map[string]any{
			"affiliate_link_id": uuid.NewString(),
			"time_start_ms":     5000,
			"time_end_ms":       1000, // end < start
		})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("backwards time window: status=%d want 400", w.Code)
	}
}

func TestCreateProductTagRejectsMissingAffiliateLinkID(t *testing.T) {
	// binding:"required" on AffiliateLinkID — empty body fails validation.
	r := newCreateRouter()
	req := httptest.NewRequest(http.MethodPost,
		"/v1/posts/"+uuid.NewString()+"/product-tags",
		strings.NewReader(`{}`))
	req.Header.Set("X-User-Id", uuid.NewString())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing affiliate_link_id: status=%d want 400", w.Code)
	}
}
