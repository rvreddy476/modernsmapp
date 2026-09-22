package turn

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The documented response: an array with a STUN entry and a TURN entry that
// lists UDP, TCP, and TLS on 3478/80/5349/443.
const documentedResponse = `{"iceServers":[
  {"urls":["stun:stun.cloudflare.com:3478"]},
  {"urls":[
    "turn:turn.cloudflare.com:3478?transport=udp",
    "turn:turn.cloudflare.com:3478?transport=tcp",
    "turn:turn.cloudflare.com:80?transport=tcp",
    "turns:turn.cloudflare.com:5349?transport=tcp",
    "turns:turn.cloudflare.com:443?transport=tcp"],
   "username":"u1","credential":"c1"}
]}`

func TestParseICEServersDocumentedShape(t *testing.T) {
	servers, err := parseICEServers([]byte(documentedResponse))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(servers) != 2 {
		t.Fatalf("want 2 servers, got %d", len(servers))
	}
	if servers[1].Username != "u1" || servers[1].Credential != "c1" {
		t.Errorf("credentials not carried: %+v", servers[1])
	}
	if len(servers[1].URLs) != 5 || !strings.HasPrefix(servers[1].URLs[4], "turns:") {
		t.Errorf("TURN urls not carried: %v", servers[1].URLs)
	}
}

func TestParseICEServersSingleObjectShape(t *testing.T) {
	raw := `{"iceServers":{"urls":["turn:turn.cloudflare.com:3478?transport=udp"],"username":"u","credential":"c"}}`
	servers, err := parseICEServers([]byte(raw))
	if err != nil || len(servers) != 1 {
		t.Fatalf("single-object form: err=%v n=%d", err, len(servers))
	}
}

func TestParseICEServersRefusesStunOnly(t *testing.T) {
	// A response with no relay would satisfy the parser and then reproduce
	// exactly the failure this package exists to remove.
	if _, err := parseICEServers([]byte(`{"iceServers":[{"urls":["stun:stun.cloudflare.com:3478"]}]}`)); err == nil {
		t.Fatal("expected an error for a STUN-only response")
	}
}

func TestICEServersCachesUntilNearExpiry(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("missing bearer: %q", r.Header.Get("Authorization"))
		}
		var body map[string]int64
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["ttl"] != 3600 {
			t.Errorf("ttl not sent in seconds: %v", body)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(documentedResponse))
	}))
	defer srv.Close()

	c, err := NewCloudflare("key", "tok", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	c.endpoint = srv.URL

	for i := 0; i < 5; i++ {
		if _, err := c.ICEServers(context.Background()); err != nil {
			t.Fatalf("mint %d: %v", i, err)
		}
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("five joins should cost one mint, got %d", n)
	}

	// Push the cache to within a quarter of its TTL of expiring: next call
	// must mint again.
	c.expiresAt = time.Now().Add(10 * time.Minute)
	if _, err := c.ICEServers(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Fatalf("near-expiry should re-mint, got %d calls", n)
	}
}

func TestICEServersServesValidCacheWhenMintFails(t *testing.T) {
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(documentedResponse))
	}))
	defer srv.Close()

	c, _ := NewCloudflare("key", "tok", time.Hour)
	c.endpoint = srv.URL
	if _, err := c.ICEServers(context.Background()); err != nil {
		t.Fatal(err)
	}
	fail.Store(true)
	c.expiresAt = time.Now().Add(10 * time.Minute) // near expiry, still valid
	if _, err := c.ICEServers(context.Background()); err != nil {
		t.Fatalf("a still-valid cache should be served over a mint error: %v", err)
	}
	c.expiresAt = time.Now().Add(-time.Second) // expired
	if _, err := c.ICEServers(context.Background()); err == nil {
		t.Fatal("an expired cache must not mask a mint error")
	}
}

func TestNewCloudflareRefusesHalfConfig(t *testing.T) {
	if _, err := NewCloudflare("key", "", time.Hour); err == nil {
		t.Fatal("token missing should be refused")
	}
	if _, err := NewCloudflare("", "tok", time.Hour); err == nil {
		t.Fatal("key missing should be refused")
	}
	if _, err := NewCloudflare("key", "tok", 10*time.Second); err == nil {
		t.Fatal("a ttl shorter than any call should be refused")
	}
}
