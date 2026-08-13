package http

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// privateDerivedReadPolicy is intentionally exact. A new translation/thread
// resolver must be classified here, and the inventory test below fails if the
// registered routes and policy drift in either direction.
var privateDerivedReadPolicy = map[string]struct{}{
	http.MethodGet + " /v1/chat/messages/:messageId/translation":            {},
	http.MethodGet + " /v1/chat/conversations/:id/threads/:parentMessageId": {},
	http.MethodGet + " /v1/chat/conversations/:id/threads":                  {},
}

func TestPrivateDerivedRouteInventoryIsComplete(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(nil, nil).RegisterRoutes(router)
	actual := make(map[string]struct{})
	for _, route := range router.Routes() {
		if route.Method != http.MethodGet {
			continue
		}
		if strings.Contains(route.Path, "/translation") || strings.Contains(route.Path, "/threads") {
			actual[route.Method+" "+route.Path] = struct{}{}
		}
	}
	for route := range actual {
		if _, classified := privateDerivedReadPolicy[route]; !classified {
			t.Fatalf("private derived resolver is unclassified: %s", route)
		}
	}
	for route := range privateDerivedReadPolicy {
		if _, registered := actual[route]; !registered {
			t.Fatalf("private derived route policy is stale or route was removed: %s", route)
		}
	}
}
