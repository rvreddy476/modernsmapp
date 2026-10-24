package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// A creator's Reposts tab on the real router: the reachable
// /v1/creators/:creatorId/reposts and the older /v1/users/:userId/reposts
// answer the same viewer-gated page in the same shape.

type repostsRouteRig struct {
	router             *gin.Engine
	reposter, original uuid.UUID
	open               bool
	viewers            []*uuid.UUID
}

func newRepostsRouteRig(t *testing.T) *repostsRouteRig {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := &repostsRouteRig{reposter: uuid.New(), original: uuid.New(), open: true}
	deps := service.HandlerTestDeps{
		UserReposts: func(_ context.Context, userID uuid.UUID, _ int, _ string) ([]postgres.Repost, string, error) {
			if userID != r.reposter {
				return nil, "", nil
			}
			return []postgres.Repost{{ID: uuid.New(), UserID: userID, OriginalPostID: r.original, RepostType: "quote",
				QuoteText: "worth a watch", Visibility: "public", Status: "active",
				CreatedAt: time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)}}, "2026-10-24T09:00:00Z", nil
		},
		RepostProfileGate: func(_ context.Context, viewer *uuid.UUID, _ uuid.UUID) bool {
			r.viewers = append(r.viewers, viewer)
			return r.open
		},
		BatchPosts: func(_ context.Context, ids []uuid.UUID, _ *uuid.UUID) (map[uuid.UUID]*service.PostDetail, error) {
			out := map[uuid.UUID]*service.PostDetail{}
			for _, id := range ids {
				out[id] = &service.PostDetail{Post: &postgres.Post{ID: id, AuthorID: uuid.New(), Visibility: "public", Text: "hello"}}
			}
			return out, nil
		},
	}
	r.router = gin.New()
	h := New(service.NewForHandlerTests(deps), nil)
	h.RegisterRepostRoutes(r.router)
	return r
}

func (r *repostsRouteRig) get(t *testing.T, path, viewer string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if viewer != "" {
		req.Header.Set("X-User-Id", viewer)
	}
	w := httptest.NewRecorder()
	r.router.ServeHTTP(w, req)
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w, body
}

func TestCreatorRepostsRouteAnswersTheGatedPage(t *testing.T) {
	r := newRepostsRouteRig(t)
	viewer := uuid.New()
	for _, path := range []string{
		"/v1/creators/" + r.reposter.String() + "/reposts",
		"/v1/users/" + r.reposter.String() + "/reposts",
	} {
		w, body := r.get(t, path, viewer.String())
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d body %s", path, w.Code, w.Body.String())
		}
		data, _ := body["data"].(map[string]any)
		items, _ := data["items"].([]any)
		if len(items) != 1 || data["next_cursor"] != "2026-10-24T09:00:00Z" {
			t.Fatalf("%s: data = %v", path, data)
		}
		item, _ := items[0].(map[string]any)
		post, _ := item["post"].(map[string]any)
		if item["original_post_id"] != r.original.String() || item["type"] != "quote" || item["quote_text"] != "worth a watch" || post == nil {
			t.Fatalf("%s: item = %v", path, item)
		}
	}
	if len(r.viewers) != 2 || r.viewers[0] == nil || *r.viewers[0] != viewer {
		t.Fatalf("the profile gate was not asked as the viewer: %v", r.viewers)
	}
}

func TestCreatorRepostsRouteClosedProfileIsAnEmptyPage(t *testing.T) {
	r := newRepostsRouteRig(t)
	r.open = false
	w, body := r.get(t, "/v1/creators/"+r.reposter.String()+"/reposts", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	data, _ := body["data"].(map[string]any)
	if items, ok := data["items"].([]any); !ok || len(items) != 0 {
		t.Fatalf("data = %v, want items: []", data)
	}
	if len(r.viewers) != 1 || r.viewers[0] != nil {
		t.Fatalf("signed out must be asked as nobody: %v", r.viewers)
	}
}

func TestCreatorRepostsRouteErrors(t *testing.T) {
	r := newRepostsRouteRig(t)
	if w, body := r.get(t, "/v1/creators/"+r.reposter.String()+"/reposts?cursor=yesterday", ""); w.Code != http.StatusBadRequest ||
		body["error"].(map[string]any)["code"] != "INVALID_CURSOR" {
		t.Fatalf("bad cursor: %d %s", w.Code, w.Body.String())
	}
	if w, body := r.get(t, "/v1/creators/nope/reposts", ""); w.Code != http.StatusBadRequest ||
		body["error"].(map[string]any)["code"] != "INVALID_ID" {
		t.Fatalf("bad id: %d %s", w.Code, w.Body.String())
	}
}
