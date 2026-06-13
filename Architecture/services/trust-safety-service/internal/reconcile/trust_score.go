// Package reconcile holds trust-safety background jobs.
//
// trust_score.go implements the periodic, READ-ONLY trust-score recompute job
// described in messaging/privacy spec §8.11, §10.1 and §10.2.
//
// Phase 1 scope:
//   - The job only computes trust_score / trust_tier and writes them back to
//     trust.user_trust_state. It performs NO enforcement and changes NO
//     behavior anywhere else in the platform.
//   - Several §10.1 signals (verified_phone, verified_email, has_profile_photo,
//     has_bio, connection_count, followers_count, connection_accept_ratio,
//     spam_pattern_signals, device_risk_signals, unusual_bulk_messaging_flags)
//     live in other services. trust-safety-service cannot cheaply obtain them,
//     so they default to 0. The formula shape and every weight are kept as
//     named constants below so those signals can be wired in later without a
//     formula rewrite.
package reconcile

import (
	"context"
	"log/slog"
	"time"

	"github.com/atpost/trust-safety-service/internal/store/postgres"
)

// ─── §10.1 formula constants ──────────────────────────────────────────────────
//
// score = clamp(0,100, BaseScore
//   + min(AccountAgeCap, account_age_days*AccountAgePerDay)
//   + VerifiedPhonePts*verified_phone + VerifiedEmailPts*verified_email
//   + ProfilePhotoPts*has_profile_photo + BioPts*has_bio
//   + ConnectionThresholdPts*(connection_count>=ConnectionThreshold)
//   + FollowersThresholdPts*(followers_count>=FollowersThreshold)
//   + ConnectionAcceptRatioPts*connection_accept_ratio
//   - BlocksPenalty*blocks_received_last_30d
//   - ReportsUpheldPenalty*reports_upheld_last_30d
//   - ReportsPendingPenalty*reports_pending_last_30d
//   - SpamSignalPenalty*spam_pattern_signals
//   - DeviceRiskPenalty*device_risk_signals
//   - BulkMessagingPenalty*unusual_bulk_messaging_flags)
const (
	BaseScore = 50.0

	// Positive: account maturity.
	AccountAgePerDay = 0.5
	AccountAgeCap    = 30.0

	// Positive: profile completeness / verification (signals default to 0).
	VerifiedPhonePts = 5.0
	VerifiedEmailPts = 5.0
	ProfilePhotoPts  = 3.0
	BioPts           = 3.0

	// Positive: social graph (signals default to 0).
	ConnectionThreshold    = 10
	ConnectionThresholdPts = 5.0
	FollowersThreshold     = 50
	FollowersThresholdPts  = 5.0

	// Positive: connection accept ratio (0..1, signal defaults to 0).
	ConnectionAcceptRatioPts = 10.0

	// Negative: abuse signals over the trailing 30 days.
	BlocksPenalty         = 8.0
	ReportsUpheldPenalty  = 10.0
	ReportsPendingPenalty = 5.0

	// Negative: behavioral risk signals (default to 0).
	SpamSignalPenalty    = 6.0
	DeviceRiskPenalty    = 4.0
	BulkMessagingPenalty = 3.0

	// Score clamp bounds.
	MinScore = 0.0
	MaxScore = 100.0
)

// ─── §10.2 tier thresholds ────────────────────────────────────────────────────
const (
	// Accounts younger than this are always tier 'new'.
	NewAccountAgeDays = 7

	// Score bands (inclusive lower bounds).
	TierLowMax      = 34 // 0..34   -> low
	TierStandardMax = 69 // 35..69  -> standard
	// 70..100 -> trusted. Tier 'verified' is a manual flag only; the recompute
	// job never auto-assigns it (caps auto-assignment at 'trusted').

	TierNew      = "new"
	TierLow      = "low"
	TierStandard = "standard"
	TierTrusted  = "trusted"
)

// ─── Recompute job ────────────────────────────────────────────────────────────

// RecomputeInterval is how often the trust-score job runs.
const RecomputeInterval = 6 * time.Hour

// RecomputeBatchSize bounds how many users are recomputed per tick.
const RecomputeBatchSize = 500

// TrustScoreReconciler periodically recomputes trust_score / trust_tier for
// users that have a trust.user_trust_state row.
type TrustScoreReconciler struct {
	store *postgres.TrustStateStore
}

// NewTrustScoreReconciler constructs a TrustScoreReconciler.
func NewTrustScoreReconciler(store *postgres.TrustStateStore) *TrustScoreReconciler {
	return &TrustScoreReconciler{store: store}
}

// Start runs the recompute job on a ticker until ctx is cancelled.
// Call it in a goroutine.
func (r *TrustScoreReconciler) Start(ctx context.Context) {
	ticker := time.NewTicker(RecomputeInterval)
	defer ticker.Stop()

	// Run once shortly after boot so a fresh process does not wait a full
	// interval before the first recompute.
	r.recompute(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.recompute(ctx)
		}
	}
}

// recompute performs a single recompute pass over a batch of users.
func (r *TrustScoreReconciler) recompute(ctx context.Context) {
	userIDs, err := r.store.ListTrustStateUserIDs(ctx, RecomputeBatchSize)
	if err != nil {
		slog.Error("trust-score recompute: list users failed", "error", err)
		return
	}
	if len(userIDs) == 0 {
		slog.Info("trust-score recompute: no users to recompute")
		return
	}

	inputs, err := r.store.CollectRecomputeInputs(ctx, userIDs)
	if err != nil {
		slog.Error("trust-score recompute: collect inputs failed", "error", err)
		return
	}

	var updated, failed int
	for _, in := range inputs {
		score := ComputeTrustScore(in)
		tier := ComputeTrustTier(score, in.AccountAgeDays)

		st := &postgres.UserTrustState{
			UserID:          in.UserID,
			TrustScore:      score,
			TrustTier:       tier,
			AccountAgeDays:  in.AccountAgeDays,
			ReportsReceived: in.ReportsReceived,
			BlocksReceived:  in.BlocksReceived,
			// connection_accept_ratio is a foreign signal (defaults to 0/NULL).
		}
		if err := r.store.UpsertTrustScore(ctx, st); err != nil {
			slog.Error("trust-score recompute: upsert failed", "user_id", in.UserID, "error", err)
			failed++
			continue
		}
		updated++
	}

	slog.Info("trust-score recompute complete", "updated", updated, "failed", failed)
}

// ComputeTrustScore applies the §10.1 formula to the locally-derived signals.
// Foreign signals not present on TrustRecomputeInput are treated as 0.
func ComputeTrustScore(in *postgres.TrustRecomputeInput) int {
	score := BaseScore

	// Positive — account maturity (locally available).
	ageBonus := float64(in.AccountAgeDays) * AccountAgePerDay
	if ageBonus > AccountAgeCap {
		ageBonus = AccountAgeCap
	}
	score += ageBonus

	// Positive — verification / profile / graph / accept-ratio signals are all
	// foreign to trust-safety-service and default to 0 in Phase 1:
	//   + VerifiedPhonePts*0 + VerifiedEmailPts*0 + ProfilePhotoPts*0 + BioPts*0
	//   + ConnectionThresholdPts*0 + FollowersThresholdPts*0
	//   + ConnectionAcceptRatioPts*0

	// Negative — abuse signals over the trailing 30 days (locally available).
	score -= BlocksPenalty * float64(in.BlocksReceived30d)
	score -= ReportsUpheldPenalty * float64(in.ReportsUpheld30d)
	score -= ReportsPendingPenalty * float64(in.ReportsPending30d)

	// Negative — spam / device-risk / bulk-messaging signals are foreign and
	// default to 0 in Phase 1:
	//   - SpamSignalPenalty*0 - DeviceRiskPenalty*0 - BulkMessagingPenalty*0

	return clamp(score)
}

// ComputeTrustTier maps a score to a tier per §10.2. Accounts younger than
// NewAccountAgeDays are always 'new'. Auto-assignment is capped at 'trusted';
// 'verified' is a manual flag only.
func ComputeTrustTier(score, accountAgeDays int) string {
	if accountAgeDays < NewAccountAgeDays {
		return TierNew
	}
	switch {
	case score <= TierLowMax:
		return TierLow
	case score <= TierStandardMax:
		return TierStandard
	default:
		return TierTrusted
	}
}

// clamp bounds a float score to [MinScore, MaxScore] and rounds to an int.
func clamp(score float64) int {
	if score < MinScore {
		score = MinScore
	}
	if score > MaxScore {
		score = MaxScore
	}
	// Round to nearest integer for SMALLINT storage.
	return int(score + 0.5)
}
