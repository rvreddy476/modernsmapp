package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/atpost/shared/store/migrationrunner"
	"github.com/jackc/pgx/v5/pgxpool"
)

// bootstrapLockService names food-service's per-service advisory lock.
const bootstrapLockService = "food-service"

// BootstrapSchema applies the base food schema on startup so a fresh local
// database can serve the FiGo mini app without a separate migration step.
//
// ─── WHY IT HOLDS AN ADVISORY LOCK ──────────────────────────────────────
//
// setup.sql is idempotent once applied, but idempotent is not exclusive. It
// re-applies statements on every boot — replacing trigger functions and
// dropping and re-adding CHECK constraints — and two sessions doing that at
// once collide: Postgres answers `tuple concurrently updated` (XX000), and a
// pair of `CREATE ... IF NOT EXISTS` can both pass the existence check with
// the loser dying on a catalog unique index. Observed 13 Sep 2026 when three
// integration-test packages bootstrapped food_it_test together; the same race
// waits for two food-service replicas booting at the same moment, which
// would crash-loop one of them.
//
// migrationrunner.WithLock serialises the whole script on a dedicated
// connection under food-service's advisory key, the same fix
// monetization-service's boot took for the identical race. The script MUST
// run on that locked connection: sent through the pool it would run on a
// session that holds nothing.
func BootstrapSchema(ctx context.Context, db *pgxpool.Pool, schemaSQL string) error {
	if db == nil {
		return fmt.Errorf("db pool is nil")
	}
	if strings.TrimSpace(schemaSQL) == "" {
		return fmt.Errorf("schema sql is empty")
	}
	return migrationrunner.WithLock(ctx, db, bootstrapLockService, func(ctx context.Context, conn *pgxpool.Conn) error {
		if _, err := conn.Exec(ctx, schemaSQL); err != nil {
			return fmt.Errorf("apply food schema: %w", err)
		}
		return nil
	})
}
