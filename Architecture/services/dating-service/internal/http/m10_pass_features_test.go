package http

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/atpost/dating-service/internal/service"
	"github.com/google/uuid"
)

// Mechanic M10 (part 2): a pass lists the features of the mechanics that are
// on, in the catalogue and in premium/me, and nothing else.

func allMechanics() service.MechanicsConfig {
	return service.MechanicsConfig{
		DeckRefill: true, DeckDailyLimitFree: 25, DeckDailyLimitPass: 100,
		Rewind: true, RewindDailyLimitFree: 1,
		SuperSpark: true, SuperSparkDailyLimitFree: 1, SuperSparkDailyLimitPass: 5,
		LikedYouGate: true, FirstMove: true, FiltersV2: true, Picks: true, Travel: true,
		ReadReceipts: true, CallAfterExchange: true,
	}
}

func passFeaturesFrom(t *testing.T, body []byte) []string {
	t.Helper()
	var doc struct {
		Data struct {
			Products []struct {
				Kind     string   `json:"kind"`
				Features []string `json:"features"`
			} `json:"products"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	for _, p := range doc.Data.Products {
		if p.Kind == "pass" {
			return p.Features
		}
	}
	t.Fatalf("no pass in the catalogue: %s", body)
	return nil
}

func TestM10PassFeaturesContracts(t *testing.T) {
	d := newM1Deck(t, allMechanics())
	assertContract(t, contractDo(d.env.r, http.MethodGet, "/v1/dating/premium/catalogue", ``, d.viewer),
		http.StatusOK, "premium_catalogue_get_200_all_mechanics", nil)
	d.grantPass()
	assertContract(t, contractDo(d.env.r, http.MethodGet, "/v1/dating/premium/me", ``, d.viewer),
		http.StatusOK, "premium_me_get_200_all_mechanics", map[uuid.UUID]string{d.viewer: "<viewer>"})
}

// Guard: each mechanic's feature appears only while it is on.
func TestPassFeaturesFollowTheMechanics(t *testing.T) {
	on := newM1Deck(t, allMechanics())
	got := passFeaturesFrom(t, contractDo(on.env.r, http.MethodGet, "/v1/dating/premium/catalogue", ``, on.viewer).Body.Bytes())
	want := []string{"match_extend", "daily_boost", "more_daily_cards", "unlimited_rewinds", "more_super_sparks",
		"see_who_sparked", "advanced_filters", "travel_mode", "read_receipts"}
	if len(got) != len(want) {
		t.Fatalf("features with every mechanic on = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("features with every mechanic on = %v, want %v", got, want)
		}
	}
	off := newM1Deck(t, service.MechanicsConfig{})
	got = passFeaturesFrom(t, contractDo(off.env.r, http.MethodGet, "/v1/dating/premium/catalogue", ``, off.viewer).Body.Bytes())
	if len(got) != 2 || got[0] != "match_extend" || got[1] != "daily_boost" {
		t.Fatalf("features with every mechanic off = %v, want the two base features", got)
	}
}
