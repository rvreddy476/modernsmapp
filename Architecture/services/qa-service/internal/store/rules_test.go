package store

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// H1: a vote pays only on a transition, and a removed or flipped vote
// reverses what it paid.
func TestVoteReputation(t *testing.T) {
	sum := func(evs []RepEvent) int {
		n := 0
		for _, e := range evs {
			n += e.Points
		}
		return n
	}
	for _, c := range []struct {
		target, old, new string
		want             int
		events           int
	}{
		{"answer", "", "up", RepAnswerUpvoted, 1},
		{"answer", "up", "up", 0, 0},
		{"answer", "down", "down", 0, 0},
		{"answer", "up", "", -RepAnswerUpvoted, 1},
		{"answer", "", "down", RepAnswerDownvoted, 1},
		{"answer", "down", "", -RepAnswerDownvoted, 1},
		{"answer", "up", "down", -RepAnswerUpvoted + RepAnswerDownvoted, 2},
		{"answer", "down", "up", -RepAnswerDownvoted + RepAnswerUpvoted, 2},
		{"question", "", "up", RepQuestionUpvoted, 1},
		{"question", "up", "", -RepQuestionUpvoted, 1},
		{"question", "up", "down", -RepQuestionUpvoted + RepQuestionDownvoted, 2},
		{"comment", "", "up", 0, 0},
		{"", "", "", 0, 0},
	} {
		evs := VoteReputation(c.target, c.old, c.new)
		if sum(evs) != c.want || len(evs) != c.events {
			t.Fatalf("%s %q->%q = %+v (sum %d), want sum %d in %d events", c.target, c.old, c.new, evs, sum(evs), c.want, c.events)
		}
	}
	// Repeated up votes: one award, however many calls.
	total := sum(VoteReputation("answer", "", "up"))
	for i := 0; i < 1000; i++ {
		total += sum(VoteReputation("answer", "up", "up"))
	}
	if total != RepAnswerUpvoted {
		t.Fatalf("1001 up votes paid %d, want %d", total, RepAnswerUpvoted)
	}
	// Up, remove, up again: one net award.
	if n := sum(VoteReputation("answer", "", "up")) + sum(VoteReputation("answer", "up", "")) + sum(VoteReputation("answer", "", "up")); n != RepAnswerUpvoted {
		t.Fatalf("up/remove/up paid %d, want %d", n, RepAnswerUpvoted)
	}
	names := []string{}
	for _, e := range VoteReputation("answer", "up", "down") {
		names = append(names, e.EventType)
	}
	if !slices.Equal(names, []string{"answer_upvote_removed", "answer_downvoted"}) {
		t.Fatalf("flip events = %v", names)
	}
}

// H1/L4: no one votes on their own question, answer or comment; taking a
// vote back is always allowed.
func TestVoteAllowed(t *testing.T) {
	me, them := uuid.New(), uuid.New()
	if err := voteAllowed("up", me, me); !errors.Is(err, ErrForbidden) {
		t.Fatalf("self up vote: %v", err)
	}
	if err := voteAllowed("down", me, me); !errors.Is(err, ErrForbidden) {
		t.Fatalf("self down vote: %v", err)
	}
	if err := voteAllowed("", me, me); err != nil {
		t.Fatalf("removing a vote on own content: %v", err)
	}
	if err := voteAllowed("up", them, me); err != nil {
		t.Fatalf("a vote on another's content: %v", err)
	}
	if !strings.HasPrefix(ErrOwnContent.Error(), "forbidden: ") {
		t.Fatalf("ErrOwnContent = %q, want a forbidden refusal", ErrOwnContent)
	}
}

// H1: only the asker accepts, only on a question that can take one; no pay
// for accepting one's own answer.
func TestAcceptRules(t *testing.T) {
	asker, other := uuid.New(), uuid.New()
	if err := acceptDecision(asker, asker, "open"); err != nil {
		t.Fatal(err)
	}
	if err := acceptDecision(asker, asker, "closed"); err != nil {
		t.Fatal(err)
	}
	if err := acceptDecision(asker, other, "open"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a stranger accepting: %v", err)
	}
	for _, st := range []string{"merged", "pending_approval", "deleted"} {
		if err := acceptDecision(asker, asker, st); !errors.Is(err, ErrConflict) {
			t.Fatalf("accept on %s: %v", st, err)
		}
	}
	if acceptPays(asker, asker) {
		t.Fatal("accepting one's own answer must pay nothing")
	}
	if !acceptPays(asker, other) {
		t.Fatal("accepting another's answer pays")
	}
}

func TestVoteDelta(t *testing.T) {
	for _, c := range []struct {
		old, new   string
		ds, du, dd int
	}{
		{"", "up", 1, 1, 0}, {"up", "", -1, -1, 0}, {"up", "down", -2, -1, 1}, {"down", "up", 2, 1, -1}, {"up", "up", 0, 0, 0},
	} {
		ds, du, dd := voteDelta(c.old, c.new)
		if ds != c.ds || du != c.du || dd != c.dd {
			t.Fatalf("%q->%q = %d %d %d", c.old, c.new, ds, du, dd)
		}
	}
}

// H5: the list predicate names every part of the rule; the database proof
// is visibility_integration_test.go.
func TestQuestionVisibleSQLShape(t *testing.T) {
	sql := questionVisibleSQL("q", "$7")
	for _, want := range []string{"q.deleted_at IS NULL", "'pending_approval'", "q.author_id = $7::uuid", "('private', 'invite')", memberRolesSQL, moderatorRolesSQL} {
		if !strings.Contains(sql, want) {
			t.Fatalf("predicate lacks %q:\n%s", want, sql)
		}
	}
	if strings.Contains(memberRolesSQL, "'pending'") {
		t.Fatal("a pending member is not a member")
	}
}
