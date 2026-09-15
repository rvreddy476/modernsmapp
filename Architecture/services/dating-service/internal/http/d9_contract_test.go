package http

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

// Golden contract fixtures for lane D9 (consent): the consents view, a
// withdrawal, the 422 for a sensitive field and for the selfie check without
// consent, and the unknown-type 400. Same harness as d3_contract_test.go;
// regenerate with UPDATE_CONTRACTS=1 and review.

var d9Fixtures = []string{
	"consents_get_200",
	"consent_put_200_withdrawn",
	"consent_put_400_invalid_type",
	"profile_upsert_422_consent_required",
	"selfie_challenge_422_consent_required",
}

func TestD9Contracts(t *testing.T) {
	r, st, cleanup := setupTestRouter(t)
	defer cleanup()

	t.Run("consents_get_200", func(t *testing.T) {
		u := uuid.New()
		mustSeedProfile(t, st, u)
		rec := contractDo(r, http.MethodPut, "/v1/dating/consents/sensitive_religion", `{"granted":true}`, u)
		if rec.Code != http.StatusOK {
			t.Fatalf("grant: %d %s", rec.Code, rec.Body.String())
		}
		rec = contractDo(r, http.MethodGet, "/v1/dating/consents", ``, u)
		assertContract(t, rec, http.StatusOK, "consents_get_200", map[uuid.UUID]string{u: "<user>"})
	})

	t.Run("consent_put_200_withdrawn", func(t *testing.T) {
		u := uuid.New()
		mustSeedActiveProfile(t, st, u)
		_ = contractDo(r, http.MethodPut, "/v1/dating/consents/sensitive_community", `{"granted":true}`, u)
		rec := contractDo(r, http.MethodPost, "/v1/dating/profile", `{"community":"Kodava"}`, u)
		if rec.Code != http.StatusOK {
			t.Fatalf("community with consent: %d %s", rec.Code, rec.Body.String())
		}
		rec = contractDo(r, http.MethodPut, "/v1/dating/consents/sensitive_community", `{"granted":false}`, u)
		assertContract(t, rec, http.StatusOK, "consent_put_200_withdrawn", map[uuid.UUID]string{u: "<user>"})
		rec = contractDo(r, http.MethodGet, "/v1/dating/profile", ``, u)
		var body struct {
			Data map[string]any `json:"data"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if _, still := body.Data["community"]; still {
			t.Fatalf("community still on the profile after withdrawal: %s", rec.Body.String())
		}
	})

	t.Run("consent_put_400_invalid_type", func(t *testing.T) {
		u := uuid.New()
		rec := contractDo(r, http.MethodPut, "/v1/dating/consents/marketing", `{"granted":true}`, u)
		assertContract(t, rec, http.StatusBadRequest, "consent_put_400_invalid_type", map[uuid.UUID]string{u: "<user>"})
	})

	t.Run("profile_upsert_422_consent_required", func(t *testing.T) {
		u := uuid.New()
		mustSeedProfile(t, st, u)
		rec := contractDo(r, http.MethodPost, "/v1/dating/profile", `{"religion":"Jain"}`, u)
		assertContract(t, rec, http.StatusUnprocessableEntity, "profile_upsert_422_consent_required", map[uuid.UUID]string{u: "<user>"})
	})

	t.Run("selfie_challenge_422_consent_required", func(t *testing.T) {
		u := uuid.New()
		mustSeedProfile(t, st, u)
		rec := contractDo(r, http.MethodPost, "/v1/dating/verification/selfie/challenge", ``, u)
		assertContract(t, rec, http.StatusUnprocessableEntity, "selfie_challenge_422_consent_required", map[uuid.UUID]string{u: "<user>"})
	})
}

// TestD9ContractFixturesWellFormed runs without a database.
func TestD9ContractFixturesWellFormed(t *testing.T) {
	for _, name := range d9Fixtures {
		raw, err := os.ReadFile(filepath.Join(contractsDir, name+".json"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: not JSON: %v", name, err)
		}
		if _, ok := doc["data"]; !ok {
			if _, ok := doc["error"]; !ok {
				t.Fatalf("%s: neither data nor error", name)
			}
		}
		if contractUUIDRe.Match(raw) || contractTimeRe.Match(raw) {
			t.Fatalf("%s: carries a raw uuid or timestamp", name)
		}
	}
}
