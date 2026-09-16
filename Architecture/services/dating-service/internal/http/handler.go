// Package http wires gin routes to the dating service.
package http

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/dating-service/internal/store"
	"github.com/atpost/shared/api"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	svc         *service.Service
	internalKey string
	// verifier admits service tokens on the /v1/dating/internal family
	// (auth.go). Nil leaves the legacy internal key as the only service
	// credential there.
	verifier *servicetoken.Verifier
}

func New(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

// WithInternalKey gates every /v1/dating/* route behind the shared
// X-Internal-Service-Key header. The api-gateway sets this header
// before forwarding traffic (and strips any inbound copy from the
// public client). Without the gate, anyone reaching dating-service
// directly could spoof X-User-Id and impersonate any user — the
// P0-2 finding in PRODUCTION_GAP_ANALYSIS.md.
//
// The key does NOT identify the caller: the gateway injects it on every
// proxied request, user or anonymous. Admin routes additionally require a
// gateway-set admin scope (requireAdmin) and the internal family requires a
// service credential with no user identity (requireServiceCaller).
//
// Empty key disables the gate. main.go refuses to boot without a key unless
// ENV is local/dev (ResolveInternalKey).
func (h *Handler) WithInternalKey(key string) *Handler {
	h.internalKey = key
	return h
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	// Lane P2: the Razorpay webhook (/v1/dating/premium/webhook) is gone.
	// Payments reach dating only as payments-service events on Kafka.

	// Service-only family. Outside the key group on purpose: a service-token
	// caller does not carry the internal key. Each route requires a service
	// credential (token, or the legacy key) and refuses any request carrying
	// a gateway-set user identity. The "/internal/" segment also puts these
	// behind the gateway's admin-scope gate for anything proxied.
	r.GET(InternalProfilePreviewPath, h.requireServiceCaller(OpProfilePreview), h.GetProfilePreview)
	r.POST(InternalFirstMessagePath, h.requireServiceCaller(OpMatchFirstMessage), h.MatchFirstMessage)
	r.GET(InternalRiskPath, h.requireServiceCaller(OpRiskRead), h.GetAccountRisk)
	// Lane D8 — notification-service's paging context for one panic incident.
	r.GET(InternalPanicNotifyContextPath, h.requireServiceCaller(OpSafetyPanicNotify), h.GetPanicNotifyContext)

	// Everything else sits behind the internal-service-key gate.
	v1 := r.Group("")
	if h.internalKey != "" {
		v1.Use(sharedmiddleware.RequireInternalKey(h.internalKey))
	}
	dating := v1.Group("/v1/dating")
	{
		dating.GET("/profile", h.GetProfile)
		dating.POST("/profile", h.UpsertProfile)
		dating.PATCH("/profile/intent", h.PatchIntent)
		dating.POST("/profile/pause", h.PostPause)
		dating.DELETE("/profile", h.DeleteProfile)
		// LEGACY path of the profile preview (moved to
		// InternalProfilePreviewPath). Kept for one release for
		// notification-service's internal-key call: served only when no
		// gateway user identity is present, 410 otherwise.
		dating.GET("/profile/:userId/preview", h.GetProfilePreviewLegacy)

		// §P1-3 — Privacy controls (incognito, hide_last_active,
		// approximate_location, verified_only_filter,
		// blur_photos_until_match). Partial-update PATCH semantics.
		dating.GET("/profile/privacy", h.GetPrivacy)
		dating.PATCH("/profile/privacy", h.PatchPrivacy)

		dating.GET("/tune", h.GetTune)
		dating.PUT("/tune", h.PutTune)

		dating.GET("/preferences", h.GetPreferences)
		dating.PUT("/preferences", h.PutPreferences)

		dating.GET("/photos", h.ListPhotos)
		// §P1-2 transparency — owner-only view that includes the
		// moderation_reason column so the "Why was my photo
		// rejected?" UI can render the moderator note inline.
		dating.GET("/photos/me", h.ListMyPhotos)
		dating.POST("/photos", h.CreatePhoto)
		dating.PATCH("/photos/:id", h.UpdatePhoto)
		dating.DELETE("/photos/:id", h.DeletePhoto)
		// Lane D6: a photo's image, decided per viewer on every fetch and
		// redirected to a short-lived media-service URL.
		dating.GET("/photos/:id/full", h.GetPhotoImage(service.PhotoVariantFull))
		dating.GET("/photos/:id/blurred", h.GetPhotoImage(service.PhotoVariantBlurred))
		// Admin / moderator moderation flip. Drives deck-cache
		// invalidation + profile-state transition +
		// photo.moderation_rejected event. Requires the admin scope, so a
		// user cannot approve their own photo.
		dating.POST("/photos/:id/moderation", h.requireAdmin(PermPhotosReview), h.SetPhotoModerationStatus)

		dating.GET("/prompts/catalog", h.GetPromptCatalog)
		dating.GET("/prompts", h.ListPrompts)
		dating.PUT("/prompts/:promptId", h.UpsertPrompt)
		dating.DELETE("/prompts/:promptId", h.DeletePrompt)

		// §P0-7 Phase B — capture (X-Device-Fingerprint, client IP)
		// on every pulse + spark request so the risk job can compute
		// the device-reuse + IP/ASN-velocity signals. Middleware is
		// best-effort: a missing header skips the write.
		fpMW := DeviceFingerprintMiddleware(h.svc)
		dating.GET("/pulse/today", fpMW, h.GetPulseToday)
		dating.GET("/pulse/nebula", fpMW, h.GetPulseNebula)
		// §P1-2 transparency — "Why am I seeing this profile?"
		// Returns structured reasons (age band, distance, gender
		// pref, shared community, shared interest, promoted).
		dating.GET("/pulse/:targetUserId/explain", fpMW, h.ExplainPulseCandidate)
		// Lane D3 — pass on a deck candidate (idempotent, 30-day cooldown).
		dating.POST("/pulse/:candidateId/pass", fpMW, h.PassCandidate)

		// Sprint 3 — Sparks
		dating.POST("/sparks", fpMW, h.CreateSpark)
		dating.GET("/sparks/incoming", fpMW, h.ListIncomingSparks)
		dating.DELETE("/sparks/:id", h.RevokeSpark)
		// Lane D3 — the recipient declines; the sender is never told.
		dating.POST("/sparks/:id/decline", h.DeclineSpark)
		// Lane D10 — the recipient sparks back and the match forms.
		dating.POST("/sparks/:id/accept", h.AcceptSpark)

		// Lane D10 — the compact card for one person, for a current match,
		// an incoming spark or someone in the viewer's deck. 404 otherwise.
		dating.GET("/people/:userId", h.GetPersonCard)
		// Sprint 3 — Stash
		dating.GET("/stash", h.ListStash)
		dating.POST("/stash", h.AddStash)
		dating.DELETE("/stash/:candidateId", h.RemoveStash)

		// Sprint 3 — Matches
		dating.GET("/matches", h.ListMatches)
		dating.GET("/matches/:id", h.GetMatch)
		dating.POST("/matches/:id/close", h.CloseMatch)
		dating.POST("/matches/:id/extend", h.ExtendMatch)
		// Moved to InternalFirstMessagePath; 410 for one release.
		dating.POST("/matches/:id/first-message", movedTo(InternalFirstMessagePath))

		// Sprint 4 — Verification (Aadhaar via DigiLocker, optional).
		// DPDP Act compliant — see PULSE_DATING_SPEC.md §15.8
		// Lane D10 — the selfie state, attempts left and the next step.
		dating.GET("/verification/status", h.GetVerificationStatus)
		dating.POST("/verification/aadhaar/start", h.StartAadhaar)
		dating.POST("/verification/aadhaar/callback", h.AadhaarCallback)
		// Lane D5 — required selfie, decided server-side: a single-use
		// liveness challenge, then {media_id, challenge_id}.
		dating.POST("/verification/selfie/challenge", h.CreateSelfieChallenge)
		dating.POST("/verification/selfie", h.SubmitSelfie)

		// Sprint 4 — Vouching (spec §15).
		dating.POST("/vouches", h.CreateVouch)
		dating.POST("/vouches/:id/accept", h.AcceptVouch)
		dating.POST("/vouches/:id/decline", h.DeclineVouch)
		dating.DELETE("/vouches/:id", h.RevokeVouch)
		dating.GET("/vouches/for/:userId", h.ListVouchesFor)
		dating.GET("/vouches/sent", h.ListVouchesSent)

		// Sprint 4 — Safety center (spec §15).
		dating.POST("/safety/panic", h.PostPanic)
		// Lane D8 — trusted contacts (max 3; accepted connection or current
		// match) and live location to a trusted contact or current match.
		dating.GET("/safety/trusted-contacts", h.ListTrustedContacts)
		dating.PUT("/safety/trusted-contacts/:contactId", h.PutTrustedContact)
		dating.DELETE("/safety/trusted-contacts/:contactId", h.DeleteTrustedContact)
		dating.GET("/safety/share-location", h.ListMyLocationShares)
		dating.POST("/safety/share-location", h.PostShareLocation)
		dating.DELETE("/safety/share-location/:id", h.DeleteShareLocation)
		dating.GET("/safety/shared-locations", h.ListSharedLocations)
		dating.GET("/safety/shared-locations/:id", h.GetSharedLocation)
		dating.POST("/safety/meet", h.PostScheduleMeet)
		dating.POST("/safety/meet/:id/check-in", h.PostMeetCheckIn)
		dating.POST("/safety/block", h.PostBlock)
		// Lane D10 — the caller's block list and undoing a block. Unblocking
		// restores nothing the block severed.
		dating.GET("/blocks", h.ListBlocks)
		dating.DELETE("/blocks/:userId", h.DeleteBlock)
		dating.POST("/safety/report", h.PostReport)

		// Sprint 4 — AI moderation (SHADOW MODE for v1; internal-only).
		dating.POST("/moderation/scan", h.PostScanMessage)

		// Lane P2 — Premium passes and Boost through payments-service.
		dating.GET("/premium/catalogue", h.GetPremiumCatalogue)
		dating.POST("/premium/purchases", h.PostPremiumPurchase)
		dating.GET("/premium/purchases/:id/payment", h.GetPremiumPurchasePayment)
		dating.GET("/premium/me", h.GetMyPremium)
		// Retired Razorpay subscription routes: 410 with a code.
		dating.GET("/premium/plans", premiumPlansMoved)
		dating.POST("/premium/checkout", premiumSubscriptionsRemoved)
		dating.POST("/premium/cancel", premiumSubscriptionsRemoved)

		// Pulse boost (a purchased Boost token OR a pass holder's daily boost).
		dating.POST("/pulse/boost", h.PostBoost)

		// Sprint 5 — DPDP data export (§15.8).
		dating.POST("/data-export", h.PostDataExport)
		dating.GET("/data-export/me", h.GetMyDataExports)
		// Lane D9 — the finished export, sealed at rest, owner only.
		dating.GET("/data-export/:id/download", h.GetDataExportDownload)

		// Lane D9 — explicit consent for sensitive data (religion, community,
		// the biometric selfie check, Echoes). Withdrawal clears what it
		// covered; the history is in the data export.
		dating.GET("/consents", h.GetConsents)
		dating.PUT("/consents/:type", h.PutConsent)

		// §P0-8 admin queues for the /admin/dating console. The
		// gateway does NOT admin-gate these paths (they have no
		// "/internal/" segment), so dating checks the gateway-set
		// scopes itself: every route requires admin|moderator|superadmin.
		//
		// Admin console Wave 2: each route also names its permission, and a
		// request carrying an admin-service token is judged by that token
		// alone (requireAdmin, admin_token.go). admin-service itself calls
		// the token-only mirror under InternalAdminPrefix below.
		admin := dating.Group("/admin")
		h.registerAdminRoutes(admin, h.requireAdmin)
	}

	// Token-only admin family for admin-service: the same handlers, outside
	// the internal-key group, admitted only by an admin-service token.
	h.registerAdminRoutes(r.Group(InternalAdminPrefix), h.requireAdminToken)
	r.GET(InternalAdminPrefix+"/stats", h.requireAdminToken(PermStatsRead), h.GetAdminStats)
	r.POST(InternalAdminPrefix+"/photos/:id/moderation", h.requireAdminToken(PermPhotosReview), h.SetPhotoModerationStatus)

	{
		dating := v1.Group("/v1/dating")

		// Moved to InternalRiskPath; 410 for one release.
		dating.GET("/risk/:userId", movedTo(InternalRiskPath))
	}
}

// registerAdminRoutes declares the admin console routes once, with the
// permission each needs, under whichever gate the caller passes: requireAdmin
// (token or LEGACY gateway scopes) or requireAdminToken (token only).
//
//	GET  /reports                              dating:reports.read
//	POST /reports/:id/action                   dating:reports.act, or dating:users.ban for suspend/reinstate
//	GET  /safety/panic                         dating:panic.read (no coordinates)
//	GET  /safety/panic/:id                     dating:panic.reveal (GPS; audited per view)
//	POST /safety/panic/:id/ack|resolve         dating:panic.act
//	GET  /photos/pending                       dating:photos.review
//	GET  /verification/selfie/pending          dating:selfie.review
//	POST /verification/selfie/:userId/review   dating:selfie.review
//	GET  /audit                                dating:audit.read
//	GET  /risk                                 dating:risk.read
func (h *Handler) registerAdminRoutes(g *gin.RouterGroup, gate func(perms ...string) gin.HandlerFunc) {
	g.GET("/reports", gate(PermReportsRead), h.ListReports)
	g.POST("/reports/:id/action", gate(PermReportsAct, PermUsersBan), h.ActOnReport)
	// Lane D8 — the panic queue is paginated and carries no coordinates; the
	// point is only in the single-incident detail, and every detail view is
	// audited.
	g.GET("/safety/panic", gate(PermPanicRead), h.ListPanicIncidents)
	g.GET("/safety/panic/:id", gate(PermPanicReveal), h.GetPanicIncident)
	// Acknowledge moves open → acknowledged, writes the audit row and emits
	// dating.safety.panic.acknowledged so the user sees support has triaged
	// their alert; resolve closes it with a note (audited).
	g.POST("/safety/panic/:id/ack", gate(PermPanicAct), h.AcknowledgePanic)
	g.POST("/safety/panic/:id/resolve", gate(PermPanicAct), h.ResolvePanic)
	g.GET("/photos/pending", gate(PermPhotosReview), h.ListPendingPhotos)
	// Lane D5 — selfie review queue (borderline similarity, high-risk first
	// attempts) and the moderator decision.
	g.GET("/verification/selfie/pending", gate(PermSelfieReview), h.ListSelfieReviews)
	g.POST("/verification/selfie/:userId/review", gate(PermSelfieReview), h.ReviewSelfie)
	// §P0-8 — append-only audit log surface for the console.
	g.GET("/audit", gate(PermAuditRead), h.ListAdminAudit)
	// §P0-7 Phase A — fake-account risk queue.
	g.GET("/risk", gate(PermRiskRead), h.ListAccountRisks)
}

// GetAdminStats — GET /v1/dating/internal/admin/stats (admin-service token,
// dating:stats.read). Read-only dashboard counts.
func (h *Handler) GetAdminStats(c *gin.Context) {
	stats, err := h.svc.AdminStats(c.Request.Context())
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, stats, nil)
}

func getUserID(c *gin.Context) (uuid.UUID, bool) {
	raw := c.GetHeader("X-User-ID")
	if raw == "" {
		raw = c.GetHeader("X-User-Id")
	}
	if raw == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "AUTH_REQUIRED", "missing user id", nil)
		return uuid.Nil, false
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid user id", nil)
		return uuid.Nil, false
	}
	return id, true
}

func parseUUID(c *gin.Context, param string) (uuid.UUID, bool) {
	raw := c.Param(param)
	id, err := uuid.Parse(raw)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid "+param, nil)
		return uuid.Nil, false
	}
	return id, true
}

func parseIntParam(c *gin.Context, param string) (int, bool) {
	raw := c.Param(param)
	n, err := strconv.Atoi(raw)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid "+param, nil)
		return 0, false
	}
	return n, true
}

func parseUUIDValue(raw string) (uuid.UUID, error) {
	return uuid.Parse(raw)
}

func respondServiceError(c *gin.Context, err error, defaultCode int, defaultCodeName string) {
	if err == nil {
		return
	}
	// P0-5: surface the underage gate as 403 AGE_REQUIRED so mobile +
	// web can render the "complete your birth date / 18+ required"
	// flow rather than dumping the raw message.
	if errors.Is(err, service.ErrUnderage) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "AGE_REQUIRED", err.Error(), nil)
		return
	}
	// Lane D9: a sensitive field or the biometric check without consent, an
	// unknown consent type, and a sealing write without keys (local/dev).
	var consentRequired *service.ConsentRequiredError
	if errors.As(err, &consentRequired) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnprocessableEntity, "CONSENT_REQUIRED",
			"explicit consent is required first: PUT /v1/dating/consents/"+consentRequired.ConsentType+` {"granted": true}`,
			map[string]any{"consent_type": consentRequired.ConsentType, "policy_version": consentRequired.PolicyVersion})
		return
	}
	if errors.Is(err, service.ErrUnknownConsentType) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_CONSENT_TYPE", "unknown consent type",
			map[string]any{"allowed": service.ConsentTypes})
		return
	}
	if errors.Is(err, store.ErrPIINotConfigured) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable, "PII_NOT_CONFIGURED",
			"this data cannot be stored right now", nil)
		return
	}
	// Lane D2: identity could not confirm the birth date for a profile that
	// has none locked yet, so the client's value is not trusted instead.
	if errors.Is(err, service.ErrIdentityUnavailable) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable, "IDENTITY_UNAVAILABLE",
			"identity service is unavailable; try again shortly", nil)
		return
	}
	// Lane D2: the status machine refused the edge (e.g. pausing a deleted
	// profile, reinstating one that is not held). Stable code for clients.
	if errors.Is(err, store.ErrProfileTransitionNotAllowed) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "PROFILE_TRANSITION_NOT_ALLOWED", err.Error(), nil)
		return
	}
	if errors.Is(err, store.ErrProfileStatusConflict) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "PROFILE_STATUS_CONFLICT", err.Error(), nil)
		return
	}
	// Lane D3: one refusal for blocked / inactive / suspended / deleted /
	// under-18 counterparts, so a block is never revealed.
	if errors.Is(err, service.ErrCandidateUnavailable) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "CANDIDATE_UNAVAILABLE", "this person is not available", nil)
		return
	}
	// Lane D7: location changes and explain requests are rate limited, and a
	// malformed location has its own 400.
	var locationLimited *store.LocationRateLimitError
	if errors.As(err, &locationLimited) {
		minutes := int(locationLimited.Limits.MinInterval / time.Minute)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "LOCATION_CHANGE_RATE_LIMITED",
			fmt.Sprintf("location can change at most once every %d minutes and %d times a day; try again later",
				minutes, locationLimited.Limits.MaxPerDay),
			map[string]any{
				"min_interval_minutes": minutes,
				"max_changes_per_day":  locationLimited.Limits.MaxPerDay,
				"window_hours":         int(store.LocationChangeWindow.Hours()),
			})
		return
	}
	if errors.Is(err, store.ErrInvalidLocation) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_LOCATION",
			"latitude and longitude must be sent together, within range, and not 0,0", nil)
		return
	}
	var explainLimited *store.ExplainRateLimitError
	if errors.As(err, &explainLimited) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "EXPLAIN_RATE_LIMITED", "explain limit reached; try again later",
			map[string]any{"limit": explainLimited.Limit, "window_hours": int(store.ExplainQuotaWindow.Hours())})
		return
	}
	if errors.Is(err, store.ErrSparkRateLimited) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "SPARK_RATE_LIMITED", "spark limit reached; try again later",
			map[string]any{"limit": service.DefaultSparkDailyLimit, "window_hours": int(store.SparkQuotaWindow.Hours())})
		return
	}
	if errors.Is(err, service.ErrSparkNoteRefused) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "SPARK_NOTE_REFUSED", "spark notes cannot contain phone numbers, email addresses or links", nil)
		return
	}
	// The profile's own gender is an enum too. Its own code, separate from
	// the preference's, so the app knows which of the two pickers to show.
	if errors.Is(err, service.ErrInvalidGender) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_GENDER",
			"gender must be one of "+strings.Join(service.Genders, ", "),
			map[string]any{"allowed": service.Genders})
		return
	}
	// Preferences: the gender filter is an enum, so it gets its own code
	// instead of the generic INVALID_REQUEST the "invalid: " fallback gives.
	if errors.Is(err, service.ErrInvalidInterestedInGender) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_INTERESTED_IN_GENDER",
			"interested_in_gender must be one of "+strings.Join(service.InterestedInGenders, ", "),
			map[string]any{"allowed": service.InterestedInGenders})
		return
	}
	// The rest of the client-facing validation refusals. Same idea as the
	// gender filter above: each names the field the server refused and
	// carries the allowed values or the limit in details, so the app can
	// write copy against a code instead of parsing a sentence.
	if errors.Is(err, service.ErrMinAgeTooLow) || errors.Is(err, service.ErrMaxAgeTooHigh) || errors.Is(err, service.ErrAgeRangeInverted) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_AGE_RANGE", err.Error(),
			map[string]any{"min": service.MinPreferenceAge, "max": service.MaxPreferenceAge})
		return
	}
	if errors.Is(err, service.ErrInvalidDistanceKm) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_DISTANCE_KM", err.Error(),
			map[string]any{"min": service.MinDistanceKm, "max": service.MaxDistanceKm})
		return
	}
	if errors.Is(err, service.ErrInvalidIntentFilter) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_INTENT_FILTER", err.Error(),
			map[string]any{"allowed": service.Intents})
		return
	}
	if errors.Is(err, service.ErrInvalidIntent) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_INTENT", err.Error(),
			map[string]any{"allowed": service.Intents})
		return
	}
	if errors.Is(err, service.ErrInvalidVisibility) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_VISIBILITY", err.Error(),
			map[string]any{"allowed": service.PhotoVisibilities})
		return
	}
	// Onboarding is a state gate, not a malformed request: 409, beside the
	// other profile-state refusals above.
	var onboarding *service.OnboardingIncompleteError
	if errors.As(err, &onboarding) {
		// The key is "status", not "profile_status": a bare
		// "profile_status" literal outside the guarded writer trips the
		// store's raw-lifecycle-write scanner (profile_status_scan_test).
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "ONBOARDING_INCOMPLETE", err.Error(),
			map[string]any{"status": onboarding.ProfileStatus, "step": onboarding.Step})
		return
	}
	if errors.Is(err, service.ErrUnknownPrompt) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "UNKNOWN_PROMPT", err.Error(),
			map[string]any{"allowed": service.PromptCatalogIDs()})
		return
	}
	if errors.Is(err, service.ErrPromptAnswerRequired) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "PROMPT_ANSWER_REQUIRED", err.Error(),
			map[string]any{"min": 1, "max": service.MaxPromptAnswerLen})
		return
	}
	if errors.Is(err, service.ErrPromptAnswerTooLong) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "PROMPT_ANSWER_TOO_LONG", err.Error(),
			map[string]any{"max": service.MaxPromptAnswerLen})
		return
	}
	if errors.Is(err, service.ErrPassReasonTooLong) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "PASS_REASON_TOO_LONG", err.Error(),
			map[string]any{"max": service.MaxPassReasonLen})
		return
	}
	// Lane D8: reports, trusted contacts, live location and meets.
	if errors.Is(err, service.ErrInvalidReportReason) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REPORT_REASON", "reason must be one of the report reason codes",
			map[string]any{"allowed": store.ReportReasons})
		return
	}
	if errors.Is(err, store.ErrReportEvidenceInvalid) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REPORT_EVIDENCE", strings.TrimPrefix(err.Error(), "invalid: "), nil)
		return
	}
	if errors.Is(err, store.ErrReportRateLimited) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "REPORT_RATE_LIMITED", "report limit reached; try again later",
			map[string]any{"window_hours": int(store.ReportQuotaWindow.Hours())})
		return
	}
	if errors.Is(err, service.ErrReportTargetMismatch) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "REPORT_TARGET_MISMATCH", err.Error(), nil)
		return
	}
	if errors.Is(err, service.ErrTrustedContactNotEligible) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "TRUSTED_CONTACT_NOT_ELIGIBLE", err.Error(), nil)
		return
	}
	if errors.Is(err, store.ErrTrustedContactLimit) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "TRUSTED_CONTACT_LIMIT", "you already have the maximum number of trusted contacts",
			map[string]any{"max": store.MaxTrustedContacts})
		return
	}
	if errors.Is(err, service.ErrConnectionCheckUnavailable) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable, "CONNECTION_CHECK_UNAVAILABLE", "could not confirm the connection; try again shortly", nil)
		return
	}
	if errors.Is(err, service.ErrShareRecipientNotAllowed) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "SHARE_RECIPIENT_NOT_ALLOWED", err.Error(), nil)
		return
	}
	if errors.Is(err, service.ErrMeetRequiresMatch) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "MEET_REQUIRES_MATCH", err.Error(), nil)
		return
	}
	msg := err.Error()
	if detail, ok := strings.CutPrefix(msg, "invalid: "); ok {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", detail, nil)
		return
	}
	if detail, ok := strings.CutPrefix(msg, "forbidden: "); ok {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", detail, nil)
		return
	}
	if detail, ok := strings.CutPrefix(msg, "not_found: "); ok {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", detail, nil)
		return
	}
	api.ErrorWithContext(c.Request.Context(), c.Writer, defaultCode, defaultCodeName, msg, nil)
}
