package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/atpost/post-service/internal/engagement"
	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Heart only and no reel repost through the real router (service/
// heart_only.go, founder decision 24 Oct 2026).

type heartEnvelope struct {
	Error *struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

// failOpenLimiter is the engagement limiter with no Redis behind it: it
// fails open at once, as production does on a Redis error.
func failOpenLimiter() *engagement.RateLimiter {
	return engagement.NewRateLimiter(redis.NewClient(&redis.Options{
		Addr:       "unit-test:0",
		MaxRetries: -1,
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("no redis in unit tests")
		},
	}))
}

// heartRouter registers the post, repost and reel engagement routes over a
// Service whose post (of contentType) is answered by the seams. commentHits
// counts comment lookups — a comment reaction that got past validation.
func heartRouter(contentType, visibility string, commentHits *int) *gin.Engine {
	gin.SetMode(gin.TestMode)
	load := func(postID uuid.UUID) *postgres.Post {
		return &postgres.Post{ID: postID, AuthorID: uuid.New(), ContentType: contentType, Visibility: visibility}
	}
	svc := service.NewForHandlerTests(service.HandlerTestDeps{
		ReadGate:    func(context.Context, uuid.UUID, *uuid.UUID) error { return nil },
		RateLimiter: failOpenLimiter(),
		EngagementPost: func(_ context.Context, postID, _ uuid.UUID) (*postgres.Post, error) {
			return load(postID), nil
		},
		RepostTarget: func(_ context.Context, postID uuid.UUID) (*postgres.Post, error) {
			return load(postID), nil
		},
		CommentLookup: func(context.Context, uuid.UUID) (*postgres.Comment, error) {
			*commentHits++
			return nil, fmt.Errorf("COMMENT_NOT_FOUND")
		},
	})
	router := gin.New()
	h := New(svc, nil)
	h.RegisterRoutes(router)
	h.RegisterRepostRoutes(router)
	h.RegisterReelEngagementRoutes(router)
	return router
}

func heartCall(t *testing.T, router *gin.Engine, method, path, body string) (int, heartEnvelope, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", uuid.NewString())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	var env heartEnvelope
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	return w.Code, env, w.Body.String()
}

var heartContentTypes = []string{"post", "poll", "flick", "reel", "long_video", "video"}

// Every post-reaction route refuses each emoji on every content type with
// 400 REACTION_NOT_ALLOWED {allowed: ["like"]}.
func TestPostReactionRoutes_RefuseEmoji(t *testing.T) {
	var hits int
	for _, ct := range heartContentTypes {
		router := heartRouter(ct, "public", &hits)
		for _, emoji := range []string{"love", "haha", "wow", "sad", "angry"} {
			id := uuid.NewString()
			for _, rt := range []struct{ path, body string }{
				{"/v1/posts/" + id + "/reactions", `{"reaction":"` + emoji + `"}`},
				{"/v1/posts/" + id + "/react", `{"reaction_type":"` + emoji + `"}`},
				{"/v1/reels/" + id + "/react", `{"reaction":"` + emoji + `"}`},
			} {
				code, env, body := heartCall(t, router, http.MethodPost, rt.path, rt.body)
				if code != http.StatusBadRequest || env.Error == nil || env.Error.Code != "REACTION_NOT_ALLOWED" {
					t.Fatalf("%s POST %s %s: %d %s, want 400 REACTION_NOT_ALLOWED", ct, rt.path, rt.body, code, body)
				}
				if got := env.Error.Details["allowed"]; !reflect.DeepEqual(got, []any{"like"}) {
					t.Fatalf("%s %s: details.allowed = %v, want [like]", ct, rt.path, got)
				}
			}
		}
	}
}

// The heart is not refused: the first two routes go on to their next step
// (no Scylla in unit tests, so a later failure is expected and fine — it is
// simply not the refusal).
func TestPostReactionRoutes_AcceptTheHeart(t *testing.T) {
	var hits int
	router := heartRouter("post", "public", &hits)
	id := uuid.NewString()
	for _, rt := range []struct{ path, body string }{
		{"/v1/posts/" + id + "/reactions", `{"reaction":"like"}`},
		{"/v1/posts/" + id + "/react", `{"reaction_type":"like"}`},
		{"/v1/reels/" + id + "/react", `{"reaction":"like"}`},
	} {
		func() {
			defer func() { _ = recover() }() // a nil Scylla store past the guard
			code, env, body := heartCall(t, router, http.MethodPost, rt.path, rt.body)
			if env.Error != nil && env.Error.Code == "REACTION_NOT_ALLOWED" {
				t.Fatalf("POST %s like: %d %s — the heart was refused", rt.path, code, body)
			}
		}()
	}
}

// Comment reactions keep the full emoji set: each reaches the comment lookup
// (404 here, the seam has no comment) and none is REACTION_NOT_ALLOWED.
func TestCommentReactions_Unaffected(t *testing.T) {
	var hits int
	router := heartRouter("post", "public", &hits)
	emojis := []string{"👍", "❤️", "😂", "😮", "😢", "😡"}
	for _, e := range emojis {
		code, env, body := heartCall(t, router, http.MethodPut, "/v1/comments/"+uuid.NewString()+"/reaction", `{"emoji":"`+e+`"}`)
		if code != http.StatusNotFound || env.Error == nil || env.Error.Code != "NOT_FOUND" {
			t.Fatalf("PUT comment reaction %s: %d %s, want the comment lookup's 404", e, code, body)
		}
	}
	if hits != len(emojis) {
		t.Fatalf("comment lookups = %d, want %d", hits, len(emojis))
	}
}

// A reel is refused on every repost route, plain and quote, with its
// content_type in the details.
func TestRepostRoutes_RefuseReels(t *testing.T) {
	var hits int
	for _, ct := range []string{"flick", "reel"} {
		router := heartRouter(ct, "public", &hits)
		id := uuid.NewString()
		for _, rt := range []struct{ path, body string }{
			{"/v1/posts/" + id + "/repost", `{"type":"plain"}`},
			{"/v1/posts/" + id + "/repost", `{"type":"quote","quote_text":"so good"}`},
			{"/v1/posts/" + id + "/share", `{"share_type":"repost"}`},
			{"/v1/posts/" + id + "/share", `{"share_type":"quote","quote_text":"so good"}`},
			{"/v1/reels/" + id + "/share", `{"share_type":"repost"}`},
			{"/v1/reels/" + id + "/share", `{"share_type":"quote"}`},
		} {
			code, env, body := heartCall(t, router, http.MethodPost, rt.path, rt.body)
			if code != http.StatusBadRequest || env.Error == nil || env.Error.Code != "REPOST_NOT_ALLOWED" {
				t.Fatalf("%s POST %s %s: %d %s, want 400 REPOST_NOT_ALLOWED", ct, rt.path, rt.body, code, body)
			}
			if got := env.Error.Details["content_type"]; got != ct {
				t.Fatalf("%s %s: details.content_type = %v", ct, rt.path, got)
			}
		}
	}
}

// A feed post and a long video still repost: with a private target the
// visibility rule answers (403 NOT_ELIGIBLE / CIRCLE_SHARE_RESTRICTED), so
// the reel guard let them through. A reel's external share is no repost and
// reaches the same circle check.
func TestRepostRoutes_FeedPostAndLongVideoAllowed(t *testing.T) {
	var hits int
	for _, ct := range []string{"post", "long_video"} {
		router := heartRouter(ct, "private", &hits)
		id := uuid.NewString()
		for _, rt := range []struct{ path, body, code string }{
			{"/v1/posts/" + id + "/repost", `{"type":"plain"}`, "NOT_ELIGIBLE"},
			{"/v1/posts/" + id + "/repost", `{"type":"quote","quote_text":"so good"}`, "NOT_ELIGIBLE"},
			{"/v1/posts/" + id + "/share", `{"share_type":"repost"}`, "CIRCLE_SHARE_RESTRICTED"},
			{"/v1/posts/" + id + "/share", `{"share_type":"quote","quote_text":"so good"}`, "CIRCLE_SHARE_RESTRICTED"},
		} {
			code, env, body := heartCall(t, router, http.MethodPost, rt.path, rt.body)
			if code != http.StatusForbidden || env.Error == nil || env.Error.Code != rt.code {
				t.Fatalf("%s POST %s %s: %d %s, want 403 %s", ct, rt.path, rt.body, code, body, rt.code)
			}
		}
	}
	router := heartRouter("flick", "private", &hits)
	code, env, body := heartCall(t, router, http.MethodPost, "/v1/posts/"+uuid.NewString()+"/share", `{"share_type":"external"}`)
	if code != http.StatusForbidden || env.Error == nil || env.Error.Code != "CIRCLE_SHARE_RESTRICTED" {
		t.Fatalf("flick external share: %d %s, want the circle check", code, body)
	}
}
