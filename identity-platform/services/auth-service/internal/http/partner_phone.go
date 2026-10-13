package http

import (
	"context"
	"errors"
	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
	"net/http"
	"strconv"
	"time"
)

type partnerPhoneService interface {
	PartnerPhoneAvailable() bool
	RequestPartnerPhone(context.Context, service.PartnerPhoneInput, string) (*service.PartnerPhoneChallenge, error)
	VerifyPartnerPhone(context.Context, string, string, string, string, string, string) (*service.AuthResponse, error)
}

func (h *Handler) partnerPhoneRequest(c *gin.Context) {
	s, ok := h.svc.(partnerPhoneService)
	if !ok || !s.PartnerPhoneAvailable() {
		api.Error(c.Writer, 503, "SMS_NOT_AVAILABLE", "Mobile verification is not available yet.", nil, nil)
		return
	}
	var in service.PartnerPhoneInput
	if c.ShouldBindJSON(&in) != nil {
		api.Error(c.Writer, 400, "INVALID_REQUEST", "Check your registration details.", nil, nil)
		return
	}
	out, err := s.RequestPartnerPhone(c.Request.Context(), in, c.ClientIP())
	if t, ok := service.AsThrottled(err); ok {
		c.Header("Retry-After", strconv.Itoa(int(t.RetryAfter/time.Second)))
		api.Error(c.Writer, 429, "RATE_LIMITED", t.Error(), nil, nil)
		return
	}
	if err != nil {
		if errors.Is(err, service.ErrPartnerPhoneInput) {
			api.Error(c.Writer, 400, "INVALID_REQUEST", err.Error(), nil, nil)
		} else {
			api.Error(c.Writer, 503, "OTP_UNAVAILABLE", "We could not create a code. Please try again shortly.", nil, nil)
		}
		return
	}
	c.Header("Cache-Control", "no-store")
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

func (h *Handler) partnerPhoneVerify(c *gin.Context) {
	s, ok := h.svc.(partnerPhoneService)
	if !ok || !s.PartnerPhoneAvailable() {
		api.Error(c.Writer, 503, "SMS_NOT_AVAILABLE", "Mobile verification is not available yet.", nil, nil)
		return
	}
	var in struct {
		ChallengeID string `json:"challenge_id"`
		Code        string `json:"code"`
		DeviceID    string `json:"device_id"`
		Platform    string `json:"platform"`
	}
	if c.ShouldBindJSON(&in) != nil {
		api.Error(c.Writer, 400, "INVALID_REQUEST", "Enter the six-digit code.", nil, nil)
		return
	}
	out, err := s.VerifyPartnerPhone(c.Request.Context(), in.ChallengeID, in.Code, in.DeviceID, in.Platform, c.ClientIP(), c.Request.UserAgent())
	if err != nil && !(errors.Is(err, service.ErrAnomalyStepUpRequired) && out != nil) {
		message := "Unable to sign in. Check the code and account details, or register if you do not have an account."
		if errors.Is(err, service.ErrPartnerPhoneCode) {
			message = err.Error()
		}
		api.Error(c.Writer, 401, "AUTH_FAILED", message, nil, nil)
		return
	}
	if !out.Requires2FA && !out.RequiresStepUp {
		h.setAuthCookies(c, out.Tokens)
	}
	c.Header("Cache-Control", "no-store")
	api.JSON(c.Writer, http.StatusOK, out, nil)
}
