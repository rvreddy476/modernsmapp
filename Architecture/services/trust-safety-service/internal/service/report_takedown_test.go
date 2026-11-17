package service

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

// reels-29: an upheld post report's takedown is sent as post-service's
// report-v1 reject, recorded on the report, and retried until it lands.

type takedownRecord struct {
	id         uuid.UUID
	status     string
	lastError  string
	retryAfter time.Duration
}

type fakeTakedownStore struct {
	mu       sync.Mutex
	due      []postgres.ReportTakedown
	only     *uuid.UUID
	claimErr error
	records  []takedownRecord
}

func (f *fakeTakedownStore) ClaimReportTakedowns(_ context.Context, only *uuid.UUID, _ time.Duration, _ int) ([]postgres.ReportTakedown, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.only = only
	if f.claimErr != nil {
		return nil, f.claimErr
	}
	out := f.due
	f.due = nil
	return out, nil
}

func (f *fakeTakedownStore) RecordReportTakedown(_ context.Context, id uuid.UUID, status, lastError string, retryAfter time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records = append(f.records, takedownRecord{id, status, lastError, retryAfter})
	return nil
}

type rejectCall struct {
	report, post, moderator uuid.UUID
	revision                int64
	reason                  string
}

type fakeTakedownPost struct {
	subject    *PostModerationSubject
	subjectErr error
	rejectErr  error
	rejects    []rejectCall
}

func (f *fakeTakedownPost) GetSubject(context.Context, uuid.UUID) (*PostModerationSubject, error) {
	if f.subjectErr != nil {
		return nil, f.subjectErr
	}
	return f.subject, nil
}

func (f *fakeTakedownPost) OverturnAppeal(context.Context, *postgres.ContentAppeal, uuid.UUID, int64, string) error {
	return errors.New("not used")
}

func (f *fakeTakedownPost) RejectReportedPost(_ context.Context, report, post, moderator uuid.UUID, revision int64, reason string) error {
	f.rejects = append(f.rejects, rejectCall{report, post, moderator, revision, reason})
	return f.rejectErr
}

func takedownRig(t *testing.T, post *fakeTakedownPost) (*Service, *fakeTakedownStore, postgres.ReportTakedown) {
	t.Helper()
	td := postgres.ReportTakedown{ReportID: uuid.New(), PostID: uuid.New(), ActorID: uuid.New(), Reason: "spam reel removed", Attempts: 0}
	if post.subject == nil && post.subjectErr == nil {
		post.subject = &PostModerationSubject{PostID: td.PostID, AuthorID: uuid.New(), ReviewStatus: "approved", ContentRevision: 9}
	}
	st := &fakeTakedownStore{due: []postgres.ReportTakedown{td}}
	s := New(nil, nil)
	s.SetPostModerationClient(post)
	s.SetReportTakedownStore(st)
	return s, st, td
}

func TestReportTakedown_SendsTheReportDecisionAndRecordsSent(t *testing.T) {
	post := &fakeTakedownPost{}
	s, st, td := takedownRig(t, post)
	done, err := s.ProcessReportTakedowns(context.Background(), &td.ReportID)
	if err != nil || done != 1 {
		t.Fatalf("done=%d err=%v", done, err)
	}
	if st.only == nil || *st.only != td.ReportID {
		t.Fatalf("claim scoped to %v, want the report", st.only)
	}
	if len(post.rejects) != 1 {
		t.Fatalf("rejects = %d", len(post.rejects))
	}
	got := post.rejects[0]
	// The report id is the decision id; the revision is the fresh subject read.
	if got.report != td.ReportID || got.post != td.PostID || got.moderator != td.ActorID || got.revision != 9 || got.reason != td.Reason {
		t.Fatalf("reject = %+v, takedown = %+v", got, td)
	}
	if len(st.records) != 1 || st.records[0].status != postgres.TakedownSent || st.records[0].lastError != "" {
		t.Fatalf("records = %+v", st.records)
	}
}

func TestReportTakedown_Outcomes(t *testing.T) {
	cases := []struct {
		name       string
		subjectErr error
		deleted    bool
		rejectErr  error
		want       string
		sends      int
	}{
		{"post-service down", nil, false, errors.New("dial tcp: connection refused"), postgres.TakedownPending, 1},
		{"subject read fails", errors.New("post moderation subject returned 503"), false, nil, postgres.TakedownPending, 0},
		{"no token signer", nil, false, ErrPostModerationNotConfigured, postgres.TakedownPending, 1},
		{"stale revision", nil, false, ErrPostSubjectStale, postgres.TakedownPending, 1},
		{"post gone", ErrPostSubjectNotFound, false, nil, postgres.TakedownSuperseded, 0},
		{"post deleted", nil, true, nil, postgres.TakedownSuperseded, 0},
		{"cannot be rejected", nil, false, ErrPostDecisionSuperseded, postgres.TakedownSuperseded, 1},
		{"decision id reused", nil, false, ErrPostDecisionConflict, postgres.TakedownFailed, 1},
		{"invalid decision", nil, false, ErrPostDecisionRefused, postgres.TakedownFailed, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			post := &fakeTakedownPost{subjectErr: c.subjectErr, rejectErr: c.rejectErr}
			s, st, td := takedownRig(t, post)
			if post.subject != nil {
				post.subject.Deleted = c.deleted
			}
			if _, err := s.ProcessReportTakedowns(context.Background(), nil); err != nil {
				t.Fatal(err)
			}
			if len(post.rejects) != c.sends {
				t.Fatalf("sends = %d, want %d", len(post.rejects), c.sends)
			}
			if len(st.records) != 1 || st.records[0].id != td.ReportID || st.records[0].status != c.want {
				t.Fatalf("records = %+v, want %s", st.records, c.want)
			}
			r := st.records[0]
			if c.want == postgres.TakedownPending {
				// A retry is scheduled and the failure stays visible.
				if r.retryAfter <= 0 || r.lastError == "" {
					t.Fatalf("pending record = %+v", r)
				}
			} else if r.retryAfter != 0 {
				t.Fatalf("final record retries: %+v", r)
			}
		})
	}
}

func TestReportTakedown_NoSenderStaysPending(t *testing.T) {
	st := &fakeTakedownStore{due: []postgres.ReportTakedown{{ReportID: uuid.New(), PostID: uuid.New(), ActorID: uuid.New(), Reason: "r"}}}
	s := New(nil, nil)
	s.SetReportTakedownStore(st)
	if _, err := s.ProcessReportTakedowns(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if len(st.records) != 1 || st.records[0].status != postgres.TakedownPending {
		t.Fatalf("records = %+v", st.records)
	}
}

func TestReportTakedownBackoff(t *testing.T) {
	if reportTakedownBackoff(1) != 15*time.Second || reportTakedownBackoff(2) != 30*time.Second || reportTakedownBackoff(3) != time.Minute {
		t.Fatalf("backoff 1..3 = %v %v %v", reportTakedownBackoff(1), reportTakedownBackoff(2), reportTakedownBackoff(3))
	}
	if reportTakedownBackoff(40) != reportTakedownMaxRetry {
		t.Fatalf("backoff cap = %v", reportTakedownBackoff(40))
	}
}

// The real client: the report-v1 reject, signed, with the token, and the
// refusals mapped for the sender.
func TestPostModerationClient_RejectReportedPost(t *testing.T) {
	client, fake := newClientRig(t)
	report, post, moderator := uuid.New(), uuid.New(), uuid.New()
	if err := client.RejectReportedPost(context.Background(), report, post, moderator, 11, "spam reel removed"); err != nil {
		t.Fatal(err)
	}
	if !fake.tokenOK {
		t.Fatal("the takedown did not carry a valid post:moderation.write token")
	}
	c := fake.lastClaims
	if c.DecisionID != report.String() || c.SubjectID != post.String() || c.ContentRevision != 11 ||
		c.Decision != "reject" || c.ActorID != moderator.String() || c.Reason != "spam reel removed" ||
		c.PolicyVersion != "report-v1" || c.Issuer != "trust-safety-service" || c.Purpose != "post_moderation" {
		t.Fatalf("claims=%+v", c)
	}
	for _, tc := range []struct {
		code int
		body string
		want error
	}{
		{http.StatusConflict, "STALE_MODERATION_SUBJECT", ErrPostSubjectStale},
		{http.StatusConflict, "INVALID_TRANSITION", ErrPostDecisionSuperseded},
		{http.StatusConflict, "SUPERSEDED", ErrPostDecisionSuperseded},
		{http.StatusConflict, "DECISION_CONFLICT", ErrPostDecisionConflict},
		{http.StatusBadRequest, "INVALID_DECISION", ErrPostDecisionRefused},
	} {
		fake.commandCode, fake.commandErr = tc.code, tc.body
		if err := client.RejectReportedPost(context.Background(), report, post, moderator, 11, "r"); !errors.Is(err, tc.want) {
			t.Fatalf("%d %s: got %v, want %v", tc.code, tc.body, err, tc.want)
		}
	}
	for _, tc := range []struct {
		code int
		body string
	}{{http.StatusInternalServerError, ""}, {http.StatusForbidden, "INVALID_CAPABILITY"}, {http.StatusUnauthorized, "ADMIN_TOKEN_REQUIRED"}} {
		fake.commandCode, fake.commandErr = tc.code, tc.body
		err := client.RejectReportedPost(context.Background(), report, post, moderator, 11, "r")
		if status, _ := takedownOutcome(err); err == nil || status != postgres.TakedownPending {
			t.Fatalf("%d %s: got %v (%s), want a retried error", tc.code, tc.body, err, status)
		}
	}
	client.tokens = nil
	before := fake.commands
	if err := client.RejectReportedPost(context.Background(), report, post, moderator, 11, "r"); !errors.Is(err, ErrPostModerationNotConfigured) {
		t.Fatalf("no token signer: %v", err)
	}
	if fake.commands != before {
		t.Fatal("a takedown reached post-service without a token")
	}
}

// SetPostModerationClient picks the takedown sender up from the client; the
// appeal-only fakes leave it unset.
func TestSetPostModerationClientPicksUpTheTakedownSender(t *testing.T) {
	s := New(nil, nil)
	s.SetPostModerationClient(&fakeTakedownPost{})
	if s.takedownSender == nil {
		t.Fatal("takedown sender not set from a client that implements it")
	}
}
