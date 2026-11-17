// Package health serves the Kubernetes probe routes for the identity
// services: GET /livez (liveness) and GET /readyz (readiness). These are the
// paths the Helm chart probes (charts/atpost-service/values.yaml), with no
// headers.
//
// It is the identity-platform counterpart of Architecture/shared/health,
// which this Go workspace cannot import.
package health

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	LivezPath  = "/livez"
	ReadyzPath = "/readyz"

	// checkTimeout bounds one readiness run. It does not come from the
	// request: a probe must not inherit a deadline some middleware set.
	checkTimeout = 2 * time.Second
)

// CheckFunc reports whether one dependency answers.
type CheckFunc func(ctx context.Context) error

// Checker holds the dependency checks /readyz runs.
type Checker struct {
	service string
	log     *slog.Logger

	mu     sync.RWMutex
	checks map[string]CheckFunc
}

// New returns a Checker with no checks: /readyz answers 200 until one is
// registered. A nil logger means slog.Default().
func New(service string, log *slog.Logger) *Checker {
	if log == nil {
		log = slog.Default()
	}
	return &Checker{service: service, log: log, checks: map[string]CheckFunc{}}
}

// Register adds a dependency the service cannot work without. Register only
// those: a failing check takes the pod out of the Service.
func (c *Checker) Register(name string, fn CheckFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checks[name] = fn
}

// Result is the /readyz body. It names each check and says up or down; the
// error text goes to the log only, since the route is unauthenticated and a
// driver error can carry host and user names.
type Result struct {
	Status  string            `json:"status"`
	Service string            `json:"service"`
	Checks  map[string]string `json:"checks,omitempty"`
}

// Run executes every check concurrently and reports whether all passed.
func (c *Checker) Run(ctx context.Context) (Result, bool) {
	c.mu.RLock()
	checks := make(map[string]CheckFunc, len(c.checks))
	for name, fn := range c.checks {
		checks[name] = fn
	}
	c.mu.RUnlock()

	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	errs := make(map[string]error, len(checks))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for name, fn := range checks {
		wg.Add(1)
		go func(name string, fn CheckFunc) {
			defer wg.Done()
			err := fn(ctx)
			mu.Lock()
			errs[name] = err
			mu.Unlock()
		}(name, fn)
	}
	wg.Wait()

	res := Result{Status: "ready", Service: c.service, Checks: make(map[string]string, len(errs))}
	ok := true
	names := make([]string, 0, len(errs))
	for name := range errs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := errs[name]; err != nil {
			ok = false
			res.Checks[name] = "down"
			c.log.Warn("readiness check failed", "check", name, "err", err)
			continue
		}
		res.Checks[name] = "up"
	}
	if !ok {
		res.Status = "not_ready"
	}
	return res, ok
}

// Livez answers 200 while the process can serve HTTP. It must never run a
// dependency check: one database outage would make Kubernetes restart every
// pod.
func Livez(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "alive"})
}

// Readyz answers 200 when every registered check passes and 503 otherwise.
func (c *Checker) Readyz(gc *gin.Context) {
	// WithoutCancel: keep the request's values, drop any deadline set on it.
	res, ok := c.Run(context.WithoutCancel(gc.Request.Context()))
	status := http.StatusOK
	if !ok {
		status = http.StatusServiceUnavailable
	}
	gc.JSON(status, res)
}

// RegisterRoutes adds GET /livez and GET /readyz to the engine.
//
// The kubelet sends no credentials, so the two routes must carry no
// middleware at all. Gin gives a route the middleware registered before it,
// so the routes go on a group whose chain is emptied: an r.Use before this
// call cannot gate them, and one after it never applies to a route that
// already exists.
func (c *Checker) RegisterRoutes(r *gin.Engine) {
	bare := Bare(r)
	bare.GET(LivezPath, Livez)
	bare.GET(ReadyzPath, c.Readyz)
}

// Bare returns a route group on the engine that carries none of the
// engine-wide middleware, whatever was registered before the call.
func Bare(r *gin.Engine) *gin.RouterGroup {
	g := r.Group("")
	g.Handlers = nil
	return g
}
