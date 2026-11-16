package http

import (
	"context"
	"net/http"
	"testing"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// feed-03 through the real router: POST /repost and POST /share on a post
// the caller may not see are the 404 a missing post gets.

// gatedRepostRouter serves the repost and share routes over a Service whose
// read gate answers gateErr and whose engagement loader answers loadErr
// (nil = the post, public).
func gatedRepostRouter(gateErr, loadErr error) *gin.Engine {
	gin.SetMode(gin.TestMode)
	svc := service.NewForHandlerTests(service.HandlerTestDeps{
		ReadGate:    func(context.Context, uuid.UUID, *uuid.UUID) error { return gateErr },
		RateLimiter: failOpenLimiter(),
		EngagementPost: func(_ context.Context, postID, _ uuid.UUID) (*postgres.Post, error) {
			if loadErr != nil {
				return nil, loadErr
			}
			return &postgres.Post{ID: postID, AuthorID: uuid.New(), ContentType: "post", Visibility: "public"}, nil
		},
	})
	router := gin.New()
	h := New(svc, nil)
	h.RegisterRoutes(router)
	h.RegisterRepostRoutes(router)
	return router
}

type repostRoute struct{ path, body string }

func repostAndShareRoutes(id string) []repostRoute {
	return []repostRoute{
		{"/v1/posts/" + id + "/repost", `{"type":"plain"}`},
		{"/v1/posts/" + id + "/repost", `{"type":"quote","quote_text":"look"}`},
		{"/v1/posts/" + id + "/share", `{"share_type":"external"}`},
		{"/v1/posts/" + id + "/share", `{"share_type":"repost"}`},
		{"/v1/posts/" + id + "/share", `{"share_type":"quote","quote_text":"look"}`},
	}
}

// The handler's read gate refuses before the service is asked. The loader
// here would let the caller through, so only the gate can answer 404.
func TestRepostAndShareRoutes_ReadGateRefuses(t *testing.T) {
	for name, gateErr := range map[string]error{"not visible": service.ErrPostNotVisible, "missing": service.ErrPostNotFound} {
		router := gatedRepostRouter(gateErr, nil)
		for _, rt := range repostAndShareRoutes(uuid.NewString()) {
			code, env, body := heartCall(t, router, http.MethodPost, rt.path, rt.body)
			if code != http.StatusNotFound || env.Error == nil || env.Error.Code != "NOT_FOUND" {
				t.Fatalf("%s: POST %s %s = %d %s, want 404 NOT_FOUND", name, rt.path, rt.body, code, body)
			}
		}
	}
}

// With the read gate open, the service's own loader still refuses, and a
// post the caller may not see is answered byte for byte as a missing one.
func TestRepostAndShareRoutes_ServiceLoaderRefuses_SameAnswerAsMissing(t *testing.T) {
	hidden := gatedRepostRouter(nil, service.ErrPostNotVisible)
	missing := gatedRepostRouter(nil, service.ErrPostNotFound)
	for _, rt := range repostAndShareRoutes(uuid.NewString()) {
		code, env, hiddenBody := heartCall(t, hidden, http.MethodPost, rt.path, rt.body)
		if code != http.StatusNotFound || env.Error == nil || env.Error.Code != "NOT_FOUND" {
			t.Fatalf("hidden: POST %s %s = %d %s, want 404 NOT_FOUND", rt.path, rt.body, code, hiddenBody)
		}
		missingCode, _, missingBody := heartCall(t, missing, http.MethodPost, rt.path, rt.body)
		if missingCode != code || missingBody != hiddenBody {
			t.Fatalf("POST %s %s: hidden = %d %s, missing = %d %s; the two must not differ", rt.path, rt.body, code, hiddenBody, missingCode, missingBody)
		}
	}
}
