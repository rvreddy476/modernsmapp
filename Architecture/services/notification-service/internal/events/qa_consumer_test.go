package events

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeQALookup answers the legacy lookups and counts them.
type fakeQALookup struct {
	question  *qaQuestionResponse
	answer    *qaAnswerResponse
	err       error
	answerErr error // overrides err for answers
	calls     int
}

func (f *fakeQALookup) getQuestion(context.Context, string) (*qaQuestionResponse, error) {
	f.calls++
	return f.question, f.err
}

func (f *fakeQALookup) getAnswer(context.Context, string) (*qaAnswerResponse, error) {
	f.calls++
	if f.answerErr != nil {
		return nil, f.answerErr
	}
	return f.answer, f.err
}

func qaJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// H4: an anonymous answer notifies the asker without naming the answerer.
// M10: the recipient comes from the payload, with no lookup (the lookup
// cannot see anonymous or private-community questions).
func TestAnswerCreatedNotifiesTheAskerWithoutNamingAnAnonymousAnswerer(t *testing.T) {
	asker, answerer, q := uuid.New(), uuid.New(), uuid.New()
	lookup := &fakeQALookup{err: errors.New("403 private community")}
	for _, anon := range []bool{true, false} {
		d, err := qaAnswerCreatedDelivery(context.Background(), qaJSON(t, map[string]any{
			"answer_id": uuid.NewString(), "question_id": q.String(), "author_id": answerer.String(),
			"is_anonymous": anon, "question_author_id": asker.String(), "created_at": time.Now(),
		}), lookup)
		if err != nil || d == nil {
			t.Fatalf("anon=%v: delivery %v err %v", anon, d, err)
		}
		if d.Recipient != asker {
			t.Fatalf("anon=%v: recipient %s, want the asker", anon, d.Recipient)
		}
		if anon && d.Actor != uuid.Nil {
			t.Fatalf("an anonymous answer named its author %s", d.Actor)
		}
		if !anon && d.Actor != answerer {
			t.Fatalf("a named answer lost its actor")
		}
	}
	if lookup.calls != 0 {
		t.Fatalf("the payload carried the recipient but %d lookups ran", lookup.calls)
	}
}

func TestAnswerCreatedSkipsSelfAnswers(t *testing.T) {
	me := uuid.New()
	d, _ := qaAnswerCreatedDelivery(context.Background(), qaJSON(t, map[string]any{
		"answer_id": uuid.NewString(), "question_id": uuid.NewString(), "author_id": me.String(),
		"is_anonymous": true, "question_author_id": me.String(),
	}), &fakeQALookup{})
	if d != nil {
		t.Fatalf("notified the asker of their own anonymous answer: %+v", d)
	}
}

// A legacy event (no recipient, no anonymity flag) falls back to the lookup,
// and an answer it cannot confirm as named is sent without an actor.
func TestAnswerCreatedLegacyEventHidesAnUnconfirmedActor(t *testing.T) {
	asker, answerer := uuid.New(), uuid.New()
	lookup := &fakeQALookup{
		question: &qaQuestionResponse{ID: "q", AuthorID: asker.String()},
		answer:   &qaAnswerResponse{ID: "a", AuthorID: uuid.Nil.String(), IsAnonymous: true},
	}
	d, _ := qaAnswerCreatedDelivery(context.Background(), qaJSON(t, map[string]any{
		"answer_id": uuid.NewString(), "question_id": uuid.NewString(), "author_id": answerer.String(),
	}), lookup)
	if d == nil || d.Recipient != asker || d.Actor != uuid.Nil {
		t.Fatalf("legacy anonymous answer: %+v", d)
	}
	lookup.answer = &qaAnswerResponse{ID: "a", AuthorID: answerer.String()}
	d, _ = qaAnswerCreatedDelivery(context.Background(), qaJSON(t, map[string]any{
		"answer_id": uuid.NewString(), "question_id": uuid.NewString(), "author_id": answerer.String(),
	}), lookup)
	if d == nil || d.Actor != answerer {
		t.Fatalf("legacy named answer: %+v", d)
	}
	// The answer lookup failed: the answerer may be anonymous, so not named.
	lookup.answerErr = errors.New("down")
	d, _ = qaAnswerCreatedDelivery(context.Background(), qaJSON(t, map[string]any{
		"answer_id": uuid.NewString(), "question_id": uuid.NewString(), "author_id": answerer.String(),
	}), lookup)
	if d == nil || d.Recipient != asker || d.Actor != uuid.Nil {
		t.Fatalf("legacy answer with a failed lookup: %+v", d)
	}
}

// H4: an anonymous asker who accepts an answer is not named.
func TestBestAnswerHidesAnAnonymousSelector(t *testing.T) {
	selector, answerer := uuid.New(), uuid.New()
	payload := func(anon any) json.RawMessage {
		m := map[string]any{"question_id": uuid.NewString(), "answer_id": uuid.NewString(),
			"selector_id": selector.String(), "answer_author_id": answerer.String()}
		if anon != nil {
			m["selector_anonymous"] = anon
		}
		return qaJSON(t, m)
	}
	d, _ := qaBestAnswerDelivery(context.Background(), payload(true), &fakeQALookup{})
	if d == nil || d.Recipient != answerer || d.Actor != uuid.Nil {
		t.Fatalf("anonymous selector: %+v", d)
	}
	d, _ = qaBestAnswerDelivery(context.Background(), payload(false), &fakeQALookup{})
	if d == nil || d.Actor != selector {
		t.Fatalf("named selector: %+v", d)
	}
	// Legacy: the question lookup failed, so the selector is not named.
	d, _ = qaBestAnswerDelivery(context.Background(), payload(nil), &fakeQALookup{err: errors.New("down")})
	if d == nil || d.Actor != uuid.Nil {
		t.Fatalf("legacy unknown selector: %+v", d)
	}
}

// M10: votes, comments and pins reach the author named in the payload even
// when the public lookup would fail (anonymous or private content).
func TestRecipientsComeFromThePayload(t *testing.T) {
	author, actor, target := uuid.New(), uuid.New(), uuid.New()
	failing := &fakeQALookup{err: errors.New("403")}
	ctx := context.Background()

	d, _ := qaVoteDelivery(ctx, qaJSON(t, map[string]any{"target_id": target.String(), "voter_id": actor.String(), "vote_type": "up", "target_author_id": author.String()}), failing, false)
	if d == nil || d.Recipient != author || d.Type != "qa.question.voted" {
		t.Fatalf("question vote: %+v", d)
	}
	d, _ = qaVoteDelivery(ctx, qaJSON(t, map[string]any{"target_id": target.String(), "voter_id": actor.String(), "vote_type": "up", "target_author_id": author.String()}), failing, true)
	if d == nil || d.Recipient != author || d.Type != "qa.answer.voted" || d.EntityType != "qa_answer" {
		t.Fatalf("answer vote: %+v", d)
	}
	if d, _ := qaVoteDelivery(ctx, qaJSON(t, map[string]any{"target_id": target.String(), "voter_id": actor.String(), "vote_type": "down", "target_author_id": author.String()}), failing, true); d != nil {
		t.Fatalf("a downvote notified: %+v", d)
	}
	d, _ = qaCommentCreatedDelivery(ctx, qaJSON(t, map[string]any{"comment_id": uuid.NewString(), "answer_id": target.String(), "author_id": actor.String(), "answer_author_id": author.String()}), failing)
	if d == nil || d.Recipient != author || d.Actor != actor {
		t.Fatalf("comment: %+v", d)
	}
	d, _ = qaQuestionPinnedDelivery(ctx, qaJSON(t, map[string]any{"question_id": target.String(), "actor_id": actor.String(), "pinned": true, "question_author_id": author.String()}), failing)
	if d == nil || d.Recipient != author {
		t.Fatalf("pin: %+v", d)
	}
	if failing.calls != 0 {
		t.Fatalf("%d lookups ran for events that carried the recipient", failing.calls)
	}
	// Legacy vote: no recipient in the payload and the lookup fails: skipped.
	if d, _ := qaVoteDelivery(ctx, qaJSON(t, map[string]any{"target_id": target.String(), "voter_id": actor.String(), "vote_type": "up"}), failing, false); d != nil {
		t.Fatalf("legacy vote without a recipient: %+v", d)
	}
}

func TestSelfActionsAreNotNotified(t *testing.T) {
	me := uuid.New()
	ctx := context.Background()
	if d, _ := qaVoteDelivery(ctx, qaJSON(t, map[string]any{"target_id": uuid.NewString(), "voter_id": me.String(), "vote_type": "up", "target_author_id": me.String()}), &fakeQALookup{}, false); d != nil {
		t.Fatal("self vote notified")
	}
	if d, _ := qaAnswerRequestedDelivery(qaJSON(t, map[string]any{"question_id": uuid.NewString(), "requester_id": me.String(), "requested_user_id": me.String()})); d != nil {
		t.Fatal("self request notified")
	}
}
