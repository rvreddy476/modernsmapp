package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/atpost/dating-service/internal/service"
	"github.com/google/uuid"
)

// Mechanic M12 — dealbreakers (DATING_DEALBREAKERS_ENABLED): golden
// fixtures and the guards behind them. Same harness as m1_deck_test.go.

var m12Fixtures = []string{
	"preferences_put_200_dealbreakers",
	"preferences_get_200_dealbreakers",
	"preferences_put_400_invalid_dealbreaker",
	"preferences_put_403_dealbreakers_require_pass",
	"preferences_put_404_dealbreakers_not_enabled",
}

func m12Config(on bool) service.MechanicsConfig {
	return service.MechanicsConfig{DeckRefill: true, DeckDailyLimitFree: 50, DeckDailyLimitPass: 50, Picks: true, Dealbreakers: on}
}

func TestM12DealbreakersContracts(t *testing.T) {
	d := newM1Deck(t, m12Config(true))
	labels := map[uuid.UUID]string{d.viewer: "<viewer>"}
	put := func(body string) *httptest.ResponseRecorder {
		return d7Rewrite(contractDo(d.env.r, http.MethodPut, "/v1/dating/preferences", body, d.viewer), d.gender, "<gender>")
	}
	assertContract(t, put(`{"min_age":25,"max_age":35,"dealbreakers":["age","intent"]}`), http.StatusOK, "preferences_put_200_dealbreakers", labels)
	assertContract(t, d7Rewrite(contractDo(d.env.r, http.MethodGet, "/v1/dating/preferences", ``, d.viewer), d.gender, "<gender>"),
		http.StatusOK, "preferences_get_200_dealbreakers", labels)
	assertContract(t, put(`{"dealbreakers":["religion"]}`), http.StatusBadRequest, "preferences_put_400_invalid_dealbreaker", labels)
	assertContract(t, put(`{"dealbreakers":["age","diet"]}`), http.StatusForbidden, "preferences_put_403_dealbreakers_require_pass", labels)

	off := newM1Deck(t, m12Config(false))
	assertContract(t, contractDo(off.env.r, http.MethodPut, "/v1/dating/preferences", `{"dealbreakers":["age"]}`, off.viewer),
		http.StatusNotFound, "preferences_put_404_dealbreakers_not_enabled", map[uuid.UUID]string{off.viewer: "<viewer>"})
}

// Guard: a candidate whose dealbreaker the viewer fails is in neither the
// viewer's deck nor their picks; with the flag off, or with the same
// preference not marked as a dealbreaker, they are.
func TestDealbreakersKeepTheViewerOut(t *testing.T) {
	for _, tc := range []struct {
		name   string
		on     bool
		marked bool
		want   bool
	}{
		{"marked, flag on", true, true, false},
		{"marked, flag off", false, true, true},
		{"not marked, flag on", true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newM1Deck(t, m12Config(tc.on))
			open, picky := d.candidate(), d.candidate()
			// The viewer is casual; picky wants marriage only.
			d.exec(`UPDATE dating_preferences SET intent_filter = '{marriage}' WHERE user_id = $1`, picky)
			if tc.marked {
				d.exec(`UPDATE dating_preferences SET dealbreakers = '{intent}' WHERE user_id = $1`, picky)
			}
			deck, _ := d.deck()
			if !deck[open] {
				t.Fatalf("the open candidate is not in the deck")
			}
			if deck[picky] != tc.want {
				t.Fatalf("picky in the deck = %v, want %v", deck[picky], tc.want)
			}
			if got := picked(d.picks("UTC").ids(), picky); got != tc.want {
				t.Fatalf("picky in the picks = %v, want %v", got, tc.want)
			}
		})
	}
}

// Guard: a pass dealbreaker counts only while its owner holds a pass.
func TestPassDealbreakerNeedsAPass(t *testing.T) {
	d := newM1Deck(t, m12Config(true))
	picky := d.candidate()
	d.exec(`UPDATE dating_preferences SET diet_filter = '{vegan}', dealbreakers = '{diet}' WHERE user_id = $1`, picky)
	if deck, _ := d.deck(); !deck[picky] {
		t.Fatalf("a pass dealbreaker without a pass kept the viewer out")
	}
	d.exec(`INSERT INTO dating_premium_subscriptions (user_id, plan, started_at, expires_at, source)
        VALUES ($1, 'pass_30d', now(), now() + interval '10 days', 'test')
        ON CONFLICT (user_id) DO UPDATE SET expires_at = EXCLUDED.expires_at`, picky)
	d.exec(`UPDATE dating_profiles SET diet = 'vegetarian' WHERE user_id = $1`, d.viewer)
	d.env.svc.InvalidatePulseCache(t.Context(), d.viewer)
	if deck, _ := d.deck(); deck[picky] {
		t.Fatalf("a held pass dealbreaker did not keep the viewer out")
	}
}

func TestM12ContractFixturesWellFormed(t *testing.T) {
	for _, name := range m12Fixtures {
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
	}
}
