package http

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// Lane D10 — the person card's access rule: a current match, a live incoming
// spark, or someone in the viewer's own deck. Everything else is 404.

// personCardOf decodes the card out of the envelope.
func personCardOf(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode person card: %v (%s)", err, body)
	}
	return env.Data
}

// The pre-match projection: the deck card and the person card carry the
// description, the prompt answers, the languages and the gallery — and
// nothing that is meant to stay sealed until the two people match.
//
// This is the test the privacy projection is mutation-checked against: widen
// ProfileDetail (or copy a profile field into it wholesale) and the key-set
// assertion below fails.
func TestPreMatchDetailHoldsBackSealedFields(t *testing.T) {
	r, st, cleanup := setupTestRouter(t)
	defer cleanup()
	ctx := context.Background()

	lat, lng := uniqueTestPoint()
	viewer, candidate := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{viewer, candidate} {
		mustSeedActiveProfile(t, st, id)
	}
	// Pair them so the candidate is the viewer's deck, and give the candidate
	// every field that must NOT cross: sealed religion and community (lane
	// D9) and a real point (lane D7).
	gender := "d10p-" + uuid.NewString()[:8]
	bio := "Filter coffee and bad directions."
	religion, community := "hindu", "kamma"
	if _, err := st.UpsertProfile(ctx, candidate, store.UpsertProfileParams{
		Gender: &gender, Bio: &bio, Religion: &religion, Community: &community,
		Latitude: &lat, Longitude: &lng,
	}); err != nil {
		t.Fatalf("seed candidate: %v", err)
	}
	if _, err := st.UpsertProfile(ctx, viewer, store.UpsertProfileParams{Latitude: &lat, Longitude: &lng}); err != nil {
		t.Fatalf("seed viewer location: %v", err)
	}
	if _, err := st.UpsertPreferences(ctx, viewer, store.UpsertPreferencesParams{InterestedInGender: &gender}); err != nil {
		t.Fatalf("seed viewer preference: %v", err)
	}
	if _, err := st.UpsertPrompt(ctx, candidate, 1, "Dosa and no alarm."); err != nil {
		t.Fatalf("seed prompt: %v", err)
	}

	// The deck first — it is also what makes the person card readable.
	deckRec := contractDo(r, http.MethodGet, "/v1/dating/pulse/today", ``, viewer)
	if deckRec.Code != http.StatusOK {
		t.Fatalf("deck: status %d body %s", deckRec.Code, deckRec.Body.String())
	}
	cardRec := contractDo(r, http.MethodGet, "/v1/dating/people/"+candidate.String(), ``, viewer)
	if cardRec.Code != http.StatusOK {
		t.Fatalf("person card: status %d body %s", cardRec.Code, cardRec.Body.String())
	}

	for _, surface := range []struct {
		name string
		body []byte
	}{{"deck", deckRec.Body.Bytes()}, {"person card", cardRec.Body.Bytes()}} {
		// The founder's ask actually landed: the description crossed.
		if !strings.Contains(string(surface.body), bio) {
			t.Fatalf("%s does not carry the description: %s", surface.name, surface.body)
		}
		// And nothing that stays sealed until a match did. The sealed values
		// are checked as bare text (they must not appear at all); the rest as
		// JSON keys, so the Echoes ribbon's own "top_community" member — which
		// is null here and carries no activity — is not mistaken for a leak.
		for _, value := range []string{religion, community} {
			if strings.Contains(string(surface.body), value) {
				t.Fatalf("%s leaks the sealed value %q before a match: %s", surface.name, value, surface.body)
			}
		}
		for _, key := range []string{"religion", "community", "latitude", "longitude",
			"geohash", "last_active_at", "birth_date"} {
			if strings.Contains(string(surface.body), `"`+key+`":`) {
				t.Fatalf("%s leaks %q before a match: %s", surface.name, key, surface.body)
			}
		}
	}

	// The card's own member set is pinned too, so a field can only be added
	// to it by editing this list on purpose. city, intent, last_active_bucket
	// and last_active_label were added deliberately: all three are coarse
	// (a city name, an intent, a bucket code), never a coordinate and never a
	// timestamp.
	card := personCardOf(t, cardRec.Body.Bytes())
	allowedCard := map[string]bool{
		"user_id": true, "first_name": true, "age": true,
		"primary_photo_id": true, "primary_photo_url": true, "photo_state": true,
		"verified": true, "trust_tier": true,
		"distance_bucket": true, "distance_label": true,
		"city": true, "intent": true,
		"last_active_bucket": true, "last_active_label": true,
		"detail": true,
	}
	for key := range card {
		if !allowedCard[key] {
			t.Fatalf("person card carries an unexpected member %q: %s", key, cardRec.Body.String())
		}
	}

	// The detail block is exactly the four allowed members — a projection
	// that starts copying the profile wholesale fails here.
	detail, ok := card["detail"].(map[string]any)
	if !ok {
		t.Fatalf("person card has no detail block: %s", cardRec.Body.String())
	}
	allowed := map[string]bool{"bio": true, "prompts": true, "languages": true, "photos": true}
	for key := range detail {
		if !allowed[key] {
			t.Fatalf("detail carries an unexpected member %q: %s", key, cardRec.Body.String())
		}
	}
	if detail["bio"] != bio || detail["prompts"] == nil || detail["photos"] == nil {
		t.Fatalf("detail is missing what it should carry: %v", detail)
	}
}

func TestPersonCardAccessRule(t *testing.T) {
	r, st, cleanup := setupTestRouter(t)
	defer cleanup()
	ctx := context.Background()

	viewer, partner, sparker, stranger := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{viewer, partner, sparker, stranger} {
		mustSeedActiveProfile(t, st, id)
	}

	// A current match: readable, and the photo is full (lane D6).
	matchID, _, err := st.CreateOrGetOpenMatch(ctx, viewer, partner, map[string]any{"target_kind": "photo", "target_ref": "0"})
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if err := st.MarkMatchActive(ctx, matchID, uuid.New()); err != nil {
		t.Fatalf("activate: %v", err)
	}
	rec := contractDo(r, http.MethodGet, "/v1/dating/people/"+partner.String(), ``, viewer)
	if rec.Code != http.StatusOK {
		t.Fatalf("matched person: status %d body %s", rec.Code, rec.Body.String())
	}
	card := personCardOf(t, rec.Body.Bytes())
	if card["first_name"] != "Asha" || card["photo_state"] != "full" {
		t.Fatalf("matched card: %v", card)
	}
	if _, leaked := card["religion"]; leaked {
		t.Fatalf("person card carries religion: %v", card)
	}

	// A live incoming spark: readable.
	if _, err := st.CreateSpark(ctx, sparker, viewer, "photo", "0", ""); err != nil {
		t.Fatalf("seed spark: %v", err)
	}
	if rec := contractDo(r, http.MethodGet, "/v1/dating/people/"+sparker.String(), ``, viewer); rec.Code != http.StatusOK {
		t.Fatalf("incoming spark person: status %d body %s", rec.Code, rec.Body.String())
	}

	// A stranger with no relationship: 404.
	if rec := contractDo(r, http.MethodGet, "/v1/dating/people/"+stranger.String(), ``, viewer); rec.Code != http.StatusNotFound {
		t.Fatalf("stranger: status %d body %s; want 404", rec.Code, rec.Body.String())
	}
	// Yourself: 404 too (the profile route is the way to read your own card).
	if rec := contractDo(r, http.MethodGet, "/v1/dating/people/"+viewer.String(), ``, viewer); rec.Code != http.StatusNotFound {
		t.Fatalf("self: status %d; want 404", rec.Code)
	}

	// A block either way hides the person, even with a live spark.
	blocked := uuid.New()
	mustSeedActiveProfile(t, st, blocked)
	if _, err := st.CreateSpark(ctx, blocked, viewer, "photo", "0", ""); err != nil {
		t.Fatalf("seed spark: %v", err)
	}
	if err := st.BlockUser(ctx, blocked, viewer); err != nil {
		t.Fatalf("block: %v", err)
	}
	if rec := contractDo(r, http.MethodGet, "/v1/dating/people/"+blocked.String(), ``, viewer); rec.Code != http.StatusNotFound {
		t.Fatalf("blocked person: status %d; want 404", rec.Code)
	}
}

// TestMatchesAndSparksCarryPerson: the list responses carry the compact card.
func TestMatchesAndSparksCarryPerson(t *testing.T) {
	r, st, cleanup := setupTestRouter(t)
	defer cleanup()
	ctx := context.Background()
	viewer, partner, sparker := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{viewer, partner, sparker} {
		mustSeedActiveProfile(t, st, id)
	}
	matchID, _, err := st.CreateOrGetOpenMatch(ctx, viewer, partner, nil)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if err := st.MarkMatchActive(ctx, matchID, uuid.New()); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if _, err := st.CreateSpark(ctx, sparker, viewer, "prompt", "p1", ""); err != nil {
		t.Fatalf("seed spark: %v", err)
	}

	var list struct {
		Data []struct {
			ID     uuid.UUID `json:"id"`
			Person *struct {
				UserID     uuid.UUID `json:"user_id"`
				FirstName  string    `json:"first_name"`
				Age        int       `json:"age"`
				PhotoState string    `json:"photo_state"`
			} `json:"person"`
		} `json:"data"`
	}
	rec := contractDo(r, http.MethodGet, "/v1/dating/matches", ``, viewer)
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("matches: %v (%s)", err, rec.Body.String())
	}
	if len(list.Data) != 1 || list.Data[0].Person == nil {
		t.Fatalf("matches carry no person: %s", rec.Body.String())
	}
	if list.Data[0].Person.UserID != partner || list.Data[0].Person.Age < 18 || list.Data[0].Person.PhotoState != "full" {
		t.Fatalf("match person: %+v", *list.Data[0].Person)
	}

	// The match detail carries it too.
	rec = contractDo(r, http.MethodGet, "/v1/dating/matches/"+matchID.String(), ``, viewer)
	var detail struct {
		Data struct {
			Person *struct {
				UserID uuid.UUID `json:"user_id"`
			} `json:"person"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil || detail.Data.Person == nil || detail.Data.Person.UserID != partner {
		t.Fatalf("match detail person: %s", rec.Body.String())
	}

	rec = contractDo(r, http.MethodGet, "/v1/dating/sparks/incoming", ``, viewer)
	var sparks struct {
		Data []struct {
			FromUserID uuid.UUID `json:"from_user_id"`
			Person     *struct {
				UserID    uuid.UUID `json:"user_id"`
				FirstName string    `json:"first_name"`
			} `json:"person"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sparks); err != nil {
		t.Fatalf("incoming: %v (%s)", err, rec.Body.String())
	}
	if len(sparks.Data) != 1 || sparks.Data[0].Person == nil || sparks.Data[0].Person.UserID != sparker {
		t.Fatalf("incoming sparks carry no person: %s", rec.Body.String())
	}
}

// The card's coarse context: city, intent and the lane D7 last-active BUCKET.
// A person decides on this screen before sending a spark, so it shows the same
// context the deck card does — and no more: a city name, not a point; a bucket
// code, not a timestamp.
func TestPersonCardCarriesCityIntentAndLastActiveBucket(t *testing.T) {
	r, st, cleanup := setupTestRouter(t)
	defer cleanup()
	ctx := context.Background()

	viewer, partner := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{viewer, partner} {
		mustSeedActiveProfile(t, st, id)
	}
	// The seed hides last active (the default for a new profile), so this
	// person opts back in — the only way the bucket is ever shown.
	show := false
	if _, err := st.UpdatePrivacy(ctx, partner, store.PrivacyUpdate{HideLastActive: &show}); err != nil {
		t.Fatalf("show last active: %v", err)
	}
	matchID, _, err := st.CreateOrGetOpenMatch(ctx, viewer, partner, nil)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if err := st.MarkMatchActive(ctx, matchID, uuid.New()); err != nil {
		t.Fatalf("activate: %v", err)
	}

	rec := contractDo(r, http.MethodGet, "/v1/dating/people/"+partner.String(), ``, viewer)
	if rec.Code != http.StatusOK {
		t.Fatalf("person card: status %d body %s", rec.Code, rec.Body.String())
	}
	card := personCardOf(t, rec.Body.Bytes())
	if card["city"] != "Hyderabad" {
		t.Fatalf("card city = %v, want Hyderabad: %s", card["city"], rec.Body.String())
	}
	if card["intent"] != "casual" {
		t.Fatalf("card intent = %v, want casual: %s", card["intent"], rec.Body.String())
	}
	// The profile was written moments ago, so the bucket is today.
	if card["last_active_bucket"] != "today" {
		t.Fatalf("card last_active_bucket = %v, want today: %s", card["last_active_bucket"], rec.Body.String())
	}
	if label, _ := card["last_active_label"].(string); label == "" {
		t.Fatalf("card carries a bucket with no label: %s", rec.Body.String())
	}
	// A bucket, never the time itself, and never a coordinate.
	for _, key := range []string{"last_active_at", "latitude", "longitude", "geohash"} {
		if strings.Contains(rec.Body.String(), `"`+key+`":`) {
			t.Fatalf("person card leaks %q: %s", key, rec.Body.String())
		}
	}
}

// The owner's hide_last_active wins on EVERY surface that embeds the card,
// not just the one it was implemented on. The city and the intent still cross
// — hiding last active hides last active, nothing else.
func TestPersonCardHiddenLastActiveOmittedOnEverySurface(t *testing.T) {
	r, st, cleanup := setupTestRouter(t)
	defer cleanup()
	ctx := context.Background()

	viewer, partner, sparker := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{viewer, partner, sparker} {
		mustSeedActiveProfile(t, st, id)
	}
	// Both of them keep the default: last active hidden. Set it explicitly so
	// the test does not silently depend on the column default.
	hide := true
	for _, id := range []uuid.UUID{partner, sparker} {
		if _, err := st.UpdatePrivacy(ctx, id, store.PrivacyUpdate{HideLastActive: &hide}); err != nil {
			t.Fatalf("hide last active: %v", err)
		}
	}
	matchID, _, err := st.CreateOrGetOpenMatch(ctx, viewer, partner, nil)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if err := st.MarkMatchActive(ctx, matchID, uuid.New()); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if _, err := st.CreateSpark(ctx, sparker, viewer, "prompt", "p1", ""); err != nil {
		t.Fatalf("seed spark: %v", err)
	}
	// A trusted contact needs the current match, which viewer and partner have.
	if rec := contractDo(r, http.MethodPut, "/v1/dating/safety/trusted-contacts/"+partner.String(),
		`{"share_location_on_panic":true}`, viewer); rec.Code != http.StatusOK {
		t.Fatalf("trusted contact: status %d body %s", rec.Code, rec.Body.String())
	}

	for _, surface := range []struct{ name, path string }{
		{"person card", "/v1/dating/people/" + partner.String()},
		{"person card (sparker)", "/v1/dating/people/" + sparker.String()},
		{"match list", "/v1/dating/matches"},
		{"match detail", "/v1/dating/matches/" + matchID.String()},
		{"incoming sparks", "/v1/dating/sparks/incoming"},
		{"trusted contacts", "/v1/dating/safety/trusted-contacts"},
	} {
		rec := contractDo(r, http.MethodGet, surface.path, ``, viewer)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d body %s", surface.name, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		for _, key := range []string{"last_active_bucket", "last_active_label", "last_active_at"} {
			if strings.Contains(body, `"`+key+`":`) {
				t.Fatalf("%s carries %q though the owner hides last active: %s", surface.name, key, body)
			}
		}
		// The rest of the card is still there, so this is a suppression and
		// not an empty response that would pass the check above by accident.
		for _, key := range []string{"city", "intent"} {
			if !strings.Contains(body, `"`+key+`":`) {
				t.Fatalf("%s lost %q: %s", surface.name, key, body)
			}
		}
	}
}
