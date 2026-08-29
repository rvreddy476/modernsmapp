package push

import (
	"context"
	"errors"
	"testing"
)

// CALL-LB-4 provider-boundary classification: permanent token rejection must
// be distinguishable from a transient outage, a missing provider must not be
// silent success, and required call-push config must fail closed at startup.

func TestFCMBodyClassification(t *testing.T) {
	fcmErr := func(code, extraDetail string) []byte {
		details := `{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"` + code + `"}`
		if extraDetail != "" {
			details += "," + extraDetail
		}
		return []byte(`{"error":{"status":"INVALID_ARGUMENT","details":[` + details + `]}}`)
	}
	badRequestDetail := `{"@type":"type.googleapis.com/google.rpc.BadRequest","fieldViolations":[{"field":"message.data"}]}`

	for name, body := range map[string][]byte{
		"UNREGISTERED":                     fcmErr("UNREGISTERED", ""),
		"SENDER_ID_MISMATCH":               fcmErr("SENDER_ID_MISMATCH", ""),
		"INVALID_ARGUMENT without details": fcmErr("INVALID_ARGUMENT", ""),
	} {
		if !isFCMTokenRejection(body) {
			t.Fatalf("%s must be a token rejection (CALL-LB-4A)", name)
		}
	}
	for name, body := range map[string][]byte{
		"INVALID_ARGUMENT with BadRequest (invalid request, NOT the token)": fcmErr("INVALID_ARGUMENT", badRequestDetail),
		"QUOTA_EXCEEDED":     fcmErr("QUOTA_EXCEEDED", ""),
		"UNAVAILABLE":        fcmErr("UNAVAILABLE", ""),
		"THIRD_PARTY_AUTH":   fcmErr("THIRD_PARTY_AUTH_ERROR", ""),
		"detail-less status": []byte(`{"error":{"status":"UNAUTHENTICATED"}}`),
		"unparsable body":    []byte(`<html>bad gateway</html>`),
	} {
		if isFCMTokenRejection(body) {
			t.Fatalf("%s must NOT retire a device (CALL-LB-4A)", name)
		}
	}
}

func TestAPNSBodyClassification(t *testing.T) {
	for _, terminal := range []string{"BadDeviceToken", "Unregistered"} {
		if !isAPNSTokenRejection([]byte(`{"reason":"` + terminal + `"}`)) {
			t.Fatalf("apns %s must be a token rejection (CALL-LB-4A)", terminal)
		}
	}
	for _, operational := range []string{
		"BadTopic", "MissingTopic", "InvalidPushType", "MissingPushType",
		"BadCollapseId", "PayloadTooLarge", "TooManyRequests",
		"ExpiredProviderToken", "InvalidProviderToken", "InternalServerError",
		"ServiceUnavailable", "DeviceTokenNotForTopic",
	} {
		if isAPNSTokenRejection([]byte(`{"reason":"` + operational + `"}`)) {
			t.Fatalf("apns %s must NOT retire a device (CALL-LB-4A)", operational)
		}
	}
	if isAPNSTokenRejection([]byte(`not json`)) {
		t.Fatal("unparsable apns body must not retire a device")
	}
}

func TestDispatcherMissingProviderIsNotSilentSuccess(t *testing.T) {
	d := NewDispatcher(nil, nil)
	for _, platform := range []string{"android", "web", "ios"} {
		err := d.Send(context.Background(), "tok", platform, "t", "b", nil)
		if err == nil || !errors.Is(err, ErrProviderNotConfigured) {
			t.Fatalf("%s with no provider returned %v — a committed-but-unpushed ring (CALL-LB-4)", platform, err)
		}
	}
}

func TestDispatcherUnknownPlatformIsAPermanentDeviceRejection(t *testing.T) {
	d := NewDispatcher(nil, nil)
	err := d.Send(context.Background(), "tok", "symbian", "t", "b", nil)
	if err == nil || !errors.Is(err, ErrDeviceRejected) {
		t.Fatalf("unknown platform returned %v — that row can never deliver and must retire, not retry", err)
	}
}

func TestUnconfiguredPushersAreNotSilentSuccess(t *testing.T) {
	if err := NewFCMPusher("", "").Send(context.Background(), "tok", "android", "t", "b", nil); err == nil ||
		!errors.Is(err, ErrProviderNotConfigured) {
		t.Fatalf("credential-less FCM returned %v", err)
	}
}

func TestValidateCallPushConfigFailsClosed(t *testing.T) {
	if err := ValidateCallPushConfig(true, false); err == nil {
		t.Fatal("required call pushes with no FCM must refuse startup (CALL-LB-4)")
	}
	if err := ValidateCallPushConfig(true, true); err != nil {
		t.Fatalf("valid required config refused: %v", err)
	}
	if err := ValidateCallPushConfig(false, false); err != nil {
		t.Fatalf("dev posture refused: %v", err)
	}
}
