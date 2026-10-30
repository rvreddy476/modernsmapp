package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atpost/analytics-service/internal/store/scylla"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func testCache(ttl time.Duration, max int) (*chartCache, *fakeClock) {
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	c := newChartCache(ttl, max)
	c.now = clock.now
	return c, clock
}

// Audit M9: a chart is scanned once per TTL, not once per request.
func TestChartCacheServesWithinTheTTL(t *testing.T) {
	c, clock := testCache(time.Minute, 10)
	loads := 0
	load := func(context.Context) (any, bool, error) { loads++; return loads, true, nil }

	v, sampled, err := c.get(context.Background(), "k", load)
	if err != nil || v != 1 || !sampled {
		t.Fatalf("first get = %v %v %v", v, sampled, err)
	}
	clock.t = clock.t.Add(59 * time.Second)
	v, sampled, _ = c.get(context.Background(), "k", load)
	if v != 1 || !sampled || loads != 1 {
		t.Fatalf("within the TTL: v=%v sampled=%v loads=%d, want the cached 1 and one load", v, sampled, loads)
	}
	clock.t = clock.t.Add(time.Second)
	v, _, _ = c.get(context.Background(), "k", load)
	if v != 2 || loads != 2 {
		t.Fatalf("after the TTL: v=%v loads=%d, want a fresh load", v, loads)
	}
	// Keys are independent.
	if v, _, _ := c.get(context.Background(), "other", load); v != 3 {
		t.Fatalf("another key served %v", v)
	}
}

func TestChartCacheNeverCachesAnError(t *testing.T) {
	c, _ := testCache(time.Minute, 10)
	fail := true
	loads := 0
	load := func(context.Context) (any, bool, error) {
		loads++
		if fail {
			return nil, false, errors.New("scylla timeout")
		}
		return "ok", false, nil
	}
	if _, _, err := c.get(context.Background(), "k", load); err == nil {
		t.Fatal("the error was swallowed")
	}
	fail = false
	if v, _, err := c.get(context.Background(), "k", load); err != nil || v != "ok" || loads != 2 {
		t.Fatalf("after a failure: v=%v err=%v loads=%d, want a retry", v, err, loads)
	}
}

// Concurrent misses for one key share a single scan.
func TestChartCacheCollapsesConcurrentMisses(t *testing.T) {
	c, _ := testCache(time.Minute, 10)
	var loads atomic.Int32
	release := make(chan struct{})
	load := func(context.Context) (any, bool, error) {
		loads.Add(1)
		<-release
		return "curve", false, nil
	}
	var wg sync.WaitGroup
	results := make(chan any, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, _, _ := c.get(context.Background(), "k", load)
			results <- v
		}()
	}
	// Let every goroutine reach the cache before the load finishes.
	deadline := time.Now().Add(2 * time.Second)
	for {
		c.mu.Lock()
		waiting := c.inflight["k"] != nil
		c.mu.Unlock()
		if waiting || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	close(results)
	for v := range results {
		if v != "curve" {
			t.Fatalf("a waiter got %v", v)
		}
	}
	if n := loads.Load(); n != 1 {
		t.Fatalf("%d scans for 8 concurrent requests, want 1", n)
	}
}

// The cache is bounded: a full cache drops expired entries, then the one
// closest to expiry.
func TestChartCacheIsBounded(t *testing.T) {
	c, clock := testCache(time.Minute, 2)
	val := func(v any) func(context.Context) (any, bool, error) {
		return func(context.Context) (any, bool, error) { return v, false, nil }
	}
	get := func(key string) {
		t.Helper()
		if _, _, err := c.get(context.Background(), key, val(key)); err != nil {
			t.Fatalf("get %s: %v", key, err)
		}
	}
	get("a")
	clock.t = clock.t.Add(time.Second)
	get("b")
	clock.t = clock.t.Add(time.Second)
	get("c")
	if len(c.entries) != 2 {
		t.Fatalf("entries=%d want 2", len(c.entries))
	}
	if _, ok := c.entries["a"]; ok {
		t.Fatal("the oldest entry survived eviction")
	}
	clock.t = clock.t.Add(2 * time.Minute)
	get("d")
	if len(c.entries) != 1 {
		t.Fatalf("expired entries were not dropped: %d left", len(c.entries))
	}
}

type countingCharts struct {
	retention, demographics atomic.Int32
	sampled                 bool
}

func (f *countingCharts) GetRetentionCurve(_ context.Context, _ uuid.UUID, _ int64, _ int) ([]scylla.RetentionPoint, bool, error) {
	f.retention.Add(1)
	return []scylla.RetentionPoint{{SecondOffset: 0, RetainedRatio: 1, SessionCount: 3}}, f.sampled, nil
}

func (f *countingCharts) GetAudienceDemographics(_ context.Context, _ uuid.UUID, _ int) (*scylla.AudienceDemographics, error) {
	f.demographics.Add(1)
	return &scylla.AudienceDemographics{TotalSessions: 3, Sampled: f.sampled}, nil
}

// The routes go through the cache, behind the ownership gate, and flag a
// sampled chart.
func TestChartRoutesAreCachedBehindTheOwnershipGate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	owner, stranger, content := uuid.New(), uuid.New(), uuid.New()
	charts := &countingCharts{sampled: true}
	h := &DashboardHandler{
		watchStore: charts,
		charts:     newChartCache(time.Minute, 16),
		owns: func(_ context.Context, creatorID, contentID uuid.UUID) (bool, error) {
			return creatorID == owner && contentID == content, nil
		},
	}
	r := gin.New()
	h.RegisterRoutes(r.Group("/v1/analytics"))
	get := func(path string, user uuid.UUID) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-User-Id", user.String())
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	for _, route := range []string{"retention", "demographics"} {
		path := "/v1/analytics/dashboard/content/" + content.String() + "/" + route
		for i := 0; i < 3; i++ {
			w := get(path, owner)
			if w.Code != http.StatusOK {
				t.Fatalf("%s: status %d body %s", route, w.Code, w.Body.String())
			}
			if w.Header().Get(sampledHeader) != "true" {
				t.Fatalf("%s: a sampled chart was not flagged (request %d)", route, i)
			}
		}
		// A cached chart is never served past the gate.
		if w := get(path, stranger); w.Code != http.StatusNotFound {
			t.Fatalf("%s: a stranger got %d from the cache", route, w.Code)
		}
	}
	if charts.retention.Load() != 1 || charts.demographics.Load() != 1 {
		t.Fatalf("scans: retention=%d demographics=%d, want one each for three requests",
			charts.retention.Load(), charts.demographics.Load())
	}

	// An unsampled chart carries no flag.
	charts.sampled = false
	other := uuid.New()
	h.owns = func(context.Context, uuid.UUID, uuid.UUID) (bool, error) { return true, nil }
	if w := get("/v1/analytics/dashboard/content/"+other.String()+"/retention", owner); w.Header().Get(sampledHeader) != "" {
		t.Fatal("an exact chart was flagged as sampled")
	}
}

// A deployment without Scylla passes a nil *scylla.WatchStore; it must
// read as "no store" (503), not as a typed nil that panics on first use.
func TestChartRoutesWithoutScyllaAre503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewDashboardHandler(nil).WithWatchStore(nil)
	h.owns = func(context.Context, uuid.UUID, uuid.UUID) (bool, error) { return true, nil }
	r := gin.New()
	h.RegisterRoutes(r.Group("/v1/analytics"))
	for _, route := range []string{"retention", "demographics"} {
		req := httptest.NewRequest(http.MethodGet, "/v1/analytics/dashboard/content/"+uuid.New().String()+"/"+route, nil)
		req.Header.Set("X-User-Id", uuid.New().String())
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s without scylla: %d", route, w.Code)
		}
	}
}
