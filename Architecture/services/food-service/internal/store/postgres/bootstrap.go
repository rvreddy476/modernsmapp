package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/atpost/shared/store/migrationrunner"
	"github.com/jackc/pgx/v5"
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
//
// ─── WHY IT IS FINGERPRINTED AND HAS A LOCK TIMEOUT (audit M4) ──────────
//
// Applying the script takes table locks (ADD COLUMN IF NOT EXISTS takes
// ACCESS EXCLUSIVE before it checks, CREATE INDEX IF NOT EXISTS takes SHARE),
// and all of them are held until the script commits. A pod added by the HPA
// at lunch peak, queued behind a long report on food.orders, would stall all
// ordering. So:
//   - the script's sha256 is recorded in food.schema_bootstrap when it
//     commits, and a boot whose script is unchanged skips it entirely (an
//     ordinary scale-out or restart takes no table lock at all);
//   - a boot that does apply it runs in one explicit transaction with
//     SET LOCAL lock_timeout (BootstrapLockTimeout), so a busy table fails
//     the boot fast (the pod retries; the old pods keep serving) instead of
//     queueing every order behind it;
//   - setup.sql itself re-creates triggers and CHECK constraints only when
//     they are missing or changed.
//
// A fresh database has no fingerprint, so it always gets the whole script.
func BootstrapSchema(ctx context.Context, db *pgxpool.Pool, schemaSQL string) error {
	if db == nil {
		return fmt.Errorf("db pool is nil")
	}
	if strings.TrimSpace(schemaSQL) == "" {
		return fmt.Errorf("schema sql is empty")
	}
	fingerprint := SchemaFingerprint(schemaSQL)
	return migrationrunner.WithLock(ctx, db, bootstrapLockService, func(ctx context.Context, conn *pgxpool.Conn) error {
		applied, err := appliedSchemaFingerprint(ctx, conn)
		if err != nil {
			return fmt.Errorf("read food schema fingerprint: %w", err)
		}
		if applied == fingerprint {
			return nil
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
		if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = '"+BootstrapLockTimeout+"'"); err != nil {
			return fmt.Errorf("set food schema lock_timeout: %w", err)
		}
		if _, err := tx.Exec(ctx, schemaSQL); err != nil {
			return fmt.Errorf("apply food schema: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			CREATE TABLE IF NOT EXISTS food.schema_bootstrap (
				id SMALLINT PRIMARY KEY CHECK (id = 1),
				sha256 TEXT NOT NULL,
				applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)`); err != nil {
			return fmt.Errorf("create food schema fingerprint table: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO food.schema_bootstrap (id, sha256, applied_at) VALUES (1, $1, NOW())
			ON CONFLICT (id) DO UPDATE SET sha256 = EXCLUDED.sha256, applied_at = NOW()
		`, fingerprint); err != nil {
			return fmt.Errorf("record food schema fingerprint: %w", err)
		}
		return tx.Commit(ctx)
	})
}

// BootstrapLockTimeout bounds every lock the schema script waits for.
const BootstrapLockTimeout = "5s"

// SchemaFingerprint is the hex sha256 of the schema script.
func SchemaFingerprint(schemaSQL string) string {
	sum := sha256.Sum256([]byte(schemaSQL))
	return hex.EncodeToString(sum[:])
}

// appliedSchemaFingerprint is the fingerprint of the last applied script, or
// "" when none was recorded (a fresh database, or one bootstrapped before
// fingerprints). Reading the catalog and one row takes no table lock that
// blocks writers.
func appliedSchemaFingerprint(ctx context.Context, conn *pgxpool.Conn) (string, error) {
	var present bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass('food.schema_bootstrap') IS NOT NULL`).Scan(&present); err != nil {
		return "", err
	}
	if !present {
		return "", nil
	}
	var sha string
	err := conn.QueryRow(ctx, `SELECT sha256 FROM food.schema_bootstrap WHERE id = 1`).Scan(&sha)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return sha, err
}
