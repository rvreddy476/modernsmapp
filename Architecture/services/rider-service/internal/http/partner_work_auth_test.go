package http

import (
	"github.com/atpost/shared/partnerwork"
	"github.com/google/uuid"
	"net/http"
	"testing"
)

func TestPartnerWorkReadbackRequiresTrustedIdentityAndReportsDisabled(t *testing.T) {
	t.Setenv(partnerwork.EnabledVariable, "false")
	r := newKeyedRouter(testInternalKey)
	p := keyProbe{method: http.MethodGet, path: "/v1/partner/work-session", headers: map[string]string{"X-User-Id": uuid.NewString()}}
	if w := serve(r, p, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("forged identity status %d", w.Code)
	}
	if w := serve(r, p, testInternalKey); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled coordinator status %d", w.Code)
	}
	p.headers = nil
	if w := serve(r, p, testInternalKey); w.Code != http.StatusUnauthorized {
		t.Fatalf("missing identity status %d", w.Code)
	}
}
