//go:build integration

package piibackfill

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/atpost/commerce-service/database"
	"github.com/atpost/commerce-service/internal/pii"
	pgstore "github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// B5 — the backfill, against a live PostgreSQL.
//
// The properties that matter are all about failure: what the job does when it
// is interrupted, when KMS refuses, and when it is asked to mark complete a
// table that is not. A backfill that only works on the happy path is a
// backfill that will licence the scrub to destroy addresses.
//
// # Why this suite owns its own database
//
// The job is GLOBAL by nature: it seals every address in the estate, and
// `completed_at` means "nothing is left anywhere". Those are exactly the
// assertions that cannot be made against a database other test packages are
// concurrently inserting unsealed addresses into — and Go runs packages in
// parallel, so they were.
//
// Scoping the assertions to a label was the first attempt and it was wrong:
// it would have tested a narrower property than the one the gated scrub
// depends on. So the suite creates and drops its own database instead, and
// asserts the real thing.
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/piibackfill/... -v

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	dsn := os.Getenv("COMMERCE_TEST_DSN")
	refuseTheLiveDatabase(dsn)
	if dsn == "" {
		fmt.Println("COMMERCE_TEST_DSN not set; skipping backfill integration proofs")
		os.Exit(0)
	}
	ctx := context.Background()

	// Connect to the server the caller named, and create a scratch database
	// beside theirs. Named per run so a crashed run cannot poison the next.
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Printf("connect: %v\n", err)
		os.Exit(1)
	}
	scratch := "piibf_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+scratch); err != nil {
		fmt.Printf("creating the scratch database: %v\n", err)
		os.Exit(1)
	}
	admin.Close()

	scratchDSN := swapDatabase(dsn, scratch)
	pool, err := pgxpool.New(ctx, scratchDSN)
	if err != nil {
		fmt.Printf("connect to scratch: %v\n", err)
		os.Exit(1)
	}
	// The production bootstrap, so the schema under test is the real one.
	if err := pgstore.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		fmt.Printf("bootstrapping the scratch schema: %v\n", err)
		os.Exit(1)
	}
	testPool = pool

	code := m.Run()

	pool.Close()
	if cleanup, err := pgxpool.New(ctx, dsn); err == nil {
		_, _ = cleanup.Exec(ctx, `DROP DATABASE IF EXISTS `+scratch+` WITH (FORCE)`)
		cleanup.Close()
	}
	os.Exit(code)
}

// swapDatabase replaces the database name in a libpq URL.
func swapDatabase(dsn, name string) string {
	q := ""
	if i := strings.Index(dsn, "?"); i >= 0 {
		q = dsn[i:]
		dsn = dsn[:i]
	}
	if i := strings.LastIndex(dsn, "/"); i >= 0 {
		dsn = dsn[:i+1] + name
	}
	return dsn + q
}

// devCipher is the same static-key cipher a development environment uses. The
// backfill's contract is with pii.Cipher, not with KMS, so the key source is
// irrelevant to what these tests assert — and a real KMS call per row would
// make them untestable here.
func devCipher(t *testing.T) *pii.Cipher {
	t.Helper()
	c, err := pii.New(&pii.StaticKeyProvider{Keys: map[pii.Scope][]byte{
		pii.ScopeProfile:       []byte("0123456789abcdef0123456789abcdef"),
		pii.ScopeOrderSnapshot: []byte("fedcba9876543210fedcba9876543210"),
	}}, []byte("backfill-test-salt"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// reset clears the rows THIS suite owns so each test starts from a known
// population, and leaves every other package's fixtures alone.
func reset(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	for _, sql := range []string{
		`DELETE FROM customer_addresses`,
		`DELETE FROM seller_addresses`,
		`DELETE FROM pii_backfill_progress`,
		`UPDATE pii_cutover_state SET ciphertext_authoritative_since=NULL,
		        old_writers_drained_at=NULL, scrubbed_at=NULL WHERE id`,
	} {
		if _, err := testPool.Exec(ctx, sql); err != nil {
			t.Fatalf("reset (%s): %v", sql, err)
		}
	}
}

// seedPlaintextAddress inserts a legacy row: plaintext only, no ciphertext.
func seedPlaintextAddress(t *testing.T, n int) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := testPool.Exec(context.Background(), `
		INSERT INTO customer_addresses
		    (id,user_id,label,contact_name,phone,address_line_1,address_line_2,landmark,
		     city,state,country,postal_code,address_type,is_default)
		VALUES ($1,$2,'BackfillTest',$3,$4,$5,$6,$7,'Bengaluru','KA','IN','560002','home',FALSE)`,
		id, uuid.New(),
		fmt.Sprintf("Buyer %d", n),
		fmt.Sprintf("90000000%02d", n),
		fmt.Sprintf("%d Main St", n),
		fmt.Sprintf("Flat %d", n),
		fmt.Sprintf("Near park %d", n),
	)
	if err != nil {
		t.Fatalf("seed address: %v", err)
	}
	return id
}

func remaining(t *testing.T) int64 {
	t.Helper()
	var n int64
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM customer_addresses WHERE contact_name_enc IS NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// ─── The whole estate is encrypted ───────────────────────────────────

func TestB5MixedEstateBackfillsToCompletion(t *testing.T) {
	reset(t)
	ctx := context.Background()
	const n = 25
	for i := 0; i < n; i++ {
		seedPlaintextAddress(t, i)
	}

	job, err := New(testPool, devCipher(t))
	if err != nil {
		t.Fatal(err)
	}
	job.BatchSize = 7 // several batches, so the cursor is genuinely exercised

	stats, err := job.Run(ctx)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}

	if got := remaining(t); got != 0 {
		t.Fatalf("%d address(es) still have no ciphertext", got)
	}
	for _, s := range stats {
		if !s.Completed {
			t.Fatalf("%s did not complete: %s", s.Table, s)
		}
		if s.Failed != 0 {
			t.Fatalf("%s reported %d failed row(s)", s.Table, s.Failed)
		}
	}

	// And every row actually decrypts back to its source. "Encrypted" is
	// worthless if it encrypted to nothing.
	c := devCipher(t)
	rows, err := testPool.Query(ctx, `
		SELECT contact_name_enc, phone_enc, address_line_1_enc, address_line_2_enc, landmark_enc,
		       city, state, postal_code, country
		  FROM customer_addresses
		`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var s pii.Sealed
		var city, state, postal, country string
		if err := rows.Scan(&s.ContactName, &s.Phone, &s.AddressLine1,
			&s.AddressLine2, &s.Landmark, &city, &state, &postal, &country); err != nil {
			t.Fatal(err)
		}
		got, err := c.OpenAddress(ctx, pii.ScopeProfile, s, city, state, postal, country)
		if err != nil {
			t.Fatalf("a backfilled row does not decrypt: %v", err)
		}
		if got.ContactName == "" || got.AddressLine1 == "" {
			t.Fatal("a backfilled row decrypted to empty identifying fields")
		}
		seen++
	}
	if seen != n {
		t.Fatalf("verified %d rows, want %d", seen, n)
	}
}

// Re-running over a completed estate must be a no-op, not a re-encryption.
// Re-sealing would change every key version for no benefit and would make the
// job non-idempotent — which matters because operators retry things.
func TestB5RerunIsIdempotent(t *testing.T) {
	reset(t)
	for i := 0; i < 5; i++ {
		seedPlaintextAddress(t, i)
	}
	job, _ := New(testPool, devCipher(t))

	if _, err := job.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	var firstVersions []int
	rows, _ := testPool.Query(context.Background(),
		`SELECT pii_key_version FROM customer_addresses ORDER BY id`)
	for rows.Next() {
		var v int
		_ = rows.Scan(&v)
		firstVersions = append(firstVersions, v)
	}
	rows.Close()

	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("second run: %v", err)
	}
	rows2, _ := testPool.Query(context.Background(),
		`SELECT pii_key_version FROM customer_addresses ORDER BY id`)
	i := 0
	for rows2.Next() {
		var v int
		_ = rows2.Scan(&v)
		if v != firstVersions[i] {
			t.Fatalf("row %d was re-encrypted (version %d -> %d)", i, firstVersions[i], v)
		}
		i++
	}
	rows2.Close()
}

// ─── Interruption and resumption ─────────────────────────────────────

// A cancelled run leaves a durable cursor, and the next run finishes the job
// without skipping or repeating logical work.
func TestB5ResumesAfterInterruption(t *testing.T) {
	reset(t)
	const n = 20
	for i := 0; i < n; i++ {
		seedPlaintextAddress(t, i)
	}

	job, _ := New(testPool, devCipher(t))
	job.BatchSize = 3

	// Cancel partway: the context is cut after a few rows have committed.
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			if remaining(t) <= n-5 {
				cancel()
				return
			}
		}
	}()
	_, _ = job.Run(ctx)

	partial := remaining(t)
	if partial == 0 {
		t.Skip("the run completed before it could be interrupted; timing-dependent")
	}
	if partial == n {
		t.Fatal("the interrupted run committed nothing at all")
	}

	// Resume. Nothing may be left, and nothing may have been double-counted.
	stats, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if got := remaining(t); got != 0 {
		t.Fatalf("after resuming, %d row(s) still unencrypted", got)
	}
	for _, s := range stats {
		if s.Table == "customer_addresses" && s.Encrypted > int64(n) {
			t.Fatalf("encrypted=%d exceeds the %d rows that exist; work was repeated",
				s.Encrypted, n)
		}
	}
}

// THE invariant. Progress must never claim a row the ciphertext does not back.
//
// Asserted directly against the estate: every row at or before the cursor has
// ciphertext. If that ever fails, the scrub will clear a plaintext address
// that nothing can rebuild.
func TestB5ProgressNeverRunsAheadOfCiphertext(t *testing.T) {
	reset(t)
	for i := 0; i < 15; i++ {
		seedPlaintextAddress(t, i)
	}
	job, _ := New(testPool, devCipher(t))
	job.BatchSize = 4
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	var behindCursor int64
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM customer_addresses a
		 WHERE a.contact_name_enc IS NULL
		   AND a.id <= (SELECT last_id FROM pii_backfill_progress WHERE table_name='customer_addresses')
	`).Scan(&behindCursor); err != nil {
		t.Fatal(err)
	}
	if behindCursor != 0 {
		t.Fatalf("%d row(s) at or before the cursor have no ciphertext; the cursor is a lie "+
			"and the scrub would destroy them", behindCursor)
	}
}

// A KMS failure must NOT advance the cursor past the row it failed on.
func TestB5AFailedRowDoesNotAdvanceTheCursor(t *testing.T) {
	reset(t)
	for i := 0; i < 5; i++ {
		seedPlaintextAddress(t, i)
	}
	// A cipher with no key for the profile scope: every seal fails.
	broken, err := pii.New(&pii.StaticKeyProvider{Keys: map[pii.Scope][]byte{
		pii.ScopeOrderSnapshot: []byte("fedcba9876543210fedcba9876543210"),
	}}, []byte("backfill-test-salt"))
	if err != nil {
		t.Fatal(err)
	}
	job, _ := New(testPool, broken)

	if _, err := job.Run(context.Background()); err == nil {
		t.Fatal("a backfill with no usable key reported success")
	}

	var last *uuid.UUID
	var failed int64
	if err := testPool.QueryRow(context.Background(),
		`SELECT last_id, failed FROM pii_backfill_progress WHERE table_name='customer_addresses'`).
		Scan(&last, &failed); err != nil {
		t.Fatal(err)
	}
	if last != nil {
		t.Fatalf("the cursor advanced to %s despite every row failing", last)
	}
	if failed == 0 {
		t.Fatal("the failure was not recorded; the scrub's precondition would not see it")
	}
	if got := remaining(t); got != 5 {
		t.Fatalf("%d row(s) remain unencrypted, want all 5 — a failed seal must not be "+
			"mistaken for a completed one", got)
	}
}

// A table with failures must never be marked complete: completed_at is exactly
// what the gated scrub trusts.
func TestB5FailuresBlockCompletion(t *testing.T) {
	reset(t)
	seedPlaintextAddress(t, 1)
	broken, _ := pii.New(&pii.StaticKeyProvider{Keys: map[pii.Scope][]byte{
		pii.ScopeOrderSnapshot: []byte("fedcba9876543210fedcba9876543210"),
	}}, []byte("backfill-test-salt"))
	job, _ := New(testPool, broken)
	_, _ = job.Run(context.Background())

	var completed *string
	if err := testPool.QueryRow(context.Background(),
		`SELECT completed_at::text FROM pii_backfill_progress WHERE table_name='customer_addresses'`).
		Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if completed != nil {
		t.Fatal("a table with failed rows was marked complete; the scrub would proceed")
	}
}

// Two workers must not both drive one ordered cursor.
func TestB5ConcurrentRunsAreRefused(t *testing.T) {
	reset(t)
	for i := 0; i < 3; i++ {
		seedPlaintextAddress(t, i)
	}
	a, _ := New(testPool, devCipher(t))
	b, _ := New(testPool, devCipher(t))

	// Hold the lock by starting a run that blocks on a cancelled context
	// after acquiring it is not deterministic, so assert the simpler
	// property directly: while one run holds the advisory lock, the other
	// is refused.
	conn, err := testPool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	var got bool
	if err := conn.QueryRow(context.Background(),
		`SELECT pg_try_advisory_lock($1)`, lockKey).Scan(&got); err != nil || !got {
		t.Fatalf("could not take the backfill lock: %v", err)
	}

	if _, err := a.Run(context.Background()); err == nil {
		t.Fatal("a second backfill ran while another held the lock; two workers over one " +
			"ordered cursor each advance it past rows the other is mid-way through")
	}
	_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, lockKey)
	conn.Release()

	if _, err := b.Run(context.Background()); err != nil {
		t.Fatalf("after the lock was released the backfill should run: %v", err)
	}
}

// ─── The two defects this job's own proof caught ─────────────────────

// A row inserted WHILE the backfill is running must still be encrypted.
//
// This is the defect that made the cursor design wrong. Primary keys here are
// gen_random_uuid(), so a row written after the job passed that point in the
// ordering sorts BELOW the old cursor — and `WHERE id > last_id` would never
// visit it again. Twelve addresses sat permanently unsealed behind a cursor
// that reported "complete".
//
// Selecting on "still unsealed" makes the remaining work a property of the
// data, so a late arrival is simply the next batch.
func TestB5RowsWrittenAfterAPassAreStillEncrypted(t *testing.T) {
	reset(t)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		seedPlaintextAddress(t, i)
	}
	job, _ := New(testPool, devCipher(t))

	if _, err := job.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if got := remaining(t); got != 0 {
		t.Fatalf("first pass left %d unsealed", got)
	}

	// A live service writes more rows. Random UUIDs, so several will sort
	// below wherever the old cursor stopped.
	for i := 100; i < 115; i++ {
		seedPlaintextAddress(t, i)
	}
	before := remaining(t)
	if before == 0 {
		t.Fatal("the new rows were seeded already sealed; the test proves nothing")
	}

	if _, err := job.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if got := remaining(t); got != 0 {
		t.Fatalf("%d of %d rows written after the first pass are still unencrypted — "+
			"they sort below where a cursor would have stopped, and the scrub would "+
			"destroy them", got, before)
	}
}

// A table that WAS complete and has since gained unsealed rows must stop
// reporting complete.
//
// completed_at is exactly what the gated scrub's precondition reads. A stamp
// that stopped being true is worse than no stamp: it licences the destructive
// step on a claim nobody re-checked.
func TestB5CompletionIsClearedWhenATableRegresses(t *testing.T) {
	reset(t)
	ctx := context.Background()
	seedPlaintextAddress(t, 1)
	job, _ := New(testPool, devCipher(t))

	if _, err := job.Run(ctx); err != nil {
		t.Fatal(err)
	}
	var completed *string
	_ = testPool.QueryRow(ctx,
		`SELECT completed_at::text FROM pii_backfill_progress WHERE table_name='customer_addresses'`).
		Scan(&completed)
	if completed == nil {
		t.Fatal("the table did not complete on a clean estate")
	}

	// A straggler writer, or a restored backup, reintroduces plaintext.
	seedPlaintextAddress(t, 2)

	// Any pass must notice. This one deliberately fails to seal, so the only
	// thing under test is whether the stale stamp is cleared.
	broken, _ := pii.New(&pii.StaticKeyProvider{Keys: map[pii.Scope][]byte{
		pii.ScopeOrderSnapshot: []byte("fedcba9876543210fedcba9876543210"),
	}}, []byte("backfill-test-salt"))
	brokenJob, _ := New(testPool, broken)
	_, _ = brokenJob.Run(ctx)

	_ = testPool.QueryRow(ctx,
		`SELECT completed_at::text FROM pii_backfill_progress WHERE table_name='customer_addresses'`).
		Scan(&completed)
	if completed != nil {
		t.Fatal("the table still claims to be complete while holding an unsealed row; " +
			"the gated scrub would proceed on a claim that stopped being true")
	}
}

// After the scrub, the ciphertext is the SOLE copy. This asserts it still
// opens — run against the scrubbed database, so it is the one check that
// says the cutover did not quietly destroy the estate.
//
// It does not reset(), because it is meant to be pointed at a database the
// gated scrub has already run against.
func TestB5PostScrubCiphertextStillOpens(t *testing.T) {
	ctx := context.Background()
	c := devCipher(t)

	rows, err := testPool.Query(ctx, `
		SELECT contact_name_enc, phone_enc, address_line_1_enc, address_line_2_enc, landmark_enc,
		       city, state, postal_code, country, contact_name
		  FROM customer_addresses
		 WHERE contact_name_enc IS NOT NULL`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	seen, scrubbed := 0, 0
	for rows.Next() {
		var s pii.Sealed
		var city, state, postal, country, plaintextName string
		if err := rows.Scan(&s.ContactName, &s.Phone, &s.AddressLine1,
			&s.AddressLine2, &s.Landmark, &city, &state, &postal, &country,
			&plaintextName); err != nil {
			t.Fatal(err)
		}
		got, err := c.OpenAddress(ctx, pii.ScopeProfile, s, city, state, postal, country)
		if err != nil {
			t.Fatalf("a row does not decrypt after the scrub — its address is gone: %v", err)
		}
		if got.ContactName == "" || got.AddressLine1 == "" {
			t.Fatal("a row decrypted to empty identifying fields after the scrub")
		}
		if plaintextName == "" {
			scrubbed++
		}
		seen++
	}
	if seen == 0 {
		t.Skip("no encrypted rows in this database; point this at a scrubbed estate")
	}
	if scrubbed == 0 {
		t.Skip("this estate has not been scrubbed; nothing to assert")
	}
	t.Logf("%d of %d encrypted rows are scrubbed and still decrypt correctly", scrubbed, seen)
}
