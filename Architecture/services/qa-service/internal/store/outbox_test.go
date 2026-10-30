package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// recordingTx captures the outbox insert; every other pgx.Tx method is nil.
type recordingTx struct {
	pgx.Tx
	sql  string
	args []any
}

func (r *recordingTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	r.sql, r.args = sql, args
	return pgconn.CommandTag{}, nil
}

func (r *recordingTx) envelope(t *testing.T) (events.EventEnvelope, map[string]any) {
	t.Helper()
	if !strings.Contains(r.sql, "INSERT INTO qa.outbox_events") {
		t.Fatalf("not an outbox insert: %s", r.sql)
	}
	var env events.EventEnvelope
	if err := json.Unmarshal(r.args[2].([]byte), &env); err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		t.Fatal(err)
	}
	return env, p
}

func TestQuestionFansOut(t *testing.T) {
	cases := []struct {
		anon          bool
		status, ctype string
		want          bool
	}{
		{false, "open", "", true},
		{false, "open", "public", true},
		{true, "open", "", false},
		{false, "pending_approval", "", false},
		{false, "open", "private", false},
		{false, "open", "invite", false},
	}
	for _, c := range cases {
		if got := questionFansOut(c.anon, c.status, c.ctype); got != c.want {
			t.Errorf("questionFansOut(%v, %q, %q) = %v", c.anon, c.status, c.ctype, got)
		}
	}
}

// H4: an anonymous (or private, or unapproved) question is announced without
// its author, so feed-service cannot fan it out under the author's name.
func TestQuestionCreatedEventHidesTheAuthorWhenItMustNotFanOut(t *testing.T) {
	author := uuid.New()
	for _, tc := range []struct {
		name          string
		anon          bool
		status, ctype string
		wantAuthor    bool
	}{
		{"public", false, "open", "", true},
		{"anonymous", true, "open", "", false},
		{"private community", false, "open", "private", false},
		{"awaiting approval", false, "pending_approval", "public", false},
	} {
		tx := &recordingTx{}
		if err := enqueueQuestionCreated(context.Background(), tx, uuid.New(), author, nil, "t", tc.status, tc.ctype, tc.anon); err != nil {
			t.Fatal(err)
		}
		env, p := tx.envelope(t)
		hasAuthor := p["author_id"] == author.String()
		if hasAuthor != tc.wantAuthor {
			t.Errorf("%s: author_id = %v", tc.name, p["author_id"])
		}
		if (env.ActorUserID != nil) != tc.wantAuthor {
			t.Errorf("%s: envelope actor = %v", tc.name, env.ActorUserID)
		}
		if env.EventType != events.EventQAQuestionCreated {
			t.Errorf("%s: type %s", tc.name, env.EventType)
		}
	}
}

// H4/M10: answer.created names the recipient and flags an anonymous
// answerer; the envelope never names them.
func TestAnswerCreatedEventFlagsAnonymity(t *testing.T) {
	qAuthor := uuid.New()
	a := &Answer{ID: uuid.New(), QuestionID: uuid.New(), AuthorID: uuid.New(), IsAnonymous: true, CreatedAt: time.Now()}
	tx := &recordingTx{}
	if err := enqueueAnswerCreated(context.Background(), tx, a, qAuthor); err != nil {
		t.Fatal(err)
	}
	env, p := tx.envelope(t)
	if p["is_anonymous"] != true || p["question_author_id"] != qAuthor.String() {
		t.Fatalf("payload %v", p)
	}
	if env.ActorUserID != nil {
		t.Fatalf("an anonymous answer named its author as the envelope actor: %v", *env.ActorUserID)
	}
	if tx.args[1] != "question:"+a.QuestionID.String() {
		t.Fatalf("partition key %v", tx.args[1])
	}

	a.IsAnonymous = false
	tx = &recordingTx{}
	_ = enqueueAnswerCreated(context.Background(), tx, a, qAuthor)
	if env, _ := tx.envelope(t); env.ActorUserID == nil || *env.ActorUserID != a.AuthorID.String() {
		t.Fatalf("a named answer lost its actor")
	}
}

func TestCommentCreatedEventNamesTheAnswerAuthor(t *testing.T) {
	answerAuthor := uuid.New()
	c := &AnswerComment{ID: uuid.New(), AnswerID: uuid.New(), AuthorID: uuid.New(), CreatedAt: time.Now()}
	tx := &recordingTx{}
	if err := enqueueCommentCreated(context.Background(), tx, c, uuid.New(), answerAuthor); err != nil {
		t.Fatal(err)
	}
	if _, p := tx.envelope(t); p["answer_author_id"] != answerAuthor.String() {
		t.Fatalf("payload %v", p)
	}
}

func TestSlugIDRange(t *testing.T) {
	id := uuid.MustParse("0123abcd-ef45-6789-0123-456789abcdef")
	lo, hi, ok := slugIDRange(generateSlug("How do I do this?", id))
	if !ok {
		t.Fatal("a generated slug has no id range")
	}
	if lo.String() != "0123abcd-ef45-0000-0000-000000000000" || hi.String() != "0123abcd-ef45-ffff-ffff-ffffffffffff" {
		t.Fatalf("range %s..%s", lo, hi)
	}
	for _, bad := range []string{"", "no-suffix", "title-0123ABCDEF45", "title-0123abcdef4", "title-0123abcdef4g"} {
		if _, _, ok := slugIDRange(bad); ok {
			t.Errorf("slugIDRange(%q) accepted", bad)
		}
	}
}
