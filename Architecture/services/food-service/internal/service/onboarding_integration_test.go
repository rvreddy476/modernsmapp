package service

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/atpost/food-service/database"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/outbox"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// serviceTestPool opens TEST_PG_DSN, refusing any database whose name does
// not end in _test (the suite writes fixture restaurants).
func serviceTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping food-service service integration tests")
	}
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil || !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatal("TEST_PG_DSN must parse and name a database ending in _test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := postgres.BootstrapSchema(context.Background(), pool, database.SetupSQL); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	return pool
}

func TestFSSAIExpiryPassPausesAndAnnouncesOnce(t *testing.T) {
	pool := serviceTestPool(t)
	ctx := context.Background()
	st := postgres.New(pool)

	owner := uuid.New()
	r, err := st.CreatePartnerRestaurant(ctx, owner, postgres.PartnerRestaurantInput{
		Name: "Expiry " + owner.String()[:8], Slug: "expiry-" + owner.String(), AddressLine1: "1 Test Lane", City: "Bengaluru",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE food.restaurants SET status = 'ACTIVE', is_open = TRUE, is_accepting_orders = TRUE WHERE id = $1`, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO food.restaurant_documents (restaurant_id, document_type, document_number, status, expires_at)
		VALUES ($1, 'FSSAI', '10099999000000', 'APPROVED', NOW() - INTERVAL '1 hour')`, r.ID); err != nil {
		t.Fatal(err)
	}

	capture := &eventCapture{}
	svc := New(st).WithOutbox(outbox.NewQueuer("food"), pool)
	svc.rtPublisher = capture

	if _, err := svc.PauseExpiredFSSAIRestaurants(ctx); err != nil {
		t.Fatalf("pass: %v", err)
	}
	topic := "food.restaurant." + r.ID.String()
	count := func() int {
		n := 0
		for _, e := range capture.events {
			if e[0] == topic && e[1] == EventRestaurantFSSAIExpired {
				n++
			}
		}
		return n
	}
	if count() != 1 {
		t.Fatalf("realtime events for the restaurant = %d, want 1 (%v)", count(), capture.events)
	}
	var accepting bool
	if err := pool.QueryRow(ctx, `SELECT is_accepting_orders FROM food.restaurants WHERE id = $1`, r.ID).Scan(&accepting); err != nil {
		t.Fatal(err)
	}
	if accepting {
		t.Fatalf("restaurant still accepting orders after its licence expired")
	}
	var outboxRows int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM food.outbox_events WHERE event_type = $1 AND partition_key = $2`,
		EventRestaurantFSSAIExpired, topic).Scan(&outboxRows); err != nil {
		t.Fatal(err)
	}
	if outboxRows != 1 {
		t.Fatalf("outbox rows = %d, want 1", outboxRows)
	}

	if _, err := svc.PauseExpiredFSSAIRestaurants(ctx); err != nil {
		t.Fatal(err)
	}
	if count() != 1 {
		t.Fatalf("a second pass announced the already paused restaurant again")
	}
}
