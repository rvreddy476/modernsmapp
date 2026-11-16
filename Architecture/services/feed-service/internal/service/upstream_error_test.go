package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Finding feed-06: the event consumer retries a failed event until it
// succeeds, so it has to be able to tell a dependency that is down from one
// that refused the request. These pin both the rule and the fact that the
// fan-out's real graph calls carry the status the rule needs.

func TestUpstreamRejected(t *testing.T) {
	status := func(code int) error {
		return &UpstreamStatusError{Service: "graph-service", Status: code, Body: "x"}
	}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain error", errors.New("dial tcp: connection refused"), false},
		{"deadline", context.DeadlineExceeded, false},
		{"400", status(400), true},
		{"413", status(413), true},
		{"414", status(414), true},
		{"422", status(422), true},
		{"401 is a key to fix, then the event succeeds", status(401), false},
		{"403", status(403), false},
		{"404 is a missing route, not a verdict on the event", status(404), false},
		{"408", status(408), false},
		{"429", status(429), false},
		{"500", status(500), false},
		{"502", status(502), false},
		{"503", status(503), false},
		{"wrapped 400", fmt.Errorf("fetch followers at offset 10100: %w", status(400)), true},
		{"wrapped 503", fmt.Errorf("fetch followers at offset 0: %w", status(503)), false},
		{"join of refusals", fmt.Errorf("incomplete: %w", errors.Join(status(400), fmt.Errorf("leg: %w", status(422)))), true},
		{"join with one outage left", fmt.Errorf("incomplete: %w", errors.Join(status(400), status(503))), false},
		{"join with a plain error", errors.Join(status(400), errors.New("3 timeline write(s) failed")), false},
		{"empty join", errors.Join(), false},
	}
	for _, tc := range cases {
		if got := UpstreamRejected(tc.err); got != tc.want {
			t.Errorf("%s: UpstreamRejected = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The text is what these calls returned before the type existed; logs and
// the x-dlq-error header read the same.
func TestUpstreamStatusError_Text(t *testing.T) {
	err := &UpstreamStatusError{Service: "graph-service", Status: 400, Body: "OFFSET_TOO_LARGE"}
	if got, want := err.Error(), "graph-service returned 400: OFFSET_TOO_LARGE"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

// graphStub answers the followers and connections lists with the given
// statuses (200 is an empty page).
func graphStub(t *testing.T, followers, connections int) *Service {
	t.Helper()
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		code := connections
		if strings.Contains(r.URL.Path, "/followers/") {
			code = followers
		}
		if code != http.StatusOK {
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"error":{"code":"OFFSET_TOO_LARGE"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"ids":[],"next_cursor":""}}`))
	}))
	t.Cleanup(graph.Close)
	return &Service{
		graphURL: graph.URL, graphClient: &http.Client{Timeout: 2 * time.Second},
		timelines: newRecordingTimelines(), celebs: fixedCelebs(false),
	}
}

// graph-service answers 400 OFFSET_TOO_LARGE past offset 10,000. A fan-out
// that gets that answer gets it on every attempt, so its error must read as
// a refusal or the event would hold its partition for good.
func TestFanoutPost_GraphRefusalIsARejection(t *testing.T) {
	s := graphStub(t, http.StatusBadRequest, http.StatusOK)
	err := s.FanoutPost(context.Background(), uuid.New(), uuid.New(), time.Now(), "post", "public", uuid.Nil)
	if err == nil {
		t.Fatal("a refused followers page was swallowed")
	}
	if !UpstreamRejected(err) {
		t.Fatalf("a 400 from graph-service did not read as a refusal: %v", err)
	}
}

// An outage must never read as a refusal: that is the event loss feed-06
// is about.
func TestFanoutPost_GraphOutageIsNotARejection(t *testing.T) {
	for _, code := range []int{http.StatusServiceUnavailable, http.StatusInternalServerError, http.StatusUnauthorized, http.StatusNotFound} {
		s := graphStub(t, code, http.StatusOK)
		err := s.FanoutPost(context.Background(), uuid.New(), uuid.New(), time.Now(), "post", "public", uuid.Nil)
		if err == nil {
			t.Fatalf("status %d: a failed followers page was swallowed", code)
		}
		if UpstreamRejected(err) {
			t.Fatalf("status %d read as a refusal; the event would be dead-lettered instead of retried: %v", code, err)
		}
	}
}

// One leg refused and another merely down: keep retrying, so the leg that
// can finish does.
func TestFanoutPost_RefusalPlusOutageIsNotARejection(t *testing.T) {
	s := graphStub(t, http.StatusBadRequest, http.StatusServiceUnavailable)
	err := s.FanoutPost(context.Background(), uuid.New(), uuid.New(), time.Now(), "post", "public", uuid.Nil)
	if err == nil || UpstreamRejected(err) {
		t.Fatalf("err = %v; want an error that is not a refusal while the connections leg is down", err)
	}
}

func TestFanoutQuestion_GraphRefusalIsARejection(t *testing.T) {
	s := graphStub(t, http.StatusBadRequest, http.StatusBadRequest)
	err := s.FanoutQuestion(context.Background(), uuid.New(), uuid.New(), time.Now())
	if !UpstreamRejected(err) {
		t.Fatalf("err = %v; want a refusal", err)
	}
}
