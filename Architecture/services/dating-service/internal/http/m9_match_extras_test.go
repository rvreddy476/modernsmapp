package http

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/atpost/dating-service/internal/payments"
	"github.com/atpost/dating-service/internal/service"
	"github.com/google/uuid"
)

// Mechanic M9 — in-match extras: golden fixtures and the guards behind them.
// Same harness as m1_deck_test.go, with a recording chat stub.

var m9Fixtures = []string{
	"read_receipts_get_200",
	"read_receipts_put_200",
	"read_receipts_put_403_requires_pass",
	"read_receipts_get_404_not_enabled",
	"match_get_200_can_call",
}

// receiptsPush is one read-receipt push to chat.
type receiptsPush struct {
	match, user uuid.UUID
	until       *time.Time
}

// extrasChat records conversations, read-receipt pushes, and answers the
// call check with `callable`.
type extrasChat struct {
	chatRecorder
	mu       sync.Mutex
	pushes   []receiptsPush
	callable bool
	calls    int
}

func (c *extrasChat) SetReadReceipts(_ context.Context, matchID, userID uuid.UUID, until *time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pushes = append(c.pushes, receiptsPush{matchID, userID, until})
	return nil
}

func (c *extrasChat) MatchCallable(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return c.callable, nil
}

// lastPush is the most recent push for user (nil, false when none).
func (c *extrasChat) lastPush(user uuid.UUID) (*time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.pushes) - 1; i >= 0; i-- {
		if c.pushes[i].user == user {
			return c.pushes[i].until, true
		}
	}
	return nil, false
}

func m9Config(receipts, calls bool) service.MechanicsConfig {
	return service.MechanicsConfig{DeckRefill: true, DeckDailyLimitFree: 50, DeckDailyLimitPass: 50, ReadReceipts: receipts, CallAfterExchange: calls}
}

func newM9Deck(t *testing.T, receipts, calls bool) (*m1Deck, *extrasChat) {
	t.Helper()
	d := newM1Deck(t, m9Config(receipts, calls))
	chat := &extrasChat{}
	d.env.svc.SetMessageClient(chat)
	return d, chat
}

func TestM9MatchExtrasContracts(t *testing.T) {
	d, chat := newM9Deck(t, true, true)
	labels := map[uuid.UUID]string{d.viewer: "<viewer>"}
	assertContract(t, contractDo(d.env.r, http.MethodGet, "/v1/dating/read-receipts", ``, d.viewer), http.StatusOK, "read_receipts_get_200", labels)
	assertContract(t, contractDo(d.env.r, http.MethodPut, "/v1/dating/read-receipts", `{"enabled":true}`, d.viewer),
		http.StatusForbidden, "read_receipts_put_403_requires_pass", labels)
	d.grantPass()
	assertContract(t, contractDo(d.env.r, http.MethodPut, "/v1/dating/read-receipts", `{"enabled":true}`, d.viewer),
		http.StatusOK, "read_receipts_put_200", labels)

	// The pair is ordered so the fixture never depends on random ids.
	other := d.candidate()
	for other.String() < d.viewer.String() {
		other = d.candidate()
	}
	match := d.matchWith(other)
	chat.callable = true
	labels[other], labels[match] = "<other>", "<match>"
	assertContract(t, contractDo(d.env.r, http.MethodGet, "/v1/dating/matches/"+match.String(), ``, d.viewer),
		http.StatusOK, "match_get_200_can_call", labels)

	off, _ := newM9Deck(t, false, false)
	assertContract(t, contractDo(off.env.r, http.MethodGet, "/v1/dating/read-receipts", ``, off.viewer),
		http.StatusNotFound, "read_receipts_get_404_not_enabled", map[uuid.UUID]string{off.viewer: "<viewer>"})
}

func TestM9ContractFixturesWellFormed(t *testing.T) {
	for _, name := range m9Fixtures {
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

// Guard: chat creates the conversation with the M9 rules while they are on,
// and without them while they are off.
func TestMatchExtrasRulesReachChat(t *testing.T) {
	for _, on := range []bool{true, false} {
		d, chat := newM9Deck(t, on, on)
		d.matchWith(d.candidate())
		chat.chatRecorder.mu.Lock()
		req := chat.creates[len(chat.creates)-1]
		chat.chatRecorder.mu.Unlock()
		if req.ReceiptsGated != on || req.CallAfterExchange != on {
			t.Fatalf("flags %v: chat got receipts_gated=%v call_after_exchange=%v", on, req.ReceiptsGated, req.CallAfterExchange)
		}
	}
}

// Guard: read receipts need a pass to turn on, and chat hears the pass
// expiry as the until-time; turning them off pushes nil.
func TestReadReceiptsPushThePassExpiry(t *testing.T) {
	d, chat := newM9Deck(t, true, false)
	other := d.candidate()
	match := d.matchWith(other)
	if until, ok := chat.lastPush(d.viewer); !ok || until != nil {
		t.Fatalf("a new match pushed %v for the viewer (opted out), want nil", until)
	}
	d.wantRefusal(contractDo(d.env.r, http.MethodPut, "/v1/dating/read-receipts", `{"enabled":true}`, d.viewer), http.StatusForbidden, "READ_RECEIPTS_REQUIRE_PASS")
	d.grantPass()
	if rec := contractDo(d.env.r, http.MethodPut, "/v1/dating/read-receipts", `{"enabled":true}`, d.viewer); rec.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", rec.Code, rec.Body.String())
	}
	ent, err := d.env.st.GetPremiumEntitlement(context.Background(), d.viewer)
	if err != nil || ent.PassExpiresAt == nil {
		t.Fatalf("entitlement: %+v err=%v", ent, err)
	}
	until, ok := chat.lastPush(d.viewer)
	if !ok || until == nil || !until.Equal(ent.PassExpiresAt.UTC()) {
		t.Fatalf("pushed until = %v, want the pass expiry %v", until, ent.PassExpiresAt)
	}
	if rec := contractDo(d.env.r, http.MethodPut, "/v1/dating/read-receipts", `{"enabled":false}`, d.viewer); rec.Code != http.StatusOK {
		t.Fatalf("disable: %d", rec.Code)
	}
	if until, _ := chat.lastPush(d.viewer); until != nil {
		t.Fatalf("disabling pushed %v, want nil", until)
	}
	_ = match
}

// Guard: a pass payment re-syncs the until-time.
func TestReadReceiptsFollowPassPayments(t *testing.T) {
	d, chat := newM9Deck(t, true, false)
	d.matchWith(d.candidate())
	d.grantPass()
	if rec := contractDo(d.env.r, http.MethodPut, "/v1/dating/read-receipts", `{"enabled":true}`, d.viewer); rec.Code != http.StatusOK {
		t.Fatalf("enable: %d", rec.Code)
	}
	before := len(chat.pushes)
	d.exec(`UPDATE dating_premium_subscriptions SET expires_at = now() + interval '40 days' WHERE user_id = $1`, d.viewer)
	d.env.svc.OnPremiumPaymentApplied(context.Background(), payments.Applied{
		UserID: d.viewer, Product: payments.ProductPass30d, Decision: payments.Decision{Effect: payments.EffectGrant},
	})
	if len(chat.pushes) <= before {
		t.Fatalf("a pass grant pushed nothing to chat")
	}
	until, _ := chat.lastPush(d.viewer)
	if until == nil || time.Until(*until) < 39*24*time.Hour {
		t.Fatalf("pushed until = %v after the grant, want the new expiry", until)
	}
	// A refund that ends the pass switches receipts off, though the opt-in
	// stays on.
	d.exec(`UPDATE dating_premium_subscriptions SET expires_at = now() - interval '1 minute' WHERE user_id = $1`, d.viewer)
	d.env.svc.OnPremiumPaymentApplied(context.Background(), payments.Applied{
		UserID: d.viewer, Product: payments.ProductPass30d, Decision: payments.Decision{Effect: payments.EffectRevoke},
	})
	if until, _ := chat.lastPush(d.viewer); until != nil {
		t.Fatalf("pushed until = %v after the pass ended, want nil", until)
	}
}

// Guard: can_call is chat's answer, and is omitted while the mechanic is off.
func TestCanCallFollowsChat(t *testing.T) {
	d, chat := newM9Deck(t, false, true)
	match := d.matchWith(d.candidate())
	get := func(dd *m1Deck) map[string]any {
		rec := contractDo(dd.env.r, http.MethodGet, "/v1/dating/matches/"+match.String(), ``, dd.viewer)
		var body struct {
			Data map[string]any `json:"data"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return body.Data
	}
	if got := get(d)["can_call"]; got != false {
		t.Fatalf("before both wrote: can_call = %v, want false", got)
	}
	chat.callable = true
	if got := get(d)["can_call"]; got != true {
		t.Fatalf("after both wrote: can_call = %v, want true", got)
	}
	d.env.svc.SetMechanicsConfig(m9Config(false, false))
	if _, present := get(d)["can_call"]; present {
		t.Fatalf("can_call present while the mechanic is off")
	}
}
