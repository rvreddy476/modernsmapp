package http

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// Lane D10 — GET /v1/dating/verification/status.
func TestVerificationStatus(t *testing.T) {
	r, st, cleanup := setupTestRouter(t)
	defer cleanup()
	ctx := context.Background()

	type statusBody struct {
		Data struct {
			Selfie struct {
				State             string `json:"state"`
				AttemptsLeftToday int    `json:"attempts_left_today"`
				AttemptsPerDay    int    `json:"attempts_per_day"`
				WindowHours       int    `json:"window_hours"`
			} `json:"selfie"`
			TrustTier     string `json:"trust_tier"`
			Verified      bool   `json:"verified"`
			ProfileStatus string `json:"profile_status"`
			NextStep      string `json:"next_step"`
		} `json:"data"`
	}
	read := func(t *testing.T, user uuid.UUID) statusBody {
		t.Helper()
		rec := contractDo(r, http.MethodGet, "/v1/dating/verification/status", ``, user)
		if rec.Code != http.StatusOK {
			t.Fatalf("status: %d body %s", rec.Code, rec.Body.String())
		}
		var out statusBody
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v (%s)", err, rec.Body.String())
		}
		return out
	}

	// Nobody has submitted anything yet.
	fresh := uuid.New()
	mustSeedProfile(t, st, fresh)
	got := read(t, fresh)
	if got.Data.Selfie.State != "none" || got.Data.NextStep != "submit_selfie" {
		t.Fatalf("fresh status = %+v", got.Data)
	}
	if got.Data.Selfie.AttemptsLeftToday != got.Data.Selfie.AttemptsPerDay || got.Data.Selfie.WindowHours != 24 {
		t.Fatalf("fresh attempts = %+v", got.Data.Selfie)
	}
	if got.Data.Verified {
		t.Fatalf("a fresh profile is verified: %+v", got.Data)
	}

	// A moderator review is reported as "review".
	inReview := uuid.New()
	mustSeedProfile(t, st, inReview)
	if err := st.RecordSelfieAttempt(ctx, inReview, 0.85, store.SelfieStatusPendingReview); err != nil {
		t.Fatalf("seed review: %v", err)
	}
	if got := read(t, inReview); got.Data.Selfie.State != "review" || got.Data.NextStep != "wait_for_review" {
		t.Fatalf("review status = %+v", got.Data)
	}

	// A passed selfie: verified, nothing left to do.
	passed := uuid.New()
	mustSeedActiveProfile(t, st, passed)
	got = read(t, passed)
	if got.Data.Selfie.State != "passed" || got.Data.NextStep != "none" {
		t.Fatalf("passed status = %+v", got.Data)
	}
	if got.Data.ProfileStatus != "active" {
		t.Fatalf("passed profile status = %+v", got.Data)
	}
	// The badge follows the trust tier, which the selfie pass steps up.
	if err := st.UpdateTrustTier(ctx, passed, "selfie"); err != nil {
		t.Fatalf("trust tier: %v", err)
	}
	if got := read(t, passed); !got.Data.Verified || got.Data.TrustTier != "selfie" {
		t.Fatalf("passed badge = %+v", got.Data)
	}

	// A failed selfie leaves the user with attempts and the same next step.
	failed := uuid.New()
	mustSeedProfile(t, st, failed)
	if err := st.RecordSelfieAttempt(ctx, failed, 0.10, store.SelfieStatusFailed); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	if got := read(t, failed); got.Data.Selfie.State != "failed" || got.Data.NextStep != "submit_selfie" {
		t.Fatalf("failed status = %+v", got.Data)
	}
}
