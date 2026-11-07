package http

import (
	"testing"

	"github.com/gin-gonic/gin"
)

/*
The v1 post API and the dead approval queue are retired (audit 2026-11-07
R-L1).

  - The v1 feed listed every group_posts row, pending and rejected included.
  - v1 pin/unpin wrote the shared posts table by id, with no group scope on
    unpin (G-M3).
  - The approval queue read post_approval_queue, which nothing ever wrote.

A repo-wide search (Android, the three web trees, every Go service) found no
caller of any of them; /posts/v2 and /posts/v2/pending replace them. This test
keeps them from coming back, and keeps the replacements registered.
*/
func TestRetiredGroupRoutesAreNotRegistered(t *testing.T) {
	r := gin.New()
	New(nil).RegisterRoutes(r)
	have := map[string]bool{}
	for _, ri := range r.Routes() {
		have[ri.Method+" "+ri.Path] = true
	}

	for _, retired := range []string{
		"GET /v1/groups/:groupId/feed",
		"POST /v1/groups/:groupId/posts",
		"DELETE /v1/groups/:groupId/posts/:postId",
		"PUT /v1/groups/:groupId/posts/:postId/pin",
		"DELETE /v1/groups/:groupId/posts/:postId/pin",
		"GET /v1/groups/:groupId/approval-queue",
		"POST /v1/groups/:groupId/approval-queue/:itemId/approve",
		"POST /v1/groups/:groupId/approval-queue/:itemId/reject",
	} {
		if have[retired] {
			t.Errorf("%s is registered again", retired)
		}
	}
	for _, kept := range []string{
		"GET /v1/groups/:groupId/feed/v2",
		"POST /v1/groups/:groupId/posts/v2",
		"DELETE /v1/groups/:groupId/posts/v2/:postId",
		"GET /v1/groups/:groupId/posts/v2/pending",
		"POST /v1/groups/:groupId/posts/v2/:postId/approve",
		"POST /v1/groups/:groupId/posts/v2/:postId/reject",
		"GET /v1/groups/:groupId/rules",
		"GET /v1/groups/:groupId/channels",
		"GET /v1/groups/:groupId/wiki",
	} {
		if !have[kept] {
			t.Errorf("%s is not registered", kept)
		}
	}
}
