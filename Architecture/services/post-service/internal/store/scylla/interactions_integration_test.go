//go:build integration

package scylla

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

// feed-14 against a real ScyllaDB: the like writes are lightweight
// transactions, and nothing short of the database shows that N concurrent
// taps add one like.
//
//	POST_SCYLLA_IT_HOSTS=localhost go test -tags integration ./internal/store/scylla/ -v
//
// The tests never touch the application keyspace (social_engagement): they
// create their own, itKeyspace, whose name ends in _test, with the two
// tables these writes use, copied from Architecture/docker/scylla/schema.cql.
// CI starts ScyllaDB for the post-service job (integration-all.yml); without
// the variable the tests skip.

const itKeyspace = "post_it_test"

func openScyllaIT(t *testing.T) *gocql.Session {
	t.Helper()
	hosts := os.Getenv("POST_SCYLLA_IT_HOSTS")
	if hosts == "" {
		t.Skip("POST_SCYLLA_IT_HOSTS not set")
	}
	if !strings.HasSuffix(itKeyspace, "_test") {
		t.Fatalf("refusing to run against keyspace %q: the name must end in _test", itKeyspace)
	}
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
		`CREATE KEYSPACE IF NOT EXISTS ` + itKeyspace + ` WITH replication = {'class': 'SimpleStrategy', 'replication_factor': 1}`,
		`CREATE TABLE IF NOT EXISTS ` + itKeyspace + `.reactions_by_post (
			post_id text, user_id text, reaction text, created_at timestamp,
			PRIMARY KEY ((post_id), user_id))`,
		`CREATE TABLE IF NOT EXISTS ` + itKeyspace + `.post_counters (
			post_id text PRIMARY KEY, like_count counter, comment_count counter)`,
	} {
		if err := admin.Query(ddl).Exec(); err != nil {
			admin.Close()
			t.Fatalf("schema: %v", err)
		}
	}
	admin.Close()

	cluster.Keyspace = itKeyspace
	session, err := cluster.CreateSession()
	if err != nil {
		t.Fatalf("open %s: %v", itKeyspace, err)
	}
	t.Cleanup(session.Close)
	return session
}

func storedReactions(t *testing.T, session *gocql.Session, post uuid.UUID) int {
	t.Helper()
	var n int
	if err := session.Query(`SELECT COUNT(*) FROM reactions_by_post WHERE post_id = ?`, post.String()).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func likeCount(t *testing.T, store *InteractionStore, post uuid.UUID) int64 {
	t.Helper()
	c, err := store.GetCounts(context.Background(), post)
	if err != nil {
		t.Fatal(err)
	}
	return c.Likes
}

func TestReactIT_RepeatsDoNotCount(t *testing.T) {
	session := openScyllaIT(t)
	store := New(session)
	ctx := context.Background()
	post, user := uuid.New(), uuid.New()

	for i, want := range []bool{true, false, false} {
		added, err := store.React(ctx, post, user, "like")
		if err != nil {
			t.Fatal(err)
		}
		if added != want {
			t.Fatalf("React #%d reported added=%v, want %v", i+1, added, want)
		}
	}
	if rows, likes := storedReactions(t, session, post), likeCount(t, store, post); rows != 1 || likes != 1 {
		t.Fatalf("after three likes by one viewer: %d row(s), like_count %d; want 1 and 1", rows, likes)
	}

	for i, want := range []bool{true, false} {
		removed, err := store.Unreact(ctx, post, user)
		if err != nil {
			t.Fatal(err)
		}
		if removed != want {
			t.Fatalf("Unreact #%d reported removed=%v, want %v", i+1, removed, want)
		}
	}
	if rows, likes := storedReactions(t, session, post), likeCount(t, store, post); rows != 0 || likes != 0 {
		t.Fatalf("after the unlike, twice: %d row(s), like_count %d; want 0 and 0", rows, likes)
	}
	if r, err := store.GetReaction(ctx, post, user); err != nil || r != "" {
		t.Fatalf("stored reaction = %q, %v; want none", r, err)
	}
}

// Fifty concurrent likes by one viewer: exactly one is told it added the
// like, one row exists and the counter moved by one. Without IF NOT EXISTS
// every one of them is "added" and the counter reads fifty.
func TestReactIT_FiftyConcurrentLikesByOneViewerCountOnce(t *testing.T) {
	session := openScyllaIT(t)
	store := New(session)
	post, user := uuid.New(), uuid.New()

	const taps = 50
	added := make([]bool, taps)
	errs := make([]error, taps)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < taps; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			added[i], errs[i] = store.React(context.Background(), post, user, "like")
		}(i)
	}
	close(start)
	wg.Wait()

	winners := 0
	for i := range added {
		if errs[i] != nil {
			t.Fatalf("React %d: %v", i, errs[i])
		}
		if added[i] {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("%d of %d concurrent likes were told they added the like, want exactly 1", winners, taps)
	}
	if rows, likes := storedReactions(t, session, post), likeCount(t, store, post); rows != 1 || likes != 1 {
		t.Fatalf("%d row(s), like_count %d; want 1 and 1", rows, likes)
	}

	// And the same on the way down.
	removed := make([]bool, taps)
	start = make(chan struct{})
	for i := 0; i < taps; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			removed[i], errs[i] = store.Unreact(context.Background(), post, user)
		}(i)
	}
	close(start)
	wg.Wait()
	winners = 0
	for i := range removed {
		if errs[i] != nil {
			t.Fatalf("Unreact %d: %v", i, errs[i])
		}
		if removed[i] {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("%d of %d concurrent unlikes were told they removed the like, want exactly 1", winners, taps)
	}
	if rows, likes := storedReactions(t, session, post), likeCount(t, store, post); rows != 0 || likes != 0 {
		t.Fatalf("after the unlikes: %d row(s), like_count %d; want 0 and 0", rows, likes)
	}
}

// Fifty viewers are fifty likes.
func TestReactIT_FiftyViewersCountFifty(t *testing.T) {
	session := openScyllaIT(t)
	store := New(session)
	post := uuid.New()

	const viewers = 50
	errs := make([]error, viewers)
	added := make([]bool, viewers)
	var wg sync.WaitGroup
	for i := 0; i < viewers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			added[i], errs[i] = store.React(context.Background(), post, uuid.New(), "like")
		}(i)
	}
	wg.Wait()
	for i := range errs {
		if errs[i] != nil || !added[i] {
			t.Fatalf("viewer %d: added=%v err=%v", i, added[i], errs[i])
		}
	}
	if rows, likes := storedReactions(t, session, post), likeCount(t, store, post); rows != viewers || likes != viewers {
		t.Fatalf("%d row(s), like_count %d; want %d and %d", rows, likes, viewers, viewers)
	}
}
