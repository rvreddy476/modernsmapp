package http

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// Mechanic M1 — the refilling swipe deck (DATING_DECK_REFILL_ENABLED): golden
// fixtures for the deck's new meta and the guards behind it. Same harness as
// d3_contract_test.go (TEST_PG_DSN on a "_test" database; regenerate with
// UPDATE_CONTRACTS=1 and review).

var m1Fixtures = []string{
	"pulse_today_get_200_refill",
	"pulse_today_get_200_out_of_cards",
}

// m1Deck is one viewer and their private pool of candidates. The pool shares
// a gender nobody else in the test database has and the viewer looks only for
// it, so the deck holds exactly the candidates this test seeded.
type m1Deck struct {
	t      *testing.T
	env    *d10Env
	viewer uuid.UUID
	gender string
}

func newM1Deck(t *testing.T, cfg service.MechanicsConfig) *m1Deck {
	t.Helper()
	env := setupD10(t)
	env.svc.SetMechanicsConfig(cfg)
	d := &m1Deck{t: t, env: env, viewer: uuid.New(), gender: "m1-" + uuid.NewString()[:8]}
	mustSeedActiveProfile(t, env.st, d.viewer)
	if _, err := env.st.UpsertPreferences(context.Background(), d.viewer, store.UpsertPreferencesParams{InterestedInGender: &d.gender}); err != nil {
		t.Fatalf("viewer preferences: %v", err)
	}
	d.locate(d.viewer, 17.385)
	return d
}

func (d *m1Deck) locate(id uuid.UUID, lat float64) {
	d.t.Helper()
	lng := 78.4867
	if _, err := d.env.st.UpsertProfile(context.Background(), id, store.UpsertProfileParams{Latitude: &lat, Longitude: &lng}); err != nil {
		d.t.Fatalf("location: %v", err)
	}
}

// candidate seeds one active profile into the viewer's pool.
func (d *m1Deck) candidate() uuid.UUID {
	d.t.Helper()
	id := uuid.New()
	ctx := context.Background()
	mustSeedActiveProfile(d.t, d.env.st, id)
	if _, err := d.env.st.UpsertProfile(ctx, id, store.UpsertProfileParams{Gender: &d.gender}); err != nil {
		d.t.Fatalf("candidate gender: %v", err)
	}
	d.locate(id, 17.41)
	hide := false
	if _, err := d.env.st.UpdatePrivacy(ctx, id, store.PrivacyUpdate{HideLastActive: &hide}); err != nil {
		d.t.Fatalf("candidate privacy: %v", err)
	}
	return id
}

type m1DeckBody struct {
	Data []struct {
		CandidateID uuid.UUID `json:"candidate_id"`
	} `json:"data"`
	Meta map[string]any `json:"meta"`
}

// deck fetches the viewer's deck and returns the candidate ids and the meta.
func (d *m1Deck) deck() (map[uuid.UUID]bool, map[string]any) {
	d.t.Helper()
	rec := contractDo(d.env.r, http.MethodGet, "/v1/dating/pulse/today", ``, d.viewer)
	if rec.Code != http.StatusOK {
		d.t.Fatalf("deck: status %d body %s", rec.Code, rec.Body.String())
	}
	var body m1DeckBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		d.t.Fatalf("deck: %v", err)
	}
	ids := map[uuid.UUID]bool{}
	for _, c := range body.Data {
		ids[c.CandidateID] = true
	}
	return ids, body.Meta
}

func (d *m1Deck) spark(to uuid.UUID) {
	d.t.Helper()
	rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/sparks", sparkBody(to, "m1"), d.viewer)
	if rec.Code != http.StatusCreated {
		d.t.Fatalf("spark: status %d body %s", rec.Code, rec.Body.String())
	}
}

func (d *m1Deck) pass(candidate uuid.UUID) {
	d.t.Helper()
	rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/pulse/"+candidate.String()+"/pass", ``, d.viewer)
	if rec.Code != http.StatusOK {
		d.t.Fatalf("pass: status %d body %s", rec.Code, rec.Body.String())
	}
}

func m1Config(free, pass int) service.MechanicsConfig {
	return service.MechanicsConfig{DeckRefill: true, DeckDailyLimitFree: free, DeckDailyLimitPass: pass}
}

func TestM1DeckContracts(t *testing.T) {
	d := newM1Deck(t, m1Config(2, 4))
	first := d.candidate()
	labels := map[uuid.UUID]string{d.viewer: "<viewer>", first: "<candidate>"}

	rec := contractDo(d.env.r, http.MethodGet, "/v1/dating/pulse/today", ``, d.viewer)
	d7AssertBucketsOnly(t, "refilling deck", rec.Body.Bytes())
	assertContract(t, rec, http.StatusOK, "pulse_today_get_200_refill", labels)

	// Both cards of the allowance used: the deck is empty although a third
	// candidate is waiting, and the meta says when the allowance resets.
	d.spark(first)
	second, third := d.candidate(), d.candidate()
	labels[second], labels[third] = "<second>", "<third>"
	if ids, _ := d.deck(); len(ids) != 1 || ids[first] {
		t.Fatalf("with one card of the allowance left the deck = %v, want exactly one unseen candidate", ids)
	}
	ids, _ := d.deck()
	for id := range ids {
		d.pass(id)
	}
	rec = contractDo(d.env.r, http.MethodGet, "/v1/dating/pulse/today", ``, d.viewer)
	assertContract(t, rec, http.StatusOK, "pulse_today_get_200_out_of_cards", labels)
}

func TestM1ContractFixturesWellFormed(t *testing.T) {
	for _, name := range m1Fixtures {
		raw, err := os.ReadFile(filepath.Join(contractsDir, name+".json"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var doc struct {
			Data []any          `json:"data"`
			Meta map[string]any `json:"meta"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: not JSON: %v", name, err)
		}
		if doc.Data == nil || doc.Meta["daily_limit"] == nil {
			t.Fatalf("%s: wants a data array and meta.daily_limit", name)
		}
		if contractUUIDRe.Match(raw) || contractTimeRe.Match(raw) {
			t.Fatalf("%s: carries a raw uuid or timestamp", name)
		}
		if strings.Contains(string(raw), "latitude") || strings.Contains(string(raw), "distance_km") {
			t.Fatalf("%s: carries a precise location field", name)
		}
	}
}

// Guard: a candidate the viewer sparked never comes back to the deck
// (store.CandidateQuery.ExcludeActed).
func TestDeckNeverRepeatsASparkedCandidate(t *testing.T) {
	d := newM1Deck(t, m1Config(50, 50))
	sparked, other := d.candidate(), d.candidate()
	if ids, _ := d.deck(); !ids[sparked] || !ids[other] {
		t.Fatalf("deck before any action = %v, want both candidates", ids)
	}
	d.spark(sparked)
	for i := 0; i < 3; i++ {
		ids, _ := d.deck()
		if ids[sparked] {
			t.Fatalf("fetch %d: the sparked candidate is back in the deck", i+1)
		}
		if !ids[other] {
			t.Fatalf("fetch %d: the untouched candidate left the deck: %v", i+1, ids)
		}
	}
}

// Guard: a matched candidate never comes back either, whichever side sparked
// first — accepting an incoming spark writes no deck spark of its own.
func TestDeckNeverRepeatsAMatchedCandidate(t *testing.T) {
	d := newM1Deck(t, m1Config(50, 50))
	admirer := d.candidate()
	rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/sparks", sparkBody(d.viewer, "m1"), admirer)
	if rec.Code != http.StatusCreated {
		t.Fatalf("incoming spark: status %d body %s", rec.Code, rec.Body.String())
	}
	sp, err := d.env.st.ListIncomingSparks(context.Background(), d.viewer, 10, 0)
	if err != nil || len(sp) != 1 {
		t.Fatalf("incoming sparks = %d err=%v, want 1", len(sp), err)
	}
	rec = contractDo(d.env.r, http.MethodPost, "/v1/dating/sparks/"+sp[0].ID.String()+"/accept", ``, d.viewer)
	if rec.Code != http.StatusCreated {
		t.Fatalf("accept: status %d body %s", rec.Code, rec.Body.String())
	}
	ids, meta := d.deck()
	if ids[admirer] {
		t.Fatalf("the matched candidate is still in the deck")
	}
	// Answering an incoming spark is not a deck card.
	if meta["remaining_today"] != float64(50) || meta["resets_at"] != nil {
		t.Fatalf("accepting a spark spent a deck card: meta = %v", meta)
	}
}

// Guard: the daily allowance stops the deck. With one card allowed and one
// used, the deck is empty even though a fresh candidate exists.
func TestDeckDailyAllowanceStopsTheDeck(t *testing.T) {
	d := newM1Deck(t, m1Config(1, 3))
	a, b := d.candidate(), d.candidate()
	ids, meta := d.deck()
	if len(ids) != 1 {
		t.Fatalf("deck with an allowance of one = %v, want one card", ids)
	}
	if meta["daily_limit"] != float64(1) || meta["remaining_today"] != float64(1) {
		t.Fatalf("meta = %v, want daily_limit 1 remaining_today 1", meta)
	}
	for id := range ids {
		d.pass(id)
	}
	ids, meta = d.deck()
	if len(ids) != 0 {
		t.Fatalf("allowance spent but the deck still serves %v (pool %s, %s)", ids, a, b)
	}
	if meta["daily_limit"] != float64(1) || meta["remaining_today"] != nil {
		t.Fatalf("meta = %v, want daily_limit 1 and no remaining_today", meta)
	}
	resets, _ := meta["resets_at"].(string)
	at, err := time.Parse(time.RFC3339Nano, resets)
	if err != nil || time.Until(at) < 23*time.Hour || time.Until(at) > 24*time.Hour+time.Minute {
		t.Fatalf("resets_at = %q (err %v), want about 24 hours from now", resets, err)
	}
	// Passing the same card twice is one card.
	used, _, err := d.env.st.DeckUsage(context.Background(), d.viewer)
	if err != nil || used != 1 {
		t.Fatalf("ledger usage = %d err=%v, want 1", used, err)
	}
}

// Guard: only an unexpired pass raises the allowance.
func TestDeckPassHolderGetsThePassAllowance(t *testing.T) {
	d := newM1Deck(t, m1Config(1, 3))
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		d.candidate()
	}
	grant := func(interval string) {
		t.Helper()
		tx, err := d.env.st.BeginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `
            INSERT INTO dating_premium_subscriptions (user_id, plan, started_at, expires_at, source)
            VALUES ($1, 'pass_30d', now() - interval '40 days', now() + $2::interval, 'test')
            ON CONFLICT (user_id) DO UPDATE SET expires_at = EXCLUDED.expires_at`, d.viewer, interval); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	grant("-1 day")
	if ids, meta := d.deck(); len(ids) != 1 || meta["daily_limit"] != float64(1) {
		t.Fatalf("expired pass: deck %v meta %v, want the free allowance of 1", ids, meta)
	}
	grant("10 days")
	if ids, meta := d.deck(); len(ids) != 3 || meta["daily_limit"] != float64(3) {
		t.Fatalf("active pass: deck %v meta %v, want the pass allowance of 3", ids, meta)
	}
}

// With the flag off the deck is exactly the pilot's: no allowance in the meta
// and no ledger rows.
func TestDeckRefillOffKeepsThePilotDeck(t *testing.T) {
	d := newM1Deck(t, service.MechanicsConfig{DeckRefill: false, DeckDailyLimitFree: 1})
	a, b := d.candidate(), d.candidate()
	d.pass(a)
	ids, meta := d.deck()
	if !ids[b] || ids[a] {
		t.Fatalf("deck = %v, want only the candidate that was not passed", ids)
	}
	for _, key := range []string{"daily_limit", "remaining_today", "resets_at"} {
		if _, ok := meta[key]; ok {
			t.Fatalf("meta carries %q with the flag off: %v", key, meta)
		}
	}
	if used, _, err := d.env.st.DeckUsage(context.Background(), d.viewer); err != nil || used != 0 {
		t.Fatalf("ledger usage = %d err=%v with the flag off, want 0", used, err)
	}
}

func TestResolveMechanicsConfig(t *testing.T) {
	for env, want := range map[string]bool{"dev": true, "local": true, "": false, "staging": false, "prod": false} {
		cfg, err := ResolveMechanicsConfig(envOf(map[string]string{"ENV": env}))
		if err != nil || cfg.DeckRefill != want {
			t.Fatalf("ENV=%q: deck refill = %v err=%v, want %v", env, cfg.DeckRefill, err, want)
		}
		if cfg.DeckDailyLimitFree != 25 || cfg.DeckDailyLimitPass != 100 {
			t.Fatalf("ENV=%q: default allowances = %d/%d, want 25/100", env, cfg.DeckDailyLimitFree, cfg.DeckDailyLimitPass)
		}
	}
	cfg, err := ResolveMechanicsConfig(envOf(map[string]string{"ENV": "prod", "DATING_DECK_REFILL_ENABLED": "true", "DATING_DECK_DAILY_LIMIT_FREE": "10"}))
	if err != nil || !cfg.DeckRefill || cfg.DeckDailyLimitFree != 10 {
		t.Fatalf("explicit prod opt-in = %+v err=%v", cfg, err)
	}
	if cfg, err := ResolveMechanicsConfig(envOf(map[string]string{"ENV": "dev", "DATING_DECK_REFILL_ENABLED": "false"})); err != nil || cfg.DeckRefill {
		t.Fatalf("explicit dev opt-out = %+v err=%v", cfg, err)
	}
	for key, bad := range map[string]string{
		"DATING_DECK_REFILL_ENABLED":   "yes",
		"DATING_DECK_DAILY_LIMIT_FREE": "0",
		"DATING_DECK_DAILY_LIMIT_PASS": "many",
	} {
		if _, err := ResolveMechanicsConfig(envOf(map[string]string{key: bad})); err == nil || !strings.Contains(err.Error(), key) {
			t.Fatalf("%s=%q: err=%v, want an error naming the key", key, bad, err)
		}
	}
}
