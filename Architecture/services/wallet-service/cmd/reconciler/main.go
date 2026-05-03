// Command reconciler nightly compares our wallet.transactions mirror against
// the partner bank's settlement file.
//
// BC-of-PPI MODEL: the partner bank is the source of truth for funds. We
// snapshot a daily settlement file the bank publishes, sum it by user_id,
// and look for discrepancies vs our wallet.transactions sums. Any mismatch
// is written to wallet.partner_bank_settlements.discrepancies as JSON for
// the on-call engineer to inspect; status moves from 'pending' to either
// 'reconciled' or 'discrepancy'.
//
// v1: file ingestion is stubbed — the actual settlement-file format is
// negotiated with the partner once the BC agreement is signed. The job
// records a 'pending' settlement row with zero counts so production gets
// the cron wiring right ahead of the contract.
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"time"

	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/wallet-service/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logging.Init(logging.Config{ServiceName: "wallet-reconciler"})

	pgDSN := os.Getenv("POSTGRES_DSN")
	settlementFileRef := os.Getenv("SETTLEMENT_FILE_REF") // s3://… handed in by the cron

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dbPool, err := pgxpool.New(ctx, pgDSN)
	if err != nil {
		slog.Error("failed to connect to postgres", "error", err)
		os.Exit(1)
	}
	defer dbPool.Close()

	if err := database.BootstrapSchema(ctx, dbPool); err != nil {
		slog.Error("bootstrap schema", "error", err)
		os.Exit(1)
	}

	// v1 stub: insert a pending settlement row covering yesterday. The real
	// fetch + parse + compare logic ships once the partner-bank API is wired.
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	id := uuid.New()
	stubDiscrepancies, _ := json.Marshal(map[string]any{
		"note": "v1 stub: settlement-file ingestion pending BC contract",
	})
	const q = `
        INSERT INTO wallet.partner_bank_settlements (
            id, settlement_date, settlement_file_ref, total_paise, transaction_count,
            status, discrepancies, created_at
        ) VALUES ($1, $2, NULLIF($3, ''), 0, 0, 'pending', $4, now())`
	if _, err := dbPool.Exec(ctx, q, id, yesterday, settlementFileRef, stubDiscrepancies); err != nil {
		slog.Error("insert settlement row", "error", err)
		os.Exit(1)
	}

	slog.Info("wallet-reconciler stub run complete",
		"settlement_id", id,
		"settlement_date", yesterday,
		"file_ref_present", settlementFileRef != "",
	)
}
