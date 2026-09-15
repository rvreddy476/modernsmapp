// Package http wires gin routes to the dating service.
package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

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
	// Razorpay webhook is signature-authenticated (X-Razorpay-Signature
	// HMAC verified inside the handler), NOT internal-key gated. The
	// gateway forwards it untouched, and Razorpay itself cannot carry
	// the internal key. Register it outside the v1 group.
	r.POST("/v1/dating/premium/webhook", h.PostWebhook)

	// Service-only family. Outside the key group on purpose: a service-token
	// caller does not carry the internal key. Each route requires a service
	// credential (token, or the legacy key) and refuses any request carrying
	// a gateway-set user identity. The "/internal/" segment also puts these
	// behind the gateway's admin-scope gate for anything proxied.
	r.GET(InternalProfilePreviewPath, h.requireServiceCaller(OpProfilePreview), h.GetProfilePreview)
	r.POST(InternalFirstMessagePath, h.requireServiceCaller(OpMatchFirstMessage), h.MatchFirstMessage)
	r.GET(InternalRiskPath, h.requireServiceCaller(OpRiskRead), h.GetAccountRisk)

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
		// Admin / moderator moderation flip. Drives deck-cache
		// invalidation + profile-state transition +
		// photo.moderation_rejected event. Requires the admin scope, so a
		// user cannot approve their own photo.
		dating.POST("/photos/:id/moderation", h.requireAdmin(), h.SetPhotoModerationStatus)

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

		// Sprint 3 — Sparks
		dating.POST("/sparks", fpMW, h.CreateSpark)
		dating.GET("/sparks/incoming", fpMW, h.ListIncomingSparks)
		dating.DELETE("/sparks/:id", h.RevokeSpark)

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

		// Sprint 4 — Verification (Aadhaar via DigiLocker + selfie face match).
		// DPDP Act compliant — see PULSE_DATING_SPEC.md §15.8
		dating.POST("/verification/aadhaar/start", h.StartAadhaar)
		dating.POST("/verification/aadhaar/callback", h.AadhaarCallback)
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
		dating.POST("/safety/share-location", h.PostShareLocation)
		dating.POST("/safety/meet", h.PostScheduleMeet)
		dating.POST("/safety/meet/:id/check-in", h.PostMeetCheckIn)
		dating.POST("/safety/block", h.PostBlock)
		dating.POST("/safety/report", h.PostReport)

		// Sprint 4 — AI moderation (SHADOW MODE for v1; internal-only).
		dating.POST("/moderation/scan", h.PostScanMessage)

		// Sprint 5 — Premium / Razorpay (spec §14).
		dating.GET("/premium/plans", h.GetPlans)
		dating.POST("/premium/checkout", h.PostCheckout)
		dating.GET("/premium/me", h.GetMyPremium)
		dating.POST("/premium/cancel", h.PostCancelPremium)
		// (Razorpay webhook /v1/dating/premium/webhook is registered
		// outside this group — it's HMAC-authenticated, not
		// internal-key gated.)

		// Sprint 5 — Pulse boost (premium daily OR one-shot token).
		dating.POST("/pulse/boost", h.PostBoost)

		// Sprint 5 — DPDP data export (§15.8).
		dating.POST("/data-export", h.PostDataExport)
		dating.GET("/data-export/me", h.GetMyDataExports)

		// §P0-8 admin queues for the /admin/dating console. The
		// gateway does NOT admin-gate these paths (they have no
		// "/internal/" segment), so dating checks the gateway-set
		// scopes itself: every route requires admin|moderator|superadmin.
		admin := dating.Group("/admin", h.requireAdmin())
		admin.GET("/reports", h.ListReports)
		admin.POST("/reports/:id/action", h.ActOnReport)
		admin.GET("/safety/panic", h.ListPanicEvents)
		// Phase 1 follow-up — admin acknowledgement flips
		// acknowledged_at on the panic safety_event row, writes the
		// audit row and emits dating.safety.panic.acknowledged so the
		// user sees support has triaged their alert.
		admin.POST("/safety/panic/:id/ack", h.AcknowledgePanic)
		admin.GET("/photos/pending", h.ListPendingPhotos)
		// §P0-8 — append-only audit log surface for the console.
		admin.GET("/audit", h.ListAdminAudit)
		// §P0-7 Phase A — fake-account risk queue.
		admin.GET("/risk", h.ListAccountRisks)

		// Moved to InternalRiskPath; 410 for one release.
		dating.GET("/risk/:userId", movedTo(InternalRiskPath))
	}
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
