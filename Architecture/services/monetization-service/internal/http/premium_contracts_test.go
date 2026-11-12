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
	pcNow     = time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	pcClock   = pcNow
	pcBuyer   = uuid.MustParse("a1111111-1111-4111-8111-111111111111")
	pcPass    = "b2222222-2222-4222-8222-222222222222"
	pcIntent  = "c3333333-3333-4333-8333-333333333333"
	pcInvoice = "d4444444-4444-4444-8444-444444444444"
	// The credit-note fixture: a second buyer whose pass is refunded in full.
	pcBuyer2  = uuid.MustParse("e5555555-5555-4555-8555-555555555555")
	pcPass2   = "f6666666-6666-4666-8666-666666666666"
	pcIntent2 = "a7777777-7777-4777-8777-777777777777"
	pcInv2    = "b8888888-8888-4888-8888-888888888888"
	pcCredit  = "c9999999-9999-4999-8999-999999999999"
	// pcSupplier is synthetic: no real company or GSTIN.
	pcSupplier = premium.Supplier{LegalName: "Example Supplier Private Limited", Address: "1 Example Road, Bengaluru 560001",
		GSTIN: "29AAAAA0000A1Z0", StateCode: "29", StateName: "Karnataka"}
)

// premiumContracts serves the four routes on the estimate path (no GSTIN,
// as dev runs) and returns each body with the random ids pinned.
func premiumContracts(t *testing.T) map[string][]byte {
	t.Helper()
	store, pays := premiumtest.New(), premiumtest.NewPayments()
	svc := service.NewPremium(store, premium.Prices{premium.Plan30d: 14900, premium.Plan365d: 149900}).
		WithPayments(pays, false).WithRoles(&premiumtest.Roles{}).WithClock(func() time.Time { return pcClock }).
		WithSupplier(pcSupplier).WithProfiles(contractProfiles{pcBuyer: "Asha Rao"})
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

	// Tax documents (migration 029).
	w = serveJSON(h, http.MethodGet, "/v1/monetization/premium/invoices", user, "")
	if w.Code != http.StatusOK {
		t.Fatalf("invoices: %d %s", w.Code, w.Body.String())
	}
	out["invoices.json"] = w.Body.Bytes()
	var list struct {
		Invoices []service.PremiumInvoiceSummary `json:"invoices"`
	}
	decodeData(t, w, &list)
	if len(list.Invoices) != 1 {
		t.Fatalf("invoices: %+v", list)
	}
	invID := list.Invoices[0].ID
	w = serveJSON(h, http.MethodGet, "/v1/monetization/premium/invoices/"+invID.String(), user, "")
	if w.Code != http.StatusOK {
		t.Fatalf("invoice: %d %s", w.Code, w.Body.String())
	}
	out["invoice.json"] = w.Body.Bytes()

	// A second buyer, refunded in full the next day: the credit note.
	user2 := pcBuyer2.String()
	w = serveJSON(h, http.MethodPost, "/v1/monetization/premium/checkout", user2, `{"plan":"premium_30d"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("checkout 2: %d %s", w.Code, w.Body.String())
	}
	var co2 service.PremiumCheckout
	decodeData(t, w, &co2)
	ev2 := premium.Event{EventID: "evt_contract_2", EventType: "payment.succeeded", IntentID: co2.IntentID.String(),
		PeriodID: co2.PassID, PayerID: pcBuyer2, AmountMinor: co2.AmountPaise, Currency: "INR"}
	if o, err := svc.ApplyPremiumPaymentEvent(context.Background(), ev2); err != nil || o != premium.OutcomeActivated {
		t.Fatalf("activate 2: %s %v", o, err)
	}
	pcClock = pcNow.Add(24 * time.Hour)
	defer func() { pcClock = pcNow }()
	ev2.EventID, ev2.EventType = "evt_contract_2_refund", "payment.refunded"
	if o, err := svc.ApplyPremiumPaymentEvent(context.Background(), ev2); err != nil || o != premium.OutcomeRefunded {
		t.Fatalf("refund 2: %s %v", o, err)
	}
	decodeData(t, serveJSON(h, http.MethodGet, "/v1/monetization/premium/invoices", user2, ""), &list)
	if len(list.Invoices) != 2 || list.Invoices[0].Kind != premium.KindCreditNote {
		t.Fatalf("invoices 2: %+v", list)
	}
	creditID, inv2ID := list.Invoices[0].ID, list.Invoices[1].ID
	w = serveJSON(h, http.MethodGet, "/v1/monetization/premium/invoices/"+creditID.String(), user2, "")
	if w.Code != http.StatusOK {
		t.Fatalf("credit note: %d %s", w.Code, w.Body.String())
	}
	out["credit_note.json"] = w.Body.Bytes()

	pin := strings.NewReplacer(co.PassID.String(), pcPass, co.IntentID.String(), pcIntent,
		"order_"+co.PassID.String()[:8], "order_"+pcPass[:8], invID.String(), pcInvoice,
		co2.PassID.String(), pcPass2, co2.IntentID.String(), pcIntent2, inv2ID.String(), pcInv2, creditID.String(), pcCredit)
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

// contractProfiles names buyers for the invoice fixtures.
type contractProfiles map[uuid.UUID]string

func (c contractProfiles) Profiles(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]service.PublicProfile, error) {
	out := map[uuid.UUID]service.PublicProfile{}
	for _, id := range ids {
		if n, ok := c[id]; ok {
			out[id] = service.PublicProfile{DisplayName: n}
		}
	}
	return out, nil
}

// The invoice fixtures carry what a GST document must: the pass's split to
// the paisa, the supplier, the place of supply, a 16-character number, no
// provisional flag; and the credit note is the exact negative of its invoice.
func TestPremiumInvoiceFixturesMeanWhatTheContractSays(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "contracts", "premium")
	read := func(name string) map[string]any {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(name, "invoice") || strings.Contains(name, "credit") {
			if strings.Contains(string(b), "provisional") {
				t.Fatalf("%s exposes tax_provisional", name)
			}
		}
		var env map[string]any
		if err := json.Unmarshal(b, &env); err != nil {
			t.Fatal(err)
		}
		return env["data"].(map[string]any)
	}
	list, _ := read("invoices.json")["invoices"].([]any)
	if len(list) != 1 {
		t.Fatalf("invoices: %v", list)
	}
	row := list[0].(map[string]any)
	if row["number"] != "MP/26-27/000001" || row["kind"] != "invoice" || row["total_paise"] != float64(17582) || row["pass_id"] != pcPass {
		t.Fatalf("list row %v", row)
	}
	inv, cn := read("invoice.json"), read("credit_note.json")
	if inv["taxable_paise"] != float64(14900) || inv["cgst_paise"] != float64(1341) || inv["sgst_paise"] != float64(1341) ||
		inv["igst_paise"] != float64(0) || inv["total_paise"] != float64(17582) || inv["reverse_charge"] != false ||
		inv["buyer_name"] != "Asha Rao" || inv["original_invoice"] != nil {
		t.Fatalf("invoice %v", inv)
	}
	if pos := inv["place_of_supply"].(map[string]any); pos["state_code"] != "29" {
		t.Fatalf("place of supply %v", pos)
	}
	if line := inv["lines"].([]any)[0].(map[string]any); line["sac"] != "998439" || line["gst_rate_bp"] != float64(1800) ||
		line["description"] != "Momentum Premium — 30 days" {
		t.Fatalf("line %v", line)
	}
	for _, k := range []string{"taxable_paise", "cgst_paise", "sgst_paise", "igst_paise", "total_paise"} {
		if cn[k].(float64) != -inv[k].(float64) {
			t.Errorf("credit note %s = %v, invoice %v", k, cn[k], inv[k])
		}
	}
	orig := cn["original_invoice"].(map[string]any)
	if cn["number"] != "MPC/26-27/000001" || cn["kind"] != "credit_note" || orig["number"] != "MP/26-27/000002" || orig["id"] != pcInv2 ||
		cn["date"] != "2026-11-14" || orig["date"] != "2026-11-13" {
		t.Fatalf("credit note %v", cn)
	}
	if pass := read("pass.json"); pass["invoice_id"] != pcInvoice {
		t.Fatalf("pass.invoice_id %v", pass["invoice_id"])
	}
}
