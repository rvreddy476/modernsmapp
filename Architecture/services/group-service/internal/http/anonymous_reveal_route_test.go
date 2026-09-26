package http

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRevealAuthorRouteIsRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(nil).RegisterRoutes(r)
	const path = "/v1/groups/:groupId/posts/v2/:postId/author"
	for _, ri := range r.Routes() {
		if ri.Method == http.MethodGet && ri.Path == path {
			if !strings.Contains(ri.Handler, ".RevealPostAuthor") {
				t.Fatalf("%s served by %s", path, ri.Handler)
			}
			return
		}
	}
	t.Fatalf("GET %s is not registered", path)
}

// The reveal answer must never be cacheable: a shared cache would hand one
// moderator's answer to the next viewer of the URL.
func TestRevealAuthorAnswerIsNotCacheable(t *testing.T) {
	body := codeOnly(handlerBody(t, "handler_anonymous_reveal.go", "RevealPostAuthor"))
	if !strings.Contains(body, `"private, no-store"`) {
		t.Fatal("RevealPostAuthor does not set Cache-Control: private, no-store")
	}
}
