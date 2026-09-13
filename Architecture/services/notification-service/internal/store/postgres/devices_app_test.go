package postgres

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/notification-service/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDeviceApp_OmittedMeansMomentum(t *testing.T) {
	if NormalizeDeviceApp("") != AppMomentum {
		t.Fatal("an omitted app must register for Momentum (every pre-Feast client)")
	}
	for _, app := range []string{AppMomentum, AppFeastKitchen, AppFeastRider} {
		if !ValidDeviceApp(app) || NormalizeDeviceApp(app) != app {
			t.Fatalf("%q rejected or rewritten", app)
		}
	}
	for _, app := range []string{"", "feast", "MOMENTUM", "kitchen"} {
		if ValidDeviceApp(app) {
			t.Fatalf("%q accepted", app)
		}
	}
}

// notificationTestPool opens NOTIFICATION_TEST_DSN and applies this service's
// real schema bootstrap (setup.sql + every migration). It refuses any
// database whose name does not end in _test.
func notificationTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("NOTIFICATION_TEST_DSN")
	if dsn == "" {
		t.Skip("NOTIFICATION_TEST_DSN not set; skipping notification Postgres integration tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse NOTIFICATION_TEST_DSN: %v", err)
	}
	if name := cfg.ConnConfig.Database; !strings.HasSuffix(name, "_test") {
		t.Fatalf("refusing to run against database %q: the name must end in _test", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatalf("bootstrap schema: %v", err)
	}
	// Twice: every migration and setup statement must be re-runnable.
	if err := BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatalf("bootstrap schema (second run): %v", err)
	}
	return pool
}

func TestDevicesIntegration_PushTargetsAreScopedToTheirApp(t *testing.T) {
	pool := notificationTestPool(t)
	store := New(pool)
	ctx := context.Background()
	user := uuid.New()
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM user_devices WHERE user_id = $1`, user) })

	suffix := user.String()
	if _, err := store.RegisterDevice(ctx, user, "android", "momentum-"+suffix, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterDevice(ctx, user, "android", "kitchen-"+suffix, AppFeastKitchen); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterDevice(ctx, user, "android", "rider-"+suffix, AppFeastRider); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterDevice(ctx, user, "android", "bogus-"+suffix, "feast"); err == nil {
		t.Fatal("unknown app registered")
	}

	general, err := store.GetUserDevices(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if len(general) != 1 || general[0].PushToken != "momentum-"+suffix || general[0].App != AppMomentum {
		t.Fatalf("general (Momentum) push targets = %+v", general)
	}
	for app, token := range map[string]string{AppFeastKitchen: "kitchen-" + suffix, AppFeastRider: "rider-" + suffix} {
		got, err := store.GetUserDevicesForApp(ctx, user, app)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].PushToken != token || got[0].App != app {
			t.Fatalf("%s targets = %+v", app, got)
		}
	}
}

// Rows written before migration 007 (no app column in the INSERT) are
// Momentum's, and the database refuses an unknown app outright.
func TestDevicesIntegration_LegacyRowsAreMomentumAndUnknownAppsRejected(t *testing.T) {
	pool := notificationTestPool(t)
	ctx := context.Background()
	user := uuid.New()
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM user_devices WHERE user_id = $1`, user) })

	if _, err := pool.Exec(ctx, `INSERT INTO user_devices (user_id, platform, push_token) VALUES ($1, 'android', 'legacy')`, user); err != nil {
		t.Fatal(err)
	}
	var app string
	if err := pool.QueryRow(ctx, `SELECT app FROM user_devices WHERE user_id = $1`, user).Scan(&app); err != nil {
		t.Fatal(err)
	}
	if app != AppMomentum {
		t.Fatalf("legacy row app = %q, want momentum", app)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_devices (user_id, platform, push_token, app) VALUES ($1, 'android', 'x', 'bogus')`, user); err == nil {
		t.Fatal("CHECK constraint accepted an unknown app")
	}
}

func TestEventDedupIntegration_ClaimsExactlyOnce(t *testing.T) {
	pool := notificationTestPool(t)
	store := New(pool)
	ctx := context.Background()
	id := uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM notify_meta.event_dedup WHERE event_id = $1`, id)
	})
	first, err := store.ClaimEventDedup(ctx, id)
	if err != nil || !first {
		t.Fatalf("first claim = %v, %v", first, err)
	}
	again, err := store.ClaimEventDedup(ctx, id)
	if err != nil || again {
		t.Fatalf("second claim = %v, %v; want false", again, err)
	}
}

func TestPreferencesIntegration_FoodOrdersRoundTrip(t *testing.T) {
	pool := notificationTestPool(t)
	store := New(pool)
	ctx := context.Background()
	user := uuid.New().String()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM notification_preferences WHERE user_id::text = $1`, user)
	})

	defaults, err := store.GetNotificationPreferences(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if !defaults.PushFoodOrders || !defaults.InappFoodOrders {
		t.Fatalf("food_orders defaults = push %v inapp %v", defaults.PushFoodOrders, defaults.InappFoodOrders)
	}
	defaults.PushFoodOrders = false
	if err := store.UpdateNotificationPreferences(ctx, defaults); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetNotificationPreferences(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if got.PushFoodOrders || !got.InappFoodOrders {
		t.Fatalf("stored food_orders = push %v inapp %v", got.PushFoodOrders, got.InappFoodOrders)
	}
}
