//go:build integration

package postgres

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Module 1 fixes-v3 / LB-1 — store-level acceptance tests.
//
// LOCAL EVIDENCE: executed against PostgreSQL 16.4 on 2026-08-10; results
// are recorded in prompt/module-01-core-posting-creative-tools-claude-fixes-v4.md.
// This suite remains a required CI gate
// (.github/workflows/integration-postgres.yml, job
// `media-reference-integrity`).
//
// They require a live PostgreSQL with the media-service and post-service
// schemas (including post-service migration 030, which adds the foreign
// keys that provide the concurrency guarantee). Run with:
//
//	POSTGRES_DSN=postgres://... go test -tags integration ./internal/store/postgres/ -run Orphan -v
//
// The concurrency case below is a REAL two-goroutine race against a real
// database, not a mock. It is written so that a run without the FKs in
// place will fail — that is intentional, because the FK is the actual
// guarantee and a green result without it would be misleading.

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// fkShape is the catalog-level shape of one media foreign key.
type fkShape struct {
	Constraint string
	Child      string
	Parent     string
	ChildCol   string
	ParentCol  string
	ConType    string
	DelAction  string // confdeltype: 'r' = RESTRICT
	Enforced   bool
}

// requireMediaForeignKeys asserts BOTH constraints exist with the correct
// shape (Codex re-review v3, LB-1.1: "checking conname alone is
// insufficient" — verify child table, parent table, columns,
// ON DELETE RESTRICT, and enforcement state).
//
// This runs FIRST in every destructive test below, so a missing or
// misinstalled migration 030 fails the suite DETERMINISTICALLY rather
// than intermittently through race scheduling. Without it, a green run
// on a database lacking the FKs would be dangerously misleading — the
// application-level snapshot check alone cannot close the race.
func requireMediaForeignKeys(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	// PostgreSQL 18 added pg_constraint.conenforced (NOT ENFORCED
	// constraints). On older versions every constraint is enforced, so
	// report true.
	var hasConenforced bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM pg_attribute
			WHERE attrelid = 'pg_constraint'::regclass
			  AND attname  = 'conenforced'
			  AND NOT attisdropped)`).Scan(&hasConenforced); err != nil {
		t.Fatalf("probe pg_constraint.conenforced: %v", err)
	}
	enforcedExpr := "TRUE"
	if hasConenforced {
		enforcedExpr = "c.conenforced"
	}

	want := []struct{ constraint, child string }{
		{"fk_post_media_media_asset", "public.post_media"},
		{"fk_post_draft_media_media_asset", "public.post_draft_media"},
	}

	for _, w := range want {
		var got fkShape
		err := pool.QueryRow(ctx, `
			SELECT c.conname,
			       c.conrelid::regclass::text,
			       c.confrelid::regclass::text,
			       a.attname,
			       af.attname,
			       c.contype::text,
			       c.confdeltype::text,
			       `+enforcedExpr+`
			  FROM pg_constraint c
			  JOIN unnest(c.conkey)  WITH ORDINALITY AS ck(attnum, ord) ON TRUE
			  JOIN unnest(c.confkey) WITH ORDINALITY AS fk(attnum, ord) ON fk.ord = ck.ord
			  JOIN pg_attribute a  ON a.attrelid  = c.conrelid  AND a.attnum  = ck.attnum
			  JOIN pg_attribute af ON af.attrelid = c.confrelid AND af.attnum = fk.attnum
			 WHERE c.conname  = $1
			   AND c.conrelid = $2::regclass`,
			w.constraint, w.child).
			Scan(&got.Constraint, &got.Child, &got.Parent, &got.ChildCol,
				&got.ParentCol, &got.ConType, &got.DelAction, &got.Enforced)
		if err != nil {
			t.Fatalf("LB-1 PRECONDITION FAILED: constraint %s on %s not found (%v).\n"+
				"post-service migration 030 has not installed the media foreign keys. "+
				"Without them the attach-vs-delete race is NOT closed and the "+
				"concurrency test below would pass for the wrong reason.",
				w.constraint, w.child, err)
		}

		if got.ConType != "f" {
			t.Fatalf("%s: contype=%q, want \"f\" (FOREIGN KEY)", w.constraint, got.ConType)
		}
		if got.Parent != "media_assets" && got.Parent != "public.media_assets" {
			t.Fatalf("%s: parent=%q, want public.media_assets", w.constraint, got.Parent)
		}
		if got.ChildCol != "media_id" {
			t.Fatalf("%s: child column=%q, want media_id", w.constraint, got.ChildCol)
		}
		if got.ParentCol != "id" {
			t.Fatalf("%s: parent column=%q, want id", w.constraint, got.ParentCol)
		}
		if got.DelAction != "r" {
			t.Fatalf("%s: confdeltype=%q, want \"r\" (ON DELETE RESTRICT). "+
				"A different referential action would not prevent deleting "+
				"referenced media.", w.constraint, got.DelAction)
		}
		if !got.Enforced {
			t.Fatalf("%s exists but is NOT ENFORCED — it provides no runtime guarantee",
				w.constraint)
		}
		t.Logf("FK OK: %s  %s(%s) -> %s(%s)  ON DELETE RESTRICT, enforced",
			got.Constraint, got.Child, got.ChildCol, got.Parent, got.ParentCol)
	}
}

// TestMediaForeignKeysInstalled is the standalone gate. It is the first
// thing CI should run: if migration 030 did not install both constraints,
// this fails immediately with a clear reason.
func TestMediaForeignKeysInstalled(t *testing.T) {
	pool := testPool(t)
	requireMediaForeignKeys(t, pool)
}

// NOT VALID is expected and acceptable (historical rows were not
// rescanned); this test records the current validation state so a future
// VALIDATE CONSTRAINT step has a baseline, and proves the constraint is
// nonetheless enforced for new rows.
func TestMediaForeignKeysValidationState(t *testing.T) {
	pool := testPool(t)
	requireMediaForeignKeys(t, pool)

	rows, err := pool.Query(context.Background(), `
		SELECT conname, convalidated FROM pg_constraint
		WHERE conname IN ('fk_post_media_media_asset','fk_post_draft_media_media_asset')`)
	if err != nil {
		t.Fatalf("query validation state: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var validated bool
		if err := rows.Scan(&name, &validated); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s convalidated=%v (NOT VALID is acceptable; enforcement is what matters)", name, validated)
	}
}

// seedMedia inserts an asset with a chosen age.
func seedMedia(t *testing.T, pool *pgxpool.Pool, age time.Duration) uuid.UUID {
	t.Helper()
	id := uuid.New()
	created := time.Now().Add(-age)
	_, err := pool.Exec(context.Background(), `
		INSERT INTO media_assets (id, uploader_id, file_type, media_subtype, mime_type,
		    file_size_bytes, storage_bucket, storage_key, processing_status, created_at, updated_at)
		VALUES ($1, $2, 'image', 'general', 'image/jpeg', 100, 'media', $3, 'ready', $4, $4)`,
		id, uuid.New(), "test/"+id.String(), created)
	if err != nil {
		t.Fatalf("seed media: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM media_assets WHERE id = $1`, id)
	})
	return id
}

func TestOrphanDelete_YoungMediaRefused(t *testing.T) {
	pool := testPool(t)
	requireMediaForeignKeys(t, pool)
	store := New(pool)
	id := seedMedia(t, pool, 1*time.Hour) // inside a 24h window

	_, err := store.DeleteOrphanMediaAtomic(context.Background(), id, 24*time.Hour)
	if err != ErrMediaTooYoung {
		t.Fatalf("young media must be refused, got %v", err)
	}
}

func TestOrphanDelete_PublishedReferenceRefused(t *testing.T) {
	pool := testPool(t)
	requireMediaForeignKeys(t, pool)
	store := New(pool)
	ctx := context.Background()
	id := seedMedia(t, pool, 48*time.Hour)

	postID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO posts (id, author_id, text, visibility, content_type, created_at, updated_at)
		VALUES ($1, $2, 'x', 'public', 'post', NOW(), NOW())`, postID, uuid.New()); err != nil {
		t.Fatalf("seed post: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO post_media (post_id, media_id, kind) VALUES ($1,$2,'image')`, postID, id); err != nil {
		t.Fatalf("seed post_media: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM post_media WHERE post_id = $1`, postID)
		_, _ = pool.Exec(ctx, `DELETE FROM posts WHERE id = $1`, postID)
	})

	if _, err := store.DeleteOrphanMediaAtomic(ctx, id, 24*time.Hour); err != ErrMediaStillReferenced {
		t.Fatalf("published reference must block deletion, got %v", err)
	}
}

// The v2 defect: only post_media was checked, so a surviving draft's
// media was wrongly reclaimable after 24 hours.
func TestOrphanDelete_SurvivingDraftReferenceRefused(t *testing.T) {
	pool := testPool(t)
	requireMediaForeignKeys(t, pool)
	store := New(pool)
	ctx := context.Background()
	id := seedMedia(t, pool, 48*time.Hour)

	draftID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO post_drafts (id, author_id, post_type, payload, status)
		VALUES ($1, $2, 'post', '{}'::jsonb, 'draft')`, draftID, uuid.New()); err != nil {
		t.Fatalf("seed draft: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO post_draft_media (draft_id, media_id) VALUES ($1,$2)`, draftID, id); err != nil {
		t.Fatalf("seed draft media: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM post_draft_media WHERE draft_id = $1`, draftID)
		_, _ = pool.Exec(ctx, `DELETE FROM post_drafts WHERE id = $1`, draftID)
	})

	if _, err := store.DeleteOrphanMediaAtomic(ctx, id, 24*time.Hour); err != ErrMediaStillReferenced {
		t.Fatalf("surviving draft reference must block deletion, got %v", err)
	}

	// A soft-deleted draft must NOT block reclamation.
	if _, err := pool.Exec(ctx,
		`UPDATE post_drafts SET status = 'deleted' WHERE id = $1`, draftID); err != nil {
		t.Fatalf("soft-delete draft: %v", err)
	}
	if _, err := store.DeleteOrphanMediaAtomic(ctx, id, 24*time.Hour); err != nil {
		t.Fatalf("deleted-draft media should be reclaimable, got %v", err)
	}
}

func TestOrphanDelete_UnreferencedOldMediaDeletedOnce(t *testing.T) {
	pool := testPool(t)
	requireMediaForeignKeys(t, pool)
	store := New(pool)
	ctx := context.Background()
	id := seedMedia(t, pool, 48*time.Hour)

	keys, err := store.DeleteOrphanMediaAtomic(ctx, id, 24*time.Hour)
	if err != nil {
		t.Fatalf("unreferenced old media must delete, got %v", err)
	}
	if len(keys) == 0 {
		t.Fatal("object keys must be returned for blob cleanup")
	}

	// Keys must be durably recorded BEFORE rows vanished, so a blob
	// failure is retryable.
	var pending int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM media_blob_reclaim WHERE media_id = $1`, id).Scan(&pending); err != nil {
		t.Fatalf("count reclaim rows: %v", err)
	}
	if pending == 0 {
		t.Fatal("blob reclaim intent must be persisted before deletion")
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM media_blob_reclaim WHERE media_id = $1`, id)
	})

	// Retry after deletion is explicit and safe, not a 500.
	if _, err := store.DeleteOrphanMediaAtomic(ctx, id, 24*time.Hour); err != ErrMediaNotFound {
		t.Fatalf("retry after deletion must report not-found, got %v", err)
	}
}

// THE race. A concurrent attach must never end with a reference pointing
// at deleted media. With post-service migration 030's foreign keys the
// two transactions serialize on the media_assets row: exactly one wins.
func TestOrphanDelete_ConcurrentAttachmentCannotDangle(t *testing.T) {
	pool := testPool(t)
	requireMediaForeignKeys(t, pool)
	store := New(pool)
	ctx := context.Background()

	for i := 0; i < 25; i++ {
		id := seedMedia(t, pool, 48*time.Hour)
		postID := uuid.New()
		if _, err := pool.Exec(ctx, `
			INSERT INTO posts (id, author_id, text, visibility, content_type, created_at, updated_at)
			VALUES ($1, $2, 'x', 'public', 'post', NOW(), NOW())`, postID, uuid.New()); err != nil {
			t.Fatalf("seed post: %v", err)
		}

		var wg sync.WaitGroup
		var delErr, attachErr error
		start := make(chan struct{})

		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, delErr = store.DeleteOrphanMediaAtomic(ctx, id, 24*time.Hour)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, attachErr = pool.Exec(ctx,
				`INSERT INTO post_media (post_id, media_id, kind) VALUES ($1,$2,'image')`, postID, id)
		}()
		close(start)
		wg.Wait()

		// INVARIANT 1 — mutual exclusion. With the foreign keys installed
		// the two transactions serialize on the media_assets row, so at
		// most one may succeed. Both succeeding means the FK is absent or
		// not enforced.
		if delErr == nil && attachErr == nil {
			t.Fatalf("iteration %d: LB-1 VIOLATION — delete and attach BOTH succeeded; "+
				"the media foreign key is not serializing the two transactions", i)
		}

		// INVARIANT 2 — at least one must succeed. If both failed the
		// system is deadlocking or refusing legitimate work.
		if delErr != nil && attachErr != nil {
			t.Logf("iteration %d: neither side won (delete=%v attach=%v) — "+
				"acceptable only if the delete lost the reference check", i, delErr, attachErr)
		}

		// INVARIANT 3 — no dangling reference, checked unconditionally
		// rather than only when both succeeded.
		var dangling bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM post_media pm
				LEFT JOIN media_assets ma ON ma.id = pm.media_id
				WHERE pm.media_id = $1 AND ma.id IS NULL)`, id).Scan(&dangling); err != nil {
			t.Fatalf("iteration %d: dangling check failed: %v", i, err)
		}
		if dangling {
			t.Fatalf("iteration %d: LB-1 VIOLATION — post_media references deleted media "+
				"(delete=%v attach=%v)", i, delErr, attachErr)
		}

		_, _ = pool.Exec(ctx, `DELETE FROM post_media WHERE post_id = $1`, postID)
		_, _ = pool.Exec(ctx, `DELETE FROM posts WHERE id = $1`, postID)
		_, _ = pool.Exec(ctx, `DELETE FROM media_blob_reclaim WHERE media_id = $1`, id)
	}
}
