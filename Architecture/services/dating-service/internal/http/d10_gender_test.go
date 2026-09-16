package http

import (
	"context"
	"encoding/json"
	"math/rand"
	"net/http"
	"testing"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// seedActiveProfileGender is mustSeedActiveProfile with the gender, the
// discovery preference and the point chosen by the caller (lane D10).
func seedActiveProfileGender(t *testing.T, st *store.Store, id uuid.UUID, gender, interested string, lat, lng float64) {
	t.Helper()
	ctx := context.Background()
	// The caller's point scopes the deck (see uniqueTestPoint).
	intent, city := "casual", "Testville"
	if _, err := st.UpsertProfile(ctx, id, store.UpsertProfileParams{Intent: &intent, Gender: &gender, City: &city, Latitude: &lat, Longitude: &lng}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	if _, err := st.SetProfileBirthDate(ctx, id, time.Date(1995, 1, 1, 0, 0, 0, 0, time.UTC), store.BasicsSourceIdentity); err != nil {
		t.Fatalf("seed birth date: %v", err)
	}
	if _, err := st.SetProfileFirstName(ctx, id, "Asha", store.BasicsSourceIdentity); err != nil {
		t.Fatalf("seed first name: %v", err)
	}
	if _, err := st.UpsertPreferences(ctx, id, store.UpsertPreferencesParams{InterestedInGender: &interested}); err != nil {
		t.Fatalf("seed preferences: %v", err)
	}
	photo, err := st.CreatePhoto(ctx, id, store.CreatePhotoParams{MediaID: uuid.New(), IsPrimary: true, Visibility: "public"})
	if err != nil {
		t.Fatalf("seed photo: %v", err)
	}
	if _, err := st.SetPhotoModerationStatus(ctx, photo.ID, "approved", ""); err != nil {
		t.Fatalf("approve photo: %v", err)
	}
	if err := st.RecordSelfieAttempt(ctx, id, 0.99, "passed"); err != nil {
		t.Fatalf("seed selfie: %v", err)
	}
	for _, ev := range []store.ProfileEvent{store.ProfileEventBasicsComplete, store.ProfileEventPhotoApproved, store.ProfileEventSelfiePassed} {
		if _, err := st.TransitionProfileStatus(ctx, id, ev, store.ProfileActorSystem); err != nil {
			t.Fatalf("seed transition %s: %v", ev, err)
		}
	}
}

// deckIDs returns the candidate ids in the viewer's deck.
func deckIDs(t *testing.T, r http.Handler, viewer uuid.UUID) map[uuid.UUID]bool {
	t.Helper()
	rec := contractDo(r, http.MethodGet, "/v1/dating/pulse/today", ``, viewer)
	if rec.Code != http.StatusOK {
		t.Fatalf("pulse: status %d body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data []struct {
			CandidateID uuid.UUID `json:"candidate_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode pulse: %v (%s)", err, rec.Body.String())
	}
	out := map[uuid.UUID]bool{}
	for _, c := range body.Data {
		out[c.CandidateID] = true
	}
	return out
}

// The gender rule holds in both directions, and "everyone" means no filter.
func TestGenderPreferenceIsTwoWay(t *testing.T) {
	r, st, cleanup := setupTestRouter(t)
	defer cleanup()

	lat, lng := uniqueTestPoint()
	viewer := uuid.New()
	seedActiveProfileGender(t, st, viewer, "man", "woman", lat, lng)

	mutual, oneWay, everyone, wrongGender := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seedActiveProfileGender(t, st, mutual, "woman", "man", lat, lng)        // both ways: in
	seedActiveProfileGender(t, st, oneWay, "woman", "nonbinary", lat, lng)  // she does not want men: out
	seedActiveProfileGender(t, st, everyone, "woman", "everyone", lat, lng) // no filter on her side: in
	seedActiveProfileGender(t, st, wrongGender, "man", "man", lat, lng)     // the viewer does not want men: out

	deck := deckIDs(t, r, viewer)
	if !deck[mutual] {
		t.Fatalf("a mutual preference is not in the deck")
	}
	if !deck[everyone] {
		t.Fatalf("an everyone candidate is not in the deck")
	}
	if deck[oneWay] {
		t.Fatalf("a candidate whose own preference excludes the viewer is in the deck")
	}
	if deck[wrongGender] {
		t.Fatalf("a candidate the viewer's preference excludes is in the deck")
	}

	// Lane D10: someone in the viewer's deck is readable as a person card.
	rec := contractDo(r, http.MethodGet, "/v1/dating/people/"+mutual.String(), ``, viewer)
	if rec.Code != http.StatusOK {
		t.Fatalf("deck member person card: status %d body %s", rec.Code, rec.Body.String())
	}
	rec = contractDo(r, http.MethodGet, "/v1/dating/people/"+oneWay.String(), ``, viewer)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a non-deck stranger is readable: status %d body %s", rec.Code, rec.Body.String())
	}
}

// An "everyone" viewer sees every gender that admits them.
func TestInterestedInEveryoneDropsTheGenderFilter(t *testing.T) {
	r, st, cleanup := setupTestRouter(t)
	defer cleanup()
	lat, lng := uniqueTestPoint()
	viewer := uuid.New()
	seedActiveProfileGender(t, st, viewer, "nonbinary", "everyone", lat, lng)
	woman, man, picky := uuid.New(), uuid.New(), uuid.New()
	seedActiveProfileGender(t, st, woman, "woman", "everyone", lat, lng)
	seedActiveProfileGender(t, st, man, "man", "nonbinary", lat, lng)
	seedActiveProfileGender(t, st, picky, "woman", "man", lat, lng)

	deck := deckIDs(t, r, viewer)
	if !deck[woman] {
		t.Fatalf("everyone viewer misses a woman")
	}
	if !deck[man] {
		t.Fatalf("everyone viewer misses a man who wants nonbinary")
	}
	if deck[picky] {
		t.Fatalf("everyone viewer sees someone who only wants men")
	}
}

// The preference enum is validated on write.
func TestPreferencesRejectAnUnknownGender(t *testing.T) {
	r, st, cleanup := setupTestRouter(t)
	defer cleanup()
	user := uuid.New()
	mustSeedProfile(t, st, user)
	for _, bad := range []string{`"female"`, `"male"`, `"anything"`, `""`} {
		rec := contractDo(r, http.MethodPut, "/v1/dating/preferences", `{"interested_in_gender":`+bad+`}`, user)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("interested_in_gender %s was accepted: status %d body %s", bad, rec.Code, rec.Body.String())
		}
	}
	for _, good := range []string{"woman", "man", "nonbinary", "everyone"} {
		rec := contractDo(r, http.MethodPut, "/v1/dating/preferences", `{"interested_in_gender":"`+good+`"}`, user)
		if rec.Code != http.StatusOK {
			t.Fatalf("interested_in_gender %s was refused: status %d body %s", good, rec.Code, rec.Body.String())
		}
	}
}

// uniqueTestPoint is a random snapped point. Seeding a test's profiles at
// their own point makes the geohash prefilter scope that test's deck to that
// test's own profiles, so neither another test nor an earlier run of this
// one can crowd the deck's diversity cap.
func uniqueTestPoint() (float64, float64) {
	return float64(rand.Intn(8000))/100 - 40, float64(rand.Intn(30000))/100 - 150
}
