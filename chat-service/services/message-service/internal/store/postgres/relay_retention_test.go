package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/database"
	"github.com/atpost/chat-message-service/internal/contentcrypt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Chat backend audit, 30 Oct 2026: H3/H4 (outbox claims and dead letters),
// M1 (idempotency purge and takeover), M2 (scheduled cap and fair claims),
// M8 (delivery intent compaction), L9 (online index builds).

func normSQL(s string) string { return strings.Join(strings.Fields(s), " ") }

func mustContain(t *testing.T, sql string, wants ...string) {
	t.Helper()
	n := normSQL(sql)
	for _, w := range wants {
		if !strings.Contains(n, normSQL(w)) {
			t.Fatalf("SQL lacks %q:\n%s", w, n)
		}
	}
}

func TestOutboxFetchClaimsSkipLockedLeasedRowsInIDOrder(t *testing.T) {
	now := time.Now()
	s, f := newFakeStore(t, func(sql string, _ []any) [][]any {
		if strings.Contains(sql, "RETURNING o.id") {
			// RETURNING order is not id order.
			return [][]any{
				{int64(9), "MemberAdded", "evt:9", json.RawMessage(`{"conversation_id":"c"}`), now},
				{int64(4), "MemberAdded", "evt:4", json.RawMessage(`{"conversation_id":"c"}`), now},
			}
		}
		return nil
	})
	s.crypt = nil // legacy plaintext rows
	got, err := s.FetchUnpublishedOutboxEvents(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != 4 || got[1].ID != 9 || got[0].DedupeKey != "evt:4" {
		t.Fatalf("events = %+v", got)
	}
	c := f.calls[0]
	mustContain(t, c.sql,
		"UPDATE chat.outbox_events o SET claimed_until = NOW() + make_interval(secs => $2::float8)",
		"published_at IS NULL AND failed_at IS NULL",
		"(claimed_until IS NULL OR claimed_until <= NOW())",
		"ORDER BY id LIMIT $1 FOR UPDATE SKIP LOCKED")
	if c.args[0] != 50 || c.args[1] != outboxClaimLease.Seconds() {
		t.Fatalf("claim args = %v", c.args)
	}
}

func TestOutboxRowThatDoesNotOpenCountsAsAFailedAttempt(t *testing.T) {
	c := testCipher(t)
	moved, _ := c.SealJSON(context.Background(), contentcrypt.ScopeOutboxPayload, contentcrypt.OutboxRow("MessageCreated", "message-created:9"), []byte(`{"text":"x"}`))
	s, f := newFakeStore(t, func(sql string, _ []any) [][]any {
		if strings.Contains(sql, "RETURNING o.id") {
			return [][]any{{int64(2), "MessageCreated", "message-created:2", json.RawMessage(moved), time.Now()}}
		}
		if strings.Contains(sql, "RETURNING failed_at IS NOT NULL") {
			return [][]any{{false}}
		}
		return nil
	})
	got, err := s.FetchUnpublishedOutboxEvents(context.Background(), 50)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %+v, %v", got, err)
	}
	var rec *dbCall
	for i := range f.calls {
		if strings.Contains(f.calls[i].sql, "SET attempts = attempts + 1") {
			rec = &f.calls[i]
		}
	}
	if rec == nil {
		t.Fatal("an unopenable row was not recorded as a failed attempt")
	}
	if rec.args[0] != int64(2) || rec.args[2] != false || rec.args[3] != true {
		t.Fatalf("failure args = %v; want id 2, not permanent, counted", rec.args)
	}
	for _, a := range rec.args {
		if strings.Contains(argText(a), `"text"`) {
			t.Fatal("the payload reached last_error")
		}
	}
}

func TestRecordOutboxEventFailureDeadLettersAndBacksOff(t *testing.T) {
	s, f := newFakeStore(t, func(string, []any) [][]any { return [][]any{{true}} })
	dead, err := s.RecordOutboxEventFailure(context.Background(), 5, OutboxFailure{Reason: strings.Repeat("e", 2000), Permanent: true})
	if err != nil || !dead {
		t.Fatalf("dead=%v err=%v", dead, err)
	}
	c := f.calls[0]
	mustContain(t, c.sql,
		"failed_at = COALESCE(failed_at, CASE WHEN $3::bool OR ($4::bool AND attempts + 1 >= $5) THEN NOW() END)",
		"WHEN $4::bool THEN NOW() + LEAST(INTERVAL '5 minutes'",
		"WHERE id = $1 AND published_at IS NULL")
	if len(c.args[1].(string)) != 1000 || c.args[4] != OutboxMaxAttempts {
		t.Fatalf("args = %v", c.args[2:])
	}

	// A row published meanwhile is not an error.
	s2, _ := newFakeStore(t, nil)
	if dead, err := s2.RecordOutboxEventFailure(context.Background(), 5, OutboxFailure{Reason: "x"}); err != nil || dead {
		t.Fatalf("dead=%v err=%v", dead, err)
	}
}

func TestMarkOutboxEventsPublishedIsOneStatement(t *testing.T) {
	s, f := newFakeStore(t, nil)
	if err := s.MarkOutboxEventsPublished(context.Background(), nil); err != nil || len(f.calls) != 0 {
		t.Fatal("an empty batch must not touch the database")
	}
	if err := s.MarkOutboxEventsPublished(context.Background(), []int64{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 {
		t.Fatalf("%d statements for one batch", len(f.calls))
	}
	mustContain(t, f.calls[0].sql, "SET published_at = NOW(), claimed_until = NULL", "WHERE id = ANY($1)")
}

func TestRelayLockIsANoOpWithoutAPool(t *testing.T) {
	s, _ := newFakeStore(t, nil)
	release, ok, err := s.TryOutboxRelayLock(context.Background())
	if err != nil || !ok || release == nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	release()
}

// M1: an expired key or an abandoned in-progress key is taken over; a
// tombstone (unsent message) never is.
func TestCreateIdempotencyKeyTakesOverExpiredAndAbandonedKeys(t *testing.T) {
	s, f := newFakeStore(t, nil)
	if _, err := s.CreateIdempotencyKey(context.Background(), "k", "h"); err != nil {
		t.Fatal(err)
	}
	c := f.calls[0]
	mustContain(t, c.sql,
		"ON CONFLICT (key) DO UPDATE",
		"created_at = NOW(), expires_at = NOW() + INTERVAL '24 hours'",
		"k.expires_at <= NOW()",
		"k.response IS NULL AND k.request_hash = EXCLUDED.request_hash AND k.created_at < NOW() - make_interval(secs => $3::float8)",
		"AND NOT "+idempotencyTombstoneSQL)
	if c.args[2] != IdempotencyInProgressLease.Seconds() || IdempotencyInProgressLease > 5*time.Minute {
		t.Fatalf("lease arg = %v", c.args[2])
	}
}

func TestCheckIdempotencyKeyReadsExpiredRows(t *testing.T) {
	s, f := newFakeStore(t, nil)
	_, _ = s.CheckIdempotencyKey(context.Background(), "k")
	if strings.Contains(f.calls[0].sql, "expires_at") {
		t.Fatal("Check must not hide an expired tombstone (it would answer 500 instead of in progress)")
	}
}

func TestPurgeExpiredIdempotencyKeysKeepsTombstones(t *testing.T) {
	s, f := newFakeStore(t, nil)
	cut := time.Now()
	n, err := s.PurgeExpiredIdempotencyKeys(context.Background(), cut, 500)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if len(f.calls) != 2 {
		t.Fatalf("%d statements", len(f.calls))
	}
	mustContain(t, f.calls[0].sql, "SET expires_at = 'infinity'", "k.expires_at < $1 AND "+idempotencyTombstoneSQL)
	mustContain(t, f.calls[1].sql, "DELETE FROM chat.idempotency_keys", "k.expires_at < $1 AND NOT "+idempotencyTombstoneSQL,
		"LIMIT $2 FOR UPDATE SKIP LOCKED")
	if f.calls[1].args[0] != cut || f.calls[1].args[1] != 500 {
		t.Fatalf("args = %v", f.calls[1].args)
	}
	if n, _ := s.PurgeExpiredIdempotencyKeys(context.Background(), cut, 0); n != 0 {
		t.Fatal("limit 0 must do nothing")
	}
}

func TestIdempotencyTombstoneIsAnUnsentCompletedSend(t *testing.T) {
	mustContain(t, idempotencyTombstoneSQL,
		"k.response IS NULL AND EXISTS",
		"i.idempotency_key = k.key AND i.completed_at IS NOT NULL")
}

// M2: the cap check and the insert are one statement under a per-sender lock.
func TestCreateScheduledMessageEnforcesThePerUserCap(t *testing.T) {
	s, f := newFakeStore(t, nil) // the INSERT ... WHERE count < cap returns no row
	sender := uuid.New()
	content := "later"
	_, err := s.CreateScheduledMessage(context.Background(), &ScheduledMessage{ConversationID: uuid.New(), SenderID: sender, Type: "text", Content: &content, SendAt: time.Now().Add(time.Hour)})
	if !errors.Is(err, ErrScheduledMessageLimit) {
		t.Fatalf("err = %v, want ErrScheduledMessageLimit", err)
	}
	if len(f.calls) != 2 {
		t.Fatalf("%d statements", len(f.calls))
	}
	mustContain(t, f.calls[0].sql, "pg_advisory_xact_lock(hashtextextended('chat.scheduled_messages:' || $1::text, 0))")
	if f.calls[0].args[0] != sender.String() {
		t.Fatalf("lock key = %v", f.calls[0].args[0])
	}
	mustContain(t, f.calls[1].sql,
		"SELECT $1, $2, $3, $4, $5, $6, $7",
		"WHERE (SELECT COUNT(*) FROM chat.scheduled_messages WHERE sender_id = $3 AND status = 'pending') < $8")
	if f.calls[1].args[7] != MaxPendingScheduledPerUser || MaxPendingScheduledPerUser != 100 {
		t.Fatalf("cap = %v", f.calls[1].args[7])
	}
	if !strings.Contains(ErrScheduledMessageLimit.Error(), "100") {
		t.Fatalf("error text %q does not name the limit", ErrScheduledMessageLimit)
	}
}

func TestScheduledClaimIsFairAndSkipLocked(t *testing.T) {
	now := time.Now()
	early, late := now.Add(-time.Hour), now.Add(-time.Minute)
	retry := now.Add(-30 * time.Minute)
	s, f := newFakeStore(t, func(sql string, _ []any) [][]any {
		row := func(id uuid.UUID, sendAt time.Time, next *time.Time) []any {
			return []any{id, uuid.New(), uuid.New(), "text", nil, nil, sendAt, "pending", 0, next, nil, nil, now}
		}
		return [][]any{row(uuid.New(), late, nil), row(uuid.New(), early, &retry), row(uuid.New(), early, nil)}
	})
	s.crypt = nil
	got, err := s.GetPendingScheduledMessages(context.Background(), now, 100)
	if err != nil || len(got) != 3 {
		t.Fatalf("got %d, %v", len(got), err)
	}
	if !got[0].SendAt.Equal(early) || got[0].NextAttemptAt != nil || got[1].NextAttemptAt == nil || !got[2].SendAt.Equal(late) {
		t.Fatalf("not in due order: %+v", got)
	}
	c := f.calls[0]
	mustContain(t, c.sql,
		"(claimed_until IS NULL OR claimed_until <= NOW())",
		"LIMIT $3 FOR UPDATE SKIP LOCKED",
		"row_number() OVER (PARTITION BY sender_id ORDER BY due_at, id) AS rn",
		"WHERE rn <= $4 ORDER BY due_at, id LIMIT $2",
		"SET claimed_until = NOW() + make_interval(secs => $5::float8)")
	if c.args[1] != 100 || c.args[2] != 100*scheduledClaimWindow || c.args[3] != ScheduledPerSenderPerClaim || c.args[4] != ScheduledClaimLease.Seconds() {
		t.Fatalf("claim args = %v", c.args)
	}
}

func TestScheduledOutcomeReleasesTheLease(t *testing.T) {
	s, f := newFakeStore(t, nil)
	_ = s.MarkScheduledMessageSent(context.Background(), uuid.New())
	_ = s.RecordScheduledMessageFailure(context.Background(), uuid.New(), "x", 5)
	for _, c := range f.calls {
		mustContain(t, c.sql, "claimed_until = NULL")
	}
}

// M8: completed intents lose their member list unless a disappearing
// message's expiry still needs it.
func TestCompactCompletedDeliveryIntentsKeepsPendingExpiries(t *testing.T) {
	s, f := newFakeStore(t, func(sql string, args []any) [][]any {
		if strings.Contains(sql, "pg_index") && args[0] == compactionIndex {
			return [][]any{{true}}
		}
		return nil
	})
	cut := time.Now().Add(-7 * 24 * time.Hour)
	if _, err := s.CompactCompletedDeliveryIntents(context.Background(), cut, 1000); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 2 {
		t.Fatalf("%d statements", len(f.calls))
	}
	mustContain(t, f.calls[1].sql,
		"SET member_ids = '{}'::uuid[]",
		"d.completed_at IS NOT NULL AND d.completed_at < $1 AND d.member_ids <> '{}'::uuid[]",
		"NOT EXISTS ( SELECT 1 FROM chat.message_expiries e WHERE e.message_id = d.message_id AND (e.deleted_at IS NULL OR e.deleted_at >= $1))",
		"FOR UPDATE SKIP LOCKED")
	if strings.Contains(normSQL(f.calls[1].sql), "DELETE") {
		t.Fatal("compaction must not delete the intent row")
	}
	if n, _ := s.CompactCompletedDeliveryIntents(context.Background(), cut, 0); n != 0 {
		t.Fatal("limit 0 must do nothing")
	}
}

// Without its partial index a compaction batch would sort the whole table:
// it waits for the online build.
func TestCompactionWaitsForItsIndex(t *testing.T) {
	for name, state := range map[string][][]any{"missing": nil, "invalid": {{false}}} {
		s, f := newFakeStore(t, func(sql string, _ []any) [][]any {
			if strings.Contains(sql, "pg_index") {
				return state
			}
			return nil
		})
		n, err := s.CompactCompletedDeliveryIntents(context.Background(), time.Now(), 1000)
		if err != nil || n != 0 || len(f.calls) != 1 {
			t.Fatalf("%s index: n=%d err=%v statements=%d", name, n, err, len(f.calls))
		}
	}
}

// L9: the online index list is valid and identical to migration 020.
func TestConcurrentIndexListMatchesMigration020(t *testing.T) {
	if err := ValidateConcurrentIndexes(database.ConcurrentIndexes); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../../migrations/020_concurrent_indexes.sql")
	if err != nil {
		t.Fatal(err)
	}
	var stmts []string
	for _, st := range strings.Split(stripSQLComments(string(raw)), ";") {
		if st = normSQL(st); st != "" {
			stmts = append(stmts, st)
		}
	}
	if len(stmts) != len(database.ConcurrentIndexes) {
		t.Fatalf("migration has %d statements, list has %d", len(stmts), len(database.ConcurrentIndexes))
	}
	for i, ix := range database.ConcurrentIndexes {
		if stmts[i] != normSQL(ix.SQL) {
			t.Fatalf("statement %d differs:\nfile: %s\nlist: %s", i, stmts[i], normSQL(ix.SQL))
		}
	}
	setup, err := os.ReadFile("../../../database/setup.sql")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToUpper(stripSQLComments(string(setup))), "CONCURRENTLY") {
		t.Fatal("setup.sql runs in one transaction; CONCURRENTLY there fails every boot")
	}
}

var sqlLineComment = regexp.MustCompile(`(?m)--.*$`)

func stripSQLComments(s string) string { return sqlLineComment.ReplaceAllString(s, "") }

func TestValidateConcurrentIndexesRefusesBadEntries(t *testing.T) {
	for name, list := range map[string][]database.ConcurrentIndex{
		"not concurrent": {{Name: "idx_a", SQL: "CREATE INDEX IF NOT EXISTS idx_a ON chat.t (a)"}},
		"name mismatch":  {{Name: "idx_a", SQL: "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_b ON chat.t (a)"}},
		"two statements": {{Name: "idx_a", SQL: "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_a ON chat.t (a); DROP TABLE chat.t"}},
		"bad name":       {{Name: "idx;a", SQL: "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx;a ON chat.t (a)"}},
		"duplicate": {
			{Name: "idx_a", SQL: "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_a ON chat.t (a)"},
			{Name: "idx_a", SQL: "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_a ON chat.t (a)"},
		},
	} {
		if ValidateConcurrentIndexes(list) == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
}

// fakeIndexConn answers the pg_index lookup per index name.
type fakeIndexConn struct {
	state map[string]string // name -> "valid" | "invalid" (absent: missing)
	execs []string
}

func (c *fakeIndexConn) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	c.execs = append(c.execs, sql)
	return pgconn.NewCommandTag("CREATE INDEX"), nil
}

func (c *fakeIndexConn) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	switch c.state[args[0].(string)] {
	case "valid":
		return fakeRow{vals: []any{true}}
	case "invalid":
		return fakeRow{vals: []any{false}}
	}
	return fakeRow{err: pgx.ErrNoRows}
}

func TestBuildIndexesSkipsValidRebuildsInvalidBuildsMissing(t *testing.T) {
	list := []database.ConcurrentIndex{
		{Name: "idx_ok", SQL: "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_ok ON chat.t (a)"},
		{Name: "idx_bad", SQL: "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_bad ON chat.t (b)"},
		{Name: "idx_new", SQL: "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_new ON chat.t (c)"},
	}
	conn := &fakeIndexConn{state: map[string]string{"idx_ok": "valid", "idx_bad": "invalid"}}
	if err := buildIndexes(context.Background(), conn, list, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"DROP INDEX CONCURRENTLY IF EXISTS chat.idx_bad",
		list[1].SQL,
		list[2].SQL,
	}
	if strings.Join(conn.execs, "\n") != strings.Join(want, "\n") {
		t.Fatalf("execs:\n%s\nwant:\n%s", strings.Join(conn.execs, "\n"), strings.Join(want, "\n"))
	}
}
