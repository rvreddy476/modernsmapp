// Package http wires gin routes to the dating service.
package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/shared/api"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	svc         *service.Service
	internalKey string
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
// Empty key disables the gate (dev-loop only); main.go emits a loud
// warning at startup when the env var isn't set.
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
		// Internal-only: minimal profile preview for cross-service name
		// lookups (notification-service uses this for "<first_name>
		// Sparked your photo" titles).
		dating.GET("/profile/:userId/preview", h.GetProfilePreview)

		dating.GET("/tune", h.GetTune)
		dating.PUT("/tune", h.PutTune)

		dating.GET("/preferences", h.GetPreferences)
		dating.PUT("/preferences", h.PutPreferences)

		dating.GET("/photos", h.ListPhotos)
		dating.POST("/photos", h.CreatePhoto)
		dating.PATCH("/photos/:id", h.UpdatePhoto)
		dating.DELETE("/photos/:id", h.DeletePhoto)
		// Internal-only: admin / content-scanner moderation flip.
		// Drives deck-cache invalidation + profile-state transition
		// + photo.moderation_rejected event. Same internal-key gate
		// as the rest of /v1/dating/*.
		dating.POST("/photos/:id/moderation", h.SetPhotoModerationStatus)

		dating.GET("/prompts/catalog", h.GetPromptCatalog)
		dating.GET("/prompts", h.ListPrompts)
		dating.PUT("/prompts/:promptId", h.UpsertPrompt)
		dating.DELETE("/prompts/:promptId", h.DeletePrompt)

		dating.GET("/pulse/today", h.GetPulseToday)
		dating.GET("/pulse/nebula", h.GetPulseNebula)

		// Sprint 3 — Sparks
		dating.POST("/sparks", h.CreateSpark)
		dating.GET("/sparks/incoming", h.ListIncomingSparks)
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
		// Internal-only (called by message-service consumer)
		dating.POST("/matches/:id/first-message", h.MatchFirstMessage)

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
