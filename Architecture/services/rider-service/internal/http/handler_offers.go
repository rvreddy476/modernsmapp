package http

import (
	"net/http"
	"strings"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// GetIncomingOffers — GET /v1/rider/offers/incoming. Returns the partner's
// open `sent` offers.
func (h *Handler) GetIncomingOffers(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.ListIncomingOffers(c.Request.Context(), uid)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "OFFERS_LIST_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// PostAcceptOffer — POST /v1/rider/offers/:id/accept. Returns the plain-text
// OTP exactly once (subsequent reads see only the bcrypt hash).
func (h *Handler) PostAcceptOffer(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	offerID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	out, err := h.svc.AcceptOffer(c.Request.Context(), uid, offerID)
	if err != nil {
		// "conflict:" -> 409. Otherwise route through the standard mapper.
		if msg := err.Error(); strings.HasPrefix(msg, "conflict: ") {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "CONFLICT", strings.TrimPrefix(msg, "conflict: "), nil)
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "OFFER_ACCEPT_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// rejectOfferRequest is the body for POST /v1/rider/offers/:id/reject.
type rejectOfferRequest struct {
	Reason string `json:"reason"`
}

// PostRejectOffer — POST /v1/rider/offers/:id/reject.
func (h *Handler) PostRejectOffer(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	offerID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	var body rejectOfferRequest
	_ = c.ShouldBindJSON(&body) // body optional
	if err := h.svc.RejectOffer(c.Request.Context(), uid, offerID, body.Reason); err != nil {
		if msg := err.Error(); strings.HasPrefix(msg, "conflict: ") {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "CONFLICT", strings.TrimPrefix(msg, "conflict: "), nil)
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "OFFER_REJECT_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"ok": true})
}
