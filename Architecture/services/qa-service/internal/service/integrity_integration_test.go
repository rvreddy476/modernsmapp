// DB-backed proof of the Q&A access, privacy and integrity guards (backend
// audit 30 Oct 2026: H1, H2, H4, H5, M2, M4, M9, L4). The rules are unit
// tested in guards_test.go and store/rules_test.go; what only the database
// proves is here: the SQL list predicate, the single-transaction pay and
// reversal, the per-(voter, target) serialization and the unique answer
// request. TEST_PG_DSN on a "_test" database (qa_it_test), CI only.
package service

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/atpost/qa-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type integrityRig struct {
	svc  *Service
	st   *store.Store
	pool *pgxpool.Pool
	ctx  context.Context
	t    *testing.T
}

func setupIntegrityIT(t *testing.T) *integrityRig {
	t.Helper()
	svc, st, pool, _ := setupMediaClaimsIT(t)
	return &integrityRig{svc: svc, st: st, pool: pool, ctx: context.Background(), t: t}
}

type qOpt struct {
	community *uuid.UUID
	status    string
	anonymous bool
	token     string
}

// question seeds one question; its title carries token so a search finds
// exactly it.
func (r *integrityRig) question(author uuid.UUID, o qOpt) uuid.UUID {
	r.t.Helper()
	if o.status == "" {
		o.status = "open"
	}
	var id uuid.UUID
	if err := r.pool.QueryRow(r.ctx, `
		INSERT INTO questions (author_id, community_id, title, body, slug, status, is_anonymous)
		VALUES ($1, $2, $3, 'body', $4, $5, $6) RETURNING id`,
		author, o.community, "integrity "+o.token, "integrity-"+uuid.NewString(), o.status, o.anonymous).Scan(&id); err != nil {
		r.t.Fatalf("seed question: %v", err)
	}
	return id
}

func (r *integrityRig) answer(questionID, author uuid.UUID, anonymous bool) uuid.UUID {
	r.t.Helper()
	var id uuid.UUID
	if err := r.pool.QueryRow(r.ctx, `
		INSERT INTO answers (question_id, author_id, body, is_anonymous) VALUES ($1, $2, 'answer', $3) RETURNING id`,
		questionID, author, anonymous).Scan(&id); err != nil {
		r.t.Fatalf("seed answer: %v", err)
	}
	if _, err := r.pool.Exec(r.ctx, `UPDATE questions SET answer_count = answer_count + 1 WHERE id = $1`, questionID); err != nil {
		r.t.Fatal(err)
	}
	return id
}

func (r *integrityRig) community(kind string) uuid.UUID {
	r.t.Helper()
	id := uuid.New()
	if _, err := r.pool.Exec(r.ctx, `INSERT INTO qa_communities (id, owner_id, name, community_type) VALUES ($1, $2, $3, $4)`,
		id, uuid.New(), "it-"+kind+"-"+id.String()[:8], kind); err != nil {
		r.t.Fatalf("seed community: %v", err)
	}
	return id
}

func (r *integrityRig) member(community, user uuid.UUID, role string) {
	r.t.Helper()
	if err := r.st.UpsertCommunityMember(r.ctx, community, user, role); err != nil {
		r.t.Fatal(err)
	}
}

func (r *integrityRig) intOf(sql string, args ...any) int {
	r.t.Helper()
	var n int
	if err := r.pool.QueryRow(r.ctx, sql, args...).Scan(&n); err != nil {
		r.t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func (r *integrityRig) rep(user uuid.UUID) int {
	return r.intOf(`SELECT COALESCE((SELECT reputation_score FROM qa_profiles WHERE user_id = $1), 0)`, user)
}

func wantRefusal(t *testing.T, err error, prefix string) {
	t.Helper()
	if err == nil || !strings.HasPrefix(err.Error(), prefix) {
		t.Fatalf("got %v, want a %q refusal", err, prefix)
	}
}

func hasQuestion(list []store.QuestionSummary, id uuid.UUID) bool {
	for _, q := range list {
		if q.ID == id {
			return true
		}
	}
	return false
}

// H1 + M2: a vote pays once, a repeat pays nothing, a removal or a flip
// reverses, concurrent first votes count once, self-votes are refused.
func TestIntegrityIT_VotesPayOnlyOnChange(t *testing.T) {
	r := setupIntegrityIT(t)
	asker, answerer, voter := uuid.New(), uuid.New(), uuid.New()
	q := r.question(asker, qOpt{})
	a := r.answer(q, answerer, false)

	for i := 0; i < 5; i++ {
		if err := r.svc.VoteAnswer(r.ctx, voter, a, "up"); err != nil {
			t.Fatal(err)
		}
	}
	if got := r.rep(answerer); got != store.RepAnswerUpvoted {
		t.Fatalf("5 identical up votes paid %d, want %d", got, store.RepAnswerUpvoted)
	}
	if got := r.intOf(`SELECT upvote_count FROM answers WHERE id = $1`, a); got != 1 {
		t.Fatalf("upvote_count = %d, want 1", got)
	}
	if err := r.svc.VoteAnswer(r.ctx, voter, a, "down"); err != nil {
		t.Fatal(err)
	}
	if got := r.rep(answerer); got != store.RepAnswerDownvoted {
		t.Fatalf("after a flip to down: %d, want %d", got, store.RepAnswerDownvoted)
	}
	if err := r.svc.RemoveAnswerVote(r.ctx, voter, a); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.RemoveAnswerVote(r.ctx, voter, a); err != nil {
		t.Fatal(err)
	}
	if got := r.rep(answerer); got != 0 {
		t.Fatalf("after removing the vote (twice): %d, want 0", got)
	}
	if got := r.intOf(`SELECT vote_score + upvote_count + downvote_count FROM answers WHERE id = $1`, a); got != 0 {
		t.Fatalf("counters after removal = %d, want 0", got)
	}

	// Questions too.
	for i := 0; i < 3; i++ {
		if err := r.svc.VoteQuestion(r.ctx, voter, q, "up"); err != nil {
			t.Fatal(err)
		}
	}
	if got := r.rep(asker); got != store.RepQuestionUpvoted {
		t.Fatalf("3 identical question up votes paid %d, want %d", got, store.RepQuestionUpvoted)
	}
	if err := r.svc.RemoveQuestionVote(r.ctx, voter, q); err != nil {
		t.Fatal(err)
	}
	if got := r.rep(asker); got != 0 {
		t.Fatalf("question vote removed: rep %d, want 0", got)
	}

	// M2: concurrent first votes by one voter count once.
	b := r.answer(q, answerer, false)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.svc.VoteAnswer(r.ctx, voter, b, "up"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got := r.intOf(`SELECT upvote_count FROM answers WHERE id = $1`, b); got != 1 {
		t.Fatalf("8 concurrent first votes: upvote_count = %d, want 1", got)
	}
	if got := r.intOf(`SELECT vote_score FROM answers WHERE id = $1`, b); got != 1 {
		t.Fatalf("8 concurrent first votes: vote_score = %d, want 1", got)
	}
	if got := r.rep(answerer); got != store.RepAnswerUpvoted {
		t.Fatalf("8 concurrent first votes paid %d, want %d", got, store.RepAnswerUpvoted)
	}

	// Self votes: question, answer and comment (L4).
	wantRefusal(t, r.svc.VoteAnswer(r.ctx, answerer, b, "up"), "forbidden: ")
	wantRefusal(t, r.svc.VoteQuestion(r.ctx, asker, q, "up"), "forbidden: ")
	c, err := r.svc.CreateComment(r.ctx, b, voter, "a comment")
	if err != nil {
		t.Fatal(err)
	}
	wantRefusal(t, r.svc.VoteComment(r.ctx, voter, c.ID, "up"), "forbidden: ")
	if err := r.svc.VoteComment(r.ctx, asker, c.ID, "up"); err != nil {
		t.Fatal(err)
	}
	if got := r.intOf(`SELECT vote_score FROM answer_comments WHERE id = $1`, c.ID); got != 1 {
		t.Fatalf("comment vote_score = %d, want 1", got)
	}
}

// H1 + M9: accepting an answer.
func TestIntegrityIT_BestAnswer(t *testing.T) {
	r := setupIntegrityIT(t)
	asker, answerer, other, stranger := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	q := r.question(asker, qOpt{})
	a := r.answer(q, answerer, false)
	b := r.answer(q, other, false)
	own := r.answer(q, asker, false)
	foreignQ := r.question(uuid.New(), qOpt{})
	foreign := r.answer(foreignQ, stranger, false)

	wantRefusal(t, r.svc.SelectBestAnswer(r.ctx, q, a, stranger), "forbidden: ")
	wantRefusal(t, r.svc.SelectBestAnswer(r.ctx, q, foreign, asker), "invalid: ")
	if got := r.intOf(`SELECT count(*) FROM answers WHERE id = $1 AND is_best`, foreign); got != 0 {
		t.Fatal("a foreign answer was marked best")
	}

	for i := 0; i < 3; i++ {
		if err := r.svc.SelectBestAnswer(r.ctx, q, a, asker); err != nil {
			t.Fatal(err)
		}
	}
	if got := r.rep(answerer); got != store.RepBestAnswerSelected {
		t.Fatalf("3 accepts paid the answerer %d, want %d", got, store.RepBestAnswerSelected)
	}
	if got := r.rep(asker); got != store.RepBestAnswerSelectorBonus {
		t.Fatalf("3 accepts paid the asker %d, want %d", got, store.RepBestAnswerSelectorBonus)
	}
	if got := r.intOf(`SELECT best_answer_count FROM qa_profiles WHERE user_id = $1`, answerer); got != 1 {
		t.Fatalf("best_answer_count = %d, want 1", got)
	}

	// Accepting another moves the award: one accepted answer.
	if err := r.svc.SelectBestAnswer(r.ctx, q, b, asker); err != nil {
		t.Fatal(err)
	}
	if r.rep(answerer) != 0 || r.rep(other) != store.RepBestAnswerSelected || r.rep(asker) != store.RepBestAnswerSelectorBonus {
		t.Fatalf("after moving the accept: answerer %d other %d asker %d", r.rep(answerer), r.rep(other), r.rep(asker))
	}
	if got := r.intOf(`SELECT count(*) FROM answers WHERE question_id = $1 AND is_best`, q); got != 1 {
		t.Fatalf("%d accepted answers, want 1", got)
	}

	// Self accept pays nothing (and reverses the previous accept).
	if err := r.svc.SelectBestAnswer(r.ctx, q, own, asker); err != nil {
		t.Fatal(err)
	}
	if r.rep(asker) != 0 || r.rep(other) != 0 {
		t.Fatalf("self accept: asker %d other %d, want 0 and 0", r.rep(asker), r.rep(other))
	}

	// Unaccept reverses; a repeat is a no-op.
	if err := r.svc.SelectBestAnswer(r.ctx, q, a, asker); err != nil {
		t.Fatal(err)
	}
	wantRefusal(t, r.svc.UnselectBestAnswer(r.ctx, q, stranger), "forbidden: ")
	for i := 0; i < 2; i++ {
		if err := r.svc.UnselectBestAnswer(r.ctx, q, asker); err != nil {
			t.Fatal(err)
		}
	}
	if r.rep(answerer) != 0 || r.rep(asker) != 0 {
		t.Fatalf("after unaccept: answerer %d asker %d, want 0", r.rep(answerer), r.rep(asker))
	}
	if got := r.intOf(`SELECT (best_answer_id IS NULL AND NOT is_answered)::int FROM questions WHERE id = $1`, q); got != 1 {
		t.Fatal("the question still has an accepted answer")
	}

	// M9: deleting the accepted answer clears it and reverses; a second
	// delete, or two at once, decrements answer_count once.
	if err := r.svc.SelectBestAnswer(r.ctx, q, a, asker); err != nil {
		t.Fatal(err)
	}
	before := r.intOf(`SELECT answer_count FROM questions WHERE id = $1`, q)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.svc.DeleteAnswer(r.ctx, a, answerer); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got := r.intOf(`SELECT answer_count FROM questions WHERE id = $1`, q); got != before-1 {
		t.Fatalf("answer_count after 3 deletes = %d, want %d", got, before-1)
	}
	if got := r.intOf(`SELECT (best_answer_id IS NULL AND NOT is_answered)::int FROM questions WHERE id = $1`, q); got != 1 {
		t.Fatal("deleting the accepted answer left the question answered")
	}
	if r.rep(answerer) != 0 || r.rep(asker) != 0 {
		t.Fatalf("after deleting the accepted answer: answerer %d asker %d, want 0", r.rep(answerer), r.rep(asker))
	}
	wantRefusal(t, r.svc.DeleteAnswer(r.ctx, b, stranger), "forbidden: ")

	// Hiding the accepted answer (admin console) does the same.
	if err := r.svc.SelectBestAnswer(r.ctx, q, b, asker); err != nil {
		t.Fatal(err)
	}
	if _, err := r.st.AdminHideContent(r.ctx, uuid.New(), "answer", b, nil, "spam"); err != nil {
		t.Fatal(err)
	}
	if got := r.intOf(`SELECT (best_answer_id IS NULL AND NOT is_answered)::int FROM questions WHERE id = $1`, q); got != 1 {
		t.Fatal("hiding the accepted answer left the question answered")
	}
	if r.rep(other) != 0 {
		t.Fatalf("hidden accepted answer kept %d reputation", r.rep(other))
	}
}

// H2: close, reopen and the approval queue.
func TestIntegrityIT_CloseReopenApprove(t *testing.T) {
	r := setupIntegrityIT(t)
	author, stranger, mod := uuid.New(), uuid.New(), uuid.New()

	q := r.question(author, qOpt{})
	wantRefusal(t, r.svc.CloseQuestion(r.ctx, q, stranger, "troll"), "forbidden: ")
	if err := r.svc.CloseQuestion(r.ctx, q, author, "done"); err != nil {
		t.Fatal(err)
	}
	wantRefusal(t, r.svc.CloseQuestion(r.ctx, q, author, "again"), "conflict: ")
	wantRefusal(t, r.svc.ReopenQuestion(r.ctx, q, stranger), "forbidden: ")
	if err := r.svc.ReopenQuestion(r.ctx, q, author); err != nil {
		t.Fatalf("author reopening their own close: %v", err)
	}

	// A platform moderator's lock holds against the author.
	if _, err := r.st.AdminLockQuestion(r.ctx, uuid.New(), q, nil, "locked"); err != nil {
		t.Fatal(err)
	}
	wantRefusal(t, r.svc.ReopenQuestion(r.ctx, q, author), "conflict: ")
	if got := r.intOf(`SELECT (status = 'closed')::int FROM questions WHERE id = $1`, q); got != 1 {
		t.Fatal("the author reopened a moderator lock")
	}

	// A merge and a pending question never reopen.
	merged := r.question(author, qOpt{})
	into := r.question(uuid.New(), qOpt{})
	if _, err := r.st.AdminMergeQuestion(r.ctx, uuid.New(), merged, into, nil, "dup"); err != nil {
		t.Fatal(err)
	}
	wantRefusal(t, r.svc.ReopenQuestion(r.ctx, merged, author), "conflict: ")

	c := r.community("public")
	r.member(c, mod, "moderator")
	r.member(c, author, "member")
	pending := r.question(author, qOpt{community: &c, status: "pending_approval"})
	wantRefusal(t, r.svc.ReopenQuestion(r.ctx, pending, author), "conflict: ")
	if got := r.intOf(`SELECT (status = 'pending_approval')::int FROM questions WHERE id = $1`, pending); got != 1 {
		t.Fatal("a pending question left the queue by reopen")
	}

	// A community moderator's close holds against the author; the moderator
	// reopens.
	cq := r.question(author, qOpt{community: &c})
	if err := r.svc.CloseQuestion(r.ctx, cq, mod, "off topic"); err != nil {
		t.Fatal(err)
	}
	wantRefusal(t, r.svc.ReopenQuestion(r.ctx, cq, author), "conflict: ")
	if err := r.svc.ReopenQuestion(r.ctx, cq, mod); err != nil {
		t.Fatal(err)
	}

	// The approval queue: moderators only; approve opens, reject deletes.
	wantRefusal(t, r.svc.DecidePendingQuestion(r.ctx, c, pending, author, true, ""), "forbidden: ")
	wantRefusal(t, r.svc.DecidePendingQuestion(r.ctx, c, pending, stranger, true, ""), "forbidden: ")
	if err := r.svc.DecidePendingQuestion(r.ctx, c, pending, mod, true, "fine"); err != nil {
		t.Fatal(err)
	}
	if got := r.intOf(`SELECT (status = 'open')::int FROM questions WHERE id = $1`, pending); got != 1 {
		t.Fatal("approve did not open the question")
	}
	wantRefusal(t, r.svc.DecidePendingQuestion(r.ctx, c, pending, mod, true, ""), "not_found: ")
	rej := r.question(author, qOpt{community: &c, status: "pending_approval"})
	other := r.community("public")
	r.member(other, mod, "moderator")
	wantRefusal(t, r.svc.DecidePendingQuestion(r.ctx, other, rej, mod, false, ""), "not_found: ")
	if err := r.svc.DecidePendingQuestion(r.ctx, c, rej, mod, false, "spam"); err != nil {
		t.Fatal(err)
	}
	if got := r.intOf(`SELECT (deleted_at IS NOT NULL)::int FROM questions WHERE id = $1`, rej); got != 1 {
		t.Fatal("reject did not remove the question")
	}
	if got := r.intOf(`SELECT count(*) FROM moderation_actions WHERE target_id = $1 AND actor_id = $2`, rej, mod); got != 1 {
		t.Fatalf("reject recorded %d actions, want 1", got)
	}
}

// H4: anonymous posts stay off other viewers' profile lists and reputation
// history.
func TestIntegrityIT_AnonymousStaysOffProfiles(t *testing.T) {
	r := setupIntegrityIT(t)
	user, viewer, asker := uuid.New(), uuid.New(), uuid.New()
	anonQ := r.question(user, qOpt{anonymous: true})
	openQ := r.question(user, qOpt{})
	host := r.question(asker, qOpt{})
	anonA := r.answer(host, user, true)
	openA := r.answer(host, user, false)
	r.svc.awardReputation(r.ctx, user, "question_asked", ReputationQuestionAsked, "question", &anonQ)
	r.svc.awardReputation(r.ctx, user, "question_asked", ReputationQuestionAsked, "question", &openQ)
	if err := r.svc.VoteAnswer(r.ctx, viewer, anonA, "up"); err != nil {
		t.Fatal(err)
	}

	for name, v := range map[string]*uuid.UUID{"another user": &viewer, "signed out": nil} {
		qs, err := r.svc.ListQuestionsByAuthor(r.ctx, user, v, 100, 0)
		if err != nil {
			t.Fatal(err)
		}
		if hasQuestion(qs, anonQ) || !hasQuestion(qs, openQ) {
			t.Fatalf("%s: questions = %+v, want the open one only", name, qs)
		}
		as, err := r.svc.ListAnswersByAuthor(r.ctx, user, v, 100, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(as) != 1 || as[0].ID != openA {
			t.Fatalf("%s: answers = %+v, want the open one only", name, as)
		}
		hist, err := r.svc.GetReputationHistory(r.ctx, user, v, 100, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range hist {
			if e.SourceID != nil && (*e.SourceID == anonQ || *e.SourceID == anonA) {
				t.Fatalf("%s: reputation history shows an anonymous source: %+v", name, e)
			}
		}
		if len(hist) != 1 {
			t.Fatalf("%s: %d history rows, want 1 (the open question)", name, len(hist))
		}
	}
	qs, _ := r.svc.ListQuestionsByAuthor(r.ctx, user, &user, 100, 0)
	if !hasQuestion(qs, anonQ) {
		t.Fatal("the author does not see their own anonymous question")
	}
	as, _ := r.svc.ListAnswersByAuthor(r.ctx, user, &user, 100, 0)
	if len(as) != 2 {
		t.Fatalf("the author sees %d of their answers, want 2", len(as))
	}
	hist, _ := r.svc.GetReputationHistory(r.ctx, user, &user, 100, 0)
	if len(hist) != 3 {
		t.Fatalf("the owner sees %d history rows, want 3", len(hist))
	}
}

// H5: one visibility rule for every list and every action.
func TestIntegrityIT_PrivateAndPendingNeverLeak(t *testing.T) {
	r := setupIntegrityIT(t)
	member, outsider, author, mod := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	priv := r.community("private")
	r.member(priv, member, "member")
	r.member(priv, author, "member")
	pub := r.community("public")
	r.member(pub, author, "member")
	r.member(pub, mod, "moderator")
	token := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]

	privQ := r.question(author, qOpt{community: &priv, token: token})
	pendQ := r.question(author, qOpt{community: &pub, status: "pending_approval", token: token})
	openQ := r.question(author, qOpt{token: token})
	topicID := uuid.New()
	if _, err := r.pool.Exec(r.ctx, `INSERT INTO topics (id, name, slug) VALUES ($1, $2, $2)`, topicID, "it-vis-"+token); err != nil {
		t.Fatal(err)
	}
	for _, q := range []uuid.UUID{privQ, pendQ, openQ} {
		if _, err := r.pool.Exec(r.ctx, `INSERT INTO question_topics (question_id, topic_id) VALUES ($1, $2)`, q, topicID); err != nil {
			t.Fatal(err)
		}
	}
	privA := r.answer(privQ, member, false)

	type lister func(v *uuid.UUID) ([]store.QuestionSummary, error)
	lists := map[string]lister{
		"search": func(v *uuid.UUID) ([]store.QuestionSummary, error) {
			return r.svc.SearchQuestions(r.ctx, token, nil, nil, v, 100, 0)
		},
		"similar": func(v *uuid.UUID) ([]store.QuestionSummary, error) {
			return r.svc.FindSimilarQuestions(r.ctx, token, v, 20)
		},
		"trending": func(v *uuid.UUID) ([]store.QuestionSummary, error) {
			return r.svc.GetTrendingQuestions(r.ctx, v, 100, 0)
		},
		"unanswered": func(v *uuid.UUID) ([]store.QuestionSummary, error) {
			return r.svc.GetUnansweredQuestions(r.ctx, &topicID, v, 100, 0)
		},
		"topic": func(v *uuid.UUID) ([]store.QuestionSummary, error) {
			return r.svc.GetTopicQuestions(r.ctx, topicID, v, "newest", 100, 0)
		},
		"list pending": func(v *uuid.UUID) ([]store.QuestionSummary, error) {
			return r.svc.ListQuestions(r.ctx, v, "", nil, "", "recent", "pending_approval", 100, 0)
		},
		"profile": func(v *uuid.UUID) ([]store.QuestionSummary, error) {
			return r.svc.ListQuestionsByAuthor(r.ctx, author, v, 100, 0)
		},
	}
	for name, list := range lists {
		for vname, v := range map[string]*uuid.UUID{"outsider": &outsider, "signed out": nil} {
			got, err := list(v)
			if err != nil {
				t.Fatalf("%s as %s: %v", name, vname, err)
			}
			if hasQuestion(got, privQ) || hasQuestion(got, pendQ) {
				t.Fatalf("%s as %s leaks a private or pending question: %+v", name, vname, got)
			}
		}
	}
	// The open question is there for everyone (search, similar, topic).
	for _, name := range []string{"search", "similar", "topic"} {
		got, _ := lists[name](&outsider)
		if !hasQuestion(got, openQ) {
			t.Fatalf("%s hides an open question", name)
		}
	}
	// A member sees the private one; the author and a moderator see the
	// pending one.
	if got, _ := lists["search"](&member); !hasQuestion(got, privQ) {
		t.Fatal("search hides a private question from a member")
	}
	if got, _ := lists["list pending"](&mod); !hasQuestion(got, pendQ) {
		t.Fatal("a moderator does not see the approval queue")
	}
	if got, _ := lists["list pending"](&author); !hasQuestion(got, pendQ) {
		t.Fatal("the author does not see their pending question")
	}

	// A community filter on search is refused to a non-member.
	_, err := r.svc.SearchQuestions(r.ctx, token, &priv, nil, &outsider, 10, 0)
	wantRefusal(t, err, "forbidden: ")
	_, err = r.svc.SearchQuestions(r.ctx, token, &priv, nil, nil, 10, 0)
	wantRefusal(t, err, "forbidden: ")

	// By id and every action.
	_, err = r.svc.GetQuestion(r.ctx, pendQ, &outsider)
	wantRefusal(t, err, "not_found: ")
	if _, err := r.svc.GetQuestion(r.ctx, pendQ, &mod); err != nil {
		t.Fatalf("a moderator reading a pending question: %v", err)
	}
	_, err = r.svc.ListComments(r.ctx, privA, &outsider, 10, 0)
	wantRefusal(t, err, "forbidden: ")
	_, err = r.svc.CreateComment(r.ctx, privA, outsider, "hi")
	wantRefusal(t, err, "forbidden: ")
	wantRefusal(t, r.svc.VoteAnswer(r.ctx, outsider, privA, "up"), "forbidden: ")
	wantRefusal(t, r.svc.VoteQuestion(r.ctx, outsider, privQ, "up"), "forbidden: ")
	wantRefusal(t, r.svc.FollowQuestion(r.ctx, outsider, privQ), "forbidden: ")
	wantRefusal(t, r.svc.SaveQuestion(r.ctx, outsider, privQ), "forbidden: ")
	wantRefusal(t, r.svc.SaveAnswer(r.ctx, outsider, privA), "forbidden: ")
	_, err = r.svc.CreateAnswerRequest(r.ctx, privQ, outsider, member)
	wantRefusal(t, err, "forbidden: ")
	wantRefusal(t, r.svc.VoteQuestion(r.ctx, outsider, pendQ, "up"), "not_found: ")

	// A save made while a member stops reading once membership ends.
	if err := r.svc.SaveQuestion(r.ctx, member, privQ); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.SaveAnswer(r.ctx, member, privA); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.svc.GetSavedQuestions(r.ctx, member, 100, 0); !hasQuestion(got, privQ) {
		t.Fatal("a member's save is missing")
	}
	if err := r.st.RemoveCommunityMember(r.ctx, priv, member); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.svc.GetSavedQuestions(r.ctx, member, 100, 0); hasQuestion(got, privQ) {
		t.Fatal("a former member still reads a saved private question")
	}
	if got, _ := r.svc.GetSavedAnswers(r.ctx, member, 100, 0); len(got) != 0 {
		t.Fatalf("a former member still reads %d saved private answers", len(got))
	}

	// A hidden answer takes no comments and shows none.
	if _, err := r.st.AdminHideContent(r.ctx, uuid.New(), "answer", privA, nil, "spam"); err != nil {
		t.Fatal(err)
	}
	r.member(priv, member, "member")
	_, err = r.svc.CreateComment(r.ctx, privA, member, "hi")
	wantRefusal(t, err, "not_found: ")
	_, err = r.svc.ListComments(r.ctx, privA, &member, 10, 0)
	wantRefusal(t, err, "not_found: ")
}

// M4: answer requests are unique per (question, requester, target), never
// to oneself, and refused when the target cannot see the question; posting
// is rate limited per user.
func TestIntegrityIT_AnswerRequestsAndLimits(t *testing.T) {
	r := setupIntegrityIT(t)
	author, requester, target := uuid.New(), uuid.New(), uuid.New()
	q := r.question(author, qOpt{})
	if _, err := r.svc.CreateAnswerRequest(r.ctx, q, requester, target); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		_, err := r.svc.CreateAnswerRequest(r.ctx, q, requester, target)
		wantRefusal(t, err, "conflict: ")
	}
	if got := r.intOf(`SELECT count(*) FROM answer_requests WHERE question_id = $1`, q); got != 1 {
		t.Fatalf("%d answer requests stored, want 1", got)
	}
	_, err := r.svc.CreateAnswerRequest(r.ctx, q, requester, requester)
	wantRefusal(t, err, "invalid: ")

	priv := r.community("private")
	r.member(priv, author, "member")
	r.member(priv, requester, "member")
	pq := r.question(author, qOpt{community: &priv})
	_, err = r.svc.CreateAnswerRequest(r.ctx, pq, requester, target)
	wantRefusal(t, err, "forbidden: ")

	wantRefusal(t, r.svc.RespondToAnswerRequest(r.ctx, uuid.New(), target, "maybe"), "invalid: ")

	// Per-user limits on a real write path.
	lim := &countingLimiter{hits: map[string]int{}}
	r.svc.WithRateLimiter(lim)
	a := r.answer(q, uuid.New(), false)
	limit := commentRates[0].limit
	for i := 0; i < limit; i++ {
		if _, err := r.svc.CreateComment(r.ctx, a, requester, "c"); err != nil {
			t.Fatal(err)
		}
	}
	_, err = r.svc.CreateComment(r.ctx, a, requester, "one too many")
	wantRefusal(t, err, "rate_limited: ")
	if got := r.intOf(`SELECT count(*) FROM answer_comments WHERE answer_id = $1`, a); got != limit {
		t.Fatalf("%d comments stored, want %d", got, limit)
	}
	_, err = r.svc.CreateComment(r.ctx, a, requester, strings.Repeat("x", maxCommentLen+1))
	wantRefusal(t, err, "invalid: ")
}
