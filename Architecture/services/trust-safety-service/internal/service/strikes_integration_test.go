//go:build integration

// Strike lifecycle, standing snapshot, enforcement outbox and migration 011
// against trust_safety_it_test (Copyright Match plan T5-1..T5-4, T4-3,
// P-7). Every test seeds its own users, so the package can run beside the
// other integration packages.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/shared/events"
	"github.com/atpost/trust-safety-service/database"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newStrikeRig(t *testing.T) (*pgxpool.Pool, *Service, *postgres.TrustExtrasStore) {
	t.Helper()
	pool := openM7TrustDB(t)
	extras := postgres.NewExtrasStore(pool).WithEventsTopic("trust.test.v1")
	svc := New(postgres.New(pool), nil)
	svc.SetExtrasStore(extras)
	return pool, svc, extras
}

type outboxRow struct {
	ID        int64
	EventType string
	Topic     string
	Key       string
	Payload   json.RawMessage
	Attempts  int
	Published bool
	LastError *string
}

func outboxRowsFor(t *testing.T, pool *pgxpool.Pool, key string) []outboxRow {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT id, event_type, topic, partition_key, payload, attempts, published_at IS NOT NULL, last_error
		FROM trust.enforcement_outbox WHERE partition_key = $1 ORDER BY id`, key)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []outboxRow
	for rows.Next() {
		var r outboxRow
		if err := rows.Scan(&r.ID, &r.EventType, &r.Topic, &r.Key, &r.Payload, &r.Attempts, &r.Published, &r.LastError); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func strikeRowCount(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM trust.user_strikes WHERE user_id = $1`, userID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestStrikeIssue_IdempotentAuditedAndOutboxed(t *testing.T) {
	pool, svc, extras := newStrikeRig(t)
	ctx := context.Background()
	user, admin, caseID := uuid.New(), uuid.New(), uuid.New()
	in := IssueStrikeInput{UserID: user, Reason: "copyright upheld", Severity: "strike", CaseID: &caseID,
		StrikeGroup: "copyright", IdempotencyKey: "copyright_case:" + caseID.String() + ":strike"}

	first, created, err := svc.IssueStrike(ctx, in, adminMeta(admin, "copyright upheld"))
	if err != nil || !created {
		t.Fatalf("first issue: created=%v err=%v", created, err)
	}
	if first.PolicyVersion != postgres.StrikePolicyVersion || !first.ExpiresAt.Equal(first.CreatedAt.Add(postgres.StrikeDuration)) ||
		first.CaseID == nil || *first.CaseID != caseID || first.CreatedBy == nil || *first.CreatedBy != admin || first.VoidedAt != nil {
		t.Fatalf("stored strike: %+v", first)
	}

	// Same key, different reason and severity, another admin: the FIRST
	// strike comes back and nothing is written.
	replay := in
	replay.Reason, replay.Severity = "retry", "severe_strike"
	second, created, err := svc.IssueStrike(ctx, replay, adminMeta(uuid.New(), "retry"))
	if err != nil || created || second.ID != first.ID || second.Severity != "strike" || second.Reason != "copyright upheld" {
		t.Fatalf("replay: created=%v err=%v got=%+v", created, err, second)
	}
	if n := strikeRowCount(t, pool, user); n != 1 {
		t.Fatalf("strike rows=%d, want 1", n)
	}
	audit := auditRows(t, postgres.New(pool), postgres.AuditTargetStrike, first.ID)
	if len(audit) != 1 || audit[0].Action != postgres.AuditStrikeIssued || audit[0].ActorUserID == nil || *audit[0].ActorUserID != admin ||
		audit[0].NewStatus == nil || *audit[0].NewStatus != "active" || audit[0].Reason == nil || *audit[0].Reason != "copyright upheld" {
		t.Fatalf("audit=%+v, want one strike.issued by %s", audit, admin)
	}
	ob := outboxRowsFor(t, pool, user.String())
	if len(ob) != 1 || ob[0].EventType != events.StrikeIssued || ob[0].Topic != "trust.test.v1" || ob[0].Published {
		t.Fatalf("outbox=%+v, want one unpublished StrikeIssued on trust.test.v1", ob)
	}
	var env events.EventEnvelope
	if err := json.Unmarshal(ob[0].Payload, &env); err != nil || env.EventType != events.StrikeIssued || env.EventID == "" {
		t.Fatalf("envelope: %s err=%v", ob[0].Payload, err)
	}
	var p events.StrikeIssuedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil || p.StrikeID != first.ID.String() || p.UserID != user.String() ||
		p.Severity != "strike" || p.CaseID == nil || *p.CaseID != caseID.String() || p.PolicyVersion != postgres.StrikePolicyVersion ||
		!p.IssuedAt.Equal(first.CreatedAt) || !p.ExpiresAt.Equal(first.ExpiresAt) {
		t.Fatalf("payload=%+v err=%v", p, err)
	}

	// A different key for the same user is a second strike.
	other := in
	other.IdempotencyKey = "manual:" + uuid.NewString()
	if _, created, err := svc.IssueStrike(ctx, other, adminMeta(admin, "second")); err != nil || !created {
		t.Fatalf("second key: created=%v err=%v", created, err)
	}
	active, err := extras.GetActiveStrikes(ctx, user)
	if err != nil || len(active) != 2 {
		t.Fatalf("active=%d err=%v", len(active), err)
	}
}

// Ten concurrent issues with one key give one strike, one audit row and
// one outbox row (T5-1's "five retried approvals give one strike").
func TestStrikeIssue_ConcurrentSameKeyIsOneStrike(t *testing.T) {
	pool, svc, _ := newStrikeRig(t)
	user, admin := uuid.New(), uuid.New()
	in := IssueStrikeInput{UserID: user, Reason: "spam", Severity: "strike", IdempotencyKey: "concurrent:" + uuid.NewString()}
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		ids     = map[uuid.UUID]int{}
		creates int
	)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, created, err := svc.IssueStrike(context.Background(), in, adminMeta(admin, "spam"))
			if err != nil {
				t.Errorf("issue: %v", err)
				return
			}
			mu.Lock()
			ids[st.ID]++
			if created {
				creates++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(ids) != 1 || creates != 1 || strikeRowCount(t, pool, user) != 1 {
		t.Fatalf("ids=%v creates=%d rows=%d, want one strike created once", ids, creates, strikeRowCount(t, pool, user))
	}
	for id := range ids {
		if n := len(auditRows(t, postgres.New(pool), postgres.AuditTargetStrike, id)); n != 1 {
			t.Fatalf("audit rows=%d, want 1", n)
		}
	}
	if ob := outboxRowsFor(t, pool, user.String()); len(ob) != 1 {
		t.Fatalf("outbox rows=%d, want 1", len(ob))
	}
}

// The strike, its audit row and its outbox row share one transaction: when
// the audit insert fails, no strike and no event exist.
func TestStrikeIssue_AuditFailureRollsBackStrikeAndOutbox(t *testing.T) {
	pool, svc, _ := newStrikeRig(t)
	installAuditFailure(t, pool)
	user := uuid.New()
	in := IssueStrikeInput{UserID: user, Reason: "spam", Severity: "strike", IdempotencyKey: "rollback:" + uuid.NewString()}
	if _, _, err := svc.IssueStrike(context.Background(), in, adminMeta(uuid.New(), injectAuditFailure)); err == nil || !strings.Contains(err.Error(), "injected audit failure") {
		t.Fatalf("err=%v, want the injected audit failure", err)
	}
	if strikeRowCount(t, pool, user) != 0 || len(outboxRowsFor(t, pool, user.String())) != 0 {
		t.Fatal("a failed audit insert must leave no strike and no outbox row")
	}
	// The key is free again: the retry succeeds and writes everything once.
	if _, created, err := svc.IssueStrike(context.Background(), in, adminMeta(uuid.New(), "retry")); err != nil || !created {
		t.Fatalf("retry: created=%v err=%v", created, err)
	}
	if strikeRowCount(t, pool, user) != 1 || len(outboxRowsFor(t, pool, user.String())) != 1 {
		t.Fatal("retry must write exactly one strike and one outbox row")
	}
}

func TestStrikeVoid_AuditedOutboxedReplaySafeNeverDeleted(t *testing.T) {
	pool, svc, extras := newStrikeRig(t)
	ctx := context.Background()
	user, admin, voider := uuid.New(), uuid.New(), uuid.New()
	st, _, err := svc.IssueStrike(ctx, IssueStrikeInput{UserID: user, Reason: "spam", Severity: "severe_strike", IdempotencyKey: "void:" + uuid.NewString()}, adminMeta(admin, "spam"))
	if err != nil {
		t.Fatal(err)
	}
	// Wrong user → not found, nothing written.
	if _, _, err := svc.VoidStrike(ctx, uuid.New(), st.ID, "claim withdrawn", adminMeta(voider, "")); !errors.Is(err, postgres.ErrStrikeNotFound) {
		t.Fatalf("void with the wrong user: err=%v, want ErrStrikeNotFound", err)
	}
	if _, _, err := svc.VoidStrike(ctx, user, uuid.New(), "claim withdrawn", adminMeta(voider, "")); !errors.Is(err, postgres.ErrStrikeNotFound) {
		t.Fatalf("void of an unknown strike: err=%v, want ErrStrikeNotFound", err)
	}
	// A service actor cannot void (voided_by must be a person).
	if _, _, err := svc.VoidStrike(ctx, user, st.ID, "claim withdrawn", postgres.AuditMeta{Actor: postgres.ServiceActor("post-service")}); !errors.Is(err, ErrActorRequired) {
		t.Fatalf("service actor void: err=%v, want ErrActorRequired", err)
	}
	if _, _, err := svc.VoidStrike(ctx, user, st.ID, "   ", adminMeta(voider, "")); !errors.Is(err, ErrInvalidStrike) {
		t.Fatalf("blank reason: err=%v, want ErrInvalidStrike", err)
	}
	if n := len(auditRows(t, postgres.New(pool), postgres.AuditTargetStrike, st.ID)); n != 1 {
		t.Fatalf("refusals wrote audit rows: %d", n)
	}

	voided, changed, err := svc.VoidStrike(ctx, user, st.ID, "claim withdrawn", adminMeta(voider, "claim withdrawn"))
	if err != nil || !changed || voided.VoidedAt == nil || voided.VoidReason == nil || *voided.VoidReason != "claim withdrawn" ||
		voided.VoidedBy == nil || *voided.VoidedBy != voider {
		t.Fatalf("void: changed=%v err=%v got=%+v", changed, err, voided)
	}
	// Replay: no-op, same row, nothing new written.
	again, changed, err := svc.VoidStrike(ctx, user, st.ID, "another reason", adminMeta(uuid.New(), "another reason"))
	if err != nil || changed || !again.VoidedAt.Equal(*voided.VoidedAt) || *again.VoidReason != "claim withdrawn" || *again.VoidedBy != voider {
		t.Fatalf("void replay: changed=%v err=%v got=%+v", changed, err, again)
	}
	audit := auditRows(t, postgres.New(pool), postgres.AuditTargetStrike, st.ID)
	if len(audit) != 2 || audit[0].Action != postgres.AuditStrikeIssued || audit[1].Action != postgres.AuditStrikeVoided ||
		*audit[1].ActorUserID != voider || *audit[1].PrevStatus != "active" || *audit[1].NewStatus != "voided" || *audit[1].NewResolution != "claim withdrawn" {
		t.Fatalf("audit=%+v, want strike.issued then strike.voided by %s", audit, voider)
	}
	ob := outboxRowsFor(t, pool, user.String())
	if len(ob) != 2 || ob[1].EventType != events.StrikeVoided {
		t.Fatalf("outbox=%+v, want StrikeIssued then StrikeVoided", ob)
	}
	var env events.EventEnvelope
	var p events.StrikeVoidedPayload
	if err := json.Unmarshal(ob[1].Payload, &env); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(env.Payload, &p); err != nil || p.StrikeID != st.ID.String() || p.VoidReason != "claim withdrawn" || !p.VoidedAt.Equal(*voided.VoidedAt) || p.Severity != "severe_strike" {
		t.Fatalf("void payload=%+v err=%v", p, err)
	}
	// Never deleted, never active.
	if strikeRowCount(t, pool, user) != 1 {
		t.Fatal("void must not delete")
	}
	if active, err := extras.GetActiveStrikes(ctx, user); err != nil || len(active) != 0 {
		t.Fatalf("active after void=%d err=%v, want 0 (never nil)", len(active), err)
	}
	if got, err := extras.GetStrike(ctx, st.ID); err != nil || got.VoidedAt == nil {
		t.Fatalf("GetStrike after void: %+v err=%v", got, err)
	}
	// Stats do not count it.
	stats, err := postgres.New(pool).AdminStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var live int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM trust.user_strikes WHERE created_at >= NOW() - interval '7 days' AND voided_at IS NULL`).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if stats.StrikesLast7Days != live {
		t.Fatalf("stats strikes=%d, want %d (voided excluded)", stats.StrikesLast7Days, live)
	}
}

// Expired and voided strikes are excluded by the store, in both the active
// list and the standing snapshot.
func TestStrikes_ActiveExcludesExpiredAndVoided(t *testing.T) {
	pool, _, extras := newStrikeRig(t)
	ctx := context.Background()
	user := uuid.New()
	seed := func(severity string, issuedAgo, lifetime time.Duration, voided bool) uuid.UUID {
		id := uuid.New()
		var voidedAt *time.Time
		var reason *string
		var by *uuid.UUID
		if voided {
			now, r, b := time.Now(), "duplicate", uuid.New()
			voidedAt, reason, by = &now, &r, &b
		}
		if _, err := pool.Exec(ctx, `INSERT INTO trust.user_strikes (id, user_id, reason, severity, created_at, expires_at, voided_at, void_reason, voided_by)
			VALUES ($1, $2, 'seeded', $3, $4, $5, $6, $7, $8)`, id, user, severity, time.Now().Add(-issuedAgo), time.Now().Add(-issuedAgo).Add(lifetime), voidedAt, reason, by); err != nil {
			t.Fatal(err)
		}
		return id
	}
	live := seed("strike", time.Hour, postgres.StrikeDuration, false)
	seed("strike", 91*24*time.Hour, postgres.StrikeDuration, false) // expired
	seed("severe_strike", time.Hour, postgres.StrikeDuration, true) // voided
	active, err := extras.GetActiveStrikes(ctx, user)
	if err != nil || len(active) != 1 || active[0].ID != live {
		t.Fatalf("active=%+v err=%v, want only %s", active, err, live)
	}
	if n, err := extras.CountActiveStrikes(ctx, user); err != nil || n != 1 {
		t.Fatalf("count=%d err=%v", n, err)
	}
	snap, err := extras.StandingSnapshot(ctx, user, time.Now())
	if err != nil || len(snap.ActiveStrikes) != 1 || snap.ActiveStrikes[0].ID != live || snap.SuspendedUntil != nil {
		t.Fatalf("snapshot=%+v err=%v", snap, err)
	}
	// Void shape is enforced by the database, not only by the store.
	if _, err := pool.Exec(ctx, `UPDATE trust.user_strikes SET voided_at = NOW() WHERE id = $1`, live); err == nil {
		t.Fatal("a void without reason and actor must be refused by user_strikes_void_shape")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO trust.user_strikes (user_id, reason, severity) VALUES ($1, 'x', 'strike')`, user); err == nil {
		t.Fatal("expires_at must be NOT NULL")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO trust.user_strikes (user_id, reason, severity, expires_at, created_at) VALUES ($1, 'x', 'strike', NOW() - interval '1 day', NOW())`, user); err == nil {
		t.Fatal("expires_at must be after created_at")
	}
}

// The standing policy over real rows (T4-3): the store filter and
// EvaluateStanding together.
func TestStanding_EndToEndOverRows(t *testing.T) {
	pool, svc, _ := newStrikeRig(t)
	ctx := context.Background()
	day := 24 * time.Hour
	seed := func(user uuid.UUID, severity string, issuedAgo time.Duration) uuid.UUID {
		id := uuid.New()
		if _, err := pool.Exec(ctx, `INSERT INTO trust.user_strikes (id, user_id, reason, severity, created_at, expires_at)
			VALUES ($1, $2, 'seeded', $3, $4::timestamptz, $4::timestamptz + interval '90 days')`, id, user, severity, time.Now().Add(-issuedAgo)); err != nil {
			t.Fatal(err)
		}
		return id
	}
	void := func(id uuid.UUID) {
		if _, err := pool.Exec(ctx, `UPDATE trust.user_strikes SET voided_at = NOW(), void_reason = 'test', voided_by = $2 WHERE id = $1`, id, uuid.New()); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name      string
		seed      func(user uuid.UUID)
		want      string
		wantCount int
		suspended bool
	}{
		{"no rows at all", func(uuid.UUID) {}, StandingOK, 0, false},
		{"two strikes", func(u uuid.UUID) { seed(u, "strike", day); seed(u, "strike", 2*day) }, StandingOK, 2, false},
		{"three strikes", func(u uuid.UUID) { seed(u, "strike", day); seed(u, "strike", 2*day); seed(u, "strike", 3*day) }, StandingSuspended, 3, true},
		{"three strikes, one voided", func(u uuid.UUID) { seed(u, "strike", day); seed(u, "strike", 2*day); void(seed(u, "strike", 3*day)) }, StandingOK, 2, false},
		{"three strikes, one expired", func(u uuid.UUID) { seed(u, "strike", day); seed(u, "strike", 2*day); seed(u, "strike", 91*day) }, StandingOK, 2, false},
		{"one severe", func(u uuid.UUID) { seed(u, "severe_strike", day) }, StandingSuspended, 1, true},
		{"one severe voided", func(u uuid.UUID) { void(seed(u, "severe_strike", day)) }, StandingOK, 0, false},
		{"warnings only", func(u uuid.UUID) { seed(u, "warning", day); seed(u, "warning", 2*day); seed(u, "warning", 3*day) }, StandingOK, 3, false},
		{"suspended_until in the future, no strikes", func(u uuid.UUID) {
			if _, err := pool.Exec(ctx, `INSERT INTO trust.user_trust_state (user_id, suspended_until) VALUES ($1, NOW() + interval '3 days')`, u); err != nil {
				t.Fatal(err)
			}
		}, StandingSuspended, 0, true},
		{"suspended_until in the past", func(u uuid.UUID) {
			if _, err := pool.Exec(ctx, `INSERT INTO trust.user_trust_state (user_id, suspended_until) VALUES ($1, NOW() - interval '3 days')`, u); err != nil {
				t.Fatal(err)
			}
		}, StandingOK, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			user := uuid.New()
			tc.seed(user)
			got, err := svc.Standing(ctx, user)
			if err != nil {
				t.Fatal(err)
			}
			if got.Standing != tc.want || len(got.ActiveStrikes) != tc.wantCount || (got.SuspendedUntil != nil) != tc.suspended {
				t.Fatalf("standing=%s strikes=%d suspended_until=%v, want %s/%d/%v", got.Standing, len(got.ActiveStrikes), got.SuspendedUntil, tc.want, tc.wantCount, tc.suspended)
			}
			if tc.suspended && !got.SuspendedUntil.After(time.Now()) {
				t.Fatalf("suspended_until=%v is not in the future", got.SuspendedUntil)
			}
		})
	}
}

// Migration 011 is re-runnable and backfills legacy rows (F-15): a strike
// with no expiry gets created_at + 90 days and the legacy policy marker;
// rows that already had an expiry are untouched; a second run changes
// nothing and NOT NULL holds.
func TestMigration011_IdempotentAndBackfillsLegacyStrikes(t *testing.T) {
	pool, _, extras := newStrikeRig(t)
	ctx := context.Background()
	raw, err := database.Migrations.ReadFile("migrations/011_strike_lifecycle_outbox.sql")
	if err != nil {
		t.Fatal(err)
	}
	apply := func() {
		t.Helper()
		runLockedDDL(t, pool, string(raw))
	}
	// Simulate the pre-011 world for two rows: NOT NULL dropped, no expiry.
	runLockedDDL(t, pool, `ALTER TABLE trust.user_strikes ALTER COLUMN expires_at DROP NOT NULL`)
	legacy1, legacy2, modern := uuid.New(), uuid.New(), uuid.New()
	issued := time.Date(2026, 1, 15, 8, 0, 0, 0, time.UTC)
	for _, id := range []uuid.UUID{legacy1, legacy2} {
		if _, err := pool.Exec(ctx, `INSERT INTO trust.user_strikes (id, user_id, reason, severity, created_at, expires_at) VALUES ($1, $2, 'legacy', 'strike', $3, NULL)`, id, uuid.New(), issued); err != nil {
			t.Fatal(err)
		}
	}
	modernExpiry := time.Now().Add(30 * 24 * time.Hour).Truncate(time.Microsecond)
	if _, err := pool.Exec(ctx, `INSERT INTO trust.user_strikes (id, user_id, reason, severity, created_at, expires_at) VALUES ($1, $2, 'modern', 'strike', NOW(), $3)`, modern, uuid.New(), modernExpiry); err != nil {
		t.Fatal(err)
	}
	before, err := extras.CountLegacyBackfilledStrikes(ctx)
	if err != nil {
		t.Fatal(err)
	}

	apply()
	after, err := extras.CountLegacyBackfilledStrikes(ctx)
	if err != nil || after-before != 2 {
		t.Fatalf("legacy count moved by %d (err=%v), want 2", after-before, err)
	}
	for _, id := range []uuid.UUID{legacy1, legacy2} {
		st, err := extras.GetStrike(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if !st.ExpiresAt.Equal(issued.Add(90*24*time.Hour)) || st.PolicyVersion != postgres.LegacyStrikePolicyVersion {
			t.Fatalf("legacy row %s: expires_at=%v policy=%q, want %v / %s", id, st.ExpiresAt, st.PolicyVersion, issued.Add(90*24*time.Hour), postgres.LegacyStrikePolicyVersion)
		}
	}
	if st, err := extras.GetStrike(ctx, modern); err != nil || !st.ExpiresAt.Equal(modernExpiry) || st.PolicyVersion != postgres.StrikePolicyVersion {
		t.Fatalf("modern row touched: %+v err=%v", st, err)
	}
	var notNull bool
	if err := pool.QueryRow(ctx, `SELECT attnotnull FROM pg_attribute WHERE attrelid = 'trust.user_strikes'::regclass AND attname = 'expires_at'`).Scan(&notNull); err != nil || !notNull {
		t.Fatalf("expires_at NOT NULL=%v err=%v", notNull, err)
	}

	// Second run: nothing changes.
	snapshot := func() string {
		var s string
		if err := pool.QueryRow(ctx, `SELECT string_agg(id::text || ':' || expires_at::text || ':' || policy_version, ',' ORDER BY id)
			FROM trust.user_strikes WHERE id = ANY($1)`, []uuid.UUID{legacy1, legacy2, modern}).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	first := snapshot()
	apply()
	if second := snapshot(); second != first || mustCount(t, extras) != after {
		t.Fatalf("second run changed rows:\n%s\n%s", first, second)
	}
	// The audit CHECK now admits 'strike' (and, since 013, 'copyright_case')
	// and still refuses anything else.
	if _, err := pool.Exec(ctx, `INSERT INTO trust.admin_audit (actor_type, actor_service, action, target_type, target_id) VALUES ('service', 't', 'x', 'not_a_target', $1)`, uuid.New()); err == nil {
		t.Fatal("an unknown audit target type must be refused")
	}
}

func mustCount(t *testing.T, extras *postgres.TrustExtrasStore) int64 {
	t.Helper()
	n, err := extras.CountLegacyBackfilledStrikes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// Purge writes its ack into the outbox in the erase transaction, and the
// erase is idempotent (a redelivery acks again).
func TestPurgeUserAndAck_AckRowInEraseTransaction(t *testing.T) {
	pool, svc, _ := newStrikeRig(t)
	ctx := context.Background()
	store := postgres.New(pool)
	user := uuid.New()
	if _, _, err := svc.IssueStrike(ctx, IssueStrikeInput{UserID: user, Reason: "spam", Severity: "strike", IdempotencyKey: "purge:" + uuid.NewString()}, adminMeta(uuid.New(), "spam")); err != nil {
		t.Fatal(err)
	}
	ack := []byte(`{"user_id":"` + user.String() + `","service":"trust-safety","purged_at":"2026-09-29T00:00:00Z"}`)
	if err := store.PurgeUserAndAck(ctx, user, "platform.purge-acks.test", ack); err != nil {
		t.Fatal(err)
	}
	if strikeRowCount(t, pool, user) != 0 {
		t.Fatal("purge must erase the user's strikes")
	}
	rows := outboxRowsFor(t, pool, user.String())
	var acks int
	for _, r := range rows {
		if r.EventType == postgres.PurgeAckEventType {
			acks++
			// JSONB normalises key order, so compare the parsed values.
			var want, got map[string]any
			if err := json.Unmarshal(ack, &want); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(r.Payload, &got); err != nil {
				t.Fatal(err)
			}
			if r.Topic != "platform.purge-acks.test" || r.Published || len(got) != len(want) ||
				got["user_id"] != want["user_id"] || got["service"] != want["service"] || got["purged_at"] != want["purged_at"] {
				t.Fatalf("ack row=%+v payload=%s", r, r.Payload)
			}
		}
	}
	if acks != 1 {
		t.Fatalf("ack rows=%d, want 1", acks)
	}
	if err := store.PurgeUserAndAck(ctx, user, "platform.purge-acks.test", ack); err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if err := store.PurgeUserAndAck(ctx, user, "", ack); err == nil {
		t.Fatal("a blank topic must be refused")
	}
	// Payload must stay the bare ack auth-service parses (not enveloped).
	var parsed struct {
		UserID  string `json:"user_id"`
		Service string `json:"service"`
	}
	if err := json.Unmarshal(rows[len(rows)-1].Payload, &parsed); err != nil || parsed.UserID != user.String() || parsed.Service != "trust-safety" {
		t.Fatalf("ack payload=%s err=%v", rows[len(rows)-1].Payload, err)
	}
}

// The outbox store's failure bookkeeping: a failure counts an attempt,
// keeps the row unpublished, records the error and (when asked) holds the
// row back; a publish marks it once.
func TestOutboxStore_FailureAndPublishBookkeeping(t *testing.T) {
	pool, svc, _ := newStrikeRig(t)
	ctx := context.Background()
	ob := postgres.NewOutboxStore(pool)
	user := uuid.New()
	if _, _, err := svc.IssueStrike(ctx, IssueStrikeInput{UserID: user, Reason: "spam", Severity: "strike", IdempotencyKey: "ob:" + uuid.NewString()}, adminMeta(uuid.New(), "spam")); err != nil {
		t.Fatal(err)
	}
	row := outboxRowsFor(t, pool, user.String())[0]
	pending := func() bool {
		rows, err := ob.PendingOutbox(ctx, 500)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if r.ID == row.ID {
				return true
			}
		}
		return false
	}
	if !pending() {
		t.Fatal("a fresh row must be pending")
	}
	if err := ob.RecordOutboxFailure(ctx, row.ID, errors.New("broker down"), 0); err != nil {
		t.Fatal(err)
	}
	if got := outboxRowsFor(t, pool, user.String())[0]; got.Attempts != 1 || got.Published || got.LastError == nil || *got.LastError != "broker down" || !pending() {
		t.Fatalf("after failure: %+v pending=%v", got, pending())
	}
	if err := ob.RecordOutboxFailure(ctx, row.ID, errors.New("still down"), time.Hour); err != nil {
		t.Fatal(err)
	}
	if pending() {
		t.Fatal("a row held back for an hour must not be pending")
	}
	if err := ob.MarkOutboxPublished(ctx, row.ID); err != nil {
		t.Fatal(err)
	}
	got := outboxRowsFor(t, pool, user.String())[0]
	if !got.Published || got.Attempts != 3 || got.LastError != nil {
		t.Fatalf("after publish: %+v", got)
	}
	if err := ob.MarkOutboxPublished(ctx, 0); err == nil {
		t.Fatal("marking an unknown row must fail")
	}
	if age, err := ob.OldestPendingAge(ctx); err != nil || age < 0 {
		t.Fatalf("oldest pending age=%v err=%v", age, err)
	}
}
