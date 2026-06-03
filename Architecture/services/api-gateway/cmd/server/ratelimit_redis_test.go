package main

import (
	"net/http/httptest"
	"testing"
)

// allow() / Redis behavior is integration-test territory (testcontainers).
// What can be pinned in unit tests without a Redis instance is the
// client-IP extraction logic — the bit that decides which key the limiter
// counts against. Getting that wrong (treating every proxied request as
// the same key) would silently amplify rate limits at the wrong tier, so
// we lock the precedence down.

func TestClientIPForRateLimit(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		xRealIP    string
		xff        string
		want       string
	}{
		{
			name:       "RemoteAddr only — port stripped",
			remoteAddr: "203.0.113.7:54321",
			want:       "203.0.113.7",
		},
		{
			name:       "X-Real-IP wins over RemoteAddr",
			remoteAddr: "10.0.0.1:54321",
			xRealIP:    "203.0.113.9",
			want:       "203.0.113.9",
		},
		{
			name:       "X-Forwarded-For first hop wins when X-Real-IP empty",
			remoteAddr: "10.0.0.1:54321",
			xff:        "203.0.113.10, 10.0.0.5",
			want:       "203.0.113.10",
		},
		{
			name:       "X-Real-IP beats X-Forwarded-For",
			remoteAddr: "10.0.0.1:54321",
			xRealIP:    "203.0.113.11",
			xff:        "198.51.100.1, 10.0.0.5",
			want:       "203.0.113.11",
		},
		{
			name:       "single-hop XFF without comma",
			remoteAddr: "10.0.0.1:54321",
			xff:        "203.0.113.12",
			want:       "203.0.113.12",
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
			got := clientIPForRateLimit(req)
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
