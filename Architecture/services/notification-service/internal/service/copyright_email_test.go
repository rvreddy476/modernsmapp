package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/shared/events"
	"github.com/atpost/shared/mailer"
	"github.com/google/uuid"
)

// Copyright email (founder 24 Oct 2026): the uploader is emailed on a hold
// and on every block, once per (review, state), only at a verified address,
// with plain copy and a Creator Hub link.

type recordingMailer struct {
	sent []mailer.Message
	fail int // the first fail sends fail
}

func (m *recordingMailer) Send(_ context.Context, msg mailer.Message) error {
	if m.fail > 0 {
		m.fail--
		return errors.New("smtp: 421 to someone@example.com")
	}
	m.sent = append(m.sent, msg)
	return nil
}

type fakeContacts struct {
	addr     string
	verified bool
	err      error
	reads    int
}

func (f *fakeContacts) VerifiedEmail(context.Context, uuid.UUID) (string, bool, error) {
	f.reads++
	if f.err != nil {
		return "", false, f.err
	}
	if !f.verified || f.addr == "" {
		return "", false, nil
	}
	return f.addr, true, nil
}

type fakeLedger struct {
	claimed  map[uuid.UUID]bool
	released []uuid.UUID
}

func (l *fakeLedger) ClaimEventDedup(_ context.Context, id uuid.UUID) (bool, error) {
	if l.claimed[id] {
		return false, nil
	}
	l.claimed[id] = true
	return true, nil
}

func (l *fakeLedger) ReleaseEventDedup(_ context.Context, id uuid.UUID) error {
	delete(l.claimed, id)
	l.released = append(l.released, id)
	return nil
}

type emailRig struct {
	svc      *Service
	mail     *recordingMailer
	contacts *fakeContacts
	ledger   *fakeLedger
}

func newEmailRig(t *testing.T) *emailRig {
	t.Helper()
	r := &emailRig{mail: &recordingMailer{}, contacts: &fakeContacts{addr: "maker@example.com", verified: true},
		ledger: &fakeLedger{claimed: map[uuid.UUID]bool{}}}
	r.svc = &Service{}
	r.svc.SetMailer(r.mail)
	r.svc.SetCopyrightEmail(r.contacts, "https://cleestudio.com/")
	r.svc.setCopyrightEmailLedger(r.ledger, func(time.Duration) {})
	return r
}

func copyrightNotice(typ, reason string) CopyrightReviewNotification {
	return CopyrightReviewNotification{RecipientID: uuid.New(), ReviewID: uuid.New(), PostID: uuid.New(), Type: typ,
		MatchedTitle: "Jailer 2", PostTitle: "Jailer 2 full movie HD", RightsOwner: "Sun Pictures", Reason: reason,
		DeepLink: CopyrightHubDeepLink, CreatedAt: time.Now()}
}

func TestCopyrightEmail_SentOncePerState(t *testing.T) {
	r := newEmailRig(t)
	ctx := context.Background()
	hold := copyrightNotice(CopyrightTypeHold, "")
	for i := 0; i < 3; i++ { // redelivered
		if err := r.svc.sendCopyrightEmail(ctx, hold); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.mail.sent) != 1 || r.mail.sent[0].To[0] != "maker@example.com" {
		t.Fatalf("hold sent %d emails: %+v", len(r.mail.sent), r.mail.sent)
	}
	// The block of the same review is a different state: one more email.
	block := hold
	block.Type = CopyrightTypeBlocked
	for i := 0; i < 2; i++ {
		if err := r.svc.sendCopyrightEmail(ctx, block); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.mail.sent) != 2 {
		t.Fatalf("sent %d emails, want hold + block", len(r.mail.sent))
	}
	// A clear is not emailed.
	clear := hold
	clear.Type = CopyrightTypeCleared
	if err := r.svc.sendCopyrightEmail(ctx, clear); err != nil || len(r.mail.sent) != 2 {
		t.Fatalf("a clear was emailed: %d %v", len(r.mail.sent), err)
	}
	if CopyrightEmailDedupID(hold.ReviewID, CopyrightTypeHold) == CopyrightEmailDedupID(hold.ReviewID, CopyrightTypeBlocked) ||
		CopyrightEmailDedupID(hold.ReviewID, CopyrightTypeHold) == CopyrightEmailDedupID(uuid.New(), CopyrightTypeHold) {
		t.Fatal("dedup ids collide across states or reviews")
	}
}

// Dev without SMTP: the noop mailer would log the recipient; the copyright
// path logs without the address and does not hand it the message.
func TestCopyrightEmail_NoopMailerNeverLogsTheAddress(t *testing.T) {
	r := newEmailRig(t)
	var logs strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(prev)
	r.svc.SetMailer(mailer.NoopMailer{})
	if err := r.svc.sendCopyrightEmail(context.Background(), copyrightNotice(CopyrightTypeBlocked, "")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "maker@example.com") {
		t.Fatalf("the address reached the log: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "noop mailer") {
		t.Fatalf("no trace of the would-be email: %s", logs.String())
	}
}

func TestCopyrightEmail_SkippedWithoutVerifiedEmail(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]*fakeContacts{
		"unverified": {addr: "maker@example.com", verified: false},
		"no address": {verified: true},
	} {
		r := newEmailRig(t)
		r.svc.SetCopyrightEmail(c, "https://cleestudio.com")
		r.svc.setCopyrightEmailLedger(r.ledger, func(time.Duration) {})
		n := copyrightNotice(CopyrightTypeBlocked, events.CopyrightReasonOwnerRegistered)
		if err := r.svc.sendCopyrightEmail(ctx, n); err != nil {
			t.Fatalf("%s: err=%v, want a silent skip", name, err)
		}
		if len(r.mail.sent) != 0 {
			t.Fatalf("%s: emailed %+v", name, r.mail.sent)
		}
		// Kept claimed: a redelivery does not look the address up again.
		if err := r.svc.sendCopyrightEmail(ctx, n); err != nil || c.reads != 1 {
			t.Fatalf("%s: reads=%d err=%v", name, c.reads, err)
		}
	}
	// Not configured at all: nothing, no error.
	s := &Service{}
	if err := s.sendCopyrightEmail(ctx, copyrightNotice(CopyrightTypeHold, "")); err != nil {
		t.Fatal(err)
	}
}

// A send that fails is retried a bounded number of times; when it still
// fails the claim is released so a redelivery can send it.
func TestCopyrightEmail_FailureRetriesAndReleases(t *testing.T) {
	r := newEmailRig(t)
	ctx := context.Background()
	n := copyrightNotice(CopyrightTypeBlocked, "")
	r.mail.fail = 1
	if err := r.svc.sendCopyrightEmail(ctx, n); err != nil || len(r.mail.sent) != 1 {
		t.Fatalf("one failure then success: sent=%d err=%v", len(r.mail.sent), err)
	}
	r = newEmailRig(t)
	r.mail.fail = copyrightEmailAttempts
	err := r.svc.sendCopyrightEmail(ctx, n)
	if err == nil || len(r.mail.sent) != 0 || len(r.ledger.released) != 1 {
		t.Fatalf("all attempts failed: err=%v sent=%d released=%d", err, len(r.mail.sent), len(r.ledger.released))
	}
	if strings.Contains(err.Error(), "@") {
		t.Fatalf("the error carries an address: %v", err)
	}
	if err := r.svc.sendCopyrightEmail(ctx, n); err != nil || len(r.mail.sent) != 1 {
		t.Fatalf("redelivery after a release: sent=%d err=%v", len(r.mail.sent), err)
	}
	// A contact read that keeps failing is an error too, and released.
	r = newEmailRig(t)
	r.contacts.err = errors.New("identity down")
	if err := r.svc.sendCopyrightEmail(ctx, n); err == nil || len(r.ledger.released) != 1 || r.contacts.reads != copyrightEmailAttempts {
		t.Fatalf("contact failure: err=%v released=%d reads=%d", err, len(r.ledger.released), r.contacts.reads)
	}
}

func TestRenderCopyrightEmail_Copy(t *testing.T) {
	hub := CopyrightHubURL("https://cleestudio.com/")
	if hub != "https://cleestudio.com/posttube/hub" {
		t.Fatalf("hub = %q", hub)
	}

	auto, ok := RenderCopyrightEmail(copyrightNotice(CopyrightTypeBlocked, events.CopyrightReasonOwnerRegistered), hub)
	if !ok || auto.Subject != "Your VChat video was blocked for copyright" {
		t.Fatalf("subject = %q", auto.Subject)
	}
	for _, want := range []string{
		"\u201cJailer 2 full movie HD\u201d", "\u201cJailer 2\u201d", "Sun Pictures",
		"registered this title and asked us to block", "This is not a strike",
		"repeated copyright blocks pause your long-video uploads", "Creator Hub: https://cleestudio.com/posttube/hub", "The VChat team",
	} {
		if !strings.Contains(auto.Text, want) {
			t.Errorf("auto-block text lacks %q:\n%s", want, auto.Text)
		}
	}
	if !strings.Contains(auto.HTML, `href="https://cleestudio.com/posttube/hub"`) || !strings.Contains(auto.HTML, "Sun Pictures") {
		t.Errorf("html = %s", auto.HTML)
	}

	reviewer, _ := RenderCopyrightEmail(copyrightNotice(CopyrightTypeBlocked, ""), hub)
	if !strings.Contains(reviewer.Text, "Our team reviewed it and upheld the block") || strings.Contains(reviewer.Text, "asked us to block") {
		t.Errorf("reviewer block text:\n%s", reviewer.Text)
	}

	hold, _ := RenderCopyrightEmail(copyrightNotice(CopyrightTypeHold, ""), hub)
	if hold.Subject != "Your VChat video is in copyright review" || !strings.Contains(hold.Text, "hidden from viewers until the review is done") ||
		!strings.Contains(hold.Text, "This is not a strike") || !strings.Contains(hold.Text, "Creator Hub") {
		t.Errorf("hold: %q\n%s", hold.Subject, hold.Text)
	}
	mute, _ := RenderCopyrightEmail(copyrightNotice(CopyrightTypeHold, events.CopyrightReasonMuteRequested), hub)
	if !strings.Contains(mute.Text, "asked for its audio to be muted") {
		t.Errorf("mute hold:\n%s", mute.Text)
	}

	// No owner named, no title, no link.
	bare := copyrightNotice(CopyrightTypeBlocked, "")
	bare.RightsOwner, bare.PostTitle = "", ""
	b, _ := RenderCopyrightEmail(bare, "")
	if strings.Contains(b.Text, "registered by") || !strings.Contains(b.Text, "blocked your video because") ||
		!strings.Contains(b.Text, "Open Creator Hub in the VChat app") || strings.Contains(b.HTML, "href") {
		t.Errorf("bare:\n%s\n%s", b.Text, b.HTML)
	}
	if _, ok := RenderCopyrightEmail(copyrightNotice(CopyrightTypeCleared, ""), hub); ok {
		t.Fatal("a clear rendered an email")
	}
}

// No user-supplied value reaches a header, starts a line, or escapes the
// HTML.
func TestRenderCopyrightEmail_HostileTitles(t *testing.T) {
	n := copyrightNotice(CopyrightTypeBlocked, "")
	n.PostTitle = "Movie\r\nBcc: victim@example.com\r\n--atpost_alt_boundary_a1b2c3\u2028<script>alert(1)</script>"
	n.RightsOwner = "Owner\nX-Header: 1"
	e, _ := RenderCopyrightEmail(n, "https://cleestudio.com")
	if strings.ContainsAny(e.Subject, "\r\n") || strings.Contains(e.Subject, "Movie") {
		t.Fatalf("subject carries user text: %q", e.Subject)
	}
	for _, line := range strings.Split(e.Text, "\n") {
		if strings.HasPrefix(line, "Bcc:") || strings.HasPrefix(line, "--") || strings.HasPrefix(line, "X-Header") {
			t.Fatalf("a title started its own line: %q", line)
		}
	}
	if strings.Contains(e.HTML, "<script>") {
		t.Fatalf("html not escaped: %s", e.HTML)
	}
	// Non-space control characters (terminal escapes, NUL) are blanked too.
	ctl := copyrightNotice(CopyrightTypeHold, "")
	ctl.PostTitle = "Movie\x1b[31mRed\x00"
	if c, _ := RenderCopyrightEmail(ctl, ""); strings.ContainsAny(c.Text, "\x1b\x00") || strings.ContainsAny(c.HTML, "\x1b\x00") {
		t.Fatalf("a control character survived: %q", c.Text)
	}
	long := copyrightNotice(CopyrightTypeHold, "")
	long.PostTitle = strings.Repeat("a", 500)
	if l, _ := RenderCopyrightEmail(long, ""); strings.Contains(l.Text, strings.Repeat("a", 121)) {
		t.Fatal("an over-long title was not cut")
	}
}

func TestCopyrightHubURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://app.cleestudio.com":  "https://app.cleestudio.com/posttube/hub",
		"http://localhost:3000/":      "http://localhost:3000/posttube/hub",
		"":                            "",
		"cleestudio.com":              "",
		"javascript:alert(1)":         "",
		"https://x.com/?a=b":          "",
		"https://user:pw@example.com": "",
		"ftp://example.com":           "",
	} {
		if got := CopyrightHubURL(in); got != want {
			t.Errorf("CopyrightHubURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// The in-app notice and the email go together through
// CreateCopyrightReviewNotification: a reviewer's block and the system's
// both email; the email still goes when the in-app write fails (and the
// failure is returned); a clear is in-app only.
func TestCopyrightEmail_DeliveredWithTheInAppNotice(t *testing.T) {
	ctx := context.Background()
	r := newEmailRig(t)
	var inApp []string
	var inAppErr error
	r.svc.copyrightInAppSeam = func(_ context.Context, n CopyrightReviewNotification) error {
		inApp = append(inApp, n.Type)
		return inAppErr
	}
	reviewer := copyrightNotice(CopyrightTypeBlocked, "") // a reviewer's block: no reason
	if err := r.svc.CreateCopyrightReviewNotification(ctx, reviewer); err != nil || len(r.mail.sent) != 1 || len(inApp) != 1 {
		t.Fatalf("reviewer block: sent=%d inApp=%v err=%v", len(r.mail.sent), inApp, err)
	}
	if !strings.Contains(r.mail.sent[0].TextBody, "upheld the block") || r.mail.sent[0].HTMLBody == "" {
		t.Fatalf("message = %+v", r.mail.sent[0])
	}
	auto := copyrightNotice(CopyrightTypeBlocked, events.CopyrightReasonOwnerRegistered)
	inAppErr = errors.New("scylla down")
	if err := r.svc.CreateCopyrightReviewNotification(ctx, auto); err == nil || len(r.mail.sent) != 2 {
		t.Fatalf("in-app failure: sent=%d err=%v, want the email sent and the failure returned", len(r.mail.sent), err)
	}
	inAppErr = nil
	clear := copyrightNotice(CopyrightTypeCleared, "")
	if err := r.svc.CreateCopyrightReviewNotification(ctx, clear); err != nil || len(r.mail.sent) != 2 || len(inApp) != 3 {
		t.Fatalf("clear: sent=%d inApp=%v err=%v", len(r.mail.sent), inApp, err)
	}
}

// identity's contact read: only a verified, plausible address for the same
// user; the key goes, no user header, no redirect followed.
func TestHTTPVerifiedEmails(t *testing.T) {
	user := uuid.New()
	var gotKey, gotUser string
	body := map[string]any{"user_id": user.String(), "email": "maker@example.com", "email_verified": true}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotKey, gotUser = req.Header.Get("X-Internal-Service-Key"), req.Header.Get("X-User-Id")
		switch req.URL.Path {
		case "/v1/auth/internal/users/" + user.String():
			_ = json.NewEncoder(w).Encode(map[string]any{"data": body})
		case "/redirect":
			http.Redirect(w, req, "/v1/auth/internal/users/"+user.String(), http.StatusFound)
		default:
			http.NotFound(w, req)
		}
	}))
	defer srv.Close()
	c := NewHTTPVerifiedEmails(srv.URL+"/", "k-test")
	ctx := context.Background()
	if addr, ok, err := c.VerifiedEmail(ctx, user); err != nil || !ok || addr != "maker@example.com" || gotKey != "k-test" || gotUser != "" {
		t.Fatalf("verified: %q %v %v key=%q user=%q", addr, ok, err, gotKey, gotUser)
	}
	for name, b := range map[string]map[string]any{
		"unverified":   {"user_id": user.String(), "email": "maker@example.com", "email_verified": false},
		"no address":   {"user_id": user.String(), "email": "", "email_verified": true},
		"injection":    {"user_id": user.String(), "email": "a@b.com\r\nBcc: c@d.com", "email_verified": true},
		"two at signs": {"user_id": user.String(), "email": "a@b@c.com", "email_verified": true},
		"inner CRLF":   {"user_id": user.String(), "email": "a\r\nb@c.com", "email_verified": true},
	} {
		body = b
		if addr, ok, err := c.VerifiedEmail(ctx, user); err != nil || ok || addr != "" {
			t.Errorf("%s: %q %v %v", name, addr, ok, err)
		}
	}
	body = map[string]any{"user_id": uuid.NewString(), "email": "x@y.com", "email_verified": true}
	if _, ok, err := c.VerifiedEmail(ctx, user); err == nil || ok {
		t.Fatal("a body for another user was accepted")
	}
	body = map[string]any{"user_id": user.String(), "email": "x@y.com"}
	if _, _, err := c.VerifiedEmail(ctx, user); err == nil {
		t.Fatal("a body without email_verified was read as an answer")
	}
	if _, ok, err := c.VerifiedEmail(ctx, uuid.New()); err != nil || ok {
		t.Fatalf("unknown account: ok=%v err=%v", ok, err)
	}
	if NewHTTPVerifiedEmails(" ", "k") != nil {
		t.Fatal("an empty base URL built a client")
	}
}
