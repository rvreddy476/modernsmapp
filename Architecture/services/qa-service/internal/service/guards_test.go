package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atpost/qa-service/internal/store"
	"github.com/google/uuid"
)

func refusal(t *testing.T, err error, prefix string) {
	t.Helper()
	if prefix == "" {
		if err != nil {
			t.Fatalf("refused: %v", err)
		}
		return
	}
	if err == nil || !strings.HasPrefix(err.Error(), prefix) {
		t.Fatalf("got %v, want a %q refusal", err, prefix)
	}
}

// H5: the by-id visibility rule.
func TestQuestionVisibility(t *testing.T) {
	author, stranger := uuid.New(), uuid.New()
	cases := []struct {
		name                    string
		status                  string
		viewer                  *uuid.UUID
		inCommunity, restricted bool
		role                    string
		want                    string
	}{
		{"open global, signed out", "open", nil, false, false, "", ""},
		{"deleted", "deleted", &author, false, false, "", "not_found: "},
		{"private community, non-member", "open", &stranger, true, true, "", "forbidden: "},
		{"private community, pending member", "open", &stranger, true, true, "pending", "forbidden: "},
		{"private community, signed out", "open", nil, true, true, "", "forbidden: "},
		{"private community, member", "open", &stranger, true, true, "member", ""},
		{"public community, non-member", "open", &stranger, true, false, "", ""},
		{"pending, stranger", "pending_approval", &stranger, true, false, "member", "not_found: "},
		{"pending, signed out", "pending_approval", nil, true, false, "", "not_found: "},
		{"pending, author", "pending_approval", &author, true, false, "member", ""},
		{"pending, community moderator", "pending_approval", &stranger, true, false, "moderator", ""},
		{"pending, no community, stranger", "pending_approval", &stranger, false, false, "moderator", "not_found: "},
		{"closed, stranger", "closed", &stranger, false, false, "", ""},
		{"merged, stranger", "merged", &stranger, false, false, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			refusal(t, questionVisibility(c.status, author, c.viewer, c.inCommunity, c.restricted, c.role), c.want)
		})
	}
}

// H2: who closes, from which state.
func TestCloseDecision(t *testing.T) {
	author, stranger := uuid.New(), uuid.New()
	refusal(t, closeDecision("open", author, author, false, ""), "")
	refusal(t, closeDecision("open", author, stranger, false, ""), "forbidden: ")
	refusal(t, closeDecision("open", author, stranger, true, "member"), "forbidden: ")
	refusal(t, closeDecision("open", author, stranger, true, "moderator"), "")
	refusal(t, closeDecision("open", author, stranger, false, "moderator"), "forbidden: ") // a role outside a community is nothing
	for _, st := range []string{"closed", "merged", "pending_approval", "deleted"} {
		refusal(t, closeDecision(st, author, author, false, ""), "conflict: ")
	}
	// A stranger learns nothing about the state.
	refusal(t, closeDecision("merged", author, stranger, false, ""), "forbidden: ")
}

// H2: the author cannot undo a moderator's close, a merge or a pending state.
func TestReopenDecision(t *testing.T) {
	author, mod, stranger := uuid.New(), uuid.New(), uuid.New()
	self, other := &author, &mod

	asMod, err := reopenDecision("closed", author, self, author, false, "")
	refusal(t, err, "")
	if asMod {
		t.Fatal("an author's reopen must not take the moderator rule")
	}
	_, err = reopenDecision("closed", author, other, author, true, "member")
	refusal(t, err, "conflict: ")
	_, err = reopenDecision("closed", author, nil, author, false, "")
	refusal(t, err, "conflict: ")
	for _, st := range []string{"merged", "pending_approval", "open", "deleted"} {
		_, err = reopenDecision(st, author, self, author, false, "")
		refusal(t, err, "conflict: ")
		_, err = reopenDecision(st, author, other, mod, true, "admin")
		refusal(t, err, "conflict: ")
	}
	_, err = reopenDecision("closed", author, self, stranger, false, "")
	refusal(t, err, "forbidden: ")
	_, err = reopenDecision("closed", author, other, stranger, true, "expert")
	refusal(t, err, "forbidden: ")

	asMod, err = reopenDecision("closed", author, other, mod, true, "moderator")
	refusal(t, err, "")
	if !asMod {
		t.Fatal("a community moderator's reopen must take the moderator rule")
	}
}

func TestApprovalDecision(t *testing.T) {
	for _, role := range []string{"owner", "admin", "moderator"} {
		refusal(t, approvalDecision(role), "")
	}
	for _, role := range []string{"", "pending", "member", "expert", "space_manager"} {
		refusal(t, approvalDecision(role), "forbidden: ")
	}
}

func TestAnswerRequestDecision(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	refusal(t, answerRequestDecision("open", a, b), "")
	refusal(t, answerRequestDecision("open", a, a), "invalid: ")
	for _, st := range []string{"closed", "merged", "pending_approval"} {
		refusal(t, answerRequestDecision(st, a, b), "conflict: ")
	}
}

// M4: length caps.
func TestLengthCaps(t *testing.T) {
	long := func(n int) string { return strings.Repeat("é", n) } // multi-byte: caps count characters
	refusal(t, checkQuestionText(long(maxTitleLen), long(maxQuestionBodyLen), []string{long(maxTagLen)}), "")
	refusal(t, checkQuestionText(long(maxTitleLen+1), "", nil), "invalid: ")
	refusal(t, checkQuestionText("t", long(maxQuestionBodyLen+1), nil), "invalid: ")
	refusal(t, checkQuestionText("t", "", []string{long(maxTagLen + 1)}), "invalid: ")
	tags := make([]string, maxTags+1)
	for i := range tags {
		tags[i] = "t"
	}
	refusal(t, checkQuestionText("t", "", tags), "invalid: ")
	refusal(t, checkQuestionText("t", "", tags[:maxTags]), "")

	refusal(t, checkAnswerText(long(maxAnswerBodyLen)), "")
	refusal(t, checkAnswerText(long(maxAnswerBodyLen+1)), "invalid: ")
	refusal(t, checkAnswerText("   "), "invalid: ")
	refusal(t, checkCommentText(long(maxCommentLen)), "")
	refusal(t, checkCommentText(long(maxCommentLen+1)), "invalid: ")
	refusal(t, checkCommentText(""), "invalid: ")
	refusal(t, checkLen("reason", long(maxReasonLen+1), maxReasonLen), "invalid: ")
	refusal(t, checkVoteType("up"), "")
	refusal(t, checkVoteType("down"), "")
	refusal(t, checkVoteType("sideways"), "invalid: ")
}

// countingLimiter is an in-memory fixed window.
type countingLimiter struct {
	hits map[string]int
	err  error
}

func (l *countingLimiter) Allow(_ context.Context, key string, limit int, _ time.Duration) (bool, error) {
	if l.err != nil {
		return false, l.err
	}
	l.hits[key]++
	return l.hits[key] <= limit, nil
}

// M4: per-user limits refuse the hit past the limit, per user, per window.
func TestCheckRate(t *testing.T) {
	lim := &countingLimiter{hits: map[string]int{}}
	s := (&Service{}).WithRateLimiter(lim)
	ctx := context.Background()
	user, other := uuid.New(), uuid.New()
	rules := []rateRule{{"comment", 3, time.Hour}, {"comment", 5, 24 * time.Hour}}
	for i := 0; i < 3; i++ {
		refusal(t, s.checkRate(ctx, user, rules), "")
	}
	refusal(t, s.checkRate(ctx, user, rules), "rate_limited: ")
	refusal(t, s.checkRate(ctx, other, rules), "") // another user has their own window

	// The longer window binds too.
	daily := []rateRule{{"report", 100, time.Hour}, {"report", 2, 24 * time.Hour}}
	refusal(t, s.checkRate(ctx, user, daily), "")
	refusal(t, s.checkRate(ctx, user, daily), "")
	refusal(t, s.checkRate(ctx, user, daily), "rate_limited: ")

	// The production caps exist for every posting action.
	for name, r := range map[string][]rateRule{"question": questionRates, "answer": answerRates, "comment": commentRates,
		"answer_request": answerRequestRates, "report": reportRates} {
		if len(r) == 0 || r[0].limit <= 0 || r[0].action != name {
			t.Fatalf("%s caps = %+v", name, r)
		}
	}

	// Redis down: fail open with a warning.
	down := (&Service{}).WithRateLimiter(&countingLimiter{err: errors.New("redis down")})
	refusal(t, down.checkRate(ctx, user, rules), "")
}

// H1: only a real change to a cast vote is worth an event.
func TestShouldNotifyVote(t *testing.T) {
	s := &Service{}
	ctx := context.Background()
	v, tgt := uuid.New(), uuid.New()
	for _, c := range []struct {
		res  store.VoteResult
		want bool
	}{
		{store.VoteResult{Previous: "up", Current: "up"}, false},
		{store.VoteResult{Previous: "", Current: ""}, false},
		{store.VoteResult{Previous: "up", Current: ""}, false},
		{store.VoteResult{Previous: "", Current: "up"}, true},
		{store.VoteResult{Previous: "down", Current: "up"}, true},
	} {
		if got := s.shouldNotifyVote(ctx, "answer", v, tgt, c.res); got != c.want {
			t.Fatalf("%+v: notify=%v, want %v", c.res, got, c.want)
		}
	}
}

// The caps and enum checks run before any store call: this Service has no
// store, so a call that got past them would panic.
func TestServiceRefusesBadInputBeforeTheStore(t *testing.T) {
	s := &Service{}
	ctx := context.Background()
	u, id := uuid.New(), uuid.New()
	long := func(n int) string { return strings.Repeat("x", n) }

	refusal(t, s.RespondToAnswerRequest(ctx, id, u, ""), "invalid: ")
	refusal(t, s.RespondToAnswerRequest(ctx, id, u, "pending"), "invalid: ")
	_, _, err := s.CreateReport(ctx, u, "profile", id, "spam", "")
	refusal(t, err, "invalid: ")
	_, _, err = s.CreateReport(ctx, u, "answer", id, "  ", "")
	refusal(t, err, "invalid: ")
	_, _, err = s.CreateReport(ctx, u, "answer", id, long(maxReportReasonLen+1), "")
	refusal(t, err, "invalid: ")
	_, _, err = s.CreateReport(ctx, u, "answer", id, "spam", long(maxReportDetailLen+1))
	refusal(t, err, "invalid: ")
	_, err = s.CreateComment(ctx, id, u, long(maxCommentLen+1))
	refusal(t, err, "invalid: ")
	_, err = s.UpdateComment(ctx, id, u, "")
	refusal(t, err, "invalid: ")
	_, err = s.CreateAnswer(ctx, id, u, long(maxAnswerBodyLen+1), "", false)
	refusal(t, err, "invalid: ")
	_, err = s.UpdateAnswer(ctx, id, u, long(maxAnswerBodyLen+1), "")
	refusal(t, err, "invalid: ")
	_, err = s.CreateQuestion(ctx, u, store.CreateQuestionParams{Title: long(maxTitleLen + 1), Topics: []string{"go"}})
	refusal(t, err, "invalid: ")
	_, err = s.CreateQuestion(ctx, u, store.CreateQuestionParams{Title: "   ", Topics: []string{"go"}})
	refusal(t, err, "invalid: ")
	title := long(maxTitleLen + 1)
	_, err = s.UpdateQuestion(ctx, id, u, store.UpdateQuestionParams{Title: &title})
	refusal(t, err, "invalid: ")
	refusal(t, s.CloseQuestion(ctx, id, u, long(maxReasonLen+1)), "invalid: ")
	refusal(t, s.DecidePendingQuestion(ctx, id, id, u, false, long(maxReasonLen+1)), "invalid: ")
	refusal(t, s.VoteAnswer(ctx, u, id, "sideways"), "invalid: ")
	refusal(t, s.VoteQuestion(ctx, u, id, ""), "invalid: ")
	refusal(t, s.VoteComment(ctx, u, id, "UP"), "invalid: ")
}

// Reports: a target the reporter cannot see is the same 404 whatever hides
// it; one's own content, or oneself, is refused; another error passes
// through untouched (a 500, not a 404 that would hide an outage).
func TestReportTargetDecision(t *testing.T) {
	reporter, author := uuid.New(), uuid.New()
	outage := errors.New("connection reset")
	cases := []struct {
		name       string
		targetType string
		owner      uuid.UUID
		accessErr  error
		want       string
	}{
		{"visible, someone else's answer", "answer", author, nil, ""},
		{"visible, someone else's question", "question", author, nil, ""},
		{"another user", "user", author, nil, ""},
		{"missing answer", "answer", uuid.Nil, errors.New("not_found: answer not found"), "not_found: answer not found"},
		{"private community question", "question", uuid.Nil, errors.New("forbidden: this question is in a private community"), "not_found: question not found"},
		{"comment on a hidden answer", "comment", uuid.Nil, errors.New("not_found: question not found"), "not_found: comment not found"},
		{"missing user", "user", uuid.Nil, errors.New("not_found: user not found"), "not_found: user not found"},
		{"store outage", "answer", uuid.Nil, outage, "connection reset"},
		{"own answer", "answer", reporter, nil, "invalid: you cannot report your own answer"},
		{"own question", "question", reporter, nil, "invalid: you cannot report your own question"},
		{"own comment", "comment", reporter, nil, "invalid: you cannot report your own comment"},
		{"oneself", "user", reporter, nil, "invalid: you cannot report yourself"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := reportTargetDecision(c.targetType, reporter, c.owner, c.accessErr)
			if c.want == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if err == nil || err.Error() != c.want {
				t.Fatalf("got %v, want %q", err, c.want)
			}
			if c.accessErr == outage && !errors.Is(err, outage) {
				t.Fatalf("the outage error was rewrapped: %v", err)
			}
		})
	}
}
