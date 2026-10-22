package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Security sweep #11 (21 Oct 2026): the gateway stamps the shared
// X-Internal-Service-Key on every proxied /v1/posts request, so the key is
// no evidence of who is calling. Every /v1/posts/internal WRITE now needs a
// signed service token from its one caller, carrying its own operation.
// The router runs with a nil service (adminTokenRouter): a request that
// gets past the gate either panics into gin.Recovery (500) or is answered
// by the handler's own checks — never by one of the gate's codes.

type internalWriteRoute struct {
	path   string
	body   string
	issuer string
	op     string
}

func internalWriteRoutes() []internalWriteRoute {
	post := uuid.NewString()
	return []internalWriteRoute{
		{"/v1/posts/internal/review-status", `{"post_id":"` + post + `","status":"approved"}`, IssuerReviewerService, OpReviewStatusWrite},
		{"/v1/posts/internal/visibility", `{"post_id":"` + post + `","visibility":"public"}`, IssuerReviewerService, OpVisibilityWrite},
		{"/v1/posts/internal/moderation", `{"claims":{},"capability":"x"}`, IssuerTrustSafety, OpModerationWrite},
		{"/v1/posts/internal/restrictions", `{"claims":{},"capability":"x"}`, IssuerTrustSafety, OpRestrictionsWrite},
	}
}

// gateCodes are the codes requireServiceToken answers with.
var gateCodes = map[string]bool{
	CodeAdminTokenRequired:        true,
	CodeServiceCredentialRequired: true,
	CodeServiceTokenRejected:      true,
	CodeAdminPermissionScope:      true,
}

func (rg *adminTokenRig) signerFor(issuer string) *servicetoken.Signer {
	switch issuer {
	case IssuerReviewerService:
		return rg.reviewer
	case IssuerTrustSafety:
		return rg.trustSafety
	}
	return nil
}

func serviceTokenHeaders(t *testing.T, s *servicetoken.Signer, aud string, scope ...string) map[string]string {
	t.Helper()
	tok, err := s.Mint(aud, "internal-write", scope, nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{"X-Internal-Service-Key": adminTestKey, ServiceAuthHeader: "Bearer " + tok}
}

func TestInternalWritesRefuseTheKeyAlone(t *testing.T) {
	rg := newAdminTokenRig(t)
	for _, rt := range internalWriteRoutes() {
		// The bare key — what every proxied request carries.
		if w := adminServe(rg.r, http.MethodPost, rt.path, rt.body, withKey()); w.Code != http.StatusUnauthorized || adminErrorCode(t, w) != CodeAdminTokenRequired {
			t.Errorf("%s key only: status=%d body=%s, want 401 %s", rt.path, w.Code, w.Body.String(), CodeAdminTokenRequired)
		}
		// The key plus a gateway moderator identity: still no token.
		mod := withKey()
		mod["X-User-Id"] = uuid.NewString()
		mod["X-Scopes"] = "user moderator admin superadmin"
		if w := adminServe(rg.r, http.MethodPost, rt.path, rt.body, mod); w.Code != http.StatusUnauthorized {
			t.Errorf("%s moderator headers: status=%d, want 401", rt.path, w.Code)
		}
	}
}

func TestInternalWritesRefuseEveryOtherToken(t *testing.T) {
	rg := newAdminTokenRig(t)
	for _, rt := range internalWriteRoutes() {
		right := rg.signerFor(rt.issuer)
		other := rg.reviewer
		if rt.issuer == IssuerReviewerService {
			other = rg.trustSafety
		}
		cases := map[string]map[string]string{
			// Registered AND over-granted the op: the issuer pin refuses it.
			"notification-service with the op": serviceTokenHeaders(t, rg.notifier, AudiencePost, rt.op),
			// admin-service is not registered for the internal writes.
			"admin-service with the op": serviceTokenHeaders(t, rg.admin, AudiencePost, rt.op),
			// The other internal caller, asking for this route's op.
			"the other internal caller": serviceTokenHeaders(t, other, AudiencePost, rt.op),
			// The right caller without this route's op.
			"right issuer, no op": serviceTokenHeaders(t, right, AudiencePost, OpRestrictionsRead),
			// The right caller and op for another audience.
			"right issuer, wrong audience": serviceTokenHeaders(t, right, "media", rt.op),
			// Claims the right issuer, signed with an unregistered key.
			"forged key": serviceTokenHeaders(t, rg.rogue, AudiencePost, rt.op),
		}
		for name, hdr := range cases {
			w := adminServe(rg.r, http.MethodPost, rt.path, rt.body, hdr)
			if w.Code != http.StatusForbidden || !gateCodes[adminErrorCode(t, w)] {
				t.Errorf("%s %s: status=%d body=%s, want the gate's 403", rt.path, name, w.Code, w.Body.String())
			}
		}
	}
}

func TestInternalWritesAdmitTheirOneCaller(t *testing.T) {
	rg := newAdminTokenRig(t)
	for _, rt := range internalWriteRoutes() {
		hdr := serviceTokenHeaders(t, rg.signerFor(rt.issuer), AudiencePost, rt.op)
		// A forged user identity rides along; the gate strips it.
		hdr["X-User-Id"] = uuid.NewString()
		w := adminServe(rg.r, http.MethodPost, rt.path, rt.body, hdr)
		if w.Code == http.StatusUnauthorized || (w.Code == http.StatusForbidden && gateCodes[adminErrorCode(t, w)]) {
			t.Errorf("%s: the gate refused its own caller: status=%d body=%s", rt.path, w.Code, w.Body.String())
		}
	}
}

// probeContext is a bare gin context with a request, for calling a
// handler helper directly.
func probeContext() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	return c
}

// The audit actor of an admitted review write is the token's issuer — and a
// handler reached without the gate (a wiring slip) refuses rather than
// recording an anonymous "service".
func TestServiceReviewActorIsTheTokenIssuer(t *testing.T) {
	c := probeContext()
	if _, ok := serviceReviewActor(c); ok {
		t.Fatal("no admitted token, yet an actor was produced")
	}
	c = probeContext()
	c.Set(ctxServiceIssuer, IssuerReviewerService)
	actor, ok := serviceReviewActor(c)
	if !ok || actor.Service != IssuerReviewerService || actor.UserID != nil {
		t.Fatalf("actor = %+v, %v; want service %q", actor, ok, IssuerReviewerService)
	}
}
