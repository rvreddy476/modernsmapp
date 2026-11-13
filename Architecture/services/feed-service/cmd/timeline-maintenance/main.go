// timeline-maintenance repairs the feed's Scylla timelines in one pass over
// every row (internal/store/scylla/maintenance.go):
//
//   - collapses duplicate rows of a post onto its TimelineTS key
//   - deletes rows of posts older than 360 days, and ghost rows
//   - gives rows written before the TTL existed what is left of it
//   - indexes every kept row in timeline_index_by_post_v2
//
// Dry run by default: it reads everything and prints what it WOULD do.
// -apply carries it out. Safe to re-run and to stop at any time; -start
// resumes at the token range a stopped run printed last.
//
// After an -apply pass over both tables, deploy feed-service with
// FEED_INDEX_LEGACY_READ=false, then run once more with
// -truncate-legacy-index -apply to empty the unsharded index.
//
// Echo's notes_timeline (2026-11-13) has three modes:
//
//   - -tables notes: the same repair pass over notes_timeline.
//   - -purge-notes-author <uuid>: a pass over notes_timeline that deletes
//     every row that author wrote, from every follower's partition, with
//     its index entries, and touches nothing else.
//   - -rebuild-notes-user <uuid>: one user's notes_timeline rebuilt from
//     echo_notes and their follow list (service.RebuildNotesTimeline); also
//     needs POSTGRES_DSN and GRAPH_SERVICE_URL.
//
// Every mode is a dry run without -apply.
//
//	SCYLLA_HOSTS=... timeline-maintenance [-apply] [-tables home,author,notes]
//	    [-ranges 4096] [-workers 4] [-start 0]
//	SCYLLA_HOSTS=... timeline-maintenance -purge-notes-author <uuid> [-apply] [-start 0]
//	SCYLLA_HOSTS=... POSTGRES_DSN=... GRAPH_SERVICE_URL=... timeline-maintenance -rebuild-notes-user <uuid> [-apply]
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/atpost/feed-service/internal/service"
	"github.com/atpost/feed-service/internal/store/postgres"
	"github.com/atpost/feed-service/internal/store/scylla"
	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// validTableKinds are the -tables values.
var validTableKinds = map[string]bool{"home": true, "author": true, scylla.NotesTimelineKind: true}

// notesMode is the one notes-only mode a run asked for, or none.
type notesMode struct {
	purgeAuthor uuid.UUID
	rebuildUser uuid.UUID
}

// parseNotesMode reads the two notes flags: at most one, each a uuid.
func parseNotesMode(purgeAuthor, rebuildUser string) (notesMode, error) {
	var m notesMode
	purgeAuthor, rebuildUser = strings.TrimSpace(purgeAuthor), strings.TrimSpace(rebuildUser)
	if purgeAuthor != "" && rebuildUser != "" {
		return m, fmt.Errorf("-purge-notes-author and -rebuild-notes-user are separate runs")
	}
	var err error
	if purgeAuthor != "" {
		if m.purgeAuthor, err = uuid.Parse(purgeAuthor); err != nil || m.purgeAuthor == uuid.Nil {
			return m, fmt.Errorf("-purge-notes-author: %q is not a user id", purgeAuthor)
		}
	}
	if rebuildUser != "" {
		if m.rebuildUser, err = uuid.Parse(rebuildUser); err != nil || m.rebuildUser == uuid.Nil {
			return m, fmt.Errorf("-rebuild-notes-user: %q is not a user id", rebuildUser)
		}
	}
	return m, nil
}

func main() {
	var (
		apply        = flag.Bool("apply", false, "write the changes; without it, only count them")
		tables       = flag.String("tables", "home,author", "timelines to repair: any of home, author, notes")
		nRanges      = flag.Int("ranges", 4096, "token ranges to split each table scan into")
		workers      = flag.Int("workers", 4, "token ranges processed at once")
		start        = flag.Int("start", 0, "first token range to process (resume)")
		truncate     = flag.Bool("truncate-legacy-index", false, "only TRUNCATE the unsharded timeline_index_by_post (needs -apply)")
		purgeAuthor  = flag.String("purge-notes-author", "", "only delete this author's rows from every notes_timeline partition")
		rebuildUser  = flag.String("rebuild-notes-user", "", "only rebuild this user's notes_timeline (needs POSTGRES_DSN, GRAPH_SERVICE_URL)")
	)
	flag.Parse()
	notes, err := parseNotesMode(*purgeAuthor, *rebuildUser)
	if err != nil {
		log.Fatal(err)
	}

	hosts := os.Getenv("SCYLLA_HOSTS")
	if hosts == "" {
		hosts = "localhost"
	}
	cluster := gocql.NewCluster(strings.Split(hosts, ",")...)
	cluster.Keyspace = "social_feed"
	cluster.Consistency = gocql.Quorum
	cluster.Timeout = 30 * time.Second
	session, err := cluster.CreateSession()
	if err != nil {
		log.Fatalf("connect to scylla: %v", err)
	}
	defer session.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	m := scylla.NewMaintainer(session, *apply)

	mode := "DRY RUN (nothing is written; add -apply)"
	if *apply {
		mode = "APPLY"
	}

	if notes.rebuildUser != uuid.Nil {
		log.Printf("notes rebuild for %s: %s", notes.rebuildUser, mode)
		if err := rebuildNotes(ctx, session, notes.rebuildUser, *apply); err != nil {
			log.Fatal(err)
		}
		return
	}
	if notes.purgeAuthor != uuid.Nil {
		log.Printf("notes author purge of %s: %s, ranges=%d workers=%d start=%d", notes.purgeAuthor, mode, *nRanges, *workers, *start)
		ranges := scylla.SplitTokenRing(*nRanges)
		if *start < 0 || *start >= len(ranges) {
			log.Fatalf("-start %d is outside [0, %d)", *start, len(ranges))
		}
		m.PurgeNotesAuthor(notes.purgeAuthor)
		total, ok := runTable(ctx, m, scylla.NotesTimelineKind, ranges, *start, *workers)
		report(scylla.NotesTimelineKind, total)
		if !ok {
			os.Exit(1)
		}
		return
	}

	if *truncate {
		if !*apply {
			log.Println("dry run: would TRUNCATE social_feed.timeline_index_by_post (add -apply)")
			return
		}
		if err := m.TruncateLegacyIndex(ctx); err != nil {
			log.Fatalf("truncate legacy index: %v", err)
		}
		log.Println("truncated social_feed.timeline_index_by_post")
		return
	}

	log.Printf("timeline maintenance: %s, tables=%s ranges=%d workers=%d start=%d", mode, *tables, *nRanges, *workers, *start)

	ranges := scylla.SplitTokenRing(*nRanges)
	if *start < 0 || *start >= len(ranges) {
		log.Fatalf("-start %d is outside [0, %d)", *start, len(ranges))
	}
	failed := false
	for _, kind := range strings.Split(*tables, ",") {
		kind = strings.TrimSpace(kind)
		if !validTableKinds[kind] {
			log.Fatalf("-tables: %q is not home, author or notes", kind)
		}
		total, ok := runTable(ctx, m, kind, ranges, *start, *workers)
		report(kind, total)
		if !ok {
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

// runTable processes ranges[start:] of one table with a pool of workers.
// It stops handing out ranges at the first error or a signal, and reports
// the lowest range that did not finish so a re-run can -start there.
func runTable(ctx context.Context, m *scylla.Maintainer, kind string, ranges []scylla.TokenRange, start, workers int) (scylla.MaintenanceStats, bool) {
	if workers < 1 {
		workers = 1
	}
	var (
		mu         sync.Mutex
		total      scylla.MaintenanceStats
		done       int
		unfinished = -1
	)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				stats, err := m.Range(ctx, kind, ranges[i])
				mu.Lock()
				total.Merge(stats)
				if err != nil {
					log.Printf("%s range %d [%d, %d]: %v", kind, i, ranges[i].Start, ranges[i].End, err)
					if unfinished < 0 || i < unfinished {
						unfinished = i
					}
				} else {
					done++
					if done%256 == 0 {
						log.Printf("%s: %d/%d ranges, %d rows read", kind, done, len(ranges)-start, total.Rows)
					}
				}
				mu.Unlock()
			}
		}()
	}
feed:
	for i := start; i < len(ranges); i++ {
		mu.Lock()
		stopped := unfinished >= 0
		mu.Unlock()
		if stopped {
			break
		}
		select {
		case <-ctx.Done():
			mu.Lock()
			if unfinished < 0 || i < unfinished {
				unfinished = i
			}
			mu.Unlock()
			break feed
		case jobs <- i:
		}
	}
	close(jobs)
	wg.Wait()
	if unfinished >= 0 {
		log.Printf("%s: STOPPED. Re-run with -tables %s -start %d to resume.", kind, kind, unfinished)
		return total, false
	}
	return total, true
}

func report(kind string, s scylla.MaintenanceStats) {
	keys := make([]string, 0, len(s.Actions))
	for k := range s.Actions {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %d rows read", kind, s.Rows)
	for _, k := range keys {
		fmt.Fprintf(&b, ", %s=%d", k, s.Actions[k])
	}
	log.Print(b.String())
}

// rebuildNotes runs service.RebuildNotesTimeline for one user against the
// live stores: Scylla (this session), feed-service's Postgres (echo_notes)
// and graph-service (the follow list, GRAPH_SERVICE_URL).
func rebuildNotes(ctx context.Context, session *gocql.Session, userID uuid.UUID, apply bool) error {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		return fmt.Errorf("-rebuild-notes-user needs POSTGRES_DSN (feed-service's database, for echo_notes)")
	}
	if os.Getenv("GRAPH_SERVICE_URL") == "" {
		return fmt.Errorf("-rebuild-notes-user needs GRAPH_SERVICE_URL (the user's follow list)")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect to postgres: %w", err)
	}
	defer pool.Close()
	svc := service.New(scylla.New(session), postgres.New(pool), nil)
	st, err := svc.RebuildNotesTimeline(ctx, userID, apply)
	log.Printf("notes rebuild %s: following=%d pulled=%d written=%d scanned=%d stale=%d",
		userID, st.Following, st.Pulled, st.Written, st.Scanned, st.Stale)
	return err
}
