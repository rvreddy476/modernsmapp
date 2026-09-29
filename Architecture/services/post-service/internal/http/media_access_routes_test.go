package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// /v1/internal/media-access and /batch with the anonymous sentinel
// (2026-09-29): viewer_id "" (or the nil UUID) is the signed-out viewer and
// reaches the service as uuid.Nil; anything else that is not a UUID is still
// a 403. Driven through the real router with the internal key the route
// demands, over an in-memory store.

type routeMediaStore struct {
	media  uuid.UUID
	author uuid.UUID
	post   *postgres.Post
}

func (s *routeMediaStore) GetMediaAccessFacts(_ context.Context, id uuid.UUID) (*postgres.MediaAccessFacts, error) {
	if id != s.media {
		return nil, nil
	}
	return &postgres.MediaAccessFacts{UploaderID: s.author, ProcessingStatus: "ready", ModerationStatus: "passed"}, nil
}
func (s *routeMediaStore) GetMediaAccessFactsBatch(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]postgres.MediaAccessFacts, error) {
	out := map[uuid.UUID]postgres.MediaAccessFacts{}
	for _, id := range ids {
		if f, _ := s.GetMediaAccessFacts(context.Background(), id); f != nil {
			out[id] = *f
		}
	}
	return out, nil
}
func (s *routeMediaStore) ChannelAvatarOwners(context.Context, []uuid.UUID) (map[uuid.UUID]uuid.UUID, error) {
	return map[uuid.UUID]uuid.UUID{}, nil
}
func (s *routeMediaStore) StoryForMedia(context.Context, uuid.UUID) (*postgres.Story, error) {
	return nil, nil
}
func (s *routeMediaStore) StoriesForMediaBatch(context.Context, []uuid.UUID) (map[uuid.UUID]*postgres.Story, error) {
	return map[uuid.UUID]*postgres.Story{}, nil
}
func (s *routeMediaStore) PostIDsByMediaID(_ context.Context, id uuid.UUID) ([]uuid.UUID, error) {
	if id != s.media {
		return nil, nil
	}
	return []uuid.UUID{s.post.ID}, nil
}
func (s *routeMediaStore) PostIDsByMediaIDs(_ context.Context, ids []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	out := map[uuid.UUID][]uuid.UUID{}
	for _, id := range ids {
		if id == s.media {
			out[id] = []uuid.UUID{s.post.ID}
		}
	}
	return out, nil
}
func (s *routeMediaStore) GetPost(_ context.Context, id uuid.UUID) (*postgres.Post, error) {
	if id != s.post.ID {
		return nil, nil
	}
	cp := *s.post
	return &cp, nil
}
func (s *routeMediaStore) GetPostsByIDs(_ context.Context, ids []uuid.UUID) ([]postgres.Post, error) {
	var out []postgres.Post
	for _, id := range ids {
		if id == s.post.ID {
			out = append(out, *s.post)
		}
	}
	return out, nil
}

const mediaAccessTestKey = "media-access-test-key"

func newMediaAccessRig(t *testing.T, visibility string) (*gin.Engine, *routeMediaStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store := &routeMediaStore{media: uuid.New(), author: uuid.New()}
	store.post = &postgres.Post{ID: uuid.New(), AuthorID: store.author, Visibility: visibility, ReviewStatus: "approved"}
	r := gin.New()
	h := New(service.NewForHandlerTests(service.HandlerTestDeps{MediaAccess: store}), nil).WithInternalKey(mediaAccessTestKey)
	h.RegisterRoutes(r)
	return r, store
}

func postMediaAccess(r *gin.Engine, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", mediaAccessTestKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestMediaAccessAnonymousSentinelReachesTheRule(t *testing.T) {
	for _, viewer := range []string{"", uuid.Nil.String()} {
		t.Run("viewer "+strings.Trim(viewer, "0-")+"public", func(t *testing.T) {
			r, store := newMediaAccessRig(t, "public")
			w := postMediaAccess(r, "/v1/internal/media-access", `{"viewer_id":"`+viewer+`","media_id":"`+store.media.String()+`"}`)
			var out struct {
				Allowed bool   `json:"allowed"`
				Reason  string `json:"reason"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &out)
			if w.Code != 200 || !out.Allowed || out.Reason != "post_allowed" {
				t.Fatalf("anonymous public: %d %s", w.Code, w.Body.String())
			}
		})
		t.Run("viewer "+strings.Trim(viewer, "0-")+"unlisted", func(t *testing.T) {
			r, store := newMediaAccessRig(t, "unlisted")
			w := postMediaAccess(r, "/v1/internal/media-access", `{"viewer_id":"`+viewer+`","media_id":"`+store.media.String()+`"}`)
			if w.Code != 403 || !strings.Contains(w.Body.String(), `"allowed":false`) || !strings.Contains(w.Body.String(), "no_public_post") {
				t.Fatalf("anonymous unlisted: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestMediaAccessStillRefusesAMalformedViewer(t *testing.T) {
	r, store := newMediaAccessRig(t, "public")
	w := postMediaAccess(r, "/v1/internal/media-access", `{"viewer_id":"not-a-uuid","media_id":"`+store.media.String()+`"}`)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "invalid_viewer_id") {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	w = postMediaAccess(r, "/v1/internal/media-access/batch", `{"viewer_id":"not-a-uuid","media_ids":["`+store.media.String()+`"]}`)
	if w.Code != 403 {
		t.Fatalf("batch got %d %s", w.Code, w.Body.String())
	}
}

func TestMediaAccessBatchAnonymousSentinel(t *testing.T) {
	r, store := newMediaAccessRig(t, "public")
	unknown := uuid.New()
	w := postMediaAccess(r, "/v1/internal/media-access/batch",
		`{"viewer_id":"","media_ids":["`+store.media.String()+`","`+unknown.String()+`"]}`)
	var out struct {
		Allowed map[string]bool   `json:"allowed"`
		Reasons map[string]string `json:"reasons"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != 200 {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	if !out.Allowed[store.media.String()] || out.Reasons[store.media.String()] != "post_allowed" {
		t.Fatalf("anonymous public in batch: %s", w.Body.String())
	}
	if out.Allowed[unknown.String()] || out.Reasons[unknown.String()] != "not_found" {
		t.Fatalf("unknown asset in batch: %s", w.Body.String())
	}
}

// A service with no media store behind the gate is unresolved (503), never
// an allow — the handler-test rig without MediaAccess proves the wiring.
func TestMediaAccessUnwiredIsUnresolved(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(service.NewForHandlerTests(service.HandlerTestDeps{}), nil).WithInternalKey(mediaAccessTestKey).RegisterRoutes(r)
	w := postMediaAccess(r, "/v1/internal/media-access", `{"viewer_id":"","media_id":"`+uuid.New().String()+`"}`)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "policy_unresolved") {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
}
