package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atpost/dating-service/internal/service"
	"github.com/google/uuid"
)

// Mechanic M2 — rewind (DATING_REWIND_ENABLED): golden fixtures and the
// guards behind them. Same harness as m1_deck_test.go.

var m2Fixtures = []string{
	"pulse_rewind_post_200",
	"pulse_rewind_404_not_enabled",
	"pulse_rewind_409_nothing_to_undo",
	"pulse_rewind_429_limit_reached",
}

func m2Config() service.MechanicsConfig {
	return service.MechanicsConfig{DeckRefill: true, DeckDailyLimitFree: 50, DeckDailyLimitPass: 50, Rewind: true, RewindDailyLimitFree: 1}
}

func (d *m1Deck) rewind() *httptest.ResponseRecorder {
	return contractDo(d.env.r, http.MethodPost, "/v1/dating/pulse/rewind", ``, d.viewer)
}

// mustRewind rewinds and returns the candidate whose pass was undone.
func (d *m1Deck) mustRewind() uuid.UUID {
	d.t.Helper()
	rec := d.rewind()
	if rec.Code != http.StatusOK {
		d.t.Fatalf("rewind: status %d body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data struct {
			CandidateID uuid.UUID `json:"candidate_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		d.t.Fatalf("rewind: %v", err)
	}
	return body.Data.CandidateID
}

func (d *m1Deck) wantRefusal(rec *httptest.ResponseRecorder, status int, code string) {
	d.t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != status || body.Error.Code != code {
		d.t.Fatalf("status %d code %q, want %d %s (body %s)", rec.Code, body.Error.Code, status, code, rec.Body.String())
	}
}

// grantPass gives the viewer an unexpired pass.
func (d *m1Deck) grantPass() {
	d.t.Helper()
	ctx := context.Background()
	tx, err := d.env.st.BeginTx(ctx)
	if err != nil {
		d.t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
        INSERT INTO dating_premium_subscriptions (user_id, plan, started_at, expires_at, source)
        VALUES ($1, 'pass_30d', now(), now() + interval '10 days', 'test')
        ON CONFLICT (user_id) DO UPDATE SET expires_at = EXCLUDED.expires_at`, d.viewer); err != nil {
		d.t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		d.t.Fatal(err)
	}
}

func TestM2RewindContracts(t *testing.T) {
	d := newM1Deck(t, m2Config())
	first, second := d.candidate(), d.candidate()
	labels := map[uuid.UUID]string{d.viewer: "<viewer>", first: "<candidate>", second: "<second>"}

	assertContract(t, d.rewind(), http.StatusConflict, "pulse_rewind_409_nothing_to_undo", labels)

	d.pass(first)
	rec := d.rewind()
	d7AssertBucketsOnly(t, "rewind", rec.Body.Bytes())
	assertContract(t, rec, http.StatusOK, "pulse_rewind_post_200", labels)

	d.pass(second)
	assertContract(t, d.rewind(), http.StatusTooManyRequests, "pulse_rewind_429_limit_reached", labels)

	off := newM1Deck(t, service.MechanicsConfig{DeckRefill: true})
	off.pass(off.candidate())
	assertContract(t, off.rewind(), http.StatusNotFound, "pulse_rewind_404_not_enabled", map[uuid.UUID]string{off.viewer: "<viewer>"})
}

func TestM2ContractFixturesWellFormed(t *testing.T) {
	for _, name := range m2Fixtures {
		raw, err := os.ReadFile(filepath.Join(contractsDir, name+".json"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: not JSON: %v", name, err)
		}
		if _, ok := doc["data"]; !ok {
			if _, ok := doc["error"]; !ok {
				t.Fatalf("%s: neither data nor error", name)
			}
		}
		if contractUUIDRe.Match(raw) || contractTimeRe.Match(raw) {
			t.Fatalf("%s: carries a raw uuid or timestamp", name)
		}
		if strings.Contains(string(raw), "latitude") || strings.Contains(string(raw), "distance_km") {
			t.Fatalf("%s: carries a precise location field", name)
		}
	}
}

// A rewind brings the passed card back to the deck and hands the card back in
// its response.
func TestRewindBringsThePassedCardBack(t *testing.T) {
	d := newM1Deck(t, m2Config())
	a := d.candidate()
	d.pass(a)
	if ids, _ := d.deck(); ids[a] {
		t.Fatalf("a passed candidate is still in the deck")
	}
	if got := d.mustRewind(); got != a {
		t.Fatalf("rewound %s, want the passed candidate %s", got, a)
	}
	ids, meta := d.deck()
	if !ids[a] {
		t.Fatalf("the rewound candidate did not come back to the deck")
	}
	// The card goes back into the daily allowance too.
	if meta["remaining_today"] != float64(50) {
		t.Fatalf("meta = %v, want the rewound card back in the allowance", meta)
	}
}

// Guard: a rewind is one step. With the last pass undone, a second rewind
// does not reach the pass before it (a pass holder, so the allowance is not
// what refuses).
func TestRewindIsOneStepNeverAChain(t *testing.T) {
	d := newM1Deck(t, m2Config())
	d.grantPass()
	a, b := d.candidate(), d.candidate()
	d.pass(a)
	d.pass(b)
	if got := d.mustRewind(); got != b {
		t.Fatalf("rewound %s, want the LAST pass %s", got, b)
	}
	d.wantRefusal(d.rewind(), http.StatusConflict, "REWIND_NOTHING_TO_UNDO")
	if ids, _ := d.deck(); ids[a] {
		t.Fatalf("the earlier pass was undone as well")
	}
}

// Guard: a spark is never undone. Once the caller sparks, the pass before it
// is out of reach, and the spark itself stays.
func TestRewindNeverUndoesASpark(t *testing.T) {
	d := newM1Deck(t, m2Config())
	d.grantPass()
	passed, sparked := d.candidate(), d.candidate()
	d.pass(passed)
	d.spark(sparked)
	d.wantRefusal(d.rewind(), http.StatusConflict, "REWIND_NOTHING_TO_UNDO")
	sp, err := d.env.st.ListIncomingSparks(context.Background(), sparked, 10, 0)
	if err != nil || len(sp) != 1 {
		t.Fatalf("the spark is gone after a refused rewind: %d err=%v", len(sp), err)
	}
	if ids, _ := d.deck(); ids[passed] || ids[sparked] {
		t.Fatalf("deck after a refused rewind = %v, want neither candidate", ids)
	}
}

// Guard: a free user gets one rewind per rolling 24 hours, and passing the
// same person again does not hand the rewind back.
func TestRewindFreeAllowanceIsOneADay(t *testing.T) {
	d := newM1Deck(t, m2Config())
	a := d.candidate()
	d.pass(a)
	d.mustRewind()
	d.pass(a)
	rec := d.rewind()
	d.wantRefusal(rec, http.StatusTooManyRequests, "REWIND_LIMIT_REACHED")
	var body struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Error.Details["limit"] != float64(1) || body.Error.Details["window_hours"] != float64(24) || body.Error.Details["resets_at"] == nil {
		t.Fatalf("details = %v, want limit 1, window_hours 24 and resets_at", body.Error.Details)
	}
	if ids, _ := d.deck(); ids[a] {
		t.Fatalf("a refused rewind still undid the pass")
	}
}

// Guard: a pass holder is not limited.
func TestRewindPassHolderIsNotLimited(t *testing.T) {
	d := newM1Deck(t, m2Config())
	d.grantPass()
	a := d.candidate()
	for i := 0; i < 3; i++ {
		d.pass(a)
		rec := d.rewind()
		if rec.Code != http.StatusOK {
			t.Fatalf("rewind %d: status %d body %s", i+1, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"unlimited":true`) {
			t.Fatalf("rewind %d: allowance is not unlimited: %s", i+1, rec.Body.String())
		}
	}
}

// Guard: a block applies to a rewind. The blocked person's card is never
// handed back, the refusal is the usual single one, and it costs no rewind.
func TestRewindRefusesABlockedCandidate(t *testing.T) {
	d := newM1Deck(t, m2Config())
	a := d.candidate()
	d.pass(a)
	rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/safety/block", `{"target_user_id":"`+d.viewer.String()+`"}`, a)
	if rec.Code != http.StatusOK {
		t.Fatalf("block: status %d body %s", rec.Code, rec.Body.String())
	}
	rec = d.rewind()
	d.wantRefusal(rec, http.StatusNotFound, "CANDIDATE_UNAVAILABLE")
	if strings.Contains(rec.Body.String(), a.String()) {
		t.Fatalf("the refusal names the blocked candidate: %s", rec.Body.String())
	}
	if used, _, err := d.env.st.RewindUsage(context.Background(), d.viewer); err != nil || used != 0 {
		t.Fatalf("a refused rewind used the allowance: used=%d err=%v", used, err)
	}
}
