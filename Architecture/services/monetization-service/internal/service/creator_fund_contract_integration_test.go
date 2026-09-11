//go:build integration

package service

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/buildinfo"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// The analytics boundary has a name (plan Phase 5C, audit M-15)
// ---------------------------------------------------------------------------
//
// monetization-service prices a day from analytics rows it reads over a
// shared DSN. Until 5C it named analytics.content_daily_summary directly,
// so a column change in analytics broke settlement with nothing saying
// so. The contract is now the view analytics.v_creator_daily_metrics_v1,
// and the accrual must read that and nothing else in the analytics
// schema.
//
// How this is asserted, and why. Renaming the table inside a transaction
// does not work here: the accrual runs on its own pool connections, which
// would not see an uncommitted rename and would block on its lock; a
// committed rename on the shared scratch database would race the http
// package's tests. And a view binds to its table by OID, so a rename
// would prove a PostgreSQL property, not ours. What we actually want to
// know is which relations the accrual NAMES, so the test records every
// statement the accrual executes, through pgx's query tracer on the very
// pool the service runs on, and reads the relation names off them. That
// is the executed SQL, not the source text, and not a fake.

// sqlRecorder is a pgx.QueryTracer that keeps every statement text.
type sqlRecorder struct {
	mu    sync.Mutex
	stmts []string
}

func (r *sqlRecorder) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	r.mu.Lock()
	r.stmts = append(r.stmts, data.SQL)
	r.mu.Unlock()
	return ctx
}

func (r *sqlRecorder) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (r *sqlRecorder) statements() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.stmts...)
}

var analyticsRelation = regexp.MustCompile(`(?i)\banalytics\.([a-z_0-9]+)`)

// analyticsRelations returns every analytics.<name> the recorded
// statements reference, de-duplicated, in first-seen order.
func analyticsRelations(stmts []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range stmts {
		for _, m := range analyticsRelation.FindAllStringSubmatch(s, -1) {
			name := strings.ToLower(m[1])
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	return out
}

// tracedPool opens a pool on the scratch database with every statement
// recorded.
func tracedPool(ctx context.Context, t *testing.T, dsn string) (*pgxpool.Pool, *sqlRecorder) {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	rec := &sqlRecorder{}
	cfg.ConnConfig.Tracer = rec
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool, rec
}

// accrualFixture seeds one eligible creator with one flick day of views
// and a rate/band window around it, removing everything on cleanup.
func accrualFixture(ctx context.Context, t *testing.T, pool *pgxpool.Pool, day time.Time) (creator uuid.UUID) {
	t.Helper()
	seedRateAndBandFor(ctx, t, pool, "flick", 300, day, false)
	creator, content := uuid.New(), uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM creator_fund_earnings WHERE creator_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM creator_fund_carry WHERE creator_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM creator_fund_eligibility WHERE creator_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM analytics.content_daily_summary WHERE creator_id = $1`, creator)
	})
	makeEligible(ctx, t, pool, creator)
	insertDailySummary(ctx, t, pool, content, creator, day, "flick", 1_000, 5_000, 0.4)
	return creator
}

func TestAccrualReadsOnlyTheContractView(t *testing.T) {
	dsn := requireTestDSN(t)
	ctx := context.Background()
	// Fixtures go in through a plain pool so their INSERTs into the
	// underlying table are not mistaken for the accrual's reads.
	plain, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(plain.Close)
	day := time.Date(2025, 8, 3, 0, 0, 0, 0, time.UTC)
	creator := accrualFixture(ctx, t, plain, day)

	traced, rec := tracedPool(ctx, t, dsn)
	svc := New(postgres.New(traced), nil)
	res, err := svc.AccrueCreatorFundDay(ctx, creator, day)
	if err != nil {
		t.Fatalf("accrue: %v", err)
	}
	if res.Accrued == 0 {
		t.Fatalf("setup: nothing accrued: %+v", res)
	}

	rels := analyticsRelations(rec.statements())
	if len(rels) == 0 {
		t.Fatal("the accrual executed no statement naming an analytics relation; the recorder is not seeing the service's queries")
	}
	for _, r := range rels {
		if r != "v_creator_daily_metrics_v1" {
			t.Errorf("the accrual read analytics.%s directly; the only analytics relation it may name is the contract view v_creator_daily_metrics_v1 (relations seen: %v)", r, rels)
		}
	}
	t.Logf("analytics relations named by the accrual: %v", rels)
}

// Every accrual row names the build that wrote it, beside the rule that
// priced it. The value comes from internal/buildinfo, which the release
// path stamps at link time.
func TestAccrualStampsBuildSHA(t *testing.T) {
	dsn := requireTestDSN(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	day := time.Date(2025, 8, 5, 0, 0, 0, 0, time.UTC)
	creator := accrualFixture(ctx, t, pool, day)

	const stamped = "5c-test-build-0123abcd"
	prev := buildinfo.SHA
	buildinfo.SHA = stamped
	t.Cleanup(func() { buildinfo.SHA = prev })

	svc := New(postgres.New(pool), nil)
	if _, err := svc.AccrueCreatorFundDay(ctx, creator, day); err != nil {
		t.Fatalf("accrue: %v", err)
	}

	var buildSHA, ruleVersion *string
	if err := pool.QueryRow(ctx, `
		SELECT build_sha, rule_version FROM creator_fund_earnings
		WHERE creator_id = $1 AND day_bucket = $2 AND content_type = 'flick'`,
		creator, day).Scan(&buildSHA, &ruleVersion); err != nil {
		t.Fatalf("read the accrual row back: %v", err)
	}
	if buildSHA == nil || *buildSHA != stamped {
		t.Fatalf("build_sha = %v, want %q stamped from buildinfo.SHA", buildSHA, stamped)
	}
	if ruleVersion == nil || *ruleVersion != RuleVersion {
		t.Fatalf("rule_version = %v, want %q beside the build sha", ruleVersion, RuleVersion)
	}
}
