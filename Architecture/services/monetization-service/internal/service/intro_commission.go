package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
)

// ---------------------------------------------------------------------------
// Introductory zero commission — "0% commission for the first 3 months"
// ---------------------------------------------------------------------------
//
// Decisions (16 Nov 2026), implemented here and nowhere else:
//
//   - Per creator. The window opens on the UTC day the creator's
//     monetization was approved: creator_fund_eligibility.eligible_since,
//     which EvaluateEligibility stamps once and never moves (a later
//     suspension or lapse does not restart or extend it).
//   - A creator approved before the launch date gets the window from the
//     launch date instead. The launch date is CreatorFundConfig
//     .IntroZeroCommissionLaunch (env CF_INTRO_ZERO_COMMISSION_LAUNCH_DATE,
//     YYYY-MM-DD, UTC).
//   - It lasts IntroZeroCommissionMonths calendar months (3; env
//     CF_INTRO_ZERO_COMMISSION_MONTHS, 0 switches the offer off). The window
//     is half-open [start, end) on UTC midnights. When the start day does not
//     exist three months later (30 Nov -> 30 Feb) the window runs to the end
//     of that shorter month: end is 1 March, never 28 Feb.
//   - It covers every creator earning the service settles: creator fund
//     (the ads share), tips and subscriptions/memberships. The platform's
//     commission is 0 on an earning whose EVENT time is inside the window
//     (a fund day's day_bucket, a tip's or subscription earning's
//     created_at), in UTC.
//   - Payment processor fees are not commission and are not waived. The
//     service books none on these streams today; nothing here touches one.
//   - Money already priced keeps its price. A fund row records the rate it
//     was priced at (creator_fund_earnings.platform_fee_bps) and is never
//     repriced, so re-running a settlement gives the same money, and a
//     settled period is never rewritten by a later change of these knobs.

// Commission reasons, as recorded on an earning row and a statement line.
const (
	CommissionReasonIntroZero = "intro_zero_commission"
	CommissionReasonStandard  = "standard"
	// CommissionReasonMixed is only ever a statement line's summary: the
	// period straddles a window edge, and the line's Commission slices say
	// which part took which rate.
	CommissionReasonMixed = "mixed"

	defaultIntroZeroCommissionMonths = 3
)

// defaultIntroZeroCommissionLaunch is the launch date used when
// CF_INTRO_ZERO_COMMISSION_LAUNCH_DATE is not set. A creator approved
// before it gets their three months from it.
var defaultIntroZeroCommissionLaunch = time.Date(2027, time.January, 1, 0, 0, 0, 0, time.UTC)

// DefaultIntroZeroCommissionLaunch exposes the default for main.go's log
// line and for tests.
func DefaultIntroZeroCommissionLaunch() time.Time { return defaultIntroZeroCommissionLaunch }

// IntroCommissionWindow is one creator's zero-commission window, [Start, End)
// on UTC midnights.
type IntroCommissionWindow struct {
	Start time.Time `json:"window_start"`
	End   time.Time `json:"window_end"` // exclusive
}

// Contains reports whether an event at t falls inside the window. The
// comparison is in UTC and the end is exclusive.
func (w IntroCommissionWindow) Contains(t time.Time) bool {
	t = t.UTC()
	return !t.Before(w.Start) && t.Before(w.End)
}

// AddCalendarMonths moves a UTC midnight n calendar months forward. A day
// that does not exist in the target month (31st, 30 Feb) does not spill
// into the month after it the way time.AddDate does: the result is the
// first day of the month after the target, i.e. the target month is
// covered to its last day and no further.
func AddCalendarMonths(start time.Time, n int) time.Time {
	start = utcDay(start)
	firstOfTarget := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, n, 0)
	lastDay := firstOfTarget.AddDate(0, 1, -1).Day()
	if start.Day() > lastDay {
		return firstOfTarget.AddDate(0, 1, 0)
	}
	return time.Date(firstOfTarget.Year(), firstOfTarget.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
}

// IntroCommissionWindowFor returns the creator's window given the moment
// their monetization was approved. ok is false when the offer is off
// (months <= 0 or no launch date) or the creator was never approved.
func IntroCommissionWindowFor(approvedAt *time.Time, launch time.Time, months int) (IntroCommissionWindow, bool) {
	if approvedAt == nil || approvedAt.IsZero() || launch.IsZero() || months <= 0 {
		return IntroCommissionWindow{}, false
	}
	start := utcDay(*approvedAt)
	if l := utcDay(launch); start.Before(l) {
		start = l
	}
	return IntroCommissionWindow{Start: start, End: AddCalendarMonths(start, months)}, true
}

// introWindow is the creator's window under this service's config.
func (s *Service) introWindow(eligibility *postgres.CreatorFundEligibility) *IntroCommissionWindow {
	if eligibility == nil {
		return nil
	}
	cfg := s.creatorFundCfg
	w, ok := IntroCommissionWindowFor(eligibility.EligibleSince, cfg.IntroZeroCommissionLaunch, cfg.IntroZeroCommissionMonths)
	if !ok {
		return nil
	}
	return &w
}

// CommissionDecision is the rate applied to one earning and why.
type CommissionDecision struct {
	PlatformFeeBps int64      `json:"platform_fee_bps"`
	Reason         string     `json:"reason"`
	WindowStart    *time.Time `json:"window_start,omitempty"`
	WindowEnd      *time.Time `json:"window_end,omitempty"`
}

// DecideCommission is the whole rule for one earning: zero inside the
// creator's window, the stream's standard rate outside it. Pure; the
// event time is compared in UTC with the window end exclusive.
func DecideCommission(eventTime time.Time, window *IntroCommissionWindow, standardBps int64) CommissionDecision {
	if window != nil && window.Contains(eventTime) {
		start, end := window.Start, window.End
		return CommissionDecision{PlatformFeeBps: 0, Reason: CommissionReasonIntroZero, WindowStart: &start, WindowEnd: &end}
	}
	return CommissionDecision{PlatformFeeBps: standardBps, Reason: CommissionReasonStandard}
}

// IntroCommissionTerms is the creator-facing statement of the offer: the
// rate, the reason code, and the exact window.
type IntroCommissionTerms struct {
	Reason         string    `json:"reason"`
	PlatformFeeBps int64     `json:"platform_fee_bps"`
	WindowStart    time.Time `json:"window_start"`
	WindowEnd      time.Time `json:"window_end"` // exclusive
	// AppliesToPeriod is set on a statement whose period overlaps the window.
	AppliesToPeriod bool `json:"applies_to_period,omitempty"`
}

func introTerms(w *IntroCommissionWindow) *IntroCommissionTerms {
	if w == nil {
		return nil
	}
	return &IntroCommissionTerms{
		Reason:         CommissionReasonIntroZero,
		PlatformFeeBps: 0,
		WindowStart:    w.Start,
		WindowEnd:      w.End,
	}
}

// ---------------------------------------------------------------------------
// Statement lines
// ---------------------------------------------------------------------------

// CommissionSlice is the part of one statement stream priced under one
// commission rule. A line has one slice per (reason, rate) it contains;
// the slices of a line sum to the line.
type CommissionSlice struct {
	Reason           string     `json:"reason"`
	PlatformFeeBps   int64      `json:"platform_fee_bps"`
	Count            int64      `json:"count"`
	GrossPaise       int64      `json:"gross_paise"`
	PlatformFeePaise int64      `json:"platform_fee_paise"`
	NetPaise         int64      `json:"net_paise"`
	WindowStart      *time.Time `json:"window_start,omitempty"`
	WindowEnd        *time.Time `json:"window_end,omitempty"`
}

// overlap is the part of [from, to) inside the window.
func (w IntroCommissionWindow) overlap(from, to time.Time) (time.Time, time.Time, bool) {
	start, end := from, to
	if w.Start.After(start) {
		start = w.Start
	}
	if w.End.Before(end) {
		end = w.End
	}
	return start, end, start.Before(end)
}

// periodCommissionReason is what an EMPTY stream reports: the rule that
// would have applied to an earning in the period.
func periodCommissionReason(from, to time.Time, w *IntroCommissionWindow) string {
	if w == nil {
		return CommissionReasonStandard
	}
	start, end, ok := w.overlap(from, to)
	switch {
	case !ok:
		return CommissionReasonStandard
	case start.Equal(from) && end.Equal(to):
		return CommissionReasonIntroZero
	default:
		return CommissionReasonMixed
	}
}

// eventStreamTotals is a count and gross over a time range.
type eventStreamTotals struct {
	Count      int64
	GrossPaise int64
}

// priceEventStream builds a tips or subscriptions line. total is the whole
// period; intro is the part whose event time fell inside the window (zero
// when there is no window). The intro part takes no commission; the rest
// takes standardBps, truncated once on the slice total exactly as the line
// was before this offer existed.
func priceEventStream(stream string, total, intro eventStreamTotals, standardBps int64, w *IntroCommissionWindow, from, to time.Time) StreamLine {
	line := StreamLine{Stream: stream, Count: total.Count, GrossPaise: total.GrossPaise}
	rest := eventStreamTotals{Count: total.Count - intro.Count, GrossPaise: total.GrossPaise - intro.GrossPaise}
	if intro.Count > 0 || intro.GrossPaise > 0 {
		s := CommissionSlice{Reason: CommissionReasonIntroZero, PlatformFeeBps: 0,
			Count: intro.Count, GrossPaise: intro.GrossPaise, NetPaise: intro.GrossPaise}
		if w != nil {
			start, end := w.Start, w.End
			s.WindowStart, s.WindowEnd = &start, &end
		}
		line.Commission = append(line.Commission, s)
	}
	if rest.Count > 0 || rest.GrossPaise > 0 {
		fee := rest.GrossPaise * standardBps / 10_000
		line.Commission = append(line.Commission, CommissionSlice{Reason: CommissionReasonStandard, PlatformFeeBps: standardBps,
			Count: rest.Count, GrossPaise: rest.GrossPaise, PlatformFeePaise: fee, NetPaise: rest.GrossPaise - fee})
		line.PlatformFeePaise = fee
	}
	line.NetPaise = line.GrossPaise - line.PlatformFeePaise
	summariseCommission(&line, standardBps, w, from, to)
	return line
}

// priceFundStream builds the creator-fund line's commission slices from
// the accrual rows grouped by the rule recorded on each. The line's money
// is the sum of the rows; nothing is repriced. Rows from before migration
// 030 carry no rule and are reported as standard at the configured rate.
//
// The line's totals are set from the same groups, so the slices and the
// line come from one read and always agree (an accrual landing between two
// separate reads cannot make them disagree).
func priceFundStream(line *StreamLine, groups []postgres.FundCommissionGroup, standardBps int64, w *IntroCommissionWindow, from, to time.Time) {
	line.Commission = nil
	line.Count, line.GrossPaise, line.PlatformFeePaise, line.NetPaise = 0, 0, 0, 0
	for _, g := range groups {
		line.Count += g.Rows
		line.GrossPaise += g.GrossPaise
		line.PlatformFeePaise += g.PlatformFeePaise
		line.NetPaise += g.NetPaise
		s := CommissionSlice{
			Reason: g.Reason, Count: g.Rows, GrossPaise: g.GrossPaise,
			PlatformFeePaise: g.PlatformFeePaise, NetPaise: g.NetPaise,
			WindowStart: g.WindowStart, WindowEnd: g.WindowEnd,
		}
		if s.Reason == "" {
			s.Reason = CommissionReasonStandard
		}
		if g.PlatformFeeBps != nil {
			s.PlatformFeeBps = *g.PlatformFeeBps
		} else {
			s.PlatformFeeBps = standardBps
		}
		line.Commission = append(line.Commission, s)
	}
	summariseCommission(line, standardBps, w, from, to)
}

// summariseCommission sets a line's PlatformFeeBps and CommissionReason
// from its slices: one rule means that rule's rate and reason; more than
// one means "mixed" with the standard rate on the line and the truth in
// the slices; none (an empty stream) reports the rule the period was under.
func summariseCommission(line *StreamLine, standardBps int64, w *IntroCommissionWindow, from, to time.Time) {
	reasons := map[string]bool{}
	rates := map[int64]bool{}
	for _, s := range line.Commission {
		reasons[s.Reason] = true
		rates[s.PlatformFeeBps] = true
	}
	switch {
	case len(line.Commission) == 0:
		line.CommissionReason = periodCommissionReason(from, to, w)
		line.PlatformFeeBps = standardBps
		if line.CommissionReason == CommissionReasonIntroZero {
			line.PlatformFeeBps = 0
		}
	case len(reasons) == 1 && len(rates) == 1:
		line.CommissionReason = line.Commission[0].Reason
		line.PlatformFeeBps = line.Commission[0].PlatformFeeBps
	default:
		line.CommissionReason = CommissionReasonMixed
		line.PlatformFeeBps = standardBps
	}
}

// introGrossPaise is the gross on a statement that took the intro rate.
func introGrossPaise(st *PeriodStatement) int64 {
	var sum int64
	for _, line := range []StreamLine{st.Fund, st.Tips, st.Subs} {
		for _, s := range line.Commission {
			if s.Reason == CommissionReasonIntroZero {
				sum += s.GrossPaise
			}
		}
	}
	return sum
}

// checkCommissionSlices refuses a line whose slices do not add up to it.
// A line with no slices is a pre-030 statement and is not checked.
func checkCommissionSlices(line StreamLine) error {
	if len(line.Commission) == 0 {
		return nil
	}
	var count, gross, fee, net int64
	for _, s := range line.Commission {
		if s.GrossPaise-s.PlatformFeePaise != s.NetPaise {
			return fmt.Errorf("STATEMENT_ARITHMETIC: %s %s slice gross %d - fee %d != net %d",
				line.Stream, s.Reason, s.GrossPaise, s.PlatformFeePaise, s.NetPaise)
		}
		if s.Reason == CommissionReasonIntroZero && s.PlatformFeePaise != 0 {
			return fmt.Errorf("STATEMENT_ARITHMETIC: %s took %d paise of commission inside the intro zero-commission window",
				line.Stream, s.PlatformFeePaise)
		}
		count += s.Count
		gross += s.GrossPaise
		fee += s.PlatformFeePaise
		net += s.NetPaise
	}
	if count != line.Count || gross != line.GrossPaise || fee != line.PlatformFeePaise || net != line.NetPaise {
		return fmt.Errorf("STATEMENT_ARITHMETIC: %s commission slices sum to %d rows / %d gross / %d fee / %d net, line says %d / %d / %d / %d",
			line.Stream, count, gross, fee, net, line.Count, line.GrossPaise, line.PlatformFeePaise, line.NetPaise)
	}
	return nil
}

// rowPlatformFeeBps is the rate a fund row was actually priced at: the
// recorded one, or the configured standard for a row from before the rate
// was recorded.
func rowPlatformFeeBps(row *postgres.EarningsDailyBreakdown, standardBps int64) int64 {
	if row.PlatformFeeBps != nil {
		return *row.PlatformFeeBps
	}
	return standardBps
}

// explainRowCommission is appended to a fund row's explanation when the
// row was priced under the intro window.
func explainRowCommission(row *postgres.EarningsDailyBreakdown) string {
	if row.CommissionReason != CommissionReasonIntroZero || row.CommissionWindowStart == nil || row.CommissionWindowEnd == nil {
		return ""
	}
	return fmt.Sprintf(" The platform share is 0%% on this day: introductory zero commission from %s until %s (not including that day).",
		row.CommissionWindowStart.UTC().Format("2 January 2006"), row.CommissionWindowEnd.UTC().Format("2 January 2006"))
}

// ---------------------------------------------------------------------------
// Persistence of a statement's commission breakdown
// ---------------------------------------------------------------------------

// statementCommission is what creator_fund_period_settlements.commission
// holds: each line's reason, rate and slices, and the creator's window.
type statementCommission struct {
	Fund  streamCommission      `json:"creator_fund"`
	Tips  streamCommission      `json:"tips"`
	Subs  streamCommission      `json:"subscriptions"`
	Intro *IntroCommissionTerms `json:"intro_commission,omitempty"`
}

type streamCommission struct {
	Reason         string            `json:"reason"`
	PlatformFeeBps int64             `json:"platform_fee_bps"`
	Slices         []CommissionSlice `json:"slices,omitempty"`
}

// encodeStatementCommission renders the breakdown deterministically (struct
// field order, slices in the order the statement built them), so a re-run
// that finds the same rows writes the same bytes.
func encodeStatementCommission(st *PeriodStatement) string {
	c := statementCommission{
		Fund:  streamCommission{Reason: st.Fund.CommissionReason, PlatformFeeBps: st.Fund.PlatformFeeBps, Slices: st.Fund.Commission},
		Tips:  streamCommission{Reason: st.Tips.CommissionReason, PlatformFeeBps: st.Tips.PlatformFeeBps, Slices: st.Tips.Commission},
		Subs:  streamCommission{Reason: st.Subs.CommissionReason, PlatformFeeBps: st.Subs.PlatformFeeBps, Slices: st.Subs.Commission},
		Intro: st.IntroCommission,
	}
	if c.Fund.Reason == "" && c.Tips.Reason == "" && c.Subs.Reason == "" && c.Intro == nil {
		return ""
	}
	b, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	return string(b)
}

// decodeStatementCommission puts a stored breakdown back on the statement.
// An empty or unreadable value leaves the statement as it was before 030:
// the per-stream rates from their own columns, no reason, no slices.
func decodeStatementCommission(raw string, st *PeriodStatement) {
	if raw == "" {
		return
	}
	var c statementCommission
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return
	}
	apply := func(line *StreamLine, sc streamCommission) {
		line.CommissionReason = sc.Reason
		line.Commission = sc.Slices
	}
	apply(&st.Fund, c.Fund)
	apply(&st.Tips, c.Tips)
	apply(&st.Subs, c.Subs)
	st.IntroCommission = c.Intro
}

func introWindowStart(t *IntroCommissionTerms) *time.Time {
	if t == nil {
		return nil
	}
	v := t.WindowStart
	return &v
}

func introWindowEnd(t *IntroCommissionTerms) *time.Time {
	if t == nil {
		return nil
	}
	v := t.WindowEnd
	return &v
}

// explainIntroCommission is the sentence the statement adds when the
// period overlaps the creator's window.
func explainIntroCommission(st *PeriodStatement) string {
	t := st.IntroCommission
	if t == nil || !t.AppliesToPeriod {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, " Introductory offer: 0%% platform share on everything you earn from %s until %s (not including that day)",
		t.WindowStart.Format("2 January 2006"), t.WindowEnd.Format("2 January 2006"))
	if g := introGrossPaise(st); g > 0 {
		fmt.Fprintf(&b, "; %s of this period's gross was earned under it", rupees(g))
	}
	b.WriteString(".")
	return b.String()
}
