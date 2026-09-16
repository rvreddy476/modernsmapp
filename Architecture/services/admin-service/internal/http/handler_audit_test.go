package http

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
)

const testActor = "7b0c6c1e-0f3a-4d59-9a57-3c1f7d1c2b10"

type fakeRecorder struct {
	mu      sync.Mutex
	entries []postgres.AdminAuditEntry
	err     error
}

func (f *fakeRecorder) RecordAdminWrite(_ context.Context, e postgres.AdminAuditEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, e)
	return f.err
}

type upstreamSeen struct {
	mu      sync.Mutex
	hits    int
	actor   string
	request string
}

// commerceRig stands up a commerce stub answering status, registers every
// commerce and catalogue route against it, and returns the router, the
// recorder and what the stub saw.
func commerceRig(t *testing.T, status int) (*gin.Engine, *fakeRecorder, *upstreamSeen, *httptest.Server) {
	t.Helper()
	seen := &upstreamSeen{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		seen.mu.Lock()
		seen.hits++
		seen.actor = r.Header.Get("X-User-Id")
		seen.request = r.Header.Get("X-Request-Id")
		seen.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(upstream.Close)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.RequestID())
	rec := &fakeRecorder{}
	h := &Handler{audit: rec}
	cc := service.NewCommerceClient(upstream.URL, "test-internal-key")
	h.RegisterCommerceRoutes(r, cc)
	h.RegisterCatalogueRoutes(r, cc)
	return r, rec, seen, upstream
}

type writeRoute struct {
	method, path, body            string
	operation, targetType, target string
	reason                        string
}

var commerceWriteRoutes = []writeRoute{
	{http.MethodPost, "/v1/admin/commerce/sellers/s-1/approve", `{"notes":"ok"}`, "seller.approve", "seller", "s-1", ""},
	{http.MethodPost, "/v1/admin/commerce/sellers/s-1/reject", `{"reason":"fake docs"}`, "seller.reject", "seller", "s-1", "fake docs"},
	{http.MethodPost, "/v1/admin/commerce/sellers/s-1/request-changes", `{"changes":"gstin"}`, "seller.request_changes", "seller", "s-1", ""},
	{http.MethodPost, "/v1/admin/commerce/sellers/s-1/suspend", `{"reason":"fraud"}`, "seller.suspend", "seller", "s-1", "fraud"},
	{http.MethodPost, "/v1/admin/commerce/products/p-1/approve", `{}`, "product.approve", "product", "p-1", ""},
	{http.MethodPost, "/v1/admin/commerce/products/p-1/reject", `{"reason":"counterfeit"}`, "product.reject", "product", "p-1", "counterfeit"},
	{http.MethodPost, "/v1/admin/commerce/catalogue/attribute-definitions", `{}`, "catalogue.post", "attribute-definitions", "attribute-definitions", ""},
	{http.MethodPatch, "/v1/admin/commerce/catalogue/categories/c-1/attributes", `{}`, "catalogue.patch", "categories", "categories/c-1/attributes", ""},
	{http.MethodPut, "/v1/admin/commerce/catalogue/attribute-schema/c-1", `{}`, "catalogue.put", "attribute-schema", "attribute-schema/c-1", ""},
}

func sendWrite(r *gin.Engine, wr writeRoute, actor string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(wr.method, wr.path, strings.NewReader(wr.body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Scopes", "admin")
	req.Header.Set("X-Request-Id", "req-123")
	if actor != "" {
		req.Header.Set("X-User-Id", actor)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestEveryCommerceWriteForwardsTheActorAndIsAudited(t *testing.T) {
	for _, wr := range commerceWriteRoutes {
		t.Run(wr.operation, func(t *testing.T) {
			r, rec, seen, _ := commerceRig(t, http.StatusOK)
			w := sendWrite(r, wr, testActor)
			if w.Code != http.StatusOK {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if seen.hits != 1 || seen.actor != testActor {
				t.Fatalf("commerce saw hits=%d X-User-Id=%q, want 1 and %q", seen.hits, seen.actor, testActor)
			}
			if seen.request != "req-123" {
				t.Fatalf("request id not forwarded: %q", seen.request)
			}
			if len(rec.entries) != 1 {
				t.Fatalf("audit rows = %d, want 1", len(rec.entries))
			}
			e := rec.entries[0]
			if e.Actor != testActor || e.App != "commerce" || e.Operation != wr.operation ||
				e.TargetType != wr.targetType || e.TargetID != wr.target || e.Reason != wr.reason ||
				e.RequestID != "req-123" || e.Outcome != postgres.AuditOutcomeSuccess || e.StatusCode != http.StatusOK {
				t.Fatalf("audit entry %+v", e)
			}
		})
	}
}

func TestAWriteWithoutAnActorIsRefusedBeforeCommerce(t *testing.T) {
	for _, actor := range []string{"", "not-a-uuid"} {
		for _, wr := range commerceWriteRoutes {
			r, rec, seen, _ := commerceRig(t, http.StatusOK)
			w := sendWrite(r, wr, actor)
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "ACTOR_REQUIRED") {
				t.Fatalf("%s actor=%q: status %d body %s, want 400 ACTOR_REQUIRED", wr.operation, actor, w.Code, w.Body.String())
			}
			if seen.hits != 0 {
				t.Fatalf("%s actor=%q reached commerce", wr.operation, actor)
			}
			if len(rec.entries) != 0 {
				t.Fatalf("%s actor=%q wrote %d audit rows for a write that never happened", wr.operation, actor, len(rec.entries))
			}
		}
	}
}

func TestADownstreamFailureIsStillAudited(t *testing.T) {
	for _, wr := range commerceWriteRoutes {
		r, rec, _, _ := commerceRig(t, http.StatusInternalServerError)
		w := sendWrite(r, wr, testActor)
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("%s: status %d, want the upstream 500", wr.operation, w.Code)
		}
		if len(rec.entries) != 1 {
			t.Fatalf("%s: audit rows = %d, want 1", wr.operation, len(rec.entries))
		}
		if e := rec.entries[0]; e.Outcome != postgres.AuditOutcomeFailure || e.StatusCode != http.StatusInternalServerError {
			t.Fatalf("%s: audit entry %+v", wr.operation, e)
		}
	}
}

func TestAnUnreachableCommerceIsStillAudited(t *testing.T) {
	for _, wr := range commerceWriteRoutes {
		r, rec, _, upstream := commerceRig(t, http.StatusOK)
		upstream.Close()
		w := sendWrite(r, wr, testActor)
		if w.Code != http.StatusBadGateway {
			t.Fatalf("%s: status %d, want 502", wr.operation, w.Code)
		}
		if len(rec.entries) != 1 {
			t.Fatalf("%s: audit rows = %d, want 1", wr.operation, len(rec.entries))
		}
		e := rec.entries[0]
		if e.Outcome != postgres.AuditOutcomeFailure || e.StatusCode != 0 || e.Payload["error"] == nil {
			t.Fatalf("%s: audit entry %+v", wr.operation, e)
		}
	}
}

func TestAFailedAuditInsertDoesNotHideTheWrite(t *testing.T) {
	r, rec, seen, _ := commerceRig(t, http.StatusOK)
	rec.err = errors.New("db down")
	w := sendWrite(r, commerceWriteRoutes[0], testActor)
	if w.Code != http.StatusOK || seen.hits != 1 || len(rec.entries) != 1 {
		t.Fatalf("status %d hits %d attempts %d", w.Code, seen.hits, len(rec.entries))
	}
}

func TestWritesAreRefusedWithoutARecorder(t *testing.T) {
	hits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer upstream.Close()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handler{} // no recorder
	h.RegisterCommerceRoutes(r, service.NewCommerceClient(upstream.URL, "k"))
	w := sendWrite(r, commerceWriteRoutes[0], testActor)
	if w.Code != http.StatusServiceUnavailable || hits != 0 {
		t.Fatalf("status %d hits %d, want 503 and no downstream call", w.Code, hits)
	}
}

func TestCommerceClientRefusesAWriteWithoutAnActor(t *testing.T) {
	hits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer upstream.Close()
	cc := service.NewCommerceClient(upstream.URL, "k")
	ctx := context.Background()

	if _, err := cc.ApproveSeller(ctx, "s-1", "", ""); !errors.Is(err, service.ErrActorRequired) {
		t.Fatalf("ApproveSeller err = %v", err)
	}
	if _, _, err := cc.RawProxy(ctx, http.MethodPost, "/v1/commerce/internal/categories", "", "", nil); !errors.Is(err, service.ErrActorRequired) {
		t.Fatalf("RawProxy err = %v", err)
	}
	if hits != 0 {
		t.Fatalf("upstream hit %d times", hits)
	}
	if _, _, err := cc.ListSellerQueue(ctx, 1, 0); err != nil || hits != 1 {
		t.Fatalf("reads need no actor: err=%v hits=%d", err, hits)
	}
}

func TestOAuthTokenIsNotImplemented(t *testing.T) {
	r := newAdminTestRouter(&stubAdminService{})
	req := httptest.NewRequest(http.MethodPost, "/v1/oauth/token", strings.NewReader(`{"grant_type":"authorization_code"}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotImplemented || !strings.Contains(w.Body.String(), "OAUTH_TOKEN_NOT_IMPLEMENTED") {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "access_token") {
		t.Fatal("token route still issues a token")
	}
}

func TestCreateOAuthClientHandsTheSecretToTheServiceAndNeverEchoesIt(t *testing.T) {
	var gotSecret, gotHashField string
	svc := &stubAdminService{createOAuthClientFn: func(_ context.Context, c *postgres.OAuthClient, secret string) error {
		gotSecret, gotHashField = secret, c.ClientSecretHash
		c.ClientSecretHash = "$argon2id$stored"
		return nil
	}}
	r := newAdminTestRouter(svc)
	req := httptest.NewRequest(http.MethodPost, "/v1/oauth/clients",
		strings.NewReader(`{"name":"n","client_id":"cid","client_secret":"s3cret-value"}`))
	req.Header.Set("X-User-Id", testActor)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if gotSecret != "s3cret-value" || gotHashField != "" {
		t.Fatalf("service got secret=%q hash field=%q; the handler must not put plaintext in the hash field", gotSecret, gotHashField)
	}
	if body := w.Body.String(); strings.Contains(body, "s3cret-value") || strings.Contains(body, "argon2id") {
		t.Fatalf("response leaks the secret or its hash: %s", body)
	}
}
