//go:build integration

// Admin console (Wave 2 — Money) against a real scratch database
// (MONETIZATION_POSTGRES_DSN must name a *_test database, e.g.
// monetization_it_test, carrying the analytics schema too):
//
//   - every admin write through the token family writes exactly one
//     monetization_audit_log row whose performer is the token's act;
//   - the row is in the SAME transaction as the change: when the audit
//     insert fails, the change is rolled back;
//   - a settlement run writes a start and a finish row sharing a run_id;
//   - the stats route counts seeded data.
package http

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"testing"
	"time"

	"github.com/atpost/monetization-service/database"
	"github.com/atpost/monetization-service/internal/service"
	pgstore "github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type adminIT struct {
	*adminTokenRig
	ctx  context.Context
	pool *pgxpool.Pool
	r    *gin.Engine
}

func newAdminIT(t *testing.T) *adminIT {
	t.Helper()
	dsn := requireTestDSN(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pgstore.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatal(err)
	}
	rg := newAdminTokenRig(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	New(service.New(pgstore.New(pool), nil)).
		WithInternalKey(adminTestInternalKey).
		WithServiceAuth(rg.v).
		WithWritesEnabled(true).
		RegisterRoutes(r)
	it := &adminIT{adminTokenRig: rg, ctx: ctx, pool: pool, r: r}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM monetization_audit_log WHERE performer_id = $1`, rg.actor)
	})
	return it
}

func (it *adminIT) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := it.pool.Exec(it.ctx, sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func (it *adminIT) call(t *testing.T, actor uuid.UUID, perm, method, path, body string) (int, json.RawMessage) {
	t.Helper()
	tok := it.mint(t, it.admin, AudienceMonetization, []string{perm}, actor.String())
	w := adminServe(it.r, method, InternalAdminPrefix+path, body, bearer(tok))
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if env.Data == nil {
		env.Data = w.Body.Bytes()
	}
	return w.Code, env.Data
}

type auditRow struct {
	Table, Op string
	NewData   map[string]any
	OldData   map[string]any
}

func (it *adminIT) auditRows(t *testing.T, actor uuid.UUID) []auditRow {
	t.Helper()
	rows, err := it.pool.Query(it.ctx, `
		SELECT table_name, operation, COALESCE(new_data, 'null'::jsonb), COALESCE(old_data, 'null'::jsonb)
		FROM monetization_audit_log WHERE performer_id = $1 ORDER BY created_at, id`, actor)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var r auditRow
		var nd, od []byte
		if err := rows.Scan(&r.Table, &r.Op, &nd, &od); err != nil {
			t.Fatal(err)
		}
		_ = json.Unmarshal(nd, &r.NewData)
		_ = json.Unmarshal(od, &r.OldData)
		out = append(out, r)
	}
	return out
}

func testRegion() string {
	const letters = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	b := []byte("T")
	for i := 0; i < 5; i++ {
		b = append(b, letters[rand.Intn(len(letters))])
	}
	return string(b)
}

// Every admin write on the token path: exactly one audit row, performer =
// act, the expected table and operation, via admin-service.
func TestAdminTokenIntegration_EveryWriteAuditedWithAct(t *testing.T) {
	it := newAdminIT(t)
	creator := uuid.New()
	region := testRegion()
	period := "2091-03"
	txnID := uuid.New()
	disputeID := uuid.New()
	reviewID := uuid.New()
	earningID := uuid.New()
	t.Cleanup(func() {
		ctx := it.ctx
		_, _ = it.pool.Exec(ctx, `DELETE FROM refunds WHERE transaction_id = $1`, txnID)
		_, _ = it.pool.Exec(ctx, `DELETE FROM disputes WHERE id = $1`, disputeID)
		_, _ = it.pool.Exec(ctx, `DELETE FROM transactions WHERE wallet_id = $1`, creator)
		_, _ = it.pool.Exec(ctx, `DELETE FROM fraud_reviews WHERE id = $1`, reviewID)
		_, _ = it.pool.Exec(ctx, `DELETE FROM creator_fund_earnings WHERE creator_id = $1`, creator)
		_, _ = it.pool.Exec(ctx, `DELETE FROM creator_fund_period_settlements WHERE creator_id = $1`, creator)
		_, _ = it.pool.Exec(ctx, `DELETE FROM creator_fund_eligibility WHERE creator_id = $1`, creator)
		_, _ = it.pool.Exec(ctx, `DELETE FROM creator_ledger WHERE user_id = $1`, creator)
		_, _ = it.pool.Exec(ctx, `DELETE FROM monetization_rpm_rates WHERE region_code = $1`, region)
		_, _ = it.pool.Exec(ctx, `DELETE FROM monetization_quality_bands WHERE region_code = $1`, region)
		_, _ = it.pool.Exec(ctx, `DELETE FROM creator_fund_budgets WHERE period_key = $1 AND region_code = $2`, period, region)
	})
	it.exec(t, `INSERT INTO creator_ledger (user_id, balance) VALUES ($1, 1000)`, creator)
	it.exec(t, `INSERT INTO transactions (id, wallet_id, type, amount, currency, status) VALUES ($1, $2, 'subscription_payment', 5000, 'INR', 'completed')`, txnID, creator)
	it.exec(t, `INSERT INTO disputes (id, user_id, transaction_id, reason) VALUES ($1, $2, $3, 'test')`, disputeID, creator, txnID)
	it.exec(t, `INSERT INTO fraud_reviews (id, creator_id, review_type, status) VALUES ($1, $2, 'manual', 'pending')`, reviewID, creator)
	it.exec(t, `INSERT INTO creator_fund_earnings (id, creator_id, day_bucket, content_type, region_code, status, credited) VALUES ($1, $2, '2091-03-02', 'flick', $3, 'settled', false)`, earningID, creator, region)

	actor := it.actor
	u := creator.String()
	steps := []struct {
		perm, method, path, body string
		want                     int
		table, op                string
	}{
		{PermWalletFreeze, http.MethodPost, "/wallet/" + u + "/freeze", ``, 200, "creator_ledger", "freeze"},
		{PermWalletUnfreeze, http.MethodPost, "/wallet/" + u + "/unfreeze", ``, 200, "creator_ledger", "unfreeze"},
		{PermWalletRebuild, http.MethodPost, "/wallet/" + u + "/rebuild", ``, 200, "creator_ledger", "rebuild"},
		{PermFraudReview, http.MethodPatch, "/fraud-reviews/" + reviewID.String(), `{"status":"cleared","notes":"ok"}`, 200, "fraud_reviews", "decide"},
		{PermDisputesAct, http.MethodPatch, "/disputes/" + disputeID.String(), `{"status":"resolved_denied","resolution_notes":"no"}`, 200, "disputes", "update"},
		{PermRefundIssue, http.MethodPost, "/refunds", `{"transaction_id":"` + txnID.String() + `","amount_paise":700,"reason":"test"}`, 201, "refunds", "create"},
		{PermCreatorsSuspend, http.MethodPost, "/creator-fund/" + u + "/suspend", `{"reason":"test"}`, 200, "creator_fund_eligibility", "suspend"},
		{PermCreatorsSuspend, http.MethodPost, "/creator-fund/" + u + "/unsuspend", ``, 200, "creator_fund_eligibility", "unsuspend"},
		{PermFundRates, http.MethodPut, "/creator-fund/rates", `{"content_type":"flick","region_code":"` + region + `","rpm_paise":321}`, 200, "monetization_rpm_rates", "set"},
		{PermFundRates, http.MethodPut, "/creator-fund/quality-bands", `{"content_type":"flick","region_code":"` + region + `","floor_bps":5000,"ceiling_bps":15000}`, 200, "monetization_quality_bands", "set"},
		{PermFundBudget, http.MethodPut, "/creator-fund/budgets", `{"period_key":"` + period + `","region_code":"` + region + `","cap_paise":90000}`, 200, "creator_fund_budgets", "set"},
		{PermFundReverse, http.MethodPost, "/creator-fund/earnings/" + earningID.String() + "/reverse", `{"reason":"test reversal"}`, 200, "creator_fund_earnings", "reverse"},
	}
	for i, st := range steps {
		code, body := it.call(t, actor, st.perm, st.method, st.path, st.body)
		if code != st.want {
			t.Fatalf("%s %s: status=%d body=%s, want %d", st.method, st.path, code, body, st.want)
		}
		rows := it.auditRows(t, actor)
		if len(rows) != i+1 {
			t.Fatalf("%s %s: %d audit rows by act after %d writes, want exactly one per write", st.method, st.path, len(rows), i+1)
		}
		last := rows[i]
		if last.Table != st.table || last.Op != st.op {
			t.Fatalf("%s %s: audit row %s/%s, want %s/%s", st.method, st.path, last.Table, last.Op, st.table, st.op)
		}
		if st.op != "reverse" && last.NewData["via"] != service.ViaAdminService {
			t.Fatalf("%s %s: new_data.via=%v, want %s", st.method, st.path, last.NewData["via"], service.ViaAdminService)
		}
	}

	// The refund moved the money it recorded, in the same commit.
	var balance int64
	if err := it.pool.QueryRow(it.ctx, `SELECT balance FROM creator_ledger WHERE user_id = $1`, creator).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	// rebuild set the balance from the (empty) double-entry legs: 0; the
	// refund then credited 700.
	if balance != 700 {
		t.Fatalf("balance after rebuild and refund = %d, want 700", balance)
	}
	var reviewer uuid.UUID
	if err := it.pool.QueryRow(it.ctx, `SELECT reviewer_id FROM fraud_reviews WHERE id = $1`, reviewID).Scan(&reviewer); err != nil || reviewer != actor {
		t.Fatalf("fraud review reviewer=%s err=%v, want act %s", reviewer, err, actor)
	}
	var createdBy uuid.UUID
	if err := it.pool.QueryRow(it.ctx, `SELECT created_by FROM monetization_rpm_rates WHERE region_code = $1 AND effective_to IS NULL`, region).Scan(&createdBy); err != nil || createdBy != actor {
		t.Fatalf("rate created_by=%s err=%v, want act %s", createdBy, err, actor)
	}

	// A refused change writes no row: an unknown wallet, a second refund of
	// the same transaction, a refund above the transaction.
	before := len(it.auditRows(t, actor))
	if code, _ := it.call(t, actor, PermWalletFreeze, http.MethodPost, "/wallet/"+uuid.NewString()+"/freeze", ``); code != http.StatusNotFound {
		t.Fatalf("freeze unknown wallet: status=%d, want 404", code)
	}
	if code, _ := it.call(t, actor, PermRefundIssue, http.MethodPost, "/refunds", `{"transaction_id":"`+txnID.String()+`","amount_paise":1,"reason":"again"}`); code != http.StatusConflict {
		t.Fatalf("second refund: status=%d, want 409", code)
	}
	if after := len(it.auditRows(t, actor)); after != before {
		t.Fatalf("refused changes wrote %d audit rows", after-before)
	}
}

// The audit row is in the same transaction as the change: make the audit
// insert fail for one actor and every write rolls back.
func TestAdminTokenIntegration_AuditFailureRollsBackTheChange(t *testing.T) {
	it := newAdminIT(t)
	failing := uuid.New()
	fn := "monetization_it_fail_audit_" + fmt.Sprint(time.Now().UnixNano())
	it.exec(t, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.performer_id = '%s'::uuid THEN RAISE EXCEPTION 'audit insert refused for test'; END IF;
			RETURN NEW;
		END $$`, fn, failing))
	it.exec(t, fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT ON monetization_audit_log FOR EACH ROW EXECUTE FUNCTION %s()`, fn, fn))
	t.Cleanup(func() {
		_, _ = it.pool.Exec(it.ctx, fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON monetization_audit_log`, fn))
		_, _ = it.pool.Exec(it.ctx, fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, fn))
	})

	creator := uuid.New()
	region := testRegion()
	txnID := uuid.New()
	reviewID := uuid.New()
	disputeID := uuid.New()
	earningID := uuid.New()
	t.Cleanup(func() {
		ctx := it.ctx
		_, _ = it.pool.Exec(ctx, `DELETE FROM creator_fund_earnings WHERE id = $1`, earningID)
		_, _ = it.pool.Exec(ctx, `DELETE FROM disputes WHERE id = $1`, disputeID)
		_, _ = it.pool.Exec(ctx, `DELETE FROM refunds WHERE transaction_id = $1`, txnID)
		_, _ = it.pool.Exec(ctx, `DELETE FROM transactions WHERE wallet_id = $1`, creator)
		_, _ = it.pool.Exec(ctx, `DELETE FROM fraud_reviews WHERE id = $1`, reviewID)
		_, _ = it.pool.Exec(ctx, `DELETE FROM creator_fund_eligibility WHERE creator_id = $1`, creator)
		_, _ = it.pool.Exec(ctx, `DELETE FROM creator_ledger WHERE user_id = $1`, creator)
		_, _ = it.pool.Exec(ctx, `DELETE FROM monetization_rpm_rates WHERE region_code = $1`, region)
		_, _ = it.pool.Exec(ctx, `DELETE FROM creator_fund_budgets WHERE period_key = '2091-07' AND region_code = $1`, region)
		_, _ = it.pool.Exec(ctx, `DELETE FROM monetization_audit_log WHERE performer_id = $1`, failing)
	})
	it.exec(t, `INSERT INTO creator_ledger (user_id, balance) VALUES ($1, 1000)`, creator)
	it.exec(t, `INSERT INTO transactions (id, wallet_id, type, amount, currency, status) VALUES ($1, $2, 'subscription_payment', 5000, 'INR', 'completed')`, txnID, creator)
	it.exec(t, `INSERT INTO fraud_reviews (id, creator_id, review_type, status) VALUES ($1, $2, 'manual', 'pending')`, reviewID, creator)
	it.exec(t, `INSERT INTO disputes (id, user_id, transaction_id, reason) VALUES ($1, $2, $3, 'test')`, disputeID, creator, txnID)
	it.exec(t, `INSERT INTO creator_fund_earnings (id, creator_id, day_bucket, content_type, region_code, status, credited) VALUES ($1, $2, '2091-07-02', 'flick', $3, 'settled', false)`, earningID, creator, region)

	u := creator.String()
	for _, st := range []struct{ perm, method, path, body string }{
		{PermWalletFreeze, http.MethodPost, "/wallet/" + u + "/freeze", ``},
		{PermFraudReview, http.MethodPatch, "/fraud-reviews/" + reviewID.String(), `{"status":"action_taken"}`},
		{PermRefundIssue, http.MethodPost, "/refunds", `{"transaction_id":"` + txnID.String() + `","amount_paise":700,"reason":"test"}`},
		{PermCreatorsSuspend, http.MethodPost, "/creator-fund/" + u + "/suspend", `{"reason":"test"}`},
		{PermFundRates, http.MethodPut, "/creator-fund/rates", `{"content_type":"flick","region_code":"` + region + `","rpm_paise":999}`},
		{PermFundBudget, http.MethodPut, "/creator-fund/budgets", `{"period_key":"2091-07","region_code":"` + region + `","cap_paise":5}`},
		{PermFundReverse, http.MethodPost, "/creator-fund/earnings/" + earningID.String() + "/reverse", `{"reason":"test"}`},
		{PermDisputesAct, http.MethodPatch, "/disputes/" + disputeID.String(), `{"status":"resolved_refund"}`},
	} {
		// Refused, whatever status the handler maps a store error to (the
		// rate handler has always answered 400 for any error).
		if code, body := it.call(t, failing, st.perm, st.method, st.path, st.body); code < 400 {
			t.Fatalf("%s %s with a failing audit insert: status=%d body=%s, want refused", st.method, st.path, code, body)
		}
	}

	var frozen bool
	var balance int64
	if err := it.pool.QueryRow(it.ctx, `SELECT is_frozen, balance FROM creator_ledger WHERE user_id = $1`, creator).Scan(&frozen, &balance); err != nil {
		t.Fatal(err)
	}
	if frozen || balance != 1000 {
		t.Fatalf("ledger after failed audits: frozen=%t balance=%d, want unchanged (false, 1000)", frozen, balance)
	}
	count := func(sql string, args ...any) int {
		var n int
		if err := it.pool.QueryRow(it.ctx, sql, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(`SELECT count(*) FROM fraud_reviews WHERE id = $1 AND status = 'pending'`, reviewID); n != 1 {
		t.Fatal("fraud decision committed without its audit row")
	}
	if n := count(`SELECT count(*) FROM refunds WHERE transaction_id = $1`, txnID); n != 0 {
		t.Fatal("refund committed without its audit row")
	}
	if n := count(`SELECT count(*) FROM transactions WHERE wallet_id = $1 AND type = 'refund'`, creator); n != 0 {
		t.Fatal("refund transaction committed without its audit row")
	}
	if n := count(`SELECT count(*) FROM creator_fund_eligibility WHERE creator_id = $1`, creator); n != 0 {
		t.Fatal("suspension committed without its audit row")
	}
	if n := count(`SELECT count(*) FROM monetization_rpm_rates WHERE region_code = $1`, region); n != 0 {
		t.Fatal("rate committed without its audit row")
	}
	if n := count(`SELECT count(*) FROM creator_fund_budgets WHERE period_key = '2091-07' AND region_code = $1`, region); n != 0 {
		t.Fatal("budget committed without its audit row")
	}
	if n := count(`SELECT count(*) FROM creator_fund_earnings WHERE id = $1 AND status = 'settled' AND reversed_at IS NULL`, earningID); n != 1 {
		t.Fatal("reversal committed without its audit row")
	}
	if n := count(`SELECT count(*) FROM disputes WHERE id = $1 AND status = 'open'`, disputeID); n != 1 {
		t.Fatal("dispute update committed without its audit row")
	}
	if n := count(`SELECT count(*) FROM monetization_audit_log WHERE performer_id = $1`, failing); n != 0 {
		t.Fatalf("%d audit rows written for the failing actor", n)
	}
}

// Settlement runs: a start row and a finish row sharing run_id, performer =
// act; and the legacy route audits too, with X-User-Id as the actor.
func TestAdminTokenIntegration_SettlementRunsAndLegacyActor(t *testing.T) {
	it := newAdminIT(t)
	creator := uuid.New()
	legacyUser := uuid.New()
	t.Cleanup(func() {
		ctx := it.ctx
		_, _ = it.pool.Exec(ctx, `DELETE FROM creator_fund_period_settlements WHERE creator_id = $1 OR period_key IN ('2091-04', '2091-05')`, creator)
		_, _ = it.pool.Exec(ctx, `DELETE FROM creator_fund_earnings WHERE day_bucket >= '2091-04-01' AND day_bucket < '2091-06-02'`)
		_, _ = it.pool.Exec(ctx, `DELETE FROM creator_ledger WHERE user_id IN ($1, $2)`, creator, legacyUser)
		_, _ = it.pool.Exec(ctx, `DELETE FROM monetization_audit_log WHERE performer_id = $1`, legacyUser)
	})

	runs := []struct{ perm, path, start, finish string }{
		{PermFundSettle, "/creator-fund/" + creator.String() + "/settle-period?period=2091-04", pgstore.AuditOpSettleCreatorStart, pgstore.AuditOpSettleCreatorFinish},
		{PermFundSettle, "/creator-fund/settle-period?period=2091-05", pgstore.AuditOpSettleStart, pgstore.AuditOpSettleFinish},
		{PermFundSettle, "/creator-fund/settle?day=2091-06-01", pgstore.AuditOpAccrueDayStart, pgstore.AuditOpAccrueDayFinish},
	}
	for _, run := range runs {
		code, body := it.call(t, it.actor, run.perm, http.MethodPost, run.path, ``)
		if code != http.StatusOK {
			t.Fatalf("POST %s: status=%d body=%s, want 200", run.path, code, body)
		}
		var startRun, finishRun, status string
		var n int
		if err := it.pool.QueryRow(it.ctx, `
			SELECT count(*), max(new_data->>'run_id') FILTER (WHERE operation = $2), max(new_data->>'run_id') FILTER (WHERE operation = $3),
			       max(new_data->>'status') FILTER (WHERE operation = $3)
			FROM monetization_audit_log WHERE performer_id = $1 AND operation IN ($2, $3)`,
			it.actor, run.start, run.finish).Scan(&n, &startRun, &finishRun, &status); err != nil {
			t.Fatal(err)
		}
		if n != 2 || startRun == "" || startRun != finishRun || status != pgstore.SettlementRunStatusCompleted {
			t.Fatalf("%s: rows=%d start run=%q finish run=%q status=%q, want one start and one completed finish on one run", run.path, n, startRun, finishRun, status)
		}
	}

	// Legacy route: the audit row names X-User-Id, via gateway.
	it.exec(t, `INSERT INTO creator_ledger (user_id) VALUES ($1)`, creator)
	w := adminServe(it.r, http.MethodPost, "/v1/monetization/admin/wallet/"+creator.String()+"/freeze", ``, legacyAdmin(legacyUser, "admin"))
	if w.Code != http.StatusOK {
		t.Fatalf("legacy freeze: status=%d body=%s", w.Code, w.Body.String())
	}
	rows := it.auditRows(t, legacyUser)
	if len(rows) != 1 || rows[0].Table != "creator_ledger" || rows[0].Op != "freeze" || rows[0].NewData["via"] != service.ViaGateway {
		t.Fatalf("legacy freeze audit rows = %+v, want one creator_ledger/freeze via gateway", rows)
	}
}

// Stats on seeded data: deltas against a snapshot, since the scratch
// database is shared with the other suites.
func TestAdminTokenIntegration_Stats(t *testing.T) {
	it := newAdminIT(t)
	stats := func() pgstore.AdminStats {
		t.Helper()
		code, body := it.call(t, it.actor, PermStatsRead, http.MethodGet, "/stats", ``)
		if code != http.StatusOK {
			t.Fatalf("stats: status=%d body=%s", code, body)
		}
		var s pgstore.AdminStats
		if err := json.Unmarshal(body, &s); err != nil {
			t.Fatalf("decode stats: %v body=%s", err, body)
		}
		return s
	}
	before := stats()

	creator := uuid.New()
	other := uuid.New()
	txnID := uuid.New()
	t.Cleanup(func() {
		ctx := it.ctx
		_, _ = it.pool.Exec(ctx, `DELETE FROM payout_requests WHERE user_id IN ($1, $2)`, creator, other)
		_, _ = it.pool.Exec(ctx, `DELETE FROM disputes WHERE user_id = $1`, creator)
		_, _ = it.pool.Exec(ctx, `DELETE FROM transactions WHERE wallet_id = $1`, creator)
		_, _ = it.pool.Exec(ctx, `DELETE FROM fraud_reviews WHERE creator_id = $1`, creator)
		_, _ = it.pool.Exec(ctx, `DELETE FROM creator_fund_earnings WHERE creator_id = $1`, creator)
		_, _ = it.pool.Exec(ctx, `DELETE FROM creator_fund_period_settlements WHERE creator_id = $1`, creator)
		_, _ = it.pool.Exec(ctx, `DELETE FROM creator_ledger WHERE user_id IN ($1, $2)`, creator, other)
	})
	it.exec(t, `INSERT INTO creator_ledger (user_id, is_frozen) VALUES ($1, true), ($2, false)`, creator, other)
	it.exec(t, `INSERT INTO fraud_reviews (creator_id, review_type, status) VALUES ($1, 'manual', 'pending'), ($1, 'velocity', 'investigating'), ($1, 'manual', 'cleared')`, creator)
	it.exec(t, `INSERT INTO payout_requests (user_id, amount, status) VALUES ($1, 12345, 'held'), ($1, 55, 'reserved'), ($2, 999, 'paid')`, creator, other)
	it.exec(t, `INSERT INTO transactions (id, wallet_id, type, amount, currency, status) VALUES ($1, $2, 'subscription_payment', 100, 'INR', 'completed')`, txnID, creator)
	it.exec(t, `INSERT INTO disputes (user_id, transaction_id, reason, status) VALUES ($1, $2, 'a', 'open'), ($1, $2, 'b', 'resolved_denied')`, creator, txnID)
	it.exec(t, `INSERT INTO creator_fund_earnings (creator_id, day_bucket, content_type, region_code, status, reversed_at) VALUES ($1, '2090-01-01', 'flick', 'IN', 'reversed', now()), ($1, '2090-01-02', 'flick', 'IN', 'reversed', now() - interval '30 days')`, creator)

	// A settlement run so the last-run field names ours.
	if code, body := it.call(t, it.actor, PermFundSettle, http.MethodPost, "/creator-fund/"+creator.String()+"/settle-period?period=2091-08", ``); code != http.StatusOK {
		t.Fatalf("settle: status=%d body=%s", code, body)
	}

	after := stats()
	check := func(name string, got, want int64) {
		t.Helper()
		if got != want {
			t.Fatalf("%s delta = %d, want %d (before %+v, after %+v)", name, got, want, before, after)
		}
	}
	check("open_fraud_reviews", after.OpenFraudReviews-before.OpenFraudReviews, 2)
	check("frozen_wallets", after.FrozenWallets-before.FrozenWallets, 1)
	check("pending_payout_requests", after.PendingPayoutRequests-before.PendingPayoutRequests, 2)
	check("pending_payout_paise", after.PendingPayoutPaise-before.PendingPayoutPaise, 12400)
	check("reversals_last_7_days", after.ReversalsLast7Days-before.ReversalsLast7Days, 1)
	check("open_disputes", after.OpenDisputes-before.OpenDisputes, 1)

	want := service.PeriodContaining(time.Now().UTC(), service.DefaultCreatorFundConfig().SettlementCadence)
	if after.CurrentPeriod.PeriodKey != want.Key {
		t.Fatalf("current_period.period_key = %q, want %q", after.CurrentPeriod.PeriodKey, want.Key)
	}
	if after.CurrentPeriod.Capped != (after.CurrentPeriod.CapPaise != nil) {
		t.Fatalf("current_period capped=%t but cap_paise=%v", after.CurrentPeriod.Capped, after.CurrentPeriod.CapPaise)
	}
	run := after.LastSettlementRun
	if run == nil || run.Operation != pgstore.AuditOpSettleCreatorStart || run.PeriodKey != "2091-08" ||
		run.Status != pgstore.SettlementRunStatusCompleted || run.ActorID != it.actor || run.EndedAt == nil {
		t.Fatalf("last_settlement_run = %+v, want our completed 2091-08 run by %s", run, it.actor)
	}
	if after.GeneratedAt.IsZero() {
		t.Fatal("generated_at unset")
	}
}
