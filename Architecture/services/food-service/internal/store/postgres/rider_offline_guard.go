package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/atpost/shared/partnerwork"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Food server lane (9 Nov 2026), Android food audit M2: a rider carrying a
// delivery may not go offline. With partnerwork on, partner_work.domain_duty
// already refuses (409 WORK_BUSY); with it off, the toggle used to succeed
// and leave the rider ACTIVE but "offline", so the app stopped pinging while
// the job was still in hand. Now both paths refuse the same way.

// ErrRiderOnActiveJob refuses going offline while a delivery is held. It
// wraps partnerwork.ErrBusy so the answer is the 409 WORK_BUSY the
// partnerwork path already gives (handler_errors.go writeKnownError), and the
// Android rider app keeps the rider on duty (RiderLocationService
// OFFLINE_REFUSED) whichever path refused.
var ErrRiderOnActiveJob = fmt.Errorf("finish the delivery you are carrying before going offline: %w", partnerwork.ErrBusy)

// offlineRefusedOnJob is the rule: only a request to go OFFLINE, only while
// partnerwork is off (it guards itself when on), only when a job is held.
func offlineRefusedOnJob(online, partnerworkOn, onJob bool) bool {
	return !online && !partnerworkOn && onJob
}

// refuseOfflineOnJobTx locks the partner row and refuses going offline while
// they hold a live job (riderOnJobSQL, the presence worker's on_job rule). A
// caller who is not a delivery partner passes through: the status update
// that follows answers them as before.
func refuseOfflineOnJobTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, online bool) error {
	partnerworkOn := partnerwork.Enabled(os.Getenv)
	if !offlineRefusedOnJob(online, partnerworkOn, true) {
		return nil // nothing could be refused: skip the read
	}
	var onJob bool
	err := tx.QueryRow(ctx, `
		SELECT `+riderOnJobSQL+`
		FROM food.delivery_partners dp
		WHERE dp.user_id = $1
		FOR UPDATE
	`, userID).Scan(&onJob)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if offlineRefusedOnJob(online, partnerworkOn, onJob) {
		return ErrRiderOnActiveJob
	}
	return nil
}
