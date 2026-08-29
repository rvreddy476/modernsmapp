package push

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// CALL-LB-4 provider-boundary classification. The durable call consumer
// retries a failed delivery forever, so an UNDIFFERENTIATED provider error
// from one expired device token would stall its Kafka partition and stop
// later users' rings. Providers therefore distinguish:
//
//   - delivered            → nil
//   - permanent rejection  → wraps ErrDeviceRejected: THIS device can never
//     receive this (unregistered/invalid token, unknown platform). The
//     caller retires the device durably and continues.
//   - transient failure    → any other error: outage/throttle/auth — worth
//     retrying the SAME delivery, event stays uncommitted.
var ErrDeviceRejected = errors.New("device token permanently rejected")

// ErrProviderNotConfigured marks a delivery that was SKIPPED because no
// provider exists for the device's platform. Reporting nil here (the old
// behavior) let the call consumer commit a ring that was never pushed.
var ErrProviderNotConfigured = errors.New("push provider not configured")

// CALL-LB-4A: an HTTP status is NOT a token verdict. Both providers use the
// same status (notably 400) for invalid tokens AND for request/payload/
// configuration defects, so classifying by status alone can retire a fleet
// of healthy devices over one server-side payload bug — and commit rings
// that never delivered. Only the DOCUMENTED terminal token/device reasons in
// the response body may retire a device; everything else (payload, topic,
// headers, authentication, quota, provider outages, unparsable bodies)
// propagates as an operational error and keeps the event uncommitted.

// fcmErrorBody is the FCM v1 error envelope. Firebase distinguishes the two
// HTTP-400 INVALID_ARGUMENT cases only in `error.details`: a
// google.firebase.fcm.v1.FcmError names the FCM error code, and a
// google.rpc.BadRequest is present exactly when the REQUEST (not the token)
// was invalid.
type fcmErrorBody struct {
	Error struct {
		Status  string `json:"status"`
		Details []struct {
			Type      string `json:"@type"`
			ErrorCode string `json:"errorCode"`
		} `json:"details"`
	} `json:"error"`
}

// isFCMTokenRejection reports whether the FCM error RESPONSE BODY documents
// a terminal token/device rejection:
//   - FcmError UNREGISTERED — token expired / app uninstalled;
//   - FcmError SENDER_ID_MISMATCH — token belongs to a different sender;
//   - FcmError INVALID_ARGUMENT WITHOUT google.rpc.BadRequest details — per
//     Firebase docs this is the invalid-registration-token case; with
//     BadRequest details it is an invalid request value and must NOT be
//     blamed on the device.
//
// Unparsable or detail-less bodies are OPERATIONAL (false): when the
// provider does not say "this token is dead", we never guess.
func isFCMTokenRejection(body []byte) bool {
	var parsed fcmErrorBody
	if json.Unmarshal(body, &parsed) != nil {
		return false
	}
	fcmCode := ""
	badRequest := false
	for _, d := range parsed.Error.Details {
		switch {
		case strings.HasSuffix(d.Type, "fcm.v1.FcmError"):
			fcmCode = d.ErrorCode
		case strings.HasSuffix(d.Type, "google.rpc.BadRequest"):
			badRequest = true
		}
	}
	switch fcmCode {
	case "UNREGISTERED", "SENDER_ID_MISMATCH":
		return true
	case "INVALID_ARGUMENT":
		return !badRequest
	default:
		return false
	}
}

// apnsErrorBody is the APNs error response; `reason` is the verdict.
type apnsErrorBody struct {
	Reason string `json:"reason"`
}

// isAPNSTokenRejection: only the documented DEVICE-terminal reasons retire —
// BadDeviceToken and Unregistered. Everything else (BadTopic, MissingTopic,
// InvalidPushType, MissingPushType, BadCollapseId, PayloadTooLarge,
// TooManyRequests, ExpiredProviderToken, InternalServerError, …) is a
// request/configuration/provider problem and must propagate.
// DeviceTokenNotForTopic is deliberately NOT here: a misconfigured topic
// would produce it for EVERY device, and mass-retiring healthy tokens is the
// exact failure this classification exists to prevent.
func isAPNSTokenRejection(body []byte) bool {
	var parsed apnsErrorBody
	if json.Unmarshal(body, &parsed) != nil {
		return false
	}
	switch parsed.Reason {
	case "BadDeviceToken", "Unregistered":
		return true
	default:
		return false
	}
}

// ValidateCallPushConfig is the fail-closed startup guard (CALL-LB-4):
// enabling calling makes the FCM wake-up load-bearing — a deployment that
// requires call pushes but has no FCM must refuse to START rather than
// silently commit undelivered rings. Launch is Android-first, so FCM is the
// required provider; add APNs here when iOS devices go live.
//
// Operational contract: CALLS_ENABLED=true on call-service REQUIRES
// CALL_PUSH_REQUIRED=true (and therefore configured FCM) on this service.
func ValidateCallPushConfig(callPushRequired, fcmConfigured bool) error {
	if callPushRequired && !fcmConfigured {
		return fmt.Errorf("CALL_PUSH_REQUIRED is set but FCM is not configured " +
			"(FCM_PROJECT_ID / FCM_SERVICE_ACCOUNT_KEY): the incoming-call wake-up " +
			"cannot deliver — refusing to start")
	}
	return nil
}
