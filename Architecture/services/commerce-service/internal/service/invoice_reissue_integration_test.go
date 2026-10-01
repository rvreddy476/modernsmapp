//go:build integration

package service

// The in-place correction of the ₹0 invoices (invoice_reissue.go,
// cmd/reissue-invoices), against a real database.
//
// Each test places a real P0 order through the checkout, then writes the
// invoice the OLD IssueInvoice wrote for it — a ₹0 row under a freshly
// allocated number, and a ₹0 document at its key — and runs the correction
// with a fake blob store.
//
//	source scratchpad/c1dsn.sh   # commerce_it_test, never commerce_db
//	go test -p 1 -count=1 -tags integration ./internal/service/ -run Reissue
//
// The database is shared with other suites, so every assertion is scoped to
// the rows a test made; a run may also correct (or skip) leftovers.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/commerce-service/database"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/invoice"
	"github.com/google/uuid"
)

var reissueSchemaOnce sync.Once

// ensureReissueSchema applies migration 039 (and only what is pending) to
// the scratch database. It refuses a database the migration runner has
// never seen, rather than replay every migration onto it.
func ensureReissueSchema(t *testing.T) {
	t.Helper()
	var err error
	reissueSchemaOnce.Do(func() {
		ctx := context.Background()
		var seen bool
		if err = svcTestPool.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM schema_migrations
			                WHERE service = 'commerce-service' AND filename = '038_coupons_and_bank_offers.sql')`,
		).Scan(&seen); err != nil {
			return
		}
		if !seen {
			err = errors.New("the scratch database has not had migration 038; bootstrap it first")
			return
		}
		err = postgres.BootstrapSchema(ctx, svcTestPool, database.SetupSQL, database.Migrations)
	})
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
}

// oldZeroInvoice writes what the pre-31dc9eac IssueInvoice wrote for an
// order: a number from the sequence, a row with every total at 0 and
// is_interstate false, and a ₹0 document at invoices/<fy>/<file>.html.
func oldZeroInvoice(t *testing.T, st *postgres.Store, blob *capturedBlob, orderID uuid.UUID, issued time.Time, withPDF bool) postgres.Invoice {
	t.Helper()
	ctx := context.Background()
	order, err := st.GetOrderByID(ctx, orderID)
	if err != nil {
		t.Fatal(err)
	}
	items, err := st.GetOrderItems(ctx, orderID)
	if err != nil {
		t.Fatal(err)
	}
	fy := invoice.FinancialYear(issued)
	seq, err := st.NextInvoiceSequence(ctx, fy)
	if err != nil {
		t.Fatal(err)
	}
	number := invoice.NumberFor(issued, seq)
	stem := invoice.Invoice{Number: number}.AsFilename()
	htmlKey := fmt.Sprintf("invoices/%s/%s.html", fy, stem)
	var pdfKey *string
	_ = blob.Upload(ctx, htmlKey, []byte("OLD ₹0.00 INVOICE "+number), "text/html")
	if withPDF {
		k := fmt.Sprintf("invoices/%s/%s.pdf", fy, stem)
		pdfKey = &k
		_ = blob.Upload(ctx, k, []byte("OLD ₹0.00 PDF "+number), "application/pdf")
	}
	inv := postgres.Invoice{OrderID: orderID, InvoiceNumber: number, FinancialYear: fy, Sequence: seq,
		SellerID: items[0].SellerID, BuyerUserID: order.CustomerUserID, CurrencyCode: "INR",
		HTMLMediaKey: &htmlKey, PDFMediaKey: pdfKey}
	if err := svcTestPool.QueryRow(ctx, `
		INSERT INTO invoices (order_id, invoice_number, financial_year, sequence, seller_id, buyer_user_id,
		                      grand_total, currency_code, is_interstate, cgst_total, sgst_total, igst_total,
		                      html_media_key, pdf_media_key, issued_at, created_at)
		VALUES ($1,$2,$3,$4,$5,$6, 0.00,'INR',FALSE, 0.00,0.00,0.00, $7,$8,$9,$9)
		RETURNING id, issued_at`,
		inv.OrderID, inv.InvoiceNumber, inv.FinancialYear, inv.Sequence, inv.SellerID, inv.BuyerUserID,
		htmlKey, pdfKey, issued).Scan(&inv.ID, &inv.IssuedAt); err != nil {
		t.Fatalf("old ₹0 invoice: %v", err)
	}
	return inv
}

// invoiceRow reads the row back whole, for "nothing changed" comparisons.
func invoiceRow(t *testing.T, id uuid.UUID) postgres.Invoice {
	t.Helper()
	var inv postgres.Invoice
	if err := svcTestPool.QueryRow(context.Background(), `
		SELECT id, order_id, invoice_number, financial_year, sequence, seller_id, buyer_user_id,
		       grand_total, currency_code, is_interstate, cgst_total, sgst_total, igst_total,
		       html_media_key, pdf_media_key, issued_at, created_at
		  FROM invoices WHERE id = $1`, id).Scan(&inv.ID, &inv.OrderID, &inv.InvoiceNumber, &inv.FinancialYear,
		&inv.Sequence, &inv.SellerID, &inv.BuyerUserID, &inv.GrandTotal, &inv.CurrencyCode, &inv.IsInterstate,
		&inv.CGSTTotal, &inv.SGSTTotal, &inv.IGSTTotal, &inv.HTMLMediaKey, &inv.PDFMediaKey,
		&inv.IssuedAt, &inv.CreatedAt); err != nil {
		t.Fatalf("read invoice %s: %v", id, err)
	}
	return inv
}

// sameInvoice compares two reads of a row by value (pointers dereferenced,
// instants by Equal).
func sameInvoice(a, b postgres.Invoice) bool {
	key := func(p *string) string {
		if p == nil {
			return "<nil>"
		}
		return *p
	}
	return a.ID == b.ID && a.OrderID == b.OrderID && a.InvoiceNumber == b.InvoiceNumber &&
		a.FinancialYear == b.FinancialYear && a.Sequence == b.Sequence && a.SellerID == b.SellerID &&
		a.BuyerUserID == b.BuyerUserID && a.GrandTotal == b.GrandTotal && a.CurrencyCode == b.CurrencyCode &&
		a.IsInterstate == b.IsInterstate && a.CGSTTotal == b.CGSTTotal && a.SGSTTotal == b.SGSTTotal &&
		a.IGSTTotal == b.IGSTTotal && key(a.HTMLMediaKey) == key(b.HTMLMediaKey) &&
		key(a.PDFMediaKey) == key(b.PDFMediaKey) && a.IssuedAt.Equal(b.IssuedAt) && a.CreatedAt.Equal(b.CreatedAt)
}

type reissueAudit struct {
	actor                  uuid.UUID
	action, target, reason string
	before, after          map[string]any
}

func reissueAudits(t *testing.T, invoiceID uuid.UUID) []reissueAudit {
	t.Helper()
	rows, err := svcTestPool.Query(context.Background(), `
		SELECT actor_user_id, action, target_type, COALESCE(reason,''), before_state, after_state
		  FROM commerce_admin_audit_log WHERE target_id = $1 ORDER BY created_at`, invoiceID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []reissueAudit
	for rows.Next() {
		var a reissueAudit
		var b, af []byte
		if err := rows.Scan(&a.actor, &a.action, &a.target, &a.reason, &b, &af); err != nil {
			t.Fatal(err)
		}
		_ = json.Unmarshal(b, &a.before)
		_ = json.Unmarshal(af, &a.after)
		out = append(out, a)
	}
	return out
}

func resultFor(rep *InvoiceReissueReport, id uuid.UUID) *InvoiceReissueResult {
	for i := range rep.Results {
		if rep.Results[i].Row.InvoiceID == id {
			return &rep.Results[i]
		}
	}
	return nil
}

func countRows(t *testing.T, sql string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := svcTestPool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func blobSnapshot(b *capturedBlob) map[string][]byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string][]byte, len(b.objs))
	for k, v := range b.objs {
		out[k] = append([]byte(nil), v...)
	}
	return out
}

func TestReissueCorrectsTheZeroInvoicesInPlace(t *testing.T) {
	ensureReissueSchema(t)
	ctx := context.Background()
	st := postgres.New(svcTestPool)
	blob := &capturedBlob{}
	toMinor := func(r float64) int64 { return int64(math.Round(r * 100)) }
	issued := time.Date(2026, time.September, 20, 10, 15, 0, 0, time.UTC)

	// Two P0 orders invoiced at ₹0 (intra- and inter-state), one P0 order
	// whose money does not reconcile, and one pre-P0 legacy order.
	type fixture struct {
		name       string
		orderID    uuid.UUID
		inv        postgres.Invoice
		interstate bool
	}
	good := []*fixture{
		{name: "intra-state", interstate: false},
		{name: "inter-state", interstate: true},
	}
	for _, f := range good {
		dest := invDest{"Bengaluru", "KA", "560002"}
		if f.interstate {
			dest = invDest{"Mumbai", "MH", "400001"}
		}
		f.orderID = placeP0Order(t, st, dest)
		f.inv = oldZeroInvoice(t, st, blob, f.orderID, issued, false)
	}

	badOrder := placeP0Order(t, st, invDest{"Bengaluru", "KA", "560002"})
	invExec(t, `UPDATE orders SET taxable_minor = taxable_minor + 1 WHERE id = $1`, badOrder)
	bad := oldZeroInvoice(t, st, blob, badOrder, issued, false)

	sellerID := seedSeller(t, "reissue-legacy")
	productID, variantID, sku := invProduct(t, sellerID, "Legacy Item", 90000, "18", "6205")
	method := "upi"
	legacyOrder := &postgres.Order{
		CustomerUserID: uuid.New(), Subtotal: 900, FinalAmount: 900, CurrencyCode: "INR",
		PaymentMethod: &method, PaymentStatus: "paid", Status: "confirmed",
	}
	if err := st.CreateOrder(ctx, legacyOrder, []*postgres.OrderItem{{
		ProductID: productID, VariantID: variantID, SellerID: sellerID, ProductTitle: "Legacy Item",
		SKU: sku, Quantity: 1, UnitMRP: 900, UnitPrice: 900, FinalPrice: 900, Status: "confirmed",
	}}); err != nil {
		t.Fatalf("legacy order: %v", err)
	}
	legacy := oldZeroInvoice(t, st, blob, legacyOrder.ID, issued, false)

	untouched := []postgres.Invoice{bad, legacy}
	before := map[uuid.UUID]postgres.Invoice{}
	for _, f := range good {
		before[f.inv.ID] = invoiceRow(t, f.inv.ID)
	}
	for _, u := range untouched {
		before[u.ID] = invoiceRow(t, u.ID)
	}
	seqSum := func() int64 {
		return countRows(t, `SELECT COALESCE(SUM(last_sequence),0) FROM invoice_sequences`)
	}
	outbox := func() int64 { return countRows(t, `SELECT COUNT(*) FROM outbox_events`) }

	// ── Dry run: reports, writes nothing ────────────────────────────────
	dryBlob := &capturedBlob{}
	seq0, out0 := seqSum(), outbox()
	rep, err := NewOffline(st).WithBlob(dryBlob).ReissueInvoices(ctx, false)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	for _, f := range good {
		r := resultFor(rep, f.inv.ID)
		if r == nil || r.Outcome != ReissueWouldCorrect {
			t.Fatalf("%s: dry run result %+v, want would-correct", f.name, r)
		}
		if r.After == nil || r.After.GrandTotal != readStoredOrder(t, f.orderID).final {
			t.Fatalf("%s: dry run would write %+v", f.name, r.After)
		}
		if got := invoiceRow(t, f.inv.ID); !sameInvoice(got, before[f.inv.ID]) {
			t.Fatalf("%s: the dry run changed the row: %+v → %+v", f.name, before[f.inv.ID], got)
		}
		if n := len(reissueAudits(t, f.inv.ID)); n != 0 {
			t.Fatalf("%s: the dry run wrote %d audit rows", f.name, n)
		}
	}
	if len(dryBlob.objs) != 0 {
		t.Fatalf("the dry run uploaded %d documents", len(dryBlob.objs))
	}
	if seqSum() != seq0 || outbox() != out0 {
		t.Fatal("the dry run moved an invoice sequence or emitted an event")
	}

	// ── Apply ────────────────────────────────────────────────────────────
	seq1, out1 := seqSum(), outbox()
	keysBefore := blobSnapshot(blob)
	rep, err = NewOffline(st).WithBlob(blob).ReissueInvoices(ctx, true)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	for _, f := range good {
		r := resultFor(rep, f.inv.ID)
		if r == nil || r.Outcome != ReissueCorrected {
			t.Fatalf("%s: apply result %+v, want corrected", f.name, r)
		}
		want := readStoredOrder(t, f.orderID)
		got := invoiceRow(t, f.inv.ID)
		was := before[f.inv.ID]

		// Same identity.
		if got.InvoiceNumber != was.InvoiceNumber || got.FinancialYear != was.FinancialYear ||
			got.Sequence != was.Sequence || !got.IssuedAt.Equal(was.IssuedAt) || !got.CreatedAt.Equal(was.CreatedAt) ||
			*got.HTMLMediaKey != *was.HTMLMediaKey || got.PDFMediaKey != nil ||
			got.SellerID != was.SellerID || got.BuyerUserID != was.BuyerUserID {
			t.Fatalf("%s: the invoice's identity changed: %+v → %+v", f.name, was, got)
		}
		// Correct money.
		if toMinor(got.GrandTotal) != want.final || toMinor(got.CGSTTotal) != want.cgst ||
			toMinor(got.SGSTTotal) != want.sgst || toMinor(got.IGSTTotal) != want.igst ||
			got.IsInterstate != want.interstate {
			t.Fatalf("%s: corrected row %v/%v/%v/%v interstate=%v, order stored %d/%d/%d/%d interstate=%v",
				f.name, got.GrandTotal, got.CGSTTotal, got.SGSTTotal, got.IGSTTotal, got.IsInterstate,
				want.final, want.cgst, want.sgst, want.igst, want.interstate)
		}
		if want.final == 0 || got.IsInterstate != f.interstate {
			t.Fatalf("%s: fixture is not the hard case (final %d, interstate %v)", f.name, want.final, got.IsInterstate)
		}

		// The document at the SAME key, overwritten.
		key := *was.HTMLMediaKey
		doc := string(blob.objs[key])
		if bytes.Equal(blob.objs[key], keysBefore[key]) || strings.Contains(doc, "OLD ₹0.00") {
			t.Fatalf("%s: the document at %s was not overwritten", f.name, key)
		}
		for _, w := range []string{
			was.InvoiceNumber, "Invoice Date: 20 Sep 2026", "5 Main St", "Generated on 20 Sep 2026 10:15 UTC",
		} {
			if !strings.Contains(doc, w) {
				t.Errorf("%s: the corrected document lacks %q", f.name, w)
			}
		}
		totals := map[string]string{}
		if i := strings.Index(doc, `class="totals"`); i >= 0 {
			end := strings.Index(doc[i:], "</table>")
			for _, m := range trRe.FindAllStringSubmatch(doc[i:i+end], -1) {
				if c := cells(m[1]); len(c) == 2 {
					totals[c[0]] = c[1]
				}
			}
		}
		if totals["Grand Total"] != rupeeCell(want.final) || totals["Taxable value"] != rupeeCell(want.taxable) {
			t.Errorf("%s: corrected totals %q, want grand %s taxable %s", f.name, totals,
				rupeeCell(want.final), rupeeCell(want.taxable))
		}
		for k := range blob.objs {
			if strings.HasPrefix(k, strings.TrimSuffix(key, ".html")) && k != key {
				t.Errorf("%s: a new object was written beside the invoice: %s", f.name, k)
			}
		}

		// One append-only audit row, by the system actor, before and after.
		audits := reissueAudits(t, f.inv.ID)
		if len(audits) != 1 {
			t.Fatalf("%s: %d audit rows, want 1", f.name, len(audits))
		}
		a := audits[0]
		if a.actor != postgres.InvoiceReissueActorID || a.action != postgres.AuditActionInvoiceReissue ||
			a.target != "invoice" || a.reason != postgres.InvoiceReissueReason {
			t.Fatalf("%s: audit row %+v", f.name, a)
		}
		if a.before["grand_total_minor"] != float64(0) || a.after["grand_total_minor"] != float64(want.final) ||
			a.after["invoice_number"] != was.InvoiceNumber || a.after["is_interstate"] != want.interstate {
			t.Fatalf("%s: audit before %v after %v", f.name, a.before, a.after)
		}
	}

	// The unreconciled order and the legacy order: reported, untouched.
	for _, u := range []struct {
		inv    postgres.Invoice
		reason string
	}{{bad, "does not reconcile"}, {legacy, "pre-P0 legacy order"}} {
		r := resultFor(rep, u.inv.ID)
		if r == nil || r.Outcome != ReissueSkipped || !strings.Contains(r.Reason, u.reason) {
			t.Fatalf("invoice %s: result %+v, want skipped (%s)", u.inv.InvoiceNumber, r, u.reason)
		}
		if got := invoiceRow(t, u.inv.ID); !sameInvoice(got, before[u.inv.ID]) {
			t.Fatalf("invoice %s was changed: %+v → %+v", u.inv.InvoiceNumber, before[u.inv.ID], got)
		}
		if !bytes.Equal(blob.objs[*u.inv.HTMLMediaKey], keysBefore[*u.inv.HTMLMediaKey]) {
			t.Fatalf("invoice %s's document was overwritten", u.inv.InvoiceNumber)
		}
		if n := len(reissueAudits(t, u.inv.ID)); n != 0 {
			t.Fatalf("invoice %s has %d audit rows", u.inv.InvoiceNumber, n)
		}
	}
	if seqSum() != seq1 {
		t.Fatal("the correction allocated an invoice number")
	}
	if outbox() != out1 {
		t.Fatal("the correction emitted an event")
	}

	// ── A second run changes nothing ────────────────────────────────────
	afterFirst := map[uuid.UUID]postgres.Invoice{}
	for id := range before {
		afterFirst[id] = invoiceRow(t, id)
	}
	blob2 := &capturedBlob{}
	rep, err = NewOffline(st).WithBlob(blob2).ReissueInvoices(ctx, true)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	for _, f := range good {
		if r := resultFor(rep, f.inv.ID); r != nil {
			t.Fatalf("%s: the second run reported the corrected invoice again: %+v", f.name, r)
		}
		if n := len(reissueAudits(t, f.inv.ID)); n != 1 {
			t.Fatalf("%s: %d audit rows after the second run", f.name, n)
		}
		if _, wrote := blob2.objs[*f.inv.HTMLMediaKey]; wrote {
			t.Fatalf("%s: the second run rewrote the document", f.name)
		}
	}
	for id, was := range afterFirst {
		if got := invoiceRow(t, id); !sameInvoice(got, was) {
			t.Fatalf("invoice %s changed on the second run: %+v → %+v", was.InvoiceNumber, was, got)
		}
	}
	if rep.AlreadyCorrect < len(good) {
		t.Fatalf("second run counted %d already-correct invoices, want at least %d", rep.AlreadyCorrect, len(good))
	}
	if seqSum() != seq1 || outbox() != out1 {
		t.Fatal("the second run moved a sequence or emitted an event")
	}
}

// An invoice with a PDF is corrected only if the PDF can be re-rendered:
// the download link prefers it, so a ₹0 PDF left beside a corrected row
// would still be what the buyer gets.
func TestReissueTouchesNothingWhenItCannotReRenderThePDF(t *testing.T) {
	ensureReissueSchema(t)
	ctx := context.Background()
	st := postgres.New(svcTestPool)
	blob := &capturedBlob{}
	orderID := placeP0Order(t, st, invDest{"Bengaluru", "KA", "560002"})
	inv := oldZeroInvoice(t, st, blob, orderID, time.Date(2026, time.September, 21, 9, 0, 0, 0, time.UTC), true)
	was := invoiceRow(t, inv.ID)
	keysBefore := blobSnapshot(blob)

	orig := renderReissuePDF
	t.Cleanup(func() { renderReissuePDF = orig })
	renderReissuePDF = func(invoice.Invoice) ([]byte, string, error) {
		return nil, "", errors.New("wkhtmltopdf not found")
	}
	rep, err := NewOffline(st).WithBlob(blob).ReissueInvoices(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if r := resultFor(rep, inv.ID); r == nil || r.Outcome != ReissueSkipped || !strings.Contains(r.Reason, "PDF") {
		t.Fatalf("result %+v, want skipped for the PDF", r)
	}
	if got := invoiceRow(t, inv.ID); !sameInvoice(got, was) {
		t.Fatalf("the row changed: %+v → %+v", was, got)
	}
	for _, k := range []string{*inv.HTMLMediaKey, *inv.PDFMediaKey} {
		if !bytes.Equal(blob.objs[k], keysBefore[k]) {
			t.Fatalf("%s was overwritten", k)
		}
	}
	if n := len(reissueAudits(t, inv.ID)); n != 0 {
		t.Fatalf("%d audit rows", n)
	}

	renderReissuePDF = func(i invoice.Invoice) ([]byte, string, error) {
		return []byte("PDF " + i.Number + fmt.Sprintf(" %.2f", i.GrandTotal)), "application/pdf", nil
	}
	rep, err = NewOffline(st).WithBlob(blob).ReissueInvoices(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if r := resultFor(rep, inv.ID); r == nil || r.Outcome != ReissueCorrected {
		t.Fatalf("result %+v, want corrected", r)
	}
	final := readStoredOrder(t, orderID).final
	wantPDF := fmt.Sprintf("PDF %s %d.%02d", inv.InvoiceNumber, final/100, final%100)
	if got := string(blob.objs[*inv.PDFMediaKey]); got != wantPDF {
		t.Fatalf("PDF at the same key = %q, want %q", got, wantPDF)
	}
	if got := invoiceRow(t, inv.ID); got.PDFMediaKey == nil || *got.PDFMediaKey != *inv.PDFMediaKey {
		t.Fatalf("the PDF key moved: %v", got.PDFMediaKey)
	}
}

// CorrectInvoiceTotals re-checks the row under its lock: a listing that no
// longer matches is refused before anything is written, and the documents
// are never overwritten.
func TestReissueRefusesAStaleListing(t *testing.T) {
	ensureReissueSchema(t)
	ctx := context.Background()
	st := postgres.New(svcTestPool)
	blob := &capturedBlob{}
	orderID := placeP0Order(t, st, invDest{"Bengaluru", "KA", "560002"})
	inv := oldZeroInvoice(t, st, blob, orderID, time.Date(2026, time.September, 22, 9, 0, 0, 0, time.UTC), false)
	so := readStoredOrder(t, orderID)
	final := so.final

	rows, err := st.ListInvoicesForReissue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var listed postgres.InvoiceReissueRow
	for _, r := range rows {
		if r.InvoiceID == inv.ID {
			listed = r
		}
	}
	if listed.InvoiceID == uuid.Nil || !listed.HasStoredSplit || listed.OrderFinalMinor != final {
		t.Fatalf("listing %+v", listed)
	}
	after := postgres.InvoiceTotalsMinor{GrandTotal: final, CGST: so.cgst, SGST: so.sgst, IGST: so.igst, IsInterstate: so.interstate}
	for _, tc := range []struct {
		name   string
		mutate func(*postgres.InvoiceCorrection)
	}{
		{"totals moved", func(c *postgres.InvoiceCorrection) { c.Listed.Totals.GrandTotal = 1 }},
		{"number differs", func(c *postgres.InvoiceCorrection) { c.Listed.InvoiceNumber += "X" }},
		{"html key differs", func(c *postgres.InvoiceCorrection) { k := "other.html"; c.Listed.HTMLMediaKey = &k }},
		{"order final moved", func(c *postgres.InvoiceCorrection) { c.Listed.OrderFinalMinor++ }},
		{"rebuilt total disagrees", func(c *postgres.InvoiceCorrection) { c.After.GrandTotal++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := postgres.InvoiceCorrection{Listed: listed, After: after}
			tc.mutate(&c)
			called := false
			err := st.CorrectInvoiceTotals(ctx, c, func(context.Context) error { called = true; return nil })
			if !errors.Is(err, postgres.ErrInvoiceReissueStale) {
				t.Fatalf("err = %v, want ErrInvoiceReissueStale", err)
			}
			if called {
				t.Fatal("the documents were overwritten for a refused correction")
			}
		})
	}
	// A failed overwrite rolls the row and the audit back.
	err = st.CorrectInvoiceTotals(ctx, postgres.InvoiceCorrection{Listed: listed, After: after},
		func(context.Context) error { return errors.New("minio down") })
	if err == nil || errors.Is(err, postgres.ErrInvoiceReissueStale) {
		t.Fatalf("err = %v, want the overwrite failure", err)
	}
	if got := invoiceRow(t, inv.ID); got.GrandTotal != 0 {
		t.Fatalf("the row was written despite the failed overwrite: %v", got.GrandTotal)
	}
	if n := len(reissueAudits(t, inv.ID)); n != 0 {
		t.Fatalf("%d audit rows despite the failed overwrite", n)
	}
	// Corrected once, a listing that already shows the right total is
	// refused: the store never rewrites a correct invoice, whatever the
	// caller's filter said.
	if err := st.CorrectInvoiceTotals(ctx, postgres.InvoiceCorrection{Listed: listed, After: after},
		func(context.Context) error { return nil }); err != nil {
		t.Fatalf("correct: %v", err)
	}
	again := listed
	again.Totals = after
	called := false
	err = st.CorrectInvoiceTotals(ctx, postgres.InvoiceCorrection{Listed: again, After: after},
		func(context.Context) error { called = true; return nil })
	if !errors.Is(err, postgres.ErrInvoiceReissueStale) || called {
		t.Fatalf("re-correcting a correct invoice: err %v, overwrite called %v", err, called)
	}
	if n := len(reissueAudits(t, inv.ID)); n != 1 {
		t.Fatalf("%d audit rows, want 1", n)
	}
	// The audit table refuses the system actor on any other action.
	_, err = svcTestPool.Exec(ctx, `
		INSERT INTO commerce_admin_audit_log (actor_user_id, action, target_type, target_id, reason)
		VALUES ($1, 'banner_delete', 'banner', $2, $3)`,
		postgres.InvoiceReissueActorID, uuid.New(), postgres.InvoiceReissueReason)
	if err == nil {
		t.Fatal("the system actor wrote a banner_delete audit row")
	}
	_, err = svcTestPool.Exec(ctx, `
		INSERT INTO commerce_admin_audit_log (actor_user_id, action, target_type, target_id, reason)
		VALUES ($1, 'invoice_reissue', 'invoice', $2, 'system:invoice_reissue')`, uuid.New(), inv.ID)
	if err == nil {
		t.Fatal("a user id wrote an invoice_reissue audit row")
	}
}
