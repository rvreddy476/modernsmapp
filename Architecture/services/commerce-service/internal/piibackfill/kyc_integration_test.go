//go:build integration

package piibackfill

// Migration 035 — the seller-KYC backfill and gated/1002, against the suite's
// own scratch database (see TestMain in backfill_integration_test.go).
//
//	COMMERCE_TEST_DSN=.../commerce_it_test go test -tags=integration ./internal/piibackfill/ -run KYC -v

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/atpost/commerce-service/database"
	"github.com/atpost/commerce-service/internal/kyc"
	"github.com/atpost/commerce-service/internal/pii"
	"github.com/google/uuid"
)

func kycCipher(t *testing.T) *pii.Cipher {
	t.Helper()
	p, _, err := pii.LocalKeyProvider(
		[]byte("0123456789abcdef0123456789abcdef"),
		[]byte("fedcba9876543210fedcba9876543210"), nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := pii.New(p, []byte("backfill-test-salt"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// brokenKYCCipher has no KYC key: every seal fails.
func brokenKYCCipher(t *testing.T) *pii.Cipher {
	t.Helper()
	c, err := pii.New(&pii.StaticKeyProvider{Keys: map[pii.Scope][]byte{
		pii.ScopeProfile: []byte("0123456789abcdef0123456789abcdef"),
	}}, []byte("backfill-test-salt"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func kycExec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec: %v\nSQL: %s", err, sql)
	}
}

func resetKYC(t *testing.T) {
	t.Helper()
	for _, sql := range []string{
		`DELETE FROM seller_documents`,
		`DELETE FROM seller_payout_accounts`,
		`DELETE FROM organization_members`,
		`DELETE FROM organizations`,
		`DELETE FROM sellers`,
		`DELETE FROM pii_kyc_backfill_progress`,
		`UPDATE pii_kyc_cutover_state SET ciphertext_authoritative_since=NULL,
		        old_writers_drained_at=NULL, scrubbed_at=NULL WHERE id`,
	} {
		kycExec(t, sql)
	}
}

// seedKYCSeller inserts a legacy seller: PAN and payout account in plaintext
// only, exactly what every environment holds before this migration.
func seedKYCSeller(t *testing.T, n int) uuid.UUID {
	t.Helper()
	id := uuid.New()
	kycExec(t, `INSERT INTO sellers (id,user_id,store_name,slug,email,pan_number)
	            VALUES ($1,$2,'KYC Backfill',$3,'kyc@example.test',$4)`,
		id, uuid.New(), "kyc-bf-"+id.String()[:8], fmt.Sprintf("abcde%04df", n))
	// A leading space and a lower-case IFSC, so normalisation is exercised
	// and the plaintext guard compares against the RAW stored value.
	kycExec(t, `INSERT INTO seller_payout_accounts (seller_id,account_holder_name,account_number,ifsc_code,is_primary)
	            VALUES ($1,'KYC Holder',$2,'hdfc0001234',TRUE)`,
		id, fmt.Sprintf(" 1234567%05d", n))
	return id
}

func seedKYCOrg(t *testing.T, n int) uuid.UUID {
	t.Helper()
	id := uuid.New()
	kycExec(t, `INSERT INTO organizations (id,name,pan) VALUES ($1,$2,$3)`,
		id, fmt.Sprintf("KYC Org %d", n), fmt.Sprintf("PQRST%04dZ", n))
	return id
}

func kycRemaining(t *testing.T) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, f := range kycFields {
		var n int64
		if err := testPool.QueryRow(context.Background(), f.remainingSQL).Scan(&n); err != nil {
			t.Fatal(err)
		}
		out[f.name] = n
	}
	return out
}

func requireNothingRemaining(t *testing.T) {
	t.Helper()
	for table, n := range kycRemaining(t) {
		if n != 0 {
			t.Fatalf("%s: %d row(s) still unsealed", table, n)
		}
	}
}

// ─── The estate is sealed, verified, and dedupe-compatible ───────────

func TestKYCMixedEstateBackfillsToCompletion(t *testing.T) {
	resetKYC(t)
	ctx := context.Background()
	for i := 0; i < 9; i++ {
		seedKYCSeller(t, i)
	}
	noPAN := seedKYCSeller(t, 99)
	kycExec(t, `UPDATE sellers SET pan_number=NULL WHERE id=$1`, noPAN)
	for i := 0; i < 3; i++ {
		seedKYCOrg(t, i)
	}

	job, err := New(testPool, kycCipher(t))
	if err != nil {
		t.Fatal(err)
	}
	job.BatchSize = 4 // several batches per table

	stats, err := job.RunKYC(ctx)
	if err != nil {
		t.Fatalf("RunKYC: %v", err)
	}
	requireNothingRemaining(t)
	want := map[string]int64{"seller_payout_accounts": 10, "sellers": 9, "organizations": 3}
	for _, s := range stats {
		if !s.Completed || s.Failed != 0 {
			t.Fatalf("%s did not complete cleanly: %s", s.Table, s)
		}
		if s.Verified != want[s.Table] || s.Encrypted != want[s.Table] {
			t.Fatalf("%s: verified=%d encrypted=%d, want %d of each", s.Table, s.Verified, s.Encrypted, want[s.Table])
		}
	}

	c := kycCipher(t)

	// Payout accounts: opens to the trimmed number; last4 and the lookup hash
	// match what the SERVICE computes on write, so duplicate detection spans
	// backfilled and newly written rows.
	rows, err := testPool.Query(ctx, `SELECT account_number, ifsc_code, account_number_enc,
	                                          account_number_hash, account_number_last4
	                                     FROM seller_payout_accounts`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var plain, ifsc, hash, last4 string
		var enc []byte
		if err := rows.Scan(&plain, &ifsc, &enc, &hash, &last4); err != nil {
			t.Fatal(err)
		}
		number := strings.TrimSpace(plain)
		got, err := c.OpenIdentifier(ctx, pii.ScopeKYC, enc)
		if err != nil || got != number {
			t.Fatalf("a backfilled account opens to %q (%v), want %q", got, err, number)
		}
		serviceSide, _ := c.SealIdentifier(ctx, pii.ScopeKYC, "bank_account", number, strings.ToUpper(ifsc))
		if hash != serviceSide.Hash {
			t.Fatal("the backfill's lookup hash differs from the service's for the same account")
		}
		if last4 != number[len(number)-4:] {
			t.Fatalf("last4 = %q for %q", last4, number)
		}
	}
	rows.Close()

	for _, q := range []string{
		`SELECT pan_number, pan_enc, pan_hash, pan_masked FROM sellers WHERE pan_number IS NOT NULL`,
		`SELECT pan, pan_enc, pan_hash, pan_masked FROM organizations`,
	} {
		rows, err := testPool.Query(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		seen := 0
		for rows.Next() {
			var plain, hash, masked string
			var enc []byte
			if err := rows.Scan(&plain, &enc, &hash, &masked); err != nil {
				t.Fatal(err)
			}
			pan := pii.NormalizePAN(plain)
			got, err := c.OpenIdentifier(ctx, pii.ScopeKYC, enc)
			if err != nil || got != pan {
				t.Fatalf("a backfilled PAN opens to %q (%v), want %q", got, err, pan)
			}
			serviceSide, _ := c.SealIdentifier(ctx, pii.ScopeKYC, "pan", pan)
			if hash != serviceSide.Hash || masked != pii.MaskPAN(pan) {
				t.Fatalf("PAN hash/mask disagree with the service: masked=%q", masked)
			}
			seen++
		}
		rows.Close()
		if seen == 0 {
			t.Fatalf("no rows checked for %s", q)
		}
	}

	// Progress never runs ahead of the ciphertext.
	var behind int64
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM seller_payout_accounts p
		 WHERE p.account_number_enc IS NULL AND btrim(p.account_number) <> ''
		   AND p.id <= (SELECT last_id FROM pii_kyc_backfill_progress WHERE table_name='seller_payout_accounts')`).
		Scan(&behind); err != nil {
		t.Fatal(err)
	}
	if behind != 0 {
		t.Fatalf("%d payout account(s) at or before the cursor have no ciphertext", behind)
	}

	// The address progress table was not touched: separate cutovers.
	var addressRows int64
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM pii_backfill_progress WHERE table_name IN ('seller_payout_accounts','sellers','organizations')`).
		Scan(&addressRows); err != nil {
		t.Fatal(err)
	}
	if addressRows != 0 {
		t.Fatal("the KYC backfill wrote into the ADDRESS progress table; gated/1000 would wait on it")
	}
}

func TestKYCRerunIsIdempotent(t *testing.T) {
	resetKYC(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		seedKYCSeller(t, i)
	}
	job, _ := New(testPool, kycCipher(t))
	if _, err := job.RunKYC(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot := func() []byte {
		var b []byte
		if err := testPool.QueryRow(ctx,
			`SELECT string_agg(account_number_enc, '' ORDER BY id) FROM seller_payout_accounts`).Scan(&b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	before := snapshot()
	stats, err := job.RunKYC(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, snapshot()) {
		t.Fatal("a re-run re-encrypted rows that were already sealed")
	}
	for _, s := range stats {
		if s.Table == "seller_payout_accounts" && s.Encrypted != 5 {
			t.Fatalf("encrypted=%d after a re-run over 5 rows; work was repeated", s.Encrypted)
		}
	}
}

// A failure is durable, blocks completion, and advances nothing.
func TestKYCAFailedRowIsRecordedAndBlocksCompletion(t *testing.T) {
	resetKYC(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		seedKYCSeller(t, i)
	}
	job, _ := New(testPool, brokenKYCCipher(t))
	if _, err := job.RunKYC(ctx); err == nil {
		t.Fatal("a KYC backfill with no usable key reported success")
	}

	var last *uuid.UUID
	var failed int64
	var completed *string
	if err := testPool.QueryRow(ctx, `SELECT last_id, failed, completed_at::text
	                                    FROM pii_kyc_backfill_progress WHERE table_name='seller_payout_accounts'`).
		Scan(&last, &failed, &completed); err != nil {
		t.Fatal(err)
	}
	if last != nil || failed == 0 || completed != nil {
		t.Fatalf("after a failed seal: last_id=%v failed=%d completed_at=%v; want nil, >0, nil", last, failed, completed)
	}
	if got := kycRemaining(t)["seller_payout_accounts"]; got != 3 {
		t.Fatalf("%d payout account(s) unsealed, want all 3", got)
	}
}

// An account an old writer changes while it is being sealed must not be
// sealed with the value it had a moment ago.
func TestKYCARowThatChangedSinceItWasReadIsNotSealedWithItsOldValue(t *testing.T) {
	resetKYC(t)
	ctx := context.Background()
	sellerID := seedKYCSeller(t, 1)
	var payoutID uuid.UUID
	if err := testPool.QueryRow(ctx, `SELECT id FROM seller_payout_accounts WHERE seller_id=$1`, sellerID).
		Scan(&payoutID); err != nil {
		t.Fatal(err)
	}
	job, _ := New(testPool, kycCipher(t))
	if err := job.ensureProgressRow(ctx, kycProgress, "seller_payout_accounts"); err != nil {
		t.Fatal(err)
	}

	// The candidate as the batch read it; the row has since moved on.
	stale := kycCandidate{id: payoutID, plain: "111122223333", ifsc: "hdfc0001234"}
	err := job.sealKYCOne(ctx, kycFields[0], stale)
	if !errors.Is(err, errRowMoved) {
		t.Fatalf("sealing a stale read: err = %v, want errRowMoved", err)
	}
	var enc []byte
	if err := testPool.QueryRow(ctx, `SELECT account_number_enc FROM seller_payout_accounts WHERE id=$1`, payoutID).
		Scan(&enc); err != nil {
		t.Fatal(err)
	}
	if enc != nil {
		t.Fatal("the row was sealed with a value it no longer holds; the seller would be paid into the wrong account")
	}
}

// ─── gated/1002 ──────────────────────────────────────────────────────

func execKYCScrub(t *testing.T) error {
	t.Helper()
	sql, err := database.Gated.ReadFile("gated/1002_kyc_pii_plaintext_scrub.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, err = testPool.Exec(context.Background(), string(sql))
	return err
}

func stampKYCCutover(t *testing.T) {
	t.Helper()
	kycExec(t, `UPDATE pii_kyc_cutover_state
	               SET ciphertext_authoritative_since=NOW(), old_writers_drained_at=NOW() WHERE id`)
}

func plaintextKYCLeft(t *testing.T) int64 {
	t.Helper()
	var n int64
	if err := testPool.QueryRow(context.Background(), `
		SELECT (SELECT count(*) FROM seller_payout_accounts WHERE account_number <> '')
		     + (SELECT count(*) FROM sellers WHERE pan_number IS NOT NULL)
		     + (SELECT count(*) FROM organizations WHERE pan IS NOT NULL)`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func requireRefusal(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("gated/1002 ran; want a refusal containing %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("gated/1002 refused for the wrong reason: %v (want %q)", err, want)
	}
}

func TestKYCScrubRefusesWithoutTheOperatorAssertions(t *testing.T) {
	resetKYC(t)
	seedKYCSeller(t, 1)
	job, _ := New(testPool, kycCipher(t))
	if _, err := job.RunKYC(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := plaintextKYCLeft(t)

	requireRefusal(t, execKYCScrub(t), "ciphertext-authoritative image is not recorded as live")
	kycExec(t, `UPDATE pii_kyc_cutover_state SET ciphertext_authoritative_since=NOW() WHERE id`)
	requireRefusal(t, execKYCScrub(t), "old writers are not recorded as drained")

	if plaintextKYCLeft(t) != before {
		t.Fatal("a refused scrub still cleared plaintext")
	}
}

func TestKYCScrubRefusesAnUnfinishedBackfill(t *testing.T) {
	resetKYC(t)
	seedKYCSeller(t, 1)
	stampKYCCutover(t)

	requireRefusal(t, execKYCScrub(t), "the KYC backfill has never run")

	job, _ := New(testPool, brokenKYCCipher(t))
	_, _ = job.RunKYC(context.Background())
	requireRefusal(t, execKYCScrub(t), "backfill incomplete_tables=")
}

// The progress table is a claim; the scrub checks the estate too. A row written
// after the backfill completed has no ciphertext, and must not be scrubbed.
func TestKYCScrubRefusesAnUnsealedRowEvenWhenProgressSaysComplete(t *testing.T) {
	resetKYC(t)
	seedKYCSeller(t, 1)
	job, _ := New(testPool, kycCipher(t))
	if _, err := job.RunKYC(context.Background()); err != nil {
		t.Fatal(err)
	}
	stampKYCCutover(t)
	seedKYCSeller(t, 2) // a straggler, after completion was stamped

	requireRefusal(t, execKYCScrub(t), "unsealed_payout_accounts=1")
}

func TestKYCScrubClearsThePlaintextAndLeavesEveryValueOpenable(t *testing.T) {
	resetKYC(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		seedKYCSeller(t, i)
	}
	seedKYCOrg(t, 1)

	// Legacy raw Aadhaar numbers, written before 035's trigger existed.
	var aadhaar string
	for d := '0'; d <= '9'; d++ {
		if v := "23456789012" + string(d); kyc.LooksLikeAadhaar(v) {
			aadhaar = v
		}
	}
	docSeller := seedKYCSeller(t, 50)
	kycExec(t, `ALTER TABLE seller_documents DISABLE TRIGGER trg_seller_documents_no_raw_aadhaar`)
	for _, typ := range []string{"aadhaar", "other", "cancelled_cheque"} {
		kycExec(t, `INSERT INTO seller_documents (seller_id,document_type,document_number,media_id)
		            VALUES ($1,$2,$3,$4)`, docSeller, typ, aadhaar, uuid.New())
	}
	kycExec(t, `ALTER TABLE seller_documents ENABLE TRIGGER trg_seller_documents_no_raw_aadhaar`)

	// Remember every value, to prove it survives only as ciphertext.
	originals := map[uuid.UUID]string{}
	rows, err := testPool.Query(ctx, `SELECT id, btrim(account_number) FROM seller_payout_accounts`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id uuid.UUID
		var v string
		if err := rows.Scan(&id, &v); err != nil {
			t.Fatal(err)
		}
		originals[id] = v
	}
	rows.Close()

	job, _ := New(testPool, kycCipher(t))
	if _, err := job.RunKYC(ctx); err != nil {
		t.Fatal(err)
	}
	stampKYCCutover(t)

	if err := execKYCScrub(t); err != nil {
		t.Fatalf("gated/1002 refused a completed, stamped cutover: %v", err)
	}

	if n := plaintextKYCLeft(t); n != 0 {
		t.Fatalf("%d KYC plaintext value(s) survived the scrub", n)
	}
	c := kycCipher(t)
	for id, want := range originals {
		var enc []byte
		if err := testPool.QueryRow(ctx, `SELECT account_number_enc FROM seller_payout_accounts WHERE id=$1`, id).
			Scan(&enc); err != nil {
			t.Fatal(err)
		}
		got, err := c.OpenIdentifier(ctx, pii.ScopeKYC, enc)
		if err != nil || got != want {
			t.Fatalf("after the scrub a payout account opens to %q (%v), want %q — the only copy is wrong", got, err, want)
		}
	}

	docs := map[string]*string{}
	drows, err := testPool.Query(ctx, `SELECT document_type, document_number FROM seller_documents WHERE seller_id=$1`, docSeller)
	if err != nil {
		t.Fatal(err)
	}
	for drows.Next() {
		var typ string
		var num *string
		if err := drows.Scan(&typ, &num); err != nil {
			t.Fatal(err)
		}
		docs[typ] = num
	}
	drows.Close()
	if docs["aadhaar"] != nil || docs["other"] != nil {
		t.Fatal("a raw Aadhaar number survived the scrub")
	}
	if docs["cancelled_cheque"] == nil {
		t.Fatal("the scrub cleared a cancelled cheque's account number as if it were Aadhaar")
	}

	var scrubbed *string
	if err := testPool.QueryRow(ctx, `SELECT scrubbed_at::text FROM pii_kyc_cutover_state WHERE id`).Scan(&scrubbed); err != nil {
		t.Fatal(err)
	}
	if scrubbed == nil {
		t.Fatal("the scrub did not stamp scrubbed_at")
	}
	if err := execKYCScrub(t); err != nil {
		t.Fatalf("re-running the scrub over a scrubbed estate: %v", err)
	}
}

// Boot migrations re-run; 035 must be a no-op the second time.
func TestKYCMigration035IsIdempotent(t *testing.T) {
	resetKYC(t)
	sql, err := database.Migrations.ReadFile("migrations/035_seller_kyc_pii_encryption.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := testPool.Exec(context.Background(), string(sql)); err != nil {
			t.Fatalf("applying 035 (pass %d): %v", i+1, err)
		}
	}
	var progress, triggers int64
	if err := testPool.QueryRow(context.Background(), `
		SELECT (SELECT count(*) FROM pii_kyc_backfill_progress),
		       (SELECT count(*) FROM pg_trigger
		         WHERE NOT tgisinternal AND tgname IN ('trg_payout_account_stale_ciphertext',
		               'trg_seller_pan_stale_ciphertext','trg_organization_pan_stale_ciphertext',
		               'trg_seller_documents_no_raw_aadhaar'))`).Scan(&progress, &triggers); err != nil {
		t.Fatal(err)
	}
	if progress != 3 || triggers != 4 {
		t.Fatalf("after applying 035 twice: %d progress rows (want 3), %d triggers (want 4)", progress, triggers)
	}
}
