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

	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/atpost/shared/mailer"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

// fakeQueue is copyright_email_jobs with the store's semantics: an id is
// inserted once (ON CONFLICT DO NOTHING), a legacy event_dedup claim under
// the same id blocks it, a claim leases one due job, and only the lease
// holder's finish lands. expire() is a lease running out (a dead worker).
type fakeQueue struct {
	jobs     map[uuid.UUID]*fakeJob
	order    []uuid.UUID
	legacy   map[uuid.UUID]bool
	enqErr   error
	finished []string // status:code, in order
}

type fakeJob struct {
	recipient uuid.UUID
	payload   []byte
	status    string
	attempts  int
	lease     uuid.UUID
	delay     time.Duration
}

func newFakeQueue() *fakeQueue {
	return &fakeQueue{jobs: map[uuid.UUID]*fakeJob{}, legacy: map[uuid.UUID]bool{}}
}

func (q *fakeQueue) EnqueueCopyrightEmail(_ context.Context, id, recipient uuid.UUID, payload []byte) (bool, error) {
	if q.enqErr != nil {
		return false, q.enqErr
	}
	if q.jobs[id] != nil || q.legacy[id] {
		return false, nil
	}
	q.jobs[id] = &fakeJob{recipient: recipient, payload: append([]byte(nil), payload...), status: "pending"}
	q.order = append(q.order, id)
	return true, nil
}

func (q *fakeQueue) ClaimCopyrightEmail(context.Context) (*postgres.CopyrightEmailJob, error) {
	for _, id := range q.order {
		j := q.jobs[id]
		if j.status == "pending" || j.status == "expired" {
			j.status, j.lease = "processing", uuid.New()
			j.attempts++
			return &postgres.CopyrightEmailJob{ID: id, Payload: j.payload, LeaseID: j.lease, Attempts: j.attempts}, nil
		}
	}
	return nil, pgx.ErrNoRows
}

func (q *fakeQueue) FinishCopyrightEmail(_ context.Context, j *postgres.CopyrightEmailJob, status, code string, delay time.Duration) error {
	fj := q.jobs[j.ID]
	if fj == nil || fj.lease != j.LeaseID || fj.status != "processing" {
		return nil // not the lease holder: nothing lands
	}
	fj.status, fj.lease, fj.delay = status, uuid.Nil, delay
	q.finished = append(q.finished, status+":"+code)
	return nil
}

// expire hands every processing job back, as a lapsed lease does.
func (q *fakeQueue) expire() {
	for _, j := range q.jobs {
		if j.status == "processing" {
			j.status = "expired"
		}
	}
}

func (q *fakeQueue) status(id uuid.UUID) string {
	if j := q.jobs[id]; j != nil {
		return j.status
	}
	return ""
}

type emailRig struct {
	svc        *Service
	mail       *recordingMailer
	contacts   *fakeContacts
	queue      *fakeQueue
	suppressed bool
	suppErr    error
}

func newEmailRig(t *testing.T) *emailRig {
	t.Helper()
	r := &emailRig{mail: &recordingMailer{}, contacts: &fakeContacts{addr: "maker@example.com", verified: true}, queue: newFakeQueue()}
	r.svc = &Service{}
	r.svc.SetMailer(r.mail)
	r.svc.SetCopyrightEmail(r.contacts, "https://cleestudio.com/")
	r.svc.setCopyrightEmailQueue(r.queue, func(context.Context, uuid.UUID) (bool, error) { return r.suppressed, r.suppErr })
	return r
}

// drain runs the worker until the queue has nothing due.
func (r *emailRig) drain(t *testing.T) {
	t.Helper()
	for i := 0; i < 100 && r.svc.runCopyrightEmailJob(context.Background()); i++ {
	}
}

func copyrightNotice(typ, reason string) CopyrightReviewNotification {
	return CopyrightReviewNotification{RecipientID: uuid.New(), ReviewID: uuid.New(), PostID: uuid.New(), Type: typ,
		MatchedTitle: "Jailer 2", PostTitle: "Jailer 2 full movie HD", RightsOwner: "Sun Pictures", Reason: reason,
		DeepLink: CopyrightHubDeepLink, CreatedAt: time.Now()}
}

// A redelivered event queues nothing new: one email per review and state;
// a clear is never mailed; the answer to an appeal is a new email.
func TestCopyrightEmail_QueuedOncePerState(t *testing.T) {
	r := newEmailRig(t)
	ctx := context.Background()
	hold := copyrightNotice(CopyrightTypeHold, "")
	for i := 0; i < 3; i++ { // redelivered
		if err := r.svc.sendCopyrightEmail(ctx, hold); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.queue.order) != 1 {
		t.Fatalf("hold queued %d jobs, want one", len(r.queue.order))
	}
	if len(r.mail.sent) != 0 {
		t.Fatal("the consumer's call sent the email itself; only the worker sends")
	}
	if strings.Contains(string(r.queue.jobs[r.queue.order[0]].payload), "@") {
		t.Fatalf("the queued payload carries an address: %s", r.queue.jobs[r.queue.order[0]].payload)
	}
	block := hold
	block.Type = CopyrightTypeBlocked
	for i := 0; i < 2; i++ {
		if err := r.svc.sendCopyrightEmail(ctx, block); err != nil {
			t.Fatal(err)
		}
	}
	clear := hold
	clear.Type = CopyrightTypeCleared
	if err := r.svc.sendCopyrightEmail(ctx, clear); err != nil || len(r.queue.order) != 2 {
		t.Fatalf("a clear was queued: %d %v", len(r.queue.order), err)
	}
	r.drain(t)
	if len(r.mail.sent) != 2 || r.mail.sent[0].To[0] != "maker@example.com" {
		t.Fatalf("sent %d emails, want hold + block: %+v", len(r.mail.sent), r.mail.sent)
	}
	// Redelivered after it was sent: still nothing new.
	if err := r.svc.sendCopyrightEmail(ctx, block); err != nil || len(r.queue.order) != 2 {
		t.Fatalf("redelivery after the send queued again: %d %v", len(r.queue.order), err)
	}
	r.drain(t)
	if len(r.mail.sent) != 2 {
		t.Fatalf("a redelivery after the send mailed again: %d", len(r.mail.sent))
	}
	// The reviewer's answer to an appeal of the same block is mailed.
	answer := block
	answer.Appeal = true
	if err := r.svc.sendCopyrightEmail(ctx, answer); err != nil || len(r.queue.order) != 3 {
		t.Fatalf("appeal answer: jobs=%d err=%v", len(r.queue.order), err)
	}
	r.drain(t)
	if len(r.mail.sent) != 3 || !strings.Contains(r.mail.sent[2].TextBody, "reviewed your request and upheld the block") ||
		!strings.Contains(r.mail.sent[2].TextBody, "already used the review") {
		t.Fatalf("appeal answer email = %+v", r.mail.sent)
	}
	if CopyrightEmailDedupID(hold.ReviewID, CopyrightTypeHold) == CopyrightEmailDedupID(hold.ReviewID, CopyrightTypeBlocked) ||
		CopyrightEmailDedupID(hold.ReviewID, CopyrightTypeHold) == CopyrightEmailDedupID(uuid.New(), CopyrightTypeHold) ||
		block.emailDedupID() == answer.emailDedupID() {
		t.Fatal("dedup ids collide across states, reviews or appeals")
	}
	// The pre-queue ledger id is kept, so an email the ledger recorded
	// before the upgrade is not queued again.
	if block.emailDedupID() != CopyrightEmailDedupID(block.ReviewID, CopyrightTypeBlocked) {
		t.Fatal("the queue id is not the ledger id")
	}
	legacy := copyrightNotice(CopyrightTypeBlocked, "")
	r.queue.legacy[legacy.emailDedupID()] = true
	if err := r.svc.sendCopyrightEmail(ctx, legacy); err != nil || len(r.queue.order) != 3 {
		t.Fatalf("a ledger-recorded email was queued again: %d %v", len(r.queue.order), err)
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
	r.drain(t)
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
		r.svc.setCopyrightEmailQueue(r.queue, nil)
		n := copyrightNotice(CopyrightTypeBlocked, events.CopyrightReasonOwnerRegistered)
		if err := r.svc.sendCopyrightEmail(ctx, n); err != nil {
			t.Fatalf("%s: err=%v", name, err)
		}
		r.drain(t)
		if len(r.mail.sent) != 0 || r.queue.status(n.emailDedupID()) != "skipped" || c.reads != 1 {
			t.Fatalf("%s: sent=%+v status=%s reads=%d", name, r.mail.sent, r.queue.status(n.emailDedupID()), c.reads)
		}
		// Done: a redelivery does not look the address up again.
		_ = r.svc.sendCopyrightEmail(ctx, n)
		r.drain(t)
		if c.reads != 1 {
			t.Fatalf("%s: reads=%d after a redelivery", name, c.reads)
		}
	}
	// Not configured at all: nothing queued, no error.
	s := &Service{}
	if err := s.sendCopyrightEmail(ctx, copyrightNotice(CopyrightTypeHold, "")); err != nil {
		t.Fatal(err)
	}
}

// A failed send is NOT lost: the job stays pending with a backoff and the
// next claim sends it; after the last attempt it is failed for an operator.
// A contact read that fails is retried the same way.
func TestCopyrightEmail_FailureIsRetriedDurably(t *testing.T) {
	r := newEmailRig(t)
	ctx := context.Background()
	n := copyrightNotice(CopyrightTypeBlocked, "")
	if err := r.svc.sendCopyrightEmail(ctx, n); err != nil {
		t.Fatal(err)
	}
	r.mail.fail = 1
	if !r.svc.runCopyrightEmailJob(ctx) {
		t.Fatal("no job")
	}
	j := r.queue.jobs[n.emailDedupID()]
	if j.status != "pending" || j.delay != time.Minute || len(r.mail.sent) != 0 || r.queue.finished[0] != "pending:send_failed" {
		t.Fatalf("after a failed send: %+v finished=%v", j, r.queue.finished)
	}
	r.drain(t)
	if len(r.mail.sent) != 1 || j.status != "sent" {
		t.Fatalf("retry: sent=%d status=%s", len(r.mail.sent), j.status)
	}

	// Every attempt fails: failed after the last one, never sent.
	r = newEmailRig(t)
	n = copyrightNotice(CopyrightTypeHold, "")
	_ = r.svc.sendCopyrightEmail(ctx, n)
	r.mail.fail = 1000
	r.drain(t)
	j = r.queue.jobs[n.emailDedupID()]
	if j.status != "failed" || j.attempts != copyrightEmailMaxAttempts || len(r.mail.sent) != 0 {
		t.Fatalf("exhausted: %+v", j)
	}
	if last := r.queue.finished[len(r.queue.finished)-1]; last != "failed:send_failed" {
		t.Fatalf("last finish = %q", last)
	}

	// Identity unreachable: retried, not dropped.
	r = newEmailRig(t)
	r.contacts.err = errors.New("identity down")
	n = copyrightNotice(CopyrightTypeBlocked, "")
	_ = r.svc.sendCopyrightEmail(ctx, n)
	r.svc.runCopyrightEmailJob(ctx)
	if r.queue.status(n.emailDedupID()) != "pending" || r.queue.finished[0] != "pending:contact_read_failed" {
		t.Fatalf("contact failure: %v", r.queue.finished)
	}
	r.contacts.err = nil
	r.drain(t)
	if len(r.mail.sent) != 1 {
		t.Fatalf("after identity recovered: sent=%d", len(r.mail.sent))
	}
}

// A worker that dies after its claim (no finish) does not lose the email:
// the lease runs out and the next claim sends it, once.
func TestCopyrightEmail_DeadWorkerDoesNotLoseIt(t *testing.T) {
	r := newEmailRig(t)
	ctx := context.Background()
	n := copyrightNotice(CopyrightTypeBlocked, "")
	_ = r.svc.sendCopyrightEmail(ctx, n)
	if _, err := r.queue.ClaimCopyrightEmail(ctx); err != nil { // claimed, then the worker died
		t.Fatal(err)
	}
	r.drain(t)
	if len(r.mail.sent) != 0 {
		t.Fatal("a leased job was sent by a second worker before its lease ran out")
	}
	r.queue.expire()
	r.drain(t)
	if len(r.mail.sent) != 1 || r.queue.status(n.emailDedupID()) != "sent" {
		t.Fatalf("after the lease ran out: sent=%d status=%s", len(r.mail.sent), r.queue.status(n.emailDedupID()))
	}
}

// A queue that cannot be written is an error, so the consumer does not
// commit the event; an account suppressed since is skipped at send time,
// and a suppression read that fails is retried.
func TestCopyrightEmail_QueueAndSuppressionFailures(t *testing.T) {
	r := newEmailRig(t)
	ctx := context.Background()
	r.queue.enqErr = errors.New("postgres down")
	if err := r.svc.sendCopyrightEmail(ctx, copyrightNotice(CopyrightTypeHold, "")); err == nil {
		t.Fatal("an email that was not queued reported success")
	}
	r.queue.enqErr = nil

	n := copyrightNotice(CopyrightTypeHold, "")
	_ = r.svc.sendCopyrightEmail(ctx, n)
	r.suppErr = errors.New("postgres down")
	r.svc.runCopyrightEmailJob(ctx)
	if r.queue.status(n.emailDedupID()) != "pending" || len(r.mail.sent) != 0 {
		t.Fatalf("suppression read failure: status=%s", r.queue.status(n.emailDedupID()))
	}
	r.suppErr, r.suppressed = nil, true
	r.drain(t)
	if r.queue.status(n.emailDedupID()) != "skipped" || len(r.mail.sent) != 0 {
		t.Fatalf("suppressed account: status=%s sent=%d", r.queue.status(n.emailDedupID()), len(r.mail.sent))
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
// both queue an email; the email is queued even when the in-app write fails
// (and the failure is returned, so the event is redelivered; queueing again
// inserts nothing); a clear is in-app only; a suppression read that fails
// is an error to retry, never a silent drop.
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
	if err := r.svc.CreateCopyrightReviewNotification(ctx, reviewer); err != nil || len(r.queue.order) != 1 || len(inApp) != 1 {
		t.Fatalf("reviewer block: jobs=%d inApp=%v err=%v", len(r.queue.order), inApp, err)
	}
	auto := copyrightNotice(CopyrightTypeBlocked, events.CopyrightReasonOwnerRegistered)
	inAppErr = errors.New("scylla down")
	if err := r.svc.CreateCopyrightReviewNotification(ctx, auto); err == nil || len(r.queue.order) != 2 {
		t.Fatalf("in-app failure: jobs=%d err=%v, want the email queued and the failure returned", len(r.queue.order), err)
	}
	inAppErr = nil
	if err := r.svc.CreateCopyrightReviewNotification(ctx, auto); err != nil || len(r.queue.order) != 2 {
		t.Fatalf("redelivery: jobs=%d err=%v, want no second email", len(r.queue.order), err)
	}
	clear := copyrightNotice(CopyrightTypeCleared, "")
	if err := r.svc.CreateCopyrightReviewNotification(ctx, clear); err != nil || len(r.queue.order) != 2 || len(inApp) != 4 {
		t.Fatalf("clear: jobs=%d inApp=%v err=%v", len(r.queue.order), inApp, err)
	}
	r.drain(t)
	if len(r.mail.sent) != 2 || !strings.Contains(r.mail.sent[0].TextBody, "upheld the block") || r.mail.sent[0].HTMLBody == "" {
		t.Fatalf("sent = %+v", r.mail.sent)
	}
	r.suppErr = errors.New("postgres down")
	if err := r.svc.CreateCopyrightReviewNotification(ctx, copyrightNotice(CopyrightTypeHold, "")); err == nil || len(inApp) != 4 {
		t.Fatalf("suppression read failure: err=%v inApp=%v, want an error and nothing delivered", err, inApp)
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
