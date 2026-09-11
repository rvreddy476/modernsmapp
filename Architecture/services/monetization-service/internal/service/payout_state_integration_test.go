//go:build integration

package service

import (
	"testing"

	"github.com/atpost/monetization-service/database"
	"github.com/atpost/monetization-service/internal/store/postgres"
)

// A withdrawal that passes every gate starts in 'requested' (Wave 3), and
// after migration 021 the database itself refuses every status from the
// old eleven-value vocabulary: the collapsed state machine is enforced by
// the CHECK constraint, not only by the transition table.
func TestRequestPayoutRowStartsRequested(t *testing.T) {
	ctx, pool := openTestPool(t)
	if err := postgres.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatal(err)
	}
	store := postgres.New(pool)
	svc := enabledPayoutService(store)

	creator, method := seedPayoutCreator(ctx, t, pool, healthyPayoutFixture(50_000))
	out, err := svc.RequestPayout(ctx, creator, 20_000, method)
	if err != nil {
		t.Fatalf("RequestPayout: %v", err)
	}
	if out.Request.Status != "requested" {
		t.Fatalf("outcome status = %q, want requested", out.Request.Status)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM payout_requests WHERE id = $1`, out.Request.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "requested" {
		t.Fatalf("stored status = %q, want requested", status)
	}

	// The old vocabulary must be unrepresentable once 021 has run.
	for _, old := range []string{"pending", "kyc_check", "approved", "batched", "in_flight", "settled", "returned"} {
		if _, err := pool.Exec(ctx, `UPDATE payout_requests SET status = $2 WHERE id = $1`, out.Request.ID, old); err == nil {
			t.Fatalf("payout_requests accepted the pre-Phase-4 status %q; migration 021 has not collapsed the state machine", old)
		}
	}
	// And every collapsed state is representable.
	for _, s := range []string{"reserved", "submitted", "processing", "paid", "failed", "reversed", "held", "requested"} {
		if _, err := pool.Exec(ctx, `UPDATE payout_requests SET status = $2 WHERE id = $1`, out.Request.ID, s); err != nil {
			t.Fatalf("payout_requests refused status %q: %v", s, err)
		}
	}
}
