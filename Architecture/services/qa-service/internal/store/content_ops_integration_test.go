// DB-backed proof of the Q&A content and operations fixes (backend audit
// 30 Oct 2026: H3, M5, M6, M7, M8, L2, L3). The rules are unit tested next
// to the code (outbox_test.go, search_match_test.go,
// postgres/bootstrap_guard_test.go); what only the database proves is here:
// the boot script on a live schema, the online indexes and their use, the
// outbox rows committed with their writes, the edit history and stable
// slug, the off-path community counts, and the single-transaction erasure.
// TEST_PG_DSN on a "_test" database (qa_it_test), CI only.
package store

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/qa-service/database"
	pgstore "github.com/atpost/qa-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type contentRig struct {
	t     *testing.T
	ctx   context.Context
	pool  *pgxpool.Pool
	st    *Store
	topic string
}

func setupContentIT(t *testing.T) *contentRig {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping qa-service content integration tests")
	}
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil || !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatal("TEST_PG_DSN must parse and name a database ending in _test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pgstore.BootstrapSchema(ctx, pool, database.SetupSQL); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	slug := "content-" + uuid.NewString()[:12]
	if _, err := pool.Exec(ctx, `INSERT INTO topics (name, slug) VALUES ($1, $1)`, slug); err != nil {
		t.Fatalf("seed topic: %v", err)
	}
	return &contentRig{t: t, ctx: ctx, pool: pool, st: New(pool), topic: slug}
}

func (r *contentRig) question(author uuid.UUID, title, body string, p CreateQuestionParams) *Question {
	r.t.Helper()
	p.Title, p.Body, p.Topics = title, body, []string{r.topic}
	q, err := r.st.CreateQuestion(r.ctx, author, p)
	if err != nil {
		r.t.Fatalf("create question: %v", err)
	}
	return q
}

func (r *contentRig) community(ctype string) uuid.UUID {
	r.t.Helper()
	id := uuid.New()
	if err := r.st.UpsertCommunity(r.ctx, MirroredCommunity{ID: id, OwnerID: uuid.New(), Name: "c-" + id.String()[:8], CommunityType: ctype, Status: "active"}); err != nil {
		r.t.Fatalf("seed community: %v", err)
	}
	return id
}

// outboxEvents returns the payloads of eventType rows keyed to aggregate.
func (r *contentRig) outboxEvents(eventType, partitionKey string) []events.EventEnvelope {
	r.t.Helper()
	rows, err := r.pool.Query(r.ctx, `SELECT payload FROM qa.outbox_events WHERE event_type = $1 AND partition_key = $2 ORDER BY id`, eventType, partitionKey)
	if err != nil {
		r.t.Fatalf("read outbox: %v", err)
	}
	defer rows.Close()
	var out []events.EventEnvelope
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			r.t.Fatal(err)
		}
		var env events.EventEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			r.t.Fatal(err)
		}
		out = append(out, env)
	}
	return out
}

func payloadOf(t *testing.T, env events.EventEnvelope) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(env.Payload, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// M5: a boot whose script is unchanged applies nothing; the status CHECK is
// guarded, tagged and validated; every migration ran.
func TestContentIT_BootIsFingerprintedAndGuarded(t *testing.T) {
	r := setupContentIT(t)
	var appliedAt time.Time
	if err := r.pool.QueryRow(r.ctx, `SELECT applied_at FROM qa_schema_bootstrap WHERE id = 1`).Scan(&appliedAt); err != nil {
		t.Fatalf("no fingerprint recorded: %v", err)
	}
	if err := pgstore.BootstrapSchema(r.ctx, r.pool, database.SetupSQL); err != nil {
		t.Fatal(err)
	}
	var again time.Time
	_ = r.pool.QueryRow(r.ctx, `SELECT applied_at FROM qa_schema_bootstrap WHERE id = 1`).Scan(&again)
	if !again.Equal(appliedAt) {
		t.Fatal("an unchanged script was applied again")
	}

	// Re-apply (as a changed script would): still succeeds on a live schema,
	// and the CHECK ends up validated and tagged.
	if err := pgstore.ForgetSchemaFingerprint(r.ctx, r.pool); err != nil {
		t.Fatal(err)
	}
	if err := pgstore.BootstrapSchema(r.ctx, r.pool, database.SetupSQL); err != nil {
		t.Fatalf("re-apply on a live schema: %v", err)
	}
	var validated bool
	var tag string
	if err := r.pool.QueryRow(r.ctx, `
		SELECT convalidated, COALESCE(obj_description(oid, 'pg_constraint'), '')
		FROM pg_constraint WHERE conrelid = 'questions'::regclass AND conname = 'questions_status_check'`).Scan(&validated, &tag); err != nil {
		t.Fatalf("questions_status_check missing: %v", err)
	}
	if !validated || !strings.HasPrefix(tag, "setup.sql:") {
		t.Fatalf("questions_status_check validated=%v tag=%q", validated, tag)
	}
	for _, rel := range []string{"qa.outbox_events", "question_revisions", "answer_revisions", "qa_platform_event_dead_letters"} {
		var ok bool
		_ = r.pool.QueryRow(r.ctx, `SELECT to_regclass($1) IS NOT NULL`, rel).Scan(&ok)
		if !ok {
			t.Errorf("%s was not created", rel)
		}
	}
	var hasStale bool
	_ = r.pool.QueryRow(r.ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'qa_communities' AND column_name = 'qa_stats_stale_at')`).Scan(&hasStale)
	if !hasStale {
		t.Error("qa_communities.qa_stats_stale_at was not added")
	}
}

// M6/M7: the online indexes build, are valid, and the search predicate uses
// the GIN index.
func TestContentIT_OnlineIndexesAreBuiltAndUsed(t *testing.T) {
	r := setupContentIT(t)
	if err := pgstore.BuildConcurrentIndexes(r.ctx, r.pool, database.ConcurrentIndexes); err != nil {
		t.Fatalf("build: %v", err)
	}
	for _, ix := range database.ConcurrentIndexes {
		var valid bool
		if err := r.pool.QueryRow(r.ctx, `
			SELECT i.indisvalid FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
			WHERE c.relname = $1`, ix.Name).Scan(&valid); err != nil || !valid {
			t.Errorf("%s: valid=%v err=%v", ix.Name, valid, err)
		}
	}
	tx, err := r.pool.Begin(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(r.ctx) }()
	if _, err := tx.Exec(r.ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	// With sequential scans priced out, the planner takes the index only if
	// the predicate can use it at all; on a near-empty CI table it would
	// otherwise rightly prefer a scan. Both search predicates are checked.
	explain := func(name, pred string) {
		t.Helper()
		var plan strings.Builder
		rows, err := tx.Query(r.ctx, `EXPLAIN SELECT q.id FROM questions q WHERE q.deleted_at IS NULL AND `+pred, PrefixTSQuery("salary"))
		if err != nil {
			t.Fatalf("explain: %v", err)
		}
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatalf("explain row: %v", err)
			}
			plan.WriteString(line + "\n")
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatalf("explain: %v", err)
		}
		if !strings.Contains(plan.String(), name) {
			// One line: CI summaries keep only a failure's first line.
			t.Fatalf("search does not use %s: plan: %s", name, strings.ReplaceAll(strings.TrimSpace(plan.String()), "\n", " | "))
		}
	}
	explain("idx_questions_search_tsv", searchMatchSQL("q", "$1"))
	explain("idx_questions_title_tsv", titleMatchSQL("q", "$1"))
}

// M6: words match as prefixes in title or body, in any order; LIKE
// metacharacters and tsquery syntax are inert.
func TestContentIT_SearchMatchesWordPrefixes(t *testing.T) {
	r := setupContentIT(t)
	tok := "zq" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	author := uuid.New()
	inTitle := r.question(author, "Negotiating "+tok+" salary", "plain body", CreateQuestionParams{})
	inBody := r.question(author, "Another question", "the "+tok+" appears in the body", CreateQuestionParams{})
	r.question(author, "Unrelated", "nothing to see", CreateQuestionParams{})

	got, err := r.st.SearchQuestions(r.ctx, tok[:6], nil, nil, nil, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[uuid.UUID]bool{}
	for _, q := range got {
		ids[q.ID] = true
	}
	if !ids[inTitle.ID] || !ids[inBody.ID] || len(got) != 2 {
		t.Fatalf("prefix search found %d: %v", len(got), ids)
	}
	got, _ = r.st.SearchQuestions(r.ctx, "salary "+tok, nil, nil, nil, 50, 0)
	if len(got) != 1 || got[0].ID != inTitle.ID {
		t.Fatalf("two-word search: %+v", got)
	}
	for _, inert := range []string{"%", "_", "' OR 1=1 --", "!|&():*"} {
		if res, err := r.st.SearchQuestions(r.ctx, inert, nil, nil, nil, 5, 0); err != nil {
			t.Fatalf("search %q failed: %v", inert, err)
		} else if len(res) > 0 && inert != "' OR 1=1 --" {
			t.Fatalf("search %q matched %d questions", inert, len(res))
		}
	}
	sim, err := r.st.GetSimilarQuestions(r.ctx, "negotiating "+tok, nil, 5)
	if err != nil || len(sim) != 1 || sim[0].ID != inTitle.ID {
		t.Fatalf("similar: %+v err %v", sim, err)
	}
}

// H3: client HTML never reaches storage, and a row stored before the fix
// (raw HTML in body_html) is never served.
func TestContentIT_BodyHTMLIsAlwaysRendered(t *testing.T) {
	r := setupContentIT(t)
	author := uuid.New()
	evil := `<img src=x onerror=alert(1)>`
	q := r.question(author, "xss", "hello "+evil, CreateQuestionParams{BodyHTML: evil})
	var stored string
	_ = r.pool.QueryRow(r.ctx, `SELECT body_html FROM questions WHERE id = $1`, q.ID).Scan(&stored)
	if strings.Contains(stored, "<img") || !strings.Contains(stored, "&lt;img") {
		t.Fatalf("stored body_html %q", stored)
	}
	a, err := r.st.CreateAnswer(r.ctx, q.ID, uuid.New(), "answer "+evil, evil, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(a.BodyHTML, "<img") {
		t.Fatalf("answer body_html %q", a.BodyHTML)
	}
	// A legacy row with raw HTML in the column.
	if _, err := r.pool.Exec(r.ctx, `UPDATE questions SET body_html = $2 WHERE id = $1`, q.ID, evil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(r.ctx, `UPDATE answers SET body_html = $2 WHERE id = $1`, a.ID, evil); err != nil {
		t.Fatal(err)
	}
	got, _ := r.st.GetQuestion(r.ctx, q.ID)
	if strings.Contains(got.BodyHTML, "<img") {
		t.Fatalf("served a stored raw body_html: %q", got.BodyHTML)
	}
	ga, _ := r.st.GetAnswer(r.ctx, a.ID)
	list, _ := r.st.ListAnswersByQuestion(r.ctx, q.ID, "", 10, 0)
	if strings.Contains(ga.BodyHTML, "<img") || len(list) != 1 || strings.Contains(list[0].BodyHTML, "<img") {
		t.Fatalf("served a stored raw answer body_html: %q / %+v", ga.BodyHTML, list)
	}
}

// L2/H4/M10: creation events commit with their rows, name the recipient,
// and never name an anonymous author.
func TestContentIT_EventsCommitWithTheirWrites(t *testing.T) {
	r := setupContentIT(t)
	asker, answerer := uuid.New(), uuid.New()
	named := r.question(asker, "named", "b", CreateQuestionParams{})
	anon := r.question(asker, "anonymous", "b", CreateQuestionParams{IsAnonymous: true})

	evs := r.outboxEvents(events.EventQAQuestionCreated, "question:"+named.ID.String())
	if len(evs) != 1 || payloadOf(t, evs[0])["author_id"] != asker.String() {
		t.Fatalf("named question.created: %+v", evs)
	}
	evs = r.outboxEvents(events.EventQAQuestionCreated, "question:"+anon.ID.String())
	if len(evs) != 1 || payloadOf(t, evs[0])["author_id"] != nil || evs[0].ActorUserID != nil {
		t.Fatalf("anonymous question.created names its author: %+v", evs)
	}

	a, err := r.st.CreateAnswer(r.ctx, anon.ID, answerer, "an answer", "", true)
	if err != nil {
		t.Fatal(err)
	}
	evs = r.outboxEvents(events.EventQAAnswerCreated, "question:"+anon.ID.String())
	if len(evs) != 1 {
		t.Fatalf("answer.created rows: %d", len(evs))
	}
	p := payloadOf(t, evs[0])
	if p["question_author_id"] != asker.String() || p["is_anonymous"] != true || evs[0].ActorUserID != nil {
		t.Fatalf("answer.created payload %v actor %v", p, evs[0].ActorUserID)
	}

	if _, err := r.st.CreateComment(r.ctx, a.ID, asker, "thanks"); err != nil {
		t.Fatal(err)
	}
	evs = r.outboxEvents(events.EventQAAnswerCommentCreated, "question:"+anon.ID.String())
	if len(evs) != 1 || payloadOf(t, evs[0])["answer_author_id"] != answerer.String() {
		t.Fatalf("comment.created: %+v", evs)
	}

	// A write that fails leaves no event: an unknown topic aborts the insert.
	title := "rollback-" + uuid.NewString()
	_, err = r.st.CreateQuestion(r.ctx, asker, CreateQuestionParams{Title: title, Topics: []string{"no-such-topic-" + uuid.NewString()}})
	if err == nil {
		t.Fatal("an unknown topic was accepted")
	}
	var n int
	_ = r.pool.QueryRow(r.ctx, `SELECT count(*) FROM qa.outbox_events WHERE event_type = $1 AND payload->'payload'->>'title' = $2`, events.EventQAQuestionCreated, title).Scan(&n)
	if n != 0 {
		t.Fatalf("a rolled-back question left %d events", n)
	}
}

// L3: an edit keeps the slug (old links work), records the replaced text,
// and a slug minted before the fix still resolves by its id suffix.
func TestContentIT_EditsKeepTheSlugAndHistory(t *testing.T) {
	r := setupContentIT(t)
	author := uuid.New()
	q := r.question(author, "Original title", "original body", CreateQuestionParams{})
	newTitle, newBody := "A completely new title", "new body"
	up, err := r.st.UpdateQuestion(r.ctx, q.ID, UpdateQuestionParams{Title: &newTitle, Body: &newBody})
	if err != nil {
		t.Fatal(err)
	}
	if up.Slug != q.Slug || up.Title != newTitle {
		t.Fatalf("slug %q -> %q, title %q", q.Slug, up.Slug, up.Title)
	}
	if got, err := r.st.GetQuestionBySlug(r.ctx, q.Slug); err != nil || got.ID != q.ID {
		t.Fatalf("the original link broke: %v", err)
	}
	var title, body string
	var editor uuid.UUID
	if err := r.pool.QueryRow(r.ctx, `SELECT title, body, editor_id FROM question_revisions WHERE question_id = $1`, q.ID).Scan(&title, &body, &editor); err != nil {
		t.Fatalf("no revision: %v", err)
	}
	if title != "Original title" || body != "original body" || editor != author {
		t.Fatalf("revision %q %q %s", title, body, editor)
	}
	if evs := r.outboxEvents(events.EventQAQuestionUpdated, "question:"+q.ID.String()); len(evs) != 1 {
		t.Fatalf("question.updated rows: %d", len(evs))
	}
	// An edit that changes nothing writes nothing.
	if _, err := r.st.UpdateQuestion(r.ctx, q.ID, UpdateQuestionParams{Title: &newTitle}); err != nil {
		t.Fatal(err)
	}
	var revs int
	_ = r.pool.QueryRow(r.ctx, `SELECT count(*) FROM question_revisions WHERE question_id = $1`, q.ID).Scan(&revs)
	if revs != 1 {
		t.Fatalf("a no-op edit wrote a revision (%d)", revs)
	}
	// A link minted by the old regenerate-on-edit code.
	suffix := strings.ReplaceAll(q.ID.String(), "-", "")[:slugIDDigits]
	if got, err := r.st.GetQuestionBySlug(r.ctx, "some-older-title-"+suffix); err != nil || got.ID != q.ID {
		t.Fatalf("an old slug did not resolve: %v", err)
	}

	a, err := r.st.CreateAnswer(r.ctx, q.ID, uuid.New(), "first", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.st.UpdateAnswer(r.ctx, a.ID, "second <b>", ""); err != nil {
		t.Fatal(err)
	}
	var old string
	if err := r.pool.QueryRow(r.ctx, `SELECT body FROM answer_revisions WHERE answer_id = $1`, a.ID).Scan(&old); err != nil || old != "first" {
		t.Fatalf("answer revision %q err %v", old, err)
	}
}

// M7: an answer only stamps its community; the refresher recounts it; the
// follow count moves by deltas and a repeat follow changes nothing.
func TestContentIT_CommunityCountsAreRecountedOffThePath(t *testing.T) {
	r := setupContentIT(t)
	cid := r.community("public")
	asker, a1, a2 := uuid.New(), uuid.New(), uuid.New()
	q := r.question(asker, "community q", "b", CreateQuestionParams{CommunityID: &cid})
	if _, err := r.st.CreateAnswer(r.ctx, q.ID, a1, "x", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.st.CreateAnswer(r.ctx, q.ID, a2, "y", "", false); err != nil {
		t.Fatal(err)
	}
	var stale *time.Time
	_ = r.pool.QueryRow(r.ctx, `SELECT qa_stats_stale_at FROM qa_communities WHERE id = $1`, cid).Scan(&stale)
	if stale == nil {
		t.Fatal("an answer did not stamp its community")
	}
	for i := 0; i < 10; i++ { // other tests' communities may be stamped too
		if _, err := r.st.RefreshStaleCommunityStats(r.ctx, 1000); err != nil {
			t.Fatal(err)
		}
		_ = r.pool.QueryRow(r.ctx, `SELECT qa_stats_stale_at FROM qa_communities WHERE id = $1`, cid).Scan(&stale)
		if stale == nil {
			break
		}
	}
	var qs, as, cs, settingsAnswers int
	_ = r.pool.QueryRow(r.ctx, `SELECT qa_question_count, qa_answer_count, qa_contributor_count FROM qa_communities WHERE id = $1`, cid).Scan(&qs, &as, &cs)
	_ = r.pool.QueryRow(r.ctx, `SELECT total_answers_count FROM community_qa_settings WHERE community_id = $1`, cid).Scan(&settingsAnswers)
	if stale != nil || qs != 1 || as != 2 || cs != 3 || settingsAnswers != 2 {
		t.Fatalf("stale=%v questions=%d answers=%d contributors=%d settings=%d", stale, qs, as, cs, settingsAnswers)
	}

	follower := uuid.New()
	for i := 0; i < 2; i++ {
		if err := r.st.FollowQuestion(r.ctx, follower, q.ID); err != nil {
			t.Fatal(err)
		}
	}
	_ = r.st.FollowQuestion(r.ctx, uuid.New(), q.ID)
	var follows int
	_ = r.pool.QueryRow(r.ctx, `SELECT follow_count FROM questions WHERE id = $1`, q.ID).Scan(&follows)
	if follows != 2 {
		t.Fatalf("follow_count %d after a repeat follow, want 2", follows)
	}
	_ = r.st.UnfollowQuestion(r.ctx, follower, q.ID)
	_ = r.st.UnfollowQuestion(r.ctx, follower, q.ID)
	_ = r.pool.QueryRow(r.ctx, `SELECT follow_count FROM questions WHERE id = $1`, q.ID).Scan(&follows)
	if follows != 1 {
		t.Fatalf("follow_count %d after a repeat unfollow, want 1", follows)
	}
}

// M8: erasure is one transaction that blanks the user's words, removes
// their votes with the counters they moved, deletes what hangs off them,
// and is idempotent.
func TestContentIT_ErasureScrubsAndReversesCounters(t *testing.T) {
	r := setupContentIT(t)
	gone, other := uuid.New(), uuid.New()
	theirQ := r.question(other, "someone else's question", "b", CreateQuestionParams{})
	mine := r.question(gone, "my private trouble", "details about me", CreateQuestionParams{})
	myAnswer, err := r.st.CreateAnswer(r.ctx, theirQ.ID, gone, "my answer text", "", false)
	if err != nil {
		t.Fatal(err)
	}
	newBody := "edited text"
	if _, err := r.st.UpdateAnswer(r.ctx, myAnswer.ID, newBody, ""); err != nil {
		t.Fatal(err)
	}
	theirAnswer, _ := r.st.CreateAnswer(r.ctx, theirQ.ID, other, "their answer", "", false)
	if _, err := r.st.CreateComment(r.ctx, theirAnswer.ID, gone, "my comment"); err != nil {
		t.Fatal(err)
	}
	// The erased user's votes, with the counters they moved.
	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := r.pool.Exec(r.ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	mustExec(`INSERT INTO question_votes (user_id, question_id, vote_type) VALUES ($1, $2, 'up')`, gone, theirQ.ID)
	mustExec(`UPDATE questions SET vote_score = vote_score + 1, upvote_count = upvote_count + 1 WHERE id = $1`, theirQ.ID)
	mustExec(`INSERT INTO answer_votes (user_id, answer_id, vote_type) VALUES ($1, $2, 'down')`, gone, theirAnswer.ID)
	mustExec(`UPDATE answers SET vote_score = vote_score - 1, downvote_count = downvote_count + 1 WHERE id = $1`, theirAnswer.ID)
	if err := r.st.FollowQuestion(r.ctx, gone, theirQ.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.st.UpsertQuestionDraft(r.ctx, UpsertQuestionDraftParams{AuthorID: gone, Title: "draft"}); err != nil {
		t.Fatal(err)
	}
	mustExec(`INSERT INTO answer_requests (question_id, requester_id, requested_user_id) VALUES ($1, $2, $3)`, theirQ.ID, other, gone)

	erased, err := r.st.AnonymizeUser(r.ctx, gone)
	if err != nil {
		t.Fatalf("erase: %v", err)
	}
	if len(erased.QuestionIDs) != 1 || erased.QuestionIDs[0] != mine.ID || len(erased.AnswerIDs) != 1 || erased.AnswerIDs[0] != myAnswer.ID {
		t.Fatalf("erased %+v", erased)
	}

	var title, body, slug string
	_ = r.pool.QueryRow(r.ctx, `SELECT title, body, slug FROM questions WHERE id = $1`, mine.ID).Scan(&title, &body, &slug)
	if title != ErasedQuestionTitle || body != "" || strings.Contains(slug, "trouble") {
		t.Fatalf("question not scrubbed: %q %q %q", title, body, slug)
	}
	var aBody, cBody string
	_ = r.pool.QueryRow(r.ctx, `SELECT body FROM answers WHERE id = $1`, myAnswer.ID).Scan(&aBody)
	_ = r.pool.QueryRow(r.ctx, `SELECT body FROM answer_comments WHERE author_id = $1`, gone).Scan(&cBody)
	if aBody != "" || cBody != "" {
		t.Fatalf("answer %q / comment %q not scrubbed", aBody, cBody)
	}
	var qScore, qUp, aScore, aDown, follows, answerCount, commentCount int
	_ = r.pool.QueryRow(r.ctx, `SELECT vote_score, upvote_count, follow_count, answer_count FROM questions WHERE id = $1`, theirQ.ID).Scan(&qScore, &qUp, &follows, &answerCount)
	_ = r.pool.QueryRow(r.ctx, `SELECT vote_score, downvote_count, comment_count FROM answers WHERE id = $1`, theirAnswer.ID).Scan(&aScore, &aDown, &commentCount)
	if qScore != 0 || qUp != 0 || aScore != 0 || aDown != 0 || follows != 0 {
		t.Fatalf("counters left inflated: q score %d up %d, a score %d down %d, follows %d", qScore, qUp, aScore, aDown, follows)
	}
	if answerCount != 1 || commentCount != 0 {
		t.Fatalf("answer_count %d (want 1: theirs only), comment_count %d", answerCount, commentCount)
	}
	for table, where := range map[string]string{
		"question_votes":     "user_id = $1",
		"answer_votes":       "user_id = $1",
		"question_follows":   "user_id = $1",
		"question_drafts":    "author_id = $1",
		"answer_requests":    "requested_user_id = $1 OR requester_id = $1",
		"answer_revisions":   "editor_id = $1",
		"question_revisions": "editor_id = $1",
		"reputation_events":  "user_id = $1",
		"qa_profiles":        "user_id = $1",
	} {
		var n int
		if err := r.pool.QueryRow(r.ctx, `SELECT count(*) FROM `+table+` WHERE `+where, gone).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s: %d rows left (err %v)", table, n, err)
		}
	}
	again, err := r.st.AnonymizeUser(r.ctx, gone)
	if err != nil || len(again.QuestionIDs)+len(again.AnswerIDs) != 0 {
		t.Fatalf("a second erasure: %+v err %v", again, err)
	}
	_ = r.pool.QueryRow(r.ctx, `SELECT answer_count FROM questions WHERE id = $1`, theirQ.ID).Scan(&answerCount)
	if answerCount != 1 {
		t.Fatalf("a second erasure moved answer_count again: %d", answerCount)
	}
}

// M8: a dead letter is durable and idempotent per offset.
func TestContentIT_DeadLetterIsIdempotentPerOffset(t *testing.T) {
	r := setupContentIT(t)
	off := time.Now().UnixNano()
	for i := 0; i < 2; i++ {
		if err := r.st.RecordPlatformEventDeadLetter(r.ctx, "platform-events", 3, off, "user.deletion_requested", []byte(`{}`), 10, strings.Repeat("e", 5000)); err != nil {
			t.Fatal(err)
		}
	}
	var n, attempts int
	var lastErr string
	_ = r.pool.QueryRow(r.ctx, `SELECT count(*), max(attempts), max(last_error) FROM qa_platform_event_dead_letters WHERE kafka_offset = $1`, off).Scan(&n, &attempts, &lastErr)
	if n != 1 || attempts != 20 || len(lastErr) > maxDeadLetterError {
		t.Fatalf("rows %d attempts %d error length %d", n, attempts, len(lastErr))
	}
}
