package http

import (
	"net/http"

	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
)

// Module 3 M3-P0-3 / SR-6 — phone OTP is retired at launch.
//
// WHY THESE ROUTES WERE WORSE THAN MISSING
//
// `POST /v1/auth/request-otp` generated a code, stored it in Redis, and
// returned 200. It sent nothing. There was no SMS integration in this service
// — no AWS End User Messaging client, no gateway call, nothing. The whole
// delivery step did not exist:
//
//	func (s *Service) RequestOTP(ctx, phone, purpose) error {
//		otp, _ := s.generateOTP()
//		s.log.Debug("otp generated", ...)
//		return s.store.SaveOTP(ctx, phone, otp, purpose, s.cfg.OTPExpiry)
//	}
//
// So a user could request a code, be told it was on its way, and wait forever.
// Worse, an account created this way had no recoverable identifier at all: the
// only path back in was a code that could never be delivered.
//
// AWS End User Messaging SMS is the approved integration, but it needs a
// registered sender ID, a DLT template registration for India, and a
// deliverability budget — none of which exist yet. Rather than ship a route
// that pretends, the phone path answers 410 and points at the email flow that
// actually delivers.
//
// The 410 body names the replacement so a deployed client shows a real error
// with a real next step instead of a spinner that never resolves.

const emailAuthReplacement = "POST /v1/auth/register (email + password), then " +
	"POST /v1/auth/forgot-password for recovery"

// retiredSMSRoute answers 410 for every phone-OTP endpoint.
func retiredSMSRoute(c *gin.Context) {
	api.Error(c.Writer, http.StatusGone, "SMS_NOT_AVAILABLE",
		"Phone sign-in is not available. SMS delivery is not enabled, so this "+
			"endpoint stored a code and sent nothing — the code could never arrive. "+
			"Use email and password instead: "+emailAuthReplacement+".",
		map[string]any{
			"replacement":       emailAuthReplacement,
			"reason":            "no SMS delivery integration",
			"email_flow_active": true,
		}, nil)
}
