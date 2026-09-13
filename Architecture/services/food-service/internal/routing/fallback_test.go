package routing

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Whatever Google does, the caller gets the haversine answer and no error.
func TestFallback_AnswersFromHaversineOnEveryGoogleFailure(t *testing.T) {
	h := Haversine{SpeedKmh: 20, WindingFactor: 1}
	want, _ := h.Route(context.Background(), blr, indira)

	cases := []struct {
		name, class string
		handler     func(http.ResponseWriter, *http.Request)
	}{
		{"non-2xx", FailureHTTPStatus, answer(http.StatusServiceUnavailable, `{"error":{"status":"UNAVAILABLE"}}`)},
		{"empty route", FailureEmptyRoute, answer(http.StatusOK, `{}`)},
		{"garbage", FailureDecode, answer(http.StatusOK, `not json`)},
		{"timeout", FailureTimeout, hangUntilCanceled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeRoutes(t, tc.handler)
			var logs bytes.Buffer
			fb := NewFallback(googleFor(f, 100*time.Millisecond), h, slog.New(slog.NewTextHandler(&logs, nil)))
			for i := 0; i < 2; i++ {
				r, err := fb.Route(context.Background(), blr, indira)
				if err != nil {
					t.Fatalf("fallback failed the caller: %v", err)
				}
				if r != want {
					t.Fatalf("route = %+v, want the haversine answer %+v", r, want)
				}
			}
			if got := fb.Failures()[tc.class]; got != 2 {
				t.Fatalf("failures[%s] = %d, want 2 (all: %v)", tc.class, got, fb.Failures())
			}
			if n := strings.Count(logs.String(), "class="+tc.class); n != 1 {
				t.Fatalf("logged %d times for class %s, want once:\n%s", n, tc.class, logs.String())
			}
		})
	}

	t.Run("transport", func(t *testing.T) {
		f := newFakeRoutes(t, answer(http.StatusOK, `{}`))
		g := googleFor(f, 100*time.Millisecond)
		f.Close()
		fb := NewFallback(g, h, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
		if r, err := fb.Route(context.Background(), blr, indira); err != nil || r != want {
			t.Fatalf("route = %+v err = %v", r, err)
		}
		if fb.Failures()[FailureTransport] != 1 {
			t.Fatalf("failures = %v", fb.Failures())
		}
	})
}

func TestFallback_PassesAGoogleAnswerThrough(t *testing.T) {
	f := newFakeRoutes(t, answer(http.StatusOK, `{"routes":[{"distanceMeters":6120,"duration":"845s"}]}`))
	fb := NewFallback(googleFor(f, time.Second), Haversine{}, nil)
	r, err := fb.Route(context.Background(), blr, indira)
	if err != nil || r.Source != SourceGoogle || r.Duration != 845*time.Second {
		t.Fatalf("route = %+v err = %v", r, err)
	}
	if len(fb.Failures()) != 0 {
		t.Fatalf("failures = %v", fb.Failures())
	}
}

// No GOOGLE_MAPS_SERVER_KEY: no primary, haversine only, nothing counted.
func TestFallback_WithoutAKeyIsHaversineOnly(t *testing.T) {
	fb := NewFallback(nil, Haversine{}, nil)
	r, err := fb.Route(context.Background(), blr, indira)
	if err != nil || r.Source != SourceHaversine {
		t.Fatalf("route = %+v err = %v", r, err)
	}
	if len(fb.Failures()) != 0 {
		t.Fatalf("failures = %v", fb.Failures())
	}
}
