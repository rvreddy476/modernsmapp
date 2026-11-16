package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/membership"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

func utc(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, time.UTC)
}

var testLaunch = utc(2027, time.January, 1, 0, 0)

func mustWindow(t *testing.T, approved time.Time) IntroCommissionWindow {
	t.Helper()
	w, ok := IntroCommissionWindowFor(&approved, testLaunch, 3)
	if !ok {
		t.Fatalf("no window for approval %s", approved)
	}
	return w
}

// The window opens on the UTC day of approval and closes, exclusive, on the
// same day three calendar months later.
func TestIntroWindowEdgesAreUTCAndEndExclusive(t *testing.T) {
	w := mustWindow(t, utc(2027, time.February, 10, 14, 30))
	if !w.Start.Equal(utc(2027, time.February, 10, 0, 0)) || !w.End.Equal(utc(2027, time.May, 10, 0, 0)) {
		t.Fatalf("window = [%s, %s), want [2027-02-10, 2027-05-10)", w.Start, w.End)
	}
	for _, tc := range []struct {
		name string
		at   time.Time
		want bool
	}{
		{"one nanosecond before the start", w.Start.Add(-time.Nanosecond), false},
		{"the start itself", w.Start, true},
		{"the approval moment", utc(2027, time.February, 10, 14, 30), true},
		{"earlier on the approval day", utc(2027, time.February, 10, 0, 1), true},
		{"one nanosecond before the end", w.End.Add(-time.Nanosecond), true},
		{"the end itself", w.End, false},
		{"after the end", w.End.Add(time.Hour), false},
		// 00:00 IST on 10 May is 18:30 UTC on 9 May: still inside in UTC.
		{"IST midnight on the end day", time.Date(2027, time.May, 10, 0, 0, 0, 0, time.FixedZone("IST", 5*3600+1800)), true},
		// 05:29 IST on 10 May is 23:59 UTC on 9 May; 05:30 IST is the end.
		{"IST 05:30 on the end day", time.Date(2027, time.May, 10, 5, 30, 0, 0, time.FixedZone("IST", 5*3600+1800)), false},
	} {
		if got := w.Contains(tc.at); got != tc.want {
			t.Errorf("%s (%s): Contains = %v, want %v", tc.name, tc.at.UTC(), got, tc.want)
		}
	}

	// An approval late in the evening IST is the previous UTC day.
	ist := time.FixedZone("IST", 5*3600+1800)
	approvedIST := time.Date(2027, time.February, 10, 3, 0, 0, 0, ist) // 9 Feb 21:30 UTC
	if got := mustWindow(t, approvedIST).Start; !got.Equal(utc(2027, time.February, 9, 0, 0)) {
		t.Errorf("IST approval start = %s, want the UTC day 2027-02-09", got)
	}
}

// Three calendar months, never spilling into the month after the target.
func TestAddCalendarMonthsCoversShortMonthsToTheirLastDay(t *testing.T) {
	for _, tc := range []struct{ start, want time.Time }{
		{utc(2027, time.January, 15, 0, 0), utc(2027, time.April, 15, 0, 0)},
		{utc(2027, time.November, 30, 0, 0), utc(2028, time.March, 1, 0, 0)}, // no 30 Feb 2028: covers to 29 Feb
		{utc(2026, time.November, 30, 0, 0), utc(2027, time.March, 1, 0, 0)}, // no 30 Feb 2027: covers to 28 Feb
		{utc(2027, time.November, 29, 0, 0), utc(2028, time.February, 29, 0, 0)},
		{utc(2027, time.January, 31, 0, 0), utc(2027, time.May, 1, 0, 0)}, // no 31 Apr
		{utc(2026, time.December, 31, 0, 0), utc(2027, time.March, 31, 0, 0)},
		{utc(2027, time.October, 1, 0, 0), utc(2028, time.January, 1, 0, 0)},
	} {
		if got := AddCalendarMonths(tc.start, 3); !got.Equal(tc.want) {
			t.Errorf("%s + 3 months = %s, want %s", tc.start.Format("2006-01-02"), got.Format("2006-01-02"), tc.want.Format("2006-01-02"))
		}
	}
}

// A creator approved before launch gets their three months from launch;
// one approved on or after launch from their own day.
func TestIntroWindowForPreLaunchApprovalStartsAtLaunch(t *testing.T) {
	for _, tc := range []struct {
		name      string
		approved  time.Time
		wantStart time.Time
		wantEnd   time.Time
	}{
		{"months before launch", utc(2026, time.June, 1, 9, 0), testLaunch, utc(2027, time.April, 1, 0, 0)},
		{"the evening before launch", utc(2026, time.December, 31, 23, 59), testLaunch, utc(2027, time.April, 1, 0, 0)},
		{"launch day", utc(2027, time.January, 1, 12, 0), testLaunch, utc(2027, time.April, 1, 0, 0)},
		{"after launch", utc(2027, time.March, 3, 8, 0), utc(2027, time.March, 3, 0, 0), utc(2027, time.June, 3, 0, 0)},
	} {
		w := mustWindow(t, tc.approved)
		if !w.Start.Equal(tc.wantStart) || !w.End.Equal(tc.wantEnd) {
			t.Errorf("%s: window = [%s, %s), want [%s, %s)", tc.name, w.Start, w.End, tc.wantStart, tc.wantEnd)
		}
	}
	approved := utc(2026, time.June, 1, 9, 0)
	if _, ok := IntroCommissionWindowFor(nil, testLaunch, 3); ok {
		t.Error("a creator never approved has a window")
	}
	if _, ok := IntroCommissionWindowFor(&approved, testLaunch, 0); ok {
		t.Error("months=0 must switch the offer off")
	}
	if _, ok := IntroCommissionWindowFor(&approved, time.Time{}, 3); ok {
		t.Error("no launch date must switch the offer off")
	}
	d := DefaultCreatorFundConfig()
	if d.IntroZeroCommissionMonths != 3 || !d.IntroZeroCommissionLaunch.Equal(DefaultIntroZeroCommissionLaunch()) {
		t.Errorf("default config = %d months from %s", d.IntroZeroCommissionMonths, d.IntroZeroCommissionLaunch)
	}
	// A zero-value config (as hand-built in older tests) has the offer off.
	if (CreatorFundConfig{}).IntroZeroCommissionMonths != 0 {
		t.Error("the zero config must not switch the offer on")
	}
}

func TestDecideCommissionAtTheEdges(t *testing.T) {
	w := mustWindow(t, utc(2027, time.February, 10, 0, 0))
	in := DecideCommission(w.Start, &w, 3000)
	if in.PlatformFeeBps != 0 || in.Reason != CommissionReasonIntroZero || in.WindowStart == nil || !in.WindowEnd.Equal(w.End) {
		t.Errorf("first day: %+v", in)
	}
	last := DecideCommission(w.End.AddDate(0, 0, -1), &w, 3000)
	if last.PlatformFeeBps != 0 || last.Reason != CommissionReasonIntroZero {
		t.Errorf("last day: %+v", last)
	}
	for _, at := range []time.Time{w.End, w.Start.AddDate(0, 0, -1)} {
		out := DecideCommission(at, &w, 3000)
		if out.PlatformFeeBps != 3000 || out.Reason != CommissionReasonStandard || out.WindowStart != nil {
			t.Errorf("%s: %+v, want standard 3000", at, out)
		}
	}
	if none := DecideCommission(w.Start, nil, 3000); none.PlatformFeeBps != 3000 || none.Reason != CommissionReasonStandard {
		t.Errorf("no window: %+v", none)
	}
}

// Creator fund (the ads share): a day inside the window keeps the whole
// gross; the day after the window takes the standard 30%.
func TestIntroCommissionCreatorFundDayIsPricedAtZero(t *testing.T) {
	w := mustWindow(t, utc(2027, time.February, 10, 0, 0))
	gross := int64(12_345) * MicroPaisePerPaise
	in := PriceAccrualDay(gross, 0, nil, DecideCommission(w.End.AddDate(0, 0, -1), &w, 3000).PlatformFeeBps)
	if in.PlatformFeePaise != 0 || in.NetPaise != 12_345 {
		t.Errorf("inside the window: fee %d net %d, want 0 / 12345", in.PlatformFeePaise, in.NetPaise)
	}
	out := PriceAccrualDay(gross, 0, nil, DecideCommission(w.End, &w, 3000).PlatformFeeBps)
	if out.PlatformFeePaise != 3_703 || out.NetPaise != 8_642 {
		t.Errorf("after the window: fee %d net %d, want 3703 / 8642", out.PlatformFeePaise, out.NetPaise)
	}
	// The budget cap is on gross and does not move with the commission.
	remaining := int64(100)
	capped := PriceAccrualDay(gross, 0, &remaining, 0)
	if capped.GrossPaise != 100 || capped.NetPaise != 100 || capped.SkipReason != SkipBudgetCapped {
		t.Errorf("capped intro day = %+v", capped)
	}
}

// The fund line is read back from the rows, per recorded rule; legacy rows
// with no rule are standard at the configured rate.
func TestIntroCommissionCreatorFundLineFromRecordedRows(t *testing.T) {
	w := mustWindow(t, utc(2027, time.March, 20, 0, 0))
	zero, std := int64(0), int64(3000)
	start, end := w.Start, w.End
	groups := []postgres.FundCommissionGroup{
		{Reason: CommissionReasonIntroZero, PlatformFeeBps: &zero, WindowStart: &start, WindowEnd: &end, Rows: 12, GrossPaise: 40_000, NetPaise: 40_000},
		{Reason: CommissionReasonStandard, PlatformFeeBps: &std, Rows: 19, GrossPaise: 60_000, PlatformFeePaise: 18_000, NetPaise: 42_000},
	}
	period := MonthPeriod(2027, time.March)
	line := StreamLine{Stream: "creator_fund"}
	priceFundStream(&line, groups, 3000, &w, period.Start, period.End)
	if line.CommissionReason != CommissionReasonMixed || line.PlatformFeeBps != 3000 || len(line.Commission) != 2 {
		t.Fatalf("mixed line = %+v", line)
	}
	if line.Count != 31 || line.GrossPaise != 100_000 || line.PlatformFeePaise != 18_000 || line.NetPaise != 82_000 {
		t.Errorf("line totals = %+v", line)
	}
	if err := checkCommissionSlices(line); err != nil {
		t.Fatal(err)
	}

	// A period entirely inside: the line says 0% and why.
	inside := StreamLine{Stream: "creator_fund"}
	priceFundStream(&inside, groups[:1], 3000, &w, MonthPeriod(2027, time.April).Start, MonthPeriod(2027, time.April).End)
	if inside.CommissionReason != CommissionReasonIntroZero || inside.PlatformFeeBps != 0 {
		t.Errorf("intro-only line = %s %d", inside.CommissionReason, inside.PlatformFeeBps)
	}

	// Rows from before migration 030 carry no rule.
	legacy := StreamLine{Stream: "creator_fund"}
	priceFundStream(&legacy, []postgres.FundCommissionGroup{{Rows: 3, GrossPaise: 1_000, PlatformFeePaise: 300, NetPaise: 700}}, 3000, nil, period.Start, period.End)
	if legacy.CommissionReason != CommissionReasonStandard || legacy.PlatformFeeBps != 3000 || legacy.Commission[0].Reason != CommissionReasonStandard {
		t.Errorf("legacy line = %+v", legacy)
	}

	// A slice inside the window that took commission is refused.
	bad := line
	bad.Commission = append([]CommissionSlice(nil), line.Commission...)
	bad.Commission[0].PlatformFeePaise, bad.Commission[0].NetPaise = 1, 39_999
	if err := checkCommissionSlices(bad); err == nil {
		t.Error("commission inside the intro window was accepted")
	}
}

// Tips and subscriptions (which carry memberships): split at the window
// edge by event time. Their standard rate is 0 today, so a hypothetical
// 10% proves the waiver applies to them rather than being a coincidence.
func TestIntroCommissionTipsAndSubscriptionsSplitAtTheEdge(t *testing.T) {
	w := mustWindow(t, utc(2027, time.March, 20, 0, 0)) // [20 Mar, 20 Jun)
	period := MonthPeriod(2027, time.June)
	for _, stream := range []string{"tips", "subscriptions"} {
		total := eventStreamTotals{Count: 5, GrossPaise: 50_000}
		intro := eventStreamTotals{Count: 2, GrossPaise: 20_000} // 1-19 June
		line := priceEventStream(stream, total, intro, 1000, &w, period.Start, period.End)
		if line.CommissionReason != CommissionReasonMixed || len(line.Commission) != 2 {
			t.Fatalf("%s: %+v", stream, line)
		}
		if s := line.Commission[0]; s.Reason != CommissionReasonIntroZero || s.PlatformFeePaise != 0 || s.NetPaise != 20_000 || !s.WindowEnd.Equal(w.End) {
			t.Errorf("%s intro slice = %+v", stream, s)
		}
		if s := line.Commission[1]; s.Reason != CommissionReasonStandard || s.PlatformFeePaise != 3_000 || s.Count != 3 {
			t.Errorf("%s standard slice = %+v", stream, s)
		}
		if line.PlatformFeePaise != 3_000 || line.NetPaise != 47_000 {
			t.Errorf("%s line fee %d net %d", stream, line.PlatformFeePaise, line.NetPaise)
		}
		if err := checkCommissionSlices(line); err != nil {
			t.Error(err)
		}

		// Today's rate (0): the money is exactly what it was before the offer.
		today := priceEventStream(stream, total, intro, 0, &w, period.Start, period.End)
		if today.PlatformFeePaise != 0 || today.NetPaise != total.GrossPaise {
			t.Errorf("%s at 0 bps moved money: %+v", stream, today)
		}
		if today.CommissionReason != CommissionReasonMixed {
			t.Errorf("%s at 0 bps reason = %s; the reason is still recorded", stream, today.CommissionReason)
		}

		// Empty line: the rule the period was under.
		empty := priceEventStream(stream, eventStreamTotals{}, eventStreamTotals{}, 0, &w, MonthPeriod(2027, time.April).Start, MonthPeriod(2027, time.April).End)
		if empty.CommissionReason != CommissionReasonIntroZero || len(empty.Commission) != 0 {
			t.Errorf("%s empty in-window line = %+v", stream, empty)
		}
	}
}

// Tips, subscriptions and memberships move money at the moment they happen,
// at 0% today. The statement applies the window to them, but if any of these
// rates is ever raised, the waiver must also be applied where that money is
// moved (SendTip, Subscribe, membership.Split at checkout). This test is the
// tripwire.
func TestIntroCommissionEventStreamsMoveNoCommissionToday(t *testing.T) {
	if tipsPlatformFeeBps != 0 || subscriptionsPlatformFeeBps != 0 || membership.FeeBps != 0 {
		t.Fatalf("a commission on tips (%d), subscriptions (%d) or memberships (%d) was switched on; apply DecideCommission where that money moves before shipping it",
			tipsPlatformFeeBps, subscriptionsPlatformFeeBps, membership.FeeBps)
	}
}

func introStatement(t *testing.T) *PeriodStatement {
	t.Helper()
	w := mustWindow(t, utc(2027, time.March, 20, 0, 0))
	period := MonthPeriod(2027, time.June)
	zero, std := int64(0), int64(3000)
	start, end := w.Start, w.End
	st := &PeriodStatement{
		CreatorID: uuid.MustParse("00000000-0000-0000-0000-0000000000aa"),
		PeriodKey: period.Key, PeriodLabel: period.Label(), PeriodStart: period.Start, PeriodEnd: period.End,
		Fund:            StreamLine{Stream: "creator_fund", CreditedByThisRun: true},
		IntroCommission: introTerms(&w),
	}
	st.IntroCommission.AppliesToPeriod = true
	priceFundStream(&st.Fund, []postgres.FundCommissionGroup{
		{Reason: CommissionReasonIntroZero, PlatformFeeBps: &zero, WindowStart: &start, WindowEnd: &end, Rows: 19, GrossPaise: 19_000, NetPaise: 19_000},
		{Reason: CommissionReasonStandard, PlatformFeeBps: &std, Rows: 11, GrossPaise: 11_000, PlatformFeePaise: 3_300, NetPaise: 7_700},
	}, 3000, &w, period.Start, period.End)
	st.Tips = priceEventStream("tips", eventStreamTotals{2, 5_000}, eventStreamTotals{1, 2_000}, 0, &w, period.Start, period.End)
	st.Subs = priceEventStream("subscriptions", eventStreamTotals{1, 9_900}, eventStreamTotals{}, 0, &w, period.Start, period.End)
	st.GrossPaise = st.Fund.GrossPaise + st.Tips.GrossPaise + st.Subs.GrossPaise
	st.PlatformFeePaise = st.Fund.PlatformFeePaise + st.Tips.PlatformFeePaise + st.Subs.PlatformFeePaise
	st.NetPaise = st.Fund.NetPaise + st.Tips.NetPaise + st.Subs.NetPaise
	st.CreditedPaise = st.Fund.NetPaise
	st.AlreadyCreditedPaise = st.Tips.NetPaise + st.Subs.NetPaise
	st.WalletMovementPaise = st.CreditedPaise
	return st
}

// The statement says what was waived and when, and adds up.
func TestIntroCommissionStatementIsExplainedAndAddsUp(t *testing.T) {
	st := introStatement(t)
	if err := CheckStatementArithmetic(st); err != nil {
		t.Fatal(err)
	}
	got := ExplainStatement(st)
	for _, want := range []string{"Introductory offer", "0% platform share", "20 March 2027", "20 June 2027", "Rs 210.00"} {
		if !strings.Contains(got, want) {
			t.Errorf("explanation is missing %q:\n%s", want, got)
		}
	}
	// A creator whose window does not touch the period is told nothing.
	st.IntroCommission.AppliesToPeriod = false
	if strings.Contains(ExplainStatement(st), "Introductory offer") {
		t.Error("the offer is mentioned on a period it does not cover")
	}
}

// Re-settling reproduces the statement byte for byte, and what is stored
// reads back to the same commission fields.
func TestIntroCommissionReSettlementIsIdempotent(t *testing.T) {
	a, b := introStatement(t), introStatement(t)
	ra, rb := toSettlementRow(uuid.Nil, a), toSettlementRow(uuid.Nil, b)
	if ra.CommissionJSON == "" || ra.CommissionJSON != rb.CommissionJSON {
		t.Fatalf("two runs over the same rows stored different commission:\n%s\n%s", ra.CommissionJSON, rb.CommissionJSON)
	}
	if ra.FundPlatformFeeBps != 3000 || ra.TipsPlatformFeeBps != 0 || !ra.IntroWindowStart.Equal(a.IntroCommission.WindowStart) {
		t.Errorf("stored row = %+v", ra)
	}
	ra.SettledAt = time.Time{}
	back := fromSettlementRow(ra)
	ja, _ := json.Marshal([]StreamLine{a.Fund, a.Tips, a.Subs})
	jb, _ := json.Marshal([]StreamLine{back.Fund, back.Tips, back.Subs})
	if string(ja) != string(jb) {
		t.Errorf("stored statement reads back differently:\nwrote %s\nread  %s", ja, jb)
	}
	if back.IntroCommission == nil || !back.IntroCommission.AppliesToPeriod || back.Fund.CommissionReason != CommissionReasonMixed {
		t.Errorf("read back intro = %+v, fund reason %q", back.IntroCommission, back.Fund.CommissionReason)
	}
	if err := CheckStatementArithmetic(back); err != nil {
		t.Errorf("read-back statement rejected: %v", err)
	}

	// A pre-030 settlement row has no commission and reads as it always did.
	ra.CommissionJSON = ""
	old := fromSettlementRow(ra)
	if old.Fund.CommissionReason != "" || old.Fund.Commission != nil || old.IntroCommission != nil {
		t.Errorf("a pre-030 row grew commission fields: %+v", old.Fund)
	}
}

// A fund row's explanation names the rate it was priced at, not today's.
func TestIntroCommissionRowExplanationUsesTheRecordedRate(t *testing.T) {
	zero := int64(0)
	start, end := utc(2027, time.January, 1, 0, 0), utc(2027, time.April, 1, 0, 0)
	row := &postgres.EarningsDailyBreakdown{PlatformFeeBps: &zero, CommissionReason: CommissionReasonIntroZero, CommissionWindowStart: &start, CommissionWindowEnd: &end}
	if rowPlatformFeeBps(row, 3000) != 0 {
		t.Error("an intro row explained at the standard rate")
	}
	if !strings.Contains(explainRowCommission(row), "1 April 2027") {
		t.Errorf("row explanation = %q", explainRowCommission(row))
	}
	if rowPlatformFeeBps(&postgres.EarningsDailyBreakdown{}, 3000) != 3000 || explainRowCommission(&postgres.EarningsDailyBreakdown{}) != "" {
		t.Error("a legacy row is not explained at the standard rate")
	}
}
