// Verification service — Aadhaar/DigiLocker + server-side blink selfie.
//
// DPDP Act compliant — see PULSE_DATING_SPEC.md §15.8
// Aadhaar number is NEVER stored or logged. The service touches only:
//   - DigiLocker assertion id (digilocker_ref)
//   - SHA-256 hash of the document-type label
//   - Verification timestamp
//
// Selfie verification (lane D5) is REQUIRED before a profile reaches
// 'active'. It is decided server-side from an uploaded video: the client
// requests a "blink_twice" challenge, records a short video, uploads it to
// media-service and submits {challenge_id, video_media_id}; media-service
// counts the blinks, checks one consistent face and compares it with the
// approved primary photo. Client-computed embeddings are refused (410) and
// no embedding is stored.
//
// Trust tiers step up phone -> selfie -> aadhaar; never demote.
package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"os"
	"time"

	"github.com/atpost/dating-service/internal/digilocker"
	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// AadhaarFlowStart is returned by StartAadhaarFlow to the mobile client.
type AadhaarFlowStart struct {
	DigiLockerAuthorizeURL string `json:"digilocker_authorize_url"`
	State                  string `json:"state"`
}

// AadhaarFlowResult is returned to the client after a successful callback.
type AadhaarFlowResult struct {
	Verified  bool      `json:"verified"`
	TrustTier string    `json:"trust_tier"`
	IssuedAt  time.Time `json:"issued_at"`
}

// ErrAadhaarDisabled: DIGILOCKER_MODE=disabled (no partner client wired).
// Aadhaar is optional; the selfie is the required step.
var ErrAadhaarDisabled = errors.New("aadhaar verification is not enabled on this deployment")

// SetDigiLockerClient injects the partner client. main.go selects HTTP, mock
// (local/dev only) or none via DIGILOCKER_MODE.
func (s *Service) SetDigiLockerClient(c digilocker.Client) {
	s.digilockerClient = c
}

// digilockerStateTTL is the spec-required PKCE state lifetime (10 min).
const digilockerStateTTL = 10 * time.Minute

// stateKey returns the Redis key for a PKCE state nonce.
func stateKey(state string) string { return "dating:digilocker:state:" + state }

// generateState returns a 32-byte URL-safe random nonce.
func generateState() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// StartAadhaarFlow allocates a one-shot state nonce, stores it in Redis
// keyed by the user, and returns the DigiLocker authorize URL the mobile
// app should open.
//
// DPDP Act compliant — see PULSE_DATING_SPEC.md §15.8
func (s *Service) StartAadhaarFlow(ctx context.Context, userID uuid.UUID) (*AadhaarFlowStart, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: userID required")
	}
	if s.digilockerClient == nil {
		return nil, ErrAadhaarDisabled
	}
	clientID := os.Getenv("DIGILOCKER_CLIENT_ID")
	redirectURI := os.Getenv("DIGILOCKER_REDIRECT_URI")
	authorizeBase := os.Getenv("DIGILOCKER_AUTHORIZE_URL")
	if authorizeBase == "" {
		authorizeBase = "https://api.digitallocker.gov.in/public/oauth2/1/authorize"
	}
	state, err := generateState()
	if err != nil {
		return nil, err
	}
	if s.rdb != nil {
		if err := s.rdb.Set(ctx, stateKey(state), userID.String(), digilockerStateTTL).Err(); err != nil {
			return nil, fmt.Errorf("persist state: %w", err)
		}
	}
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("scope", "aadhaar")
	authorizeURL := authorizeBase + "?" + q.Encode()

	if s.producer != nil {
		// Submission attempt (the user has clicked "verify with Aadhaar").
		if perr := s.producer.PublishVerificationSubmitted(ctx, userID, "aadhaar"); perr != nil {
			slog.Warn("publish verification.submitted failed", "error", perr)
		}
	}
	return &AadhaarFlowStart{DigiLockerAuthorizeURL: authorizeURL, State: state}, nil
}

// CompleteAadhaarFlow validates the PKCE state via Redis, exchanges the
// code with the partner, and persists the assertion. On success, trust
// tier is bumped to 'aadhaar' and dating.verification.completed is emitted.
//
// DPDP Act compliant — see PULSE_DATING_SPEC.md §15.8
func (s *Service) CompleteAadhaarFlow(ctx context.Context, userID uuid.UUID, code, state string) (*AadhaarFlowResult, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: userID required")
	}
	if code == "" || state == "" {
		return nil, fmt.Errorf("invalid: code and state required")
	}
	if s.digilockerClient == nil {
		return nil, ErrAadhaarDisabled
	}
	if s.rdb != nil {
		stored, err := s.rdb.Get(ctx, stateKey(state)).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				return nil, fmt.Errorf("forbidden: state expired or invalid")
			}
			return nil, fmt.Errorf("lookup state: %w", err)
		}
		if stored != userID.String() {
			return nil, fmt.Errorf("forbidden: state does not match user")
		}
		// One-shot: remove regardless of outcome below.
		if err := s.rdb.Del(ctx, stateKey(state)).Err(); err != nil {
			slog.Warn("clear state key", "error", err)
		}
	}
	assertion, err := s.digilockerClient.ExchangeCode(ctx, code, state)
	if err != nil {
		return nil, fmt.Errorf("digilocker exchange: %w", err)
	}
	if assertion == nil || assertion.Reference == "" {
		return nil, fmt.Errorf("digilocker: empty assertion")
	}
	docHash := digilocker.HashDocumentType(assertion.DocumentType)
	if err := s.store.RecordAadhaarVerification(ctx, userID, assertion.Reference, docHash); err != nil {
		return nil, fmt.Errorf("persist verification: %w", err)
	}
	if err := s.store.UpdateTrustTier(ctx, userID, "aadhaar"); err != nil {
		return nil, fmt.Errorf("bump trust tier: %w", err)
	}
	if s.producer != nil {
		if perr := s.producer.PublishVerificationCompleted(ctx, userID, "aadhaar", "aadhaar"); perr != nil {
			slog.Warn("publish verification.completed failed", "error", perr)
		}
	}
	return &AadhaarFlowResult{
		Verified:  true,
		TrustTier: "aadhaar",
		IssuedAt:  assertion.IssuedAt,
	}, nil
}

// ── Selfie verification (lane D5, blink liveness) ───────────────────────────

// Selfie thresholds and limits. Similarity is media-service's 0-100 score.
const (
	DefaultSelfiePassThreshold   = 90.0
	DefaultSelfieReviewThreshold = 80.0
	DefaultSelfieMaxAttempts     = 5
	DefaultSelfieRequiredBlinks  = 2
	DefaultSelfieMaxVideoMs      = 4000
	// SelfieChallengeTTL is how long a liveness challenge stays usable.
	SelfieChallengeTTL = 10 * time.Minute
	// selfieChallengesPerAttempt caps issued challenges at this multiple of
	// the attempt limit per window, so issuance cannot grow without bound.
	selfieChallengesPerAttempt = 4
)

// SelfieInstructionBlinkTwice is the challenge instruction.
const SelfieInstructionBlinkTwice = store.SelfieInstructionBlinkTwice

// Client-facing reason codes. The internal reason for a moderator review is
// kept for moderators and never returned to the user.
const (
	SelfieReasonNoFace          = "NO_FACE"
	SelfieReasonMultipleFaces   = "MULTIPLE_FACES"
	SelfieReasonFaceChanged     = "FACE_CHANGED"
	SelfieReasonLowQuality      = "LOW_QUALITY"
	SelfieReasonNotEnoughBlinks = "NOT_ENOUGH_BLINKS"
	SelfieReasonNoMatch         = "NO_MATCH"
	SelfieReasonManualReview    = "MANUAL_REVIEW"

	mediaReasonVideoTooLong   = "VIDEO_TOO_LONG"
	selfieReviewBorderline    = "BORDERLINE_SIMILARITY"
	selfieReviewHighRiskFirst = "HIGH_RISK_FIRST_ATTEMPT"
	selfieFailLowSimilarity   = "LOW_SIMILARITY"
)

// SelfieConfig holds the decision bars and limits.
//
//	not one consistent face (NO_FACE, MULTIPLE_FACES, FACE_CHANGED,
//	LOW_QUALITY)                                   → failed
//	blinks < RequiredBlinks                        → failed NOT_ENOUGH_BLINKS
//	similarity >= PassThreshold                    → passed (pending_review
//	                                                  for a high-risk
//	                                                  account's first attempt)
//	ReviewThreshold <= similarity < PassThreshold  → pending_review
//	similarity < ReviewThreshold                   → failed NO_MATCH
//	video longer than media-service allows         → refused (no verdict)
type SelfieConfig struct {
	PassThreshold      float64
	ReviewThreshold    float64
	MaxAttemptsPerDay  int
	RequiredBlinks     int
	MaxVideoDurationMs int
}

// DefaultSelfieConfig is 90 / 80 / 5 per 24h / 2 blinks / 4000 ms.
func DefaultSelfieConfig() SelfieConfig {
	return SelfieConfig{
		PassThreshold:      DefaultSelfiePassThreshold,
		ReviewThreshold:    DefaultSelfieReviewThreshold,
		MaxAttemptsPerDay:  DefaultSelfieMaxAttempts,
		RequiredBlinks:     DefaultSelfieRequiredBlinks,
		MaxVideoDurationMs: DefaultSelfieMaxVideoMs,
	}
}

// SetSelfieConfig installs the selfie bars (boot_config.ResolveSelfieConfig).
// An invalid config is replaced by the defaults rather than weakening them.
func (s *Service) SetSelfieConfig(c SelfieConfig) {
	if c.PassThreshold <= 0 || c.PassThreshold > 100 || c.ReviewThreshold <= 0 ||
		c.ReviewThreshold >= c.PassThreshold || c.MaxAttemptsPerDay <= 0 ||
		c.RequiredBlinks < 1 || c.MaxVideoDurationMs <= 0 {
		slog.Warn("selfie config rejected; using defaults", "config", c)
		c = DefaultSelfieConfig()
	}
	s.selfieCfg = c
}

// SelfieSettings returns the active selfie configuration.
func (s *Service) SelfieSettings() SelfieConfig {
	if s.selfieCfg.MaxAttemptsPerDay == 0 {
		return DefaultSelfieConfig()
	}
	return s.selfieCfg
}

var (
	// ErrPrimaryPhotoNotApproved: no primary photo, or it is not yet
	// moderation-approved. The selfie is compared only against an approved
	// photo.
	ErrPrimaryPhotoNotApproved = errors.New("primary photo is not moderation-approved")
	// ErrSelfieSameAsPrimaryPhoto: the submitted media is the primary photo.
	ErrSelfieSameAsPrimaryPhoto = errors.New("the selfie video must be a new recording, not the primary profile photo")
	// ErrSelfieMediaNotFound: media-service does not know the video (or the
	// primary photo) as the caller's ready media.
	ErrSelfieMediaNotFound = errors.New("selfie media not found")
	// ErrSelfieVideoUnsupported: media-service cannot analyse the upload.
	ErrSelfieVideoUnsupported = errors.New("selfie video cannot be analysed")
	// ErrSelfieVideoTooLong: the recording is longer than allowed.
	ErrSelfieVideoTooLong = errors.New("selfie video is longer than allowed")
	// ErrFaceCompareUnavailable: no liveness result (media-service or the
	// provider unavailable). Never treated as pass or fail.
	ErrFaceCompareUnavailable = errors.New("selfie liveness check is unavailable")
)

// SelfieChallengeResponse is returned by CreateSelfieChallenge.
type SelfieChallengeResponse struct {
	ChallengeID   uuid.UUID `json:"challenge_id"`
	Instruction   string    `json:"instruction"`
	MaxDurationMs int       `json:"max_duration_ms"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// SelfieFlowResult is returned by SubmitSelfie and ReviewSelfie. The raw
// similarity is deliberately not returned, so a client cannot tune an upload
// against the score.
type SelfieFlowResult struct {
	Status            string `json:"status"` // passed | failed | pending_review
	Passed            bool   `json:"passed"`
	Reason            string `json:"reason,omitempty"`
	TrustTier         string `json:"trust_tier,omitempty"`
	ProfileStatus     string `json:"profile_status,omitempty"`
	AttemptsRemaining int    `json:"attempts_remaining"`
}

// approvedPrimaryPhoto returns the user's primary photo when it is approved.
func (s *Service) approvedPrimaryPhoto(ctx context.Context, userID uuid.UUID) (*store.Photo, error) {
	photos, err := s.store.ListPhotos(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("load photos: %w", err)
	}
	for i := range photos {
		if !photos[i].IsPrimary {
			continue
		}
		if photos[i].ModerationStatus != "approved" {
			return nil, ErrPrimaryPhotoNotApproved
		}
		return &photos[i], nil
	}
	return nil, ErrPrimaryPhotoNotApproved
}

// CreateSelfieChallenge issues a single-use "blink_twice" challenge (10
// minute expiry, max_duration_ms recording) for the caller. Refused when the
// primary photo is not approved, the selfie already passed or is in review,
// or the attempt limit is used up.
func (s *Service) CreateSelfieChallenge(ctx context.Context, userID uuid.UUID) (*SelfieChallengeResponse, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: userID required")
	}
	// Lane D9: the liveness / face check is biometric processing.
	if err := s.requireConsent(ctx, userID, ConsentBiometricSelfie); err != nil {
		return nil, err
	}
	if _, err := s.store.GetProfile(ctx, userID); err != nil {
		return nil, err
	}
	if _, err := s.approvedPrimaryPhoto(ctx, userID); err != nil {
		return nil, err
	}
	cfg := s.SelfieSettings()
	ch, err := s.store.CreateSelfieChallenge(ctx, userID, SelfieInstructionBlinkTwice, cfg.MaxVideoDurationMs,
		SelfieChallengeTTL, cfg.MaxAttemptsPerDay, cfg.MaxAttemptsPerDay*selfieChallengesPerAttempt)
	if err != nil {
		return nil, err
	}
	return &SelfieChallengeResponse{ChallengeID: ch.ID, Instruction: ch.Instruction,
		MaxDurationMs: ch.MaxDurationMs, ExpiresAt: ch.ExpiresAt.UTC()}, nil
}

// SubmitSelfie verifies an uploaded blink video against the approved primary
// photo.
//
// Order: profile exists → primary photo approved (409) and not the submitted
// media → under a per-user lock, not already passed / in review (409),
// attempt limit (429), challenge consumed (400) and attempt recorded →
// media-service liveness (404 / 422 / 503 / over-long video leave an 'error'
// attempt) → decision (see SelfieConfig):
//
//   - passed: selfie_status=passed, trust tier ≥ selfie, and pending_selfie →
//     active through TransitionProfileStatus (advanceOnboarding);
//   - pending_review: a moderator decides (ReviewSelfie);
//   - failed: selfie_status=failed; the profile stays pending_selfie.
func (s *Service) SubmitSelfie(ctx context.Context, userID, videoMediaID, challengeID uuid.UUID) (*SelfieFlowResult, error) {
	if userID == uuid.Nil || videoMediaID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user and video_media_id required")
	}
	// Lane D9: no check runs after the biometric consent is withdrawn.
	if err := s.requireConsent(ctx, userID, ConsentBiometricSelfie); err != nil {
		return nil, err
	}
	if challengeID == uuid.Nil {
		return nil, store.ErrSelfieChallengeInvalid
	}
	if _, err := s.store.GetProfile(ctx, userID); err != nil {
		return nil, err
	}
	primary, err := s.approvedPrimaryPhoto(ctx, userID)
	if err != nil {
		return nil, err
	}
	if primary.MediaID == videoMediaID {
		return nil, ErrSelfieSameAsPrimaryPhoto
	}
	// Lane D10: a video media-service has not finished with is a 409
	// MEDIA_NOT_READY before any attempt is consumed, so the client retries
	// the same upload instead of burning an attempt.
	if err := s.requireReadySelfieVideo(ctx, userID, videoMediaID); err != nil {
		return nil, err
	}
	cfg := s.SelfieSettings()
	start, err := s.store.BeginSelfieAttempt(ctx, userID, challengeID, videoMediaID, cfg.MaxAttemptsPerDay)
	if err != nil {
		return nil, err
	}
	remaining := cfg.MaxAttemptsPerDay - start.UsedInWindow
	if remaining < 0 {
		remaining = 0
	}
	noVerdict := func(cause error, reason string) error {
		if ferr := s.store.FinishSelfieAttempt(ctx, userID, start.AttemptID, videoMediaID,
			store.SelfieDecision{Outcome: store.SelfieOutcomeError, Reason: reason}); ferr != nil {
			slog.Warn("selfie: record error outcome failed", "user_id", userID, "attempt_id", start.AttemptID, "error", ferr)
		}
		return cause
	}
	if s.livenessClient == nil {
		return nil, noVerdict(ErrFaceCompareUnavailable, "LIVENESS_CLIENT_NOT_CONFIGURED")
	}
	res, err := s.livenessClient.CheckLiveness(ctx, LivenessRequest{
		VideoMediaID:     videoMediaID,
		ReferenceMediaID: primary.MediaID,
		RequesterUserID:  userID,
	})
	switch {
	case errors.Is(err, ErrSelfieMediaNotFound):
		return nil, noVerdict(ErrSelfieMediaNotFound, "MEDIA_NOT_FOUND")
	case errors.Is(err, ErrSelfieVideoUnsupported):
		return nil, noVerdict(ErrSelfieVideoUnsupported, "VIDEO_UNSUPPORTED")
	case err != nil:
		slog.Warn("selfie: liveness unavailable", "user_id", userID, "attempt_id", start.AttemptID, "error", err)
		return nil, noVerdict(ErrFaceCompareUnavailable, "PROVIDER_UNAVAILABLE")
	case res.Reason == mediaReasonVideoTooLong:
		return nil, noVerdict(ErrSelfieVideoTooLong, mediaReasonVideoTooLong)
	}

	reviewFirst := start.PriorVerdicts == 0 && s.selfieHighRisk(ctx, userID)
	decision, clientReason := decideSelfie(cfg, res, reviewFirst)
	if err := s.store.FinishSelfieAttempt(ctx, userID, start.AttemptID, videoMediaID, decision); err != nil {
		return nil, fmt.Errorf("persist selfie outcome: %w", err)
	}
	slog.Info("selfie verification decided", "user_id", userID, "attempt_id", start.AttemptID,
		"outcome", decision.Outcome, "reason", decision.Reason, "blinks", res.BlinksDetected, "provider", decision.Provider)

	out := &SelfieFlowResult{Status: decision.Outcome, Reason: clientReason, AttemptsRemaining: remaining}
	switch decision.Outcome {
	case store.SelfieOutcomePassed:
		out.Passed = true
		out.TrustTier = s.completeSelfiePass(ctx, userID)
	case store.SelfieOutcomePendingReview:
		if s.producer != nil {
			if perr := s.producer.PublishVerificationSubmitted(ctx, userID, "selfie"); perr != nil {
				slog.Warn("publish verification.submitted failed", "error", perr)
			}
		}
	default:
		if s.producer != nil {
			if perr := s.producer.PublishVerificationRejected(ctx, userID, "selfie"); perr != nil {
				slog.Warn("publish verification.rejected failed", "error", perr)
			}
		}
	}
	if p, err := s.store.GetProfile(ctx, userID); err == nil && p != nil {
		out.ProfileStatus = p.ProfileStatus
		if out.TrustTier == "" {
			out.TrustTier = p.TrustTier
		}
	}
	return out, nil
}

// decideSelfie maps a liveness result to an outcome. It returns the decision
// to persist and the reason code shown to the client.
func decideSelfie(cfg SelfieConfig, res *LivenessResult, reviewFirst bool) (store.SelfieDecision, string) {
	blinks, frames := res.BlinksDetected, res.FramesAnalysed
	d := store.SelfieDecision{Provider: res.Provider, Blinks: &blinks, FramesAnalysed: &frames}
	fail := func(reason string) (store.SelfieDecision, string) {
		d.Outcome, d.Reason = store.SelfieOutcomeFailed, reason
		return d, reason
	}
	switch res.Reason {
	case SelfieReasonNoFace, SelfieReasonMultipleFaces, SelfieReasonFaceChanged, SelfieReasonLowQuality:
		return fail(res.Reason)
	}
	switch {
	case !res.SingleFace:
		return fail(SelfieReasonMultipleFaces)
	case !res.SameFaceAcrossFrames:
		return fail(SelfieReasonFaceChanged)
	case res.Reason == SelfieReasonNotEnoughBlinks || res.BlinksDetected < cfg.RequiredBlinks:
		return fail(SelfieReasonNotEnoughBlinks)
	case res.Reason != "":
		// An unknown reason from media-service is never a pass.
		return fail(SelfieReasonLowQuality)
	}
	sim := math.Round(res.Similarity)
	if sim < 0 {
		sim = 0
	}
	if sim > 100 {
		sim = 100
	}
	d.Similarity = &sim
	switch {
	case sim >= cfg.PassThreshold && reviewFirst:
		d.Outcome, d.Reason = store.SelfieOutcomePendingReview, selfieReviewHighRiskFirst
		return d, SelfieReasonManualReview
	case sim >= cfg.PassThreshold:
		d.Outcome = store.SelfieOutcomePassed
		return d, ""
	case sim >= cfg.ReviewThreshold:
		d.Outcome, d.Reason = store.SelfieOutcomePendingReview, selfieReviewBorderline
		return d, SelfieReasonManualReview
	default:
		d.Outcome, d.Reason = store.SelfieOutcomeFailed, selfieFailLowSimilarity
		return d, SelfieReasonNoMatch
	}
}

// selfieHighRisk reports whether the account's risk level keeps its first
// selfie from passing automatically. A failed lookup counts as high risk.
func (s *Service) selfieHighRisk(ctx context.Context, userID uuid.UUID) bool {
	level, err := s.GetUserRiskLevel(ctx, userID)
	if err != nil {
		slog.Warn("selfie: risk lookup failed; treating as high risk", "user_id", userID, "error", err)
		return true
	}
	switch level {
	case "", store.RiskLevelAllow, store.RiskLevelReduceReach:
		return false
	}
	return true
}

// completeSelfiePass bumps the trust tier (never demoting aadhaar), emits
// verification.completed and graduates pending_selfie → active through the
// status machine. A restricted/suspended profile keeps its hold (only its
// remembered step advances). Returns the resulting trust tier.
func (s *Service) completeSelfiePass(ctx context.Context, userID uuid.UUID) string {
	tier := "selfie"
	v, _ := s.store.GetVerification(ctx, userID)
	if v != nil && v.AadhaarStatus != nil && *v.AadhaarStatus == "verified" {
		tier = "aadhaar"
	} else if err := s.store.UpdateTrustTier(ctx, userID, "selfie"); err != nil {
		slog.Warn("selfie: bump trust tier failed", "user_id", userID, "error", err)
	}
	if s.producer != nil {
		if perr := s.producer.PublishVerificationCompleted(ctx, userID, "selfie", tier); perr != nil {
			slog.Warn("publish verification.completed failed", "error", perr)
		}
	}
	if _, err := s.advanceOnboarding(ctx, userID); err != nil && !errors.Is(err, store.ErrProfileNotFound) {
		slog.Warn("profile state: advance onboarding after selfie failed", "user_id", userID, "error", err)
	}
	return tier
}

// ListSelfieReviews is the moderator queue (GET /v1/dating/admin/verification/selfie/pending).
func (s *Service) ListSelfieReviews(ctx context.Context, limit int) ([]*store.SelfieReview, error) {
	return s.store.ListPendingSelfieReviews(ctx, limit)
}

// ReviewSelfie applies a moderator decision to a selfie in review.
// decision is "approve" (same as a pass: active through the writer) or
// "reject" (same as a fail). The admin action is audited.
func (s *Service) ReviewSelfie(ctx context.Context, adminID, userID uuid.UUID, decision, note string) (*SelfieFlowResult, error) {
	if adminID == uuid.Nil {
		return nil, fmt.Errorf("forbidden: admin actor required")
	}
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user id required")
	}
	// Lane D9: a withdrawn biometric consent stops a pending check too.
	if err := s.requireConsent(ctx, userID, ConsentBiometricSelfie); err != nil {
		return nil, err
	}
	var approve bool
	switch decision {
	case "approve":
		approve = true
	case "reject":
	default:
		return nil, fmt.Errorf("invalid: decision must be approve or reject")
	}
	if err := s.store.ResolveSelfieReview(ctx, userID, adminID, approve); err != nil {
		return nil, err
	}
	entry := &store.AdminAuditEntry{
		ActorAdminID:   adminID,
		Action:         "selfie_review_" + decision,
		TargetUserID:   userID,
		TargetResource: "selfie_verification:" + userID.String(),
		Reason:         note,
	}
	if err := s.store.InsertAdminAudit(ctx, entry); err != nil {
		slog.Error("admin audit: insert failed for ReviewSelfie", "user_id", userID, "actor_admin_id", adminID, "error", err)
	}
	out := &SelfieFlowResult{Status: store.SelfieStatusFailed}
	if approve {
		out.Status, out.Passed = store.SelfieStatusPassed, true
		out.TrustTier = s.completeSelfiePass(ctx, userID)
	} else if s.producer != nil {
		if perr := s.producer.PublishVerificationRejected(ctx, userID, "selfie"); perr != nil {
			slog.Warn("publish verification.rejected failed", "error", perr)
		}
	}
	if p, err := s.store.GetProfile(ctx, userID); err == nil && p != nil {
		out.ProfileStatus = p.ProfileStatus
		if out.TrustTier == "" {
			out.TrustTier = p.TrustTier
		}
	}
	return out, nil
}

// ErrSelfieMediaNotReady: the selfie video exists and is the caller's, but
// media-service has not finished processing (or moderating) it yet. Distinct
// from ErrSelfieMediaNotFound so the client can retry the SAME upload in a
// moment instead of recording again. Maps to 409 MEDIA_NOT_READY.
var ErrSelfieMediaNotReady = errors.New("selfie media is still being processed")

// requireReadySelfieVideo checks the video is the caller's and finished
// before an attempt is consumed. media-service answers 404 for a media that
// is missing OR not theirs, so both stay ErrSelfieMediaNotFound; a media that
// exists but is not a ready, moderation-passed asset is ErrSelfieMediaNotReady.
//
// Best effort by design: with no media client wired, or when media-service
// cannot answer, the check is skipped and the liveness call decides — a
// media outage must not make every selfie unverifiable.
func (s *Service) requireReadySelfieVideo(ctx context.Context, userID, videoMediaID uuid.UUID) error {
	if s.mediaPhotos == nil {
		return nil
	}
	st, err := s.mediaPhotos.PhotoOwnerStatus(ctx, videoMediaID, userID)
	switch {
	case errors.Is(err, ErrPhotoMediaNotFound):
		return ErrSelfieMediaNotFound
	case errors.Is(err, ErrPhotoMediaNotReady):
		return ErrSelfieMediaNotReady
	case err != nil:
		slog.Warn("selfie: media readiness check unavailable; continuing",
			"user_id", userID, "video_media_id", videoMediaID, "error", err)
		return nil
	case st == nil:
		return nil
	case !st.OwnerMatches:
		return ErrSelfieMediaNotFound
	case st.Status != "ready" || st.ModerationStatus == "pending" || st.ModerationStatus == "":
		return ErrSelfieMediaNotReady
	}
	return nil
}
