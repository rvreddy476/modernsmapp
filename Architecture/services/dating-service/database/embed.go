package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	_ "embed"

	"github.com/atpost/shared/store/migrationrunner"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed setup.sql
var SetupSQL string

// bootstrapLockService names dating-service's per-service advisory lock (the
// migrationrunner key every service's schema step serialises on).
const bootstrapLockService = "dating-service"

// BootstrapLockTimeout bounds every lock the schema script waits for.
const BootstrapLockTimeout = "5s"

// BootstrapSchema applies the dating-service schema on startup so a fresh
// database has the Pulse tables before runtime consumers start.
//
// ─── WHY IT IS LOCKED, FINGERPRINTED AND HAS A LOCK TIMEOUT (audit M4) ──
//
// The server, the north-star, echo-refresher, data-exporter and data-purger
// binaries all call this at boot. Applying setup.sql takes table locks (ADD
// COLUMN IF NOT EXISTS takes ACCESS EXCLUSIVE before it checks), all held
// until the script commits; a new pod queued behind a long deck read would
// stall every profile read and write behind it. So:
//   - the script runs under migrationrunner.WithLock on dating-service's
//     advisory key, so two replicas or binaries never apply it at once;
//   - its sha256 is recorded in dating_schema_bootstrap when it commits, and a
//     boot whose script is unchanged skips it entirely (an ordinary restart or
//     scale-out takes no table lock at all);
//   - a boot that does apply it runs in one explicit transaction with
//     SET LOCAL lock_timeout (BootstrapLockTimeout), so a busy table fails the
//     boot fast (the pod retries; old pods keep serving);
//   - setup.sql re-adds a CHECK constraint only when it is missing or changed,
//     NOT VALID, and validateSetupConstraints validates it after the commit
//     under SHARE UPDATE EXCLUSIVE, which blocks no reads or writes.
//
// A fresh database has no fingerprint, so it always gets the whole script.
func BootstrapSchema(ctx context.Context, db *pgxpool.Pool) error {
	if db == nil {
		return fmt.Errorf("db pool is nil")
	}
	if strings.TrimSpace(SetupSQL) == "" {
		return fmt.Errorf("schema sql is empty")
	}
	if err := migrationrunner.WithLock(ctx, db, bootstrapLockService, bootstrapLocked); err != nil {
		return err
	}
	// Numbered migrations after setup.sql (migrations.go; audit H2 outbox).
	return ApplyMigrations(ctx, db)
}

// bootstrapLocked is BootstrapSchema's work on the connection that holds the
// advisory lock.
func bootstrapLocked(ctx context.Context, conn *pgxpool.Conn) error {
	fingerprint := SchemaFingerprint(SetupSQL)
	applied, err := appliedSchemaFingerprint(ctx, conn)
	if err != nil {
		return fmt.Errorf("read dating schema fingerprint: %w", err)
	}
	if applied != fingerprint {
		if err := applySchema(ctx, conn, fingerprint); err != nil {
			return err
		}
	}
	// Every boot: finish any constraint a previous apply left NOT VALID
	// (a catalog read when there is none).
	return validateSetupConstraints(ctx, conn)
}

func applySchema(ctx context.Context, conn *pgxpool.Conn, fingerprint string) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = '"+BootstrapLockTimeout+"'"); err != nil {
		return fmt.Errorf("set dating schema lock_timeout: %w", err)
	}
	if _, err := tx.Exec(ctx, SetupSQL); err != nil {
		return fmt.Errorf("apply dating schema: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS dating_schema_bootstrap (
			id SMALLINT PRIMARY KEY CHECK (id = 1),
			sha256 TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`); err != nil {
		return fmt.Errorf("create dating schema fingerprint table: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO dating_schema_bootstrap (id, sha256, applied_at) VALUES (1, $1, NOW())
		ON CONFLICT (id) DO UPDATE SET sha256 = EXCLUDED.sha256, applied_at = NOW()
	`, fingerprint); err != nil {
		return fmt.Errorf("record dating schema fingerprint: %w", err)
	}
	return tx.Commit(ctx)
}

// setupConstraintTagPrefix marks a constraint dating_ensure_check_constraint
// added (its comment). Only those are validated here: constraints added
// NOT VALID on purpose (dating_reports_reason_chk keeps legacy rows) carry
// no tag and stay as they are.
const setupConstraintTagPrefix = "setup.sql:"

// validateSetupConstraints validates every tagged CHECK constraint that is
// still NOT VALID, one statement each, outside the schema transaction.
// VALIDATE CONSTRAINT scans the table under SHARE UPDATE EXCLUSIVE, which
// lets reads and writes continue. A row that breaks the constraint fails the
// boot, as the old validating ADD CONSTRAINT did.
func validateSetupConstraints(ctx context.Context, conn *pgxpool.Conn) error {
	rows, err := conn.Query(ctx, `
		SELECT c.conrelid::regclass::text, c.conname
		FROM pg_constraint c
		WHERE c.contype = 'c' AND NOT c.convalidated
		  AND obj_description(c.oid, 'pg_constraint') LIKE $1 || '%'
		ORDER BY 1, 2`, setupConstraintTagPrefix)
	if err != nil {
		return fmt.Errorf("list unvalidated dating constraints: %w", err)
	}
	type pending struct{ table, name string }
	var todo []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.table, &p.name); err != nil {
			rows.Close()
			return err
		}
		todo = append(todo, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range todo {
		if err := validateConstraint(ctx, conn, p.table, p.name); err != nil {
			return err
		}
	}
	return nil
}

func validateConstraint(ctx context.Context, conn *pgxpool.Conn, table, name string) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = '"+BootstrapLockTimeout+"'"); err != nil {
		return err
	}
	// table is regclass::text (already quoted where needed); name is an
	// identifier read from the catalog.
	stmt := ValidateConstraintSQL(table, name)
	if _, err := tx.Exec(ctx, stmt); err != nil {
		return fmt.Errorf("validate %s.%s: %w", table, name, err)
	}
	return tx.Commit(ctx)
}

// ValidateConstraintSQL is the VALIDATE statement for a catalog constraint.
func ValidateConstraintSQL(regclassText, constraint string) string {
	return "ALTER TABLE " + regclassText + " VALIDATE CONSTRAINT " + pgx.Identifier{constraint}.Sanitize()
}

// SchemaFingerprint is the hex sha256 of the schema script.
func SchemaFingerprint(schemaSQL string) string {
	sum := sha256.Sum256([]byte(schemaSQL))
	return hex.EncodeToString(sum[:])
}

// ForgetSchemaFingerprint clears the recorded fingerprint so the next
// BootstrapSchema re-applies the whole script. For tests that prove a
// backfill (a fresh deploy of a changed script does the same). It takes the
// boot lock, so it never races a boot.
func ForgetSchemaFingerprint(ctx context.Context, db *pgxpool.Pool) error {
	return migrationrunner.WithLock(ctx, db, bootstrapLockService, func(ctx context.Context, conn *pgxpool.Conn) error {
		_, err := conn.Exec(ctx, `DO $$ BEGIN
			IF to_regclass('dating_schema_bootstrap') IS NOT NULL THEN DELETE FROM dating_schema_bootstrap; END IF;
		END $$`)
		return err
	})
}

// appliedSchemaFingerprint is the fingerprint of the last applied script, or
// "" when none was recorded (a fresh database, or one bootstrapped before
// fingerprints). Reading the catalog and one row takes no table lock that
// blocks writers.
func appliedSchemaFingerprint(ctx context.Context, conn *pgxpool.Conn) (string, error) {
	var present bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass('dating_schema_bootstrap') IS NOT NULL`).Scan(&present); err != nil {
		return "", err
	}
	if !present {
		return "", nil
	}
	var sha string
	err := conn.QueryRow(ctx, `SELECT sha256 FROM dating_schema_bootstrap WHERE id = 1`).Scan(&sha)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return sha, err
}
