package routing

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// Every test here talks to an httptest fake. Nothing calls the real API.

const testKey = "AIzaTEST-not-a-real-key-7f3c"

var (
	blr    = LatLng{Lat: 12.9716, Lng: 77.5946}
	indira = LatLng{Lat: 12.9784, Lng: 77.6408}
)

type fakeRoutes struct {
	*httptest.Server
	mu     sync.Mutex
	bodies []string
	heads  []http.Header
	uris   []string
}

func newFakeRoutes(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *fakeRoutes {
	t.Helper()
	f := &fakeRoutes{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.bodies = append(f.bodies, string(body))
		f.heads = append(f.heads, r.Header.Clone())
		f.uris = append(f.uris, r.RequestURI)
		f.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

func answer(status int, body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func googleFor(f *fakeRoutes, timeout time.Duration) *GoogleRoutes {
	return NewGoogleRoutes(testKey, GoogleOptions{Endpoint: f.URL, Timeout: timeout})
}

func TestDeterministicCalculator(t *testing.T) {
	calc := NewDeterministicCalculator(1.25, 22.0)
	res, err := calc.CalculateRoute(context.Background(), 12.9716, 77.5946, 12.9352, 77.6245)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.DistanceMeters <= 0 || res.DurationSeconds <= 0 {
		t.Errorf("expected positive route, got %+v", res)
	}
	if res.ProviderVersion != VersionDeterministic {
		t.Errorf("expected %s, got %s", VersionDeterministic, res.ProviderVersion)
	}
}

func TestRouting_InvalidCoordinates(t *testing.T) {
	calc := NewDeterministicCalculator(1.25, 22.0)
	if _, err := calc.CalculateRoute(context.Background(), 999.0, 77.5946, 12.9352, 77.6245); !errors.Is(err, ErrInvalidCoordinates) {
		t.Errorf("expected ErrInvalidCoordinates, got %v", err)
	}
	chain := CalculatorFromRouter(NewFallback(nil, Haversine{}, slog.Default()))
	if _, err := chain.CalculateRoute(context.Background(), 0, 0, 12.9352, 77.6245); !errors.Is(err, ErrInvalidCoordinates) {
		t.Errorf("chain: expected ErrInvalidCoordinates, got %v", err)
	}
}

func TestGoogleRoutes_SendsTheDocumentedRequestAndReadsTheRoute(t *testing.T) {
	f := newFakeRoutes(t, answer(200, `{"routes":[{"distanceMeters":6420,"duration":"1140s"}]}`))
	r, err := googleFor(f, 0).Route(context.Background(), blr, indira)
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if r.DistanceMeters != 6420 || r.Duration != 1140*time.Second || r.Source != SourceGoogle {
		t.Fatalf("route = %+v", r)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	body, head, uri := f.bodies[0], f.heads[0], f.uris[0]
	for _, want := range []string{`"travelMode":"TWO_WHEELER"`, `"routingPreference":"TRAFFIC_AWARE"`, `"latitude":12.9716`, `"longitude":77.6408`} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %s: %s", want, body)
		}
	}
	if head.Get("X-Goog-Api-Key") != testKey || head.Get("X-Goog-FieldMask") != RoutesFieldMask {
		t.Errorf("headers %v", head)
	}
	if strings.Contains(uri, testKey) {
		t.Errorf("key in the URL: %s", uri)
	}
}

func TestGoogleRoutes_ClassifiesEveryFailure(t *testing.T) {
	cases := []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request)
		class   string
	}{
		{"http 403", answer(403, `{"error":{"status":"PERMISSION_DENIED","message":"key `+testKey+` is bad"}}`), FailureHTTPStatus},
		{"decode", answer(200, `not json`), FailureDecode},
		{"empty", answer(200, `{}`), FailureEmptyRoute},
		{"bad duration", answer(200, `{"routes":[{"distanceMeters":1,"duration":"1h"}]}`), FailureDecode},
	}
	for _, c := range cases {
		f := newFakeRoutes(t, c.handler)
		_, err := googleFor(f, 0).Route(context.Background(), blr, indira)
		if ClassOf(err) != c.class {
			t.Errorf("%s: class %s (%v), want %s", c.name, ClassOf(err), err, c.class)
		}
		if err != nil && strings.Contains(err.Error(), testKey) {
			t.Errorf("%s: key leaked into the error: %v", c.name, err)
		}
	}
	f := newFakeRoutes(t, func(w http.ResponseWriter, _ *http.Request) { time.Sleep(300 * time.Millisecond) })
	if _, err := googleFor(f, 50*time.Millisecond).Route(context.Background(), blr, indira); ClassOf(err) != FailureTimeout {
		t.Errorf("timeout: class %s (%v)", ClassOf(err), err)
	}
	g := NewGoogleRoutes(testKey, GoogleOptions{Endpoint: "http://127.0.0.1:1"})
	if _, err := g.Route(context.Background(), blr, indira); ClassOf(err) != FailureTransport {
		t.Errorf("transport: class %s (%v)", ClassOf(err), err)
	}
	if _, err := g.Route(context.Background(), LatLng{999, 0}, indira); ClassOf(err) != FailureRequest {
		t.Errorf("request: %v", err)
	}
	if g := NewGoogleRoutes(testKey, GoogleOptions{}); g.timeout != 2*time.Second || g.endpoint != DefaultRoutesEndpoint {
		t.Errorf("defaults %v %s", g.timeout, g.endpoint)
	}
}

func TestFallback_HaversineOnEveryGoogleFailureAndWithoutAKey(t *testing.T) {
	f := newFakeRoutes(t, answer(500, `{}`))
	fb := NewFallback(googleFor(f, 0), Haversine{}, slog.Default())
	r, err := fb.Route(context.Background(), blr, indira)
	if err != nil || r.Source != SourceHaversine || r.DistanceMeters <= 0 {
		t.Fatalf("fallback: %+v %v", r, err)
	}
	if fb.Failures()[FailureHTTPStatus] != 1 {
		t.Fatalf("failures %v", fb.Failures())
	}
	noKey := NewFallback(nil, Haversine{}, nil)
	if r, err := noKey.Route(context.Background(), blr, indira); err != nil || r.Source != SourceHaversine {
		t.Fatalf("no key: %+v %v", r, err)
	}
	ok := newFakeRoutes(t, answer(200, `{"routes":[{"distanceMeters":6420,"duration":"1140s"}]}`))
	if r, _ := NewFallback(googleFor(ok, 0), Haversine{}, nil).Route(context.Background(), blr, indira); r.Source != SourceGoogle {
		t.Fatalf("google answer must pass through: %+v", r)
	}
}

func TestCache_ServesGoogleAnswersOnlyAndSurvivesRedisDown(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	calls := 0
	f := newFakeRoutes(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		answer(200, `{"routes":[{"distanceMeters":6420,"duration":"1140s"}]}`)(w, r)
	})
	at := time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)
	c := NewCache(rdb, googleFor(f, 0), nil).(*Cache).WithClock(func() time.Time { return at })
	for i := 0; i < 3; i++ {
		r, err := c.Route(context.Background(), blr, indira)
		if err != nil || r.Source != SourceGoogle || r.DistanceMeters != 6420 {
			t.Fatalf("call %d: %+v %v", i, r, err)
		}
	}
	if calls != 1 {
		t.Fatalf("google called %d times, want 1", calls)
	}
	if !mr.Exists(CacheKey(blr, indira, at)) {
		t.Fatal("key missing")
	}
	// A haversine answer is never stored.
	hv := NewCache(rdb, NewFallback(nil, Haversine{}, nil), nil).(*Cache).WithClock(func() time.Time { return at })
	if _, err := hv.Route(context.Background(), blr, LatLng{12.99, 77.70}); err != nil {
		t.Fatal(err)
	}
	if mr.Exists(CacheKey(blr, LatLng{12.99, 77.70}, at)) {
		t.Fatal("haversine answer cached")
	}
	// Redis down: pass through.
	mr.Close()
	if r, err := c.Route(context.Background(), blr, indira); err != nil || r.Source != SourceGoogle {
		t.Fatalf("redis down: %+v %v", r, err)
	}
	if NewCache(nil, Haversine{}, nil) == nil {
		t.Fatal("nil redis must return next")
	}
}

func TestCalculatorFromRouter_ReportsTheSource(t *testing.T) {
	f := newFakeRoutes(t, answer(200, `{"routes":[{"distanceMeters":6420,"duration":"1140s"}]}`))
	calc := CalculatorFromRouter(NewFallback(googleFor(f, 0), Haversine{}, nil))
	res, err := calc.CalculateRoute(context.Background(), blr.Lat, blr.Lng, indira.Lat, indira.Lng)
	if err != nil {
		t.Fatal(err)
	}
	if res.ProviderVersion != VersionGoogle || res.DistanceMeters != 6420 || res.DurationSeconds != 1140 || res.DistanceKM != 6.42 {
		t.Fatalf("%+v", res)
	}
	hv := CalculatorFromRouter(NewFallback(nil, Haversine{}, nil))
	if res, _ := hv.CalculateRoute(context.Background(), blr.Lat, blr.Lng, indira.Lat, indira.Lng); res.ProviderVersion != VersionHaversine {
		t.Fatalf("%+v", res)
	}
}

func TestConfigFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	cfg, err := ConfigFromEnv(env(map[string]string{}))
	if err != nil || cfg.GoogleKey != "" || cfg.Timeout != DefaultTimeout {
		t.Fatalf("defaults %+v %v", cfg, err)
	}
	cfg, err = ConfigFromEnv(env(map[string]string{"GOOGLE_MAPS_SERVER_KEY": "k", "MOPEDU_ROUTING_TIMEOUT_MS": "1500"}))
	if err != nil || cfg.GoogleKey != "k" || cfg.Timeout != 1500*time.Millisecond {
		t.Fatalf("set %+v %v", cfg, err)
	}
	for _, bad := range []string{"0", "abc", "20000"} {
		if _, err := ConfigFromEnv(env(map[string]string{"MOPEDU_ROUTING_TIMEOUT_MS": bad})); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
