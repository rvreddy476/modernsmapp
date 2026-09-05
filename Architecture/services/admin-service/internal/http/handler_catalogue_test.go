package http

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/admin-service/internal/service"
	"github.com/gin-gonic/gin"
)

// catalogueRig stands a real upstream up, points a CommerceClient at it, and
// returns the router plus a pointer to what the upstream last saw. The
// upstream is a real server rather than a stub transport because the point of
// this proxy is what goes over the wire — the method, the path, the query and
// the key.
func catalogueRig(t *testing.T) (*gin.Engine, *struct {
	Method, Path, Query, Key, Body string
	Hits                           int
}) {
	t.Helper()
	seen := &struct {
		Method, Path, Query, Key, Body string
		Hits                           int
	}{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		seen.Method, seen.Path = r.Method, r.URL.Path
		seen.Query, seen.Key, seen.Body = r.URL.RawQuery, r.Header.Get("X-Internal-Service-Key"), string(buf)
		seen.Hits++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	t.Cleanup(upstream.Close)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handler{}
	h.RegisterCatalogueRoutes(r, service.NewCommerceClient(upstream.URL, "test-internal-key"))
	return r, seen
}

func callCatalogue(r *gin.Engine, method, path, scopes, body string) *httptest.ResponseRecorder {
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, rdr)
	if scopes != "" {
		req.Header.Set("X-Scopes", scopes)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestTheConsoleReachesTheInternalRoutesWithTheKeyAttached(t *testing.T) {
	r, seen := catalogueRig(t)

	w := callCatalogue(r, http.MethodGet,
		"/v1/admin/commerce/catalogue/attribute-definitions?limit=50&active=true", "admin", "")

	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if seen.Path != "/v1/commerce/internal/attribute-definitions" {
		t.Fatalf("upstream path %q", seen.Path)
	}
	if seen.Query != "limit=50&active=true" {
		t.Fatalf("query not forwarded: %q", seen.Query)
	}
	if seen.Key != "test-internal-key" {
		t.Fatal("the internal key never reached the upstream; a browser cannot supply it, so this is the whole point of the proxy")
	}
	if body := w.Body.String(); !strings.Contains(body, `"ok":true`) {
		t.Fatalf("upstream body not passed through: %s", body)
	}
}

func TestAWriteCarriesItsBodyAndMethodThrough(t *testing.T) {
	r, seen := catalogueRig(t)

	w := callCatalogue(r, http.MethodPatch,
		"/v1/admin/commerce/catalogue/categories/abc/attributes?ack_impact=4",
		"superadmin", `{"is_required":true}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if seen.Method != http.MethodPatch {
		t.Fatalf("method %q — a proxy that downgrades a PATCH silently does nothing", seen.Method)
	}
	if seen.Path != "/v1/commerce/internal/categories/abc/attributes" {
		t.Fatalf("upstream path %q", seen.Path)
	}
	if seen.Query != "ack_impact=4" {
		t.Fatalf("the impact acknowledgement was dropped: %q", seen.Query)
	}
	if !strings.Contains(seen.Body, "is_required") {
		t.Fatalf("body not forwarded: %q", seen.Body)
	}
}

func TestTheProxyOnlyOpensTheCatalogue(t *testing.T) {
	// The reason the allowlist exists: these are real internal routes, and
	// reaching them here would gate seller money decisions behind this
	// group's scopes instead of their own.
	for _, path := range []string{
		"/v1/admin/commerce/catalogue/sellers/queue",
		"/v1/admin/commerce/catalogue/payouts",
		"/v1/admin/commerce/catalogue/products/123/approve",
	} {
		r, seen := catalogueRig(t)
		w := callCatalogue(r, http.MethodGet, path, "superadmin", "")
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s: status %d, want 404", path, w.Code)
		}
		if seen.Hits != 0 {
			t.Fatalf("%s reached the upstream — the allowlist did not hold", path)
		}
	}
}

func TestTraversalCannotClimbOutOfTheInternalNamespace(t *testing.T) {
	r, seen := catalogueRig(t)
	w := callCatalogue(r, http.MethodGet,
		"/v1/admin/commerce/catalogue/categories/..%2f..%2fsellers/queue", "superadmin", "")
	if w.Code == http.StatusOK {
		t.Fatal("a traversal was proxied with the internal key attached")
	}
	if seen.Hits != 0 {
		t.Fatalf("traversal reached the upstream (status %d)", w.Code)
	}
}

func TestAuthoringNeedsMoreThanReading(t *testing.T) {
	cases := []struct {
		method, scopes string
		want           int
	}{
		{http.MethodGet, "moderator", http.StatusOK},
		{http.MethodGet, "", http.StatusForbidden},
		{http.MethodPost, "moderator", http.StatusForbidden},
		{http.MethodPost, "admin", http.StatusOK},
		{http.MethodPatch, "moderator", http.StatusForbidden},
		{http.MethodPut, "superadmin", http.StatusOK},
	}
	for _, tc := range cases {
		r, _ := catalogueRig(t)
		w := callCatalogue(r, tc.method,
			"/v1/admin/commerce/catalogue/attribute-definitions", tc.scopes, "{}")
		if w.Code != tc.want {
			t.Fatalf("%s with scopes %q: status %d, want %d",
				tc.method, tc.scopes, w.Code, tc.want)
		}
	}
}
