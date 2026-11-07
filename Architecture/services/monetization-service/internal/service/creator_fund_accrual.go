package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/atpost/monetization-service/internal/buildinfo"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/atpost/shared/postclassify"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// ---------------------------------------------------------------------------
// Accrual — daily, no money moves (plan Phase 2B and 2C)
// ---------------------------------------------------------------------------
//
// AccrueCreatorFundDay measures one creator's fund earnings for one UTC
// day and records them, crediting nothing. Compared with the version it
// replaces, three things changed and one did not:
//
//   - Money is computed in MICRO-paise and truncated once, at the carry
//     boundary. The sub-paise remainder rolls into the next day for the
//     same (creator, content_type, region), so one flick view a day is no
//     longer worth nothing forever. Days therefore accrue in order.
//   - Every row names the rate row, the band row, the formula version and
//     a fingerprint of the analytics rows it was measured from. Inputs
//     that move after a day was accrued are an error, not a silent no-op.
//   - A period may carry a budget cap. Once it is spent, later days are
//     zero rows that say so. Nothing already earned is reduced.
//   - The pricing itself — RPM as of the day, quality band as of the day
//     (frozen bands read as 1.0x from the row, never assumed), platform
//     split — is what it was.
//
// Every reason a (creator, day, type) did NOT accrue in full is counted
// and returned, logged, and exported as a metric.

// RuleVersion names the accrual formula stamped on every row written by
// this code. 'cf-0' is the pre-019 per-step-truncation formula.
const RuleVersion = "cf-1"

// MicroPaisePerPaise is the fixed-point scale the accrual computes in.
const MicroPaisePerPaise = int64(1_000_000)

// Skip reasons, as recorded in DayAccrual.Skipped and on the row.
const (
	SkipZeroViews       = "zero_views"
	SkipUnsupportedType = "unsupported_type:" // followed by the raw content type
	SkipAlreadyAccrued  = "already_accrued"
	SkipNoRate          = "no_rate"
	SkipBudgetExhausted = "budget_exhausted"
	SkipBudgetCapped    = "budget_capped"
	// SkipDayNotFinal: analytics may still re-roll the day (its 48-hour
	// window is open). Nothing is written; a later run accrues it.
	SkipDayNotFinal = "day_not_final"
	// SkipRevisionDrift: an accrued, final day whose inputs moved anyway.
	// The difference is recorded as a pending adjustment; nothing is
	// repriced and the period is not failed.
	SkipRevisionDrift = "revision_drift"
)

// ErrInputRevisionChanged describes an accrued day measured again from
// analytics rows that no longer match the rows it was priced from. Since
// the accrual prices only final days (2026-11-07), that is recorded as a
// pending adjustment (recordRevisionDrift) instead of being returned; the
// error text is what the adjustment and its log line describe.
var ErrInputRevisionChanged = errors.New("INPUT_REVISION_CHANGED: the analytics rows behind an accrued day have changed since it was priced")

// analyticsFinality is analytics' answer to "which days will never be
// re-rolled again" (analytics.v_daily_rollup_finality_v1): every day
// strictly before `before` is final. known=false means analytics has not
// said yet, and nothing is final.
type analyticsFinality struct {
	before time.Time
	known  bool
}

// dayFinal reports whether one UTC day is final.
func (f analyticsFinality) dayFinal(day time.Time) bool {
	return f.known && utcDay(day).Before(f.before)
}

// periodFinal reports whether every day of the period is final. End is
// exclusive, so the last day End-1 is final exactly when End <= before.
func (f analyticsFinality) periodFinal(p SettlementPeriod) bool {
	return f.known && !p.End.After(f.before)
}

func (s *Service) analyticsFinality(ctx context.Context) (analyticsFinality, error) {
	before, ok, err := s.store.AnalyticsFinalBefore(ctx)
	if err != nil {
		return analyticsFinality{}, err
	}
	return analyticsFinality{before: before, known: ok}, nil
}

// revisionDrift returns the pending adjustment for an accrued row whose
// inputs no longer hash to its recorded revision, or nil when there is
// nothing to record (no row, the same inputs, a reversed or unversioned
// row; see checkInputRevision). Both sides are priced the same way, at
// the row's own rate and quality multiplier and before the carry, so the
// delta is the change in what was measured and nothing else.
func revisionDrift(existing *postgres.CreatorFundEarning, g dailyInputGroup) *postgres.RevisionAdjustment {
	if checkInputRevision(existing, g.Revision) == nil {
		return nil
	}
	mult := existing.QualityMultiplierBps
	if mult <= 0 {
		mult = NeutralMultiplierBps
	}
	recorded := ComputeGrossMicroPaise(existing.ViewCount, existing.RpmPaise, mult)
	observed := ComputeGrossMicroPaise(g.Metric.ViewCount, existing.RpmPaise, mult)
	return &postgres.RevisionAdjustment{
		EarningID:               existing.ID,
		CreatorID:               existing.CreatorID,
		DayBucket:               existing.DayBucket,
		ContentType:             existing.ContentType,
		RegionCode:              existing.RegionCode,
		RecordedRevision:        existing.InputRevision,
		ObservedRevision:        g.Revision,
		RecordedViews:           existing.ViewCount,
		ObservedViews:           g.Metric.ViewCount,
		RecordedGrossMicroPaise: recorded,
		ObservedGrossMicroPaise: observed,
		DeltaGrossMicroPaise:    observed - recorded,
	}
}

// settleExisting handles a group whose slot is already accrued: either
// the same inputs (already_accrued) or moved inputs on a final day, which
// is recorded once as a pending adjustment (revision_drift).
func (s *Service) settleExisting(ctx context.Context, existing *postgres.CreatorFundEarning, g dailyInputGroup, res *DayAccrual) error {
	adj := revisionDrift(existing, g)
	if adj == nil {
		res.skip(SkipAlreadyAccrued)
		return nil
	}
	inserted, err := s.store.RecordRevisionAdjustment(ctx, adj)
	if err != nil {
		return fmt.Errorf("record revision adjustment for earning %s: %w", existing.ID, err)
	}
	if inserted {
		slog.Warn("creator-fund accrual: a final day's analytics moved after it was priced; recorded a pending adjustment",
			"error", ErrInputRevisionChanged, "earning_id", existing.ID, "creator_id", existing.CreatorID,
			"day", existing.DayBucket.Format("2006-01-02"), "content_type", existing.ContentType,
			"recorded_views", adj.RecordedViews, "observed_views", adj.ObservedViews,
			"delta_gross_micro_paise", adj.DeltaGrossMicroPaise)
	}
	res.skip(SkipRevisionDrift)
	return nil
}

var accrualSkipsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "atpost",
	Subsystem: "monetization",
	Name:      "creator_fund_accrual_skips_total",
	Help:      "Creator-fund (creator, day, content_type) accruals that did not accrue in full, by reason.",
}, []string{"reason"})

// DayAccrual is what one AccrueCreatorFundDay call did: rows written with
// money on them, and every reason something was not.
type DayAccrual struct {
	Accrued int            `json:"accrued"`
	Skipped map[string]int `json:"skipped,omitempty"`
	// NotFinal: analytics may still re-roll the day; nothing was read or
	// written, and a later run accrues it.
	NotFinal bool `json:"not_final,omitempty"`
}

func (d *DayAccrual) skip(reason string) {
	if d.Skipped == nil {
		d.Skipped = map[string]int{}
	}
	d.Skipped[reason]++
}

// BatchAccrual is the sum of DayAccrual over every eligible creator.
type BatchAccrual struct {
	Creators int            `json:"creators"`
	Accrued  int            `json:"accrued"`
	Skipped  map[string]int `json:"skipped,omitempty"`
	Failed   int            `json:"failed"`
	// NotFinal: the day is not final in analytics yet, so no creator was
	// walked. Not a failure; the day is accrued by a later run.
	NotFinal bool `json:"not_final,omitempty"`
}

// canonicalMonetizationType maps a raw analytics content type onto the
// two kinds the rate sheet knows: short-form ('flick', and the legacy
// 'reel'/'short') earns as flick, long-form ('long_video', legacy 'video')
// as long_video. Anything else is not monetised and is reported as such.
//
// Built on postclassify.IsShortForm / IsLongForm here rather than on the
// shared CanonicalMonetizationType the analytics side is adding, so this
// service does not wait on that change; the two must agree, and a test
// pins this one.
func canonicalMonetizationType(contentType string) (string, bool) {
	switch {
	case postclassify.IsShortForm(contentType):
		return postclassify.Flick, true
	case postclassify.IsLongForm(contentType):
		return postclassify.LongVideo, true
	default:
		return "", false
	}
}

// ComputeGrossMicroPaise is the day's gross before the carry, in
// micro-paise: views x rpm_paise x 1000, scaled by the multiplier in
// basis points. One flick view at 300 paise per 1000 is 300,000. The
// basis-point scaling is split so the intermediate product cannot
// overflow int64 for any realistic day.
func ComputeGrossMicroPaise(views, rpmPaise, multiplierBps int64) int64 {
	if views <= 0 || rpmPaise <= 0 || multiplierBps <= 0 {
		return 0
	}
	base := views * rpmPaise * 1000
	whole := base / NeutralMultiplierBps
	rem := base % NeutralMultiplierBps
	return whole*multiplierBps + rem*multiplierBps/NeutralMultiplierBps
}

// ApplyCarry adds the carried remainder and truncates once: whole paise
// out, the rest carried forward. grossPaise x 1e6 + carryOut == total,
// exactly.
func ApplyCarry(grossMicroPaise, carryInMicroPaise int64) (grossPaise, carryOutMicroPaise int64) {
	if grossMicroPaise < 0 {
		grossMicroPaise = 0
	}
	if carryInMicroPaise < 0 {
		carryInMicroPaise = 0
	}
	total := grossMicroPaise + carryInMicroPaise
	return total / MicroPaisePerPaise, total % MicroPaisePerPaise
}

// PriceAccrualDay is the pure pricing step the store calls under its
// locks: carry boundary first, then the budget.
//
//   - No budget row (remaining == nil): full accrual, remainder carried.
//   - Budget exhausted (remaining <= 0): a zero row marked
//     budget_exhausted; the carry is left exactly as it was.
//   - Gross exceeds what remains: the day takes exactly the remainder and
//     is marked budget_capped; the capped-off portion, carry included, is
//     dropped rather than carried into a period that may have its own cap.
func PriceAccrualDay(grossMicroPaise, carryInMicroPaise int64, budgetRemainingPaise *int64, platformFeeBps int64) postgres.PricedDay {
	if budgetRemainingPaise != nil && *budgetRemainingPaise <= 0 {
		return postgres.PricedDay{SkipReason: SkipBudgetExhausted, CarryOutMicroPaise: carryInMicroPaise}
	}
	grossPaise, carryOut := ApplyCarry(grossMicroPaise, carryInMicroPaise)
	skip := ""
	if budgetRemainingPaise != nil && grossPaise > *budgetRemainingPaise {
		grossPaise = *budgetRemainingPaise
		carryOut = 0
		skip = SkipBudgetCapped
	}
	net, fee := SplitEarnings(grossPaise, platformFeeBps)
	return postgres.PricedDay{
		GrossPaise:         grossPaise,
		PlatformFeePaise:   fee,
		NetPaise:           net,
		CarryOutMicroPaise: carryOut,
		SkipReason:         skip,
	}
}

// dailyInputGroup is the rows of one canonical content type on one day,
// aggregated for pricing and fingerprinted for the revision.
type dailyInputGroup struct {
	Metric   postgres.DailyContentMetric
	Revision string
	RawTypes []string
}

// unsupportedInput is a raw content type the fund does not pay for.
type unsupportedInput struct {
	RawType string
	Views   int64
}

// AggregateDailyInputs groups raw analytics rows by canonical content
// type, computing the same view-weighted quality score the old SQL did
// (one obscure clip cannot drag down a day carried by a video that reached
// people), and the input revision per group. Unsupported types are
// returned separately with their views so the skip can be named.
func AggregateDailyInputs(rows []postgres.DailyInputRow) ([]dailyInputGroup, []unsupportedInput) {
	type acc struct {
		views, watch, impressions int64
		weighted                  float64
		weight                    int64
		rows                      []postgres.DailyInputRow
		raw                       map[string]struct{}
	}
	byType := map[string]*acc{}
	var unsupported []unsupportedInput
	for _, r := range rows {
		canonical, ok := canonicalMonetizationType(r.ContentType)
		if !ok {
			unsupported = append(unsupported, unsupportedInput{RawType: r.ContentType, Views: r.ViewsDisplay})
			continue
		}
		a := byType[canonical]
		if a == nil {
			a = &acc{raw: map[string]struct{}{}}
			byType[canonical] = a
		}
		a.views += r.ViewsDisplay
		a.watch += r.WatchTimeMs
		a.impressions += r.Impressions
		if w := r.ViewsDisplay; w > 0 {
			a.weighted += r.CQS * float64(w)
			a.weight += w
		}
		a.rows = append(a.rows, r)
		a.raw[r.ContentType] = struct{}{}
	}
	types := make([]string, 0, len(byType))
	for t := range byType {
		types = append(types, t)
	}
	sort.Strings(types)
	out := make([]dailyInputGroup, 0, len(types))
	for _, t := range types {
		a := byType[t]
		avg := 0.0
		if a.weight > 0 {
			avg = a.weighted / float64(a.weight)
		}
		raw := make([]string, 0, len(a.raw))
		for r := range a.raw {
			raw = append(raw, r)
		}
		sort.Strings(raw)
		out = append(out, dailyInputGroup{
			Metric: postgres.DailyContentMetric{
				ContentType: t,
				ViewCount:   a.views,
				WatchTimeMs: a.watch,
				Impressions: a.impressions,
				AvgCQS:      avg,
			},
			Revision: InputRevision(a.rows),
			RawTypes: raw,
		})
	}
	return out, unsupported
}

// InputRevision is the sha256 over the VALUE columns of the rows a day
// was priced from — content_id, day_bucket, content_type, views_display,
// watch_time_total_ms, impressions, content_quality_score — in a fixed
// order. Row timestamps are deliberately excluded: the analytics rollup
// is delete-then-insert inside a 48-hour window, so a day accrued at
// 03:00 is routinely rewritten with identical values and a fresh
// updated_at, and that is not a change to what was measured. A changed
// value, or a row added or removed, is.
func InputRevision(rows []postgres.DailyInputRow) string {
	sorted := make([]postgres.DailyInputRow, len(rows))
	copy(sorted, rows)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].ContentType != sorted[j].ContentType {
			return sorted[i].ContentType < sorted[j].ContentType
		}
		return sorted[i].ContentID.String() < sorted[j].ContentID.String()
	})
	h := sha256.New()
	for _, r := range sorted {
		h.Write([]byte(r.ContentID.String()))
		h.Write([]byte{'|'})
		h.Write([]byte(r.DayBucket.UTC().Format("2006-01-02")))
		h.Write([]byte{'|'})
		h.Write([]byte(r.ContentType))
		h.Write([]byte{'|'})
		h.Write([]byte(strconv.FormatInt(r.ViewsDisplay, 10)))
		h.Write([]byte{'|'})
		h.Write([]byte(strconv.FormatInt(r.WatchTimeMs, 10)))
		h.Write([]byte{'|'})
		h.Write([]byte(strconv.FormatInt(r.Impressions, 10)))
		h.Write([]byte{'|'})
		h.Write([]byte(strconv.FormatFloat(r.CQS, 'g', -1, 64)))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// checkInputRevision compares an existing row's recorded revision with the
// one just computed. Rows priced before revisions existed carry none and
// cannot be compared; reversed rows have already been corrected by hand
// and are exempt, or a fixed rollup would trip the check forever.
func checkInputRevision(existing *postgres.CreatorFundEarning, current string) error {
	if existing == nil || existing.Status == "reversed" || existing.InputRevision == "" {
		return nil
	}
	if existing.InputRevision != current {
		return fmt.Errorf("%w: earning %s (%s %s) recorded %s, inputs now hash to %s",
			ErrInputRevisionChanged, existing.ID, existing.DayBucket.Format("2006-01-02"),
			existing.ContentType, existing.InputRevision[:12], current[:12])
	}
	return nil
}

// resolveQualityBandRow is ResolveQualityBand with the row's identity kept,
// so the accrual can stamp band_id. A nil row means the launch default.
func (s *Service) resolveQualityBandRow(ctx context.Context, contentType, regionCode string, asOf time.Time) (*uuid.UUID, QualityBand, error) {
	row, err := s.store.GetActiveQualityBand(ctx, contentType, regionCode, asOf)
	if err != nil {
		return nil, DefaultQualityBand(), err
	}
	if row == nil {
		return nil, DefaultQualityBand(), nil
	}
	id := row.ID
	return &id, QualityBand{
		FloorBps:              row.FloorBps,
		CeilingBps:            row.CeilingBps,
		PivotCQS:              row.PivotCQS,
		ConfidenceImpressions: row.ConfidenceImpressions,
		Enabled:               row.Enabled,
	}, nil
}

var uncappedPeriodsWarned sync.Map

// warnUncappedOnce logs, once per period key per process, that accruals
// are running with no budget row. The plan's context section wants a cap
// per period; the mechanism here treats a missing row as uncapped and
// says so, rather than silently stopping the fund.
func warnUncappedOnce(periodKey, regionCode string) {
	if _, loaded := uncappedPeriodsWarned.LoadOrStore(periodKey+"/"+regionCode, struct{}{}); loaded {
		return
	}
	slog.Warn("creator-fund accrual: no budget row for period; accruing UNCAPPED",
		"period", periodKey, "region", regionCode,
		"fix", "PUT /v1/monetization/admin/creator-fund/budgets")
}

// AccrueCreatorFundDay measures one creator's fund earnings for one UTC
// day and records them, crediting nothing. Returns what it wrote and
// every reason something was skipped. A day analytics may still re-roll
// is not measured at all (NotFinal); an accrued final day whose inputs
// moved is recorded as a pending adjustment (revision_drift).
func (s *Service) AccrueCreatorFundDay(ctx context.Context, creatorID uuid.UUID, day time.Time) (DayAccrual, error) {
	fin, err := s.analyticsFinality(ctx)
	if err != nil {
		return DayAccrual{}, err
	}
	return s.accrueDay(ctx, creatorID, day, fin)
}

func (s *Service) accrueDay(ctx context.Context, creatorID uuid.UUID, day time.Time, fin analyticsFinality) (DayAccrual, error) {
	day = utcDay(day)
	var res DayAccrual

	// Accrue a day only once analytics treats it as final. Inside the
	// re-roll window a late view changes the rows, and a day priced from
	// rows that then move was the 03:00 failure this replaced.
	if !fin.dayFinal(day) {
		res.NotFinal = true
		res.skip(SkipDayNotFinal)
		return res, nil
	}

	row, err := s.store.GetCreatorFundEligibility(ctx, creatorID)
	if err != nil {
		return res, err
	}
	// The eligibility gate is still the gate. An ineligible or suspended
	// creator accrues nothing, so a period settlement finds nothing to
	// pay them from the fund. Their tips and subscriptions are still
	// reported on the statement, because those are theirs regardless.
	if row == nil || row.Status != "eligible" {
		return res, nil
	}

	inputs, err := s.store.QueryCreatorDailyInputs(ctx, creatorID, day)
	if err != nil {
		return res, fmt.Errorf("query daily inputs: %w", err)
	}
	if len(inputs) == 0 {
		return res, nil
	}
	groups, unsupported := AggregateDailyInputs(inputs)
	for _, u := range unsupported {
		if u.Views <= 0 {
			res.skip(SkipZeroViews)
			continue
		}
		res.skip(SkipUnsupportedType + u.RawType)
	}

	cfg := s.creatorFundCfg
	period := PeriodContaining(day, cfg.SettlementCadence)

	// With payouts on, an accrual is a claim on real money, and a claim
	// against a fund nobody has sized is refused outright — before any
	// row is written, so the day is provably NOT measured rather than
	// measured uncapped. With payouts off the loop below warns once per
	// period and accrues uncapped (Phase 2C), because an estimate that
	// is too high is corrected by a cap later and an estimate that is
	// missing is a blank creators cannot plan against.
	if s.payoutsEnabled {
		budget, err := s.store.GetCreatorFundBudget(ctx, period.Key, defaultRegionCode)
		if err != nil {
			return res, fmt.Errorf("check budget: %w", err)
		}
		if budget == nil {
			slog.Error("creator-fund accrual: refusing to accrue against a period with no budget row while payouts are enabled",
				"creator_id", creatorID, "day", day.Format("2006-01-02"),
				"period", period.Key, "region", defaultRegionCode,
				"fix", "PUT /v1/monetization/admin/creator-fund/budgets")
			return res, fmt.Errorf("%w: period %s region %s has no budget row and payouts are enabled",
				ErrNoBudget, period.Key, defaultRegionCode)
		}
	}

	for _, g := range groups {
		m := g.Metric
		if m.ViewCount <= 0 {
			res.skip(SkipZeroViews)
			continue
		}
		// Cheap pre-check before any lock; the authoritative check runs
		// again under the locks inside AccrueDayTx.
		existing, err := s.store.FindCreatorFundEarningSlot(ctx, creatorID, day, m.ContentType, defaultRegionCode)
		if err != nil {
			return res, fmt.Errorf("slot check: %w", err)
		}
		if existing != nil {
			if err := s.settleExisting(ctx, existing, g, &res); err != nil {
				return res, err
			}
			continue
		}
		rate, err := s.store.GetActiveRpmRate(ctx, m.ContentType, defaultRegionCode, day)
		if err != nil {
			return res, fmt.Errorf("fetch rpm rate: %w", err)
		}
		if rate == nil || rate.RpmPaise <= 0 {
			res.skip(SkipNoRate)
			continue
		}
		bandID, band, err := s.resolveQualityBandRow(ctx, m.ContentType, defaultRegionCode, day)
		if err != nil {
			return res, fmt.Errorf("fetch quality band: %w", err)
		}
		// A disabled band returns 10000 here. The freeze is read off the
		// row that applied on the day, never assumed.
		multiplierBps := ComputeQualityMultiplierBps(m.AvgCQS, m.Impressions, band)
		grossMicro := ComputeGrossMicroPaise(m.ViewCount, rate.RpmPaise, multiplierBps)
		rateID := rate.ID

		earning := &postgres.CreatorFundEarning{
			CreatorID:            creatorID,
			DayBucket:            day,
			ContentType:          m.ContentType,
			RegionCode:           defaultRegionCode,
			ViewCount:            m.ViewCount,
			WatchTimeMs:          m.WatchTimeMs,
			RpmPaise:             rate.RpmPaise,
			Status:               "settled",
			SettledAt:            time.Now(),
			BaseGrossPaise:       ComputeGrossPaise(m.ViewCount, rate.RpmPaise),
			QualityCQS:           m.AvgCQS,
			QualityEffectiveCQS:  ShrinkCQS(m.AvgCQS, m.Impressions, band),
			QualityImpressions:   m.Impressions,
			QualityMultiplierBps: multiplierBps,
			RateID:               &rateID,
			BandID:               bandID,
			RuleVersion:          RuleVersion,
			BuildSHA:             buildinfo.SHA,
			InputRevision:        g.Revision,
			GrossMicroPaise:      grossMicro,
		}

		var outcome postgres.AccrueDayOutcome
		err = s.store.WithTx(ctx, func(tx pgxTx) error {
			o, err := s.store.AccrueDayTx(ctx, tx, postgres.AccrueDayInput{
				Earning:   earning,
				PeriodKey: period.Key,
				Price: func(carryIn int64, remaining *int64) postgres.PricedDay {
					return PriceAccrualDay(grossMicro, carryIn, remaining, cfg.PlatformFeeBps)
				},
			})
			outcome = o
			return err
		})
		if err != nil {
			return res, fmt.Errorf("accrue %s %s: %w", day.Format("2006-01-02"), m.ContentType, err)
		}
		if !outcome.Inserted {
			// Lost the race with a parallel accrual; the row exists.
			if err := s.settleExisting(ctx, outcome.Existing, g, &res); err != nil {
				return res, err
			}
			continue
		}
		if outcome.Budget == nil {
			warnUncappedOnce(period.Key, defaultRegionCode)
		}
		switch earning.SkipReason {
		case SkipBudgetExhausted:
			res.skip(SkipBudgetExhausted)
		case SkipBudgetCapped:
			res.skip(SkipBudgetCapped)
			res.Accrued++
		default:
			res.Accrued++
		}
	}

	if len(res.Skipped) > 0 {
		attrs := make([]any, 0, 6+2*len(res.Skipped))
		attrs = append(attrs, "creator_id", creatorID, "day", day.Format("2006-01-02"), "accrued", res.Accrued)
		for reason, n := range res.Skipped {
			attrs = append(attrs, reason, n)
			accrualSkipsTotal.WithLabelValues(metricSkipLabel(reason)).Add(float64(n))
		}
		slog.Info("creator-fund accrual: skips", attrs...)
	}
	return res, nil
}

// metricSkipLabel bounds label cardinality: the raw content type stays in
// the log and the returned map, not in the metric.
func metricSkipLabel(reason string) string {
	if strings.HasPrefix(reason, SkipUnsupportedType) {
		return strings.TrimSuffix(SkipUnsupportedType, ":")
	}
	return reason
}

// AccrueCreatorFundDayForAllEligible walks every eligible creator, in
// creator_id order, and accrues `day`. Per-creator errors are logged and
// skipped; the order is what makes a budget cap land on the same creator
// on every run.
//
// A day analytics may still re-roll walks nobody and returns NotFinal.
func (s *Service) AccrueCreatorFundDayForAllEligible(ctx context.Context, day time.Time, log func(creatorID uuid.UUID, res DayAccrual, err error)) (BatchAccrual, error) {
	fin, err := s.analyticsFinality(ctx)
	if err != nil {
		return BatchAccrual{}, err
	}
	return s.accrueDayForAllEligible(ctx, day, fin, log)
}

func (s *Service) accrueDayForAllEligible(ctx context.Context, day time.Time, fin analyticsFinality, log func(creatorID uuid.UUID, res DayAccrual, err error)) (BatchAccrual, error) {
	var batch BatchAccrual
	if !fin.dayFinal(day) {
		batch.NotFinal = true
		return batch, nil
	}
	creators, err := s.store.ListEligibleCreators(ctx)
	if err != nil {
		return batch, err
	}
	for _, id := range creators {
		res, err := s.accrueDay(ctx, id, day, fin)
		if log != nil {
			log(id, res, err)
		}
		batch.Creators++
		if err != nil {
			batch.Failed++
			continue
		}
		batch.Accrued += res.Accrued
		for reason, n := range res.Skipped {
			if batch.Skipped == nil {
				batch.Skipped = map[string]int{}
			}
			batch.Skipped[reason] += n
		}
	}
	return batch, nil
}

// FinalDaysAccrual is what AccrueRecentFinalDays did.
type FinalDaysAccrual struct {
	// FinalBefore is analytics' finality line: every day before it is
	// final. Zero when analytics has not said (nothing was accrued).
	FinalBefore time.Time      `json:"final_before"`
	Days        []time.Time    `json:"days"`
	Batch       []BatchAccrual `json:"batch"`
}

// AccrueRecentFinalDays accrues, in day order, the lookbackDays most
// recent days analytics treats as final. The nightly worker runs it: a
// day becomes final about two days after it ends, and a run that was
// missed, or a day whose rollup was late, is picked up by the next one
// because every accrual is idempotent.
func (s *Service) AccrueRecentFinalDays(ctx context.Context, lookbackDays int, log func(creatorID uuid.UUID, res DayAccrual, err error)) (FinalDaysAccrual, error) {
	var out FinalDaysAccrual
	if lookbackDays <= 0 {
		lookbackDays = 1
	}
	fin, err := s.analyticsFinality(ctx)
	if err != nil {
		return out, err
	}
	if !fin.known {
		return out, nil
	}
	out.FinalBefore = fin.before
	for day := fin.before.AddDate(0, 0, -lookbackDays); day.Before(fin.before); day = day.AddDate(0, 0, 1) {
		batch, err := s.accrueDayForAllEligible(ctx, day, fin, log)
		if err != nil {
			return out, fmt.Errorf("accrue %s: %w", day.Format("2006-01-02"), err)
		}
		out.Days = append(out.Days, day)
		out.Batch = append(out.Batch, batch)
	}
	return out, nil
}
