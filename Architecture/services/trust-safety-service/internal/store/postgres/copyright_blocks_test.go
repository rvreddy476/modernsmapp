package postgres

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/atpost/trust-safety-service/database"
	"github.com/google/uuid"
)

// squash collapses whitespace so a guard is found however the SQL wraps.
func squash(sql string) string {
	return strings.Join(strings.Fields(sql), " ")
}

// The overturn guards live in SQL; the integration test exercises them
// against a scratch database (copyright_blocks_integration_test.go), and
// this pins their shape so a deleted guard fails without one.
func TestCopyrightBlockSQL_Guards(t *testing.T) {
	record := squash(recordCopyrightBlockSQL)
	for _, want := range []string{
		// A replay of a block (active or voided) never inserts again; only
		// a block newer than every earlier row of the review does.
		"WHERE NOT EXISTS ( SELECT 1 FROM trust.copyright_blocks b WHERE b.source = $4::text AND b.source_ref = $5::uuid AND (b.voided_at IS NULL OR b.blocked_at >= $8::timestamptz))",
		// One ACTIVE block per review (migration 020's partial unique index).
		"ON CONFLICT (source, source_ref) WHERE voided_at IS NULL DO NOTHING",
	} {
		if !strings.Contains(record, want) {
			t.Errorf("record SQL lacks %q:\n%s", want, record)
		}
	}
	void := squash(voidCopyrightBlockSQL)
	for _, want := range []string{
		"WHERE source = $1 AND source_ref = $2 AND user_id = $3",
		// Only an active block, and only one recorded before the clear.
		"AND voided_at IS NULL AND blocked_at < $6",
		"SET voided_at = clock_timestamp(), void_reason = $4, voided_by = $5",
	} {
		if !strings.Contains(void, want) {
			t.Errorf("void SQL lacks %q:\n%s", want, void)
		}
	}
}

// Migration 020 replaces 019's one-row-per-review-ever constraint with one
// active row per review, so a re-block after a clear can count again.
func TestCopyrightBlockMigration020(t *testing.T) {
	raw, err := database.Migrations.ReadFile("migrations/020_copyright_block_overturn.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := squash(string(raw))
	for _, re := range []string{
		`ALTER TABLE trust\.copyright_blocks DROP CONSTRAINT IF EXISTS copyright_blocks_one_per_source;`,
		`CREATE UNIQUE INDEX IF NOT EXISTS \w+ ON trust\.copyright_blocks \(source, source_ref\) WHERE voided_at IS NULL;`,
	} {
		if !regexp.MustCompile(re).MatchString(sql) {
			t.Errorf("migration 020 lacks %s", re)
		}
	}
}

// A void that names no user or review, no clear time or no reason is
// refused before the database is asked.
func TestVoidCopyrightBlock_RefusesIncompleteInput(t *testing.T) {
	s := &TrustExtrasStore{} // no pool: any query would panic
	by := uuid.New()
	full := CopyrightBlockVoidInput{UserID: uuid.New(), ReviewID: uuid.New(), ClearedAt: time.Now(), VoidedBy: &by, Reason: CopyrightBlockVoidAppealCleared}
	for name, mutate := range map[string]func(*CopyrightBlockVoidInput){
		"user":   func(in *CopyrightBlockVoidInput) { in.UserID = uuid.Nil },
		"review": func(in *CopyrightBlockVoidInput) { in.ReviewID = uuid.Nil },
		"time":   func(in *CopyrightBlockVoidInput) { in.ClearedAt = time.Time{} },
		"reason": func(in *CopyrightBlockVoidInput) { in.Reason = "  " },
	} {
		in := full
		mutate(&in)
		if voided, err := s.VoidCopyrightBlock(context.Background(), in); voided || !errors.Is(err, ErrCopyrightBlockInvalid) {
			t.Errorf("%s: voided=%v err=%v, want ErrCopyrightBlockInvalid", name, voided, err)
		}
	}
}
