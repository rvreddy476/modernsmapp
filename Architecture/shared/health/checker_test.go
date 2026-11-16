package health

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func newProbeRouter(t *testing.T, c *Checker) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	c.RegisterRoutes(r)
	return r
}

func probe(r http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// Liveness must say only "the process is up". If it followed a dependency,
// one database outage would make the kubelet restart every pod at once.
func TestLivenessProbesAnswer200EvenWhenADependencyIsDown(t *testing.T) {
	c := New("probe-test")
	called := false
	c.Register("postgres", func(context.Context) error {
		called = true
		return errors.New("connection refused")
	})
	r := newProbeRouter(t, c)

	for _, path := range []string{"/livez", "/healthz"} {
		rec := probe(r, path)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, rec.Code)
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["status"] != "alive" {
			t.Errorf("GET %s body = %q, want status alive", path, rec.Body.String())
		}
	}
	if called {
		t.Error("a liveness probe ran a dependency check")
	}

	// The same failing dependency does take the pod out of rotation.
	if rec := probe(r, "/readyz"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("GET /readyz with a dependency down = %d, want 503", rec.Code)
	}
}

func TestReadinessAnswers200WhenDependenciesAreUp(t *testing.T) {
	c := New("probe-test")
	c.Register("postgres", func(context.Context) error { return nil })
	r := newProbeRouter(t, c)

	if rec := probe(r, "/readyz"); rec.Code != http.StatusOK {
		t.Errorf("GET /readyz = %d, want 200", rec.Code)
	}
}

// chartProbePaths reads livenessProbe.path and readinessProbe.path from the
// chart's default values. It is a line scan, not a YAML parse: the two blocks
// are top-level keys with an indented "path:" line, and the test fails
// loudly if that shape ever changes.
func chartProbePaths(t *testing.T, valuesFile string) map[string]string {
	t.Helper()
	f, err := os.Open(valuesFile)
	if err != nil {
		t.Fatalf("open chart values: %v", err)
	}
	defer f.Close()

	paths := map[string]string{}
	block := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") {
			block = strings.TrimSuffix(strings.TrimSpace(line), ":")
			continue
		}
		if block != "livenessProbe" && block != "readinessProbe" {
			continue
		}
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "path:"); ok {
			v = strings.TrimSpace(v)
			if i := strings.Index(v, " #"); i >= 0 {
				v = strings.TrimSpace(v[:i])
			}
			paths[block] = strings.Trim(v, `"'`)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read chart values: %v", err)
	}
	return paths
}

// The Helm chart probes every Go service on the paths in its default values.
// A path this package does not register means a restart loop (liveness) or a
// pod that never receives traffic (readiness) on Kubernetes.
func TestChartDefaultProbePathsAreRegistered(t *testing.T) {
	valuesFile := filepath.Join("..", "..", "..", "charts", "atpost-service", "values.yaml")
	if _, err := os.Stat(valuesFile); errors.Is(err, os.ErrNotExist) {
		t.Skipf("%s not present (module checked out without the chart)", valuesFile)
	}
	paths := chartProbePaths(t, valuesFile)

	c := New("probe-test")
	registered := map[string]bool{}
	for _, route := range newProbeRouter(t, c).Routes() {
		if route.Method == http.MethodGet {
			registered[route.Path] = true
		}
	}

	for _, block := range []string{"livenessProbe", "readinessProbe"} {
		path := paths[block]
		if path == "" {
			t.Errorf("no %s.path found in %s; the chart layout changed, update this test", block, valuesFile)
			continue
		}
		if !registered[path] {
			t.Errorf("chart %s.path is %s, which shared/health does not register", block, path)
		}
	}
}
