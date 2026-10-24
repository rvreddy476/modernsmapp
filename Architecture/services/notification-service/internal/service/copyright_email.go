package service

// Copyright email (founder decision 24 Oct 2026: "Yes, block it with mail
// notification").
//
// On a copyright HOLD (pending) and on every copyright BLOCK — the system's
// for a registered rights owner, or a reviewer's — the uploader is told by
// EMAIL as well as in-app. The transport is the service's existing
// transactional mailer (shared/mailer over SMTP, which is SES-SMTP in the
// AWS environments: SMTP_HOST / SMTP_USERNAME / SMTP_PASSWORD /
// SMTP_FROM_ADDR); no new provider.
//
//   - The address comes from identity: auth-service's service-only contact
//     read GET {IDENTITY_AUTH_URL}/v1/auth/internal/users/:id (the route
//     live eligibility already uses). Only a VERIFIED address is mailed; no
//     address, an unverified one, or no such account is skipped silently.
//     The address is never logged, stored or put in an error.
//   - Always sent, whatever the notification preferences say: the
//     preferences model email only as a master switch and a digest, not per
//     category, and this is a legal-type notice about the user's content.
//     (Account suppression — deactivated or deletion scheduled — still
//     applies: such an account gets nothing.)
//   - At least once, and once per (review, state): the consumer handles the
//     event synchronously and commits its offset only after the email is
//     QUEUED (copyright_email_jobs, migration 019) under a UUIDv5 of the
//     pair — a redelivered event inserts nothing — and a worker sends it
//     with durable retries (RunCopyrightEmailWorker), so neither a crash nor
//     an SMTP outage loses it and an outage never stalls the topic. A send
//     repeats only if the worker dies after the mailer accepted it and
//     before the 'sent' write (the lease expires; the partner-queue window).
//     The answer to an appeal (migration 076, Appeal) is a new state of the
//     pair, so the author hears it even though the block was mailed before.
//   - The copy is plain and short, names the video and the matched
//     title/owner, says what it means (not a strike; repeated blocks pause
//     long-video uploads) and links to Creator Hub (WEB_APP_BASE_URL +
//     /posttube/hub). The subject is fixed text: no user-supplied value ever
//     reaches a mail header.

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
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/atpost/shared/mailer"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CopyrightEmailBrand is the product name the copyright emails use.
const CopyrightEmailBrand = "VChat"

// copyrightEmailMaxAttempts bounds one email's durable tries (lookup +
// send, one per lease) before it is marked failed for an operator. With the
// backoff below that is about nine hours of retrying.
const copyrightEmailMaxAttempts = 12

// VerifiedEmailReader returns a user's VERIFIED email address. ok is false
// (with a nil error) when there is none to mail: no address, an unverified
// one, or no such account.
type VerifiedEmailReader interface {
	VerifiedEmail(ctx context.Context, userID uuid.UUID) (addr string, ok bool, err error)
}

// copyrightEmailQueue is the durable queue (copyright_email_jobs, 019).
type copyrightEmailQueue interface {
	EnqueueCopyrightEmail(ctx context.Context, id, recipient uuid.UUID, payload []byte) (bool, error)
	ClaimCopyrightEmail(ctx context.Context) (*postgres.CopyrightEmailJob, error)
	FinishCopyrightEmail(ctx context.Context, j *postgres.CopyrightEmailJob, status, code string, delay time.Duration) error
}

// copyrightEmailer holds the copyright email's collaborators.
type copyrightEmailer struct {
	contacts VerifiedEmailReader
	queue    copyrightEmailQueue // nil = the Postgres store's, when there is one
	hubURL   string              // absolute Creator Hub link; "" = name it without a link
	// suppressed reports an account that gets nothing (tests); nil = the
	// Postgres store's suppression list.
	suppressed func(ctx context.Context, userID uuid.UUID) (bool, error)
}

// SetCopyrightEmail wires the copyright email: the identity contact read
// and the web origin the Creator Hub link is built on (WEB_APP_BASE_URL;
// must be an absolute http(s) URL, else the email names Creator Hub without
// a link). A nil reader turns the email off (logged at start-up).
func (s *Service) SetCopyrightEmail(contacts VerifiedEmailReader, webBaseURL string) {
	s.copyrightEmail = &copyrightEmailer{contacts: contacts, hubURL: CopyrightHubURL(webBaseURL)}
}

// setCopyrightEmailQueue replaces the queue and the suppression read (tests).
func (s *Service) setCopyrightEmailQueue(q copyrightEmailQueue, suppressed func(context.Context, uuid.UUID) (bool, error)) {
	if s.copyrightEmail != nil {
		s.copyrightEmail.queue = q
		s.copyrightEmail.suppressed = suppressed
	}
}

func (s *Service) copyrightQueue() copyrightEmailQueue {
	if s.copyrightEmail != nil && s.copyrightEmail.queue != nil {
		return s.copyrightEmail.queue
	}
	if s.pgStore != nil {
		return s.pgStore
	}
	return nil
}

// copyrightEmailConfigured: a contact read, a mailer, and somewhere to queue.
func (s *Service) copyrightEmailConfigured() bool {
	e := s.copyrightEmail
	return e != nil && e.contacts != nil && s.mail != nil
}

// CopyrightHubURL is the absolute Creator Hub link on webBaseURL, or "" when
// the base is not an absolute http(s) URL with a host and no query.
func CopyrightHubURL(webBaseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(webBaseURL), "/")
	if base == "" {
		return ""
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return ""
	}
	return base + CopyrightHubDeepLink
}

// copyrightEmailNamespace scopes the email's dedup ids.
var copyrightEmailNamespace = uuid.MustParse("8d5f3f0e-6a51-4f3c-9c2b-6c0a2f9e7d14")

// CopyrightEmailDedupID is the queue key for one (review, state) email —
// the same id the pre-queue ledger used, so an email sent before the queue
// existed is recognised (EnqueueCopyrightEmail).
func CopyrightEmailDedupID(reviewID uuid.UUID, notifType string) uuid.UUID {
	return uuid.NewSHA1(copyrightEmailNamespace, []byte("copyright-email|"+reviewID.String()+"|"+notifType))
}

// emailDedupID is n's queue key: the answer to an appeal is keyed apart
// from the block it reopened, so it is mailed too.
func (n CopyrightReviewNotification) emailDedupID() uuid.UUID {
	key := n.Type
	if n.Appeal {
		key += "|appeal"
	}
	return CopyrightEmailDedupID(n.ReviewID, key)
}

// emailsFor reports whether a copyright notice type is also emailed: the
// hold and the block. A clear is good news and stays in-app.
func emailsFor(notifType string) bool {
	return notifType == CopyrightTypeHold || notifType == CopyrightTypeBlocked
}

// emailText makes a value safe and short for the email body: every control
// or line-separator character becomes a space (so no value can start a MIME
// boundary or header line), runs of space collapse, and it is cut to max
// runes.
func emailText(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == 0x2028 || r == 0x2029 || r == 0xFEFF {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max-1]) + "…"
	}
	return s
}

// CopyrightEmail is one rendered email.
type CopyrightEmail struct {
	Subject string
	Text    string
	HTML    string
}

// copyrightEmailHTML renders the same copy as the text body, escaped.
var copyrightEmailHTML = template.Must(template.New("copyright").Parse(
	`<!doctype html><html><body style="font-family:Arial,Helvetica,sans-serif;font-size:15px;line-height:1.5;color:#111">` +
		`{{range .Paras}}<p>{{.}}</p>{{end}}` +
		`{{if .Link}}<p><a href="{{.Link}}">Open Creator Hub</a></p>{{end}}` +
		`<p>— The {{.Brand}} team</p></body></html>`))

// RenderCopyrightEmail renders the email for a hold or a block. ok is false
// for any other type.
func RenderCopyrightEmail(n CopyrightReviewNotification, hubURL string) (CopyrightEmail, bool) {
	if !emailsFor(n.Type) {
		return CopyrightEmail{}, false
	}
	video := "your video"
	if t := emailText(n.PostTitle, 120); t != "" {
		video = "your video “" + t + "”"
	}
	matched := emailText(n.MatchedTitle, 120)
	owner := emailText(n.RightsOwner, 120)
	what := "“" + matched + "”"
	if owner != "" {
		what += ", registered by its rights owner " + owner
	}
	const meaning = "This is not a strike. But repeated copyright blocks pause your long-video uploads for a while."

	var subject string
	var paras []string
	switch n.Type {
	case CopyrightTypeHold:
		subject = "Your " + CopyrightEmailBrand + " video is in copyright review"
		lead := "We have put " + video + " on hold because it matches " + what + "."
		if n.Reason == events.CopyrightReasonMuteRequested {
			lead += " The rights owner asked for its audio to be muted, so our team is reviewing it."
		} else {
			lead += " Our team is reviewing it."
		}
		paras = []string{
			"Hi,",
			lead,
			"What this means: the video is hidden from viewers until the review is done. " + meaning,
			"If you own the rights or have permission to use this content, tell us in Creator Hub so the reviewer can see it.",
		}
	case CopyrightTypeBlocked:
		subject = "Your " + CopyrightEmailBrand + " video was blocked for copyright"
		lead := "We have blocked " + video + " because it matches " + what + "."
		next := "If you think this is a mistake, or you own the rights, ask for a review in Creator Hub."
		switch {
		case n.Appeal:
			// The reviewer's answer to the author's one appeal (076).
			lead += " Our team reviewed your request and upheld the block."
			next = "You have already used the review for this video, so it cannot be requested again."
		case n.Reason == events.CopyrightReasonOwnerRegistered:
			lead += " The rights owner registered this title and asked us to block copies."
		default:
			lead += " Our team reviewed it and upheld the block."
		}
		paras = []string{
			"Hi,",
			lead,
			"What this means: the video stays hidden from viewers. " + meaning,
			next,
		}
	}
	var text strings.Builder
	for _, p := range paras {
		text.WriteString(p)
		text.WriteString("\n\n")
	}
	if hubURL != "" {
		text.WriteString("Creator Hub: " + hubURL + "\n\n")
	} else {
		text.WriteString("Open Creator Hub in the " + CopyrightEmailBrand + " app.\n\n")
	}
	text.WriteString("— The " + CopyrightEmailBrand + " team\n")

	var html bytes.Buffer
	if err := copyrightEmailHTML.Execute(&html, map[string]any{"Paras": paras, "Link": hubURL, "Brand": CopyrightEmailBrand}); err != nil {
		return CopyrightEmail{Subject: subject, Text: text.String()}, true
	}
	return CopyrightEmail{Subject: subject, Text: text.String(), HTML: html.String()}, true
}

// copyrightEmailPayload is a queued email: ids and the titles the copy
// names, never an address (it is read at send time).
type copyrightEmailPayload struct {
	RecipientID  uuid.UUID `json:"recipient_id"`
	ReviewID     uuid.UUID `json:"review_id"`
	PostID       uuid.UUID `json:"post_id"`
	Type         string    `json:"type"`
	MatchedTitle string    `json:"matched_title,omitempty"`
	PostTitle    string    `json:"post_title,omitempty"`
	RightsOwner  string    `json:"rights_owner,omitempty"`
	Reason       string    `json:"reason,omitempty"`
	Appeal       bool      `json:"appeal,omitempty"`
}

func (p copyrightEmailPayload) notification() CopyrightReviewNotification {
	return CopyrightReviewNotification{RecipientID: p.RecipientID, ReviewID: p.ReviewID, PostID: p.PostID, Type: p.Type,
		MatchedTitle: p.MatchedTitle, PostTitle: p.PostTitle, RightsOwner: p.RightsOwner, Reason: p.Reason, Appeal: p.Appeal}
}

// sendCopyrightEmail QUEUES the author's email about a hold or a block
// (the worker sends it). Skipped (nil) for other types and when the email
// is not configured. An error means nothing was queued: the consumer does
// not commit the event, so it is delivered again. Queued at most once per
// (review, state[, appeal]).
func (s *Service) sendCopyrightEmail(ctx context.Context, n CopyrightReviewNotification) error {
	if _, ok := RenderCopyrightEmail(n, ""); !ok {
		return nil
	}
	if !s.copyrightEmailConfigured() {
		slog.Debug("copyright email skipped: not configured", "review_id", n.ReviewID, "type", n.Type)
		return nil
	}
	q := s.copyrightQueue()
	if q == nil {
		return errors.New("copyright email: no queue")
	}
	raw, err := json.Marshal(copyrightEmailPayload{RecipientID: n.RecipientID, ReviewID: n.ReviewID, PostID: n.PostID, Type: n.Type,
		MatchedTitle: n.MatchedTitle, PostTitle: n.PostTitle, RightsOwner: n.RightsOwner, Reason: n.Reason, Appeal: n.Appeal})
	if err != nil {
		return fmt.Errorf("copyright email: encode: %w", err)
	}
	queued, err := q.EnqueueCopyrightEmail(ctx, n.emailDedupID(), n.RecipientID, raw)
	if err != nil {
		return fmt.Errorf("copyright email: queue: %w", err)
	}
	if queued {
		slog.Info("copyright email queued", "review_id", n.ReviewID, "type", n.Type, "appeal", n.Appeal)
	}
	return nil
}

var errNoVerifiedEmail = errors.New("copyright email: no verified address")

// RunCopyrightEmailWorker sends queued copyright emails until ctx ends.
// Without a configured email or a queue it returns at once.
func (s *Service) RunCopyrightEmailWorker(ctx context.Context) {
	if !s.copyrightEmailConfigured() || s.copyrightQueue() == nil {
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
			if !s.runCopyrightEmailJob(ctx) {
				break
			}
		}
	}
}

// runCopyrightEmailJob leases, sends and finishes one job; false when there
// was none (or the queue could not be read).
func (s *Service) runCopyrightEmailJob(ctx context.Context) bool {
	q := s.copyrightQueue()
	j, err := q.ClaimCopyrightEmail(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return false
	}
	if err != nil {
		slog.Error("copyright email queue read failed", "error", err)
		return false
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Minute)
	status, code := s.processCopyrightEmailJob(runCtx, j)
	cancel()
	if status == "pending" && j.Attempts >= copyrightEmailMaxAttempts {
		status = "failed"
	}
	if err := q.FinishCopyrightEmail(ctx, j, status, code, copyrightEmailRetryDelay(j.Attempts)); err != nil {
		// The lease runs out and the job is tried again: at least once.
		slog.Error("copyright email result not recorded", "job_id", j.ID, "status", status, "error", err)
	}
	if status == "failed" {
		slog.Error("copyright email failed; needs operator attention", "job_id", j.ID, "reason", code, "attempts", j.Attempts)
	}
	return true
}

// copyrightEmailRetryDelay: 1, 2, 4 … minutes, at most an hour.
func copyrightEmailRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 7 {
		return time.Hour
	}
	return time.Minute * time.Duration(1<<(attempt-1))
}

// processCopyrightEmailJob sends one leased email and says how it ended:
// sent, skipped (nothing to send: suppressed account, no verified address),
// pending (try again later) or failed (cannot ever be sent).
func (s *Service) processCopyrightEmailJob(ctx context.Context, j *postgres.CopyrightEmailJob) (status, code string) {
	var p copyrightEmailPayload
	if err := json.Unmarshal(j.Payload, &p); err != nil || p.RecipientID == uuid.Nil || p.ReviewID == uuid.Nil {
		return "failed", "invalid_payload"
	}
	n := p.notification()
	msg, ok := RenderCopyrightEmail(n, s.copyrightEmailHub())
	if !ok {
		return "skipped", "not_emailed"
	}
	if !s.copyrightEmailConfigured() {
		return "pending", "unconfigured"
	}
	// An account deactivated or scheduled for deletion since the event gets
	// nothing; a lookup that fails is retried, not guessed.
	suppressed, err := s.copyrightRecipientSuppressed(ctx, n.RecipientID)
	if err != nil {
		return "pending", "suppression_lookup_failed"
	}
	if suppressed {
		return "skipped", "account_suppressed"
	}
	switch err := s.deliverCopyrightEmail(ctx, n, msg); {
	case err == nil:
		return "sent", ""
	case errors.Is(err, errNoVerifiedEmail):
		slog.Info("copyright email skipped: no verified address", "review_id", n.ReviewID, "type", n.Type)
		return "skipped", "no_verified_address"
	case errors.Is(err, errCopyrightContactRead):
		return "pending", "contact_read_failed"
	default:
		return "pending", "send_failed"
	}
}

func (s *Service) copyrightRecipientSuppressed(ctx context.Context, userID uuid.UUID) (bool, error) {
	if e := s.copyrightEmail; e != nil && e.suppressed != nil {
		return e.suppressed(ctx, userID)
	}
	if s.pgStore == nil {
		return false, nil
	}
	return s.pgStore.IsSuppressed(ctx, userID)
}

var errCopyrightContactRead = errors.New("copyright email: contact read failed")

// deliverCopyrightEmail looks the address up and sends, once (the queue
// retries). No verified address is errNoVerifiedEmail (not retried). No
// error carries the address.
func (s *Service) deliverCopyrightEmail(ctx context.Context, n CopyrightReviewNotification, msg CopyrightEmail) error {
	e := s.copyrightEmail
	addr, ok, err := e.contacts.VerifiedEmail(ctx, n.RecipientID)
	if err != nil {
		slog.Warn("copyright email: contact read failed; will retry", "review_id", n.ReviewID, "type", n.Type, "error", err)
		return errCopyrightContactRead
	}
	if !ok {
		return errNoVerifiedEmail
	}
	if isNoopMailer(s.mail) {
		// Dev without SMTP: the noop mailer would log the address. Log that
		// the email would have gone, without it.
		slog.Info("copyright email (noop mailer, not sent)", "review_id", n.ReviewID, "type", n.Type, "subject", msg.Subject)
		return nil
	}
	if err := s.mail.Send(ctx, mailer.Message{To: []string{addr}, Subject: msg.Subject, TextBody: msg.Text, HTMLBody: msg.HTML}); err != nil {
		// The mailer's error may carry the address; it is not passed on.
		slog.Warn("copyright email: send failed; will retry", "review_id", n.ReviewID, "type", n.Type)
		return errors.New("copyright email: send failed")
	}
	slog.Info("copyright email sent", "review_id", n.ReviewID, "type", n.Type)
	return nil
}

func isNoopMailer(m mailer.Mailer) bool {
	switch m.(type) {
	case mailer.NoopMailer, *mailer.NoopMailer:
		return true
	}
	return false
}

func (s *Service) copyrightEmailHub() string {
	if s.copyrightEmail == nil {
		return ""
	}
	return s.copyrightEmail.hubURL
}

// ── identity's contact read ─────────────────────────────────────────────

// HTTPVerifiedEmails is VerifiedEmailReader over identity auth-service's
// service-only contact read. It sends the internal key and NO user header,
// never follows a redirect (that would resend the key), and never quotes a
// body or an address in an error.
type HTTPVerifiedEmails struct {
	baseURL, internalKey string
	http                 *http.Client
}

// NewHTTPVerifiedEmails returns nil for an empty base URL.
func NewHTTPVerifiedEmails(baseURL, internalKey string) *HTTPVerifiedEmails {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil
	}
	return &HTTPVerifiedEmails{baseURL: base, internalKey: internalKey, http: &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (c *HTTPVerifiedEmails) VerifiedEmail(ctx context.Context, userID uuid.UUID) (string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/auth/internal/users/"+userID.String(), nil)
	if err != nil {
		return "", false, errors.New("build request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Caller-Service", "notification-service")
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", false, errors.New("contact read: request failed")
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return "", false, nil // no such account: nobody to mail
	default:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return "", false, fmt.Errorf("contact read: status %d", resp.StatusCode)
	}
	var w struct {
		Data *struct {
			UserID        string `json:"user_id"`
			Email         string `json:"email"`
			EmailVerified *bool  `json:"email_verified"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&w); err != nil || w.Data == nil {
		return "", false, errors.New("contact read: malformed body")
	}
	if got, err := uuid.Parse(w.Data.UserID); err != nil || got != userID {
		return "", false, errors.New("contact read: body is for a different user")
	}
	if w.Data.EmailVerified == nil {
		return "", false, errors.New("contact read: no email_verified")
	}
	addr := strings.TrimSpace(w.Data.Email)
	if !*w.Data.EmailVerified || !plausibleAddress(addr) {
		return "", false, nil
	}
	return addr, true, nil
}

// plausibleAddress refuses anything that could not be one mailbox: no
// spaces or control characters (header injection), one @, a dot in the
// domain, and the RFC 5321 length.
func plausibleAddress(a string) bool {
	if a == "" || len(a) > 254 || strings.ContainsAny(a, " ,;<>\"\\") || strings.IndexFunc(a, unicode.IsControl) >= 0 {
		return false
	}
	at := strings.LastIndex(a, "@")
	if at <= 0 || at != strings.Index(a, "@") || at == len(a)-1 {
		return false
	}
	return strings.Contains(a[at+1:], ".")
}
