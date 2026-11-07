package middleware

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// Credential rate limits: sign-in, sign-up and second-factor routes.
//
// These all used to share one bucket of 10 attempts per IP per 15 minutes
// (LoginRateLimit). Indian carrier NAT puts many subscribers behind one
// address, so one busy exit IP locked everyone behind it out of login,
// register, 2FA and OAuth sign-up at once. Each action now has its own
// bucket, the per-IP cap is sized for a shared address, and brute force
// against one account is held by a per-account cap at the old strict limit,
// keyed on an HMAC of the normalised identifier (identifier_keys.go), never
// the raw value.

// CredentialLimits caps one credential action within Window. A zero field
// takes its default (see Default*), so a zero CredentialLimits is the default.
type CredentialLimits struct {
	PerIP      int64
	PerAccount int64
	Window     time.Duration
}

// Defaults, overridable with AUTH_RATE_LIMIT_PER_IP,
// AUTH_RATE_LIMIT_PER_ACCOUNT and AUTH_RATE_LIMIT_WINDOW.
const (
	DefaultCredentialPerIP      = 100
	DefaultCredentialPerAccount = 5
	DefaultCredentialWindow     = 15 * time.Minute
)

// StrictCredentialLimits are the old shared-bucket numbers (10 per IP, 5 per
// account, per 15 min), kept for routes whose users are not a carrier-NAT
// crowd: the admin console, in-session step-up, passkey sign-in and the
// retired SMS route. They still get a bucket of their own.
var StrictCredentialLimits = CredentialLimits{PerIP: 10, PerAccount: 5, Window: 15 * time.Minute}

func (l CredentialLimits) withDefaults() CredentialLimits {
	if l.PerIP <= 0 {
		l.PerIP = DefaultCredentialPerIP
	}
	if l.PerAccount <= 0 {
		l.PerAccount = DefaultCredentialPerAccount
	}
	if l.Window <= 0 {
		l.Window = DefaultCredentialWindow
	}
	return l
}

// CredentialAction names one bucket. Account extracts the account a request
// body names, already normalised; nil (or an empty result) means the action
// is capped per IP only.
type CredentialAction struct {
	Name    string
	Account func(body []byte) string
}

// The credential actions. Each is its own bucket.
var (
	ActionLogin             = CredentialAction{Name: "login", Account: bodyIdentifier}
	ActionRegister          = CredentialAction{Name: "register", Account: bodyIdentifier}
	ActionTwoFactor         = CredentialAction{Name: "2fa", Account: bodyUserID}
	ActionAnomalyTwoFactor  = CredentialAction{Name: "anomaly_2fa"}
	ActionOAuthToken        = CredentialAction{Name: "oauth_token"}
	ActionOAuthVerifySignup = CredentialAction{Name: "oauth_verify_signup", Account: bodyIdentifier}
	ActionPartnerPhoneSend  = CredentialAction{Name: "partner_phone_request", Account: bodyIdentifier}
	ActionPartnerPhoneCheck = CredentialAction{Name: "partner_phone_verify", Account: bodyIdentifier}
	ActionStepUp            = CredentialAction{Name: "step_up", Account: bodyIdentifier}
	ActionAdminLogin        = CredentialAction{Name: "admin_login", Account: bodyIdentifier}
	ActionAdminTwoFactor    = CredentialAction{Name: "admin_2fa", Account: bodyIdentifier}
	ActionAdminStepUp       = CredentialAction{Name: "admin_step_up", Account: bodyIdentifier}
	ActionPasskeyLogin      = CredentialAction{Name: "passkey_login", Account: bodyIdentifier}
	ActionRetiredOTP        = CredentialAction{Name: "verify_otp", Account: bodyIdentifier}
)

// CredentialRateLimit caps action at lim.PerIP per client IP and, when the
// body names an account, lim.PerAccount per account, both per lim.Window
// (fixed windows, failing closed on Redis errors; see allow). A nil rdb
// disables limiting (tests).
func CredentialRateLimit(rdb *redis.Client, keys IdentifierKeys, action CredentialAction, lim CredentialLimits) gin.HandlerFunc {
	lim = lim.withDefaults()
	retryAfter := strconv.Itoa(int(lim.Window / time.Second))
	return func(c *gin.Context) {
		if rdb == nil {
			c.Next()
			return
		}
		body, _ := io.ReadAll(c.Request.Body)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(body))

		ctx := c.Request.Context()
		ok := allow(ctx, rdb, credentialKey(action.Name, "ip", c.ClientIP()), lim.PerIP, lim.Window)
		if ok && action.Account != nil {
			if account := action.Account(body); account != "" {
				ok = allow(ctx, rdb, credentialKey(action.Name, "acct", keys.Digest(account)), lim.PerAccount, lim.Window)
			}
		}
		if !ok {
			c.Header("Retry-After", retryAfter)
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": gin.H{"code": "RATE_LIMITED", "message": "Too many attempts. Try again later."},
			})
			return
		}
		c.Next()
	}
}

func credentialKey(action, kind, value string) string {
	return fmt.Sprintf("auth_rl:%s:%s:%s", action, kind, value)
}

// bodyUserID is the account a 2FA verify names: its `user_id`, normalised.
func bodyUserID(body []byte) string {
	var req struct {
		UserID string `json:"user_id"`
	}
	_ = json.Unmarshal(body, &req)
	return strings.ToLower(strings.TrimSpace(req.UserID))
}
