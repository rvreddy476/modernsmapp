// Command expirer is a periodic worker that flips stale top-ups to failed.
//
// A pending top-up older than TopUpExpirySeconds (default 30 minutes) means
// the user opened the UPI Intent but never completed the transfer at their
// bank. We flip status to 'failed', refund pending_in_paise, and emit
// wallet.topup.failed.
//
// Run as a cron / k8s CronJob every 5 minutes. Idempotent: re-running over a
// row already failed is a no-op.
package main

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/wallet-service/database"
	"github.com/atpost/wallet-service/internal/bank"
	"github.com/atpost/wallet-service/internal/service"
	"github.com/atpost/wallet-service/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logging.Init(logging.Config{ServiceName: "wallet-expirer"})

	pgDSN := os.Getenv("POSTGRES_DSN")
	expirySecs := 1800
	if v := os.Getenv("TOPUP_EXPIRY_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			expirySecs = n
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	dbPool, err := pgxpool.New(ctx, pgDSN)
	if err != nil {
		slog.Error("failed to connect to postgres", "error", err)
		os.Exit(1)
	}
	defer dbPool.Close()

	if err := database.BootstrapSchema(ctx, dbPool); err != nil {
		slog.Error("failed to bootstrap wallet schema", "error", err)
		os.Exit(1)
	}

	walletStore := store.New(dbPool)
	walletSvc := service.New(walletStore, bank.NewMockClient(), service.Config{
		TopUpExpirySeconds: expirySecs,
	})

	expired, err := walletSvc.ExpireStaleTopUps(ctx)
	if err != nil {
		slog.Error("expire stale top-ups failed", "error", err)
		os.Exit(1)
	}

	purged, err := walletStore.PurgeExpiredIdempotency(ctx)
	if err != nil {
		slog.Warn("purge expired idempotency failed", "error", err)
	}

	slog.Info("wallet-expirer run complete",
		"expired_top_ups", expired,
		"purged_idempotency_keys", purged,
		"cutoff_seconds", expirySecs,
	)
}
