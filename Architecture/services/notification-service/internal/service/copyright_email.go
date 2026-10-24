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
//   - Idempotent per (review, state): notify_meta.event_dedup is claimed
//     under a UUIDv5 of the pair before sending; a failed send releases the
//     claim so a redelivery can try again. A crash between the claim and the
//     send loses that one email (at most once, never twice).
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

	"github.com/atpost/shared/events"
	"github.com/atpost/shared/mailer"
	"github.com/google/uuid"
)

// CopyrightEmailBrand is the product name the copyright emails use.
const CopyrightEmailBrand = "VChat"

// copyrightEmailAttempts bounds one delivery's tries (lookup + send).
const copyrightEmailAttempts = 3

// VerifiedEmailReader returns a user's VERIFIED email address. ok is false
// (with a nil error) when there is none to mail: no address, an unverified
// one, or no such account.
type VerifiedEmailReader interface {
	VerifiedEmail(ctx context.Context, userID uuid.UUID) (addr string, ok bool, err error)
}

// emailLedger is the idempotency ledger (notify_meta.event_dedup).
type emailLedger interface {
	ClaimEventDedup(ctx context.Context, id uuid.UUID) (bool, error)
	ReleaseEventDedup(ctx context.Context, id uuid.UUID) error
}

// copyrightEmailer holds the copyright email's collaborators.
type copyrightEmailer struct {
	contacts VerifiedEmailReader
	ledger   emailLedger // nil = the Postgres store's, when there is one
	hubURL   string      // absolute Creator Hub link; "" = name it without a link
	sleep    func(time.Duration)
}

// SetCopyrightEmail wires the copyright email: the identity contact read
// and the web origin the Creator Hub link is built on (WEB_APP_BASE_URL;
// must be an absolute http(s) URL, else the email names Creator Hub without
// a link). A nil reader turns the email off (logged at start-up).
func (s *Service) SetCopyrightEmail(contacts VerifiedEmailReader, webBaseURL string) {
	s.copyrightEmail = &copyrightEmailer{contacts: contacts, hubURL: CopyrightHubURL(webBaseURL), sleep: time.Sleep}
}

// setCopyrightEmailLedger replaces the ledger and the retry wait (tests).
func (s *Service) setCopyrightEmailLedger(l emailLedger, sleep func(time.Duration)) {
	if s.copyrightEmail != nil {
		s.copyrightEmail.ledger = l
		if sleep != nil {
			s.copyrightEmail.sleep = sleep
		}
	}
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

// CopyrightEmailDedupID is the ledger key for one (review, state) email.
func CopyrightEmailDedupID(reviewID uuid.UUID, notifType string) uuid.UUID {
	return uuid.NewSHA1(copyrightEmailNamespace, []byte("copyright-email|"+reviewID.String()+"|"+notifType))
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
		if n.Reason == events.CopyrightReasonOwnerRegistered {
			lead += " The rights owner registered this title and asked us to block copies."
		} else {
			lead += " Our team reviewed it and upheld the block."
		}
		paras = []string{
			"Hi,",
			lead,
			"What this means: the video stays hidden from viewers. " + meaning,
			"If you think this is a mistake, or you own the rights, ask for a review in Creator Hub.",
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

// sendCopyrightEmail emails the author about a hold or a block. Skipped
// (nil) for other types, when unconfigured, and when the author has no
// verified address. Each (review, type) is mailed at most once.
func (s *Service) sendCopyrightEmail(ctx context.Context, n CopyrightReviewNotification) error {
	msg, ok := RenderCopyrightEmail(n, s.copyrightEmailHub())
	if !ok {
		return nil
	}
	e := s.copyrightEmail
	if e == nil || e.contacts == nil || s.mail == nil {
		slog.Debug("copyright email skipped: not configured", "review_id", n.ReviewID, "type", n.Type)
		return nil
	}
	ledger := e.ledger
	if ledger == nil && s.pgStore != nil {
		ledger = s.pgStore
	}
	dedup := CopyrightEmailDedupID(n.ReviewID, n.Type)
	if ledger != nil {
		first, err := ledger.ClaimEventDedup(ctx, dedup)
		if err != nil {
			return fmt.Errorf("copyright email: claim: %w", err)
		}
		if !first {
			return nil // already sent for this review and state
		}
	}
	err := s.deliverCopyrightEmail(ctx, n, msg)
	if errors.Is(err, errNoVerifiedEmail) {
		// Nobody to mail: the claim stays, so a redelivery does not look again.
		slog.Info("copyright email skipped: no verified address", "review_id", n.ReviewID, "type", n.Type)
		return nil
	}
	if err != nil && ledger != nil {
		// Not sent: release the claim so a redelivery can try again.
		if rerr := ledger.ReleaseEventDedup(ctx, dedup); rerr != nil {
			slog.Error("copyright email: claim release failed; this email will not be retried",
				"review_id", n.ReviewID, "type", n.Type, "error", rerr)
		}
	}
	return err
}

var errNoVerifiedEmail = errors.New("copyright email: no verified address")

// deliverCopyrightEmail looks the address up and sends, with bounded
// retries. No verified address is errNoVerifiedEmail (not retried; the
// claim is kept so a redelivery does not look again).
func (s *Service) deliverCopyrightEmail(ctx context.Context, n CopyrightReviewNotification, msg CopyrightEmail) error {
	e := s.copyrightEmail
	var last error
	for attempt := 1; attempt <= copyrightEmailAttempts; attempt++ {
		if attempt > 1 {
			e.sleep(time.Duration(attempt-1) * time.Second)
		}
		addr, ok, err := e.contacts.VerifiedEmail(ctx, n.RecipientID)
		if err != nil {
			last = fmt.Errorf("copyright email: contact read: %w", err)
			continue
		}
		if !ok {
			return errNoVerifiedEmail
		}
		if isNoopMailer(s.mail) {
			// Dev without SMTP: the noop mailer would log the address. Log
			// that the email would have gone, without it.
			slog.Info("copyright email (noop mailer, not sent)", "review_id", n.ReviewID, "type", n.Type, "subject", msg.Subject)
			return nil
		}
		if err := s.mail.Send(ctx, mailer.Message{To: []string{addr}, Subject: msg.Subject, TextBody: msg.Text, HTMLBody: msg.HTML}); err != nil {
			// The mailer's error may carry the address; it is not passed on.
			last = errors.New("copyright email: send failed")
			continue
		}
		slog.Info("copyright email sent", "review_id", n.ReviewID, "type", n.Type, "attempt", attempt)
		return nil
	}
	slog.Error("copyright email failed", "review_id", n.ReviewID, "type", n.Type, "attempts", copyrightEmailAttempts, "error", last)
	return last
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
