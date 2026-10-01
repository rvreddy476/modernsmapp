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

	"github.com/atpost/post-service/internal/store/postgres"
)

// GET /v1/internal/posts/by-live-stream/:streamId (2026-10-02).

const byLiveStreamTestKey = "by-live-stream-test-key"

// The route is registered by RegisterRoutes behind the internal-key gate:
// without the key the handler is never reached; with it the handler answers
// (503 here: the nil service cannot resolve — never a 404 "no post").
func TestPostByLiveStreamRequiresTheInternalKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(nil, nil).WithInternalKey(byLiveStreamTestKey).RegisterRoutes(r)
	path := "/v1/internal/posts/by-live-stream/" + uuid.NewString()

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
	req.Header.Set("X-Internal-Service-Key", byLiveStreamTestKey)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("with the key, an unresolvable lookup must be 503: status=%d body=%s", res.Code, res.Body.String())
	}

	// The sibling routes are still reachable (no router conflict).
	for sibling, want := range map[string]int{
		"/v1/internal/posts/not-a-uuid/visibility": http.StatusBadRequest,
		"/v1/internal/posts/by-media/not-a-uuid":   http.StatusBadRequest,
	} {
		sreq := httptest.NewRequest(http.MethodGet, sibling, nil)
		sreq.Header.Set("X-Internal-Service-Key", byLiveStreamTestKey)
		sres := httptest.NewRecorder()
		r.ServeHTTP(sres, sreq)
		if sres.Code != want {
			t.Fatalf("%s broke: status=%d", sibling, sres.Code)
		}
	}
}

type fakeLiveStreamPost struct {
	ref   *postgres.LiveStreamPostRef
	err   error
	asked uuid.UUID
}

func (f *fakeLiveStreamPost) LiveStreamPostRef(_ context.Context, streamID uuid.UUID) (*postgres.LiveStreamPostRef, error) {
	f.asked = streamID
	return f.ref, f.err
}

func byLiveStreamGet(reader liveStreamPostReader, id string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/v1/internal/posts/by-live-stream/:streamId", func(c *gin.Context) { servePostByLiveStream(c, reader) })
	res := httptest.NewRecorder()
	r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/internal/posts/by-live-stream/"+id, nil))
	return res
}

func TestPostByLiveStreamAnswers(t *testing.T) {
	stream, post := uuid.New(), uuid.New()

	fake := &fakeLiveStreamPost{ref: &postgres.LiveStreamPostRef{PostID: post, Visibility: "unlisted"}}
	res := byLiveStreamGet(fake, stream.String())
	if res.Code != http.StatusOK || fake.asked != stream {
		t.Fatalf("status=%d asked=%s body=%s", res.Code, fake.asked, res.Body.String())
	}
	if got, want := res.Body.String(), `{"data":{"deleted":false,"post_id":"`+post.String()+`","visibility":"unlisted"}}`; got != want {
		t.Fatalf("body\n got %s\nwant %s", got, want)
	}

	// A soft-deleted post is still answered, flagged.
	del := byLiveStreamGet(&fakeLiveStreamPost{ref: &postgres.LiveStreamPostRef{PostID: post, Visibility: "public", Deleted: true}}, stream.String())
	var body struct {
		Data struct {
			PostID     string `json:"post_id"`
			Visibility string `json:"visibility"`
			Deleted    *bool  `json:"deleted"`
		} `json:"data"`
	}
	if err := json.Unmarshal(del.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if del.Code != http.StatusOK || body.Data.Deleted == nil || !*body.Data.Deleted || body.Data.PostID != post.String() || body.Data.Visibility != "public" {
		t.Fatalf("deleted post: status=%d body=%s", del.Code, del.Body.String())
	}
}

func TestPostByLiveStreamNoPostIs404(t *testing.T) {
	res := byLiveStreamGet(&fakeLiveStreamPost{}, uuid.NewString())
	if res.Code != http.StatusNotFound || res.Body.String() != `{"error":"not_found"}` {
		t.Fatalf("no post: status=%d body=%s", res.Code, res.Body.String())
	}
}

// A failed lookup is retryable: never 404 (which the caller reads as "not
// yet") and never 200.
func TestPostByLiveStreamLookupFailureIs503(t *testing.T) {
	res := byLiveStreamGet(&fakeLiveStreamPost{err: errors.New("db down")}, uuid.NewString())
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("lookup failure: status=%d", res.Code)
	}
	if res := byLiveStreamGet(nil, uuid.NewString()); res.Code != http.StatusServiceUnavailable {
		t.Fatalf("no reader: status=%d", res.Code)
	}
}

func TestPostByLiveStreamRefusesBadIDs(t *testing.T) {
	for _, bad := range []string{"nope", uuid.Nil.String()} {
		fake := &fakeLiveStreamPost{}
		if res := byLiveStreamGet(fake, bad); res.Code != http.StatusBadRequest || fake.asked != uuid.Nil {
			t.Fatalf("id %q: status=%d asked=%s", bad, res.Code, fake.asked)
		}
	}
}
