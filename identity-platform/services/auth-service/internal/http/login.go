// Sign-in routes: OTP request and verification, password login, and the
// shared step-up outcome writer used by every sign-in that can be held for
// a second channel. Pure move out of handler.go; behaviour is unchanged.

package http

import (
	"errors"
	"net/http"

	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
)

type RequestOTPRequest struct {
	Phone   string `json:"phone" binding:"required"`
	Purpose string `json:"purpose" binding:"required"`
}

func (h *Handler) RequestOTP(c *gin.Context) {
	var req RequestOTPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Warn("invalid request payload", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if err := validateOTPPurpose(req.Purpose); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if err := h.svc.RequestOTP(c.Request.Context(), req.Phone, req.Purpose); err != nil {
		h.log.Error("failed to request otp", "err", err, "phone", maskPhone(req.Phone), "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"message": "otp sent"}, nil)
}

type VerifyOTPRequest struct {
	Phone    string `json:"phone" binding:"required"`
	OTP      string `json:"otp" binding:"required"`
	Purpose  string `json:"purpose"`
	DeviceID string `json:"device_id"`
	Platform string `json:"platform"`
}

func (h *Handler) VerifyOTP(c *gin.Context) {
	var req VerifyOTPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Warn("invalid request payload", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if req.Purpose == "" {
		req.Purpose = "login"
	}
	if err := validateOTPPurpose(req.Purpose); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	resp, err := h.svc.VerifyOTP(c.Request.Context(), req.Phone, req.OTP, req.Purpose, req.DeviceID, req.Platform, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		// A13 — anomaly step-up envelope on a successful OTP that
		// landed in a novel device/network. Same shape as the Login
		// handler above.
		if errors.Is(err, service.ErrAnomalyStepUpRequired) && resp != nil {
			api.JSON(c.Writer, http.StatusOK, resp, nil)
			return
		}
		if errors.Is(err, service.ErrAnomalyStepUpUnavailable) {
			api.Error(c.Writer, http.StatusUnauthorized, "STEP_UP_UNAVAILABLE",
				"This sign-in looks unusual and your account has no recovery channel set up. Please contact support.", nil, nil)
			return
		}
		h.log.Warn("otp verification failed", "err", err, "phone", maskPhone(req.Phone), "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "AUTH_FAILED", "Authentication failed", nil, nil)
		return
	}

	// If 2FA or step-up is required, return pending envelope without cookies.
	if resp.Requires2FA || resp.RequiresStepUp {
		api.JSON(c.Writer, http.StatusOK, resp, nil)
		return
	}

	h.setAuthCookies(c, resp.Tokens)
	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

type LoginRequest struct {
	Identifier string `json:"identifier"`
	Email      string `json:"email"`
	Phone      string `json:"phone"`
	Password   string `json:"password" binding:"required"`
	DeviceID   string `json:"device_id"`
	Platform   string `json:"platform"`
}

func (h *Handler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Warn("invalid request payload", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	identifier := req.Identifier
	if identifier == "" {
		if req.Email != "" {
			identifier = req.Email
		} else if req.Phone != "" {
			identifier = req.Phone
		}
	}

	if identifier == "" {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "identifier, email, or phone is required", nil, nil)
		return
	}

	resp, err := h.svc.LoginWithPassword(c.Request.Context(), identifier, req.Password, req.DeviceID, req.Platform, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		// LB-5: a pending (unverified) account. Distinct from bad credentials
		// so the client can offer "resend verification" instead of showing
		// "wrong password" to someone whose password is correct.
		if errors.Is(err, service.ErrEmailNotVerified) {
			// CLB-3: the resumption path a user who closed the app needs.
			//
			// The password has already been checked, so this response is only
			// reachable by someone who owns the account. It carries a FRESH
			// verification transaction and no session at all — the client can
			// finish signing up with it, and it is useless for anything else.
			// Without this the pending state was a dead end: the credential
			// from registration was gone with the process, and both verify and
			// resend needed a session the account cannot have.
			details := map[string]any{
				"verify_via": "POST /v1/auth/verify-email",
				"resend_via": "POST /v1/auth/resend-verification",
			}
			if resp != nil && resp.VerificationToken != "" {
				details[VerificationTransactionField] = resp.VerificationToken
				if resp.VerificationExpiresAt != nil {
					details["verification_expires_at"] = *resp.VerificationExpiresAt
				}
			}
			api.Error(c.Writer, http.StatusForbidden, "EMAIL_NOT_VERIFIED", err.Error(), details, nil)
			return
		}
		if h.writeStepUpOutcome(c, resp, err) {
			return
		}
		// Account control: terminal / past-window states. Only reachable
		// after the password matched, so they reveal nothing to a guesser.
		if errors.Is(err, service.ErrAccountPurged) {
			api.Error(c.Writer, http.StatusForbidden, "ACCOUNT_PURGED", err.Error(), nil, nil)
			return
		}
		if errors.Is(err, service.ErrAccountPendingPurge) {
			api.Error(c.Writer, http.StatusForbidden, "ACCOUNT_PENDING_PURGE", err.Error(), nil, nil)
			return
		}
		h.log.Warn("login failed", "err", err, "identifier", maskIdentifier(identifier), "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "AUTH_FAILED", "Authentication failed", nil, nil)
		return
	}

	// If 2FA is required, return the pending response without setting auth cookies
	if resp.Requires2FA {
		api.JSON(c.Writer, http.StatusOK, resp, nil)
		return
	}

	h.setAuthCookies(c, resp.Tokens)
	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

// writeStepUpOutcome renders the A13 anomaly outcomes of a sign-in, password
// or provider alike, and reports whether it wrote.
//
// Step-up required: the service has already stashed the pending session and
// dispatched the email OTP where it can. The envelope goes out as a 200 so
// the UI can pivot without falling into the generic "auth failed" banner; the
// credential DID check out, so the request is halted for a second channel,
// closer to 2FA than to a rejection. No step-up channel at all: a 401 the
// client can explain.
func (h *Handler) writeStepUpOutcome(c *gin.Context, resp *service.AuthResponse, err error) bool {
	switch {
	case errors.Is(err, service.ErrAnomalyStepUpRequired) && resp != nil:
		api.JSON(c.Writer, http.StatusOK, resp, nil)
	case errors.Is(err, service.ErrAnomalyStepUpUnavailable):
		h.log.Warn("sign-in refused: no anomaly step-up channel", "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "STEP_UP_UNAVAILABLE",
			"This sign-in looks unusual and your account has no recovery channel set up. Please contact support.", nil, nil)
	default:
		return false
	}
	return true
}
