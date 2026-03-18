package workers

import (
	"context"
	"log/slog"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
)

// ---------------------------------------------------------------------------
// LedgerReconciliation — runs daily at midnight
// ---------------------------------------------------------------------------

func runLedgerReconciliation(ctx context.Context, store *postgres.Store) {
	// Calculate time until next midnight
	now := time.Now()
	next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	timer := time.NewTimer(time.Until(next))

	for {
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			reconcileLedger(ctx, store)
			// Reset timer for next midnight
			now = time.Now()
			next = time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
			timer.Reset(time.Until(next))
		}
	}
}

func reconcileLedger(ctx context.Context, store *postgres.Store) {
	slog.Info("ledger reconciliation: starting")

	offset := 0
	const batchSize = 100
	mismatchCount := 0

	for {
		wallets, err := store.GetAllWallets(ctx, batchSize, offset)
		if err != nil {
			slog.Error("ledger reconciliation: failed to fetch wallets", "error", err)
			return
		}
		if len(wallets) == 0 {
			break
		}

		for _, w := range wallets {
			ledgerBalance, err := store.GetLedgerBalanceForUser(ctx, w.UserID)
			if err != nil {
				slog.Warn("ledger reconciliation: failed to compute ledger balance",
					"user_id", w.UserID, "error", err)
				continue
			}

			walletBalancePaise := w.BalancePaise
			diff := walletBalancePaise - ledgerBalance
			if diff < 0 {
				diff = -diff
			}

			// Mismatch threshold: 100 paise = INR 1
			if diff > 100 {
				mismatchCount++
				slog.Warn("ledger reconciliation: balance mismatch",
					"user_id", w.UserID,
					"wallet_balance_paise", walletBalancePaise,
					"ledger_balance_paise", ledgerBalance,
					"diff_paise", diff,
				)
			}
		}

		offset += batchSize
		if len(wallets) < batchSize {
			break
		}
	}

	slog.Info("ledger reconciliation: completed", "mismatches", mismatchCount)
}

// ---------------------------------------------------------------------------
// StuckTransactionDetector — runs every hour
// ---------------------------------------------------------------------------

func runStuckTransactionDetector(ctx context.Context, store *postgres.Store) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			detectStuckTransactions(ctx, store)
		}
	}
}

func detectStuckTransactions(ctx context.Context, store *postgres.Store) {
	cutoff := time.Now().Add(-24 * time.Hour)
	txns, err := store.GetStuckTransactions(ctx, cutoff)
	if err != nil {
		slog.Error("stuck transaction detector: query failed", "error", err)
		return
	}

	for _, t := range txns {
		slog.Warn("stuck transaction detected",
			"transaction_id", t.ID,
			"wallet_id", t.WalletID,
			"type", t.Type,
			"amount_paise", t.AmountPaise,
			"status", t.Status,
			"created_at", t.CreatedAt,
		)
	}

	if len(txns) > 0 {
		slog.Info("stuck transaction detector: found stuck transactions", "count", len(txns))
	}
}

// ---------------------------------------------------------------------------
// StalePayoutDetector — runs every hour
// ---------------------------------------------------------------------------

func runStalePayoutDetector(ctx context.Context, store *postgres.Store) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			detectStalePayouts(ctx, store)
		}
	}
}

func detectStalePayouts(ctx context.Context, store *postgres.Store) {
	cutoff := time.Now().Add(-48 * time.Hour)
	payouts, err := store.GetStalePayouts(ctx, cutoff)
	if err != nil {
		slog.Error("stale payout detector: query failed", "error", err)
		return
	}

	for _, p := range payouts {
		slog.Warn("stale payout detected",
			"payout_id", p.ID,
			"user_id", p.UserID,
			"transaction_id", p.TransactionID,
			"amount_paise", p.AmountPaise,
			"status", p.Status,
			"requested_at", p.RequestedAt,
		)
	}

	if len(payouts) > 0 {
		slog.Info("stale payout detector: found stale payouts", "count", len(payouts))
	}
}
