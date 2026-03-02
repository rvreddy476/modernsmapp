package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// Status represents the health state of a dependency.
type Status string

const (
	StatusUp   Status = "up"
	StatusDown Status = "down"
)

// CheckFunc is a function that checks a single dependency.
type CheckFunc func(ctx context.Context) error

// Checker holds registered health checks and executes them.
type Checker struct {
	serviceName string
	checks      map[string]CheckFunc
	mu          sync.RWMutex
}

// New creates a Checker for the given service.
func New(serviceName string) *Checker {
	return &Checker{
		serviceName: serviceName,
		checks:      make(map[string]CheckFunc),
	}
}

// Register adds a named health check function.
func (c *Checker) Register(name string, fn CheckFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checks[name] = fn
}

// Result is the JSON response for a health check.
type Result struct {
	Status       Status               `json:"status"`
	Service      string               `json:"service"`
	Timestamp    time.Time            `json:"timestamp"`
	Dependencies map[string]DepResult `json:"dependencies,omitempty"`
}

// DepResult is the health status of a single dependency.
type DepResult struct {
	Status  Status `json:"status"`
	Latency string `json:"latency"`
	Error   string `json:"error,omitempty"`
}

// Run executes all health checks concurrently with a 3-second timeout.
func (c *Checker) Run(ctx context.Context) Result {
	c.mu.RLock()
	defer c.mu.RUnlock()

	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	deps := make(map[string]DepResult, len(c.checks))
	var mu sync.Mutex
	var wg sync.WaitGroup

	overallStatus := StatusUp

	for name, fn := range c.checks {
		wg.Add(1)
		go func(name string, fn CheckFunc) {
			defer wg.Done()
			start := time.Now()
			err := fn(checkCtx)
			latency := time.Since(start)

			dr := DepResult{
				Status:  StatusUp,
				Latency: latency.String(),
			}
			if err != nil {
				dr.Status = StatusDown
				dr.Error = err.Error()
			}

			mu.Lock()
			deps[name] = dr
			if dr.Status == StatusDown {
				overallStatus = StatusDown
			}
			mu.Unlock()
		}(name, fn)
	}

	wg.Wait()

	return Result{
		Status:       overallStatus,
		Service:      c.serviceName,
		Timestamp:    time.Now().UTC(),
		Dependencies: deps,
	}
}

// LivenessHandler returns a simple /healthz handler (always 200 if process is alive).
func LivenessHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "alive"})
	}
}

// ReadinessHandler returns a /readyz handler that runs all registered checks.
func (c *Checker) ReadinessHandler() gin.HandlerFunc {
	return func(gc *gin.Context) {
		result := c.Run(gc.Request.Context())

		status := http.StatusOK
		if result.Status != StatusUp {
			status = http.StatusServiceUnavailable
		}

		gc.Header("Content-Type", "application/json")
		gc.Status(status)
		json.NewEncoder(gc.Writer).Encode(result)
	}
}

// RegisterRoutes adds /healthz (liveness) and /readyz (readiness) to the router.
func (c *Checker) RegisterRoutes(r *gin.Engine) {
	r.GET("/healthz", LivenessHandler())
	r.GET("/readyz", c.ReadinessHandler())
}
