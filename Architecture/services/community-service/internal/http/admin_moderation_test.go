// Suspend and unsuspend on the admin-service token family
// (admin_moderation.go). No database: the recording fake proves admission,
// the actor and body handed down, and the error mapping. The transaction and
// its audit row are in admin_moderation_integration_test.go (CI).
package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/community-service/internal/service"
	"github.com/atpost/community-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func suspendPath(id string) string { return InternalAdminPrefix + "/communities/" + id + "/suspend" }
func unsuspendPath(id string) string {
	return InternalAdminPrefix + "/communities/" + id + "/unsuspend"
}

func TestCommunitySuspend_AdmittedActorIsActAndBodyHandedDown(t *testing.T) {
	rg := newAdminTokenRig(t)
	id := uuid.New()
	hdr := forgedEdge(uuid.New()) // a forged edge actor rides along and must be ignored
	hdr[ServiceAuthHeader] = "Bearer " + rg.mint(t, rg.admin, AudienceChat, []string{PermCommunitiesModerate}, rg.actor.String())
	w := adminServe(rg.r, http.MethodPost, suspendPath(id.String()), `{"reason_code":"spam","note":"bot ring"}`, hdr)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", w.Code, w.Body.String())
	}
	if len(rg.be.suspensions) != 1 {
		t.Fatalf("backend calls=%d, want 1", len(rg.be.suspensions))
	}
	got := rg.be.suspensions[0]
	if !got.suspend || got.actor != rg.actor || got.community != id || got.reasonCode != "spam" || got.note != "bot ring" {
		t.Fatalf("handed down %+v, want suspend by %s of %s with spam/bot ring", got, rg.actor, id)
	}
	var body struct {
		Data store.CommunitySuspensionChange `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Data.Status != "suspended" || !body.Data.Changed {
		t.Fatalf("response %s (err %v), want the change", w.Body.String(), err)
	}
}

func TestCommunityUnsuspend_AdmittedNoteHandedDown(t *testing.T) {
	rg := newAdminTokenRig(t)
	id := uuid.New()
	w := adminServe(rg.r, http.MethodPost, unsuspendPath(id.String()), `{"note":"appeal granted"}`,
		bearer(rg.mint(t, rg.admin, AudienceChat, []string{PermCommunitiesModerate}, rg.actor.String())))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", w.Code, w.Body.String())
	}
	if len(rg.be.suspensions) != 1 || rg.be.suspensions[0].suspend || rg.be.suspensions[0].note != "appeal granted" || rg.be.suspensions[0].actor != rg.actor {
		t.Fatalf("handed down %+v", rg.be.suspensions)
	}
}

// Only chat:communities.moderate opens these routes; the internal key and a
// forged X-User-Id / X-Scopes are no evidence, and the report permission is
// not the moderation permission.
func TestCommunitySuspend_Refusals(t *testing.T) {
	rg := newAdminTokenRig(t)
	id := uuid.NewString()
	body := `{"reason_code":"spam","note":"x"}`
	cases := []struct {
		name     string
		hdr      map[string]string
		wantCode int
		wantErr  string
	}{
		{"no token, key and forged staff headers", forgedEdge(uuid.New()), http.StatusUnauthorized, CodeAdminTokenRequired},
		{"report permission only", bearer(rg.mint(t, rg.admin, AudienceChat, []string{PermReportsAct}, rg.actor.String())), http.StatusForbidden, CodeAdminPermissionScope},
		{"no act", bearer(rg.mint(t, rg.admin, AudienceChat, []string{PermCommunitiesModerate}, "")), http.StatusForbidden, CodeAdminActorRequired},
		{"non-admin caller", bearer(rg.mint(t, rg.notifier, AudienceChat, []string{PermCommunitiesModerate}, rg.actor.String())), http.StatusForbidden, CodeServiceTokenRejected},
		{"wrong audience", bearer(rg.mint(t, rg.admin, "food", []string{PermCommunitiesModerate}, rg.actor.String())), http.StatusForbidden, CodeServiceTokenRejected},
	}
	for _, tc := range cases {
		for _, path := range []string{suspendPath(id), unsuspendPath(id)} {
			t.Run(tc.name+" "+path, func(t *testing.T) {
				rg.be.calls = 0
				w := adminServe(rg.r, http.MethodPost, path, body, tc.hdr)
				if w.Code != tc.wantCode || errorCode(t, w) != tc.wantErr {
					t.Fatalf("status=%d code=%q, want %d %s", w.Code, errorCode(t, w), tc.wantCode, tc.wantErr)
				}
				if rg.be.calls != 0 {
					t.Fatal("a refused request reached the backend")
				}
			})
		}
	}
}

// The backend's answers reach admin-service with the statuses it maps.
func TestCommunitySuspend_ErrorMapping(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{store.ErrAdminNotFound, http.StatusNotFound},
		{fmt.Errorf("community is archived: %w", store.ErrAdminConflict), http.StatusConflict},
		{errors.New("invalid: reason_code is required"), http.StatusUnprocessableEntity},
		{store.ErrAdminActorRequired, http.StatusForbidden},
		{errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		rg := newAdminTokenRig(t)
		rg.be.err = tc.err
		w := adminServe(rg.r, http.MethodPost, suspendPath(uuid.NewString()), `{"reason_code":"spam"}`,
			bearer(rg.mint(t, rg.admin, AudienceChat, []string{PermCommunitiesModerate}, rg.actor.String())))
		if w.Code != tc.want {
			t.Errorf("%v: status=%d, want %d", tc.err, w.Code, tc.want)
		}
	}
}

func TestCommunitySuspend_BadInput(t *testing.T) {
	rg := newAdminTokenRig(t)
	tok := bearer(rg.mint(t, rg.admin, AudienceChat, []string{PermCommunitiesModerate}, rg.actor.String()))
	if w := adminServe(rg.r, http.MethodPost, suspendPath("not-a-uuid"), `{"reason_code":"spam"}`, tok); w.Code != http.StatusBadRequest {
		t.Fatalf("bad id: status=%d, want 400", w.Code)
	}
	if w := adminServe(rg.r, http.MethodPost, suspendPath(uuid.NewString()), `{`, tok); w.Code != http.StatusBadRequest {
		t.Fatalf("bad JSON: status=%d, want 400", w.Code)
	}
	if rg.be.calls != 0 {
		t.Fatal("bad input reached the backend")
	}
}

// With no backend wired the routes answer 503, never 200.
func TestCommunitySuspend_NoBackend(t *testing.T) {
	rg := newAdminTokenRig(t)
	r := adminTokenRouter(nil, rg.v, adminTestInternalKey)
	w := adminServe(r, http.MethodPost, suspendPath(uuid.NewString()), `{"reason_code":"spam"}`,
		bearer(rg.mint(t, rg.admin, AudienceChat, []string{PermCommunitiesModerate}, rg.actor.String())))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503", w.Code)
	}
}

func serveServiceError(err error) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	handleServiceError(c, err)
	return w
}

// A member-side write on a suspended community is a 403 the client can name.
func TestCommunitySuspendedWriteMapsToItsCode(t *testing.T) {
	for _, err := range []error{service.ErrCommunitySuspended, fmt.Errorf("x: %w", service.ErrCommunitySuspended)} {
		w := serveServiceError(err)
		if w.Code != http.StatusForbidden || errorCode(t, w) != CodeCommunitySuspended {
			t.Fatalf("%v: status=%d code=%q, want 403 %s", err, w.Code, errorCode(t, w), CodeCommunitySuspended)
		}
	}
}
