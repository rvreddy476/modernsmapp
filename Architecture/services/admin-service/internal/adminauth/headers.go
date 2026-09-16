// Package adminauth holds what admin-service needs to decide who may do what:
// the gateway header contract for admin MFA and step-up, the flag that turns
// the MFA requirement on, and the identity permission client with its cache.
package adminauth

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The gateway header contract. Identity puts admin_mfa, auth_time and
// step_up_at in the access token; the api-gateway verifies the token, strips
// any client-supplied copies of these headers, and stamps them from the claims.
// This is the only place the names and the window are written down.
const (
	// HeaderAdminMFA is "true" when the session's login passed TOTP.
	HeaderAdminMFA = "X-Admin-MFA"
	// HeaderAuthTime is the login time, Unix seconds.
	HeaderAuthTime = "X-Auth-Time"
	// HeaderStepUpAt is the time of the last fresh TOTP check, Unix seconds.
	HeaderStepUpAt = "X-Step-Up-At"

	// StepUpWindow is how long a step-up stays valid.
	StepUpWindow = 300 * time.Second
	// clockSkew tolerates a step-up stamped slightly ahead of this clock. A
	// timestamp further in the future than this is refused, not trusted.
	clockSkew = 30 * time.Second
)

// Error codes the gate answers with.
const (
	CodeMFARequired    = "MFA_REQUIRED"
	CodeStepUpRequired = "STEP_UP_REQUIRED"
)

// MFAVerified reports whether the admin-MFA header says the session passed TOTP.
func MFAVerified(v string) bool {
	return strings.EqualFold(strings.TrimSpace(v), "true")
}

// ParseUnix reads a Unix-seconds header value. ok is false for an empty or
// malformed value.
func ParseUnix(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, false
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}, false
	}
	return time.Unix(n, 0).UTC(), true
}

// StepUpValidUntil returns when the step-up in header value v stops being
// valid, and whether it is valid at now. A missing, malformed, stale or
// future-dated value is not valid.
func StepUpValidUntil(v string, now time.Time) (time.Time, bool) {
	at, ok := ParseUnix(v)
	if !ok {
		return time.Time{}, false
	}
	if at.After(now.Add(clockSkew)) {
		return time.Time{}, false
	}
	until := at.Add(StepUpWindow)
	if !now.Before(until) {
		return time.Time{}, false
	}
	return until, true
}

// RequireMFAFromEnv decides ADMIN_REQUIRE_MFA.
//
// An explicit true/false wins. Unset, it is true everywhere except when ENV
// names a local or dev environment: dev compose runs without it until the
// gateway stamps X-Admin-MFA. A value that is neither is a boot error, so a
// typo cannot silently switch MFA off.
func RequireMFAFromEnv(getenv func(string) string) (bool, error) {
	switch v := strings.ToLower(strings.TrimSpace(getenv("ADMIN_REQUIRE_MFA"))); v {
	case "true", "1", "yes":
		return true, nil
	case "false", "0", "no":
		return false, nil
	case "":
	default:
		return false, fmt.Errorf("ADMIN_REQUIRE_MFA=%q is not a boolean", v)
	}
	switch strings.ToLower(strings.TrimSpace(getenv("ENV"))) {
	case "local", "dev", "development":
		return false, nil
	}
	return true, nil
}
