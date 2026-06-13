// Account-risk scoring service — §P0-7 Phase A.
//
// The Phase A scaffold lands seven signals on a 0..100 scale and maps
// the final score to one of seven enforcement levels. Phase B will
// wire the deferred device-reuse signal (no device-fingerprint table
// exists yet) and the real IP/ASN velocity hook (no request-log
// aggregator exists yet); both are surfaced as TODOs inside the
// signals JSON so callers can see exactly what's missing.
//
// Scoring direction (CRITICAL): higher score = riskier.
//
//   - Verification tier + profile completeness REDUCE risk.
//   - Photo-approval state REDUCES risk when approved, ADDS when rejected.
//   - Report count + actioned-report quality + block-by-rate + spark
//     velocity ADD risk.
//
// Weights match the test plan §P0-7 table. Each signal returns a
// 0..1 normalised contribution; the formula is then
//
//	score = base 30
//	      - (verification_tier_contrib  * 25)   // up to -25
//	      - (profile_completeness       * 10)   // up to -10
//	      - (photo_approved_contrib     *  7.5) // up to -7.5 (half of 15w when fully approved)
//	      + (photo_rejected_contrib     * 15)   // up to +15
//	      + (ip_asn_velocity_contrib    * 10)   // up to +10 (TODO Phase B: always 0 for now)
//	      + (report_quality_contrib     * 15)   // up to +15
//	      + (block_rate_contrib         *  5)   // up to +5
//	      + (spark_velocity_contrib     *  5)   // up to +5
//
// Clamped to [0,100], rounded to nearest int.
//
// Threshold table (test plan order):
//
//	 0..30  → allow
//	31..50  → reduce_reach
//	51..65  → require_recheck
//	66..75  → hide_from_discovery
//	76..85  → chat_hold
//	86..95  → admin_review
//	96..100 → suspend
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// ErrRiskBlocked is the sentinel mapped to 403 via the "forbidden:"
// prefix. Returned to actions that are gated when risk_level is
// chat_hold / admin_review / suspend.
var ErrRiskBlocked = errors.New("forbidden: account is under review")

// ErrRiskRecheck is the sentinel mapped to 400 via the "invalid:"
// prefix. Returned to actions that are gated when risk_level is
// require_recheck. The UX is "go re-do the selfie verification".
var ErrRiskRecheck = errors.New("invalid: please re-verify your selfie before continuing")

// Signal weights (test plan §P0-7). Constants so the threshold logic +
// the Phase B device-reuse hook can both reference the same numbers.
const (
	weightVerification        = 25
	weightProfileCompleteness = 10
	weightPhotoApproval       = 15
	weightDeviceReuse         = 15 // Phase B — currently unused
	weightIPASNVelocity       = 10 // Phase B — currently always 0
	weightReports             = 15
	weightBlockRate           = 5
	weightSparkVelocity       = 5

	// Base score. Without any positive signals an empty account starts
	// slightly above 'allow' but well inside it; verification + profile
	// completeness drop it to ~0 quickly.
	riskBaseScore = 30
)

// ComputeRisk aggregates the seven Phase-A signals, persists the
// resulting row, and returns it. Idempotent — re-running on the same
// user just refreshes the existing row's score/level/signals.
func (s *Service) ComputeRisk(ctx context.Context, userID uuid.UUID) (*store.AccountRisk, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user_id required")
	}

	signals := map[string]any{}

	// --- 1. Verification tier — 25w, REDUCES risk. -----------------------
	// phone=0, selfie=0.5, aadhaar=1.0. Missing profile treated as phone.
	verificationContrib := 0.0
	profile, perr := s.store.GetProfile(ctx, userID)
	if perr != nil && !errors.Is(perr, store.ErrProfileNotFound) {
		return nil, fmt.Errorf("load profile: %w", perr)
	}
	if profile != nil {
		switch profile.TrustTier {
		case "aadhaar":
			verificationContrib = 1.0
		case "selfie":
			verificationContrib = 0.5
		default:
			verificationContrib = 0.0
		}
	}
	signals["verification_tier"] = map[string]any{
		"weight":     weightVerification,
		"normalised": verificationContrib,
		"trust_tier": tierOrEmpty(profile),
	}

	// --- 2. Profile completeness — 10w, REDUCES risk. --------------------
	completeness := profileCompleteness(profile)
	signals["profile_completeness"] = map[string]any{
		"weight":     weightProfileCompleteness,
		"normalised": completeness,
	}

	// --- 3. Photo approval state — 15w, both directions. -----------------
	approved, rejected, pending, phErr := s.store.CountPhotosByModerationStatus(ctx, userID)
	if phErr != nil {
		return nil, fmt.Errorf("count photos: %w", phErr)
	}
	photoApprovedContrib, photoRejectedContrib := photoApprovalContrib(approved, rejected, pending)
	signals["photo_approval"] = map[string]any{
		"weight":              weightPhotoApproval,
		"approved":            approved,
		"rejected":            rejected,
		"pending":             pending,
		"approved_normalised": photoApprovedContrib,
		"rejected_normalised": photoRejectedContrib,
	}

	// --- 4. Device reuse — 15w, ADDS risk. ------------------------------
	// Inspect every fingerprint this user has been observed on; if any
	// of them maps to > 3 distinct users we treat the device as
	// recycled across accounts (multi-account abuse pattern). We take
	// the MAX across the user's fingerprints so an alt account that
	// keeps a fresh fingerprint alongside a recycled one still
	// surfaces.
	deviceReuseContrib, maxFPUserCount, fpCount := s.computeDeviceReuseSignal(ctx, userID)
	signals["device_reuse"] = map[string]any{
		"weight":               weightDeviceReuse,
		"normalised":           deviceReuseContrib,
		"fingerprints_seen":    fpCount,
		"max_users_per_device": maxFPUserCount,
	}

	// --- 5. IP / ASN velocity — 10w, ADDS risk. -------------------------
	// COUNT(DISTINCT user_id) on the most-recent IP within the last
	// hour. >5 distinct users on a single IP is an emulator farm /
	// shared NAT / Tor exit pattern.
	ipASNContrib, ipUserCount, observedIP := s.computeIPVelocitySignal(ctx, userID)
	signals["ip_asn_velocity"] = map[string]any{
		"weight":                   weightIPASNVelocity,
		"normalised":               ipASNContrib,
		"users_on_ip_last_hour":    ipUserCount,
		"ip_evaluated":             observedIP != "",
	}

	// --- 6. Report count + quality — 15w, ADDS risk. ---------------------
	reports, rErr := s.store.CountReportsAgainst(ctx, userID)
	if rErr != nil {
		return nil, fmt.Errorf("count reports: %w", rErr)
	}
	actioned, aErr := s.store.CountActionedReportsAgainst(ctx, userID)
	if aErr != nil {
		return nil, fmt.Errorf("count actioned reports: %w", aErr)
	}
	reportContrib := reportQualityContrib(reports, actioned)
	signals["report_quality"] = map[string]any{
		"weight":           weightReports,
		"reports_total":    reports,
		"reports_actioned": actioned,
		"normalised":       reportContrib,
	}

	// --- 7. Block rate (blocker side) — 5w, ADDS risk. -------------------
	blocks, bErr := s.store.CountBlocksOfUser(ctx, userID)
	if bErr != nil {
		return nil, fmt.Errorf("count blocks: %w", bErr)
	}
	blockContrib := blockRateContrib(blocks)
	signals["block_rate"] = map[string]any{
		"weight":     weightBlockRate,
		"blocks":     blocks,
		"normalised": blockContrib,
	}

	// --- 8. Spark velocity — 5w, ADDS risk. ------------------------------
	// Sparks sent in the last hour. The pulse_dating spec defaults to 5
	// sparks/day for free tier; >25 sparks/hour is well outside any
	// human pattern.
	sparks, spErr := s.store.CountSparksLast(ctx, userID, time.Hour)
	if spErr != nil {
		return nil, fmt.Errorf("count sparks: %w", spErr)
	}
	sparkContrib := sparkVelocityContrib(sparks)
	signals["spark_velocity"] = map[string]any{
		"weight":            weightSparkVelocity,
		"sparks_last_hour":  sparks,
		"normalised":        sparkContrib,
	}

	// --- Aggregate. ------------------------------------------------------
	raw := float64(riskBaseScore) -
		(verificationContrib * float64(weightVerification)) -
		(completeness * float64(weightProfileCompleteness)) -
		(photoApprovedContrib * (float64(weightPhotoApproval) / 2)) +
		(photoRejectedContrib * float64(weightPhotoApproval)) +
		(deviceReuseContrib * float64(weightDeviceReuse)) +
		(ipASNContrib * float64(weightIPASNVelocity)) +
		(reportContrib * float64(weightReports)) +
		(blockContrib * float64(weightBlockRate)) +
		(sparkContrib * float64(weightSparkVelocity))

	if raw < 0 {
		raw = 0
	}
	if raw > 100 {
		raw = 100
	}
	score := int(math.Round(raw))
	level := riskLevelForScore(score)

	row := &store.AccountRisk{
		UserID:    userID,
		RiskScore: score,
		RiskLevel: level,
		Signals:   signals,
	}
	if err := s.store.UpsertAccountRisk(ctx, row); err != nil {
		return nil, err
	}
	return row, nil
}

// GetUserRiskLevel is the cheap lookup other code paths call before
// gating an action. Missing row implicitly = RiskLevelAllow.
func (s *Service) GetUserRiskLevel(ctx context.Context, userID uuid.UUID) (string, error) {
	if userID == uuid.Nil {
		return store.RiskLevelAllow, nil
	}
	r, err := s.store.GetAccountRisk(ctx, userID)
	if err != nil {
		return "", err
	}
	if r == nil {
		return store.RiskLevelAllow, nil
	}
	return r.RiskLevel, nil
}

// ListAccountRisksByLevel is the admin-queue wrapper. The HTTP layer
// passes the raw query string through; an empty level returns every
// row regardless.
func (s *Service) ListAccountRisksByLevel(ctx context.Context, level string, limit, offset int) ([]*store.AccountRisk, error) {
	return s.store.ListByLevel(ctx, level, limit, offset)
}

// GetAccountRisk returns the raw row (or nil) for the internal
// /v1/dating/risk/:userId endpoint. Other services (gateway,
// commerce, message) call this before allowing sensitive actions.
func (s *Service) GetAccountRisk(ctx context.Context, userID uuid.UUID) (*store.AccountRisk, error) {
	return s.store.GetAccountRisk(ctx, userID)
}

// --- Signal helpers --------------------------------------------------------

func tierOrEmpty(p *store.Profile) string {
	if p == nil {
		return ""
	}
	return p.TrustTier
}

// profileCompleteness returns a 0..1 fraction of the eight onboarding
// fields that are populated (bio, gender, birth_date, city, country,
// occupation, education, language_prefs).
func profileCompleteness(p *store.Profile) float64 {
	if p == nil {
		return 0
	}
	filled := 0
	total := 8
	if p.Bio != "" {
		filled++
	}
	if p.Gender != nil && *p.Gender != "" {
		filled++
	}
	if p.BirthDate != nil {
		filled++
	}
	if p.City != nil && *p.City != "" {
		filled++
	}
	if p.Country != nil && *p.Country != "" {
		filled++
	}
	if p.Occupation != nil && *p.Occupation != "" {
		filled++
	}
	if p.Education != nil && *p.Education != "" {
		filled++
	}
	if len(p.LanguagePrefs) > 0 {
		filled++
	}
	return float64(filled) / float64(total)
}

// photoApprovalContrib returns (approvedNormalised, rejectedNormalised)
// in [0,1]. approvedNormalised reaches 1.0 with 3+ approved photos;
// rejectedNormalised reaches 1.0 with 3+ rejected photos.
func photoApprovalContrib(approved, rejected, _ int) (float64, float64) {
	a := float64(approved) / 3.0
	if a > 1 {
		a = 1
	}
	r := float64(rejected) / 3.0
	if r > 1 {
		r = 1
	}
	return a, r
}

// reportQualityContrib weighs actioned reports 3x raw reports. Caps at
// 1.0 with ~5 actioned reports or ~15 raw reports.
func reportQualityContrib(total, actioned int) float64 {
	if total <= 0 {
		return 0
	}
	weighted := float64(actioned)*3.0 + float64(total-actioned)*1.0
	c := weighted / 15.0
	if c > 1 {
		c = 1
	}
	return c
}

// blockRateContrib reaches 1.0 at 50 blocks by a single user. A
// well-curated account rarely blocks more than a handful.
func blockRateContrib(blocks int) float64 {
	c := float64(blocks) / 50.0
	if c > 1 {
		c = 1
	}
	return c
}

// sparkVelocityContrib reaches 1.0 at 25 sparks/hour. Free tier hard
// caps at 5/day, so anything approaching 25/hr is automation.
func sparkVelocityContrib(sparksLastHour int) float64 {
	c := float64(sparksLastHour) / 25.0
	if c > 1 {
		c = 1
	}
	return c
}

// riskLevelForScore maps the clamped 0..100 score onto the seven-band
// enforcement ladder. Conservative bands: a freshly-onboarded account
// (no photos, no verification, no reports) sits comfortably inside
// 'allow'; the bands grow tighter as the score climbs because the
// signals stacking that high indicate aggregated abuse.
func riskLevelForScore(score int) string {
	switch {
	case score <= 30:
		return store.RiskLevelAllow
	case score <= 50:
		return store.RiskLevelReduceReach
	case score <= 65:
		return store.RiskLevelRequireRecheck
	case score <= 75:
		return store.RiskLevelHideFromDiscovery
	case score <= 85:
		return store.RiskLevelChatHold
	case score <= 95:
		return store.RiskLevelAdminReview
	default:
		return store.RiskLevelSuspend
	}
}

// computeDeviceReuseSignal walks the user's recent fingerprints and
// returns the saturating 0..1 contribution along with diagnostic
// values for the signals JSON. A fingerprint that has carried more
// than 3 distinct users contributes 1.0; below 3 the value scales
// linearly. We pick the MAX across the user's fingerprints so an alt
// account that keeps a fresh fingerprint alongside a recycled one
// still surfaces.
//
// Errors are best-effort: a Postgres blip during risk recompute must
// not turn the score into NaN. We log and treat the signal as 0.
func (s *Service) computeDeviceReuseSignal(ctx context.Context, userID uuid.UUID) (normalised float64, maxUsersPerDevice int, fingerprintsSeen int) {
	fps, err := s.store.ListFingerprintsForUser(ctx, userID)
	if err != nil {
		slog.Warn("device reuse: list fingerprints failed", "user_id", userID, "error", err)
		return 0, 0, 0
	}
	if len(fps) == 0 {
		return 0, 0, 0
	}
	for _, fp := range fps {
		count, cerr := s.store.CountUsersByFingerprint(ctx, fp.Fingerprint)
		if cerr != nil {
			slog.Warn("device reuse: count users by fingerprint failed",
				"user_id", userID, "error", cerr)
			continue
		}
		if count > maxUsersPerDevice {
			maxUsersPerDevice = count
		}
	}
	return deviceReuseContrib(maxUsersPerDevice), maxUsersPerDevice, len(fps)
}

// computeIPVelocitySignal looks at the user's most-recently-seen
// fingerprint row, pulls the IP, and counts distinct users on that IP
// in the last hour. >5 → 1.0. <=5 scales linearly.
//
// We use the latest IP (rather than every IP the user has ever been
// on) because the abuse signature we're looking for is *now*: an
// emulator farm spinning up alt accounts on one bridge in the last
// few minutes. Older IPs aren't relevant.
func (s *Service) computeIPVelocitySignal(ctx context.Context, userID uuid.UUID) (normalised float64, usersOnIP int, observedIP string) {
	fps, err := s.store.ListFingerprintsForUser(ctx, userID)
	if err != nil {
		slog.Warn("ip velocity: list fingerprints failed", "user_id", userID, "error", err)
		return 0, 0, ""
	}
	// ListFingerprintsForUser orders by last_seen_at DESC, so fps[0]
	// is the most recent. Walk the list to find the first row with a
	// non-empty IP (some upserts may have landed before the client
	// started sending forwarded-for).
	for _, fp := range fps {
		if fp.IP == "" {
			continue
		}
		observedIP = fp.IP
		break
	}
	if observedIP == "" {
		return 0, 0, ""
	}
	count, cerr := s.store.CountDistinctUsersOnIPLastHour(ctx, observedIP)
	if cerr != nil {
		slog.Warn("ip velocity: count distinct users failed",
			"user_id", userID, "error", cerr)
		return 0, 0, observedIP
	}
	return ipVelocityContrib(count), count, observedIP
}

// deviceReuseContrib normalises users-per-device into the 0..1 risk
// contribution. The spec threshold is > 3 distinct users → 1.0; at
// exactly 3 we treat the device as borderline (just under 1.0). The
// linear scaling below 3 keeps the signal continuous so a 2-user
// device still contributes a meaningful nudge instead of zero.
func deviceReuseContrib(usersOnDevice int) float64 {
	if usersOnDevice <= 1 {
		return 0
	}
	// > 3 saturates per spec; the divisor 3.0 gives 3 → 1.0 exactly.
	c := float64(usersOnDevice-1) / 3.0
	if c > 1 {
		c = 1
	}
	return c
}

// ipVelocityContrib normalises distinct-users-on-IP-last-hour into
// the 0..1 contribution. Spec threshold: > 5 distinct users → 1.0.
// 5 is the saturating denominator so 5 users → 1.0 exactly.
func ipVelocityContrib(usersOnIP int) float64 {
	if usersOnIP <= 1 {
		return 0
	}
	c := float64(usersOnIP-1) / 5.0
	if c > 1 {
		c = 1
	}
	return c
}

// RecomputeStaleRisks is the sweeper hook — runs ComputeRisk on every
// user whose last_evaluated_at is older than `staleAfter`, capped at
// `limit`. Errors on individual users are logged + skipped so one
// bad row never breaks the sweep.
func (s *Service) RecomputeStaleRisks(ctx context.Context, staleAfter time.Duration, limit int) (int, error) {
	users, err := s.store.ListStaleForRecompute(ctx, staleAfter, limit)
	if err != nil {
		return 0, err
	}
	done := 0
	for _, id := range users {
		if _, err := s.ComputeRisk(ctx, id); err != nil {
			slog.Warn("recompute risk failed", "user_id", id, "error", err)
			continue
		}
		done++
	}
	return done, nil
}
