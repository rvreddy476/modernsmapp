package http

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// Mechanic M6 — filters (DATING_FILTERS_V2_ENABLED): golden fixtures and the
// guards behind them. Same harness as m1_deck_test.go.

var m6Fixtures = []string{
	"profile_options_get_200",
	"preferences_get_200_filters",
	"preferences_put_200_filters",
	"preferences_put_403_filters_require_pass",
	"preferences_put_400_invalid_distance_bucket",
	"profile_upsert_400_invalid_interest",
	"profile_upsert_400_invalid_height",
	"privacy_patch_403_filters_require_pass",
	"person_get_200_basics",
}

func m6Config(on bool) service.MechanicsConfig {
	return service.MechanicsConfig{DeckRefill: true, DeckDailyLimitFree: 50, DeckDailyLimitPass: 50, FiltersV2: on}
}

func (d *m1Deck) setBasics(id uuid.UUID, body string) {
	d.t.Helper()
	if rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/profile", body, id); rec.Code != http.StatusOK {
		d.t.Fatalf("basics for %s: %d %s", id, rec.Code, rec.Body.String())
	}
}

func (d *m1Deck) putPrefs(body string) int {
	d.t.Helper()
	return contractDo(d.env.r, http.MethodPut, "/v1/dating/preferences", body, d.viewer).Code
}

func TestM6FiltersContracts(t *testing.T) {
	d := newM1Deck(t, m6Config(true))
	labels := map[uuid.UUID]string{d.viewer: "<viewer>"}

	assertContract(t, contractDo(d.env.r, http.MethodGet, "/v1/dating/profile/options", ``, d.viewer), http.StatusOK, "profile_options_get_200", nil)

	assertContract(t, contractDo(d.env.r, http.MethodPut, "/v1/dating/preferences", `{"pass_filters":{"verified_only":true}}`, d.viewer),
		http.StatusForbidden, "preferences_put_403_filters_require_pass", labels)
	assertContract(t, contractDo(d.env.r, http.MethodPut, "/v1/dating/preferences", `{"distance_bucket":"far"}`, d.viewer),
		http.StatusBadRequest, "preferences_put_400_invalid_distance_bucket", labels)
	assertContract(t, contractDo(d.env.r, http.MethodPatch, "/v1/dating/profile/privacy", `{"verified_only_filter":true}`, d.viewer),
		http.StatusForbidden, "privacy_patch_403_filters_require_pass", labels)
	assertContract(t, contractDo(d.env.r, http.MethodPost, "/v1/dating/profile", `{"interests":["skydiving"]}`, d.viewer),
		http.StatusBadRequest, "profile_upsert_400_invalid_interest", labels)
	assertContract(t, contractDo(d.env.r, http.MethodPost, "/v1/dating/profile", `{"height_cm":300}`, d.viewer),
		http.StatusBadRequest, "profile_upsert_400_invalid_height", labels)

	d.grantPass()
	body := `{"min_age":24,"max_age":34,"distance_bucket":"km_5_10","intent_filter":["serious"],"pass_filters":{"verified_only":true,"min_height_cm":160,"max_height_cm":190,"languages":["en","te"],"drinking":["never","socially"],"smoking":["never"],"exercise":[],"diet":["vegetarian"]}}`
	// The pool gender is random per test; the fixture names it <gender>.
	assertContract(t, d7Rewrite(contractDo(d.env.r, http.MethodPut, "/v1/dating/preferences", body, d.viewer), d.gender, "<gender>"),
		http.StatusOK, "preferences_put_200_filters", labels)
	assertContract(t, d7Rewrite(contractDo(d.env.r, http.MethodGet, "/v1/dating/preferences", ``, d.viewer), d.gender, "<gender>"),
		http.StatusOK, "preferences_get_200_filters", labels)

	// The basics on a card the viewer may open (someone in their deck).
	c := d.candidate()
	d.setBasics(c, `{"interests":["books","cricket","yoga"],"height_cm":172,"drinking":"socially","smoking":"never","exercise":"often","diet":"vegetarian","language_prefs":["en","te"]}`)
	if _, err := d.env.st.UpsertPreferences(context.Background(), d.viewer, store.UpsertPreferencesParams{MinAge: ptrInt(18), MaxAge: ptrInt(60), IntentFilter: []string{}}); err != nil {
		t.Fatal(err)
	}
	if err := d.env.st.SetPassFilters(context.Background(), d.viewer, store.PassFilters{}); err != nil {
		t.Fatal(err)
	}
	if ids, _ := d.deck(); !ids[c] {
		t.Fatalf("candidate not in the deck: %v", ids)
	}
	labels[c] = "<candidate>"
	assertContract(t, contractDo(d.env.r, http.MethodGet, "/v1/dating/people/"+c.String(), ``, d.viewer), http.StatusOK, "person_get_200_basics", labels)
}

func TestM6ContractFixturesWellFormed(t *testing.T) {
	for _, name := range m6Fixtures {
		raw, err := os.ReadFile(filepath.Join(contractsDir, name+".json"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: not JSON: %v", name, err)
		}
		if contractUUIDRe.Match(raw) || contractTimeRe.Match(raw) {
			t.Fatalf("%s: carries a raw uuid or timestamp", name)
		}
		if strings.Contains(string(raw), "latitude") || strings.Contains(string(raw), "religion") || strings.Contains(string(raw), "community") {
			t.Fatalf("%s: carries a precise location or a sealed field", name)
		}
	}
}

func ptrInt(v int) *int { return &v }

// Guard: a free user cannot set a pass filter (verified only, height,
// languages, basics, or the old language_filter field), but may clear them.
func TestFiltersFreeUserCannotSetPassFilters(t *testing.T) {
	d := newM1Deck(t, m6Config(true))
	for _, body := range []string{
		`{"pass_filters":{"verified_only":true}}`,
		`{"pass_filters":{"min_height_cm":160}}`,
		`{"pass_filters":{"languages":["en"]}}`,
		`{"pass_filters":{"diet":["vegan"]}}`,
		`{"language_filter":["en"]}`,
	} {
		if code := d.putPrefs(body); code != http.StatusForbidden {
			t.Fatalf("%s: status %d, want 403", body, code)
		}
	}
	if code := d.putPrefs(`{"pass_filters":{}}`); code != http.StatusOK {
		t.Fatalf("clearing the pass filters: status %d, want 200", code)
	}
	// The free filters stay free.
	if code := d.putPrefs(`{"distance_bucket":"lt_5_km","min_age":21,"max_age":30,"intent_filter":["casual"]}`); code != http.StatusOK {
		t.Fatalf("free filters: status %d", code)
	}
}

// Guard: the pass filters apply to the deck while the pass lasts, and stop
// (without being forgotten) when it runs out.
func TestFiltersApplyOnlyWithAPass(t *testing.T) {
	d := newM1Deck(t, m6Config(true))
	tall, short, wine := d.candidate(), d.candidate(), d.candidate()
	d.setBasics(tall, `{"height_cm":185,"drinking":"never","language_prefs":["te"]}`)
	d.setBasics(short, `{"height_cm":150,"drinking":"never","language_prefs":["te"]}`)
	d.setBasics(wine, `{"height_cm":185,"drinking":"regularly","language_prefs":["te"]}`)
	d.grantPass()
	if code := d.putPrefs(`{"pass_filters":{"min_height_cm":170,"drinking":["never"],"languages":["te"]}}`); code != http.StatusOK {
		t.Fatalf("set filters: %d", code)
	}
	ids, _ := d.deck()
	if !ids[tall] || ids[short] || ids[wine] {
		t.Fatalf("filtered deck = %v; want only %s", ids, tall)
	}
	// Pass runs out: everyone again, and the filters are still stored.
	d.exec(`UPDATE dating_premium_subscriptions SET expires_at = now() - interval '1 day' WHERE user_id = $1`, d.viewer)
	d.env.svc.InvalidatePulseCache(context.Background(), d.viewer)
	ids, _ = d.deck()
	if !ids[tall] || !ids[short] || !ids[wine] {
		t.Fatalf("deck after the pass ran out = %v; want all three", ids)
	}
	f, err := d.env.st.GetPassFilters(context.Background(), d.viewer)
	if err != nil || f.MinHeightCm == nil || *f.MinHeightCm != 170 {
		t.Fatalf("stored filters after expiry = %+v err=%v", f, err)
	}
}

// Guard: verified only is a pass filter while the flag is on — from the
// preferences or the old privacy toggle — and free while it is off.
func TestFiltersVerifiedOnly(t *testing.T) {
	setTier := func(d *m1Deck, id uuid.UUID, tier string) {
		d.exec(`UPDATE dating_profiles SET trust_tier = $2 WHERE user_id = $1`, id, tier)
	}
	on := newM1Deck(t, m6Config(true))
	phone, selfie := on.candidate(), on.candidate()
	setTier(on, phone, "phone")
	setTier(on, selfie, "selfie")
	on.exec(`UPDATE dating_profiles SET verified_only_filter = true WHERE user_id = $1`, on.viewer)
	if ids, _ := on.deck(); !ids[phone] || !ids[selfie] {
		t.Fatalf("flag on, no pass: a stored toggle filtered the deck: %v", ids)
	}
	on.grantPass()
	on.env.svc.InvalidatePulseCache(context.Background(), on.viewer)
	if ids, _ := on.deck(); ids[phone] || !ids[selfie] {
		t.Fatalf("flag on, pass: deck = %v, want verified only", ids)
	}

	off := newM1Deck(t, m6Config(false))
	p2, s2 := off.candidate(), off.candidate()
	setTier(off, p2, "phone")
	setTier(off, s2, "selfie")
	if rec := contractDo(off.env.r, http.MethodPatch, "/v1/dating/profile/privacy", `{"verified_only_filter":true}`, off.viewer); rec.Code != http.StatusOK {
		t.Fatalf("flag off: the free toggle was refused: %d", rec.Code)
	}
	if ids, _ := off.deck(); ids[p2] || !ids[s2] {
		t.Fatalf("flag off: deck = %v, want the free verified-only toggle applied", ids)
	}
}

// Guard: the new profile fields are validated against the fixed lists.
func TestProfileFieldsValidation(t *testing.T) {
	d := newM1Deck(t, m6Config(true))
	for body, code := range map[string]string{
		`{"interests":["skydiving"]}`: "INVALID_INTEREST",
		`{"interests":["art","books","chai","coding","comedy","cooking","cricket","cycling","dancing","film","yoga"]}`: "TOO_MANY_INTEREST",
		`{"interests":["art","art"]}`:    "INVALID_INTEREST",
		`{"language_prefs":["english"]}`: "INVALID_LANGUAGE",
		`{"height_cm":90}`:               "INVALID_HEIGHT",
		`{"drinking":"lots"}`:            "INVALID_LIFESTYLE",
		`{"diet":"carnivore"}`:           "INVALID_LIFESTYLE",
	} {
		d.wantRefusal(contractDo(d.env.r, http.MethodPost, "/v1/dating/profile", body, d.viewer), http.StatusBadRequest, code)
	}
	d.setBasics(d.viewer, `{"interests":["art","yoga"],"height_cm":170,"drinking":"never","language_prefs":["en"]}`)
	p, err := d.env.st.GetProfile(context.Background(), d.viewer)
	if err != nil || len(p.Interests) != 2 || p.HeightCm == nil || *p.HeightCm != 170 {
		t.Fatalf("profile after valid basics = %+v err=%v", p, err)
	}
}

// Guard: the distance bucket sets the radius, and reads back as a bucket.
func TestFiltersDistanceBucketRoundTrip(t *testing.T) {
	d := newM1Deck(t, m6Config(true))
	for bucket, km := range map[string]int{"lt_5_km": 5, "km_5_10": 10, "km_10_25": 25, "gt_25_km": service.MaxDistanceKm} {
		if code := d.putPrefs(`{"distance_bucket":"` + bucket + `"}`); code != http.StatusOK {
			t.Fatalf("%s: %d", bucket, code)
		}
		p, err := d.env.st.GetPreferences(context.Background(), d.viewer)
		if err != nil || p.DistanceKm != km {
			t.Fatalf("%s: distance_km = %d err=%v, want %d", bucket, p.DistanceKm, err, km)
		}
		rec := contractDo(d.env.r, http.MethodGet, "/v1/dating/preferences", ``, d.viewer)
		if !strings.Contains(rec.Body.String(), `"distance_bucket":"`+bucket+`"`) {
			t.Fatalf("%s: GET = %s", bucket, rec.Body.String())
		}
	}
	// With the flag off, the new fields are refused rather than ignored.
	off := newM1Deck(t, m6Config(false))
	off.wantRefusal(contractDo(off.env.r, http.MethodPut, "/v1/dating/preferences", `{"distance_bucket":"lt_5_km"}`, off.viewer), http.StatusNotFound, "MECHANIC_NOT_ENABLED")
}
