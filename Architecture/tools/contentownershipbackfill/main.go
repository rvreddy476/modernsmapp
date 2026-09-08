// Command contentownershipbackfill projects analytics.content_ownership for
// posts that already exist in `posts` but were never announced to analytics.
//
// # WHY THIS TOOL EXISTS
//
// analytics.content_ownership is the projection that answers "who made this
// and what kind is it". IngestService rebuilds every client event's
// attribution from it and REFUSES any event whose content has no row —
// the caller gets 422 CONTENT_NOT_READY (internal/http/handler.go), because
// GetContentOwnership returns ErrContentNotProjected and IngestEvents
// propagates it before a single row is written.
//
// Exactly one thing populates that table: the Kafka consumer on PostCreated
// in services/analytics-service/internal/consumers/content_ownership.go.
// So a post created before that consumer existed, or while it was down, or
// past Kafka's retention, has no row and can never be measured. Nothing
// else backfills it. This does.
//
// The blast radius is the whole monetization chain: the creator fund reads
// analytics.content_daily_summary, whose creator_id and content_type are
// written by the hourly aggregator from the event payloads that
// IngestService stamps off this projection; CQS is computed on the same
// aggregates; and feed-service's `post:cqs` ranking signal reads what those
// aggregates produced. An unmeasurable post is worth nothing, scores
// nothing, and ranks on nothing.
//
// # WHY THIS IS A SQL JOIN AND identityrolebackfill IS NOT
//
// identityrolebackfill has to cross an HTTP hop because identity lives in
// `identity_db` and its sources live in `commerce_db` and `app`. Here both
// tables — `public.posts` and `analytics.content_ownership` — live in the
// SAME database (`app`), so the whole candidate set is one join and the
// tool needs no service to be up.
//
// # WRITE SEMANTICS ARE NOT THIS TOOL'S TO INVENT
//
// The INSERT below is copied verbatim from Store.UpsertContentOwnership
// (services/analytics-service/internal/store/postgres/events.go), including
// the WHERE on the conflict path. That WHERE is the point: ownership is
// IMMUTABLE. A row whose creator disagrees is refused rather than silently
// moving a creator's historical analytics to another account, and
// RowsAffected() == 0 is how the refusal surfaces.
//
// This tool therefore never "skips a conflict". It detects them before it
// writes anything and reports them as their own category, because a
// conflict means two sources disagree about who made a piece of content and
// a human has to look at that.
//
// # USAGE
//
// DRY RUN IS THE DEFAULT. Nothing is written without -apply.
//
//	contentownershipbackfill \
//	  -dsn='postgres://postgres:postgres@127.0.0.1:5432/app?sslmode=disable'
//
// Add -apply to actually project. Add -v to list the exact rows behind each
// category. Add -batch to change the write/scan batch size.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// upsertSQL is Store.UpsertContentOwnership, character for character.
//
// If you change it here, you have changed it in only one of the two places
// it exists and the backfill no longer agrees with the live consumer.
//
//	mirrors: services/analytics-service/internal/store/postgres/events.go
const upsertSQL = `
	INSERT INTO analytics.content_ownership
		(content_id, creator_id, content_type, created_at, projected_at)
	VALUES ($1, $2, $3, $4, NOW())
	ON CONFLICT (content_id) DO UPDATE SET
		content_type = EXCLUDED.content_type,
		projected_at = NOW()
	WHERE analytics.content_ownership.creator_id = EXCLUDED.creator_id`

// scanSQL loads one keyset-paginated page of posts with everything the
// decision needs attached.
//
// Keyset on (created_at, id) rather than OFFSET: -apply mutates
// content_ownership as it walks, and an OFFSET scan over a table being
// written to can skip rows. (created_at, id) is stable because neither
// column is ever rewritten.
//
// Every LEFT JOIN is 1:1 — users.id, content_ownership.content_id and
// video_metadata.post_id are all primary keys — so the page size is exactly
// the number of posts scanned.
const scanSQL = `
	SELECT p.id,
	       p.author_id,
	       p.content_type,
	       p.created_at,
	       p.visibility,
	       p.review_status,
	       (p.deleted_at IS NOT NULL)  AS deleted,
	       p.publish_at,
	       p.content_type_explicit,
	       (u.id IS NOT NULL)          AS author_known,
	       o.creator_id,
	       o.content_type              AS owned_content_type,
	       v.upload_status,
	       v.final_category
	  FROM posts p
	  LEFT JOIN users u
	         ON u.id = p.author_id
	  LEFT JOIN analytics.content_ownership o
	         ON o.content_id = p.id
	  LEFT JOIN video_metadata v
	         ON v.post_id = p.id
	 WHERE (p.created_at, p.id) > ($1, $2)
	 ORDER BY p.created_at, p.id
	 LIMIT $3`

func main() {
	var (
		dsn = flag.String("dsn", "",
			"Postgres DSN for the database holding BOTH public.posts and analytics.content_ownership (that is `app`)")
		apply = flag.Bool("apply", false,
			"actually project ownership. WITHOUT THIS NOTHING IS WRITTEN.")
		batch = flag.Int("batch", 500,
			"rows per scan page and per write batch")
		limit = flag.Int("limit", 0,
			"stop after N posts (0 = all)")
		verbose = flag.Bool("v", false,
			"list the exact rows behind each category")
		includeUnknownAuthors = flag.Bool("include-unknown-authors", false,
			"project posts whose author_id has no row in public.users. OFF by default; see the report for why")
		skipDeleted = flag.Bool("skip-deleted", false,
			"do not project soft-deleted posts (deleted_at IS NOT NULL). ON would be the cautious choice; see the comment on qualify()")
		pause = flag.Duration("pause", 0,
			"sleep between write batches; use to be gentle on a busy database")
	)
	flag.Parse()

	if *dsn == "" {
		fail("-dsn is required")
	}
	if *batch < 1 {
		fail("-batch must be at least 1")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		fail("connect: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		fail("ping: %v", err)
	}

	dbName, err := assertSchema(ctx, pool)
	if err != nil {
		fail("%v", err)
	}

	pol := policy{
		includeUnknownAuthors: *includeUnknownAuthors,
		skipDeleted:           *skipDeleted,
	}

	fmt.Printf("database      %s (%s)\n", dbName, redactDSN(*dsn))
	fmt.Printf("source        public.posts\n")
	fmt.Printf("target        analytics.content_ownership\n")
	fmt.Printf("mirrors       services/analytics-service/internal/store/postgres/events.go (UpsertContentOwnership)\n")
	fmt.Printf("              services/analytics-service/internal/consumers/content_ownership.go\n")
	fmt.Printf("batch         %d\n", *batch)
	fmt.Printf("unknown authors  %s\n", onOff(pol.includeUnknownAuthors, "projected", "SKIPPED"))
	fmt.Printf("soft-deleted     %s\n", onOff(!pol.skipDeleted, "projected", "SKIPPED"))
	if *apply {
		fmt.Printf("mode          APPLY — ownership rows will be written\n\n")
	} else {
		fmt.Printf("mode          DRY RUN — nothing will be written (pass -apply to project)\n\n")
	}

	t := run(ctx, pool, pol, *apply, *batch, *limit, *pause)
	report(t, *apply, *verbose, pol)

	// Non-zero when a human has to look: an ownership conflict is a real
	// disagreement about authorship, and a write failure means the run is
	// incomplete. Both must be visible to a scheduled invocation, not just
	// to whoever happens to read stdout.
	if len(t.conflicts) > 0 || len(t.writeFailed) > 0 {
		os.Exit(1)
	}
}

// assertSchema refuses to run against a database that is not shaped like
// the one this tool is about. The identity backfill only printed the
// database it *expected*; a wrong -dsn there produced a confusing SQL error.
// Here a wrong -dsn is caught with a sentence a human can act on.
func assertSchema(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	var dbName string
	if err := pool.QueryRow(ctx, `SELECT current_database()`).Scan(&dbName); err != nil {
		return "", fmt.Errorf("read current_database(): %w", err)
	}
	for _, rel := range []string{"public.posts", "public.users", "analytics.content_ownership", "public.video_metadata"} {
		var ok bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, rel).Scan(&ok); err != nil {
			return "", fmt.Errorf("probe %s: %w", rel, err)
		}
		if !ok {
			return "", fmt.Errorf("database %q has no %s. "+
				"This tool joins posts to analytics.content_ownership in ONE database; "+
				"point -dsn at the one holding both (that is `app`)", dbName, rel)
		}
	}
	return dbName, nil
}

// ---------------------------------------------------------------------------
// scanning
// ---------------------------------------------------------------------------

type cursor struct {
	createdAt time.Time
	id        uuid.UUID
}

func scanPage(ctx context.Context, pool *pgxpool.Pool, after cursor, size int) ([]postRow, error) {
	rows, err := pool.Query(ctx, scanSQL, after.createdAt, after.id, size)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []postRow
	for rows.Next() {
		var (
			p            postRow
			publishAt    *time.Time
			ownerID      *uuid.UUID
			ownerType    *string
			uploadStatus *string
			finalCat     *string
		)
		if err := rows.Scan(
			&p.ID, &p.AuthorID, &p.ContentType, &p.CreatedAt,
			&p.Visibility, &p.ReviewStatus, &p.Deleted, &publishAt,
			&p.ContentTypeExplicit, &p.AuthorKnown,
			&ownerID, &ownerType, &uploadStatus, &finalCat,
		); err != nil {
			return nil, err
		}
		if publishAt != nil {
			p.PublishAt = *publishAt
		}
		if ownerID != nil {
			p.HasOwnership = true
			p.OwnerCreatorID = *ownerID
			if ownerType != nil {
				p.OwnerContentType = *ownerType
			}
		}
		if uploadStatus != nil {
			p.VideoUploadStatus = *uploadStatus
		}
		if finalCat != nil {
			p.VideoFinalCategory = *finalCat
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// running
// ---------------------------------------------------------------------------

type tally struct {
	scanned int

	skippedUnknownAuthor []string
	skippedDeleted       []string
	skippedInvalid       []string
	alreadyProjected     int

	wouldProject []string // dry run
	wouldRetype  []string // dry run
	projected    int      // apply
	retyped      int      // apply

	conflicts   []string
	writeFailed []string

	// byContentType counts what would be (or was) written, per content_type.
	// The RPM rate sheet and the creator fund both match content_type as an
	// exact string, so this line is the one that says how much of the
	// backfill is monetizable at all.
	byContentType map[string]int

	// classificationDrift is REPORT ONLY. See detectDrift.
	classificationDrift []string

	// visibility / review breakdowns, so the operator sees what a
	// deliberately unfiltered qualification actually let through.
	byVisibility   map[string]int
	byReviewStatus map[string]int
	scheduled      int
}

func newTally() tally {
	return tally{
		byContentType:  map[string]int{},
		byVisibility:   map[string]int{},
		byReviewStatus: map[string]int{},
	}
}

// pending is one row the write phase will attempt.
type pending struct {
	row  postRow
	want string // the content_type to write
	v    verdict
}

func run(ctx context.Context, pool *pgxpool.Pool, pol policy, apply bool, batch, limit int, pause time.Duration) tally {
	t := newTally()
	// Year 1: before any created_at a post can hold, so the first page is
	// the whole table from the start. (A row with a zero created_at would
	// fall below this cursor and never be scanned — which is the same
	// outcome as qualify()'s verdictSkipInvalid for one, and created_at is
	// NOT NULL in `posts` anyway.)
	after := cursor{createdAt: time.Time{}, id: uuid.Nil}

	for {
		if ctx.Err() != nil {
			fmt.Fprintf(os.Stderr, "\ninterrupted after %d posts; safe to re-run\n", t.scanned)
			break
		}
		size := batch
		if limit > 0 && t.scanned+size > limit {
			size = limit - t.scanned
		}
		if size <= 0 {
			break
		}

		page, err := scanPage(ctx, pool, after, size)
		if err != nil {
			fail("scan posts: %v", err)
		}
		if len(page) == 0 {
			break
		}
		after = cursor{createdAt: page[len(page)-1].CreatedAt, id: page[len(page)-1].ID}

		var writes []pending
		for _, p := range page {
			t.scanned++
			t.byVisibility[p.Visibility]++
			t.byReviewStatus[p.ReviewStatus]++
			if p.IsScheduled(time.Now()) {
				t.scheduled++
			}
			if detectDrift(p) {
				t.classificationDrift = append(t.classificationDrift,
					fmt.Sprintf("%s posts.content_type=%s explicit=%t video_metadata.final_category=%s",
						p.ID, p.ContentType, p.ContentTypeExplicit, p.VideoFinalCategory))
			}

			v, want := qualify(p, pol)
			switch v {
			case verdictSkipInvalid:
				t.skippedInvalid = append(t.skippedInvalid, p.describe(want))
			case verdictConflict:
				t.conflicts = append(t.conflicts, fmt.Sprintf(
					"%s posts.author_id=%s but content_ownership.creator_id=%s (post created %s)",
					p.ID, p.AuthorID, p.OwnerCreatorID, p.CreatedAt.UTC().Format(time.RFC3339)))
			case verdictSkipUnknownAuthor:
				t.skippedUnknownAuthor = append(t.skippedUnknownAuthor,
					fmt.Sprintf("%s author_id=%s", p.ID, p.AuthorID))
			case verdictSkipDeleted:
				t.skippedDeleted = append(t.skippedDeleted, fmt.Sprintf("%s author_id=%s", p.ID, p.AuthorID))
			case verdictAlreadyProjected:
				t.alreadyProjected++
			case verdictProject, verdictRetype:
				t.byContentType[want]++
				writes = append(writes, pending{row: p, want: want, v: v})
			}
		}

		if !apply {
			for _, w := range writes {
				line := w.row.describe(w.want)
				if w.v == verdictRetype {
					t.wouldRetype = append(t.wouldRetype, line+
						fmt.Sprintf(" (was content_type=%s)", w.row.OwnerContentType))
					continue
				}
				t.wouldProject = append(t.wouldProject, line)
			}
			continue
		}

		if len(writes) > 0 {
			writeBatch(ctx, pool, writes, &t)
			if pause > 0 {
				time.Sleep(pause)
			}
		}
	}
	return t
}

// writeBatch runs the upsert for one page. pgx sends a batch as a single
// pipelined implicit transaction, so a page lands whole or not at all — and
// because every statement is the idempotent upsert, a page that did not
// land is simply re-attempted by the next run.
func writeBatch(ctx context.Context, pool *pgxpool.Pool, writes []pending, t *tally) {
	b := &pgx.Batch{}
	for _, w := range writes {
		b.Queue(upsertSQL, w.row.ID, w.row.AuthorID, w.want, w.row.CreatedAt)
	}
	br := pool.SendBatch(ctx, b)
	defer br.Close()

	for _, w := range writes {
		tag, err := br.Exec()
		if err != nil {
			t.writeFailed = append(t.writeFailed, fmt.Sprintf("%s (%v)", w.row.ID, err))
			continue
		}
		if tag.RowsAffected() == 0 {
			// The upsert's WHERE refused it. We pre-detected conflicts, so
			// reaching here means the row was claimed by a different creator
			// between the scan and the write — the live consumer racing us.
			// Never a silent skip.
			t.conflicts = append(t.conflicts, fmt.Sprintf(
				"%s refused by the immutability guard at write time "+
					"(another creator claimed it after the scan); posts.author_id=%s",
				w.row.ID, w.row.AuthorID))
			continue
		}
		if w.v == verdictRetype {
			t.retyped++
			continue
		}
		t.projected++
	}
}

// ---------------------------------------------------------------------------
// reporting
// ---------------------------------------------------------------------------

func report(t tally, apply, verbose bool, pol policy) {
	fmt.Printf("scanned                          %6d posts\n", t.scanned)
	fmt.Printf("  skipped: unknown author        %6d\n", len(t.skippedUnknownAuthor))
	fmt.Printf("  skipped: soft-deleted          %6d\n", len(t.skippedDeleted))
	fmt.Printf("  skipped: unusable row          %6d\n", len(t.skippedInvalid))
	fmt.Printf("  already projected (identical)  %6d\n", t.alreadyProjected)
	if apply {
		fmt.Printf("  projected                      %6d\n", t.projected)
		fmt.Printf("  content_type corrected         %6d\n", t.retyped)
		fmt.Printf("  WRITE FAILED                   %6d\n", len(t.writeFailed))
	} else {
		fmt.Printf("  would project                  %6d\n", len(t.wouldProject))
		fmt.Printf("  would correct content_type     %6d\n", len(t.wouldRetype))
	}
	fmt.Printf("  OWNERSHIP CONFLICTS            %6d\n", len(t.conflicts))

	if len(t.byContentType) > 0 {
		fmt.Printf("\ncontent_type of the rows %s:\n", pastOrFuture(apply, "written", "that would be written"))
		for _, k := range sortedKeys(t.byContentType) {
			note := ""
			switch k {
			case "flick", "long_video":
				note = "  <- has an RPM rate; the creator fund can settle it"
			default:
				note = "  <- no RPM rate: settleDay() skips any content_type that is not flick/long_video"
			}
			fmt.Printf("  %-14s %6d%s\n", k, t.byContentType[k], note)
		}
	}

	fmt.Printf("\nwhat the unfiltered qualification let through:\n")
	fmt.Printf("  by visibility:")
	for _, k := range sortedKeys(t.byVisibility) {
		fmt.Printf("  %s=%d", k, t.byVisibility[k])
	}
	fmt.Printf("\n  by review_status:")
	for _, k := range sortedKeys(t.byReviewStatus) {
		fmt.Printf("  %s=%d", k, t.byReviewStatus[k])
	}
	fmt.Printf("\n  scheduled (publish_at in the future): %d\n", t.scheduled)
	fmt.Printf("  Neither is a filter. An ownership row grants nothing and hides nothing;\n")
	fmt.Printf("  it only makes a view attributable. Filtering on a MUTABLE column\n")
	fmt.Printf("  (visibility, review_status, publish_at) would recreate this exact bug the\n")
	fmt.Printf("  moment the column changed, because nothing re-runs the projection.\n")

	if len(t.skippedUnknownAuthor) > 0 {
		fmt.Printf("\n%d post(s) name an author_id with no row in public.users. These were SKIPPED.\n",
			len(t.skippedUnknownAuthor))
		fmt.Printf("analytics.content_ownership has NO foreign key on creator_id, so nothing in the\n")
		fmt.Printf("schema would have stopped them, and idx_content_ownership_creator — the index the\n")
		fmt.Printf("creator fund and the 90-day eligibility scan walk — would then be full of\n")
		fmt.Printf("accounts that do not exist. Pass -include-unknown-authors to project them anyway.\n")
	}
	if pol.includeUnknownAuthors {
		fmt.Printf("\n-include-unknown-authors was set: posts whose author is not in public.users\n")
		fmt.Printf("were projected. public.users is what was checked; identity (identity_db.auth.users)\n")
		fmt.Printf("is a different database and this tool does not reach it.\n")
	}

	if len(t.classificationDrift) > 0 {
		fmt.Printf("\n%d post(s) whose stored content_type disagrees with the measured video.\n",
			len(t.classificationDrift))
		fmt.Printf("REPORTED, NOT CHANGED. shared/postclassify is the one classifier and post-service\n")
		fmt.Printf("applies it; a backfill that re-decided here would become a second, disagreeing\n")
		fmt.Printf("answer to \"what kind of post is this\" — the thing model/view_rules.go was\n")
		fmt.Printf("rewritten to stop. The fix belongs in post-service's MediaTranscodeConsumer,\n")
		fmt.Printf("which will then fan PostContentTypeChanged out and correct the row this tool wrote.\n")
	}

	if len(t.conflicts) > 0 {
		fmt.Printf("\n%d OWNERSHIP CONFLICT(S). Nothing was written for these.\n", len(t.conflicts))
		fmt.Printf("posts and analytics.content_ownership disagree about who made the content.\n")
		fmt.Printf("Ownership is immutable by design: UpsertContentOwnership's ON CONFLICT carries\n")
		fmt.Printf("`WHERE content_ownership.creator_id = EXCLUDED.creator_id`, so reassigning a\n")
		fmt.Printf("creator's historical analytics is refused rather than done quietly. A human has\n")
		fmt.Printf("to decide which source is right before anything moves.\n")
		printList("CONFLICTS", t.conflicts)
	}

	if verbose {
		printList("skipped: unknown author", t.skippedUnknownAuthor)
		printList("skipped: soft-deleted", t.skippedDeleted)
		printList("classification drift (reported, not changed)", t.classificationDrift)
		printList("would project", t.wouldProject)
		printList("would correct content_type", t.wouldRetype)
	}
	printList("skipped: unusable row", t.skippedInvalid)
	printList("WRITE FAILED (re-run to retry)", t.writeFailed)

	if !apply {
		fmt.Printf("\nDry run. Nothing was written. Re-run with -apply to project.\n")
	}
}

// ---------------------------------------------------------------------------
// small helpers
// ---------------------------------------------------------------------------

func printList(label string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Printf("\n%s (%d):\n", label, len(items))
	for _, it := range items {
		fmt.Printf("  %s\n", it)
	}
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func onOff(b bool, yes, no string) string {
	if b {
		return yes
	}
	return no
}

func pastOrFuture(apply bool, past, future string) string {
	if apply {
		return past
	}
	return future
}

// redactDSN strips the password so the banner can be pasted into a ticket.
func redactDSN(dsn string) string {
	at := strings.LastIndex(dsn, "@")
	scheme := strings.Index(dsn, "://")
	if at < 0 || scheme < 0 || at < scheme {
		return dsn
	}
	return dsn[:scheme+3] + "***@" + dsn[at+1:]
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "contentownershipbackfill: "+format+"\n", args...)
	os.Exit(2)
}
