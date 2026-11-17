package main

import (
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
)

// installProbesThenInternalKey registers /livez, /healthz and /readyz and only
// then puts the internal service key on the engine.
//
// ORDER MATTERS. Gin fixes a route's middleware chain when the route is
// registered, so the probes declared first stay open and everything declared
// after this call demands X-Internal-Service-Key. The kubelet sends no
// headers (charts/atpost-service/templates/deployment.yaml): with the key in
// front of the probes it got 401 on /livez and /readyz, so in a cluster the
// pod was never ready and was restarted forever.
func installProbesThenInternalKey(r *gin.Engine, checker *health.Checker, internalKey string) {
	checker.RegisterRoutes(r)
	r.Use(middleware.RequireInternalKey(internalKey))
}
