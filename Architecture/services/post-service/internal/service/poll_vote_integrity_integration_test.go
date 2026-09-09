//go:build integration

package service

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/atpost/post-service/database"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Poll voting against a real Postgres (2026-09-09).
//
//	createdb post_polls_it_test
//	pg_dump --schema-only --no-owner --no-privileges app | psql post_polls_it_test
//	pg_dump --data-only --table=schema_migrations app | psql post_polls_it_test
//	POLL_POSTGRES_DSN=postgres://…/post_polls_it_test \
//	    go test -tags integration ./internal/service/ -run PollVote
//
// NEVER point this at `app`, `commerce_db` or `identity_db` — it writes posts,
// polls and votes.
//
// The two pg_dump lines are what makes the scratch database a fair copy: the
// post-service schema reaches across into tables it does not own (users,
// user_preferences, media_assets, …), and copying schema_migrations means
// BootstrapSchema below applies exactly the migrations production has not run
// yet. So this run proves migration 045 against a poll_votes shaped the way
// the live one was — no foreign key, a primary key of
// (post_id, user_id, option_id) and nothing else.
//
// Every case here is a defect that was live on the dev stack on 2026-09-09
// and that the happy path would not have caught: the happy path passed
// throughout.

func openPollDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("POLL_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POLL_POSTGRES_DSN not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	// Cross-service tables post-service's schema reaches for but does not
	// own. media_assets carries the FKs; user_preferences is altered by
	// migration 009. On a scratch database neither exists, and the bootstrap
	// stops on the first one it cannot find.
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS media_assets (
			id UUID PRIMARY KEY,
			uploader_id UUID NOT NULL,
			file_type TEXT NOT NULL,
			processing_status TEXT NOT NULL,
			moderation_status TEXT NOT NULL DEFAULT 'pending',
			duration_seconds INTEGER,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS user_preferences (
			user_id UUID PRIMARY KEY,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
	} {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			pool.Close()
			t.Fatal(err)
		}
	}
	if err := postgres.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		pool.Close()
		t.Fatalf("bootstrap schema: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

type testPoll struct {
	postID  uuid.UUID
	options []uuid.UUID
}

// newTestPoll creates a post with a two-option poll and returns its ids.
// endsAt nil means an open poll.
func newTestPoll(t *testing.T, pool *pgxpool.Pool, allowsMultiple bool, endsAt *time.Time) testPoll {
	t.Helper()
	ctx := context.Background()
	postID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO posts (id, author_id, text, visibility, content_type, created_at, updated_at)
		VALUES ($1, $2, 'poll', 'public', 'poll', NOW(), NOW())
	`, postID, uuid.New()); err != nil {
		t.Fatalf("insert post: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO polls (post_id, question, allows_multiple, ends_at, created_at)
		VALUES ($1, 'which?', $2, $3, NOW())
	`, postID, allowsMultiple, endsAt); err != nil {
		t.Fatalf("insert poll: %v", err)
	}
	p := testPoll{postID: postID}
	for i, label := range []string{"Teal", "Orange"} {
		optID := uuid.New()
		if _, err := pool.Exec(ctx, `
			INSERT INTO poll_options (id, post_id, label, sort_order) VALUES ($1, $2, $3, $4)
		`, optID, postID, label, i); err != nil {
			t.Fatalf("insert option: %v", err)
		}
		p.options = append(p.options, optID)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM poll_votes WHERE post_id = $1`, postID)
		_, _ = pool.Exec(ctx, `DELETE FROM poll_options WHERE post_id = $1`, postID)
		_, _ = pool.Exec(ctx, `DELETE FROM polls WHERE post_id = $1`, postID)
		_, _ = pool.Exec(ctx, `DELETE FROM post_engagement_counts WHERE post_id = $1`, postID)
		_, _ = pool.Exec(ctx, `DELETE FROM posts WHERE id = $1`, postID)
	})
	return p
}

func voteCount(t *testing.T, pool *pgxpool.Pool, postID, userID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM poll_votes WHERE post_id = $1 AND user_id = $2`,
		postID, userID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// DEFECT 1. A single-choice poll accepted a second vote for a DIFFERENT
// option, because the only guard was a primary key of
// (post_id, user_id, option_id) — which stops the same option twice and
// nothing else. Live: one account voted Teal, then Orange, both 200, and
// total_votes reached 3 from two people.
func TestPollVoteSingleChoiceRefusesSecondOption(t *testing.T) {
	pool := openPollDB(t)
	svc := &Service{pgStore: postgres.New(pool)}
	poll := newTestPoll(t, pool, false, nil)
	voter := uuid.New()
	ctx := context.Background()

	if err := svc.CastPollVote(ctx, poll.postID, poll.options[0], voter); err != nil {
		t.Fatalf("first vote: %v", err)
	}
	err := svc.CastPollVote(ctx, poll.postID, poll.options[1], voter)
	if !errors.Is(err, ErrPollAlreadyVoted) {
		t.Fatalf("second option on a single-choice poll: want ErrPollAlreadyVoted, got %v", err)
	}
	if n := voteCount(t, pool, poll.postID, voter); n != 1 {
		t.Fatalf("voter holds %d votes on a single-choice poll, want 1", n)
	}
}

// The same option twice is also refused, and with the same code — a caller
// re-submitting is "already voted", not a database error. This is the case
// that used to surface the pgx string.
func TestPollVoteSingleChoiceRefusesSameOptionTwice(t *testing.T) {
	pool := openPollDB(t)
	svc := &Service{pgStore: postgres.New(pool)}
	poll := newTestPoll(t, pool, false, nil)
	voter := uuid.New()
	ctx := context.Background()

	if err := svc.CastPollVote(ctx, poll.postID, poll.options[0], voter); err != nil {
		t.Fatalf("first vote: %v", err)
	}
	err := svc.CastPollVote(ctx, poll.postID, poll.options[0], voter)
	if !errors.Is(err, ErrPollAlreadyVoted) {
		t.Fatalf("want ErrPollAlreadyVoted, got %v", err)
	}
	// The old message carried `poll_votes_pkey` and `(SQLSTATE 23505)`.
	if got := err.Error(); got != ErrPollAlreadyVoted.Error() {
		t.Fatalf("error text leaks database detail: %q", got)
	}
}

// A multi-choice poll must still ACCEPT a second, different option —
// otherwise the fix for defect 1 would have broken the feature it guards.
func TestPollVoteMultiChoiceAllowsSecondOption(t *testing.T) {
	pool := openPollDB(t)
	svc := &Service{pgStore: postgres.New(pool)}
	poll := newTestPoll(t, pool, true, nil)
	voter := uuid.New()
	ctx := context.Background()

	if err := svc.CastPollVote(ctx, poll.postID, poll.options[0], voter); err != nil {
		t.Fatalf("first vote: %v", err)
	}
	if err := svc.CastPollVote(ctx, poll.postID, poll.options[1], voter); err != nil {
		t.Fatalf("second option on a multi-choice poll: %v", err)
	}
	if n := voteCount(t, pool, poll.postID, voter); n != 2 {
		t.Fatalf("voter holds %d votes on a multi-choice poll, want 2", n)
	}
	// …but not the SAME option twice.
	if err := svc.CastPollVote(ctx, poll.postID, poll.options[1], voter); !errors.Is(err, ErrPollAlreadyVoted) {
		t.Fatalf("repeat of the same option: want ErrPollAlreadyVoted, got %v", err)
	}
}

// DEFECT 2, part one. A fabricated option id used to insert cleanly and
// inflate total_votes without matching any option.
func TestPollVoteRejectsFabricatedOption(t *testing.T) {
	pool := openPollDB(t)
	svc := &Service{pgStore: postgres.New(pool)}
	poll := newTestPoll(t, pool, false, nil)
	voter := uuid.New()

	err := svc.CastPollVote(context.Background(), poll.postID, uuid.New(), voter)
	if !errors.Is(err, ErrPollOptionInvalid) {
		t.Fatalf("fabricated option: want ErrPollOptionInvalid, got %v", err)
	}
	if n := voteCount(t, pool, poll.postID, voter); n != 0 {
		t.Fatalf("%d rows written for an option that does not exist", n)
	}
}

// DEFECT 2, part two — the case a plain FK on option_id would have MISSED.
// The option id is real; it belongs to another poll. Option ids are public in
// every poll payload, so this is the version an attacker can actually build.
func TestPollVoteRejectsOptionFromAnotherPoll(t *testing.T) {
	pool := openPollDB(t)
	svc := &Service{pgStore: postgres.New(pool)}
	mine := newTestPoll(t, pool, false, nil)
	theirs := newTestPoll(t, pool, false, nil)
	voter := uuid.New()

	err := svc.CastPollVote(context.Background(), mine.postID, theirs.options[0], voter)
	if !errors.Is(err, ErrPollOptionInvalid) {
		t.Fatalf("cross-poll option: want ErrPollOptionInvalid, got %v", err)
	}
	if n := voteCount(t, pool, mine.postID, voter); n != 0 {
		t.Fatalf("%d rows written for an option from another poll", n)
	}
}

// The database refuses it too, not just the service. This is what protects
// the table from writers that are not this code path — a backfill, another
// service, a psql session. Before migration 045 both of these INSERTs
// succeeded.
func TestPollVotesForeignKeyRefusesOrphanRows(t *testing.T) {
	pool := openPollDB(t)
	mine := newTestPoll(t, pool, false, nil)
	theirs := newTestPoll(t, pool, false, nil)
	ctx := context.Background()

	for name, optionID := range map[string]uuid.UUID{
		"fabricated": uuid.New(),
		"cross-poll": theirs.options[0],
	} {
		_, err := pool.Exec(ctx, `
			INSERT INTO poll_votes (post_id, option_id, user_id, created_at)
			VALUES ($1, $2, $3, NOW())
		`, mine.postID, optionID, uuid.New())
		if err == nil {
			t.Fatalf("%s option id inserted directly: poll_votes has no working foreign key", name)
		}
	}
}

// DEFECT 3. Voting on an ended poll is a client error. It reached the client
// as 500 INTERNAL_ERROR on /vote and as an untyped VOTE_ERROR on /poll/vote;
// it is now one sentinel that both routes map to 400 POLL_ENDED.
func TestPollVoteRejectsEndedPoll(t *testing.T) {
	pool := openPollDB(t)
	svc := &Service{pgStore: postgres.New(pool)}
	past := time.Now().Add(-time.Hour)
	poll := newTestPoll(t, pool, false, &past)
	voter := uuid.New()

	err := svc.CastPollVote(context.Background(), poll.postID, poll.options[0], voter)
	if !errors.Is(err, ErrPollEnded) {
		t.Fatalf("ended poll: want ErrPollEnded, got %v", err)
	}
	if n := voteCount(t, pool, poll.postID, voter); n != 0 {
		t.Fatalf("%d votes recorded on an ended poll", n)
	}
}

// A post with no poll is a 404, not a 500 with "no rows in result set".
func TestPollVoteRejectsPostWithoutPoll(t *testing.T) {
	pool := openPollDB(t)
	svc := &Service{pgStore: postgres.New(pool)}
	ctx := context.Background()
	postID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO posts (id, author_id, text, visibility, content_type, created_at, updated_at)
		VALUES ($1, $2, 'no poll', 'public', 'post', NOW(), NOW())
	`, postID, uuid.New()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM post_engagement_counts WHERE post_id = $1`, postID)
		_, _ = pool.Exec(ctx, `DELETE FROM posts WHERE id = $1`, postID)
	})

	if err := svc.CastPollVote(ctx, postID, uuid.New(), uuid.New()); !errors.Is(err, ErrPollNotFound) {
		t.Fatalf("post with no poll: want ErrPollNotFound, got %v", err)
	}
}

// Both routes are the same operation. If they ever diverge again, this fails.
func TestCastVoteAndCastPollVoteAgree(t *testing.T) {
	pool := openPollDB(t)
	svc := &Service{pgStore: postgres.New(pool)}
	poll := newTestPoll(t, pool, false, nil)
	voter := uuid.New()
	ctx := context.Background()

	// /vote casts the first vote…
	if err := svc.CastVote(ctx, poll.postID, poll.options[0], voter); err != nil {
		t.Fatalf("CastVote: %v", err)
	}
	// …and /poll/vote must refuse the second, with the same reason CastVote
	// would give. The old code let this one through with 200.
	if err := svc.CastPollVote(ctx, poll.postID, poll.options[1], voter); !errors.Is(err, ErrPollAlreadyVoted) {
		t.Fatalf("CastPollVote after CastVote: want ErrPollAlreadyVoted, got %v", err)
	}
	if err := svc.CastVote(ctx, poll.postID, poll.options[1], voter); !errors.Is(err, ErrPollAlreadyVoted) {
		t.Fatalf("CastVote after CastVote: want ErrPollAlreadyVoted, got %v", err)
	}
	if n := voteCount(t, pool, poll.postID, voter); n != 1 {
		t.Fatalf("voter holds %d votes across both routes, want 1", n)
	}
}

// Concurrency: the allows_multiple rule is a NOT EXISTS under READ COMMITTED,
// which two simultaneous requests from the same voter would both pass. The
// primary key does not catch it — the options differ. Only the per-voter
// advisory lock does.
func TestPollVoteConcurrentDoubleSubmitYieldsOneVote(t *testing.T) {
	pool := openPollDB(t)
	svc := &Service{pgStore: postgres.New(pool)}
	poll := newTestPoll(t, pool, false, nil)
	voter := uuid.New()

	start := make(chan struct{})
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func(opt uuid.UUID) {
			<-start
			done <- svc.CastPollVote(context.Background(), poll.postID, opt, voter)
		}(poll.options[i])
	}
	close(start)
	var ok, refused int
	for i := 0; i < 2; i++ {
		switch err := <-done; {
		case err == nil:
			ok++
		case errors.Is(err, ErrPollAlreadyVoted):
			refused++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 || refused != 1 {
		t.Fatalf("concurrent double-submit: %d accepted, %d refused; want 1 and 1", ok, refused)
	}
	if n := voteCount(t, pool, poll.postID, voter); n != 1 {
		t.Fatalf("voter holds %d votes after a concurrent double-submit, want 1", n)
	}
}

// The read side must not inflate. GetPoll used to sum every poll_votes row
// for the post, so an orphan row pushed total_votes above the sum of the
// options and the percentages stopped adding up (live: 50% + 25%).
func TestGetPollTotalsIgnoreOrphanVotes(t *testing.T) {
	pool := openPollDB(t)
	st := postgres.New(pool)
	poll := newTestPoll(t, pool, false, nil)
	ctx := context.Background()
	voter := uuid.New()

	if _, err := st.InsertPollVote(ctx, poll.postID, poll.options[0], voter, false); err != nil {
		t.Fatal(err)
	}
	// Plant an orphan the only way still possible: drop the constraint for
	// the length of this check. This is what a pre-045 database holds.
	if _, err := pool.Exec(ctx,
		`ALTER TABLE poll_votes DROP CONSTRAINT poll_votes_option_fkey`); err != nil {
		t.Fatalf("drop fk: %v", err)
	}
	orphan := uuid.New()
	ghost := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO poll_votes (post_id, option_id, user_id, created_at)
		VALUES ($1, $2, $3, NOW())`, poll.postID, orphan, ghost); err != nil {
		t.Fatalf("plant orphan: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM poll_votes WHERE option_id = $1`, orphan)
		_, _ = pool.Exec(ctx, `
			ALTER TABLE poll_votes ADD CONSTRAINT poll_votes_option_fkey
			FOREIGN KEY (option_id, post_id) REFERENCES poll_options (id, post_id)
			ON DELETE CASCADE`)
	})

	got, err := st.GetPoll(ctx, poll.postID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalVotes != 1 {
		t.Fatalf("total_votes = %d with one real vote and one orphan, want 1", got.TotalVotes)
	}
	var sum int64
	for _, o := range got.Options {
		sum += o.VoteCount
	}
	if sum != got.TotalVotes {
		t.Fatalf("options sum to %d but total_votes is %d — percentages cannot add up", sum, got.TotalVotes)
	}

	// …and the orphan must not come back as the viewer's own vote.
	votes, err := st.GetUserPollVotes(ctx, poll.postID, ghost)
	if err != nil {
		t.Fatal(err)
	}
	if len(votes) != 0 {
		t.Fatalf("viewer_votes echoed an option that does not exist: %v", votes)
	}
}
