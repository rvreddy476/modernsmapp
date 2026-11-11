package http

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/google/uuid"
)

// premiumBody is what shared/identityroles.Client.GrantUntil sends.
func premiumBody(userID, svc, expires string) string {
	b := `{"user_id":"` + userID + `","role":"premium","service":"` + svc + `","reason":"pass PP-1"`
	if expires != "" {
		b += `,"expires_at":"` + expires + `"`
	}
	return b + `}`
}

// TestInternalGrantForwardsExpiresAt: expires_at reaches the service layer
// as the exact instant sent; an ecosystem grant forwards none.
func TestInternalGrantForwardsExpiresAt(t *testing.T) {
	stub := &stubAuthService{}
	r := internalRouter(t, stub)
	target := uuid.New().String()

	exp := time.Now().Add(30 * 24 * time.Hour).UTC().Truncate(time.Second)
	resp := internalRoleCall(t, r, http.MethodPost, premiumBody(target, "monetization-service", exp.Format(time.RFC3339)), testInternalKey)
	if resp.Code != http.StatusOK {
		t.Fatalf("got %d (body %s)", resp.Code, resp.Body.String())
	}
	if stub.lastGrantExpiresAt == nil || !stub.lastGrantExpiresAt.Equal(exp) {
		t.Fatalf("forwarded expires_at = %v, want %v", stub.lastGrantExpiresAt, exp)
	}

	resp = internalRoleCall(t, r, http.MethodPost, grantBody(target, "seller"), testInternalKey)
	if resp.Code != http.StatusOK || stub.lastGrantExpiresAt != nil {
		t.Fatalf("seller grant: code %d expires_at %v, want 200 and nil", resp.Code, stub.lastGrantExpiresAt)
	}
}

// TestInternalGrantPremiumErrorCodes pins the stable codes the shared client
// and monetization's worker classify on.
func TestInternalGrantPremiumErrorCodes(t *testing.T) {
	target := uuid.New().String()
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"missing expiry", service.ErrExpiryRequired, http.StatusBadRequest, "EXPIRES_AT_REQUIRED"},
		{"past expiry", service.ErrInvalidExpiry, http.StatusBadRequest, "INVALID_EXPIRY"},
		{"expiry on ecosystem role", service.ErrExpiryNotAllowed, http.StatusBadRequest, "EXPIRES_AT_NOT_ALLOWED"},
		// ROLE_NOT_GRANTABLE: the shared client treats it as a permanent
		// caller bug and dead-letters instead of retrying forever.
		{"wrong service", service.ErrTimedRoleWrongService, http.StatusForbidden, "ROLE_NOT_GRANTABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubAuthService{
				grantEcosystemFn: func(string, uuid.UUID, string, string) error { return tc.err },
			}
			r := internalRouter(t, stub)
			resp := internalRoleCall(t, r, http.MethodPost, premiumBody(target, "commerce-service", ""), testInternalKey)
			if resp.Code != tc.status {
				t.Fatalf("status %d want %d (body %s)", resp.Code, tc.status, resp.Body.String())
			}
			var env struct {
				Error struct {
					Code    string `json:"code"`
					Details struct {
						TimedRoleOwner map[string]string `json:"timed_role_owner"`
					} `json:"details"`
				} `json:"error"`
			}
			if err := json.Unmarshal(resp.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if env.Error.Code != tc.code {
				t.Fatalf("code %q want %q", env.Error.Code, tc.code)
			}
			if tc.code == "ROLE_NOT_GRANTABLE" && env.Error.Details.TimedRoleOwner["premium"] != "monetization-service" {
				t.Fatalf("details = %+v, want timed_role_owner.premium = monetization-service", env.Error.Details)
			}
		})
	}
}

// TestInternalRevokePremiumOverHTTP: the internal revoke accepts premium.
func TestInternalRevokePremiumOverHTTP(t *testing.T) {
	var gotRole, gotService string
	stub := &stubAuthService{
		revokeEcosystemFn: func(svc string, _ uuid.UUID, role, _ string) error {
			gotService, gotRole = svc, role
			return nil
		},
	}
	r := internalRouter(t, stub)
	resp := internalRoleCall(t, r, http.MethodDelete, premiumBody(uuid.New().String(), "monetization-service", ""), testInternalKey)
	if resp.Code != http.StatusOK || gotRole != "premium" || gotService != "monetization-service" {
		t.Fatalf("code %d role %q service %q (body %s)", resp.Code, gotRole, gotService, resp.Body.String())
	}
}
