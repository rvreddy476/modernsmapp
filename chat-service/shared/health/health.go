// Package health serves the two Kubernetes probe routes the Helm chart calls
// on every service (charts/atpost-service/values.yaml): GET /livez and
// GET /readyz, with no headers.
//
//   - /livez answers 200 while the process can serve HTTP. It never runs a
//     dependency check: one database outage must not make Kubernetes restart
//     every pod.
//   - /readyz runs the registered dependency checks and answers 503 while any
//     of them fails, so the pod leaves the Service until it can do its job.
//
// The probes are served by Wrap, an outer net/http handler placed in front of
// the service's own router. Nothing registered on that router (auth, the
// internal-key gate, rate limits, request logging, a NoRoute chain) can gate
// them, whatever order it is added in. It mirrors Architecture/shared/health,
// which chat-service (its own Go workspace) cannot import.
package health

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

const (
	// LivezPath and ReadyzPath are the chart's probe paths.
	LivezPath  = "/livez"
	ReadyzPath = "/readyz"

	// checkTimeout bounds one readiness run. The kubelet gives a probe one
	// second by default and counts a timeout as a failure, so this only
	// stops a hung dependency from piling up goroutines.
	checkTimeout = 2 * time.Second

	statusUp   = "up"
	statusDown = "down"
)

// CheckFunc reports whether one dependency answers. It must honour ctx.
type CheckFunc func(ctx context.Context) error

// Checker holds a service's readiness checks.
type Checker struct {
	service string
	log     *slog.Logger

	mu     sync.RWMutex
	checks map[string]CheckFunc
}

// New returns a Checker with no checks: /readyz answers 200 until one is
// registered.
func New(service string, log *slog.Logger) *Checker {
	if log == nil {
		log = slog.Default()
	}
	return &Checker{service: service, log: log, checks: make(map[string]CheckFunc)}
}

// Register adds (or replaces) a named dependency check.
func (c *Checker) Register(name string, fn CheckFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checks[name] = fn
}

// Result is the /readyz body. It names each dependency and whether it is up;
// the error text stays in the log, because ws-gateway's port is reachable
// from the internet and an error can carry a host name.
type Result struct {
	Status       string            `json:"status"`
	Service      string            `json:"service"`
	Dependencies map[string]string `json:"dependencies,omitempty"`
}

// Run executes every check concurrently under checkTimeout.
func (c *Checker) Run(ctx context.Context) Result {
	c.mu.RLock()
	checks := make(map[string]CheckFunc, len(c.checks))
	for name, fn := range c.checks {
		checks[name] = fn
	}
	c.mu.RUnlock()

	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	res := Result{Status: statusUp, Service: c.service}
	if len(checks) == 0 {
		return res
	}
	res.Dependencies = make(map[string]string, len(checks))

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for name, fn := range checks {
		wg.Add(1)
		go func(name string, fn CheckFunc) {
			defer wg.Done()
			err := runCheck(ctx, fn)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				res.Dependencies[name] = statusDown
				res.Status = statusDown
				c.log.Warn("readiness check failed", "dependency", name, "err", err)
				return
			}
			res.Dependencies[name] = statusUp
		}(name, fn)
	}
	wg.Wait()
	return res
}

// runCheck turns a panicking check into a failed one: a probe must never
// take the process down.
func runCheck(ctx context.Context, fn CheckFunc) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = errPanicked
		}
	}()
	return fn(ctx)
}

type checkError string

func (e checkError) Error() string { return string(e) }

const errPanicked = checkError("readiness check panicked")

// Wrap serves GET/HEAD /livez and /readyz itself and hands every other
// request to next untouched. Put it outermost, around the service's router
// and all of its middleware.
func (c *Checker) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case LivezPath:
			if !probeMethod(w, r) {
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"status": "alive"})
		case ReadyzPath:
			if !probeMethod(w, r) {
				return
			}
			res := c.Run(r.Context())
			status := http.StatusOK
			if res.Status != statusUp {
				status = http.StatusServiceUnavailable
			}
			writeJSON(w, status, res)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

func probeMethod(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	w.WriteHeader(http.StatusMethodNotAllowed)
	return false
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
