package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

// Momentum Premium invoice / credit-note e-mails: once per document, only to
// the event buyer's verified address, with facts read from monetization.

// premiumQueue adapts the copyright fakeQueue (same semantics) to the
// premium queue's method names.
type premiumQueue struct{ *fakeQueue }

func (q premiumQueue) EnqueuePremiumInvoiceEmail(ctx context.Context, id, r uuid.UUID, p []byte) (bool, error) {
	return q.EnqueueCopyrightEmail(ctx, id, r, p)
}
func (q premiumQueue) ClaimPremiumInvoiceEmail(ctx context.Context) (*postgres.CopyrightEmailJob, error) {
	return q.ClaimCopyrightEmail(ctx)
}
func (q premiumQueue) FinishPremiumInvoiceEmail(ctx context.Context, j *postgres.CopyrightEmailJob, s, c string, d time.Duration) error {
	return q.FinishCopyrightEmail(ctx, j, s, c, d)
}

// userContacts answers each user's own verified address and records whose
// address was read.
type userContacts struct {
	addrs map[uuid.UUID]string
	read  []uuid.UUID
	err   error
}

func (c *userContacts) VerifiedEmail(_ context.Context, id uuid.UUID) (string, bool, error) {
	c.read = append(c.read, id)
	if c.err != nil {
		return "", false, c.err
	}
	a, ok := c.addrs[id]
	return a, ok && a != "", nil
}

// fakeInvoices is monetization's mail read: a document is found only for
// its own buyer, unless lie is set (a broken monetization answering any
// document for any user).
type fakeInvoices struct {
	docs  map[uuid.UUID]PremiumInvoiceFacts
	lie   bool
	err   error
	reads int
}

func (f *fakeInvoices) PremiumInvoice(_ context.Context, user, id uuid.UUID) (PremiumInvoiceFacts, bool, error) {
	f.reads++
	if f.err != nil {
		return PremiumInvoiceFacts{}, false, f.err
	}
	d, ok := f.docs[id]
	if !ok || (!f.lie && d.UserID != user) {
		return PremiumInvoiceFacts{}, false, nil
	}
	return d, true, nil
}

type premiumRig struct {
	svc        *Service
	mail       *recordingMailer
	contacts   *userContacts
	invoices   *fakeInvoices
	queue      *fakeQueue
	suppressed bool
	buyer      uuid.UUID
	invoice    PremiumInvoiceFacts
	credit     PremiumInvoiceFacts
}

func newPremiumRig(t *testing.T) *premiumRig {
	t.Helper()
	buyer := uuid.New()
	inv := PremiumInvoiceFacts{ID: uuid.New(), UserID: buyer, Number: "MP/26-27/000001", Kind: PremiumKindInvoice, Date: "2026-11-13",
		PassID: uuid.New(), Description: "Momentum Premium — 30 days", TotalPaise: 17582, Currency: "INR"}
	cn := PremiumInvoiceFacts{ID: uuid.New(), UserID: buyer, Number: "MPC/26-27/000001", Kind: PremiumKindCreditNote, Date: "2026-11-14",
		PassID: inv.PassID, Description: "Momentum Premium — 30 days", TotalPaise: -17582, Currency: "INR", OriginalNumber: inv.Number}
	r := &premiumRig{mail: &recordingMailer{}, queue: newFakeQueue(), buyer: buyer, invoice: inv, credit: cn,
		contacts: &userContacts{addrs: map[uuid.UUID]string{buyer: "buyer@example.com"}},
		invoices: &fakeInvoices{docs: map[uuid.UUID]PremiumInvoiceFacts{inv.ID: inv, cn.ID: cn}}}
	r.svc = &Service{}
	r.svc.SetMailer(r.mail)
	r.svc.SetPremiumInvoiceEmail(r.contacts, r.invoices, "https://momentum.example/")
	r.svc.setPremiumInvoiceEmailQueue(premiumQueue{r.queue}, func(context.Context, uuid.UUID) (bool, error) { return r.suppressed, nil })
	return r
}

func (r *premiumRig) drain() {
	for i := 0; i < 100 && r.svc.runPremiumInvoiceEmailJob(context.Background()); i++ {
	}
}

func premiumEvent(f PremiumInvoiceFacts, user uuid.UUID) PremiumInvoiceEvent {
	et := EventPremiumInvoiceIssued
	if f.Kind == PremiumKindCreditNote {
		et = EventPremiumCreditNoteIssued
	}
	return PremiumInvoiceEvent{EventType: et, UserID: user, InvoiceID: f.ID, PassID: f.PassID, Kind: f.Kind}
}

func TestPremiumInvoiceEmail_BothKindsSendOnceToTheBuyer(t *testing.T) {
	r := newPremiumRig(t)
	ctx := context.Background()
	for _, f := range []PremiumInvoiceFacts{r.invoice, r.credit} {
		if err := r.svc.QueuePremiumInvoiceEmail(ctx, premiumEvent(f, r.buyer)); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.mail.sent) != 0 {
		t.Fatal("the consumer's call sent the e-mail itself; only the worker sends")
	}
	for _, id := range r.queue.order {
		if p := string(r.queue.jobs[id].payload); strings.Contains(p, "@") || strings.Contains(p, "MP/") || strings.Contains(p, "17582") {
			t.Fatalf("the queued payload carries an address or document facts: %s", p)
		}
	}
	r.drain()
	if len(r.mail.sent) != 2 {
		t.Fatalf("sent %d, want invoice + credit note", len(r.mail.sent))
	}
	if got := r.mail.sent[0].Subject; got != "Your Momentum Premium tax invoice MP/26-27/000001" {
		t.Errorf("invoice subject %q", got)
	}
	if got := r.mail.sent[1].Subject; got != "Credit note MPC/26-27/000001 for your Momentum Premium refund" {
		t.Errorf("credit note subject %q", got)
	}
	for _, m := range r.mail.sent {
		if len(m.To) != 1 || m.To[0] != "buyer@example.com" || len(m.Cc)+len(m.Bcc)+len(m.Attachments) != 0 {
			t.Fatalf("recipients/attachments: %+v", m)
		}
	}
}

func TestPremiumInvoiceEmail_ReplaySendsOnce(t *testing.T) {
	r := newPremiumRig(t)
	ctx := context.Background()
	ev := premiumEvent(r.invoice, r.buyer)
	for i := 0; i < 3; i++ {
		if err := r.svc.QueuePremiumInvoiceEmail(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	r.drain()
	// Redelivered after it was sent, and a worker whose lease lapsed.
	if err := r.svc.QueuePremiumInvoiceEmail(ctx, ev); err != nil {
		t.Fatal(err)
	}
	r.queue.expire()
	r.drain()
	if len(r.queue.order) != 1 || len(r.mail.sent) != 1 {
		t.Fatalf("queued %d, sent %d; want one each", len(r.queue.order), len(r.mail.sent))
	}
	if id := r.queue.order[0]; id != PremiumInvoiceEmailDedupID(EventPremiumInvoiceIssued, r.invoice.ID) {
		t.Fatal("the queue key is not the (event type, invoice) pair")
	}
}

func TestPremiumInvoiceEmail_NoVerifiedAddressNoSend(t *testing.T) {
	r := newPremiumRig(t)
	delete(r.contacts.addrs, r.buyer)
	var logs bytes.Buffer
	restore := captureLogs(&logs)
	defer restore()
	if err := r.svc.QueuePremiumInvoiceEmail(context.Background(), premiumEvent(r.invoice, r.buyer)); err != nil {
		t.Fatal(err)
	}
	r.drain()
	if len(r.mail.sent) != 0 || r.queue.status(r.queue.order[0]) != "skipped" ||
		r.queue.finished[0] != "skipped:no_verified_address" {
		t.Fatalf("sent %d, finished %v", len(r.mail.sent), r.queue.finished)
	}
	if !strings.Contains(logs.String(), "no verified address") || !strings.Contains(logs.String(), "level=INFO") {
		t.Fatalf("the skip is not logged at INFO: %s", logs.String())
	}
}

// The address is read ONLY for the event's user, and only a document of
// that user is ever mailed: another buyer's invoice id on the event finds
// nothing (monetization's owner check), and if monetization ever answered
// someone else's document the worker refuses it.
func TestPremiumInvoiceEmail_NeverToTheWrongAddress(t *testing.T) {
	r := newPremiumRig(t)
	other := uuid.New()
	r.contacts.addrs[other] = "other@example.com"
	ctx := context.Background()

	// Event for `other` naming the buyer's invoice.
	if err := r.svc.QueuePremiumInvoiceEmail(ctx, premiumEvent(r.invoice, other)); err != nil {
		t.Fatal(err)
	}
	r.drain()
	if len(r.mail.sent) != 0 || r.queue.finished[0] != "skipped:invoice_not_found" || len(r.contacts.read) != 0 {
		t.Fatalf("another user's invoice: sent %d, finished %v, contacts read %v", len(r.mail.sent), r.queue.finished, r.contacts.read)
	}

	// A broken monetization answers the buyer's document for `other`.
	r2 := newPremiumRig(t)
	r2.contacts.addrs[other] = "other@example.com"
	r2.invoices.lie = true
	if err := r2.svc.QueuePremiumInvoiceEmail(ctx, premiumEvent(r2.invoice, other)); err != nil {
		t.Fatal(err)
	}
	r2.drain()
	if len(r2.mail.sent) != 0 || r2.queue.finished[0] != "failed:invoice_mismatch" {
		t.Fatalf("mismatched document: sent %d, finished %v", len(r2.mail.sent), r2.queue.finished)
	}

	// The right pairing: only the event user's address is read and used.
	r3 := newPremiumRig(t)
	r3.contacts.addrs[other] = "other@example.com"
	if err := r3.svc.QueuePremiumInvoiceEmail(ctx, premiumEvent(r3.invoice, r3.buyer)); err != nil {
		t.Fatal(err)
	}
	r3.drain()
	if len(r3.mail.sent) != 1 || r3.mail.sent[0].To[0] != "buyer@example.com" || len(r3.contacts.read) != 1 || r3.contacts.read[0] != r3.buyer {
		t.Fatalf("sent %+v, read %v", r3.mail.sent, r3.contacts.read)
	}
}

func TestPremiumInvoiceEmail_KindMustMatchTheDocument(t *testing.T) {
	r := newPremiumRig(t)
	ctx := context.Background()
	// An invoice event whose id is a credit note: refused, never mailed as an invoice.
	ev := premiumEvent(r.credit, r.buyer)
	ev.EventType, ev.Kind = EventPremiumInvoiceIssued, PremiumKindInvoice
	if err := r.svc.QueuePremiumInvoiceEmail(ctx, ev); err != nil {
		t.Fatal(err)
	}
	r.drain()
	if len(r.mail.sent) != 0 || r.queue.finished[0] != "failed:invoice_mismatch" {
		t.Fatalf("kind mismatch: sent %d, finished %v", len(r.mail.sent), r.queue.finished)
	}
	// An event whose kind contradicts its type is not queued at all.
	bad := premiumEvent(r.invoice, r.buyer)
	bad.Kind = PremiumKindCreditNote
	if err := r.svc.QueuePremiumInvoiceEmail(ctx, bad); err != nil || len(r.queue.order) != 1 {
		t.Fatalf("contradictory event queued: %d %v", len(r.queue.order), err)
	}
}

func TestPremiumInvoiceEmail_SuppressedAndRetries(t *testing.T) {
	r := newPremiumRig(t)
	ctx := context.Background()
	r.suppressed = true
	_ = r.svc.QueuePremiumInvoiceEmail(ctx, premiumEvent(r.invoice, r.buyer))
	r.drain()
	if len(r.mail.sent) != 0 || r.queue.finished[0] != "skipped:account_suppressed" {
		t.Fatalf("suppressed: %v", r.queue.finished)
	}

	r2 := newPremiumRig(t)
	r2.invoices.err = errors.New("monetization down")
	_ = r2.svc.QueuePremiumInvoiceEmail(ctx, premiumEvent(r2.invoice, r2.buyer))
	r2.svc.runPremiumInvoiceEmailJob(ctx) // one try: the fake queue ignores the retry delay
	if len(r2.mail.sent) != 0 || r2.queue.finished[0] != "pending:invoice_read_failed" {
		t.Fatalf("read failure: %v", r2.queue.finished)
	}
	r2.invoices.err = nil
	r2.mail.fail = 1
	r2.drain()
	if len(r2.mail.sent) != 1 || r2.queue.finished[1] != "pending:send_failed" || r2.queue.finished[2] != "sent:" {
		t.Fatalf("retries: sent %d, finished %v", len(r2.mail.sent), r2.queue.finished)
	}
}

func TestPremiumInvoiceEmail_NeverLogsAddressOrContents(t *testing.T) {
	r := newPremiumRig(t)
	var logs bytes.Buffer
	restore := captureLogs(&logs)
	defer restore()
	r.mail.fail = 1
	_ = r.svc.QueuePremiumInvoiceEmail(context.Background(), premiumEvent(r.invoice, r.buyer))
	r.drain()
	if len(r.mail.sent) != 1 {
		t.Fatalf("sent %d", len(r.mail.sent))
	}
	out := logs.String()
	for _, secret := range []string{"@example.com", "MP/26-27", "175.82", "17582", "Momentum Premium —"} {
		if strings.Contains(out, secret) {
			t.Fatalf("logs carry %q: %s", secret, out)
		}
	}
}

func TestRenderPremiumInvoiceEmail(t *testing.T) {
	r := newPremiumRig(t)
	page := PremiumInvoicesPageURL("https://momentum.example/")
	if page != "https://momentum.example/premium" {
		t.Fatalf("page %q", page)
	}
	msg, err := RenderPremiumInvoiceEmail(r.invoice, page)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Invoice number: MP/26-27/000001", "Date: 13 Nov 2026", "Pass: Momentum Premium — 30 days",
		"Amount paid: ₹175.82", "View invoice: https://momentum.example/premium"} {
		if !strings.Contains(msg.Text, want) {
			t.Errorf("text lacks %q:\n%s", want, msg.Text)
		}
	}
	for _, want := range []string{`<a href="https://momentum.example/premium"`, ">View invoice</a>", "MP/26-27/000001", "₹175.82"} {
		if !strings.Contains(msg.HTML, want) {
			t.Errorf("html lacks %q", want)
		}
	}
	// Link only: no token, query, document route or attachment wording.
	for _, bad := range []string{"token", "/document", "?", "/v1/", "attach"} {
		if strings.Contains(msg.Text, bad) || strings.Contains(msg.HTML, bad) {
			t.Errorf("e-mail carries %q", bad)
		}
	}

	cn, err := RenderPremiumInvoiceEmail(r.credit, page)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Credit note number: MPC/26-27/000001", "Against invoice: MP/26-27/000001", "Amount refunded: ₹175.82", "Date: 14 Nov 2026"} {
		if !strings.Contains(cn.Text, want) {
			t.Errorf("credit note lacks %q:\n%s", want, cn.Text)
		}
	}
	if strings.Contains(cn.Text, "-") && strings.Contains(cn.Text, "₹-") {
		t.Error("refund printed negative")
	}

	// Without a usable web origin the e-mail names the page and has no link.
	nl, _ := RenderPremiumInvoiceEmail(r.invoice, PremiumInvoicesPageURL("javascript:alert(1)"))
	if strings.Contains(nl.HTML, "<a ") || strings.Contains(nl.Text, "View invoice:") || !strings.Contains(nl.Text, "Invoices") {
		t.Errorf("no-link copy: %s", nl.Text)
	}

	// The number reaches a header: anything outside the series is refused;
	// the description is escaped in HTML.
	bad := r.invoice
	bad.Number = "MP/26-27/1\r\nBcc: x@y.z"
	if _, err := RenderPremiumInvoiceEmail(bad, page); err == nil {
		t.Fatal("a number with a header injection rendered")
	}
	bad = r.invoice
	bad.Kind = "receipt"
	if _, err := RenderPremiumInvoiceEmail(bad, page); err == nil {
		t.Fatal("an unknown kind rendered")
	}
	esc := r.invoice
	esc.Description = `<script>x</script>`
	m, _ := RenderPremiumInvoiceEmail(esc, page)
	if strings.Contains(m.HTML, "<script>") {
		t.Fatal("description not escaped")
	}

	for paise, want := range map[int64]string{17582: "₹175.82", 176882: "₹1,768.82", -149900: "₹1,499.00", 12345678900: "₹12,34,56,789.00", 5: "₹0.05"} {
		if got := premiumMoney(paise, "INR"); got != want {
			t.Errorf("premiumMoney(%d) = %q, want %q", paise, got, want)
		}
	}
}

// captureLogs routes slog to buf (text) until restore is called.
func captureLogs(buf *bytes.Buffer) func() {
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	return func() { slog.SetDefault(prev) }
}

// The HTTP reader: internal key + a notification-service token for the one
// operation, user_id bound on the query, 404 = not found, a body for anyone
// else = error, no redirect followed.
func TestHTTPPremiumInvoices(t *testing.T) {
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := servicetoken.NewSignerFromBase64("notification-service", "n1", priv)
	if err != nil {
		t.Fatal(err)
	}
	v := servicetoken.NewVerifier("monetization")
	if err := v.RegisterBase64("notification-service", "n1", pub, []string{OpPremiumInvoiceMail}, nil); err != nil {
		t.Fatal(err)
	}
	user, inv := uuid.New(), uuid.New()
	var mode string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("X-Internal-Service-Key") != "k" {
			w.WriteHeader(401)
			return
		}
		tok := strings.TrimPrefix(req.Header.Get("X-Service-Authorization"), "Bearer ")
		if _, err := v.Verify(tok, OpPremiumInvoiceMail, ""); err != nil {
			w.WriteHeader(403)
			return
		}
		if req.URL.Path != "/v1/monetization/internal/premium/invoices/"+inv.String() || req.URL.Query().Get("user_id") != user.String() {
			w.WriteHeader(404)
			return
		}
		body := PremiumInvoiceFacts{ID: inv, UserID: user, Number: "MP/26-27/000001", Kind: "invoice", Date: "2026-11-13", TotalPaise: 17582, Currency: "INR"}
		switch mode {
		case "other":
			body.UserID = uuid.New()
		case "redirect":
			http.Redirect(w, req, "https://elsewhere.example/", http.StatusFound)
			return
		case "500":
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"secret":"buyer@example.com"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": body})
	}))
	defer srv.Close()
	c := NewHTTPPremiumInvoices(srv.URL+"/", "k", signer)
	ctx := context.Background()

	f, ok, err := c.PremiumInvoice(ctx, user, inv)
	if err != nil || !ok || f.Number != "MP/26-27/000001" || f.TotalPaise != 17582 {
		t.Fatalf("read: %+v %v %v", f, ok, err)
	}
	if _, ok, err := c.PremiumInvoice(ctx, uuid.New(), inv); err != nil || ok {
		t.Fatalf("another user's: %v %v", ok, err)
	}
	mode = "other"
	if _, ok, err := c.PremiumInvoice(ctx, user, inv); err == nil || ok {
		t.Fatal("a body for another user was accepted")
	}
	for _, m := range []string{"redirect", "500"} {
		mode = m
		_, ok, err := c.PremiumInvoice(ctx, user, inv)
		if err == nil || ok || strings.Contains(err.Error(), "@") {
			t.Fatalf("%s: %v %v", m, ok, err)
		}
	}
	if NewHTTPPremiumInvoices(srv.URL, "k", nil) != nil || NewHTTPPremiumInvoices("", "k", signer) != nil {
		t.Fatal("a reader without a signer or URL was built")
	}
}

func TestPremiumInvoiceSignerFromEnv(t *testing.T) {
	if s, err := PremiumInvoiceSignerFromEnv(func(string) string { return "" }); s != nil || err != nil {
		t.Fatalf("blank: %v %v", s, err)
	}
	_, priv, _ := servicetoken.GenerateKeypair()
	if s, err := PremiumInvoiceSignerFromEnv(func(k string) string {
		if k == "NOTIFICATION_SERVICE_TOKEN_KEY" {
			return priv
		}
		return ""
	}); s == nil || err != nil {
		t.Fatalf("key: %v %v", s, err)
	}
	if _, err := PremiumInvoiceSignerFromEnv(func(k string) string { return "not-a-key" }); err == nil || strings.Contains(err.Error(), "not-a-key") {
		t.Fatalf("bad key: %v", err)
	}
}
