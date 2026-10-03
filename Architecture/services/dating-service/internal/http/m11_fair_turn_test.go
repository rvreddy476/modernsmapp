package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/atpost/dating-service/internal/service"
	"github.com/google/uuid"
)

// Mechanic M11 — fair turn (DATING_FAIR_TURN_ENABLED): golden fixtures and
// the guards behind them. Same harness as m1_deck_test.go, with a chat stub
// that answers how many replies the sender owes.

var m11Fixtures = []string{
	"allowances_get_200_fair_turn",
	"sparks_post_409_fair_turn_limit",
}

// turnsChat answers DatingTurnsOwed with owed (or err).
type turnsChat struct {
	chatRecorder
	owed int
	err  error
}

func (c *turnsChat) DatingTurnsOwed(context.Context, uuid.UUID) (int, error) { return c.owed, c.err }

func m11Config(on bool) service.MechanicsConfig {
	return service.MechanicsConfig{DeckRefill: true, DeckDailyLimitFree: 50, DeckDailyLimitPass: 50, FairTurn: on, FairTurnLimit: 6}
}

func newM11Deck(t *testing.T, on bool, owed int) (*m1Deck, *turnsChat) {
	t.Helper()
	d := newM1Deck(t, m11Config(on))
	chat := &turnsChat{owed: owed}
	d.env.svc.SetMessageClient(chat)
	return d, chat
}

func m11SparkBody(to uuid.UUID) string {
	return `{"to_user_id":"` + to.String() + `","target_kind":"prompt","target_ref":"m11"}`
}

func TestM11FairTurnContracts(t *testing.T) {
	d, _ := newM11Deck(t, true, 6)
	c := d.candidate()
	labels := map[uuid.UUID]string{d.viewer: "<viewer>", c: "<candidate>"}
	assertContract(t, contractDo(d.env.r, http.MethodGet, "/v1/dating/allowances", ``, d.viewer), http.StatusOK, "allowances_get_200_fair_turn", labels)
	assertContract(t, contractDo(d.env.r, http.MethodPost, "/v1/dating/sparks", m11SparkBody(c), d.viewer), http.StatusConflict, "sparks_post_409_fair_turn_limit", labels)
}

func TestM11ContractFixturesWellFormed(t *testing.T) {
	for _, name := range m11Fixtures {
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

// Guard: at the limit a new spark is refused; below it, or with the flag off,
// it goes through.
func TestFairTurnPausesNewSparks(t *testing.T) {
	for _, tc := range []struct {
		name string
		on   bool
		owed int
		want int
	}{
		{"at the limit", true, 6, http.StatusConflict},
		{"over the limit", true, 9, http.StatusConflict},
		{"below the limit", true, 5, http.StatusCreated},
		{"flag off", false, 99, http.StatusCreated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := newM11Deck(t, tc.on, tc.owed)
			c := d.candidate()
			if rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/sparks", m11SparkBody(c), d.viewer); rec.Code != tc.want {
				t.Fatalf("spark: %d %s, want %d", rec.Code, rec.Body.String(), tc.want)
			}
		})
	}
}

// Guard: a spark that makes a match answers someone, so it is never paused —
// sparking back, and accepting a spark.
func TestFairTurnLetsAnswersThrough(t *testing.T) {
	d, chat := newM11Deck(t, true, 0)
	back, accepted := d.candidate(), d.candidate()
	// Both sparked the viewer while the viewer owed nothing.
	if rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/sparks", m11SparkBody(d.viewer), back); rec.Code != http.StatusCreated {
		t.Fatalf("seed spark: %d %s", rec.Code, rec.Body.String())
	}
	rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/sparks", m11SparkBody(d.viewer), accepted)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed spark: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Data struct {
			Spark struct {
				ID uuid.UUID `json:"id"`
			} `json:"spark"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.Data.Spark.ID == uuid.Nil {
		t.Fatalf("spark id: %v %s", err, rec.Body.String())
	}
	chat.owed = 9
	if rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/sparks", m11SparkBody(back), d.viewer); rec.Code != http.StatusCreated {
		t.Fatalf("sparking back was paused: %d %s", rec.Code, rec.Body.String())
	}
	if rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/sparks/"+created.Data.Spark.ID.String()+"/accept", ``, d.viewer); rec.Code != http.StatusCreated {
		t.Fatalf("accepting a spark was paused: %d %s", rec.Code, rec.Body.String())
	}
	// A new person is still paused.
	if rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/sparks", m11SparkBody(d.candidate()), d.viewer); rec.Code != http.StatusConflict {
		t.Fatalf("a new spark went through over the limit: %d", rec.Code)
	}
}

// A failed count lets the spark through, and drops fair_turn from allowances.
func TestFairTurnFailsOpen(t *testing.T) {
	d, chat := newM11Deck(t, true, 9)
	chat.err = errors.New("chat down")
	c := d.candidate()
	if rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/sparks", m11SparkBody(c), d.viewer); rec.Code != http.StatusCreated {
		t.Fatalf("spark during a chat outage: %d %s", rec.Code, rec.Body.String())
	}
	rec := contractDo(d.env.r, http.MethodGet, "/v1/dating/allowances", ``, d.viewer)
	var body struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, present := body.Data["fair_turn"]; present {
		t.Fatalf("fair_turn shown without a count: %s", rec.Body.String())
	}
}
