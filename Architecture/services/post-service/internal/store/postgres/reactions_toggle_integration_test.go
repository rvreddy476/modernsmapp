//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// feed-30 against a real Postgres: concurrent POST /react toggles.
//
//	POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/store/postgres/ -run ToggleReactionIT -v
//
// The Scylla copy is a model here (mirrorModel: conditional writes and a
// counter, as scylla.InteractionStore keeps them), so these run on Postgres
// alone; the same toggles against a real Scylla are in
// service/likes_scylla_integration_test.go.

// mirrorModel is reactions_by_post + post_counters.like_count for one post.
type mirrorModel struct {
	mu    sync.Mutex
	rows  map[uuid.UUID]string
	likes int
	fail  error
}

func (m *mirrorModel) apply(_ context.Context, user uuid.UUID, t postgres.ReactionToggle) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return m.fail
	}
	_, exists := m.rows[user]
	switch {
	case t.IsSet && !exists: // INSERT … IF NOT EXISTS applied
		m.rows[user] = t.NewType
		m.likes++
	case t.IsSet:
		m.rows[user] = t.NewType
	case exists: // DELETE … IF EXISTS applied
		delete(m.rows, user)
		m.likes--
	}
	return nil
}

// reactionEventForTest is service.reactionEvent's rule: a new reaction is
// PostReacted, a removed one PostUnreacted, anything else nothing.
func reactionEventForTest(t postgres.ReactionToggle) (string, interface{}, bool) {
	switch {
	case t.IsSet && t.PrevType == "":
		return events.PostReacted, map[string]string{"react_type": t.NewType}, true
	case !t.IsSet && t.PrevType != "":
		return events.PostUnreacted, map[string]string{"react_type": t.PrevType}, true
	}
	return "", nil, false
}

type toggleIT struct {
	pool   *pgxpool.Pool
	store  *postgres.Store
	post   uuid.UUID
	mirror *mirrorModel
}

func newToggleIT(t *testing.T) *toggleIT {
	t.Helper()
	pool := openSubscriptionDB(t)
	r := &toggleIT{pool: pool, store: postgres.New(pool), post: uuid.New(), mirror: &mirrorModel{rows: map[uuid.UUID]string{}}}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM reactions WHERE target_type = 'post' AND target_id = $1`, r.post)
		_, _ = pool.Exec(ctx, `DELETE FROM post_outbox_events WHERE aggregate_id = $1`, r.post)
	})
	return r
}

func (r *toggleIT) toggle(ctx context.Context, user uuid.UUID) (postgres.ReactionToggle, error) {
	return r.store.ToggleReactionMirrored(ctx, "post", r.post, user, "like", reactionEventForTest,
		func(ctx context.Context, t postgres.ReactionToggle) error { return r.mirror.apply(ctx, user, t) })
}

func (r *toggleIT) storedRows(t *testing.T) int {
	t.Helper()
	var n int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM reactions WHERE target_type = 'post' AND target_id = $1`, r.post).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (r *toggleIT) outbox(t *testing.T, eventType string) int {
	t.Helper()
	var n int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM post_outbox_events WHERE aggregate_id = $1 AND event_type = $2`, r.post, eventType).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// burst runs one toggle per user, all released together.
func (r *toggleIT) burst(t *testing.T, users []uuid.UUID) []postgres.ReactionToggle {
	t.Helper()
	results := make([]postgres.ReactionToggle, len(users))
	errs := make([]error, len(users))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, u := range users {
		wg.Add(1)
		go func(i int, u uuid.UUID) {
			defer wg.Done()
			<-start
			results[i], errs[i] = r.toggle(context.Background(), u)
		}(i, u)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("toggle %d failed (a concurrent tap must not be a 500): %v", i, err)
		}
	}
	return results
}

func sameUser(u uuid.UUID, n int) []uuid.UUID {
	out := make([]uuid.UUID, n)
	for i := range out {
		out[i] = u
	}
	return out
}

// checkAgreement: the Postgres rows, the Scylla copy and its counter agree,
// and the events announced add up to the rows that exist.
func (r *toggleIT) checkAgreement(t *testing.T, startedWith int) {
	t.Helper()
	rows := r.storedRows(t)
	reacted, unreacted := r.outbox(t, events.PostReacted), r.outbox(t, events.PostUnreacted)
	r.mirror.mu.Lock()
	copies, likes := len(r.mirror.rows), r.mirror.likes
	r.mirror.mu.Unlock()
	if copies != rows || likes != rows {
		t.Fatalf("stored reactions = %d, Scylla copies = %d, like counter = %d; all three must agree", rows, copies, likes)
	}
	if startedWith+reacted-unreacted != rows {
		t.Fatalf("announced %d PostReacted and %d PostUnreacted from %d reaction(s), but %d are stored", reacted, unreacted, startedWith, rows)
	}
}

// The finding's reproduction: fifty concurrent toggles on an existing
// reaction. They used to all read "liked", all delete, and write up to
// fifty PostUnreacted rows for the one like removed. Now they run one at a
// time: each removal and each re-add is announced exactly once.
func TestToggleReactionIT_FiftyConcurrentTogglesOnAnExistingReaction(t *testing.T) {
	r := newToggleIT(t)
	user := uuid.New()
	first, err := r.toggle(context.Background(), user)
	if err != nil || !first.IsSet {
		t.Fatalf("seed like: %+v, %v", first, err)
	}

	results := r.burst(t, sameUser(user, 50))
	var removed, added int
	for _, res := range results {
		switch {
		case res.IsSet && res.PrevType == "":
			added++
		case !res.IsSet && res.PrevType == "like":
			removed++
		default:
			t.Fatalf("a toggle under the lock neither added nor removed: %+v", res)
		}
	}
	if removed != 25 || added != 25 {
		t.Fatalf("%d removals and %d adds; fifty toggles taken one at a time alternate, 25 each", removed, added)
	}
	if got := r.outbox(t, events.PostUnreacted); got != removed {
		t.Fatalf("%d PostUnreacted rows for %d removed likes", got, removed)
	}
	if got := r.outbox(t, events.PostReacted); got != added+1 {
		t.Fatalf("%d PostReacted rows for %d likes", got, added+1)
	}
	if rows := r.storedRows(t); rows != 1 {
		t.Fatalf("%d reaction rows after an even number of toggles on a liked post, want 1", rows)
	}
	r.checkAgreement(t, 0)
}

// An odd number of taps from nothing ends liked, with one row and a counter of one.
func TestToggleReactionIT_ConcurrentTogglesFromNothing(t *testing.T) {
	r := newToggleIT(t)
	r.burst(t, sameUser(uuid.New(), 51))
	if rows := r.storedRows(t); rows != 1 {
		t.Fatalf("%d reaction rows after 51 toggles from none, want 1", rows)
	}
	r.checkAgreement(t, 0)
}

// Fifty viewers are fifty reactions: unrelated taps do not queue on one
// another's lock and each is counted once.
func TestToggleReactionIT_FiftyViewers(t *testing.T) {
	r := newToggleIT(t)
	users := make([]uuid.UUID, 50)
	for i := range users {
		users[i] = uuid.New()
	}
	for _, res := range r.burst(t, users) {
		if !res.IsSet || res.PrevType != "" {
			t.Fatalf("a first tap = %+v, want a new like", res)
		}
	}
	if rows := r.storedRows(t); rows != 50 {
		t.Fatalf("%d reaction rows, want 50", rows)
	}
	if got := r.outbox(t, events.PostReacted); got != 50 {
		t.Fatalf("%d PostReacted rows, want 50", got)
	}
	r.checkAgreement(t, 0)
}

// A copy that cannot be written rolls the toggle back: no row, no event.
func TestToggleReactionIT_MirrorFailureRollsBack(t *testing.T) {
	r := newToggleIT(t)
	user := uuid.New()
	r.mirror.fail = errors.New("scylla timeout")
	if _, err := r.toggle(context.Background(), user); err == nil {
		t.Fatal("a toggle whose copy failed answered success")
	}
	if rows, ev := r.storedRows(t), r.outbox(t, events.PostReacted); rows != 0 || ev != 0 {
		t.Fatalf("after a rolled-back toggle: %d rows, %d PostReacted", rows, ev)
	}

	// The retry does the same thing and succeeds.
	r.mirror.fail = nil
	res, err := r.toggle(context.Background(), user)
	if err != nil || !res.IsSet {
		t.Fatalf("retry: %+v, %v", res, err)
	}
	r.checkAgreement(t, 0)
}

// A like path that does not take the lock (the liked list's upsert on POST
// /like and POST /reactions) writes the row between the toggle's read and
// its insert. The insert reports no row; the toggle settles on the stored
// like and announces nothing, where it used to fail on the unique index.
func TestToggleReactionIT_SettlesOnARowWrittenSinceTheRead(t *testing.T) {
	r := newToggleIT(t)
	user := uuid.New()
	ctx := context.Background()

	// The other writer: its row is written but not yet committed, so the
	// toggle's read does not see it and its insert waits on it.
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `
		INSERT INTO reactions (id, target_type, target_id, user_id, reaction_type, created_at)
		VALUES ($1, 'post', $2, $3, 'like', NOW())`, uuid.New(), r.post, user); err != nil {
		t.Fatal(err)
	}

	type outcome struct {
		res postgres.ReactionToggle
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := r.toggle(context.Background(), user)
		done <- outcome{res, err}
	}()
	select {
	case o := <-done:
		t.Fatalf("the toggle finished before the other writer committed: %+v, %v", o.res, o.err)
	case <-time.After(500 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	select {
	case o := <-done:
		if o.err != nil {
			t.Fatalf("toggle: %v (it used to fail on the unique index)", o.err)
		}
		if !o.res.IsSet || o.res.NewType != "like" || o.res.PrevType != "like" {
			t.Fatalf("toggle = %+v, want it settled on the stored like", o.res)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the toggle did not finish after the other writer committed")
	}
	if rows := r.storedRows(t); rows != 1 {
		t.Fatalf("%d reaction rows, want the one like", rows)
	}
	if got := r.outbox(t, events.PostReacted); got != 0 {
		t.Fatalf("%d PostReacted rows; this toggle added no reaction", got)
	}
	// The copy is the idempotent write of what is stored.
	r.mirror.mu.Lock()
	defer r.mirror.mu.Unlock()
	if r.mirror.rows[user] != "like" || r.mirror.likes != 1 {
		t.Fatalf("Scylla copy = %q with counter %d, want the like counted once", r.mirror.rows[user], r.mirror.likes)
	}
}
