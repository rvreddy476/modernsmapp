package http

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// Lane D10 — envelope consistency: /pulse/today is {data, meta}, and the
// empty photo and prompt lists are [] rather than null.
func TestPulseTodayEnvelope(t *testing.T) {
	r, st, cleanup := setupTestRouter(t)
	defer cleanup()
	user := uuid.New()
	mustSeedActiveProfile(t, st, user)

	rec := contractDo(r, http.MethodGet, "/v1/dating/pulse/today", ``, user)
	if rec.Code != http.StatusOK {
		t.Fatalf("pulse: %d body %s", rec.Code, rec.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if _, ok := raw["data"]; !ok {
		t.Fatalf("no data member: %s", rec.Body.String())
	}
	if _, ok := raw["meta"]; !ok {
		t.Fatalf("no meta member: %s", rec.Body.String())
	}
	var body struct {
		Data []map[string]any `json:"data"`
		Meta struct {
			GeneratedAt string `json:"generated_at"`
			Size        int    `json:"size"`
			CohortGated bool   `json:"cohort_gated"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if body.Data == nil {
		t.Fatalf("data is null, want []: %s", rec.Body.String())
	}
	if body.Meta.GeneratedAt == "" || body.Meta.Size != len(body.Data) {
		t.Fatalf("meta = %+v for %d cards", body.Meta, len(body.Data))
	}
}

// Empty photo and prompt lists are [] (the app renders them without a null
// check).
func TestEmptyListsAreArrays(t *testing.T) {
	r, st, cleanup := setupTestRouter(t)
	defer cleanup()
	user := uuid.New()
	mustSeedProfile(t, st, user)
	for _, path := range []string{"/v1/dating/photos", "/v1/dating/prompts", "/v1/dating/photos/me"} {
		rec := contractDo(r, http.MethodGet, path, ``, user)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d body %s", path, rec.Code, rec.Body.String())
		}
		var body struct {
			Data []map[string]any `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: decode %v (%s)", path, err, rec.Body.String())
		}
		if body.Data == nil {
			t.Fatalf("%s: data is null, want []: %s", path, rec.Body.String())
		}
	}
}
