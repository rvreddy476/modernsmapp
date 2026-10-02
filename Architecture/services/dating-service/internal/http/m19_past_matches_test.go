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

// Mechanic M19 — past matches (DATING_PAST_MATCH_REPORT_ENABLED): golden
// fixtures and the guards behind them. Same harness as m1_deck_test.go.

var m19Fixtures = []string{
	"past_matches_get_200",
	"past_matches_get_404_not_enabled",
}

func m19Config(on bool) service.MechanicsConfig {
	return service.MechanicsConfig{DeckRefill: true, DeckDailyLimitFree: 50, DeckDailyLimitPass: 50, PastMatchReport: on}
}

type m19Row struct {
	MatchID uuid.UUID `json:"match_id"`
	Person  struct {
		UserID    uuid.UUID `json:"user_id"`
		FirstName string    `json:"first_name"`
	} `json:"person"`
	Ended    string `json:"ended"`
	Reported bool   `json:"reported"`
}

func (d *m1Deck) pastMatches(user uuid.UUID) map[uuid.UUID]m19Row {
	d.t.Helper()
	rec := contractDo(d.env.r, http.MethodGet, "/v1/dating/past-matches", ``, user)
	if rec.Code != http.StatusOK {
		d.t.Fatalf("past matches: %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data []m19Row `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		d.t.Fatal(err)
	}
	out := map[uuid.UUID]m19Row{}
	for _, r := range body.Data {
		out[r.Person.UserID] = r
	}
	return out
}

func (d *m1Deck) unmatch(match uuid.UUID, by uuid.UUID) {
	d.t.Helper()
	if rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/matches/"+match.String()+"/close", ``, by); rec.Code != http.StatusOK {
		d.t.Fatalf("unmatch: %d %s", rec.Code, rec.Body.String())
	}
}

func TestM19PastMatchesContracts(t *testing.T) {
	d := newM1Deck(t, m19Config(true))
	other := d.candidate()
	match := d.matchWith(other)
	d.unmatch(match, other)
	assertContract(t, contractDo(d.env.r, http.MethodGet, "/v1/dating/past-matches", ``, d.viewer), http.StatusOK, "past_matches_get_200",
		map[uuid.UUID]string{d.viewer: "<viewer>", other: "<other>", match: "<match>"})

	off := newM1Deck(t, m19Config(false))
	assertContract(t, contractDo(off.env.r, http.MethodGet, "/v1/dating/past-matches", ``, off.viewer),
		http.StatusNotFound, "past_matches_get_404_not_enabled", map[uuid.UUID]string{off.viewer: "<viewer>"})
}

func TestM19ContractFixturesWellFormed(t *testing.T) {
	for _, name := range m19Fixtures {
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

// Guard: unmatched, blocked and expired matches are listed — a block above
// all — while an open match, an old one and anyone else's are not.
func TestPastMatchesListWhatEnded(t *testing.T) {
	d := newM1Deck(t, m19Config(true))
	unmatched, blocker, expired, open, old := d.candidate(), d.candidate(), d.candidate(), d.candidate(), d.candidate()
	d.unmatch(d.matchWith(unmatched), d.viewer)
	d.matchWith(blocker)
	if rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/safety/block", `{"target_user_id":"`+d.viewer.String()+`"}`, blocker); rec.Code != http.StatusOK {
		t.Fatalf("block: %d", rec.Code)
	}
	expiredMatch := d.matchWith(expired)
	d.exec(`UPDATE dating_matches SET status = 'expired', closed_at = now() WHERE id = $1`, expiredMatch)
	d.matchWith(open)
	oldMatch := d.matchWith(old)
	d.unmatch(oldMatch, old)
	d.exec(`UPDATE dating_matches SET closed_at = now() - interval '31 days' WHERE id = $1`, oldMatch)

	got := d.pastMatches(d.viewer)
	for who, want := range map[uuid.UUID]string{unmatched: "unmatched", blocker: "blocked", expired: "expired"} {
		if got[who].Ended != want {
			t.Fatalf("%s: ended = %q, want %q (list %+v)", who, got[who].Ended, want, got)
		}
	}
	if _, listed := got[open]; listed {
		t.Fatalf("an open match is listed")
	}
	if _, listed := got[old]; listed {
		t.Fatalf("a match that ended 31 days ago is listed")
	}
	// Someone else sees only their own.
	if theirs := d.pastMatches(unmatched); len(theirs) != 1 || theirs[d.viewer].Ended != "unmatched" {
		t.Fatalf("the other side's list = %+v", theirs)
	}
	if mine := d.pastMatches(open); len(mine) != 0 {
		t.Fatalf("an unrelated user sees %+v", mine)
	}
}

// Guard: a report marks the row, and the report itself goes through the
// usual route with no match needed.
func TestPastMatchReportIsMarked(t *testing.T) {
	d := newM1Deck(t, m19Config(true))
	other := d.candidate()
	d.unmatch(d.matchWith(other), other)
	if got := d.pastMatches(d.viewer); got[other].Reported {
		t.Fatalf("reported before any report")
	}
	body := `{"target_id":"` + other.String() + `","reason":"harassment","details":"after we unmatched"}`
	if rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/safety/report", body, d.viewer); rec.Code != http.StatusCreated {
		t.Fatalf("report: %d %s", rec.Code, rec.Body.String())
	}
	if got := d.pastMatches(d.viewer); !got[other].Reported {
		t.Fatalf("the report is not marked: %+v", got)
	}
}
