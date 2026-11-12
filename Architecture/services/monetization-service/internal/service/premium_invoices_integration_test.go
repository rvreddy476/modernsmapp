//go:build integration

package service

import (
	"testing"

	"github.com/atpost/monetization-service/internal/premium"
	"github.com/google/uuid"
)

// Premium tax documents against PostgreSQL (migration 029), through the real
// store: the invoice commits with the capture, numbers are consecutive per
// series and year, replays issue nothing, the full refund issues one credit
// note, and the database CHECKs accept what the core builds. Counters are
// shared by every test on the database, so numbers are compared relatively.
// Written 13 Nov 2026, NOT run in this change (no local database): CI runs it.

func TestPremiumIntegrationInvoicesAndCreditNote(t *testing.T) {
	r := newPremiumDBRig(t)
	r.svc.WithSupplier(premium.Supplier{LegalName: "Example Supplier Private Limited", Address: "1 Example Road, Bengaluru 560001",
		GSTIN: "29AAAAA0000A1Z0", StateCode: "29", StateName: "Karnataka"}).
		WithProfiles(fixedProfiles{r.buyer: {DisplayName: "Asha Rao"}})

	co1, ev1 := r.paid(t, premium.Plan30d)
	if out, err := r.svc.ApplyPremiumPaymentEvent(r.ctx, ev1); err != nil || out != premium.OutcomeActivated {
		t.Fatalf("activate 1: %s %v", out, err)
	}
	if out, _ := r.svc.ApplyPremiumPaymentEvent(r.ctx, ev1); out != premium.OutcomeDuplicate {
		t.Fatalf("replay: %s", out)
	}
	ev1b := ev1
	ev1b.EventID = uuid.NewString()
	if out, _ := r.svc.ApplyPremiumPaymentEvent(r.ctx, ev1b); out != premium.OutcomeIgnored {
		t.Fatalf("second capture: %s", out)
	}
	co2, ev2 := r.paid(t, premium.Plan365d)
	if out, err := r.svc.ApplyPremiumPaymentEvent(r.ctx, ev2); err != nil || out != premium.OutcomeActivated {
		t.Fatalf("activate 2: %s %v", out, err)
	}

	docs, err := r.svc.Invoices(r.ctx, r.buyer)
	if err != nil || len(docs) != 2 {
		t.Fatalf("invoices %+v %v", docs, err)
	}
	var first, second *premium.Invoice
	for _, d := range docs {
		inv, err := r.svc.store.GetPremiumInvoice(r.ctx, d.ID)
		if err != nil || inv == nil {
			t.Fatal(err)
		}
		switch inv.PassID {
		case co1.PassID:
			first = inv
		case co2.PassID:
			second = inv
		}
	}
	if first == nil || second == nil || second.FY == first.FY && second.Seq != first.Seq+1 {
		t.Fatalf("numbers not consecutive: %+v %+v", first, second)
	}
	if first.TotalPaise != co1.AmountPaise || first.TaxablePaise != *co1.TaxablePaise || first.CGSTPaise != *co1.CGSTPaise ||
		first.BuyerName != "Asha Rao" || first.Supplier.GSTIN != "29AAAAA0000A1Z0" || first.SAC != "998439" {
		t.Fatalf("invoice %+v vs pass %+v", first, co1.PremiumTaxView)
	}
	if pass, _ := r.svc.Pass(r.ctx, r.buyer, co1.PassID); pass.InvoiceID == nil || *pass.InvoiceID != first.ID {
		t.Fatalf("pass invoice_id %+v", pass)
	}

	ref := ev1
	ref.EventID, ref.EventType = uuid.NewString(), "payment.refunded"
	if out, err := r.svc.ApplyPremiumPaymentEvent(r.ctx, ref); err != nil || out != premium.OutcomeRefunded {
		t.Fatalf("refund: %s %v", out, err)
	}
	ref2 := ref
	ref2.EventID = uuid.NewString()
	if out, _ := r.svc.ApplyPremiumPaymentEvent(r.ctx, ref2); out != premium.OutcomeIgnored {
		t.Fatalf("second refund: %s", out)
	}
	docs, _ = r.svc.Invoices(r.ctx, r.buyer)
	if len(docs) != 3 || docs[0].Kind != premium.KindCreditNote || docs[0].TotalPaise != -co1.AmountPaise {
		t.Fatalf("after refund %+v", docs)
	}
	cn, _ := r.svc.Invoice(r.ctx, r.buyer, docs[0].ID)
	if cn.OriginalInvoice == nil || cn.OriginalInvoice.ID != first.ID || cn.CGSTPaise != -first.CGSTPaise {
		t.Fatalf("credit note %+v", cn)
	}
	if _, err := r.svc.Invoice(r.ctx, uuid.New(), docs[0].ID); err != ErrPremiumInvoiceNotFound {
		t.Fatalf("stranger: %v", err)
	}
	reg, err := r.svc.GSTRegister(r.ctx, first.InvoiceDate, cn.Date)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, row := range reg.Rows {
		seen[row.Number] = true
	}
	if !seen[first.Number] || !seen[second.Number] || !seen[cn.Number] {
		t.Fatalf("register lacks our documents: %+v", reg.Rows)
	}
	if got := len(r.pub.Documents); got != 3 {
		t.Fatalf("document events %d", got)
	}
}
