package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/atpost/dating-service/internal/service"
	"github.com/google/uuid"
)

// Mechanic M16 — hide from people I know (DATING_HIDE_KNOWN_ENABLED):
// golden fixtures and the guards behind them, with a graph stub.

var m16Fixtures = []string{
	"hide_known_get_200",
	"hide_known_put_200",
	"hide_known_put_503_unavailable",
	"hide_known_get_404_not_enabled",
}

// knownGraph answers AcceptedConnections from a map (or fails).
type knownGraph struct {
	mu   sync.Mutex
	conn map[uuid.UUID][]uuid.UUID
	fail bool
}

func (g *knownGraph) AcceptedConnections(_ context.Context, id uuid.UUID) ([]uuid.UUID, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.fail {
		return nil, errors.New("graph down")
	}
	return g.conn[id], nil
}

func newM16Deck(t *testing.T, on bool) (*m1Deck, *knownGraph) {
	t.Helper()
	d := newM1Deck(t, service.MechanicsConfig{DeckRefill: true, DeckDailyLimitFree: 50, DeckDailyLimitPass: 50, Picks: true, HideKnown: on})
	g := &knownGraph{conn: map[uuid.UUID][]uuid.UUID{}}
	d.env.svc.SetConnectionLister(g)
	return d, g
}

func (d *m1Deck) hideKnown(user uuid.UUID, on bool) int {
	d.t.Helper()
	body := `{"enabled":false}`
	if on {
		body = `{"enabled":true}`
	}
	return contractDo(d.env.r, http.MethodPut, "/v1/dating/hide-known", body, user).Code
}

// deckOf is the deck of any user (not only the viewer).
func (d *m1Deck) deckOf(user uuid.UUID) map[uuid.UUID]bool {
	d.t.Helper()
	rec := contractDo(d.env.r, http.MethodGet, "/v1/dating/pulse/today", ``, user)
	if rec.Code != http.StatusOK {
		d.t.Fatalf("deck of %s: %d %s", user, rec.Code, rec.Body.String())
	}
	var body m1DeckBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		d.t.Fatal(err)
	}
	out := map[uuid.UUID]bool{}
	for _, c := range body.Data {
		out[c.CandidateID] = true
	}
	return out
}

func TestM16HideKnownContracts(t *testing.T) {
	d, g := newM16Deck(t, true)
	friend := d.candidate()
	g.conn[d.viewer] = []uuid.UUID{friend, uuid.New()}
	labels := map[uuid.UUID]string{d.viewer: "<viewer>"}
	assertContract(t, contractDo(d.env.r, http.MethodPut, "/v1/dating/hide-known", `{"enabled":true}`, d.viewer), http.StatusOK, "hide_known_put_200", labels)
	assertContract(t, contractDo(d.env.r, http.MethodGet, "/v1/dating/hide-known", ``, d.viewer), http.StatusOK, "hide_known_get_200", labels)
	g.fail = true
	other := d.candidate()
	assertContract(t, contractDo(d.env.r, http.MethodPut, "/v1/dating/hide-known", `{"enabled":true}`, other), http.StatusServiceUnavailable,
		"hide_known_put_503_unavailable", map[uuid.UUID]string{other: "<other>"})

	off, _ := newM16Deck(t, false)
	assertContract(t, contractDo(off.env.r, http.MethodGet, "/v1/dating/hide-known", ``, off.viewer),
		http.StatusNotFound, "hide_known_get_404_not_enabled", map[uuid.UUID]string{off.viewer: "<viewer>"})
}

func TestM16ContractFixturesWellFormed(t *testing.T) {
	for _, name := range m16Fixtures {
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

// Guard: with the setting on, a connection and the user never see each
// other — both ways, in the deck and in picks — while a stranger still does.
// Off again, they do.
func TestHideKnownKeepsConnectionsApart(t *testing.T) {
	d, g := newM16Deck(t, true)
	friend, stranger := d.candidate(), d.candidate()
	// The connection looks only for a gender unique to this viewer, so the
	// viewer is the one person who fits their deck.
	mine := d.gender + "-viewer"
	d.exec(`UPDATE dating_profiles SET gender = $2 WHERE user_id = $1`, d.viewer, mine)
	d.exec(`UPDATE dating_preferences SET interested_in_gender = $2 WHERE user_id = $1`, friend, mine)
	if deck := d.deckOf(d.viewer); !deck[friend] || !deck[stranger] {
		t.Fatalf("before: friend %v stranger %v, want both", deck[friend], deck[stranger])
	}
	if theirs := d.deckOf(friend); !theirs[d.viewer] {
		t.Fatalf("before: the connection does not see the user, so the reverse check proves nothing")
	}
	g.conn[d.viewer] = []uuid.UUID{friend}
	if code := d.hideKnown(d.viewer, true); code != http.StatusOK {
		t.Fatalf("turn on: %d", code)
	}
	if deck := d.deckOf(d.viewer); deck[friend] || !deck[stranger] {
		t.Fatalf("on: friend %v stranger %v, want only the stranger", deck[friend], deck[stranger])
	}
	if picked(d.picks("UTC").ids(), friend) {
		t.Fatalf("the connection is a pick")
	}
	// The connection, who has not turned anything on, does not see the user.
	if theirs := d.deckOf(friend); theirs[d.viewer] {
		t.Fatalf("the connection still sees the user")
	}
	if code := d.hideKnown(d.viewer, false); code != http.StatusOK {
		t.Fatalf("turn off: %d", code)
	}
	if deck := d.deckOf(d.viewer); !deck[friend] {
		t.Fatalf("off again, but the connection is still hidden")
	}
}

// Guard: the flag off means the setting does nothing, even if stored.
func TestHideKnownFlagOff(t *testing.T) {
	d, _ := newM16Deck(t, false)
	friend := d.candidate()
	if err := d.env.st.ReplaceKnownPeople(context.Background(), d.viewer, []uuid.UUID{friend}); err != nil {
		t.Fatal(err)
	}
	if deck := d.deckOf(d.viewer); !deck[friend] {
		t.Fatalf("the flag is off but the connection is hidden")
	}
}

// Guard: when the connections cannot be read, turning it on fails and
// nothing is hidden.
func TestHideKnownRefusesWithoutConnections(t *testing.T) {
	d, g := newM16Deck(t, true)
	friend := d.candidate()
	g.conn[d.viewer] = []uuid.UUID{friend}
	g.fail = true
	if code := d.hideKnown(d.viewer, true); code != http.StatusServiceUnavailable {
		t.Fatalf("turn on with graph down: %d", code)
	}
	rec := contractDo(d.env.r, http.MethodGet, "/v1/dating/hide-known", ``, d.viewer)
	var body struct {
		Data service.HideKnownView `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Data.Enabled {
		t.Fatalf("enabled after a failed read: %s", rec.Body.String())
	}
}
