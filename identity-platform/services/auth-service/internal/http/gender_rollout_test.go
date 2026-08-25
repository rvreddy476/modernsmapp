package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/rollout"
	"github.com/atpost/identity-auth-service/internal/service"
)

// Registration payload with every mandatory field EXCEPT gender.
func bodyWithoutGender() string {
	return `{` +
		`"email":"nogender@b.com",` +
		`"password":"secret",` +
		`"first_name":"Raghu",` +
		`"last_name":"Varan",` +
		`"dob":"1990-01-01",` +
		`"accepted_terms":true,` +
		`"terms_version":"` + service.CurrentTermsVersion + `"}`
}

func bodyWithGender(gender string) string {
	return `{` +
		`"email":"withgender@b.com",` +
		`"password":"secret",` +
		`"first_name":"Raghu",` +
		`"last_name":"Varan",` +
		`"dob":"1990-01-01",` +
		`"gender":"` + gender + `",` +
		`"accepted_terms":true,` +
		`"terms_version":"` + service.CurrentTermsVersion + `"}`
}

// registerWith builds a handler whose gender flag is forced to want, with no
// Redis, and posts body to /v1/auth/register.
func registerWith(t *testing.T, want bool, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()

	h := New(&stubAuthService{
		registerWithConsentFn: func(
			phone, email, password, firstName, lastName, dob, gender string,
			consent service.RegistrationConsent,
		) (*service.AuthResponse, error) {
			return &service.AuthResponse{RequiresVerification: true}, nil
		},
	}, &config.Config{}, nil, nil)

	// nil Redis means the resolver always returns its configured default,
	// which is exactly the deterministic control a contract test needs.
	h.flags = rollout.New(nil, nil, map[string]bool{
		rollout.FlagRegisterRequireGender: want,
	})
	h.RegisterRoutes(r, noopMiddleware(), noopMiddleware())

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/register", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	return resp
}

func errorCode(t *testing.T, resp *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("response is not the standard envelope: %s", resp.Body.String())
	}
	return envelope.Error.Code
}

// TestGenderRolloutExpandPhase — flag OFF is the DEFAULT and the rollback
// target. A client that has never heard of `gender` must keep registering.
//
// This is the regression guard for the incident this flag exists to prevent:
// gender was promoted to required in place, which broke every Flutter
// registration with no way back short of a redeploy.
func TestGenderRolloutExpandPhase(t *testing.T) {
	resp := registerWith(t, false, bodyWithoutGender())

	if resp.Code != http.StatusCreated {
		t.Fatalf("flag OFF: expected %d for a registration with no gender, got %d: %s",
			http.StatusCreated, resp.Code, resp.Body.String())
	}
}

// TestGenderRolloutContractPhase — flag ON rejects absence.
func TestGenderRolloutContractPhase(t *testing.T) {
	resp := registerWith(t, true, bodyWithoutGender())

	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("flag ON: expected %d for a registration with no gender, got %d: %s",
			http.StatusUnprocessableEntity, resp.Code, resp.Body.String())
	}
	if got := errorCode(t, resp); got != "GENDER_REQUIRED" {
		t.Fatalf("flag ON: expected GENDER_REQUIRED, got %q", got)
	}
}

// TestGenderValueValidatedInBothPhases — a SUPPLIED value is always checked.
//
// Accepting arbitrary text while the flag is off would just move the problem
// to the backfill: the column would fill with values the contract phase then
// has to reconcile.
func TestGenderValueValidatedInBothPhases(t *testing.T) {
	for _, flagOn := range []bool{false, true} {
		resp := registerWith(t, flagOn, bodyWithGender("banana"))

		if resp.Code != http.StatusUnprocessableEntity {
			t.Fatalf("flag=%v: expected %d for an invalid gender, got %d: %s",
				flagOn, http.StatusUnprocessableEntity, resp.Code, resp.Body.String())
		}
		if got := errorCode(t, resp); got != "GENDER_INVALID" {
			t.Fatalf("flag=%v: expected GENDER_INVALID, got %q", flagOn, got)
		}
	}
}

// TestGenderAcceptedValuesInBothPhases — the closed set works either way.
func TestGenderAcceptedValuesInBothPhases(t *testing.T) {
	for _, flagOn := range []bool{false, true} {
		for _, g := range []string{"male", "female", "other"} {
			resp := registerWith(t, flagOn, bodyWithGender(g))
			if resp.Code != http.StatusCreated {
				t.Fatalf("flag=%v gender=%q: expected %d, got %d: %s",
					flagOn, g, http.StatusCreated, resp.Code, resp.Body.String())
			}
		}
	}
}

// TestGenderIsCaseAndSpaceInsensitive — clients differ; the stored token
// must not.
func TestGenderIsCaseAndSpaceInsensitive(t *testing.T) {
	for _, g := range []string{"Male", "  female  ", "OTHER"} {
		resp := registerWith(t, true, bodyWithGender(g))
		if resp.Code != http.StatusCreated {
			t.Fatalf("gender=%q: expected %d, got %d: %s",
				g, http.StatusCreated, resp.Code, resp.Body.String())
		}
	}
}

// TestGenderErrorCarriesAllowedValues — the client cannot guess the closed
// set, so the rejection has to state it.
func TestGenderErrorCarriesAllowedValues(t *testing.T) {
	resp := registerWith(t, true, bodyWithoutGender())

	var envelope struct {
		Error struct {
			Details struct {
				Allowed []string `json:"allowed"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("unparseable envelope: %s", resp.Body.String())
	}
	if len(envelope.Error.Details.Allowed) != 3 {
		t.Fatalf("expected the allowed set in error.details, got %v",
			envelope.Error.Details.Allowed)
	}
}
