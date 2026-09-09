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

	"github.com/atpost/search-service/internal/commerceclient"
	"github.com/atpost/search-service/internal/reindex"
	"github.com/atpost/search-service/internal/store/search"
	"github.com/atpost/shared/events"
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

// postHashtags returns the tags a post document should carry.
//
// WHY THIS IS NOT JUST extractHashtags(text):
//
// posts.hashtags is the authoritative array — it is what post-service
// persists and what /v1/hashtags/* serves. The composer writes it from a
// STRUCTURED tag field, so a post can carry tags that never appear as
// "#tag" anywhere in its body. Deriving the index's tags by regex over the
// text alone therefore drops every structurally-tagged post on the floor:
// on the dev rig, 3 of the 7 tagged posts indexed with no hashtags at all,
// and the tags "momentum", "worker", "test", "my" and "bangaram" existed in
// Postgres while being unfindable through search.
//
// The stored column leads (it is the source of truth and preserves the
// author's chosen order); inline "#tag" text mentions are unioned in
// behind it, because a tag typed into the body and never registered in the
// column is still a tag the author wrote. Both sides are normalized to
// lowercase, stripped of a leading '#', and de-duplicated, so the terms
// aggregation in SearchHashtags sees one bucket per tag.
func postHashtags(stored []string, text string) []string {
	out := make([]string, 0, len(stored)+4)
	seen := make(map[string]struct{}, len(stored)+4)
	add := func(raw string) {
		t := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "#")))
		if t == "" {
			return
		}
		if _, ok := seen[t]; ok {
			return
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	for _, s := range stored {
		add(s)
	}
	for _, s := range extractHashtags(text) {
		add(s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
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

// backfillPosts rebuilds posts_v1 from Postgres, which is the source of
// truth, and is ALSO the M2-P0-2 reconciler.
//
// It previously selected every non-deleted post and indexed it, ignoring
// visibility and moderation state entirely. That made a rebuild a
// re-exposure event: a post held at 'pending' by the Module 1 safety gate,
// or a followers-only post, would be published to the public index by an
// operator running a routine reindex.
//
// Now it walks the SAME eligibility predicate the consumer uses and
// reconciles in both directions:
//
//	eligible   → upsert with the row's current review_status and search_rev
//	ineligible → DELETE from the index (repairing drift left behind by a
//	             lost, failed, or dead-lettered eligibility event)
//
// The ineligible→delete direction is what lets the post-cutover audit
// assert zero ineligible indexed documents. Deletion is issued even in
// -dry-run=false only; -dry-run reports what it would do.
func backfillPosts(ctx context.Context, store *search.Store, dsn string, limit int, dry bool) (int, error) {
	pool, err := connect(ctx, dsn, "POSTGRES_DSN")
	if err != nil {
		return 0, err
	}
	defer pool.Close()

	args := []any{}
	// Result-row projection (title, first attached asset, longest video
	// duration) is read here exactly as post-service puts it on the
	// PostCreated / eligibility events, so a rebuilt document matches a
	// live one.
	q := `SELECT p.id, p.author_id, p.text, p.visibility,
	             COALESCE(p.review_status, ''), COALESCE(p.search_rev, 1),
	             COALESCE(p.content_type, ''), p.created_at,
	             (p.deleted_at IS NOT NULL) AS is_deleted,
	             (p.publish_at IS NOT NULL) AS is_scheduled,
	             COALESCE(p.title, ''),
	             COALESCE(p.hashtags, ARRAY[]::text[]),
	             COALESCE((SELECT pm.media_id::text FROM post_media pm
	                       WHERE pm.post_id = p.id ORDER BY pm.position LIMIT 1), ''),
	             COALESCE((SELECT pm.kind FROM post_media pm
	                       WHERE pm.post_id = p.id ORDER BY pm.position LIMIT 1), ''),
	             COALESCE((SELECT MAX(COALESCE(ma.duration_ms, ma.duration_seconds * 1000, 0))
	                       FROM post_media pm JOIN media_assets ma ON ma.id = pm.media_id
	                       WHERE pm.post_id = p.id AND pm.kind = 'video'), 0)::int
	      FROM posts p
	      ORDER BY p.created_at DESC`
	if limit > 0 {
		q += limitClause(limit, 1)
		args = append(args, limit)
	}
	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var indexed, removed, skipped int
	// Hashtag honesty counters, in the spirit of the product reindex's
	// index_total/orphans: report what the run could NOT repair, not only
	// what it wrote.
	//
	// columnOnly counts eligible posts whose tags exist only in
	// posts.hashtags and appear nowhere as "#tag" in the body. Those are
	// exactly the posts the LIVE Kafka path cannot index correctly, because
	// PostCreatedPayload / PostSearchEligibilityChangedPayload carry no
	// hashtags field and the consumer can only regex the text. A backfill
	// repairs them; the next edit through the live path loses them again.
	var withTags, columnOnly int
	for rows.Next() {
		var id, authorID, text, visibility, reviewStatus, contentType string
		var title, mediaID, mediaKind string
		var storedHashtags []string
		var durationMs int
		var searchRev int64
		var createdAt time.Time
		var isDeleted, isScheduled bool
		if err := rows.Scan(&id, &authorID, &text, &visibility,
			&reviewStatus, &searchRev, &contentType, &createdAt, &isDeleted, &isScheduled,
			&title, &storedHashtags, &mediaID, &mediaKind, &durationMs); err != nil {
			return indexed, err
		}

		// The one eligibility rule, shared with the consumer so the
		// rebuild can never be more permissive than the live path.
		// A scheduled post (publish_at set) is not public yet either.
		if isScheduled || !events.SearchEligible(visibility, reviewStatus, isDeleted) {
			if dry {
				skipped++
				continue
			}
			// Only repair actual drift. Writing a tombstone for every
			// ineligible row would add a document for every private and
			// pending post in the database — most of which were never
			// indexed and need no marker. Checking existence first keeps
			// the index proportional to public content.
			_, exists, err := store.GetPostSearchRev(ctx, id)
			if err != nil {
				slog.Warn("backfill posts: reconcile read failed", "id", id, "err", err)
				continue
			}
			if !exists {
				skipped++
				continue
			}
			// AutoRev, not a revision computed here. The reconciler races
			// the live consumer by definition — it runs while events are
			// still flowing — so computing "current+1" in this process and
			// then writing would be the same compare-then-write race the
			// consumer used to have. Letting OpenSearch stamp storedRev+1
			// under the document lock means a repair can never clobber a
			// newer transition that landed while we were deciding.
			if err := store.ApplyPostProjection(ctx, search.PostProjection{
				PostID:   id,
				AutoRev:  true,
				Removed:  true,
				AuthorID: authorID,
			}); err != nil {
				slog.Warn("backfill posts: reconcile removal failed",
					"id", id, "visibility", visibility,
					"review_status", reviewStatus, "err", err)
				continue
			}
			slog.Warn("backfill posts: removed ineligible indexed document",
				"id", id, "visibility", visibility, "review_status", reviewStatus,
				"deleted", isDeleted)
			removed++
			continue
		}

		tags := postHashtags(storedHashtags, text)
		if len(tags) > 0 {
			withTags++
			if len(extractHashtags(text)) < len(tags) {
				columnOnly++
			}
		}

		if dry {
			indexed++
			continue
		}

		// Re-review v2 P0-1: this must go through the author-fence
		// handshake, not the bare projection.
		//
		// The reconciler reads a PostgreSQL statement snapshot and writes
		// long afterwards, which is the exact shape the fence exists to
		// catch:
		//
		//	1. a public+approved row is not yet in posts_v1
		//	2. backfill reads it as eligible
		//	3. the account is deleted; the fence lands and the sweep runs,
		//	   but the absent post is not in the sweep snapshot
		//	4. backfill writes its stale row and creates a public document
		//
		// There is no per-post erasure marker to stop it — the post did
		// not exist when the sweep ran — so only the author-level check
		// plus recheck can. A bare ApplyPostProjection here resurrected a
		// deleted account's content.
		//
		// Reproject: the row's revision has not moved, but the projection
		// shape may have (result-row fields, 2026-09-05), so an eligible
		// document is rewritten at its current revision. Removal ties and
		// erased authors still win — see PostProjection.Reproject.
		if err := store.IndexPostUnlessAuthorErased(ctx, search.PostProjection{
			PostID:    id,
			Rev:       searchRev,
			Reproject: true,
			Doc: search.PostDoc{
				PostID:       id,
				AuthorID:     authorID,
				Text:         text,
				Visibility:   visibility,
				ReviewStatus: reviewStatus,
				SearchRev:    searchRev,
				PostType:     contentType,
				ContentType:  contentType,
				Hashtags:     tags,
				CreatedAt:    createdAt,
				Title:        title,
				DurationMs:   durationMs,
				MediaID:      mediaID,
				MediaKind:    mediaKind,
			},
		}); err != nil {
			slog.Warn("backfill posts: index failed", "id", id, "err", err)
			continue
		}
		indexed++
	}
	slog.Info("backfill posts: reconciled",
		"indexed", indexed, "removed_ineligible", removed, "skipped_dry", skipped,
		"documents_with_hashtags", withTags,
		"hashtags_from_column_only", columnOnly)
	if columnOnly > 0 {
		slog.Warn("backfill posts: these documents will LOSE their hashtags on the next live update",
			"count", columnOnly,
			"why", "their tags live only in posts.hashtags and never appear as #tag in the body; "+
				"PostCreated / PostSearchEligibilityChanged carry no hashtags field, so the Kafka "+
				"consumer re-derives tags by regex over text and writes an empty array",
			"fix", "post-service must publish posts.hashtags on both payloads; until then a backfill "+
				"is the only path that indexes them and every edit undoes it")
	}
	return indexed, rows.Err()
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
	// created_at is selected, not just ordered by: BulkIndexUsers is a
	// full-document replace, so a column left out of this SELECT is a
	// column erased from every document the run touches. created_at drives
	// the gauss recency function in the ranked query.
	q := `SELECT user_id, COALESCE(username,''), COALESCE(display_name,''), COALESCE(bio,''),
	             COALESCE(is_verified, false), created_at
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

	// Usernames do NOT live in profile.profiles — that column is NULL for
	// every row on every environment checked (50/50 on the dev rig). The
	// authority is app.users.username, behind user-service. Because
	// BulkIndexUsers is a full-document replace, indexing the profile row
	// as-is does not merely fail to add a username: it OVERWRITES the one
	// that the UserRegistered event delivered, with "". That is why almost
	// every users_v1 document carries username:"" and handle search
	// matches nothing.
	//
	// So the profile row supplies display_name / bio / verified, and the
	// username is overlaid from app.users. Missing here means missing
	// everywhere, and is left empty rather than guessed.
	handles := loadUsernames(ctx, appDSN)

	count := 0
	missingUsername := 0
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
		var createdAt *time.Time
		if err := rows.Scan(&d.UserID, &d.Username, &d.DisplayName, &d.Bio, &d.IsVerified, &createdAt); err != nil {
			return count, err
		}
		d.CreatedAt = createdAt
		if d.Username == "" {
			d.Username = handles[d.UserID]
		}
		if d.Username == "" {
			missingUsername++
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
	if missingUsername > 0 {
		slog.Warn("backfill users: indexed documents with no username — these users are unfindable by handle",
			"count", missingUsername,
			"why", "neither profile.profiles.username nor app.users.username holds a value for them")
	}
	if dry {
		// In dry-run we never indexed but rows-scanned is the meaningful count.
		count = -1
	}
	return count, rows.Err()
}

// loadUsernames reads the authoritative user_id -> username map from
// app.users (user-service's table). Best-effort: an unreachable or
// differently-shaped app DB yields an empty map, which leaves every
// username exactly as the profile row had it rather than blanking it.
func loadUsernames(ctx context.Context, appDSN string) map[string]string {
	out := map[string]string{}
	if strings.TrimSpace(appDSN) == "" {
		slog.Warn("backfill users: POSTGRES_DSN unset — usernames cannot be resolved; " +
			"documents will index with an empty username")
		return out
	}
	pool, err := connect(ctx, appDSN, "APP DSN (usernames)")
	if err != nil {
		slog.Warn("backfill users: app DB unreachable; usernames not overlaid", "err", err)
		return out
	}
	defer pool.Close()

	rows, err := pool.Query(ctx,
		`SELECT id::text, COALESCE(NULLIF(username, ''), NULLIF(handle, ''), '')
		 FROM public.users`)
	if err != nil {
		slog.Warn("backfill users: username query failed; usernames not overlaid", "err", err)
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, username string
		if err := rows.Scan(&id, &username); err != nil {
			slog.Warn("backfill users: username scan failed", "err", err)
			return out
		}
		if username != "" {
			out[id] = username
		}
	}
	slog.Info("backfill users: usernames loaded from app.users", "resolved", len(out))
	return out
}

// --- hashtags --------------------------------------------------------------

// backfillHashtags PURGES the legacy hashtags_v1 counter index.
//
// M2-P0-4: hashtags_v1 was an increment-only projection. Nothing
// decremented it when a post was rejected, taken down, made private,
// edited, or deleted, so a tag from a once-approved post stayed
// discoverable forever — in autocomplete, in trending, and in the ranked
// hashtag entity — pointing at content the system had already hidden.
// Rebuilding it with correct counts would have fixed the snapshot and
// left the same defect running the moment the next post was removed.
//
// So the counter index is no longer read by anything. Every viewer-facing
// hashtag surface (SearchHashtags, autocomplete, the ranked hashtags
// entity) now derives from a live aggregation over posts_v1, which is
// reversible by construction. This step deletes the stale documents so
// nothing can quietly start reading them again, and so an audit of the
// index finds no residue of removed content.
func backfillHashtags(ctx context.Context, store *search.Store, dsn string, limit int, dry bool) (int, error) {
	if dry {
		slog.Info("backfill hashtags: dry run — would purge the legacy hashtags_v1 index")
		return 0, nil
	}
	purged, err := store.PurgeLegacyHashtagIndex(ctx)
	if err != nil {
		return 0, err
	}
	slog.Info("backfill hashtags: purged legacy counter index",
		"documents_removed", purged,
		"note", "hashtag surfaces now derive from the posts_v1 aggregation")
	return purged, nil
}

// --- products --------------------------------------------------------------

// backfillProducts walks commerce-service, not commerce's database.
//
// ─── WHAT THIS REPLACES, AND WHY ────────────────────────────────────────
//
// It used to hand-write a SELECT over commerce's `products` table:
//
//	SELECT id, seller_id, title, description, view_count, order_count,
//	       status, created_at
//	  FROM products WHERE status IN ('active','paused')
//
// Three things were wrong with that, and they compounded.
//
// It set NEITHER CATEGORY NOR PRICE. Both are real work — the price lives
// on the variants (cheapest active one, in paise, with a NULLIF dance for
// the rows migration 007 defaulted to zero) and the category needs a
// recursive walk to get the ancestor chain. So the index it produced could
// not be filtered by category or sorted by price, which is most of what a
// product search is for.
//
// It filtered on `status IN ('active','paused')` alone, where commerce's
// own shopper-facing rule is `status='active' AND approval_status='approved'`
// — so it indexed listings awaiting moderation and listings a moderator had
// rejected.
//
// And it was a SECOND opinion about a projection commerce already owns.
// Both problems above are what a second opinion looks like six months on.
//
// It now calls the same endpoint the reindex does, converts with the same
// productindex.Doc the Kafka consumer uses, and therefore cannot disagree
// with either. COMMERCE_POSTGRES_DSN is no longer read for products; the
// address is COMMERCE_SERVICE_URL.
func backfillProducts(ctx context.Context, store *search.Store, _ string, limit int, dry bool) (int, error) {
	baseURL := os.Getenv("COMMERCE_SERVICE_URL")
	if baseURL == "" {
		baseURL = "http://commerce-service:8109"
	}
	client := commerceclient.New(baseURL, os.Getenv("INTERNAL_SERVICE_KEY"))

	if dry {
		// A dry run reports what a real one would index, and the only
		// honest source for that is commerce's own count of live listings.
		page, err := client.ListProductSearchDocs(ctx, "", 1)
		if err != nil {
			return 0, err
		}
		if limit > 0 && page.VisibleTotal > limit {
			return limit, nil
		}
		return page.VisibleTotal, nil
	}

	res, err := reindex.ReindexProducts(ctx, client, store, slog.Default())
	if err != nil {
		return res.Indexed, err
	}
	return res.Indexed, nil
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
