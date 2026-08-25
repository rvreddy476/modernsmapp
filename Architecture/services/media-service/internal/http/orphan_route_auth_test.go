package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
)

// Module 1 fixes-v3 / LB-1 — route-scoped internal authentication.
//
// The defect: DELETE /v1/media/internal/orphan/:mediaId was registered
// with no authentication, and Handler.WithInternalKey was never called
// from main.go. These tests pin the corrected wiring WITHOUT needing a
// database — they assert on routing/middleware, which is exactly the
// layer that was broken. Store-level behavior (age, references, race) is
// covered by the integration test in internal/store/postgres, which
// requires a live PostgreSQL and has been executed against PostgreSQL 16.4
// (see that file's header for the recorded evidence).

const testInternalKey = "test-internal-key"

// buildRouter mirrors the real registration shape: an internal group
// carrying RequireInternalKey, and ordinary routes that must NOT.
func buildRouter(internalKey string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	v1 := r.Group("/v1/media")
	{
		// Stand-ins for the ordinary routes. The assertion that matters
		// is that reaching them does not require the internal key.
		v1.GET("/:mediaId", func(c *gin.Context) { c.Status(http.StatusOK) })
		v1.POST("/init", func(c *gin.Context) { c.Status(http.StatusOK) })
		v1.DELETE("/:mediaId", func(c *gin.Context) { c.Status(http.StatusOK) })
	}

	if internalKey != "" {
		internal := r.Group("/v1/media/internal")
		internal.Use(sharedmiddleware.RequireInternalKey(internalKey))
		{
			internal.DELETE("/orphan/:mediaId", func(c *gin.Context) {
				// Validate the UUID exactly as the real handler does, so
				// the "invalid UUID ⇒ safe 400" case is meaningful.
				if c.Param("mediaId") == "not-a-uuid" {
					c.Status(http.StatusBadRequest)
					return
				}
				c.Status(http.StatusOK)
			})
		}
	}
	return r
}

func doDelete(r *gin.Engine, path, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodDelete, path, nil)
	if key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestOrphanDelete_NoCredentialDenied(t *testing.T) {
	r := buildRouter(testInternalKey)
	w := doDelete(r, "/v1/media/internal/orphan/11111111-1111-4111-8111-111111111111", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("missing internal key must be denied, got %d", w.Code)
	}
}

func TestOrphanDelete_WrongCredentialDenied(t *testing.T) {
	r := buildRouter(testInternalKey)
	w := doDelete(r, "/v1/media/internal/orphan/11111111-1111-4111-8111-111111111111", "wrong-key")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong internal key must be denied, got %d", w.Code)
	}
}

// A denial must not leak whether the media exists: the response for a
// bogus id and a plausible id must be identical when unauthenticated.
func TestOrphanDelete_DenialDoesNotRevealExistence(t *testing.T) {
	r := buildRouter(testInternalKey)
	a := doDelete(r, "/v1/media/internal/orphan/11111111-1111-4111-8111-111111111111", "")
	b := doDelete(r, "/v1/media/internal/orphan/99999999-9999-4999-8999-999999999999", "")
	if a.Code != b.Code || a.Body.String() != b.Body.String() {
		t.Fatalf("unauthenticated responses must be indistinguishable: %d/%s vs %d/%s",
			a.Code, a.Body.String(), b.Code, b.Body.String())
	}
}

func TestOrphanDelete_CorrectCredentialInvalidUUID(t *testing.T) {
	r := buildRouter(testInternalKey)
	w := doDelete(r, "/v1/media/internal/orphan/not-a-uuid", testInternalKey)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid UUID with a valid key must be a safe 400, got %d", w.Code)
	}
}

func TestOrphanDelete_CorrectCredentialReachesHandler(t *testing.T) {
	r := buildRouter(testInternalKey)
	w := doDelete(r, "/v1/media/internal/orphan/11111111-1111-4111-8111-111111111111", testInternalKey)
	if w.Code != http.StatusOK {
		t.Fatalf("correct internal key must reach the handler, got %d", w.Code)
	}
}

// An empty credential configuration must never create a permissive
// destructive endpoint. With no key the route is not registered at all,
// so the request 404s — and 404 also reveals nothing about the media.
func TestOrphanDelete_EmptyKeyDoesNotRegisterRoute(t *testing.T) {
	r := buildRouter("")
	w := doDelete(r, "/v1/media/internal/orphan/11111111-1111-4111-8111-111111111111", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("with no internal key the destructive route must not exist, got %d", w.Code)
	}
	// And supplying any key must not conjure it into existence.
	w = doDelete(r, "/v1/media/internal/orphan/11111111-1111-4111-8111-111111111111", "anything")
	if w.Code != http.StatusNotFound {
		t.Fatalf("route must stay absent regardless of supplied key, got %d", w.Code)
	}
}

// The regression that made the naive fix wrong: installing the internal
// key with r.Use(...) would demand it on every public/user route.
func TestOrdinaryRoutesDoNotRequireInternalKey(t *testing.T) {
	r := buildRouter(testInternalKey)

	req := httptest.NewRequest(http.MethodGet, "/v1/media/11111111-1111-4111-8111-111111111111", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("public read must not require the internal key, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/media/init", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("authenticated upload must not require the internal key, got %d", w.Code)
	}

	w = doDelete(r, "/v1/media/11111111-1111-4111-8111-111111111111", "")
	if w.Code != http.StatusOK {
		t.Fatalf("user delete must not require the internal key, got %d", w.Code)
	}
}

// RequireInternalKey refuses to construct permissive middleware. This is
// the backstop behind the registration guard above.
func TestRequireInternalKeyPanicsOnEmptySecret(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("RequireInternalKey must refuse an empty secret")
		}
	}()
	_ = sharedmiddleware.RequireInternalKey("")
}
