package http

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/atpost/dating-service/internal/service"
	"github.com/google/uuid"
)

// Mechanic M10 — GET /v1/dating/allowances and the reset time on the spark
// limit refusal. Same harness as m1_deck_test.go.

var m10Fixtures = []string{
	"allowances_get_200",
	"allowances_get_200_mechanics_off",
}

func m10Config() service.MechanicsConfig {
	return service.MechanicsConfig{
		DeckRefill: true, DeckDailyLimitFree: 25, DeckDailyLimitPass: 100,
		Rewind: true, RewindDailyLimitFree: 1,
		SuperSpark: true, SuperSparkDailyLimitFree: 1, SuperSparkDailyLimitPass: 5,
	}
}

type m10Allowances struct {
	Data map[string]map[string]any `json:"data"`
}

func (d *m1Deck) allowances() map[string]map[string]any {
	d.t.Helper()
	rec := contractDo(d.env.r, http.MethodGet, "/v1/dating/allowances", ``, d.viewer)
	if rec.Code != http.StatusOK {
		d.t.Fatalf("allowances: status %d body %s", rec.Code, rec.Body.String())
	}
	var body m10Allowances
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		d.t.Fatalf("allowances: %v", err)
	}
	return body.Data
}

func TestM10AllowancesContracts(t *testing.T) {
	d := newM1Deck(t, m10Config())
	a, b := d.candidate(), d.candidate()
	d.spark(a)
	d.pass(b)
	assertContract(t, contractDo(d.env.r, http.MethodGet, "/v1/dating/allowances", ``, d.viewer),
		http.StatusOK, "allowances_get_200", map[uuid.UUID]string{d.viewer: "<viewer>"})

	off := newM1Deck(t, service.MechanicsConfig{})
	assertContract(t, contractDo(off.env.r, http.MethodGet, "/v1/dating/allowances", ``, off.viewer),
		http.StatusOK, "allowances_get_200_mechanics_off", map[uuid.UUID]string{off.viewer: "<viewer>"})
}

func TestM10ContractFixturesWellFormed(t *testing.T) {
	for _, name := range m10Fixtures {
		raw, err := os.ReadFile(filepath.Join(contractsDir, name+".json"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var doc m10Allowances
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: not JSON: %v", name, err)
		}
		if doc.Data["sparks"] == nil {
			t.Fatalf("%s: sparks must always be present", name)
		}
		if contractUUIDRe.Match(raw) || contractTimeRe.Match(raw) {
			t.Fatalf("%s: carries a raw uuid or timestamp", name)
		}
	}
}

// Guard: a mechanic whose flag is off is absent, so a client never shows a
// control for it; sparks are always there.
func TestAllowancesOmitMechanicsThatAreOff(t *testing.T) {
	d := newM1Deck(t, service.MechanicsConfig{Rewind: true})
	got := d.allowances()
	if got["sparks"] == nil || got["rewind"] == nil {
		t.Fatalf("allowances = %v, want sparks and rewind", got)
	}
	for _, key := range []string{"deck", "super_spark"} {
		if _, ok := got[key]; ok {
			t.Fatalf("allowances carry %q while its flag is off: %v", key, got)
		}
	}
}

// The counts move with use, and a pass lifts the rewind allowance.
func TestAllowancesFollowUseAndThePass(t *testing.T) {
	d := newM1Deck(t, m10Config())
	a, b := d.candidate(), d.candidate()
	before := d.allowances()
	if before["deck"]["remaining_today"] != float64(25) || before["rewind"]["remaining_today"] != float64(1) || before["super_spark"]["remaining_today"] != float64(1) {
		t.Fatalf("fresh allowances = %v", before)
	}
	d.mustSuperSpark(a)
	d.pass(b)
	d.mustRewind()
	after := d.allowances()
	if after["deck"]["remaining_today"] != float64(24) {
		t.Fatalf("deck after a Super Spark and a rewound pass = %v, want 24 left", after["deck"])
	}
	if after["super_spark"]["remaining_today"] != nil || after["super_spark"]["resets_at"] == nil {
		t.Fatalf("super spark after the daily one = %v, want none left and a reset time", after["super_spark"])
	}
	if after["rewind"]["remaining_today"] != nil || after["rewind"]["resets_at"] == nil {
		t.Fatalf("rewind after the free one = %v, want none left and a reset time", after["rewind"])
	}
	if after["sparks"]["remaining_today"] != float64(49) {
		t.Fatalf("sparks after one = %v, want 49 left", after["sparks"])
	}
	d.grantPass()
	pass := d.allowances()
	if pass["rewind"]["unlimited"] != true || pass["deck"]["daily_limit"] != float64(100) || pass["super_spark"]["daily_limit"] != float64(5) {
		t.Fatalf("allowances with a pass = %v", pass)
	}
}
