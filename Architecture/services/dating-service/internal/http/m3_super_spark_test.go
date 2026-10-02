package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atpost/dating-service/internal/service"
	"github.com/google/uuid"
)

// Mechanic M3 — Super Spark (DATING_SUPER_SPARK_ENABLED): golden fixtures and
// the guards behind them. Same harness as m1_deck_test.go.

var m3Fixtures = []string{
	"spark_create_post_201_super",
	"spark_create_429_super_limit_reached",
	"sparks_incoming_get_200_super_first",
	"premium_catalogue_get_200_super_spark",
}

func m3Config() service.MechanicsConfig {
	return service.MechanicsConfig{
		DeckRefill: true, DeckDailyLimitFree: 50, DeckDailyLimitPass: 50,
		SuperSpark: true, SuperSparkDailyLimitFree: 1, SuperSparkDailyLimitPass: 2,
	}
}

func superSparkBody(to uuid.UUID, ref string) string {
	return fmt.Sprintf(`{"to_user_id":%q,"target_kind":"prompt","target_ref":%q,"super":true}`, to.String(), ref)
}

func (d *m1Deck) superSpark(to uuid.UUID, ref string) *httptest.ResponseRecorder {
	return contractDo(d.env.r, http.MethodPost, "/v1/dating/sparks", superSparkBody(to, ref), d.viewer)
}

func (d *m1Deck) mustSuperSpark(to uuid.UUID) {
	d.t.Helper()
	if rec := d.superSpark(to, "m3"); rec.Code != http.StatusCreated {
		d.t.Fatalf("super spark: status %d body %s", rec.Code, rec.Body.String())
	}
}

// superUsage is the viewer's daily Super Sparks used and purchased balance.
func (d *m1Deck) superUsage() (int, int) {
	d.t.Helper()
	used, _, balance, err := d.env.st.SuperSparkUsage(context.Background(), d.viewer)
	if err != nil {
		d.t.Fatal(err)
	}
	return used, balance
}

func (d *m1Deck) exec(sql string, args ...any) {
	d.t.Helper()
	ctx := context.Background()
	tx, err := d.env.st.BeginTx(ctx)
	if err != nil {
		d.t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, sql, args...); err != nil {
		_ = tx.Rollback(ctx)
		d.t.Fatalf("exec: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		d.t.Fatal(err)
	}
}

// giveSuperSparks sets the viewer's purchased balance.
func (d *m1Deck) giveSuperSparks(n int) {
	d.exec(`INSERT INTO dating_super_spark_balances (user_id, balance) VALUES ($1, $2)
        ON CONFLICT (user_id) DO UPDATE SET balance = EXCLUDED.balance`, d.viewer, n)
}

func (d *m1Deck) sparkCountTo(to uuid.UUID) int {
	d.t.Helper()
	sp, err := d.env.st.ListIncomingSparks(context.Background(), to, 50, 0)
	if err != nil {
		d.t.Fatal(err)
	}
	n := 0
	for _, s := range sp {
		if s.FromUserID == d.viewer {
			n++
		}
	}
	return n
}

func TestM3SuperSparkContracts(t *testing.T) {
	d := newM1Deck(t, m3Config())
	first, second := d.candidate(), d.candidate()
	labels := map[uuid.UUID]string{d.viewer: "<viewer>", first: "<candidate>", second: "<second>"}

	assertContract(t, d.superSpark(first, "m3"), http.StatusCreated, "spark_create_post_201_super", labels)
	assertContract(t, d.superSpark(second, "m3"), http.StatusTooManyRequests, "spark_create_429_super_limit_reached", labels)

	// The recipient's list: the Super Spark first and marked, although the
	// ordinary spark is newer.
	other := uuid.New()
	mustSeedActiveProfile(t, d.env.st, other)
	labels[other] = "<other>"
	if rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/sparks", sparkBody(first, "m3"), other); rec.Code != http.StatusCreated {
		t.Fatalf("ordinary spark: status %d body %s", rec.Code, rec.Body.String())
	}
	rec := contractDo(d.env.r, http.MethodGet, "/v1/dating/sparks/incoming", ``, first)
	d7AssertBucketsOnly(t, "incoming sparks", rec.Body.Bytes())
	assertContract(t, rec, http.StatusOK, "sparks_incoming_get_200_super_first", labels)

	assertContract(t, contractDo(d.env.r, http.MethodGet, "/v1/dating/premium/catalogue", ``, d.viewer),
		http.StatusOK, "premium_catalogue_get_200_super_spark", nil)
}

func TestM3ContractFixturesWellFormed(t *testing.T) {
	for _, name := range m3Fixtures {
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
		if strings.Contains(string(raw), "declined_at") {
			t.Fatalf("%s: exposes declined_at", name)
		}
	}
}

// Guard: the daily allowance. One Super Spark a day for a free user; the
// refused one leaves no spark behind and uses none of the ordinary allowance.
func TestSuperSparkDailyAllowance(t *testing.T) {
	d := newM1Deck(t, m3Config())
	a, b := d.candidate(), d.candidate()
	d.mustSuperSpark(a)
	rec := d.superSpark(b, "m3")
	d.wantRefusal(rec, http.StatusTooManyRequests, "SUPER_SPARK_LIMIT_REACHED")
	var body struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Error.Details["limit"] != float64(1) || body.Error.Details["window_hours"] != float64(24) || body.Error.Details["resets_at"] == nil {
		t.Fatalf("details = %v, want limit 1, window_hours 24 and resets_at", body.Error.Details)
	}
	if n := d.sparkCountTo(b); n != 0 {
		t.Fatalf("a refused Super Spark left %d spark(s) behind", n)
	}
	if ids, _ := d.deck(); !ids[b] {
		t.Fatalf("a refused Super Spark took the card out of the deck")
	}
	// An ordinary spark to the same person still goes through.
	d.spark(b)
}

// Guard: beyond the daily allowance a purchased Super Spark is spent, one per
// person, and when the balance is gone the refusal returns.
func TestSuperSparkSpendsThePurchasedBalance(t *testing.T) {
	d := newM1Deck(t, m3Config())
	a, b, c := d.candidate(), d.candidate(), d.candidate()
	d.giveSuperSparks(1)
	d.mustSuperSpark(a)
	if used, balance := d.superUsage(); used != 1 || balance != 1 {
		t.Fatalf("after the daily one: used=%d balance=%d, want 1 and 1 (the allowance goes first)", used, balance)
	}
	d.mustSuperSpark(b)
	if used, balance := d.superUsage(); used != 1 || balance != 0 {
		t.Fatalf("after the purchased one: used=%d balance=%d, want 1 and 0", used, balance)
	}
	d.wantRefusal(d.superSpark(c, "m3"), http.StatusTooManyRequests, "SUPER_SPARK_LIMIT_REACHED")
}

// Guard: a person is charged for once. Repeating the Super Spark, or sending
// one on another part of the same profile, costs nothing more.
func TestSuperSparkSamePersonIsChargedOnce(t *testing.T) {
	d := newM1Deck(t, m3Config())
	a := d.candidate()
	d.giveSuperSparks(3)
	d.mustSuperSpark(a)
	d.mustSuperSpark(a)
	if rec := d.superSpark(a, "another-prompt"); rec.Code != http.StatusCreated {
		t.Fatalf("second target: status %d body %s", rec.Code, rec.Body.String())
	}
	if used, balance := d.superUsage(); used != 1 || balance != 3 {
		t.Fatalf("used=%d balance=%d after three Super Sparks to one person, want 1 and 3", used, balance)
	}
}

// Guard: only an unexpired pass raises the daily allowance.
func TestSuperSparkPassHolderAllowance(t *testing.T) {
	d := newM1Deck(t, m3Config())
	a, b, c := d.candidate(), d.candidate(), d.candidate()
	d.grantPass()
	d.mustSuperSpark(a)
	d.mustSuperSpark(b)
	d.wantRefusal(d.superSpark(c, "m3"), http.StatusTooManyRequests, "SUPER_SPARK_LIMIT_REACHED")
	d.exec(`UPDATE dating_premium_subscriptions SET expires_at = now() - interval '1 day' WHERE user_id = $1`, d.viewer)
	d.exec(`DELETE FROM dating_super_spark_ledger WHERE user_id = $1 AND to_user_id = $2`, d.viewer, b)
	// One of the two is still inside the window: an expired pass is back to
	// the free allowance of one.
	d.wantRefusal(d.superSpark(c, "m3"), http.StatusTooManyRequests, "SUPER_SPARK_LIMIT_REACHED")
}

// Guard: a block applies to a Super Spark, and the refusal charges nothing.
func TestSuperSparkRefusesABlockedRecipient(t *testing.T) {
	d := newM1Deck(t, m3Config())
	a := d.candidate()
	if rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/safety/block", `{"target_user_id":"`+d.viewer.String()+`"}`, a); rec.Code != http.StatusOK {
		t.Fatalf("block: status %d body %s", rec.Code, rec.Body.String())
	}
	d.wantRefusal(d.superSpark(a, "m3"), http.StatusNotFound, "CANDIDATE_UNAVAILABLE")
	if used, _ := d.superUsage(); used != 0 {
		t.Fatalf("a refused Super Spark was charged")
	}
}

// With the flag off a Super Spark is refused outright, its packs are neither
// listed nor sold, and nothing is written.
func TestSuperSparkOffIsRefused(t *testing.T) {
	d := newM1Deck(t, service.MechanicsConfig{DeckRefill: true})
	a := d.candidate()
	d.wantRefusal(d.superSpark(a, "m3"), http.StatusNotFound, "MECHANIC_NOT_ENABLED")
	if n := d.sparkCountTo(a); n != 0 {
		t.Fatalf("a refused Super Spark left %d spark(s) behind", n)
	}
	rec := contractDo(d.env.r, http.MethodGet, "/v1/dating/premium/catalogue", ``, d.viewer)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "super_spark") {
		t.Fatalf("catalogue with the flag off lists the packs: %d %s", rec.Code, rec.Body.String())
	}
	rec = contractDo(d.env.r, http.MethodPost, "/v1/dating/premium/purchases", `{"product":"super_spark_5","idempotency_key":"k-`+uuid.NewString()+`"}`, d.viewer)
	d.wantRefusal(rec, http.StatusBadRequest, "INVALID_PRODUCT")
}
