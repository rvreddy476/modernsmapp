// HTTP handlers for /v1/dating/safety (spec §15 safety center, Dating plan
// lane D8).
//
// CRITICAL RULES #6: every error path is explicit. Panic + report MUST
// persist before responding 200; the service layer enforces this — this
// file just maps service errors to status codes.
package http

import (
	"net/http"
	"time"

	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/dating-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// PostPanic — POST /v1/dating/safety/panic.
// Body (optional): {latitude?, longitude?, context?}. Always records an
// incident; a repeat within the dedupe window returns the same incident.
func (h *Handler) PostPanic(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body service.PanicRequest
	// Body is optional — a panic with no metadata still fires.
	_ = c.ShouldBindJSON(&body)
	out, err := h.svc.RecordPanic(c.Request.Context(), userID, body)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "PANIC_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"recorded":     true,
		"incident_id":  out.IncidentID,
		"status":       out.Status,
		"deduplicated": out.Deduplicated,
	}, nil)
}

// ── Trusted contacts ────────────────────────────────────────────────────────

// ListTrustedContacts — GET /v1/dating/safety/trusted-contacts.
func (h *Handler) ListTrustedContacts(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	items, err := h.svc.ListTrustedContacts(c.Request.Context(), userID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "TRUSTED_CONTACTS_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": items, "max": store.MaxTrustedContacts}, nil)
}

type trustedContactRequest struct {
	ShareLocationOnPanic bool `json:"share_location_on_panic"`
}

// PutTrustedContact — PUT /v1/dating/safety/trusted-contacts/:contactId.
// Body (optional): {share_location_on_panic}. The contact must be an
// accepted connection or a current match; at most three.
func (h *Handler) PutTrustedContact(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	contactID, ok := parseUUID(c, "contactId")
	if !ok {
		return
	}
	var body trustedContactRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&body); err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
			return
		}
	}
	tc, err := h.svc.SetTrustedContact(c.Request.Context(), userID, contactID, body.ShareLocationOnPanic)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "TRUSTED_CONTACT_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, tc, nil)
}

// DeleteTrustedContact — DELETE /v1/dating/safety/trusted-contacts/:contactId.
func (h *Handler) DeleteTrustedContact(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	contactID, ok := parseUUID(c, "contactId")
	if !ok {
		return
	}
	if err := h.svc.RemoveTrustedContact(c.Request.Context(), userID, contactID); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "TRUSTED_CONTACT_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"removed": true}, nil)
}

// ── Live location ───────────────────────────────────────────────────────────

// shareLocationRequest is the body for POST /v1/dating/safety/share-location.
// contact_id is the legacy name of recipient_id.
type shareLocationRequest struct {
	RecipientID     string   `json:"recipient_id"`
	ContactID       string   `json:"contact_id"`
	DurationMinutes int      `json:"duration_minutes"`
	Latitude        *float64 `json:"latitude,omitempty"`
	Longitude       *float64 `json:"longitude,omitempty"`
}

// PostShareLocation — POST /v1/dating/safety/share-location.
func (h *Handler) PostShareLocation(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body shareLocationRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	raw := body.RecipientID
	if raw == "" {
		raw = body.ContactID
	}
	recipient, err := parseUUIDValue(raw)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid recipient_id", nil)
		return
	}
	out, err := h.svc.ShareLocation(c.Request.Context(), userID, service.LocationShareRequest{
		RecipientID:     recipient,
		DurationMinutes: body.DurationMinutes,
		Latitude:        body.Latitude,
		Longitude:       body.Longitude,
	})
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "SHARE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// DeleteShareLocation — DELETE /v1/dating/safety/share-location/:id. The
// sharer stops the share; the point is cleared.
func (h *Handler) DeleteShareLocation(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	shareID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	share, err := h.svc.StopLocationShare(c.Request.Context(), userID, shareID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "SHARE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"stopped": true, "share_id": share.ShareID, "stopped_at": share.StoppedAt}, nil)
}

// GetSharedLocation — GET /v1/dating/safety/shared-locations/:id. Only the
// recipient, only while the share is live.
func (h *Handler) GetSharedLocation(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	shareID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	share, err := h.svc.GetSharedLocation(c.Request.Context(), userID, shareID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "SHARE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, share, nil)
}

// ── Meets ───────────────────────────────────────────────────────────────────

// scheduleMeetRequest is the body for POST /v1/dating/safety/meet.
type scheduleMeetRequest struct {
	WithUserID string    `json:"with_user_id"`
	When       time.Time `json:"when"`
	Latitude   float64   `json:"latitude"`
	Longitude  float64   `json:"longitude"`
	Venue      string    `json:"venue"`
}

// PostScheduleMeet — POST /v1/dating/safety/meet.
func (h *Handler) PostScheduleMeet(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body scheduleMeetRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	with, err := parseUUIDValue(body.WithUserID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid with_user_id", nil)
		return
	}
	out, err := h.svc.ScheduleMeet(c.Request.Context(), userID, service.MeetRequest{
		WithUserID: with,
		When:       body.When,
		Latitude:   body.Latitude,
		Longitude:  body.Longitude,
		Venue:      body.Venue,
	})
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "MEET_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusCreated, out, nil)
}

// meetCheckInRequest — body for POST /v1/dating/safety/meet/:id/check-in.
// The optional point is kept on the help incident only.
type meetCheckInRequest struct {
	Status    string   `json:"status"`
	Latitude  *float64 `json:"latitude,omitempty"`
	Longitude *float64 `json:"longitude,omitempty"`
}

// PostMeetCheckIn — POST /v1/dating/safety/meet/:id/check-in.
func (h *Handler) PostMeetCheckIn(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	meetID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	var body meetCheckInRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.MeetCheckInAt(c.Request.Context(), meetID, userID, body.Status, body.Latitude, body.Longitude); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "CHECKIN_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"status": body.Status}, nil)
}

// ── Block and report ────────────────────────────────────────────────────────

// blockRequest — body for POST /v1/dating/safety/block.
type blockRequest struct {
	TargetUserID string `json:"target_user_id"`
}

// PostBlock — POST /v1/dating/safety/block.
func (h *Handler) PostBlock(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body blockRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	target, err := parseUUIDValue(body.TargetUserID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid target_user_id", nil)
		return
	}
	if err := h.svc.Block(c.Request.Context(), userID, target); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "BLOCK_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"blocked": true}, nil)
}

// reportEvidenceRequest carries evidence references as strings so a bad id
// is a 400, not a bind error.
type reportEvidenceRequest struct {
	PhotoIDs   []string `json:"photo_ids"`
	MessageIDs []string `json:"message_ids"`
	SparkIDs   []string `json:"spark_ids"`
}

// reportRequest — body for POST /v1/dating/safety/report. category is the
// legacy name of reason.
type reportRequest struct {
	TargetID string                `json:"target_id"`
	Reason   string                `json:"reason"`
	Category string                `json:"category"`
	Details  string                `json:"details"`
	Evidence reportEvidenceRequest `json:"evidence"`
}

func parseUUIDList(raw []string) ([]uuid.UUID, bool) {
	out := make([]uuid.UUID, 0, len(raw))
	for _, r := range raw {
		id, err := uuid.Parse(r)
		if err != nil {
			return nil, false
		}
		out = append(out, id)
	}
	return out, true
}

// PostReport — POST /v1/dating/safety/report.
// Body: {target_id, reason, details?, evidence?{photo_ids, message_ids,
// spark_ids}}. 201 with the report and blocked=true when the auto-block
// landed; 400 INVALID_REPORT_REASON; 429 REPORT_RATE_LIMITED.
func (h *Handler) PostReport(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body reportRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	target, err := parseUUIDValue(body.TargetID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid target_id", nil)
		return
	}
	photos, ok := parseUUIDList(body.Evidence.PhotoIDs)
	if !ok {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REPORT_EVIDENCE", "invalid photo id", nil)
		return
	}
	sparks, ok := parseUUIDList(body.Evidence.SparkIDs)
	if !ok {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REPORT_EVIDENCE", "invalid spark id", nil)
		return
	}
	out, err := h.svc.Report(c.Request.Context(), userID, service.ReportRequest{
		TargetID: target,
		Reason:   body.Reason,
		Category: body.Category,
		Details:  body.Details,
		Evidence: store.ReportEvidence{PhotoIDs: photos, MessageIDs: body.Evidence.MessageIDs, SparkIDs: sparks},
	})
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "REPORT_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusCreated, out, nil)
}

// ── Internal ────────────────────────────────────────────────────────────────

// GetPanicNotifyContext — GET InternalPanicNotifyContextPath. Service
// callers only (notification-service): the incident's first name and
// trusted contacts, with the point only for contacts the user opted in.
func (h *Handler) GetPanicNotifyContext(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	out, err := h.svc.PanicNotifyContext(c.Request.Context(), id)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "PANIC_CONTEXT_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// ListMyLocationShares — GET /v1/dating/safety/share-location.
// The caller's live outgoing shares; no coordinates.
func (h *Handler) ListMyLocationShares(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	items, err := h.svc.ListMyLocationShares(c.Request.Context(), userID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "SHARE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": items}, nil)
}

// ListSharedLocations — GET /v1/dating/safety/shared-locations.
// The live shares aimed at the caller: the share id to read the point with,
// the sharer's compact card and when the share ends. No coordinates here —
// GET /v1/dating/safety/shared-locations/:id serves the point.
func (h *Handler) ListSharedLocations(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	items, err := h.svc.ListSharedLocationsForMe(c.Request.Context(), userID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "SHARE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": items}, nil)
}
