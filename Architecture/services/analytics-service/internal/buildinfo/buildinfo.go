// Package buildinfo carries the identity of the running binary so that
// image-versus-source drift is visible from the outside without reading
// logs. The audit found a deployed analytics image that predated the
// view cap: source and binary disagreed on a money rule and nothing
// said so. /healthz now reports the commit the binary was built from.
//
// SHA is set at link time:
//
//	go build -ldflags "-X github.com/atpost/analytics-service/internal/buildinfo.SHA=$(git rev-parse HEAD)" ./cmd/server
//
// and in the Dockerfile via a BUILD_SHA build argument. A binary built
// without it reports "unknown", which is itself the signal: nothing
// built by the release path should ever say that.
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
// same two routes, with /healthz carrying build_sha. main.go swaps the
// one call for this one.
func RegisterHealthRoutes(r *gin.Engine, checker *health.Checker) {
	r.GET("/healthz", Healthz())
	r.GET("/readyz", checker.ReadinessHandler())
}
