package service

// Momentum Premium tax-invoice and credit-note e-mails (13 Nov 2026).
//
// monetization-service publishes premium.invoice.issued and
// premium.credit_note.issued on monetization.events with ids only (user_id,
// invoice_id, pass_id, kind). The buyer is e-mailed once per document:
//
//   - Queued, then sent by a worker: the consumer commits its offset only
//     after the job row is written (premium_invoice_email_jobs, migration
//     020) under a UUIDv5 of (event type, invoice id) — the same key the
//     event id is derived from — so a redelivered or re-published event
//     inserts nothing and the e-mail goes once. The worker retries durably
//     (the copyright e-mail queue's semantics, copyright_email.go).
//   - Facts from monetization, never guessed: number, date, plan and amount
//     are read at send time from monetization's internal, service-token
//     route GET /v1/monetization/internal/premium/invoices/:id?user_id=,
//     which answers only the event's buyer's own document (404 otherwise).
//     The answer must name that same buyer, invoice and kind, or nothing is
//     sent (invoice_mismatch, an operator alert).
//   - The address comes ONLY from identity's verified-contact read for the
//     event's user_id (the copyright e-mail's VerifiedEmailReader). No
//     verified address: skipped, logged at INFO without any address.
//   - Always sent, whatever the notification preferences say, like the
//     commerce invoice e-mail and the copyright e-mail: a tax document is a
//     transactional receipt and preferences model e-mail only as a master
//     switch and a digest. A deactivated or deletion-scheduled account still
//     gets nothing (suppression).
//   - Link, not attachment: the button opens the signed-in web page
//     WEB_APP_BASE_URL + /premium (Invoices section). No token, no document
//     URL and no document travels in the e-mail; the commerce invoice e-mail
//     links too.
//   - Nothing about the e-mail is logged: no address, number or amount.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/atpost/shared/mailer"
	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Premium document events (monetization-service events.EventPremium*).
const (
	EventPremiumInvoiceIssued    = "premium.invoice.issued"
	EventPremiumCreditNoteIssued = "premium.credit_note.issued"

	PremiumKindInvoice    = "invoice"
	PremiumKindCreditNote = "credit_note"

	// PremiumInvoicesDeepLink is the web page with the Invoices section.
	PremiumInvoicesDeepLink = "/premium"
	// PremiumEmailBrand names the product in the e-mail.
	PremiumEmailBrand = "Momentum"
)

// PremiumKindFor is the document kind an event announces ("" for none).
func PremiumKindFor(eventType string) string {
	switch eventType {
	case EventPremiumInvoiceIssued:
		return PremiumKindInvoice
	case EventPremiumCreditNoteIssued:
		return PremiumKindCreditNote
	}
	return ""
}

const premiumInvoiceEmailMaxAttempts = 12

// PremiumInvoiceFacts is what monetization answers for the e-mail. A credit
// note's TotalPaise is negative.
type PremiumInvoiceFacts struct {
	ID             uuid.UUID `json:"id"`
	UserID         uuid.UUID `json:"user_id"`
	Number         string    `json:"number"`
	Kind           string    `json:"kind"`
	Date           string    `json:"date"`
	PassID         uuid.UUID `json:"pass_id"`
	Description    string    `json:"description"`
	TotalPaise     int64     `json:"total_paise"`
	Currency       string    `json:"currency"`
	OriginalNumber string    `json:"original_number"`
}

// PremiumInvoiceReader reads one of userID's documents. found is false
// (nil error) when monetization has no such document for that user.
type PremiumInvoiceReader interface {
	PremiumInvoice(ctx context.Context, userID, invoiceID uuid.UUID) (PremiumInvoiceFacts, bool, error)
}

type premiumInvoiceEmailQueue interface {
	EnqueuePremiumInvoiceEmail(ctx context.Context, id, recipient uuid.UUID, payload []byte) (bool, error)
	ClaimPremiumInvoiceEmail(ctx context.Context) (*postgres.CopyrightEmailJob, error)
	FinishPremiumInvoiceEmail(ctx context.Context, j *postgres.CopyrightEmailJob, status, code string, delay time.Duration) error
}

type premiumInvoiceEmailer struct {
	contacts   VerifiedEmailReader
	invoices   PremiumInvoiceReader
	queue      premiumInvoiceEmailQueue // nil = the Postgres store's
	pageURL    string                   // absolute /premium link; "" = name the page without a link
	suppressed func(ctx context.Context, userID uuid.UUID) (bool, error)
}

// SetPremiumInvoiceEmail wires the e-mail: identity's verified-contact read,
// monetization's invoice read, and the web origin the /premium link is built
// on (WEB_APP_BASE_URL). A nil reader turns the e-mail off.
func (s *Service) SetPremiumInvoiceEmail(contacts VerifiedEmailReader, invoices PremiumInvoiceReader, webBaseURL string) {
	s.premiumInvoiceEmail = &premiumInvoiceEmailer{contacts: contacts, invoices: invoices, pageURL: PremiumInvoicesPageURL(webBaseURL)}
}

// setPremiumInvoiceEmailQueue replaces the queue and suppression read (tests).
func (s *Service) setPremiumInvoiceEmailQueue(q premiumInvoiceEmailQueue, suppressed func(context.Context, uuid.UUID) (bool, error)) {
	if s.premiumInvoiceEmail != nil {
		s.premiumInvoiceEmail.queue = q
		s.premiumInvoiceEmail.suppressed = suppressed
	}
}

// PremiumInvoicesPageURL is the absolute /premium link on webBaseURL, or ""
// when the base is not an absolute http(s) origin (CopyrightHubURL's rule).
func PremiumInvoicesPageURL(webBaseURL string) string {
	hub := CopyrightHubURL(webBaseURL)
	if hub == "" {
		return ""
	}
	return strings.TrimSuffix(hub, CopyrightHubDeepLink) + PremiumInvoicesDeepLink
}

func (s *Service) premiumInvoiceEmailConfigured() bool {
	e := s.premiumInvoiceEmail
	return e != nil && e.contacts != nil && e.invoices != nil && s.mail != nil
}

func (s *Service) premiumInvoiceQueue() premiumInvoiceEmailQueue {
	if s.premiumInvoiceEmail != nil && s.premiumInvoiceEmail.queue != nil {
		return s.premiumInvoiceEmail.queue
	}
	if s.pgStore != nil {
		return s.pgStore
	}
	return nil
}

var premiumInvoiceEmailNamespace = uuid.MustParse("4b1f0c2e-9d7a-4e55-8f36-2a6d1c9e0b71")

// PremiumInvoiceEmailDedupID is the queue key of one document's e-mail.
func PremiumInvoiceEmailDedupID(eventType string, invoiceID uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(premiumInvoiceEmailNamespace, []byte("premium-invoice-email|"+eventType+"|"+invoiceID.String()))
}

// PremiumInvoiceEvent is one decoded document event.
type PremiumInvoiceEvent struct {
	EventType string
	UserID    uuid.UUID
	InvoiceID uuid.UUID
	PassID    uuid.UUID
	Kind      string
}

// premiumInvoiceEmailPayload is a queued job: ids only.
type premiumInvoiceEmailPayload struct {
	RecipientID uuid.UUID `json:"recipient_id"`
	InvoiceID   uuid.UUID `json:"invoice_id"`
	PassID      uuid.UUID `json:"pass_id"`
	EventType   string    `json:"event_type"`
	Kind        string    `json:"kind"`
}

// QueuePremiumInvoiceEmail queues the buyer's e-mail for one document; the
// worker sends it. nil when it is queued, was queued before, or the e-mail
// is not configured. An error means nothing was queued: the consumer does
// not commit, so the event is delivered again.
func (s *Service) QueuePremiumInvoiceEmail(ctx context.Context, ev PremiumInvoiceEvent) error {
	if ev.UserID == uuid.Nil || ev.InvoiceID == uuid.Nil || PremiumKindFor(ev.EventType) == "" || PremiumKindFor(ev.EventType) != ev.Kind {
		return nil
	}
	if !s.premiumInvoiceEmailConfigured() {
		slog.Debug("premium invoice email skipped: not configured", "invoice_id", ev.InvoiceID, "kind", ev.Kind)
		return nil
	}
	q := s.premiumInvoiceQueue()
	if q == nil {
		return errors.New("premium invoice email: no queue")
	}
	raw, err := json.Marshal(premiumInvoiceEmailPayload{RecipientID: ev.UserID, InvoiceID: ev.InvoiceID, PassID: ev.PassID,
		EventType: ev.EventType, Kind: ev.Kind})
	if err != nil {
		return fmt.Errorf("premium invoice email: encode: %w", err)
	}
	queued, err := q.EnqueuePremiumInvoiceEmail(ctx, PremiumInvoiceEmailDedupID(ev.EventType, ev.InvoiceID), ev.UserID, raw)
	if err != nil {
		return fmt.Errorf("premium invoice email: queue: %w", err)
	}
	if queued {
		slog.Info("premium invoice email queued", "invoice_id", ev.InvoiceID, "kind", ev.Kind)
	}
	return nil
}

// RunPremiumInvoiceEmailWorker sends queued e-mails until ctx ends; it
// returns at once without a configured e-mail or a queue.
func (s *Service) RunPremiumInvoiceEmailWorker(ctx context.Context) {
	if !s.premiumInvoiceEmailConfigured() || s.premiumInvoiceQueue() == nil {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for i := 0; i < 20 && ctx.Err() == nil; i++ {
			if !s.runPremiumInvoiceEmailJob(ctx) {
				break
			}
		}
	}
}

// runPremiumInvoiceEmailJob leases, sends and finishes one job; false when
// there was none.
func (s *Service) runPremiumInvoiceEmailJob(ctx context.Context) bool {
	q := s.premiumInvoiceQueue()
	j, err := q.ClaimPremiumInvoiceEmail(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return false
	}
	if err != nil {
		slog.Error("premium invoice email queue read failed", "error", err)
		return false
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Minute)
	status, code := s.processPremiumInvoiceEmailJob(runCtx, j)
	cancel()
	if status == "pending" && j.Attempts >= premiumInvoiceEmailMaxAttempts {
		status = "failed"
	}
	if err := q.FinishPremiumInvoiceEmail(ctx, j, status, code, copyrightEmailRetryDelay(j.Attempts)); err != nil {
		slog.Error("premium invoice email result not recorded", "job_id", j.ID, "status", status, "error", err)
	}
	if status == "failed" {
		slog.Error("premium invoice email failed; needs operator attention", "job_id", j.ID, "reason", code, "attempts", j.Attempts)
	}
	return true
}

// processPremiumInvoiceEmailJob sends one leased e-mail: sent, skipped
// (suppressed, no such document for the buyer, no verified address),
// pending (retry) or failed (never sendable).
func (s *Service) processPremiumInvoiceEmailJob(ctx context.Context, j *postgres.CopyrightEmailJob) (status, code string) {
	var p premiumInvoiceEmailPayload
	if err := json.Unmarshal(j.Payload, &p); err != nil || p.RecipientID == uuid.Nil || p.InvoiceID == uuid.Nil ||
		PremiumKindFor(p.EventType) == "" || PremiumKindFor(p.EventType) != p.Kind {
		return "failed", "invalid_payload"
	}
	if !s.premiumInvoiceEmailConfigured() {
		return "pending", "unconfigured"
	}
	e := s.premiumInvoiceEmail
	suppressed, err := s.premiumInvoiceRecipientSuppressed(ctx, p.RecipientID)
	if err != nil {
		return "pending", "suppression_lookup_failed"
	}
	if suppressed {
		return "skipped", "account_suppressed"
	}
	facts, found, err := e.invoices.PremiumInvoice(ctx, p.RecipientID, p.InvoiceID)
	if err != nil {
		slog.Warn("premium invoice email: invoice read failed; will retry", "invoice_id", p.InvoiceID, "error", err)
		return "pending", "invoice_read_failed"
	}
	if !found {
		slog.Info("premium invoice email skipped: no such document for the buyer", "invoice_id", p.InvoiceID, "kind", p.Kind)
		return "skipped", "invoice_not_found"
	}
	// The document must be the event's buyer's, and the one announced: the
	// address below is the event user's, so anything else is never mailed.
	if facts.UserID != p.RecipientID || facts.ID != p.InvoiceID || facts.Kind != p.Kind {
		return "failed", "invoice_mismatch"
	}
	msg, err := RenderPremiumInvoiceEmail(facts, e.pageURL)
	if err != nil {
		return "failed", "unrenderable"
	}
	addr, ok, err := e.contacts.VerifiedEmail(ctx, p.RecipientID)
	if err != nil {
		slog.Warn("premium invoice email: contact read failed; will retry", "invoice_id", p.InvoiceID, "error", err)
		return "pending", "contact_read_failed"
	}
	if !ok {
		slog.Info("premium invoice email skipped: no verified address", "invoice_id", p.InvoiceID, "kind", p.Kind)
		return "skipped", "no_verified_address"
	}
	if isNoopMailer(s.mail) {
		slog.Info("premium invoice email (noop mailer, not sent)", "invoice_id", p.InvoiceID, "kind", p.Kind)
		return "sent", ""
	}
	if err := s.mail.Send(ctx, mailer.Message{To: []string{addr}, Subject: msg.Subject, TextBody: msg.Text, HTMLBody: msg.HTML}); err != nil {
		// The mailer's error may carry the address; it is not passed on.
		slog.Warn("premium invoice email: send failed; will retry", "invoice_id", p.InvoiceID, "kind", p.Kind)
		return "pending", "send_failed"
	}
	slog.Info("premium invoice email sent", "invoice_id", p.InvoiceID, "kind", p.Kind)
	return "sent", ""
}

func (s *Service) premiumInvoiceRecipientSuppressed(ctx context.Context, userID uuid.UUID) (bool, error) {
	if e := s.premiumInvoiceEmail; e != nil && e.suppressed != nil {
		return e.suppressed(ctx, userID)
	}
	if s.pgStore == nil {
		return false, nil
	}
	return s.pgStore.IsSuppressed(ctx, userID)
}

// ── rendering ───────────────────────────────────────────────────────────

// PremiumInvoiceEmail is one rendered e-mail.
type PremiumInvoiceEmail struct {
	Subject string
	Text    string
	HTML    string
}

// premiumDocNumber is the series shape (MP/26-27/000001, MPC/26-27/000001):
// the number reaches the Subject header, so nothing else is accepted.
var premiumDocNumber = regexp.MustCompile(`^[A-Z]{1,6}/[0-9]{2}-[0-9]{2}/[0-9]{1,12}$`)

var premiumInvoiceEmailHTML = template.Must(template.New("premium-invoice").Parse(
	`<!doctype html><html><body style="font-family:Arial,Helvetica,sans-serif;font-size:15px;line-height:1.5;color:#111">` +
		`{{range .Paras}}<p>{{.}}</p>{{end}}` +
		`<table cellpadding="0" cellspacing="0" style="border-collapse:collapse;margin:8px 0 16px">` +
		`{{range .Rows}}<tr><td style="padding:4px 16px 4px 0;color:#555">{{.Label}}</td><td style="padding:4px 0;font-weight:bold">{{.Value}}</td></tr>{{end}}` +
		`</table>` +
		`{{if .Link}}<p><a href="{{.Link}}" style="display:inline-block;padding:10px 20px;background:#1d3557;color:#fff;text-decoration:none;border-radius:6px">View invoice</a></p>` +
		`<p style="color:#555;font-size:13px">{{.After}}</p>{{else}}<p>{{.After}}</p>{{end}}` +
		`<p>— The {{.Brand}} team</p></body></html>`))

type premiumEmailRow struct{ Label, Value string }

// RenderPremiumInvoiceEmail renders the buyer's e-mail for a tax invoice or
// a credit note from monetization's facts. An error means the facts cannot
// be printed safely (unknown kind, a number outside the series, no date).
func RenderPremiumInvoiceEmail(f PremiumInvoiceFacts, pageURL string) (PremiumInvoiceEmail, error) {
	if !premiumDocNumber.MatchString(f.Number) {
		return PremiumInvoiceEmail{}, errors.New("premium invoice email: number outside the series")
	}
	date, err := time.Parse("2006-01-02", f.Date)
	if err != nil {
		return PremiumInvoiceEmail{}, errors.New("premium invoice email: bad date")
	}
	pass := emailText(f.Description, 80)
	if pass == "" {
		pass = "Momentum Premium"
	}
	amount := premiumMoney(f.TotalPaise, f.Currency)
	var subject string
	var paras []string
	var rows []premiumEmailRow
	switch f.Kind {
	case PremiumKindInvoice:
		subject = "Your Momentum Premium tax invoice " + f.Number
		paras = []string{"Hi,", "Thank you for buying Momentum Premium. Your tax invoice is ready."}
		rows = []premiumEmailRow{{"Invoice number", f.Number}, {"Date", date.Format("2 Jan 2006")}, {"Pass", pass}, {"Amount paid", amount}}
	case PremiumKindCreditNote:
		subject = "Credit note " + f.Number + " for your Momentum Premium refund"
		paras = []string{"Hi,", "We have refunded your Momentum Premium payment and issued a credit note for it."}
		rows = []premiumEmailRow{{"Credit note number", f.Number}, {"Date", date.Format("2 Jan 2006")}, {"Pass", pass}}
		if premiumDocNumber.MatchString(f.OriginalNumber) {
			rows = append(rows, premiumEmailRow{"Against invoice", f.OriginalNumber})
		}
		rows = append(rows, premiumEmailRow{"Amount refunded", amount})
	default:
		return PremiumInvoiceEmail{}, errors.New("premium invoice email: unknown kind")
	}
	after := "Open your Premium page in the " + PremiumEmailBrand + " app or on the web, and find it under Invoices."
	if pageURL != "" {
		after = "Sign in to see and download it under Invoices on your Premium page."
	}

	var text strings.Builder
	for _, p := range paras {
		text.WriteString(p + "\n\n")
	}
	for _, r := range rows {
		text.WriteString(r.Label + ": " + r.Value + "\n")
	}
	text.WriteString("\n")
	if pageURL != "" {
		text.WriteString("View invoice: " + pageURL + "\n")
	}
	text.WriteString(after + "\n\n— The " + PremiumEmailBrand + " team\n")

	var html bytes.Buffer
	if err := premiumInvoiceEmailHTML.Execute(&html, map[string]any{"Paras": paras, "Rows": rows, "Link": pageURL,
		"After": after, "Brand": PremiumEmailBrand}); err != nil {
		return PremiumInvoiceEmail{}, errors.New("premium invoice email: render")
	}
	return PremiumInvoiceEmail{Subject: subject, Text: text.String(), HTML: html.String()}, nil
}

// premiumMoney prints |paise| as rupees with Indian digit grouping
// (₹1,768.82); another currency is printed with its code.
func premiumMoney(paise int64, currency string) string {
	if paise < 0 {
		paise = -paise
	}
	whole := fmt.Sprintf("%d", paise/100)
	if len(whole) > 3 {
		head, tail := whole[:len(whole)-3], whole[len(whole)-3:]
		var groups []string
		for len(head) > 2 {
			groups = append([]string{head[len(head)-2:]}, groups...)
			head = head[:len(head)-2]
		}
		groups = append([]string{head}, groups...)
		whole = strings.Join(groups, ",") + "," + tail
	}
	n := fmt.Sprintf("%s.%02d", whole, paise%100)
	if c := strings.ToUpper(strings.TrimSpace(currency)); c != "" && c != "INR" {
		return emailText(c, 3) + " " + n
	}
	return "₹" + n
}

// ── monetization's invoice read ─────────────────────────────────────────

// OpPremiumInvoiceMail is the operation monetization's mail read requires.
const OpPremiumInvoiceMail = "monetization:premium-invoice-mail" //nolint:gosec // G101: service-token operation name, not a credential

// HTTPPremiumInvoices is PremiumInvoiceReader over monetization-service's
// internal route. Each call carries the internal key and a freshly minted
// notification-service token (aud monetization, the one operation); it never
// follows a redirect and never quotes a body in an error.
type HTTPPremiumInvoices struct {
	baseURL, internalKey string
	signer               *servicetoken.Signer
	http                 *http.Client
}

// NewHTTPPremiumInvoices returns nil without a base URL or a signer.
func NewHTTPPremiumInvoices(baseURL, internalKey string, signer *servicetoken.Signer) *HTTPPremiumInvoices {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" || signer == nil {
		return nil
	}
	return &HTTPPremiumInvoices{baseURL: base, internalKey: internalKey, signer: signer, http: &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// PremiumInvoice reads userID's document invoiceID.
func (c *HTTPPremiumInvoices) PremiumInvoice(ctx context.Context, userID, invoiceID uuid.UUID) (PremiumInvoiceFacts, bool, error) {
	tok, err := c.signer.Mint("monetization", "premium-invoice-mail", []string{OpPremiumInvoiceMail}, nil, time.Minute)
	if err != nil {
		return PremiumInvoiceFacts{}, false, errors.New("invoice read: mint token")
	}
	u := c.baseURL + "/v1/monetization/internal/premium/invoices/" + invoiceID.String() + "?user_id=" + url.QueryEscape(userID.String())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return PremiumInvoiceFacts{}, false, errors.New("invoice read: build request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Caller-Service", "notification-service")
	req.Header.Set("X-Service-Authorization", "Bearer "+tok)
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return PremiumInvoiceFacts{}, false, errors.New("invoice read: request failed")
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return PremiumInvoiceFacts{}, false, nil
	default:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return PremiumInvoiceFacts{}, false, fmt.Errorf("invoice read: status %d", resp.StatusCode)
	}
	var w struct {
		Data *PremiumInvoiceFacts `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&w); err != nil || w.Data == nil {
		return PremiumInvoiceFacts{}, false, errors.New("invoice read: malformed body")
	}
	if w.Data.UserID != userID || w.Data.ID != invoiceID {
		return PremiumInvoiceFacts{}, false, errors.New("invoice read: body is for a different document or user")
	}
	return *w.Data, true, nil
}

// PremiumInvoiceSignerFromEnv builds notification-service's token signer
// from NOTIFICATION_SERVICE_TOKEN_KEY / _KID (default kid "notification-1").
// A blank key is (nil, nil): the invoice e-mail is then off.
func PremiumInvoiceSignerFromEnv(getenv func(string) string) (*servicetoken.Signer, error) {
	key := strings.TrimSpace(getenv("NOTIFICATION_SERVICE_TOKEN_KEY"))
	if key == "" {
		return nil, nil
	}
	kid := strings.TrimSpace(getenv("NOTIFICATION_SERVICE_TOKEN_KID"))
	if kid == "" {
		kid = "notification-1"
	}
	s, err := servicetoken.NewSignerFromBase64("notification-service", kid, key)
	if err != nil {
		return nil, errors.New("NOTIFICATION_SERVICE_TOKEN_KEY is not a usable signing key")
	}
	return s, nil
}
