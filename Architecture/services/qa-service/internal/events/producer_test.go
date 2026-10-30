package events

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/atpost/qa-service/internal/qaevent"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

type captured struct {
	eventType, key string
	actor          *uuid.UUID
	payload        any
}

type fakeAuthors struct {
	question, answer         uuid.UUID
	questionAnon, answerAnon bool
	err                      error
}

func (f fakeAuthors) QuestionAuthor(context.Context, uuid.UUID) (uuid.UUID, bool, error) {
	return f.question, f.questionAnon, f.err
}

func (f fakeAuthors) AnswerAuthor(context.Context, uuid.UUID) (uuid.UUID, bool, error) {
	return f.answer, f.answerAnon, f.err
}

func capturingProducer(a AuthorLookup) (*Producer, *[]captured) {
	var got []captured
	return &Producer{
		authors: a,
		enqueue: func(_ context.Context, eventType, key string, actor *uuid.UUID, payload any) error {
			got = append(got, captured{eventType, key, actor, payload})
			return nil
		},
	}, &got
}

func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

// M10: vote events name the target's author, so notification-service needs
// no lookup (which fails for anonymous and private-community content).
func TestVoteEventsCarryTheRecipient(t *testing.T) {
	qAuthor, aAuthor, voter := uuid.New(), uuid.New(), uuid.New()
	p, got := capturingProducer(fakeAuthors{question: qAuthor, answer: aAuthor, questionAnon: true, answerAnon: true})
	q, a := uuid.New(), uuid.New()
	_ = p.PublishQuestionVoted(context.Background(), q, voter, "up")
	_ = p.PublishAnswerVoted(context.Background(), a, voter, "up")
	if len(*got) != 2 {
		t.Fatalf("got %d events", len(*got))
	}
	if m := asMap(t, (*got)[0].payload); m["target_author_id"] != qAuthor.String() || (*got)[0].eventType != events.EventQAQuestionVoted || (*got)[0].key != qaevent.QuestionKey(q) {
		t.Fatalf("question vote: %v key %s", m, (*got)[0].key)
	}
	if m := asMap(t, (*got)[1].payload); m["target_author_id"] != aAuthor.String() {
		t.Fatalf("answer vote: %v", m)
	}
}

// H4: when the question's author asked anonymously, accepting an answer
// does not name them, in the payload flag or the envelope actor.
func TestBestAnswerByAnonymousAskerHidesTheSelector(t *testing.T) {
	selector, answerer := uuid.New(), uuid.New()
	for _, tc := range []struct {
		name      string
		lookup    AuthorLookup
		wantAnon  bool
		wantActor bool
	}{
		{"anonymous asker", fakeAuthors{question: selector, questionAnon: true}, true, false},
		{"named asker", fakeAuthors{question: selector}, false, true},
		{"lookup failed", fakeAuthors{err: errors.New("db down")}, true, false},
		{"no lookup", nil, true, false},
	} {
		p, got := capturingProducer(tc.lookup)
		_ = p.PublishBestAnswerSelected(context.Background(), uuid.New(), uuid.New(), selector, answerer)
		ev := (*got)[0]
		m := asMap(t, ev.payload)
		if (m["selector_anonymous"] == true) != tc.wantAnon {
			t.Errorf("%s: selector_anonymous = %v", tc.name, m["selector_anonymous"])
		}
		if (ev.actor != nil) != tc.wantActor {
			t.Errorf("%s: envelope actor %v", tc.name, ev.actor)
		}
		if m["answer_author_id"] != answerer.String() {
			t.Errorf("%s: answer_author_id %v", tc.name, m["answer_author_id"])
		}
	}
}

func TestPinnedEventCarriesTheQuestionAuthor(t *testing.T) {
	author := uuid.New()
	p, got := capturingProducer(fakeAuthors{question: author})
	_ = p.PublishQuestionPinned(context.Background(), uuid.New(), uuid.New(), uuid.New(), true)
	if m := asMap(t, (*got)[0].payload); m["question_author_id"] != author.String() {
		t.Fatalf("pinned: %v", m)
	}
}

// The outbox insert error is reported (and never panics on a nil producer).
func TestPublishReportsOutboxFailure(t *testing.T) {
	p := &Producer{enqueue: func(context.Context, string, string, *uuid.UUID, any) error { return errors.New("insert failed") }}
	if err := p.PublishQuestionClosed(context.Background(), uuid.New(), uuid.New(), "r"); err == nil {
		t.Fatal("an outbox failure was swallowed")
	}
	var nilP *Producer
	if err := nilP.PublishQuestionClosed(context.Background(), uuid.New(), uuid.New(), "r"); err != nil {
		t.Fatal(err)
	}
}
