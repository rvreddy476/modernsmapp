package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/premium"
	"github.com/atpost/monetization-service/internal/service"
	"github.com/atpost/monetization-service/internal/service/premiumtest"
	"github.com/google/uuid"
)

// Golden fixtures for the Momentum Premium routes (testdata/contracts/premium
// at the module root). Prices exclude GST (founder, 13 Nov 2026): the
// fixtures pin the breakdown clients show.
//
//	UPDATE_CONTRACTS=1 go test ./internal/http -run TestPremiumContracts
//
// regenerates them; review the diff before committing. Web and Android copy
// them byte for byte.

var (
	pcNow    = time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	pcBuyer  = uuid.MustParse("a1111111-1111-4111-8111-111111111111")
	pcPass   = "b2222222-2222-4222-8222-222222222222"
	pcIntent = "c3333333-3333-4333-8333-333333333333"
)

// premiumContracts serves the four routes on the estimate path (no GSTIN,
// as dev runs) and returns each body with the random ids pinned.
func premiumContracts(t *testing.T) map[string][]byte {
	t.Helper()
	store, pays := premiumtest.New(), premiumtest.NewPayments()
	svc := service.NewPremium(store, premium.Prices{premium.Plan30d: 14900, premium.Plan365d: 149900}).
		WithPayments(pays, false).WithRoles(&premiumtest.Roles{}).WithClock(func() time.Time { return pcNow })
	h := New(nil).WithPremium(svc, true)
	user := pcBuyer.String()

	out := map[string][]byte{}
	w := serveJSON(h, http.MethodGet, "/v1/monetization/premium/plans", user, "")
	if w.Code != http.StatusOK {
		t.Fatalf("plans: %d %s", w.Code, w.Body.String())
	}
	out["plans.json"] = w.Body.Bytes()

	w = serveJSON(h, http.MethodPost, "/v1/monetization/premium/checkout", user, `{"plan":"premium_30d"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("checkout: %d %s", w.Code, w.Body.String())
	}
	var co service.PremiumCheckout
	decodeData(t, w, &co)
	out["checkout.json"] = w.Body.Bytes()

	outcome, err := svc.ApplyPremiumPaymentEvent(context.Background(), premium.Event{EventID: "evt_contract", EventType: "payment.succeeded",
		IntentID: co.IntentID.String(), PeriodID: co.PassID, PayerID: pcBuyer, AmountMinor: co.AmountPaise, Currency: "INR"})
	if err != nil || outcome != premium.OutcomeActivated {
		t.Fatalf("activate: %s %v", outcome, err)
	}
	w = serveJSON(h, http.MethodGet, "/v1/monetization/premium/passes/"+co.PassID.String(), user, "")
	if w.Code != http.StatusOK {
		t.Fatalf("pass: %d %s", w.Code, w.Body.String())
	}
	out["pass.json"] = w.Body.Bytes()
	w = serveJSON(h, http.MethodGet, "/v1/monetization/premium/me", user, "")
	if w.Code != http.StatusOK {
		t.Fatalf("me: %d %s", w.Code, w.Body.String())
	}
	out["me.json"] = w.Body.Bytes()

	pin := strings.NewReplacer(co.PassID.String(), pcPass, co.IntentID.String(), pcIntent,
		"order_"+co.PassID.String()[:8], "order_"+pcPass[:8])
	for k, v := range out {
		out[k] = []byte(pin.Replace(string(v)))
	}
	return out
}

func TestPremiumContracts(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "contracts", "premium")
	update := os.Getenv("UPDATE_CONTRACTS") == "1"
	if update {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, raw := range premiumContracts(t) {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := json.Indent(&buf, bytes.TrimSpace(raw), "", "  "); err != nil {
				t.Fatal(err)
			}
			got := append(buf.Bytes(), '\n')
			path := filepath.Join(dir, name)
			if update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read fixture: %v (UPDATE_CONTRACTS=1 to generate)", err)
			}
			if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
				t.Fatalf("fixture %s is stale.\n--- want\n%s\n--- got\n%s", name, want, got)
			}
		})
	}
}

// The fixtures carry what the contract says: prices exclude GST, the amount
// charged is the total, and the split adds up.
func TestPremiumFixturesMeanWhatTheContractSays(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "contracts", "premium")
	read := func(name string) map[string]any {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		var env map[string]any
		if err := json.Unmarshal(b, &env); err != nil {
			t.Fatal(err)
		}
		return env["data"].(map[string]any)
	}
	num := func(m map[string]any, k string) float64 {
		v, ok := m[k].(float64)
		if !ok {
			t.Fatalf("%q is not a number: %v", k, m[k])
		}
		return v
	}
	plans, _ := read("plans.json")["plans"].([]any)
	if len(plans) != 2 {
		t.Fatalf("plans: %v", plans)
	}
	for i, w := range []struct{ price, gst, total float64 }{{14900, 2682, 17582}, {149900, 26982, 176882}} {
		p := plans[i].(map[string]any)
		if num(p, "price_paise") != w.price || num(p, "taxable_paise") != w.price || num(p, "gst_paise") != w.gst ||
			num(p, "total_paise") != w.total || p["prices_exclude_gst"] != true || p["available"] != true {
			t.Fatalf("plan %d: %v", i, p)
		}
	}
	for _, name := range []string{"checkout.json", "pass.json"} {
		d := read(name)
		for _, k := range []string{"taxable_paise", "gst_paise", "cgst_paise", "sgst_paise", "igst_paise", "total_paise",
			"gst_rate_bp", "sac", "tax_provisional", "prices_exclude_gst", "amount_paise", "currency", "pass_id", "plan", "status"} {
			if _, ok := d[k]; !ok {
				t.Errorf("%s lacks %q", name, k)
			}
		}
		if num(d, "amount_paise") != 17582 || num(d, "total_paise") != 17582 || num(d, "taxable_paise") != 14900 ||
			num(d, "cgst_paise")+num(d, "sgst_paise")+num(d, "igst_paise") != num(d, "gst_paise") ||
			num(d, "taxable_paise")+num(d, "gst_paise") != num(d, "total_paise") || d["prices_exclude_gst"] != true {
			t.Fatalf("%s: %v", name, d)
		}
	}
	if s := read("pass.json")["status"]; s != "paid" {
		t.Fatalf("pass status %v", s)
	}
	me := read("me.json")
	if me["active"] != true {
		t.Fatalf("me: %v", me)
	}
	if rp, _ := me["renewal_plans"].([]any); len(rp) != 2 || rp[0].(map[string]any)["total_paise"] != float64(17582) {
		t.Fatalf("me renewal_plans: %v", me["renewal_plans"])
	}
}
