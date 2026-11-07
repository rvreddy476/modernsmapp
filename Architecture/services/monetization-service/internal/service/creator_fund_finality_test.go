package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Review 2026-11-07: a day is accrued only once analytics treats it as
// final, a period is settled only once all its days are, and neither
// waiting is a failure. These run without a database: the Service below
// has no store, so any path that reached one would panic.

func finalBefore(y int, m time.Month, d int) analyticsFinality {
	return analyticsFinality{before: time.Date(y, m, d, 0, 0, 0, 0, time.UTC), known: true}
}

func TestAnalyticsFinalityDayAndPeriod(t *testing.T) {
	fin := finalBefore(2026, 11, 5)
	if !fin.dayFinal(time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)) {
		t.Error("the day before final_before must be final")
	}
	if !fin.dayFinal(time.Date(2026, 11, 4, 23, 59, 0, 0, time.UTC)) {
		t.Error("any instant of a final day is final")
	}
	if fin.dayFinal(time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)) {
		t.Error("final_before itself is still open")
	}
	if (analyticsFinality{}).dayFinal(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Error("unknown finality must finalise nothing, however old the day")
	}

	oct := MonthPeriod(2026, time.October) // ends (exclusive) 1 Nov
	if !finalBefore(2026, 11, 1).periodFinal(oct) {
		t.Error("a period whose last day is the last final day is final")
	}
	if finalBefore(2026, 10, 31).periodFinal(oct) {
		t.Error("a period with an open last day must wait")
	}
	if (analyticsFinality{}).periodFinal(oct) {
		t.Error("unknown finality must finalise no period")
	}
}

func TestAccrueDaySkipsANonFinalDayWithoutReadingAnything(t *testing.T) {
	s := &Service{} // no store: reaching one panics
	day := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	for _, fin := range []analyticsFinality{finalBefore(2026, 11, 5), {}} {
		res, err := s.accrueDay(context.Background(), uuid.New(), day, fin)
		if err != nil {
			t.Fatalf("a non-final day is not an error: %v", err)
		}
		if !res.NotFinal || res.Skipped[SkipDayNotFinal] != 1 || res.Accrued != 0 {
			t.Fatalf("non-final day = %+v, want NotFinal with day_not_final=1 and nothing accrued", res)
		}
	}
}

func TestBatchAccrualSkipsANonFinalDayWithoutWalkingCreators(t *testing.T) {
	s := &Service{}
	b, err := s.accrueDayForAllEligible(context.Background(), time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC), finalBefore(2026, 11, 5), nil)
	if err != nil || !b.NotFinal || b.Creators != 0 {
		t.Fatalf("batch over a non-final day = %+v, %v; want NotFinal, nobody walked", b, err)
	}
}

func TestSettlementWaitsForEveryDayToBeFinal(t *testing.T) {
	s := &Service{}
	creator := uuid.New()
	oct := MonthPeriod(2026, time.October)

	st, err := s.settlePeriodWithFinality(context.Background(), creator, oct, finalBefore(2026, 10, 31), true)
	if err != nil {
		t.Fatalf("a period that is not final must wait, not fail: %v", err)
	}
	if st.Status != StatusAwaitingFinalAnalytics || st.Persisted || st.NewlyCredited || st.CreditedPaise != 0 {
		t.Fatalf("waiting statement = %+v", st)
	}
	if !strings.Contains(st.Explanation, "not settled yet") {
		t.Errorf("the statement must say why it waits: %q", st.Explanation)
	}

	res, err := s.settleAllWithFinality(context.Background(), oct, finalBefore(2026, 10, 31), nil)
	if err != nil || !res.AwaitingFinalAnalytics || res.Failed != 0 || res.Considered != 0 {
		t.Fatalf("batch over a non-final period = %+v, %v; want AwaitingFinalAnalytics and nothing else", res, err)
	}
}

func TestSettlementHoldReasonHoldsOnlySuspendedCreators(t *testing.T) {
	if settlementHoldReason("suspended") == "" {
		t.Fatal("a suspended creator's period must be held")
	}
	for _, status := range []string{"eligible", "pending", "ineligible", ""} {
		if r := settlementHoldReason(status); r != "" {
			t.Errorf("status %q held (%q): days accrued while eligible stay payable", status, r)
		}
	}
}

func TestExplainStatementSaysAHeldPeriodIsNotLost(t *testing.T) {
	st := &PeriodStatement{PeriodLabel: "October 2026", PendingPaise: 12_345, HoldReason: "creator_fund_suspended"}
	got := ExplainStatement(st)
	if !strings.Contains(got, "on hold") || !strings.Contains(got, "not lost") {
		t.Fatalf("held explanation = %q", got)
	}
}

func TestRevisionDriftPricesTheDifferenceAtTheRowsOwnRate(t *testing.T) {
	rows := func(views int64) []postgres.DailyInputRow {
		return []postgres.DailyInputRow{{
			ContentID: uuid.MustParse("00000000-0000-0000-0000-000000000001"), ContentType: "flick",
			DayBucket: time.Date(2025, 9, 9, 0, 0, 0, 0, time.UTC), ViewsDisplay: views, Impressions: 20_000, CQS: 0.4,
		}}
	}
	group := func(views int64) dailyInputGroup {
		g, _ := AggregateDailyInputs(rows(views))
		return g[0]
	}
	recorded := group(5_000)
	row := &postgres.CreatorFundEarning{
		ID: uuid.New(), CreatorID: uuid.New(), DayBucket: time.Date(2025, 9, 9, 0, 0, 0, 0, time.UTC), ContentType: "flick",
		RegionCode: "IN", ViewCount: 5_000, RpmPaise: 300, QualityMultiplierBps: NeutralMultiplierBps,
		Status: "settled", InputRevision: recorded.Revision,
	}

	if revisionDrift(nil, recorded) != nil {
		t.Error("no row, no drift")
	}
	if revisionDrift(row, recorded) != nil {
		t.Error("the same inputs are not a drift")
	}
	reversed := *row
	reversed.Status = "reversed"
	if revisionDrift(&reversed, group(5_001)) != nil {
		t.Error("a reversed row was corrected by hand and is exempt")
	}

	up := revisionDrift(row, group(5_001))
	if up == nil || up.DeltaGrossMicroPaise != 300_000 || up.ObservedViews != 5_001 || up.RecordedViews != 5_000 {
		t.Fatalf("one more view at 300 paise per 1000 = %+v, want +300000 micro-paise", up)
	}
	if up.EarningID != row.ID || up.RecordedRevision != row.InputRevision || up.ObservedRevision == row.InputRevision {
		t.Errorf("the adjustment must name the row and both revisions: %+v", up)
	}

	down := revisionDrift(row, group(4_000))
	if down == nil || down.DeltaGrossMicroPaise != -300_000_000 {
		t.Fatalf("1000 fewer views = %+v, want -300000000 micro-paise", down)
	}

	// A row from before the quality multiplier carries 0: priced neutral,
	// never as zero money on both sides.
	legacy := *row
	legacy.QualityMultiplierBps = 0
	if d := revisionDrift(&legacy, group(5_001)); d == nil || d.DeltaGrossMicroPaise != 300_000 {
		t.Fatalf("legacy multiplier drift = %+v", d)
	}

	// A boosted row prices the delta at its own multiplier.
	boosted := *row
	boosted.QualityMultiplierBps = 12_500
	if d := revisionDrift(&boosted, group(5_001)); d == nil || d.DeltaGrossMicroPaise != 375_000 {
		t.Fatalf("1.25x drift = %+v, want +375000", d)
	}
}
