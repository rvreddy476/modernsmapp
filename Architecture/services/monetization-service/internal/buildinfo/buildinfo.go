// Package buildinfo carries the identity of the running binary so that
// image-versus-source drift is visible from the outside, and so every
// creator-fund accrual can name the build that wrote it (plan Phase 5C,
// audit M-15). It is the monetization twin of analytics-service's
// internal/buildinfo.
//
// SHA is set at link time:
//
//	go build -ldflags "-X github.com/atpost/monetization-service/internal/buildinfo.SHA=$(git rev-parse HEAD)" ./cmd/server
//
// and in the Dockerfile via a BUILD_SHA build argument. A binary built
// without it reports "unknown", which is itself the signal: nothing built
// by the release path should ever say that, and an accrual row stamped
// "unknown" is one to ask questions about.
package buildinfo

import (
	"net/http"

	"github.com/atpost/shared/health"
	"github.com/gin-gonic/gin"
)

// SHA is the git commit the binary was built from. Overridden by ldflags.
var SHA = "unknown"

// Healthz is the liveness handler with the build identity attached. It
// answers exactly what health.LivenessHandler answers, plus build_sha.
func Healthz() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "alive", "build_sha": SHA})
	}
}

// RegisterHealthRoutes is a drop-in for checker.RegisterRoutes(r): the
// same two routes, with /healthz carrying build_sha.
func RegisterHealthRoutes(r *gin.Engine, checker *health.Checker) {
	r.GET("/healthz", Healthz())
	r.GET("/readyz", checker.ReadinessHandler())
}
