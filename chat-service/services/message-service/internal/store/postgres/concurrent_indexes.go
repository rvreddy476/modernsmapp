package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/atpost/chat-message-service/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// concurrentIndexLockKey serialises online index builds across replicas.
const concurrentIndexLockKey int64 = 0x63686174_69647863 // "chatidxc"

var indexNameRE = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// indexConn is the part of a dedicated connection the builder uses.
type indexConn interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ValidateConcurrentIndexes checks the static list: every entry is a single
// CREATE INDEX CONCURRENTLY IF NOT EXISTS statement for its own name in
// schema chat. BuildConcurrentIndexes refuses an invalid list.
func ValidateConcurrentIndexes(indexes []database.ConcurrentIndex) error {
	seen := map[string]bool{}
	for _, ix := range indexes {
		if !indexNameRE.MatchString(ix.Name) {
			return fmt.Errorf("concurrent index %q: bad name", ix.Name)
		}
		if seen[ix.Name] {
			return fmt.Errorf("concurrent index %q listed twice", ix.Name)
		}
		seen[ix.Name] = true
		want := "CREATE INDEX CONCURRENTLY IF NOT EXISTS " + ix.Name + " ON chat."
		if !strings.HasPrefix(ix.SQL, want) {
			return fmt.Errorf("concurrent index %q: statement must start with %q", ix.Name, want)
		}
		if strings.Contains(ix.SQL, ";") {
			return fmt.Errorf("concurrent index %q: one statement only", ix.Name)
		}
	}
	return nil
}

// BuildConcurrentIndexes builds the listed indexes online (audit L9). Each
// statement runs alone on a dedicated connection, outside any transaction,
// so CONCURRENTLY is allowed and writers are never blocked. Only one replica
// builds at a time: the others return at once (nil) when the advisory lock
// is taken. Meant to run in the background after BootstrapSchema.
func BuildConcurrentIndexes(ctx context.Context, pool poolAcquirer, indexes []database.ConcurrentIndex, log *slog.Logger) error {
	if err := ValidateConcurrentIndexes(indexes); err != nil {
		return err
	}
	if log == nil {
		log = slog.Default()
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, concurrentIndexLockKey).Scan(&got); err != nil {
		return err
	}
	if !got {
		log.Info("online index build: another replica holds the build lock; skipping")
		return nil
	}
	defer func() {
		if _, err := conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, concurrentIndexLockKey); err != nil {
			_ = conn.Conn().Close(context.Background())
		}
	}()
	return buildIndexes(ctx, conn, indexes, log)
}

func buildIndexes(ctx context.Context, conn indexConn, indexes []database.ConcurrentIndex, log *slog.Logger) error {
	for _, ix := range indexes {
		var valid bool
		err := conn.QueryRow(ctx, `
			SELECT i.indisvalid
			FROM pg_index i
			JOIN pg_class c ON c.oid = i.indexrelid
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = 'chat' AND c.relname = $1`, ix.Name).Scan(&valid)
		switch {
		case err == nil && valid:
			continue
		case err == nil && !valid:
			// An interrupted CONCURRENTLY build leaves an INVALID index that
			// IF NOT EXISTS would keep forever (and writers still maintain).
			log.Warn("online index build: dropping invalid leftover", "index", ix.Name)
			if _, err := conn.Exec(ctx, `DROP INDEX CONCURRENTLY IF EXISTS chat.`+ix.Name); err != nil {
				return fmt.Errorf("drop invalid index %s: %w", ix.Name, err)
			}
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("inspect index %s: %w", ix.Name, err)
		}
		log.Info("online index build: building", "index", ix.Name)
		if _, err := conn.Exec(ctx, ix.SQL); err != nil {
			return fmt.Errorf("build index %s: %w", ix.Name, err)
		}
		log.Info("online index build: built", "index", ix.Name)
	}
	return nil
}
