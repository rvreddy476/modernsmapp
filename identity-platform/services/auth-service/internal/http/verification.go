// Email and phone verification routes: verify email (with the verification
// transaction field), verify phone, and resend. Pure move out of
// handler.go; behaviour is unchanged.

package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// --- Email/Phone Verification ---

// VerificationTransactionField is the request key carrying the server-issued
// verification credential. Named so tests can assert on the exact contract
// rather than a string literal that could drift.
const VerificationTransactionField = "verification_token"

type VerifyEmailRequest struct {
	// CLB-3: the account is named by this server-issued credential.
	//
	// There is deliberately NO user_id field. On a public route a
	// caller-supplied id would let anyone grind codes against any account they
	// can name, which is exactly the shape this endpoint must not have.
	VerificationToken string `json:"verification_token" binding:"required"`
	Code              string `json:"code" binding:"required"`
}

func (h *Handler) VerifyEmail(c *gin.Context) {
	var req VerifyEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if err := h.svc.VerifyEmailWithTransaction(c.Request.Context(), req.VerificationToken, req.Code); err != nil {
		// The per-account attempt budget is spent. Like the resend quota this
		// is only reachable with a valid token, so saying "wait" reveals
		// nothing, and it stops a real user retrying in a loop.
		if writeIfThrottled(c, err, "Too many verification attempts. Please wait before trying again.") {
			h.log.Warn("email verification throttled", "request_id", RequestIDFromContext(c))
			return
		}
		// One response for every failure — invalid credential, wrong purpose,
		// expired, replayed, wrong code. Distinguishing them would let a
		// caller probe which credentials and accounts exist.
		h.log.Warn("email verification failed", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "VERIFY_FAILED", "Email verification failed", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"message": "Email verified successfully"}, nil)
}

type VerifyPhoneRequest struct {
	Code string `json:"code" binding:"required"`
}

func (h *Handler) VerifyPhone(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid user ID", nil, nil)
		return
	}

	var req VerifyPhoneRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if err := h.svc.VerifyPhone(c.Request.Context(), userID, req.Code); err != nil {
		h.log.Warn("phone verification failed", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "VERIFY_FAILED", "Phone verification failed", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"message": "Phone verified successfully"}, nil)
}

type ResendVerificationRequest struct {
	Type string `json:"type" binding:"required"` // "email" or "phone"
	// CLB-3: as on VerifyEmailRequest, there is deliberately no user_id.
	VerificationToken string `json:"verification_token" binding:"required"`
}

func (h *Handler) ResendVerification(c *gin.Context) {
	var req ResendVerificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	// CLB-3: resend is the recovery path for a user whose first email never
	// arrived, so it must work with NO session — but it must still be scoped
	// to one account by a credential the server issued, not by a user id the
	// caller picked.
	switch req.Type {
	case "email":
		if err := h.svc.ResendVerificationWithTransaction(c.Request.Context(), req.VerificationToken); err != nil {
			// A quota denial is NOT folded into VERIFY_FAILED. The generic
			// error exists so an attacker cannot distinguish an invalid token
			// from a wrong code; a rate limit reveals nothing about the token
			// (you must already hold a valid one to be counted), and telling a
			// legitimate user "wait" rather than "that failed" is the
			// difference between waiting and retrying in a loop.
			if throttled, ok := service.AsThrottled(err); ok {
				retryAfter := int(throttled.RetryAfter.Seconds())
				if retryAfter < 1 {
					retryAfter = 1
				}
				c.Header("Retry-After", strconv.Itoa(retryAfter))
				api.Error(c.Writer, http.StatusTooManyRequests, "RATE_LIMITED",
					"Too many verification emails requested. Please wait before trying again.",
					map[string]any{"retry_after_seconds": retryAfter}, nil)
				return
			}
			h.log.Warn("resend email verification failed", "err", err, "request_id", RequestIDFromContext(c))
			api.Error(c.Writer, http.StatusBadRequest, "VERIFY_FAILED", "Could not resend verification", nil, nil)
			return
		}
	case "phone":
		// SR-6: no SMS delivery exists, so a phone code can never arrive.
		// Reporting success here would be a lie the user acts on.
		api.Error(c.Writer, http.StatusGone, "SMS_UNAVAILABLE",
			"Phone verification is unavailable; verify by email", nil, nil)
		return
	default:
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "type must be email or phone", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"message": "Verification code sent"}, nil)
}
