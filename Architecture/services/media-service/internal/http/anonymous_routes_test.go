package http

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/postgres"
)

func passthrough(c *gin.Context) { c.Next() }

// The segment route is public-with-gate like every other read; the anonymize
// route exists only under the internal key and at the path group-service
// calls (group-service/internal/service/media_anonymize.go).
func TestAnonymousRoutesAreRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(service.New(postgres.New(nil), nil)).WithInternalKey("k").RegisterRoutes(r, passthrough, passthrough)

	want := map[string]string{
		"GET /v1/media/:mediaId/hls-seg/:name":       ".ServeHLSSegment",
		"POST /v1/media/internal/:mediaId/anonymize": ".AnonymizeMedia",
	}
	found := map[string]string{}
	for _, ri := range r.Routes() {
		found[ri.Method+" "+ri.Path] = ri.Handler
	}
	for key, handler := range want {
		h, ok := found[key]
		if !ok {
			t.Errorf("%s is not registered", key)
			continue
		}
		if !strings.Contains(h, handler) {
			t.Errorf("%s served by %s, want %s", key, h, handler)
		}
	}
}

func TestAnonymizeRouteAbsentWithoutInternalKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(service.New(postgres.New(nil), nil)).RegisterRoutes(r, passthrough, passthrough)
	for _, ri := range r.Routes() {
		if ri.Method == http.MethodPost && ri.Path == "/v1/media/internal/:mediaId/anonymize" {
			t.Fatal("the anonymize route is registered with no internal key — an unkeyed caller could scope assets")
		}
	}
}
