package jobs

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/atpost/rider-service/database"
	"github.com/atpost/rider-service/internal/events"
	"github.com/atpost/rider-service/internal/store"
	"github.com/atpost/rider-service/internal/wallet"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// integrationStore returns a *Store backed by TEST_PG_DSN. Skips when
// the DSN is unset (CI may run unit-only).
func integrationStore(t *testing.T) (*store.Store, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping rider jobs integration tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := database.BootstrapSchema(context.Background(), pool); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	return store.New(pool), func() { pool.Close() }
}

// recordingPub records every published event for assertions.
type recordingPub struct {
	graceCalls    []events.SubscriptionGracePayload
	expiredCalls  []events.SubscriptionGracePayload
	renewedCalls  []events.SubscriptionRenewedPayload
	failedCalls   []events.SubscriptionRenewalFailedPayload
	docCalls      []events.DocumentExpiringPayload
	fraudCalls    []events.PartnerFraudFlaggedPayload
	revenueCalls  []events.DailyRevenueReportPayload
	summaryCalls  []events.AdminQueueSummaryPayload
	rideExpired   []uuid.UUID
}

func (p *recordingPub) PublishSubscriptionGracePeriod(_ context.Context, payload events.SubscriptionGracePayload) error {
	p.graceCalls = append(p.graceCalls, payload)
	return nil
}
func (p *recordingPub) PublishSubscriptionExpired(_ context.Context, payload events.SubscriptionGracePayload) error {
	p.expiredCalls = append(p.expiredCalls, payload)
	return nil
}
func (p *recordingPub) PublishSubscriptionRenewed(_ context.Context, payload events.SubscriptionRenewedPayload) error {
	p.renewedCalls = append(p.renewedCalls, payload)
	return nil
}
func (p *recordingPub) PublishSubscriptionRenewalFailed(_ context.Context, payload events.SubscriptionRenewalFailedPayload) error {
	p.failedCalls = append(p.failedCalls, payload)
	return nil
}
func (p *recordingPub) PublishDocumentExpiring(_ context.Context, payload events.DocumentExpiringPayload) error {
	p.docCalls = append(p.docCalls, payload)
	return nil
}
func (p *recordingPub) PublishPartnerFraudFlagged(_ context.Context, payload events.PartnerFraudFlaggedPayload) error {
	p.fraudCalls = append(p.fraudCalls, payload)
	return nil
}
func (p *recordingPub) PublishDailyRevenueReport(_ context.Context, payload events.DailyRevenueReportPayload) error {
	p.revenueCalls = append(p.revenueCalls, payload)
	return nil
}
func (p *recordingPub) PublishAdminQueueSummary(_ context.Context, payload events.AdminQueueSummaryPayload) error {
	p.summaryCalls = append(p.summaryCalls, payload)
	return nil
}
func (p *recordingPub) PublishRideExpired(_ context.Context, rideID uuid.UUID) error {
	p.rideExpired = append(p.rideExpired, rideID)
	return nil
}

// makePartnerFor is a small helper.
func makePartner(t *testing.T, st *store.Store) uuid.UUID {
	t.Helper()
	p, err := st.CreatePartner(context.Background(), store.CreatePartnerInput{
		UserID: uuid.New(), PartnerType: "individual_driver",
		FullName: "Job Test", Phone: "+91" + uuid.New().String()[:10],
	})
	if err != nil {
		t.Fatalf("create partner: %v", err)
	}
	return p.ID
}

// TestRunSubscriptionExpiryChecker_HappyPath verifies the job emits one
// grace-period event for an expiring sub and dedupes the second pass.
func TestRunSubscriptionExpiryChecker_HappyPath(t *testing.T) {
	st, cleanup := integrationStore(t)
	defer cleanup()
	ctx := context.Background()
	pid := makePartner(t, st)
	plan, _ := st.GetPlanByCode(ctx, "basic_199")
	if _, err := st.CreateSubscription(ctx, store.CreateSubscriptionInput{
		PartnerID: pid, PlanID: plan.ID, Status: "active",
		StartsAt: time.Now(), ExpiresAt: time.Now().Add(2 * time.Hour),
	}); err != nil {
		t.Fatalf("create sub: %v", err)
	}
	pub := &recordingPub{}
	count, err := RunSubscriptionExpiryChecker(ctx, st, pub)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if count == 0 {
		t.Errorf("expected at least one reminder emitted")
	}
	// Idempotency: re-run should produce zero new reminders.
	pub2 := &recordingPub{}
	count2, err := RunSubscriptionExpiryChecker(ctx, st, pub2)
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if count2 != 0 {
		t.Errorf("re-run sent %d new reminders; idempotency broken", count2)
	}
}

// TestRunGracePeriodTransition_HappyPath verifies an expired-but-still-
// active sub gets flipped, and the second pass is a no-op.
func TestRunGracePeriodTransition_HappyPath(t *testing.T) {
	st, cleanup := integrationStore(t)
	defer cleanup()
	ctx := context.Background()
	pid := makePartner(t, st)
	plan, _ := st.GetPlanByCode(ctx, "basic_199")
	sub, err := st.CreateSubscription(ctx, store.CreateSubscriptionInput{
		PartnerID: pid, PlanID: plan.ID, Status: "active",
		StartsAt: time.Now().Add(-30 * 24 * time.Hour), ExpiresAt: time.Now().Add(-1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	pub := &recordingPub{}
	count, err := RunGracePeriodTransition(ctx, st, pub)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if count == 0 {
		t.Errorf("expected at least one transition")
	}
	// Verify status moved.
	got, _ := st.GetSubscription(ctx, sub.ID)
	if got.Status != "grace_period" {
		t.Errorf("status = %q, want grace_period", got.Status)
	}
	// Re-run: zero rows.
	count2, err := RunGracePeriodTransition(ctx, st, pub)
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if count2 != 0 {
		t.Errorf("re-run flipped %d more rows; idempotency broken", count2)
	}
}

// TestRunSubscriptionAutoRenewal_WalletSuccess verifies the happy path:
// successful debit extends expires_at and emits 'renewed'.
func TestRunSubscriptionAutoRenewal_WalletSuccess(t *testing.T) {
	st, cleanup := integrationStore(t)
	defer cleanup()
	ctx := context.Background()
	pid := makePartner(t, st)
	plan, _ := st.GetPlanByCode(ctx, "basic_199")
	sub, err := st.CreateSubscription(ctx, store.CreateSubscriptionInput{
		PartnerID: pid, PlanID: plan.ID, Status: "active",
		StartsAt: time.Now(), ExpiresAt: time.Now().Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := st.DB().Exec(ctx, `UPDATE rider_partner_subscriptions SET auto_renew = TRUE WHERE id = $1`, sub.ID); err != nil {
		t.Fatalf("enable auto_renew: %v", err)
	}
	mock := wallet.NewMockClient()
	pub := &recordingPub{}
	processed, err := RunSubscriptionAutoRenewal(ctx, st, mock, pub)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if processed == 0 {
		t.Errorf("expected at least one processed renewal")
	}
	if len(mock.Debits()) == 0 {
		t.Errorf("expected wallet debit")
	}
	if len(pub.renewedCalls) == 0 {
		t.Errorf("expected renewed event")
	}
}

// TestRunSubscriptionAutoRenewal_ThreeStrikeAutoDisable verifies the
// failure branch increments the counter and disables auto_renew at 3.
func TestRunSubscriptionAutoRenewal_ThreeStrikeAutoDisable(t *testing.T) {
	st, cleanup := integrationStore(t)
	defer cleanup()
	ctx := context.Background()
	pid := makePartner(t, st)
	plan, _ := st.GetPlanByCode(ctx, "basic_199")
	sub, err := st.CreateSubscription(ctx, store.CreateSubscriptionInput{
		PartnerID: pid, PlanID: plan.ID, Status: "active",
		StartsAt: time.Now(), ExpiresAt: time.Now().Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := st.DB().Exec(ctx, `UPDATE rider_partner_subscriptions SET auto_renew = TRUE WHERE id = $1`, sub.ID); err != nil {
		t.Fatalf("enable auto_renew: %v", err)
	}
	for i := 1; i <= 3; i++ {
		mock := &alwaysFailWallet{}
		pub := &recordingPub{}
		// Reset the cooldown on each attempt so the candidate is reselected.
		if _, err := st.DB().Exec(ctx, `UPDATE rider_partner_subscriptions SET renewal_attempted_at = NULL WHERE id = $1`, sub.ID); err != nil {
			t.Fatalf("reset cooldown: %v", err)
		}
		if _, err := RunSubscriptionAutoRenewal(ctx, st, mock, pub); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
		if len(pub.failedCalls) == 0 {
			t.Errorf("attempt %d: expected failure event", i)
		}
	}
	// auto_renew should be off now.
	var ar bool
	_ = st.DB().QueryRow(ctx, `SELECT auto_renew FROM rider_partner_subscriptions WHERE id = $1`, sub.ID).Scan(&ar)
	if ar {
		t.Errorf("expected auto_renew=false after 3 strikes")
	}
}

// alwaysFailWallet returns an error from every Debit call.
type alwaysFailWallet struct{}

func (alwaysFailWallet) DebitForSubscription(_ context.Context, _ uuid.UUID, _ int64, _ uuid.UUID, _ string) (*wallet.DebitResult, error) {
	return nil, errors.New("simulated insufficient_balance")
}
func (alwaysFailWallet) RefundSubscription(_ context.Context, _ uuid.UUID, _ int64, _ string) error {
	return nil
}

// TestRunDocumentExpiryReminder_DedupesAcrossRuns is the load-bearing
// idempotency test for the doc-expiry job.
func TestRunDocumentExpiryReminder_DedupesAcrossRuns(t *testing.T) {
	st, cleanup := integrationStore(t)
	defer cleanup()
	ctx := context.Background()
	pid := makePartner(t, st)
	// Create a partner_document with expires_at 2 days from now.
	if _, err := st.DB().Exec(ctx, `
        INSERT INTO rider_partner_documents (partner_id, document_type, file_url, status, expires_at)
        VALUES ($1, 'driving_license', 'https://t/x.pdf', 'approved', NOW() + INTERVAL '2 days')`,
		pid); err != nil {
		t.Fatalf("seed doc: %v", err)
	}
	pub := &recordingPub{}
	first, err := RunDocumentExpiryReminder(ctx, st, pub)
	if err != nil {
		t.Fatalf("run 1: %v", err)
	}
	if first == 0 {
		t.Errorf("expected at least 1 reminder on first run")
	}
	pub2 := &recordingPub{}
	second, err := RunDocumentExpiryReminder(ctx, st, pub2)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if second != 0 {
		t.Errorf("re-run sent %d more reminders; idempotency broken", second)
	}
}

// TestRunPartnerMetricsRecalc_StampsMetricsRecalcAt verifies the job
// touches every active partner.
func TestRunPartnerMetricsRecalc_StampsMetricsRecalcAt(t *testing.T) {
	st, cleanup := integrationStore(t)
	defer cleanup()
	ctx := context.Background()
	pid := makePartner(t, st)
	if err := st.UpdatePartnerStatus(ctx, pid, "approved"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := RunPartnerMetricsRecalc(ctx, st); err != nil {
		t.Fatalf("run: %v", err)
	}
	var ts *time.Time
	_ = st.DB().QueryRow(ctx, `SELECT metrics_recalc_at FROM rider_partners WHERE id = $1`, pid).Scan(&ts)
	if ts == nil {
		t.Errorf("metrics_recalc_at not stamped")
	}
}

// TestRunFraudScoreRecalc_CleanPartnerStaysZero ensures a partner with
// no signal stays at 0 after the job runs.
func TestRunFraudScoreRecalc_CleanPartnerStaysZero(t *testing.T) {
	st, cleanup := integrationStore(t)
	defer cleanup()
	ctx := context.Background()
	pid := makePartner(t, st)
	if err := st.UpdatePartnerStatus(ctx, pid, "approved"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	pub := &recordingPub{}
	if _, err := RunFraudScoreRecalc(ctx, st, pub); err != nil {
		t.Fatalf("run: %v", err)
	}
	p, _ := st.GetPartner(ctx, pid)
	if p.FraudScore != 0 {
		t.Errorf("clean partner fraud_score = %v, want 0", p.FraudScore)
	}
	if len(pub.fraudCalls) != 0 {
		t.Errorf("clean partner should not be flagged")
	}
}

// TestRunDailyRevenueReport_RowsWritten verifies the rollup writes the
// at-least-1 overall row.
func TestRunDailyRevenueReport_RowsWritten(t *testing.T) {
	st, cleanup := integrationStore(t)
	defer cleanup()
	ctx := context.Background()
	pub := &recordingPub{}
	rows, err := runDailyRevenueReportFor(ctx, st, pub, time.Now().UTC().AddDate(0, 0, -1))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rows < 1 {
		t.Errorf("expected at least 1 rollup row, got %d", rows)
	}
	if len(pub.revenueCalls) != 1 {
		t.Errorf("expected 1 revenue event, got %d", len(pub.revenueCalls))
	}
}

// TestRunDailyRevenueReport_Idempotent verifies a re-run for the same
// day produces the same row count + does not duplicate rows.
func TestRunDailyRevenueReport_Idempotent(t *testing.T) {
	st, cleanup := integrationStore(t)
	defer cleanup()
	ctx := context.Background()

	day := time.Now().UTC().AddDate(0, 0, -2) // pin to a specific day
	if _, err := runDailyRevenueReportFor(ctx, st, nil, day); err != nil {
		t.Fatalf("first: %v", err)
	}
	var firstCount int
	_ = st.DB().QueryRow(ctx, `SELECT COUNT(*) FROM rider_daily_revenue WHERE computed_at >= NOW() - INTERVAL '1 hour'`).Scan(&firstCount)
	if _, err := runDailyRevenueReportFor(ctx, st, nil, day); err != nil {
		t.Fatalf("second: %v", err)
	}
	var secondCount int
	_ = st.DB().QueryRow(ctx, `SELECT COUNT(*) FROM rider_daily_revenue WHERE computed_at >= NOW() - INTERVAL '1 hour'`).Scan(&secondCount)
	if secondCount != firstCount {
		t.Errorf("re-run grew row count from %d to %d; idempotency broken", firstCount, secondCount)
	}
}

// TestRunAdminQueueSummary_PublishesEvent verifies one event per call.
func TestRunAdminQueueSummary_PublishesEvent(t *testing.T) {
	st, cleanup := integrationStore(t)
	defer cleanup()
	pub := &recordingPub{}
	n, err := RunAdminQueueSummary(context.Background(), st, pub)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if n != 1 || len(pub.summaryCalls) != 1 {
		t.Errorf("expected 1 publish, got n=%d calls=%d", n, len(pub.summaryCalls))
	}
}

// TestRunAdminQueueSummary_NilPubReturnsZero verifies the no-op publisher
// path returns 0 (nothing to do).
func TestRunAdminQueueSummary_NilPubReturnsZero(t *testing.T) {
	st, cleanup := integrationStore(t)
	defer cleanup()
	n, err := RunAdminQueueSummary(context.Background(), st, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if n != 0 {
		t.Errorf("nil publisher should produce n=0, got %d", n)
	}
}

// TestRunOfferExpiry_NoErrorOnEmptyDB verifies the offer-expiry sweeper
// runs cleanly against an empty schema.
func TestRunOfferExpiry_NoErrorOnEmptyDB(t *testing.T) {
	st, cleanup := integrationStore(t)
	defer cleanup()
	if _, err := RunOfferExpiry(context.Background(), st); err != nil {
		t.Fatalf("run: %v", err)
	}
}
