package http

import (
	"os"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestMain sets gin's mode once, before any test runs. gin.SetMode writes
// package-level globals in gin, so calling it from a t.Parallel test races
// with every other parallel test that calls it (go test -race flagged the
// FileReportRequest tests). Tests that run in parallel must not call
// gin.SetMode themselves; they rely on this.
func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}
