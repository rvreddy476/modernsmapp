//go:build integration

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
)

// feed-07 against a real Postgres: two replicas' relay passes over the same
// unpublished rows.
//
//	POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/service/ -run OutboxRelayIT -v
//
// Each "replica" is a store on a pool of its own, so its lock connection is
// another Postgres session. The publisher is a fake that records what it was
// handed; nothing here needs a broker.

// countingPublisher records every message by event id. The short sleep keeps
// a pass in flight long enough for the other replica's tick to overlap it.
type countingPublisher struct {
	mu    sync.Mutex
	count map[string]int      // event id -> times published
	order map[string][]string // key (aggregate id) -> event ids in publish order
}

func (p *countingPublisher) publish(_ context.Context, msg kafka.Message) error {
	var env events.EventEnvelope
	if err := json.Unmarshal(msg.Value, &env); err != nil {
		return err
	}
	time.Sleep(200 * time.Microsecond)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.count[env.EventID]++
	p.order[string(msg.Key)] = append(p.order[string(msg.Key)], env.EventID)
	return nil
}

func TestOutboxRelayIT_TwoReplicasPublishEachRowOnce(t *testing.T) {
	poolA := openStandingDB(t)
	poolB, err := pgxpool.New(context.Background(), os.Getenv("POSTGRES_DSN")) // checked by openStandingDB
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(poolB.Close)
	replicas := []*postgres.Store{postgres.New(poolA), postgres.New(poolB)}
	ctx := context.Background()

	// 250 rows over 5 posts: more than two passes' worth (100 a pass), so
	// the replicas keep meeting at the lock.
	const perPost, posts = 50, 5
	aggregates := make([]uuid.UUID, posts)
	for i := range aggregates {
		aggregates[i] = uuid.New()
	}
	t.Cleanup(func() {
		_, _ = poolA.Exec(context.Background(), `DELETE FROM post_outbox_events WHERE aggregate_id = ANY($1)`, aggregates)
	})
	want := map[string][]string{} // aggregate id -> row ids in write order
	for n := 0; n < perPost; n++ {
		for _, agg := range aggregates {
			var id uuid.UUID
			if err := poolA.QueryRow(ctx, `
				INSERT INTO post_outbox_events (event_type, aggregate_type, aggregate_id, payload, created_at)
				VALUES ($1, 'post', $2, '{}', clock_timestamp())
				RETURNING id`, fmt.Sprintf("it.relay.%02d", n), agg).Scan(&id); err != nil {
				t.Fatalf("seed: %v", err)
			}
			want[agg.String()] = append(want[agg.String()], id.String())
		}
	}

	pub := &countingPublisher{count: map[string]int{}, order: map[string][]string{}}
	remaining := func() int {
		var n int
		if err := poolA.QueryRow(ctx, `
			SELECT COUNT(*) FROM post_outbox_events
			WHERE aggregate_id = ANY($1) AND published = FALSE`, aggregates).Scan(&n); err != nil {
			t.Fatalf("count unpublished: %v", err)
		}
		return n
	}

	deadline := time.Now().Add(2 * time.Minute)
	var wg sync.WaitGroup
	errs := make(chan error, len(replicas))
	stop := make(chan struct{})
	for _, st := range replicas {
		wg.Add(1)
		go func(st *postgres.Store) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := relayOutboxPass(ctx, st, pub.publish); err != nil {
					errs <- err
					return
				}
			}
		}(st)
	}
	for remaining() > 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	close(stop)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("relay pass: %v", err)
	}
	if n := remaining(); n != 0 {
		t.Fatalf("%d seeded row(s) still unpublished", n)
	}

	pub.mu.Lock()
	defer pub.mu.Unlock()
	for agg, ids := range want {
		for _, id := range ids {
			if got := pub.count[id]; got != 1 {
				t.Errorf("row %s published %d times, want exactly once", id, got)
			}
		}
		// One partition key per post, in the order written.
		if fmt.Sprint(pub.order[agg]) != fmt.Sprint(ids) {
			t.Errorf("post %s: publish order differs from write order", agg)
		}
	}
	var marked int
	if err := poolA.QueryRow(ctx, `
		SELECT COUNT(*) FROM post_outbox_events
		WHERE aggregate_id = ANY($1) AND published = TRUE AND published_at IS NOT NULL`, aggregates).Scan(&marked); err != nil {
		t.Fatal(err)
	}
	if marked != perPost*posts {
		t.Fatalf("%d rows marked published, want %d", marked, perPost*posts)
	}
}
