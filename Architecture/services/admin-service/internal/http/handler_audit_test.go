package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/atpost/admin-service/internal/approvals"
	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/admin-service/internal/store/postgres"
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
	{http.MethodPost, "/v1/admin/commerce/sellers/s-1/unsuspend", `{"reason":"cleared"}`, "seller.unsuspend", "seller", "s-1", "cleared"},
	{http.MethodPost, "/v1/admin/commerce/sellers/s-1/kyc/verify", `{"reason":"docs in"}`, "seller.kyc_verify", "seller", "s-1", "docs in"},
	{http.MethodPost, "/v1/admin/commerce/products/p-1/approve", `{}`, "product.approve", "product", "p-1", ""},
	{http.MethodPost, "/v1/admin/commerce/products/p-1/reject", `{"reason":"counterfeit"}`, "product.reject", "product", "p-1", "counterfeit"},
	{http.MethodPost, "/v1/admin/commerce/products/p-1/request-changes", `{"changes":"photos"}`, "product.request_changes", "product", "p-1", ""},
	{http.MethodPost, "/v1/admin/commerce/catalogue/attribute-definitions", `{}`, "catalogue.post", "attribute-definitions", "attribute-definitions", ""},
	{http.MethodPatch, "/v1/admin/commerce/catalogue/categories/c-1/attributes", `{}`, "catalogue.patch", "categories", "categories/c-1/attributes", ""},
	{http.MethodPut, "/v1/admin/commerce/catalogue/attribute-schema/c-1", `{}`, "catalogue.put", "attribute-schema", "attribute-schema/c-1", ""},
}

func TestEveryCommerceWriteForwardsTheActorAndIsAuditedOnce(t *testing.T) {
	for _, wr := range commerceWriteRoutes {
		t.Run(wr.operation, func(t *testing.T) {
			rg := newRig(t, rigOpts{})
			rg.perms.grant(testActor, commerceAll...)
			w := rg.do(wr.method, wr.path, wr.body, testActor)
			if w.Code != http.StatusOK {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			hits, actor, _, _ := rg.seen.snapshot()
			if hits != 1 || actor != testActor {
				t.Fatalf("commerce saw hits=%d X-User-Id=%q, want 1 and %q", hits, actor, testActor)
			}
			if rg.seen.request != "req-123" {
				t.Fatalf("request id not forwarded: %q", rg.seen.request)
			}
			e := rg.onlyEntry(t)
			if e.Actor != testActor || e.App != "commerce" || e.Operation != wr.operation ||
				e.TargetType != wr.targetType || e.TargetID != wr.target || e.Reason != wr.reason ||
				e.RequestID != "req-123" || e.Outcome != postgres.AuditOutcomeSuccess || e.StatusCode != http.StatusOK {
				t.Fatalf("audit entry %+v", e)
			}
		})
	}
}

func TestAReadIsAuditedToo(t *testing.T) {
	rg := newRig(t, rigOpts{})
	rg.perms.grant(testActor, permSellerApprove)
	w := rg.do(http.MethodGet, "/v1/admin/commerce/sellers/queue?limit=5", "", testActor)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if e := rg.onlyEntry(t); e.Operation != "sellers.queue" || e.Outcome != postgres.AuditOutcomeSuccess || e.App != "commerce" {
		t.Fatalf("audit entry %+v", e)
	}
}

func TestARequestWithoutAnActorIsRefusedBeforeAnything(t *testing.T) {
	for _, actor := range []string{"", "not-a-uuid"} {
		for _, wr := range commerceWriteRoutes {
			rg := newRig(t, rigOpts{})
			w := rg.do(wr.method, wr.path, wr.body, actor)
			if w.Code != http.StatusUnauthorized || !hasCode(w, CodeActorRequired) {
				t.Fatalf("%s actor=%q: status %d body %s, want 401 ACTOR_REQUIRED", wr.operation, actor, w.Code, w.Body.String())
			}
			if hits, _, _, _ := rg.seen.snapshot(); hits != 0 || rg.perms.calls != 0 {
				t.Fatalf("%s actor=%q reached commerce (%d) or identity (%d)", wr.operation, actor, hits, rg.perms.calls)
			}
			if n := len(rg.entries()); n != 0 {
				t.Fatalf("%s actor=%q wrote %d audit rows with no actor", wr.operation, actor, n)
			}
		}
	}
}

func TestADownstreamFailureIsStillAudited(t *testing.T) {
	for _, wr := range commerceWriteRoutes {
		rg := newRig(t, rigOpts{status: http.StatusInternalServerError})
		rg.perms.grant(testActor, commerceAll...)
		w := rg.do(wr.method, wr.path, wr.body, testActor)
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("%s: status %d, want the upstream 500", wr.operation, w.Code)
		}
		if e := rg.onlyEntry(t); e.Outcome != postgres.AuditOutcomeFailure || e.StatusCode != http.StatusInternalServerError {
			t.Fatalf("%s: audit entry %+v", wr.operation, e)
		}
	}
}

func TestAnUnreachableCommerceIsStillAudited(t *testing.T) {
	for _, wr := range commerceWriteRoutes {
		rg := newRig(t, rigOpts{})
		rg.perms.grant(testActor, commerceAll...)
		rg.upstream.Close()
		w := rg.do(wr.method, wr.path, wr.body, testActor)
		if w.Code != http.StatusBadGateway {
			t.Fatalf("%s: status %d, want 502", wr.operation, w.Code)
		}
		e := rg.onlyEntry(t)
		if e.Outcome != postgres.AuditOutcomeFailure || e.StatusCode != 0 || e.Payload["error"] == nil {
			t.Fatalf("%s: audit entry %+v", wr.operation, e)
		}
	}
}

func TestAFailedAuditInsertDoesNotHideTheWrite(t *testing.T) {
	rg := newRig(t, rigOpts{})
	rg.perms.grant(testActor, commerceAll...)
	rg.rec.err = errors.New("db down")
	w := rg.do(commerceWriteRoutes[0].method, commerceWriteRoutes[0].path, commerceWriteRoutes[0].body, testActor)
	if hits, _, _, _ := rg.seen.snapshot(); w.Code != http.StatusOK || hits != 1 || len(rg.entries()) != 1 {
		t.Fatalf("status %d hits %d attempts %d", w.Code, hits, len(rg.entries()))
	}
}

func TestAdminRoutesAreRefusedWithoutARecorder(t *testing.T) {
	hits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer upstream.Close()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	perms := &fakePerms{byUser: map[string]adminauth.Permissions{}}
	perms.grant(testActor, commerceAll...)
	h := New(&stubAdminService{}, NewGate(perms, nil, true), approvals.NewService(newMemStore(), &fakeHolders{}))
	if err := h.RegisterAllRoutes(r, service.NewCommerceClient(upstream.URL, "k")); err != nil {
		t.Fatal(err)
	}
	rg := &rig{r: r}
	w := rg.do(commerceWriteRoutes[0].method, commerceWriteRoutes[0].path, `{}`, testActor)
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
	if _, _, err := cc.SettleCODRemittance(ctx, "r-1", "", ""); !errors.Is(err, service.ErrActorRequired) {
		t.Fatalf("SettleCODRemittance err = %v", err)
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
