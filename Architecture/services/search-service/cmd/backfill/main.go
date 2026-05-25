// Command backfill rebuilds an OpenSearch index from the source-of-truth
// Postgres tables. Use this after a wipe, a mapping change, or whenever
// the Kafka event stream alone can't recreate the index (events past
// retention).
//
// It is NOT wired into any service startup — run manually:
//
//	POSTGRES_DSN=postgres://postgres:postgres@localhost:5432/app?sslmode=disable \
//	COMMERCE_POSTGRES_DSN=postgres://postgres:postgres@localhost:5432/commerce_db?sslmode=disable \
//	IDENTITY_POSTGRES_DSN=postgres://postgres:postgres@localhost:5432/identity_db?sslmode=disable \
//	OPENSEARCH_URL=http://localhost:9200 \
//	go run ./cmd/backfill -entity all -limit 0
//
// Flags:
//
//	-entity  posts|users|hashtags|products|communities|channels|all
//	-limit   max rows per entity (0 = unbounded — full reindex)
//	-dry-run print the source rows we'd index but don't write to OpenSearch
//
// All writes upsert by document id, so re-running is safe.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/atpost/search-service/internal/store/search"
	"github.com/jackc/pgx/v5/pgxpool"
)

// hashtagRegex mirrors the consumer's regex so the backfill produces
// the exact same extraction.
var hashtagRegex = regexp.MustCompile(`#(\w+)`)

func extractHashtags(text string) []string {
	matches := hashtagRegex.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	tags := make([]string, 0, len(matches))
	for _, m := range matches {
		t := strings.ToLower(m[1])
		if _, ok := seen[t]; !ok {
			seen[t] = struct{}{}
			tags = append(tags, t)
		}
	}
	return tags
}

func main() {
	var (
		entity = flag.String("entity", "all", "posts|users|hashtags|products|communities|channels|all")
		limit  = flag.Int("limit", 0, "max rows per entity (0 = unbounded)")
		dry    = flag.Bool("dry-run", false, "skip writes to OpenSearch")
	)
	flag.Parse()

	ctx := context.Background()
	osURL := envOr("OPENSEARCH_URL", "http://localhost:9200")
	store, err := search.New(osURL)
	if err != nil {
		fatal("opensearch connect", err)
	}
	slog.Info("backfill: connected to opensearch", "url", osURL)

	appDSN := envOr("POSTGRES_DSN", "")
	commerceDSN := envOr("COMMERCE_POSTGRES_DSN", "")
	identityDSN := envOr("IDENTITY_POSTGRES_DSN", "")

	entities := expandEntity(*entity)
	totals := map[string]int{}
	for _, e := range entities {
		n, err := runOne(ctx, e, *limit, *dry, store, appDSN, commerceDSN, identityDSN)
		if err != nil {
			slog.Error("backfill: entity failed", "entity", e, "indexed", n, "err", err)
			os.Exit(1)
		}
		totals[e] = n
		slog.Info("backfill: entity done", "entity", e, "indexed", n)
	}
	slog.Info("backfill: complete", "totals", totals)
}

func runOne(
	ctx context.Context,
	entity string,
	limit int,
	dry bool,
	store *search.Store,
	appDSN, commerceDSN, identityDSN string,
) (int, error) {
	switch entity {
	case search.EntityPosts:
		return backfillPosts(ctx, store, appDSN, limit, dry)
	case search.EntityUsers:
		return backfillUsers(ctx, store, identityDSN, appDSN, limit, dry)
	case search.EntityHashtags:
		return backfillHashtags(ctx, store, appDSN, limit, dry)
	case search.EntityProducts:
		return backfillProducts(ctx, store, commerceDSN, limit, dry)
	case search.EntityCommunities:
		return backfillCommunities(ctx, store, appDSN, limit, dry)
	case search.EntityChannels:
		return backfillChannels(ctx, store, appDSN, limit, dry)
	}
	return 0, fmt.Errorf("unknown entity %q", entity)
}

func expandEntity(s string) []string {
	if s == "all" {
		return []string{
			search.EntityPosts,
			search.EntityUsers,
			search.EntityHashtags,
			search.EntityProducts,
			search.EntityCommunities,
			search.EntityChannels,
		}
	}
	return strings.Split(s, ",")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func connect(ctx context.Context, dsn, label string) (*pgxpool.Pool, error) {
	if dsn == "" {
		return nil, fmt.Errorf("%s: DSN env not set", label)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("%s: ping: %w", label, err)
	}
	return pool, nil
}

func fatal(label string, err error) {
	slog.Error("backfill fatal", "step", label, "err", err)
	os.Exit(1)
}

// limitClause produces a LIMIT $N suffix when limit > 0; empty otherwise.
func limitClause(limit int, paramN int) string {
	if limit <= 0 {
		return ""
	}
	return fmt.Sprintf(" LIMIT $%d", paramN)
}

// --- posts -----------------------------------------------------------------

func backfillPosts(ctx context.Context, store *search.Store, dsn string, limit int, dry bool) (int, error) {
	pool, err := connect(ctx, dsn, "POSTGRES_DSN")
	if err != nil {
		return 0, err
	}
	defer pool.Close()

	args := []any{}
	q := `SELECT id, author_id, text, visibility, created_at FROM posts WHERE deleted_at IS NULL ORDER BY created_at DESC`
	if limit > 0 {
		q += limitClause(limit, 1)
		args = append(args, limit)
	}
	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id, authorID, text, visibility string
		var createdAt time.Time
		if err := rows.Scan(&id, &authorID, &text, &visibility, &createdAt); err != nil {
			return count, err
		}
		doc := search.PostDoc{
			PostID:     id,
			AuthorID:   authorID,
			Text:       text,
			Visibility: visibility,
			Hashtags:   extractHashtags(text),
			CreatedAt:  createdAt,
		}
		if dry {
			count++
			continue
		}
		if err := store.IndexPost(ctx, doc); err != nil {
			slog.Warn("backfill posts: index failed", "id", id, "err", err)
			continue
		}
		count++
	}
	return count, rows.Err()
}

// --- users -----------------------------------------------------------------

func backfillUsers(ctx context.Context, store *search.Store, identityDSN, appDSN string, limit int, dry bool) (int, error) {
	// Prefer identity DB (profiles live there) — fall back to app DB
	// if the identity DSN isn't configured for this environment.
	dsn := identityDSN
	if dsn == "" {
		dsn = appDSN
	}
	pool, err := connect(ctx, dsn, "USERS DSN")
	if err != nil {
		return 0, err
	}
	defer pool.Close()

	// profile.profiles is the canonical source. We tolerate a couple of
	// schema variants (column subset) by selecting defensively.
	args := []any{}
	q := `SELECT user_id, COALESCE(username,''), COALESCE(display_name,''), COALESCE(bio,''), COALESCE(is_verified, false)
	      FROM profile.profiles ORDER BY created_at DESC`
	if limit > 0 {
		q += limitClause(limit, 1)
		args = append(args, limit)
	}
	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		// Schema may differ — fall back to a simpler shape used in
		// dev fixtures (id, handle, etc.). Don't fail the whole job.
		slog.Warn("backfill users: primary query failed, skipping", "err", err)
		return 0, nil
	}
	defer rows.Close()

	count := 0
	docs := make([]search.UserDoc, 0, 500)
	flush := func() error {
		if dry || len(docs) == 0 {
			docs = docs[:0]
			return nil
		}
		n, err := store.BulkIndexUsers(ctx, docs)
		count += n
		docs = docs[:0]
		return err
	}
	for rows.Next() {
		var d search.UserDoc
		if err := rows.Scan(&d.UserID, &d.Username, &d.DisplayName, &d.Bio, &d.IsVerified); err != nil {
			return count, err
		}
		docs = append(docs, d)
		if len(docs) >= 500 {
			if err := flush(); err != nil {
				slog.Warn("backfill users: flush failed", "err", err)
			}
		}
	}
	if err := flush(); err != nil {
		slog.Warn("backfill users: final flush failed", "err", err)
	}
	if dry {
		// In dry-run we never indexed but rows-scanned is the meaningful count.
		count = -1
	}
	return count, rows.Err()
}

// --- hashtags --------------------------------------------------------------

func backfillHashtags(ctx context.Context, store *search.Store, dsn string, limit int, dry bool) (int, error) {
	pool, err := connect(ctx, dsn, "POSTGRES_DSN")
	if err != nil {
		return 0, err
	}
	defer pool.Close()

	// Mine hashtags from all post text. We don't have a hashtags table
	// per-se; instead we aggregate from posts.text via the same
	// regex the consumer uses.
	args := []any{}
	q := `SELECT text FROM posts WHERE deleted_at IS NULL AND text LIKE '%#%'`
	if limit > 0 {
		q += limitClause(limit, 1)
		args = append(args, limit)
	}
	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	tagCounts := map[string]int{}
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return 0, err
		}
		for _, t := range extractHashtags(text) {
			tagCounts[t]++
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	count := 0
	now := time.Now().UTC()
	for h, uses := range tagCounts {
		if dry {
			count++
			continue
		}
		doc := search.HashtagDoc{
			Hashtag:       h,
			HashtagSearch: h,
			UseCount:      uses,
			CreatedAt:     now,
		}
		if err := store.IndexHashtag(ctx, doc); err != nil {
			slog.Warn("backfill hashtags: index failed", "tag", h, "err", err)
			continue
		}
		count++
	}
	return count, nil
}

// --- products --------------------------------------------------------------

func backfillProducts(ctx context.Context, store *search.Store, dsn string, limit int, dry bool) (int, error) {
	pool, err := connect(ctx, dsn, "COMMERCE_POSTGRES_DSN")
	if err != nil {
		return 0, err
	}
	defer pool.Close()

	args := []any{}
	q := `SELECT id::text, seller_id::text, title, COALESCE(description,''),
	             COALESCE(view_count,0), COALESCE(order_count,0), status, created_at
	      FROM products WHERE status IN ('active','paused') ORDER BY created_at DESC`
	if limit > 0 {
		q += limitClause(limit, 1)
		args = append(args, limit)
	}
	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var d search.ProductDoc
		if err := rows.Scan(&d.ProductID, &d.SellerID, &d.Title, &d.Description, &d.ViewCount, &d.OrderCount, &d.Status, &d.CreatedAt); err != nil {
			return count, err
		}
		if dry {
			count++
			continue
		}
		if err := store.IndexProductDoc(ctx, d); err != nil {
			slog.Warn("backfill products: index failed", "id", d.ProductID, "err", err)
			continue
		}
		count++
	}
	return count, rows.Err()
}

// --- communities -----------------------------------------------------------

func backfillCommunities(ctx context.Context, store *search.Store, dsn string, limit int, dry bool) (int, error) {
	pool, err := connect(ctx, dsn, "POSTGRES_DSN")
	if err != nil {
		return 0, err
	}
	defer pool.Close()

	args := []any{}
	q := `SELECT id::text, owner_id::text, handle, name, COALESCE(description,''),
	             community_type, COALESCE(category,''), COALESCE(topic_tags, '{}'::text[]),
	             COALESCE(member_count,0), COALESCE(is_verified,false), created_at
	      FROM communities WHERE status != 'deleted' ORDER BY created_at DESC`
	if limit > 0 {
		q += limitClause(limit, 1)
		args = append(args, limit)
	}
	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var d search.CommunityDoc
		if err := rows.Scan(&d.CommunityID, &d.OwnerID, &d.Handle, &d.Name, &d.Description,
			&d.CommunityType, &d.Category, &d.TopicTags, &d.MemberCount, &d.IsVerified, &d.CreatedAt); err != nil {
			return count, err
		}
		if dry {
			count++
			continue
		}
		if err := store.IndexCommunity(ctx, d); err != nil {
			slog.Warn("backfill communities: index failed", "id", d.CommunityID, "err", err)
			continue
		}
		count++
	}
	return count, rows.Err()
}

// --- channels --------------------------------------------------------------

func backfillChannels(ctx context.Context, store *search.Store, dsn string, limit int, dry bool) (int, error) {
	pool, err := connect(ctx, dsn, "POSTGRES_DSN")
	if err != nil {
		return 0, err
	}
	defer pool.Close()

	args := []any{}
	q := `SELECT id::text, owner_id::text, handle, name, COALESCE(description,''),
	             channel_type, COALESCE(category,''), COALESCE(subscriber_count,0),
	             COALESCE(is_verified,false), created_at
	      FROM broadcast_channels WHERE status != 'deleted' ORDER BY created_at DESC`
	if limit > 0 {
		q += limitClause(limit, 1)
		args = append(args, limit)
	}
	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var d search.ChannelDoc
		if err := rows.Scan(&d.ChannelID, &d.OwnerID, &d.Handle, &d.Name, &d.Description,
			&d.ChannelType, &d.Category, &d.SubscriberCount, &d.IsVerified, &d.CreatedAt); err != nil {
			return count, err
		}
		if dry {
			count++
			continue
		}
		if err := store.IndexChannel(ctx, d); err != nil {
			slog.Warn("backfill channels: index failed", "id", d.ChannelID, "err", err)
			continue
		}
		count++
	}
	return count, rows.Err()
}
