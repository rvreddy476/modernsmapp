package http

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// These tests run in parallel, so they must not call gin.SetMode (it writes
// gin package globals and races under -race); TestMain in main_test.go sets
// gin.TestMode once for the package.

func TestFileReportRequestAcceptsReleasedEntityAliases(t *testing.T) {
	t.Parallel()

	for _, entityType := range []string{"user", "post", "comment", "hashtag", "sound", "reel", "video"} {
		entityType := entityType
		t.Run(entityType, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest("POST", "/v1/reports", strings.NewReader(
				`{"entity_type":"`+entityType+`","entity_id":"11111111-1111-1111-1111-111111111111","reason":"spam"}`,
			))
			request.Header.Set("Content-Type", "application/json")
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = request

			var body FileReportRequest
			if err := ctx.ShouldBindJSON(&body); err != nil {
				t.Fatalf("released entity_type %q was rejected before normalization: %v", entityType, err)
			}
		})
	}
}

func TestFileReportRequestRejectsUnknownEntityType(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequest("POST", "/v1/reports", strings.NewReader(
		`{"entity_type":"story","entity_id":"11111111-1111-1111-1111-111111111111","reason":"spam"}`,
	))
	request.Header.Set("Content-Type", "application/json")
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = request

	var body FileReportRequest
	if err := ctx.ShouldBindJSON(&body); err == nil {
		t.Fatal("unknown entity_type passed the HTTP allowlist")
	}
}
