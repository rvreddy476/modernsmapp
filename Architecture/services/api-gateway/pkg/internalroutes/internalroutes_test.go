package internalroutes

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var refusedPaths = []string{
	"/v1/wallet/internal/debit",
	"/v1/wallet/internal/balance/11111111-1111-4111-8111-111111111111",
	"/v1/commerce/internal/sellers/abc/approve",
	"/v1/monetization/internal/charge-and-credit",
	"/v1/wallet/internal",
	"/v1/wallet/internal/",
	// Case tricks.
	"/v1/wallet/INTERNAL/debit",
	"/v1/wallet/Internal/debit",
	// Encoded slashes and letters.
	"/v1/wallet%2Finternal/debit",
	"/v1/wallet%2finternal%2fdebit",
	"/v1/wallet/%69nternal/debit",
	"/v1/wallet/%49NTERNAL/debit",
	// Double and triple encoding.
	"/v1/wallet%252Finternal/debit",
	"/v1/wallet%25252Finternal/debit",
	// Dot segments, raw and encoded.
	"/v1/wallet/x/../internal/debit",
	"/v1/wallet/x/..%2Finternal/debit",
	"/v1/wallet/x/%2E%2E/internal/debit",
	// Double slashes.
	"//v1/wallet//internal//debit",
	"/v1/wallet//internal/debit",
	// Backslash separators, encoded.
	"/v1/wallet%5Cinternal%5Cdebit",
	// Path parameters.
	"/v1/wallet/internal;x=1/debit",
}

var allowedPaths = []string{
	"/v1/wallet/balance",
	"/v1/commerce/products/internalize",
	"/v1/posts/internals",
	"/v1/auth/internally-managed",
	"/v1/users/me",
	"/v1/admin/commerce/sellers/queue",
	"/v1/search?q=/internal/",
}

// sendRaw writes a request line exactly as given, so Go's own URL parsing is
// part of what is tested rather than bypassed by httptest.NewRequest.
func sendRaw(t *testing.T, addr, target string) int {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("GET " + target + " HTTP/1.1\r\nHost: gateway\r\nConnection: close\r\n\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read response for %q: %v", target, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestInternalPathsAreRefusedOverTheWire(t *testing.T) {
	var reached []string
	srv := httptest.NewServer(Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = append(reached, r.RequestURI)
		w.WriteHeader(http.StatusOK)
	})))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	for _, p := range refusedPaths {
		if got := sendRaw(t, addr, p); got != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", p, got)
		}
	}
	if len(reached) != 0 {
		t.Fatalf("internal paths reached the upstream: %v", reached)
	}
	for _, p := range allowedPaths {
		if got := sendRaw(t, addr, p); got != http.StatusOK {
			t.Errorf("%s: status %d, want 200 (not an internal path)", p, got)
		}
	}
}

func TestIsInternalPathForms(t *testing.T) {
	cases := []struct {
		decoded, raw string
		want         bool
	}{
		{"/v1/wallet/internal/debit", "", true},
		{"/v1/wallet/ internal /debit", "", true},
		{`/v1/wallet\internal\debit`, "", true},
		// A malformed escape elsewhere must not stop the rest being decoded.
		{"", "/v1/wallet/%zz/%69nternal/debit", true},
		{"/v1/wallet/balance", "/v1/wallet/balance", false},
		{"/v1/wallet/internalx", "", false},
	}
	for _, c := range cases {
		if got := IsInternalPath(c.decoded, c.raw); got != c.want {
			t.Errorf("IsInternalPath(%q, %q) = %t, want %t", c.decoded, c.raw, got, c.want)
		}
	}
}

// No scope opens an internal path: the refusal never reads identity.
func TestInternalPathsAreRefusedWhateverTheScopes(t *testing.T) {
	for _, scopes := range []string{"", "user", "moderator", "admin", "superadmin", "admin moderator superadmin"} {
		reached := false
		h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
		req := httptest.NewRequest(http.MethodPost, "/v1/wallet/internal/debit", nil)
		if scopes != "" {
			req.Header.Set("X-Scopes", scopes)
			req.Header.Set("X-Admin-Role", "superadmin")
		}
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code != http.StatusNotFound || reached {
			t.Errorf("scopes %q: status %d reached %t, want 404 and not reached", scopes, res.Code, reached)
		}
	}
}

func TestApplyKey(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/wallet/balance", nil)
	req.Header.Set(InternalKeyHeader, "forged")
	req.Header["x-internal-service-key"] = []string{"forged-lowercase"}
	ApplyKey(req, false, "real")
	for name := range req.Header {
		if strings.EqualFold(name, InternalKeyHeader) {
			t.Fatalf("unstamped upstream still carries %s", name)
		}
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/commerce/products", nil)
	req.Header.Set(InternalKeyHeader, "forged")
	ApplyKey(req, true, "real")
	if got := req.Header.Values(InternalKeyHeader); len(got) != 1 || got[0] != "real" {
		t.Fatalf("stamped upstream key = %v, want [real]", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/commerce/products", nil)
	ApplyKey(req, true, "")
	if req.Header.Get(InternalKeyHeader) != "" {
		t.Fatal("an empty secret must not be stamped")
	}
}

func TestServiceOnlyUpstreamsAreNotStamped(t *testing.T) {
	for _, p := range []string{"/v1/wallet", "/v1/billpay", "/v1/auth", "/v1/media", "/v1/audio", "/v1/chat", "/v1/calls", "/v1/ws"} {
		if ShouldStamp(p) {
			t.Errorf("%s: stamped, but its user routes do not need the internal key", p)
		}
	}
	if ShouldStamp("/v1/not-a-route") {
		t.Error("an unclassified prefix must never be stamped")
	}
	if !ShouldStamp("/v1/commerce") || !ShouldStamp("/v1/posts") {
		t.Error("commerce and post-service still require the key on user traffic")
	}
}

func TestGuardStampPolicy(t *testing.T) {
	if err := GuardStampPolicy([]string{"/v1/wallet", "/v1/commerce"}); err != nil {
		t.Fatalf("classified prefixes rejected: %v", err)
	}
	err := GuardStampPolicy([]string{"/v1/wallet", "/v1/brand-new"})
	if err == nil || !strings.Contains(err.Error(), "/v1/brand-new") {
		t.Fatalf("unclassified prefix not reported: %v", err)
	}
}
