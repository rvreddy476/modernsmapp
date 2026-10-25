// DB-backed proof of the media claim protocol for questions
// (media_claims.go): a question's media holds a claim in media-service from
// before the row names it until after it stops, and the reconcile tool claims
// what legacy question and answer rows hold. The media-service side is the
// claim-keeping fake from media_claims_test.go; the Q&A rows are real.
// TEST_PG_DSN on a "_test" database (qa_it_test), CI only.
package service

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/qa-service/database"
	"github.com/atpost/qa-service/internal/claimsync"
	"github.com/atpost/qa-service/internal/mediaclaims"
	"github.com/atpost/qa-service/internal/mediaclient"
	"github.com/atpost/qa-service/internal/store"
	pgstore "github.com/atpost/qa-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setupMediaClaimsIT(t *testing.T) (*Service, *store.Store, *pgxpool.Pool, string) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping qa-service media claim integration tests")
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
	slug := "mc-" + uuid.NewString()[:12]
	if _, err := pool.Exec(ctx, `INSERT INTO topics (name, slug) VALUES ($1, $1)`, slug); err != nil {
		t.Fatalf("seed topic: %v", err)
	}
	st := store.New(pool)
	return New(st, nil), st, pool, slug
}

func TestMediaClaimsIT_QuestionLifecycle(t *testing.T) {
	svc, _, _, topic := setupMediaClaimsIT(t)
	ctx := context.Background()
	author := uuid.New()
	a, b := readyAsset(author), readyAsset(author)
	f := newFakeMedia(a, b)
	svc.WithMedia(mediaclaims.New(f, f))

	q, err := svc.CreateQuestion(ctx, author, store.CreateQuestionParams{
		Title: "with media", Topics: []string{topic}, MediaIDs: []string{a.ID.String(), b.ID.String()},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !f.holds(mediaclient.KindQuestionMedia, q.ID, a.ID) || !f.holds(mediaclient.KindQuestionMedia, q.ID, b.ID) {
		t.Fatalf("a created question does not hold claims on its media (or under another id than the row): %v", f.held)
	}
	if err := svc.DeleteQuestion(ctx, q.ID, author); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(f.held) != 0 {
		t.Fatalf("a deleted question still holds media: %v", f.held)
	}
}

// A write that fails after the claim releases it: the store refuses an
// unknown topic after the media was claimed.
func TestMediaClaimsIT_FailedWriteReleasesItsClaims(t *testing.T) {
	svc, _, pool, _ := setupMediaClaimsIT(t)
	ctx := context.Background()
	author := uuid.New()
	a := readyAsset(author)
	f := newFakeMedia(a)
	svc.WithMedia(mediaclaims.New(f, f))
	if _, err := svc.CreateQuestion(ctx, author, store.CreateQuestionParams{
		Title: "bad topic", Topics: []string{"no-such-topic-" + uuid.NewString()}, MediaIDs: []string{a.ID.String()},
	}); err == nil {
		t.Fatal("a question with an unknown topic was created")
	}
	if len(f.held) != 0 {
		t.Fatalf("a failed write left its claims: %v", f.held)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM question_media WHERE media_id = $1`, a.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a failed write left media rows: %d %v", n, err)
	}
}

func TestMediaClaimsIT_ReconcileBackfillsAndReleases(t *testing.T) {
	_, st, pool, _ := setupMediaClaimsIT(t)
	ctx := context.Background()
	author := uuid.New()
	qm, am := readyAsset(author), readyAsset(author)
	f := newFakeMedia(qm, am)
	// Rows written before the protocol: they name media with no claim.
	var qid, aid uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO questions (author_id, title, slug) VALUES ($1, 'legacy', $2) RETURNING id`,
		author, "legacy-"+uuid.NewString()).Scan(&qid); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO question_media (question_id, media_id) VALUES ($1, $2)`, qid, qm.ID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO answers (question_id, author_id, body) VALUES ($1, $2, 'a') RETURNING id`,
		qid, author).Scan(&aid); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO answer_media (answer_id, media_id) VALUES ($1, $2)`, aid, am.ID); err != nil {
		t.Fatal(err)
	}
	pagers := []claimsync.Pager{st.QuestionMediaClaimRows, st.AnswerMediaClaimRows}
	if _, err := claimsync.Run(ctx, st, f, pagers, false); err != nil || len(f.held) != 0 {
		t.Fatalf("a dry run sent claims: %v %v", err, f.held)
	}
	if rep, err := claimsync.Run(ctx, st, f, pagers, true); err != nil || rep.Failed != 0 {
		t.Fatalf("apply: %v %s", err, rep)
	}
	if !f.holds(mediaclient.KindQuestionMedia, qid, qm.ID) || !f.holds(mediaclient.KindAnswerMedia, aid, am.ID) {
		t.Fatalf("the backfill did not claim legacy media: %v", f.held)
	}
	// Deleting the question kills its answers too.
	if _, err := pool.Exec(ctx, `UPDATE questions SET deleted_at = now(), status = 'deleted' WHERE id = $1`, qid); err != nil {
		t.Fatal(err)
	}
	if _, err := claimsync.Run(ctx, st, f, pagers, true); err != nil {
		t.Fatal(err)
	}
	if len(f.held) != 0 {
		t.Fatalf("the repair did not release a deleted question's media: %v", f.held)
	}
}

// The second writer of one row waits for the first; another row does not.
func TestMediaClaimsIT_HolderLockSerialises(t *testing.T) {
	_, st, _, _ := setupMediaClaimsIT(t)
	ctx := context.Background()
	ref := uuid.New()
	unlock, err := st.LockMediaHolder(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.LockMediaHolder(ctx, uuid.New())
	if err != nil {
		t.Fatalf("a different row's lock waited: %v", err)
	}
	other()
	got := make(chan struct{})
	go func() {
		u, err := st.LockMediaHolder(ctx, ref)
		if err == nil {
			u()
		}
		close(got)
	}()
	select {
	case <-got:
		t.Fatal("a second writer took the lock while the first held it")
	case <-time.After(300 * time.Millisecond):
	}
	unlock()
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("the lock was not freed by unlock")
	}
}
