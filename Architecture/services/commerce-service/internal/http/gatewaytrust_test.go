package http

// Who is allowed to say who the caller is.
//
// `getUserID` reads `X-User-Id` and believes it. Before RequireGatewayTrust
// nothing checked where that header came from, so any process that could open
// a TCP connection to this pod could act as any user.
//
// These are unit tests, deliberately: the defect is in what the middleware
// accepts, not in anything a database can show.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const testKey = "test-internal-service-key-0123456789"

// trustEngine is a minimal engine carrying the middleware plus one
// identity-reading route and the two kinds of route that must stay open.
func trustEngine(t *testing.T, key string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequireGatewayTrust(key))
	r.GET("/healthz", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	r.GET("/metrics", func(c *gin.Context) { c.String(http.StatusOK, "metrics") })
	r.GET("/v1/commerce/whoami", func(c *gin.Context) {
		id, ok := getUserID(c)
		if !ok {
			return
		}
		c.String(http.StatusOK, id.String())
	})
	return r
}

func get(r *gin.Engine, path, key, userID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if key != "" {
		req.Header.Set(InternalServiceKeyHeader, key)
	}
	if userID != "" {
		req.Header.Set("X-User-Id", userID)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// The defect, stated as a test: a caller who never went through the gateway
// asserts an identity and is believed.
func TestAnIdentityClaimWithoutTheGatewayKeyIsRefused(t *testing.T) {
	r := trustEngine(t, testKey)
	victim := uuid.New().String()

	w := get(r, "/v1/commerce/whoami", "", victim)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401 — a caller that never passed the gateway just acted as "+
			"user %s; every ownership check in this service resolves from that header\n%s",
			w.Code, victim, w.Body.String())
	}
	if strings.Contains(w.Body.String(), victim) {
		t.Fatalf("the response echoed the claimed identity\n%s", w.Body.String())
	}
}

// A wrong key is no better than no key.
func TestAWrongGatewayKeyIsRefused(t *testing.T) {
	r := trustEngine(t, testKey)
	w := get(r, "/v1/commerce/whoami", "not-the-key", uuid.New().String())
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", w.Code)
	}
}

// A key that is a prefix of the real one must not pass. This is the shape a
// byte-by-byte comparison would leak under timing analysis, and the shape a
// `strings.HasPrefix` mistake would let through outright.
func TestAPrefixOfTheGatewayKeyIsRefused(t *testing.T) {
	r := trustEngine(t, testKey)
	for _, bad := range []string{
		testKey[:len(testKey)-1],
		testKey + "x",
		strings.ToUpper(testKey),
		" " + testKey,
	} {
		w := get(r, "/v1/commerce/whoami", bad, uuid.New().String())
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("key %q got status %d, want 401", bad, w.Code)
		}
	}
}

// The gateway's own requests still work.
func TestTheGatewaysRequestPassesThrough(t *testing.T) {
	r := trustEngine(t, testKey)
	user := uuid.New().String()

	w := get(r, "/v1/commerce/whoami", testKey, user)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 — the gateway itself is now locked out\n%s",
			w.Code, w.Body.String())
	}
	if w.Body.String() != user {
		t.Fatalf("body = %q, want %q", w.Body.String(), user)
	}
}

// Health and metrics must stay reachable: the kubelet and the Prometheus
// scraper do not hold the key, and a service that fails its probes is a
// service that gets restarted forever.
func TestProbesAndMetricsStayOpen(t *testing.T) {
	r := trustEngine(t, testKey)
	for _, p := range []string{"/healthz", "/metrics"} {
		if w := get(r, p, "", ""); w.Code != http.StatusOK {
			t.Fatalf("%s returned %d with no key; the kubelet would restart this pod forever", p, w.Code)
		}
	}
}

// The prefix match is exact. `/v1/commercexyz` is not under `/v1/commerce`,
// and a naive HasPrefix on the bare string would gate it — harmless here, but
// the same sloppiness in reverse leaves `/v1/commerce` itself ungated.
func TestThePrefixMatchIsOnPathSegments(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequireGatewayTrust(testKey))
	r.GET("/v1/commercial/report", func(c *gin.Context) { c.String(http.StatusOK, "open") })
	r.GET("/v1/commerce", func(c *gin.Context) { c.String(http.StatusOK, "gated") })

	if w := get(r, "/v1/commercial/report", "", ""); w.Code != http.StatusOK {
		t.Fatalf("/v1/commercial/report returned %d; it is not under /v1/commerce", w.Code)
	}
	if w := get(r, "/v1/commerce", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("the bare /v1/commerce path returned %d, want 401", w.Code)
	}
}

// Constructing the middleware with no key must be impossible.
//
// A security middleware that silently allows everything when unconfigured
// reports a protection that is not there — and this one sits over X-User-Id.
func TestAnEmptyKeyPanicsRatherThanAllowingEverything(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("RequireGatewayTrust(\"\") returned a middleware instead of panicking; " +
				"it would have allowed every unauthenticated identity claim while appearing " +
				"in the wiring as a protection")
		}
	}()
	RequireGatewayTrust("")
}
