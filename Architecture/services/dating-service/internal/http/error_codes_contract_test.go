package http

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Golden contract fixtures for the client-facing validation refusals that
// used to come back as the generic INVALID_REQUEST. Same harness as
// d10_contract_test.go (setupD10, assertContract, UPDATE_CONTRACTS=1), so
// these cases need TEST_PG_DSN and a *_test database.
//
// Every code here is pinned by a fixture: if a message, a details key or a
// status moves, the fixture fails and the client contract change is a
// deliberate edit rather than a silent drift.

var errorCodeFixtures = []string{
	"preferences_put_400_invalid_age_range",
	"preferences_put_400_invalid_distance_km",
	"preferences_put_400_invalid_intent_filter",
	"profile_upsert_400_invalid_intent",
	"photos_post_400_invalid_visibility",
	"prompts_put_400_unknown_prompt",
	"prompts_put_400_answer_required",
	"prompts_put_400_answer_too_long",
	"pulse_pass_400_reason_too_long",
	"spark_create_409_onboarding_incomplete",
}

func TestValidationErrorCodeContracts(t *testing.T) {
	env := setupD10(t)
	r, st := env.r, env.st

	t.Run("preferences", func(t *testing.T) {
		user := uuid.New()
		seedD10Profile(t, st, user)
		labels := map[uuid.UUID]string{user: "<user>"}

		assertContract(t, contractDo(r, http.MethodPut, "/v1/dating/preferences", `{"min_age":16}`, user),
			http.StatusBadRequest, "preferences_put_400_invalid_age_range", labels)
		// The ceiling and the inverted range share INVALID_AGE_RANGE and
		// differ only in the message naming the bound that was broken.
		for _, body := range []string{`{"max_age":130}`, `{"min_age":40,"max_age":30}`} {
			rec := contractDo(r, http.MethodPut, "/v1/dating/preferences", body, user)
			if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "INVALID_AGE_RANGE" {
				t.Fatalf("%s: status %d code %q, want 400 INVALID_AGE_RANGE (body %s)",
					body, rec.Code, errorCode(t, rec), rec.Body.String())
			}
		}

		assertContract(t, contractDo(r, http.MethodPut, "/v1/dating/preferences", `{"distance_km":900}`, user),
			http.StatusBadRequest, "preferences_put_400_invalid_distance_km", labels)
		// 0 km is refused by the same floor.
		if rec := contractDo(r, http.MethodPut, "/v1/dating/preferences", `{"distance_km":0}`, user); errorCode(t, rec) != "INVALID_DISTANCE_KM" {
			t.Fatalf("distance_km=0: code %q, want INVALID_DISTANCE_KM (body %s)", errorCode(t, rec), rec.Body.String())
		}

		assertContract(t, contractDo(r, http.MethodPut, "/v1/dating/preferences", `{"intent_filter":["situationship"]}`, user),
			http.StatusBadRequest, "preferences_put_400_invalid_intent_filter", labels)
	})

	t.Run("profile_intent", func(t *testing.T) {
		user := uuid.New()
		seedD10Profile(t, st, user)
		labels := map[uuid.UUID]string{user: "<user>"}
		assertContract(t, contractDo(r, http.MethodPost, "/v1/dating/profile", `{"intent":"situationship"}`, user),
			http.StatusBadRequest, "profile_upsert_400_invalid_intent", labels)
	})

	t.Run("photo_visibility", func(t *testing.T) {
		user := uuid.New()
		mustSeedProfile(t, st, user)
		labels := map[uuid.UUID]string{user: "<user>"}
		attach := `{"media_id":"` + uuid.New().String() + `","visibility":"friends_only"}`
		assertContract(t, contractDo(r, http.MethodPost, "/v1/dating/photos", attach, user),
			http.StatusBadRequest, "photos_post_400_invalid_visibility", labels)
		// The edit path refuses the same value with the same code, before
		// the photo is even looked up.
		rec := contractDo(r, http.MethodPatch, "/v1/dating/photos/"+uuid.NewString(), `{"visibility":"friends_only"}`, user)
		if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "INVALID_VISIBILITY" {
			t.Fatalf("PATCH photo: status %d code %q, want 400 INVALID_VISIBILITY (body %s)",
				rec.Code, errorCode(t, rec), rec.Body.String())
		}
	})

	t.Run("prompts", func(t *testing.T) {
		user := uuid.New()
		mustSeedProfile(t, st, user)
		labels := map[uuid.UUID]string{user: "<user>"}
		assertContract(t, contractDo(r, http.MethodPut, "/v1/dating/prompts/999", `{"answer":"Filter coffee."}`, user),
			http.StatusBadRequest, "prompts_put_400_unknown_prompt", labels)
		assertContract(t, contractDo(r, http.MethodPut, "/v1/dating/prompts/1", `{"answer":"   "}`, user),
			http.StatusBadRequest, "prompts_put_400_answer_required", labels)
		long := `{"answer":"` + strings.Repeat("a", 281) + `"}`
		assertContract(t, contractDo(r, http.MethodPut, "/v1/dating/prompts/1", long, user),
			http.StatusBadRequest, "prompts_put_400_answer_too_long", labels)
	})

	t.Run("pass_reason", func(t *testing.T) {
		viewer, candidate := uuid.New(), uuid.New()
		mustSeedActiveProfile(t, st, viewer)
		mustSeedActiveProfile(t, st, candidate)
		labels := map[uuid.UUID]string{viewer: "<viewer>", candidate: "<candidate>"}
		body := `{"reason":"` + strings.Repeat("b", 201) + `"}`
		assertContract(t, contractDo(r, http.MethodPost, "/v1/dating/pulse/"+candidate.String()+"/pass", body, viewer),
			http.StatusBadRequest, "pulse_pass_400_reason_too_long", labels)
	})

	t.Run("onboarding_incomplete", func(t *testing.T) {
		// seedD10Basics stops before the selfie: an adult whose onboarding
		// is not finished, so the spark gate — not the age gate — refuses.
		sender, recipient := uuid.New(), uuid.New()
		seedD10Basics(t, st, sender)
		seedD10Profile(t, st, recipient)
		labels := map[uuid.UUID]string{sender: "<sender>", recipient: "<recipient>"}
		assertContract(t, contractDo(r, http.MethodPost, "/v1/dating/sparks", sparkBody(recipient, "prompt-1"), sender),
			http.StatusConflict, "spark_create_409_onboarding_incomplete", labels)
	})
}

// TestValidationErrorCodeFixturesWellFormed runs without a database: every
// fixture exists, is JSON carrying an error member with a stable code that
// is not the generic INVALID_REQUEST, and holds no raw id or timestamp.
func TestValidationErrorCodeFixturesWellFormed(t *testing.T) {
	for _, name := range errorCodeFixtures {
		raw, err := os.ReadFile(filepath.Join(contractsDir, name+".json"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var doc struct {
			Error *struct {
				Code    string         `json:"code"`
				Message string         `json:"message"`
				Details map[string]any `json:"details"`
			} `json:"error"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: not JSON: %v", name, err)
		}
		if doc.Error == nil {
			t.Fatalf("%s: no error member", name)
		}
		if doc.Error.Code == "" || doc.Error.Code == "INVALID_REQUEST" {
			t.Fatalf("%s: code = %q, want a stable code", name, doc.Error.Code)
		}
		if doc.Error.Message == "" {
			t.Fatalf("%s: empty message", name)
		}
		if len(doc.Error.Details) == 0 {
			t.Fatalf("%s: details must carry the allowed values or the limit", name)
		}
		if contractUUIDRe.Match(raw) || contractTimeRe.Match(raw) {
			t.Fatalf("%s: carries a raw uuid or timestamp", name)
		}
	}
}
