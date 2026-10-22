// Command fieldcrypt-backfill re-seals chat content written before field
// encryption at rest (22 Oct 2026) in Scylla and PostgreSQL, and optionally
// deletes the unencrypted Redis lists chat_messages:{conversation} that
// message-service used to write and never read.
//
// It reads the same environment as message-service (POSTGRES_DSN,
// SCYLLA_HOSTS, SCYLLA_KEYSPACE, REDIS_*, APP_ENV and the key ring:
// CHAT_KMS_KEY_ID or CHAT_FIELD_KEYS) and builds the same key ring, so it
// runs inside a message-service pod:
//
//	kubectl exec deploy/chat-message-service -- fieldcrypt-backfill -batch 500
//	kubectl exec deploy/chat-message-service -- fieldcrypt-backfill -redis
//
// Deploy the new message-service first: it creates the tables this uses
// (chat.field_key_ring, chat.field_backfill_progress) at boot.
//
// Safe to stop and re-run at any point: values already sealed are skipped,
// every rewrite is compare-and-set against what was read, and each table's
// resume point is saved after every batch (-restart walks from the start).
// It refuses to run without a key ring and logs counts only.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/atpost/chat-message-service/internal/config"
	"github.com/atpost/chat-message-service/internal/fieldbackfill"
	"github.com/atpost/chat-message-service/internal/fieldkeys"
	"github.com/atpost/chat-message-service/internal/kmsclient"
	pgStore "github.com/atpost/chat-message-service/internal/store/postgres"
	scyllaStore "github.com/atpost/chat-message-service/internal/store/scylla"
	"github.com/atpost/chat-shared/fieldcrypt"
	"github.com/atpost/chat-shared/logging"
	"github.com/atpost/chat-shared/transport"
	"github.com/gocql/gocql"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func main() {
	batch := flag.Int("batch", 500, "rows per batch")
	maxBatches := flag.Int("max-batches", 0, "batches per table in this run (0 = until done)")
	only := flag.String("only", "", "comma-separated table names to walk (default: all)")
	restart := flag.Bool("restart", false, "ignore saved progress and walk every table from the start")
	redisLists := flag.Bool("redis", false, "also delete the legacy Redis lists chat_messages:*")
	skipTables := flag.Bool("skip-tables", false, "do not walk the tables (with -redis: Redis only)")
	flag.Parse()

	logger := logging.New("fieldcrypt-backfill")
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, logger, *batch, *maxBatches, *only, *restart, *redisLists, *skipTables); err != nil {
		logger.Error("fieldcrypt backfill failed", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger, batch, maxBatches int, only string, restart, redisLists, skipTables bool) error {
	cfg := config.Load()
	pool, err := pgxpool.New(ctx, cfg.PostgresDSN)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("postgres ping: %w", err)
	}

	keys, err := fieldkeys.Load(ctx, fieldkeys.Deps{
		Getenv: os.Getenv,
		NewWrapper: func(ctx context.Context, keyID string) (fieldcrypt.KeyWrapper, error) {
			return kmsclient.New(ctx, keyID)
		},
		Store: pgStore.NewFieldKeyRingStore(pool),
		Log:   logger,
	})
	if err != nil {
		return err
	}
	if !keys.Cipher.Enabled() {
		return fieldbackfill.ErrNoKeyRing
	}

	if !skipTables {
		cluster := gocql.NewCluster(cfg.ScyllaHosts...)
		cluster.Keyspace = cfg.ScyllaKeyspace
		cluster.Consistency = gocql.LocalQuorum
		cluster.SerialConsistency = gocql.LocalSerial
		cluster.Timeout = 30 * time.Second
		session, err := cluster.CreateSession()
		if err != nil {
			return fmt.Errorf("scylla: %w", err)
		}
		defer session.Close()

		conv := pgStore.New(pool)
		msgs := scyllaStore.New(session)
		targets := append(msgs.BackfillTargets(), conv.BackfillTargets()...)
		targets, err = selectTargets(targets, only)
		if err != nil {
			return err
		}
		reports, err := fieldbackfill.Run(ctx, keys.Cipher, targets, conv,
			fieldbackfill.Options{Batch: batch, MaxBatches: maxBatches, Restart: restart}, logger)
		for _, r := range reports {
			logger.Info("fieldcrypt backfill: summary", "target", r.Name, "scanned", r.Scanned, "sealed", r.Sealed, "done", r.Done)
		}
		if err != nil {
			return err
		}
	}

	if redisLists {
		rdb, err := transport.NewRedisClientFromEnv(cfg.RedisAddr)
		if err != nil {
			return fmt.Errorf("redis: %w", err)
		}
		defer func() { _ = rdb.Close() }()
		n, err := deleteLegacyLists(ctx, rdb)
		logger.Info("fieldcrypt backfill: legacy Redis lists deleted", "count", n)
		if err != nil {
			return err
		}
	}
	return nil
}

func selectTargets(all []fieldbackfill.Target, only string) ([]fieldbackfill.Target, error) {
	if strings.TrimSpace(only) == "" {
		return all, nil
	}
	byName := map[string]fieldbackfill.Target{}
	var names []string
	for _, t := range all {
		byName[t.Name()] = t
		names = append(names, t.Name())
	}
	var out []fieldbackfill.Target
	for _, n := range strings.Split(only, ",") {
		n = strings.TrimSpace(n)
		t, ok := byName[n]
		if !ok {
			return nil, fmt.Errorf("unknown table %q; known: %s", n, strings.Join(names, ", "))
		}
		out = append(out, t)
	}
	return out, nil
}

// deleteLegacyLists removes chat_messages:{conversation} lists with SCAN (never
// KEYS) and returns how many it deleted.
func deleteLegacyLists(ctx context.Context, rdb *redis.Client) (int64, error) {
	var deleted int64
	var cursor uint64
	for {
		keys, next, err := rdb.Scan(ctx, cursor, "chat_messages:*", 500).Result()
		if err != nil {
			return deleted, err
		}
		if len(keys) > 0 {
			n, err := rdb.Del(ctx, keys...).Result()
			deleted += n
			if err != nil {
				return deleted, err
			}
		}
		if next == 0 {
			return deleted, nil
		}
		cursor = next
	}
}
