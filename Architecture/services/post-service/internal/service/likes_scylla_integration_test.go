//go:build integration

package service

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	postEvents "github.com/atpost/post-service/internal/events"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/post-service/internal/store/scylla"
	"github.com/atpost/shared/events"
	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// feed-14, feed-49 and feed-30 against a real ScyllaDB and a real Postgres:
// the three like routes under concurrent taps.
//
//	POSTGRES_DSN=postgres://…/post_it_test POST_SCYLLA_IT_HOSTS=localhost \
//	    go test -tags integration ./internal/service/ -run LikesIT -v
//
//	POST / DELETE /v1/posts/:id/reactions  Service.React / Unreact
//	POST /v1/posts/:id/like                Service.ToggleLike
//	POST /v1/posts/:id/react               Service.ToggleReaction
//
// Scylla: the tests create and use their own keyspace, likesITKeyspace
// (never social_engagement), with the two tables the like writes touch.
// Postgres: POSTGRES_DSN, refused unless the database name ends in _test
// (openStandingDB). Redis is absent on purpose (a client that cannot dial):
// the Redis copies are covered with miniredis in like_toggle_test.go, and
// what is proved here is that the stored reaction decides.
//
// CI starts ScyllaDB for the post-service job (integration-all.yml). Without
// POST_SCYLLA_IT_HOSTS the tests skip.

const likesITKeyspace = "post_it_test"

type likesIT struct {
	svc     *Service
	pool    *pgxpool.Pool
	session *gocql.Session
	post    *postgres.Post
}

func newLikesIT(t *testing.T) *likesIT {
	t.Helper()
	hosts := os.Getenv("POST_SCYLLA_IT_HOSTS")
	if hosts == "" {
		t.Skip("POST_SCYLLA_IT_HOSTS not set")
	}
	if !strings.HasSuffix(likesITKeyspace, "_test") {
		t.Fatalf("refusing to run against keyspace %q: the name must end in _test", likesITKeyspace)
	}
	pool := openStandingDB(t)

	cluster := gocql.NewCluster(strings.Split(hosts, ",")...)
	cluster.Timeout = 20 * time.Second
	cluster.ConnectTimeout = 20 * time.Second
	cluster.Consistency = gocql.Quorum
	// One node behind a published port: use the address given, not the
	// container address the node advertises.
	cluster.DisableInitialHostLookup = true
	admin, err := cluster.CreateSession()
	if err != nil {
		t.Fatalf("POST_SCYLLA_IT_HOSTS set but ScyllaDB unreachable: %v", err)
	}
	for _, ddl := range []string{
		`CREATE KEYSPACE IF NOT EXISTS ` + likesITKeyspace + ` WITH replication = {'class': 'SimpleStrategy', 'replication_factor': 1}`,
		`CREATE TABLE IF NOT EXISTS ` + likesITKeyspace + `.reactions_by_post (
			post_id text, user_id text, reaction text, created_at timestamp,
			PRIMARY KEY ((post_id), user_id))`,
		`CREATE TABLE IF NOT EXISTS ` + likesITKeyspace + `.post_counters (
			post_id text PRIMARY KEY, like_count counter, comment_count counter)`,
	} {
		if err := admin.Query(ddl).Exec(); err != nil {
			admin.Close()
			t.Fatalf("scylla schema: %v", err)
		}
	}
	admin.Close()
	cluster.Keyspace = likesITKeyspace
	session, err := cluster.CreateSession()
	if err != nil {
		t.Fatalf("open %s: %v", likesITKeyspace, err)
	}
	t.Cleanup(session.Close)

	// No Redis: every command fails at dial, which the like paths log and
	// the rate limiter treats as "allow".
	rdb := redis.NewClient(&redis.Options{
		Addr:       "likes-it:0",
		MaxRetries: -1,
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("no redis in the like integration tests")
		},
	})
	t.Cleanup(func() { _ = rdb.Close() })

	st := postgres.New(pool)
	r := &likesIT{
		pool:    pool,
		session: session,
		post:    &postgres.Post{ID: uuid.New(), AuthorID: uuid.New(), ContentType: "post", Visibility: "public"},
	}
	r.svc = New(st, scylla.New(session), rdb)
	// A producer makes the like paths write their outbox rows; nothing is
	// published through it (the relay is not running).
	r.svc.producer = &postEvents.Producer{}
	// The post is not a row in posts: the like paths only need it loaded
	// and visible.
	r.svc.engagementPostSeam = func(_ context.Context, postID, _ uuid.UUID) (*postgres.Post, error) {
		if postID != r.post.ID {
			return nil, ErrPostNotFound
		}
		return r.post, nil
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM reactions WHERE target_type = 'post' AND target_id = $1`, r.post.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM post_outbox_events WHERE aggregate_id = $1`, r.post.ID)
	})
	return r
}

// scyllaRows is the number of viewers with a stored reaction on the post.
func (r *likesIT) scyllaRows(t *testing.T) int {
	t.Helper()
	var n int
	if err := r.session.Query(`SELECT COUNT(*) FROM reactions_by_post WHERE post_id = ?`, r.post.ID.String()).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// likeCounter is post_counters.like_count, the number the post's card shows.
func (r *likesIT) likeCounter(t *testing.T) int {
	t.Helper()
	var n int64
	err := r.session.Query(`SELECT like_count FROM post_counters WHERE post_id = ?`, r.post.ID.String()).Scan(&n)
	if err != nil && !errors.Is(err, gocql.ErrNotFound) {
		t.Fatal(err)
	}
	return int(n)
}

func (r *likesIT) pgRows(t *testing.T) int {
	t.Helper()
	var n int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM reactions WHERE target_type = 'post' AND target_id = $1`, r.post.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (r *likesIT) outbox(t *testing.T, eventType string) int {
	t.Helper()
	var n int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM post_outbox_events WHERE aggregate_id = $1 AND event_type = $2`, r.post.ID, eventType).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// agree asserts the invariant every like route must keep: the like counter
// equals the number of stored reactions, the liked list's Postgres record
// has the same number of rows, and the announcements add up to it.
func (r *likesIT) agree(t *testing.T, want int) {
	t.Helper()
	rows, counter, pg := r.scyllaRows(t), r.likeCounter(t), r.pgRows(t)
	reacted, unreacted := r.outbox(t, events.PostReacted), r.outbox(t, events.PostUnreacted)
	if rows != want || counter != want || pg != want {
		t.Fatalf("stored reactions = %d, like counter = %d, Postgres rows = %d; want %d each", rows, counter, pg, want)
	}
	if reacted-unreacted != want {
		t.Fatalf("%d PostReacted - %d PostUnreacted = %d, want %d", reacted, unreacted, reacted-unreacted, want)
	}
}

// burst runs fn once per user, all released together, and fails on any error.
func burst(t *testing.T, users []uuid.UUID, fn func(user uuid.UUID) error) {
	t.Helper()
	errs := make([]error, len(users))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, u := range users {
		wg.Add(1)
		go func(i int, u uuid.UUID) {
			defer wg.Done()
			<-start
			errs[i] = fn(u)
		}(i, u)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
}

func oneViewer(n int) []uuid.UUID {
	u := uuid.New()
	out := make([]uuid.UUID, n)
	for i := range out {
		out[i] = u
	}
	return out
}

func manyViewers(n int) []uuid.UUID {
	out := make([]uuid.UUID, n)
	for i := range out {
		out[i] = uuid.New()
	}
	return out
}

// feed-14: fifty concurrent POST /reactions by one viewer are one reaction
// row, a counter up by one and exactly one PostReacted outbox row.
func TestLikesIT_React_FiftyConcurrentByOneViewer(t *testing.T) {
	r := newLikesIT(t)
	burst(t, oneViewer(50), func(u uuid.UUID) error {
		return r.svc.React(context.Background(), r.post.ID, u, "like")
	})
	r.agree(t, 1)
	if got := r.outbox(t, events.PostReacted); got != 1 {
		t.Fatalf("%d PostReacted rows for one like, want exactly 1", got)
	}
}

// feed-14: fifty viewers are fifty.
func TestLikesIT_React_FiftyViewers(t *testing.T) {
	r := newLikesIT(t)
	burst(t, manyViewers(50), func(u uuid.UUID) error {
		return r.svc.React(context.Background(), r.post.ID, u, "like")
	})
	r.agree(t, 50)
	if got := r.outbox(t, events.PostReacted); got != 50 {
		t.Fatalf("%d PostReacted rows for fifty likes, want 50", got)
	}
}

// feed-49: a like, then two taps on POST /like, with the stored reaction
// deciding (there is no Redis membership key here at all, which is the
// state the key is in after 24 h). Unlike, then like: everything ends where
// it started. The old toggle counted the viewer again on the first tap.
func TestLikesIT_ToggleLike_TwoTapsAfterALikeEndWhereTheyStarted(t *testing.T) {
	r := newLikesIT(t)
	user := uuid.New()
	ctx := context.Background()

	first, err := r.svc.ToggleLike(ctx, r.post.ID, user)
	if err != nil || !first.Liked || first.Count != 1 {
		t.Fatalf("like = %+v, %v; want liked with count 1", first, err)
	}
	r.agree(t, 1)

	second, err := r.svc.ToggleLike(ctx, r.post.ID, user)
	if err != nil || second.Liked || second.Count != 0 {
		t.Fatalf("tap on a liked post = %+v, %v; want an unlike with count 0", second, err)
	}
	r.agree(t, 0)

	third, err := r.svc.ToggleLike(ctx, r.post.ID, user)
	if err != nil || !third.Liked || third.Count != 1 {
		t.Fatalf("second tap = %+v, %v; want liked with count 1", third, err)
	}
	r.agree(t, 1)
}

// feed-49: a like made through POST /reactions is taken off by POST /like.
func TestLikesIT_ToggleLike_UnlikesAReactionsLike(t *testing.T) {
	r := newLikesIT(t)
	user := uuid.New()
	ctx := context.Background()
	if err := r.svc.React(ctx, r.post.ID, user, "like"); err != nil {
		t.Fatal(err)
	}
	r.agree(t, 1)

	res, err := r.svc.ToggleLike(ctx, r.post.ID, user)
	if err != nil || res.Liked || res.Count != 0 {
		t.Fatalf("tap = %+v, %v; want an unlike with count 0", res, err)
	}
	r.agree(t, 0)
}

// feed-49: concurrent taps on POST /like by one viewer. Which state a burst
// of toggles ends in depends on how they interleave, but the counter always
// equals the stored reactions and the announcements always add up.
//
// The liked list's Postgres record is not asserted here: it is written
// before the Scylla row with no lock across the two, so a like and an unlike
// in flight together can leave it opposite to the stored reaction (the same
// holds for POST and DELETE /reactions racing each other).
func TestLikesIT_ToggleLike_ConcurrentTapsKeepTheCounterRight(t *testing.T) {
	r := newLikesIT(t)
	burst(t, oneViewer(50), func(u uuid.UUID) error {
		_, err := r.svc.ToggleLike(context.Background(), r.post.ID, u)
		return err
	})
	rows := r.scyllaRows(t)
	if rows != 0 && rows != 1 {
		t.Fatalf("%d stored reactions for one viewer", rows)
	}
	if counter := r.likeCounter(t); counter != rows {
		t.Fatalf("like counter = %d with %d stored reaction(s)", counter, rows)
	}
	if net := r.outbox(t, events.PostReacted) - r.outbox(t, events.PostUnreacted); net != rows {
		t.Fatalf("announcements add up to %d with %d stored reaction(s)", net, rows)
	}
}

// feed-49: fifty viewers tapping POST /like once each are fifty likes.
func TestLikesIT_ToggleLike_FiftyViewers(t *testing.T) {
	r := newLikesIT(t)
	burst(t, manyViewers(50), func(u uuid.UUID) error {
		res, err := r.svc.ToggleLike(context.Background(), r.post.ID, u)
		if err == nil && !res.Liked {
			return errors.New("a first tap answered liked=false")
		}
		return err
	})
	r.agree(t, 50)
}

// feed-30: concurrent POST /react toggles by one viewer. Postgres decides
// and the Scylla copy is written under the same lock, so the reactions
// table, reactions_by_post and the like counter all agree afterwards. The
// copies used to be written after the commit in no fixed order.
func TestLikesIT_ToggleReaction_ConcurrentTogglesKeepTheStoresInStep(t *testing.T) {
	for _, taps := range []int{50, 51} {
		r := newLikesIT(t)
		burst(t, oneViewer(taps), func(u uuid.UUID) error {
			_, err := r.svc.ToggleReaction(context.Background(), r.post.ID, u, "like")
			return err
		})
		// Taken one at a time, an even number of toggles from nothing ends
		// unliked and an odd number liked.
		r.agree(t, taps%2)
	}
}

// feed-30: fifty viewers through POST /react are fifty.
func TestLikesIT_ToggleReaction_FiftyViewers(t *testing.T) {
	r := newLikesIT(t)
	burst(t, manyViewers(50), func(u uuid.UUID) error {
		res, err := r.svc.ToggleReaction(context.Background(), r.post.ID, u, "like")
		if err == nil && !res.IsSet {
			return errors.New("a first tap answered is_set=false")
		}
		return err
	})
	r.agree(t, 50)
}
