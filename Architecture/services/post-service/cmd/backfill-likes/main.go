// Command backfill-likes copies likes recorded before the 16 Oct 2026
// deploy from Scylla (reactions_by_post, reel_reactions) into Postgres
// reactions, which GET /v1/reels/liked reads. See internal/backfill.
//
// Dry run by default; pass -apply to write. Safe to run again: rows
// already in Postgres are kept.
//
//	POSTGRES_DSN=... SCYLLA_HOSTS=... backfill-likes [-apply] [-batch 500]
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/backfill"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/gocql/gocql"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	apply := flag.Bool("apply", false, "write the rows (default: dry run, rolled back)")
	batch := flag.Int("batch", 500, "rows per Postgres batch")
	flag.Parse()

	if err := run(*apply, *batch); err != nil {
		fmt.Fprintln(os.Stderr, "backfill-likes:", err)
		os.Exit(1)
	}
}

func run(apply bool, batch int) error {
	ctx := context.Background()
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		return fmt.Errorf("POSTGRES_DSN is required")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer pool.Close()

	hosts := os.Getenv("SCYLLA_HOSTS")
	if hosts == "" {
		hosts = "localhost"
	}
	cluster := gocql.NewCluster(strings.Split(hosts, ",")...)
	cluster.Keyspace = "social_engagement"
	cluster.Consistency = gocql.Quorum
	cluster.Timeout = 30 * time.Second
	session, err := cluster.CreateSession()
	if err != nil {
		return fmt.Errorf("scylla: %w", err)
	}
	defer session.Close()

	sources := []backfill.Source{
		newScyllaSource(session, "reactions_by_post",
			`SELECT post_id, user_id, reaction, created_at FROM reactions_by_post`, false),
		newScyllaSource(session, "reel_reactions",
			`SELECT reel_id, user_id, reaction, ts FROM reel_reactions`, true),
	}
	rep, err := backfill.Run(ctx, sources, pgWriter{postgres.New(pool)}, apply, batch, time.Now())
	out, _ := json.MarshalIndent(rep, "", "  ")
	fmt.Println(string(out))
	return err
}

// scyllaSource pages through one table. timeuuidTime reads the fourth
// column as a timeuuid (reel_reactions.ts) instead of a timestamp.
type scyllaSource struct {
	name         string
	iter         *gocql.Iter
	timeuuidTime bool
}

func newScyllaSource(s *gocql.Session, name, cql string, timeuuidTime bool) *scyllaSource {
	return &scyllaSource{name: name, iter: s.Query(cql).PageSize(1000).Iter(), timeuuidTime: timeuuidTime}
}

func (s *scyllaSource) Name() string { return s.name }

func (s *scyllaSource) Next() (string, string, string, time.Time, bool) {
	var post, user, reaction string
	if s.timeuuidTime {
		var ts gocql.UUID
		if !s.iter.Scan(&post, &user, &reaction, &ts) {
			return "", "", "", time.Time{}, false
		}
		var at time.Time
		if ts != (gocql.UUID{}) {
			at = ts.Time()
		}
		return post, user, reaction, at, true
	}
	var at time.Time
	if !s.iter.Scan(&post, &user, &reaction, &at) {
		return "", "", "", time.Time{}, false
	}
	return post, user, reaction, at, true
}

func (s *scyllaSource) Close() error { return s.iter.Close() }

type pgWriter struct{ store *postgres.Store }

func (w pgWriter) WriteBatch(ctx context.Context, rows []backfill.Row, apply bool) (int64, int64, error) {
	in := make([]postgres.BackfillReaction, len(rows))
	for i, r := range rows {
		in[i] = postgres.BackfillReaction{PostID: r.PostID, UserID: r.UserID, Reaction: r.Reaction, CreatedAt: r.CreatedAt}
	}
	return w.store.BackfillPostReactions(ctx, in, apply)
}
