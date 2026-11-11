package http

import (
	"context"
	"errors"
	"net/http"

	"github.com/atpost/identity-profile-service/internal/personverify"
	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Person verification routes (internal/personverify). All four are signed
// in and answer 503 PERSON_VERIFICATION_DISABLED unless
// PERSON_VERIFICATION_ENABLED=true:
//
//	GET  /v1/profiles/me/verification/person           status
//	POST /v1/profiles/me/verification/person/start     {} → {attempt_id, authorize_url, expires_at}
//	POST /v1/profiles/me/verification/person/callback  {state, code} → status
//	POST /v1/profiles/me/verification/person/selfie    {selfie_media_id} → status
//
// The DigiLocker redirect lands on a client page (PERSON_VERIFICATION_REDIRECT_URI),
// which posts code and state here with the user's session. The state is
// bound to the user who started, so no server route needs to be public and
// the gateway's /v1/profiles proxy needs no change. code and state travel in
// the body, never the URL, so request logs (path only) never carry them.

// PersonVerification is the person-verification service.
type PersonVerification interface {
	Start(ctx context.Context, userID uuid.UUID) (*personverify.StartResult, error)
	Callback(ctx context.Context, userID uuid.UUID, state, code string) (*personverify.Status, error)
	SubmitSelfie(ctx context.Context, userID, selfieMediaID uuid.UUID) (*personverify.Status, error)
	Status(ctx context.Context, userID uuid.UUID) (*personverify.Status, error)
}

// WithPersonVerification wires the person-verification routes. Unwired,
// they answer 503 like a disabled service.
func (h *Handler) WithPersonVerification(p PersonVerification) *Handler {
	h.person = p
	return h
}

func (h *Handler) registerPersonVerificationRoutes(protected *gin.RouterGroup, csrf gin.HandlerFunc) {
	protected.GET("/me/verification/person", h.GetPersonVerification)
	protected.POST("/me/verification/person/start", csrf, h.StartPersonVerification)
	protected.POST("/me/verification/person/callback", csrf, h.PersonVerificationCallback)
	protected.POST("/me/verification/person/selfie", csrf, h.SubmitPersonVerificationSelfie)
}

type personCallbackRequest struct {
	State string `json:"state"`
	Code  string `json:"code"`
}

type personSelfieRequest struct {
	SelfieMediaID string `json:"selfie_media_id"`
}

// GetPersonVerification — GET /v1/profiles/me/verification/person.
func (h *Handler) GetPersonVerification(c *gin.Context) {
	userID, ok := h.personUser(c)
	if !ok {
		return
	}
	out, err := h.person.Status(c.Request.Context(), userID)
	h.writePerson(c, userID, http.StatusOK, out, err)
}

// StartPersonVerification — POST /v1/profiles/me/verification/person/start.
func (h *Handler) StartPersonVerification(c *gin.Context) {
	userID, ok := h.personUser(c)
	if !ok {
		return
	}
	out, err := h.person.Start(c.Request.Context(), userID)
	h.writePerson(c, userID, http.StatusCreated, out, err)
}

// PersonVerificationCallback — POST /v1/profiles/me/verification/person/callback.
func (h *Handler) PersonVerificationCallback(c *gin.Context) {
	userID, ok := h.personUser(c)
	if !ok {
		return
	}
	var req personCallbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "state and code are required", nil, nil)
		return
	}
	out, err := h.person.Callback(c.Request.Context(), userID, req.State, req.Code)
	h.writePerson(c, userID, http.StatusOK, out, err)
}

// SubmitPersonVerificationSelfie — POST /v1/profiles/me/verification/person/selfie.
func (h *Handler) SubmitPersonVerificationSelfie(c *gin.Context) {
	userID, ok := h.personUser(c)
	if !ok {
		return
	}
	var req personSelfieRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "selfie_media_id is required", nil, nil)
		return
	}
	media, err := uuid.Parse(req.SelfieMediaID)
	if err != nil || media == uuid.Nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "selfie_media_id must be a UUID", nil, nil)
		return
	}
	out, err := h.person.SubmitSelfie(c.Request.Context(), userID, media)
	h.writePerson(c, userID, http.StatusOK, out, err)
}

// personUser answers 503 when the feature is unwired and 401 without a user.
func (h *Handler) personUser(c *gin.Context) (uuid.UUID, bool) {
	if h.person == nil {
		e := personverify.ErrDisabled
		api.Error(c.Writer, e.Status, e.Code, e.Message, nil, nil)
		return uuid.Nil, false
	}
	userID, err := parseUserHeader(c)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return uuid.Nil, false
	}
	return userID, true
}

func (h *Handler) writePerson(c *gin.Context, userID uuid.UUID, status int, out any, err error) {
	var refusal *personverify.Error
	switch {
	case errors.As(err, &refusal):
		api.Error(c.Writer, refusal.Status, refusal.Code, refusal.Message, nil, nil)
	case err != nil:
		h.log.Error("person verification failed", "user_id", userID, "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
	default:
		api.JSON(c.Writer, status, out, nil)
	}
}
