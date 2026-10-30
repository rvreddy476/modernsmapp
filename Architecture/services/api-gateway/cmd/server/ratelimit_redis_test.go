package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/api-gateway/pkg/clientip"
)

// allow() / Redis behavior is integration-test territory (testcontainers).
// What can be pinned in unit tests without a Redis instance is the
// client-IP extraction logic — the bit that decides which key the limiter
// counts against. Getting that wrong (treating every proxied request as
// the same key) would silently amplify rate limits at the wrong tier, so
// we lock the precedence down.

// Audit M1: the key is never taken from a header the client wrote. The old
// cases here pinned the opposite (X-Real-IP and the leftmost XFF hop won),
// which is exactly what let a client pick a fresh bucket per request.
func TestClientIPForRateLimit(t *testing.T) {
	vpc, err := clientip.New([]string{"10.30.0.0/16"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name       string
		resolver   *clientip.Resolver // nil: the middleware did not run
		remoteAddr string
		xRealIP    string
		xff        string
		want       string
	}{
		{
			name:       "RemoteAddr only, port stripped",
			remoteAddr: "203.0.113.7:54321",
			want:       "203.0.113.7",
		},
		{
			name:       "without the middleware headers are ignored",
			remoteAddr: "10.30.0.1:54321",
			xRealIP:    "203.0.113.9",
			xff:        "203.0.113.10, 10.30.0.5",
			want:       "10.30.0.1",
		},
		{
			name:       "forged X-Real-IP is ignored behind the ALB",
			resolver:   &vpc,
			remoteAddr: "10.30.0.1:54321",
			xRealIP:    "198.51.100.77",
			xff:        "203.0.113.11",
			want:       "203.0.113.11",
		},
		{
			name:       "forged leftmost XFF hop is ignored behind the ALB",
			resolver:   &vpc,
			remoteAddr: "10.30.0.1:54321",
			xff:        "198.51.100.1, 203.0.113.12",
			want:       "203.0.113.12",
		},
		{
			name:       "XFF from an untrusted peer is ignored",
			resolver:   &vpc,
			remoteAddr: "203.0.113.50:54321",
			xff:        "198.51.100.1",
			want:       "203.0.113.50",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/v1/posts", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.xRealIP != "" {
				req.Header.Set("X-Real-IP", tc.xRealIP)
			}
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			var got string
			if tc.resolver == nil {
				got = clientIPForRateLimit(req)
			} else {
				clientip.Middleware(*tc.resolver, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
					got = clientIPForRateLimit(r)
				})).ServeHTTP(httptest.NewRecorder(), req)
			}
			if got != tc.want {
				t.Errorf("clientIPForRateLimit() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSplitHostPortLenient(t *testing.T) {
	cases := []struct {
		in       string
		wantHost string
	}{
		{"203.0.113.7:54321", "203.0.113.7"},
		{"203.0.113.7", "203.0.113.7"},
		{"[2001:db8::1]:54321", "2001:db8::1"},
		{"[2001:db8::1]", "2001:db8::1"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			host, _, _ := splitHostPortLenient(tc.in)
			if host != tc.wantHost {
				t.Errorf("splitHostPortLenient(%q) host=%q want %q", tc.in, host, tc.wantHost)
			}
		})
	}
}

// allow() on a nil receiver must be a no-op (fail-open) — guarantees
// the in-memory fallback continues to function unaltered when Redis
// isn't configured.
func TestRedisRateLimiterNilSafe(t *testing.T) {
	var rl *redisRateLimiter
	ok, err := rl.allow(nil, "ip", "1.2.3.4", 10)
	if err != nil {
		t.Fatalf("nil receiver should not return an error, got %v", err)
	}
	if !ok {
		t.Fatalf("nil receiver should fail open (allow=true)")
	}
}
