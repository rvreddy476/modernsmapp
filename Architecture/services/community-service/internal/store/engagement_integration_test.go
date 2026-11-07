// DB-backed proof that community spark and view counts are idempotent per
// user (engagement_test.go pins the statements), and that migration 004 closes
// private communities stored as open. TEST_PG_DSN on a "_test" database, CI
// only.
package store_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/atpost/community-service/database"
	"github.com/atpost/community-service/internal/store"
	pgstore "github.com/atpost/community-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setupEngagementIT(t *testing.T) (*store.Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping community-service engagement integration tests")
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
	if err := pgstore.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	return store.New(pool), pool
}

func seedPost(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO community_posts (community_id, space_id, author_id, body) VALUES ($1, $2, $3, 'it') RETURNING id`,
		uuid.New(), uuid.New(), uuid.NewString()).Scan(&id); err != nil {
		t.Fatalf("seed post: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM community_post_sparks WHERE post_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM community_post_views WHERE post_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM community_posts WHERE id = $1`, id)
	})
	return id
}

func counts(t *testing.T, pool *pgxpool.Pool, postID uuid.UUID) (sparks, views int) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT spark_count, view_count FROM community_posts WHERE id = $1`, postID).Scan(&sparks, &views); err != nil {
		t.Fatal(err)
	}
	return sparks, views
}

func TestEngagementIT_SparkCountsOncePerUser(t *testing.T) {
	st, pool := setupEngagementIT(t)
	ctx := context.Background()
	post := seedPost(t, pool)
	a, b := uuid.NewString(), uuid.NewString()

	for i := 0; i < 3; i++ {
		if err := st.SparkCommunityPost(ctx, post, a, false); err != nil {
			t.Fatal(err)
		}
	}
	// A repeat as a supernova is still the same user's one spark.
	if err := st.SparkCommunityPost(ctx, post, a, true); err != nil {
		t.Fatal(err)
	}
	if s, _ := counts(t, pool, post); s != 1 {
		t.Fatalf("one user sparking four times: spark_count = %d, want 1", s)
	}
	if err := st.SparkCommunityPost(ctx, post, b, true); err != nil {
		t.Fatal(err)
	}
	if s, _ := counts(t, pool, post); s != 6 {
		t.Fatalf("a second user's supernova: spark_count = %d, want 1 + 5", s)
	}
}

func TestEngagementIT_ViewCountsOncePerWindow(t *testing.T) {
	st, pool := setupEngagementIT(t)
	ctx := context.Background()
	post := seedPost(t, pool)
	a, b := uuid.NewString(), uuid.NewString()

	for i := 0; i < 5; i++ {
		if err := st.RecordCommunityPostView(ctx, post, a); err != nil {
			t.Fatal(err)
		}
	}
	if _, v := counts(t, pool, post); v != 1 {
		t.Fatalf("five views in the window: view_count = %d, want 1", v)
	}
	if err := st.RecordCommunityPostView(ctx, post, b); err != nil {
		t.Fatal(err)
	}
	if _, v := counts(t, pool, post); v != 2 {
		t.Fatalf("a second viewer: view_count = %d, want 2", v)
	}

	// Once the window has passed, the same viewer counts again, once.
	if _, err := pool.Exec(ctx,
		`UPDATE community_post_views SET viewed_at = NOW() - make_interval(secs => $3)
		 WHERE post_id = $1 AND user_id = $2`, post, a, store.ViewDedupWindow.Seconds()+60); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := st.RecordCommunityPostView(ctx, post, a); err != nil {
			t.Fatal(err)
		}
	}
	if _, v := counts(t, pool, post); v != 3 {
		t.Fatalf("a view after the window: view_count = %d, want 3", v)
	}
}

func TestEngagementIT_Migration004ClosesPrivateOpenCommunities(t *testing.T) {
	_, pool := setupEngagementIT(t)
	ctx := context.Background()
	migration, err := database.Migrations.ReadFile("migrations/004_private_communities_not_open.sql")
	if err != nil {
		t.Fatal(err)
	}
	seed := map[string]struct{ ctype, mode, want string }{
		"private open":   {"private", "open", "invite_only"},
		"fan open":       {"fan", "open", "invite_only"},
		"private ask":    {"private", "request", "request"},
		"public open":    {"public", "open", "open"},
		"education open": {"education", "open", "open"},
	}
	ids := map[string]uuid.UUID{}
	for name, s := range seed {
		var id uuid.UUID
		handle := "m4" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
		if err := pool.QueryRow(ctx,
			`INSERT INTO communities (owner_id, handle, name, community_type, join_mode)
			 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			uuid.New(), handle, "m004 "+name, s.ctype, s.mode).Scan(&id); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		ids[name] = id
	}
	t.Cleanup(func() {
		for _, id := range ids {
			_, _ = pool.Exec(context.Background(), `DELETE FROM communities WHERE id = $1`, id)
		}
	})
	for run := 1; run <= 2; run++ {
		if _, err := pool.Exec(ctx, string(migration)); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		for name, s := range seed {
			var got string
			if err := pool.QueryRow(ctx, `SELECT join_mode FROM communities WHERE id = $1`, ids[name]).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != s.want {
				t.Fatalf("run %d, %s: join_mode = %q, want %q", run, name, got, s.want)
			}
		}
	}
}
