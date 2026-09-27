package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// GET /v1/internal/posts/by-media/:mediaId (2026-09-27).

const byMediaTestKey = "by-media-test-key"

// The route is registered by RegisterRoutes and sits behind the internal-key
// gate: without the key the handler is never reached (nil service proves
// it); with the key the handler answers (503 here, because the nil service
// cannot resolve — never an empty "no posts").
func TestPostsByMediaRequiresTheInternalKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(nil, nil).WithInternalKey(byMediaTestKey).RegisterRoutes(r)
	path := "/v1/internal/posts/by-media/" + uuid.NewString()

	for name, key := range map[string]string{"no key": "", "wrong key": "nope"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if key != "" {
			req.Header.Set("X-Internal-Service-Key", key)
		}
		res := httptest.NewRecorder()
		r.ServeHTTP(res, req)
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status=%d body=%s", name, res.Code, res.Body.String())
		}
	}

	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-Internal-Service-Key", byMediaTestKey)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("with the key, an unresolvable lookup must be 503: status=%d body=%s", res.Code, res.Body.String())
	}

	// The sibling param route is still reachable (no router conflict).
	vis := httptest.NewRequest(http.MethodGet, "/v1/internal/posts/not-a-uuid/visibility", nil)
	vis.Header.Set("X-Internal-Service-Key", byMediaTestKey)
	visRes := httptest.NewRecorder()
	r.ServeHTTP(visRes, vis)
	if visRes.Code != http.StatusBadRequest {
		t.Fatalf("the visibility route broke: status=%d", visRes.Code)
	}
}

type fakePostsByMedia struct {
	ids   []uuid.UUID
	err   error
	asked uuid.UUID
}

func (f *fakePostsByMedia) PostIDsByMediaID(_ context.Context, mediaID uuid.UUID) ([]uuid.UUID, error) {
	f.asked = mediaID
	return f.ids, f.err
}

func byMediaRouter(reader postsByMediaReader) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/v1/internal/posts/by-media/:mediaId", func(c *gin.Context) { servePostsByMedia(c, reader) })
	return r
}

func byMediaGet(r *gin.Engine, id string) *httptest.ResponseRecorder {
	res := httptest.NewRecorder()
	r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/internal/posts/by-media/"+id, nil))
	return res
}

func TestPostsByMediaAnswers(t *testing.T) {
	media, p1, p2 := uuid.New(), uuid.New(), uuid.New()

	fake := &fakePostsByMedia{ids: []uuid.UUID{p1, p2}}
	res := byMediaGet(byMediaRouter(fake), media.String())
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	var body struct {
		MediaID string   `json:"media_id"`
		PostIDs []string `json:"post_ids"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if fake.asked != media || body.MediaID != media.String() || len(body.PostIDs) != 2 ||
		body.PostIDs[0] != p1.String() || body.PostIDs[1] != p2.String() {
		t.Fatalf("got %+v (asked %s)", body, fake.asked)
	}

	// None is an empty list, not null.
	empty := byMediaGet(byMediaRouter(&fakePostsByMedia{}), media.String())
	if empty.Code != http.StatusOK || !json.Valid(empty.Body.Bytes()) ||
		string(empty.Body.Bytes()) != `{"media_id":"`+media.String()+`","post_ids":[]}` {
		t.Fatalf("no posts: status=%d body=%s", empty.Code, empty.Body.String())
	}

	// A failed lookup is retryable, never an empty answer.
	failed := byMediaGet(byMediaRouter(&fakePostsByMedia{err: errors.New("db down")}), media.String())
	if failed.Code != http.StatusServiceUnavailable {
		t.Fatalf("lookup failure: status=%d", failed.Code)
	}

	for _, bad := range []string{"nope", uuid.Nil.String()} {
		if res := byMediaGet(byMediaRouter(&fakePostsByMedia{}), bad); res.Code != http.StatusBadRequest {
			t.Fatalf("id %q: status=%d", bad, res.Code)
		}
	}
}
