package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// gatedJWKS serves the test Google key; while gate is non-nil each request
// waits for it to close. hits counts requests.
type gatedJWKS struct {
	srv  *httptest.Server
	hits atomic.Int32
	mu   sync.Mutex
	gate chan struct{}
}

func newGatedJWKS(t *testing.T) *gatedJWKS {
	t.Helper()
	key, _ := testKeys()
	body, _ := json.Marshal(map[string]any{"keys": []map[string]string{{
		"kid": testGoogleKID, "kty": "RSA",
		"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}}})
	g := &gatedJWKS{}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		g.hits.Add(1)
		g.mu.Lock()
		gate := g.gate
		g.mu.Unlock()
		if gate != nil {
			<-gate
		}
		w.Header().Set("Cache-Control", "max-age=3600")
		_, _ = w.Write(body)
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *gatedJWKS) hold() chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.gate = make(chan struct{})
	return g.gate
}

// fakeClock is a settable clock for the key cache.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// The cache lock used to be held across the HTTP fetch: while one sign-in
// waited on a slow Google, every other Google sign-in waited too, even those
// the cache could answer.
func TestGoogleKeyFetchDoesNotBlockCachedLookups(t *testing.T) {
	jwks := newGatedJWKS(t)
	clock := &fakeClock{t: time.Now()}
	ks := &googleKeySet{url: jwks.srv.URL, client: jwks.srv.Client(), now: clock.now}
	ctx := context.Background()
	if _, err := ks.key(ctx, testGoogleKID); err != nil {
		t.Fatalf("prime: %v", err)
	}

	// An unknown kid after the refetch gap starts a fetch that hangs.
	clock.add(googleRefetchMinGap + time.Second)
	release := jwks.hold()
	defer close(release)
	go func() { _, _ = ks.key(ctx, "rotated-kid") }()
	for jwks.hits.Load() < 2 {
		time.Sleep(time.Millisecond)
	}

	got := make(chan error, 1)
	go func() {
		_, err := ks.key(ctx, testGoogleKID)
		got <- err
	}()
	select {
	case err := <-got:
		if err != nil {
			t.Fatalf("cached key lookup failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a cached key lookup waited on another caller's fetch")
	}
}

// Callers that all need a refresh share one request.
func TestGoogleKeyRefreshIsSingleFlight(t *testing.T) {
	jwks := newGatedJWKS(t)
	ks := &googleKeySet{url: jwks.srv.URL, client: jwks.srv.Client(), now: time.Now}
	release := jwks.hold()
	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := ks.key(context.Background(), testGoogleKID)
			errs <- err
		}()
	}
	for jwks.hits.Load() < 1 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // let the others join the flight
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("key lookup: %v", err)
		}
	}
	if hits := jwks.hits.Load(); hits != 1 {
		t.Fatalf("%d concurrent cold lookups made %d fetches, want 1", n, hits)
	}
}

// A caller that gives up stops waiting; the shared fetch is not cancelled
// with it, and its result serves the next caller.
func TestGoogleKeyWaitHonoursTheCallersContext(t *testing.T) {
	jwks := newGatedJWKS(t)
	ks := &googleKeySet{url: jwks.srv.URL, client: jwks.srv.Client(), now: time.Now}
	release := jwks.hold()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := ks.key(ctx, testGoogleKID); err == nil {
		t.Fatal("a lookup outlived its context")
	}
	close(release)
	if _, err := ks.key(context.Background(), testGoogleKID); err != nil {
		t.Fatalf("lookup after the shared fetch finished: %v", err)
	}
	if hits := jwks.hits.Load(); hits != 1 {
		t.Fatalf("%d fetches, want the abandoned one to have been reused", hits)
	}
}
