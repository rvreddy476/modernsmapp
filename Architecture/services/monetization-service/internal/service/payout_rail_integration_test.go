//go:build integration

package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/atpost/monetization-service/database"
	"github.com/atpost/monetization-service/internal/client/razorpayx"
	"github.com/atpost/monetization-service/internal/client/razorpayx/razorpayxtest"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// 4C — submitter, reconciler, webhook, and the ambiguous-response rule
// ---------------------------------------------------------------------------
//
// Every test here runs the real service against the scratch database and
// an httptest stub of RazorpayX (razorpayxtest). No real credentials
// exist yet; the controlled Rs 1 transfer in the plan's acceptance table
// waits for test-mode keys.

const railWebhookSecret = "whsec-test"

type railRig struct {
	ctx   context.Context
	pool  *pgxpool.Pool
	store *postgres.Store
	stub  *razorpayxtest.Server
	svc   *Service
}

func newRailRig(t *testing.T) *railRig {
	t.Helper()
	ctx, pool := openTestPool(t)
	if err := postgres.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatal(err)
	}
	stub := razorpayxtest.NewServer()
	t.Cleanup(stub.Close)
	store := postgres.New(pool)
	client := razorpayx.New(stub.Config(railWebhookSecret))
	svc := enabledPayoutService(store).WithPayoutRail(client, railWebhookSecret)
	return &railRig{ctx: ctx, pool: pool, store: store, stub: stub, svc: svc}
}

// seedRailCreator is seedPayoutCreator plus a bank payout method that
// already has a RazorpayX fund account, so the submitter goes straight
// to CreatePayout. It also removes the rail's own rows on cleanup.
func (r *railRig) seedRailCreator(t *testing.T, balance int64) (creator, method uuid.UUID) {
	t.Helper()
	creator, method = seedPayoutCreator(r.ctx, t, r.pool, healthyPayoutFixture(balance))
	t.Cleanup(func() {
		_, _ = r.pool.Exec(r.ctx, `DELETE FROM payout_provider_events WHERE payout_request_id IN (SELECT id FROM payout_requests WHERE user_id = $1)`, creator)
		_, _ = r.pool.Exec(r.ctx, `DELETE FROM creator_payout_accounts WHERE user_id = $1`, creator)
	})
	if _, err := r.pool.Exec(r.ctx, `
		UPDATE payout_methods
		SET method_type = 'bank_account', rzp_fund_account_id = $2, ifsc = 'HDFC0000053',
		    account_last4 = '6789', holder_name = 'Test Creator', verified_at = NOW(), is_verified = true
		WHERE id = $1`, method, "fa_seed_"+strings.ReplaceAll(method.String(), "-", "")[:12]); err != nil {
		t.Fatal(err)
	}
	return creator, method
}

// requestAged requests a payout and backdates it past the 24 h review
// window so the submitter picks it up on its next run.
func (r *railRig) requestAged(t *testing.T, creator, method uuid.UUID, amount int64) uuid.UUID {
	t.Helper()
	out, err := r.svc.RequestPayout(r.ctx, creator, amount, method)
	if err != nil {
		t.Fatalf("RequestPayout: %v", err)
	}
	if _, err := r.pool.Exec(r.ctx, `UPDATE payout_requests SET requested_at = NOW() - INTERVAL '25 hours' WHERE id = $1`, out.Request.ID); err != nil {
		t.Fatal(err)
	}
	return out.Request.ID
}

type railRow struct {
	Status            string
	ProviderReference *string
	ProviderStatus    *string
	UTR               *string
	FailureReason     *string
	RetryCount        int
	ProcessedAt       *time.Time
	SubmittedAt       *time.Time
	TransactionID     *uuid.UUID
}

func (r *railRig) row(t *testing.T, id uuid.UUID) railRow {
	t.Helper()
	var row railRow
	if err := r.pool.QueryRow(r.ctx, `
		SELECT status, provider_reference, provider_status, utr, failure_reason, retry_count, processed_at, submitted_at, transaction_id
		FROM payout_requests WHERE id = $1`, id).
		Scan(&row.Status, &row.ProviderReference, &row.ProviderStatus, &row.UTR, &row.FailureReason, &row.RetryCount, &row.ProcessedAt, &row.SubmittedAt, &row.TransactionID); err != nil {
		t.Fatal(err)
	}
	return row
}

func (r *railRig) eventConsumed(t *testing.T, eventID string) (exists bool, consumed bool) {
	t.Helper()
	var consumedAt *time.Time
	err := r.pool.QueryRow(r.ctx, `SELECT consumed_at FROM payout_provider_events WHERE provider = 'razorpayx' AND event_id = $1`, eventID).Scan(&consumedAt)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return false, false
		}
		t.Fatal(err)
	}
	return true, consumedAt != nil
}

// submitToProvider runs the submitter once with a healthy stub and
// returns the request id and the provider's payout.
func (r *railRig) submitToProvider(t *testing.T, creator, method uuid.UUID, amount int64) (uuid.UUID, razorpayx.Payout) {
	t.Helper()
	id := r.requestAged(t, creator, method, amount)
	if err := r.svc.SubmitPayouts(r.ctx); err != nil {
		t.Fatalf("SubmitPayouts: %v", err)
	}
	row := r.row(t, id)
	if row.Status != PayoutStatusSubmitted && row.Status != PayoutStatusProcessing {
		t.Fatalf("after submit: status=%s, want submitted/processing", row.Status)
	}
	if row.ProviderReference == nil || row.SubmittedAt == nil {
		t.Fatalf("after submit: provider_reference=%v submitted_at=%v, want both set", row.ProviderReference, row.SubmittedAt)
	}
	ps := r.stub.PayoutsByReference("payout:" + id.String())
	if len(ps) != 1 || ps[0].ID != *row.ProviderReference {
		t.Fatalf("stub payouts for the request = %+v, want exactly the adopted one %s", ps, *row.ProviderReference)
	}
	return id, ps[0]
}

func (r *railRig) webhook(t *testing.T, eventID, eventType string, p razorpayx.Payout) WebhookResult {
	t.Helper()
	body, hdr := r.stub.WebhookRequest(railWebhookSecret, eventID, eventType, p)
	res, err := r.svc.HandleProviderWebhook(r.ctx, hdr, body)
	if err != nil {
		t.Fatalf("HandleProviderWebhook(%s): %v", eventType, err)
	}
	return res
}

// The submitter: a requested payout past the review window is reserved
// and submitted with the request's own idempotency key and reference;
// the amount sent is the NET (after TDS), and the mode is IMPS under
// Rs 2 lakh.
func TestSubmitterSubmitsReservedWithIdempotencyKey(t *testing.T) {
	r := newRailRig(t)
	creator, method := r.seedRailCreator(t, 50_000)
	id, p := r.submitToProvider(t, creator, method, 20_000)

	if p.AmountPaise != 20_000 || p.Mode != "IMPS" || p.ReferenceID != "payout:"+id.String() {
		t.Fatalf("provider payout = %+v, want amount 20000 IMPS ref payout:%s", p, id)
	}
	if keys := r.stub.IdempotencyKeys(); len(keys) != 1 || keys[0] != "payout:"+id.String() {
		t.Fatalf("X-Payout-Idempotency keys sent = %v, want [payout:%s]", keys, id)
	}
	// A young request is left alone.
	out, err := r.svc.RequestPayout(r.ctx, creator, 10_000, method)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.svc.SubmitPayouts(r.ctx); err != nil {
		t.Fatal(err)
	}
	if row := r.row(t, out.Request.ID); row.Status != PayoutStatusRequested {
		t.Fatalf("a request inside the 24 h window was moved to %s", row.Status)
	}
}

// The ambiguous-response rule (plan 4C, verbatim): on timeout the row
// stays reserved, and the next attempt calls ListPayoutsByReference
// FIRST. One match is adopted — no second CreatePayout.
func TestAmbiguousTimeoutAdoptsExistingPayout(t *testing.T) {
	r := newRailRig(t)
	creator, method := r.seedRailCreator(t, 50_000)
	id := r.requestAged(t, creator, method, 20_000)

	// The provider creates the payout but the response is lost.
	r.stub.FailNextCreatePayout(504, true)
	if err := r.svc.SubmitPayouts(r.ctx); err != nil {
		t.Fatalf("SubmitPayouts (timeout): %v", err)
	}
	row := r.row(t, id)
	if row.Status != PayoutStatusReserved || row.ProviderReference != nil {
		t.Fatalf("after a 504: status=%s provider_reference=%v, want reserved with no reference", row.Status, row.ProviderReference)
	}
	if row.RetryCount != 1 {
		t.Fatalf("retry_count after the first attempt = %d, want 1", row.RetryCount)
	}
	if n := r.stub.CreatePayoutCalls(); n != 1 {
		t.Fatalf("CreatePayout calls = %d, want 1", n)
	}

	// Next attempt: look first, adopt the one match.
	if err := r.svc.SubmitPayouts(r.ctx); err != nil {
		t.Fatalf("SubmitPayouts (retry): %v", err)
	}
	if n := r.stub.CreatePayoutCalls(); n != 1 {
		t.Fatalf("CreatePayout calls after the retry = %d, want still 1: the existing payout must be adopted, not resubmitted", n)
	}
	ps := r.stub.PayoutsByReference("payout:" + id.String())
	if len(ps) != 1 {
		t.Fatalf("provider payouts for the reference = %d, want 1", len(ps))
	}
	row = r.row(t, id)
	if row.ProviderReference == nil || *row.ProviderReference != ps[0].ID {
		t.Fatalf("provider_reference = %v, want the adopted payout %s", row.ProviderReference, ps[0].ID)
	}
	if row.Status != PayoutStatusSubmitted && row.Status != PayoutStatusProcessing {
		t.Fatalf("status after adoption = %s, want submitted/processing", row.Status)
	}
	if row.SubmittedAt == nil {
		t.Fatal("submitted_at not set on adoption")
	}
}

// Two payouts carrying our reference is a state we cannot resolve by
// machine: the row is held, a manual review is opened, and nothing is
// submitted again.
func TestAmbiguousTimeoutTwoMatchesHolds(t *testing.T) {
	r := newRailRig(t)
	creator, method := r.seedRailCreator(t, 50_000)
	id := r.requestAged(t, creator, method, 20_000)

	r.stub.FailNextCreatePayout(504, true)
	if err := r.svc.SubmitPayouts(r.ctx); err != nil {
		t.Fatal(err)
	}
	// A second payout with the same reference (e.g. the idempotency
	// window lapsed and something else resubmitted).
	r.stub.AddPayout("payout:"+id.String(), 20_000, "processing")

	if err := r.svc.SubmitPayouts(r.ctx); err != nil {
		t.Fatal(err)
	}
	if n := r.stub.CreatePayoutCalls(); n != 1 {
		t.Fatalf("CreatePayout calls = %d, want 1: an ambiguous match must never be resubmitted", n)
	}
	row := r.row(t, id)
	if row.Status != PayoutStatusHeld {
		t.Fatalf("status = %s, want held", row.Status)
	}
	if row.FailureReason == nil || !strings.Contains(*row.FailureReason, "2 payouts") {
		t.Fatalf("failure_reason = %v, want the ambiguity spelled out", row.FailureReason)
	}
	if n := countRows(r.ctx, t, r.pool, `SELECT count(*) FROM fraud_reviews WHERE creator_id = $1 AND review_type = 'manual' AND status = 'pending'`, creator); n != 1 {
		t.Fatalf("manual fraud_reviews rows = %d, want 1", n)
	}
	// The money is still reserved: nothing has been returned or paid.
	if balance, pending := ledgerState(r.ctx, t, r.pool, creator); balance != 30_000 || pending != 20_000 {
		t.Fatalf("ledger while held: balance=%d pending=%d, want 30000/20000", balance, pending)
	}
}

// paid: the transaction completes, pending_payout is decremented, and
// the ledger leg moves from payout_hold to platform_revenue because the
// money has left. The UTR is captured.
func TestPaidConvergenceCompletesLedger(t *testing.T) {
	r := newRailRig(t)
	creator, method := r.seedRailCreator(t, 50_000)
	id, p := r.submitToProvider(t, creator, method, 20_000)

	p = r.stub.SetPayoutStatus(p.ID, "processed", "UTR0001234", "")
	res := r.webhook(t, "evt_paid_"+id.String(), "payout.processed", p)
	if res.Outcome != WebhookProcessed {
		t.Fatalf("webhook outcome = %s, want processed", res.Outcome)
	}

	row := r.row(t, id)
	if row.Status != PayoutStatusPaid || row.UTR == nil || *row.UTR != "UTR0001234" || row.ProcessedAt == nil {
		t.Fatalf("row after paid: %+v", row)
	}
	var txStatus string
	if err := r.pool.QueryRow(r.ctx, `SELECT status FROM transactions WHERE id = $1`, *row.TransactionID).Scan(&txStatus); err != nil {
		t.Fatal(err)
	}
	if txStatus != "completed" {
		t.Fatalf("transaction status = %s, want completed", txStatus)
	}
	if balance, pending := ledgerState(r.ctx, t, r.pool, creator); balance != 30_000 || pending != 0 {
		t.Fatalf("ledger after paid: balance=%d pending=%d, want 30000/0", balance, pending)
	}
	if n := countRows(r.ctx, t, r.pool, `
		SELECT count(*) FROM ledger_entries le
		JOIN accounts d ON d.id = le.debit_account_id
		JOIN accounts c ON c.id = le.credit_account_id
		WHERE le.idempotency_key = $1 AND d.owner_id = $2 AND d.account_type = 'payout_hold'
		  AND c.owner_id = $3 AND c.account_type = 'platform_revenue' AND le.amount_paise = 20000`,
		"payout_paid:"+id.String(), creator, platformOwnerID); n != 1 {
		t.Fatalf("payout_hold -> platform_revenue legs = %d, want 1", n)
	}
	if exists, consumed := r.eventConsumed(t, "evt_paid_"+id.String()); !exists || !consumed {
		t.Fatalf("provider event exists=%v consumed=%v, want stored and consumed", exists, consumed)
	}
}

// failed: the gross goes back from pending_payout to balance, the
// transaction is failed, the row is kept with its reason, and a negative
// counter-entry lands in tds_ledger so the yearly gross is right again.
func TestFailedConvergenceReturnsFunds(t *testing.T) {
	r := newRailRig(t)
	creator, method := r.seedRailCreator(t, 50_000)
	id, p := r.submitToProvider(t, creator, method, 20_000)

	p = r.stub.SetPayoutStatus(p.ID, "failed", "", "Beneficiary account is invalid")
	res := r.webhook(t, "evt_failed_"+id.String(), "payout.failed", p)
	if res.Outcome != WebhookProcessed {
		t.Fatalf("webhook outcome = %s, want processed", res.Outcome)
	}

	row := r.row(t, id)
	if row.Status != PayoutStatusFailed || row.FailureReason == nil || !strings.Contains(*row.FailureReason, "Beneficiary") {
		t.Fatalf("row after failed: %+v", row)
	}
	var txStatus string
	if err := r.pool.QueryRow(r.ctx, `SELECT status FROM transactions WHERE id = $1`, *row.TransactionID).Scan(&txStatus); err != nil {
		t.Fatal(err)
	}
	if txStatus != "failed" {
		t.Fatalf("transaction status = %s, want failed", txStatus)
	}
	if balance, pending := ledgerState(r.ctx, t, r.pool, creator); balance != 50_000 || pending != 0 {
		t.Fatalf("ledger after failed: balance=%d pending=%d, want 50000/0", balance, pending)
	}
	if n := countRows(r.ctx, t, r.pool, `
		SELECT count(*) FROM ledger_entries le
		JOIN accounts d ON d.id = le.debit_account_id
		JOIN accounts c ON c.id = le.credit_account_id
		WHERE le.idempotency_key = $1 AND d.owner_id = $2 AND d.account_type = 'payout_hold'
		  AND c.owner_id = $2 AND c.account_type = 'user_wallet' AND le.amount_paise = 20000`,
		"payout_return:"+id.String(), creator); n != 1 {
		t.Fatalf("payout_hold -> user_wallet return legs = %d, want 1", n)
	}
	if n := countRows(r.ctx, t, r.pool, `
		SELECT count(*) FROM tds_ledger WHERE creator_id = $1 AND reference_id = $2 AND gross_amount_paise = -20000`, creator, id); n != 1 {
		t.Fatalf("negative tds_ledger counter-entries = %d, want 1", n)
	}
	var yearlyGross int64
	if err := r.pool.QueryRow(r.ctx, `SELECT COALESCE(SUM(gross_amount_paise),0) FROM tds_ledger WHERE creator_id = $1`, creator).Scan(&yearlyGross); err != nil {
		t.Fatal(err)
	}
	if yearlyGross != 0 {
		t.Fatalf("yearly gross after a failed payout = %d, want 0", yearlyGross)
	}
	if n := countRows(r.ctx, t, r.pool, `SELECT count(*) FROM payout_requests WHERE id = $1`, id); n != 1 {
		t.Fatal("the failed request row was removed; it must be kept with its reason")
	}
}

// The same event twice is a no-op: payout_provider_events(provider,
// event_id) is the primary key, the second insert affects zero rows,
// and nothing downstream runs again.
func TestWebhookReplayIsNoOp(t *testing.T) {
	r := newRailRig(t)
	creator, method := r.seedRailCreator(t, 50_000)
	id, p := r.submitToProvider(t, creator, method, 20_000)
	p = r.stub.SetPayoutStatus(p.ID, "processed", "UTR777", "")

	eventID := "evt_replay_" + id.String()
	body, hdr := r.stub.WebhookRequest(railWebhookSecret, eventID, "payout.processed", p)
	first, err := r.svc.HandleProviderWebhook(r.ctx, hdr, body)
	if err != nil || first.Outcome != WebhookProcessed {
		t.Fatalf("first delivery: outcome=%s err=%v", first.Outcome, err)
	}
	second, err := r.svc.HandleProviderWebhook(r.ctx, hdr, body)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if second.Outcome != WebhookReplay {
		t.Fatalf("replay outcome = %s, want replay", second.Outcome)
	}
	if balance, pending := ledgerState(r.ctx, t, r.pool, creator); balance != 30_000 || pending != 0 {
		t.Fatalf("ledger after replay: balance=%d pending=%d, want 30000/0 (unchanged)", balance, pending)
	}
	if n := countRows(r.ctx, t, r.pool, `SELECT count(*) FROM ledger_entries WHERE idempotency_key = $1`, "payout_paid:"+id.String()); n != 1 {
		t.Fatalf("payout_paid legs after replay = %d, want 1", n)
	}
	if n := countRows(r.ctx, t, r.pool, `SELECT count(*) FROM payout_provider_events WHERE provider = 'razorpayx' AND event_id = $1`, eventID); n != 1 {
		t.Fatalf("stored events = %d, want 1", n)
	}
	// A replay does not need the row to still be in flight: the same
	// event after the row is paid is still a replay, not an error.
	if _, err := r.svc.HandleProviderWebhook(r.ctx, hdr, body); err != nil {
		t.Fatalf("third delivery: %v", err)
	}
}

// An event is marked consumed only when a request row was actually
// updated: an event for a payout we do not know, and a stale event that
// no transition accepts, are stored but never consumed.
func TestWebhookEventNotConsumedWhenNoRowUpdated(t *testing.T) {
	r := newRailRig(t)
	creator, method := r.seedRailCreator(t, 50_000)

	// 1. Unknown payout: stored, unmatched, not consumed.
	orphan := razorpayx.Payout{ID: "pout_orphan_" + uuid.NewString()[:8], Status: "processed", AmountPaise: 100, ReferenceID: "payout:" + uuid.NewString()}
	res := r.webhook(t, "evt_orphan_"+orphan.ID, "payout.processed", orphan)
	if res.Outcome != WebhookUnmatched {
		t.Fatalf("orphan outcome = %s, want unmatched", res.Outcome)
	}
	if exists, consumed := r.eventConsumed(t, "evt_orphan_"+orphan.ID); !exists || consumed {
		t.Fatalf("orphan event exists=%v consumed=%v, want stored and NOT consumed", exists, consumed)
	}
	t.Cleanup(func() {
		_, _ = r.pool.Exec(r.ctx, `DELETE FROM payout_provider_events WHERE event_id = $1`, "evt_orphan_"+orphan.ID)
	})

	// 2. Stale event after paid: no transition accepts processing after
	//    paid, so nothing is updated and the event is not consumed.
	id, p := r.submitToProvider(t, creator, method, 20_000)
	p = r.stub.SetPayoutStatus(p.ID, "processed", "UTR1", "")
	if res := r.webhook(t, "evt_paid_"+id.String(), "payout.processed", p); res.Outcome != WebhookProcessed {
		t.Fatalf("paid outcome = %s", res.Outcome)
	}
	stale := p
	stale.Status = "processing"
	stale.UTR = ""
	res = r.webhook(t, "evt_stale_"+id.String(), "payout.pending", stale)
	if res.Outcome != WebhookIgnored {
		t.Fatalf("stale outcome = %s, want ignored", res.Outcome)
	}
	if exists, consumed := r.eventConsumed(t, "evt_stale_"+id.String()); !exists || consumed {
		t.Fatalf("stale event exists=%v consumed=%v, want stored and NOT consumed", exists, consumed)
	}
	if row := r.row(t, id); row.Status != PayoutStatusPaid {
		t.Fatalf("a stale event moved the row to %s", row.Status)
	}
}

// The reconciler: a submitted payout not reconciled in fifteen minutes is
// fetched and converged through the same table; here it has been
// processed at the provider without a webhook ever arriving.
func TestReconcilerConvergesWithoutWebhook(t *testing.T) {
	r := newRailRig(t)
	creator, method := r.seedRailCreator(t, 50_000)
	id, p := r.submitToProvider(t, creator, method, 20_000)
	r.stub.SetPayoutStatus(p.ID, "processed", "UTRRECON", "")

	// Too fresh: nothing happens.
	if err := r.svc.ReconcilePayouts(r.ctx); err != nil {
		t.Fatal(err)
	}
	if row := r.row(t, id); row.Status == PayoutStatusPaid {
		t.Fatal("a payout submitted seconds ago was reconciled; the window is fifteen minutes")
	}
	if _, err := r.pool.Exec(r.ctx, `UPDATE payout_requests SET submitted_at = NOW() - INTERVAL '20 minutes', last_reconciled_at = NULL WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.ReconcilePayouts(r.ctx); err != nil {
		t.Fatal(err)
	}
	row := r.row(t, id)
	if row.Status != PayoutStatusPaid || row.UTR == nil || *row.UTR != "UTRRECON" {
		t.Fatalf("row after reconcile: %+v, want paid with the UTR", row)
	}
	if balance, pending := ledgerState(r.ctx, t, r.pool, creator); balance != 30_000 || pending != 0 {
		t.Fatalf("ledger after reconcile: balance=%d pending=%d, want 30000/0", balance, pending)
	}
}

// A reversal after payment: the money comes back from the bank, so the
// gross returns to balance from platform_revenue (pending_payout was
// already released) and the TDS counter-entry is posted.
func TestReversedAfterPaidReturnsFunds(t *testing.T) {
	r := newRailRig(t)
	creator, method := r.seedRailCreator(t, 50_000)
	id, p := r.submitToProvider(t, creator, method, 20_000)
	p = r.stub.SetPayoutStatus(p.ID, "processed", "UTRX", "")
	if res := r.webhook(t, "evt_p_"+id.String(), "payout.processed", p); res.Outcome != WebhookProcessed {
		t.Fatal(res.Outcome)
	}
	p = r.stub.SetPayoutStatus(p.ID, "reversed", "UTRX", "Returned by beneficiary bank")
	if res := r.webhook(t, "evt_r_"+id.String(), "payout.reversed", p); res.Outcome != WebhookProcessed {
		t.Fatal(res.Outcome)
	}
	if row := r.row(t, id); row.Status != PayoutStatusReversed {
		t.Fatalf("status = %s, want reversed", row.Status)
	}
	if balance, pending := ledgerState(r.ctx, t, r.pool, creator); balance != 50_000 || pending != 0 {
		t.Fatalf("ledger after reversal: balance=%d pending=%d, want 50000/0", balance, pending)
	}
	if n := countRows(r.ctx, t, r.pool, `
		SELECT count(*) FROM ledger_entries le
		JOIN accounts d ON d.id = le.debit_account_id
		JOIN accounts c ON c.id = le.credit_account_id
		WHERE le.idempotency_key = $1 AND d.owner_id = $2 AND d.account_type = 'platform_revenue'
		  AND c.owner_id = $3 AND c.account_type = 'user_wallet' AND le.amount_paise = 20000`,
		"payout_reversal:"+id.String(), platformOwnerID, creator); n != 1 {
		t.Fatalf("platform_revenue -> user_wallet reversal legs = %d, want 1", n)
	}
	if n := countRows(r.ctx, t, r.pool, `SELECT count(*) FROM tds_ledger WHERE creator_id = $1 AND reference_id = $2 AND gross_amount_paise = -20000`, creator, id); n != 1 {
		t.Fatalf("negative tds_ledger counter-entries = %d, want 1", n)
	}
}

// A definitive provider refusal at CreatePayout (4xx) is a failure, not
// an ambiguity: the funds go back at once.
func TestDefinitiveCreateFailureReturnsFunds(t *testing.T) {
	r := newRailRig(t)
	creator, method := r.seedRailCreator(t, 50_000)
	id := r.requestAged(t, creator, method, 20_000)
	r.stub.FailNextCreatePayout(400, false)
	if err := r.svc.SubmitPayouts(r.ctx); err != nil {
		t.Fatal(err)
	}
	row := r.row(t, id)
	if row.Status != PayoutStatusFailed || row.FailureReason == nil {
		t.Fatalf("row after a 400: %+v, want failed with a reason", row)
	}
	if balance, pending := ledgerState(r.ctx, t, r.pool, creator); balance != 50_000 || pending != 0 {
		t.Fatalf("ledger after a refused create: balance=%d pending=%d, want 50000/0", balance, pending)
	}
}

// Over the auto-approve limit: held with a manual review, no submission.
func TestSubmitterHoldsOverAutoApproveLimit(t *testing.T) {
	r := newRailRig(t)
	creator, method := r.seedRailCreator(t, 5_000_000)
	id := r.requestAged(t, creator, method, PayoutAutoApproveLimitPaise+1)
	if err := r.svc.SubmitPayouts(r.ctx); err != nil {
		t.Fatal(err)
	}
	if row := r.row(t, id); row.Status != PayoutStatusHeld {
		t.Fatalf("status = %s, want held", row.Status)
	}
	if n := r.stub.CreatePayoutCalls(); n != 0 {
		t.Fatalf("CreatePayout calls = %d, want 0", n)
	}
	if n := countRows(r.ctx, t, r.pool, `SELECT count(*) FROM fraud_reviews WHERE creator_id = $1 AND review_type = 'manual'`, creator); n != 1 {
		t.Fatalf("manual reviews = %d, want 1", n)
	}
}

// With no client configured the rail is off: the submitter and
// reconciler do nothing and say so, and the webhook is refused.
func TestRailOffWithoutClient(t *testing.T) {
	ctx, pool := openTestPool(t)
	svc := enabledPayoutService(postgres.New(pool))
	if svc.PayoutRailEnabled() {
		t.Fatal("rail reported enabled with no client")
	}
	if err := svc.SubmitPayouts(ctx); err == nil || !strings.Contains(err.Error(), "PAYOUT_RAIL_NOT_CONFIGURED") {
		t.Fatalf("SubmitPayouts without a client: %v, want PAYOUT_RAIL_NOT_CONFIGURED", err)
	}
	if err := svc.ReconcilePayouts(ctx); err == nil || !strings.Contains(err.Error(), "PAYOUT_RAIL_NOT_CONFIGURED") {
		t.Fatalf("ReconcilePayouts without a client: %v, want PAYOUT_RAIL_NOT_CONFIGURED", err)
	}
}
