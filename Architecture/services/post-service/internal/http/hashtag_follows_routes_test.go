package http

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Followed hashtags over the real routes (2026-10-24).

type routeFollowStore struct {
	follows map[uuid.UUID]map[string]time.Time
	blocked map[string]bool
}

func (m *routeFollowStore) FollowHashtag(_ context.Context, u uuid.UUID, tag string, max int) (bool, error) {
	if m.follows[u] == nil {
		m.follows[u] = map[string]time.Time{}
	}
	if _, ok := m.follows[u][tag]; ok {
		return false, nil
	}
	if len(m.follows[u]) >= max {
		return false, postgres.ErrHashtagFollowLimit
	}
	m.follows[u][tag] = time.Now()
	return true, nil
}
func (m *routeFollowStore) UnfollowHashtag(_ context.Context, u uuid.UUID, tag string) (bool, error) {
	_, ok := m.follows[u][tag]
	delete(m.follows[u], tag)
	return ok, nil
}
func (m *routeFollowStore) IsFollowingHashtag(_ context.Context, u uuid.UUID, tag string) (bool, error) {
	_, ok := m.follows[u][tag]
	return ok, nil
}
func (m *routeFollowStore) ListFollowedHashtags(_ context.Context, u uuid.UUID, _ int, _ string) ([]postgres.FollowedHashtag, string, error) {
	out := []postgres.FollowedHashtag{}
	for t, at := range m.follows[u] {
		out = append(out, postgres.FollowedHashtag{Tag: t, FollowedAt: at})
	}
	return out, "", nil
}
func (m *routeFollowStore) FollowedHashtagTags(_ context.Context, u uuid.UUID) ([]string, error) {
	var out []string
	for t := range m.follows[u] {
		out = append(out, t)
	}
	return out, nil
}
func (m *routeFollowStore) GetPostsByHashtags(context.Context, []string, uuid.UUID, int, string, time.Time) ([]postgres.Post, string, error) {
	return []postgres.Post{}, "", nil
}
func (m *routeFollowStore) GetBlockedHashtags(_ context.Context, tags []string) ([]string, error) {
	var out []string
	for _, t := range tags {
		if m.blocked[t] {
			out = append(out, t)
		}
	}
	return out, nil
}

func TestHashtagFollowRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	st := &routeFollowStore{follows: map[uuid.UUID]map[string]time.Time{}, blocked: map[string]bool{"banned": true}}
	r := gin.New()
	New(service.NewForHandlerTests(service.HandlerTestDeps{HashtagFollows: st}), nil).RegisterRoutes(r)
	u := uuid.New()

	if code, _ := doJSON(r, http.MethodPost, "/v1/hashtags/cricket/follow", uuid.Nil, ""); code != http.StatusUnauthorized {
		t.Fatalf("signed out: %d", code)
	}
	for i := 0; i < 2; i++ { // idempotent; %23 is a leading #
		code, env := doJSON(r, http.MethodPost, "/v1/hashtags/%23Cricket/follow", u, "")
		if code != http.StatusOK || !strings.Contains(string(env["data"]), `"tag":"cricket"`) || !strings.Contains(string(env["data"]), `"following":true`) {
			t.Fatalf("follow %d: %d %s", i, code, env["data"])
		}
	}
	if len(st.follows[u]) != 1 {
		t.Fatalf("follows = %v", st.follows[u])
	}
	code, env := doJSON(r, http.MethodGet, "/v1/hashtags/CRICKET/follow", u, "")
	if code != http.StatusOK || !strings.Contains(string(env["data"]), `"following":true`) {
		t.Fatalf("state: %d %s", code, env["data"])
	}
	code, env = doJSON(r, http.MethodGet, "/v1/hashtags/followed", u, "")
	if code != http.StatusOK || !strings.Contains(string(env["data"]), `"display_name":"#cricket"`) {
		t.Fatalf("list: %d %s", code, env["data"])
	}
	code, env = doJSON(r, http.MethodGet, "/v1/hashtags/followed/posts", u, "")
	if code != http.StatusOK || string(env["data"]) != "[]" {
		t.Fatalf("followed posts: %d %s", code, env["data"])
	}
	for i := 0; i < 2; i++ {
		code, env = doJSON(r, http.MethodDelete, "/v1/hashtags/cricket/follow", u, "")
		if code != http.StatusOK || !strings.Contains(string(env["data"]), `"following":false`) {
			t.Fatalf("unfollow %d: %d %s", i, code, env["data"])
		}
	}
	if code, env := doJSON(r, http.MethodPost, "/v1/hashtags/two%20words/follow", u, ""); code != http.StatusBadRequest || errCode(env) != "INVALID_HASHTAG" {
		t.Fatalf("invalid: %d %s", code, errCode(env))
	}
	if code, env := doJSON(r, http.MethodPost, "/v1/hashtags/banned/follow", u, ""); code != http.StatusNotFound || errCode(env) != "HASHTAG_UNAVAILABLE" {
		t.Fatalf("blocked: %d %s", code, errCode(env))
	}
	// The existing hashtag routes still resolve beside the new static ones.
	if code, _ := doJSON(r, http.MethodGet, "/v1/hashtags/followed/follow", u, ""); code != http.StatusOK {
		t.Fatalf("a tag literally named followed: %d", code)
	}
}
