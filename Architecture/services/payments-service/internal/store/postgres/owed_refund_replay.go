package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// A provider refund of an owed capture is counted once, by its provider
// refund id, in payments.refund_required_refunds.
//
// applyOwedCaptureRefundTx adds the event's amount to what the row already
// has refunded and refuses any total past the owed amount BEFORE its insert
// reaches ON CONFLICT DO NOTHING. So the same refund redelivered under a new
// event id (Razorpay sends refund.created and refund.processed for one
// refund, and retries either) was summed a second time and refused once the
// row was refunded in full: "refunds of 236000 on an owed payment of
// 118000". Nothing was double counted, but a legitimate replay was rejected
// as a provider amount mismatch, rolled back with its inbox row, retried by
// the provider forever and alarmed every time.
//
// The intent path (applyProviderRefundTx) has always checked its refund
// ledger first and done the arithmetic second. applyOwedCaptureRefundOnceTx
// gives the owed path the same order: a refund id already in the ledger is a
// no-op and never reaches the arithmetic. The owed row is already locked FOR
// UPDATE by owedCaptureForRefundTx, so two deliveries of one refund are
// serialised on it, and the ledger's primary key still backs the check.

// owedRefundRecordedTx reports whether this provider refund is already in
// the owed-capture refund ledger.
func owedRefundRecordedTx(ctx context.Context, tx pgx.Tx, provider, providerRefundID string) (bool, error) {
	var recorded bool
	err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM payments.refund_required_refunds
		                 WHERE provider = $1 AND provider_refund_id = $2)`,
		provider, providerRefundID).Scan(&recorded)
	return recorded, err
}

// applyOwedCaptureRefundOnceTx is applyOwedCaptureRefundTx for a refund not
// yet counted, and a no-op (false, nil) for one that is.
func applyOwedCaptureRefundOnceTx(ctx context.Context, tx pgx.Tx, o *OwedCapture, intentCurrency string,
	e WebhookEffect, providerRefundID string) (bool, error) {
	recorded, err := owedRefundRecordedTx(ctx, tx, e.Provider, providerRefundID)
	if err != nil {
		return false, err
	}
	if recorded {
		return false, nil // this refund is already counted
	}
	return applyOwedCaptureRefundTx(ctx, tx, o, intentCurrency, e, providerRefundID)
}
