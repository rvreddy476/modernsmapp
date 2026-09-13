package routing

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Every test here talks to an httptest fake. Nothing calls the real API.

const testKey = "AIzaTEST-not-a-real-key-7f3c"

var (
	blr    = LatLng{Lat: 12.9716, Lng: 77.5946}
	indira = LatLng{Lat: 12.9784, Lng: 77.6408}
)

// seenRequest is what the fake server received.
type seenRequest struct {
	method, path, rawQuery, requestURI string
	header                             http.Header
	body                               []byte
}

type fakeRoutes struct {
	*httptest.Server
	mu   sync.Mutex
	seen []seenRequest
}

func newFakeRoutes(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *fakeRoutes {
	t.Helper()
	f := &fakeRoutes{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.seen = append(f.seen, seenRequest{r.Method, r.URL.Path, r.URL.RawQuery, r.RequestURI, r.Header.Clone(), body})
		f.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeRoutes) requests() []seenRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]seenRequest(nil), f.seen...)
}

func answer(status int, body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

func googleFor(f *fakeRoutes, timeout time.Duration) *GoogleRoutes {
	return NewGoogleRoutes(testKey, GoogleOptions{Endpoint: f.URL + "/directions/v2:computeRoutes", Timeout: timeout})
}

func TestGoogleRoutes_SendsTheDocumentedRequestAndReadsTheRoute(t *testing.T) {
	f := newFakeRoutes(t, answer(http.StatusOK, `{"routes":[{"distanceMeters":6120,"duration":"845s"}]}`))
	r, err := googleFor(f, time.Second).Route(context.Background(), blr, indira)
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if r != (Route{DistanceMeters: 6120, Duration: 845 * time.Second, Source: SourceGoogle}) {
		t.Fatalf("route = %+v", r)
	}

	reqs := f.requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d", len(reqs))
	}
	got := reqs[0]
	if got.method != http.MethodPost || got.path != "/directions/v2:computeRoutes" {
		t.Fatalf("%s %s", got.method, got.path)
	}
	if got.header.Get("X-Goog-Api-Key") != testKey {
		t.Fatalf("X-Goog-Api-Key = %q", got.header.Get("X-Goog-Api-Key"))
	}
	if got.header.Get("X-Goog-FieldMask") != "routes.duration,routes.distanceMeters" {
		t.Fatalf("X-Goog-FieldMask = %q", got.header.Get("X-Goog-FieldMask"))
	}
	if got.header.Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q", got.header.Get("Content-Type"))
	}
	var body, want any
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(`{
		"origin":{"location":{"latLng":{"latitude":12.9716,"longitude":77.5946}}},
		"destination":{"location":{"latLng":{"latitude":12.9784,"longitude":77.6408}}},
		"travelMode":"TWO_WHEELER",
		"routingPreference":"TRAFFIC_AWARE"
	}`), &want)
	gotJSON, _ := json.Marshal(body)
	wantJSON, _ := json.Marshal(want)
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("body = %s\nwant   %s", gotJSON, wantJSON)
	}
}

func TestGoogleRoutes_FractionalAndZeroDurations(t *testing.T) {
	for body, want := range map[string]Route{
		`{"routes":[{"distanceMeters":12,"duration":"3.5s"}]}`: {12, 3500 * time.Millisecond, SourceGoogle},
		`{"routes":[{"duration":"0s"}]}`:                       {0, 0, SourceGoogle},
	} {
		f := newFakeRoutes(t, answer(http.StatusOK, body))
		r, err := googleFor(f, time.Second).Route(context.Background(), blr, blr)
		if err != nil || r != want {
			t.Fatalf("%s: %+v %v", body, r, err)
		}
	}
}

func TestGoogleRoutes_ClassifiesEveryFailure(t *testing.T) {
	cases := []struct {
		name, class string
		handler     func(http.ResponseWriter, *http.Request)
	}{
		{"403", FailureHTTPStatus, answer(http.StatusForbidden, `{"error":{"code":403,"message":"The caller does not have permission","status":"PERMISSION_DENIED"}}`)},
		{"500", FailureHTTPStatus, answer(http.StatusInternalServerError, `oops`)},
		{"302", FailureHTTPStatus, answer(http.StatusNotModified, ``)},
		{"empty object", FailureEmptyRoute, answer(http.StatusOK, `{}`)},
		{"empty routes", FailureEmptyRoute, answer(http.StatusOK, `{"routes":[]}`)},
		{"route without duration", FailureEmptyRoute, answer(http.StatusOK, `{"routes":[{"distanceMeters":10}]}`)},
		{"not json", FailureDecode, answer(http.StatusOK, `<html>`)},
		{"duration not seconds", FailureDecode, answer(http.StatusOK, `{"routes":[{"distanceMeters":10,"duration":"5m"}]}`)},
		{"negative duration", FailureDecode, answer(http.StatusOK, `{"routes":[{"distanceMeters":10,"duration":"-5s"}]}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeRoutes(t, tc.handler)
			_, err := googleFor(f, time.Second).Route(context.Background(), blr, indira)
			if ClassOf(err) != tc.class {
				t.Fatalf("class = %s (%v), want %s", ClassOf(err), err, tc.class)
			}
		})
	}

	t.Run("transport", func(t *testing.T) {
		f := newFakeRoutes(t, answer(http.StatusOK, `{}`))
		g := googleFor(f, time.Second)
		f.Close()
		if _, err := g.Route(context.Background(), blr, indira); ClassOf(err) != FailureTransport {
			t.Fatalf("closed server: %v", err)
		}
	})
	t.Run("caller canceled", func(t *testing.T) {
		f := newFakeRoutes(t, answer(http.StatusOK, `{}`))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := googleFor(f, time.Second).Route(ctx, blr, indira); ClassOf(err) != FailureCanceled {
			t.Fatalf("canceled: %v", err)
		}
	})
	t.Run("bad coordinates never call out", func(t *testing.T) {
		f := newFakeRoutes(t, answer(http.StatusOK, `{}`))
		if _, err := googleFor(f, time.Second).Route(context.Background(), LatLng{Lat: 91}, indira); ClassOf(err) != FailureRequest {
			t.Fatalf("bad coordinates: %v", err)
		}
		if len(f.requests()) != 0 {
			t.Fatal("bad coordinates reached the API")
		}
	})
}

// hangUntilCanceled answers only when the client gives up (or after 5 s).
func hangUntilCanceled(w http.ResponseWriter, r *http.Request) {
	select {
	case <-r.Context().Done():
	case <-time.After(5 * time.Second):
		answer(http.StatusOK, `{"routes":[{"distanceMeters":1,"duration":"1s"}]}`)(w, r)
	}
}

// The configured timeout bounds the whole call: a server that never answers
// costs the caller the timeout, not the server's patience.
func TestGoogleRoutes_TimeoutIsApplied(t *testing.T) {
	f := newFakeRoutes(t, hangUntilCanceled)
	start := time.Now()
	_, err := googleFor(f, 150*time.Millisecond).Route(context.Background(), blr, indira)
	elapsed := time.Since(start)
	if ClassOf(err) != FailureTimeout {
		t.Fatalf("class = %s (%v), want timeout", ClassOf(err), err)
	}
	if elapsed > time.Second {
		t.Fatalf("call took %v with a 150ms timeout", elapsed)
	}
}

func TestNewGoogleRoutes_DefaultsToTwoSecondsAndTheRealEndpoint(t *testing.T) {
	g := NewGoogleRoutes(testKey, GoogleOptions{})
	if g.timeout != 2*time.Second || g.endpoint != "https://routes.googleapis.com/directions/v2:computeRoutes" {
		t.Fatalf("timeout %v endpoint %s", g.timeout, g.endpoint)
	}
}

// The key rides only in the header: never in the URL the server sees, never in
// an error, never in any log line, on success and on every failure path.
func TestGoogleRoutes_KeyNeverInURLOrAnyLogLine(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// A hostile or buggy upstream that echoes the key back in its error body.
	echo := func(w http.ResponseWriter, r *http.Request) {
		answer(http.StatusBadRequest, `{"error":{"status":"INVALID_ARGUMENT","message":"bad key `+r.Header.Get("X-Goog-Api-Key")+`"}}`)(w, r)
	}
	servers := map[string]*fakeRoutes{
		"ok":      newFakeRoutes(t, answer(http.StatusOK, `{"routes":[{"distanceMeters":6120,"duration":"845s"}]}`)),
		"echo":    newFakeRoutes(t, echo),
		"empty":   newFakeRoutes(t, answer(http.StatusOK, `{}`)),
		"timeout": newFakeRoutes(t, hangUntilCanceled),
	}
	closed := newFakeRoutes(t, answer(http.StatusOK, `{}`))
	closedURL := closed.URL
	closed.Close()

	var errs []string
	run := func(endpoint string) {
		g := NewGoogleRoutes(testKey, GoogleOptions{Endpoint: endpoint, Timeout: 150 * time.Millisecond})
		if _, err := g.Route(context.Background(), blr, indira); err != nil {
			errs = append(errs, err.Error())
		}
		fb := NewFallback(g, Haversine{}, logger)
		if _, err := fb.Route(context.Background(), blr, indira); err != nil {
			t.Fatalf("fallback failed the caller: %v", err)
		}
	}
	for _, f := range servers {
		run(f.URL + "/directions/v2:computeRoutes")
	}
	run(closedURL + "/directions/v2:computeRoutes")

	for name, f := range servers {
		reqs := f.requests()
		if len(reqs) == 0 {
			t.Fatalf("%s: no request reached the fake", name)
		}
		for _, r := range reqs {
			if r.rawQuery != "" || strings.Contains(r.requestURI, testKey) || strings.Contains(r.path, testKey) {
				t.Fatalf("%s: key or query in the URL: %q", name, r.requestURI)
			}
			if r.header.Get("X-Goog-Api-Key") != testKey {
				t.Fatalf("%s: key header missing", name)
			}
		}
	}
	if len(errs) < 4 {
		t.Fatalf("expected failures on echo, empty, timeout and closed; got %v", errs)
	}
	for _, e := range errs {
		if strings.Contains(e, testKey) {
			t.Fatalf("error carries the key: %s", e)
		}
	}
	if logs.Len() == 0 {
		t.Fatal("fallback logged nothing; the log assertion would prove nothing")
	}
	if strings.Contains(logs.String(), testKey) {
		t.Fatalf("a log line carries the key:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "[redacted]") {
		t.Fatalf("the echoed key was not visibly redacted:\n%s", logs.String())
	}
}
