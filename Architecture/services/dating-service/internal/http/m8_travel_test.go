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

// Mechanic M8 — travel mode (DATING_TRAVEL_ENABLED): golden fixtures and the
// guards behind them. Same harness as m1_deck_test.go. The viewer's pool
// lives in Hyderabad (17.385, 78.4867); Mumbai's centre is 19.08, 72.88.

var m8Fixtures = []string{
	"travel_get_200",
	"travel_put_200",
	"travel_put_403_requires_pass",
	"travel_put_400_invalid_city",
	"travel_get_404_not_enabled",
	"pulse_today_get_200_travelling",
}

func m8Config(on bool) service.MechanicsConfig {
	return service.MechanicsConfig{DeckRefill: true, DeckDailyLimitFree: 50, DeckDailyLimitPass: 50, Travel: on}
}

// grantPassTo gives any user an unexpired pass.
func (d *m1Deck) grantPassTo(id uuid.UUID) {
	d.exec(`INSERT INTO dating_premium_subscriptions (user_id, plan, started_at, expires_at, source)
        VALUES ($1, 'pass_30d', now(), now() + interval '10 days', 'test')
        ON CONFLICT (user_id) DO UPDATE SET expires_at = EXCLUDED.expires_at`, id)
}

func (d *m1Deck) travel(user uuid.UUID, body string) int {
	d.t.Helper()
	return contractDo(d.env.r, http.MethodPut, "/v1/dating/travel", body, user).Code
}

// mumbaiCandidate seeds someone in the pool who lives in Mumbai (located
// once: a second location change would hit the change limits).
func (d *m1Deck) mumbaiCandidate() uuid.UUID {
	d.t.Helper()
	ctx := context.Background()
	id := uuid.New()
	mustSeedActiveProfile(d.t, d.env.st, id)
	lat, lng := 19.08, 72.88
	if _, err := d.env.st.UpsertProfile(ctx, id, store.UpsertProfileParams{Gender: &d.gender, Latitude: &lat, Longitude: &lng, City: ptrStr("Mumbai")}); err != nil {
		d.t.Fatalf("seed Mumbai candidate: %v", err)
	}
	return id
}

func ptrStr(s string) *string { return &s }

func TestM8TravelContracts(t *testing.T) {
	d := newM1Deck(t, m8Config(true))
	labels := map[uuid.UUID]string{d.viewer: "<viewer>"}
	assertContract(t, contractDo(d.env.r, http.MethodGet, "/v1/dating/travel", ``, d.viewer), http.StatusOK, "travel_get_200", labels)
	assertContract(t, contractDo(d.env.r, http.MethodPut, "/v1/dating/travel", `{"city":"mumbai","days":3}`, d.viewer),
		http.StatusForbidden, "travel_put_403_requires_pass", labels)
	assertContract(t, contractDo(d.env.r, http.MethodPut, "/v1/dating/travel", `{"city":"atlantis","days":3}`, d.viewer),
		http.StatusBadRequest, "travel_put_400_invalid_city", labels)
	d.grantPass()
	assertContract(t, contractDo(d.env.r, http.MethodPut, "/v1/dating/travel", `{"city":"mumbai","days":3}`, d.viewer),
		http.StatusOK, "travel_put_200", labels)

	// Someone from Mumbai travelling to Hyderabad, seen by the viewer at
	// home: the card says Hyderabad and travelling, never Mumbai.
	home := newM1Deck(t, m8Config(true))
	traveller := home.mumbaiCandidate()
	home.grantPassTo(traveller)
	if code := home.travel(traveller, `{"city":"hyderabad","days":2}`); code != http.StatusOK {
		t.Fatalf("traveller start: %d", code)
	}
	rec := contractDo(home.env.r, http.MethodGet, "/v1/dating/pulse/today", ``, home.viewer)
	d7AssertBucketsOnly(t, "travelling deck", rec.Body.Bytes())
	assertContract(t, rec, http.StatusOK, "pulse_today_get_200_travelling", map[uuid.UUID]string{home.viewer: "<viewer>", traveller: "<traveller>"})

	off := newM1Deck(t, m8Config(false))
	assertContract(t, contractDo(off.env.r, http.MethodGet, "/v1/dating/travel", ``, off.viewer),
		http.StatusNotFound, "travel_get_404_not_enabled", map[uuid.UUID]string{off.viewer: "<viewer>"})
}

func TestM8ContractFixturesWellFormed(t *testing.T) {
	for _, name := range m8Fixtures {
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
		for _, leak := range []string{"latitude", "longitude", "geohash", "distance_km"} {
			if strings.Contains(string(raw), leak) {
				t.Fatalf("%s: carries %q", name, leak)
			}
		}
	}
}

// Guard: a traveller browses the destination, not home.
func TestTravellerBrowsesTheDestination(t *testing.T) {
	d := newM1Deck(t, m8Config(true))
	local, mumbai := d.candidate(), d.mumbaiCandidate()
	if ids, _ := d.deck(); !ids[local] || ids[mumbai] {
		t.Fatalf("home deck = %v, want only the Hyderabad candidate", ids)
	}
	d.grantPass()
	if code := d.travel(d.viewer, `{"city":"mumbai","days":3}`); code != http.StatusOK {
		t.Fatalf("start trip: %d", code)
	}
	if ids, _ := d.deck(); ids[local] || !ids[mumbai] {
		t.Fatalf("travelling deck = %v, want only the Mumbai candidate", ids)
	}
	// Back home.
	if rec := contractDo(d.env.r, http.MethodDelete, "/v1/dating/travel", ``, d.viewer); rec.Code != http.StatusOK {
		t.Fatalf("end trip: %d", rec.Code)
	}
	if ids, _ := d.deck(); !ids[local] || ids[mumbai] {
		t.Fatalf("deck after the trip = %v, want home again", ids)
	}
}

// Guard: in other decks a traveller appears at the destination with the
// marker, and neither their home city nor their real point is ever sent.
func TestTravellerAppearsAtTheDestinationOnly(t *testing.T) {
	d := newM1Deck(t, m8Config(true))
	traveller := d.mumbaiCandidate()
	if ids, _ := d.deck(); ids[traveller] {
		t.Fatalf("a Mumbai resident is in a Hyderabad deck before travelling")
	}
	d.grantPassTo(traveller)
	if code := d.travel(traveller, `{"city":"hyderabad","days":2}`); code != http.StatusOK {
		t.Fatalf("start trip: %d", code)
	}
	rec := contractDo(d.env.r, http.MethodGet, "/v1/dating/pulse/today", ``, d.viewer)
	body := rec.Body.String()
	if !strings.Contains(body, traveller.String()) || !strings.Contains(body, `"travelling":true`) {
		t.Fatalf("the traveller is missing from the destination deck or unmarked: %s", body)
	}
	if strings.Contains(body, "Mumbai") {
		t.Fatalf("the deck card names the traveller's home city: %s", body)
	}
	card := contractDo(d.env.r, http.MethodGet, "/v1/dating/people/"+traveller.String(), ``, d.viewer).Body.String()
	if !strings.Contains(card, `"travelling":true`) || !strings.Contains(card, `"city":"Hyderabad"`) || strings.Contains(card, "Mumbai") {
		t.Fatalf("person card = %s, want Hyderabad, travelling, and no Mumbai", card)
	}
}

// Guard: travel needs a pass to start, and stops when the pass runs out.
func TestTravelNeedsAPassAndStopsWithIt(t *testing.T) {
	d := newM1Deck(t, m8Config(true))
	local, mumbai := d.candidate(), d.mumbaiCandidate()
	if code := d.travel(d.viewer, `{"city":"mumbai","days":3}`); code != http.StatusForbidden {
		t.Fatalf("start without a pass: %d, want 403", code)
	}
	d.grantPass()
	if code := d.travel(d.viewer, `{"city":"mumbai","days":8}`); code != http.StatusBadRequest {
		t.Fatalf("an eight-day trip: %d, want 400", code)
	}
	if code := d.travel(d.viewer, `{"city":"mumbai","days":7}`); code != http.StatusOK {
		t.Fatalf("a seven-day trip: %d", code)
	}
	d.exec(`UPDATE dating_premium_subscriptions SET expires_at = now() - interval '1 minute' WHERE user_id = $1`, d.viewer)
	if ids, _ := d.deck(); !ids[local] || ids[mumbai] {
		t.Fatalf("deck after the pass ran out = %v, want home", ids)
	}
	got := contractDo(d.env.r, http.MethodGet, "/v1/dating/travel", ``, d.viewer).Body.String()
	if strings.Contains(got, `"active"`) || !strings.Contains(got, `"available":false`) {
		t.Fatalf("travel state after the pass ran out = %s", got)
	}
}

// Guard: with the flag off, a stored trip changes nothing.
func TestTravelFlagOffIgnoresTrips(t *testing.T) {
	d := newM1Deck(t, m8Config(true))
	local, mumbai := d.candidate(), d.mumbaiCandidate()
	d.grantPass()
	if code := d.travel(d.viewer, `{"city":"mumbai","days":3}`); code != http.StatusOK {
		t.Fatalf("start trip: %d", code)
	}
	d.env.svc.SetMechanicsConfig(m8Config(false))
	if ids, _ := d.deck(); !ids[local] || ids[mumbai] {
		t.Fatalf("flag off: deck = %v, want home", ids)
	}
	d.wantRefusal(contractDo(d.env.r, http.MethodGet, "/v1/dating/travel", ``, d.viewer), http.StatusNotFound, "MECHANIC_NOT_ENABLED")
}

// Guard: with the flag off, someone else's stored trip does not move them
// into a deck either.
func TestTravelFlagOffIgnoresOtherPeoplesTrips(t *testing.T) {
	d := newM1Deck(t, m8Config(true))
	traveller := d.mumbaiCandidate()
	d.grantPassTo(traveller)
	if code := d.travel(traveller, `{"city":"hyderabad","days":2}`); code != http.StatusOK {
		t.Fatalf("start trip: %d", code)
	}
	if ids, _ := d.deck(); !ids[traveller] {
		t.Fatalf("flag on: the traveller is missing from the destination deck")
	}
	d.env.svc.SetMechanicsConfig(m8Config(false))
	if ids, _ := d.deck(); ids[traveller] {
		t.Fatalf("flag off: the traveller is still placed at the destination")
	}
}
