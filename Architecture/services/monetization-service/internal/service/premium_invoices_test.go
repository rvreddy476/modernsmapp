package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/premium"
	"github.com/google/uuid"
)

// Momentum Premium tax invoices and credit notes against the in-memory
// store, which issues them through the same premium.DocumentFor /
// NewInvoice / NewCreditNote as the Postgres store.

type fixedProfiles map[uuid.UUID]PublicProfile

func (f fixedProfiles) Profiles(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]PublicProfile, error) {
	out := map[uuid.UUID]PublicProfile{}
	for _, id := range ids {
		if p, ok := f[id]; ok {
			out[id] = p
		}
	}
	return out, nil
}

var invSupplier = premium.Supplier{LegalName: "Example Supplier Private Limited", Address: "1 Example Road, Bengaluru 560001",
	GSTIN: "29AAAAA0000A1Z0", StateCode: "29", StateName: "Karnataka"}

func newInvoiceRig(t *testing.T) *premiumRig {
	t.Helper()
	r := newPremiumRig(t, priced())
	r.svc.WithSupplier(invSupplier)
	return r
}

// buy checks out and captures a pass for buyer.
func (r *premiumRig) buy(t *testing.T, buyer uuid.UUID, plan string) (*PremiumCheckout, premium.Event) {
	t.Helper()
	r.buyer = buyer
	co := r.checkout(t, plan)
	ev := r.event(t, co, "payment.succeeded")
	if out := r.apply(t, ev); out != premium.OutcomeActivated {
		t.Fatalf("activate: %s", out)
	}
	return co, ev
}

func (r *premiumRig) docsOf(t *testing.T, user uuid.UUID) []PremiumInvoiceSummary {
	t.Helper()
	out, err := r.svc.Invoices(r.ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPremiumInvoiceIssuedOnceOnTheSignedCapture(t *testing.T) {
	r := newInvoiceRig(t)
	a := uuid.New()
	r.svc.WithProfiles(fixedProfiles{a: {DisplayName: "Asha Rao"}})
	r.buyer = a
	co := r.checkout(t, premium.Plan30d)
	if len(r.store.Invoices) != 0 {
		t.Fatal("a checkout issues no invoice")
	}
	ev := r.event(t, co, "payment.succeeded")
	if out := r.apply(t, ev); out != premium.OutcomeActivated {
		t.Fatal(out)
	}
	docs := r.docsOf(t, a)
	if len(docs) != 1 || docs[0].Kind != premium.KindInvoice || docs[0].Number != "MP/26-27/000001" ||
		docs[0].TotalPaise != co.AmountPaise || docs[0].PassID != co.PassID || docs[0].Date != "2026-11-11" {
		t.Fatalf("invoices %+v", docs)
	}
	inv, err := r.svc.Invoice(r.ctx, a, docs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	// The invoice's split is the pass's split, to the paisa.
	if inv.TaxablePaise != *co.TaxablePaise || inv.CGSTPaise != *co.CGSTPaise || inv.SGSTPaise != *co.SGSTPaise ||
		inv.IGSTPaise != *co.IGSTPaise || inv.TotalPaise != *co.TotalPaise || inv.Lines[0].GSTRateBP != *co.GSTRateBP ||
		inv.Lines[0].SAC != "998439" || inv.Lines[0].Description != "Momentum Premium — 30 days" {
		t.Fatalf("invoice %+v vs checkout %+v", inv, co.PremiumTaxView)
	}
	if inv.BuyerName == nil || *inv.BuyerName != "Asha Rao" || inv.Supplier.GSTIN != invSupplier.GSTIN ||
		inv.PlaceOfSupply.StateCode != "29" || inv.ReverseCharge || inv.OriginalInvoice != nil {
		t.Fatalf("invoice %+v", inv)
	}

	// Replays: the same event (duplicate) and a second capture event for the
	// same pass (ignored) issue nothing and take no number.
	if out := r.apply(t, ev); out != premium.OutcomeDuplicate {
		t.Fatal(out)
	}
	if out := r.apply(t, r.event(t, co, "payment.succeeded")); out != premium.OutcomeIgnored {
		t.Fatal(out)
	}
	if len(r.store.Invoices) != 1 || r.store.Counters["MP|26-27"] != 1 {
		t.Fatalf("replays issued: %d invoices, counter %d", len(r.store.Invoices), r.store.Counters["MP|26-27"])
	}
	// The pass names its invoice.
	pass, err := r.svc.Pass(r.ctx, a, co.PassID)
	if err != nil || pass.InvoiceID == nil || *pass.InvoiceID != docs[0].ID {
		t.Fatalf("pass %+v %v", pass, err)
	}
	// One event, ids only.
	if got := r.pub.Documents; len(got) != 1 || got[0].Type != premium.EventInvoiceIssued ||
		got[0].Payload.InvoiceID != docs[0].ID.String() || got[0].Payload.PassID != co.PassID.String() || got[0].Payload.UserID != a.String() {
		t.Fatalf("document events %+v", got)
	}
}

// A capture the decision refuses (wrong amount) issues no invoice.
func TestPremiumNoInvoiceForARefusedCapture(t *testing.T) {
	r := newInvoiceRig(t)
	co := r.checkout(t, premium.Plan30d)
	ev := r.event(t, co, "payment.succeeded")
	ev.AmountMinor = *co.TaxablePaise
	if out := r.apply(t, ev); out != premium.OutcomeMismatch {
		t.Fatal(out)
	}
	if len(r.store.Invoices) != 0 || len(r.store.Counters) != 0 {
		t.Fatal("a refused capture took a number")
	}
}

func TestPremiumInvoiceNumbersAreConsecutiveAndResetOnFirstAprilIST(t *testing.T) {
	r := newInvoiceRig(t)
	var numbers []string
	r.now = time.Date(2027, 3, 31, 18, 29, 0, 0, time.UTC) // 23:59 IST, 31 March
	for i := 0; i < 3; i++ {
		r.buy(t, uuid.New(), premium.Plan30d)
	}
	r.now = time.Date(2027, 3, 31, 18, 30, 0, 0, time.UTC) // 00:00 IST, 1 April
	for i := 0; i < 2; i++ {
		r.buy(t, uuid.New(), premium.Plan365d)
	}
	for _, inv := range r.store.Invoices {
		numbers = append(numbers, inv.Number+"@"+inv.InvoiceDate)
	}
	joined := strings.Join(numbers, " ")
	for _, want := range []string{"MP/26-27/000001@2027-03-31", "MP/26-27/000002@2027-03-31", "MP/26-27/000003@2027-03-31",
		"MP/27-28/000001@2027-04-01", "MP/27-28/000002@2027-04-01"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %s in %s", want, joined)
		}
	}
	if len(numbers) != 5 {
		t.Fatalf("issued %d", len(numbers))
	}
}

// A store failure rolls the whole event back: no number is taken, and the
// retry gets the next consecutive number.
func TestPremiumInvoiceFailedApplyTakesNoNumber(t *testing.T) {
	r := newInvoiceRig(t)
	r.buy(t, uuid.New(), premium.Plan30d)
	r.buyer = uuid.New()
	co := r.checkout(t, premium.Plan30d)
	ev := r.event(t, co, "payment.succeeded")
	r.store.FailApply = errors.New("database down")
	if _, err := r.svc.ApplyPremiumPaymentEvent(r.ctx, ev); err == nil {
		t.Fatal("want the outage")
	}
	r.store.FailApply = nil
	if out := r.apply(t, ev); out != premium.OutcomeActivated {
		t.Fatal(out)
	}
	if docs := r.docsOf(t, r.buyer); len(docs) != 1 || docs[0].Number != "MP/26-27/000002" {
		t.Fatalf("%+v", docs)
	}
}

func TestPremiumFullRefundIssuesOneCreditNote(t *testing.T) {
	r := newInvoiceRig(t)
	a := uuid.New()
	co, _ := r.buy(t, a, premium.Plan30d)
	r.now = r.now.Add(48 * time.Hour)

	// A partial refund leaves the pass, and no credit note in this lane.
	part := r.event(t, co, "payment.refunded")
	part.AmountMinor = 1000
	if out := r.apply(t, part); out != premium.OutcomePartialRefund {
		t.Fatal(out)
	}
	if len(r.docsOf(t, a)) != 1 {
		t.Fatal("a partial refund issued a credit note")
	}
	rest := r.event(t, co, "payment.refunded")
	rest.AmountMinor = co.AmountPaise - 1000
	if out := r.apply(t, rest); out != premium.OutcomeRefunded {
		t.Fatal(out)
	}
	if out := r.apply(t, rest); out != premium.OutcomeDuplicate {
		t.Fatal(out)
	}
	docs := r.docsOf(t, a)
	if len(docs) != 2 || docs[0].Kind != premium.KindCreditNote || docs[0].Number != "MPC/26-27/000001" || docs[0].Date != "2026-11-13" {
		t.Fatalf("documents %+v", docs)
	}
	inv, _ := r.svc.Invoice(r.ctx, a, docs[1].ID)
	cn, err := r.svc.Invoice(r.ctx, a, docs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if cn.TaxablePaise != -inv.TaxablePaise || cn.CGSTPaise != -inv.CGSTPaise || cn.SGSTPaise != -inv.SGSTPaise ||
		cn.IGSTPaise != -inv.IGSTPaise || cn.TotalPaise != -inv.TotalPaise || cn.TotalPaise != -co.AmountPaise {
		t.Fatalf("credit note %+v does not negate invoice %+v", cn, inv)
	}
	if cn.OriginalInvoice == nil || cn.OriginalInvoice.ID != inv.ID || cn.OriginalInvoice.Number != inv.Number || cn.OriginalInvoice.Date != inv.Date {
		t.Fatalf("credit note reference %+v", cn.OriginalInvoice)
	}
	if got := r.pub.Documents; len(got) != 2 || got[1].Type != premium.EventCreditNoteIssued || got[1].Payload.Kind != premium.KindCreditNote {
		t.Fatalf("events %+v", got)
	}
	// The pass still names its invoice, not the credit note.
	if pass, _ := r.svc.Pass(r.ctx, a, co.PassID); pass.InvoiceID == nil || *pass.InvoiceID != inv.ID {
		t.Fatalf("pass %+v", pass)
	}
}

// A refund of a pass that never activated has no invoice to credit.
func TestPremiumRefundWithoutInvoiceIssuesNothing(t *testing.T) {
	r := newInvoiceRig(t)
	co := r.checkout(t, premium.Plan30d)
	if out := r.apply(t, r.event(t, co, "payment.refunded")); out != premium.OutcomeRefunded {
		t.Fatal(out)
	}
	if len(r.store.Invoices) != 0 {
		t.Fatal("credit note without an invoice")
	}
}

// Another buyer's document is not found — exactly as a document that does
// not exist — on the JSON and the rendered document alike.
func TestPremiumInvoiceIsOwnerOnly(t *testing.T) {
	r := newInvoiceRig(t)
	a, b := uuid.New(), uuid.New()
	r.buy(t, a, premium.Plan30d)
	id := r.docsOf(t, a)[0].ID
	if _, err := r.svc.Invoice(r.ctx, b, id); !errors.Is(err, ErrPremiumInvoiceNotFound) {
		t.Fatalf("stranger's invoice: %v", err)
	}
	if _, err := r.svc.InvoiceDocument(r.ctx, b, id); !errors.Is(err, ErrPremiumInvoiceNotFound) {
		t.Fatalf("stranger's document: %v", err)
	}
	if _, err := r.svc.Invoice(r.ctx, b, uuid.New()); !errors.Is(err, ErrPremiumInvoiceNotFound) {
		t.Fatalf("missing invoice: %v", err)
	}
	if docs := r.docsOf(t, b); len(docs) != 0 {
		t.Fatalf("stranger's list %+v", docs)
	}
	doc, err := r.svc.InvoiceDocument(r.ctx, a, id)
	if err != nil || doc.ContentType != "text/html; charset=utf-8" || doc.Filename != "MP_26-27_000001.html" ||
		!strings.Contains(string(doc.Body), "MP/26-27/000001") || !strings.Contains(string(doc.Body), invSupplier.LegalName) {
		t.Fatalf("document %+v %v", doc, err)
	}
}

// A publish that fails after commit is retried by the sweep, once.
func TestPremiumDocumentEventSweep(t *testing.T) {
	r := newInvoiceRig(t)
	r.pub.Fail = errors.New("kafka down")
	r.buy(t, uuid.New(), premium.Plan30d)
	if len(r.pub.Documents) != 0 {
		t.Fatal("published while kafka was down")
	}
	r.pub.Fail = nil
	if n, _ := r.svc.PublishPendingDocuments(r.ctx); n != 0 {
		t.Fatal("the sweep leaves the post-commit publish a minute")
	}
	r.now = r.now.Add(2 * time.Minute)
	if n, err := r.svc.PublishPendingDocuments(r.ctx); err != nil || n != 1 || len(r.pub.Documents) != 1 {
		t.Fatalf("sweep: %d %v %+v", n, err, r.pub.Documents)
	}
	if n, _ := r.svc.PublishPendingDocuments(r.ctx); n != 0 || len(r.pub.Documents) != 1 {
		t.Fatal("a published document is not sent again")
	}
}

func TestPremiumGSTRegister(t *testing.T) {
	r := newInvoiceRig(t)
	a := uuid.New()
	co, _ := r.buy(t, a, premium.Plan30d)
	r.buy(t, uuid.New(), premium.Plan365d)
	r.now = r.now.Add(24 * time.Hour)
	r.buyer = a
	if out := r.apply(t, r.event(t, co, "payment.refunded")); out != premium.OutcomeRefunded {
		t.Fatal(out)
	}
	reg, err := r.svc.GSTRegister(r.ctx, "2026-11-01", "2026-11-30")
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Rows) != 3 || reg.Rows[0].Number != "MP/26-27/000001" || reg.Rows[1].Number != "MP/26-27/000002" ||
		reg.Rows[2].Kind != premium.KindCreditNote || reg.Rows[2].OriginalNumber != "MP/26-27/000001" {
		t.Fatalf("rows %+v", reg.Rows)
	}
	// Totals net the credit note: what is left is the second pass alone.
	if reg.Totals.Invoices != 2 || reg.Totals.CreditNotes != 1 || reg.Totals.TotalPaise != 176882 || reg.Totals.TaxablePaise != 149900 ||
		reg.Totals.CGSTPaise+reg.Totals.SGSTPaise+reg.Totals.IGSTPaise != 26982 || reg.SupplierGSTN != invSupplier.GSTIN {
		t.Fatalf("totals %+v", reg.Totals)
	}
	for _, row := range reg.Rows {
		if row.PlaceOfSupplyCode != "29" || row.SupplyType != "intra_state" || row.SAC != "998439" || row.GSTRateBP != 1800 {
			t.Fatalf("row %+v", row)
		}
	}
	if reg, _ := r.svc.GSTRegister(r.ctx, "2026-12-01", "2026-12-31"); len(reg.Rows) != 0 {
		t.Fatal("rows outside the range")
	}
	for _, rng := range [][2]string{{"", ""}, {"2026-11-30", "2026-11-01"}, {"2026-01-01", "2027-01-02"}, {"13/11/2026", "2026-11-30"}} {
		if _, err := r.svc.GSTRegister(r.ctx, rng[0], rng[1]); !errors.Is(err, ErrPremiumRegisterRange) {
			t.Errorf("%v: %v", rng, err)
		}
	}
	if _, err := r.svc.GSTRegister(r.ctx, "2026-04-01", "2027-03-31"); err != nil {
		t.Fatalf("a whole financial year: %v", err)
	}
}
