package http

import (
	"net/http"
	"testing"
)

func TestAdminGrievanceCreateRequiresActPermission(t *testing.T) {
	rg := newAdminTokenRig(t)
	read := rg.mint(t, rg.admin, AudienceTrustSafety, []string{PermGrievancesRead}, rg.actor.String())
	w := serveAdmin(rg.r, http.MethodPost, InternalAdminPrefix+"/grievances", `{"subject":"other","description":"short"}`, bearer(read))
	if w.Code != http.StatusForbidden {
		t.Fatalf("reader could create: %d %s", w.Code, w.Body.String())
	}
	act := rg.mint(t, rg.admin, AudienceTrustSafety, []string{PermGrievancesAct}, rg.actor.String())
	// A signed actor is sufficient; no X-User-Id is supplied. Validation is
	// reached before touching the store, proving admission without impersonation.
	w = serveAdmin(rg.r, http.MethodPost, InternalAdminPrefix+"/grievances", `{"subject":"other","description":"short"}`, bearer(act))
	if w.Code != http.StatusBadRequest || adminErrorCode(w) != "BAD_REQUEST" {
		t.Fatalf("actor not accepted: %d %s", w.Code, w.Body.String())
	}
}
