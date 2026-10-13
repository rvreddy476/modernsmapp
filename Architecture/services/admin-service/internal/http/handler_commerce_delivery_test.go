package http

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/atpost/admin-service/internal/service"
	"github.com/google/uuid"
)

func TestCommerceDeliveryDocumentIsPermissionGatedAndAudited(t *testing.T) {
	rg := newProductsRig(t, true)
	profile, doc := uuid.NewString(), uuid.NewString()
	path := "/delivery/profiles/" + profile + "/documents/" + doc
	rg.on(http.MethodGet, service.CommerceAdminPrefix+path+"/image", serveImage("image/png", []byte("test-image"), true))
	merchantReviewer := uuid.NewString()
	rg.perms.grant(merchantReviewer, permSellerApprove)
	w := rg.do(http.MethodGet, commercePrefix+path+"/view", "", merchantReviewer, true)
	if w.Code != 403 || len(rg.takeHits()) != 0 {
		t.Fatal("merchant approval permission exposed worker evidence")
	}
	rg.takeAudit()
	actor := kycAdmin(rg)
	w = rg.do(http.MethodGet, commercePrefix+path+"/view", "", actor, true)
	hits, audit := rg.takeHits(), rg.takeAudit()
	if w.Code != 200 || w.Body.String() != "test-image" || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("unsafe image response: %d %s", w.Code, w.Body.String())
	}
	if len(hits) != 1 || hits[0].verifyErr != nil || hits[0].verified.Actor != actor || hits[0].verified.Scope[0] != permKYCVerify {
		t.Fatal("missing signed review scope")
	}
	if len(audit) != 1 || audit[0].TargetID != profile || audit[0].TargetType != "commerce_delivery_partner" {
		t.Fatalf("audit target: %+v", audit)
	}
	rg.rec.err = errors.New("audit unavailable")
	w = rg.do(http.MethodGet, commercePrefix+path+"/view", "", actor, true)
	if w.Code != 503 || strings.Contains(w.Body.String(), "test-image") {
		t.Fatal("image escaped without audit")
	}
}
