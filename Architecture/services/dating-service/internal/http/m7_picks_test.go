package http

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atpost/dating-service/internal/service"
	"github.com/google/uuid"
)

// Mechanic M7 — daily picks (DATING_PICKS_ENABLED): golden fixtures and the
// guards behind them. Same harness as m1_deck_test.go.

var m7Fixtures = []string{
	"picks_get_200",
	"picks_get_400_invalid_timezone",
	"picks_get_404_not_enabled",
}

func m7Config(on bool) service.MechanicsConfig {
	return service.MechanicsConfig{DeckRefill: true, DeckDailyLimitFree: 50, DeckDailyLimitPass: 50, Picks: on}
}

type m7Picks struct {
	Data []struct {
		CandidateID uuid.UUID `json:"candidate_id"`
	} `json:"data"`
	Meta struct {
		Date     string    `json:"date"`
		Timezone string    `json:"timezone"`
		ResetsAt time.Time `json:"resets_at"`
		Size     int       `json:"size"`
	} `json:"meta"`
}

func (d *m1Deck) picks(tz string) m7Picks {
	d.t.Helper()
	path := "/v1/dating/picks"
	if tz != "" {
		path += "?tz=" + tz
	}
	rec := contractDo(d.env.r, http.MethodGet, path, ``, d.viewer)
	if rec.Code != http.StatusOK {
		d.t.Fatalf("picks: %d %s", rec.Code, rec.Body.String())
	}
	var out m7Picks
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		d.t.Fatal(err)
	}
	return out
}

func (p m7Picks) ids() []uuid.UUID {
	out := make([]uuid.UUID, len(p.Data))
	for i, c := range p.Data {
		out[i] = c.CandidateID
	}
	return out
}

func TestM7PicksContracts(t *testing.T) {
	d := newM1Deck(t, m7Config(true))
	c := d.candidate()
	labels := map[uuid.UUID]string{d.viewer: "<viewer>", c: "<candidate>"}
	// The date and zone in the fixture are fixed by asking in UTC; the date
	// itself is replaced like a timestamp.
	rec := contractDo(d.env.r, http.MethodGet, "/v1/dating/picks?tz=UTC", ``, d.viewer)
	d7AssertBucketsOnly(t, "picks", rec.Body.Bytes())
	today := time.Now().UTC().Format("2006-01-02")
	assertContract(t, d7Rewrite(rec, `"`+today+`"`, `"<date>"`), http.StatusOK, "picks_get_200", labels)

	assertContract(t, contractDo(d.env.r, http.MethodGet, "/v1/dating/picks?tz=Mars/Olympus", ``, d.viewer),
		http.StatusBadRequest, "picks_get_400_invalid_timezone", labels)

	off := newM1Deck(t, m7Config(false))
	assertContract(t, contractDo(off.env.r, http.MethodGet, "/v1/dating/picks", ``, off.viewer),
		http.StatusNotFound, "picks_get_404_not_enabled", map[uuid.UUID]string{off.viewer: "<viewer>"})
}

func TestM7ContractFixturesWellFormed(t *testing.T) {
	for _, name := range m7Fixtures {
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

// Guard: at most ten, and the same selection all day.
func TestPicksAreCappedAndStableForTheDay(t *testing.T) {
	d := newM1Deck(t, m7Config(true))
	for i := 0; i < 12; i++ {
		d.candidate()
	}
	first := d.picks("Asia/Kolkata")
	if first.Meta.Size != len(first.Data) || len(first.Data) == 0 || len(first.Data) > service.MaxDailyPicks {
		t.Fatalf("picks size = %d (meta %d), want 1..%d", len(first.Data), first.Meta.Size, service.MaxDailyPicks)
	}
	again := d.picks("Asia/Kolkata")
	if len(again.Data) != len(first.Data) {
		t.Fatalf("second read has %d picks, first had %d", len(again.Data), len(first.Data))
	}
	for i := range first.Data {
		if first.Data[i].CandidateID != again.Data[i].CandidateID {
			t.Fatalf("the day's picks changed between reads at %d", i)
		}
	}
	loc, _ := time.LoadLocation("Asia/Kolkata")
	now := time.Now().In(loc)
	wantReset := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, loc)
	if !first.Meta.ResetsAt.Equal(wantReset) || first.Meta.Date != now.Format("2006-01-02") || first.Meta.Timezone != "Asia/Kolkata" {
		t.Fatalf("meta = %+v, want date %s and reset at %s", first.Meta, now.Format("2006-01-02"), wantReset)
	}
}

// Guard: acting on a pick takes it out, and spends no deck card.
func TestPicksActedOnLeaveAndCostNoDeckCard(t *testing.T) {
	d := newM1Deck(t, m7Config(true))
	d.candidate()
	d.candidate()
	ids := d.picks("UTC").ids()
	if len(ids) < 2 {
		t.Fatalf("want 2 picks, got %d", len(ids))
	}
	body := `{"to_user_id":"` + ids[0].String() + `","target_kind":"prompt","target_ref":"m7","source":"picks"}`
	if rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/sparks", body, d.viewer); rec.Code != http.StatusCreated {
		t.Fatalf("spark a pick: %d %s", rec.Code, rec.Body.String())
	}
	if rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/pulse/"+ids[1].String()+"/pass", `{"source":"picks"}`, d.viewer); rec.Code != http.StatusOK {
		t.Fatalf("pass a pick: %d %s", rec.Code, rec.Body.String())
	}
	if left := d.picks("UTC").ids(); len(left) != 0 {
		t.Fatalf("acted-on picks are still shown: %v", left)
	}
	if used, _, err := d.env.st.DeckUsage(context.Background(), d.viewer); err != nil || used != 0 {
		t.Fatalf("acting on picks spent %d deck cards (err %v), want 0", used, err)
	}
	// An unknown source is refused, not treated as the deck.
	d.wantRefusal(contractDo(d.env.r, http.MethodPost, "/v1/dating/pulse/"+uuid.NewString()+"/pass", `{"source":"elsewhere"}`, d.viewer), http.StatusBadRequest, "INVALID_SOURCE")
}

// Guard: a block takes a pick out, both ways.
func TestPicksHonourBlocks(t *testing.T) {
	d := newM1Deck(t, m7Config(true))
	a, b := d.candidate(), d.candidate()
	if got := d.picks("UTC").ids(); len(got) != 2 {
		t.Fatalf("want both picks, got %v", got)
	}
	if rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/safety/block", `{"target_user_id":"`+d.viewer.String()+`"}`, a); rec.Code != http.StatusOK {
		t.Fatalf("block: %d", rec.Code)
	}
	if rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/safety/block", `{"target_user_id":"`+b.String()+`"}`, d.viewer); rec.Code != http.StatusOK {
		t.Fatalf("block: %d", rec.Code)
	}
	if got := d.picks("UTC").ids(); len(got) != 0 {
		t.Fatalf("blocked people are still picks: %v", got)
	}
}

// Guard: a verified profile comes before an otherwise identical unverified
// one.
func TestPicksPutVerifiedFirst(t *testing.T) {
	d := newM1Deck(t, m7Config(true))
	phone, selfie := d.candidate(), d.candidate()
	d.exec(`UPDATE dating_profiles SET trust_tier = 'phone' WHERE user_id = $1`, phone)
	// The unverified one is the better match on intent (the viewer wants
	// casual), so only the verified bonus can put the verified one first.
	d.exec(`UPDATE dating_profiles SET trust_tier = 'selfie', intent = 'serious' WHERE user_id = $1`, selfie)
	got := d.picks("UTC").ids()
	if len(got) != 2 || got[0] != selfie {
		t.Fatalf("picks = %v, want the verified %s first", got, selfie)
	}
}

// Guard: the day follows the viewer's zone. Two zones a day apart are two
// different days, each with its own selection.
func TestPicksFollowTheLocalDay(t *testing.T) {
	d := newM1Deck(t, m7Config(true))
	d.candidate()
	east := d.picks("Pacific/Kiritimati") // UTC+14
	west := d.picks("Pacific/Pago_Pago")  // UTC-11
	if east.Meta.Date == west.Meta.Date {
		t.Fatalf("UTC+14 and UTC-11 share the date %s", east.Meta.Date)
	}
	if !east.Meta.ResetsAt.Before(west.Meta.ResetsAt) {
		t.Fatalf("the eastern day ends at %s, after the western %s", east.Meta.ResetsAt, west.Meta.ResetsAt)
	}
	// No zone at all is Asia/Kolkata.
	if def := d.picks(""); def.Meta.Timezone != service.DefaultPicksTimezone {
		t.Fatalf("default zone = %q", def.Meta.Timezone)
	}
}

// Guard: a pick opens its person card (picks are not in the deck), and the
// card still honours a block.
func TestPicksOpenThePersonCard(t *testing.T) {
	// One deck card a day, spent below, so the deck is empty and only the
	// pick can explain access to the card.
	d := newM1Deck(t, service.MechanicsConfig{DeckRefill: true, DeckDailyLimitFree: 1, DeckDailyLimitPass: 1, Picks: true})
	x, y := d.candidate(), d.candidate()
	if got := d.picks("UTC").ids(); len(got) != 2 {
		t.Fatalf("want two picks, got %v", got)
	}
	d.pass(y)
	if ids, _ := d.deck(); len(ids) != 0 {
		t.Fatalf("deck should be empty after its one card: %v", ids)
	}
	if rec := contractDo(d.env.r, http.MethodGet, "/v1/dating/people/"+x.String(), ``, d.viewer); rec.Code != http.StatusOK {
		t.Fatalf("person card for a pick: %d %s", rec.Code, rec.Body.String())
	}
	if rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/safety/block", `{"target_user_id":"`+d.viewer.String()+`"}`, x); rec.Code != http.StatusOK {
		t.Fatalf("block: %d", rec.Code)
	}
	d.wantRefusal(contractDo(d.env.r, http.MethodGet, "/v1/dating/people/"+x.String(), ``, d.viewer), http.StatusNotFound, "CANDIDATE_UNAVAILABLE")
}
