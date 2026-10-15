package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// GET /v1/reels/liked through the real router and service over an
// in-memory store. The response keeps the shape clients already parse
// (data is the bare id array, postbook-ui src/features/reels/data/
// reelFeedApi.ts) and adds meta.next_cursor. The paging and filter rules
// are proved in service/liked_reels_test.go, the SQL in
// service/liked_reels_integration_test.go.

type likedRouteStore struct {
	mu     sync.Mutex
	rows   []postgres.LikedPost // newest first
	limits []int
}

func (s *likedRouteStore) UpsertPostReaction(context.Context, uuid.UUID, uuid.UUID, string) error {
	return nil
}
func (s *likedRouteStore) DeletePostReaction(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}
func (s *likedRouteStore) ListLikedShortFormPosts(_ context.Context, _ uuid.UUID, limit int, after *postgres.LikedCursor) ([]postgres.LikedPost, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limits = append(s.limits, limit)
	start := 0
	if after != nil {
		for i, r := range s.rows {
			if r.PostID == after.PostID {
				start = i + 1
			}
		}
	}
	end := start + limit
	if end > len(s.rows) {
		end = len(s.rows)
	}
	return s.rows[start:end], nil
}

type likedEnvelope struct {
	Data []string `json:"data"`
	Meta *struct {
		NextCursor string `json:"next_cursor"`
	} `json:"meta"`
	Error *struct {
		Code string `json:"code"`
	} `json:"error"`
}

func newLikedRouter(store *likedRouteStore) *gin.Engine {
	gin.SetMode(gin.TestMode)
	svc := service.NewForHandlerTests(service.HandlerTestDeps{
		LikedReels: store,
		ReadGate:   func(context.Context, uuid.UUID, *uuid.UUID) error { return nil },
	})
	router := gin.New()
	New(svc, nil).RegisterReelEngagementRoutes(router)
	return router
}

func getLiked(t *testing.T, router *gin.Engine, query string, user string) (int, likedEnvelope, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/reels/liked"+query, nil)
	if user != "" {
		req.Header.Set("X-User-Id", user)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	var env likedEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("body is not JSON: %v: %s", err, w.Body.String())
	}
	return w.Code, env, w.Body.String()
}

func TestLikedReelsRouteShapeAndCursor(t *testing.T) {
	base := time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)
	store := &likedRouteStore{}
	var want []string
	for i := 0; i < 3; i++ {
		id := uuid.New()
		store.rows = append(store.rows, postgres.LikedPost{PostID: id, LikedAt: base.Add(-time.Duration(i) * time.Minute)})
		want = append(want, id.String())
	}
	router := newLikedRouter(store)
	user := uuid.NewString()

	code, env, body := getLiked(t, router, "?limit=2", user)
	if code != http.StatusOK || !reflect.DeepEqual(env.Data, want[:2]) || env.Meta == nil || env.Meta.NextCursor == "" {
		t.Fatalf("page 1: %d %s; want 200, data %v and meta.next_cursor", code, body, want[:2])
	}
	code, env, body = getLiked(t, router, "?limit=2&cursor="+env.Meta.NextCursor, user)
	if code != http.StatusOK || !reflect.DeepEqual(env.Data, want[2:]) {
		t.Fatalf("page 2: %d %s; want 200, data %v", code, body, want[2:])
	}
	if env.Meta != nil && env.Meta.NextCursor != "" {
		t.Fatalf("last page carries next_cursor %q", env.Meta.NextCursor)
	}
}

func TestLikedReelsRouteEmptyIsAnArray(t *testing.T) {
	router := newLikedRouter(&likedRouteStore{})
	code, env, body := getLiked(t, router, "", uuid.NewString())
	if code != http.StatusOK || env.Data == nil || len(env.Data) != 0 {
		t.Fatalf("empty list: %d %s; want 200 and data []", code, body)
	}
}

func TestLikedReelsRouteLimitDefaultsAndCap(t *testing.T) {
	store := &likedRouteStore{}
	router := newLikedRouter(store)
	user := uuid.NewString()
	getLiked(t, router, "", user)
	getLiked(t, router, "?limit=5000", user)
	getLiked(t, router, "?limit=7", user)
	// Each page reads one row past the page to know whether more remain.
	if want := []int{51, 101, 8}; !reflect.DeepEqual(store.limits, want) {
		t.Fatalf("store limits = %v, want %v (default 50, cap 100)", store.limits, want)
	}
}

func TestLikedReelsRouteRefusals(t *testing.T) {
	router := newLikedRouter(&likedRouteStore{})
	if code, env, body := getLiked(t, router, "?cursor=garbage", uuid.NewString()); code != http.StatusBadRequest || env.Error == nil || env.Error.Code != "INVALID_CURSOR" {
		t.Fatalf("bad cursor: %d %s; want 400 INVALID_CURSOR", code, body)
	}
	if code, _, body := getLiked(t, router, "", ""); code != http.StatusUnauthorized {
		t.Fatalf("no caller: %d %s; want 401", code, body)
	}
}
