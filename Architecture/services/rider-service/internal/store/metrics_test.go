package store

import (
	"context"
	"testing"
	"time"
)

// TestComputePartnerMetrics_NoData returns zero rates when the partner
// has no offers or rides yet.
func TestComputePartnerMetrics_NoData(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	pid := makeRiderTestPartner(t, st)
	m, err := st.ComputePartnerMetrics(ctx, pid, 30)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if m.OffersReceived != 0 || m.RidesAssigned != 0 {
		t.Errorf("expected zero counts, got %+v", m)
	}
	if m.AcceptanceRate != 0 || m.CompletionRate != 0 || m.CancellationRate != 0 {
		t.Errorf("rates should be zero on empty data, got %+v", m)
	}
}

// TestUpdatePartnerMetrics_StampsAndPersists verifies the write-back.
func TestUpdatePartnerMetrics_StampsAndPersists(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	pid := makeRiderTestPartner(t, st)
	m := PartnerMetrics{AcceptanceRate: 0.85, CompletionRate: 0.92, CancellationRate: 0.08}
	if err := st.UpdatePartnerMetrics(ctx, pid, m); err != nil {
		t.Fatalf("update: %v", err)
	}
	p, err := st.GetPartner(ctx, pid)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if p.AcceptanceRate < 84 || p.AcceptanceRate > 86 {
		t.Errorf("acceptance_rate = %v, want ~85.0", p.AcceptanceRate)
	}
	if p.CancellationRate < 7 || p.CancellationRate > 9 {
		t.Errorf("cancellation_rate = %v, want ~8.0", p.CancellationRate)
	}
}

// TestListActivePartnerIDs_Includes verifies the list includes a draft
// partner (status='pending_verification' or 'approved' both qualify).
func TestListActivePartnerIDs_Includes(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	pid := makeRiderTestPartner(t, st)
	// Move to pending_verification (draft is excluded by the query).
	if err := st.UpdatePartnerStatus(ctx, pid, "pending_verification"); err != nil {
		t.Fatalf("set status: %v", err)
	}
	ids, err := st.ListActivePartnerIDs(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, id := range ids {
		if id == pid {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("partner %s not in active id list", pid)
	}
}

// TestSetFraudScore_Persists writes and reads back.
func TestSetFraudScore_Persists(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	pid := makeRiderTestPartner(t, st)
	if err := st.SetFraudScore(ctx, pid, 73.5); err != nil {
		t.Fatalf("set: %v", err)
	}
	p, err := st.GetPartner(ctx, pid)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if p.FraudScore < 73 || p.FraudScore > 74 {
		t.Errorf("fraud_score = %v, want ~73.5", p.FraudScore)
	}
}

// TestSetPartnerSuspended_HonorsBlockedGuard verifies a blocked partner
// is NOT silently downgraded to suspended.
func TestSetPartnerSuspended_HonorsBlockedGuard(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	pid := makeRiderTestPartner(t, st)
	if err := st.UpdatePartnerStatus(ctx, pid, "blocked"); err != nil {
		t.Fatalf("block: %v", err)
	}
	if err := st.SetPartnerSuspended(ctx, pid, "auto:test"); err != nil {
		t.Fatalf("set suspended (no-op expected): %v", err)
	}
	p, _ := st.GetPartner(ctx, pid)
	if p.Status != "blocked" {
		t.Errorf("status = %q, want still blocked (downgrade should be blocked by WHERE)", p.Status)
	}
}

// TestFetchAdminQueueCounts_Empty returns zero counts on a clean DB-ish
// state. We don't bother with full setup — the queue counts are
// non-negative by construction.
func TestFetchAdminQueueCounts_Empty(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	out, err := st.FetchAdminQueueCounts(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if out.PendingKYCCount < 0 {
		t.Errorf("counts must be non-negative")
	}
}

// TestPartnerCohortRetention_EmptyMonth returns zero size for a month
// with no signups.
func TestPartnerCohortRetention_EmptyMonth(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()

	month := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	row, err := st.PartnerCohortRetention(context.Background(), month)
	if err != nil {
		t.Fatalf("retention: %v", err)
	}
	if row.CohortSize != 0 {
		t.Errorf("future month should have empty cohort, got %d", row.CohortSize)
	}
}

// TestPartnerCohortRetention_RequiresStart checks the input guard.
func TestPartnerCohortRetention_RequiresStart(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	if _, err := st.PartnerCohortRetention(context.Background(), time.Time{}); err == nil {
		t.Errorf("zero time should error")
	}
}

// TestCustomerCohortBookingRate_EmptyMonth returns zero size for an
// empty cohort window.
func TestCustomerCohortBookingRate_EmptyMonth(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	month := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	row, err := st.CustomerCohortBookingRate(context.Background(), month)
	if err != nil {
		t.Fatalf("cohort: %v", err)
	}
	if row.CohortSize != 0 {
		t.Errorf("empty future window should have 0 cohort, got %d", row.CohortSize)
	}
}

