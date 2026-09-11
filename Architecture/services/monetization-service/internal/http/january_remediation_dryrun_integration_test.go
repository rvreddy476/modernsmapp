//go:build integration

package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/monetization-service/database"
	"github.com/atpost/monetization-service/internal/service"
	pgstore "github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestJanuaryRemediationDryRun rehearses the January runbook
// (phase0-evidence/02_january_remediation.md) end to end against the
// scratch database, through the real admin route, on look-alike rows
// that mirror the 19 live January earnings exactly:
//
//	15 rows  credit transaction + cf_net leg + cf_fee leg      ("both")
//	 2 rows  credit transaction + cf_net leg, NO cf_fee leg     ("mixed": e51ecb3d, 00b74229)
//	 2 rows  no transaction, no legs, credited=true by 017     ("artefact": 9517e3ab, bd6fa147)
//
// Amounts are the live ones, so every expected total in the runbook is
// an observed number from this test, not a prediction. The test is
// deliberately verbose: run it with -v and read the OBSERVED lines.
func TestJanuaryRemediationDryRun(t *testing.T) {
	dsn := requireTestDSN(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pgstore.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatal(err)
	}
	store := pgstore.New(pool)
	svc := service.New(store, nil)

	// ---- seed --------------------------------------------------------
	type seedRow struct {
		id                     uuid.UUID
		creator                uuid.UUID
		day                    time.Time
		gross, fee, net, views int64
		class                  string // both | mixed | artefact
	}
	d15 := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	d16 := time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC)
	mk := func(day time.Time, gross, fee, net int64, class string) seedRow {
		return seedRow{id: uuid.New(), creator: uuid.New(), day: day, gross: gross, fee: fee, net: net, views: gross * 1000 / 5000, class: class}
	}
	rows := []seedRow{
		// 2026-01-15: 14 rows (live: 4 x 50000, 6 x 42645, 4 x 62255)
		mk(d15, 50000, 15000, 35000, "both"), mk(d15, 50000, 15000, 35000, "both"),
		mk(d15, 50000, 15000, 35000, "both"), mk(d15, 50000, 15000, 35000, "both"),
		mk(d15, 42645, 12793, 29852, "both"), mk(d15, 42645, 12793, 29852, "both"),
		mk(d15, 42645, 12793, 29852, "both"), mk(d15, 42645, 12793, 29852, "both"),
		mk(d15, 42645, 12793, 29852, "mixed"),    // e51ecb3d look-alike
		mk(d15, 42645, 12793, 29852, "artefact"), // 9517e3ab look-alike
		mk(d15, 62255, 18676, 43579, "both"), mk(d15, 62255, 18676, 43579, "both"),
		mk(d15, 62255, 18676, 43579, "both"), mk(d15, 62255, 18676, 43579, "both"),
		// 2026-01-16: 5 rows (live: 5 x 25000)
		mk(d16, 25000, 7500, 17500, "both"), mk(d16, 25000, 7500, 17500, "both"),
		mk(d16, 25000, 7500, 17500, "both"),
		mk(d16, 25000, 7500, 17500, "mixed"),    // 00b74229 look-alike
		mk(d16, 25000, 7500, 17500, "artefact"), // bd6fa147 look-alike
	}
	if len(rows) != 19 {
		t.Fatalf("seed has %d rows, want 19", len(rows))
	}
	ids := make([]uuid.UUID, 0, 19)
	creators := make([]uuid.UUID, 0, 19)
	var artefactIDs, mixedIDs, bothIDs []uuid.UUID
	for _, r := range rows {
		ids = append(ids, r.id)
		creators = append(creators, r.creator)
		switch r.class {
		case "artefact":
			artefactIDs = append(artefactIDs, r.id)
		case "mixed":
			mixedIDs = append(mixedIDs, r.id)
		default:
			bothIDs = append(bothIDs, r.id)
		}
	}
	rateID, bandID := uuid.New(), uuid.New()
	adminID := uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM monetization_audit_log WHERE performer_id = $1`, adminID)
		_, _ = pool.Exec(ctx, `DELETE FROM ledger_entries WHERE reference_id = ANY($1)`, ids)
		_, _ = pool.Exec(ctx, `DELETE FROM ledger_entries WHERE debit_account_id IN (SELECT id FROM accounts WHERE owner_id = ANY($1)) OR credit_account_id IN (SELECT id FROM accounts WHERE owner_id = ANY($1))`, creators)
		_, _ = pool.Exec(ctx, `DELETE FROM accounts WHERE owner_id = ANY($1)`, creators)
		_, _ = pool.Exec(ctx, `DELETE FROM transactions WHERE wallet_id = ANY($1)`, creators)
		_, _ = pool.Exec(ctx, `DELETE FROM creator_ledger WHERE user_id = ANY($1)`, creators)
		_, _ = pool.Exec(ctx, `DELETE FROM creator_fund_earnings WHERE id = ANY($1)`, ids)
		_, _ = pool.Exec(ctx, `DELETE FROM monetization_rpm_rates WHERE id = $1`, rateID)
		_, _ = pool.Exec(ctx, `DELETE FROM monetization_quality_bands WHERE id = $1`, bandID)
	})

	// The fixture rate and band, tagged and windowed exactly like the live
	// pair (d830ba17 / b45d00ea: 2026-01-09 -> 2026-01-23, 5000 paise).
	if _, err := pool.Exec(ctx, `
		INSERT INTO monetization_rpm_rates (id, content_type, region_code, rpm_paise, effective_from, effective_to, notes)
		VALUES ($1, 'long_video', 'IN', 5000, '2026-01-09', '2026-01-23', 'integration test window')`, rateID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO monetization_quality_bands (id, content_type, region_code, floor_bps, ceiling_bps, pivot_cqs,
			confidence_impressions, enabled, effective_from, effective_to, notes)
		VALUES ($1, 'long_video', 'IN', 8500, 12500, 0.35, 1000, true, '2026-01-09', '2026-01-23', 'integration test window')`, bandID); err != nil {
		t.Fatal(err)
	}

	platformRev, err := store.EnsureAccount(ctx, uuid.MustParse("00000000-0000-0000-0000-000000000001"), "platform_revenue")
	if err != nil {
		t.Fatal(err)
	}
	platformFees, err := store.EnsureAccount(ctx, uuid.MustParse("00000000-0000-0000-0000-000000000001"), "platform_revenue_fees")
	if err != nil {
		t.Fatal(err)
	}
	creditedAt := time.Date(2026, 9, 6, 19, 43, 58, 0, time.UTC)
	for _, r := range rows {
		// The earning: settled, priced by the fixture, credited=true on
		// every row (migration 017's blanket UPDATE), settlement_id NULL.
		if _, err := pool.Exec(ctx, `
			INSERT INTO creator_fund_earnings (
				id, creator_id, day_bucket, content_type, region_code,
				view_count, watch_time_ms, rpm_paise, gross_paise, platform_fee_paise, net_paise,
				status, settled_at, base_gross_paise, quality_multiplier_bps, credited, credited_at, settlement_id)
			VALUES ($1, $2, $3, 'long_video', 'IN', $4::bigint, $4::bigint * 30000, 5000, $5::bigint, $6, $7,
				'settled', $8::timestamptz, $5::bigint, 10000, TRUE, $8::timestamptz, NULL)`,
			r.id, r.creator, r.day, r.views, r.gross, r.fee, r.net, creditedAt); err != nil {
			t.Fatalf("seed earning: %v", err)
		}
		balance := r.net
		if r.class == "artefact" {
			balance = 0
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO creator_ledger (user_id, balance, lifetime_earnings, pending_payout, currency, is_frozen, created_at, updated_at)
			VALUES ($1, $2, $2, 0, 'INR', false, $3, $3)`, r.creator, balance, creditedAt); err != nil {
			t.Fatalf("seed ledger: %v", err)
		}
		if r.class == "artefact" {
			continue
		}
		wallet, err := store.EnsureAccount(ctx, r.creator, "user_wallet")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO transactions (id, wallet_id, type, amount, currency, status, reference_type, reference_id, description, idempotency_key, created_at)
			VALUES ($1, $2, 'creator_fund_earning', $3, 'INR', 'completed', 'creator_fund_earning', $4, 'Creator fund earning (seeded look-alike)', NULL, $5)`,
			uuid.New(), r.creator, r.net, r.id.String(), creditedAt); err != nil {
			t.Fatalf("seed transaction: %v", err)
		}
		leg := func(debit, credit uuid.UUID, amount int64, key string) {
			if _, err := pool.Exec(ctx, `
				INSERT INTO ledger_entries (id, debit_account_id, credit_account_id, amount_paise, currency, reference_type, reference_id, idempotency_key, description, created_at)
				VALUES ($1, $2, $3, $4, 'INR', 'creator_fund', $5, $6, 'seeded look-alike', $7)`,
				uuid.New(), debit, credit, amount, r.id, key, creditedAt); err != nil {
				t.Fatalf("seed leg %s: %v", key, err)
			}
			_, _ = pool.Exec(ctx, `UPDATE accounts SET balance_paise = balance_paise - $2 WHERE id = $1`, debit, amount)
			_, _ = pool.Exec(ctx, `UPDATE accounts SET balance_paise = balance_paise + $2 WHERE id = $1`, credit, amount)
		}
		leg(platformRev.ID, wallet.ID, r.net, "cf_net:"+r.id.String()+":long_video")
		if r.class == "both" {
			leg(platformRev.ID, platformFees.ID, r.fee, "cf_fee:"+r.id.String()+":long_video")
		}
	}

	// ---- the router, exactly as the container runs it with writes on --
	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(svc).WithInternalKey("").WithWritesEnabled(true).RegisterRoutes(router)
	type reverseResp struct {
		Data struct {
			Earning struct {
				ID                    uuid.UUID  `json:"id"`
				Status                string     `json:"status"`
				Credited              bool       `json:"credited"`
				ReversedAt            *time.Time `json:"reversed_at"`
				ReversalTransactionID *uuid.UUID `json:"reversal_transaction_id"`
			} `json:"earning"`
			AlreadyReversed bool `json:"already_reversed"`
			MoneyMoved      bool `json:"money_moved"`
			Adjustment      *struct {
				ID          uuid.UUID `json:"id"`
				AmountPaise int64     `json:"amount_paise"`
			} `json:"adjustment"`
			BalanceAfter     int64 `json:"balance_after_paise"`
			NetReversedPaise int64 `json:"net_reversed_paise"`
			FeeReversedPaise int64 `json:"fee_reversed_paise"`
			FeeLegAbsent     bool  `json:"fee_leg_absent"`
			LedgerFrozen     bool  `json:"ledger_frozen"`
		} `json:"data"`
	}
	reverse := func(id uuid.UUID, reason string) (reverseResp, string) {
		body, _ := json.Marshal(map[string]string{"reason": reason})
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/monetization/admin/creator-fund/earnings/"+id.String()+"/reverse", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Scopes", "admin")
		req.Header.Set("X-User-Id", adminID.String())
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("reverse %s: status=%d body=%s", id, w.Code, w.Body.String())
		}
		var out reverseResp
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v: %s", err, w.Body.String())
		}
		return out, strings.TrimSpace(w.Body.String())
	}
	count := func(q string, args ...any) int64 {
		var n int64
		if err := pool.QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}
	sum := func(q string, args ...any) int64 {
		var n int64
		if err := pool.QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}

	// Precondition: the boundary is real. Without X-Scopes the route is 403.
	{
		body, _ := json.Marshal(map[string]string{"reason": "x"})
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/monetization/admin/creator-fund/earnings/"+ids[0].String()+"/reverse", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User-Id", adminID.String())
		router.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("without X-Scopes: status=%d, want 403", w.Code)
		}
		t.Logf("OBSERVED precondition: POST reverse without X-Scopes -> %d %s", w.Code, strings.TrimSpace(w.Body.String()))
	}

	// ---- step 1: evidence table and audit snapshot ---------------------
	var both, credNoFee, feeNoCred, neither, netWithCredit, feeWithLeg, feeWithCredit int64
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE c AND f), count(*) FILTER (WHERE c AND NOT f),
		       count(*) FILTER (WHERE NOT c AND f), count(*) FILTER (WHERE NOT c AND NOT f),
		       COALESCE(sum(net_paise) FILTER (WHERE c),0), COALESCE(sum(platform_fee_paise) FILTER (WHERE f),0),
		       COALESCE(sum(platform_fee_paise) FILTER (WHERE c),0)
		FROM (SELECT e.*,
		  EXISTS (SELECT 1 FROM transactions t WHERE t.reference_id = e.id::text AND t.type='creator_fund_earning' AND t.status='completed' AND t.amount = e.net_paise) AS c,
		  EXISTS (SELECT 1 FROM ledger_entries l WHERE l.idempotency_key = 'cf_fee:'||e.id::text||':'||e.content_type AND l.amount_paise = e.platform_fee_paise) AS f
		  FROM creator_fund_earnings e WHERE e.id = ANY($1)) x`, ids).
		Scan(&both, &credNoFee, &feeNoCred, &neither, &netWithCredit, &feeWithLeg, &feeWithCredit); err != nil {
		t.Fatal(err)
	}
	t.Logf("OBSERVED step 1 evidence: both=%d credit_no_fee=%d fee_no_credit=%d neither=%d net_with_credit=%d fee_with_leg=%d fee_with_credit=%d",
		both, credNoFee, feeNoCred, neither, netWithCredit, feeWithLeg, feeWithCredit)
	if both != 15 || credNoFee != 2 || feeNoCred != 0 || neither != 2 || netWithCredit != 533576 || feeWithLeg != 208376 || feeWithCredit != 228669 {
		t.Fatalf("seed does not mirror the live evidence")
	}
	tag, err := pool.Exec(ctx, `
		INSERT INTO monetization_audit_log (table_name, operation, old_data, new_data, performer_id)
		SELECT 'creator_fund_earnings', 'january_remediation_snapshot',
		       to_jsonb(e) || jsonb_build_object(
		         'transactions', (SELECT json_agg(t) FROM transactions t WHERE t.reference_id = e.id::text),
		         'ledger_legs',  (SELECT json_agg(l) FROM ledger_entries l WHERE l.reference_id = e.id),
		         'ledger_balance_before', (SELECT balance FROM creator_ledger WHERE user_id = e.creator_id)),
		       NULL, $2
		FROM creator_fund_earnings e WHERE e.id = ANY($1)`, ids, adminID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("OBSERVED step 1 audit snapshot: INSERT 0 %d", tag.RowsAffected())
	if tag.RowsAffected() != 19 {
		t.Fatalf("snapshot inserted %d rows, want 19", tag.RowsAffected())
	}

	// ---- step 2: evidence-guarded flag clear ---------------------------
	clearSQL := `
		UPDATE creator_fund_earnings e
		SET credited = FALSE, credited_at = NULL, settlement_id = NULL
		WHERE e.id = ANY($1)
		  AND e.status = 'settled' AND e.credited = TRUE
		  AND NOT EXISTS (SELECT 1 FROM transactions t WHERE t.reference_id = e.id::text)
		  AND NOT EXISTS (SELECT 1 FROM ledger_entries l WHERE l.reference_id = e.id)
		  AND NOT EXISTS (SELECT 1 FROM ledger_entries l WHERE l.idempotency_key IN ('cf_net:'||e.id::text||':'||e.content_type, 'cf_fee:'||e.id::text||':'||e.content_type))
		  AND COALESCE((SELECT balance FROM creator_ledger cl WHERE cl.user_id = e.creator_id), 0) = 0`
	// The guard refuses rows that have any posting: the two mixed rows.
	guard, err := pool.Exec(ctx, clearSQL, mixedIDs)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("OBSERVED step 2 guard on the two mixed rows: UPDATE %d (must be 0)", guard.RowsAffected())
	if guard.RowsAffected() != 0 {
		t.Fatalf("the guard cleared %d mixed rows", guard.RowsAffected())
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cleared, err := tx.Exec(ctx, clearSQL, artefactIDs)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.RowsAffected() != 2 {
		_ = tx.Rollback(ctx)
		t.Fatalf("flag clear touched %d rows, want exactly 2; rolled back", cleared.RowsAffected())
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO monetization_audit_log (table_name, operation, old_data, new_data, performer_id)
		SELECT 'creator_fund_earnings', 'january_remediation_flag_clear',
		       jsonb_build_object('credited', true, 'credited_at', $3::timestamptz),
		       to_jsonb(e), $2
		FROM creator_fund_earnings e WHERE e.id = ANY($1)`, artefactIDs, adminID, creditedAt); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	t.Logf("OBSERVED step 2 flag clear: UPDATE %d, audit rows %d, uncredited now %d",
		cleared.RowsAffected(),
		count(`SELECT count(*) FROM monetization_audit_log WHERE operation='january_remediation_flag_clear' AND performer_id=$1`, adminID),
		count(`SELECT count(*) FROM creator_fund_earnings WHERE id = ANY($1) AND credited = FALSE`, ids))
	again, _ := pool.Exec(ctx, clearSQL, artefactIDs)
	t.Logf("OBSERVED step 2 re-run: UPDATE %d (idempotent)", again.RowsAffected())

	// ---- step 3: reverse the two artefact rows -------------------------
	for i, id := range artefactIDs {
		res, raw := reverse(id, "M-01: migration-017 artefact; no credit transaction and no ledger leg exist; reversed per plan Phase 2A")
		if res.Data.MoneyMoved || res.Data.Adjustment != nil || res.Data.NetReversedPaise != 0 || res.Data.FeeReversedPaise != 0 || res.Data.LedgerFrozen || res.Data.Earning.Status != "reversed" || res.Data.Earning.Credited {
			t.Fatalf("artefact reversal moved money or did not reverse: %s", raw)
		}
		if i == 0 {
			t.Logf("OBSERVED step 3 response (artefact row): %s", raw)
		}
	}
	t.Logf("OBSERVED step 3: adjustment transactions for artefact rows=%d, ledger legs referencing them=%d, statuses=%s",
		count(`SELECT count(*) FROM transactions WHERE idempotency_key LIKE 'adj:creator_fund_earning_reversal:%' AND reference_id = ANY($1::text[])`, []string{artefactIDs[0].String(), artefactIDs[1].String()}),
		count(`SELECT count(*) FROM ledger_entries WHERE reference_id = ANY($1)`, artefactIDs),
		strings.Join(func() []string {
			r, _ := pool.Query(ctx, `SELECT status FROM creator_fund_earnings WHERE id = ANY($1)`, artefactIDs)
			defer r.Close()
			var out []string
			for r.Next() {
				var s string
				_ = r.Scan(&s)
				out = append(out, s)
			}
			return out
		}(), ","))

	// ---- step 4a: reverse the 15 clean rows ----------------------------
	var net15, fee15 int64
	var sample string
	for i, id := range bothIDs {
		res, raw := reverse(id, "M-01: January 2026 accrual priced from analytics rows with no hourly events (integration-test fixture); reversed per plan Phase 2A")
		if !res.Data.MoneyMoved || res.Data.Adjustment == nil || res.Data.LedgerFrozen || res.Data.Earning.Status != "reversed" || res.Data.FeeLegAbsent {
			t.Fatalf("clean reversal: %s", raw)
		}
		net15 += res.Data.NetReversedPaise
		fee15 += res.Data.FeeReversedPaise
		if i == 0 {
			sample = raw
		}
	}
	t.Logf("OBSERVED step 4a response (clean row): %s", sample)
	t.Logf("OBSERVED step 4a totals over 15 clean rows: net_reversed=%d fee_reversed=%d", net15, fee15)
	if net15 != 486224 || fee15 != 208376 {
		t.Fatalf("15-row totals net=%d fee=%d, want 486224 / 208376", net15, fee15)
	}
	// One-for-one reconciliation of the 15: every adjustment matches its
	// original credit transaction and every adj_fee leg its cf_fee leg.
	recon := count(`
		SELECT count(*) FROM creator_fund_earnings e
		JOIN transactions orig ON orig.reference_id = e.id::text AND orig.type='creator_fund_earning' AND orig.status='completed'
		JOIN transactions adj  ON adj.idempotency_key = 'adj:creator_fund_earning_reversal:'||e.id::text AND adj.amount = -orig.amount
		JOIN ledger_entries cf  ON cf.idempotency_key  = 'cf_fee:'||e.id::text||':'||e.content_type
		JOIN ledger_entries af  ON af.idempotency_key  = 'adj_fee:creator_fund_earning_reversal:'||e.id::text AND af.amount_paise = cf.amount_paise
		WHERE e.id = ANY($1) AND e.status='reversed'`, bothIDs)
	t.Logf("OBSERVED step 4a reconciliation: %d of 15 rows have adjustment = -credit AND adj_fee = cf_fee", recon)
	if recon != 15 {
		t.Fatalf("reconciliation matched %d rows, want 15", recon)
	}

	// ---- step 4b (scratch only): what the mechanism does to a MIXED row --
	// The observation behind the runbook's original stop: the mechanism
	// used to post the fee leg from platform_fee_paise alone, so a mixed
	// row produced an adj_fee leg with no original posting (2 legs,
	// 20,293 paise). The evidence guard now checks for the original
	// cf_fee leg first: the net still comes back, the fee is not reversed,
	// and the response says fee_leg_absent so the statement shows why.
	var netMixed, feeMixed int64
	for _, id := range mixedIDs {
		res, raw := reverse(id, "SCRATCH ONLY: observe the mechanism on a credit-without-fee-leg row")
		if !res.Data.MoneyMoved || res.Data.Adjustment == nil || !res.Data.FeeLegAbsent || res.Data.FeeReversedPaise != 0 || res.Data.Earning.Status != "reversed" {
			t.Fatalf("mixed-row reversal must give the net back, reverse no fee, and say fee_leg_absent: %s", raw)
		}
		netMixed += res.Data.NetReversedPaise
		feeMixed += res.Data.FeeReversedPaise
	}
	orphanFeeLegs := count(`
		SELECT count(*) FROM ledger_entries af
		WHERE af.idempotency_key LIKE 'adj_fee:creator_fund_earning_reversal:%'
		  AND af.reference_id = ANY($1)
		  AND NOT EXISTS (SELECT 1 FROM ledger_entries cf WHERE cf.idempotency_key = 'cf_fee:'||af.reference_id::text||':long_video')`, mixedIDs)
	t.Logf("OBSERVED step 4b (mixed rows, scratch only): net_reversed=%d fee_reversed=%d adj_fee legs with NO original cf_fee leg=%d (sum %d)",
		netMixed, feeMixed, orphanFeeLegs,
		sum(`SELECT COALESCE(sum(amount_paise),0) FROM ledger_entries WHERE idempotency_key LIKE 'adj_fee:creator_fund_earning_reversal:%' AND reference_id = ANY($1)`, mixedIDs))
	if orphanFeeLegs != 0 || feeMixed != 0 || netMixed != 47352 {
		t.Fatalf("mixed rows: orphan adj_fee legs=%d fee_reversed=%d net_reversed=%d, want 0 / 0 / 47352 (net back, no fee leg without an original posting)", orphanFeeLegs, feeMixed, netMixed)
	}
	t.Logf("OBSERVED step 4 totals if all 17 credited rows go through the mechanism: net=%d fee=%d (the 2 mixed rows' 20,293 paise of platform_fee_paise is NOT reversed: no original posting)",
		net15+netMixed, fee15+feeMixed)

	// ---- step 5: proof query -------------------------------------------
	proof := count(`
		SELECT count(*) FROM creator_fund_earnings e
		LEFT JOIN LATERAL (
		  SELECT id, notes FROM monetization_rpm_rates
		  WHERE content_type = e.content_type AND region_code = e.region_code
		    AND effective_from <= e.day_bucket AND (effective_to IS NULL OR effective_to > e.day_bucket)
		  ORDER BY effective_from DESC, created_at DESC, id DESC LIMIT 1) r ON true
		LEFT JOIN LATERAL (
		  SELECT id, notes FROM monetization_quality_bands
		  WHERE content_type = e.content_type AND region_code = e.region_code
		    AND effective_from <= e.day_bucket AND (effective_to IS NULL OR effective_to > e.day_bucket)
		  ORDER BY effective_from DESC, created_at DESC, id DESC LIMIT 1) b ON true
		WHERE e.status <> 'reversed'
		  AND (r.notes = 'integration test window' OR b.notes = 'integration test window')`)
	t.Logf("OBSERVED step 5 proof query: %d rows (must be 0)", proof)
	if proof != 0 {
		t.Fatalf("proof query returned %d", proof)
	}

	// ---- step 6: delete the fixture rows --------------------------------
	ratesBefore := count(`SELECT count(*) FROM monetization_rpm_rates WHERE notes = 'integration test window'`)
	bandsBefore := count(`SELECT count(*) FROM monetization_quality_bands WHERE notes = 'integration test window'`)
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	dr, _ := tx.Exec(ctx, `DELETE FROM monetization_rpm_rates WHERE notes = 'integration test window'`)
	db, _ := tx.Exec(ctx, `DELETE FROM monetization_quality_bands WHERE notes = 'integration test window'`)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	t.Logf("OBSERVED step 6: rates before=%d DELETE %d after=%d; bands before=%d DELETE %d after=%d",
		ratesBefore, dr.RowsAffected(), count(`SELECT count(*) FROM monetization_rpm_rates WHERE notes = 'integration test window'`),
		bandsBefore, db.RowsAffected(), count(`SELECT count(*) FROM monetization_quality_bands WHERE notes = 'integration test window'`))

	// ---- step 7: post-check --------------------------------------------
	t.Logf("OBSERVED step 7: creators with balance<>0: %d; frozen: %d; reversed rows: %d; audit 'reverse' rows by admin: %d; adjustment txns: %d (sum %d); adj_fee legs: %d (sum %d)",
		count(`SELECT count(*) FROM creator_ledger WHERE user_id = ANY($1) AND balance <> 0`, creators),
		count(`SELECT count(*) FROM creator_ledger WHERE user_id = ANY($1) AND is_frozen`, creators),
		count(`SELECT count(*) FROM creator_fund_earnings WHERE id = ANY($1) AND status='reversed'`, ids),
		count(`SELECT count(*) FROM monetization_audit_log WHERE operation='reverse' AND performer_id=$1`, adminID),
		count(`SELECT count(*) FROM transactions WHERE wallet_id = ANY($1) AND type='adjustment'`, creators),
		sum(`SELECT COALESCE(sum(amount),0) FROM transactions WHERE wallet_id = ANY($1) AND type='adjustment'`, creators),
		count(`SELECT count(*) FROM ledger_entries WHERE idempotency_key LIKE 'adj_fee:%' AND reference_id = ANY($1)`, ids),
		sum(`SELECT COALESCE(sum(amount_paise),0) FROM ledger_entries WHERE idempotency_key LIKE 'adj_fee:%' AND reference_id = ANY($1)`, ids))

	// Idempotency: a second pass over all 19 moves nothing.
	var moved int
	for _, id := range ids {
		res, _ := reverse(id, "second pass")
		if !res.Data.AlreadyReversed || res.Data.MoneyMoved {
			moved++
		}
	}
	t.Logf("OBSERVED re-run of all 19: rows that moved money or were not already_reversed = %d", moved)
	if moved != 0 {
		t.Fatalf("second pass moved money on %d rows", moved)
	}
	_ = fmt.Sprintf
}
